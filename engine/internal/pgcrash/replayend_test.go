package pgcrash_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/pgcrash"
)

// ciCrashLog is the recovery main d20ded21f's engine job 106582811982 logged,
// with the end of recovery checkpoint's completion line the test's excerpt
// did not keep, in the format Postgres 17 prints it (LogCheckpointEnd in
// xlog.c). The redo position in it is the one the control file carried after
// recovery.
const ciCrashLog = `2026-09-22 01:58:38.881 UTC [1] LOG:  checkpointer process (PID 57) was terminated by signal 9: Killed
2026-09-22 01:58:38.902 UTC [1] LOG:  all server processes terminated; reinitializing
2026-09-22 01:58:38.927 UTC [100] LOG:  database system was interrupted; last known up at 2026-09-22 01:58:38 UTC
2026-09-22 01:58:38.979 UTC [100] LOG:  database system was not properly shut down; automatic recovery in progress
2026-09-22 01:58:38.983 UTC [100] LOG:  redo starts at 0/1950490
2026-09-22 01:58:38.985 UTC [100] LOG:  redo done at 0/19723A8 system usage: CPU: user: 0.00 s, system: 0.00 s, elapsed: 0.00 s
2026-09-22 01:58:38.989 UTC [101] LOG:  checkpoint starting: end-of-recovery immediate wait
2026-09-22 01:58:38.998 UTC [101] LOG:  checkpoint complete: wrote 30 buffers (0.2%); 0 WAL file(s) added, 0 removed, 0 recycled; write=0.003 s, sync=0.003 s, total=0.010 s; sync files=10, longest=0.001 s, average=0.001 s; distance=135 kB, estimate=135 kB; lsn=0/19723D0, redo lsn=0/19723D0
2026-09-22 01:58:38.999 UTC [1] LOG:  database system is ready to accept connections
`

// ciResult is the run main failed on: 729 commits acknowledged, 729 present,
// none lost, and a writer that read the flush position 0/19723D0.
func ciResult(log string) *pgcrash.Result {
	return &pgcrash.Result{
		Recovery: pgcrash.ParseRecovery(log),
		Before:   pgcrash.Control{State: "in production", CheckpointLSN: "0/19504E8", RedoLSN: "0/1950490", TimeLine: 1},
		After:    pgcrash.Control{State: "in production", CheckpointLSN: "0/19723D0", RedoLSN: "0/19723D0", TimeLine: 1},
		FlushLSN: "0/19723D0",
		Reconciliation: pgcrash.Reconciliation{
			Acknowledged: 729, Present: 729, Consistent: true,
		},
	}
}

func rulesOf(ps []pgcrash.Problem) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.Rule)
	}
	return out
}

// The false replay_short that turned main red. "redo done at 0/19723A8" is
// the START of the last record replayed, and the writer's flush position
// 0/19723D0 is the END of that same record, forty bytes on. Replay was
// complete to the byte and the run was failed for replaying less than the
// client saw flushed.
func TestJudge_AReplayThatEndedAtTheLastFlushIsNotShort(t *testing.T) {
	r := ciResult(ciCrashLog)
	pgcrash.JudgeCrashForTest(r)
	require.NotContains(t, rulesOf(r.Problems), pgcrash.RuleReplayShort,
		"a replay that ended exactly at the last flush was called short")
	require.Equal(t, "0/19723D0", r.ReplayEnd, "the end of replay was not read from the end of recovery checkpoint")
}

// Postgres 14 and 15 print no redo position in the checkpoint line, so the
// end of recovery checkpoint is read from the control file, and only when it
// has the shape of one: its redo position is its own location.
func TestJudge_WithoutTheLogPositionTheControlFileGivesTheEnd(t *testing.T) {
	r := ciResult(ciCrashLog)
	r.Recovery.EndOfRecoveryRedo = ""
	pgcrash.JudgeCrashForTest(r)
	require.NotContains(t, rulesOf(r.Problems), pgcrash.RuleReplayShort)
	require.NotContains(t, rulesOf(r.Unverified), pgcrash.RuleReplayShort,
		"a control file carrying the end of recovery checkpoint was not accepted as the end of replay")
	require.Equal(t, "0/19723D0", r.ReplayEnd)
}

// The check still says no. A replay whose end is short of what a writer saw
// flushed is a durability defect, reported as one.
func TestJudge_AReplayThatEndedBeforeTheFlushIsShort(t *testing.T) {
	r := ciResult(ciCrashLog)
	r.FlushLSN = "0/1980000"
	pgcrash.JudgeCrashForTest(r)
	require.Contains(t, rulesOf(r.Problems), pgcrash.RuleReplayShort,
		"a replay that ended at 0/19723D0 against a flush at 0/1980000 was not called short")
}

// With neither reading available the end of replay is not known, and the
// run says it could not look rather than passing or failing.
func TestJudge_AnEndOfReplayNobodyRecordedIsUnverified(t *testing.T) {
	r := ciResult(ciCrashLog)
	r.Recovery.EndOfRecoveryRedo = ""
	// An online checkpoint's shape: redo before its record.
	r.After.CheckpointLSN = "0/1980000"
	pgcrash.JudgeCrashForTest(r)
	require.NotContains(t, rulesOf(r.Problems), pgcrash.RuleReplayShort)
	require.Contains(t, rulesOf(r.Unverified), pgcrash.RuleReplayShort,
		"a control file that is not the end of recovery checkpoint was taken as the end of replay")
	require.Empty(t, r.ReplayEnd)
}

// A position before the last record replayed cannot be where replay ended,
// so it describes some other checkpoint and is refused.
func TestJudge_AnEndBeforeTheLastRecordIsRefused(t *testing.T) {
	r := ciResult(ciCrashLog)
	r.Recovery.EndOfRecoveryRedo = "0/1960000"
	pgcrash.JudgeCrashForTest(r)
	require.NotContains(t, rulesOf(r.Problems), pgcrash.RuleReplayShort)
	require.Contains(t, rulesOf(r.Unverified), pgcrash.RuleReplayShort)
}

// The completion line is taken only when it follows the end of recovery
// checkpoint starting. An ordinary checkpoint's line carries a redo position
// too, and reading it would give the end of replay a later value than it had.
func TestParseRecovery_ReadsOnlyTheEndOfRecoveryCheckpoint(t *testing.T) {
	r := pgcrash.ParseRecovery(ciCrashLog)
	require.Equal(t, "0/19723D0", r.EndOfRecoveryRedo)

	r = pgcrash.ParseRecovery(`2026-09-22 01:58:30.000 UTC [27] LOG:  checkpoint starting: immediate force wait
2026-09-22 01:58:30.100 UTC [27] LOG:  checkpoint complete: wrote 38 buffers (0.2%); distance=1 kB, estimate=1 kB; lsn=0/1950490, redo lsn=0/1950490
` + ciCrashLog + `2026-09-22 01:59:40.000 UTC [101] LOG:  checkpoint starting: time
2026-09-22 01:59:40.100 UTC [101] LOG:  checkpoint complete: wrote 3 buffers (0.0%); distance=1 kB, estimate=1 kB; lsn=0/1990000, redo lsn=0/1988000
`)
	require.Equal(t, "0/19723D0", r.EndOfRecoveryRedo,
		"a checkpoint other than the end of recovery one was read as the end of replay")
}
