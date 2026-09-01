package cli

// What a hosted workload run tells the control plane about itself.
//
// THE GAP THIS CLOSES.
//
// The control plane creates a workload run, dispatches the customer's own
// workflow, accepts `workload.started`, `workload.finished` and
// `workload.cancelled`, and projects all three into run state, route metrics,
// threshold verdicts and evidence. The engine emitted none of them and claimed
// nothing, so every hosted run was dispatched, became visible, and ended as
// `abandoned` at its deadline. A live consumer with no producer: the shape this
// repository calls a dead socket, and the reason `af workload run` looked
// finished while Studio measured nothing.
//
// FOUR THINGS, AND THE ORDER MATTERS.
//
//	claim      which recorded request this job belongs to. A dispatch cannot
//	           carry the run identifier, so the engine asks.
//	started    the run is no longer waiting for somebody to pick it up.
//	heartbeat  the run is still going. Without it a long run is ABANDONED at
//	           its deadline rather than merely late, and the two are different
//	           sentences: abandoned means nobody reported, and failed means an
//	           engine did.
//	finished   what it measured, or why it stopped.
//
// EVERYTHING HERE FAILS SOFT, WITH ONE EXCEPTION.
//
// A control plane that is unreachable must not fail the run: the work is the
// thing and the reporting is a view of it. So a claim that cannot be made, a
// heartbeat that cannot be sent and a report that cannot be delivered are each
// reported to the terminal and survived. The report itself is spooled rather
// than dropped, because the spool outlives the process and the next `af`
// command on that machine drains it, which is what turns a control plane blip
// into a late run rather than an abandoned one.
//
// The exception is a cancel. A cancel that arrives and is ignored is the defect
// this whole workstream started from, so a `workload.cancel` command stops the
// work rather than being noted.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/controlplane"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/telemetry"
	"github.com/antifailure/antifailure/engine/internal/workload"
)

// hostedHeartbeat is how often a claimed run says it is still going.
//
// A minute, against a lease of fifteen and a deadline of two hours. The lease
// is what another engine may take the run on, so the interval has to leave room
// for several to be lost in a row on a runner with bad connectivity, and
// fifteen misses is that room. It is also how stale the deadline can get: a
// process killed with `kill -9` is abandoned two hours after its last
// heartbeat, so a smaller interval buys a tighter bound on nothing anybody
// waits for.
//
// The same tick asks whether a cancel is waiting, so this is also the longest a
// pressed cancel takes to reach a run. A minute is the number that has to be
// defensible for that reason rather than for the lease.
const hostedHeartbeat = time.Minute

// hostedCommandBatch bounds one poll for commands.
//
// Small on purpose. This engine is looking for the cancel of one run, and a
// claim takes a lease on everything it returns, so asking for more would take
// leases on teardowns this process has no intention of carrying out and leave
// them to expire.
const hostedCommandBatch = 5

// hostedReporter is one hosted run's connection to the control plane.
//
// Every method tolerates a nil receiver, because "there is no control plane"
// is the ordinary case rather than an error: most runs are on a laptop with no
// token at all. That keeps the call sites in workload.go free of a condition
// around each of the four steps, which is the shape that ends with three of
// them guarded and the fourth not.
type hostedReporter struct {
	client *controlplane.Client
	sink   *controlplane.Sink
	spool  *telemetry.Spool
	clock  clock.Clock
	warn   func(string)

	envID string
	// runID is empty until a run is claimed or given. Nothing is emitted
	// without one: the projection refuses an event that names no run and says
	// so, and sending one would be asking to be told off.
	runID string
	// kind is what the control plane says this run is, empty when the run id
	// came from the command line rather than from a claim.
	kind string
	// sequence orders this run's own events. See emit for why it starts at one
	// rather than continuing the environment's.
	sequence uint64

	mu sync.Mutex
	// stopped is why the work was stopped from here, so the result can say
	// which of the two it was rather than "the run was cancelled".
	stopped string
	// beating is closed to stop the heartbeat.
	beating chan struct{}
	wg      sync.WaitGroup
}

