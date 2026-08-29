package insights_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func ptr(b bool) *bool { return &b }

func TestConfigure_NoBlockMeansEveryCheckIsOn(t *testing.T) {
	t.Parallel()
	// A project that has said nothing about insights gets them. Defaulting a
	// check off would make the feature exist only for people who read the
	// manifest reference.
	c := insights.Configure(nil)
	require.True(t, c.Enabled)
	require.True(t, c.MigrationRehearsal)
	require.True(t, c.QueryRegression)
	require.True(t, c.PlanDiff)
	require.EqualValues(t, insights.DefaultRegressionFactor, c.RegressionFactor)
	require.EqualValues(t, insights.DefaultRegressionMinMS, c.RegressionMinMS)
	require.Equal(t, insights.LargeTableRows, c.LargeTableRows)
}

func TestConfigure_UnsetIsNotFalse(t *testing.T) {
	t.Parallel()
	// The whole reason every field is a *bool. A block that sets only the
	// factor must not turn three checks off.
	c := insights.Configure(&schema.Insights{RegressionFactor: 5})
	require.True(t, c.MigrationRehearsal)
	require.True(t, c.QueryRegression)
	require.True(t, c.PlanDiff)
	require.Equal(t, 5.0, c.RegressionFactor)
}

func TestConfigure_FalseTurnsOffExactlyOneCheck(t *testing.T) {
	t.Parallel()
	// The lane's one hard requirement: plan_diff: false turns off that check
	// and nothing else.
	c := insights.Configure(&schema.Insights{PlanDiff: ptr(false)})
	require.False(t, c.PlanDiff)
	require.True(t, c.MigrationRehearsal)
	require.True(t, c.QueryRegression)
	require.True(t, c.Enabled)

	c = insights.Configure(&schema.Insights{MigrationRehearsal: ptr(false)})
	require.False(t, c.MigrationRehearsal)
	require.True(t, c.PlanDiff)
	require.True(t, c.QueryRegression)

	c = insights.Configure(&schema.Insights{QueryRegression: ptr(false)})
	require.False(t, c.QueryRegression)
	require.True(t, c.PlanDiff)
	require.True(t, c.MigrationRehearsal)
}

func TestConfigure_EnabledFalseTurnsOffEverything(t *testing.T) {
	t.Parallel()
	c := insights.Configure(&schema.Insights{Enabled: ptr(false)})
	require.False(t, c.Enabled)
}

func TestConfigure_ExplicitTrueSurvives(t *testing.T) {
	t.Parallel()
	c := insights.Configure(&schema.Insights{PlanDiff: ptr(true), Enabled: ptr(true)})
	require.True(t, c.PlanDiff)
}

func TestConfigure_ZeroThresholdsFallBackToTheDefaults(t *testing.T) {
	t.Parallel()
	// A float64 cannot tell unset from zero, and a factor of zero would
	// report every query in the database as a regression.
	c := insights.Configure(&schema.Insights{})
	require.EqualValues(t, insights.DefaultRegressionFactor, c.RegressionFactor)
	require.EqualValues(t, insights.DefaultRegressionMinMS, c.RegressionMinMS)
	require.Equal(t, insights.LargeTableRows, c.LargeTableRows)
}

func TestThresholds_CarryTheManifestsFigures(t *testing.T) {
	t.Parallel()
	c := insights.Configure(&schema.Insights{RegressionFactor: 3, RegressionMinMS: 25})
	th := c.Thresholds()
	require.Equal(t, 3.0, th.CallGrowth)
	require.Equal(t, 3.0, th.TimeGrowth)
	require.Equal(t, 25.0, th.MinMS)
}

func TestCompareTo_MinMSSuppressesAFastQueryThatTripled(t *testing.T) {
	t.Parallel()
	// The reason regression_min_ms exists. 0.1ms to 0.3ms is three times
	// slower and means nothing, and a report that is all noise is one people
	// stop reading.
	diff := report(insights.Query{Text: "SELECT 1", Calls: 10, MeanMs: 0.3}).
		CompareTo(report(insights.Query{Text: "SELECT 1", Calls: 10, MeanMs: 0.1}),
			insights.Thresholds{CallGrowth: 2, TimeGrowth: 2, MinMS: 10})
	require.True(t, diff.Empty())
}

func TestCompareTo_MinMSStillReportsARealRegression(t *testing.T) {
	t.Parallel()
	// The other half. A floor that suppresses the finding it exists to show
	// is worse than no floor.
	diff := report(insights.Query{Text: "SELECT 1", Calls: 10, MeanMs: 240}).
		CompareTo(report(insights.Query{Text: "SELECT 1", Calls: 10, MeanMs: 3}),
			insights.Thresholds{CallGrowth: 2, TimeGrowth: 2, MinMS: 10})
	require.Len(t, diff.Slower, 1)
}

func TestCompareTo_MinMSDoesNotSuppressAnNPlusOne(t *testing.T) {
	t.Parallel()
	// The floor is about time per call. Four hundred calls of a fast query is
	// the bug the whole feature exists for and it must survive any floor.
	diff := report(insights.Query{Text: "SELECT * FROM items WHERE order_id = $1", Calls: 412, MeanMs: 0.4}).
		CompareTo(report(insights.Query{Text: "SELECT * FROM items WHERE order_id = $1", Calls: 4, MeanMs: 0.4}),
			insights.Thresholds{CallGrowth: 2, TimeGrowth: 2, MinMS: 1000})
	require.Len(t, diff.Busier, 1)
}

