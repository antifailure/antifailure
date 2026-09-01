package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// The call site 'af ci --load' builds its report section from. It had no test
// at all: grepping the engine's test files for --load or withLoad returned
// nothing, which is how three values were dropped and one threshold was passed
// as a literal zero without anything going red.

// A change that fails every request under load, on this repository's own
// manifest: source none, error_rate 0.02, and no p95_increase because a
// default shape carries no baseline.
func failingRun() *load.Result {
	return &load.Result{
		Source: "none", Sent: 400, Rate: 40, ErrorRate: 1.0,
		Overall: load.Latency{P95Ms: 12},
		Routes:  []load.RouteResult{{Route: "GET /", Sent: 400, Errors: 400}},
	}
}

func TestLoadReport_AnErrorRateThresholdCanFire(t *testing.T) {
	t.Parallel()
	// p95_increase is 0 because the manifest refuses it under source none.
	// The old call passed 0 for the error rate as well, and Breaches
	// short-circuits on errorRate > 0, so this run produced no breach and
	// merged green while 'af load run' on the same manifest exited non-zero.
	l := loadReport(failingRun(), nil, 0, 0.02)
	require.Equal(t, []string{"error rate"}, l.Regressed,
		"100 percent of requests failed against a 2 percent threshold")

	// And the gate turns it into the finding that decides the verdict.
	f := loadFinding(l, report.Policy{LoadRegression: report.LevelFail})
	require.NotNil(t, f)
	require.Equal(t, ruleLoadRegression, f.Rule)
	require.Equal(t, report.LevelFail, f.Level)
	require.Contains(t, f.Where, "error rate")
}

func TestLoadReport_AThresholdNobodySetStillDoesNotFire(t *testing.T) {
	t.Parallel()
	// The manifest that configures no thresholds must stay silent. A fix that
	// made every failing run a finding would be a different defect.
	l := loadReport(failingRun(), nil, 0, 0)
	require.Empty(t, l.Regressed)
	require.False(t, l.InertP95)
	require.Nil(t, loadFinding(l, report.Policy{LoadRegression: report.LevelFail}))
}

func TestLoadReport_AThresholdThatMeasuredNothingIsReported(t *testing.T) {
	t.Parallel()
	// p95_increase in force, no route carrying a baseline. 'af load run'
	// exits AF-LOD-016 here; 'af ci' never called InertP95 at all, so it
	// reported a clean p95 for a check that compared nothing.
	res := &load.Result{
		Source: "otel", Sent: 400, Rate: 40,
		Overall: load.Latency{P95Ms: 12},
		Routes:  []load.RouteResult{{Route: "GET /", Sent: 400, HasBaseline: false}},
	}
	l := loadReport(res, nil, 0.25, 0.02)
	require.True(t, l.InertP95)
	require.Empty(t, l.Regressed, "an inert threshold is not a breach; nothing was exceeded")

	f := loadFinding(l, report.Policy{LoadRegression: report.LevelFail})
	require.NotNil(t, f)
	require.Equal(t, "p95_increase", f.Where)
	require.Contains(t, f.Title, "measured nothing")

	// One route with a baseline is enough to make the threshold real again.
	res.Routes[0].HasBaseline = true
	require.False(t, loadReport(res, nil, 0.25, 0.02).InertP95)
}

func TestLoadReport_RecordsWhatTheShapeRefusedToSend(t *testing.T) {
	t.Parallel()
	refused := []load.Route{
		{Method: "POST", Path: "/api/payments"},
		{Method: "DELETE", Path: "/api/items/1"},
	}
	l := loadReport(failingRun(), refused, 0, 0.02)
	require.Equal(t, []string{"POST /api/payments", "DELETE /api/items/1"}, l.Refused)

	// And it reaches the reader rather than the struct.
	md := (&report.Run{Environment: "e", Branch: "b", Load: l}).Markdown()
	require.Contains(t, md, "POST /api/payments")
}

func TestLoadReport_TheInertThresholdReachesTheReader(t *testing.T) {
	t.Parallel()
	l := &report.Load{Sent: 400, Rate: 40, P95Ms: 12, InertP95: true}
	md := (&report.Run{Environment: "e", Branch: "b", Load: l}).Markdown()
	require.Contains(t, md, "p95_increase threshold proved nothing")
}

// The policy is still the policy. An inert threshold under load_regression:
// ignore says nothing, the same as a breach does.
func TestLoadFinding_ObeysTheManifestsPolicy(t *testing.T) {
	t.Parallel()
	inert := &report.Load{Sent: 1, InertP95: true}
	require.Nil(t, loadFinding(inert, report.Policy{LoadRegression: report.LevelIgnore}))
	warn := loadFinding(inert, report.Policy{LoadRegression: report.LevelWarn})
	require.NotNil(t, warn)
	require.Equal(t, report.LevelWarn, warn.Level)
}
