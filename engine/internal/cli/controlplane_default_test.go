package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/auth"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/controlplane"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// Which control plane a command talks to when nothing says.
//
// Two defects lived here and neither had a test, which is why both survived.
//
// The first: af login carried its own spelling of "the hosted instance" and it
// had drifted to app.dev.antifailure.dev, the STAGING deployment. So a plain
// af login signed a terminal in to staging while everything that sends events
// went to production, and the credential was stored under an origin nothing
// else in the engine ever looks up.
//
// The second: af env pull read AF_CONTROL_PLANE_URL and consulted the stored
// credential only when that produced something, so somebody who had run af
// login and set no variable was told AF-CPL-001, no control plane token is
// configured, while holding one. The default was filled in afterwards and
// deeper down, where the lookup could no longer see it.
//
// Both are the same mistake: resolving the origin in two places and letting
// them disagree. They are tested together, because a fix to either alone leaves
// the other reachable.
//
// Inside the package rather than beside it, so that resolution can be exercised
// without a request. app.antifailure.dev is a real host that really answers, so
// a test driving this through Execute would reach the live production control
// plane from whatever machine happened to run it.

// ringForTest is an in-memory keyring, so nothing here touches a real keychain.
type ringForTest struct{ items map[string]string }

func (m *ringForTest) Get(service, name string) (string, error) {
	v, ok := m.items[service+"/"+name]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (m *ringForTest) Set(service, name, value string) error {
	m.items[service+"/"+name] = value
	return nil
}
func (m *ringForTest) Delete(service, name string) error {
	delete(m.items, service+"/"+name)
	return nil
}

func storeForTest(t *testing.T) *auth.Store {
	t.Helper()
	return &auth.Store{Ring: &ringForTest{items: map[string]string{}}, Dir: t.TempDir()}
}

func envForTest(t *testing.T, store *auth.Store, vars map[string]string) *Env {
	t.Helper()
	return &Env{
		Getenv:      func(k string) string { return vars[k] },
		Clock:       clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		Credentials: store,
		Redactor:    redact.New(),
	}
}

func notConfigured(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFCPL001)),
		"expected AF-CPL-001, got %v", err)
}

func TestControlPlaneFor_DefaultsToTheHostedInstanceAndNotStaging(t *testing.T) {
	// Asserted against the constant the rest of the engine uses rather than
	// against a literal, so this cannot pass while the two disagree, which is
	// exactly the state it was written to catch.
	require.Equal(t, "https://app.antifailure.dev", controlplane.DefaultBaseURL)

	e := envForTest(t, storeForTest(t), nil)
	require.Equal(t, controlplane.DefaultBaseURL, controlPlaneFor(e, ""))

	// And the order below it, which is what makes a default safe to have: an
	// explicit flag beats a variable and a variable beats the default.
	e = envForTest(t, storeForTest(t), map[string]string{
		"AF_CONTROL_PLANE_URL": "https://cp.example.com",
	})
	require.Equal(t, "https://cp.example.com", controlPlaneFor(e, ""))
	require.Equal(t, "https://flag.example.com", controlPlaneFor(e, "https://flag.example.com"))
}

func TestControlPlaneClient_UsesTheCredentialAfLoginStoredWithNothingElseSet(t *testing.T) {
	store := storeForTest(t)
	require.NoError(t, store.Save(auth.Credential{
		ControlPlane: auth.Normalise(controlplane.DefaultBaseURL),
		Token:        "afu_" + strings.Repeat("t", 43),
		Login:        "somebody",
		Organization: "antifailure",
	}))

	// No AF_CONTROL_PLANE_URL and no flag, which is the ordinary case for
	// somebody who has only ever run af login. It must not answer that no token
	// is configured, because one is, and that message sends them off to create a
	// second credential to fix a problem they do not have.
	client, err := controlPlaneClient(envForTest(t, store, nil), "")
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestControlPlaneClient_SaysWhatIsMissingWhenNothingIsStored(t *testing.T) {
	_, err := controlPlaneClient(envForTest(t, storeForTest(t), nil), "")
	notConfigured(t, err)
}

func TestControlPlaneClient_DoesNotSendAnExpiredCredential(t *testing.T) {
	// The other side of the same lookup. An expired token must not be sent, or
	// the failure is a 401 from a server somebody then goes and investigates
	// instead of the sentence telling them to sign in again.
	store := storeForTest(t)
	require.NoError(t, store.Save(auth.Credential{
		ControlPlane: auth.Normalise(controlplane.DefaultBaseURL),
		Token:        "afu_" + strings.Repeat("t", 43),
		ExpiresAt:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}))
	_, err := controlPlaneClient(envForTest(t, store, nil), "")
	notConfigured(t, err)
}

func TestControlPlaneClient_TheEnvironmentBeatsTheStoredCredential(t *testing.T) {
	// A token exported into a shell is somebody deliberately overriding what is
	// on the machine, usually while debugging or in CI, so the explicit thing
	// wins. Making the credential lookup unconditional must not have changed
	// that, and this is what says so: the header the server actually receives.
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("authorization")
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	store := storeForTest(t)
	require.NoError(t, store.Save(auth.Credential{
		ControlPlane: auth.Normalise(server.URL),
		Token:        "afu_from_the_keyring",
	}))
	e := envForTest(t, store, map[string]string{
		"AF_CONTROL_PLANE_URL":   server.URL,
		"AF_CONTROL_PLANE_TOKEN": "aft_from_the_shell",
	})
	client, err := controlPlaneClient(e, "")
	require.NoError(t, err)
	_, _ = client.Pull(context.Background(), "env-pr-41")
	require.Equal(t, "Bearer aft_from_the_shell", seen)
}
