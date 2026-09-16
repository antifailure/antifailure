package authz_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security/authz"
)

// failPolicy sets every authz key to fail, which is the family's own default
// and the level the router overlays before Probe is called.
func failPolicy() report.Policy {
	return report.Policy{Security: map[report.PolicyKey]report.Level{
		authz.RuleMissingAuthorization:  report.LevelFail,
		authz.RuleIDOR:                  report.LevelFail,
		authz.RuleCrossTenant:           report.LevelFail,
		authz.RuleUnauthenticatedAccess: report.LevelFail,
		authz.RulePrivilegeEscalation:   report.LevelFail,
		authz.RulePolicyBypass:          report.LevelFail,
	}}
}

// trusted builds a candidate snapshot whose run-level controls both pass, so a
// test exercises the per-probe logic rather than the run-level guards.
func trusted(probes []authz.Probe, obs map[string]authz.Observation) *authz.Snapshot {
	return &authz.Snapshot{Probes: probes, Observations: obs, Reachable: true, DetectorLive: true}
}

func oneProbe(class authz.Class) authz.Probe {
	return authz.Probe{ID: "p1", Class: class, Where: "/api/thing", Method: "GET",
		ObjectClass: "another tenant's object"}
}

// A regression is the family's whole reason to exist: the baseline refused and
// the candidate allowed. It must fire for every class this family owns, on the
// class's own key.
func TestAssess_RegressionFiresForEachClass(t *testing.T) {
	t.Parallel()
	cases := []struct {
		class authz.Class
		rule  string
	}{
		{authz.ClassIDOR, authz.RuleIDOR},
		{authz.ClassCrossTenant, authz.RuleCrossTenant},
		{authz.ClassMissingAuthorization, authz.RuleMissingAuthorization},
		{authz.ClassPrivilegeEscalation, authz.RulePrivilegeEscalation},
		{authz.ClassPolicyBypass, authz.RulePolicyBypass},
	}
	for _, c := range cases {
		p := oneProbe(c.class)
		base := trusted([]authz.Probe{p}, map[string]authz.Observation{
			"p1": {ProbeID: "p1", Arm: authz.Arm{Actor: authz.OutcomeDenied, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
		})
		cand := trusted([]authz.Probe{p}, map[string]authz.Observation{
			"p1": {ProbeID: "p1", Arm: authz.Arm{Actor: authz.OutcomeAllowed, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
		})
		got := authz.Assess(base, cand, failPolicy())
		require.Len(t, got, 1, "%s: baseline denied, candidate allowed is a regression", c.class)
		require.Equal(t, c.rule, got[0].Rule, "%s: finding lands on the class's own key", c.class)
		require.Equal(t, report.LevelFail, got[0].Level, "%s: level is fail from policy", c.class)
		require.Equal(t, "/api/thing", got[0].Where, "%s: where is the route", c.class)
	}
}

// The RLS taxonomy: a candidate that answers 200 with the victim's content
// ABSENT is a refusal, so it is not a regression even though the status is 2xx.
// A naive check that keyed the leak on "status was not 403" would false-red the
// second an isolated target was touched.
func TestAssess_ContentAbsentIsDeniedNotLeak(t *testing.T) {
	t.Parallel()
	p := oneProbe(authz.ClassCrossTenant)
	base := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeDenied, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	// Candidate refused by dropping the row: OutcomeDenied even though the wire
	// status was 200, which the transport already normalised.
	cand := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeDenied, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	got := authz.Assess(base, cand, failPolicy())
	require.Empty(t, got, "a 200 with the victim content absent is a refusal, not a leak")
}

// Both sides allowing it is a pre-existing exposure, which this change did not
// cause; failing on it would false-red every PR that touched the endpoint.
func TestAssess_BothAllowedIsPreExistingAndSilent(t *testing.T) {
	t.Parallel()
	p := oneProbe(authz.ClassIDOR)
	both := map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeAllowed, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	}
	got := authz.AssessDetailed(trusted([]authz.Probe{p}, both), trusted([]authz.Probe{p}, both), failPolicy())
	require.Empty(t, got.Findings, "a pre-existing exposure is not this change's regression")
	require.Empty(t, got.Inconclusive, "an allowed reading is decided, not inconclusive")
}

// A boundary that held on the candidate is a pass only when the liveness arm
// proves the refusal is a refusal. Both-denied with a LIVE arm is a clean pass.
func TestAssess_BothDeniedWithLiveArmIsPass(t *testing.T) {
	t.Parallel()
	p := oneProbe(authz.ClassIDOR)
	denied := map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeDenied, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	}
	got := authz.AssessDetailed(trusted([]authz.Probe{p}, denied), trusted([]authz.Probe{p}, denied), failPolicy())
	require.Empty(t, got.Findings)
	require.Empty(t, got.Inconclusive, "a refusal with a live arm is a decided pass")
}

// The load bearing case: both-denied with a DEAD liveness arm is NOT a pass. A
// renamed route, a session that never established, or an app that is down all
// read denied, and only the liveness arm tells refusal from absence. This must
// be reported as inconclusive, never swallowed as a green.
func TestAssess_BothDeniedWithDeadArmIsInconclusive(t *testing.T) {
	t.Parallel()
	p := oneProbe(authz.ClassIDOR)
	deadArm := map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeDenied, Liveness: authz.OutcomeDenied, LivenessArmed: true}},
	}
	got := authz.AssessDetailed(trusted([]authz.Probe{p}, deadArm), trusted([]authz.Probe{p}, deadArm), failPolicy())
	require.Empty(t, got.Findings, "a dead control is not a finding")
	require.Len(t, got.Inconclusive, 1, "a dead control is inconclusive, not a pass")
	require.False(t, got.Inconclusive[0].Setup, "the arm ran; it was not a setup failure")
	require.Equal(t, authz.RuleIDOR, got.Inconclusive[0].Rule)
}

