package cli

// The orderings a hosted workload run has to survive.
//
// Every one of them is a question about WHEN something arrives rather than
// about what it computes, so each is written as a cell rather than as a happy
// path with variations. The table, and the state the control plane is left in:
//
//	claim then start then finish        the ordinary case. Two events.
//	no run waiting                      nothing is claimed and nothing is sent.
//	an explicit --run-id                nothing is claimed. A person reproducing
//	                                    a hosted run must not take CI's next one.
//	a cancel while the work runs        the work stops and workload.cancelled
//	                                    goes out.
//	a cancel naming another run         ignored, and this run keeps going.
//	a cancel racing the completion      the work already ended. The cancel is
//	                                    never asked for, and the control plane
//	                                    settles the command on the terminal
//	                                    event as superseded.
//	the lease expires while running     THE ORDERING NOBODY HAD TESTED. The
//	                                    heartbeat is answered 409, the work is
//	                                    stopped, and the run still reports.
//	an unreachable control plane        the heartbeat is survived, and a report
//	                                    that cannot be delivered is spooled
//	                                    rather than lost.
//	a report after the deadline         sent anyway. Whether it is a note or a
//	                                    resurrection is the control plane's
//	                                    decision and it has already made it.
//	a kind mismatch                     reported as a failure carrying no
//	                                    measurements, because the decoder on the
//	                                    other side reads them as the kind the
//	                                    database holds.
//
// Nothing here sleeps and nothing reads a real clock. The heartbeat runs on the
// injected clock and the fake server signals every request it answers, so a
// test advances time and then waits for the thing that time was supposed to
// cause, rather than waiting long enough that it probably happened.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/controlplane"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/telemetry"
	"github.com/antifailure/antifailure/engine/internal/workload"
)

const testRunID = "5e4a1c8e-1f0b-4a35-9a1b-0c6d2f8e7a91"

// fakePlane is the control plane's four endpoints, scriptable per test.
type fakePlane struct {
	mu sync.Mutex

	// claim is the body POST /v1/workloads/claim answers with.
	claim string
	// heartbeat is the status the next heartbeat is answered with. 200 unless
	// a test says otherwise.
	heartbeat int
	// commands is the body POST /v1/commands/claim answers with.
	commands string
	// eventsStatus lets a test make the ingestion endpoint unreachable.
	eventsStatus int

	// events is every event that reached the wire, in order.
	events []controlplane.Event
	// beats and polls count the two halves of a tick.
	beats, polls int

	// beat and poll are signalled after each is answered, so a test can wait
	// for the effect of advancing the clock instead of guessing at a duration.
	beat, poll chan struct{}

	// holdTerminal, when set, stops the terminal event mid-flight: the handler
	// signals holding and then waits for it to be closed. That is the only way
	// to look at the window in which the report is being delivered, which is
	// the window a heartbeat must not still be running in.
	holdTerminal chan struct{}
	holding      chan struct{}
}

