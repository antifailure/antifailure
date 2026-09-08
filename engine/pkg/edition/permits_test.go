package edition_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/edition"
)

// The gate the engine applies, and the direction it fails in.
//
// A licence check has one interesting property and it is not that it says yes.
// It is that everything which is not a positive grant says no: a community
// build, a build that forgot to attach anything, an expired licence whose
// features list is empty, and a licence for somebody else. Each of those has a
// separate test here because each is a separate way for the check to be
// bypassed, and a single test on a context with no edition would pass while
// three of them were broken.

func TestPermits_SaysNoWhenNothingIsAttached(t *testing.T) {
	t.Parallel()
	// The community binary attaches nothing at all, and this is the whole of
	// why it gets no enterprise behaviour.
	require.False(t, edition.Permits(context.Background(), edition.FeatureMultiRuntime))
}

func TestPermits_SaysNoForAStatusThatCarriesNoFeatures(t *testing.T) {
	t.Parallel()
	// An expired licence. The enterprise binary attaches a status describing
	// what happened and an empty feature list, because Enabled is asked per
	// feature rather than copied from the claims: a lapsed licence lists what
	// was bought and permits none of it.
	ctx := edition.With(context.Background(), edition.Status{
		Name: "enterprise", State: "expired",
		Message: "The license is not being honoured.",
	})
	require.False(t, edition.Permits(ctx, edition.FeatureMultiRuntime))
}

func TestPermits_SaysNoForAStatusCarryingEveryOtherFeature(t *testing.T) {
	t.Parallel()
	// The case that separates a real check from one that returns true for any
	// licence at all. Eleven of the twelve are on and the answer is still no.
	ctx := edition.With(context.Background(), edition.Status{
		Name: "enterprise", State: "active",
		Features: []string{
			"sso", "scim", "rbac", "audit_stream", "policy_enforcement",
			"enterprise_secrets", "billing", "enterprise_dashboard",
			"support_access", "compliance_packs", "air_gapped",
		},
	})
	require.False(t, edition.Permits(ctx, edition.FeatureMultiRuntime))
}

func TestPermits_SaysYesForTheFeatureTheLicenceCarries(t *testing.T) {
	t.Parallel()
	// The control. Without it every test above would still pass against a
	// function that always answered no, which is a gate nobody can open.
	ctx := edition.With(context.Background(), edition.Status{
		Name: "enterprise", State: "active",
		Features: []string{"sso", edition.FeatureMultiRuntime},
	})
	require.True(t, edition.Permits(ctx, edition.FeatureMultiRuntime))
}

func TestPermits_IsAskedOfTheStatusDirectlyToo(t *testing.T) {
	t.Parallel()
	// af license status has already read the value and has no context to ask.
	// Two entry points to one answer, so that a caller with a status in hand
	// does not write the loop again and write it differently.
	s := edition.Status{Features: []string{edition.FeatureMultiRuntime}}
	require.True(t, s.Permits(edition.FeatureMultiRuntime))
	require.False(t, s.Permits("air_gapped"))
	require.False(t, edition.Status{}.Permits(edition.FeatureMultiRuntime))
}
