package personas_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/antifailure/antifailure/engine/internal/personas"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The DDL below is Supabase's own auth schema, reduced to the tables this
// touches and faithful in the three places that break an insert written
// against an imagined schema:
//
//   - auth.users.id has no default, so a row without one is a not-null
//     violation rather than a generated key.
//   - auth.identities.email is GENERATED ALWAYS, so writing it is an error.
//   - factor_type and status are enums, so a literal outside the type fails.
//
// Testing against a schema shaped like the real one is the whole point. An
// adapter proved against a convenient table proves nothing about the table it
// will actually meet.
const supabaseDDL = `
CREATE SCHEMA IF NOT EXISTS auth;
CREATE TYPE auth.factor_type AS ENUM ('totp', 'webauthn', 'phone');
CREATE TYPE auth.factor_status AS ENUM ('unverified', 'verified');

CREATE TABLE auth.users (
  instance_id        uuid,
  id                 uuid NOT NULL PRIMARY KEY,
  aud                varchar(255),
  role               varchar(255),
  email              varchar(255),
  encrypted_password varchar(255),
  email_confirmed_at timestamptz,
  last_sign_in_at    timestamptz,
  raw_app_meta_data  jsonb,
  raw_user_meta_data jsonb,
  is_super_admin     boolean,
  created_at         timestamptz,
  updated_at         timestamptz,
  is_sso_user        boolean NOT NULL DEFAULT false,
  is_anonymous       boolean NOT NULL DEFAULT false
);
CREATE UNIQUE INDEX users_email_partial_key ON auth.users (email) WHERE is_sso_user = false;

CREATE TABLE auth.identities (
  provider_id     text NOT NULL,
  user_id         uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  identity_data   jsonb NOT NULL,
  provider        text NOT NULL,
  last_sign_in_at timestamptz,
  created_at      timestamptz,
  updated_at      timestamptz,
  email           text GENERATED ALWAYS AS (lower(identity_data->>'email')) STORED,
  id              uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
  CONSTRAINT identities_provider_id_provider_unique UNIQUE (provider_id, provider)
);

CREATE TABLE auth.mfa_factors (
  id            uuid NOT NULL PRIMARY KEY,
  user_id       uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  friendly_name text,
  factor_type   auth.factor_type NOT NULL,
  status        auth.factor_status NOT NULL,
  created_at    timestamptz NOT NULL,
  updated_at    timestamptz NOT NULL,
  secret        text
);

CREATE TABLE auth.sessions (
  id         uuid NOT NULL PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  created_at timestamptz
);
CREATE TABLE auth.refresh_tokens (
  id         bigserial PRIMARY KEY,
  token      varchar(255),
  user_id    varchar(255),
  session_id uuid REFERENCES auth.sessions(id) ON DELETE CASCADE
);
CREATE TABLE auth.one_time_tokens (
  id         uuid NOT NULL PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  token_hash text NOT NULL
);
`

// requireDatabase connects to a Postgres this package has to itself.
//
// A database of its own rather than the shared one, deliberately. These tests
// create and drop tables and a whole auth schema, and the control plane's
// tenancy suite reads its table list out of Postgres rather than out of a file
// so that an unclassified table cannot ship unprotected. A table of ours
// lingering in the shared database therefore fails somebody else's gate, and
// with eleven agents on one server that is not hypothetical: it has already
// happened once today. Creating our own costs one statement and removes the
// whole class of problem.
//
// It skips rather than fails when there is nothing to connect to, which is the
// convention already in this repository: a machine with no Postgres should not
// turn a suite red.
func requireDatabase(t *testing.T) (*pgx.Conn, func()) {
	t.Helper()
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)

	// An explicit URL is used as given, so CI can point this wherever it
	// likes. Otherwise the server is the documented test Postgres and the
	// database is ours.
	if url := os.Getenv("AF_TEST_DATABASE_URL"); url != "" {
		conn, err := pgx.Connect(ctx, url)
		if err != nil {
			cancel()
			t.Skipf("skipped: no Postgres at %s: %v", redactURL(url), err)
		}
		return conn, func() {
			_ = conn.Close(context.Background())
			cancel()
		}
	}

	const server = "postgres://postgres:test@127.0.0.1:55432/"
	const ours = "antifailure_personas"

	admin, err := pgx.Connect(ctx, server+"postgres?sslmode=disable")
	if err != nil {
		cancel()
		t.Skipf("skipped: no Postgres at 127.0.0.1:55432: %v", err)
	}
	// Not IF NOT EXISTS, which CREATE DATABASE does not support. An "already
	// exists" error is the expected result on every run after the first.
	_, err = admin.Exec(ctx, "CREATE DATABASE "+ours)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		_ = admin.Close(ctx)
		cancel()
		t.Skipf("skipped: could not create the %s database: %v", ours, err)
	}
	_ = admin.Close(ctx)

	conn, err := pgx.Connect(ctx, server+ours+"?sslmode=disable")
	if err != nil {
		cancel()
		t.Skipf("skipped: no Postgres at %s: %v", ours, err)
	}
	return conn, func() {
		_ = conn.Close(context.Background())
		cancel()
	}
}

