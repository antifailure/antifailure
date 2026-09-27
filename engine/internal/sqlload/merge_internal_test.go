package sqlload

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/load"
)

// Pooling rounds, tested from inside the package because the samples a pool is
// made of are unexported on purpose.
//
// An external test could only assert that the pooled percentiles look
// plausible, which is the assertion that would have passed against a Merge
// that averaged them. These tests are written so that averaging fails: every
// fixture's two rounds are drawn from distributions far enough apart that the
// mean of the two p95s and the p95 of the pool are different numbers, and the
// expected value is the second.

// round builds a result carrying samples, the way a real run does.
func round(samples []float64, opts func(*Result)) *Result {
	r := &Result{
		Source: SourceDeclared, Clients: 4,
		Transactions: len(samples), Duration: time.Second,
		Overall: load.Percentiles(samples),
		Errors:  map[string]int{},
		raw: &rawSamples{
			all:           samples,
			byTransaction: map[string][]float64{"checkout": samples},
			byStatement:   map[stmtKey][]float64{{"checkout", "select"}: samples},
			order:         []string{"checkout"},
			weights:       map[string]float64{"checkout": 1},
			baselines:     map[string]Baseline{"checkout": {}},
		},
		PerTransaction: []TransactionResult{{
			Name: "checkout", Executed: len(samples), Latency: load.Percentiles(samples),
		}},
		PerStatement: []StatementResult{{
			Transaction: "checkout", Label: "select",
			Executed: len(samples), Latency: load.Percentiles(samples),
		}},
	}
	if opts != nil {
		opts(r)
	}
	return r
}

func series(from, to float64) []float64 {
	out := []float64{}
	for v := from; v <= to; v++ {
		out = append(out, v)
	}
	return out
}

// THE TEST THIS FILE EXISTS FOR. A pooled p95 is the p95 of every sample the
// rounds took, not the average of what each round reported.
//
// The two rounds here are ten samples of 1 to 10 and ten of 101 to 110. Each
// round's own p95 by nearest rank is its tenth value, 10 and 110, and the mean
// of those is 60, which is a latency neither round ever measured and which no
// sample in either is anywhere near. The pool is twenty samples and its p95 is
// its nineteenth, 109.
func TestAPooledPercentileIsTakenFromTheSamplesAndNotAveraged(t *testing.T) {
	a, b := round(series(1, 10), nil), round(series(101, 110), nil)
	require.Equal(t, 10.0, a.Overall.P95Ms)
	require.Equal(t, 110.0, b.Overall.P95Ms)

	pooled, err := Merge(a, b)
	require.NoError(t, err)
	require.Equal(t, 109.0, pooled.Overall.P95Ms,
		"the pooled p95 is the p95 of the pool, never the mean of two p95s")
	require.Equal(t, 109.0, pooled.PerTransaction[0].Latency.P95Ms)
	require.Equal(t, 109.0, pooled.PerStatement[0].Latency.P95Ms)
	// And the pool is still a pool, so a merged result can be merged again.
	require.NotNil(t, pooled.raw)
}

// A result that has been through a document carries percentiles and not the
// samples they came from, and merging it would have to average them. It is
// refused instead.
func TestAResultWithNoSamplesIsRefusedRatherThanAveraged(t *testing.T) {
	stored := round(series(1, 10), nil)
	stored.raw = nil
	_, err := Merge(round(series(1, 10), nil), stored)
	require.ErrorIs(t, err, ErrNoSamples)
}

// Rounds that ran different client counts cannot be pooled into one
// throughput. A TPS over eight clients and sixteen is a TPS at a concurrency
// nobody chose.
func TestRoundsAtDifferentConcurrenciesAreRefused(t *testing.T) {
	other := round(series(1, 10), func(r *Result) { r.Clients = 16 })
	_, err := Merge(round(series(1, 10), nil), other)
	require.Error(t, err)
	require.Contains(t, err.Error(), "concurrency nobody chose")
}

