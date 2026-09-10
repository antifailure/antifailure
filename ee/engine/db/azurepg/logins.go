// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package azurepg

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/jackc/pgx/v5"
)

// Azure reserves these service roles. A private live server confirmed azuresu
// and replication as its service logins; customer roles must not retain access.
func customerLogins(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT rolname FROM pg_catalog.pg_roles r
 WHERE (rolcanlogin OR EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity a WHERE a.usename=r.rolname)) AND rolname <> current_user
 AND rolname NOT IN ('azuresu','replication','azure_superuser','azure_pg_admin') ORDER BY rolname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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

// Keep ownership and grants while removing password and identity login paths.
// A role the administrator cannot disable makes preparation fail closed.
func (p *Provider) disableInheritedLogins(ctx context.Context, connection secret.Value) error {
	db, err := sql.Open("pgx", connection.Reveal())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	names, err := p.loginCatalog(ctx, db)
	if err != nil {
		return fmt.Errorf("azurepg: enumerating inherited logins: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, name := range names {
		if _, err := tx.ExecContext(ctx, "ALTER ROLE "+pgx.Identifier{name}.Sanitize()+" NOLOGIN PASSWORD NULL"); err != nil {
			return fmt.Errorf("azurepg: disabling inherited login %q: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	var current string
	if err := db.QueryRowContext(ctx, "SELECT current_user").Scan(&current); err != nil {
		return err
	}
	targets := append(names, current)
	for _, name := range targets {
		if _, err := db.ExecContext(ctx, "SELECT pg_terminate_backend(pid) FROM pg_catalog.pg_stat_activity WHERE usename=$1 AND pid<>pg_backend_pid()", name); err != nil {
			return fmt.Errorf("azurepg: ending inherited login sessions: %w", err)
		}
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		var remaining int
		if err := db.QueryRowContext(wait, "SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE usename=ANY($1) AND pid<>pg_backend_pid()", targets).Scan(&remaining); err != nil {
			return err
		}
		if remaining == 0 {
			return nil
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("azurepg: inherited sessions did not end: %w", wait.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
