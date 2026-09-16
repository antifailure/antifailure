package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// af ci stamps its throwaway environment with the run's own budget plus a
// grace, not the manifest's day-long default, so a CRASHED run, where the
// deferred teardown never fired, is collected by the reaper within the hour
// rather than the next day. These pin the arithmetic that decides how long a
// leaked run env sits before collection.

func manifestWithTTL(ttl string) *schema.Manifest {
	return &schema.Manifest{Runtime: &schema.Runtime{TTL: ttl}}
}

func TestCIRunTTL_IsTheDeadlinePlusGrace(t *testing.T) {
	// The ordinary run: --timeout is the deadline the run will not exceed, so
	// the environment is bounded to it plus the grace. Well below the 24h the
	// manifest allows, so no cap applies.
	got := ciRunTTL(30*time.Minute, manifestWithTTL("24h"))
	require.Equal(t, 30*time.Minute+ciRunGrace, got,
		"the lifetime is the run's deadline plus the grace")
}

func TestCIRunTTL_ADisabledDeadlineFallsBackToABoundedBudget(t *testing.T) {
	// --timeout 0 switches the deadline off. The environment is still bounded,
	// from the fallback budget, because the alternative is the day-long default
	// and a day of paying for a crash.
	got := ciRunTTL(0, manifestWithTTL("24h"))
	require.Equal(t, ciFallbackBudget+ciRunGrace, got,
		"a run with no deadline still gets a bounded lifetime")
}

func TestCIRunTTL_NeverExceedsTheManifestLifetime(t *testing.T) {
	// A repository that already chose a lifetime below budget+grace meant it. A
	// throwaway run has no business outliving a real environment on the same
	// machine, so the manifest's lifetime is the ceiling.
	got := ciRunTTL(30*time.Minute, manifestWithTTL("45m"))
	require.Equal(t, 45*time.Minute, got,
		"the budget-derived lifetime is capped at the manifest's own lifetime")
}

func TestCIRunTTL_NoManifestLifetimeIsNoCeiling(t *testing.T) {
	// A drafted or thin manifest with no runtime block states no lifetime to
	// cap against. The safe reading is to leave the budget-derived lifetime in
	// force rather than lengthen it to no lifetime at all.
	got := ciRunTTL(30*time.Minute, &schema.Manifest{})
	require.Equal(t, 30*time.Minute+ciRunGrace, got,
		"a manifest with no lifetime imposes no ceiling and does not lengthen the budget")
}

func TestManifestTTL_ReadsAPositiveLifetimeAndRejectsTheRest(t *testing.T) {
	require.Equal(t, 24*time.Hour, manifestTTL(manifestWithTTL("24h")))
	require.Zero(t, manifestTTL(manifestWithTTL("")), "an empty ttl is no ceiling")
	require.Zero(t, manifestTTL(manifestWithTTL("0h")), "a zero ttl is no ceiling")
	require.Zero(t, manifestTTL(nil), "no manifest is no ceiling")
	require.Zero(t, manifestTTL(&schema.Manifest{}), "no runtime block is no ceiling")
}
