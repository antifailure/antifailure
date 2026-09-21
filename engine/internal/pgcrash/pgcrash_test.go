package pgcrash_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/pgcrash"
)

// The tests in this file drive the parts of the crash proof that decide what a
// run MEANS, with no database anywhere near them. That is deliberate: those
// parts are the ones whose wrong branches never run in a green suite, because a
// healthy Postgres on a healthy machine produces one output over and over.
// Feeding them the control file of a cluster that never came back, the log of a
// database that was cleanly shut down and a ledger whose sets do not add up is
// the only way the refusing half of each of them is ever executed.

// A pg_controldata output, as Postgres 17 prints it, trimmed to the lines this
// package reads plus enough of its neighbours that a positional parser would
// be caught.
const controlOut = `pg_control version number:            1300
Catalog version number:               202406281
Database system identifier:           7449374374374374374
Database cluster state:               in production
pg_control last modified:             Sat Sep 20 16:00:15 2026
Latest checkpoint location:           0/19504D0
Latest checkpoint's REDO location:    0/1950478
Latest checkpoint's REDO WAL file:    000000010000000000000001
Latest checkpoint's TimeLineID:       1
Latest checkpoint's PrevTimeLineID:   1
Latest checkpoint's full_page_writes: on
Data page checksum version:           0
`

func TestParseControl_ReadsTheLinesTheAssertionsRestOn(t *testing.T) {
	c, err := pgcrash.ParseControl(controlOut)
	require.NoError(t, err)
	require.Equal(t, "in production", c.State)
	require.Equal(t, "0/19504D0", c.CheckpointLSN)
	// The redo location and the checkpoint location are different lines whose
	// labels share a prefix, and reading the wrong one makes every replay
	// assertion compare a number against itself.
	require.Equal(t, "0/1950478", c.RedoLSN)
	require.Equal(t, 1, c.TimeLine)
	require.Equal(t, 0, c.ChecksumVersion)
	require.True(t, c.InProduction())
	require.False(t, c.ChecksumsEnabled())
}

func TestParseControl_RefusesOutputItCouldNotRead(t *testing.T) {
	// Every one of these is a real way the read fails: a version that renamed
	// a line, a command that printed an error, an empty answer from a
	// container that was not running. None may come back as a zero value,
	// because a zero checkpoint reads as "the checkpoint did not move" and
	// would raise a recovery finding against the customer's database.
	for name, in := range map[string]string{
		"empty":            "",
		"an error message": "pg_controldata: could not open file\n",
		"no redo line": `Database cluster state:               in production
Latest checkpoint location:           0/19504D0
Latest checkpoint's TimeLineID:       1
Data page checksum version:           0
`,
		"a timeline that is not a number": `Database cluster state:               in production
Latest checkpoint location:           0/19504D0
Latest checkpoint's REDO location:    0/1950478
Latest checkpoint's TimeLineID:       one
Data page checksum version:           0
`,
	} {
		t.Run(name, func(t *testing.T) {
			c, err := pgcrash.ParseControl(in)
			require.Error(t, err, "unreadable output was accepted")
			require.Empty(t, c.State, "a refused parse still returned a state")
			require.Zero(t, c.TimeLine)
		})
	}
}

func TestParseLSN_OrdersPositionsByValueAndNotByText(t *testing.T) {
	// The failure this exists for: as text, "0/9FFFFFF" sorts above
	// "0/10000000", so a string comparison reports that recovery went
	// backwards on every run that crosses a power of sixteen.
	low, err := pgcrash.ParseLSN("0/9FFFFFF")
	require.NoError(t, err)
	high, err := pgcrash.ParseLSN("0/10000000")
	require.NoError(t, err)
	require.Less(t, low, high, "the low half is not being compared as a number")

	// And the high half is a separate word, so it outranks everything in the
	// low one.
	rollover, err := pgcrash.ParseLSN("1/0")
	require.NoError(t, err)
	top, err := pgcrash.ParseLSN("0/FFFFFFFF")
	require.NoError(t, err)
	require.Greater(t, rollover, top)
	require.Equal(t, "1/0", pgcrash.FormatLSN(rollover))

	for _, bad := range []string{"", "0", "/1", "1/", "0/ZZZ", "ZZZ/0"} {
		_, err := pgcrash.ParseLSN(bad)
		require.Errorf(t, err, "%q was read as a log sequence number", bad)
	}
}

