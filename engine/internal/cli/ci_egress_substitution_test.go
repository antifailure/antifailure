package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// TestSummariseEgress_CountsTheCredentialThatWasNotReplaced closes the gap
// between what the decision log records and what the report can say.
//
// The two decisions below are identical in every column a reader sees: both
// allowed, both mode sandbox, both matched by the same rule. One had its
// credential replaced on the way out and one did not, because the sidecar
// substitutes only when a value was configured for the rule's credential name.
// Without this count the report says "2 allowed" and cannot say that half of
// those carried the application's own credential to the provider.
func TestSummariseEgress_CountsTheCredentialThatWasNotReplaced(t *testing.T) {
	t.Parallel()
	got := summariseEgress([]local.Decision{
		{Host: "api.stripe.com", Mode: "sandbox", Rule: "api.stripe.com",
			Allowed: true, Substituted: true},
		{Host: "api.stripe.com", Mode: "sandbox", Rule: "api.stripe.com",
			Allowed: true, Substituted: false},
	})

	require.Equal(t, 2, got.Allowed, "both still count as allowed out")
	require.Equal(t, 2, got.Sandbox)
	require.Equal(t, 1, got.Substituted)
	require.Equal(t, 1, got.Unsubstituted)
	require.Equal(t, []string{"api.stripe.com"}, got.UnsubstitutedHosts,
		"one provider called twice is named once")
}

func TestSummariseEgress_NamesEveryHostThatLeakedOnce(t *testing.T) {
	t.Parallel()
	got := summariseEgress([]local.Decision{
		{Host: "api.twilio.com", Mode: "sandbox", Rule: "api.twilio.com", Allowed: true},
		{Host: "api.stripe.com", Mode: "sandbox", Rule: "api.stripe.com", Allowed: true},
		{Host: "api.stripe.com", Mode: "sandbox", Rule: "api.stripe.com", Allowed: true},
	})

	require.Equal(t, 3, got.Unsubstituted)
	// Sorted, so a report diffed between two runs does not churn on map order.
	require.Equal(t, []string{"api.stripe.com", "api.twilio.com"}, got.UnsubstitutedHosts)
}

func TestSummariseEgress_LeavesTheCountsAtZeroWithoutSandboxRules(t *testing.T) {
	t.Parallel()
	got := summariseEgress([]local.Decision{
		{Host: "api.example.com", Mode: "allow", Rule: "api.example.com", Allowed: true},
		{Host: "nobody.example", Mode: "block"},
	})

	require.Zero(t, got.Sandbox)
	require.Zero(t, got.Unsubstituted)
	require.Empty(t, got.UnsubstitutedHosts)
	require.Equal(t, []string{"nobody.example"}, got.Surprises,
		"the surprise detection this sits beside still works")
}
