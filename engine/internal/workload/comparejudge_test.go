package workload_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/workload"
)

// A comparison built by hand rather than driven through a fake runner.
//
// Judge reads only the differences Compare already computed, so building the
// Comparison directly is what lets one test change one number and see which
// threshold reacts. The arithmetic that produced those differences has its own
// tests in compare_test.go, and repeating it here would test that file twice
// and this one not at all.

func rate(baseline, candidate float64) workload.MeasureDifference {
	delta := candidate - baseline
	return workload.MeasureDifference{
		Measure: "achieved_rate", Baseline: &baseline, Candidate: &candidate,
		Delta: &delta,
	}
}

func errorRate(baseline, candidate float64) workload.MeasureDifference {
	delta := candidate - baseline
	return workload.MeasureDifference{
		Measure: "error_rate", Baseline: &baseline, Candidate: &candidate,
		Delta: &delta,
	}
}

func route(name string, baseline, candidate *float64) workload.RouteDifference {
	return workload.RouteDifference{
		Route: name, InBaseline: baseline != nil, InCandidate: candidate != nil,
		P95Baseline: baseline, P95Candidate: candidate,
		// A route this run can see a one percent difference on. These tests
		// are about what the JUDGE does with a difference, so they hand it a
		// resolution generous enough to keep that question separate from
		// whether the run could resolve anything, which compareresolution_test
		// asks on its own. Without it every row here would be unverified for
		// the other reason and these tests would stop testing the judge.
		Resolution: workload.RouteResolution{SmallestVisible: p(0.01)},
	}
}

func p(v float64) *float64 { return &v }

func byName(rows []workload.ComparisonVerdict, name, scope string) workload.ComparisonVerdict {
	for _, r := range rows {
		if r.Name == name && r.Scope == scope {
			return r
		}
	}
	return workload.ComparisonVerdict{Name: "NOT FOUND"}
}

func TestThroughputDropIsJudgedAgainstTheBaseBranchRate(t *testing.T) {
	t.Parallel()
	// The number nothing in this product has ever compared. A build serving
	// half as many requests per second as the base branch is the regression a
	// latency percentile can miss entirely: every request that did complete
	// was fast, and half as many completed.
	c := &workload.Comparison{Measures: []workload.MeasureDifference{rate(100, 50)}}

	rows := workload.Judge(c, workload.ComparisonThresholds{ThroughputDrop: 0.1})
	require.Len(t, rows, 1)
	v := rows[0]
	require.Equal(t, "throughput_drop", v.Name)
	require.Equal(t, workload.VerdictFail, v.Value)
	require.InDelta(t, 0.5, *v.Observed, 1e-9, "a halved rate is a drop of 0.5")
	require.Contains(t, v.Detail, "a drop of 50.0 percent")
	require.Contains(t, v.Detail, "limit of 10.0 percent")
	require.Equal(t, workload.VerdictFail, workload.ComparisonOutcome(rows))

	// The same drop under a limit that allows it passes, which is what stops
	// this being a check that always says no.
	rows = workload.Judge(c, workload.ComparisonThresholds{ThroughputDrop: 0.75})
	require.Equal(t, workload.VerdictPass, rows[0].Value)
	require.Equal(t, workload.VerdictPass, workload.ComparisonOutcome(rows))
}

func TestAFasterBuildNeverBreachesTheThroughputThreshold(t *testing.T) {
	t.Parallel()
	// A drop is signed. An improvement producing a negative drop must not be
	// compared as a magnitude, or every build that got faster would fail.
	c := &workload.Comparison{Measures: []workload.MeasureDifference{rate(50, 100)}}
	rows := workload.Judge(c, workload.ComparisonThresholds{ThroughputDrop: 0.1})
	require.Equal(t, workload.VerdictPass, rows[0].Value)
	require.InDelta(t, -1.0, *rows[0].Observed, 1e-9, "a doubled rate is a drop of minus one")
}