func newFakePlane(t *testing.T) (*fakePlane, string) {
	t.Helper()
	f := &fakePlane{
		claim:     `{"run":null}`,
		heartbeat: http.StatusOK,
		commands:  `{"commands":[]}`,
		beat:      make(chan struct{}, 64),
		poll:      make(chan struct{}, 64),
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func (f *fakePlane) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("content-type", "application/json")

	switch {
	case r.URL.Path == "/v1/workloads/claim":
		_, _ = w.Write([]byte(f.claim))
	case strings.HasSuffix(r.URL.Path, "/heartbeat"):
		f.beats++
		w.WriteHeader(f.heartbeat)
		if f.heartbeat == http.StatusConflict {
			_, _ = w.Write([]byte(`{"error":"Run ` + testRunID +
				` is not held by this token. Stop and claim again."}`))
		} else {
			_, _ = w.Write([]byte(`{"held":true}`))
		}
		select {
		case f.beat <- struct{}{}:
		default:
		}
	case r.URL.Path == "/v1/commands/claim":
		f.polls++
		_, _ = w.Write([]byte(f.commands))
		select {
		case f.poll <- struct{}{}:
		default:
		}
	case r.URL.Path == "/v1/events":
		var body struct {
			Events []controlplane.Event `json:"events"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.events = append(f.events, body.Events...)
		if hold := f.holdTerminal; hold != nil && terminal(body.Events) {
			// The lock is released across the wait on purpose. Holding it would
			// block the heartbeat handler too, and a test that cannot receive
			// the request it is watching for passes for the wrong reason.
			f.holdTerminal = nil
			close(f.holding)
			f.mu.Unlock()
			<-hold
			f.mu.Lock()
		}
		if f.eventsStatus != 0 && f.eventsStatus != http.StatusAccepted {
			w.WriteHeader(f.eventsStatus)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":` +
			itoa(len(body.Events)) + `,"duplicates":0,"rejected":0,"outcomes":[]}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// terminal reports whether a batch carries the event that ends a run.
func terminal(batch []controlplane.Event) bool {
	for _, e := range batch {
		if e.Type == "workload.finished" || e.Type == "workload.cancelled" {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func (f *fakePlane) set(mutate func(*fakePlane)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mutate(f)
}

func (f *fakePlane) sent() []controlplane.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]controlplane.Event, len(f.events))
	copy(out, f.events)
	return out
}

// reporterFor builds a reporter against the fake, with a fake clock and a real
// spool in a temporary directory.
func reporterFor(t *testing.T, url string) (*hostedReporter, *clock.Fake, string) {
	t.Helper()
	fake := clock.NewFake(time.Date(2026, 9, 1, 6, 40, 0, 0, time.UTC))
	stateDir := t.TempDir()
	e := &Env{
		Out:      NewOutput(&bytes.Buffer{}, &bytes.Buffer{}),
		Clock:    fake,
		Redactor: redact.New(),
		Getenv: func(k string) string {
			switch k {
			case "AF_CONTROL_PLANE_TOKEN":
				return "aft_" + strings.Repeat("a", 40)
			case "AF_CONTROL_PLANE_URL":
				return url
			}
			return ""
		},
	}
	h, err := newHostedReporter(e, stateDir, "pr-42")
	require.NoError(t, err)
	require.NotNil(t, h, "a token was configured and no reporter was built")
	t.Cleanup(h.Close)
	return h, fake, stateDir
}

// runningRun is the claim body for a run waiting on pr-42.
func runningRun(kind string) string {
	return `{"run":{"runId":"` + testRunID + `","workload":"checkout-mix","kind":"` + kind +
		`","version":3,"body":{},"attempt":1,` +
		`"deadlineAt":"2026-09-01T08:40:00Z","leaseExpiresAt":"2026-09-01T06:55:00Z"}}`
}

func resultFor(state workload.State, verdict string) *workload.Result {
	requests := 1200
	return &workload.Result{
		Schema:     workload.ResultSchema,
		Kind:       workload.ObservedLoad,
		State:      state,
		Verdict:    verdict,
		Measured:   workload.Measured{Requests: &requests, RefusedRoutes: []string{}},
		Routes:     []workload.RouteMetric{{Route: "GET /checkout", Sent: 1200}},
		Thresholds: []workload.ThresholdVerdict{},
		Evidence:   []workload.Evidence{},
		Reproduce:  workload.Reproduce{Command: "af load run --duration 60s --scale 1"},
	}
}

// beatOnce advances the clock one heartbeat interval and waits for the request
// that advance was supposed to cause.
//
// BlockUntil first, and it is not a nicety. A fake clock releases the waiters
// whose deadline the move passes, so advancing before the heartbeat goroutine
// has registered its ticker registers that ticker AFTER the move and it never
// fires. Every timing test in this file hung on exactly that.
//
// Two waiters: the control plane sink's own flush ticker, built with the
// reporter, and the heartbeat's, built by Start.
func beatOnce(t *testing.T, f *fakePlane, fake *clock.Fake) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, fake.BlockUntil(ctx, 2),
		"the heartbeat never registered a ticker, so advancing the clock reaches nothing")
	fake.Advance(hostedHeartbeat)
	select {
	case <-f.beat:
	case <-time.After(5 * time.Second):
		t.Fatal("no heartbeat was sent after the clock advanced past the interval")
	}
}

// ---------------------------------------------------------------------------
// The ordinary case
// ---------------------------------------------------------------------------

func TestAClaimedRunReportsAStartAndAFinish(t *testing.T) {
	f, url := newFakePlane(t)
	f.set(func(p *fakePlane) { p.claim = runningRun("observed_load") })
	h, _, _ := reporterFor(t, url)

	h.Claim(context.Background(), "", workload.ObservedLoad)
	require.True(t, h.Reporting(), "a run was waiting and the reporter did not take it")
	require.Equal(t, testRunID, h.RunID())

	ctx := h.Start(context.Background(), workload.ObservedLoad)
	require.NoError(t, ctx.Err(), "the work context was cancelled before any work ran")

	h.Finish(context.Background(), resultFor(workload.StateSucceeded, workload.VerdictPass))

	sent := f.sent()
	require.Len(t, sent, 2, "a run should report exactly a start and an end")

	require.Equal(t, "workload.started", sent[0].Type)
	require.Equal(t, testRunID, sent[0].Payload["workload_run_id"])
	require.Equal(t, uint64(1), sent[0].Sequence)

	require.Equal(t, "workload.finished", sent[1].Type)
	require.Equal(t, testRunID, sent[1].Payload["workload_run_id"])
	require.Equal(t, "succeeded", sent[1].Payload["outcome"])
	// The sequence has to ADVANCE, because the control plane's start only
	// applies where last_sequence is behind it and its terminal statement takes
	// the greater of the two. Equal numbers would make the finish look like a
	// replay of the start.
	require.Greater(t, sent[1].Sequence, sent[0].Sequence)

	// The payload IS the result document, which is the property that stops the
	// artifact a job uploads and the numbers a console draws from disagreeing.
	require.Equal(t, "observed_load", sent[1].Payload["kind"])
	require.Equal(t, "pass", sent[1].Payload["verdict"])
	measured, ok := sent[1].Payload["result"].(map[string]any)
	require.True(t, ok, "the aggregate did not travel as an object: %#v", sent[1].Payload["result"])
	require.Equal(t, float64(1200), measured["requests"])
	routes, ok := sent[1].Payload["routes"].([]any)
	require.True(t, ok, "routes did not travel as a list: %#v", sent[1].Payload["routes"])
	require.Len(t, routes, 1)

	// Identifiers are derived rather than random, so the same report arriving
	// twice is a duplicate the control plane drops rather than a second row.
	require.NotEqual(t, sent[0].ID, sent[1].ID)
	require.Contains(t, sent[0].ID, testRunID)
}

// ---------------------------------------------------------------------------
// Nothing to report
// ---------------------------------------------------------------------------

func TestWithNoRunWaitingNothingIsReported(t *testing.T) {
	f, url := newFakePlane(t)
	h, _, _ := reporterFor(t, url)

	h.Claim(context.Background(), "", workload.ObservedLoad)
	require.False(t, h.Reporting())

	ctx := h.Start(context.Background(), workload.ObservedLoad)
	h.Finish(ctx, resultFor(workload.StateSucceeded, workload.VerdictPass))

	require.Empty(t, f.sent(),
		"a run the control plane never asked for reported events against no run at all")
}

// An explicit --run-id claims nothing.
//
// A person reproducing a hosted run passes the identifier of the run they are
// reproducing. Claiming there would take the next queued run away from CI and
// then report a laptop's numbers against it.
func TestAnExplicitRunIdentifierClaimsNothing(t *testing.T) {
	f, url := newFakePlane(t)
	f.set(func(p *fakePlane) { p.claim = runningRun("observed_load") })
	h, _, _ := reporterFor(t, url)

	h.Claim(context.Background(), "given-by-hand", workload.ObservedLoad)
	require.Equal(t, "given-by-hand", h.RunID())

	h.Finish(context.Background(), resultFor(workload.StateSucceeded, workload.VerdictPass))
	sent := f.sent()
	require.Len(t, sent, 1)
	require.Equal(t, "given-by-hand", sent[0].Payload["workload_run_id"])
}

// ---------------------------------------------------------------------------
// Cancellation
// ---------------------------------------------------------------------------

func TestACancelCommandStopsTheWorkAndReportsItCancelled(t *testing.T) {
	f, url := newFakePlane(t)
	f.set(func(p *fakePlane) { p.claim = runningRun("observed_load") })
	h, fake, _ := reporterFor(t, url)

	h.Claim(context.Background(), "", workload.ObservedLoad)
	ctx := h.Start(context.Background(), workload.ObservedLoad)

	f.set(func(p *fakePlane) {
		p.commands = `{"commands":[{"id":"c1","kind":"workload.cancel","envId":"pr-42",` +
			`"workloadRunId":"` + testRunID + `","payload":{},"attempts":1}]}`
	})
	beatOnce(t, f, fake)

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a cancel was waiting in the control plane and the work was not stopped")
	}
	require.Contains(t, h.StoppedBy(), "cancel",
		"the run was stopped and cannot say why, so its detail will read as a signal")

	res := resultFor(workload.StateCancelled, workload.VerdictBlocked)
	res.Detail = "the run was cancelled before finishing"
	noteHostedStop(res, h.StoppedBy())
	h.Finish(context.Background(), res)

	sent := f.sent()
	require.Len(t, sent, 2)
	require.Equal(t, "workload.cancelled", sent[1].Type)
	require.Contains(t, sent[1].Payload["detail"], "cancel was requested in the control plane")
}

// A cancel naming a different run is not this run's cancel.
//
// The claim takes a lease on everything it returns, so an engine polling for
// its own cancel sees other runs' commands, and acting on one would stop a
// healthy run because somebody cancelled a different one.
func TestACancelForAnotherRunIsIgnored(t *testing.T) {
	f, url := newFakePlane(t)
	f.set(func(p *fakePlane) { p.claim = runningRun("observed_load") })
	h, fake, _ := reporterFor(t, url)

	h.Claim(context.Background(), "", workload.ObservedLoad)
	ctx := h.Start(context.Background(), workload.ObservedLoad)

	f.set(func(p *fakePlane) {
		p.commands = `{"commands":[{"id":"c1","kind":"workload.cancel","envId":"pr-42",` +
			`"workloadRunId":"a-different-run","payload":{},"attempts":1}]}`
	})
	beatOnce(t, f, fake)
	select {
	case <-f.poll:
	case <-time.After(5 * time.Second):
		t.Fatal("the commands were never polled")
	}

	// A second tick, and it is the assertion rather than a nicety. Reading
	// ctx.Err() straight after the poll reads it before the goroutine has
	// decided anything, so the check passes whatever the decision was: a
	// version that cancelled on any run at all went green here. Waiting for a
	// heartbeat that could only be sent by a loop that did NOT stop is what
	// makes this a measurement.
	beatOnce(t, f, fake)

	require.NoError(t, ctx.Err(), "another run's cancel stopped this one")
	require.Empty(t, h.StoppedBy())

	h.Finish(context.Background(), resultFor(workload.StateSucceeded, workload.VerdictPass))
	sent := f.sent()
	require.Equal(t, "workload.finished", sent[len(sent)-1].Type)
}

// A cancel racing the completion, where the completion wins.
//
// Nothing is asked for after the terminal event, which is what makes the
// control plane's own settlement correct: it marks the outstanding cancel
// superseded on the terminal event rather than waiting for an engine to
// acknowledge a command about a run that no longer exists to be cancelled.
func TestNoHeartbeatIsSentAfterTheTerminalEvent(t *testing.T) {
	f, url := newFakePlane(t)
	hold := make(chan struct{})
	var released sync.Once
	release := func() { released.Do(func() { close(hold) }) }
	// Registered before anything can fail, because a test that stops while the
	// handler is still held leaves an open connection and httptest.Close waits
	// for it forever, which turns a readable failure into a timeout.
	t.Cleanup(release)
	f.set(func(p *fakePlane) {
		p.claim = runningRun("observed_load")
		p.holdTerminal = hold
		p.holding = make(chan struct{})
	})
	h, fake, _ := reporterFor(t, url)

	h.Claim(context.Background(), "", workload.ObservedLoad)
	h.Start(context.Background(), workload.ObservedLoad)
	beatOnce(t, f, fake)

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Finish(context.Background(), resultFor(workload.StateSucceeded, workload.VerdictPass))
	}()

	// The report is now in flight and the control plane has not answered. This
	// is the window the whole test is about: a heartbeat that fires here keeps
	// a finished run's lease alive, and races the retry that would take it.
	select {
	case <-f.holding:
	case <-time.After(5 * time.Second):
		t.Fatal("the terminal event never reached the control plane")
	}

	drain(f.beat)
	fake.Advance(4 * hostedHeartbeat)
	select {
	case <-f.beat:
		t.Fatal("a heartbeat fired while the run was reporting that it had ended")
	case <-time.After(200 * time.Millisecond):
	}

	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Finish did not return once the control plane answered")
	}

	sent := f.sent()
	require.Equal(t, "workload.finished", sent[len(sent)-1].Type)
}

