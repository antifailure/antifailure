package env

import (
	"testing"
	"time"

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
			o := &Orchestrator{opts: Options{Manifest: tc.manifest}}
			got, ok := o.ttlSeconds()
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

// The number the control plane fills expires_at from and the label the reaper
// destroys on are the same duration, read once. Two parses of runtime.ttl
// would agree until somebody changed one, and the disagreement would be a
// console saying an environment lives until Friday and a reaper taking it on
// Thursday.
func TestTTLSeconds_IsTheSameLifetimeTheReaperEnforces(t *testing.T) {
	o := &Orchestrator{opts: Options{
		Manifest: &schema.Manifest{Runtime: &schema.Runtime{TTL: "36h"}},
	}}

	secs, ok := o.ttlSeconds()

	require.True(t, ok)
	require.Equal(t, o.ttl().Seconds(), secs)
	require.Equal(t, float64(36*3600), secs)
}

func TestStartedField_IsTheInstantTheWorkBeganAndNotWhenTheEventFired(t *testing.T) {
	// The two are separated by the whole build on the ready event, and the
	// control plane bills from this one. RFC3339 with nanoseconds and in UTC,
	// because the receiver puts it straight into a timestamptz and a local
	// offset there is an hour of somebody's bill.
	at := time.Date(2026, 3, 4, 5, 6, 7, 891011121, time.FixedZone("somewhere", 5*3600))

	f := startedField(at)

	require.Equal(t, "started_at", f.Key)
	require.Equal(t, "2026-03-04T00:06:07.891011121Z", f.Value)
}
