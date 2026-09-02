package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/state"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), state.DirName)
	db, err := state.Open(context.Background(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return NewStore(db, clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))), dir
}

func args(pairs ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i].(string)] = pairs[i+1]
	}
	return m
}

func TestSubmit_RepeatedKeyWithTheSameInputsReturnsTheSameRun(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	first, created, fault := s.Submit(ctx, "cli", "repo", "rehearse", "key-1",
		args("file", "001.sql"))
	require.Nil(t, fault)
	require.True(t, created)

	// The retry a client makes after a timeout. It must find the experiment
	// that is already running rather than start a second one, which would
	// double the cost and produce two verdicts for one question.
	second, created, fault := s.Submit(ctx, "cli", "repo", "rehearse", "key-1",
		args("file", "001.sql"))
	require.Nil(t, fault)
	require.False(t, created)
	require.Equal(t, first.ID, second.ID)
}

func TestSubmit_RepeatedKeyWithDifferentInputsConflicts(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	_, _, fault := s.Submit(ctx, "cli", "repo", "rehearse", "key-1", args("file", "001.sql"))
	require.Nil(t, fault)

	// The same key naming a different experiment. Answering with the first
	// run would report one experiment's verdict as though it were another's,
	// which is the most damaging thing a cache can do.
	_, _, fault = s.Submit(ctx, "cli", "repo", "rehearse", "key-1", args("file", "002.sql"))
	require.NotNil(t, fault)
	require.Equal(t, FaultIdempotencyConflict, fault.Code)
	require.Equal(t, "idempotency_key", fault.Field)
}

func TestSubmit_CanonicalInputsIgnoreMemberOrder(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	// The same call with its members in a different order is the same call.
	// Hashing the bytes as they arrived would make it a conflict, and a
	// client that serialises a map would hit that at random.
	first, _, fault := s.Submit(ctx, "cli", "repo", "rehearse", "k",
		args("a", "1", "b", "2"))
	require.Nil(t, fault)

	second, created, fault := s.Submit(ctx, "cli", "repo", "rehearse", "k",
		args("b", "2", "a", "1"))
	require.Nil(t, fault)
	require.False(t, created)
	require.Equal(t, first.ID, second.ID)
}

func TestSubmit_TheKeyIsScopedToCallerProjectAndTool(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()
	same := args("file", "001.sql")

	base, _, fault := s.Submit(ctx, "cli", "repo", "rehearse", "shared", same)
	require.Nil(t, fault)

	// Each of these shares the key and differs in one scope element, so each
	// must get its own run. Merging any of them would let one caller's retry
	// return another caller's experiment.
	for _, tc := range []struct{ caller, project, tool string }{
		{"other-client", "repo", "rehearse"},
		{"cli", "other-repo", "rehearse"},
		{"cli", "repo", "stress"},
	} {
		got, created, fault := s.Submit(ctx, tc.caller, tc.project, tc.tool, "shared", same)
		require.Nil(t, fault)
		require.True(t, created, "%v must not collide with the base run", tc)
		require.NotEqual(t, base.ID, got.ID)
	}
}

func TestSubmit_WithoutAKeyAlwaysCreatesANewRun(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	// Keyless runs are not part of the idempotency scheme, so two identical
	// keyless submissions are two experiments. The partial index is what
	// keeps them from colliding on an empty key.
	first, _, fault := s.Submit(ctx, "cli", "repo", "rehearse", "", args("file", "001.sql"))
	require.Nil(t, fault)
	second, created, fault := s.Submit(ctx, "cli", "repo", "rehearse", "", args("file", "001.sql"))
	require.Nil(t, fault)
	require.True(t, created)
	require.NotEqual(t, first.ID, second.ID)
}

