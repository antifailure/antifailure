package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/auth"
	"github.com/antifailure/antifailure/engine/internal/cli"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// af provider, end to end through the command line.
//
// The client is tested in internal/auth against a scripted server. What this
// adds is the layer between a person and that client, which is where the
// mistakes with real consequences live:
//
//   - the key must never appear in the argument vector, because argv is in the
//     shell's history, in ps for every other user, and in any recording;
//   - a command that needs a key and has no terminal has to refuse rather than
//     read, or it hangs in CI looking like a network problem;
//   - and nothing printed may contain a key, on the success path or any of the
//     failure paths.
//
// The credential store is injected, so none of this touches the developer's
// keychain or leaves a token on a CI machine.

// memoryRing is an in-memory keyring for tests.
type memoryRing struct{ items map[string]string }

func newMemoryRing() *memoryRing { return &memoryRing{items: map[string]string{}} }

func (m *memoryRing) Get(service, name string) (string, error) {
	v, ok := m.items[service+"/"+name]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (m *memoryRing) Set(service, name, value string) error {
	m.items[service+"/"+name] = value
	return nil
}
func (m *memoryRing) Delete(service, name string) error {
	delete(m.items, service+"/"+name)
	return nil
}

const fakeKey = "sk-ant-api03-qqqqqqqqqqqqqqqqqqqqqqqqqqqq7777"

// providerHarness is a control plane that records what it was sent.
type providerHarness struct {
	t      *testing.T
	server *httptest.Server
	store  *auth.Store
	// requests is every path the CLI called, in order.
	requests []string
	// bodies is every body it sent.
	bodies []string
	// reply, when set, overrides the default answer for the next call.
	status int
	reply  any
}

func newProviderHarness(t *testing.T) *providerHarness {
	t.Helper()
	h := &providerHarness{t: t, store: &auth.Store{Ring: newMemoryRing(), Dir: t.TempDir()}}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		h.requests = append(h.requests, r.Method+" "+r.URL.Path)
		h.bodies = append(h.bodies, body.String())

		w.Header().Set("content-type", "application/json")
		if h.reply != nil {
			status := h.status
			if status == 0 {
				status = 200
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(h.reply)
			h.reply, h.status = nil, 0
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/providers":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sealing": true,
				"keys": []map[string]any{{
					"provider": "anthropic", "last4": "7777", "fingerprint": "ff00ff00ff00ff00",
					"createdAt": "2026-08-01T00:00:00Z",
				}},
				"budgets": []map[string]any{{
					"provider": "anthropic", "period": "2026-08-01",
					"capUsd": 50, "spentUsd": 12.5, "remainingUsd": 37.5,
				}},
			})
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/budget"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"provider": "anthropic", "period": "2026-08-01",
				"capUsd": 25, "spentUsd": 0, "remainingUsd": 25,
			})
		case r.Method == http.MethodPut:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"provider": "anthropic", "last4": "7777",
				"fingerprint": "ff00ff00ff00ff00", "replaced": false, "sameAsBefore": false,
			})
		case r.Method == http.MethodDelete:
			_ = json.NewEncoder(w).Encode(map[string]any{"provider": "anthropic", "revoked": true})
		default:
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "no such route"})
		}
	}))
	t.Cleanup(h.server.Close)
	return h
}

// signIn stores a credential the way af login would.
func (h *providerHarness) signIn(scopes ...string) {
	h.t.Helper()
	require.NoError(h.t, h.store.Save(auth.Credential{
		ControlPlane: auth.Normalise(h.server.URL),
		Token:        "afu_" + strings.Repeat("t", 43),
		Login:        "somebody",
		Organization: "antifailure",
		Scopes:       scopes,
	}))
}

// run executes the CLI against this harness.
func (h *providerHarness) run(stdin string, args ...string) result {
	h.t.Helper()
	var out, errW bytes.Buffer
	full := append([]string{}, args...)
	full = append(full, "--control-plane", h.server.URL)
	code := cli.Execute(context.Background(), full, cli.Options{
		Stdout:      &out,
		Stderr:      &errW,
		Stdin:       strings.NewReader(stdin),
		Getenv:      func(string) string { return "" },
		Clock:       clock.NewFake(epoch),
		WorkDir:     h.t.TempDir(),
		Credentials: h.store,
	})
	return result{code: code, stdout: out.String(), stderr: errW.String()}
}