func TestFull_CleanIsFalseWhenAnyCheckFoundSomething(t *testing.T) {
	t.Parallel()
	require.True(t, insights.Full{}.Clean())
	require.False(t, insights.Full{
		Rehearsal: &insights.Rehearsal{Failed: true},
	}.Clean())
	require.False(t, insights.Full{
		Rehearsal: &insights.Rehearsal{Lint: []insights.LintFinding{{Rule: insights.RuleDropColumnInView}}},
	}.Clean())
	require.False(t, insights.Full{
		PlanFindings: []insights.PlanFinding{{Kind: insights.PlanNewSeqScan}},
	}.Clean())
	require.False(t, insights.Full{
		Regression: &insights.Diff{NewQueries: []insights.Query{{Text: "x"}}},
	}.Clean())
	require.True(t, insights.Full{Regression: &insights.Diff{}}.Clean())
}

func TestExplain_NamesTheChecksTheManifestTurnedOff(t *testing.T) {
	t.Parallel()
	// A report that silently omits a check reads exactly like a check that
	// found nothing, which is the difference between a clean bill of health
	// and no examination.
	out := insights.Full{Off: []string{"the plan diff, because insights.plan_diff is false"}}.Explain()
	require.Contains(t, out, "Turned off in the manifest:")
	require.Contains(t, out, "plan_diff")
}

func TestExplain_LeadsWithAMigrationThatDidNotApply(t *testing.T) {
	t.Parallel()
	// The worst thing in the report is a deploy that will fail, so somebody
	// who reads only the first line has read the most important one.
	out := insights.Full{Rehearsal: &insights.Rehearsal{
		Failed: true, Error: `relation "users_email_key" already exists`,
	}}.Explain()
	require.True(t, len(out) > 0)
	require.Contains(t, out[:60], "did not apply")
}

func TestCompareTo_MatchesOnTheQueryIdRatherThanTheText(t *testing.T) {
	t.Parallel()
	// Proved against a real Postgres 17 and not invented: the same statement,
	// run four times and then four hundred, came back as "... LIMIT $2" the
	// first time and "... LIMIT 1" the second, with an identical queryid.
	// Matching on text reported the busiest query in the database as brand new
	// and found no N+1 at all.
	diff := report(insights.Query{
		ID: 4672296084596538411, Text: "SELECT id FROM orders WHERE user_id = $1 LIMIT 1",
		Calls: 412, MeanMs: 1.2,
	}).CompareTo(report(insights.Query{
		ID: 4672296084596538411, Text: "SELECT id FROM orders WHERE user_id = $1 LIMIT $2",
		Calls: 4, MeanMs: 1.2,
	}), insights.Thresholds{})

	require.Empty(t, diff.NewQueries, "the same statement must not be reported as a new one")
	require.Len(t, diff.Busier, 1)
	require.InDelta(t, 103, diff.Busier[0].Factor, 1)
}

func TestCompareTo_FallsBackToTheTextWhenThereIsNoQueryId(t *testing.T) {
	t.Parallel()
	// A server too old to report a queryid, or a row the extension could not
	// attribute. The comparison degrades rather than stopping.
	diff := report(insights.Query{Text: "SELECT 1", Calls: 412, MeanMs: 1}).
		CompareTo(report(insights.Query{Text: "SELECT 1", Calls: 4, MeanMs: 1}),
			insights.Thresholds{})
	require.Len(t, diff.Busier, 1)
}

func TestCompareTo_DoesNotConflateTwoStatementsWithNoQueryId(t *testing.T) {
	t.Parallel()
	// Zero is "not reported", not an identifier, so two statements without one
	// must not collapse into each other.
	diff := report(
		insights.Query{Text: "SELECT a", Calls: 10, MeanMs: 1},
		insights.Query{Text: "SELECT b", Calls: 10, MeanMs: 1},
	).CompareTo(report(
		insights.Query{Text: "SELECT a", Calls: 10, MeanMs: 1},
	), insights.Thresholds{})
	require.Len(t, diff.NewQueries, 1)
	require.Equal(t, "SELECT b", diff.NewQueries[0].Text)
}

func TestDefaults_AgreeWithTheManifestsOwn(t *testing.T) {
	t.Parallel()
	// Two packages fill in the same three blanks: the manifest normaliser,
	// for a manifest read off disk, and Configure, for a block that reached
	// the engine some other way. They disagreed, and the way that showed up
	// was `af explain` printing "above 1.5x and 5 ms" for thresholds the code
	// would have applied as 2 and 10. Whichever pair is right, there cannot
	// be two.
	require.Equal(t, manifest.DefaultRegressionFac, insights.DefaultRegressionFactor)
	require.EqualValues(t, manifest.DefaultRegressionMS, insights.DefaultRegressionMinMS)
	require.Equal(t, manifest.DefaultLargeTable, insights.LargeTableRows)

	c := insights.Configure(nil)
	require.Equal(t, manifest.DefaultRegressionFac, c.RegressionFactor)
	require.EqualValues(t, manifest.DefaultRegressionMS, c.RegressionMinMS)
	require.Equal(t, manifest.DefaultLargeTable, c.LargeTableRows)
}
