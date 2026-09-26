package sqlload

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// The arithmetic of folding samples into a wait count, tested where a server
// cannot reach it.
//
// The postgres suite proves the query is real: that pg_blocking_pids answers,
// that a blocked backend is this run's and a holder may not be, and that the
// labels join. What it cannot do stably is prove what a sequence of samples
// MEANS, because it cannot choose which samples land. A wait that continues
// across four samples has to be one wait and four intervals, a wait that
// stopped and started has to be two, and a sample that failed must not make
// the next one look like a continuation. Those are decisions in this file and
// they are tested against a sequence chosen on purpose.

const testIntervalMS = 200

func sampleFrom(rows map[int32]LockWait) lockSample {
	s := lockSample{waiting: map[string]lockRow{}, pids: map[int32]bool{}}
	for pid, w := range rows {
		s.pids[pid] = true
		key := lockKey(w)
		r := s.waiting[key]
		r.wait = w
		r.count++
		s.waiting[key] = r
	}
	return s
}

func aWait(blocked, blocking string) LockWait {
	return LockWait{
		BlockedTransaction: "tx", BlockedStatement: blocked,
		BlockingTransaction: "tx", BlockingStatement: blocking,
		BlockingState: "active", BlockingInRun: true,
		LockType: "transactionid", Mode: "ShareLock",
	}
}

// TestAWaitHeldAcrossSamplesIsOneWaitAndManyIntervals.
//
// The number a reader cares about most is "how often did this build queue",
// and a sampler that answered it by counting samples would report a single
// slow lock as twenty waits. The interval total is the other half: the same
// wait costs four intervals because it was still there four times.
func TestAWaitHeldAcrossSamplesIsOneWaitAndManyIntervals(t *testing.T) {
	totals := newLockTotals()
	s := sampleFrom(map[int32]LockWait{101: aWait("take the row", "hold it")})
	for i := 0; i < 4; i++ {
		totals.add(s, testIntervalMS)
	}

	require.True(t, totals.ran)
	require.Equal(t, 1, totals.waits,
		"one backend waiting through four samples was counted as four separate waits")
	require.InDelta(t, 4*testIntervalMS, totals.waitMS, 1e-9)

	pairs := totals.list()
	require.Len(t, pairs, 1)
	require.Equal(t, 1, pairs[0].Waits)
	require.InDelta(t, 4*testIntervalMS, pairs[0].WaitedMS, 1e-9)
}

// TestAWaitThatEndedAndStartedAgainIsTwoWaits.
//
// The other side of the same decision. If a continuing wait is one wait, then
// the sampler has to notice when it stops, or a run that queued fifty times
// with gaps between would report one very long wait.
func TestAWaitThatEndedAndStartedAgainIsTwoWaits(t *testing.T) {
	totals := newLockTotals()
	busy := sampleFrom(map[int32]LockWait{101: aWait("take the row", "hold it")})
	quiet := sampleFrom(nil)

	totals.add(busy, testIntervalMS)
	totals.add(quiet, testIntervalMS)
	totals.add(busy, testIntervalMS)

	require.Equal(t, 2, totals.waits,
		"a wait that stopped and started again was counted once")
	require.InDelta(t, 2*testIntervalMS, totals.waitMS, 1e-9,
		"the sample with nobody waiting was charged an interval")
	require.Equal(t, 2, totals.list()[0].Waits)
}

// TestTwoBackendsInOnePairAreTwoWaitsAndTwoIntervals.
//
// Two clients queued behind one holder on the same statement produce ONE pair
// key and two waits, and a sampler that keyed on the pair alone would report
// half the contention it found. The run wide count is taken over backends for
// exactly this reason and the pair has to agree with it.
func TestTwoBackendsInOnePairAreTwoWaitsAndTwoIntervals(t *testing.T) {
	totals := newLockTotals()
	w := aWait("take the row", "hold it")
	totals.add(sampleFrom(map[int32]LockWait{101: w, 102: w}), testIntervalMS)

	require.Equal(t, 2, totals.waits)
	require.InDelta(t, 2*testIntervalMS, totals.waitMS, 1e-9)

	pairs := totals.list()
	require.Len(t, pairs, 1, "one pair shape seen twice became two rows")
	require.Equal(t, 2, pairs[0].Waits,
		"two backends in one pair were reported as one wait")
	require.InDelta(t, 2*testIntervalMS, pairs[0].WaitedMS, 1e-9)
}

