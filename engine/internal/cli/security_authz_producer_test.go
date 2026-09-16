package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/canaryleak"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
	"github.com/antifailure/antifailure/engine/internal/security/authz"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// This file proves the PRODUCER side end to end: a manifest access fixture and
// the observations the runner's access-probe pass would emit, threaded through
// the real collector helpers this lane added (goldenFromManifest,
// observationsFrom, mergeObservations) and into the real authz and canary_leak
// families. Before this lane the golden was always empty and the runner emitted
// nothing, so the authenticated differential and the planted-canary leak path
// were dead. These tests are the live callers that make them fire.

const producerCanary = "CANARY-ALICE-PII-6d1f"

// fixtureManifest is a two-persona application with one ownership-scoped object
// owned by alice, its canary planted by the app's own seed. alice and bob are in
// one tenant so a bob-reads-alice reach is an idor rather than a cross-tenant
// break.
func fixtureManifest() *schema.Manifest {
	return &schema.Manifest{
		Personas: []schema.Persona{
			{Name: "alice", Role: "member", Tenant: "org_a"},
			{Name: "bob", Role: "member", Tenant: "org_a"},
		},
		Security: &schema.Security{
			Access: &schema.SecurityAccess{
				Objects: []schema.AccessObject{{
					Route:       "/api/orders/{id}",
					ID:          "1001",
					Owner:       schema.AccessOwner{Persona: "alice"},
					ObjectClass: "another customer's order",
					Canary:      producerCanary,
					CanaryKind:  schema.CanaryPII,
				}},
			},
		},
	}
}

// runnerEmission builds the report.Exploration the runner's access-probe pass
// returns, one explore.Observation per reach, exactly the shape access.ts emits.
// It mirrors the field mapping observationsFrom consumes, so a test that feeds
// it exercises that decoder too.
func runnerEmission(obs ...explore.Observation) *report.Exploration {
	return &report.Exploration{
		Results: []explore.Exploration{{
			Name:         "access-probe",
			Observations: obs,
		}},
	}
}

// bobReadsAlice is the headline reach: bob, in alice's tenant, read alice's
// order and the order's canary came back. It is the idor the differential must
// fire on.
func bobReadsAlice() explore.Observation {
	return explore.Observation{
		Route: "/api/orders/{id}", Method: "GET",
		ActorTenant: "org_a", ActorUser: "bob", ActorRole: "member",
		OwnerTenant: "org_a", OwnerUser: "alice", OwnerRole: "member",
		ObjectClass: "another customer's order", Status: 200,
		VictimContentPresent: true, SetupConfirmed: true,
	}
}

// aliceReadsAlice is the self-access liveness arm: the owner reading its own
// object, canary present. It crosses no boundary, so it must produce no probe.
func aliceReadsAlice() explore.Observation {
	return explore.Observation{
		Route: "/api/orders/{id}", Method: "GET",
		ActorTenant: "org_a", ActorUser: "alice", ActorRole: "member",
		OwnerTenant: "org_a", OwnerUser: "alice", OwnerRole: "member",
		ObjectClass: "another customer's order", Status: 200,
		VictimContentPresent: true, SetupConfirmed: true,
	}
}

func authzFailPolicy() report.Policy {
	return report.Policy{Security: map[report.PolicyKey]report.Level{
		authz.RuleMissingAuthorization:  report.LevelFail,
		authz.RuleIDOR:                  report.LevelFail,
		authz.RuleCrossTenant:           report.LevelFail,
		authz.RuleUnauthenticatedAccess: report.LevelFail,
		authz.RulePrivilegeEscalation:   report.LevelFail,
		authz.RulePolicyBypass:          report.LevelFail,
	}}
}

