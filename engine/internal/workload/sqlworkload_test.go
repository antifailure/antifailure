package workload_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/sqlload"
	"github.com/antifailure/antifailure/engine/internal/workload"
)

// The fifth kind's projection, its verdict, and what a comparison can see.
//
// The projection is where a measurement is lost, and this package has already
// shipped that defect once in the other direction: the control plane's decoder
// read the engine's NATIVE result rather than this document and a run that sent
// twelve hundred requests recorded as having sent none. So these tests read the
// projected Result rather than the sqlload.Result that went in, which is the
// only way to see a field that did not survive the crossing.

func sqlRunner(res *sqlload.Result, meanIncrease, errorRate float64) *fakeRunner {
	return &fakeRunner{
		envID:     "aftest-sql",
		status:    &env.Result{EnvID: "aftest-sql", URL: "http://127.0.0.1:9"},
		sqlResult: res,
		sqlPlan: &env.SQLLoadPlan{
			Clients: res.Clients, Duration: res.Duration,
		},
		sqlMeanIncrease:   meanIncrease,
		sqlErrorRateLimit: errorRate,
	}
}

// healthySQL is a run that committed, with one transaction over its baseline
// and one under it.
func healthySQL() *sqlload.Result {
	peak, seen := 7, 8
	waits, waitMS := 12, 2400.0
	return &sqlload.Result{
		Source: sqlload.SourceStatementStatistics, Clients: 8,
		Transactions: 900, TransactionsFailed: 4, Retries: 11,
		Deadlocks: 8, SerializationFailures: 3,
		Statements: 1800, StatementsFailed: 4, Rows: 17422,
		Duration: 30 * time.Second, TPS: 30, ErrorRate: 0.0044,
		Overall: load.Latency{P50Ms: 10, P90Ms: 30, P95Ms: 44, P99Ms: 90, MaxMs: 210},
		PerTransaction: []sqlload.TransactionResult{
			{
				Name: "slow one", Executed: 400, Weight: 1000,
				Latency:   load.Latency{P50Ms: 20, P95Ms: 80},
				Baselines: sqlload.Baseline{MeanMs: 4, Has: true, MeanIncrease: 0.9},
			},
			{
				Name: "fast one", Executed: 500, Weight: 2000,
				Latency:   load.Latency{P50Ms: 5, P95Ms: 12},
				Baselines: sqlload.Baseline{MeanMs: 2, Has: true, MeanIncrease: 0.05},
			},
		},
		PerStatement: []sqlload.StatementResult{
			{Transaction: "slow one", Label: "slow one", Executed: 400, Rows: 400,
				Latency: load.Latency{P50Ms: 20, P95Ms: 80}},
			{Transaction: "fast one", Label: "fast one", Executed: 500, Errors: 4, Rows: 17022,
				Latency: load.Latency{P50Ms: 5, P95Ms: 12}},
		},
		Errors:               map[string]int{"deadlock": 8},
		Refused:              []sqlload.Refused{{Statement: "DELETE FROM t", Code: sqlload.RefusedWrite, Reason: "no"}},
		PeakActiveBackends:   &peak,
		PeakOpenTransactions: &peak,
		BackendsSeen:         &seen,
		LockWaits:            &waits,
		LockWaitMS:           &waitMS,
	}
}

func runSQL(t *testing.T, res *sqlload.Result, meanIncrease, errorRate float64) *workload.Result {
	t.Helper()
	plan, err := workload.Parse(workload.Request{Kind: "sql_workload", Concurrency: "8"})
	require.NoError(t, err)
	out, err := workload.Execute(context.Background(), workload.Options{
		Plan: plan, Runner: sqlRunner(res, meanIncrease, errorRate),
		Clock: clock.NewFake(time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)),
	})
	require.NoError(t, err)
	return out
}

