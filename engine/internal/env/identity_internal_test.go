package env

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func TestIdentity_CarriesEverythingTheControlPlaneNeedsToCreateARow(t *testing.T) {
	o := &Orchestrator{opts: Options{
		Repository:  "antifailure/antifailure",
		Branch:      "feature/a-branch",
		PullRequest: 42,
		Manifest:    &schema.Manifest{Runtime: &schema.Runtime{TTL: "24h"}},
	}}

	got := fieldMap(t, o.identity())

	// repository and branch are the two NOT NULL columns. Without both of
	// them the control plane cannot insert the row at all, which was the
	// state of the world before this existed.
	require.Equal(t, "antifailure/antifailure", got["repository"])
	require.Equal(t, "feature/a-branch", got["branch"])
	require.Equal(t, 42, got["pull_request"])
	require.Equal(t, float64(24*3600), got["ttl_seconds"])
}

func TestIdentity_OmitsWhatItWasNotToldRatherThanSendingEmptyStrings(t *testing.T) {
	// Empty is a real state: a checkout with no remote, run outside CI. The
	// control plane distinguishes "not told" from "told nothing", and an empty
	// string here would be a repository literally named "" holding every
	// environment from every unconfigured machine.
	o := &Orchestrator{opts: Options{Branch: "main"}}

	got := fieldMap(t, o.identity())

	require.NotContains(t, got, "repository")
	require.NotContains(t, got, "pull_request")
	require.NotContains(t, got, "ttl_seconds")
	require.Equal(t, "main", got["branch"])
}

func TestTTLSeconds_TheManifestsSpellingsAndTheOnesThatMeanNoLifetime(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest *schema.Manifest
		want     float64
		ok       bool
	}{
		{"hours", &schema.Manifest{Runtime: &schema.Runtime{TTL: "168h"}}, 168 * 3600, true},
		{"days, which Go does not parse and the manifest does", &schema.Manifest{Runtime: &schema.Runtime{TTL: "7d"}}, 7 * 24 * 3600, true},
		{"no runtime block", &schema.Manifest{}, 0, false},
		{"no ttl", &schema.Manifest{Runtime: &schema.Runtime{}}, 0, false},
		{"no manifest, which is every command that inventories a machine", nil, 0, false},
		// Zero would be an environment that expired the instant it was
		// created, and a reaper that believed it would destroy live work.
		{"zero", &schema.Manifest{Runtime: &schema.Runtime{TTL: "0h"}}, 0, false},
		{"not a duration", &schema.Manifest{Runtime: &schema.Runtime{TTL: "soon"}}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ttlSeconds(tc.manifest)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}