func reachableTwin(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The golden the collector builds from the fixtures is what makes the detector
// live. NewGoldenView had no production caller before goldenFromManifest, so the
// view was always empty and every authenticated reading went inconclusive. This
// proves the fixtures now populate it, canary value, kind and owning tenant.
func TestGoldenFromManifest_PopulatesCanariesFromFixtures(t *testing.T) {
	t.Parallel()
	g := goldenFromManifest(fixtureManifest())
	cs := g.Canaries()
	require.Len(t, cs, 1)
	require.Equal(t, producerCanary, cs[0].Value)
	require.Equal(t, string(schema.CanaryPII), cs[0].Kind)
	require.Equal(t, "org_a", cs[0].Tenant, "the canary belongs to the owning persona's tenant")

	require.Empty(t, goldenFromManifest(&schema.Manifest{}).Canaries(),
		"a manifest with no access fixtures plants nothing, so the view is empty")
}

// The whole chain: manifest fixture -> golden, runner emission -> observationsFrom
// -> mergeObservations -> the REAL authz family -> security.authz.idor. Every hop
// this lane added is exercised, and the finding names the location and never a
// value.
func TestProducerChain_IDORFiresThroughRealFamily(t *testing.T) {
	t.Parallel()
	m := fixtureManifest()

	run := &report.Run{
		URL:         reachableTwin(t),
		AccessProbe: runnerEmission(aliceReadsAlice(), bobReadsAlice()),
	}
	obs := mergeObservations(observationsFrom(run.Exploration), observationsFrom(run.AccessProbe))
	require.NotNil(t, obs, "the runner made readings, so observations are measured, not nil")

	in := security.Input{
		Env:    security.Environment{BaseURL: run.URL},
		Golden: goldenFromManifest(m),
		Policy: authzFailPolicy(),
	}.WithRunArtifacts(security.RunArtifacts{Observations: obs})

	got, err := authz.New().Probe(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, got, 1, "bob reading alice's order with the canary present is one idor")
	require.Equal(t, authz.RuleIDOR, got[0].Rule)
	require.Equal(t, "/api/orders/{id}", got[0].Where)
	require.NotContains(t, got[0].Detail, producerCanary, "a finding never carries the planted value")
	require.NotContains(t, got[0].Detail, "alice", "a finding names a class, never an identity value")
}

// Absent observations is NOT MEASURED: no access-probe pass ran, so the
// differential must fail closed and emit nothing rather than read the absence as
// a clean pass. mergeObservations must return nil when both channels are absent,
// which is what BuildSnapshot reads as not measured.
func TestProducerChain_AbsentObservationsFailClosed(t *testing.T) {
	t.Parallel()
	m := fixtureManifest()
	run := &report.Run{URL: reachableTwin(t)} // no exploration, no access probe

	obs := mergeObservations(observationsFrom(run.Exploration), observationsFrom(run.AccessProbe))
	require.Nil(t, obs, "both channels absent is not measured, which must be nil")

	in := security.Input{
		Env:    security.Environment{BaseURL: run.URL},
		Golden: goldenFromManifest(m),
		Policy: authzFailPolicy(),
	}.WithRunArtifacts(security.RunArtifacts{Observations: obs})

	got, err := authz.New().Probe(context.Background(), in)
	require.NoError(t, err, "a missing pass is not a blocked family")
	require.Empty(t, got, "not measured must never emit an authenticated finding")
}

// A boundary the application refused, paired with the owner's self-access arm, is
// the control working: bob was denied and got no canary, and alice can read her
// own object, so the object was really seeded. No finding.
func TestProducerChain_DeniedBoundaryWithSelfAccessArmDoesNotFire(t *testing.T) {
	t.Parallel()
	m := fixtureManifest()

	denied := bobReadsAlice()
	denied.Status = 404
	denied.VictimContentPresent = false // the boundary dropped the row for a non-owner

	run := &report.Run{
		URL:         reachableTwin(t),
		AccessProbe: runnerEmission(aliceReadsAlice(), denied),
	}
	obs := mergeObservations(observationsFrom(run.Exploration), observationsFrom(run.AccessProbe))

	in := security.Input{
		Env:    security.Environment{BaseURL: run.URL},
		Golden: goldenFromManifest(m),
		Policy: authzFailPolicy(),
	}.WithRunArtifacts(security.RunArtifacts{Observations: obs})

	got, err := authz.New().Probe(context.Background(), in)
	require.NoError(t, err)
	require.Empty(t, got, "a refusal with a live self-access arm is the boundary holding")
}

// canary_leak's planted-value path: the same canary the fixtures planted,
// surfacing in a response it must not, fires the pii key. This is the other path
// goldenFromManifest lights: the family had a golden with no canaries and its
// value loop did nothing.
func TestProducerChain_CanaryLeakFiresOnPlantedValue(t *testing.T) {
	t.Parallel()
	m := fixtureManifest()
	pol := report.Policy{Security: map[report.PolicyKey]report.Level{
		canaryleak.KeyPII:    report.LevelFail,
		canaryleak.KeySecret: report.LevelFail,
	}}

	leaked := security.Input{
		Golden: goldenFromManifest(m),
		Policy: pol,
	}.WithRunArtifacts(security.RunArtifacts{
		Evidence: security.Evidence{Responses: []string{
			`{"order":"1001","leaked":"` + producerCanary + `"}`,
		}},
	})
	got, err := canaryleak.New().Probe(context.Background(), leaked)
	require.NoError(t, err)
	require.NotEmpty(t, got, "the planted pii canary appearing in a response is a leak")
	found := false
	for _, f := range got {
		if f.Rule == string(canaryleak.KeyPII) {
			found = true
			require.NotContains(t, f.Detail, producerCanary, "a leak finding never carries the value")
		}
	}
	require.True(t, found, "the pii key fires for a planted pii canary")

	clean := security.Input{
		Golden: goldenFromManifest(m),
		Policy: pol,
	}.WithRunArtifacts(security.RunArtifacts{
		Evidence: security.Evidence{Responses: []string{`{"order":"1001"}`}},
	})
	gotClean, err := canaryleak.New().Probe(context.Background(), clean)
	require.NoError(t, err)
	for _, f := range gotClean {
		require.NotEqual(t, string(canaryleak.KeyPII), f.Rule,
			"with the canary absent the planted-value path stays silent")
	}
}

// mergeObservations preserves the nil-means-not-measured discipline: both absent
// is nil, either present is the non-nil concatenation. A zero-length non-nil
// slice standing in for absence is the banned defect this suite keeps finding.
func TestMergeObservations_PreservesNotMeasured(t *testing.T) {
	t.Parallel()
	require.Nil(t, mergeObservations(nil, nil), "both absent is not measured")

	a := []security.RawObservation{{Route: "/a"}}
	b := []security.RawObservation{{Route: "/b"}}
	require.Len(t, mergeObservations(a, nil), 1)
	require.Len(t, mergeObservations(nil, b), 1)
	require.Len(t, mergeObservations(a, b), 2)

	both := mergeObservations([]security.RawObservation{}, []security.RawObservation{})
	require.NotNil(t, both, "an empty pass that ran is measured, not absent")
	require.Empty(t, both)
}
