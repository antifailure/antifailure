package authz_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/security"
	"github.com/antifailure/antifailure/engine/internal/security/authz"
)

// crossUser is one reach by alice into bob's object inside org_a: a same-tenant
// cross-user reference, the idor shape, that returned the victim's content.
func crossUser() security.RawObservation {
	return security.RawObservation{
		Route: "/api/orders/{id}", Method: "GET",
		ActorTenant: "org_a", ActorUser: "alice", ActorRole: "member",
		OwnerTenant: "org_a", OwnerUser: "bob", OwnerRole: "member",
		ObjectClass: "another user's order", Status: 200,
		VictimContentPresent: true, SetupConfirmed: true,
	}
}

// A nil slice is NOT MEASURED and must map to a nil snapshot, so the caller reads
// it as "the authenticated classes were not exercised" rather than as a clean
// run. This is the whole distinction the family turns on.
func TestBuildSnapshot_NilIsNotMeasured(t *testing.T) {
	t.Parallel()
	require.Nil(t, authz.BuildSnapshot(nil, goldenWith(probeMarker)),
		"nil observations is not measured, which must be a nil snapshot")
}

// An empty non-nil slice is MEASURED with nothing to probe: reachable, no probes,
// a quiet legitimate pass rather than a blocked run.
func TestBuildSnapshot_EmptyIsMeasuredAndReachable(t *testing.T) {
	t.Parallel()
	snap := authz.BuildSnapshot([]security.RawObservation{}, goldenWith(probeMarker))
	require.NotNil(t, snap, "an empty non-nil slice is measured, so the snapshot is present")
	require.True(t, snap.Reachable, "the runner reached the twin to observe, so the run is reachable")
	require.Empty(t, snap.Probes, "no cross-owner reach means no probe")
}

// DetectorLive is carried from the golden: with a usable canary it is true, and
// with none it is false, so a golden that planted no marker cannot let an
// authenticated leak read as a decided absence.
func TestBuildSnapshot_DetectorLiveTracksTheGolden(t *testing.T) {
	t.Parallel()
	live := authz.BuildSnapshot([]security.RawObservation{crossUser()}, goldenWith(probeMarker))
	require.True(t, live.DetectorLive, "a golden with a canary proves the content matcher")

	none := authz.BuildSnapshot([]security.RawObservation{crossUser()}, security.GoldenView{})
	require.False(t, none.DetectorLive, "a golden with no canary cannot prove the matcher")
}

// Each identity comparison selects the class its severity demands, most severe
// first: a cross-tenant reach outranks the cross-user one it also is.
func TestBuildSnapshot_ClassifiesByIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		obs  security.RawObservation
		want authz.Class
	}{
		{
			name: "anonymous",
			obs:  security.RawObservation{Route: "/a", Method: "GET", Anonymous: true, Status: 200, VictimContentPresent: true, SetupConfirmed: true},
			want: authz.ClassUnauthenticated,
		},
		{
			name: "cross tenant outranks cross user",
			obs: security.RawObservation{Route: "/a", Method: "GET",
				ActorTenant: "org_a", ActorUser: "alice", OwnerTenant: "org_b", OwnerUser: "bob",
				Status: 200, VictimContentPresent: true, SetupConfirmed: true},
			want: authz.ClassCrossTenant,
		},
		{
			name: "same tenant different user is idor",
			obs:  crossUser(),
			want: authz.ClassIDOR,
		},
		{
			name: "same tenant and user different role is privilege escalation",
			obs: security.RawObservation{Route: "/a", Method: "GET",
				ActorTenant: "org_a", ActorUser: "alice", ActorRole: "member",
				OwnerTenant: "org_a", OwnerUser: "alice", OwnerRole: "admin",
				Status: 200, VictimContentPresent: true, SetupConfirmed: true},
			want: authz.ClassPrivilegeEscalation,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := authz.BuildSnapshot([]security.RawObservation{c.obs}, goldenWith(probeMarker))
			require.Len(t, snap.Probes, 1, "%s: one boundary crossed is one probe", c.name)
			require.Equal(t, c.want, snap.Probes[0].Class, c.name)
		})
	}
}

