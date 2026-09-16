package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// crossUserObs is one exploration observation: persona alice reached persona
// bob's order across a user boundary inside org_a and the planted canary came
// back. It is the producer-side shape the runner emits.
func crossUserObs() explore.Observation {
	return explore.Observation{
		Route: "/api/orders/{id}", Method: "GET",
		ActorTenant: "org_a", ActorUser: "alice", ActorRole: "member",
		OwnerTenant: "org_a", OwnerUser: "bob", OwnerRole: "member",
		ObjectClass: "another user's order", Status: 200,
		VictimContentPresent: true, SetupConfirmed: true,
	}
}

// observationsFrom is nil when nothing measured, so authz fails closed rather
// than reading the absence as no violation.
func TestObservationsFrom_NilWhenNothingMeasured(t *testing.T) {
	require.Nil(t, observationsFrom(nil), "no exploration is not measured")
	require.Nil(t, observationsFrom(&report.Exploration{}), "an exploration that recorded nothing folds to nil")
	// An exploration that ran but made no authorization reading also folds to nil.
	var ex explore.Exploration
	ex.Evidence.DOM = []string{"<html></html>"}
	require.Nil(t, observationsFrom(&report.Exploration{Results: []explore.Exploration{ex}}),
		"an exploration with evidence but no observations still folds to nil")
}

// observationsFrom folds every exploration's readings into one flat slice, in a
// faithful field-for-field mapping, so the authz differential reads exactly what
// the runner emitted.
func TestObservationsFrom_FoldsEveryReading(t *testing.T) {
	var a, b explore.Exploration
	a.Observations = []explore.Observation{crossUserObs()}
	other := crossUserObs()
	other.Route = "/api/invoices/{id}"
	other.OwnerTenant = "org_b" // a cross-tenant reach
	b.Observations = []explore.Observation{other}

	got := observationsFrom(&report.Exploration{Results: []explore.Exploration{a, b}})
	require.Len(t, got, 2, "both explorations' readings are folded")
	require.Equal(t, security.RawObservation{
		Route: "/api/orders/{id}", Method: "GET",
		ActorTenant: "org_a", ActorUser: "alice", ActorRole: "member",
		OwnerTenant: "org_a", OwnerUser: "bob", OwnerRole: "member",
		ObjectClass: "another user's order", Status: 200,
		VictimContentPresent: true, SetupConfirmed: true,
	}, got[0], "the reading maps field for field, no value invented or dropped")
	require.Equal(t, "org_b", got[1].OwnerTenant, "the second exploration's cross-tenant reach is preserved")
}

// The collector wires observationsFrom to the family Input, so a family reads the
// runner's observations through in.Observations(). This proves the producer half
// reaches the consumer's accessor: the wire at security.go is effective, not
// merely defined.
func TestSecurityFindings_ObservationsReachTheFamily(t *testing.T) {
	fam := &spyFamily{name: "authz", surfaces: authzSpy().surfaces, checks: authzSpy().checks, keys: authzSpy().keys}
	reg := security.NewRegistry()
	reg.Register(fam)

	var ex explore.Exploration
	ex.Observations = []explore.Observation{crossUserObs()}
	run := report.Run{URL: "http://twin.local", Exploration: &report.Exploration{
		Results: []explore.Exploration{ex},
	}}

	securityFindings(context.Background(), testEnv(), &fakeReader{profile: codeProfile()},
		reg, report.Configure(nil), &run, nil, nil, "", "", 0)

	got := fam.gotInput.Observations()
	require.Len(t, got, 1, "the exploration's observation reached the family through in.Observations()")
	require.Equal(t, "/api/orders/{id}", got[0].Route)
	require.Equal(t, "bob", got[0].OwnerUser, "the owning identity crossed the wire intact")
	require.True(t, got[0].VictimContentPresent, "the content-presence verdict crossed intact")
}
