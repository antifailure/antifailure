package workload_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/sqlload"
	"github.com/antifailure/antifailure/engine/internal/workload"
)

// Comparing the SQL workload on two builds.
//
// Every test here points at a way this could report a confident answer about
// something it did not measure, which is the failure mode the rest of this
// package's comparison files were written against. Two of them, the unit key
// and the throughput measure, are defects that would have shipped SILENTLY:
// neither raises an error, both produce a full report, and the report is
// wrong in a direction a reader cannot see.

func sqlSide(tps float64, txs []sqlload.TransactionResult,
	stmts []sqlload.StatementResult) *sqlload.Result {
	return &sqlload.Result{
		Source: sqlload.SourceDeclared, Clients: 8,
		Transactions: 400, TransactionsFailed: 4, Retries: 2,
		Statements: 800, Rows: 1600, TPS: tps, ErrorRate: 0.01,
		Overall:        load.Latency{P50Ms: 10, P90Ms: 30, P95Ms: 40, P99Ms: 90, MaxMs: 120},
		PerTransaction: txs, PerStatement: stmts,
		Refused: []sqlload.Refused{},
	}
}

func tx(name string, p50, p95, p99 float64) sqlload.TransactionResult {
	return sqlload.TransactionResult{
		Name: name, Executed: 200,
		Latency: load.Latency{P50Ms: p50, P95Ms: p95, P99Ms: p99},
	}
}

func stmt(transaction, label string, p50, p95, p99 float64) sqlload.StatementResult {
	return sqlload.StatementResult{
		Transaction: transaction, Label: label, Executed: 200,
		Latency: load.Latency{P50Ms: p50, P95Ms: p95, P99Ms: p99},
	}
}

// The unit of comparison is the transaction AND the statement, and a
// transaction row is keyed so that it can never collide with a statement's.
func TestASQLComparisonCarriesBothTheTransactionAndItsStatements(t *testing.T) {
	side := func(mul float64) *sqlload.Result {
		return sqlSide(100, []sqlload.TransactionResult{tx("checkout", 10*mul, 40*mul, 90*mul)},
			[]sqlload.StatementResult{stmt("checkout", "select", 4*mul, 12*mul, 30*mul)})
	}
	base := workload.ProjectSQLLoad(side(1), workload.ProjectSQLLoadOptions{Branch: "main"})
	cand := workload.ProjectSQLLoad(side(2), workload.ProjectSQLLoadOptions{Branch: "mine"})
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)
	require.Equal(t, workload.SQLWorkload, c.Kind)

	byScope := map[string]workload.RouteDifference{}
	for _, r := range c.Routes {
		byScope[workload.UnitKey(r.Scenario, r.Route)] = r
	}
	whole, ok := byScope["checkout"]
	require.True(t, ok, "the transaction itself is a unit and it is missing")
	one, ok := byScope["checkout select"]
	require.True(t, ok, "the statement inside it is a unit and it is missing")

	// Three percentiles a side on every unit. A p95 alone is not a latency
	// distribution, and a p50 that holds while a p99 doubles is a lock.
	require.Equal(t, 10.0, *whole.P50Baseline)
	require.Equal(t, 40.0, *whole.P95Baseline)
	require.Equal(t, 90.0, *whole.P99Baseline)
	require.Equal(t, 20.0, *whole.P50Candidate)
	require.Equal(t, 80.0, *whole.P95Candidate)
	require.Equal(t, 180.0, *whole.P99Candidate)
	require.Equal(t, 12.0, *one.P95Baseline)
	require.Equal(t, 24.0, *one.P95Candidate)
	require.Equal(t, workload.DirectionWorse, whole.Direction)
}

// TWO STATEMENTS WITH THE SAME LABEL IN DIFFERENT TRANSACTIONS ARE TWO UNITS.
//
// This is the one that would have shipped silently. A declared mix routinely
// runs the same statement inside several transactions, the runner already
// keeps them apart by the pair, and a comparison keyed on the label alone
// would have pooled two unrelated latencies into one ratio and reported the
// mixture as the change.
func TestTwoStatementsSharingALabelAreNotPooled(t *testing.T) {
	side := func(fastP95, slowP95 float64) *sqlload.Result {
		return sqlSide(100,
			[]sqlload.TransactionResult{tx("read", 1, 2, 3), tx("write", 1, 2, 3)},
			[]sqlload.StatementResult{
				stmt("read", "by id", 1, fastP95, 5),
				stmt("write", "by id", 1, slowP95, 500),
			})
	}
	base := workload.ProjectSQLLoad(side(5, 100), workload.ProjectSQLLoadOptions{Branch: "main"})
	cand := workload.ProjectSQLLoad(side(5, 400), workload.ProjectSQLLoadOptions{Branch: "mine"})
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	byScope := map[string]workload.RouteDifference{}
	for _, r := range c.Routes {
		byScope[workload.UnitKey(r.Scenario, r.Route)] = r
	}
	require.Equal(t, 5.0, *byScope["read by id"].P95Candidate,
		"the statement that did not move was pooled with the one that did")
	require.Equal(t, 400.0, *byScope["write by id"].P95Candidate)
	require.Equal(t, workload.DirectionSame, byScope["read by id"].Direction)
	require.Equal(t, workload.DirectionWorse, byScope["write by id"].Direction)
}

