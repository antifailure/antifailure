// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// The logins a restore inherits are closed before anything is masked and
// before anything is published.
//
// The fixture's instances share one Postgres and so one role catalog, which is
// why every role these tests create is named after the rotated administrator
// with an _x suffix: that is what scopedLogins, the catalog every test here
// uses, is able to see.

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/rds"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

func quote(name string) string { return pgx.Identifier{name}.Sanitize() }

// verifiedSpec is a refresh whose verification returns this attestation.
func verifiedSpec(verify func(context.Context, secret.Value) (string, error)) provider.GoldenSpec {
	var masked, verified int
	s := spec(&masked, &verified, `{"findings":0}`)
	if verify != nil {
		s.Verify = verify
	}
	return s
}

// A customer login and another administrator session that exist when the
// golden is about to be published are gone once it is, including a role that
// was already NOLOGIN and only had a session open.
func TestFinalPublicationRevokesCustomerAndOtherAdminSessions(t *testing.T) {
	for _, noLogin := range []bool{false, true} {
		t.Run(fmt.Sprint("nologin=", noLogin), func(t *testing.T) {
			s := newFake(t, conformance.DefaultSeedSQL, "")
			p := newProvider(t, s)
			ctx := context.Background()
			var customerURL string
			var customer, adminSession *sql.Conn
			g := verifiedSpec(func(ctx context.Context, connection secret.Value) (string, error) {
				u, err := url.Parse(connection.Reveal())
				require.NoError(t, err)
				role := u.User.Username() + "_xlogin"
				admin, err := sql.Open("pgx", connection.Reveal())
				require.NoError(t, err)
				t.Cleanup(func() { _ = admin.Close() })
				_, err = admin.ExecContext(ctx, "CREATE ROLE "+quote(role)+" LOGIN PASSWORD 'AfFixture9!'")
				require.NoError(t, err)
				root, err := sql.Open("pgx", requirePostgres(t))
				require.NoError(t, err)
				t.Cleanup(func() { _, _ = root.Exec("DROP ROLE IF EXISTS " + quote(role)); _ = root.Close() })
				u.User = url.UserPassword(role, "AfFixture9!")
				customerURL = u.String()
				customerDB, err := sql.Open("pgx", customerURL)
				require.NoError(t, err)
				t.Cleanup(func() { _ = customerDB.Close() })
				customer, err = customerDB.Conn(ctx)
				require.NoError(t, err)
				t.Cleanup(func() { _ = customer.Close() })
				require.NoError(t, customer.PingContext(ctx))
				adminSession, err = admin.Conn(ctx)
				require.NoError(t, err)
				t.Cleanup(func() { _ = adminSession.Close() })
				require.NoError(t, adminSession.PingContext(ctx))
				if noLogin {
					_, err = root.Exec("ALTER ROLE " + quote(role) + " NOLOGIN")
					require.NoError(t, err)
				}
				return `{"findings":0}`, nil
			})
			_, err := p.RefreshGolden(ctx, g)
			require.NoError(t, err)
			require.Error(t, customer.PingContext(ctx), "a customer session opened before publication survived it")
			require.Error(t, adminSession.PingContext(ctx), "another administrator session survived publication")
			require.Error(t, reachable(customerURL), "the inherited customer credential still authenticates")

			root, err := sql.Open("pgx", requirePostgres(t))
			require.NoError(t, err)
			defer func() { _ = root.Close() }()
			u, err := url.Parse(customerURL)
			require.NoError(t, err)
			var password sql.NullString
			require.NoError(t, root.QueryRow("SELECT rolpassword FROM pg_authid WHERE rolname=$1", u.User.Username()).Scan(&password))
			require.False(t, password.Valid, "the customer's password hash survived publication")
			require.NoError(t, reachable(requirePostgres(t)), "the fixture's own administrator was altered")
		})
	}
}

// A login the restore carried is closed before the masking step runs, so the
// customer's masking code never runs in a database a production credential can
// also open.
func TestInitialPreparationRevokesLoginBeforeMasking(t *testing.T) {
	s := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, s)
	ctx := context.Background()
	first := true
	var credential string
	// One shared role catalog, so an inherited role is installed immediately
	// before the provider's first catalog read, which is the moment a restore
	// would have delivered it.
	rds.SetLoginCatalogForTest(p, func(ctx context.Context, db *sql.DB) ([]string, error) {
		if first {
			first = false
			var role, database string
			if err := db.QueryRowContext(ctx, "SELECT current_user, current_database()").Scan(&role, &database); err != nil {
				return nil, err
			}
			role += "_xinitial"
			if _, err := db.ExecContext(ctx, "CREATE ROLE "+quote(role)+" LOGIN PASSWORD 'AfFixture9!'"); err != nil {
				return nil, err
			}
			root, err := sql.Open("pgx", requirePostgres(t))
			require.NoError(t, err)
			t.Cleanup(func() { _, _ = root.Exec("DROP ROLE IF EXISTS " + quote(role)); _ = root.Close() })
			u, err := url.Parse(requirePostgres(t))
			require.NoError(t, err)
			u.User = url.UserPassword(role, "AfFixture9!")
			u.Path = "/" + database
			credential = u.String()
			require.NoError(t, reachable(credential), "the inherited login could not open the candidate to begin with")
		}
		return scopedLogins(ctx, db)
	})
	masked := false
	g := verifiedSpec(nil)
	g.Mask = func(context.Context, secret.Value) error {
		masked = true
		require.NotEmpty(t, credential)
		require.Error(t, reachable(credential), "the masking step ran while an inherited login could still open the candidate")
		return nil
	}
	_, err := p.RefreshGolden(ctx, g)
	require.NoError(t, err)
	require.True(t, masked)
}

// The shipped catalog finds a customer login, and still finds it after it is
// made NOLOGIN while a session it opened is live. It is read on the shared
// server with a role made for the purpose and never acted on.
func TestProductionCatalogFindsCustomerLoginAndNoLoginSession(t *testing.T) {
	ctx := context.Background()
	root, err := sql.Open("pgx", requirePostgres(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })
	role := "af_rds_catalog_" + randomSuffix(t)
	_, err = root.Exec("CREATE ROLE " + quote(role) + " LOGIN PASSWORD 'AfFixture9!'")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = root.Exec("DROP ROLE IF EXISTS " + quote(role)) })
	names, err := rds.CustomerLoginsForTest(ctx, root)
	require.NoError(t, err)
	require.Contains(t, names, role)

	u, err := url.Parse(requirePostgres(t))
	require.NoError(t, err)
	u.User = url.UserPassword(role, "AfFixture9!")
	db, err := sql.Open("pgx", u.String())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.NoError(t, db.PingContext(ctx))
	_, err = root.Exec("ALTER ROLE " + quote(role) + " NOLOGIN")
	require.NoError(t, err)
	names, err = rds.CustomerLoginsForTest(ctx, root)
	require.NoError(t, err)
	require.Contains(t, names, role, "a NOLOGIN role with a live session was not listed")
}

// A login the administrator cannot disable stops publication rather than
// surviving into the golden.
func TestUnknownUnmanageableLoginPreventsPublication(t *testing.T) {
	s := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, s)
	rds.SetLoginCatalogForTest(p, func(context.Context, *sql.DB) ([]string, error) {
		return []string{"af_rds_nonexistent_unmanageable"}, nil
	})
	_, err := p.RefreshGolden(context.Background(), verifiedSpec(nil))
	require.Error(t, err)
	versions, err := p.ListGoldens(context.Background())
	require.NoError(t, err)
	require.Empty(t, versions)
}