// drain empties a signal channel so a later receive can only be a new one.
func drain(c chan struct{}) {
	for {
		select {
		case <-c:
		default:
			return
		}
	}
}

// ---------------------------------------------------------------------------
// The ordering nobody had tested
// ---------------------------------------------------------------------------

// The lease expires while the work is still running.
//
// The control plane answers the heartbeat 409, which means another engine may
// already have taken this run. Carrying on would have two engines measuring one
// environment and reporting over each other, so the work stops.
//
// The report still goes out. Whether it lands is the control plane's decision
// and it has already made it: a terminal statement that moves no row writes no
// results, so a report about a run somebody else finished is a note rather than
// a second answer.
func TestALostLeaseStopsTheWorkAndTheRunStillReports(t *testing.T) {
	f, url := newFakePlane(t)
	f.set(func(p *fakePlane) { p.claim = runningRun("observed_load") })
	h, fake, _ := reporterFor(t, url)

	h.Claim(context.Background(), "", workload.ObservedLoad)
	ctx := h.Start(context.Background(), workload.ObservedLoad)

	f.set(func(p *fakePlane) { p.heartbeat = http.StatusConflict })
	beatOnce(t, f, fake)

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the lease was taken and the work kept going, so two engines would be measuring one environment")
	}
	require.Contains(t, h.StoppedBy(), "no longer holds the run")

	res := resultFor(workload.StateCancelled, workload.VerdictBlocked)
	noteHostedStop(res, h.StoppedBy())
	h.Finish(context.Background(), res)

	sent := f.sent()
	require.Len(t, sent, 2, "a run whose lease was taken said nothing about how it ended")
	require.Equal(t, "workload.cancelled", sent[1].Type)
	require.Contains(t, sent[1].Payload["detail"], "no longer holds the run")
}

