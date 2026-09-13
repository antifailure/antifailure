// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds

// The master password rotation, and what "applied" has to mean.
//
// This used to set the password and then wait for the instance to be
// available, on the assumption that ModifyDBInstance with ApplyImmediately puts
// the instance in modifying before it returns. A live run against real RDS
// disproved that. ModifyDBInstance returned at 15:44:21.431Z on 2026-09-13, the
// DescribeDBInstances 0.6 seconds later still answered available, the wait
// returned at once, and the first connection was refused the new password with
// SQLSTATE 28P01. The fake had applied every modification inside the call, so
// no test here could have shown it.
//
// So two things are waited for, in order. First what RDS says: the instance is
// available AND no master password is listed in PendingModifiedValues, because
// a pending password means the old one is still in force whatever the status
// is. Then what the database says: the derived password opens it. The second is
// the decisive observation, and it is kept because RDS does not promise the
// pending value is visible the instant the call returns.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// rotate sets an instance's master password to the derived one and returns
// once RDS has applied it and it opens the database.
//
// It happens before anything else connects, and that ordering is the point
// rather than a detail: a restored instance carries the SOURCE's master
// credential until this call, and an environment handed the connection string
// before it would be holding production's database password.
func (p *Provider) rotate(ctx context.Context, in dbInstance) (dbInstance, error) {
	if err := p.api.rotatePassword(ctx, in.Identifier, p.passwordFor(in.Identifier)); err != nil {
		return dbInstance{}, err
	}
	live, err := p.waitPasswordApplied(ctx, in.Identifier)
	if err != nil {
		return dbInstance{}, err
	}
	return p.awaitCredential(ctx, live)
}

// waitPasswordApplied waits until an instance is available with no master
// password pending, bounded by the ready timeout.
func (p *Provider) waitPasswordApplied(ctx context.Context, name string) (dbInstance, error) {
	started := p.now()
	deadline := started.Add(p.readyTimeout)
	reported := false
	for {
		in, err := p.waitInstance(ctx, name)
		if err != nil {
			return dbInstance{}, err
		}
		if in.PendingPassword == "" {
			if reported {
				p.report(fmt.Sprintf("RDS applied the new master password for %s after %s",
					name, p.now().Sub(started).Round(time.Second)))
			}
			return in, nil
		}
		if !p.now().Before(deadline) {
			return dbInstance{}, fmt.Errorf(
				"rds: %s still lists the master password set with ModifyDBInstance as pending %s after "+
					"it was set, so the rotation has not been applied and nothing has connected with the "+
					"credential the restore inherited", name, p.readyTimeout)
		}
		if !reported {
			reported = true
			p.report(fmt.Sprintf("RDS lists the new master password for %s as pending; waiting for "+
				"it to be applied before connecting", name))
		}
		if err := p.sleep(ctx); err != nil {
			return dbInstance{}, err
		}
	}
}

// awaitCredential tries the derived password until it authenticates, bounded
// by the ready timeout.
//
// Bounded in time rather than in attempts, and by the same timeout the instance
// wait uses, so the bound means the same thing against a real account and
// against the fake: a count of attempts is minutes at the default poll and a
// fraction of a second at a test's. The shape and the sentence are shared with
// the Aurora provider, which met the same asynchronous rotation live.
//
// Only a wrong password is retried. Anything else, an unreachable address, a
// certificate that does not verify, or a pg_hba or TLS refusal, is not a
// rotation still landing, and retrying it would turn a configuration error into
// minutes of silence.
func (p *Provider) awaitCredential(ctx context.Context, in dbInstance) (dbInstance, error) {
	set := p.now()
	credentialDeadline := set.Add(p.readyTimeout)
	for attempt := 1; ; attempt++ {
		connection, err := p.connString(in)
		if err != nil {
			return dbInstance{}, err
		}
		err = authenticate(ctx, connection)
		if err == nil {
			if attempt > 1 {
				p.report(fmt.Sprintf("RDS put the new master password for %s in force after %d attempts",
					in.Identifier, attempt))
			}
			return in, nil
		}
		if !refusedCredential(err) {
			return dbInstance{}, err
		}
		if !p.now().Before(credentialDeadline) {
			return dbInstance{}, fmt.Errorf(
				"rds: the derived master password for %s was still refused %s after ModifyDBInstance "+
					"set it, so the rotation did not take effect and nothing has connected with the "+
					"credential the restore inherited: %w", in.Identifier, p.readyTimeout, err)
		}
		if attempt == 1 {
			p.report(fmt.Sprintf("RDS reports the new master password for %s as applied and has not "+
				"put it in force yet; waiting for it rather than connecting with the old one", in.Identifier))
		}
		if err := p.sleep(ctx); err != nil {
			return dbInstance{}, err
		}
		// Described again, so an instance that starts applying the change is
		// waited for, and one that fails is reported as failed.
		next, err := p.waitPasswordApplied(ctx, in.Identifier)
		if err != nil {
			return dbInstance{}, err
		}
		in = next
	}
}

// authenticate opens one connection with a credential and closes it.
func authenticate(ctx context.Context, connection secret.Value) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", connection.Reveal())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	return db.PingContext(ctx)
}

// refusedCredential reports whether a connection failed on a wrong password.
//
// 28P01 only, which is what real RDS answered while the new password was not
// yet in force. 28000 is left out on purpose: on RDS it is a missing pg_hba
// entry or a refused TLS setting, which no amount of waiting fixes.
func refusedCredential(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "28P01"
}
