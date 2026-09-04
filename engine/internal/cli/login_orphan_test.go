package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/auth"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// The credential that could not be stored, and what happens to it.
//
// THE FAILURE. `af login` polls, the control plane approves, and a token good
// for ninety days is minted and handed over. The command then writes it to the
// operating system's credential store, and on macOS that write can be refused:
// over ssh or under a launchd agent there is nobody to authorise the keychain
// prompt and `security` reports the authorization was cancelled. That is
// exactly the machine with no browser the device grant exists for.
//
// What used to happen next was a returned error and nothing else. The token was
// live on the server, no longer held by anything on this machine, invisible to
// the person who caused it, and every retry of `af login` left another one
// beside it. Nobody can revoke a credential they cannot see.
//
// So a login that cannot keep its token gives it back.

// refusingRing is a keychain that exists and will not take a write. Not
// ErrKeyringUnavailable, which is the different case of no keyring at all and
// which Store.Save deliberately handles by falling through to a file.
type refusingRing struct{}

func (refusingRing) Get(string, string) (string, error) { return "", secrets.ErrNotFound }
func (refusingRing) Set(string, string, string) error {
	return errors.New("secrets: the keychain refused the write: the authorization was canceled")
}
func (refusingRing) Delete(string, string) error { return nil }

// devicePlane is the half of the control plane af login talks to: the two
// endpoints the terminal calls, whoami, and logout. It records every token
// presented for revocation.
type devicePlane struct {
	mu      sync.Mutex
	revoked []string
	// refuseLogout makes the revocation fail, which is the second case: the
	// token could not be stored AND could not be withdrawn.
	refuseLogout bool
}

func (d *devicePlane) revocations() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.revoked...)
}

