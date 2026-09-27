package cli

import (
	"fmt"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/sqlload"
	"github.com/antifailure/antifailure/engine/internal/workload"
)

// Rendering a SQL workload comparison.
//
// A SEPARATE RENDERER RATHER THAN A CONDITIONAL INSIDE THE HTTP ONE, and the
// reason is not taste. The HTTP report is on film. Threading a branch through
// renderLoadComparison would put every one of its lines one edit away from
// changing, and the kind of change that matters here is a space or a column
// heading, which no test that only checks for a substring would catch. The two
// renderers share the tail, renderComparisonTail, because that is the part
// where a missing block reads as a clean run.
//
// WHAT THIS TABLE SHOWS THAT THE HTTP ONE DOES NOT. Three percentiles a side
// rather than one. A p95 alone is not a latency distribution, and the shapes
// that separate two storage engines live in the difference between the three:
// a p50 that holds while the p99 doubles is a lock, a checkpoint or a flush,
// and a p95 on its own reports it as a modest slowdown or as nothing.
//
// They are printed as one column per side, "12 / 44 / 98ms", rather than as
// six columns. Six would be ten columns across the table, which wraps on every
// terminal anybody uses, and a wrapped table is a table nobody reads. The
// three numbers in one cell read left to right in the order they grow, which
// is how a distribution is read anyway.

// renderSQLComparison prints a SQL workload comparison for somebody with
// thirty seconds.
func renderSQLComparison(
	e *Env, res *env.LoadCompareResult, c *workload.Comparison,
	judged []workload.ComparisonVerdict, verdict string,
) {
	e.Out.Println("")
	e.Out.Printf("  %s against %s\n", shortRev(res.CandidateRev), shortRev(res.Rev))
	if res.How != "" {
		e.Out.Printf("  the base was resolved %s\n", res.How)
	}

	e.Out.Println("")
	shape := fmt.Sprintf("  %s statements", describeSQLSource(res.SQLSource))
	if res.SQLDescription != "" {
		shape += ", " + res.SQLDescription
	}
	e.Out.Printf("%s, %s on each side.\n", shape,
		plural2Count(res.Clients, "client", "clients"))

	// The observation before the numbers, exactly as `af load sql` puts it
	// there, because it is what says whether the numbers are of a concurrent
	// run at all. Per side, because one side can lose its observer while the
	// other keeps it, and a single line would then describe one run and be
	// read as describing both.
	renderSQLSideObservation(e, "the base branch", res.BaselineSQL)
	renderSQLSideObservation(e, "this build", res.CandidateSQL)

	rows := [][]string{}
	for _, m := range c.Measures {
		rows = append(rows, []string{m.Measure, numberOf(m.Baseline), numberOf(m.Candidate),
			ratioOf(m.Ratio), m.Direction})
	}
	if len(rows) > 0 {
		e.Out.Println("")
		// Capitals, which is the convention Column states and seven of the
		// eight tables in this tree follow. The HTTP comparison's two tables
		// are the eighth and they are in sentence case, so the two reports do
		// not match each other: that table is on film and cannot be changed
		// without a reshoot, and writing a NEW table in the old style to match
		// a frozen one would spread the thing that was wrong about it.
		e.Out.Table([]Column{Flex("MEASURE"), Num("BASE"), Num("THIS BUILD"),
			Num("CHANGE"), Col("MOVED")}, rows)
	}

	// The per unit table, which is the one somebody changing an index came
	// for. A transaction row and then the statements inside it, because a
	// transaction's latency is the sum of several statements and a report that
	// showed only one of the two levels would hide either the lock or the
	// statement that caused it.
	units := [][]string{}
	for _, r := range c.Routes {
		units = append(units, []string{
			sqlUnitName(r), latencySpread(r.P50Baseline, r.P95Baseline, r.P99Baseline),
			latencySpread(r.P50Candidate, r.P95Candidate, r.P99Candidate),
			ratioOf(r.P95Ratio), movedOf(r), resolutionOf(r.Resolution),
		})
	}
	if len(units) > 0 {
		e.Out.Println("")
		// The legend above the table rather than inside the headings, because
		// "BASE P50/P95/P99" and "THIS BUILD P50/P95/P99" are 16 and 22
		// characters of heading over cells of 15, which pushes the table past
		// eighty columns and makes it stack on a narrow terminal. The three
		// numbers are named once, here, and the columns stay the width of
		// their own data.
		e.Out.Println("  Latency is p50 / p95 / p99. The change and the verdict are on the p95.")
		e.Out.Table([]Column{Flex("UNIT"), Num("BASE"), Num("THIS BUILD"),
			Num("P95 CHANGE"), Col("MOVED"), Num("CAN SEE")}, units)
	}

	renderComparisonTail(e, c, judged, verdict)
}