// TestAFailedSampleDoesNotMakeTheNextOneLookLikeAContinuation.
//
// The ordering nobody enumerates. A sample fails in the middle of a wait, the
// next one succeeds and finds the same backend still queueing. Nobody knows
// what happened in the gap, so the choice is between counting that as one
// continuing wait or as a second one, and the second is the honest direction:
// it reports contention rather than hiding it behind a reading that never
// happened.
func TestAFailedSampleDoesNotMakeTheNextOneLookLikeAContinuation(t *testing.T) {
	totals := newLockTotals()
	s := sampleFrom(map[int32]LockWait{101: aWait("take the row", "hold it")})

	totals.add(s, testIntervalMS)
	totals.failed("the wait queues could not be read: boom")
	totals.add(s, testIntervalMS)

	require.Equal(t, 2, totals.waits,
		"a wait either side of a failed sample was reported as one uninterrupted wait, "+
			"which is a claim about an interval nobody read")
	require.Equal(t, "the wait queues could not be read: boom", totals.note)
}

// TestASampleThatLandedAndOneThatFailedReportBothHalves.
//
// The note a run carries when its wait queue reading was interrupted has to say
// that a reading was LOST, not that the counts stop there. Only the first
// failure is kept, so a run whose opening sample timed out under load and whose
// every later sample succeeded carries the same note, and "the counts stop
// here" would be a false claim about a run that recovered. The instrument's own
// limits still have to be attached, because the numbers are real.
func TestASampleThatLandedAndOneThatFailedReportBothHalves(t *testing.T) {
	o := &observer{pids: map[int32]bool{}, locks: newLockTotals()}
	// The failure first and the good samples after, which is the ordering that
	// catches a note claiming sampling stopped.
	o.locks.failed("the wait queues could not be read: timed out")
	o.locks.add(sampleFrom(map[int32]LockWait{101: aWait("take the row", "hold it")}),
		testIntervalMS)
	o.locks.add(sampleFrom(nil), testIntervalMS)

	var res Result
	o.locksInto(&res)

	require.NotNil(t, res.LockWaits, "samples landed and the result reports nothing")
	require.Equal(t, 1, *res.LockWaits)
	require.Contains(t, res.LockWaitNote, "timed out")
	require.Contains(t, res.LockWaitNote, "at least one reading of them was lost")
	require.Contains(t, res.LockWaitNote, "pg_blocking_pids",
		"a partly sampled run lost the instrument's own limits")
	require.NotContains(t, res.LockWaitNote, "stop at whatever",
		"a run that recovered was told its counts stopped at the failure")
}

// TestOnlyTheFirstFailureIsKept, for the reason the backend observer keeps
// only its first: a database that went away produces one informative reason
// and then one identical reason every 200 milliseconds for the rest of the run.
func TestOnlyTheFirstFailureIsKept(t *testing.T) {
	totals := newLockTotals()
	totals.failed("the first reason")
	totals.failed("the second reason")
	require.Equal(t, "the first reason", totals.note)
}

// TestThePairsComeBackWorstFirst, because a list somebody has to sort is a
// list where the finding is on line forty.
func TestThePairsComeBackWorstFirst(t *testing.T) {
	totals := newLockTotals()
	small := aWait("small", "holder")
	big := aWait("big", "holder")

	totals.add(sampleFrom(map[int32]LockWait{101: small, 102: big}), testIntervalMS)
	for i := 0; i < 5; i++ {
		totals.add(sampleFrom(map[int32]LockWait{102: big}), testIntervalMS)
	}

	pairs := totals.list()
	require.Len(t, pairs, 2)
	require.Equal(t, "big", pairs[0].BlockedStatement,
		"the pair that cost the most waiting was not first")
	require.Greater(t, pairs[0].WaitedMS, pairs[1].WaitedMS)
}

// TestTheCapOnPairsIsCountedRatherThanSilentlyDropped.
//
// A list that quietly stopped growing is a list somebody reads as complete.
// The cap exists so one surprising mix cannot grow a map for a whole run, and
// the count exists so the truncation reaches the note.
func TestTheCapOnPairsIsCountedRatherThanSilentlyDropped(t *testing.T) {
	totals := newLockTotals()
	rows := map[int32]LockWait{}
	for i := 0; i < maxLockPairs+7; i++ {
		rows[int32(1000+i)] = aWait("statement "+string(rune('a'+i%26))+string(rune('a'+i/26)), "holder")
	}
	totals.add(sampleFrom(rows), testIntervalMS)

	require.Len(t, totals.pairs, maxLockPairs, "the cap did not hold")
	require.Equal(t, 7, totals.dropped,
		"pairs were dropped and the count did not say how many")
	require.Equal(t, maxLockPairs+7, totals.waits,
		"the cap on the pair list changed the run wide count, which it must not: "+
			"the waits happened whether or not a row was kept for them")
}

