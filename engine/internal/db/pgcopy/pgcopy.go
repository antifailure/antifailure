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

	dump := exec.CommandContext(ctx, dumpPath,
		"--format=custom",
		"--no-owner", "--no-privileges", "--no-acl",
		// Row level security policies are preserved rather than dropped,
		// because a Supabase schema depends on them and restoring without them
		// produces a database where every query returns nothing.
		"--quote-all-identifiers",
		"--dbname="+source.Reveal(),
	)
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
	if err := ensureRoles(ctx, source, target); err != nil {
		return err
	}

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
	// A restore loads rows and leaves the planner with no statistics at all.
	// Anything that reads the copy afterwards, including the masking run's own
	// chunked updates, plans against a database that looks empty.
	return Analyze(ctx, target)
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