// newHostedReporter builds the reporter, or returns nil when this engine has no
// control plane to report to.
//
// Nil rather than an error for a missing token, and that is the same decision
// telemetry makes: a run on a laptop reports to nobody and is not broken. An
// error here is a control plane that is configured and unusable, which is worth
// saying out loud.
func newHostedReporter(e *Env, stateDir, envID string) (*hostedReporter, error) {
	token := controlplane.TokenFromEnvironment(func(k string) (string, bool) {
		v := e.Getenv(k)
		return v, v != ""
	})
	if token == "" {
		return nil, nil
	}
	client, err := controlplane.New(controlplane.Options{
		BaseURL:  controlPlaneFor(e, ""),
		Token:    token,
		Clock:    e.Clock,
		Redactor: e.Redactor,
	})
	if err != nil {
		if errors.Is(err, controlplane.ErrNotConfigured) {
			return nil, nil
		}
		return nil, err
	}

	h := &hostedReporter{
		client: client,
		clock:  e.Clock,
		envID:  envID,
		warn: func(msg string) {
			e.Out.Status(e.Out.S(StyleDim, SymbolSkip), "control plane", msg)
		},
	}

	// The same spool the rest of the engine writes to, so a report that could
	// not be delivered is drained by whichever `af` command runs next on this
	// machine rather than by this one alone. The directory is designed to be
	// shared: a batch is claimed by rename, so two processes draining at once
	// cannot both send it.
	if stateDir != "" {
		spool, serr := telemetry.NewSpool(telemetry.SpoolOptions{
			Dir: filepath.Join(stateDir, telemetry.SpoolDir), Redactor: e.Redactor,
		})
		if serr != nil {
			h.warn(fmt.Sprintf(
				"a report that cannot be delivered will be lost rather than kept, because the spool could not be opened: %v", serr))
		} else {
			h.spool = spool
		}
	}

	var overflow controlplane.Overflow
	if h.spool != nil {
		overflow = h.spool
	}
	h.sink = controlplane.NewSink(controlplane.SinkOptions{
		Client: client, Clock: e.Clock, Overflow: overflow,
		OnError: func(err error) { h.warn(err.Error()) },
	})
	return h, nil
}

// Claim decides which run this job is reporting on.
//
// An explicit --run-id wins and claims nothing. A person reproducing a hosted
// run on a laptop passes the identifier of the run they are reproducing, and
// claiming there would take the next queued run away from CI and then report
// somebody else's numbers against it.
//
// Otherwise it asks. Nothing waiting is not an error: somebody typing
// `af workload run` on a runner by hand is a real thing to do and it reports
// nothing, which is exactly what a run the control plane never asked for should
// do.
func (h *hostedReporter) Claim(ctx context.Context, explicit string, kind workload.Kind) {
	if h == nil {
		return
	}
	if strings.TrimSpace(explicit) != "" {
		h.runID = strings.TrimSpace(explicit)
		return
	}

	run, err := h.client.ClaimWorkload(ctx, h.envID)
	if err != nil {
		h.warn("no run was claimed, so this run is not reported to the control plane: " + err.Error())
		return
	}
	if run == nil {
		return
	}
	h.runID = run.RunID
	h.kind = run.Kind
	if run.Kind != "" && run.Kind != string(kind) {
		// Said here and reported at the end rather than refused. The claim has
		// already been taken, so walking away would leave the run held with
		// nobody reporting and end it as abandoned, which reads as a plumbing
		// fault rather than as the mismatch it is.
		h.warn(fmt.Sprintf(
			"the control plane's run %s is a %s workload and this job was dispatched as %s; "+
				"the result will say so rather than being recorded as a %s measurement",
			run.RunID, run.Kind, kind, run.Kind))
	}
}

// Reporting says whether anything will be sent, so a caller can print the run
// identifier it is working under.
func (h *hostedReporter) Reporting() bool { return h != nil && h.runID != "" }

// RunID is the control plane's identifier for this run, or empty.
func (h *hostedReporter) RunID() string {
	if h == nil {
		return ""
	}
	return h.runID
}

// Start emits workload.started and begins the heartbeat.
//
// It returns the context the work should run on. That context is cancelled when
// a cancel command arrives or when this engine loses its lease, which is the
// only way either of those two facts can reach work that is already going.
func (h *hostedReporter) Start(ctx context.Context, kind workload.Kind) context.Context {
	if !h.Reporting() {
		return ctx
	}
	h.emit(ctx, events.WorkloadStarted, map[string]any{
		"workload_run_id": h.runID,
		"kind":            string(kind),
		"env_id":          h.envID,
	})

	work, cancel := context.WithCancel(ctx)
	h.beating = make(chan struct{})
	h.wg.Add(1)
	go h.beat(ctx, work, cancel)
	return work
}

// beat keeps the lease alive and watches for a cancel.
//
// It runs on the CALLER's context rather than on the work's, so that cancelling
// the work does not cancel the goroutine that is trying to tell somebody about
// it. The work's context is only ever cancelled from here.
func (h *hostedReporter) beat(ctx context.Context, work context.Context, cancel context.CancelFunc) {
	defer h.wg.Done()
	defer cancel()

	ticker := h.clock.NewTicker(hostedHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-h.beating:
			return
		case <-ctx.Done():
			return
		case <-work.Done():
			// The work ended on its own. Nothing left to keep alive, and the
			// terminal event is what says so.
			return
		case <-ticker.C():
			if reason := h.tick(ctx); reason != "" {
				h.mu.Lock()
				h.stopped = reason
				h.mu.Unlock()
				cancel()
				return
			}
		}
	}
}

