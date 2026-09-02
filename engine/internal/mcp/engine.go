package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Experiment is the work behind one submitted run.
//
// It is handed a context that outlives the tool call that submitted it, and a
// run id it uses to report progress and to observe cancellation. It returns
// the engine's own verdict word and the body of the result; this package maps
// the verdict and assembles the envelope, so no experiment can choose its own
// vocabulary.
type Experiment func(ctx context.Context, runID string) (native string, body *ResultBody, fault *Fault)

// ResultBody is what an experiment produces, before the envelope is added.
//
// Split from Result so that an experiment cannot set the fields it has no
// business setting. There is no way for one to write its own verdict, its own
// run id or its own status: those come from the store, which is the only thing
// that knows them.
type ResultBody struct {
	Summary  string
	Findings FindingPage
	Metrics  []Metric
	Evidence []Evidence
	// Detail is the tool specific evidence, already stripped of anything the
	// candidate repository wrote. Each tool has its own shape, so the store
	// carries it as an opaque document rather than knowing them all.
	Detail any
}

// Engine submits and runs experiments.
type Engine struct {
	project *Project
	store   *Store
	log     io.Writer

	// base is the context every experiment runs under. It is the server's
	// lifetime and never a request's: an experiment that inherited the
	// context of the call that submitted it would be cancelled the instant
	// that call returned, which for an asynchronous submission is
	// immediately.
	//nolint:containedctx // an experiment outlives the call that submitted it,
	// so the lifetime it runs under cannot be a request's; this is the
	// server's, cancelled once when the server stops.
	base context.Context

	wg sync.WaitGroup
}

// NewEngine builds the experiment runner.
//
// It takes no clock: every timestamp a run carries is written by the store,
// which has one, and a second clock here would be a second answer to what time
// it is.
func NewEngine(base context.Context, p *Project, store *Store, log io.Writer) *Engine {
	if log == nil {
		log = io.Discard
	}
	return &Engine{project: p, store: store, log: log, base: base}
}

// Wait blocks until every started experiment has stopped.
//
// Called on shutdown so that a server being closed does not leave an
// environment half built. An experiment abandoned mid flight is the leak this
// product exists to prevent, so the process waits for its own work rather than
// exiting out from under it.
func (e *Engine) Wait() { e.wg.Wait() }

// pollAfterMs is how long a caller is asked to wait before polling.
//
// A number rather than a promise of a notification, because the transport has
// no way to push one and a caller that polls a busy server every hundred
// milliseconds is a caller making the run slower.
const pollAfterMs = 2000

// submission is the acknowledgement a submitting tool returns.
//
// It is deliberately small. The answer to "did you accept this" is a run id
// and a status, and anything more would be a result the experiment has not
// produced yet.
type submission struct {
	Kind        string `json:"kind"`
	RunID       string `json:"run_id"`
	Tool        string `json:"tool"`
	Status      Status `json:"status"`
	Phase       string `json:"phase"`
	PollAfterMs int    `json:"poll_after_ms"`
	// Existing marks a submission answered from a previous identical request
	// rather than newly started, so a caller can tell a retry that was
	// absorbed from one that started work.
	Existing bool   `json:"existing_run,omitempty"`
	Note     string `json:"note"`
}

// Submit records a run and starts it, or returns the run an identical earlier
// request already started.
func (e *Engine) Submit(
	call *Call, tool string, args map[string]any, exp Experiment,
) (any, *Fault) {
	idemKey, _ := args["idempotency_key"].(string)

	run, created, fault := e.store.Submit(
		e.base, call.Caller, e.project.ID, tool, idemKey, args)
	if fault != nil {
		return nil, fault
	}
	ack := submission{
		Kind: "rehearsal_submitted", RunID: run.ID, Tool: tool,
		Status: run.Status, Phase: run.Phase, PollAfterMs: pollAfterMs,
		Existing: !created,
	}
	if created {
		ack.Note = "Accepted. Poll get_rehearsal_run with this run_id for the verdict."
		e.start(run.ID, tool, exp)
	} else {
		ack.Status, ack.Phase = run.Status, run.Phase
		ack.Note = "This idempotency key was already used for exactly this request, " +
			"so the run it started is returned rather than a second one."
		if run.Status.terminal() {
			ack.PollAfterMs = 0
			ack.Note += " It has already finished; read it with get_rehearsal_run."
		}
	}
	return ack, nil
}

// start runs one experiment in the background.
func (e *Engine) start(runID, tool string, exp Experiment) {
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				e.logf("the experiment for %s panicked: %v", runID, r)
				// A panicking experiment is settled as failed rather than
				// left running forever. Its verdict is INCONCLUSIVE, which is
				// the honest report: the experiment did not finish.
				_ = e.store.Fail(e.base, runID, &Fault{
					Code: FaultInternal,
					Detail: "This experiment stopped on an internal defect and did not " +
						"finish, so it says nothing about the change.",
					Retryable: true,
				})
			}
		}()

		if err := e.store.Start(e.base, runID, "starting"); err != nil {
			e.logf("marking %s started: %v", runID, err)
		}
		native, body, fault := exp(e.base, runID)

		switch {
		case e.store.Cancelled(e.base, runID):
			// Checked before the result is recorded, but MarkCancelled
			// refuses to overwrite a run that already finished, so an
			// experiment that completed on the line still keeps its real
			// verdict rather than losing it to a late cancel.
			if fault == nil && body != nil {
				if err := e.store.Finish(e.base, runID, native, e.assemble(tool, body)); err == nil {
					return
				}
			}
			if err := e.store.MarkCancelled(e.base, runID); err != nil {
				e.logf("marking %s cancelled: %v", runID, err)
			}
		case fault != nil:
			e.logf("%s failed: %s", runID, fault.Error())
			if fault.wrapped != nil {
				e.logf("%s cause: %v", runID, fault.wrapped)
			}
			if err := e.store.Fail(e.base, runID, fault); err != nil {
				e.logf("recording the failure of %s: %v", runID, err)
			}
		default:
			if err := e.store.Finish(e.base, runID, native, e.assemble(tool, body)); err != nil {
				e.logf("recording the result of %s: %v", runID, err)
			}
		}
	}()
}

