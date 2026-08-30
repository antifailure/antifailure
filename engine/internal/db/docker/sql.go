package docker

import (
	"context"
	"time"

	"github.com/antifailure/antifailure/engine/internal/db/pgcopy"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// readyTimeout is how long a container has to start accepting connections.
//
// Postgres initialises a data directory on first start, which on a cold Docker
// virtual machine can take twenty seconds. Being generous here costs nothing
// when things are fast, because the wait polls and returns the moment the
// database answers, and it avoids a flaky failure when they are not.
//
// Five minutes rather than the ninety seconds this was, and the number is
// measured rather than guessed. On a busy machine a candidate container was
// watched from creation to a healthy Postgres at about a hundred seconds,
// which is over the old limit, and the failure it produced was the worst kind:
// every Docker backed test in the repository calls t.Skipf on it, so a whole
// suite reported ok while proving nothing. A false green is worse than a slow
// test, and generosity here is free.
const readyTimeout = 5 * time.Minute

// waitReady blocks until the database accepts a query, or the deadline passes.
//
// It polls rather than watching the container's health status because the
// health check reports what the container thinks and a query reports what the
// caller will actually experience. The difference matters during the window
// where Postgres is up but still replaying its write ahead log.
func (p *Provider) waitReady(ctx context.Context, conn secrets.Value) error {
	err := pgcopy.WaitReady(ctx, conn, readyTimeout, p.clock.Now, p.clock.Sleep)
	if err == nil || ctx.Err() != nil {
		return err
	}
	return aferrors.Wrap(err, aferrors.AFDB002, "host", "127.0.0.1")
}

func (p *Provider) ping(ctx context.Context, conn secrets.Value) error {
	return pgcopy.Ping(ctx, conn)
}

// execSQL runs a script against a database.
func (p *Provider) execSQL(ctx context.Context, conn secrets.Value, script string) error {
	return pgcopy.Exec(ctx, conn, script)
}

// copyDatabase copies a source database into a candidate.
func (p *Provider) copyDatabase(ctx context.Context, source, target secrets.Value) error {
	return pgcopy.Copy(ctx, source, target)
}