// runWithEnv is the same with an environment.
func (h *providerHarness) runWithEnv(env map[string]string, args ...string) result {
	h.t.Helper()
	var out, errW bytes.Buffer
	full := append([]string{}, args...)
	full = append(full, "--control-plane", h.server.URL)
	code := cli.Execute(context.Background(), full, cli.Options{
		Stdout:      &out,
		Stderr:      &errW,
		Stdin:       strings.NewReader(""),
		Getenv:      func(k string) string { return env[k] },
		Clock:       clock.NewFake(epoch),
		WorkDir:     h.t.TempDir(),
		Credentials: h.store,
	})
	return result{code: code, stdout: out.String(), stderr: errW.String()}
}

// ---------------------------------------------------------------------------

func TestProvider_ThereIsNoKeyFlag(t *testing.T) {
	// THE ONE THAT MATTERS MOST, and the only one that catches somebody adding
	// a --key flag later because it seemed convenient. A secret in argv is in
	// the shell history file, in ps for every other user on the machine, and in
	// any terminal recording, before it is ever sent anywhere.
	h := newProviderHarness(t)
	h.signIn("providers.write")

	help := h.run("", "provider", "set", "--help")
	// The flag list only. The prose above it says there is no --key flag and
	// why, and a naive substring check would match that and pass for a command
	// that had one.
	_, flags, found := strings.Cut(help.stdout, "\nFlags:\n")
	require.True(t, found, "the help has no flag list to check")
	flags, _, _ = strings.Cut(flags, "\nGlobal flags:")
	require.NotContains(t, flags, "--key")

	// And it is refused rather than ignored, so nobody believes it worked.
	res := h.run("", "provider", "set", "anthropic", "--key", fakeKey)
	require.NotZero(t, res.code)
	require.Empty(t, h.requests, "a key on the command line must not reach the network")
}

func TestProviderSet_ReadsTheKeyFromStdin(t *testing.T) {
	h := newProviderHarness(t)
	h.signIn("providers.write")

	res := h.run(fakeKey+"\n", "provider", "set", "anthropic", "--stdin")
	require.Zero(t, res.code, res.stderr)
	require.Equal(t, []string{"PUT /v1/providers/anthropic"}, h.requests)
	require.Contains(t, h.bodies[0], fakeKey)

	// Reported by its last four, and the key itself is nowhere in the output.
	require.Contains(t, res.stdout, "7777")
	require.NotContains(t, res.stdout, fakeKey)
	require.NotContains(t, res.stdout, "qqqq")
}

func TestProviderSet_ReadsTheKeyFromANamedEnvironmentVariable(t *testing.T) {
	h := newProviderHarness(t)
	h.signIn("providers.write")

	res := h.runWithEnv(map[string]string{"MY_ANTHROPIC_KEY": fakeKey},
		"provider", "set", "anthropic", "--from-env", "MY_ANTHROPIC_KEY")
	require.Zero(t, res.code, res.stderr)
	require.Contains(t, h.bodies[0], fakeKey)
	require.NotContains(t, res.stdout, fakeKey)
}

func TestProviderSet_AnUnsetVariableIsNamedInTheComplaint(t *testing.T) {
	// "The key was empty" would send somebody looking at the key. The fix is to
	// export that variable, so the message has to say which one.
	h := newProviderHarness(t)
	h.signIn("providers.write")

	res := h.runWithEnv(nil, "provider", "set", "anthropic", "--from-env", "MY_ANTHROPIC_KEY")
	require.NotZero(t, res.code)
	require.Contains(t, res.stderr, "MY_ANTHROPIC_KEY")
	require.Empty(t, h.requests)
}

func TestProviderSet_RefusesWithNoTerminalRatherThanHanging(t *testing.T) {
	// The CI case. A read from a stdin nobody is typing into either blocks
	// forever or returns nothing at once, and both look like a network problem
	// to whoever is watching the log. So it refuses, and names the two flags
	// that work without a terminal.
	h := newProviderHarness(t)
	h.signIn("providers.write")

	res := h.run("", "provider", "set", "anthropic")
	require.NotZero(t, res.code)
	require.Contains(t, res.stderr, "--stdin")
	require.Contains(t, res.stderr, "--from-env")
	require.Empty(t, h.requests)
}

func TestProviderSet_ChecksTheProviderBeforeAskingForAKey(t *testing.T) {
	// The order is the point. Discovering the typo after somebody has pasted a
	// secret into a prompt means the secret was read for nothing.
	h := newProviderHarness(t)
	h.signIn("providers.write")

	res := h.run(fakeKey+"\n", "provider", "set", "gemini", "--stdin")
	require.NotZero(t, res.code)
	require.Contains(t, res.stderr, "anthropic, openai")
	require.Empty(t, h.requests)
}