// renderSQLSideObservation prints one side's evidence that its clients really
// overlapped, or says plainly that nobody looked.
//
// "Nobody looked" is a warning rather than a silence, and it is not the same
// answer as "they did not overlap". A run whose observer never connected
// produces a full set of latencies and a throughput, all of them correct about
// a workload that may have been serial, and the only thing that can say so is
// this line.
func renderSQLSideObservation(e *Env, side string, r *sqlload.Result) {
	if r == nil {
		e.Out.Printf("  %s %s measured nothing at all.\n",
			e.Out.S(StyleBad, SymbolFail), side)
		return
	}
	if r.BackendsSeen == nil {
		e.Out.Printf("  %s %s never sampled its own backends: %s.\n",
			e.Out.S(StyleWarn, SymbolWarn), side, r.ObserverNote)
		return
	}
	e.Out.Printf("  %s held %d separate sessions, and the server had %d of them inside a "+
		"transaction at once (%d executing).\n",
		side, *r.BackendsSeen, deref(r.PeakOpenTransactions), deref(r.PeakActiveBackends))
	if r.ClientsStopped > 0 {
		for _, reason := range sortedKeys(r.StoppedBecause) {
			e.Out.Printf("  %s %s: %d clients stopped before the run ended: %s\n",
				e.Out.S(StyleBad, SymbolFail), side, r.StoppedBecause[reason], reason)
		}
	}
}

// sqlUnitName is how one row of the unit table is labelled.
//
// A statement is indented under the transaction that holds it, because the
// two are a hierarchy and a flat list of thirty labels is a list nobody reads
// down. The transaction row itself carries no scenario, which is exactly how
// the comparison keys it, so the shape of the key and the shape of the table
// are the same fact rather than two that have to be kept in step.
func sqlUnitName(r workload.RouteDifference) string {
	if r.Scenario == "" {
		return r.Route
	}
	return "  " + r.Route
}

// latencySpread renders three percentiles as one cell.
//
// A side that recorded none prints "none" rather than "0 / 0 / 0ms", because a
// unit no round ran and a unit that answered instantly are different facts and
// a row of zeros reads as the second. A side that recorded some and not others
// prints a dash in the gap rather than dropping the cell, so the columns stay
// readable down the page.
func latencySpread(p50, p95, p99 *float64) string {
	if p50 == nil && p95 == nil && p99 == nil {
		return "none"
	}
	one := func(v *float64) string {
		if v == nil {
			return "-"
		}
		return fmt.Sprintf("%.3g", *v)
	}
	return one(p50) + " / " + one(p95) + " / " + one(p99) + "ms"
}

// plural2Count renders "8 clients" and "1 client".
//
// A second helper beside plural2, which renders the noun alone, because a
// count with its noun and a noun chosen by a count are used in different
// sentences and folding them would make every call site pass a number it did
// not want printed.
func plural2Count(n int, one, many string) string {
	return fmt.Sprintf("%d %s", n, plural2(n, one, many))
}