func TestGet_RefusesACrossProjectLookupAsNotFound(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	run, _, fault := s.Submit(ctx, "cli", "repo-a", "rehearse", "", args("file", "1.sql"))
	require.Nil(t, fault)

	// Not "forbidden". Telling a caller that a run exists but belongs to
	// somebody else is itself a disclosure, so the two cases are made
	// indistinguishable by scoping the query rather than checking afterwards.
	_, fault = s.Get(ctx, "cli", "repo-b", run.ID)
	require.NotNil(t, fault)
	require.Equal(t, FaultRunNotFound, fault.Code)

	_, fault = s.Get(ctx, "other-caller", "repo-a", run.ID)
	require.NotNil(t, fault)
	require.Equal(t, FaultRunNotFound, fault.Code)

	got, fault := s.Get(ctx, "cli", "repo-a", run.ID)
	require.Nil(t, fault)
	require.Equal(t, run.ID, got.ID)
}

func TestGet_AnUnknownRunIDIsNotFound(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	_, fault := s.Get(context.Background(), "cli", "repo", "run_deadbeef")
	require.NotNil(t, fault)
	require.Equal(t, FaultRunNotFound, fault.Code)
}

func TestFinish_DerivesTheVerdictFromTheEngine(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	run, _, fault := s.Submit(ctx, "cli", "repo", "rehearse", "", args())
	require.Nil(t, fault)
	require.NoError(t, s.Finish(ctx, run.ID, report.VerdictFail, map[string]any{"ok": false}))

	got, fault := s.Get(ctx, "cli", "repo", run.ID)
	require.Nil(t, fault)
	require.Equal(t, StatusFinished, got.Status)
	require.Equal(t, VerdictFail, got.Verdict)
	require.Equal(t, report.VerdictFail, got.NativeVerdict, "the engine's own word is kept")

	var doc map[string]any
	require.NoError(t, json.Unmarshal(got.Result, &doc))
	require.Equal(t, false, doc["ok"])
}

func TestVerdictFor_NeverTurnsAnIncompleteRunIntoAPass(t *testing.T) {
	t.Parallel()
	// The single most important property in this package. An experiment that
	// did not finish says nothing about the change, and reporting it as a
	// pass would defeat the reason a model calls this server at all.
	for _, native := range []string{
		report.VerdictBlocked, report.VerdictUnverified, report.VerdictFlaky,
		"", "something-added-later", "PASS", "pass ",
	} {
		require.Equal(t, VerdictInconclusive, verdictFor(native),
			"the native verdict %q must not read as a pass", native)
	}
	require.Equal(t, VerdictPass, verdictFor(report.VerdictPass))
	require.Equal(t, VerdictPass, verdictFor(report.VerdictWarn))
	require.Equal(t, VerdictFail, verdictFor(report.VerdictFail))
}

func TestFail_RecordsInconclusiveRatherThanNothing(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	run, _, _ := s.Submit(ctx, "cli", "repo", "rehearse", "", args())
	require.NoError(t, s.Fail(ctx, run.ID,
		faultf(FaultSafetyUnavailable, "the sandbox could not be established")))

	got, fault := s.Get(ctx, "cli", "repo", run.ID)
	require.Nil(t, fault)
	require.Equal(t, StatusFailed, got.Status)
	// Written rather than left empty, so a caller reading only the verdict
	// cannot mistake an absent word for a passing one.
	require.Equal(t, VerdictInconclusive, got.Verdict)
	require.Equal(t, string(FaultSafetyUnavailable), got.ErrorCode)
}