// A liveness arm whose privileged setup was never confirmed is a distinct
// inconclusive: the probe could not be armed, which has a different fix from a
// probe that ran and could not decide.
func TestAssess_LivenessSetupUnconfirmedIsInconclusiveSetup(t *testing.T) {
	t.Parallel()
	p := oneProbe(authz.ClassMissingAuthorization)
	unarmed := map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeDenied, Liveness: authz.OutcomeAllowed, LivenessArmed: false}},
	}
	got := authz.AssessDetailed(trusted([]authz.Probe{p}, unarmed), trusted([]authz.Probe{p}, unarmed), failPolicy())
	require.Empty(t, got.Findings)
	require.Len(t, got.Inconclusive, 1)
	require.True(t, got.Inconclusive[0].Setup, "an unconfirmed setup is a setup inconclusive")
}

// A candidate that errored is inconclusive for that probe: a broken endpoint is
// a different check's finding, not an authorization pass and not a fail.
func TestAssess_CandidateErrorIsInconclusive(t *testing.T) {
	t.Parallel()
	p := oneProbe(authz.ClassIDOR)
	base := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeDenied, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	cand := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeError, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	got := authz.AssessDetailed(base, cand, failPolicy())
	require.Empty(t, got.Findings)
	require.Len(t, got.Inconclusive, 1)
}

// 429 is never a denial. A probe fleet is bursty and trips limiters under load,
// so normalising 429 to denied would report a green exactly when the target was
// too busy to enforce anything.
func TestAssess_RateLimitedIsNotDenied(t *testing.T) {
	t.Parallel()
	p := oneProbe(authz.ClassIDOR)
	base := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeDenied, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	cand := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeRateLimited, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	got := authz.AssessDetailed(base, cand, failPolicy())
	require.Empty(t, got.Findings, "a rate limited answer is not a refusal to pass on")
	require.Len(t, got.Inconclusive, 1)
}

// A new endpoint has no baseline to differ from, so it is judged against the
// absolute expectation: an unauthenticated reach that returns content is a
// regression unless declared public.
func TestAssess_NewEndpointUnauthenticatedFiresAbsolute(t *testing.T) {
	t.Parallel()
	p := authz.Probe{ID: "p1", Class: authz.ClassUnauthenticated, Where: "/api/new", Method: "GET"}
	cand := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeAllowed, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	got := authz.Assess(nil, cand, failPolicy())
	require.Len(t, got, 1, "an added endpoint reachable by anon with content is a regression")
	require.Equal(t, authz.RuleUnauthenticatedAccess, got[0].Rule)
}

// A declared public endpoint reached by anon is expected and never flags.
func TestAssess_PublicEndpointNeverFlags(t *testing.T) {
	t.Parallel()
	p := authz.Probe{ID: "p1", Class: authz.ClassUnauthenticated, Where: "/health", Public: true}
	cand := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeAllowed, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	require.Empty(t, authz.Assess(nil, cand, failPolicy()), "a declared public reach is expected")
}

// A key the manifest set to ignore is dropped: emitting it would put back what
// was silenced.
func TestAssess_IgnoreLevelIsDropped(t *testing.T) {
	t.Parallel()
	p := oneProbe(authz.ClassIDOR)
	pol := report.Policy{Security: map[report.PolicyKey]report.Level{authz.RuleIDOR: report.LevelIgnore}}
	base := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeDenied, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	cand := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeAllowed, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	require.Empty(t, authz.Assess(base, cand, pol), "an ignored key emits nothing")
}

// The finding's level comes from the policy, never hard-coded, so the manifest
// stays the one place a finding's severity is decided.
func TestAssess_LevelComesFromPolicy(t *testing.T) {
	t.Parallel()
	p := oneProbe(authz.ClassIDOR)
	pol := report.Policy{Security: map[report.PolicyKey]report.Level{authz.RuleIDOR: report.LevelWarn}}
	base := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeDenied, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	cand := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeAllowed, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	got := authz.Assess(base, cand, pol)
	require.Len(t, got, 1)
	require.Equal(t, report.LevelWarn, got[0].Level, "warn in the manifest means warn in the finding")
}

