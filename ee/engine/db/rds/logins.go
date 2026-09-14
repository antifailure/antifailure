// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds

// The logins a restored instance inherits, and why rotating one password is
// not the whole of making a copy of production safe to hand out.
//
// A snapshot restore carries the source instance's entire role catalog. Every
// application login, every analyst's account, every service user production
// has is present in the candidate and in every branch, each with the password
// it has in production. Rotating the master password closes one of them. The
// rest would open a preview environment's database with a production
// credential, which is exactly the exposure the rotation exists to prevent, so
// before a golden is masked and again before it is published this provider
// disables every other login role in the restored instance's own catalog,
// clears its password, and ends its sessions.
//
// The same step as the aurora provider takes, for the same reason, and kept
// as a separate copy rather than a shared helper because each provider's
// catalog query names the service accounts its own vendor reserves, and a
// helper that took that list as a parameter would be one call site away from
// being handed the wrong one.

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// customerLogins lists the roles in a restored instance that could still open
// it: every role that may log in, and every role that has a live session even
// if it may not, because a session authenticated before NOLOGIN was set is
// still open.
//
// AWS's own service logins are named explicitly and are the only exemption.
// A customer role that merely begins with rds_ is not exempt, because the
// prefix is not reserved against customers.
// https://aws.amazon.com/blogs/database/managing-postgresql-users-and-roles/
func customerLogins(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT rolname FROM pg_catalog.pg_roles
 WHERE (rolcanlogin OR EXISTS (SELECT 1 FROM pg_catalog.pg_stat_activity WHERE usename=rolname)) AND rolname<>current_user
 AND rolname NOT IN ('rdsadmin','rdsrepladmin')
 ORDER BY rolname`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// disableInheritedLogins leaves the derived master credential as the only way
// into the instance.
//
// A password change affects the next authentication and not a session that is
// already open, so the sessions are ended as well, including any other session
// of the administrator itself, which the inherited master password may have
// opened before the rotation landed. The catalog is read from SQL rather than
// from the control plane, because a login created by an extension or a hook
// never appears in any AWS API.
//
// A login this administrator cannot disable is an error, and every caller
// treats it as one that stops publication: a golden that kept a production
// login it could not remove is a golden that must not exist.
func (p *Provider) disableInheritedLogins(ctx context.Context, connection secret.Value) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", connection.Reveal())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	var admin string
	if err := db.QueryRowContext(ctx, "SELECT current_user").Scan(&admin); err != nil {
		return err
	}
	names, err := p.loginCatalog(ctx, db)
	if err != nil {
		return fmt.Errorf("rds: enumerating inherited logins: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, name := range names {
		if _, err := tx.ExecContext(ctx, "ALTER ROLE "+pgx.Identifier{name}.Sanitize()+" NOLOGIN PASSWORD NULL"); err != nil {
			return fmt.Errorf("rds: disabling inherited login %q: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, name := range append(names, admin) {
		if _, err := db.ExecContext(ctx, "SELECT pg_terminate_backend(pid) FROM pg_catalog.pg_stat_activity WHERE usename=$1 AND pid<>pg_backend_pid()", name); err != nil {
			return fmt.Errorf("rds: terminating inherited sessions: %w", err)
		}
	}
	// Terminating a backend is a request, not a completion, so the catalog is
	// read again until nothing that could open the instance remains.
	for {
		remaining, err := p.loginCatalog(ctx, db)
		if err != nil {
			return err
		}
		var adminSessions bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE usename=current_user AND pid<>pg_backend_pid())").Scan(&adminSessions); err != nil {
			return err
		}
		if len(remaining) == 0 && !adminSessions {
			return nil
		}
		delay := p.poll
		if delay <= 0 {
			delay = 10 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("rds: inherited login sessions did not terminate: %w", ctx.Err())
		case <-time.After(delay):
		}
	}
}
