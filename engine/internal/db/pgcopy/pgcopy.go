// Package pgcopy holds the Postgres operations every database provider needs.
//
// It exists because there was nearly a second copy of them. A provider has to
// wait for a database to answer, run a seed script, and copy a source database
// into a candidate, and the last of those is the most consequential operation
// in the product: the wire format, the ordering of dependent objects, extension
// handling, and large objects are all things a reimplementation gets subtly
// wrong. One implementation, used by every provider, is the only version of
// this worth having.
package pgcopy

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// Sleeper is the clock's sleep, injected so that waiting is not real time in a
// test.
type Sleeper func(ctx context.Context, d time.Duration) error

// Ping runs one query.
//
// A query rather than a ping, because Postgres accepts a connection before it
// will answer during recovery, and a database that connects but cannot read is
// not ready for what a caller is about to do with it.
func Ping(ctx context.Context, conn secrets.Value) error {
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return fmt.Errorf("pgcopy: open a connection: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	var one int
	return db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// WaitReady polls until the database answers a query, or the deadline passes.
// It returns the last error seen, so the caller can wrap it in whichever coded
// error suits its own transport.
func WaitReady(ctx context.Context, conn secrets.Value, timeout time.Duration, now func() time.Time, sleep Sleeper) error {
	deadline := now().Add(timeout)
	var last error
	for now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := Ping(attempt, conn)
		cancel()
		if err == nil {
			return nil
		}
		last = err
		if err := sleep(ctx, 250*time.Millisecond); err != nil {
			return err
		}
	}
	if last == nil {
		last = fmt.Errorf("pgcopy: the database did not answer within %s", timeout)
	}
	return last
}

// Exec runs a script. An empty script is not an error, because a provider with
// no seed configured is a normal configuration and not a broken one.
func Exec(ctx context.Context, conn secrets.Value, script string) error {
	if strings.TrimSpace(script) == "" {
		return nil
	}
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return fmt.Errorf("pgcopy: open a connection: %w", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, script); err != nil {
		return fmt.Errorf("pgcopy: run the script: %w", err)
	}
	return nil
}

// Copy copies a source database into a target.
//
// It shells out to pg_dump and pg_restore rather than reimplementing them, for
// the reason in the package comment.
//
// The subprocess environment is constructed explicitly rather than inherited,
// so that the workstation's own credentials cannot reach the child, and the
// connection strings go through flags the child reads rather than being echoed,
// with PGCONNECT_TIMEOUT set so that an unreachable source fails rather than
// hangs.
func Copy(ctx context.Context, source, target secrets.Value) error {
	return copyWith(ctx, source, target)
}

// CopySchema copies a source database's structure and none of its rows.
//
// It is what subsetting needs: the tables, the keys, the sequences, the
// indexes and the constraints have to be there before a slice of the rows can
// be loaded into them, and copying the rows as well would be copying the thing
// the subset exists to avoid copying.
//
// It is here rather than in the subsetting package for the reason in the
// package comment. The flags below are the same ones Copy uses and they were
// each chosen for a reason; a second invocation elsewhere would be a second
// place for one of them to be forgotten.
func CopySchema(ctx context.Context, source, target secrets.Value) error {
	return copyWith(ctx, source, target, "--schema-only")
}

// pg_dump refuses outright to dump a server newer than itself: "aborting
// because of server version mismatch". That is not a nicety, it is a hard
// refusal, and it is the single most likely way the golden refresh fails on a
// machine that is otherwise set up correctly. Postgres 17 is three years old
// and Debian, Ubuntu and the GitHub runners still ship a 16 client by default,
// so "install the Postgres client tools" is advice that produces exactly this.
//
// Two things follow, and both are here rather than left to the operator.
// Distributions install every major version side by side under a predictable
// path and put only ONE of them on PATH, so the tool that can do the job is
// very often already on the machine and merely not first. And when it truly is
// not there, the error has to name the version needed and the package that
// carries it, because the message Postgres gives names neither.

// serverMajor asks a database what version it is, so the right client can be
// chosen before anything is spawned. A failure here is not fatal: it falls back
// to whatever is on PATH, which is the behaviour this had before.
func serverMajor(ctx context.Context, conn secrets.Value) int {
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return 0
	}
	defer func() { _ = db.Close() }()

	var num int
	if err := db.QueryRowContext(ctx, "SHOW server_version_num").Scan(&num); err != nil {
		return 0
	}
	// 170011 is 17.11. Versions before 10 encoded the minor in the middle
	// digits, and none of those are supported here, so the division is safe.
	return num / 10000
}