// A heartbeat the control plane never answers is not a lost lease.
//
// A run that stopped because a dashboard was briefly unreachable is the failure
// the soft-failure rule exists to prevent, and it is invisible: the work would
// simply end early and report itself cancelled.
func TestAnUnreachableControlPlaneDoesNotStopTheWork(t *testing.T) {
	f, url := newFakePlane(t)
	f.set(func(p *fakePlane) { p.claim = runningRun("observed_load") })
	h, fake, _ := reporterFor(t, url)

	h.Claim(context.Background(), "", workload.ObservedLoad)
	ctx := h.Start(context.Background(), workload.ObservedLoad)

	f.set(func(p *fakePlane) { p.heartbeat = http.StatusBadGateway })
	beatOnce(t, f, fake)

	require.NoError(t, ctx.Err(), "a failing heartbeat stopped a healthy run")
	require.Empty(t, h.StoppedBy())

	f.set(func(p *fakePlane) { p.heartbeat = http.StatusOK })
	beatOnce(t, f, fake)
	require.NoError(t, ctx.Err())

	h.Finish(context.Background(), resultFor(workload.StateSucceeded, workload.VerdictPass))
	require.Equal(t, "workload.finished", f.sent()[1].Type)
}

// A report that cannot be delivered is spooled, not lost.
//
// This is what turns a control plane blip into a LATE run rather than an
// abandoned one: the spool outlives the process, and whichever `af` command
// runs next on the machine drains it. Without it, an ingestion endpoint that is
// down for the thirty seconds a run ends in loses the only report there will
// ever be, and the run abandons two hours later saying nobody reported, which
// is true and useless.
func TestAReportThatCannotBeDeliveredIsSpooled(t *testing.T) {
	f, url := newFakePlane(t)
	f.set(func(p *fakePlane) {
		p.claim = runningRun("observed_load")
		p.eventsStatus = http.StatusInternalServerError
	})
	h, _, stateDir := reporterFor(t, url)

	h.Claim(context.Background(), "", workload.ObservedLoad)
	h.Start(context.Background(), workload.ObservedLoad)
	h.Finish(context.Background(), resultFor(workload.StateSucceeded, workload.VerdictPass))
	h.Close()

	entries, err := os.ReadDir(filepath.Join(stateDir, telemetry.SpoolDir))
	require.NoError(t, err)
	var spooled int
	for _, entry := range entries {
		if !entry.IsDir() {
			spooled++
		}
	}
	require.NotZero(t, spooled,
		"the control plane refused the report and nothing was kept, so the run will abandon")
}