// A SQL unit is resolved round against round like a route is, and it is found
// by its scope rather than by its route name.
//
// ResolveByRounds skipped every row carrying a scenario, which was right while
// the HTTP mix was the only thing compared and would have left every SQL
// statement permanently unresolved: a statement's scenario is the transaction
// it belongs to and is never empty.
func TestASQLUnitIsResolvedRoundAgainstRound(t *testing.T) {
	side := func(p95 float64) *sqlload.Result {
		return sqlSide(100, []sqlload.TransactionResult{tx("checkout", 1, p95, 3)},
			[]sqlload.StatementResult{stmt("checkout", "select", 1, p95, 3)})
	}
	base := workload.ProjectSQLLoad(side(10), workload.ProjectSQLLoadOptions{Branch: "main"})
	cand := workload.ProjectSQLLoad(side(20), workload.ProjectSQLLoadOptions{Branch: "mine"})
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	// Eight rounds, every one of them exactly a doubling, so the interval is
	// as tight as rounds that agree can make it.
	rounds := make([]workload.RoundP95, 0, 8)
	for k := 0; k < 8; k++ {
		rounds = append(rounds, workload.RoundP95{
			Base:      map[string]float64{"checkout": 10, "checkout select": 10},
			Candidate: map[string]float64{"checkout": 20, "checkout select": 20},
		})
	}
	workload.ResolveByRounds(c, rounds)

	for _, r := range c.Routes {
		scope := workload.UnitKey(r.Scenario, r.Route)
		require.Equal(t, workload.ResolutionRounds, r.Resolution.Method, scope)
		require.Equal(t, 8, r.Resolution.Rounds, scope)
		require.NotNil(t, r.Resolution.ChangeLow, scope)
		require.InDelta(t, 1.0, *r.P95Ratio, 1e-9, scope)
	}
}

// A unit too few rounds ran on both sides is left unresolved WITH THE REASON,
// never judged on a band this comparison exists because it could not see.
func TestAUnitTooFewRoundsRanIsLeftUnresolved(t *testing.T) {
	side := func() *sqlload.Result {
		return sqlSide(100, []sqlload.TransactionResult{tx("rare", 1, 10, 3)},
			[]sqlload.StatementResult{stmt("rare", "select", 1, 10, 3)})
	}
	base := workload.ProjectSQLLoad(side(), workload.ProjectSQLLoadOptions{Branch: "main"})
	cand := workload.ProjectSQLLoad(side(), workload.ProjectSQLLoadOptions{Branch: "mine"})
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	// Three rounds and the unit appears in only one of them on both sides.
	rounds := []workload.RoundP95{
		{Base: map[string]float64{"rare": 10, "rare select": 4},
			Candidate: map[string]float64{"rare": 10, "rare select": 4}},
		{Base: map[string]float64{}, Candidate: map[string]float64{}},
		{Base: map[string]float64{}, Candidate: map[string]float64{}},
	}
	workload.ResolveByRounds(c, rounds)
	for _, r := range c.Routes {
		require.Nil(t, r.Resolution.ChangeLow)
		require.Contains(t, r.Resolution.Detail, "only 1 of 3 rounds ran this unit")
	}
}

// A SQL workload has no requests, so its throughput limit is judged on TPS.
//
// Reading achieved_rate for it would find nothing and report the declared
// limit as unmeasurable forever: a threshold in force that evaluated nothing,
// reported as a clean run by every reader who checks for breaches.
func TestTheThroughputLimitIsJudgedOnTransactionsPerSecond(t *testing.T) {
	base := workload.ProjectSQLLoad(sqlSide(100, nil, nil),
		workload.ProjectSQLLoadOptions{Branch: "main"})
	cand := workload.ProjectSQLLoad(sqlSide(50, nil, nil),
		workload.ProjectSQLLoadOptions{Branch: "mine"})
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	judged := workload.Judge(c, workload.ComparisonThresholds{ThroughputDrop: 0.1})
	require.Len(t, judged, 1)
	require.Equal(t, "throughput_drop", judged[0].Name)
	require.Equal(t, "tps", judged[0].Measure)
	require.Equal(t, workload.VerdictFail, judged[0].Value)
	require.InDelta(t, 0.5, *judged[0].Observed, 1e-9)
	require.Contains(t, judged[0].Detail, "committed 50 transactions per second")
	require.Equal(t, workload.VerdictFail, workload.ComparisonOutcome(judged))
}

