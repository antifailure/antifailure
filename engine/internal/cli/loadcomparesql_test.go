package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/sqlload"
	"github.com/antifailure/antifailure/engine/internal/workload"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// What `af load compare --sql` refuses, prints and publishes.

func sqlManifest() *schema.Manifest {
	return &schema.Manifest{Load: &schema.Load{SQL: &schema.LoadSQL{Script: "workload.sql"}}}
}

// --sql without a load.sql block asks for a workload that does not exist, and
// it is the same fact `af load sql` refuses with, under the same code.
func TestTheSQLComparisonIsRefusedWithoutALoadSQLBlock(t *testing.T) {
	cmd := newLoadCompareCommand(&Env{})
	require.NoError(t, cmd.ParseFlags([]string{"--sql"}))

	err := checkSQLCompareFlags(cmd.Flags(), true, &schema.Manifest{})
	require.Error(t, err)
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, aferrors.AFLOD017, coded.Code())
	require.Contains(t, err.Error(), "declares no load.sql block")

	require.NoError(t, checkSQLCompareFlags(cmd.Flags(), true, sqlManifest()))
}

// A SQL knob typed without --sql would be read and used for nothing. It is
// refused rather than ignored: the same flag SET and IGNORED is how load.scale
// spent months unreachable from the command line.
func TestASQLKnobWithoutTheSQLFlagIsRefusedRatherThanIgnored(t *testing.T) {
	for _, knob := range [][]string{
		{"--concurrency", "32"},
		{"--transactions", "500"},
		{"--think-time", "50ms"},
	} {
		cmd := newLoadCompareCommand(&Env{})
		require.NoError(t, cmd.ParseFlags(knob))
		err := checkSQLCompareFlags(cmd.Flags(), false, sqlManifest())
		require.Error(t, err, "%v", knob)
		require.Contains(t, err.Error(), "read and used for nothing", "%v", knob)
		// And with --sql the same knob is accepted.
		require.NoError(t, checkSQLCompareFlags(cmd.Flags(), true, sqlManifest()), "%v", knob)
	}
	// An untyped knob holds its default and is not a choice, so a plain HTTP
	// comparison is not refused by the default value of --concurrency.
	plain := newLoadCompareCommand(&Env{})
	require.NoError(t, plain.ParseFlags(nil))
	require.NoError(t, checkSQLCompareFlags(plain.Flags(), false, sqlManifest()))
}

// --scale is a fraction of production's arrival rate and the SQL workload has
// none. Accepting it silently would report a run under a number the reader
// believes they set.
func TestScaleIsRefusedWithTheSQLWorkload(t *testing.T) {
	cmd := newLoadCompareCommand(&Env{})
	require.NoError(t, cmd.ParseFlags([]string{"--sql", "--scale", "0.5"}))
	err := checkSQLCompareFlags(cmd.Flags(), true, sqlManifest())
	require.Error(t, err)
	require.Contains(t, err.Error(), "arrival rate")
	require.Contains(t, err.Error(), "--concurrency")
}

// A flag holds its default whether or not anybody set it, so a default passed
// down as a choice makes the manifest key it falls back on unreachable.
func TestAnUntypedSQLKnobIsNotPassedDownAsAChoice(t *testing.T) {
	cmd := newLoadCompareCommand(&Env{})
	require.NoError(t, cmd.ParseFlags([]string{"--sql"}))
	require.Zero(t, changedInt(cmd.Flags(), "concurrency", 8),
		"the default 8 was handed down as a choice and load.sql.clients became unreachable")
	require.Zero(t, changedDuration(cmd.Flags(), "think-time", 0))

	typed := newLoadCompareCommand(&Env{})
	require.NoError(t, typed.ParseFlags([]string{"--sql", "--concurrency", "32",
		"--think-time", "40ms"}))
	require.Equal(t, 32, changedInt(typed.Flags(), "concurrency", 32))
	require.Equal(t, 40*time.Millisecond,
		changedDuration(typed.Flags(), "think-time", 40*time.Millisecond))
}