// tick is one heartbeat and one look for a cancel.
//
// It returns the sentence to stop the work with, or empty to keep going. Two
// things stop it and they are different sentences, which is why this returns
// prose rather than a boolean: a lease that was taken and a cancel somebody
// pressed lead a reader to different places.
func (h *hostedReporter) tick(ctx context.Context) string {
	if err := h.client.Heartbeat(ctx, h.runID); err != nil {
		var lost *controlplane.LeaseLost
		if errors.As(err, &lost) {
			return "the control plane says this engine no longer holds the run: " + lost.Error()
		}
		// Everything else is the network. A heartbeat that could not be sent
		// says nothing about whether the work is going well, and stopping a
		// healthy run because a dashboard is unreachable is the failure this
		// package's soft-failure rule exists to prevent.
		h.warn("a heartbeat could not be sent: " + err.Error())
		return ""
	}

	commands, err := h.client.ClaimCommands(ctx, h.envID, hostedCommandBatch)
	if err != nil {
		h.warn("the pending commands could not be read: " + err.Error())
		return ""
	}
	for _, c := range commands {
		if c.Kind == controlplane.CommandCancelWorkload && c.WorkloadRunID == h.runID {
			return "a cancel was requested in the control plane"
		}
	}
	return ""
}

// StoppedBy is why this reporter stopped the work, or empty when it did not.
func (h *hostedReporter) StoppedBy() string {
	if h == nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stopped
}

// Finish emits the terminal event and delivers everything.
//
// It is called for every outcome including a cancellation, a timeout and a
// refused knob, because a run this engine says nothing about is a run the
// control plane can only call abandoned two hours later.
func (h *hostedReporter) Finish(ctx context.Context, res *workload.Result) {
	if !h.Reporting() {
		h.Close()
		return
	}
	h.stopBeating()

	payload := hostedPayload(res, h.runID, h.kind)
	// Both constants written out at their own call site rather than chosen into
	// a variable, because the two source scans that ask what this engine emits
	// read call arguments. A type reached only through a variable reads as
	// emitted by nothing, which is the finding those scans exist to report.
	if res.State == workload.StateCancelled {
		h.emit(ctx, events.WorkloadCancelled, payload)
	} else {
		h.emit(ctx, events.WorkloadFinished, payload)
	}
	h.Close()
}

// Failed reports a run that could not produce a result document at all.
//
// It exists because the alternative is the defect this file closes, one level
// up: an error return between Start and Finish leaves the run claimed, and a
// claimed run nobody reports on ends as `abandoned`, which says the control
// plane never heard when in fact it did and the engine had something to say.
//
// No measurements travel, because there are none. What travels is the sentence.
func (h *hostedReporter) Failed(ctx context.Context, detail string) {
	if !h.Reporting() {
		h.Close()
		return
	}
	h.stopBeating()
	h.emit(ctx, events.WorkloadFinished, map[string]any{
		"workload_run_id": h.runID,
		"outcome":         "failed",
		"state":           string(workload.StateFailed),
		"detail":          detail,
	})
	h.Close()
}

// emit hands one event to the control plane sink and delivers it now.
//
// NOT THROUGH A BUS, AND THE THREE REASONS ARE EACH A PROPERTY THIS NEEDS.
//
// The identifier is deterministic. A bus assigns a random one per event, which
// is right for a stream where every event is new; here the same start and the
// same report may be sent twice, once live and once out of the spool after a
// crash, and an identifier derived from the run and the phase is what makes the
// second one a duplicate the control plane drops rather than a second row.
//
// The sequence is this RUN's rather than the environment's, and starts at one.
// The control plane compares a workload event's sequence with
// `workload_runs.last_sequence`, which is per run and starts at zero, and never
// with the environment's, because a workload event is keyed on the run
// identifier and touches no environment row.
//
// And delivery here is synchronous. A bus hands the event to a queue another
// goroutine reads, so a flush can run before the event has arrived and send
// nothing at all; the only way to be sure with a bus is to close it, and
// closing a bus closes its sinks, which would leave the second event with
// nowhere to go.
//
// Flushed rather than left to the sink's own timer, at both ends. A start held
// for five seconds is five seconds of a console showing a run as still waiting
// to be picked up, and a terminal event held at all is one the process may exit
// before sending.
func (h *hostedReporter) emit(ctx context.Context, t events.Type, payload map[string]any) {
	h.sequence++
	e := events.Event{
		// Derived rather than random. See above.
		ID:    "wl_" + h.runID + "_" + string(t),
		TS:    h.clock.Now(),
		Env:   h.envID,
		Seq:   h.sequence,
		Type:  t,
		Level: events.LevelInfo,
		Data:  payload,
	}
	// Deliver buffers and never fails, so the error is checked for the shape of
	// it rather than because one is expected.
	if err := h.sink.Deliver(ctx, e); err != nil {
		h.warn(err.Error())
		return
	}
	if err := h.sink.Flush(ctx); err != nil {
		h.warn("the control plane could not be reached, so this was spooled for a later run to send: " +
			err.Error())
	}
}

