package supabase_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/db/supabase"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// realApplicationSeed is the shape of an actual Supabase application, which the
// conformance suite's schema deliberately is not.
//
// The conformance schema is awkward in the ways a generic Postgres provider
// gets wrong: a composite key, a self reference, a sequence. This one is
// awkward in the ways a SUPABASE provider gets wrong, and every line of it is
// here because it broke something:
//
//   - a public table with a foreign key to auth.users, which is the commonest
//     shape there is on this platform and which fails to validate if the users
//     do not travel with the golden;
//   - a row in auth.identities, without which a user is a row rather than an
//     account that can sign in;
//   - a row in auth.sessions, which must NOT travel, because masking has no
//     rule that would touch a session token and a golden carrying live sessions
//     hands anybody who reaches a branch a working login as a real customer;
//   - a schema other than public, because pg_dump emits CREATE SCHEMA for one
//     and a restore into a branch that already has it fails;
//   - row level security with a policy, because a Supabase application is
//     unusable without it and a copy that drops policies produces a database
//     where every query returns nothing.
const realApplicationSeed = `
INSERT INTO auth.users (id, email, aud, role, instance_id)
VALUES ('11111111-1111-4111-8111-111111111111', 'real.customer@example.test',
        'authenticated', 'authenticated', '00000000-0000-0000-0000-000000000000');

INSERT INTO auth.identities (id, user_id, provider, provider_id, identity_data, last_sign_in_at)
VALUES ('22222222-2222-4222-8222-222222222222',
        '11111111-1111-4111-8111-111111111111', 'email',
        '11111111-1111-4111-8111-111111111111',
        '{"sub":"11111111-1111-4111-8111-111111111111","email":"real.customer@example.test"}'::jsonb,
        now());

INSERT INTO auth.sessions (id, user_id, created_at, updated_at)
VALUES ('33333333-3333-4333-8333-333333333333',
        '11111111-1111-4111-8111-111111111111', now(), now());

CREATE TABLE public.profiles (
    id        uuid PRIMARY KEY REFERENCES auth.users(id) ON DELETE CASCADE,
    nickname  text NOT NULL
);
INSERT INTO public.profiles VALUES ('11111111-1111-4111-8111-111111111111', 'ada');

ALTER TABLE public.profiles ENABLE ROW LEVEL SECURITY;
CREATE POLICY profiles_are_self_readable ON public.profiles
    FOR SELECT USING (id = auth.uid());

CREATE SCHEMA reporting;
CREATE TABLE reporting.daily (day date PRIMARY KEY, signups int NOT NULL);
INSERT INTO reporting.daily VALUES ('2026-01-01', 7);
`

// TestARealApplicationShapeSurvivesTheCopyAndTheReset is the test the
// conformance suite cannot be: it is about what Supabase specifically does to a
// copy, and it exercises the ordering the suite exercises only by accident.
//
// A branch is filled twice here. The first fill is into a branch as Supabase
// creates it. The second is into a branch that already holds an application,
// which is what every branch of a project with a migration history looks like
// and what every reset looks like. Those two are the same code path on purpose,
// and this is the test that says so.
func TestARealApplicationShapeSurvivesTheCopyAndTheReset(t *testing.T) {
	token, ref := supabaseCredentials(t)

	p, err := supabase.New(supabase.Options{
		Token:        token,
		ProjectRef:   ref,
		Clock:        clock.New(),
		SeedSQL:      realApplicationSeed,
		Version:      17,
		PollInterval: 2 * time.Second,
		PollTimeout:  6 * time.Minute,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	masked, verified := 0, 0
	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version:   17,
		RulesHash: "realshape",
		Mask: func(context.Context, secrets.Value) error {
			masked++
			return nil
		},
		Verify: func(context.Context, secrets.Value) (string, error) {
			verified++
			return `{"scanner":"realshape","findings":0}`, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = p.DestroyGolden(context.WithoutCancel(ctx), gv.ID)
	})
	require.Equal(t, 1, masked)
	require.Equal(t, 1, verified)

	branch, err := p.Branch(ctx, gv.ID, "env_realshape00001")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = p.Destroy(context.WithoutCancel(ctx), branch)
	})

	assertTheApplicationArrivedIntact(ctx, t, p, branch)

	// Now make the branch look like one Supabase handed over after running a
	// project's migrations: objects already present, in public and elsewhere,
	// that the golden knows nothing about.
	exec(ctx, t, p, branch, `
		CREATE TABLE public.left_over (id int PRIMARY KEY);
		INSERT INTO public.left_over VALUES (1);
		CREATE SCHEMA stale;
		CREATE TABLE stale.rows (id int PRIMARY KEY);
		INSERT INTO public.profiles VALUES
			('11111111-1111-4111-8111-111111111111', 'changed')
			ON CONFLICT (id) DO UPDATE SET nickname = 'changed';
	`)

	require.NoError(t, p.Reset(ctx, branch))

	assertTheApplicationArrivedIntact(ctx, t, p, branch)
	require.Equal(t, 0, count(ctx, t, p, branch,
		`SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename = 'left_over'`),
		"a table the golden does not have survived the reset")
	require.Equal(t, 0, count(ctx, t, p, branch,
		`SELECT count(*) FROM pg_namespace WHERE nspname = 'stale'`),
		"a schema the golden does not have survived the reset")
	require.Equal(t, 1, count(ctx, t, p, branch,
		`SELECT count(*) FROM public.profiles WHERE nickname = 'ada'`),
		"the reset did not undo a write")

	// The ordering nobody enumerates: the branch was CREATED and the fill
	// failed. Creating and filling are two calls, and the retry that follows a
	// failure of the second one used to be handed the existing branch without
	// anybody asking whether it held anything. An environment whose database is
	// empty because a copy failed silently is the failure this product exists
	// to make impossible, so it is a test rather than a comment.
	//
	// A branch that was never filled is one with no record of which golden
	// filled it, which is what dropping the schema reproduces exactly.
	exec(ctx, t, p, branch, `DROP SCHEMA `+supabase.MetaSchema+` CASCADE;
		DROP TABLE public.profiles;`)

	again, err := p.Branch(ctx, gv.ID, "env_realshape00001")
	require.NoError(t, err)
	require.Equal(t, branch.ProviderRef, again.ProviderRef,
		"the retry made a second branch instead of healing the first")
	assertTheApplicationArrivedIntact(ctx, t, p, again)
}