// The HTTP comparison still reads achieved_rate and still says "served", word
// for word. The throughput words are a table now and the HTTP row of it is the
// original.
func TestTheHTTPThroughputVerdictIsUnchanged(t *testing.T) {
	side := func(rate float64) *load.Result {
		return &load.Result{Sent: 100, Rate: rate, Duration: 1,
			Overall: load.Latency{P95Ms: 10},
			Routes: []load.RouteResult{
				{Route: "GET /a", Sent: 100, Latency: load.Latency{P95Ms: 10}},
			}}
	}
	base := workload.ProjectLoad(side(100), workload.ProjectLoadOptions{Branch: "main"})
	cand := workload.ProjectLoad(side(50), workload.ProjectLoadOptions{Branch: "mine"})
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	judged := workload.Judge(c, workload.ComparisonThresholds{ThroughputDrop: 0.1})
	require.Equal(t, "achieved_rate", judged[0].Measure)
	require.Equal(t,
		"this branch served 50 requests per second against the base branch's 100, "+
			"a drop of 50.0 percent against a limit of 10.0 percent",
		judged[0].Detail)
}

// The HTTP per route wording is unchanged too, character for character. Every
// sentence that used to say "route" is built from a nouns table now, and the
// HTTP entry of that table has to resolve to what it said before.
func TestTheHTTPPerRouteWordingIsUnchanged(t *testing.T) {
	base := workload.ProjectLoad(&load.Result{
		Sent: 10, Overall: load.Latency{P95Ms: 10},
		Routes: []load.RouteResult{{Route: "GET /gone", Sent: 10,
			Latency: load.Latency{P95Ms: 10}}},
	}, workload.ProjectLoadOptions{Branch: "main"})
	cand := workload.ProjectLoad(&load.Result{
		Sent: 10, Overall: load.Latency{P95Ms: 10},
	}, workload.ProjectLoadOptions{Branch: "mine"})
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	// The run wide row carries no scope, so it sorts above the route's.
	judged := workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.1})
	require.Equal(t,
		"a per route latency limit was in force and not one route carried a "+
			"measurement it could be judged on, so this threshold compared nothing",
		judged[0].Detail)
	require.Equal(t,
		"this route was sent on the base branch and not on this one, so there is no "+
			"measurement on this side to compare",
		judged[1].Detail)
}

// The SQL report calls a row a unit, because it is not a route. The same
// sentence, with the same shape, in the vocabulary of the thing being
// measured.
func TestTheSQLPerUnitWordingSaysUnit(t *testing.T) {
	base := workload.ProjectSQLLoad(sqlSide(100,
		[]sqlload.TransactionResult{tx("gone", 1, 10, 3)}, nil),
		workload.ProjectSQLLoadOptions{Branch: "main"})
	cand := workload.ProjectSQLLoad(sqlSide(100, nil, nil),
		workload.ProjectSQLLoadOptions{Branch: "mine"})
	c, err := workload.Compare(base, cand)
	require.NoError(t, err)

	judged := workload.Judge(c, workload.ComparisonThresholds{P95Increase: 0.1})
	require.Contains(t, judged[0].Detail, "a per unit latency limit was in force")
	require.Contains(t, judged[1].Detail, "this unit was run on the base branch")
}

// A side that did not run is unverified and failed, never a document of zeros.
//
// Zero transactions a second differenced against a real run reports a hundred
// percent throughput drop, which is the most confident possible way to be
// wrong about a run that never happened.
func TestASideThatDidNotRunIsUnverifiedRatherThanZero(t *testing.T) {
	missing := workload.ProjectSQLLoad(nil, workload.ProjectSQLLoadOptions{Branch: "main"})
	require.Equal(t, workload.VerdictUnverified, missing.Verdict)
	require.Equal(t, workload.StateFailed, missing.State)
	require.Nil(t, missing.Measured.TPS)

	real := workload.ProjectSQLLoad(sqlSide(100, nil, nil),
		workload.ProjectSQLLoadOptions{Branch: "mine"})
	c, err := workload.Compare(missing, real)
	require.NoError(t, err)
	judged := workload.Judge(c, workload.ComparisonThresholds{ThroughputDrop: 0.1})
	require.Equal(t, workload.VerdictUnverified, judged[0].Value)
	require.Contains(t, judged[0].Detail, "did not record a transaction rate")
	require.Contains(t, c.Notes[len(c.Notes)-1], "measured nothing")
}