// assemble builds the stored result document.
//
// The full evidence list is stored rather than the first page, because paging
// happens at read time against whatever cursor the caller presents. Storing
// one page would make every later page unreachable.
func (e *Engine) assemble(tool string, body *ResultBody) storedResult {
	return storedResult{
		Tool: tool, Summary: body.Summary, Findings: body.Findings,
		Metrics: boundMetrics(body.Metrics), Evidence: body.Evidence,
		Detail: encodeDetail(body.Detail),
	}
}

// encodeDetail marshals a tool's own evidence document.
//
// A failure here is a defect in this package's own types rather than anything
// a caller did, so it is dropped rather than allowed to lose the whole result:
// a verdict with no detail is worth far more than no verdict at all.
func encodeDetail(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	body, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return body
}

// storedResult is the result as it sits in the database.
//
// It holds the whole evidence list, unpaginated. The Result a caller reads is
// built from this at read time, so that a cursor can address any page.
type storedResult struct {
	Tool     string          `json:"tool"`
	Summary  string          `json:"summary"`
	Findings FindingPage     `json:"findings"`
	Metrics  []Metric        `json:"metrics"`
	Evidence []Evidence      `json:"evidence"`
	Detail   json.RawMessage `json:"detail,omitempty"`
}

// Phase records progress on a run, for a caller that is polling.
func (e *Engine) Phase(ctx context.Context, runID, phase string) {
	if err := e.store.Phase(ctx, runID, phase); err != nil {
		e.logf("recording the phase of %s: %v", runID, err)
	}
}

// Cancelled reports whether the caller asked for this run to stop.
func (e *Engine) Cancelled(ctx context.Context, runID string) bool {
	return e.store.Cancelled(ctx, runID)
}

func (e *Engine) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(e.log, "af mcp: "+format+"\n", args...)
}

// view renders a stored run as the caller facing result.
//
// This is where the envelope is put back on, and it is the only place a
// verdict reaches a caller. An experiment cannot reach these fields, and the
// verdict written here came from the store, which derived it from the
// deterministic evaluator.
func view(run Run, cursor string) (*Result, *Fault) {
	out := &Result{
		Kind: "rehearsal_result", RunID: run.ID, Tool: run.Tool,
		Status: run.Status, Phase: run.Phase,
		Verdict: run.Verdict, NativeVerdict: run.NativeVerdict,
	}
	if out.Verdict == "" {
		// A run still in flight has no verdict yet. Reporting the empty
		// string would let a caller that compares against PASS by inequality
		// draw the wrong conclusion, so it is stated.
		out.Verdict = VerdictInconclusive
	}

	if run.Status == StatusFailed && run.ErrorCode != "" {
		out.Error = &faultDocument{
			Kind: "error", Code: run.ErrorCode, Detail: run.ErrorDetail,
		}
	}

	if len(run.Result) == 0 {
		out.Summary = summaryForIncomplete(run)
		out.Findings = FindingPage{Items: []Finding{}}
		out.Evidence = EvidencePage{Items: []Evidence{}}
		return out, nil
	}

	var stored storedResult
	if err := json.Unmarshal(run.Result, &stored); err != nil {
		return nil, internalFault(fmt.Errorf("decoding the stored result of %s: %w", run.ID, err))
	}
	out.Summary = stored.Summary
	out.Findings = stored.Findings
	out.Metrics = stored.Metrics
	out.Detail = stored.Detail

	page, fault := boundEvidence(run.ID, stored.Evidence, cursor)
	if fault != nil {
		return nil, fault
	}
	out.Evidence = page
	return out, nil
}

// summaryForIncomplete says in words what a run that produced no result is.
func summaryForIncomplete(run Run) string {
	switch run.Status {
	case StatusQueued:
		return "This rehearsal has been accepted and has not started yet. " +
			"It has no verdict, and INCONCLUSIVE here means not yet known."
	case StatusRunning:
		return fmt.Sprintf(
			"This rehearsal is running, currently %s. It has no verdict yet, "+
				"and INCONCLUSIVE here means not yet known.", orUnknownPhase(run.Phase))
	case StatusCancelled:
		return "This rehearsal was cancelled before it finished, so it says " +
			"nothing about the change."
	case StatusFailed:
		return "This rehearsal could not be completed, so it says nothing about " +
			"the change. The error field says why."
	default:
		return "This rehearsal produced no result."
	}
}

func orUnknownPhase(p string) string {
	if p == "" {
		return "in an unreported phase"
	}
	return p
}