func assertTheApplicationArrivedIntact(ctx context.Context, t *testing.T, p *supabase.Provider, b provider.Branch) {
	t.Helper()

	require.Equal(t, 1, count(ctx, t, p, b, `SELECT count(*) FROM public.profiles`),
		"the application's rows did not arrive")

	// The one that fails silently. pg_restore reports a foreign key it cannot
	// validate and carries on, so the rows land and the constraint does not,
	// and nothing downstream ever notices until a delete does not cascade.
	require.Equal(t, 1, count(ctx, t, p, b,
		`SELECT count(*) FROM pg_constraint WHERE conname = 'profiles_id_fkey' AND contype = 'f'`),
		"the foreign key to auth.users is missing, so the copy has referential integrity it did not report losing")

	require.Equal(t, 1, count(ctx, t, p, b,
		`SELECT count(*) FROM auth.users WHERE email = 'real.customer@example.test'`),
		"the user the application's foreign key points at did not arrive")
	require.Equal(t, 1, count(ctx, t, p, b, `SELECT count(*) FROM auth.identities`),
		"the identity did not arrive, so the user is a row rather than an account")

	// The security property. A session is not personal data by any rule the
	// verification scanner applies, so nothing else in the product would catch
	// one travelling.
	require.Equal(t, 0, count(ctx, t, p, b, `SELECT count(*) FROM auth.sessions`),
		"a live session travelled with the golden; anybody who can reach this branch is logged in as a real customer")

	require.Equal(t, 1, count(ctx, t, p, b, `SELECT count(*) FROM reporting.daily`),
		"a schema other than public did not arrive")

	require.Equal(t, 1, count(ctx, t, p, b,
		`SELECT count(*) FROM pg_policies WHERE tablename = 'profiles'`),
		"the row level security policy did not arrive, so this branch is a database where every query returns nothing")
	require.True(t, boolean(ctx, t, p, b,
		`SELECT relrowsecurity FROM pg_class WHERE oid = 'public.profiles'::regclass`),
		"row level security is not enabled on the copy")

	// Supabase's own grants are untouched by the emptying, which is why the
	// emptying drops objects rather than the public schema.
	require.Equal(t, 1, count(ctx, t, p, b,
		`SELECT count(*) FROM pg_namespace
		 WHERE nspname = 'public' AND array_to_string(nspacl, ',') LIKE '%anon=U%'`),
		"the anon grant on public is gone, so every table created here is invisible to the REST API")
}

func open(ctx context.Context, t *testing.T, p *supabase.Provider, b provider.Branch) *sql.DB {
	t.Helper()
	conn, err := p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)
	db, err := sql.Open("pgx", conn.Reveal())
	require.NoError(t, err)
	db.SetMaxOpenConns(2)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func exec(ctx context.Context, t *testing.T, p *supabase.Provider, b provider.Branch, script string) {
	t.Helper()
	_, err := open(ctx, t, p, b).ExecContext(ctx, script)
	require.NoError(t, err)
}

func count(ctx context.Context, t *testing.T, p *supabase.Provider, b provider.Branch, query string) int {
	t.Helper()
	var n int
	require.NoError(t, open(ctx, t, p, b).QueryRowContext(ctx, query).Scan(&n))
	return n
}

func boolean(ctx context.Context, t *testing.T, p *supabase.Provider, b provider.Branch, query string) bool {
	t.Helper()
	var v bool
	require.NoError(t, open(ctx, t, p, b).QueryRowContext(ctx, query).Scan(&v))
	return v
}