// The sentence every SQL comparison opens with is written for a SQL
// comparison. The HTTP one says the seed does not make the database contents
// the same, which is not what happens here: these clients change the rows they
// are measuring, on purpose.
func TestTheSQLComparisonOpensWithItsOwnHonesty(t *testing.T) {
	side := workload.ProjectSQLLoad(sqlSide(100, nil, nil),
		workload.ProjectSQLLoadOptions{Branch: "main"})
	c, err := workload.Compare(side, side)
	require.NoError(t, err)
	require.Contains(t, c.Notes[0], "transaction sequence")
	require.Contains(t, c.Notes[0], "autovacuum")
	require.Contains(t, c.Notes[0], "changes the rows, the table size and the index depth")
	require.NotContains(t, c.Notes[0], "request sequence")

	http := workload.ProjectLoad(&load.Result{Sent: 1, Overall: load.Latency{P95Ms: 1}},
		workload.ProjectLoadOptions{Branch: "main"})
	h, err := workload.Compare(http, http)
	require.NoError(t, err)
	require.Equal(t,
		"two runs against two environments are not a controlled experiment: the seed "+
			"makes the request sequence the same and does not make the machine, the "+
			"database contents or the load on the host the same",
		h.Notes[0])
}

// A transaction that ran nothing carries no percentiles rather than zeros. A
// zero p95 would enter the comparison as a side that answered instantly, and
// it is the loudest possible way to report a transaction that never ran.
func TestATransactionThatRanNothingCarriesNoPercentiles(t *testing.T) {
	never := sqlload.TransactionResult{Name: "rare", Executed: 0,
		Latency: load.Latency{P50Ms: 0, P95Ms: 0}}
	res := workload.ProjectSQLLoad(
		sqlSide(100, []sqlload.TransactionResult{never}, nil),
		workload.ProjectSQLLoadOptions{Branch: "main"})
	require.Equal(t, "rare", res.Routes[0].Route)
	require.Nil(t, res.Routes[0].P95Ms)
	require.Nil(t, res.Routes[0].P50Ms)
	require.Nil(t, res.Routes[0].P99Ms)
	// The row is still there, and it still says it ran nothing. A missing row
	// and a row of zeros are the two ways to lose this finding.
	require.Zero(t, res.Routes[0].Sent)
}

// THE SIDE'S OWN LIMITS TRAVEL WITH IT, so a verdict that PASSED on the base
// branch and fails on this one is visible as a transition.
//
// That is a different fact from any delta under load.comparison.thresholds.
// load.sql.thresholds asks "is this run acceptable on its own"; the comparison
// asks "is it worse than the last build". A projection that dropped the first
// would report a side as clean while its own declared error_rate was breached,
// and Compare would have nothing to count as regressed.
func TestEachSideCarriesItsOwnDeclaredLimits(t *testing.T) {
	side := func(errorRate float64) *sqlload.Result {
		r := sqlSide(100, []sqlload.TransactionResult{tx("checkout", 1, 2, 3)}, nil)
		r.ErrorRate = errorRate
		return r
	}
	opts := func(branch string) workload.ProjectSQLLoadOptions {
		return workload.ProjectSQLLoadOptions{Branch: branch, ErrorRate: 0.01}
	}
	base := workload.ProjectSQLLoad(side(0.005), opts("main"))
	cand := workload.ProjectSQLLoad(side(0.05), opts("mine"))
	require.Equal(t, workload.VerdictPass, base.Verdict)
	require.Equal(t, workload.VerdictFail, cand.Verdict)

	c, err := workload.Compare(base, cand)
	require.NoError(t, err)
	require.Equal(t, 1, c.Regressed,
		"a declared limit that passed on the base branch and fails here was not counted")
	var moved workload.ThresholdDifference
	for _, d := range c.Thresholds {
		if d.Name == "error_rate" {
			moved = d
		}
	}
	require.Equal(t, workload.VerdictPass, moved.Baseline)
	require.Equal(t, workload.VerdictFail, moved.Candidate)
	require.True(t, moved.Changed)
	require.True(t, moved.Regressed)
}

// A run that committed nothing is unverified on its own side, never a pass
// over an empty measurement, and the threshold row it carries records no
// observation rather than a zero.
func TestASideThatCommittedNothingIsUnverifiedOnItsOwnLimits(t *testing.T) {
	empty := sqlSide(0, nil, nil)
	empty.Transactions, empty.TransactionsFailed, empty.ErrorRate = 0, 0, 0
	res := workload.ProjectSQLLoad(empty,
		workload.ProjectSQLLoadOptions{Branch: "main", ErrorRate: 0.01})
	require.Equal(t, workload.VerdictUnverified, res.Verdict)
	require.NotEmpty(t, res.Thresholds)
	for _, v := range res.Thresholds {
		require.Equal(t, workload.VerdictUnverified, v.Value, v.Name)
		require.Nil(t, v.Observed, v.Name)
	}
}
