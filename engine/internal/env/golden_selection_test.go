package env_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// A repository holds more than one manifest, and they share one golden store.
//
// The regression: `af up` took the newest verified golden and nothing else, so
// bringing the control plane up branched a golden an example had refreshed
// thirty seconds earlier. The environment came up with another project's
// schema and another project's masked data in it. Copying one project's data
// into another project's preview is the failure this product exists to
// prevent, and it took one line of selection logic to cause.
func TestPickGolden_RefusesAGoldenMadeUnderOtherRules(t *testing.T) {
	goldens := []provider.GoldenVersion{
		{ID: "gv_2_store", Verified: true, RulesHash: "store111"},
		{ID: "gv_1_control", Verified: true, RulesHash: "ctrl2222"},
	}
	got, refused := env.PickGoldenForTest(goldens, "ctrl2222")
	require.Equal(t, "gv_1_control", got,
		"the newest golden belongs to another manifest and must not be branched here")
	require.Equal(t, 1, refused)
}

// An unverified version is never branched, whatever its rules say. That rule
// predates this one and is the product's central promise.
func TestPickGolden_NeverBranchesAnUnverifiedVersion(t *testing.T) {
	goldens := []provider.GoldenVersion{
		{ID: "gv_new", Verified: false, RulesHash: "ctrl2222"},
		{ID: "gv_old", Verified: true, RulesHash: "ctrl2222"},
	}
	got, _ := env.PickGoldenForTest(goldens, "ctrl2222")
	require.Equal(t, "gv_old", got)
}

// Nothing usable, and the count says why.
//
// Six goldens in the store and none of them usable here is a different
// situation from an empty store, and the caller says something different
// about each.
func TestPickGolden_SaysHowManyItRefused(t *testing.T) {
	goldens := []provider.GoldenVersion{
		{ID: "a", Verified: true, RulesHash: "other111"},
		{ID: "b", Verified: true, RulesHash: "other222"},
		{ID: "c", Verified: false, RulesHash: "ctrl2222"},
	}
	got, refused := env.PickGoldenForTest(goldens, "ctrl2222")
	require.Empty(t, got)
	require.Equal(t, 2, refused, "an unverified version is not a refusal about rules")
}

// A version that records no rules at all is refused.
//
// The first version of this accepted one, reasoning that a missing record
// should not break a machine that already had goldens on it. That reasoning
// was wrong and it cost a run: with the lenient rule in place, bringing the
// control plane up branched an empty golden the masking test suite had
// published minutes earlier, and the environment came up with a schema and no
// data in it. A golden whose provenance is unknown cannot be shown to match
// this manifest, and refusing costs a refresh where accepting costs somebody a
// preview built on the wrong data.
func TestPickGolden_RefusesAVersionThatRecordsNoRules(t *testing.T) {
	got, refused := env.PickGoldenForTest(
		[]provider.GoldenVersion{{ID: "gv_unknown", Verified: true}}, "ctrl2222")
	require.Empty(t, got)
	require.Equal(t, 1, refused)
}

// The observed case, exactly: a newer golden from another suite, with nothing
// recorded about what made it.
func TestPickGolden_PrefersTheMatchOverANewerUnknown(t *testing.T) {
	got, _ := env.PickGoldenForTest([]provider.GoldenVersion{
		{ID: "gv_20260829173008_278879ab", Verified: true},
		{ID: "gv_20260829163911_710c39a7", Verified: true, RulesHash: "ctrl2222"},
	}, "ctrl2222")
	require.Equal(t, "gv_20260829163911_710c39a7", got)
}
