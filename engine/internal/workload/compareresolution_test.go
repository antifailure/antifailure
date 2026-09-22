package workload_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/workload"
)

// The case that produced the defect, rebuilt from the numbers it printed.
//
// Two commits differing by one comment, compared on a loaded host, 145
// requests across seven routes. The tool called the second sample a failure on
// six routes and the first a pass. These are the measured p95 values from
// evidence/shot10b-noise-floor.txt with the rest of each distribution filled
// in around them, because the failure is driven by the SAMPLE COUNT and the
// width of the tail, and twenty samples make any plausible tail too wide to
// see sixty percent through.

func metric(route string, sent int, p50, p90, p95, p99, max float64) workload.RouteMetric {
	return workload.RouteMetric{
		Route: route, Sent: sent,
		P50Ms: &p50, P90Ms: &p90, P95Ms: &p95, P99Ms: &p99, MaxMs: &max,
	}
}

func sideWith(branch string, rows ...workload.RouteMetric) *workload.Result {
	return &workload.Result{
		Schema: workload.ResultSchema, Kind: workload.ObservedLoad,
		State: workload.StateSucceeded, Verdict: workload.VerdictPass,
		Environment: workload.Environment{Branch: branch},
		Routes:      rows,
	}
}

func judgedRow(t *testing.T, rows []workload.ComparisonVerdict, scope string) workload.ComparisonVerdict {
	t.Helper()
	for _, r := range rows {
		if r.Scope == scope {
			return r
		}
	}
	t.Fatalf("no judged row for %q", scope)
	return workload.ComparisonVerdict{}
}

func TestTheIdenticalBuildThatFailedSixRoutesNowResolvesNothing(t *testing.T) {
	t.Parallel()
	// Sample 2 of the identical build comparison. Every one of these numbers
	// came from two commits whose only difference is a comment, so a verdict
	// of fail on any of them is the tool reporting a regression that does not
	// exist.
	base := sideWith("main",
		metric("GET /accounts", 20, 40, 70, 83.1, 150, 200),
		metric("GET /accounts/7/balance", 21, 120, 190, 223, 300, 380),
	)
	cand := sideWith("noise-floor",
		metric("GET /accounts", 20, 200, 450, 570, 1500, 2000),
		metric("GET /accounts/7/balance", 21, 400, 700, 877, 1400, 1900),
	)
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	// The differences are still reported. Nothing is hidden, and the
	// direction is still worse, because that is what was measured.
	accounts := routeRow(t, c, "GET /accounts")
	require.Equal(t, workload.DirectionWorse, accounts.Direction)
	require.InDelta(t, 5.859, *accounts.P95Ratio, 0.01, "the +585.9 percent it printed")

	// What changed is that the run now says it cannot see that far. The band
	// is wider than the difference itself, so the interval around plus 585.9
	// percent runs well below zero and the limit sits inside it.
	require.NotNil(t, accounts.Resolution.SmallestVisible)
	require.Greater(t, *accounts.Resolution.SmallestVisible, 5.859,
		"the band must be wider than the difference, which is why the difference is noise")
	require.False(t, accounts.Resolution.DirectionResolved(*accounts.P95Ratio),
		"a difference smaller than the band has no sign this run may claim")
	require.False(t, accounts.Resolution.TooFewSamples,
		"twenty samples is exactly the boundary; the band alone is what refuses this one")

	rows := workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.6})
	for _, scope := range []string{"GET /accounts", "GET /accounts/7/balance"} {
		row := judgedRow(t, rows, scope)
		require.Equal(t, workload.VerdictUnverified, row.Value,
			"%s reported %s on an identical build", scope, row.Value)
		require.True(t, row.Unresolvable)
		require.Contains(t, row.Detail, "neither a pass nor a breach would have meant anything")
	}

	// And the run as a whole refuses, rather than failing six routes.
	require.Equal(t, workload.VerdictUnverified, workload.ComparisonOutcome(rows))
	require.Empty(t, workload.ComparisonBreaches(rows),
		"an identical build must produce no breaches at all")
}

