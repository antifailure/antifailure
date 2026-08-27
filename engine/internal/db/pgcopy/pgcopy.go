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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
}

// Copy copies a source database into a target.
//
// It shells out to pg_dump and pg_restore rather than reimplementing them, for
// the reason in the package comment.
func Copy(ctx context.Context, source, target secrets.Value) error {
	return CopyWith(ctx, source, target, CopyOptions{})
}

// CopyWith copies a source database into a target, narrowed by opts.
//
// The subprocess environment is constructed explicitly rather than inherited,
// so that the workstation's own credentials cannot reach the child, and the
// connection strings go through flags the child reads rather than being echoed,
// with PGCONNECT_TIMEOUT set so that an unreachable source fails rather than
// hangs.
func CopyWith(ctx context.Context, source, target secrets.Value, opts CopyOptions) error {
	if len(opts.ExcludeArchiveKinds) > 0 {
		return copyThroughArchive(ctx, source, target, opts)
	}
	return copyThroughPipe(ctx, source, target, opts)
}

func copyThroughPipe(ctx context.Context, source, target secrets.Value, opts CopyOptions) error {
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
	dumpPath, err := exec.LookPath("pg_dump")
	if err != nil {
		return fmt.Errorf("pgcopy: pg_dump is not on the path: %w", err)
	}
	restorePath, err := exec.LookPath("pg_restore")
	if err != nil {
		return fmt.Errorf("pgcopy: pg_restore is not on the path: %w", err)
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

// Tail returns the last few lines of output, which is where the actual error
// is. Printing the whole stream buries it under progress notices.
func Tail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return strings.Join(lines, "; ")
}
