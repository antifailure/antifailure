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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// CopyOptions narrows what a copy moves, for a target that is not a blank
// database.
//
// The zero value is the whole database into an empty one, which is what a
// container or a fresh branch of a project's own storage is. A managed platform
// is the case this exists for: it owns schemas and cluster wide objects in the
// source AND in the target, so copying everything fails on the first object
// that is already there.
type CopyOptions struct {
	// ExcludeSchemas are left out of the dump entirely, for schemas the target
	// already has and the platform owns.
	ExcludeSchemas []string
	// ExcludeArchiveKinds drops entries from the archive by their table of
	// contents description, such as "EVENT TRIGGER". It exists because some
	// objects have no pg_dump flag and cannot be restored into a target that
	// already has them: recreating one fails on ownership rather than on
	// existence, which no restore flag suppresses.
	//
	// Naming any kind changes how the copy runs. The archive has to exist as a
	// file before its contents can be listed, so a filtered copy is dump then
	// restore rather than dump piped into restore.
	ExcludeArchiveKinds []string
	// SchemaOnly copies the structure and none of the rows.
	//
	// It is what subsetting needs: the tables, the keys, the sequences, the
	// indexes and the constraints have to be there before a slice of the rows
	// can be loaded into them, and copying the rows as well would be copying
	// the thing the subset exists to avoid copying.
	SchemaOnly bool
}

// Copy copies a source database into a target.
//
// It shells out to pg_dump and pg_restore rather than reimplementing them, for
// the reason in the package comment.
func Copy(ctx context.Context, source, target secrets.Value) error {
	return CopyWith(ctx, source, target, CopyOptions{})
}