// searchDirs are where distributions put the versions that are not on PATH.
// Debian and Ubuntu use the first, Homebrew the other two.
var searchDirs = []string{
	"/usr/lib/postgresql/*/bin",
	"/opt/homebrew/opt/postgresql@*/bin",
	"/usr/local/opt/postgresql@*/bin",
}

// toolFor finds a pg_dump or pg_restore new enough for the server.
//
// It returns the newest one it can find, and whether that one is new enough,
// so the caller can produce a message about the actual gap rather than a
// generic one.
func toolFor(name string, wantMajor int) (path string, haveMajor int, err error) {
	type candidate struct {
		path  string
		major int
	}
	var found []candidate

	if p, lookErr := exec.LookPath(name); lookErr == nil {
		found = append(found, candidate{p, toolMajor(p)})
	}
	for _, pattern := range searchDirs {
		matches, _ := filepath.Glob(filepath.Join(pattern, name))
		for _, m := range matches {
			found = append(found, candidate{m, toolMajor(m)})
		}
	}
	if len(found) == 0 {
		return "", 0, fmt.Errorf(
			"pgcopy: %s is not on the path and is not installed anywhere this looked. "+
				"It is what copies a source database. Install the Postgres client tools, "+
				"or configure a seed command instead of a source", name)
	}

	best := found[0]
	for _, c := range found[1:] {
		if c.major > best.major {
			best = c
		}
	}
	// Prefer the oldest that is still new enough, so a machine with several
	// installed uses the one that matches rather than the newest for its own
	// sake.
	chosen := best
	for _, c := range found {
		if c.major >= wantMajor && (chosen.major < wantMajor || c.major < chosen.major) {
			chosen = c
		}
	}
	return chosen.path, chosen.major, nil
}

// toolMajor asks a binary its version. Zero when it will not say, which sorts
// it below everything that will.
func toolMajor(path string) int {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return 0
	}
	// "pg_dump (PostgreSQL) 17.2" and "pg_restore (PostgreSQL) 16.15 (Ubuntu ...)".
	fields := strings.Fields(string(out))
	for i := len(fields) - 1; i >= 0; i-- {
		major, _, _ := strings.Cut(fields[i], ".")
		if n, convErr := strconv.Atoi(major); convErr == nil && n >= 8 {
			return n
		}
	}
	return 0
}

// tooOld renders the refusal Postgres gives with the parts it leaves out: what
// is needed, what is here, and the package that closes the gap.
func tooOld(name string, have, want int) error {
	return fmt.Errorf(
		"pgcopy: the source database is Postgres %d and the newest %s on this machine is %d. "+
			"pg_dump refuses to read a server newer than itself, so this cannot be worked around. "+
			"Install the matching client tools: on Debian or Ubuntu, "+
			"apt-get install postgresql-client-%d; on macOS, brew install libpq or postgresql@%d",
		want, name, have, want, want)
}