// Round k of the base is paired with round k of this build, keyed by the unit
// rather than by the label, and a unit a round did not run is absent rather
// than zero.
func TestSQLRoundsArePairedByUnitAndAZeroIsAbsent(t *testing.T) {
	side := func(p95 float64) *sqlload.Result {
		return &sqlload.Result{
			PerTransaction: []sqlload.TransactionResult{
				{Name: "read", Executed: 10, Latency: load.Latency{P95Ms: p95}},
				{Name: "never", Executed: 0, Latency: load.Latency{P95Ms: 0}},
			},
			PerStatement: []sqlload.StatementResult{
				{Transaction: "read", Label: "by id", Executed: 10,
					Latency: load.Latency{P95Ms: p95 / 2}},
				{Transaction: "write", Label: "by id", Executed: 10,
					Latency: load.Latency{P95Ms: p95 * 4}},
			},
		}
	}
	res := &env.LoadCompareResult{
		BaselineSQLRounds:  []*sqlload.Result{side(10), side(11), side(12)},
		CandidateSQLRounds: []*sqlload.Result{side(20), side(21)},
	}
	got := sqlRoundP95s(res)
	require.Len(t, got, 2, "an unpaired trailing round is not a pair")
	require.Equal(t, 10.0, got[0].Base["read"])
	require.Equal(t, 20.0, got[0].Candidate["read"])
	// The two "by id" statements live in two transactions and are two keys.
	require.Equal(t, 5.0, got[0].Base["read by id"])
	require.Equal(t, 40.0, got[0].Base["write by id"])
	_, present := got[0].Base["never"]
	require.False(t, present, "a transaction that ran nothing entered as an infinitely fast round")
	_, present = got[0].Base["by id"]
	require.False(t, present, "a statement was keyed by its label alone and two were pooled")
}

// The SQL document carries what a difference cannot: where the mix came from,
// how many clients ran, and each side's own evidence that they overlapped.
func TestTheSQLDocumentCarriesTheEvidenceADifferenceCannot(t *testing.T) {
	open, seen := 7, 8
	res := &env.LoadCompareResult{
		SQL: true, SQLSource: sqlload.SourceDeclared, SQLDescription: "a checkout",
		Clients: 8, ThinkTime: 50 * time.Millisecond, RoundTransactions: 20,
		BaselineSQL: &sqlload.Result{
			PeakOpenTransactions: &open, BackendsSeen: &seen,
			Refused: []sqlload.Refused{{Statement: "DROP TABLE x", Code: sqlload.RefusedNotDataChanging}},
		},
		CandidateSQL: &sqlload.Result{
			ObserverNote: "no connection was free", ClientsStopped: 2,
			StoppedBecause: map[string]int{"connection reset": 2},
		},
	}
	doc := loadCompareSQLDoc(res)
	require.NotNil(t, doc)
	require.Equal(t, sqlload.SourceDeclared, doc.Source)
	require.Equal(t, "a checkout", doc.Description)
	require.Equal(t, 8, doc.Clients)
	require.Equal(t, "50ms", doc.ThinkTime)
	require.Equal(t, 20, doc.RoundTransactions)
	require.Equal(t, 8, *doc.Baseline.BackendsSeen)
	require.Len(t, doc.Baseline.Refused, 1)
	// Nil stays nil all the way out: "nobody looked" and "no overlap" are
	// different answers and a zero would be the second.
	require.Nil(t, doc.Candidate.BackendsSeen)
	require.Equal(t, "no connection was free", doc.Candidate.ObserverNote)
	require.Equal(t, 2, doc.Candidate.ClientsStopped)
	require.NotNil(t, doc.Candidate.Refused, "an absent refusal list is not a null")

	// And the key is absent entirely from an HTTP comparison, so nothing a
	// reader of that document already parses gained a field.
	require.Nil(t, loadCompareSQLDoc(&env.LoadCompareResult{}))
}

