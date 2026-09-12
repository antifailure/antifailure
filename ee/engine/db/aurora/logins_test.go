package aurora_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"testing"

	"github.com/antifailure/antifailure/ee/engine/db/aurora"
	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/stretchr/testify/require"
)

func TestFinalPublicationRevokesCustomerAndOtherAdminSessions(t *testing.T) {
	for _, noLogin := range []bool{false, true} {
		t.Run(fmt.Sprint(noLogin), func(t *testing.T) {
			s := newFake(t, seedSQL, "")
			p := newProvider(t, s)
			ctx := context.Background()
			g, _ := spec("sessions")
			var customerURL string
			var customer, adminSession *sql.Conn
			g.Verify = func(ctx context.Context, connection secret.Value) (string, error) {
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
				return "verified", nil
			}
			_, err := p.RefreshGolden(ctx, g)
			require.NoError(t, err)
			require.Error(t, customer.PingContext(ctx), "pre-existing customer session survived")
			require.Error(t, adminSession.PingContext(ctx), "another administrator session survived")
			require.Error(t, reachable(customerURL), "inherited customer credential still authenticates")
			root, err := sql.Open("pgx", requirePostgres(t))
			require.NoError(t, err)
			u, err := url.Parse(customerURL)
			require.NoError(t, err)
			var password sql.NullString
			require.NoError(t, root.QueryRow("SELECT rolpassword FROM pg_authid WHERE rolname=$1", u.User.Username()).Scan(&password))
			_ = root.Close()
			require.False(t, password.Valid, "customer password hash survived")
			require.NoError(t, reachable(requirePostgres(t)), "source admin was altered")
		})
	}
}

func TestInitialPreparationRevokesLoginBeforeMasking(t *testing.T) {
	s := newFake(t, seedSQL, "")
	p := newProvider(t, s)
	ctx := context.Background()
	first := true
	var credential string
	// The physical fixture has one shared role catalog. Install a clone-owned
	// inherited role immediately before the provider's initial catalog read.
	aurora.SetLoginCatalogForTest(p, func(ctx context.Context, db *sql.DB) ([]string, error) {
		if first {
			first = false
			var role, database string
			if err := db.QueryRowContext(ctx, "SELECT current_user,current_database()").Scan(&role, &database); err != nil {
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
			require.NoError(t, reachable(credential))
		}
		return scopedLogins(ctx, db)
	})
	g, _ := spec("initial")
	g.Mask = func(context.Context, secret.Value) error {
		require.NotEmpty(t, credential)
		require.Error(t, reachable(credential))
		return nil
	}
	g.Verify = func(context.Context, secret.Value) (string, error) { return "verified", nil }
	_, err := p.RefreshGolden(ctx, g)
	require.NoError(t, err)
}

func TestProductionCatalogFindsCustomerLoginAndNoLoginSession(t *testing.T) {
	ctx := context.Background()
	root, err := sql.Open("pgx", requirePostgres(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })
	role := "af_aur_catalog_" + randomSuffix(t)
	_, err = root.Exec("CREATE ROLE " + quote(role) + " LOGIN PASSWORD 'AfFixture9!'")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = root.Exec("DROP ROLE IF EXISTS " + quote(role)) })
	names, err := aurora.CustomerLoginsForTest(ctx, root)
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
	names, err = aurora.CustomerLoginsForTest(ctx, root)
	require.NoError(t, err)
	require.Contains(t, names, role)
}

func TestUnknownUnmanageableLoginPreventsPublication(t *testing.T) {
	s := newFake(t, seedSQL, "")
	p := newProvider(t, s)
	aurora.SetLoginCatalogForTest(p, func(context.Context, *sql.DB) ([]string, error) {
		return []string{"af_aur_nonexistent_unmanageable"}, nil
	})
	g, _ := spec("unknown")
	_, err := p.RefreshGolden(context.Background(), g)
	require.Error(t, err)
	versions, err := p.ListGoldens(context.Background())
	require.NoError(t, err)
	require.Empty(t, versions)
}
