package insights_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
)

func report(queries ...insights.Query) insights.Report {
	return insights.Report{Queries: queries}
}

func TestCompareTo_FindsAQueryThisBranchRunsAndTheBaselineDidNot(t *testing.T) {
	t.Parallel()
	diff := report(
		insights.Query{Text: "SELECT * FROM users WHERE id = $1", Calls: 4, MeanMs: 1},
		insights.Query{Text: "SELECT * FROM audit_log", Calls: 1, MeanMs: 80},
	).CompareTo(report(
		insights.Query{Text: "SELECT * FROM users WHERE id = $1", Calls: 4, MeanMs: 1},
	), insights.Thresholds{})

	require.Len(t, diff.NewQueries, 1)
	require.Contains(t, diff.NewQueries[0].Text, "audit_log")
}

func TestCompareTo_FindsTheNPlusOne(t *testing.T) {
	t.Parallel()
	// The bug no test catches, because the test passes. Four hundred calls of
	// a two millisecond query is correct and slow, and correct and slow is
	// what takes a site down under load rather than in review.
	diff := report(
		insights.Query{Text: "SELECT * FROM items WHERE order_id = $1", Calls: 412, MeanMs: 2},
	).CompareTo(report(
		insights.Query{Text: "SELECT * FROM items WHERE order_id = $1", Calls: 4, MeanMs: 2},
	), insights.Thresholds{})

	require.Len(t, diff.Busier, 1)
	require.InDelta(t, 103, diff.Busier[0].Factor, 1)
	require.Equal(t, 4.0, diff.Busier[0].Before)
	require.Equal(t, 412.0, diff.Busier[0].After)
	require.Empty(t, diff.Slower, "each call is the same speed; there are just far more of them")
}

func TestCompareTo_FindsAQueryThatGotSlower(t *testing.T) {
	t.Parallel()
	// The index somebody stopped using. Same number of calls, each one worse.
	diff := report(
		insights.Query{Text: "SELECT * FROM orders WHERE email = $1", Calls: 10, MeanMs: 240},
	).CompareTo(report(
		insights.Query{Text: "SELECT * FROM orders WHERE email = $1", Calls: 10, MeanMs: 3},
	), insights.Thresholds{})

	require.Len(t, diff.Slower, 1)
	require.InDelta(t, 80, diff.Slower[0].Factor, 1)
	require.Empty(t, diff.Busier)
}

func TestCompareTo_IgnoresSmallChanges(t *testing.T) {
	t.Parallel()
	// A ratio rather than an absolute, for the same reason the load report
	// uses one: two milliseconds becoming eight is a quadrupling worth seeing
	// and six milliseconds of change is not.
	diff := report(
		insights.Query{Text: "SELECT 1", Calls: 5, MeanMs: 1.1},
	).CompareTo(report(
		insights.Query{Text: "SELECT 1", Calls: 4, MeanMs: 1.0},
	), insights.Thresholds{})
	require.True(t, diff.Empty())
}

func TestCompareTo_WorstFirst(t *testing.T) {
	t.Parallel()
	// Somebody scrolling to find the worst one is the same as not showing it.
	diff := report(
		insights.Query{Text: "a", Calls: 20, MeanMs: 1},
		insights.Query{Text: "b", Calls: 400, MeanMs: 1},
	).CompareTo(report(
		insights.Query{Text: "a", Calls: 10, MeanMs: 1},
		insights.Query{Text: "b", Calls: 4, MeanMs: 1},
	), insights.Thresholds{})

	require.Len(t, diff.Busier, 2)
	require.Equal(t, "b", diff.Busier[0].Text)
}

func TestScan_RatioIsTheShareReadEndToEnd(t *testing.T) {
	t.Parallel()
	require.InDelta(t, 0.9, insights.Scan{SeqScans: 90, IndexScans: 10}.Ratio(), 0.001)
	require.Zero(t, insights.Scan{}.Ratio(), "a table nothing read is not a finding")
}

func TestExplain_SaysWhatItCouldNotMeasure(t *testing.T) {
	t.Parallel()
	// An insight that silently reports nothing because an extension is missing
	// is worse than one that says so: the first looks like a clean bill of
	// health.
	r := insights.Report{Missing: []string{"query statistics need pg_stat_statements"}}
	require.Contains(t, r.Explain(), "Not measured:")
	require.Contains(t, r.Explain(), "pg_stat_statements")
}

func TestExplain_SaysWhyAnUnusedIndexMightNotBeAFinding(t *testing.T) {
	t.Parallel()
	// Advice that would break a schema if taken is worse than no advice.
	r := insights.Report{Unused: []insights.Index{
		{Table: "orders", Name: "idx_orders_status", Scans: 0, Size: "12 MB"},
	}}
	out := r.Explain()
	require.Contains(t, out, "idx_orders_status")
	require.Contains(t, out, "did not run here")
}

func TestDiff_EmptyMeansNothingGotWorse(t *testing.T) {
	t.Parallel()
	require.True(t, insights.Diff{}.Empty())
	require.False(t, insights.Diff{NewQueries: []insights.Query{{Text: "x"}}}.Empty())
}