// crashLog is what a Postgres 17 postmaster writes around a backend killed
// with SIGKILL, taken verbatim from a run of this suite.
const crashLog = `2026-09-20 16:00:22.143 UTC [1] LOG:  checkpointer process (PID 56) was terminated by signal 9: Killed
2026-09-20 16:00:22.800 UTC [1] LOG:  all server processes terminated; reinitializing
2026-09-20 16:00:23.664 UTC [100] LOG:  database system was interrupted; last known up at 2026-09-20 16:00:15 UTC
2026-09-20 16:00:27.543 UTC [100] LOG:  database system was not properly shut down; automatic recovery in progress
2026-09-20 16:00:27.598 UTC [100] LOG:  redo starts at 0/1950478
2026-09-20 16:00:27.649 UTC [100] LOG:  redo done at 0/197AD88 system usage: CPU: user: 0.01 s, system: 0.00 s, elapsed: 0.05 s
2026-09-20 16:00:27.726 UTC [101] LOG:  checkpoint starting: end-of-recovery immediate wait
2026-09-20 16:00:28.811 UTC [1] LOG:  database system is ready to accept connections
`

// cleanLog is what the same postmaster writes when the container is stopped
// rather than killed. It has to produce a different answer, and it is the one
// that catches a recovery check that reports every successful reconnection as
// a recovery.
const cleanLog = `2026-09-20 16:36:09.100 UTC [1] LOG:  received fast shutdown request
2026-09-20 16:36:09.300 UTC [1] LOG:  aborting any active transactions
2026-09-20 16:36:10.724 UTC [1] LOG:  database system is shut down
2026-09-20 16:36:25.100 UTC [1] LOG:  database system was shut down at 2026-09-20 16:36:10 UTC
2026-09-20 16:36:25.237 UTC [1] LOG:  database system is ready to accept connections
`

func TestParseRecovery_ReadsACrashAndItsReplay(t *testing.T) {
	r := pgcrash.ParseRecovery(crashLog)
	require.True(t, r.Crashed)
	require.Equal(t, 9, r.Signal)
	require.Contains(t, r.CrashLine, "checkpointer process (PID 56)")
	require.True(t, r.Reinitialised)
	require.True(t, r.Unclean)
	require.Equal(t, "0/1950478", r.RedoStart)
	require.Equal(t, "0/197AD88", r.RedoEnd)
	require.True(t, r.Replayed())
	require.False(t, r.RedoNotRequired)
	require.True(t, r.Ready)
}

func TestParseRecovery_SaysACleanShutdownReplayedNothing(t *testing.T) {
	r := pgcrash.ParseRecovery(cleanLog)
	require.False(t, r.Crashed, "a clean shutdown was read as a crash")
	require.False(t, r.Unclean, "a clean shutdown was read as an unclean one")
	require.False(t, r.Replayed(), "a clean shutdown was read as having replayed")
	// It still came back, which is exactly why "it came back" is not the
	// assertion this package makes.
	require.True(t, r.Ready)
}

func TestParseRecovery_KeepsTheLastReplayWhenTheWindowHoldsTwo(t *testing.T) {
	// A container restarted twice inside one window. The assertion is about
	// the replay that brought the database back, so the later positions win.
	r := pgcrash.ParseRecovery(crashLog + `2026-09-20 16:01:00.000 UTC [1] LOG:  server process (PID 9) was terminated by signal 9: Killed
2026-09-20 16:01:01.000 UTC [200] LOG:  database system was not properly shut down; automatic recovery in progress
2026-09-20 16:01:01.100 UTC [200] LOG:  redo starts at 0/197AD88
2026-09-20 16:01:01.200 UTC [200] LOG:  redo done at 0/1990000
`)
	require.Equal(t, "0/197AD88", r.RedoStart)
	require.Equal(t, "0/1990000", r.RedoEnd)
	// The first crash line is kept, because it is the one that started this.
	require.Contains(t, r.CrashLine, "checkpointer process (PID 56)")
}

