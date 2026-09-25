package env

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/sqlload"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The seam between what a SQL comparison resolved and what each side was
// handed.
//
// Internal, because the thing worth proving is that the two sides cannot
// diverge, and the place they would diverge is one field of a plan that never
// leaves this package. An external test could only run two environments, which
// is a test nobody runs.

// THE DEFECT THIS EXISTS FOR, AND IT IS A SILENT ONE. Three of the four SQL
// knobs have a ZERO THAT IS A CHOICE. Without Resolved, a comparison that
// settled think_time to nothing hands zero down, ResolveSQLLoad reads "the
// caller did not ask", and each side falls back to its OWN manifest. Two sides
// waiting different amounts between transactions is a comparison of two
// workloads, and every number in its report is still a number.
func TestAResolvedPlanNeverFallsBackToTheManifest(t *testing.T) {
	cfg := &schema.LoadSQL{
		Clients: 32, Duration: "5m", Transactions: 900, ThinkTime: "250ms",
	}
	settled := SQLLoadOptions{
		Resolved: true, Clients: 8, Duration: 30 * time.Second,
		Transactions: 0, ThinkTime: 0,
	}
	plan := ResolveSQLLoad(cfg, settled)
	require.Equal(t, 8, plan.Clients)
	require.Equal(t, 30*time.Second, plan.Duration)
	require.Zero(t, plan.Transactions,
		"a settled transaction bound of none fell through to the manifest's 900")
	require.Zero(t, plan.ThinkTime,
		"a settled think time of none fell through to the manifest's 250ms")
}

// And without Resolved the precedence is unchanged, which is what every other
// caller of this function depends on.
func TestTheOrdinaryPrecedenceStillReadsTheManifest(t *testing.T) {
	cfg := &schema.LoadSQL{Clients: 32, Duration: "5m", ThinkTime: "250ms"}
	plan := ResolveSQLLoad(cfg, SQLLoadOptions{})
	require.Equal(t, 32, plan.Clients)
	require.Equal(t, 5*time.Minute, plan.Duration)
	require.Equal(t, 250*time.Millisecond, plan.ThinkTime)

	typed := ResolveSQLLoad(cfg, SQLLoadOptions{Clients: 4})
	require.Equal(t, 4, typed.Clients)
	require.Equal(t, 5*time.Minute, typed.Duration)
}

// The rounds divide the work the manifest asked for. Sixteen rounds of the
// whole transaction count would run sixteen times it.
func TestTheRoundsDivideTheWorkRatherThanRepeatIt(t *testing.T) {
	plan := &SQLLoadPlan{Clients: 8, Duration: 160 * time.Second, Transactions: 320}
	sched := sqlComparePlanFor(LoadCompareOptions{Rounds: 16}, plan)
	require.Equal(t, 16, sched.rounds)
	require.Equal(t, 10*time.Second, sched.perRound)
	require.Equal(t, 20, sched.perRoundTransactions)
	require.Equal(t, 8, sched.clients)
}

// A transaction bound smaller than the number of rounds cannot be divided, and
// one per round is the closest thing to what was asked for. The result says
// what one round carried, so the notes can say what that came to.
func TestATransactionBoundSmallerThanTheRoundsRunsOnePerRound(t *testing.T) {
	plan := &SQLLoadPlan{Clients: 4, Transactions: 5}
	sched := sqlComparePlanFor(LoadCompareOptions{Rounds: 16}, plan)
	require.Equal(t, 1, sched.perRoundTransactions)
	// And a workload sized in transactions alone keeps a per round duration of
	// zero, rather than picking up a time bound it never asked for.
	require.Zero(t, sched.perRound)
}

// A workload with neither bound would run a round that never ends, and the
// notes say so about the comparison rather than about the first round.
func TestAWorkloadWithNoBoundAtAllIsRefused(t *testing.T) {
	plan := &SQLLoadPlan{Clients: 4}
	sched := sqlComparePlanFor(LoadCompareOptions{Rounds: 8}, plan)
	require.Zero(t, sched.perRound)
	require.Zero(t, sched.perRoundTransactions)
}

func sqlNotesFor(r *LoadCompareResult) string {
	out := ""
	for _, n := range sqlCompareNotes(r) {
		out += n + "\n"
	}
	return out
}

// What a SQL comparison cannot see is written for a SQL comparison.
//
// Two of these sentences have no counterpart in the HTTP notes and both are
// the ones this reader needs: a branch is copy on write, so a write heavy
// round measures the branching on whichever side reached the page first, and
// the server's own housekeeping runs on its schedule rather than the
// comparison's.
func TestTheSQLNotesSayWhatIsTrueOfASQLComparison(t *testing.T) {
	notes := sqlNotesFor(&LoadCompareResult{
		SQL: true, Rounds: 16, RoundDuration: 10 * time.Second,
		Warmup: 5 * time.Second, Golden: "golden-2026-09-23",
		Clients: 8, ThinkTime: 0, SQLSource: sqlload.SourceStatementStatistics,
	})
	require.Contains(t, notes, "copy on write")
	require.Contains(t, notes, "autovacuum, the checkpointer and the background writer")
	require.Contains(t, notes, "the two databases diverge as the comparison runs")
	require.Contains(t, notes, "built once on this build from pg_stat_statements")
	require.Contains(t, notes, "8 clients")
	require.Contains(t, notes, "no think time between transactions")
	require.Contains(t, notes, "each side ran 16 rounds of 10s")
	// And it never borrows the HTTP wording, which promises the rows stay the
	// same for the whole run.
	require.NotContains(t, notes, "answered queries over the same rows")
	require.NotContains(t, notes, "the mix in rounds")
}

