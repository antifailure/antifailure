package workload_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/workload"
)

// twoRuns produces a baseline and a candidate of the same kind, so a test can
// change one thing and see what the comparison says about it.
func twoRuns(t *testing.T, tune func(candidate *fakeRunner)) (*workload.Result, *workload.Result) {
	t.Helper()
	baseline := execute(t, context.Background(), mixRunner(), workload.Request{
		RunID: "run_base", Kind: "observed_load",
	}, func(o *workload.Options) { o.Branch = "main" })

	r := mixRunner()
	if tune != nil {
		tune(r)
	}
	candidate := execute(t, context.Background(), r, workload.Request{
		RunID: "run_cand", Kind: "observed_load",
	}, func(o *workload.Options) { o.Branch = "feature" })
	return baseline, candidate
}

func TestTwoResultsOfDifferentKindsAreRefusedRatherThanDifferenced(t *testing.T) {
	mix := execute(t, context.Background(), mixRunner(), workload.Request{Kind: "observed_load"}, nil)
	r := mixRunner()
	r.testReport = onePassingWorkflow()
	browser := execute(t, context.Background(), r, workload.Request{Kind: "browser_workflow"}, nil)

	_, err := workload.Compare(mix, browser)
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF-WLD-011")
	require.Contains(t, err.Error(), "the kinds differ")

	_, err = workload.Compare(nil, mix)
	require.Error(t, err)
}

func TestASlowerRouteIsReportedAsWorseAndAFasterOneAsBetter(t *testing.T) {
	baseline, candidate := twoRuns(t, func(r *fakeRunner) {
		r.loadResult.Routes[0].Latency.P95Ms = 60 // was 30
		r.loadResult.Routes[1].Latency.P95Ms = 2  // was 4
	})
	c, err := workload.Compare(baseline, candidate)
	require.NoError(t, err)

	byRoute := map[string]workload.RouteDifference{}
	for _, r := range c.Routes {
		byRoute[r.Route] = r
	}
	orders := byRoute["GET /orders"]
	require.True(t, orders.InBaseline)
	require.True(t, orders.InCandidate)
	require.InDelta(t, 30, *orders.P95Delta, 1e-9)
	require.InDelta(t, 1.0, *orders.P95Ratio, 1e-9)
	require.Equal(t, workload.DirectionWorse, orders.Direction)

	health := byRoute["GET /health"]
	require.InDelta(t, -2, *health.P95Delta, 1e-9)
	require.Equal(t, workload.DirectionBetter, health.Direction)
}

func TestARouteOnOneSideOnlyIsAFindingRatherThanAGap(t *testing.T) {
	// A route that stopped being sent between two runs is exactly the thing
	// somebody wants to know, and dropping it because it cannot be differenced
	// is how it goes unnoticed.
	baseline, candidate := twoRuns(t, func(r *fakeRunner) {
		r.loadResult.Routes = r.loadResult.Routes[:1]
		r.loadResult.Routes = append(r.loadResult.Routes, load.RouteResult{
			Route: "GET /pricing", Sent: 5, Latency: load.Latency{P95Ms: 9},
		})
	})
	c, err := workload.Compare(baseline, candidate)
	require.NoError(t, err)

	byRoute := map[string]workload.RouteDifference{}
	for _, r := range c.Routes {
		byRoute[r.Route] = r
	}
	gone := byRoute["GET /health"]
	require.True(t, gone.InBaseline)
	require.False(t, gone.InCandidate)
	require.Equal(t, workload.DirectionUnmeasurable, gone.Direction)
	require.Nil(t, gone.P95Delta)

	added := byRoute["GET /pricing"]
	require.False(t, added.InBaseline)
	require.True(t, added.InCandidate)
	require.Equal(t, workload.DirectionUnmeasurable, added.Direction)
}

func TestOnlyAMoveAwayFromAPassCountsAsARegression(t *testing.T) {
	// A check that went from unverified to fail is a change worth showing and
	// was never passing. Counting it as a regression tells somebody a working
	// thing broke, which sends them looking for a change that does not exist.
	baseline, candidate := twoRuns(t, func(r *fakeRunner) {
		r.loadResult.ErrorRate = 0.5
	})
	c, err := workload.Compare(baseline, candidate)
	require.NoError(t, err)

	// The baseline already breached its error rate, so this comparison has a
	// change and no regression.
	require.Equal(t, 0, c.Regressed)

	byName := map[string]workload.ThresholdDifference{}
	for _, d := range c.Thresholds {
		byName[d.Name+"|"+d.Scope] = d
	}
	rate := byName["error_rate|"]
	require.Equal(t, workload.VerdictFail, rate.Baseline)
	require.Equal(t, workload.VerdictFail, rate.Candidate)
	require.False(t, rate.Changed)
	require.False(t, rate.Regressed)
}