// redactURL keeps a connection string's password out of the test log, which
// is an artifact like any other.
func redactURL(u string) string {
	parsed, err := pgx.ParseConfig(u)
	if err != nil {
		return "the configured Postgres"
	}
	return fmt.Sprintf("%s:%d/%s", parsed.Host, parsed.Port, parsed.Database)
}

// freshSupabase gives one test its own copy of the auth schema.
func freshSupabase(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	_, err := conn.Exec(ctx, "DROP SCHEMA IF EXISTS auth CASCADE")
	require.NoError(t, err)
	_, err = conn.Exec(ctx, "DROP TYPE IF EXISTS auth.factor_type")
	_ = err
	_, err = conn.Exec(ctx, supabaseDDL)
	require.NoError(t, err)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = conn.Exec(c, "DROP SCHEMA IF EXISTS auth CASCADE")
	})
}

func owner() schema.Persona {
	return schema.Persona{
		Name: "owner", Email: "owner@example.test", Role: "authenticated",
		Login: schema.LoginPassword,
		Attributes: map[string]string{
			"plan": "pro", "onboarded": "true",
		},
	}
}

func TestSupabaseAdapterCreatesAnAccountThatCanActuallySignIn(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	freshSupabase(t, conn)
	ctx := context.Background()

	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})
	a := personas.NewSQLAdapter(conn, personas.SchemeSupabase, "Antifailure")

	got, err := personas.Provision(ctx, a, d, []schema.Persona{owner()})
	require.NoError(t, err)
	require.Len(t, got.Accounts, 1)

	account := got.Accounts[0]
	require.False(t, account.Reconciled, "a fresh schema had nothing to reconcile with")
	require.NotEmpty(t, account.Subject)

	// The property that matters is not that a row exists. It is that the hash
	// in the row verifies against the password the runner will be told, which
	// is the only thing that makes the sign in work.
	var hash, aud, role string
	var confirmed *time.Time
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT encrypted_password, aud, role, email_confirmed_at
		 FROM auth.users WHERE email = $1`, "owner@example.test").
		Scan(&hash, &aud, &role, &confirmed))

	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash),
		[]byte(account.Password.Reveal())),
		"the stored hash does not verify against the password the runner is given")

	cost, err := bcrypt.Cost([]byte(hash))
	require.NoError(t, err)
	require.Equal(t, personas.BcryptCost, cost)

	require.Equal(t, "authenticated", aud)
	require.Equal(t, "authenticated", role)
	require.NotNil(t, confirmed,
		"an unconfirmed persona cannot sign in when email confirmation is on")

	// The identity row is what Supabase's own sign in looks for. Without it
	// the user exists and the password is right and the sign in still fails.
	var provider, providerID string
	var identityEmail string
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT provider, provider_id, email FROM auth.identities WHERE user_id = $1::uuid`,
		account.Subject).Scan(&provider, &providerID, &identityEmail))
	require.Equal(t, "email", provider)
	require.Equal(t, account.Subject, providerID)
	require.Equal(t, "owner@example.test", identityEmail,
		"the generated email column is computed from identity_data")

	// Attributes with no column of their own go to the JSON Supabase keeps
	// user metadata in, which is where an application reads them from.
	var meta map[string]any
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT raw_user_meta_data FROM auth.users WHERE id = $1::uuid`,
		account.Subject).Scan(&meta))
	require.Equal(t, "pro", meta["plan"])
	require.Equal(t, "true", meta["onboarded"])
}

func TestProvisioningTwiceReconcilesAndNeverDuplicates(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	freshSupabase(t, conn)
	ctx := context.Background()

	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})
	a := personas.NewSQLAdapter(conn, personas.SchemeSupabase, "Antifailure")

	p := owner()
	p.MFA = true

	first, err := personas.Provision(ctx, a, d, []schema.Persona{p})
	require.NoError(t, err)

	// The second run is the normal case rather than the exception: a persona
	// is provisioned into the golden and reconciled again on the branch.
	second, err := personas.Provision(ctx, a, d, []schema.Persona{p})
	require.NoError(t, err)

	require.True(t, second.Accounts[0].Reconciled)
	require.Equal(t, first.Accounts[0].Subject, second.Accounts[0].Subject,
		"reconciling produced a different account, so the first one is orphaned")

	for _, q := range []struct {
		what string
		sql  string
	}{
		{"users", `SELECT count(*) FROM auth.users WHERE email = 'owner@example.test'`},
		{"identities", `SELECT count(*) FROM auth.identities`},
		{"mfa factors", `SELECT count(*) FROM auth.mfa_factors`},
	} {
		var n int
		require.NoError(t, conn.QueryRow(ctx, q.sql).Scan(&n))
		require.Equal(t, 1, n, "running twice left %d rows in %s", n, q.what)
	}

	// The password still works after reconciling. A second run that rewrote
	// the hash from a different derivation would leave an account nobody can
	// sign in to, and nothing above this would notice.
	var hash string
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT encrypted_password FROM auth.users WHERE email = 'owner@example.test'`).Scan(&hash))
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash),
		[]byte(second.Accounts[0].Password.Reveal())))
}