func TestProviderSet_SaysSoWhenTheKeyIsTheOneAlreadyThere(t *testing.T) {
	// The mistake people make at the exact moment they believe they have
	// rotated a leaked key. Silence here would report success for an action
	// that changed nothing.
	h := newProviderHarness(t)
	h.signIn("providers.write")
	h.reply = map[string]any{
		"provider": "anthropic", "last4": "7777", "fingerprint": "ff00ff00ff00ff00",
		"replaced": true, "sameAsBefore": true,
	}

	res := h.run(fakeKey+"\n", "provider", "set", "anthropic", "--stdin")
	require.Zero(t, res.code, res.stderr)
	require.Contains(t, res.stdout, "already stored")
	require.Contains(t, res.stdout, "NEW key")
}

func TestProviderSet_ARotationSaysToRevokeAtTheProviderToo(t *testing.T) {
	// Removing a key here stops us using it and stops nobody else. Somebody
	// rotating a leaked key needs to be told that, at the moment they rotate.
	h := newProviderHarness(t)
	h.signIn("providers.write")
	h.reply = map[string]any{
		"provider": "anthropic", "last4": "7777", "fingerprint": "ff00ff00ff00ff00",
		"replaced": true, "sameAsBefore": false,
	}

	res := h.run(fakeKey+"\n", "provider", "set", "anthropic", "--stdin")
	require.Zero(t, res.code, res.stderr)
	require.Contains(t, res.stdout, "Rotated")
	require.Contains(t, res.stdout, "Revoke it at the provider too")
}

func TestProviderList_ShowsTheLastFourAndNeverAKey(t *testing.T) {
	h := newProviderHarness(t)
	h.signIn("providers.view")

	res := h.run("", "provider", "list")
	require.Zero(t, res.code, res.stderr)
	require.Contains(t, res.stdout, "anthropic")
	require.Contains(t, res.stdout, "7777")
	require.Contains(t, res.stdout, "50.00 USD")
	require.Contains(t, res.stdout, "12.50 USD")
	require.NotContains(t, res.stdout, fakeKey)
}

func TestProviderList_AProviderWithNoCapSaysNothingMayBeSpent(t *testing.T) {
	// A dash would read as "unlimited", and it is the opposite: a missing cap
	// means nothing may be spent at all.
	h := newProviderHarness(t)
	h.signIn("providers.view")

	res := h.run("", "provider", "list")
	require.Zero(t, res.code, res.stderr)
	require.Contains(t, res.stdout, "openai")
	require.Contains(t, res.stdout, "nothing may be spent")
}

func TestProviderList_SaysWhenTheControlPlaneCannotStoreAKeyAtAll(t *testing.T) {
	// Otherwise an installation with no sealing secret looks merely empty, and
	// the first person to try to store a key finds out at the failure.
	h := newProviderHarness(t)
	h.signIn("providers.view")
	h.reply = map[string]any{"sealing": false, "keys": []any{}, "budgets": []any{}}

	res := h.run("", "provider", "list")
	require.Zero(t, res.code, res.stderr)
	require.Contains(t, res.stdout, "AF_PROVIDER_KEY_SECRET")
}

func TestProviderList_JSONCarriesNoFieldForAKey(t *testing.T) {
	h := newProviderHarness(t)
	h.signIn("providers.view")

	res := h.run("", "provider", "list", "-o", "json")
	require.Zero(t, res.code, res.stderr)
	var out cli.ProviderListJSON
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &out))
	require.Len(t, out.Keys, 1)
	require.Equal(t, "7777", out.Keys[0].Last4)
	require.NotContains(t, res.stdout, fakeKey)
	// Machine readable output is what a script pipes into a log.
	require.NotContains(t, strings.ToLower(res.stdout), `"key"`)
}

func TestProviderBudget_RefusesAnythingThatIsNotAnAmount(t *testing.T) {
	// Every coercion of a non-number lands on zero, and a silent cap of zero
	// looks like a working setup until every run is refused for having no
	// allowance. So it refuses rather than coercing.
	h := newProviderHarness(t)
	h.signIn("providers.write")

	for _, bad := range []string{"lots", "", "-5", "1e", "50%"} {
		res := h.run("", "provider", "budget", "anthropic", bad)
		require.NotZero(t, res.code, "%q should be refused", bad)
		require.Empty(t, h.requests, "%q reached the network", bad)
	}
}