func TestAThresholdThatWentFromUnverifiedToFailIsChangedAndNotARegression(t *testing.T) {
	// The distinction the Regressed counter exists to make. GET /health has no
	// baseline in the mix, so its p95 check is unverified: nothing was ever
	// compared. The candidate's shape carries a baseline and the route is over
	// it, so the check now fails. Something moved, and nothing that was working
	// broke. Counting this as a regression sends somebody looking for a change
	// that does not exist.
	baseline := execute(t, context.Background(), mixRunner(), workload.Request{
		RunID: "run_base", Kind: "observed_load",
	}, nil)

	after := mixRunner()
	after.loadResult.Routes[1].HasBaseline = true
	after.loadResult.Routes[1].BaselineP95Ms = 1
	after.loadResult.Routes[1].P95Increase = 3
	candidate := execute(t, context.Background(), after, workload.Request{
		RunID: "run_cand", Kind: "observed_load",
	}, nil)

	c, err := workload.Compare(baseline, candidate)
	require.NoError(t, err)

	var found bool
	for _, d := range c.Thresholds {
		if d.Scope == "GET /health" {
			found = true
			require.Equal(t, workload.VerdictUnverified, d.Baseline)
			require.Equal(t, workload.VerdictFail, d.Candidate)
			require.True(t, d.Changed, "something moved and a reader should see it")
			require.False(t, d.Regressed,
				"a check that was never passing has not regressed, and saying it did "+
					"sends somebody looking for a change that does not exist")
		}
	}
	require.True(t, found)
	require.Equal(t, 0, c.Regressed)
}

func TestAThresholdThatUsedToPassAndNoLongerDoesIsARegression(t *testing.T) {
	// Both sides are built here rather than through twoRuns, because the
	// baseline has to be a run where the threshold actually passed. A
	// "regression" measured against a baseline that was already failing is the
	// false finding this counter exists to avoid.
	before := mixRunner()
	before.loadResult.Routes[0].P95Increase = 0.1
	baseline := execute(t, context.Background(), before, workload.Request{
		RunID: "run_base", Kind: "observed_load",
	}, func(o *workload.Options) { o.Branch = "main" })

	after := mixRunner()
	after.loadResult.Routes[0].P95Increase = 4.0
	candidate := execute(t, context.Background(), after, workload.Request{
		RunID: "run_cand", Kind: "observed_load",
	}, func(o *workload.Options) { o.Branch = "feature" })

	c, err := workload.Compare(baseline, candidate)
	require.NoError(t, err)
	require.Equal(t, 1, c.Regressed)

	var found bool
	for _, d := range c.Thresholds {
		if d.Scope == "GET /orders" {
			found = true
			require.Equal(t, workload.VerdictPass, d.Baseline)
			require.Equal(t, workload.VerdictFail, d.Candidate)
			require.True(t, d.Changed)
			require.True(t, d.Regressed)
		}
	}
	require.True(t, found)
}

func TestAComparisonAlwaysSaysWhatItCannotSee(t *testing.T) {
	// A number labelled "regression" that is really machine noise is how a
	// check stops being read.
	baseline, candidate := twoRuns(t, nil)
	c, err := workload.Compare(baseline, candidate)
	require.NoError(t, err)
	require.NotEmpty(t, c.Notes)
	require.Contains(t, strings.Join(c.Notes, "\n"), "not a controlled experiment")
}

func TestAComparisonNamesTheThingsThatMakeItMeaningless(t *testing.T) {
	baseline, candidate := twoRuns(t, nil)

	// Two different manifests means two different safe lists and thresholds.
	baseline.Reproduce.ManifestDigest = "sha256:aaa"
	candidate.Reproduce.ManifestDigest = "sha256:bbb"
	// Two different commands means the runs were not asked for the same thing.
	candidate.Reproduce.Command = "af load run --duration 10s --scale 0.1 --seed 1"
	// And a run that did not complete cannot be differenced honestly.
	candidate.State = workload.StateTimedOut

	c, err := workload.Compare(baseline, candidate)
	require.NoError(t, err)
	joined := strings.Join(c.Notes, "\n")
	require.Contains(t, joined, "different manifests")
	require.Contains(t, joined, "not asked for the same thing")
	require.Contains(t, joined, "did not complete")
}

func TestAMeasureThatNeitherKindHasProducesNoRow(t *testing.T) {
	// A console rendering "requests: null to null" for a browser run would be
	// showing the reader a measurement that does not exist.
	r1, r2 := mixRunner(), mixRunner()
	r1.testReport = onePassingWorkflow()
	r2.testReport = onePassingWorkflow()
	a := execute(t, context.Background(), r1, workload.Request{Kind: "browser_workflow"}, nil)
	b := execute(t, context.Background(), r2, workload.Request{Kind: "browser_workflow"}, nil)

	c, err := workload.Compare(a, b)
	require.NoError(t, err)
	for _, m := range c.Measures {
		require.NotEqual(t, "requests", m.Measure)
		require.NotEqual(t, "p95_ms", m.Measure)
	}
	names := []string{}
	for _, m := range c.Measures {
		names = append(names, m.Measure)
	}
	require.Contains(t, names, "workflows_passed")
}

func TestBothSidesAreNamedSoAReaderCanGoBackToEither(t *testing.T) {
	baseline, candidate := twoRuns(t, nil)
	c, err := workload.Compare(baseline, candidate)
	require.NoError(t, err)
	require.Equal(t, "run_base", c.Baseline.RunID)
	require.Equal(t, "run_cand", c.Candidate.RunID)
	require.Equal(t, "main", c.Baseline.Branch)
	require.Equal(t, "feature", c.Candidate.Branch)
	require.NotEmpty(t, c.Baseline.Command)
	require.Equal(t, workload.ComparisonSchema, c.Schema)
}

func onePassingWorkflow() *env.TestReport {
	return &env.TestReport{Passed: 1, Results: make([]env.WorkflowResult, 1)}
}