func TestReconcilingAMaskedRealUserUpdatesTheRowRatherThanAddingOne(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	freshSupabase(t, conn)
	ctx := context.Background()

	// This is the golden's real shape: the address is already there, because
	// a real customer had it and masking rewrote the name and not the fact
	// that the row exists. Two rows with one email is a broken fixture that
	// looks exactly like a broken application.
	_, err := conn.Exec(ctx, `
		INSERT INTO auth.users (id, email, aud, role, raw_user_meta_data, created_at, updated_at)
		VALUES (gen_random_uuid(), 'owner@example.test', 'authenticated', 'authenticated',
		        '{"legacy":"kept"}'::jsonb, now(), now())`)
	require.NoError(t, err)

	var before string
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT id::text FROM auth.users WHERE email = 'owner@example.test'`).Scan(&before))

	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})
	a := personas.NewSQLAdapter(conn, personas.SchemeSupabase, "Antifailure")
	got, err := personas.Provision(ctx, a, d, []schema.Persona{owner()})
	require.NoError(t, err)

	require.True(t, got.Accounts[0].Reconciled)
	require.Equal(t, before, got.Accounts[0].Subject)

	var n int
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT count(*) FROM auth.users WHERE email = 'owner@example.test'`).Scan(&n))
	require.Equal(t, 1, n)

	// The application's own metadata on that row survives, because the
	// persona's attributes are merged into it rather than replacing it.
	var meta map[string]any
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT raw_user_meta_data FROM auth.users WHERE id = $1::uuid`, before).Scan(&meta))
	require.Equal(t, "kept", meta["legacy"], "reconciling discarded the application's metadata")
	require.Equal(t, "pro", meta["plan"])
}

func TestEnrolledFactorProducesCodesTheApplicationWouldAccept(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	freshSupabase(t, conn)
	ctx := context.Background()

	p := owner()
	p.Login = schema.LoginTOTP
	p.MFA = true

	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})
	a := personas.NewSQLAdapter(conn, personas.SchemeSupabase, "Antifailure")
	got, err := personas.Provision(ctx, a, d, []schema.Persona{p})
	require.NoError(t, err)

	account := got.Accounts[0]
	require.NotEmpty(t, account.TOTPSecret.Reveal())

	var secret, factorType, status string
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT secret, factor_type::text, status::text FROM auth.mfa_factors WHERE user_id = $1::uuid`,
		account.Subject).Scan(&secret, &factorType, &status))
	require.Equal(t, "totp", factorType)
	require.Equal(t, "verified", status,
		"an unverified factor is one the user is still enrolling, and the agent cannot finish that")

	// The secret in the database is the secret the runner derives, so the
	// code the runner types is the code the application computes. This is the
	// join that a TOTP integration usually gets wrong while every unit test
	// on either side passes.
	require.Equal(t, account.TOTPSecret.Reveal(), secret)

	now := time.Now()
	code, err := personas.TOTPCode(account.TOTPSecret.Reveal(), now)
	require.NoError(t, err)
	require.True(t, personas.TOTPValid(secret, code, now),
		"the code the runner would type is not the code the stored secret produces")
}