func TestParseRecovery_ReadsRedoNotRequiredAsItsOwnOutcome(t *testing.T) {
	r := pgcrash.ParseRecovery(`2026-09-20 16:00:22.143 UTC [1] LOG:  checkpointer process (PID 56) was terminated by signal 9: Killed
2026-09-20 16:00:27.543 UTC [100] LOG:  database system was not properly shut down; automatic recovery in progress
2026-09-20 16:00:27.598 UTC [100] LOG:  redo is not required
2026-09-20 16:00:28.811 UTC [1] LOG:  database system is ready to accept connections
`)
	require.True(t, r.Crashed)
	require.True(t, r.RedoNotRequired)
	require.False(t, r.Replayed(), "a cluster with nothing to replay was reported as having replayed")
}

func TestReconcile_CountsALostCommitAndAPhantomSeparately(t *testing.T) {
	l := pgcrash.NewLedger()
	for _, id := range []int64{1, 2, 3} {
		l.Attempt(id)
		l.Acknowledged(id)
	}
	l.Attempt(4) // in flight when the fault landed

	// 2 is gone, 4 landed although the client never heard, 99 is there and was
	// never attempted at all.
	got := l.Reconcile(set(1, 3, 4, 99))

	require.Equal(t, 3, got.Acknowledged)
	require.Equal(t, 1, got.Unresolved)
	require.Equal(t, 4, got.Present)
	require.Equal(t, 1, got.LostCount)
	require.Equal(t, []int64{2}, got.LostSample)
	require.Equal(t, 1, got.PhantomCount)
	require.Equal(t, []int64{99}, got.PhantomSample)
	// The in flight commit that landed is neither, and reporting it as a
	// phantom would make the check cry wolf on every run.
	require.Equal(t, 1, got.UnresolvedLanded)
	require.True(t, got.Consistent)
	require.False(t, got.Held())
}

func TestReconcile_HoldsWhenNothingWasLostOrInvented(t *testing.T) {
	l := pgcrash.NewLedger()
	for id := int64(1); id <= 100; id++ {
		l.Attempt(id)
		l.Acknowledged(id)
	}
	l.Attempt(101)
	got := l.Reconcile(set(append(seq(1, 100), 101)...))
	require.Zero(t, got.LostCount)
	require.Zero(t, got.PhantomCount)
	require.Equal(t, 1, got.UnresolvedLanded)
	require.True(t, got.Held())
}

func TestReconcile_CountsEveryLostCommitAndSamplesTen(t *testing.T) {
	// The count and the sample are separate fields, because a report that
	// printed the sample's length would say "10 lost" whether 10 or 10,000
	// were gone.
	l := pgcrash.NewLedger()
	for id := int64(1); id <= 50; id++ {
		l.Attempt(id)
		l.Acknowledged(id)
	}
	got := l.Reconcile(set())
	require.Equal(t, 50, got.LostCount)
	require.Len(t, got.LostSample, 10)
	require.Equal(t, int64(1), got.LostSample[0], "the sample is not ordered, so two runs would name different ids")
	require.False(t, got.Held())
}

func TestReconcile_SaysSoWhenItsOwnSetsDoNotAddUp(t *testing.T) {
	// A ledger whose acknowledged set contains an id it never attempted is a
	// ledger with a bookkeeping hole. The arithmetic catches it, and the
	// result is not held: "I could not work out what happened" must never come
	// back as a clean run.
	l := pgcrash.NewLedger()
	l.Attempt(1)
	l.Acknowledged(1)
	// Acknowledged without Attempt, which is the shape of the hole.
	l.Acknowledged(2)

	got := l.Reconcile(set(1))
	require.Equal(t, 2, got.Acknowledged)
	require.Equal(t, 1, got.Present)
	require.Equal(t, 1, got.LostCount)
	require.True(t, got.Consistent, "this case does add up: one acknowledged commit survived and one is gone")

	// The one that genuinely does not add up: a row that is counted twice
	// cannot happen through the ledger, so it is built by hand.
	require.False(t, pgcrash.Reconciliation{
		Acknowledged: 5, Present: 9, UnresolvedLanded: 0, Consistent: false,
	}.Held(), "an inconsistent reconciliation was reported as held")
}