// ---------------------------------------------------------------------------
// A report after the deadline, and a kind that disagrees
// ---------------------------------------------------------------------------

// A run the control plane has already abandoned still reports.
//
// The engine cannot know it was abandoned and must not try to: the control
// plane's terminal statement moves a row only from a live state, so a late
// report writes no results and changes no verdict. Deciding here to stay silent
// would remove the one case where the engine's word is worth more than the
// deadline's, which is a deadline that fired while the work was fine.
func TestALateReportIsStillSent(t *testing.T) {
	f, url := newFakePlane(t)
	f.set(func(p *fakePlane) { p.claim = runningRun("observed_load") })
	h, fake, _ := reporterFor(t, url)

	h.Claim(context.Background(), "", workload.ObservedLoad)
	h.Start(context.Background(), workload.ObservedLoad)

	// Past the deadline the claim declared. Nothing on this side reads it, and
	// that is the point of the test.
	fake.Advance(3 * time.Hour)
	h.Finish(context.Background(), resultFor(workload.StateSucceeded, workload.VerdictPass))

	sent := f.sent()
	require.Len(t, sent, 2)
	require.Equal(t, "workload.finished", sent[1].Type)
}

// A run whose kind disagrees with the claim reports a failure and no numbers.
//
// The control plane decodes a report AS THE KIND ITS OWN ROW HOLDS. A browser
// run's counts read as a load result would produce a row that satisfies every
// constraint and means something that did not happen, so nothing measured is
// sent at all and the run says why instead.
func TestAKindMismatchReportsAFailureAndNoMeasurements(t *testing.T) {
	f, url := newFakePlane(t)
	f.set(func(p *fakePlane) { p.claim = runningRun("browser_workflow") })
	h, _, _ := reporterFor(t, url)

	h.Claim(context.Background(), "", workload.ObservedLoad)
	require.True(t, h.Reporting(), "the claim was taken and then dropped, so the run will abandon")

	h.Start(context.Background(), workload.ObservedLoad)
	h.Finish(context.Background(), resultFor(workload.StateSucceeded, workload.VerdictPass))

	sent := f.sent()
	require.Len(t, sent, 2)
	report := sent[1].Payload
	require.Equal(t, "failed", report["outcome"])
	require.NotContains(t, report, "result",
		"a load aggregate was sent to a run the control plane holds as a browser workflow")
	require.NotContains(t, report, "routes")
	require.Contains(t, report["detail"], "disagree")
}