func TestTheIdenticalBuildThatPassedNoLongerClaimsAnImprovement(t *testing.T) {
	t.Parallel()
	// Sample 1, the other direction: every route "better" by up to 86 percent,
	// verdict pass, exit 0. The harm was the arrow, not the verdict. A reader
	// ships a real regression believing it an improvement.
	//
	// The verdict itself survives, and it should. The question the limit asks
	// is "did this get more than 60 percent SLOWER", and the interval around
	// minus 80 percent runs from minus 172 to plus 12, which is entirely below
	// 60. The run is entitled to say the limit held. What it is NOT entitled
	// to say is that anything got better, because the difference is smaller
	// than the distance the number could have moved on its own, and the very
	// next sample of the same two commits said plus 586 percent.
	base := sideWith("main",
		metric("GET /accounts", 20, 200, 430, 512, 900, 1200),
		metric("GET /accounts/7/entries", 20, 240, 480, 567, 1000, 1400),
	)
	cand := sideWith("noise-floor",
		metric("GET /accounts", 20, 45, 88, 103, 180, 240),
		metric("GET /accounts/7/entries", 20, 30, 65, 76.9, 140, 190),
	)
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	// Not one of these routes may claim a direction.
	for _, r := range c.Routes {
		require.NotNil(t, r.P95Ratio)
		require.False(t, r.Resolution.DirectionResolved(*r.P95Ratio),
			"%s claimed %s on a comment only change", r.Route, r.Direction)
	}

	rows := workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.6})
	require.Equal(t, workload.VerdictPass, workload.ComparisonOutcome(rows),
		"the limit asks about getting slower, and this run can say it did not")
	require.Empty(t, workload.ComparisonBreaches(rows))
}

func TestALongQuietRunStillDecides(t *testing.T) {
	t.Parallel()
	// The falsification arm, and without it the gate above is a check that
	// always says no. Four thousand samples and a tail that does not sprawl:
	// the band closes to a few percent and a sixty percent limit is well
	// inside what the run can see, so a real breach is still a breach and a
	// clean route still passes.
	base := sideWith("main",
		metric("GET /slow", 4000, 40, 52, 58, 70, 95),
		metric("GET /fine", 4000, 20, 26, 29, 35, 48),
	)
	cand := sideWith("feature",
		metric("GET /slow", 4000, 90, 130, 148, 190, 260),
		metric("GET /fine", 4000, 20, 26, 29.3, 36, 50),
	)
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	slow := routeRow(t, c, "GET /slow")
	require.NotNil(t, slow.Resolution.SmallestVisible)
	require.Less(t, *slow.Resolution.SmallestVisible, 0.6,
		"a long quiet run must be able to see sixty percent, or the gate refuses everything")
	require.False(t, slow.Resolution.TooFewSamples)

	rows := workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.6})
	require.Equal(t, workload.VerdictFail, workload.ComparisonOutcome(rows),
		"a real 155 percent regression on a run that can see 60 percent is a failure")

	breaches := workload.ComparisonBreaches(rows)
	require.Len(t, breaches, 1)
	require.Equal(t, "GET /slow", breaches[0].Scope)
	require.Equal(t, workload.VerdictPass, judgedRow(t, rows, "GET /fine").Value,
		"the unchanged route on the same run still passes, so this is not a blanket refusal")
}

func TestABreachTheRunCanSeeOutranksARouteItCannot(t *testing.T) {
	t.Parallel()
	// A resolvable failure must not be buried by a blind neighbour. The
	// opposite of the defect, and it would be the same defect pointed the
	// other way.
	base := sideWith("main",
		metric("GET /slow", 4000, 40, 52, 58, 70, 95),
		metric("GET /rare", 12, 40, 70, 83, 150, 200),
	)
	cand := sideWith("feature",
		metric("GET /slow", 4000, 90, 130, 148, 190, 260),
		metric("GET /rare", 12, 45, 75, 90, 160, 210),
	)
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	rows := workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.6})
	require.Equal(t, workload.VerdictFail, workload.ComparisonOutcome(rows),
		"a breach the run resolved is a breach whatever else went unseen")
	require.True(t, judgedRow(t, rows, "GET /rare").Unresolvable)
}

