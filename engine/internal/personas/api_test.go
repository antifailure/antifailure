package personas_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/personas"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// What these prove, and what they deliberately do not.
//
// They do not prove that Clerk accepts these requests. Only Clerk can prove
// that, and until there is a development instance to point at, the STATUS row
// for the hosted adapters says `written` rather than `proven`. Saying
// otherwise would be the exact claim this repository keeps having to retract.
//
// What they do prove is everything on this side of the wire: that a hosted
// adapter refuses to touch a production tenant, that the token goes in a
// header and never in a URL, that a rate limit is waited out rather than
// hammered through, that a 2xx with an empty body is a success rather than a
// decode error, and that a retried POST sends its body again instead of an
// empty one. Each of those is a real bug this project or its neighbours have
// already been caught by.

func hostedPersona() schema.Persona {
	return schema.Persona{
		Name: "owner", Email: "owner@example.test", Role: "admin",
		Login: schema.LoginPassword, Attributes: map[string]string{"plan": "pro"},
	}
}

func TestAHostedAdapterRefusesToCreateAnybodyWithoutASandbox(t *testing.T) {
	// The refusal the spec asks for. There is no correct fallback: the only
	// tenant left is the production one, and a persona created there is a
	// user in the real product with a password this repository derived.
	for _, hosted := range []personas.Hosted{
		personas.ClerkHosted{},
		personas.Auth0Hosted{Domain: "dev-abc.us.auth0.com"},
		personas.WorkOSHosted{},
		personas.SupabaseHosted{URL: "https://abc.supabase.co"},
	} {
		t.Run(hosted.Name(), func(t *testing.T) {
			a := personas.NewAPIAdapter(hosted, personas.APIOptions{
				Token: secrets.New("a-token"), Sandbox: false,
			})
			d := personas.NewDeriver("env", personas.PasswordPolicy{})
			_, err := personas.Provision(context.Background(), a, d,
				[]schema.Persona{hostedPersona()})

			require.Error(t, err)
			var coded *aferrors.Error
			require.ErrorAs(t, err, &coded)
			require.Equal(t, aferrors.AFDB020, coded.Code(),
				"the refusal has to carry the code, because that is what prints the "+
					"documentation link and the next step")
			require.Contains(t, err.Error(), hosted.Name(),
				"the message names which provider refused, since a manifest can have one")
		})
	}
}

func TestTheAdminTokenNeverReachesAURL(t *testing.T) {
	// A key in a URL is a key in every proxy log, every access log and every
	// browser history between here and the provider. The Neon lane already
	// holds this line and the hosted adapters have to hold it too.
	const token = "sk_test_thisisthesecret"

	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.String())
		require.Equal(t, "Bearer "+token, r.Header.Get("Authorization"))
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"user_123"}`))
	}))
	defer server.Close()

	a := personas.NewAPIAdapter(clerkAt(server.URL), personas.APIOptions{
		Token: secrets.New(token), Sandbox: true, HTTP: server.Client(),
	})
	d := personas.NewDeriver("env", personas.PasswordPolicy{})
	_, err := personas.Provision(context.Background(), a, d, []schema.Persona{hostedPersona()})
	require.NoError(t, err)

	require.NotEmpty(t, seen)
	for _, u := range seen {
		require.NotContains(t, u, token, "the admin token reached a URL: %s", u)
	}
}

func TestAnExistingAccountIsReconciledRatherThanDuplicated(t *testing.T) {
	var created, updated atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// Clerk answers a list query with a bare array rather than an
			// object, which is the kind of difference that only shows up
			// against the real API.
			_, _ = w.Write([]byte(`[{"id":"user_existing"}]`))
		case http.MethodPost:
			created.Add(1)
			_, _ = w.Write([]byte(`{"id":"user_new"}`))
		case http.MethodPatch:
			updated.Add(1)
			require.Contains(t, r.URL.Path, "user_existing")
			_, _ = w.Write([]byte(`{"id":"user_existing"}`))
		}
	}))
	defer server.Close()

	a := personas.NewAPIAdapter(clerkAt(server.URL), personas.APIOptions{
		Token: secrets.New("t"), Sandbox: true, HTTP: server.Client(),
	})
	d := personas.NewDeriver("env", personas.PasswordPolicy{})
	got, err := personas.Provision(context.Background(), a, d, []schema.Persona{hostedPersona()})
	require.NoError(t, err)

	require.Zero(t, created.Load(), "an account that already existed was created again")
	require.Equal(t, int32(1), updated.Load())
	require.True(t, got.Accounts[0].Reconciled)
	require.Equal(t, "user_existing", got.Accounts[0].Subject)
}

func TestASuccessWithAnEmptyBodyIsNotADecodeError(t *testing.T) {
	// Several of these APIs answer a successful update with 204 and nothing.
	// Treating that as malformed turns a working call into a failure, and
	// this project has already been caught by exactly that on Neon.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[{"id":"user_existing"}]`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	a := personas.NewAPIAdapter(clerkAt(server.URL), personas.APIOptions{
		Token: secrets.New("t"), Sandbox: true, HTTP: server.Client(),
	})
	d := personas.NewDeriver("env", personas.PasswordPolicy{})
	got, err := personas.Provision(context.Background(), a, d, []schema.Persona{hostedPersona()})
	require.NoError(t, err)

	// The id from the lookup survives, because an empty reply carries none
	// and forgetting it would leave the account with no subject.
	require.Equal(t, "user_existing", got.Accounts[0].Subject)
}

