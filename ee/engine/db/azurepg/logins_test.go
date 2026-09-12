// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package azurepg_test

import (
	"context"
	"database/sql"
	"net/url"
	"testing"

	"github.com/antifailure/antifailure/ee/engine/db/azurepg"
	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestInheritedLoginLosesPasswordAndExistingSessionsButKeepsItsRole(t *testing.T) {
	ctx := context.Background()
	address := requirePostgres(t)
	admin, err := sql.Open("pgx", address)
	require.NoError(t, err)
	defer admin.Close()
	name := "af_login_proof_" + randomSuffix(t)
	quoted := pgx.Identifier{name}.Sanitize()
	_, err = admin.ExecContext(ctx, "CREATE ROLE "+quoted+" LOGIN PASSWORD 'AF_FAKE_INHERITED_PASSWORD'")
	require.NoError(t, err)
	defer func() { _, _ = admin.ExecContext(context.Background(), "DROP ROLE "+quoted) }()
	owner := "af_login_owner_" + randomSuffix(t)
	ownerQuoted := pgx.Identifier{owner}.Sanitize()
	_, err = admin.ExecContext(ctx, "CREATE ROLE "+ownerQuoted+" LOGIN CREATEROLE PASSWORD 'AF_FAKE_OWNER_PASSWORD'")
	require.NoError(t, err)
	defer func() { _, _ = admin.ExecContext(context.Background(), "DROP ROLE "+ownerQuoted) }()
	_, err = admin.ExecContext(ctx, "GRANT "+quoted+" TO "+ownerQuoted+" WITH ADMIN OPTION; GRANT pg_signal_backend TO "+ownerQuoted)
	require.NoError(t, err)
	names, err := azurepg.ReadCustomerLoginsForTest(ctx, admin)
	require.NoError(t, err)
	require.Contains(t, names, name)
	var current string
	require.NoError(t, admin.QueryRowContext(ctx, "SELECT current_user").Scan(&current))
	require.NotContains(t, names, current)
	parsed, err := url.Parse(address)
	require.NoError(t, err)
	parsed.User = url.UserPassword(name, "AF_FAKE_INHERITED_PASSWORD")
	inherited, err := sql.Open("pgx", parsed.String())
	require.NoError(t, err)
	defer inherited.Close()
	inherited.SetMaxOpenConns(1)
	require.NoError(t, inherited.PingContext(ctx))
	_, err = admin.ExecContext(ctx, "ALTER ROLE "+quoted+" NOLOGIN")
	require.NoError(t, err)
	require.NoError(t, inherited.PingContext(ctx), "NOLOGIN alone does not end a session")
	names, err = azurepg.ReadCustomerLoginsForTest(ctx, admin)
	require.NoError(t, err)
	require.Contains(t, names, name, "an active session disappeared from the revocation roster")
	ownerURL, err := url.Parse(address)
	require.NoError(t, err)
	ownerURL.User = url.UserPassword(owner, "AF_FAKE_OWNER_PASSWORD")
	oldOwner, err := sql.Open("pgx", ownerURL.String())
	require.NoError(t, err)
	defer oldOwner.Close()
	oldOwner.SetMaxOpenConns(1)
	var oldOwnerPID int
	require.NoError(t, oldOwner.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&oldOwnerPID))
	// Only this test-owned role is altered. Other fixture servers share the
	// physical catalog, unlike real Azure restores, which have separate ones.
	require.NoError(t, azurepg.DisableFixtureLoginsForTest(ctx, secret.New(ownerURL.String()), []string{name}))
	var ownerSessionExists bool
	require.NoError(t, admin.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", oldOwnerPID).Scan(&ownerSessionExists))
	require.False(t, ownerSessionExists, "an existing administrator session survived credential preparation")
	require.Error(t, inherited.PingContext(ctx), "an already authenticated connection survived revocation")
	fresh, err := sql.Open("pgx", parsed.String())
	require.NoError(t, err)
	defer fresh.Close()
	require.Error(t, fresh.PingContext(ctx), "the inherited credential still authenticates")
	var canLogin, passwordAbsent bool
	require.NoError(t, admin.QueryRowContext(ctx, "SELECT rolcanlogin, rolpassword IS NULL FROM pg_authid WHERE rolname=$1", name).Scan(&canLogin, &passwordAbsent))
	require.False(t, canLogin)
	require.True(t, passwordAbsent)
}

func TestVerificationCannotPublishANewLoginWithItsOriginalPassword(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	name := "af_hook_login_" + randomSuffix(t)
	quoted := pgx.Identifier{name}.Sanitize()
	admin, err := sql.Open("pgx", requirePostgres(t))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec("DROP ROLE IF EXISTS " + quoted); _ = admin.Close() })
	azurepg.IncludeFixtureLoginForTest(p, name)
	spec := goldenSpec()
	spec.Verify = func(ctx context.Context, connection secret.Value) (string, error) {
		db, err := sql.Open("pgx", connection.Reveal())
		if err != nil {
			return "", err
		}
		defer db.Close()
		_, err = db.ExecContext(ctx, "CREATE ROLE "+quoted+" LOGIN PASSWORD 'AF_FAKE_INHERITED_PASSWORD'")
		return "verified", err
	}
	_, err = p.RefreshGolden(context.Background(), spec)
	require.NoError(t, err)
	var canLogin bool
	require.NoError(t, admin.QueryRow("SELECT rolcanlogin FROM pg_roles WHERE rolname=$1", name).Scan(&canLogin))
	require.False(t, canLogin, "the verification hook's login survived publication")
}