func TestPassingRoutesDoNotOutrankABlindOne(t *testing.T) {
	t.Parallel()
	// Three routes passing and one invisible is not a pass with a footnote.
	// The limit was in force over both and the run answered for one.
	base := sideWith("main",
		metric("GET /fine", 4000, 20, 26, 29, 35, 48),
		metric("GET /rare", 12, 40, 70, 83, 150, 200),
	)
	cand := sideWith("feature",
		metric("GET /fine", 4000, 20, 26, 29.3, 36, 50),
		metric("GET /rare", 12, 45, 75, 90, 160, 210),
	)
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	rows := workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.6})
	require.Equal(t, workload.VerdictPass, judgedRow(t, rows, "GET /fine").Value)
	require.True(t, judgedRow(t, rows, "GET /rare").Unresolvable)
	require.Equal(t, workload.VerdictUnverified, workload.ComparisonOutcome(rows),
		"one route answered and one was invisible, so the run did not clear the limit")
}

func TestFewerThanTwentySamplesIsNamedAsSuch(t *testing.T) {
	t.Parallel()
	// The crispest form of the problem, and worth its own sentence: below
	// twenty samples the nearest rank p95 IS the slowest single request, so
	// the number is not a percentile of anything.
	base := sideWith("main", metric("GET /rare", 19, 10, 18, 20, 25, 30))
	cand := sideWith("feature", metric("GET /rare", 19, 11, 19, 21, 26, 31))
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	r := routeRow(t, c, "GET /rare")
	require.True(t, r.Resolution.TooFewSamples)
	require.Contains(t, r.Resolution.Detail, "slowest single request")

	// And at twenty it stops saying so, which is what makes the boundary real
	// rather than a number somebody typed.
	base20 := sideWith("main", metric("GET /rare", 20, 10, 18, 20, 25, 30))
	cand20 := sideWith("feature", metric("GET /rare", 20, 11, 19, 21, 26, 31))
	c20, err := workload.Compare(base20, cand20)
	require.NoError(t, err)
	require.False(t, routeRow(t, c20, "GET /rare").Resolution.TooFewSamples)
}

func TestARouteWithNoDistributionCannotClaimAResolution(t *testing.T) {
	t.Parallel()
	// A side that recorded a p95 and nothing else has no shape to read the
	// band off. That is "could not look", and it must not become a band of
	// zero, which would read as perfect resolution.
	p95 := 100.0
	bare := workload.RouteMetric{Route: "GET /x", Sent: 500, P95Ms: &p95}
	base := sideWith("main", bare)
	cand := sideWith("feature", bare)
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	r := routeRow(t, c, "GET /x")
	require.NotNil(t, r.Resolution.SmallestVisible,
		"one quantile is still a distribution of one point, so a band of zero is honest here")
	require.Zero(t, *r.Resolution.SmallestVisible)

	// Zero sent is the real "could not look", and it says so.
	none := workload.RouteMetric{Route: "GET /y", Sent: 0, P95Ms: &p95}
	c2, err := workload.Compare(sideWith("main", none), sideWith("feature", none))
	require.NoError(t, err)
	r2 := routeRow(t, c2, "GET /y")
	require.Nil(t, r2.Resolution.SmallestVisible)
	require.Contains(t, r2.Resolution.Detail, "no distribution")
	_, ok := r2.Resolution.Verdict(99, 0.6)
	require.False(t, ok, "an unknown band decides nothing, whatever it is shown")
}

// ordersTail is one route's distribution, held fixed while only the sample
// count changes. The shape is a 5ms handler's on this machine: a tight body
// and a tail that opens past p95.
func ordersTail(sent int) workload.RouteMetric {
	return metric("GET /orders", sent, 5.3, 5.9, 6.2, 7.4, 11.0)
}

// smallestVisibleAt compares two identical sides of n samples each and returns
// the difference that comparison could see.
func smallestVisibleAt(t *testing.T, n int) float64 {
	t.Helper()
	c, err := workload.Compare(sideWith("main", ordersTail(n)), sideWith("feature", ordersTail(n)))
	require.NoError(t, err)
	r := routeRow(t, c, "GET /orders")
	require.NotNil(t, r.Resolution.SmallestVisible, "%d samples a side resolved nothing", n)
	return *r.Resolution.SmallestVisible
}