func TestAThroughputThresholdWithNoBaselineRateIsUnverifiedRatherThanPassed(t *testing.T) {
	t.Parallel()
	// Two ways the number can be missing, and neither is a pass. A side that
	// did not record a rate, and a base branch that achieved zero.
	missing := &workload.Comparison{Measures: []workload.MeasureDifference{
		{Measure: "achieved_rate", Baseline: p(10), Candidate: nil},
	}}
	rows := workload.Judge(missing, workload.ComparisonThresholds{ThroughputDrop: 0.1})
	require.Equal(t, workload.VerdictUnverified, rows[0].Value)
	require.Contains(t, rows[0].Detail, "did not record an achieved request rate")
	require.Equal(t, workload.VerdictUnverified, workload.ComparisonOutcome(rows))

	zero := &workload.Comparison{Measures: []workload.MeasureDifference{rate(0, 25)}}
	rows = workload.Judge(zero, workload.ComparisonThresholds{ThroughputDrop: 0.1})
	require.Equal(t, workload.VerdictUnverified, rows[0].Value)
	require.Contains(t, rows[0].Detail, "not a number")

	// And a comparison carrying no rate measure at all, which is what a
	// browser workload produces.
	none := &workload.Comparison{}
	rows = workload.Judge(none, workload.ComparisonThresholds{ThroughputDrop: 0.1})
	require.Equal(t, workload.VerdictUnverified, rows[0].Value)
}

func TestARouteSlowerThanTheBaseBranchFailsItsOwnRow(t *testing.T) {
	t.Parallel()
	// Per route rather than run wide, because a single slow route is exactly
	// what a run wide percentile averages away.
	c := &workload.Comparison{Routes: []workload.RouteDifference{
		route("GET /orders", p(100), p(200)),
		route("GET /health", p(10), p(10)),
	}}
	rows := workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.25})

	orders := byName(rows, "p95_increase", "GET /orders")
	require.Equal(t, workload.VerdictFail, orders.Value)
	require.InDelta(t, 1.0, *orders.Observed, 1e-9, "doubled is a ratio of one")
	require.Contains(t, orders.Detail, "100ms")
	require.Contains(t, orders.Detail, "200ms")

	health := byName(rows, "p95_increase", "GET /health")
	require.Equal(t, workload.VerdictPass, health.Value,
		"the unchanged route must not be dragged down by its neighbour")
	require.Equal(t, workload.VerdictFail, workload.ComparisonOutcome(rows))
}

func TestARoutePresentOnOneSideOnlyIsUnmeasurableRatherThanABreach(t *testing.T) {
	t.Parallel()
	// The case a pass would hide most loudly: a candidate that stopped serving
	// a route has no p95 to be slower than, and calling that clean is worse
	// than calling it slow.
	c := &workload.Comparison{Routes: []workload.RouteDifference{
		route("GET /vanished", p(100), nil),
		route("GET /appeared", nil, p(900)),
		route("GET /kept", p(100), p(101)),
	}}
	rows := workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.25})

	gone := byName(rows, "p95_increase", "GET /vanished")
	require.Equal(t, workload.VerdictUnverified, gone.Value)
	require.Contains(t, gone.Detail, "not on this one")

	added := byName(rows, "p95_increase", "GET /appeared")
	require.Equal(t, workload.VerdictUnverified, added.Value)
	require.Contains(t, added.Detail, "not sent on the base branch")

	// One route measured on both sides, so the threshold did something and the
	// run is a real pass rather than a silent skip.
	require.Equal(t, workload.VerdictPass, workload.ComparisonOutcome(rows))
}

func TestAPerRouteThresholdThatMeasuredNothingIsNotACleanComparison(t *testing.T) {
	t.Parallel()
	// The InertP95 lesson, carried into the base branch comparison. A limit
	// was in force, it evaluated zero routes, and a pass here would report a
	// clean latency comparison over routes nothing was ever compared against.
	c := &workload.Comparison{Routes: []workload.RouteDifference{
		route("GET /vanished", p(100), nil),
		route("GET /appeared", nil, p(900)),
	}}
	rows := workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.25})
	require.Equal(t, workload.VerdictUnverified, workload.ComparisonOutcome(rows))

	inert := byName(rows, "p95_increase", "")
	require.Equal(t, workload.VerdictUnverified, inert.Value)
	require.Contains(t, inert.Detail, "compared nothing")
}

func TestAZeroBaselineP95IsUnverifiedRatherThanAnInfiniteRegression(t *testing.T) {
	t.Parallel()
	c := &workload.Comparison{Routes: []workload.RouteDifference{
		route("GET /instant", p(0), p(50)),
	}}
	rows := workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.25})
	require.Equal(t, workload.VerdictUnverified, byName(rows, "p95_increase", "GET /instant").Value)
	require.Equal(t, workload.VerdictUnverified, workload.ComparisonOutcome(rows))
}