// TestASQLWorkloadProjectsEveryMeasurementItTook.
func TestASQLWorkloadProjectsEveryMeasurementItTook(t *testing.T) {
	out := runSQL(t, healthySQL(), 0.25, 0.01)
	m := out.Measured

	require.Equal(t, workload.SQLWorkload, out.Kind)
	require.Equal(t, 8, *m.Clients)
	require.Equal(t, 900, *m.Transactions)
	require.Equal(t, 4, *m.TransactionsFailed)
	require.Equal(t, 11, *m.Retries)
	require.Equal(t, 8, *m.Deadlocks)
	require.Equal(t, 3, *m.SerializationFailures)
	require.Equal(t, 1800, *m.StatementsRun)
	require.Equal(t, 4, *m.StatementsFailed)
	require.Equal(t, 17422, *m.RowsTouched)
	require.InDelta(t, 30, *m.TPS, 1e-9)
	require.InDelta(t, 0.0044, *m.ErrorRate, 1e-9)
	require.Equal(t, sqlload.SourceStatementStatistics, m.Source)

	// The five latency columns hold a TRANSACTION's latency here. Shared with
	// the kinds that send requests, because a percentile is a percentile and
	// the comparison differences them by name.
	require.InDelta(t, 10, *m.P50Ms, 1e-9)
	require.InDelta(t, 44, *m.P95Ms, 1e-9)
	require.InDelta(t, 210, *m.MaxMs, 1e-9)

	// And the columns that belong to the kinds that send requests stay null. A
	// statement count wearing the name of a request count is the conflation
	// load.thresholds.query_count_increase was deprecated for.
	require.Nil(t, m.Requests)
	require.Nil(t, m.Sessions)
	require.Nil(t, m.Workflows)
	require.Nil(t, m.Findings)
	require.Nil(t, m.AchievedRate)

	require.Equal(t, []string{"write_not_allowed: DELETE FROM t"}, m.RefusedRoutes)
	require.Equal(t, map[string]int{"deadlock": 8}, m.Errors)
}

// TestASQLWorkloadProjectsWhatTheServerSawAndNotAZero.
//
// The two observation columns are pointers all the way out, and this is the
// test that keeps them that way. A run nobody watched and a run whose clients
// never overlapped are different findings, and a console handed a zero for both
// will draw the second as the first.
func TestASQLWorkloadProjectsWhatTheServerSawAndNotAZero(t *testing.T) {
	watched := runSQL(t, healthySQL(), 0.25, 0.01)
	require.NotNil(t, watched.Measured.PeakOpenTransactions)
	require.Equal(t, 7, *watched.Measured.PeakOpenTransactions)
	require.Equal(t, 8, *watched.Measured.BackendsSeen)

	unwatched := healthySQL()
	unwatched.PeakActiveBackends = nil
	unwatched.PeakOpenTransactions = nil
	unwatched.BackendsSeen = nil
	out := runSQL(t, unwatched, 0.25, 0.01)
	require.Nil(t, out.Measured.PeakOpenTransactions,
		"a run nobody watched projected a number, so nothing downstream can tell it from a run whose clients never overlapped")
	require.Nil(t, out.Measured.BackendsSeen)

	// The JSON is the document the control plane stores, so the distinction has
	// to survive marshalling as well as projection.
	body, err := json.Marshal(out.Measured)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	require.Contains(t, decoded, "peak_open_transactions")
	require.Nil(t, decoded["peak_open_transactions"])
}

// TestASQLWorkloadProjectsTheContentionAndNotAZero.
//
// The same rule, on the pair where breaking it is worst. Zero lock waits is
// the most reassuring number a stored run can carry, so a run nobody watched
// must project a null rather than that number: a comparison reading the null
// as a zero would call the first watched run a regression from a clean one.
func TestASQLWorkloadProjectsTheContentionAndNotAZero(t *testing.T) {
	watched := runSQL(t, healthySQL(), 0.25, 0.01)
	require.NotNil(t, watched.Measured.LockWaits)
	require.Equal(t, 12, *watched.Measured.LockWaits)
	require.NotNil(t, watched.Measured.LockWaitMs)
	require.InDelta(t, 2400, *watched.Measured.LockWaitMs, 1e-9)

	unwatched := healthySQL()
	unwatched.LockWaits = nil
	unwatched.LockWaitMS = nil
	out := runSQL(t, unwatched, 0.25, 0.01)
	require.Nil(t, out.Measured.LockWaits,
		"a run nothing watched projected a lock wait count, so it now claims not to have blocked")
	require.Nil(t, out.Measured.LockWaitMs)

	body, err := json.Marshal(out.Measured)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	require.Contains(t, decoded, "lock_waits")
	require.Nil(t, decoded["lock_waits"])

	// A watched run that never queued keeps its zero, which is the arm that
	// makes the assertions above mean something: a projection that simply
	// dropped the field would satisfy every one of them.
	none, noneMS := 0, 0.0
	quiet := healthySQL()
	quiet.LockWaits, quiet.LockWaitMS = &none, &noneMS
	clean := runSQL(t, quiet, 0.25, 0.01)
	require.NotNil(t, clean.Measured.LockWaits)
	require.Equal(t, 0, *clean.Measured.LockWaits)
}

