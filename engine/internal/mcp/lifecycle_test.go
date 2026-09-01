package mcp

import (
	"bytes"
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

// harness is a server with a controllable experiment behind one tool.
//
// The experiment is a fake, and that is the point: what is under test here is
// the run machinery, which has to behave identically whatever the experiment
// does. Driving it with a real rehearsal would need Docker and a golden and
// would prove less, because a real one cannot be made to block on command.
type harness struct {
	server  *Server
	engine  *Engine
	store   *Store
	release chan struct{}
	started chan struct{}
	logs    *bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := filepath.Join(t.TempDir(), state.DirName)
	db, err := state.Open(context.Background(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	c := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	h := &harness{
		store:   NewStore(db, c),
		release: make(chan struct{}),
		started: make(chan struct{}, 1),
		logs:    &bytes.Buffer{},
	}
	project := &Project{ID: "test-project", Root: t.TempDir(), Gate: report.Policy{}}
	h.engine = NewEngine(context.Background(), project, h.store, h.logs)

	h.server = NewServer(project.ID, h.store, h.logs)
	h.server.Register(newGetRunTool(project, h.store))
	h.server.Register(newCancelRunTool(project, h.store))
	h.server.Register(&Tool{
		Name: "fake_rehearsal",
		Input: &Schema{Type: "object", Properties: map[string]*Schema{
			"idempotency_key": idempotencyKeySchema(),
		}},
		Handler: func(_ context.Context, call *Call, args map[string]any) (any, *Fault) {
			return h.engine.Submit(call, "fake_rehearsal", args,
				func(ctx context.Context, runID string) (string, *ResultBody, *Fault) {
					h.started <- struct{}{}
					<-h.release
					if h.engine.Cancelled(ctx, runID) {
						return "", nil, faultf(FaultRunNotCancellable, "stopped on request")
					}
					return report.VerdictPass, &ResultBody{
						Summary: "the fake experiment finished",
						// More evidence than one page holds, so the paging is
						// exercised through the protocol rather than only in
						// the unit tests.
						Evidence: manyRefs(45),
					}, nil
				})
		},
	})
	t.Cleanup(func() {
		select {
		case <-h.release:
		default:
			close(h.release)
		}
		h.engine.Wait()
	})
	return h
}

func manyRefs(n int) []Evidence {
	out := make([]Evidence, 0, n)
	for i := range n {
		out = append(out, Evidence{URI: "af://x", Kind: "synthetic", Note: string(rune('a' + i%26))})
	}
	return out
}

// call drives one tool call and returns the structured result.
func (h *harness) call(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	require.NoError(t, err)
	frames := initFrame + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":` + string(body) + "}\n"

	out := &bytes.Buffer{}
	require.NoError(t, h.server.Serve(context.Background(), bytes.NewBufferString(frames), out))

	var last map[string]any
	dec := json.NewDecoder(out)
	for dec.More() {
		var m map[string]any
		require.NoError(t, dec.Decode(&m))
		last = m
	}
	require.NotNil(t, last)
	result, ok := last["result"].(map[string]any)
	require.True(t, ok, "no result in %v", last)
	sc, ok := result["structuredContent"].(map[string]any)
	require.True(t, ok, "no structured content in %v", result)
	return sc
}

func TestLifecycle_SubmitPollFinish(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	ack := h.call(t, "fake_rehearsal", map[string]any{})
	runID, _ := ack["run_id"].(string)
	require.NotEmpty(t, runID)
	require.Equal(t, "rehearsal_submitted", ack["kind"])
	require.Equal(t, float64(pollAfterMs), ack["poll_after_ms"])

	<-h.started

	// While it runs the verdict must read INCONCLUSIVE, not blank. A caller
	// comparing against PASS by inequality has to get the right answer at
	// every moment, including before there is an answer.
	mid := h.call(t, "get_rehearsal_run", map[string]any{"run_id": runID})
	require.Equal(t, string(StatusRunning), mid["status"])
	require.Equal(t, string(VerdictInconclusive), mid["verdict"])
	require.Contains(t, mid["summary"], "not yet known")

	close(h.release)
	h.engine.Wait()

	done := h.call(t, "get_rehearsal_run", map[string]any{"run_id": runID})
	require.Equal(t, string(StatusFinished), done["status"])
	require.Equal(t, string(VerdictPass), done["verdict"])
	require.Equal(t, report.VerdictPass, done["native_verdict"])

	// The evidence is paged, and the page says how much there is.
	ev := done["evidence"].(map[string]any)
	require.Equal(t, float64(45), ev["total"])
	require.Equal(t, float64(maxEvidencePerPage), ev["shown"])
	require.True(t, ev["truncated"].(bool))
	cursor, _ := ev["next_cursor"].(string)
	require.NotEmpty(t, cursor)

	next := h.call(t, "get_rehearsal_run",
		map[string]any{"run_id": runID, "evidence_cursor": cursor})
	nextEv := next["evidence"].(map[string]any)
	require.Equal(t, float64(45), nextEv["total"], "the total does not change as pages advance")
	require.NotEqual(t, cursor, nextEv["next_cursor"])
}

func TestLifecycle_CancelStopsTheRunAndReportsInconclusive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	ack := h.call(t, "fake_rehearsal", map[string]any{})
	runID := ack["run_id"].(string)
	<-h.started

	got := h.call(t, "cancel_rehearsal_run", map[string]any{"run_id": runID})
	require.Equal(t, "cancellation_requested", got["kind"])

	close(h.release)
	h.engine.Wait()

	done := h.call(t, "get_rehearsal_run", map[string]any{"run_id": runID})
	require.Equal(t, string(VerdictInconclusive), done["verdict"],
		"an experiment that did not finish says nothing about the change")
	require.Contains(t, []any{string(StatusCancelled), string(StatusFailed)}, done["status"])

	// Cancelling a run that already stopped is a mistake worth reporting.
	again := h.call(t, "cancel_rehearsal_run", map[string]any{"run_id": runID})
	require.Equal(t, string(FaultRunNotCancellable), again["code"])
}

func TestLifecycle_IdempotencyThroughTheProtocol(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	first := h.call(t, "fake_rehearsal", map[string]any{"idempotency_key": "k"})
	<-h.started

	second := h.call(t, "fake_rehearsal", map[string]any{"idempotency_key": "k"})
	require.Equal(t, first["run_id"], second["run_id"])
	require.Equal(t, true, second["existing_run"])

	close(h.release)
	h.engine.Wait()
}

func TestLifecycle_AFailedRunCarriesItsCodeAndNotItsCause(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	run, _, fault := h.store.Submit(ctx, "tester", "test-project", "fake_rehearsal", "", nil)
	require.Nil(t, fault)
	require.NoError(t, h.store.Fail(ctx, run.ID, &Fault{
		Code:    FaultSafetyUnavailable,
		Detail:  "The sandbox could not be established.",
		wrapped: errSecretHostDetail,
	}))

	got := h.call(t, "get_rehearsal_run", map[string]any{"run_id": run.ID})
	require.Equal(t, string(VerdictInconclusive), got["verdict"])
	errDoc := got["error"].(map[string]any)
	require.Equal(t, string(FaultSafetyUnavailable), errDoc["code"])

	// The wrapped cause names the host and must not reach the caller. It is
	// written to the server log instead, where an operator will see it.
	body, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(body), "/var/secret/host/path")
}

// errSecretHostDetail stands in for an engine error that names the machine.
var errSecretHostDetail = &Fault{Code: FaultInternal, Detail: "/var/secret/host/path"}