// A single pass and no warm-up get their own sentences, written from how THIS
// run was sent rather than from how runs are usually sent.
func TestTheSQLNotesDescribeTheRunThatActuallyHappened(t *testing.T) {
	notes := sqlNotesFor(&LoadCompareResult{
		SQL: true, Rounds: 1, RoundDuration: 30 * time.Second,
		Golden: "g", Clients: 8, ThinkTime: 100 * time.Millisecond,
	})
	require.Contains(t, notes, "the two sides were measured once each")
	require.Contains(t, notes, "no warm-up was run")
	require.Contains(t, notes, "100ms of think time")
	require.NotContains(t, notes, "each unit's change is measured round against round")

	odd := sqlNotesFor(&LoadCompareResult{
		SQL: true, Rounds: 7, RoundDuration: time.Second, Golden: "g", Clients: 2,
	})
	require.Contains(t, odd, "the two sides' slots cannot be balanced")

	bounded := sqlNotesFor(&LoadCompareResult{
		SQL: true, Rounds: 4, RoundTransactions: 25, Golden: "g", Clients: 2,
	})
	require.Contains(t, bounded, "each round was bounded at 25 transactions per client")
	require.Contains(t, bounded, "up to 100 transactions per client in total")
	require.Contains(t, bounded, "25 transactions per client")
}

// loadCompareNotes routes to the right set, so an HTTP comparison keeps the
// sentences that are on film and a SQL one never borrows them.
func TestTheNotesAreChosenByWhichWorkloadRan(t *testing.T) {
	http := loadCompareNotes(&LoadCompareResult{
		Rounds: 16, RoundDuration: time.Second, Warmup: time.Second, Golden: "g",
	})
	require.Contains(t, http[0], "each side was sent 16 rounds")
	require.Contains(t, http[len(http)-1], "both sides branched the same golden")

	sql := loadCompareNotes(&LoadCompareResult{
		SQL: true, Rounds: 16, RoundDuration: time.Second, Warmup: time.Second, Golden: "g",
	})
	require.Contains(t, sql[0], "each side ran 16 rounds")
}

// WHAT EACH SIDE IS HANDED, AND THE FACT THAT IT CANNOT DIFFER BY SIDE.
//
// This is the seam the whole comparison rests on. Everything else, the Thue
// Morse order, the discarded warm-up, the per round seed and the pooling, is
// machinery the HTTP comparison already had and already tests. What is new is
// the options one round is sent with, and the way those go wrong is not an
// error: it is two sides running the same mix at different client counts, or
// one side quietly consulting its own manifest, and reporting the difference
// as a regression.
//
// The strongest thing this asserts is structural rather than numeric:
// sqlSendOptions TAKES NO SIDE. There is no argument it could vary by, so the
// two sides are handed the same values by construction rather than by two call
// sites that have to be kept in step.
func TestBothSidesAreHandedTheSameSettledWorkload(t *testing.T) {
	mix := &sqlload.Mix{Source: sqlload.SourceDeclared}
	sched := sqlComparePlan{
		comparePlan: comparePlan{rounds: 16, perRound: 10 * time.Second},
		clients:     12, thinkTime: 40 * time.Millisecond, perRoundTransactions: 20,
	}
	opts := sqlSendOptions(mix, sched, 10*time.Second, 107)

	require.True(t, opts.Resolved,
		"a side that is not told the knobs are settled resolves them against its own manifest")
	require.Same(t, mix, opts.Mix,
		"a side that is not handed the mix builds its own, and a derived mix is read "+
			"from the database it is about to run against")
	require.Equal(t, 12, opts.Clients)
	require.Equal(t, 40*time.Millisecond, opts.ThinkTime)
	require.Equal(t, 20, opts.Transactions)
	require.Equal(t, 10*time.Second, opts.Duration)
	require.Equal(t, int64(107), opts.Seed)

	// The same call with the same arguments is the same options, which is what
	// "both sides" means here: the function has no side to vary on.
	require.Equal(t, opts, sqlSendOptions(mix, sched, 10*time.Second, 107))
}

// A HANDED IN MIX IS RUN AND NOTHING IS READ TO BUILD ONE.
//
// The falsification is the script path: it names a document that does not
// exist, so a plan that built its own mix would fail on it. A plan that uses
// the mix it was handed never opens it. Asserting only that the right mix came
// back would pass against a build that read the document and then discarded
// it, which for a DERIVED mix means a query against the side's own database.
func TestAHandedInMixIsRunAndTheSourceIsNotConsulted(t *testing.T) {
	o := &Orchestrator{opts: Options{Root: "/nonexistent-root-for-this-test"}}
	cfg := &schema.LoadSQL{Script: "no-such-workload.sql"}
	handed := &sqlload.Mix{
		Source: sqlload.SourceDeclared,
		Transactions: []sqlload.Transaction{{
			Name: "one", Weight: 1,
			Statements: []sqlload.Statement{{Label: "select", SQL: "SELECT 1"}},
		}},
	}

	plan, err := o.sqlLoadPlan(context.Background(), cfg, "", SQLLoadOptions{
		Mix: handed, Resolved: true, Clients: 4, Duration: time.Second,
	})
	require.NoError(t, err, "the document was opened although a mix was handed in")
	require.Same(t, handed, plan.Mix)

	// And without one, the same call reaches for the document and says so,
	// which is what proves the arm above was not passing for another reason.
	_, err = o.sqlLoadPlan(context.Background(), cfg, "", SQLLoadOptions{
		Resolved: true, Clients: 4, Duration: time.Second,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no-such-workload.sql")
}
