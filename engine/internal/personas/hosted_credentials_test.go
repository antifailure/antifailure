package personas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/personas"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// A hosted provider's tenant holds one account per address, and every
// environment that reaches the tenant finds and updates that same account. These
// tests drive two environments against one fake tenant, the way two preview
// environments for two branches reach one development instance, and read back
// what the tenant was told. No request leaves the machine.

// fakeTenant is a Clerk shaped tenant that remembers the last password each
// account was given, and counts what it was asked to do.
type fakeTenant struct {
	*httptest.Server
	mu        sync.Mutex
	byEmail   map[string]string // address to account id
	passwords map[string]string // account id to password
	created   int
	updated   int
	deleted   int
}

func newFakeTenant(t *testing.T) *fakeTenant {
	t.Helper()
	f := &fakeTenant{byEmail: map[string]string{}, passwords: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			for _, values := range r.URL.Query() {
				for _, v := range values {
					if id, ok := f.byEmail[v]; ok {
						_, _ = w.Write([]byte(`[{"id":"` + id + `"}]`))
						return
					}
				}
			}
			_, _ = w.Write([]byte(`[]`))
		case http.MethodPost:
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			email := firstString(body["email_address"])
			id := "user_" + strings.ReplaceAll(email, "@", "_")
			f.byEmail[email] = id
			f.passwords[id], _ = body["password"].(string)
			f.created++
			_, _ = w.Write([]byte(`{"id":"` + id + `"}`))
		case http.MethodPatch:
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			if pw, ok := body["password"].(string); ok {
				f.passwords[id] = pw
			}
			f.updated++
			_, _ = w.Write([]byte(`{"id":"` + id + `"}`))
		case http.MethodDelete:
			f.deleted++
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func firstString(v any) string {
	switch x := v.(type) {
	case []any:
		if len(x) > 0 {
			s, _ := x[0].(string)
			return s
		}
	case string:
		return x
	}
	return ""
}

func (f *fakeTenant) passwordOf(email string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.passwords[f.byEmail[email]]
}

func (f *fakeTenant) adapter(token string) *personas.APIAdapter {
	return personas.NewAPIAdapter(clerkAt(f.URL), personas.APIOptions{
		Token: secrets.New(token), Sandbox: true, HTTP: f.Client(),
	})
}

// up provisions the persona into the tenant as one environment would, and
// returns the credentials that environment's runner is handed.
func up(t *testing.T, a personas.Adapter, envID string, p schema.Persona) personas.Credentials {
	t.Helper()
	d, err := personas.DeriverFor(a, envID, "shop", personas.PasswordPolicy{})
	require.NoError(t, err)
	_, err = personas.Provision(context.Background(), a, d, []schema.Persona{p})
	require.NoError(t, err)
	return d.For(p)
}

func TestHostedCredentials_ASecondEnvironmentDoesNotLockTheFirstOut(t *testing.T) {
	tenant := newFakeTenant(t)
	a := tenant.adapter("admin-token")
	p := hostedPersona()

	first := up(t, a, "shop-main-1a2b3c", p)
	up(t, a, "shop-feature-4d5e6f", p)

	require.Equal(t, tenant.passwordOf(p.Email), first.Password.Reveal(),
		"the second environment's af up changed the password the first environment signs in with")
}

func TestHostedCredentials_ADownElsewhereLeavesTheAccountSigningIn(t *testing.T) {
	tenant := newFakeTenant(t)
	a := tenant.adapter("admin-token")
	p := hostedPersona()

	up(t, a, "shop-main-1a2b3c", p)
	second := up(t, a, "shop-feature-4d5e6f", p)

	// Nothing in this package deletes a hosted account, so tearing the first
	// environment down cannot reach the tenant. Pinned, so that a delete added
	// to provisioning or teardown has to answer for the environment still using
	// the account.
	require.Zero(t, tenant.deleted, "provisioning deleted an account another environment signs in with")
	require.Equal(t, tenant.passwordOf(p.Email), second.Password.Reveal())
}

func TestHostedCredentials_APreExistingAccountIsUpdatedAndNeverDeleted(t *testing.T) {
	tenant := newFakeTenant(t)
	p := hostedPersona()
	tenant.byEmail[p.Email] = "user_customer"
	tenant.passwords["user_customer"] = "set-by-the-customer"
	a := tenant.adapter("admin-token")

	d, err := personas.DeriverFor(a, "shop-main-1a2b3c", "shop", personas.PasswordPolicy{})
	require.NoError(t, err)
	got, err := personas.Provision(context.Background(), a, d, []schema.Persona{p})
	require.NoError(t, err)

	require.True(t, got.Accounts[0].Reconciled)
	require.Equal(t, "user_customer", got.Accounts[0].Subject)
	require.Zero(t, tenant.created, "an account that already existed was created again")
	require.Equal(t, 1, tenant.updated)
	require.Zero(t, tenant.deleted)
}

func TestHostedCredentials_ASeedPersonaStillGetsPerEnvironmentCredentials(t *testing.T) {
	// A seed or SQL adapter writes the account into the environment's own
	// branch, which nobody else signs in to, so its credentials stay per
	// environment.
	seed := personas.NewSeedAdapter(personas.SeedOptions{Command: "true"})
	p := hostedPersona()

	a, err := personas.DeriverFor(seed, "shop-main-1a2b3c", "shop", personas.PasswordPolicy{})
	require.NoError(t, err)
	b, err := personas.DeriverFor(seed, "shop-feature-4d5e6f", "shop", personas.PasswordPolicy{})
	require.NoError(t, err)

	require.NotEqual(t, a.For(p).Password.Reveal(), b.For(p).Password.Reveal(),
		"two environments' seed personas got the same credentials")
}

func TestHostedCredentials_ADifferentAdminTokenGivesADifferentPassword(t *testing.T) {
	tenant := newFakeTenant(t)
	p := hostedPersona()

	one, err := personas.DeriverFor(tenant.adapter("token-one"), "env", "shop", personas.PasswordPolicy{})
	require.NoError(t, err)
	two, err := personas.DeriverFor(tenant.adapter("token-two"), "env", "shop", personas.PasswordPolicy{})
	require.NoError(t, err)

	require.NotEqual(t, one.For(p).Password.Reveal(), two.For(p).Password.Reveal(),
		"the password did not depend on the admin token, so it is not secret")
	require.NotEqual(t, one.For(p).TOTPSecret.Reveal(), two.For(p).TOTPSecret.Reveal())
}

func TestHostedCredentials_TwoProvidersWithOneTokenDoNotShareCredentials(t *testing.T) {
	tenant := newFakeTenant(t)
	p := hostedPersona()
	clerk := tenant.adapter("same-token")
	auth0 := personas.NewAPIAdapter(personas.Auth0Hosted{Domain: tenant.URL}, personas.APIOptions{
		Token: secrets.New("same-token"), Sandbox: true, HTTP: tenant.Client(),
	})

	c, err := personas.DeriverFor(clerk, "env", "shop", personas.PasswordPolicy{})
	require.NoError(t, err)
	o, err := personas.DeriverFor(auth0, "env", "shop", personas.PasswordPolicy{})
	require.NoError(t, err)

	require.NotEqual(t, c.For(p).Password.Reveal(), o.For(p).Password.Reveal(),
		"two providers holding an equal credential derived one password")
}

func TestHostedCredentials_AreNeverThePerEnvironmentDerivation(t *testing.T) {
	tenant := newFakeTenant(t)
	p := hostedPersona()
	hosted, err := personas.DeriverFor(tenant.adapter("admin-token"), "shop-main-1a2b3c", "shop", personas.PasswordPolicy{})
	require.NoError(t, err)

	for _, envID := range []string{"shop-main-1a2b3c", "shop-feature-4d5e6f", ""} {
		legacy := personas.NewDeriver(envID, personas.PasswordPolicy{})
		require.NotEqual(t, legacy.For(p).Password.Reveal(), hosted.For(p).Password.Reveal(),
			"a hosted persona got the unkeyed per environment password")
		require.NotEqual(t, legacy.For(p).TOTPSecret.Reveal(), hosted.For(p).TOTPSecret.Reveal(),
			"a hosted persona got the unkeyed per environment second factor")
	}
}

func TestHostedCredentials_AnEmptyAdminTokenIsRefused(t *testing.T) {
	tenant := newFakeTenant(t)
	_, err := personas.DeriverFor(tenant.adapter(""), "env", "shop", personas.PasswordPolicy{})

	require.Error(t, err, "an empty admin token was used as a key")
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, aferrors.AFDB025, coded.Code())
}