// stopBeating ends the heartbeat and waits for it, so that no heartbeat can be
// sent after the terminal event.
func (h *hostedReporter) stopBeating() {
	if h.beating == nil {
		return
	}
	close(h.beating)
	h.wg.Wait()
	h.beating = nil
}

// Close puts the sink away, spooling whatever it still holds.
func (h *hostedReporter) Close() {
	if h == nil {
		return
	}
	h.stopBeating()
	if h.sink != nil {
		if err := h.sink.Close(); err != nil {
			h.warn(err.Error())
		}
	}
}

// hostedPayload is the result document, plus the two things only the wire needs.
//
// The payload IS the document `--result` writes, marshalled once and read back
// as a map rather than assembled a second time from the same fields. That is
// the property worth having: the artifact a job uploads and the numbers a
// console draws cannot disagree, because they are the same bytes.
func hostedPayload(res *workload.Result, runID, claimedKind string) map[string]any {
	body, err := json.Marshal(res)
	if err != nil {
		// Unreachable in practice and not silently degraded if it happens. A
		// terminal event with no measurements still ends the run, which is
		// better than a run that abandons because one field would not marshal.
		return map[string]any{
			"workload_run_id": runID,
			"outcome":         "failed",
			"detail":          "the result document could not be serialised: " + err.Error(),
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return map[string]any{
			"workload_run_id": runID,
			"outcome":         "failed",
			"detail":          "the result document could not be read back: " + err.Error(),
		}
	}

	payload["workload_run_id"] = runID
	payload["outcome"] = hostedOutcome(res.State)

	// `native` is dropped on the wire. The control plane declined to store it,
	// and it is the engine's own type for this kind: its shape moves with an
	// engine release, nothing can query it, and it is the largest field in the
	// document. The file `--result` writes keeps it, which is where a person
	// debugging one run wants it.
	delete(payload, "native")

	if claimedKind != "" && claimedKind != string(res.Kind) {
		// The report is stripped of everything the control plane would decode
		// as a measurement, because it would decode it AS THE CLAIMED KIND and
		// write a row that satisfies every constraint and means something that
		// did not happen. What is left is a failed run saying why.
		return map[string]any{
			"workload_run_id": runID,
			"outcome":         "failed",
			"state":           string(res.State),
			"detail": fmt.Sprintf(
				"this job ran a %s workload and the control plane's run is a %s, so nothing it "+
					"measured belongs on that run. The dispatch and the definition disagree.",
				res.Kind, claimedKind),
		}
	}
	return payload
}

// hostedOutcome is the one word the control plane reads to decide whether the
// work happened.
//
// A timed out run reports `failed`, and the loss is deliberate rather than
// overlooked. The control plane's own state enum has `timed_out` and its
// projection does not yet read one from an event: anything that is not the
// literal `failed` is recorded as `succeeded`. So sending `timed_out` today
// would record a run that never finished as one that did, which is the
// green-over-nothing this product has already shipped once. The document keeps
// the exact state in `state`, so the distinction is on the row's own event and
// a projection that learns the word later loses nothing.
func hostedOutcome(state workload.State) string {
	switch state {
	case workload.StateFailed, workload.StateTimedOut:
		return "failed"
	default:
		return "succeeded"
	}
}

// hostedStateDir is where this repository keeps its spool.
func hostedStateDir(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, env.StateDir)
}

// noteHostedStop records why a run was stopped, when it was stopped from here.
//
// `workload.Execute` cannot know: from inside, a cancel pressed in a console
// and a lease taken by another engine are both a cancelled context, and its
// detail says "the run was cancelled before finishing" for either. That
// sentence sends somebody looking for the person who pressed stop, and half the
// time there is not one.
func noteHostedStop(res *workload.Result, reason string) {
	if res == nil || reason == "" {
		return
	}
	if strings.TrimSpace(res.Detail) == "" {
		res.Detail = reason
		return
	}
	res.Detail = res.Detail + ". " + reason
}
