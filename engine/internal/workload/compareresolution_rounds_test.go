package workload_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/workload"
)

const roundsRoute = "GET /accounts/7/entries"

// roundsWith builds rounds whose base p95 is 10ms every round and whose
// candidate p95 is 10ms times e to the given log ratio, so each round's change
// is known exactly and the interval can be checked by hand.
func roundsWith(logRatios ...float64) []workload.RoundP95 {
	out := make([]workload.RoundP95, 0, len(logRatios))
	for _, l := range logRatios {
		out = append(out, workload.RoundP95{
			Base:      map[string]float64{roundsRoute: 10},
			Candidate: map[string]float64{roundsRoute: 10 * math.Exp(l)},
		})
	}
	return out
}

// pooledThatTheSingleRunBandFails is a pooled comparison of the shape the
// real control produced: 200 requests a side, a tight distribution, and this
// build's p95 three times the base's. The single run band is narrow at that
// sample count, so on its own it calls this a breach.
func pooledThatTheSingleRunBandFails(t *testing.T) *workload.Comparison {
	t.Helper()
	c, err := workload.Compare(
		sideWith("main", metric(roundsRoute, 200, 4.0, 4.6, 4.9, 5.4, 6.0)),
		sideWith("noise-floor", metric(roundsRoute, 200, 12.0, 13.8, 14.7, 16.2, 18.0)))
	require.NoError(t, err)
	return c
}

// THE DEFECT, and the fix, on one comparison. The 13:58 control compared two
// builds differing by a comment and the single run band failed it: GET
// /accounts/7/entries plus 412 percent. That verdict is reproduced first, so
// this test is known to be looking at the failing case. Then the same roundsRoute is
// measured round against round, with rounds that disagree both ways the way
// the real rounds did, and the identical build must not fail.
func TestAnIdenticalBuildOnANoisyHostIsNoLongerAFailure(t *testing.T) {
	limits := workload.ComparisonThresholds{P95Increase: 0.6}

	pooled := pooledThatTheSingleRunBandFails(t)
	before := judgedRow(t, workload.Judge(pooled, limits), roundsRoute)
	require.Equal(t, workload.VerdictFail, before.Value, "the case must fail before the fix")

	c := pooledThatTheSingleRunBandFails(t)
	workload.ResolveByRounds(c, roundsWith(1.4, -0.9, 0.3, -1.1, 0.8, -0.2, 0.5, -0.6))
	judged := workload.Judge(c, limits)
	after := judgedRow(t, judged, roundsRoute)
	require.Equal(t, workload.VerdictUnverified, after.Value)
	require.True(t, after.Unresolvable)
	require.NotEqual(t, workload.VerdictFail, workload.ComparisonOutcome(judged))
	require.Contains(t, after.Detail, "across 8 rounds")
	require.Contains(t, after.Detail, "can resolve a change of about")

	r := c.Routes[0]
	require.False(t, r.Resolution.DirectionResolved(*r.P95Ratio), "no direction on noise")
	require.Less(t, *r.Resolution.ChangeLow, 0.0)
	require.Greater(t, *r.Resolution.ChangeHigh, 0.6)
}

// A real regression that stands clear of the host's noise still fails, and
// says the whole interval is above the limit. The rounds agree with each
// other about a threefold slowdown, so the interval is narrow and above 0.6.
func TestARegressionClearOfTheNoiseStillFails(t *testing.T) {
	c := pooledThatTheSingleRunBandFails(t)
	workload.ResolveByRounds(c, roundsWith(1.1, 1.2, 1.0, 1.15, 1.05, 1.1, 1.2, 1.0))
	row := judgedRow(t, workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.6}), roundsRoute)
	require.Equal(t, workload.VerdictFail, row.Value)
	require.Contains(t, row.Detail, "all of it above the limit")
	require.Greater(t, *c.Routes[0].Resolution.ChangeLow, 0.6)
	require.True(t, c.Routes[0].Resolution.DirectionResolved(*c.Routes[0].P95Ratio))
}