func TestMoreSamplesResolveASmallerDifference(t *testing.T) {
	t.Parallel()
	// The property the band rests on: with the distribution held still, more
	// samples see a smaller difference. If that stops holding, the band is not
	// measuring sampling error and every refusal it produces is arbitrary.
	//
	// THIS USED TO BE A LIVE TEST, AND IT PROVED NOTHING EITHER WAY. It timed
	// two real servers, a thin run of about 77 samples a side at concurrency 20
	// and a dense one of about 1300 at concurrency 40, and asserted the dense
	// band was narrower. It passed and failed on the same tree: PR #557 changed
	// no Go and read "thin can see 3.3%, dense can see 4.1%", red, on a commit
	// whose engine job had passed on main. Looped eight times on an idle
	// laptop it failed three, with the thin band anywhere from 4.1 to 34.4
	// percent and the dense one from 4.8 to 30.2.
	//
	// The flake was not noise around a true claim; the claim was confounded.
	// The band is the run's own distribution read either side of p95, so it
	// moves with the tail as well as with the count, and the two runs had
	// different tails: a different concurrency against a different request
	// rate, on whatever the host was doing that second. Seventeen times the
	// samples narrows the quantile window about four fold, and a tail that
	// happened to open twice as steeply in the dense run gave that back. So the
	// count is the only thing that changes here.
	// 77 and 1300 are the thin and dense counts the live version sent.
	counts := []int{20, 40, 77, 100, 400, 1300, 1600, 6400, 25600}
	previous := math.Inf(1)
	for _, n := range counts {
		got := smallestVisibleAt(t, n)
		t.Logf("%6d samples a side can see %.2f%%", n, got*100)
		require.Less(t, got, previous, "%d samples saw no smaller a difference than fewer did", n)
		previous = got
	}

	// And by how much, which is the law rather than the direction. From a
	// hundred samples up the window around p95 sits between the recorded p90
	// and p99, where the distribution is a straight line either side, so the
	// band is the window's half width times the two slopes, on both sides,
	// over the base p95. The window's half width goes as one over the square
	// root of the count, so four times the samples is exactly half the band.
	lower := (6.2 - 5.9) / 0.05
	upper := (7.4 - 6.2) / 0.04
	for _, n := range []int{100, 400, 1600, 6400} {
		dq := 1.645 * math.Sqrt(0.95*0.05/float64(n))
		want := dq * (lower + upper) / 6.2
		require.InDelta(t, want, smallestVisibleAt(t, n), 1e-9,
			"%d samples a side: the band is not the sampling window read off the distribution", n)
		require.InDelta(t, 0.5, smallestVisibleAt(t, 4*n)/smallestVisibleAt(t, n), 1e-9,
			"four times %d samples must halve the band", n)
	}
}

func TestAWiderTailIsAWiderBandAtTheSameCount(t *testing.T) {
	t.Parallel()
	// The other half of why the live version flaked, stated as its own
	// property so it is a claim and not an accident: at one sample count the
	// band follows the tail. A run whose tail opens further past p95 cannot see
	// as small a difference, which is correct, and is exactly why two live runs
	// with different tails cannot be compared on their counts alone.
	band := func(p99 float64) float64 {
		row := metric("GET /orders", 1300, 5.3, 5.9, 6.2, p99, 30)
		c, err := workload.Compare(sideWith("main", row), sideWith("feature", row))
		require.NoError(t, err)
		r := routeRow(t, c, "GET /orders")
		require.NotNil(t, r.Resolution.SmallestVisible)
		return *r.Resolution.SmallestVisible
	}
	require.Less(t, band(7.4), band(9.8))
	require.Less(t, band(9.8), band(14.0))
}

func TestIdenticalSidesNeverBreachAtAnyCount(t *testing.T) {
	t.Parallel()
	// The live test's second claim, that neither run invents a regression
	// between identical servers. On identical sides the observed change is
	// zero and the band is positive, so this holds by construction at every
	// count; asserting it here keeps it from ever depending on a host again.
	for _, n := range []int{20, 77, 1300, 25600} {
		c, err := workload.Compare(sideWith("main", ordersTail(n)), sideWith("feature", ordersTail(n)))
		require.NoError(t, err)
		rows := workload.Judge(c, workload.ComparisonThresholds{P95Increase: 1.0, ThroughputDrop: 0.25})
		require.NotEqual(t, workload.VerdictFail, workload.ComparisonOutcome(rows), "%d samples a side", n)
		require.Empty(t, workload.ComparisonBreaches(rows), "%d samples a side", n)
	}
}