func TestRecoverInterrupted_SettlesRunsLeftByADeadProcess(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	queued, _, _ := s.Submit(ctx, "cli", "repo", "rehearse", "a", args("n", "1"))
	running, _, _ := s.Submit(ctx, "cli", "repo", "rehearse", "b", args("n", "2"))
	require.NoError(t, s.Start(ctx, running.ID, "applying"))
	done, _, _ := s.Submit(ctx, "cli", "repo", "rehearse", "c", args("n", "3"))
	require.NoError(t, s.Finish(ctx, done.ID, report.VerdictPass, map[string]any{}))

	// The server restarts. A run recorded as running while no process is
	// running it will never progress, so a caller would poll it forever.
	settled, err := s.RecoverInterrupted(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, settled)

	for _, id := range []string{queued.ID, running.ID} {
		got, fault := s.Get(ctx, "cli", "repo", id)
		require.Nil(t, fault)
		require.Equal(t, StatusFailed, got.Status)
		require.Equal(t, VerdictInconclusive, got.Verdict,
			"an interrupted experiment proves nothing about the change")
	}

	// A run that had already finished keeps its verdict. Recovery settles
	// what is unsettled and must not rewrite history.
	got, fault := s.Get(ctx, "cli", "repo", done.ID)
	require.Nil(t, fault)
	require.Equal(t, StatusFinished, got.Status)
	require.Equal(t, VerdictPass, got.Verdict)
}

func TestRunStateSurvivesTheProcessThatCreatedIt(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), state.DirName)
	ctx := context.Background()
	c := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	db, err := state.Open(ctx, dir)
	require.NoError(t, err)
	run, _, fault := NewStore(db, c).Submit(ctx, "cli", "repo", "rehearse", "k", args("n", "1"))
	require.Nil(t, fault)
	require.NoError(t, db.Close())

	// A second process opens the same directory. This is the whole reason the
	// store is durable: submit and poll are separate calls, and a server that
	// forgot its runs on restart would answer RUN_NOT_FOUND for work that
	// really happened.
	reopened, err := state.Open(ctx, dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })

	got, fault := NewStore(reopened, c).Get(ctx, "cli", "repo", run.ID)
	require.Nil(t, fault)
	require.Equal(t, run.ID, got.ID)
}

func TestCancel_IsARequestAndIsRefusedOnceTerminal(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	run, _, _ := s.Submit(ctx, "cli", "repo", "rehearse", "", args())
	require.False(t, s.Cancelled(ctx, run.ID))

	got, fault := s.RequestCancel(ctx, "cli", "repo", run.ID)
	require.Nil(t, fault)
	require.True(t, got.CancelRequested)
	require.True(t, s.Cancelled(ctx, run.ID), "the running experiment can observe the request")

	require.NoError(t, s.MarkCancelled(ctx, run.ID))
	settled, fault := s.Get(ctx, "cli", "repo", run.ID)
	require.Nil(t, fault)
	require.Equal(t, StatusCancelled, settled.Status)
	require.Equal(t, VerdictInconclusive, settled.Verdict)

	// Cancelling something that already stopped is a mistake worth reporting
	// rather than a silent success.
	_, fault = s.RequestCancel(ctx, "cli", "repo", run.ID)
	require.NotNil(t, fault)
	require.Equal(t, FaultRunNotCancellable, fault.Code)
}

func TestCancel_RefusesAcrossProjects(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	run, _, _ := s.Submit(ctx, "cli", "repo-a", "rehearse", "", args())
	_, fault := s.RequestCancel(ctx, "cli", "repo-b", run.ID)
	require.NotNil(t, fault)
	require.Equal(t, FaultRunNotFound, fault.Code,
		"a run id is not a capability and must not act as one")
}

func TestMarkCancelled_DoesNotOverwriteAFinishedRun(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	// The race a cancel arriving at the finish line creates. The experiment
	// completed and produced a real verdict, so the cancel must lose:
	// rewriting a genuine PASS or FAIL into INCONCLUSIVE would discard a
	// result that was actually established.
	run, _, _ := s.Submit(ctx, "cli", "repo", "rehearse", "", args())
	require.NoError(t, s.Finish(ctx, run.ID, report.VerdictPass, map[string]any{}))
	require.NoError(t, s.MarkCancelled(ctx, run.ID))

	got, _ := s.Get(ctx, "cli", "repo", run.ID)
	require.Equal(t, StatusFinished, got.Status)
	require.Equal(t, VerdictPass, got.Verdict)
}