func TestTruncateSessionsRemovesLiveLoginsFromTheGolden(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	freshSupabase(t, conn)
	ctx := context.Background()

	// A real customer's live session, of the kind masking leaves alone
	// because a session token is not personal data by any rule a scanner
	// applies. Published in a branch, it is a working login.
	_, err := conn.Exec(ctx, `
		INSERT INTO auth.users (id, email, aud, role, created_at, updated_at)
		VALUES ('11111111-1111-1111-1111-111111111111', 'real@customer.example',
		        'authenticated', 'authenticated', now(), now());
		INSERT INTO auth.sessions (id, user_id, created_at)
		VALUES ('22222222-2222-2222-2222-222222222222',
		        '11111111-1111-1111-1111-111111111111', now());
		INSERT INTO auth.refresh_tokens (token, user_id, session_id)
		VALUES ('a-real-refresh-token', '11111111-1111-1111-1111-111111111111',
		        '22222222-2222-2222-2222-222222222222');
		INSERT INTO auth.one_time_tokens (id, user_id, token_hash)
		VALUES (gen_random_uuid(), '11111111-1111-1111-1111-111111111111', 'hash');`)
	require.NoError(t, err)

	a := personas.NewSQLAdapter(conn, personas.SchemeSupabase, "Antifailure")
	emptied, err := a.TruncateSessions(ctx)
	require.NoError(t, err)
	require.Contains(t, emptied, "auth.sessions")
	require.Contains(t, emptied, "auth.refresh_tokens")
	require.Contains(t, emptied, "auth.one_time_tokens")

	for _, table := range []string{"auth.sessions", "auth.refresh_tokens", "auth.one_time_tokens"} {
		var n int
		require.NoError(t, conn.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n))
		require.Zero(t, n, "%s still holds a session that survived masking", table)
	}

	// The user is still there. Truncating sessions must not delete accounts,
	// which would take the masked production data with it.
	var users int
	require.NoError(t, conn.QueryRow(ctx, "SELECT count(*) FROM auth.users").Scan(&users))
	require.Equal(t, 1, users)
}

func TestTablesTheSchemeDescribesButTheApplicationDoesNotHaveAreSkipped(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	freshSupabase(t, conn)
	ctx := context.Background()

	// A scheme describes the tables a framework can have. A given application
	// may not use all of them, and failing on a missing one would make the
	// adapter unusable on most real projects.
	_, err := conn.Exec(ctx, "DROP TABLE auth.one_time_tokens")
	require.NoError(t, err)

	a := personas.NewSQLAdapter(conn, personas.SchemeSupabase, "Antifailure")
	emptied, err := a.TruncateSessions(ctx)
	require.NoError(t, err)
	require.NotContains(t, emptied, "auth.one_time_tokens")
	require.Contains(t, emptied, "auth.sessions")
}

func TestAnAttributeWithNowhereToGoIsRefusedRatherThanDropped(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	_, err := conn.Exec(ctx, `
		DROP TABLE IF EXISTS plain_users;
		CREATE TABLE plain_users (
		  id       bigserial PRIMARY KEY,
		  email    text NOT NULL UNIQUE,
		  password text,
		  role     text
		)`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP TABLE IF EXISTS plain_users") })

	scheme := personas.GenericScheme(personas.Table{
		Name: "plain_users", ID: "id", Email: "email",
		Password: "password", Role: "role",
	}, nil)

	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})
	a := personas.NewSQLAdapter(conn, scheme, "Antifailure")

	// Silently dropping it would give the agent a persona in the wrong state
	// and a workflow that fails for a reason nobody can see.
	_, err = personas.Provision(ctx, a, d, []schema.Persona{owner()})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no column or json field")
}

func TestTheGenericSchemeProvisionsAnApplicationThatOwnsItsUsers(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	_, err := conn.Exec(ctx, `
		DROP TABLE IF EXISTS app_users;
		CREATE TABLE app_users (
		  id         bigserial PRIMARY KEY,
		  email      text NOT NULL UNIQUE,
		  password   text,
		  role       text,
		  plan       text,
		  created_at timestamptz,
		  updated_at timestamptz
		)`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP TABLE IF EXISTS app_users") })

	scheme := personas.GenericScheme(personas.Table{
		Name: "app_users", ID: "id", Email: "email",
		Password: "password", Role: "role",
		Attributes: map[string]string{"plan": "plan"},
		Timestamps: []string{"created_at", "updated_at"},
	}, nil)

	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})
	a := personas.NewSQLAdapter(conn, scheme, "Antifailure")

	p := owner()
	p.Attributes = map[string]string{"plan": "pro"}

	got, err := personas.Provision(ctx, a, d, []schema.Persona{p})
	require.NoError(t, err)

	var hash, role, plan string
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT password, role, plan FROM app_users WHERE email = $1`,
		"owner@example.test").Scan(&hash, &role, &plan))
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash),
		[]byte(got.Accounts[0].Password.Reveal())))
	require.Equal(t, "authenticated", role)
	require.Equal(t, "pro", plan)

	// A bigserial key, not a uuid. The adapter has to leave it to the
	// sequence rather than inventing one.
	require.NotEmpty(t, got.Accounts[0].Subject)
}
