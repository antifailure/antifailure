package env_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// noEnv is a shell with nothing exported, which is what a fresh checkout is.
func noEnv(string) string { return "" }

func githubRules() []schema.EgressRule {
	return []schema.EgressRule{{Host: "api.github.com", Mode: "block", WebhookPath: "/webhooks/github"}}
}

func sourceNames(t *testing.T, sources []secrets.Source) []string {
	t.Helper()
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		out = append(out, s.Name())
	}
	return out
}

func TestEnvironmentSources_OffersAnAppKeyToAManifestRehearsingAnApp(t *testing.T) {
	t.Parallel()
	// The condition is one rule rather than two, because the App id, the key
	// and the webhook secret are three halves of one credential and an
	// environment that has some of them is refused by the application.
	sources := env.EnvironmentSources(githubRules(), "envid", noEnv)
	require.Contains(t, sourceNames(t, sources), secrets.GitHubAppIdentitySourceName)

	value, found, err := lookup(t, sources, secrets.GitHubAppPrivateKeyEnv)
	require.NoError(t, err)
	require.True(t, found, "a manifest rehearsing a GitHub App was offered no key")
	require.Contains(t, value, "PRIVATE KEY", "the value is not a key")
}

func TestEnvironmentSources_OffersNothingToAManifestThatRehearsesNoApp(t *testing.T) {
	t.Parallel()
	// Generating a key for an environment that never asks is the cost this is
	// shaped to avoid, and a source in the "Looked in" list that could never
	// have answered sends somebody to configure a place that does not exist.
	for _, rules := range [][]schema.EgressRule{
		nil,
		{{Host: "api.stripe.com", Mode: "block", WebhookPath: "/webhooks/stripe"}},
		// A GitHub rule with no webhook path is not a rehearsal of the App: no
		// delivery reaches the service, so there is nothing for the App to be.
		{{Host: "api.github.com", Mode: "block"}},
	} {
		sources := env.EnvironmentSources(rules, "envid", noEnv)
		require.NotContains(t, sourceNames(t, sources), secrets.GitHubAppIdentitySourceName)
	}
}

func TestEnvironmentSources_StandsAsideForAKeySomebodyExported(t *testing.T) {
	t.Parallel()
	// Somebody who exported one is rehearsing against an App they really
	// registered, and generating over the top of that would hand their
	// application an identity GitHub has never heard of.
	//
	// It stands aside rather than copying the value, so the shell answers and
	// af explain names the shell. A source that copied the value would be
	// reported as having generated it, which is a small lie about where a
	// credential came from.
	getenv := func(name string) string {
		if name == secrets.GitHubAppPrivateKeyEnv {
			return "a key somebody exported"
		}
		return ""
	}
	sources := env.EnvironmentSources(githubRules(), "envid", getenv)
	require.NotContains(t, sourceNames(t, sources), secrets.GitHubAppIdentitySourceName)

	// And the signing secret is still offered, so standing aside for one value
	// did not stand aside for the other.
	require.Contains(t, sourceNames(t, sources), "the environment's webhook signing secrets")
}

func TestEnvironmentSources_StillCarriesTheSigningSecrets(t *testing.T) {
	t.Parallel()
	// The two halves of the same App come from the same call, and this is what
	// says the identity did not arrive by displacing the secret.
	sources := env.EnvironmentSources(githubRules(), "envid", noEnv)
	value, found, err := lookup(t, sources, "GITHUB_WEBHOOK_SECRET")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEmpty(t, value)
}

// TestEnvironmentSources_ThisRepositorysOwnManifestResolves is the end to end
// half, against the file the failure was found in.
//
// antifailure.yaml declared AF_GITHUB_APP_PRIVATE_KEY with a literal value: a
// 2048 bit RSA key, in a public repository, in the one file this product holds
// up as an ordinary manifest. It now declares `from`, and nothing supplies that
// name unless this path does. So the assertion is not that the manifest parses,
// which it did before and would again with the variable resolving nowhere. It
// is that the variable is answered, by this source, with a key.
func TestEnvironmentSources_ThisRepositorysOwnManifestResolves(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "antifailure.yaml"))
	require.NoError(t, err)
	m, err := manifest.Parse(body, filepath.Join(root, "antifailure.yaml"), "")
	require.NoError(t, err, "this repository's own manifest does not parse")

	var declared *schema.EnvVar
	for _, s := range m.Services {
		for i, e := range s.Env {
			if e.Name == "AF_GITHUB_APP_PRIVATE_KEY" {
				declared = &s.Env[i]
			}
		}
	}
	require.NotNil(t, declared, "the manifest no longer declares the App key at all")
	require.Empty(t, declared.Value, "the key is a literal in a committed file again")
	require.Equal(t, secrets.GitHubAppPrivateKeyEnv, declared.From)

	require.NotNil(t, m.Egress)
	sources := env.EnvironmentSources(m.Egress.Rules, "envid", noEnv)
	value, found, err := lookup(t, sources, declared.From)
	require.NoError(t, err)
	require.True(t, found, "nothing supplies %s, so af up would stop at AF-SEC-001", declared.From)
	require.Contains(t, value, "PRIVATE KEY")
}

// lookup asks each source in order, the way the chain does.
func lookup(t *testing.T, sources []secrets.Source, name string) (string, bool, error) {
	t.Helper()
	for _, s := range sources {
		v, found, err := s.Lookup(context.Background(), name)
		if err != nil {
			return "", false, err
		}
		if found {
			return v.Reveal(), true, nil
		}
	}
	return "", false, nil
}

// repositoryRoot walks up to the directory holding the manifest.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "antifailure.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "walked to the filesystem root without finding antifailure.yaml")
		dir = parent
	}
	t.Fatal("antifailure.yaml is not within ten directories of this test file")
	return ""
}