func TestErrorRateIsComparedInAbsolutePointsSoAZeroBaselineStillFires(t *testing.T) {
	t.Parallel()
	// A ratio against a base branch that failed nothing is not a number, and
	// that is the build this most needs to catch: one that introduced errors
	// where there were none.
	c := &workload.Comparison{Measures: []workload.MeasureDifference{errorRate(0, 0.05)}}
	rows := workload.Judge(c, workload.ComparisonThresholds{ErrorRateIncrease: 0.01})
	require.Equal(t, workload.VerdictFail, rows[0].Value)
	require.InDelta(t, 0.05, *rows[0].Observed, 1e-9)

	// A build that fixed errors passes.
	c = &workload.Comparison{Measures: []workload.MeasureDifference{errorRate(0.05, 0)}}
	rows = workload.Judge(c, workload.ComparisonThresholds{ErrorRateIncrease: 0.01})
	require.Equal(t, workload.VerdictPass, rows[0].Value)
}

func TestAnUndeclaredThresholdIsNotEvaluatedAtAll(t *testing.T) {
	t.Parallel()
	// Zero means "not asked for" everywhere else in this product's thresholds,
	// and a zero that meant "no tolerance" would fail every build for a
	// microsecond of host noise.
	c := &workload.Comparison{
		Measures: []workload.MeasureDifference{rate(100, 1)},
		Routes:   []workload.RouteDifference{route("GET /orders", p(10), p(9000))},
	}
	rows := workload.Judge(c, workload.ComparisonThresholds{})
	require.Empty(t, rows, "nothing was declared, so nothing is judged")
	require.False(t, workload.ComparisonThresholds{}.Declared())
	require.True(t, workload.ComparisonThresholds{ThroughputDrop: 0.1}.Declared())

	// And the outcome of judging nothing is unverified, never pass: a
	// comparison with no limits is a report, and a caller that read pass here
	// would be told a check succeeded that was never run.
	require.Equal(t, workload.VerdictUnverified, workload.ComparisonOutcome(rows))
}

func TestIdenticalRunsPassEveryDeclaredThreshold(t *testing.T) {
	t.Parallel()
	// The arm without which a failing threshold proves nothing. If this went
	// red the check would be one that always says no, which is the same
	// defect as one that can never say no.
	c := &workload.Comparison{
		Measures: []workload.MeasureDifference{rate(100, 100), errorRate(0.01, 0.01)},
		Routes: []workload.RouteDifference{
			route("GET /orders", p(40), p(40)),
			route("GET /health", p(5), p(5)),
		},
	}
	all := workload.ComparisonThresholds{
		P95Increase: 0.25, ThroughputDrop: 0.1, ErrorRateIncrease: 0.01,
	}
	rows := workload.Judge(c, all)
	require.Equal(t, workload.VerdictPass, workload.ComparisonOutcome(rows))
	require.Empty(t, workload.ComparisonBreaches(rows))
	for _, r := range rows {
		require.Equal(t, workload.VerdictPass, r.Value, "row %s/%s", r.Name, r.Scope)
	}
}

func TestBreachesAreTheFailingRowsOnly(t *testing.T) {
	t.Parallel()
	c := &workload.Comparison{
		Measures: []workload.MeasureDifference{rate(100, 10)},
		Routes: []workload.RouteDifference{
			route("GET /slow", p(10), p(100)),
			route("GET /fine", p(10), p(10)),
		},
	}
	rows := workload.Judge(c, workload.ComparisonThresholds{
		P95Increase: 0.25, ThroughputDrop: 0.1,
	})
	breaches := workload.ComparisonBreaches(rows)
	require.Len(t, breaches, 2)
	names := map[string]bool{}
	for _, b := range breaches {
		names[b.Name+"|"+b.Scope] = true
	}
	require.True(t, names["throughput_drop|"], "the run wide rate breach")
	require.True(t, names["p95_increase|GET /slow"], "the per route breach")
	require.False(t, names["p95_increase|GET /fine"], "a passing route is not a breach")
}

func TestJudgingANilComparisonReturnsNothingRatherThanPanicking(t *testing.T) {
	t.Parallel()
	require.Empty(t, workload.Judge(nil, workload.ComparisonThresholds{ThroughputDrop: 0.1}))
}