func TestProviderBudget_ZeroIsAllowedAndSaysWhatItMeans(t *testing.T) {
	// The negative control for the test above. A checker that refused every
	// falsy value would pass it and would have removed the only way to say
	// "spend nothing on this provider".
	h := newProviderHarness(t)
	h.signIn("providers.write")
	h.reply = map[string]any{
		"provider": "anthropic", "period": "2026-08-01",
		"capUsd": 0, "spentUsd": 0, "remainingUsd": 0,
	}

	res := h.run("", "provider", "budget", "anthropic", "0")
	require.Zero(t, res.code, res.stderr)
	require.Equal(t, []string{"PUT /v1/providers/anthropic/budget"}, h.requests)
	require.Contains(t, res.stdout, "refused")
}

func TestProviderBudget_ADollarSignIsAccepted(t *testing.T) {
	h := newProviderHarness(t)
	h.signIn("providers.write")

	res := h.run("", "provider", "budget", "anthropic", "$25")
	require.Zero(t, res.code, res.stderr)
	require.Contains(t, h.bodies[0], `"capUsd":25`)
}

func TestProviderRemove_IsNotAnErrorWhenThereWasNothing(t *testing.T) {
	h := newProviderHarness(t)
	h.signIn("providers.write")
	h.reply = map[string]any{"provider": "anthropic", "revoked": false}

	res := h.run("", "provider", "rm", "anthropic")
	require.Zero(t, res.code, res.stderr)
	require.Contains(t, res.stdout, "no anthropic key stored")
}

func TestProvider_NotSignedInSaysTheCommandThatFixesIt(t *testing.T) {
	h := newProviderHarness(t)
	// No signIn.
	res := h.run("", "provider", "list")
	require.NotZero(t, res.code)
	require.Contains(t, res.stderr, "af login")
	require.Contains(t, res.stderr, "--scope providers.write")
	require.Empty(t, h.requests)
}

func TestProvider_AMissingScopeIsToldApartFromAMissingRole(t *testing.T) {
	// Both are 403 from the server and they need opposite advice. Telling a
	// member to run af login --scope sends them round a loop that cannot help.
	h := newProviderHarness(t)
	h.signIn("providers.view")

	h.status, h.reply = 403, map[string]any{
		"error": "This token does not carry providers.write. Run: af login --scope providers.write -- and approve it in the browser.",
	}
	scope := h.run(fakeKey+"\n", "provider", "set", "anthropic", "--stdin")
	require.NotZero(t, scope.code)
	require.Contains(t, scope.stderr, "af login")

	h.status, h.reply = 403, map[string]any{
		"error": "Changing a provider key needs owner or admin. You are member.",
	}
	role := h.run(fakeKey+"\n", "provider", "set", "anthropic", "--stdin")
	require.NotZero(t, role.code)
	require.Contains(t, role.stderr, "owner or admin")
	require.NotContains(t, role.stderr, "--scope providers.write")
}

func TestProvider_NoOutputOnAnyPathContainsTheKey(t *testing.T) {
	// The sweep. Every command, success and failure, checked against the key
	// and against a long fragment of it.
	h := newProviderHarness(t)
	h.signIn("providers.write", "providers.view")

	runs := []result{
		h.run(fakeKey+"\n", "provider", "set", "anthropic", "--stdin"),
		h.run("", "provider", "list"),
		h.run("", "provider", "list", "-o", "json"),
		h.run("", "provider", "budget", "anthropic", "25"),
		h.run("", "provider", "rm", "anthropic"),
	}
	h.status, h.reply = 400, map[string]any{"error": "That is not a key we recognise."}
	runs = append(runs, h.run(fakeKey+"\n", "provider", "set", "anthropic", "--stdin"))
	runs = append(runs, h.run(fakeKey+"\n", "provider", "set", "gemini", "--stdin"))

	for i, r := range runs {
		both := r.stdout + r.stderr
		require.NotContains(t, both, fakeKey, "run %d printed the key", i)
		require.NotContains(t, both, "qqqqqqqq", "run %d printed part of the key", i)
	}
}

func TestLoginScope_RefusesAScopeThatDoesNotExist(t *testing.T) {
	// Caught in the terminal rather than after a code has been read out loud,
	// approved in a browser, and turned into a token that cannot do the thing.
	h := newProviderHarness(t)
	res := h.run("", "login", "--scope", "providers.wrote", "--no-browser")
	require.NotZero(t, res.code)
	require.Contains(t, res.stderr, "is not a scope")
	require.Contains(t, res.stderr, "providers.write")
	require.Empty(t, h.requests, "a login with a bad scope must not start one")
}