// ---------------------------------------------------------------------------
// The one word the control plane reads
// ---------------------------------------------------------------------------

// A timed out run must not read as succeeded.
//
// The projection records anything that is not the literal `failed` as
// `succeeded`, so a run that never finished would be recorded as one that did.
// That is the green-over-nothing this product has already shipped once, and it
// is the reason the outcome is not simply the state.
func TestATimedOutRunIsReportedAsFailedAndKeepsItsState(t *testing.T) {
	f, url := newFakePlane(t)
	f.set(func(p *fakePlane) { p.claim = runningRun("observed_load") })
	h, _, _ := reporterFor(t, url)

	h.Claim(context.Background(), "", workload.ObservedLoad)
	h.Start(context.Background(), workload.ObservedLoad)
	h.Finish(context.Background(), resultFor(workload.StateTimedOut, workload.VerdictBlocked))

	report := f.sent()[1].Payload
	require.Equal(t, "failed", report["outcome"],
		"a run that passed its deadline would be recorded as one that finished")
	require.Equal(t, "timed_out", report["state"],
		"the exact state is what a projection that learns the word later reads")
}

func TestTheOutcomeSaysWhetherTheWorkHappened(t *testing.T) {
	require.Equal(t, "succeeded", hostedOutcome(workload.StateSucceeded))
	require.Equal(t, "failed", hostedOutcome(workload.StateFailed))
	require.Equal(t, "failed", hostedOutcome(workload.StateTimedOut))
	// A cancellation travels as workload.cancelled and never carries an
	// outcome the finished projection would read, so this value is never sent.
	require.Equal(t, "succeeded", hostedOutcome(workload.StateCancelled))
}