func TestFlushLSN_KeepsTheHighestPositionSeen(t *testing.T) {
	l := pgcrash.NewLedger()
	require.Zero(t, l.FlushLSN())
	l.ObservedFlushLSN(100)
	l.ObservedFlushLSN(200)
	// The last reading is the LOWEST on purpose. With the readings in rising
	// order, a version of this that simply assigned every reading would end on
	// the highest anyway and the assertion would hold while the guard was
	// gone.
	l.ObservedFlushLSN(50)
	require.Equal(t, uint64(200), l.FlushLSN(),
		"a later, lower reading replaced a higher one, so the lower bound on replay is wrong")
}

func TestValidSynchronousCommit_RefusesWhatItDoesNotKnow(t *testing.T) {
	// The value is concatenated into a SET statement, so the list is closed.
	require.True(t, pgcrash.ValidSynchronousCommit(""))
	for _, ok := range pgcrash.SynchronousCommitValues() {
		require.Truef(t, pgcrash.ValidSynchronousCommit(ok), "%q was refused", ok)
	}
	for _, bad := range []string{"off; DROP TABLE x", "ON", "maybe", "off "} {
		require.Falsef(t, pgcrash.ValidSynchronousCommit(bad), "%q was accepted", bad)
	}
}

func TestRules_AreAllDistinctAndNamespaced(t *testing.T) {
	// The rules are read by a policy, a report and a gate, and a duplicate
	// would silently give two findings one level.
	seen := map[string]bool{}
	for _, r := range pgcrash.Rules() {
		require.False(t, seen[r], "the rule %q is declared twice", r)
		seen[r] = true
		require.Regexp(t, `^chaos\.[a-z_]+\.[a-z_]+$`, r,
			"a rule outside the chaos namespace would not route to the chaos exit codes")
	}
	require.Len(t, pgcrash.Rules(), 12)
}

// set builds the row set a reconciliation reads.
func set(ids ...int64) map[int64]struct{} {
	out := map[int64]struct{}{}
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
}

// seq is the inclusive range from lo to hi.
func seq(lo, hi int64) []int64 {
	out := make([]int64, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, i)
	}
	return out
}

// TestJudge_ChecksumsOffOnlyWhenTheControlFileWasRead holds the checksums
// warning to what pg_controldata actually said. A control file that could not
// be parsed keeps its raw output and a zero checksum version, and reading that
// zero as "off" printed a fact nobody had read beside the finding saying the
// file was unreadable.
func TestJudge_ChecksumsOffOnlyWhenTheControlFileWasRead(t *testing.T) {
	rules := func(r pgcrash.Result) []string {
		var out []string
		for _, p := range r.Unverified {
			out = append(out, p.Rule)
		}
		return out
	}

	read, err := pgcrash.ParseControl(controlOut)
	require.NoError(t, err)
	off := pgcrash.Result{Before: read, After: read, Relations: pgcrash.Relations{Checked: true, Agreed: true}}
	pgcrash.JudgeForTest(&off, nil, nil)
	require.Contains(t, rules(off), pgcrash.RuleChecksumsOff,
		"a control file that says checksum version 0 did not raise the warning")

	garbled := "pg_controldata: some output this parser cannot read\n"
	_, parseErr := pgcrash.ParseControl(garbled)
	require.Error(t, parseErr)
	unread := pgcrash.Result{Before: read, After: pgcrash.Control{Raw: garbled},
		Relations: pgcrash.Relations{Checked: true, Agreed: true}}
	pgcrash.JudgeForTest(&unread, nil, parseErr)
	require.NotContains(t, rules(unread), pgcrash.RuleChecksumsOff,
		"a control file that could not be read was reported as checksums off")
	require.Contains(t, rules(unread), pgcrash.RuleControlUnreadable)
}