// The unit table prints three percentiles a side, statements indented under
// the transaction that holds them, and a side that recorded none says so
// rather than printing zeros.
func TestWhatTheSQLComparisonPrints(t *testing.T) {
	c := &workload.Comparison{
		Kind:     workload.SQLWorkload,
		Measures: []workload.MeasureDifference{{Measure: "tps", Baseline: f(120), Candidate: f(61), Ratio: f(-0.49), Direction: workload.DirectionWorse}},
		Routes: []workload.RouteDifference{
			{Route: "checkout", InBaseline: true, InCandidate: true,
				P50Baseline: f(10), P95Baseline: f(44), P99Baseline: f(98),
				P50Candidate: f(13), P95Candidate: f(61), P99Candidate: f(210),
				P95Ratio: f(0.386), Direction: workload.DirectionWorse,
				Resolution: workload.RouteResolution{Method: workload.ResolutionRounds,
					Rounds: 16, SmallestVisible: f(0.19),
					ChangeLow: f(0.2), ChangeHigh: f(0.6)}},
			{Scenario: "checkout", Route: "insert item", InBaseline: true, InCandidate: true,
				P50Baseline: f(4), P95Baseline: f(12), P99Baseline: f(30),
				P50Candidate: f(5), P95Candidate: f(44), P99Candidate: f(180),
				P95Ratio: f(2.66), Direction: workload.DirectionWorse,
				Resolution: workload.RouteResolution{Method: workload.ResolutionRounds,
					Rounds: 16, SmallestVisible: f(0.22),
					ChangeLow: f(2.0), ChangeHigh: f(3.3)}},
			// Run on the base branch and not on this one, which is the
			// loudest result the comparison can produce and the one a row of
			// zeros would hide.
			{Route: "refund", InBaseline: true, InCandidate: false,
				P50Baseline: f(20), P95Baseline: f(80), P99Baseline: f(140),
				Direction: workload.DirectionUnmeasurable},
		},
		Notes: []string{"a branch is copy on write"},
	}
	open, seen := 7, 8
	res := &env.LoadCompareResult{
		SQL: true, Rev: "1111111111112222", CandidateRev: "3333333333334444",
		How: "the merge base with origin/main", SQLSource: sqlload.SourceDeclared,
		SQLDescription: "a checkout", Clients: 8,
		BaselineSQL: &sqlload.Result{PeakActiveBackends: &open, PeakOpenTransactions: &open,
			BackendsSeen: &seen},
		CandidateSQL: &sqlload.Result{ObserverNote: "no connection was free"},
	}

	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf)}
	// A width a developer's terminal really has. At the eighty column default
	// the table stacks into one block per unit, which is the framework doing
	// the right thing and is not the table this test is about.
	e.Out.Width = 120
	renderSQLComparison(e, res, c, nil, workload.VerdictFail)
	out := buf.String()

	require.Contains(t, out, "declared statements, a checkout, 8 clients on each side.")
	require.Contains(t, out, "the base branch held 8 separate sessions")
	require.Contains(t, out, "this build never sampled its own backends: no connection was free.")
	require.Contains(t, out, "Latency is p50 / p95 / p99.")
	require.Contains(t, out, "P95 CHANGE")
	require.Contains(t, out, "10 / 44 / 98ms")
	require.Contains(t, out, "13 / 61 / 210ms")
	// The indent, measured against the row ABOVE it rather than against a
	// count of spaces. The table indents every row by two of its own, so an
	// assertion for two spaces before a statement passes whether or not this
	// renderer indents anything, which is the whole of what it is claiming.
	lineOf := func(name string) string {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), name) {
				return line
			}
		}
		return ""
	}
	transaction, statement := lineOf("checkout"), lineOf("insert item")
	require.NotEmpty(t, transaction)
	require.NotEmpty(t, statement)
	require.Greater(t,
		len(statement)-len(strings.TrimLeft(statement, " ")),
		len(transaction)-len(strings.TrimLeft(transaction, " ")),
		"a statement is indented under the transaction that holds it")

	// The row for the unit this build stopped running, read as a line rather
	// than as a substring of the whole report. "none" appears in that report
	// for a second reason, the change column of the same row, so a Contains
	// over the whole output is satisfied by a cell this assertion is not
	// about and passes a latencySpread that prints dashes or zeros.
	var refund string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "refund") {
			refund = line
		}
	}
	require.NotEmpty(t, refund, "the unit this build stopped running has no row")
	fields := strings.Fields(refund)
	require.Equal(t,
		[]string{"refund", "20", "/", "80", "/", "140ms", "none", "none",
			"unmeasurable", "nothing"}, fields,
		"a side that recorded no percentiles must print none, not dashes and not zeros")
	require.Contains(t, out, "What this comparison cannot see:")
	require.Contains(t, out, "a branch is copy on write")
	require.Contains(t, out, "the base branch comparison is fail")
}

func f(v float64) *float64 { return &v }
