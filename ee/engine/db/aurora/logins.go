// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package aurora

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/jackc/pgx/v5"
)

// AWS service logins are explicitly named. Customer-created rds_ names are
// not exempt. https://aws.amazon.com/blogs/database/managing-postgresql-users-and-roles/
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

// Password changes affect the next authentication, not existing sessions. The
// SQL catalog also covers login roles created by hooks outside the Admin API.
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
		return fmt.Errorf("aurora: enumerating inherited logins: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, name := range names {
		if _, err := tx.ExecContext(ctx, "ALTER ROLE "+pgx.Identifier{name}.Sanitize()+" NOLOGIN PASSWORD NULL"); err != nil {
			return fmt.Errorf("aurora: disabling inherited login %q: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// The admin's old password may have authenticated a session too. Keep only
	// this backend while retaining the role for the new derived credential.
	for _, name := range append(names, admin) {
		if _, err := db.ExecContext(ctx, "SELECT pg_terminate_backend(pid) FROM pg_catalog.pg_stat_activity WHERE usename=$1 AND pid<>pg_backend_pid()", name); err != nil {
			return fmt.Errorf("aurora: terminating inherited sessions: %w", err)
		}
	}
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
			break
		}
		delay := p.poll
		if delay <= 0 {
			delay = 10 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("aurora: inherited login sessions did not terminate: %w", ctx.Err())
		case <-time.After(delay):
		}
	}

	return nil
}
