package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// af ci threw the error rate threshold away.
//
// `p95, _ := o.Thresholds()` and then `res.Breaches(p95, 0)`. Breaches short
// circuits on `errorRate > 0`, so a zero limit builds no error rate breach at
// all: a change that failed every request under load produced an empty
// Regressed list, never reached policy.load_regression, and merged green,
// while `af load run` on the same manifest and the same result exited non
// zero.
//
// Invisible for a specific reason. `p95_increase` is refused under the
// access_log and none sources, so those projects passed (0, 0) into a
// comparison with nothing to compare, and this repository's own manifest is
// `source: none` with `error_rate: 0.02`.
func failingRun() *load.Result {
	r := &load.Result{Sent: 500, Rate: 50, ErrorRate: 1.0}
	r.Overall.P95Ms = 120
	return r
}

func TestLoadReport_EveryRequestFailingIsARegression(t *testing.T) {
	t.Parallel()
	var run report.Run
	// What a `source: none` project passes: no p95 threshold, because the
	// schema refuses one there, and the error rate the manifest does set.
	l := loadReport(failingRun(), 0, 0.02, &run)
	require.NotNil(t, l)
	require.Contains(t, l.Regressed, "error rate",
		"100 percent of requests failed against a 2 percent limit")
}

// And the gate has to act on it, or the breach is another fact that reaches
// nothing. This is the half that makes it fail the check rather than appear in
// a list.
func TestLoadReport_TheBreachReachesTheVerdict(t *testing.T) {
	t.Parallel()
	var run report.Run
	run.Load = loadReport(failingRun(), 0, 0.02, &run)
	f := loadFinding(run.Load, report.Policy{LoadRegression: report.LevelFail})
	require.NotNil(t, f, "a breach the policy block never sees is not a gate")
	require.Equal(t, report.LevelFail, f.Level)

	run.Findings = append(run.Findings, *f)
	require.Equal(t, report.VerdictFail, run.Verdict())
}

// A run inside the limit is not a regression, so the tests above are not
// passing because everything is.
func TestLoadReport_AHealthyRunIsNotARegression(t *testing.T) {
	t.Parallel()
	var run report.Run
	r := &load.Result{Sent: 500, Rate: 50, ErrorRate: 0.001}
	r.Overall.P95Ms = 120
	l := loadReport(r, 0, 0.02, &run)
	require.Empty(t, l.Regressed)
	require.Nil(t, loadFinding(l, report.Policy{LoadRegression: report.LevelFail}))
}

// A threshold in force that measured nothing is not a threshold that held. af
// load run has said so for a while and af ci said nothing at all, so a report
// that could not compare read exactly like one that compared and was happy.
func TestLoadReport_SaysWhenP95HadNothingToCompareAgainst(t *testing.T) {
	t.Parallel()
	var run report.Run
	r := &load.Result{Sent: 500, Rate: 50, ErrorRate: 0.001}
	r.Overall.P95Ms = 120
	r.Routes = []load.RouteResult{{Route: "GET /", HasBaseline: false}}
	loadReport(r, 1.5, 0.02, &run)
	require.Len(t, run.Notes, 1)
	require.Contains(t, run.Notes[0], "p95_increase")
}