// TestASQLWorkloadPutsEachStatementInItsOwnRow.
func TestASQLWorkloadPutsEachStatementInItsOwnRow(t *testing.T) {
	out := runSQL(t, healthySQL(), 0.25, 0.01)
	require.Len(t, out.Routes, 2)

	// The pair is the identity, not the label. One statement in two
	// transactions cannot have its percentiles merged, which is the same rule
	// a scenario's routes already follow.
	require.Equal(t, "slow one", out.Routes[0].Scenario)
	require.Equal(t, "slow one", out.Routes[0].Route)
	require.Equal(t, 400, out.Routes[0].Sent)
	require.InDelta(t, 80, *out.Routes[0].P95Ms, 1e-9)

	// The baseline and the increase travel together or not at all.
	require.NotNil(t, out.Routes[0].BaselineP95Ms)
	require.NotNil(t, out.Routes[0].P95Increase)
	require.InDelta(t, 0.9, *out.Routes[0].P95Increase, 1e-9)
}

// TestASQLWorkloadThatCommittedNothingIsNotAPass is the most important line in
// the projection.
//
// Every threshold passes trivially over an empty measurement, so a database
// that refused every transaction reports zero breaches. This product has
// shipped that shape before: an entire nightly corpus was green having never
// reached an agent.
func TestASQLWorkloadThatCommittedNothingIsNotAPass(t *testing.T) {
	empty := healthySQL()
	empty.Transactions = 0
	empty.TransactionsFailed = 240
	empty.TPS = 0
	empty.ErrorRate = 1
	empty.Overall = load.Latency{}
	for i := range empty.PerTransaction {
		empty.PerTransaction[i].Executed = 0
	}

	// BOTH ARMS, and the second is the one that matters. With mean_increase in
	// force the empty run is already unverified because that threshold measured
	// nothing, so a verdict that had forgotten about the empty run entirely
	// would still read unverified and this test would prove nothing. A DECLARED
	// workload carries no baselines, so mean_increase is not in force, every
	// threshold row is a pass or an absence, and the only thing standing
	// between a database that refused every transaction and a clean report is
	// the empty check itself. Found by mutation: removing that check left this
	// test green until this arm was added.
	for _, meanIncrease := range []float64{0.25, 0} {
		out := runSQL(t, empty, meanIncrease, 0.01)
		require.Equal(t, workload.StateSucceeded, out.State,
			"the work ran to completion; what it found is the verdict's business")
		require.Equalf(t, workload.VerdictUnverified, out.Verdict,
			"a run that committed nothing reported a pass, with mean_increase at %v", meanIncrease)
	}

	out := runSQL(t, empty, 0.25, 0.01)
	require.Contains(t, out.Detail, "transaction attempts failed")
	require.Equal(t, 0, *out.Measured.Transactions)
	require.InDelta(t, 0, *out.Measured.TPS, 1e-9)

	// And the threshold rows say WHY rather than reporting a clean sheet.
	var errorRate workload.ThresholdVerdict
	for _, v := range out.Thresholds {
		if v.Name == "error_rate" {
			errorRate = v
		}
	}
	require.Equal(t, workload.VerdictUnverified, errorRate.Value)
	require.Nil(t, errorRate.Observed,
		"an unverified threshold carries no observation, because there was none")
	require.Contains(t, errorRate.Detail, "nothing to measure")
}

// TestASQLWorkloadFailsOnAMeasuredBreachAndSaysWhichTransaction.
func TestASQLWorkloadFailsOnAMeasuredBreachAndSaysWhichTransaction(t *testing.T) {
	out := runSQL(t, healthySQL(), 0.25, 0.01)
	require.Equal(t, workload.VerdictFail, out.Verdict)

	byScope := map[string]workload.ThresholdVerdict{}
	for _, v := range out.Thresholds {
		byScope[v.Scope+"/"+v.Name] = v
	}
	require.Equal(t, workload.VerdictFail, byScope["slow one/mean_increase"].Value,
		"the transaction ninety percent over its baseline did not fail its threshold")
	require.Equal(t, workload.VerdictPass, byScope["fast one/mean_increase"].Value)
	require.Equal(t, workload.VerdictPass, byScope["/error_rate"].Value,
		"four failures in nine hundred is under one percent")
}