// TestTheHoldersStateIsPartOfThePairIdentity.
//
// The same two statements blocking each other while the holder is executing
// and while the holder sits idle in transaction are two different defects with
// two different fixes. A key that merged them would name neither.
func TestTheHoldersStateIsPartOfThePairIdentity(t *testing.T) {
	// Two holders OUTSIDE the run, which is what isolates the claim. A holder
	// inside the run carries no statement label while it is idle, because its
	// client withdrew it, so an in run pair already differs on the statement
	// and a key that ignored the state entirely would still split those two.
	// A stranger's session never has a label either way, so these two rows
	// differ in the state and in nothing else: if the key drops it, they
	// merge, and a page shows one row that is half a holder doing work and
	// half a holder doing nothing.
	//
	// The first version of this test used an in run pair and SURVIVED exactly
	// that mutation, for exactly that reason.
	active := aWait("take the row", "")
	active.BlockingInRun = false
	active.BlockingTransaction = ""
	idle := active
	idle.BlockingState = "idle in transaction"

	require.NotEqual(t, active, idle, "the two fixtures differ in nothing at all")
	require.NotEqual(t, lockKey(active), lockKey(idle))

	totals := newLockTotals()
	totals.add(sampleFrom(map[int32]LockWait{101: active, 102: idle}), testIntervalMS)
	require.Len(t, totals.list(), 2,
		"a holder that was working and a holder that was sitting on its locks "+
			"were merged into one row")
}

// TestTheBoundNamesTheIntervalItWasBuiltFrom, so a sentence that quotes a
// number another constant owns cannot go stale when somebody tunes it.
func TestTheBoundNamesTheIntervalItWasBuiltFrom(t *testing.T) {
	require.Contains(t, LockWaitBound, observeInterval.String())
	require.Contains(t, LockWaitBound, "pg_blocking_pids")
}

// TestAClientPublishesWhatItIsRunningAndWithdrawsIt.
//
// The registry is what turns a pid into a statement label, and the withdrawal
// is the half that matters: a holder that is idle in transaction must report
// NO statement rather than the last one it ran, because reporting the last one
// would send a reader to a statement that had already finished.
func TestAClientPublishesWhatItIsRunningAndWithdrawsIt(t *testing.T) {
	reg := &statements{pids: []uint32{4242}, current: make([]atomic.Pointer[stmtRef], 1)}
	tr := track{all: reg, index: 0}
	ref := &stmtRef{Transaction: "tx", Label: "take the row"}

	got, mine := reg.lookup(4242)
	require.True(t, mine, "the run's own backend was not recognised as its own")
	require.Nil(t, got)

	tr.begin(ref)
	got, mine = reg.lookup(4242)
	require.True(t, mine)
	require.Equal(t, "take the row", got.Label)

	tr.end()
	got, _ = reg.lookup(4242)
	require.Nil(t, got, "a client that finished its statement still claimed to be running it")

	got, mine = reg.lookup(9999)
	require.False(t, mine, "a backend that is not this run's was claimed as one of its clients")
	require.Nil(t, got)
}

// TestLabelStatementsGivesEveryStatementItsIdentity, because a statement with
// no ref publishes nothing and a whole transaction would come back unnamed.
func TestLabelStatementsGivesEveryStatementItsIdentity(t *testing.T) {
	mix := &Mix{Transactions: []Transaction{
		{Name: "one", Statements: []Statement{{Label: "a"}, {Label: "b"}}},
		{Name: "two", Statements: []Statement{{Label: "c"}}},
	}}
	labelStatements(mix)

	require.Equal(t, "one", mix.Transactions[0].Statements[0].ref.Transaction)
	require.Equal(t, "a", mix.Transactions[0].Statements[0].ref.Label)
	require.Equal(t, "b", mix.Transactions[0].Statements[1].ref.Label)
	require.Equal(t, "two", mix.Transactions[1].Statements[0].ref.Transaction)
	require.Equal(t, "c", mix.Transactions[1].Statements[0].ref.Label)
}

// TestNoIntervalIsChargedForASampleThatFoundNothing, which is the zero this
// whole shape exists to be able to produce.
func TestNoIntervalIsChargedForASampleThatFoundNothing(t *testing.T) {
	totals := newLockTotals()
	for i := 0; i < 10; i++ {
		totals.add(sampleFrom(nil), testIntervalMS)
	}
	require.True(t, totals.ran, "ten samples landed and the totals say nobody looked")
	require.Zero(t, totals.waits)
	require.Zero(t, totals.waitMS)
	require.Empty(t, totals.list())
}