// CopySchema copies a source database's structure and none of its rows.
//
// It is here rather than in the subsetting package for the reason in the
// package comment. The flags are the ones Copy uses and they were each chosen
// for a reason; a second invocation elsewhere would be a second place for one
// of them to be forgotten.
func CopySchema(ctx context.Context, source, target secrets.Value) error {
	return CopyWith(ctx, source, target, CopyOptions{SchemaOnly: true})
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
// toolCandidate is one install of pg_dump or pg_restore this machine has.
type toolCandidate struct {
	path  string
	major int
}

// toolsFound is every install of name this machine has, in no order.
//
// Split out of toolFor so that a caller which wants the CEILING rather than a
// match can have it. toolFor answers "the oldest that is still new enough",
// which is the right question when a server version is known and the wrong one
// when nothing has connected yet: with no bar to clear, the oldest install is
// exactly the wrong answer.
func toolsFound(name string) ([]toolCandidate, error) {
	var found []toolCandidate
	if p, lookErr := exec.LookPath(name); lookErr == nil {
		found = append(found, toolCandidate{p, toolMajor(p)})
	}
	for _, pattern := range searchDirs {
		matches, _ := filepath.Glob(filepath.Join(pattern, name))
		for _, m := range matches {
			found = append(found, toolCandidate{m, toolMajor(m)})
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf(
			"pgcopy: %s is not on the path and is not installed anywhere this looked. "+
				"It is what copies a source database. Install the Postgres client tools, "+
				"or configure a seed command instead of a source", name)
	}
	return found, nil
}

// newest is the highest major among the candidates, which is the highest
// server version they can read.
func newest(found []toolCandidate) toolCandidate {
	best := found[0]
	for _, c := range found[1:] {
		if c.major > best.major {
			best = c
		}
	}
	return best
}

func toolFor(name string, wantMajor int) (path string, haveMajor int, err error) {
	found, err := toolsFound(name)
	if err != nil {
		return "", 0, err
	}
	chosen := bestFor(found, wantMajor)
	return chosen.path, chosen.major, nil
}

// bestFor picks the oldest candidate that is still new enough for the server,
// so a machine with several installed uses the one that matches rather than
// the newest for its own sake. With nothing new enough it returns the newest,
// which is what makes the refusal name the actual gap.
//
// Pure, and separate from the search, because it and [newest] answer different
// questions and the wrong one of the two reads as correct. bestFor with no bar
// to clear returns the OLDEST install on the machine, which as an answer to
// "what could this machine read" is exactly backwards.
func bestFor(found []toolCandidate, wantMajor int) toolCandidate {
	chosen := newest(found)
	for _, c := range found {
		if c.major >= wantMajor && (chosen.major < wantMajor || c.major < chosen.major) {
			chosen = c
		}
	}
	return chosen
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

// CopyWith copies a source database into a target, narrowed by opts.
//
// The subprocess environment is constructed explicitly rather than inherited,
// so that the workstation's own credentials cannot reach the child, and the
// connection strings go through flags the child reads rather than being echoed,
// with PGCONNECT_TIMEOUT set so that an unreachable source fails rather than
// hangs.
func CopyWith(ctx context.Context, source, target secrets.Value, opts CopyOptions) error {
	// The roles the source's policies name have to exist in the target before
	// the restore runs, or every CREATE POLICY in the dump fails.
	//
	// This is the other half of keeping row level security. The dump preserves
	// policies on purpose, because a schema that depends on them restores into
	// a database where every query returns nothing without them. But a policy
	// says `TO some_role`, pg_dump does not carry roles (they are cluster-wide
	// objects, not database ones), and `--no-owner --no-privileges` correctly
	// drops ownership and grants without touching the role a policy names. So
	// the restore reached `CREATE POLICY ... TO "antifailure_app"` and stopped
	// with `role "antifailure_app" does not exist`, and a database using the
	// pattern this product recommends could not be copied at all.
	//
	// It sits here rather than in either path because both restore, and a
	// requirement that holds for one holds for the other.
	if err := ensureRoles(ctx, source, target); err != nil {
		return err
	}

	var err error
	if len(opts.ExcludeArchiveKinds) > 0 {
		err = copyThroughArchive(ctx, source, target, opts)
	} else {
		err = copyThroughPipe(ctx, source, target, opts)
	}
	if err != nil {
		return err
	}

	// A restore loads rows and leaves the planner with no statistics at all.
	// Anything that reads the copy afterwards, including the masking run's own
	// chunked updates, plans against a database that looks empty.
	//
	// A schema-only copy has no rows to describe, so there is nothing for a
	// pass over it to learn.
	if opts.SchemaOnly {
		return nil
	}
	return Analyze(ctx, target)
}

func copyThroughPipe(ctx context.Context, source, target secrets.Value, opts CopyOptions) error {
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

	dump := exec.CommandContext(ctx, dumpPath, append(dumpArgs(opts), "--dbname="+source.Reveal())...)
	dump.Env = []string{"PGCONNECT_TIMEOUT=30"}

	restore := exec.CommandContext(ctx, restorePath,
		append(restoreArgs(), "--dbname="+target.Reveal())...)
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

// dumpArgs builds pg_dump's flags, which are the same however the copy is
// wired up.
func dumpArgs(opts CopyOptions) []string {
	args := []string{
		"--format=custom",
		"--no-owner", "--no-privileges", "--no-acl",
		// Row level security policies are preserved rather than dropped,
		// because a Supabase schema depends on them and restoring without them
		// produces a database where every query returns nothing.
		"--quote-all-identifiers",
	}
	if opts.SchemaOnly {
		args = append(args, "--schema-only")
	}
	if len(opts.ExcludeSchemas) > 0 {
		// Publications and subscriptions are cluster wide rather than owned by
		// a schema, so excluding a platform's schemas does not exclude the
		// publication it created, and a restore fails on one the target already
		// has. This follows ExcludeSchemas rather than being its own option
		// because the two are the same case: a target the platform has already
		// furnished. A database with no platform behind it has neither, so
		// nothing is lost by the pairing.
		args = append(args, "--no-publications", "--no-subscriptions")
	}
	for _, schema := range opts.ExcludeSchemas {
		args = append(args, "--exclude-schema="+schema)
	}
	return args
}

// restoreArgs builds pg_restore's flags.
func restoreArgs() []string {
	return []string{
		"--no-owner", "--no-privileges",
		// A restore that stops at the first error leaves a half loaded
		// database that looks complete. Exiting on error is what makes a
		// failed refresh fail rather than publish something partial. It is not
		// a nicety: a foreign key that could not be validated is reported and
		// skipped by default, so without this a copy can land the rows and
		// silently omit the constraint.
		"--exit-on-error",
	}
}

// copyThroughArchive dumps to a file, filters the archive's table of contents,
// and restores what is left.
//
// Slower than the pipe by one write of the dump to local disk, and the only way
// to leave out an object kind pg_dump has no flag for.
func copyThroughArchive(ctx context.Context, source, target secrets.Value, opts CopyOptions) error {
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

	dir, err := os.MkdirTemp("", "af-pgcopy-")
	if err != nil {
		return fmt.Errorf("pgcopy: make a working directory: %w", err)
	}
	// The archive holds the source's data, so it is removed whatever happens,
	// including when the restore fails and the caller is about to report why.
	defer func() { _ = os.RemoveAll(dir) }()
	archive := filepath.Join(dir, "source.dump")

	dump := exec.CommandContext(ctx, dumpPath,
		append(dumpArgs(opts), "--file="+archive, "--dbname="+source.Reveal())...)
	dump.Env = []string{"PGCONNECT_TIMEOUT=30"}
	var dumpErr strings.Builder
	dump.Stderr = &dumpErr
	if err := dump.Run(); err != nil {
		return fmt.Errorf("pgcopy: pg_dump failed: %s", Tail(dumpErr.String()))
	}

	list := exec.CommandContext(ctx, restorePath, "--list", archive)
	var listing, listErr strings.Builder
	list.Stdout = &listing
	list.Stderr = &listErr
	if err := list.Run(); err != nil {
		return fmt.Errorf("pgcopy: read the archive's table of contents: %s", Tail(listErr.String()))
	}

	toc := filepath.Join(dir, "restore.toc")
	if err := os.WriteFile(toc, []byte(filterTOC(listing.String(), opts.ExcludeArchiveKinds)), 0o600); err != nil {
		return fmt.Errorf("pgcopy: write the filtered table of contents: %w", err)
	}

	restore := exec.CommandContext(ctx, restorePath,
		append(restoreArgs(), "--use-list="+toc, "--dbname="+target.Reveal(), archive)...)
	restore.Env = []string{"PGCONNECT_TIMEOUT=30"}
	var restoreErr strings.Builder
	restore.Stderr = &restoreErr
	if err := restore.Run(); err != nil {
		return fmt.Errorf("pgcopy: pg_restore failed: %s", Tail(restoreErr.String()))
	}
	return nil
}

// tocEntry matches the identifying prefix of a table of contents line, which is
// a dump identifier, a catalog pair, and then the description.
//
// Parsed rather than searched, because a description is matched by position:
// looking for "EVENT TRIGGER" anywhere in the line would also match a table
// whose owner or name contained it, and dropping a table from a restore because
// of its name would be a data loss bug with no error message.
var tocEntry = regexp.MustCompile(`^[0-9]+; [0-9]+ [0-9]+ (.+)$`)

// filterTOC removes entries whose description begins with one of the kinds.
func filterTOC(listing string, kinds []string) string {
	var out strings.Builder
	for _, line := range strings.Split(listing, "\n") {
		if dropped(line, kinds) {
			continue
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}

func dropped(line string, kinds []string) bool {
	m := tocEntry.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return false
	}
	for _, kind := range kinds {
		if strings.HasPrefix(m[1], kind+" ") {
			return true
		}
	}
	return false
}

// CopyTableData copies the ROWS of named tables into a target that already has
// them.
//
// It exists for tables a platform owns and a copy therefore cannot create, but
// whose contents an application's own foreign keys point at. The tables are
// named schema qualified. One that does not exist in the source is skipped: a
// source that is a plain Postgres database rather than a managed one
// legitimately has none of them.
//
// The skipping is done here and not left to pg_dump, because pg_dump does not
// do it. `--table=auth.users` against a database with no auth schema exits 1
// with "no matching tables were found" and writes a zero byte file, so the
// restore then fails with "input file is too short" and the real reason is two
// errors back. Asking the source what it has is one round trip and turns that
// into nothing happening, which is the correct outcome.
func CopyTableData(ctx context.Context, source, target secrets.Value, tables []string) error {
	if len(tables) == 0 {
		return nil
	}
	tables, err := tablesThatExist(ctx, source, tables)
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return nil
	}
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

	args := []string{"--format=custom", "--data-only", "--quote-all-identifiers"}
	for _, t := range tables {
		args = append(args, "--table="+t)
	}
	dump := exec.CommandContext(ctx, dumpPath, append(args, "--dbname="+source.Reveal())...)
	dump.Env = []string{"PGCONNECT_TIMEOUT=30"}

	restore := exec.CommandContext(ctx, restorePath,
		append(restoreArgs(), "--data-only", "--dbname="+target.Reveal())...)
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

// tablesThatExist narrows a list of schema qualified names to the ones the
// database actually has.
//
// to_regclass rather than a catalog join, because it answers null for a name in
// a schema that does not exist as readily as for a missing table in one that
// does, and those are the same answer for this purpose.
func tablesThatExist(ctx context.Context, conn secrets.Value, tables []string) ([]string, error) {
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return nil, fmt.Errorf("pgcopy: open a connection: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	out := make([]string, 0, len(tables))
	for _, table := range tables {
		var present bool
		if err := db.QueryRowContext(ctx,
			`SELECT to_regclass($1) IS NOT NULL`, table).Scan(&present); err != nil {
			return nil, fmt.Errorf("pgcopy: look for %s: %w", table, err)
		}
		if present {
			out = append(out, table)
		}
	}
	return out, nil
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

// Analyze refreshes a database's planner statistics.
//
// It has to be run and nothing was running it, which is a quieter failure than
// it sounds. pg_restore loads rows and does not analyse, and masking then
// rewrites most of the columns it did load, so a golden arrives with reltuples
// at zero and no column statistics at all. Postgres does not fail on that. It
// plans as if every table were empty, which means a sequential scan for
// everything.
//
// Three things downstream are then measuring the wrong database. `af insights`
// compares query plans between main and a branch to find the index somebody
// stopped using, and every plan it reads is a sequential scan chosen because
// the planner is blind. `af load` measures a p95 against those plans. And the
// per-statement timings in a migration rehearsal are timings of a plan
// production would never choose.
//
// The cost is one pass over the sample, which is seconds on the databases this
// runs against and is what anybody does by hand after a restore.
func Analyze(ctx context.Context, conn secrets.Value) error {
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return fmt.Errorf("pgcopy: open a connection to analyse: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, "ANALYZE"); err != nil {
		return fmt.Errorf("pgcopy: analyse the database: %w", err)
	}
	return nil
}

// ensureRoles creates, in the target, every role the source's policies name.
//
// Deliberately the narrowest thing that works. Each role is created NOLOGIN and
// with no attributes and no memberships, because the only job it has here is to
// be a name that `CREATE POLICY ... TO it` can resolve. Copying a role's
// password, its attributes, or what it is a member of would put a credential
// from production into a golden, which is the one thing a golden must never
// hold, and it would do so for a role nothing in the copy connects as.
//
// PUBLIC is skipped: it is not a role, it is the absence of one, and it appears
// in pg_policy as the zero OID rather than as a row.
func ensureRoles(ctx context.Context, source, target secrets.Value) error {
	names, err := policyRoles(ctx, source)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}

	// One statement, so a partially created set cannot be left behind, and
	// IF NOT EXISTS by hand because CREATE ROLE has no such clause. The name is
	// quoted through format(%I) rather than concatenated: it comes from the
	// source database, and a role called `x"; DROP` is a role somebody is
	// allowed to create.
	var b strings.Builder
	b.WriteString("DO $af$\nBEGIN\n")
	for _, name := range names {
		fmt.Fprintf(&b,
			"  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = %s) THEN\n"+
				"    EXECUTE format('CREATE ROLE %%I NOLOGIN', %s);\n"+
				"  END IF;\n",
			quoteLiteral(name), quoteLiteral(name))
	}
	b.WriteString("END\n$af$;")

	if err := Exec(ctx, target, b.String()); err != nil {
		return fmt.Errorf(
			"pgcopy: create the roles the source's policies name (%s): %w",
			strings.Join(names, ", "), err)
	}
	return nil
}

// policyRoles reads the roles named by row level security policies.
//
// Only policies. Ownership and grants are dropped by the flags on the dump, so
// the roles they mention are not needed and creating them would be creating
// more of production in the copy than the copy needs.
func policyRoles(ctx context.Context, conn secrets.Value) ([]string, error) {
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return nil, fmt.Errorf("pgcopy: open a connection to read the source's roles: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT r.rolname
		FROM pg_policy p
		JOIN pg_roles r ON r.oid = ANY (p.polroles)
		WHERE r.rolname <> 'PUBLIC'
		ORDER BY 1`)
	if err != nil {
		// A source that cannot be asked is not a source that has no policies.
		// Saying which is which matters: the first is a connection problem and
		// the second is a schema that needs nothing done.
		return nil, fmt.Errorf("pgcopy: read the roles the source's policies name: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("pgcopy: read a role name: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgcopy: read the roles the source's policies name: %w", err)
	}
	return names, nil
}

// quoteLiteral renders a string as a Postgres literal.
//
// Used for values passed to format(%I) inside the DO block above. The block is
// one statement with no parameters, so the names are in its text, and a name
// that came from another database is not a name this code chose.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
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

// ClientTools reports the newest pg_dump and pg_restore this machine can find,
// and the major version they can read up to.
//
// It exists so that `af doctor` can answer the question a refresh answers far
// too late. Copying a source shells out to these two, and a machine with
// neither, or with a client older than the server, fails at the one step that
// comes after everything else: the repository has been read, the manifest
// written, the images built, and only then does the copy stop. pg_dump refuses
// a server newer than itself outright and that cannot be worked around, so
// knowing the ceiling in advance is the difference between a five second
// correction and a twenty minute one.
//
// It runs the binaries to ask their versions and touches no database, so it is
// safe in a check that promises to change nothing.
// lookupTools is toolsFound, injected so that a test can present a machine with
// several clients installed. The distinction this function turns on only shows
// up when there is more than one, and a test that skips unless the machine
// happens to have three is a test that runs nowhere.
var lookupTools = toolsFound

func ClientTools() (path string, major int, err error) {
	// The newest rather than the one a copy would choose. A copy picks the
	// oldest client that still clears the server's version, which is right
	// when a server has been asked and wrong here, where nothing has
	// connected: the question is what this machine COULD read, and that is
	// the ceiling.
	dumps, err := lookupTools("pg_dump")
	if err != nil {
		return "", 0, err
	}
	// Both, because a copy runs both and a machine with only one of them fails
	// halfway through with the archive already written.
	if _, restoreErr := lookupTools("pg_restore"); restoreErr != nil {
		return "", 0, restoreErr
	}
	best := newest(dumps)
	return best.path, best.major, nil
}
