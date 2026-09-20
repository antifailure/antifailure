package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/sqlload"
)

// What `af load sql` prints, over numbers a real run really produced.
//
// The numbers below are not invented and not rounded for the page. They are the
// measurements from one run of this engine's own suite against a Postgres 18
// container, the one TestTheClientsReallyHoldSeparateSessionsAndOverlapInside
// TheServer logged: eight clients, eight distinct backends, a peak of seven
// inside a transaction, 231 committed transactions in 3.08 seconds. Putting
// them through the command's own renderer is what makes the block in
// docs/src/content/docs/concepts/sql-workloads.md the real output rather than
// somebody's impression of it, and this test is what keeps the two together:
// change the renderer and the assertions below name the line that moved.
//
// The three things asserted are the three a reader of that block is entitled
// to believe. The observation is stated before the numbers, because it is what
// says whether the numbers are of a concurrent run at all. The throughput is
// committed transactions only. And the rows the statements touched are on the
// same line as the statement count, because a run that touched none measured
// the cost of finding nothing.
func TestWhatTheSQLWorkloadCommandPrints(t *testing.T) {
	peakExecuting, peakOpen, seen := 5, 7, 8
	res := &sqlload.Result{
		Source: sqlload.SourceDeclared, Clients: 8,
		Transactions: 231, TransactionsFailed: 0, Retries: 0,
		Statements: 281, Rows: 1193,
		Duration: 3081730958 * time.Nanosecond, TPS: 75.0,
		Overall: load.Latency{P50Ms: 69.961, P90Ms: 246.2, P95Ms: 340.984, P99Ms: 511.672, MaxMs: 623.891},
		PerStatement: []sqlload.StatementResult{
			{Transaction: "a merchant page", Label: "orders for a merchant",
				Executed: 48, Rows: 960, Latency: load.Latency{P95Ms: 235.553}},
			{Transaction: "a merchant page", Label: "the merchant",
				Executed: 47, Rows: 47, Latency: load.Latency{P95Ms: 187.039}},
			{Transaction: "read one order", Label: "order by id",
				Executed: 186, Rows: 186, Latency: load.Latency{P95Ms: 121.166}},
		},
		Refused:              []sqlload.Refused{},
		PeakActiveBackends:   &peakExecuting,
		PeakOpenTransactions: &peakOpen,
		BackendsSeen:         &seen,
	}

	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf)}
	// The section header too, because the command prints it and the block in
	// the documentation is the whole of what a person sees.
	e.Out.Section("Running a SQL workload")
	printSQLLoad(e, res, &env.SQLLoadPlan{Clients: 8, Description: "the read path a storefront runs"})
	printed := buf.String()
	t.Logf("\n%s", printed)

	require.Contains(t, printed,
		"8 clients held 8 separate sessions, and the server had 7 of them inside a transaction at once (5 executing).")
	require.Contains(t, printed,
		"231 transactions committed in 3.082s at 75.0 a second, 0 failed, 0 retried.")
	require.Contains(t, printed, "281 statements touched 1193 rows.")
	require.Contains(t, printed, "declared statements, the read path a storefront runs.")

	// Slowest statement first, because that is the line somebody changing an
	// index is looking for and scrolling to find it is the same as not showing
	// it. The table is not sorted here: the engine sorts it, so an unsorted
	// table means the sort was lost between the run and the page.
	merchant := strings.Index(printed, "orders for a merchant")
	order := strings.Index(printed, "order by id")
	require.Positive(t, merchant)
	require.Positive(t, order)
	require.Less(t, merchant, order, "the slowest statement is not first in the table")
}

// TestWhatTheSQLWorkloadCommandPrintsWhenItCommittedNothing.
//
// The other block worth pinning, because it is the one a reader most needs to
// be able to tell apart from a fast run. Zero transactions, zero throughput and
// zero latency are what the tiles would show, and the sentence under them is
// the only thing that says those three zeros are not a result.
func TestWhatTheSQLWorkloadCommandPrintsWhenItCommittedNothing(t *testing.T) {
	seen, peak := 2, 0
	res := &sqlload.Result{
		Source: sqlload.SourceDeclared, Clients: 2,
		Transactions: 0, TransactionsFailed: 40, StatementsFailed: 40,
		Duration: 812 * time.Millisecond, ErrorRate: 1,
		Errors:               map[string]int{"SQLSTATE 22012": 40},
		Refused:              []sqlload.Refused{},
		PeakActiveBackends:   &peak,
		PeakOpenTransactions: &peak,
		BackendsSeen:         &seen,
	}

	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf)}
	e.Out.Section("Running a SQL workload")
	printSQLLoad(e, res, nil)
	printed := buf.String()
	t.Logf("\n%s", printed)

	require.Contains(t, printed, "0 transactions committed")
	require.Contains(t, printed, "This run committed nothing")
	require.Contains(t, printed, "all 40 transaction attempts failed")
	require.Contains(t, printed, "SQLSTATE 22012")

	// And the exit code says the same thing, because a person reading the
	// sentence is not the only reader: a pipeline reads the status.
	verdict := sqlLoadExit(res, nil, 0)
	require.Error(t, verdict)
	require.Contains(t, verdict.Error(), "AF-LOD-021")
}