// The engine's own result never carries `native` onto the wire.
//
// The control plane declined to store it and the reason is worth keeping: its
// shape moves with an engine release and nothing can query it. It is also the
// largest field in the document, so leaving it in would multiply every batch by
// the size of a whole load result.
func TestTheEngineSpecificResultIsNotSentToTheControlPlane(t *testing.T) {
	res := resultFor(workload.StateSucceeded, workload.VerdictPass)
	res.Native = json.RawMessage(`{"buckets":[1,2,3]}`)
	payload := hostedPayload(res, testRunID, "observed_load")
	require.NotContains(t, payload, "native")
	// And the document on disk keeps it, which is where somebody debugging one
	// run wants it.
	require.NotEmpty(t, res.Native)
}

func TestNoteHostedStopKeepsWhatTheRunAlreadySaid(t *testing.T) {
	res := resultFor(workload.StateCancelled, workload.VerdictBlocked)
	res.Detail = "the run was cancelled before finishing"
	noteHostedStop(res, "a cancel was requested in the control plane")
	require.Equal(t,
		"the run was cancelled before finishing. a cancel was requested in the control plane",
		res.Detail)

	empty := resultFor(workload.StateCancelled, workload.VerdictBlocked)
	noteHostedStop(empty, "a cancel was requested in the control plane")
	require.Equal(t, "a cancel was requested in the control plane", empty.Detail)

	unchanged := resultFor(workload.StateSucceeded, workload.VerdictPass)
	unchanged.Detail = "everything passed"
	noteHostedStop(unchanged, "")
	require.Equal(t, "everything passed", unchanged.Detail)
}

// Without a token there is no reporter at all, which is the ordinary case.
func TestWithNoTokenNothingIsBuiltAndNothingIsSent(t *testing.T) {
	e := &Env{
		Out:      NewOutput(&bytes.Buffer{}, &bytes.Buffer{}),
		Clock:    clock.NewFake(time.Date(2026, 9, 1, 6, 40, 0, 0, time.UTC)),
		Redactor: redact.New(),
		Getenv:   func(string) string { return "" },
	}
	h, err := newHostedReporter(e, t.TempDir(), "pr-42")
	require.NoError(t, err)
	require.Nil(t, h)

	// Every method has to tolerate that, because the call sites in workload.go
	// are unconditional. A nil check around three of the four is how the fourth
	// panics on a laptop.
	h.Claim(context.Background(), "", workload.ObservedLoad)
	require.False(t, h.Reporting())
	require.Empty(t, h.RunID())
	require.Empty(t, h.StoppedBy())
	ctx := h.Start(context.Background(), workload.ObservedLoad)
	require.NoError(t, ctx.Err())
	h.Finish(ctx, resultFor(workload.StateSucceeded, workload.VerdictPass))
	h.Close()
}

// A run that could not produce a document still says so.
//
// The path is narrow and the consequence is not: an error return between the
// start and the report leaves the run claimed, and a claimed run nobody speaks
// about is recorded as abandoned, which says the control plane never heard when
// it did.
func TestARunWithNoDocumentStillReportsAFailure(t *testing.T) {
	f, url := newFakePlane(t)
	f.set(func(p *fakePlane) { p.claim = runningRun("observed_load") })
	h, _, _ := reporterFor(t, url)

	h.Claim(context.Background(), "", workload.ObservedLoad)
	h.Start(context.Background(), workload.ObservedLoad)
	h.Failed(context.Background(), "the runner could not be started")

	sent := f.sent()
	require.Len(t, sent, 2)
	require.Equal(t, "workload.finished", sent[1].Type)
	require.Equal(t, "failed", sent[1].Payload["outcome"])
	require.Equal(t, "the runner could not be started", sent[1].Payload["detail"])
	require.NotContains(t, sent[1].Payload, "result",
		"a run that produced no document sent measurements it does not have")
}