// A reach where the actor owns the object crosses no boundary, so it raises no
// probe. Manufacturing one would invent a question the reach never asked.
func TestBuildSnapshot_SelfAccessRaisesNoProbe(t *testing.T) {
	t.Parallel()
	self := security.RawObservation{Route: "/api/orders/{id}", Method: "GET",
		ActorTenant: "org_a", ActorUser: "alice", ActorRole: "member",
		OwnerTenant: "org_a", OwnerUser: "alice", OwnerRole: "member",
		Status: 200, VictimContentPresent: true, SetupConfirmed: true}
	snap := authz.BuildSnapshot([]security.RawObservation{self}, goldenWith(probeMarker))
	require.NotNil(t, snap)
	require.Empty(t, snap.Probes, "a self-read crosses no boundary")
}

// The probe's location and method carry through unchanged, and the object class
// label rides along for the finding, so the finding points where the reach
// happened.
func TestBuildSnapshot_ProbeCarriesLocation(t *testing.T) {
	t.Parallel()
	snap := authz.BuildSnapshot([]security.RawObservation{crossUser()}, goldenWith(probeMarker))
	require.Len(t, snap.Probes, 1)
	p := snap.Probes[0]
	require.Equal(t, "/api/orders/{id}", p.Where)
	require.Equal(t, "GET", p.Method)
	require.Equal(t, "another user's order", p.ObjectClass)
}

// The arm reads the twin's answer as the actor outcome and the object's seeding
// as the liveness. A present 200 is allowed; a confirmed object arms the liveness
// allowed; an unconfirmed one leaves it denied and unarmed.
func TestBuildSnapshot_ArmFromOutcomeAndSeeding(t *testing.T) {
	t.Parallel()
	snap := authz.BuildSnapshot([]security.RawObservation{crossUser()}, goldenWith(probeMarker))
	arm := snap.Observations[snap.Probes[0].ID].Arm
	require.Equal(t, authz.OutcomeAllowed, arm.Actor, "a present 200 is a leak")
	require.Equal(t, authz.OutcomeAllowed, arm.Liveness, "a seeded object is one the owner can read")
	require.True(t, arm.LivenessArmed, "a confirmed seeding arms the liveness")

	unseeded := crossUser()
	unseeded.SetupConfirmed = false
	unseeded.VictimContentPresent = false
	unseeded.Status = 404
	snap2 := authz.BuildSnapshot([]security.RawObservation{unseeded}, goldenWith(probeMarker))
	arm2 := snap2.Observations[snap2.Probes[0].ID].Arm
	require.Equal(t, authz.OutcomeDenied, arm2.Actor, "a 404 with no victim content is a refusal")
	require.Equal(t, authz.OutcomeDenied, arm2.Liveness, "an unconfirmed object leaves the liveness denied")
	require.False(t, arm2.LivenessArmed, "an unconfirmed seeding does not arm the liveness")
}

// A 200 without the victim's content is a refusal, which is how row level
// security drops a row. Keying on content presence rather than the status is the
// family's load-bearing rule, so the arm must read denied here.
func TestBuildSnapshot_ContentPresenceDecidesTheOutcome(t *testing.T) {
	t.Parallel()
	rls := crossUser()
	rls.Status = 200
	rls.VictimContentPresent = false // the row was dropped: an empty 200
	snap := authz.BuildSnapshot([]security.RawObservation{rls}, goldenWith(probeMarker))
	require.Equal(t, authz.OutcomeDenied, snap.Observations[snap.Probes[0].ID].Arm.Actor,
		"a 200 with the victim content absent is the boundary holding, not a leak")
}

// Two identical reaches collapse to one probe, keyed by the stable id, so a
// duplicate does not double-count a question.
func TestBuildSnapshot_DeduplicatesByID(t *testing.T) {
	t.Parallel()
	snap := authz.BuildSnapshot([]security.RawObservation{crossUser(), crossUser()}, goldenWith(probeMarker))
	require.Len(t, snap.Probes, 1, "the same reach twice is one probe")
}

// Probes come back in a stable order regardless of input order, so the two twins
// line up and the findings are deterministic.
func TestBuildSnapshot_StableProbeOrder(t *testing.T) {
	t.Parallel()
	a := crossUser()
	a.Route = "/api/a"
	b := crossUser()
	b.Route = "/api/b"
	forward := authz.BuildSnapshot([]security.RawObservation{a, b}, goldenWith(probeMarker))
	reverse := authz.BuildSnapshot([]security.RawObservation{b, a}, goldenWith(probeMarker))
	require.Equal(t, forward.Probes[0].ID, reverse.Probes[0].ID, "order must not depend on input order")
	require.Equal(t, forward.Probes[1].ID, reverse.Probes[1].ID)
}