func (d *devicePlane) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	write := func(v any) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	switch r.URL.Path {
	case "/auth/device/code":
		write(map[string]any{
			"device_code":               "the-device-code",
			"user_code":                 "BCDF-GHJK",
			"verification_uri":          "http://" + r.Host + "/device",
			"verification_uri_complete": "http://" + r.Host + "/device?code=BCDF-GHJK",
			"expires_in":                900,
			"interval":                  1,
		})
	case "/auth/device/token":
		// Approved already, so Poll returns on its first ask and no test here
		// waits out a polling interval.
		write(map[string]any{
			"access_token": "afu_the_minted_token",
			"token_type":   "Bearer",
			"expires_in":   7776000,
			"scope":        "environments.view runs.view events.write",
		})
	case "/v1/whoami":
		write(map[string]any{
			"login": "newcomer", "organization": "newcomer", "role": "owner",
			"scopes": []string{"environments.view", "runs.view", "events.write"},
		})
	case "/v1/logout":
		d.mu.Lock()
		d.revoked = append(d.revoked, r.Header.Get("authorization"))
		refuse := d.refuseLogout
		d.mu.Unlock()
		if refuse {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		write(map[string]any{"revoked": true})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// loginAgainst runs the real command against a real server and returns the
// error, what was printed, and the origin the credential belongs under.
func loginAgainst(t *testing.T, plane *devicePlane, store *auth.Store) (string, string, error) {
	t.Helper()
	srv := httptest.NewServer(plane)
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	e := &Env{
		Out:         NewOutput(&buf, &buf),
		Getenv:      func(k string) string { return map[string]string{"AF_CONTROL_PLANE_URL": srv.URL}[k] },
		Clock:       clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		Credentials: store,
		Redactor:    redact.New(),
	}
	cmd := newLoginCommand(e)
	cmd.SetArgs([]string{"--no-browser"})
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err := cmd.Execute()
	return buf.String(), auth.Normalise(srv.URL), err
}

func TestLogin_ACredentialThatCannotBeStoredIsGivenBackRatherThanLeftLive(t *testing.T) {
	plane := &devicePlane{}
	store := &auth.Store{Ring: refusingRing{}, Dir: t.TempDir()}

	_, origin, err := loginAgainst(t, plane, store)

	require.Error(t, err, "a login that kept nothing must not report success")
	require.Contains(t, err.Error(), "store the credential")
	require.Contains(t, err.Error(), "was revoked",
		"and it has to say the token is gone, or the reader has to guess")

	// The observable end state, which is the only thing worth asserting: the
	// control plane was told to withdraw the token it had just issued.
	require.Equal(t, []string{"Bearer afu_the_minted_token"}, plane.revocations())

	// And nothing was left behind on this machine either. A keychain that
	// refuses is not permission to write the token to a file instead.
	_, loadErr := store.Load(origin)
	require.ErrorIs(t, loadErr, auth.ErrNotSignedIn)
}

// The revocation can itself fail, and then the token really is live. Saying so
// is the difference between somebody revoking it in the console and somebody
// never learning it exists.
func TestLogin_AStoreFailureAndAFailedRevocationSayTheTokenIsStillLive(t *testing.T) {
	plane := &devicePlane{refuseLogout: true}
	store := &auth.Store{Ring: refusingRing{}, Dir: t.TempDir()}

	_, _, err := loginAgainst(t, plane, store)

	require.Error(t, err)
	require.Contains(t, err.Error(), "still live", "the one fact somebody has to act on")
	require.Contains(t, err.Error(), "Command line", "and where to go and act on it")
	require.Len(t, plane.revocations(), 1, "it did try")
}

// The ordinary path is unchanged: a store that works keeps the token and
// revokes nothing. Without this the change above could be "revoke every login".
func TestLogin_ASuccessfulLoginKeepsItsTokenAndRevokesNothing(t *testing.T) {
	plane := &devicePlane{}
	store := storeForTest(t)

	out, origin, err := loginAgainst(t, plane, store)

	require.NoError(t, err)
	require.Empty(t, plane.revocations(), "a successful login withdraws nothing")
	require.Contains(t, out, "Signed in as newcomer")

	cred, loadErr := store.Load(origin)
	require.NoError(t, loadErr)
	require.Equal(t, "afu_the_minted_token", cred.Token)
	require.Equal(t, "newcomer", cred.Organization)
}

// The last hop, which is a struct field and therefore the easiest to lose.
//
// The credential goes af login's store -> signedInControlPlane ->
// env.Options -> telemetry -> the bearer on a batch. The env package's own test
// covers the second half against a real HTTP server. This covers the first,
// through the function every lifecycle command actually calls, because a
// literal that stopped copying the field would leave every part of the chain
// passing and the product reporting nothing.
func TestLifecycle_AnOrchestratorCarriesTheCredentialAfLoginStored(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "Dockerfile"),
		[]byte("FROM scratch\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "antifailure.yaml"),
		[]byte(manifestForTest), 0o600))

	store := storeForTest(t)
	require.NoError(t, store.Save(auth.Credential{
		ControlPlane: auth.Normalise("https://cp.example.test"),
		Token:        "afu_from_af_login",
		Login:        "newcomer",
		Organization: "newcomer",
	}))

	e := &Env{
		Out:         NewOutput(io.Discard, io.Discard),
		WorkDir:     root,
		Getenv:      func(k string) string { return map[string]string{"AF_CONTROL_PLANE_URL": "https://cp.example.test"}[k] },
		Clock:       clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		Credentials: store,
		Redactor:    redact.New(),
	}

	o, _, err := orchestratorWithManifest2(e, lifecycleOptions{branch: "main", silent: true})
	require.NoError(t, err)

	url, token := o.ControlPlane()
	require.Equal(t, "https://cp.example.test", url)
	require.Equal(t, "afu_from_af_login", token)
}

// And a machine nobody signed in carries neither, so a run that never asked for
// a control plane does not acquire one.
func TestLifecycle_NoCredentialLeavesTheOrchestratorWithNoControlPlane(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "Dockerfile"),
		[]byte("FROM scratch\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "antifailure.yaml"),
		[]byte(manifestForTest), 0o600))

	e := &Env{
		Out:         NewOutput(io.Discard, io.Discard),
		WorkDir:     root,
		Getenv:      func(string) string { return "" },
		Clock:       clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		Credentials: storeForTest(t),
		Redactor:    redact.New(),
	}

	o, _, err := orchestratorWithManifest2(e, lifecycleOptions{branch: "main", silent: true})
	require.NoError(t, err)

	url, token := o.ControlPlane()
	require.Empty(t, url, "an address with no token would turn on the CI identity exchange")
	require.Empty(t, token)
}

// The smallest manifest the loader accepts, so these two tests are about the
// credential rather than about schema validation.
const manifestForTest = `version: 1
name: shop
services:
  - name: web
    kind: web
    build:
      strategy: dockerfile
      dockerfile: Dockerfile
    port: 3000
`