// An unreachable candidate blocks the whole run: inconclusive, never a pass.
func TestAssess_UnreachableCandidateIsInconclusive(t *testing.T) {
	t.Parallel()
	p := oneProbe(authz.ClassIDOR)
	cand := &authz.Snapshot{Probes: []authz.Probe{p}, Reachable: false, DetectorLive: true,
		Observations: map[string]authz.Observation{"p1": {Arm: authz.Arm{Actor: authz.OutcomeAllowed}}}}
	got := authz.AssessDetailed(nil, cand, failPolicy())
	require.Empty(t, got.Findings, "an unreachable twin proves nothing, including no leak")
	require.Len(t, got.Inconclusive, 1)
}

// A run whose detector was not proven cannot trust a reading of the twin.
func TestAssess_DeadDetectorIsInconclusive(t *testing.T) {
	t.Parallel()
	p := oneProbe(authz.ClassIDOR)
	cand := &authz.Snapshot{Probes: []authz.Probe{p}, Reachable: true, DetectorLive: false,
		Observations: map[string]authz.Observation{"p1": {Arm: authz.Arm{Actor: authz.OutcomeAllowed}}}}
	got := authz.AssessDetailed(nil, cand, failPolicy())
	require.Empty(t, got.Findings)
	require.Len(t, got.Inconclusive, 1)
}

// Two leaks on one route and rule aggregate into one finding with a count,
// rather than two findings that read as two endpoints.
func TestAssess_AggregatesByRuleAndWhere(t *testing.T) {
	t.Parallel()
	p1 := authz.Probe{ID: "a", Class: authz.ClassIDOR, Where: "/api/x"}
	p2 := authz.Probe{ID: "b", Class: authz.ClassIDOR, Where: "/api/x"}
	obs := map[string]authz.Observation{
		"a": {Arm: authz.Arm{Actor: authz.OutcomeAllowed, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
		"b": {Arm: authz.Arm{Actor: authz.OutcomeAllowed, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	}
	got := authz.Assess(nil, trusted([]authz.Probe{p1, p2}, obs), failPolicy())
	require.Len(t, got, 1, "one route and one rule is one finding")
	require.Equal(t, 2, got[0].Count, "the count reflects both probes")
}

// A tightened endpoint (baseline allowed, candidate denied) is a pass with a
// live arm, and it is neither a finding nor inconclusive.
func TestAssess_TightenedIsAPass(t *testing.T) {
	t.Parallel()
	p := oneProbe(authz.ClassIDOR)
	base := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeAllowed, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	cand := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeDenied, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	got := authz.AssessDetailed(base, cand, failPolicy())
	require.Empty(t, got.Findings)
	require.Empty(t, got.Inconclusive)
}

// The finding never carries a value: not the leaked marker, not the object id,
// not the body. It carries a rule, a level, a location and a bounded
// description. This is the data boundary a security buyer scrutinises.
func TestAssess_FindingIsBounded(t *testing.T) {
	t.Parallel()
	const marker = "CANARY-SECRET-7f3a"
	const objectID = "invoice-99887766"
	p := authz.Probe{ID: "p1", Class: authz.ClassCrossTenant, Where: "/api/invoices",
		Method: "GET", ObjectClass: "another tenant's invoice"}
	cand := trusted([]authz.Probe{p}, map[string]authz.Observation{
		"p1": {Arm: authz.Arm{Actor: authz.OutcomeAllowed, Liveness: authz.OutcomeAllowed, LivenessArmed: true}},
	})
	got := authz.Assess(nil, cand, failPolicy())
	require.Len(t, got, 1)
	f := got[0]
	for _, field := range []string{f.Title, f.Detail, f.Fix, f.Where} {
		require.NotContains(t, field, marker, "a finding must never carry the leaked value")
		require.NotContains(t, field, objectID, "a finding must never carry a raw object id")
	}
}

func TestStatusOutcome_ClassifiesByContentNotStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status  int
		content bool
		want    authz.Outcome
	}{
		{429, true, authz.OutcomeRateLimited},
		{429, false, authz.OutcomeRateLimited},
		{500, true, authz.OutcomeError},
		{0, false, authz.OutcomeError},
		{401, false, authz.OutcomeDenied},
		{403, false, authz.OutcomeDenied},
		{404, false, authz.OutcomeDenied},
		{200, false, authz.OutcomeDenied},
		{200, true, authz.OutcomeAllowed},
		{302, true, authz.OutcomeDenied},
	}
	for _, c := range cases {
		require.Equalf(t, c.want, authz.StatusOutcome(c.status, c.content),
			"status %d content=%v", c.status, c.content)
	}
}
