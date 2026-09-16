package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/security"
	"github.com/antifailure/antifailure/engine/internal/security/authz"
)

// reachableTwin is a twin that answers, so the family's reachability control
// passes and the test exercises the authenticated differential rather than a
// blocked run. It carries no planted content, so the anonymous path finds
// nothing and the only findings are the authenticated ones under test.
func reachableTwin(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// liveInput is the input a run hands the family: a reachable twin, a golden that
// planted a canary, the fail policy, no anonymous targets, and the runner's
// observations attached exactly as the collector attaches them.
func liveInput(baseURL string, obs []security.RawObservation) security.Input {
	in := security.Input{
		Env:    security.Environment{BaseURL: baseURL},
		Golden: goldenWith(probeMarker),
		Policy: failPolicy(),
	}
	return in.WithRunArtifacts(security.RunArtifacts{Observations: obs})
}

// The headline: persona alice reached persona bob's order and the planted victim
// content came back, so the authenticated differential fires idor END TO END
// through the real caller, Probe -> BuildSnapshot -> AssessDetailed. This is the
// path that was dead: in.Observations() had no reader, and this proves it now
// has one that fires.
func TestProbe_AuthenticatedIDORFiresLive(t *testing.T) {
	t.Parallel()
	srv := reachableTwin(t)
	got, err := authz.New().Probe(context.Background(), liveInput(srv.URL, []security.RawObservation{crossUser()}))
	require.NoError(t, err)
	require.Len(t, got, 1, "a cross-user reach that returned the victim's content is an idor")
	require.Equal(t, authz.RuleIDOR, got[0].Rule)
	require.Equal(t, "/api/orders/{id}", got[0].Where)
	require.NotContains(t, got[0].Detail, "bob", "the finding names a class, never an identity value")
	require.NotContains(t, got[0].Detail, probeMarker, "the finding never carries the planted value")
}

// A cross-tenant reach fires on the higher-severity key, so tenant isolation is
// governable apart from the same-tenant idor.
func TestProbe_CrossTenantFiresLive(t *testing.T) {
	t.Parallel()
	srv := reachableTwin(t)
	xt := crossUser()
	xt.OwnerTenant = "org_b" // a different tenant owns the object
	got, err := authz.New().Probe(context.Background(), liveInput(srv.URL, []security.RawObservation{xt}))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, authz.RuleCrossTenant, got[0].Rule)
}

// Observations nil is NOT MEASURED: the authenticated classes must not read as a
// clean pass, and the anonymous path (with no targets here) contributes nothing,
// so the family emits no authenticated finding and never a false green. It also
// must not error, because erroring would block the working anonymous reach.
func TestProbe_NilObservationsFailsClosedNoFalsePass(t *testing.T) {
	t.Parallel()
	srv := reachableTwin(t)
	in := security.Input{
		Env:    security.Environment{BaseURL: srv.URL},
		Golden: goldenWith(probeMarker),
		Policy: failPolicy(),
	} // no RunArtifacts attached: in.Observations() is nil
	got, err := authz.New().Probe(context.Background(), in)
	require.NoError(t, err, "missing observations must not block the working anonymous path")
	require.Empty(t, got, "not measured must not emit an authenticated finding as a pass")
}

// A boundary the candidate refused, with a live liveness arm (the object was
// seeded so the owner can read it), is the control working: no finding. This is
// the false-positive control that keeps a correctly isolated endpoint from
// red-ing the moment somebody touches it.
func TestProbe_DeniedBoundaryWithLiveArmDoesNotFire(t *testing.T) {
	t.Parallel()
	srv := reachableTwin(t)
	denied := crossUser()
	denied.Status = 404 // the row was dropped for a non-owner
	denied.VictimContentPresent = false
	denied.SetupConfirmed = true // but the object WAS seeded, so the arm is live
	got, err := authz.New().Probe(context.Background(), liveInput(srv.URL, []security.RawObservation{denied}))
	require.NoError(t, err)
	require.Empty(t, got, "a refusal with a live arm is the boundary holding")
}

// A base twin that already allowed the reach makes it a pre-existing exposure,
// not this change's regression, so it is suppressed. Without the base twin the
// same candidate leak fires, which the idor test proves; here the base is present
// and agrees, so nothing is emitted.
func TestProbe_BaseTwinSuppressesPreExisting(t *testing.T) {
	t.Parallel()
	srv := reachableTwin(t)
	in := security.Input{
		Env:    security.Environment{BaseURL: srv.URL},
		Golden: goldenWith(probeMarker),
		Policy: failPolicy(),
	}
	in = in.WithRunArtifacts(security.RunArtifacts{
		Observations: []security.RawObservation{crossUser()},
		Baseline:     &security.Baseline{Observations: []security.RawObservation{crossUser()}},
	})
	got, err := authz.New().Probe(context.Background(), in)
	require.NoError(t, err)
	require.Empty(t, got, "a reach the base already allowed is pre-existing, not a regression")
}

// A leak with NO usable canary in the golden cannot be trusted: the runner's
// content-presence flag had no marker to have matched, so the detector is not
// live and the differential goes inconclusive rather than firing. Crucially it
// does NOT read the leak as absent (a clean pass) either: it emits no finding
// because it could not decide, which is the fail-closed the liveness discipline
// requires. A build is red only on a proven finding, so an inconclusive here is
// correctly not a false green and not a false red.
func TestProbe_DetectorNotLiveDoesNotFalselyPassOrFire(t *testing.T) {
	t.Parallel()
	srv := reachableTwin(t)
	in := security.Input{
		Env:    security.Environment{BaseURL: srv.URL},
		Golden: security.GoldenView{}, // no canary planted
		Policy: failPolicy(),
	}
	in = in.WithRunArtifacts(security.RunArtifacts{Observations: []security.RawObservation{crossUser()}})
	got, err := authz.New().Probe(context.Background(), in)
	require.NoError(t, err)
	require.Empty(t, got, "with no proven detector the differential is inconclusive, never a fired finding")
}
