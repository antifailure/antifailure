package mcp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/state"
)

// secondServer is what a second af mcp process on the same checkout does at
// startup, in this process: it opens the same state directory, settles what
// it takes to be interrupted runs, and serves get_rehearsal_run over them.
type secondServer struct {
	store   *Store
	server  *Server
	settled int
}

func startSecondServer(t *testing.T, dir string) *secondServer {
	t.Helper()
	db, err := state.Open(context.Background(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	c := clock.NewFake(time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC))
	b := &secondServer{store: NewStore(db, c)}
	settled, err := b.store.RecoverInterrupted(context.Background())
	require.NoError(t, err)
	b.settled = settled
	project := &Project{ID: "test-project", Root: t.TempDir(), Gate: report.Policy{}}
	b.server = NewServer(project.ID, b.store, nil)
	b.server.Register(newGetRunTool(project, b.store))
	return b
}

// On 2026-09-06 six explore_for_friction runs over one held MCP session each
// failed 10 to 12 seconds in with "The server stopped while this run was in
// progress", while the server had not stopped. Four af mcp processes were
// alive on the machine, and each one that started settled every run the
// others had in flight. This is that, in one process.
func TestTwoServers_ASecondServerStartingDoesNotSettleTheFirstServersLiveRun(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), state.DirName)
	a := newHarnessAt(t, dir)

	ack := a.call(t, "fake_rehearsal", map[string]any{})
	runID := ack["run_id"].(string)
	<-a.started

	b := startSecondServer(t, dir)
	require.Equal(t, 0, b.settled,
		"the first server is alive, so its run was not left by an earlier process")

	got := (&harness{server: b.server}).call(t, "get_rehearsal_run", map[string]any{"run_id": runID})
	require.Equal(t, string(StatusRunning), got["status"],
		"the second server reported the first server's live run as %v: %v", got["status"], got["error"])
	require.Nil(t, got["error"])

	close(a.release)
	a.engine.Wait()

	got = (&harness{server: b.server}).call(t, "get_rehearsal_run", map[string]any{"run_id": runID})
	require.Equal(t, string(StatusFinished), got["status"])
	require.Equal(t, string(VerdictPass), got["verdict"],
		"the verdict the first server wrote is what the second server reads")
}

// A dies for real, then B arrives: the one case the "server stopped" sentence
// is for, and the one that must still be reported that way.
func TestTwoServers_ARunWhoseProcessExitedIsSettledAsServerStopped(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), state.DirName)
	a := newHarnessAt(t, dir)
	a.store.owner = deadOwner(t)

	ack := a.call(t, "fake_rehearsal", map[string]any{})
	runID := ack["run_id"].(string)
	<-a.started

	b := startSecondServer(t, dir)
	require.Equal(t, 1, b.settled)
	got := (&harness{server: b.server}).call(t, "get_rehearsal_run", map[string]any{"run_id": runID})
	require.Equal(t, string(StatusFailed), got["status"])
	require.Equal(t, string(VerdictInconclusive), got["verdict"])
	errDoc := got["error"].(map[string]any)
	require.Equal(t, string(FaultSafetyUnavailable), errDoc["code"])
	require.Contains(t, errDoc["detail"], "The server stopped while this run was in progress")

	close(a.release)
	a.engine.Wait()
}

// heldElsewhere is what the orchestrator returns when another process holds
// the branch: the lock package's own refusal, with the holder's fields.
func heldElsewhere() error {
	return aferrors.Coded(aferrors.AFRUN003,
		"pid", "55799", "command", "af up", "since", "2026-09-06T05:38:35Z")
}

// A submitted run that could not take the branch is recorded as refused by
// the lock, naming the holder, not as a subsystem that could not be
// established with a list of causes that are not the cause.
func TestTwoServers_ARunRefusedByTheBranchLockNamesTheHolder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.server.Register(&Tool{
		Name: "locked_rehearsal",
		Input: &Schema{Type: "object", Required: []string{"project_id"},
			Properties: map[string]*Schema{"project_id": projectIDSchema()}},
		Handler: func(_ context.Context, call *Call, args map[string]any) (any, *Fault) {
			return h.engine.Submit(call, "locked_rehearsal", args,
				func(context.Context, string) (string, *ResultBody, *Fault) {
					return "", nil, &Fault{
						Code:      FaultSafetyUnavailable,
						Detail:    "The exploration could not be run, so nothing was observed.",
						Retryable: true,
						wrapped:   heldElsewhere(),
					}
				})
		},
	})
	ack := h.call(t, "locked_rehearsal", map[string]any{})
	h.engine.Wait()

	got := h.call(t, "get_rehearsal_run", map[string]any{"run_id": ack["run_id"]})
	require.Equal(t, string(StatusFailed), got["status"])
	errDoc := got["error"].(map[string]any)
	require.Equal(t, string(FaultBranchLocked), errDoc["code"])
	detail := errDoc["detail"].(string)
	require.Contains(t, detail, "AF-RUN-003")
	require.Contains(t, detail, "process 55799")
	require.Contains(t, detail, `running "af up"`)
	require.Contains(t, detail, "since 2026-09-06T05:38:35Z")
	require.Contains(t, detail, "Next: Wait for it to finish")
	require.NotContains(t, detail, "server stopped")
	require.NotContains(t, detail, "The exploration could not be run",
		"the tool's list of usual causes is not the cause")
}

// The same refusal from a tool that answers in the call itself.
func TestTwoServers_AReadRefusedByTheBranchLockNamesTheHolderOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.server.Register(&Tool{
		Name: "locked_read",
		Input: &Schema{Type: "object", Required: []string{"project_id"},
			Properties: map[string]*Schema{"project_id": projectIDSchema()}},
		Handler: func(context.Context, *Call, map[string]any) (any, *Fault) {
			return nil, &Fault{
				Code:      FaultSafetyUnavailable,
				Detail:    "The golden versions could not be listed.",
				Retryable: true,
				wrapped:   heldElsewhere(),
			}
		},
	})
	got := h.call(t, "locked_read", map[string]any{})
	require.Equal(t, string(FaultBranchLocked), got["code"])
	detail := got["detail"].(string)
	require.Contains(t, detail, "process 55799")
	require.Equal(t, 1, strings.Count(detail, "AF-RUN-003"),
		"the envelope must not append the explanation the lock detail already carries")
	require.Equal(t, true, got["retryable"])
	cause := got["cause"].(map[string]any)
	require.Equal(t, "AF-RUN-003", cause["code"])
	require.Contains(t, cause["next"], "af down")
}

// A fault that is not about the lock is left exactly as it was.
func TestRefineLockFault_LeavesOtherFaultsAlone(t *testing.T) {
	t.Parallel()
	f := &Fault{Code: FaultSafetyUnavailable, Detail: "d", wrapped: errors.New("docker is down")}
	require.Same(t, f, refineLockFault(f))
	require.Nil(t, refineLockFault(nil))
}
