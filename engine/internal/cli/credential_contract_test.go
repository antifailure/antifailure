package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/auth"
	"github.com/antifailure/antifailure/engine/internal/cli"
)

// The exit code table in the error reference says scripts can branch on 4
// for "authentication or authorization failed", and af whoami and af provider
// list honoured it. af token list, create and rm did not: the same absent
// credential printed a bare string with no code, no Next line and no docs
// link, and exited 1, so a script branching on 4 to run af login treated every
// token subcommand as a generic failure. The expired, revoked and short a
// scope cases were bare strings and exit 1 on every command.
//
// One table, every command that reads the stored sign in, every reason it is
// unusable, and the code and exit each must produce.
func TestStoredCredential_EveryCommandRefusesWithTheSameCodeAndExit(t *testing.T) {
	commands := map[string][]string{
		"whoami":        {"whoami"},
		"provider list": {"provider", "list"},
		"token list":    {"token", "list"},
		"token create":  {"token", "create", "ci"},
		"token rm":      {"token", "rm", "tok_abc"},
	}
	scopeOf := map[string]string{
		"whoami":        "",
		"provider list": "--scope providers.write",
		"token list":    "--scope tokens.manage",
		"token create":  "--scope tokens.manage",
		"token rm":      "--scope tokens.manage",
	}

	for name, args := range commands {
		t.Run(name+" not signed in", func(t *testing.T) {
			h := newProviderHarness(t)
			res := h.run("", args...)
			require.Equal(t, 4, res.code, res.stderr)
			require.Contains(t, res.stderr, "AF-CPL-004")
			require.Contains(t, prose(res.stderr), "Next:")
			require.Contains(t, prose(res.stderr), "More:")
			require.Contains(t, prose(res.stderr), "af login")
			if scope := scopeOf[name]; scope != "" {
				require.Contains(t, prose(res.stderr), scope)
			}
			require.Empty(t, h.requests, "nothing to send without a credential")
		})

		t.Run(name+" expired", func(t *testing.T) {
			h := newProviderHarness(t)
			require.NoError(t, h.store.Save(auth.Credential{
				ControlPlane: auth.Normalise(h.server.URL),
				Token:        "afu_" + strings.Repeat("t", 43),
				Login:        "somebody",
				Organization: "antifailure",
				Scopes:       []string{"providers.write", "tokens.manage"},
				ExpiresAt:    epoch.Add(-time.Hour),
			}))
			res := h.run("", args...)
			require.Equal(t, 4, res.code, res.stderr)
			require.Contains(t, res.stderr, "AF-CPL-005")
			require.Contains(t, prose(res.stderr), "expired")
			require.Contains(t, prose(res.stderr), "af login")
			require.Empty(t, h.requests, "an expired credential is refused before any request")
		})

		t.Run(name+" no longer accepted", func(t *testing.T) {
			h := newProviderHarness(t)
			h.signIn("providers.write", "tokens.manage")
			h.status, h.reply = 401, map[string]any{"error": "token revoked"}
			res := h.run("", args...)
			require.Equal(t, 4, res.code, res.stderr)
			require.Contains(t, res.stderr, "AF-CPL-006")
			require.Contains(t, prose(res.stderr), "no longer accepts")
			require.Contains(t, prose(res.stderr), "af login")
			require.Len(t, h.requests, 1)
		})
	}

	// whoami has no scope, so a 403 naming one is a token or provider matter.
	for _, name := range []string{"provider list", "token list", "token create", "token rm"} {
		t.Run(name+" missing the scope", func(t *testing.T) {
			h := newProviderHarness(t)
			h.signIn("providers.view")
			h.status, h.reply = 403, map[string]any{
				"error": "This token does not carry " + strings.TrimPrefix(scopeOf[name], "--scope ") +
					". Run: af login " + scopeOf[name] + " and approve it in the browser.",
			}
			res := h.run("", commands[name]...)
			require.Equal(t, 4, res.code, res.stderr)
			require.Contains(t, res.stderr, "AF-CPL-007")
			// The server's sentence, once, without the wrapper's prefix.
			require.Contains(t, prose(res.stderr), "This token does not carry")
			require.NotContains(t, prose(res.stderr), "does not carry the scope: This token")
			require.Contains(t, prose(res.stderr), scopeOf[name])
		})
	}
}

// The exit code is the contract. A coded error that rendered its code and
// still exited 1 would pass every string assertion above and break the
// script, so the code is asserted on its own for the command that was wrong.
func TestTokenList_NotSignedInExitsFourLikeWhoami(t *testing.T) {
	h := newProviderHarness(t)
	whoami := h.run("", "whoami")
	token := h.run("", "token", "list")
	require.Equal(t, 4, whoami.code)
	require.Equal(t, whoami.code, token.code)
}

// af init on a repository that already has a manifest reused AF-MAN-002,
// whose next step is "fix the reported line, then run af doctor". There is
// no reported line, the file is usually valid, and af doctor cannot change
// the fact that stops the command. The flag that does was never named.
func TestInit_AnExistingManifestNamesTheFlagThatReplacesIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"a","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`), 0o600))
	writeManifest(t, dir, "version: 1\nname: mine\nservices:\n  - name: web\n    port: 9999\n")

	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Equal(t, 3, got.code)
	require.Contains(t, got.stderr, "AF-MAN-007")
	require.Contains(t, prose(got.stderr), "af init --force")
	require.Contains(t, prose(got.stderr), filepath.Join(dir, "antifailure.yaml"))
	require.NotContains(t, prose(got.stderr), "af doctor")
	require.NotContains(t, prose(got.stderr), "reported line")
	require.NotContains(t, got.stderr, "AF-MAN-002")

	// And the flag it names does what the message says.
	forced := runCLI(t, dir, nil, "init", "--non-interactive", "--force")
	require.Zero(t, forced.code, forced.stderr)
	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(body), "9999")
}

// af env list -o json called the services column "name". The key held the
// comma joined service list and the identifier was already in env_id, so a
// script reading .name got something that was not a name.
func TestEnvListJSON_TheServicesKeyIsCalledServices(t *testing.T) {
	t.Parallel()
	doc, err := json.Marshal(cli.EnvJSON{EnvID: "demo", Kind: "environment", Services: "af-proxy, web"})
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(doc, &got))
	require.Equal(t, "af-proxy, web", got["services"])
	require.NotContains(t, got, "name")
}
