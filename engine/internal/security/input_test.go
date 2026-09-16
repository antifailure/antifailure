package security_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/security"
)

func TestInput_ReadersAreAbsentUntilTheRouterAttachesThem(t *testing.T) {
	t.Parallel()
	// An Input built the way every existing constructor builds one, with the
	// exported fields only, exposes each reader as absent. This is what keeps
	// the enrichment additive: the merged spine and read_security_findings
	// construct Input without the readers and keep compiling and passing.
	var in security.Input
	require.Nil(t, in.Decisions(), "an unattached decision log reads as absent, not empty")
	require.Nil(t, in.Messages(), "an unattached message log reads as absent")
	require.Nil(t, in.Observations(), "unattached observations read as absent, and authz fails closed")
	require.Nil(t, in.Evidence().DOM, "unattached evidence reads as absent")
	require.Nil(t, in.Routes(), "no observed-route source is UNAVAILABLE, which is nil not empty")
	_, ok := in.Baseline()
	require.False(t, ok, "no base twin was measured, so Baseline reports ok=false")
}

func TestInput_WithRunArtifactsAttachesEachReader(t *testing.T) {
	t.Parallel()
	decisions := []local.Decision{{Host: "api.internal"}}
	messages := []local.Message{{Kind: "email"}}
	observations := []security.RawObservation{{
		Route: "/api/orders/{id}", Method: "GET", ObjectClass: "another tenant's order",
		VictimContentPresent: true, SetupConfirmed: true,
	}}
	evidence := security.Evidence{DOM: []string{"<html>"}, Responses: []string{"{}"}}
	baseline := &security.Baseline{
		Decisions:    []local.Decision{{Host: "base.host"}},
		Messages:     []local.Message{{Kind: "sms"}},
		Observations: []security.RawObservation{{Route: "/base"}},
	}
	routes := []security.Route{{Method: "GET", Path: "/api/orders/{id}", Params: []string{"id"}}}

	in := security.Input{}.WithRunArtifacts(security.RunArtifacts{
		Decisions: decisions, Messages: messages, Observations: observations,
		Evidence: evidence, Baseline: baseline, Routes: routes,
	})

	require.Equal(t, decisions, in.Decisions())
	require.Equal(t, messages, in.Messages())
	require.Equal(t, observations, in.Observations(), "the candidate observations reach the family")
	require.Equal(t, evidence, in.Evidence(), "the browser evidence reaches the family")
	got, ok := in.Baseline()
	require.True(t, ok, "a base twin was attached, so Baseline reports ok=true")
	require.Equal(t, "base.host", got.Decisions[0].Host)
	require.Equal(t, "sms", got.Messages[0].Kind)
	require.Equal(t, "/base", got.Observations[0].Route, "authz reads the base twin's observations")
	require.Equal(t, routes, in.Routes())
}

func TestInput_RoutesDistinguishesEmptyFromUnavailable(t *testing.T) {
	t.Parallel()
	// An EMPTY non-nil slice is "the run reached routes but none this change
	// touched are worth fuzzing", a legitimate pass. NIL is "no source was
	// wired", which the injection family reports as blocked. The two must not
	// collapse, so the accessor preserves the distinction the router sets.
	empty := security.Input{}.WithRunArtifacts(security.RunArtifacts{Routes: []security.Route{}})
	require.NotNil(t, empty.Routes(), "an empty non-nil slice means nothing to fuzz, a pass")
	require.Len(t, empty.Routes(), 0)

	unavailable := security.Input{}.WithRunArtifacts(security.RunArtifacts{Routes: nil})
	require.Nil(t, unavailable.Routes(), "nil means UNAVAILABLE, which injection reports as blocked")
}

func TestInput_BaselineZeroValueIsNotABaseOfZero(t *testing.T) {
	t.Parallel()
	// The banned defect: an absent baseline read as a base of zero. Baseline
	// returns a bool precisely so a reader cannot mistake "did not measure" for
	// "the base did nothing". A nil baseline pointer reports ok=false and the
	// returned struct is meaningless.
	in := security.Input{}.WithRunArtifacts(security.RunArtifacts{Baseline: nil})
	_, ok := in.Baseline()
	require.False(t, ok)
}