func TestAnEmptyOrMissingRoundIsRefused(t *testing.T) {
	_, err := Merge()
	require.Error(t, err)
	_, err = Merge(round(series(1, 10), nil), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing")
}

// Counts add, the duration is the time actually spent running, and the
// throughput is what was committed over that time rather than either round's
// own rate.
//
// The two rounds here are deliberately uneven: one commits 40 in one second
// and the other 10 in three. Either round's own TPS, 40 and about 3.3, and the
// mean of them, are all wrong; 50 over four seconds is 12.5.
func TestCountsAddAndTheRateIsOverTheWholeOfIt(t *testing.T) {
	a := round(series(1, 10), func(r *Result) {
		r.Transactions, r.TransactionsFailed, r.Duration = 40, 4, time.Second
		r.Retries, r.Deadlocks, r.SerializationFailures = 3, 2, 1
		r.Statements, r.StatementsFailed, r.Rows = 80, 5, 400
		r.Errors = map[string]int{"deadlock detected": 2}
		r.PerTransaction[0].Executed, r.PerTransaction[0].Failed = 40, 4
		r.PerStatement[0].Executed, r.PerStatement[0].Rows = 80, 400
	})
	b := round(series(1, 10), func(r *Result) {
		r.Transactions, r.TransactionsFailed, r.Duration = 10, 6, 3*time.Second
		r.Retries, r.Deadlocks, r.SerializationFailures = 1, 1, 0
		r.Statements, r.StatementsFailed, r.Rows = 20, 1, 100
		r.Errors = map[string]int{"deadlock detected": 1, "canceling statement": 4}
		r.PerTransaction[0].Executed, r.PerTransaction[0].Failed = 10, 6
		r.PerStatement[0].Executed, r.PerStatement[0].Rows = 20, 100
	})

	pooled, err := Merge(a, b)
	require.NoError(t, err)
	require.Equal(t, 50, pooled.Transactions)
	require.Equal(t, 10, pooled.TransactionsFailed)
	require.Equal(t, 4, pooled.Retries)
	require.Equal(t, 3, pooled.Deadlocks)
	require.Equal(t, 1, pooled.SerializationFailures)
	require.Equal(t, 100, pooled.Statements)
	require.Equal(t, 6, pooled.StatementsFailed)
	require.Equal(t, int64(500), pooled.Rows)
	require.Equal(t, 4*time.Second, pooled.Duration)
	require.InDelta(t, 12.5, pooled.TPS, 1e-9,
		"the rate is the whole of the work over the whole of the time")
	// Over commits plus failures, and a retry is in neither.
	require.InDelta(t, 10.0/60.0, pooled.ErrorRate, 1e-9)
	require.Equal(t, 3, pooled.Errors["deadlock detected"])
	require.Equal(t, 4, pooled.Errors["canceling statement"])
	require.Equal(t, 50, pooled.PerTransaction[0].Executed)
	require.Equal(t, 10, pooled.PerTransaction[0].Failed)
	require.Equal(t, 100, pooled.PerStatement[0].Executed)
	require.Equal(t, int64(500), pooled.PerStatement[0].Rows)
}

// A transaction no round picked keeps its row, with no executions and no
// percentiles. A mix whose rarest transaction never ran is a finding about the
// run's length, and a missing row hides it.
func TestATransactionNoRoundPickedKeepsItsRow(t *testing.T) {
	withRare := func(r *Result) {
		r.raw.order = []string{"checkout", "refund"}
		r.raw.byTransaction["refund"] = nil
		r.raw.weights["refund"] = 0.5
		r.PerTransaction = append(r.PerTransaction, TransactionResult{Name: "refund"})
	}
	pooled, err := Merge(round(series(1, 10), withRare), round(series(1, 10), withRare))
	require.NoError(t, err)
	require.Len(t, pooled.PerTransaction, 2)
	require.Equal(t, "refund", pooled.PerTransaction[1].Name)
	require.Zero(t, pooled.PerTransaction[1].Executed)
	require.Zero(t, pooled.PerTransaction[1].Latency.P95Ms)
	require.InDelta(t, 0.5, pooled.PerTransaction[1].Weight, 1e-9,
		"the weight is a property of the mix and survives a round that never picked it")
}

// The mean increase against the source's baseline is recomputed from the
// pooled samples rather than averaged from the rounds' own increases. The mean
// of several means is the mean of the pool only when every round committed the
// same number of transactions, and no round does.
func TestTheBaselineIncreaseIsRecomputedFromThePool(t *testing.T) {
	withBaseline := func(samples []float64) func(*Result) {
		return func(r *Result) {
			r.raw.baselines["checkout"] = Baseline{MeanMs: 10, Has: true}
			r.raw.byTransaction["checkout"] = samples
		}
	}
	// Two samples of 10 and ninety-eight of 20: the pool's mean is 19.8, so
	// the increase against a baseline of 10 is 0.98.
	a := round(series(1, 2), withBaseline([]float64{10, 10}))
	b := round(series(1, 98), withBaseline(repeat(20, 98)))
	pooled, err := Merge(a, b)
	require.NoError(t, err)
	require.True(t, pooled.PerTransaction[0].Baselines.Has)
	require.InDelta(t, 0.98, pooled.PerTransaction[0].Baselines.MeanIncrease, 1e-9)
}

// The peaks are the largest any round saw, because each one is "the most at
// ONE instant" and instants do not add. A round nobody watched is named rather
// than folded in, because a peak over twelve of sixteen rounds is a floor.
func TestThePeaksAreTheLargestAnyRoundSawAndAnUnwatchedRoundIsNamed(t *testing.T) {
	watched := func(active, open, seen int) func(*Result) {
		return func(r *Result) {
			a, o, s := active, open, seen
			r.PeakActiveBackends, r.PeakOpenTransactions, r.BackendsSeen = &a, &o, &s
		}
	}
	pooled, err := Merge(
		round(series(1, 10), watched(3, 4, 4)),
		round(series(1, 10), watched(2, 7, 4)),
	)
	require.NoError(t, err)
	require.Equal(t, 3, *pooled.PeakActiveBackends)
	require.Equal(t, 7, *pooled.PeakOpenTransactions)
	require.Equal(t, 4, *pooled.BackendsSeen)
	require.Empty(t, pooled.ObserverNote)

	blind := round(series(1, 10), func(r *Result) { r.ObserverNote = "no connection was free" })
	partial, err := Merge(round(series(1, 10), watched(3, 4, 4)), blind)
	require.NoError(t, err)
	require.Equal(t, 4, *partial.PeakOpenTransactions)
	require.Contains(t, partial.ObserverNote, "1 of 2 rounds were never sampled")
	require.Contains(t, partial.ObserverNote, "floor")
	require.Contains(t, partial.ObserverNote, "no connection was free")

	// Every round unwatched is not a floor, it is an absence, and the pointers
	// stay nil so that "nobody looked" cannot be read as "no overlap".
	none, err := Merge(blind, blind)
	require.NoError(t, err)
	require.Nil(t, none.BackendsSeen)
	require.Nil(t, none.PeakOpenTransactions)
	require.Equal(t, "no connection was free", none.ObserverNote)
}

// The contention tests, and what they are guarding against.
//
// `af load compare --sql` sends each side in several short rounds and pools
// them with Merge, and the pooled result is what ProjectSQLLoad hands to
// Compare. Compare emits a lock_waits row only when BOTH sides carry a number,
// and a nil means "nobody watched the queues". So a Merge that simply did not
// copy these fields across would make the one command that compares contention
// between two builds report nothing at all, on rounds that every one of them
// measured, and no existing test would have gone red: the half below Merge is
// covered by workload's TestComparingTwoSQLWorkloads, the half above it by the
// runner's own lock tests, and Merge sat silently between them.

// blocked is a round that watched the wait queues and found contention.
func blocked(waits int, ms float64, pairs ...LockWait) func(*Result) {
	return func(r *Result) {
		w, m := waits, ms
		r.LockWaits, r.LockWaitMS = &w, &m
		r.LockWaitPairs = pairs
		r.LockWaitNote = LockWaitBound
	}
}

func pair(blockedStmt, blockingStmt, lockType string, waits int, ms float64) LockWait {
	return LockWait{
		BlockedTransaction: "checkout", BlockedStatement: blockedStmt,
		BlockingTransaction: "checkout", BlockingStatement: blockingStmt,
		BlockingInRun: true, LockType: lockType, Mode: "ShareLock",
		Waits: waits, WaitedMS: ms,
	}
}

// THE COUNTS ADD. A wait is an event and a waiting millisecond is a millisecond
// somebody spent queueing, so two rounds that queued twice and three times
// queued five times between them. The fixture separates adding from every other
// thing Merge could plausibly do with a pair of numbers: the sum is 5 and 1000,
// the largest is 3 and 600, the first is 2 and 400 and the last is 3 and 600.
func TestTheContentionAddsAcrossRoundsRatherThanTakingTheWorstOne(t *testing.T) {
	held := pair("select", "hold", "transactionid", 2, 400)
	heldAgain := pair("select", "hold", "transactionid", 1, 200)
	row := pair("select", "update", "tuple", 3, 900)

	pooled, err := Merge(
		round(series(1, 10), blocked(2, 400, held)),
		round(series(1, 10), blocked(3, 600, heldAgain, row)),
	)
	require.NoError(t, err)

	require.NotNil(t, pooled.LockWaits,
		"the rounds watched the queues and the pool says nobody did, which is the one "+
			"answer a watched run must not be able to produce")
	require.Equal(t, 5, *pooled.LockWaits)
	require.NotNil(t, pooled.LockWaitMS)
	require.InDelta(t, 1000.0, *pooled.LockWaitMS, 1e-9)

	// The same pair seen in two rounds is ONE pair that waited in both, folded
	// on its identity rather than listed twice, and the list is worst first.
	require.Len(t, pooled.LockWaitPairs, 2)
	require.Equal(t, "update", pooled.LockWaitPairs[0].BlockingStatement,
		"the pairs are not worst first, so the row a reader acts on is not the top one")
	require.InDelta(t, 900.0, pooled.LockWaitPairs[0].WaitedMS, 1e-9)
	require.Equal(t, "hold", pooled.LockWaitPairs[1].BlockingStatement)
	require.Equal(t, 3, pooled.LockWaitPairs[1].Waits)
	require.InDelta(t, 600.0, pooled.LockWaitPairs[1].WaitedMS, 1e-9)

	// The bound travels with the counts. A count of waits with no statement of
	// what the instrument cannot see is a count somebody reads as a total.
	require.Contains(t, pooled.LockWaitNote, "floors rather than totals")
}

// Nil rather than zero, pooled. A round that never read the queues is named and
// not folded in, and a pool where none of them read is an absence rather than a
// clean build.
func TestAPoolOfRoundsThatNeverWatchedTheQueuesReportsNoContentionAtAll(t *testing.T) {
	blind := round(series(1, 10), func(r *Result) {
		r.LockWaitNote = "nothing watched the wait queues, so this run says nothing about " +
			"whether it blocked"
	})

	partial, err := Merge(round(series(1, 10), blocked(2, 400)), blind)
	require.NoError(t, err)
	require.NotNil(t, partial.LockWaits,
		"one round of two watched and the pool threw its measurement away")
	require.Equal(t, 2, *partial.LockWaits)
	require.Contains(t, partial.LockWaitNote, "1 of 2 rounds never read the wait queues")
	require.Contains(t, partial.LockWaitNote, "floor")
	require.Contains(t, partial.LockWaitNote, "nothing watched the wait queues")

	none, err := Merge(blind, blind)
	require.NoError(t, err)
	require.Nil(t, none.LockWaits,
		"no round read the queues and the pool reports a number, so an unwatched "+
			"comparison would read as a build that blocked")
	require.Nil(t, none.LockWaitMS)
	require.Nil(t, none.LockWaitPairs)
	require.Equal(t,
		"nothing watched the wait queues, so this run says nothing about whether it blocked",
		none.LockWaitNote)
}

// A watched pool that never queued keeps its measured zero, and keeps it as a
// zero rather than as a nil. This is the arm that stops the rule above being
// implemented as "any absence is nil": zero contention is the answer everybody
// wants and a run that watched is entitled to give it.
func TestAPoolThatWatchedAndNeverQueuedKeepsItsMeasuredZero(t *testing.T) {
	quiet := round(series(1, 10), blocked(0, 0))
	pooled, err := Merge(quiet, quiet)
	require.NoError(t, err)
	require.NotNil(t, pooled.LockWaits)
	require.Equal(t, 0, *pooled.LockWaits)
	require.NotNil(t, pooled.LockWaitMS)
	require.NotNil(t, pooled.LockWaitPairs,
		"a watched pool that never queued reports no pairs, and nil would say nobody looked")
	require.Empty(t, pooled.LockWaitPairs)
}

// More distinct pairs than a result holds, and the pool gives up the ones that
// waited LEAST. A round drops whichever pair it meets after its sixty fourth,
// because it folds samples in as they arrive; a pool has every pair in hand, so
// dropping the worst one would be discarding the finding and keeping the noise.
func TestPoolingMorePairsThanAResultHoldsDropsTheSmallestAndSaysSo(t *testing.T) {
	var many []LockWait
	for i := 0; i <= maxLockPairs; i++ {
		many = append(many, pair("select", fmt.Sprintf("hold %d", i), "tuple", 1, float64(i+1)))
	}
	pooled, err := Merge(
		round(series(1, 10), blocked(1, 1, many...)),
		round(series(1, 10), blocked(1, 1)),
	)
	require.NoError(t, err)
	require.Len(t, pooled.LockWaitPairs, maxLockPairs)
	require.InDelta(t, float64(len(many)), pooled.LockWaitPairs[0].WaitedMS, 1e-9,
		"the worst pair is not at the top of a capped list")
	require.InDelta(t, 2.0, pooled.LockWaitPairs[maxLockPairs-1].WaitedMS, 1e-9,
		"the pool dropped a pair that waited longer than one it kept")
	require.Contains(t, pooled.LockWaitNote, "1 further distinct blocking pairs")
}

func repeat(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}
