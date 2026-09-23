package cli

import (
	"bytes"
	"os"
	"path/filepath"
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

// TestWhatTheSQLWorkloadCommandPrintsAboutLockContention.
//
// Three outcomes and all three are printed, which is what this test is really
// asserting. Waiting is a finding. Not waiting is a finding. A run nothing
// watched is not a finding at all, and it has to look different from the
// second: a reader shown nothing about lock waits concludes there were none,
// so the absence has to be said out loud.
func TestWhatTheSQLWorkloadCommandPrintsAboutLockContention(t *testing.T) {
	waits, waitMS, seen := 6, 3600.0, 3
	res := &sqlload.Result{
		Source: sqlload.SourceDeclared, Clients: 3,
		Transactions: 40, Duration: 3 * time.Second, TPS: 13.3,
		Refused: []sqlload.Refused{}, BackendsSeen: &seen,
		LockWaits: &waits, LockWaitMS: &waitMS, LockWaitNote: sqlload.LockWaitBound,
		LockWaitPairs: []sqlload.LockWait{
			{
				BlockedTransaction: "bump the counter", BlockedStatement: "take the row",
				BlockingTransaction: "bump the counter", BlockingStatement: "hold it",
				BlockingState: "active", BlockingInRun: true,
				LockType: "transactionid", Mode: "ShareLock", Waits: 4, WaitedMS: 3600,
			},
			{
				BlockedTransaction: "bump the counter", BlockedStatement: "take the row",
				BlockingState: "idle in transaction", BlockingInRun: false,
				Relation: "counters", LockType: "tuple", Mode: "ExclusiveLock",
				Waits: 2, WaitedMS: 400,
			},
		},
	}

	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf)}
	printSQLLoad(e, res, nil)
	printed := buf.String()
	t.Logf("\n%s", printed)

	require.Contains(t, printed,
		"6 times a client of this run queued for a lock, 3.6s of waiting between them across 3 backends.")
	// Both statements, in the mix's own words. A pid here would be a number
	// nobody can act on and would not line up between two runs.
	require.Contains(t, printed, "bump the counter / take the row")
	require.Contains(t, printed, "bump the counter / hold it")
	require.Contains(t, printed, "queued on transactionid, ShareLock, 4 times, 3.6s")
	// The holder outside the run says so rather than being drawn as one of
	// this mix's statements, which would send a reader to fix a mix that is
	// not the problem.
	require.Contains(t, printed, "waited on another session on this database, idle in transaction")
	require.Contains(t, printed, "queued on tuple on counters, ExclusiveLock, 2 times, 400ms")
	// Every name arrives whole. The first draft of this block was a five
	// column table and at eighty columns it printed "bump the counter..." in
	// every cell, which is a run that says it blocked and will not say on
	// what.
	require.NotContains(t, printed, "...",
		"a pair was clipped, so the thing a reader came for is the part that was cut")
	require.Contains(t, printed, "pg_blocking_pids",
		"the numbers printed without saying what the sampling could not see")

	// And the documented block is THIS output rather than somebody's
	// impression of it.
	//
	// Worth its own assertion because nothing else could say no about it. The
	// first draft of this renderer was a table, the concepts page was written
	// against that table, the renderer became a list, and every gate stayed
	// green over a page showing output the product does not produce.
	// docexamples checks that commands in the documentation exist and
	// figurecheck checks that figures have a source; neither reads a fenced
	// block of program output.
	requireDocumentedBlock(t, "The contention it was under", printed)
}

// requireDocumentedBlock asserts every line of the first fenced block under a
// heading in the SQL workloads page appears in what the renderer produced.
//
// Line by line rather than as one string, so a page that has drifted names the
// line that drifted instead of printing two screens of output and leaving the
// reader to diff them.
func requireDocumentedBlock(t *testing.T, heading, printed string) {
	t.Helper()
	page, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "docs", "src", "content", "docs", "concepts", "sql-workloads.md"))
	require.NoError(t, err, "the page this block is quoted from is not in the tree")

	rest := string(page)
	at := strings.Index(rest, "### "+heading)
	require.Positive(t, at, "the page has no %q section, so this test is checking nothing", heading)
	rest = rest[at:]

	open := strings.Index(rest, "```")
	require.Positive(t, open, "the section quotes no output block")
	rest = rest[open+3:]
	rest = rest[strings.Index(rest, "\n")+1:]
	end := strings.Index(rest, "```")
	require.Positive(t, end, "the block is not closed")

	lines := 0
	for _, line := range strings.Split(rest[:end], "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines++
		require.Contains(t, printed, line,
			"the page shows a line this renderer does not produce")
	}
	require.GreaterOrEqual(t, lines, 4,
		"fewer lines were compared than the block has, so this check could pass over nothing")
}

// TestWhatTheSQLWorkloadCommandPrintsWhenNothingQueuedAndWhenNobodyLooked.
//
// The pair of lines that must never be confused, asserted together so that a
// renderer which collapsed them fails here rather than in front of somebody
// reading a clean bill of health off a run nothing measured.
func TestWhatTheSQLWorkloadCommandPrintsWhenNothingQueuedAndWhenNobodyLooked(t *testing.T) {
	none, noneMS, seen := 0, 0.0, 3
	quiet := &sqlload.Result{
		Source: sqlload.SourceDeclared, Clients: 3, Transactions: 900,
		Duration: 2 * time.Second, Refused: []sqlload.Refused{}, BackendsSeen: &seen,
		LockWaits: &none, LockWaitMS: &noneMS, LockWaitPairs: []sqlload.LockWait{},
		LockWaitNote: sqlload.LockWaitBound,
	}
	var quietBuf bytes.Buffer
	printSQLLoad(&Env{Out: NewOutput(&quietBuf, &quietBuf)}, quiet, nil)
	quietOut := quietBuf.String()
	t.Logf("\n%s", quietOut)
	require.Contains(t, quietOut, "No client of this run was ever seen waiting for a lock.")
	require.Contains(t, quietOut, "Sampled every 200ms",
		"a zero printed with no statement of the resolution it was measured at")
	require.NotContains(t, quietOut, "not measured")

	unwatched := &sqlload.Result{
		Source: sqlload.SourceDeclared, Clients: 3, Transactions: 900,
		Duration: 2 * time.Second, Refused: []sqlload.Refused{}, BackendsSeen: &seen,
		LockWaitNote: "nothing watched the wait queues, so this run says nothing about " +
			"whether it blocked, which is not the same as having found no contention",
	}
	var unwatchedBuf bytes.Buffer
	printSQLLoad(&Env{Out: NewOutput(&unwatchedBuf, &unwatchedBuf)}, unwatched, nil)
	unwatchedOut := unwatchedBuf.String()
	t.Logf("\n%s", unwatchedOut)
	require.Contains(t, unwatchedOut, "Lock waits were not measured")
	require.Contains(t, unwatchedOut, "not the same as having found no contention")
	require.NotContains(t, unwatchedOut, "No client of this run was ever seen waiting",
		"an unwatched run was drawn as a run that never queued")
}