// The change is exactly the ratio of the two columns, and a set of rounds
// that agree perfectly has no width. Twice as slow in every round is plus 100
// percent, which fails a limit of 60 and passes a limit of 150.
func TestTheChangeIsTheRatioOfTheColumnsAndAgreementHasNoWidth(t *testing.T) {
	c := pooledThatTheSingleRunBandFails(t)
	workload.ResolveByRounds(c, roundsWith(math.Ln2, math.Ln2, math.Ln2, math.Ln2))
	r := c.Routes[0]
	require.InDelta(t, 10.0, *r.P95Baseline, 1e-9)
	require.InDelta(t, 20.0, *r.P95Candidate, 1e-9)
	require.InDelta(t, 1.0, *r.P95Ratio, 1e-9)
	require.InDelta(t, 1.0, *r.Resolution.ChangeLow, 1e-9)
	require.InDelta(t, 1.0, *r.Resolution.ChangeHigh, 1e-9)
	require.Equal(t, workload.ResolutionRounds, r.Resolution.Method)
	require.Equal(t, 4, r.Resolution.Rounds)

	fail := judgedRow(t, workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.6}), roundsRoute)
	require.Equal(t, workload.VerdictFail, fail.Value)
	pass := judgedRow(t, workload.Judge(c, workload.ComparisonThresholds{P95Increase: 1.5}), roundsRoute)
	require.Equal(t, workload.VerdictPass, pass.Value)
}

// The interval is the t interval on the per round log ratios, computed here
// independently of the implementation for three rounds: log ratios 0, 0.3 and
// 0.6 have mean 0.3 and sample deviation 0.3, and t at two degrees of freedom
// is 2.920, so the half width is 2.920 * 0.3 / sqrt(3).
func TestTheIntervalIsTheTIntervalOnTheLogRatios(t *testing.T) {
	c := pooledThatTheSingleRunBandFails(t)
	workload.ResolveByRounds(c, roundsWith(0, 0.3, 0.6))
	h := 2.920 * 0.3 / math.Sqrt(3)
	res := c.Routes[0].Resolution
	require.InDelta(t, math.Exp(0.3-h)-1, *res.ChangeLow, 1e-9)
	require.InDelta(t, math.Exp(0.3+h)-1, *res.ChangeHigh, 1e-9)
	require.InDelta(t, math.Exp(h)-1, *res.SmallestVisible, 1e-9)
}

// A roundsRoute that fewer than two rounds sent on both sides keeps its pooled
// numbers and is unresolved, with the count, rather than judged on the single
// run band. A round that sent the roundsRoute on one side only is not a pair.
func TestTooFewRoundPairsLeaveARouteUnresolved(t *testing.T) {
	c := pooledThatTheSingleRunBandFails(t)
	rounds := roundsWith(0.1)
	rounds = append(rounds,
		workload.RoundP95{Base: map[string]float64{roundsRoute: 10}, Candidate: map[string]float64{}},
		workload.RoundP95{Base: map[string]float64{}, Candidate: map[string]float64{roundsRoute: 12}})
	workload.ResolveByRounds(c, rounds)

	r := c.Routes[0]
	require.Equal(t, 1, r.Resolution.Rounds)
	require.Nil(t, r.Resolution.SmallestVisible)
	require.Contains(t, r.Resolution.Detail, "only 1 of 3 rounds")
	row := judgedRow(t, workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.6}), roundsRoute)
	require.Equal(t, workload.VerdictUnverified, row.Value)
}

// A roundsRoute present on one side only is a different finding, and the rounds do
// not overwrite it.
func TestAOneSidedRouteIsLeftAlone(t *testing.T) {
	c, err := workload.Compare(
		sideWith("main", metric(roundsRoute, 200, 4, 4.6, 4.9, 5.4, 6)),
		sideWith("noise-floor"))
	require.NoError(t, err)
	before := c.Routes[0]
	workload.ResolveByRounds(c, roundsWith(0.1, 0.2, 0.3))
	require.Equal(t, before, c.Routes[0])
}