func TestARateLimitIsWaitedOutAndTheBodyIsSentAgain(t *testing.T) {
	// Two bugs in one test because they only appear together: a retry that
	// ignores Retry-After gets the whole token throttled and takes the rest
	// of the run with it, and a retried POST whose body was already consumed
	// sends an empty one, which the provider accepts as a no-op.
	var attempts atomic.Int32
	var bodies []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))

		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"id":"user_123"}`))
	}))
	defer server.Close()

	a := personas.NewAPIAdapter(clerkAt(server.URL), personas.APIOptions{
		Token: secrets.New("t"), Sandbox: true, HTTP: server.Client(),
	})
	d := personas.NewDeriver("env", personas.PasswordPolicy{})

	started := time.Now()
	got, err := personas.Provision(context.Background(), a, d, []schema.Persona{hostedPersona()})
	require.NoError(t, err)
	require.Equal(t, "user_123", got.Accounts[0].Subject)

	require.Equal(t, int32(2), attempts.Load())
	require.GreaterOrEqual(t, time.Since(started), time.Second,
		"the Retry-After was ignored, so a real provider would throttle the whole run")

	require.Len(t, bodies, 2)
	require.Equal(t, bodies[0], bodies[1],
		"the retry sent a different body, which for an empty one is a silent no-op")

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(bodies[1]), &payload))
	require.NotEmpty(t, payload["password"])
}

func TestARejectedTokenIsItsOwnErrorRatherThanARetryLoop(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	a := personas.NewAPIAdapter(clerkAt(server.URL), personas.APIOptions{
		Token: secrets.New("wrong"), Sandbox: true, HTTP: server.Client(),
	})
	d := personas.NewDeriver("env", personas.PasswordPolicy{})
	_, err := personas.Provision(context.Background(), a, d, []schema.Persona{hostedPersona()})

	require.Error(t, err)
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, aferrors.AFDB021, coded.Code())
	// Retrying a rejected credential cannot succeed and wastes the run's
	// budget arriving at the same answer four times.
	require.Equal(t, int32(1), attempts.Load())
}

func TestTheSupabaseAdminRequestCreatesAConfirmedUser(t *testing.T) {
	// A project with email confirmation on refuses a sign in for an
	// unconfirmed address, so a persona created unconfirmed is one waiting
	// for a mail nobody is going to send.
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			require.Equal(t, "/auth/v1/admin/users", r.URL.Path)
			_, _ = w.Write([]byte(`{"users":[]}`))
			return
		}
		// GoTrue wants both headers: apikey identifies the project and
		// Authorization carries the role.
		require.NotEmpty(t, r.Header.Get("apikey"))
		require.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		_, _ = w.Write([]byte(`{"id":"11111111-1111-1111-1111-111111111111"}`))
	}))
	defer server.Close()

	a := personas.NewAPIAdapter(personas.SupabaseHosted{URL: server.URL},
		personas.APIOptions{Token: secrets.New("service-role"), Sandbox: true, HTTP: server.Client()})
	d := personas.NewDeriver("env", personas.PasswordPolicy{})
	_, err := personas.Provision(context.Background(), a, d, []schema.Persona{hostedPersona()})
	require.NoError(t, err)

	require.Equal(t, true, payload["email_confirm"])
	require.Equal(t, "owner@example.test", payload["email"])
	require.NotEmpty(t, payload["password"])

	meta, ok := payload["user_metadata"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "pro", meta["plan"])
	require.Equal(t, "owner", meta["antifailure_persona"],
		"the persona's name is recorded on the account, so a later run and a "+
			"person reading the tenant can both tell what created it")
}

// clerkAt points the Clerk adapter at a test server.
//
// A tiny wrapper rather than a field on ClerkHosted, because Clerk's API root
// is not configurable in reality and a settable one would invite somebody to
// point production at something else.
type clerkTestHosted struct {
	personas.ClerkHosted
	base string
}

func (c clerkTestHosted) Base() string { return c.base }

func clerkAt(url string) personas.Hosted {
	return clerkTestHosted{base: url}
}
