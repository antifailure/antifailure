package personas_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/personas"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// TOTP is where the orderings matter, so there is a test per ordering rather
// than a test per state.
//
// The reason is that every piece of this is correct in isolation and the bugs
// live between them. The engine derives a secret, writes it where the
// application reads it, and the runner independently derives a code from the
// same secret which the application then verifies. Three separate calculations
// that have to agree, and a test that only checks "a factor row exists"
// passes while any two of them disagree.
//
// The orderings, written down first and then one test each:
//
//	enrol then sign in                  the normal case
//	sign in before enrolment            must be refused, never accepted
//	enrol twice then sign in            the second must not rotate the secret
//	enrol here, sign in as another env  must be refused
//	enrol, sign in across a window edge tolerated either side

func totpPersona() schema.Persona {
	return schema.Persona{
		Name: "secured", Email: "secured@example.test",
		Login: schema.LoginTOTP, MFA: true,
	}
}

func TestEnrolThenSignIn(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	freshSupabase(t, conn)
	ctx := context.Background()

	d := personas.NewDeriver("env-one", personas.PasswordPolicy{})
	a := personas.NewSQLAdapter(conn, personas.SchemeSupabase, "Antifailure")
	got, err := personas.Provision(ctx, a, d, []schema.Persona{totpPersona()})
	require.NoError(t, err)

	var secret string
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT secret FROM auth.mfa_factors WHERE user_id = $1::uuid`,
		got.Accounts[0].Subject).Scan(&secret))

	now := time.Now()
	code, err := personas.TOTPCode(got.Accounts[0].TOTPSecret.Reveal(), now)
	require.NoError(t, err)
	require.True(t, personas.TOTPValid(secret, code, now))
}

func TestSignInBeforeEnrolmentIsRefused(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	freshSupabase(t, conn)
	ctx := context.Background()

	// The account exists and the factor does not, which is the state between
	// a user row being created and a second factor being enrolled. A code
	// accepted here would mean the application never checked.
	_, err := conn.Exec(ctx, `
		INSERT INTO auth.users (id, email, aud, role, created_at, updated_at)
		VALUES (gen_random_uuid(), 'secured@example.test', 'authenticated',
		        'authenticated', now(), now())`)
	require.NoError(t, err)

	var factors int
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT count(*) FROM auth.mfa_factors`).Scan(&factors))
	require.Zero(t, factors)

	// Whatever the runner would produce, there is no stored secret to check
	// it against, and validating against an empty secret must be false rather
	// than an error that some caller treats as success.
	d := personas.NewDeriver("env-one", personas.PasswordPolicy{})
	code, err := personas.TOTPCode(d.For(totpPersona()).TOTPSecret.Reveal(), time.Now())
	require.NoError(t, err)
	require.False(t, personas.TOTPValid("", code, time.Now()))
}

func TestEnrolTwiceDoesNotRotateTheSecret(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	freshSupabase(t, conn)
	ctx := context.Background()

	d := personas.NewDeriver("env-one", personas.PasswordPolicy{})
	a := personas.NewSQLAdapter(conn, personas.SchemeSupabase, "Antifailure")

	first, err := personas.Provision(ctx, a, d, []schema.Persona{totpPersona()})
	require.NoError(t, err)

	// The ordering that actually happens: provisioned into the golden, then
	// reconciled on the branch. A second enrolment that wrote a new secret
	// would leave the runner deriving codes for the old one, and the sign in
	// would fail with a correct password and a correct algorithm.
	second, err := personas.Provision(ctx, a, d, []schema.Persona{totpPersona()})
	require.NoError(t, err)

	require.Equal(t, first.Accounts[0].TOTPSecret.Reveal(),
		second.Accounts[0].TOTPSecret.Reveal())

	var count int
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT count(*) FROM auth.mfa_factors`).Scan(&count))
	require.Equal(t, 1, count, "a second enrolment left two factors, and the "+
		"application may challenge with either")

	var secret string
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT secret FROM auth.mfa_factors WHERE user_id = $1::uuid`,
		second.Accounts[0].Subject).Scan(&secret))

	now := time.Now()
	code, err := personas.TOTPCode(second.Accounts[0].TOTPSecret.Reveal(), now)
	require.NoError(t, err)
	require.True(t, personas.TOTPValid(secret, code, now),
		"after reconciling, the code the runner derives no longer matches the stored secret")
}

func TestASecretFromAnotherEnvironmentIsRefused(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	freshSupabase(t, conn)
	ctx := context.Background()

	a := personas.NewSQLAdapter(conn, personas.SchemeSupabase, "Antifailure")
	got, err := personas.Provision(ctx, a,
		personas.NewDeriver("env-one", personas.PasswordPolicy{}),
		[]schema.Persona{totpPersona()})
	require.NoError(t, err)

	var secret string
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT secret FROM auth.mfa_factors WHERE user_id = $1::uuid`,
		got.Accounts[0].Subject).Scan(&secret))

	// Credentials are per environment, which is what stops a branch's
	// password or factor from being useful anywhere else. If another
	// environment's derivation validated here, that property would not exist.
	other := personas.NewDeriver("env-two", personas.PasswordPolicy{})
	code, err := personas.TOTPCode(other.For(totpPersona()).TOTPSecret.Reveal(), time.Now())
	require.NoError(t, err)
	require.False(t, personas.TOTPValid(secret, code, time.Now()),
		"a second environment's code was accepted, so the credentials are not "+
			"scoped to an environment after all")
}

func TestACodeIsAcceptedAcrossTheWindowEdge(t *testing.T) {
	// The ordering nobody writes down: the code is produced at the end of one
	// window and arrives in the next. A server that refuses it produces a
	// failure that reproduces one time in thirty and gets called flaky.
	d := personas.NewDeriver("env-one", personas.PasswordPolicy{})
	secret := d.For(totpPersona()).TOTPSecret.Reveal()

	// A moment one second before a window boundary, chosen rather than
	// waited for, so the test is about the behaviour and not about timing.
	boundary := time.Unix((time.Now().Unix()/30+1)*30, 0).UTC()
	produced := boundary.Add(-time.Second)

	code, err := personas.TOTPCode(secret, produced)
	require.NoError(t, err)

	require.True(t, personas.TOTPValid(secret, code, produced))
	require.True(t, personas.TOTPValid(secret, code, boundary.Add(time.Second)),
		"a code produced a second before the boundary was refused a second after it")

	require.False(t, personas.TOTPValid(secret, code, boundary.Add(5*time.Minute)),
		fmt.Sprintf("a code from five minutes ago was accepted at %s", boundary))
}