// TestASQLWorkloadWithNoBaselineIsUnverifiedRatherThanPassing.
//
// A declared workload carries no baselines at all, so this is every transaction
// for anybody who wrote their own statements. Reporting it as a clean
// mean_increase would be a configured check, evaluated zero times, reported
// green.
func TestASQLWorkloadWithNoBaselineIsUnverifiedRatherThanPassing(t *testing.T) {
	declared := healthySQL()
	declared.Source = sqlload.SourceDeclared
	for i := range declared.PerTransaction {
		declared.PerTransaction[i].Baselines = sqlload.Baseline{}
	}

	out := runSQL(t, declared, 0.25, 0.01)
	require.Equal(t, workload.VerdictUnverified, out.Verdict)
	for _, v := range out.Thresholds {
		if v.Name != "mean_increase" {
			continue
		}
		require.Equal(t, workload.VerdictUnverified, v.Value, v.Scope)
		require.Contains(t, v.Detail, "no baseline")
		require.Nil(t, v.Observed)
	}
}

// TestComparingTwoSQLWorkloads is what lane G differences.
func TestComparingTwoSQLWorkloads(t *testing.T) {
	baseline := runSQL(t, healthySQL(), 0.25, 0.01)

	slower := healthySQL()
	slower.TPS = 21
	slower.Transactions = 630
	slower.TransactionsFailed = 40
	slower.Deadlocks = 26
	slower.Retries = 60
	slower.Rows = 12000
	blocked, blockedMS := 31, 9100.0
	slower.LockWaits, slower.LockWaitMS = &blocked, &blockedMS
	slower.Overall = load.Latency{P50Ms: 16, P90Ms: 51, P95Ms: 77, P99Ms: 150, MaxMs: 400}
	slower.PerStatement[0].Latency = load.Latency{P50Ms: 40, P95Ms: 160}
	candidate := runSQL(t, slower, 0.25, 0.01)

	c, err := workload.Compare(baseline, candidate)
	require.NoError(t, err)
	require.Equal(t, workload.SQLWorkload, c.Kind)

	moves := map[string]workload.MeasureDifference{}
	for _, m := range c.Measures {
		moves[m.Measure] = m
	}
	// Throughput down is worse, every count of something going wrong up is
	// worse. A comparison that got either direction backwards would tell
	// somebody a regression was an improvement.
	require.Equal(t, workload.DirectionWorse, moves["tps"].Direction)
	require.Equal(t, workload.DirectionWorse, moves["transactions_failed"].Direction)
	require.Equal(t, workload.DirectionWorse, moves["deadlocks"].Direction)
	require.Equal(t, workload.DirectionWorse, moves["retries"].Direction)
	require.Equal(t, workload.DirectionWorse, moves["p95_ms"].Direction)
	require.Equal(t, workload.DirectionWorse, moves["transactions"].Direction)
	// The contention, and the pair of rows that could not exist before. A
	// build that made its clients queue longer for the same work is slower
	// for a reason, and this is the only thing in the comparison that can say
	// what the reason was.
	require.Equal(t, workload.DirectionWorse, moves["lock_waits"].Direction)
	require.Equal(t, workload.DirectionWorse, moves["lock_wait_ms"].Direction)
	require.NotNil(t, moves["lock_waits"].Delta)
	require.InDelta(t, 19, *moves["lock_waits"].Delta, 1e-9)

	// rows_touched has no direction and is still carried. More rows is not
	// better and fewer is not worse: it says whether the generated parameters
	// matched anything, so a change in it is a change in what was measured.
	rows, ok := moves["rows_touched"]
	require.True(t, ok, "rows_touched was dropped from the comparison entirely")
	require.Equal(t, workload.DirectionUnmeasurable, rows.Direction)
	require.NotNil(t, rows.Delta)
	require.InDelta(t, -5422, *rows.Delta, 1e-9)

	// And the per statement rows are differenced by the pair, so the one
	// statement that got slower is named.
	var slow workload.RouteDifference
	for _, r := range c.Routes {
		if r.Route == "slow one" {
			slow = r
		}
	}
	require.True(t, slow.InBaseline)
	require.True(t, slow.InCandidate)
	require.Equal(t, workload.DirectionWorse, slow.Direction)
	require.InDelta(t, 80, *slow.P95Delta, 1e-9)
}
