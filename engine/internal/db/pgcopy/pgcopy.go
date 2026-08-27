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

func copyWith(ctx context.Context, source, target secrets.Value, extra ...string) error {
	dumpPath, err := exec.LookPath("pg_dump")
	if err != nil {
		return fmt.Errorf(
			"pgcopy: pg_dump is not on the path, and it is what copies a source database. " +
				"Install the Postgres client tools, or configure a seed command instead of a source")
	}
	restorePath, err := exec.LookPath("pg_restore")
	if err != nil {
		return fmt.Errorf("pgcopy: pg_restore is not on the path: %w", err)
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
	dumpPath, err := exec.LookPath("pg_dump")
	if err != nil {
		return fmt.Errorf(
			"pgcopy: pg_dump is not on the path, and it is what publishes a golden. " +
				"Install the Postgres client tools, or leave database.golden.storage_url unset")
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
	restorePath, err := exec.LookPath("pg_restore")
	if err != nil {
		return fmt.Errorf("pgcopy: pg_restore is not on the path: %w", err)
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