func copyWith(ctx context.Context, source, target secrets.Value, extra ...string) error {
	want := serverMajor(ctx, source)
	dumpPath, dumpMajor, err := toolFor("pg_dump", want)
	if err != nil {
		return err
	}
	if want > 0 && dumpMajor < want {
		return tooOld("pg_dump", dumpMajor, want)
	}
	restorePath, _, err := toolFor("pg_restore", want)
	if err != nil {
		return err
	}

	args := []string{
		"--format=custom",
		"--no-owner", "--no-privileges", "--no-acl",
		// Row level security policies are preserved rather than dropped,
		// because a Supabase schema depends on them and restoring without them
		// produces a database where every query returns nothing.
		"--quote-all-identifiers",
	}
	args = append(args, extra...)
	args = append(args, "--dbname="+source.Reveal())

	dump := exec.CommandContext(ctx, dumpPath, args...)
	dump.Env = []string{"PGCONNECT_TIMEOUT=30"}

	restore := exec.CommandContext(ctx, restorePath,
		"--no-owner", "--no-privileges",
		// A restore that stops at the first error leaves a half loaded
		// database that looks complete. Exiting on error is what makes a
		// failed refresh fail rather than publish something partial.
		"--exit-on-error",
		"--dbname="+target.Reveal(),
	)
	restore.Env = []string{"PGCONNECT_TIMEOUT=30"}

	pipe, err := dump.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pgcopy: pipe the dump: %w", err)
	}
	restore.Stdin = pipe

	var dumpErr, restoreErr strings.Builder
	dump.Stderr = &dumpErr
	restore.Stderr = &restoreErr

	if err := dump.Start(); err != nil {
		return fmt.Errorf("pgcopy: start pg_dump: %w", err)
	}
	if err := restore.Start(); err != nil {
		_ = dump.Process.Kill()
		return fmt.Errorf("pgcopy: start pg_restore: %w", err)
	}
	if err := dump.Wait(); err != nil {
		_ = restore.Process.Kill()
		return fmt.Errorf("pgcopy: pg_dump failed: %s", Tail(dumpErr.String()))
	}
	if err := restore.Wait(); err != nil {
		return fmt.Errorf("pgcopy: pg_restore failed: %s", Tail(restoreErr.String()))
	}
	return nil
}

// DumpTo writes a database's dump to a writer.
//
// The custom format, the same one Copy pipes, because it is the one pg_restore
// can reorder: a plain SQL dump has to be replayed in the order it was written,
// and the order that loads cleanly is not always the order that dumped.
//
// It exists so that a golden can be published somewhere other than the machine
// that made it. The dump goes to an object store beside its attestation, and
// RestoreFrom is the other half.
func DumpTo(ctx context.Context, source secrets.Value, w io.Writer) error {
	want := serverMajor(ctx, source)
	dumpPath, dumpMajor, err := toolFor("pg_dump", want)
	if err != nil {
		return err
	}
	if want > 0 && dumpMajor < want {
		return tooOld("pg_dump", dumpMajor, want)
	}
	cmd := exec.CommandContext(ctx, dumpPath,
		"--format=custom",
		"--no-owner", "--no-privileges", "--no-acl",
		"--quote-all-identifiers",
		"--dbname="+source.Reveal(),
	)
	cmd.Env = []string{"PGCONNECT_TIMEOUT=30"}
	cmd.Stdout = w
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pgcopy: pg_dump failed: %s", Tail(stderr.String()))
	}
	return nil
}

// RestoreFrom loads a dump from a reader into a database.
//
// --exit-on-error for the reason Copy has it: a restore that stops at the
// first error leaves a half loaded database that looks complete, and a golden
// pulled from a store and silently half restored is the worst kind, because
// everything downstream treats it as verified.
func RestoreFrom(ctx context.Context, target secrets.Value, r io.Reader) error {
	restorePath, _, err := toolFor("pg_restore", serverMajor(ctx, target))
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, restorePath,
		"--no-owner", "--no-privileges",
		"--exit-on-error",
		"--dbname="+target.Reveal(),
	)
	cmd.Env = []string{"PGCONNECT_TIMEOUT=30"}
	cmd.Stdin = r
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pgcopy: pg_restore failed: %s", Tail(stderr.String()))
	}
	return nil
}

// Tail returns the last few lines of output, which is where the actual error
// is. Printing the whole stream buries it under progress notices.
func Tail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return strings.Join(lines, "; ")
}
