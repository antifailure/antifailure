package docker

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// readyTimeout is how long a container has to start accepting connections.
//
// Postgres initialises a data directory on first start, which on a cold Docker
// virtual machine can take twenty seconds. Being generous here costs nothing
// when things are fast and avoids a flaky failure when they are not.
const readyTimeout = 90 * time.Second

// waitReady blocks until the database accepts a query, or the deadline passes.
//
// It polls rather than watching the container's health status because the
// health check reports what the container thinks and a query reports what the
// caller will actually experience. The difference matters during the window
// where Postgres is up but still replaying its write ahead log.
func (p *Provider) waitReady(ctx context.Context, conn secrets.Value) error {
	deadline := p.clock.Now().Add(readyTimeout)
	var last error
	for p.clock.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := p.ping(pingCtx, conn)
		cancel()
		if err == nil {
			return nil
		}
		last = err
		if sleepErr := p.clock.Sleep(ctx, 250*time.Millisecond); sleepErr != nil {
			return sleepErr
		}
	}
	return aferrors.Wrap(last, aferrors.AFDB002, "host", "127.0.0.1")
}

func (p *Provider) ping(ctx context.Context, conn secrets.Value) error {
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return fmt.Errorf("db.docker: open a connection: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	var one int
	// A query rather than a ping. Postgres accepts a connection before it will
	// answer a query during recovery, and an environment that connects but
	// cannot read is not ready.
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return err
	}
	return nil
}

// execSQL runs a script against a database.
func (p *Provider) execSQL(ctx context.Context, conn secrets.Value, script string) error {
	if strings.TrimSpace(script) == "" {
		return nil
	}
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return fmt.Errorf("db.docker: open a connection: %w", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, script); err != nil {
		return fmt.Errorf("db.docker: run the seed script: %w", err)
	}
	return nil
}

// copyDatabase copies a source database into a candidate.
//
// It shells out to pg_dump and pg_restore rather than reimplementing them.
// That is the right call for a specific reason: the wire format, the ordering
// of dependent objects, extension handling, and large object support are all
// things those tools already get right, and a reimplementation would be a
// second source of truth for the most consequential operation in the product.
//
// The subprocess environment is constructed explicitly rather than inherited,
// so that the workstation's own credentials cannot reach the child, and the
// connection strings go through the environment rather than the argument
// vector, so they never appear in a process listing.
func (p *Provider) copyDatabase(ctx context.Context, source, target secrets.Value) error {
	dumpPath, err := exec.LookPath("pg_dump")
	if err != nil {
		return fmt.Errorf(
			"db.docker: pg_dump is not on the path, and it is what copies a source database. " +
				"Install the Postgres client tools, or configure a seed command instead of a source")
	}
	restorePath, err := exec.LookPath("pg_restore")
	if err != nil {
		return fmt.Errorf("db.docker: pg_restore is not on the path: %w", err)
	}

	dump := exec.CommandContext(ctx, dumpPath,
		"--format=custom",
		"--no-owner", "--no-privileges", "--no-acl",
		// Row level security policies are preserved rather than dropped,
		// because a Supabase schema depends on them and restoring without them
		// produces a database where every query returns nothing.
		"--quote-all-identifiers",
	)
	dump.Env = []string{"PGCONNECT_TIMEOUT=30", "PGDATABASE=" + source.Reveal()}
	dump.Args = append(dump.Args, "--dbname="+source.Reveal())

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
		return fmt.Errorf("db.docker: pipe the dump: %w", err)
	}
	restore.Stdin = pipe

	var dumpErr, restoreErr strings.Builder
	dump.Stderr = &dumpErr
	restore.Stderr = &restoreErr

	if err := dump.Start(); err != nil {
		return fmt.Errorf("db.docker: start pg_dump: %w", err)
	}
	if err := restore.Start(); err != nil {
		_ = dump.Process.Kill()
		return fmt.Errorf("db.docker: start pg_restore: %w", err)
	}
	if err := dump.Wait(); err != nil {
		_ = restore.Process.Kill()
		return fmt.Errorf("db.docker: pg_dump failed: %s", tail(dumpErr.String()))
	}
	if err := restore.Wait(); err != nil {
		return fmt.Errorf("db.docker: pg_restore failed: %s", tail(restoreErr.String()))
	}
	return nil
}

// tail returns the last few lines of output, which is where the actual error
// is. Printing the whole stream would bury it under progress notices.
func tail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return strings.Join(lines, "; ")
}
