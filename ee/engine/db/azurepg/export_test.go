// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package azurepg

import (
	"context"
	"database/sql"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// The API fixture maps separate Azure servers to databases in one shared
// Postgres cluster. Its logical servers each contain only their own admin.
// Do not treat another fixture's roles as inherited customer logins. This
// constructor exists only in test binaries; live tests use New unchanged.
func NewWithFixtureRoles(opts Options) (*Provider, error) {
	p, err := New(opts)
	if err == nil {
		p.loginCatalog = func(context.Context, *sql.DB) ([]string, error) { return nil, nil }
	}
	return p, err
}

func ReadCustomerLoginsForTest(ctx context.Context, db *sql.DB) ([]string, error) {
	return customerLogins(ctx, db)
}
func DisableFixtureLoginsForTest(ctx context.Context, connection secret.Value, names []string) error {
	p := &Provider{loginCatalog: func(context.Context, *sql.DB) ([]string, error) { return names, nil }}
	return p.disableInheritedLogins(ctx, connection)
}

func IncludeFixtureLoginForTest(p *Provider, name string) {
	p.loginCatalog = func(ctx context.Context, db *sql.DB) ([]string, error) {
		var exists bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1 AND rolcanlogin)", name).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			return []string{name}, nil
		}
		return nil, nil
	}
}
