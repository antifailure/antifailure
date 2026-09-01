package controlplane

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/events"
)

// Sink forwards engine events to the control plane.
//
// It is the one place in the engine that talks to a network service on the
// happy path, so its behaviour when that service is unavailable is the whole
// design.
//
// It buffers, it batches, and it drops. Dropping is the important one: an
// environment must not stall because a dashboard is down, so when the buffer is
// full the oldest events are discarded and the count is reported. The
// alternative, blocking the emitter, would mean a control plane outage stops
// every build on every developer's machine, which trades a cosmetic failure for
// a total one.
//
// What is never dropped is the fact that events were dropped. The counter is
// reported through the bus and printed at close, so a gap in the dashboard is
// explainable rather than mysterious.
//
// Dropping is the last resort rather than the first, because an in-memory
// buffer alone cannot keep the promise AF-CPL-003 makes. That message says
// events are buffered and sent when the control plane returns, and `af up`,
// `af test` and `af down` are three separate processes: a control plane that is
// down for the eleven seconds of an `af up` and back before `af test` would
// lose the first command's events entirely, because the buffer holding them
// exited with the process. So a sink given an Overflow spills to it instead of
// discarding, and drains it before its own buffer on every flush. Without one
// the behaviour is exactly what it was: bounded, in memory, and honest about
// what it lost.
type Sink struct {
	client   *Client
	clock    clock.Clock
	overflow Overflow

	// batchSize is how many events go in one request, bounded by what the
	// control plane accepts.
	batchSize int
	// flushEvery is the longest an event waits before being sent, so that a
	// quiet environment still reports promptly.
	flushEvery time.Duration

	mu      sync.Mutex
	pending []Event
	dropped uint64
	// throttledUntil is set when the control plane asked for a pause. Events
	// keep buffering while it holds; they are not sent and not discarded until
	// the buffer is full.
	throttledUntil time.Time

	capacity int

	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup

	// onError is called when a flush fails, so the engine can surface it
	// without this package importing the bus it is feeding.
	onError func(error)
}

// Overflow is durable storage for events the sink could not send.
//
// Declared here as an interface rather than imported as a concrete type so that
// this package keeps its one dependency direction: it talks to a control plane
// and it knows nothing about the filesystem. The implementation lives in
// internal/telemetry, which is also where the redactor that scrubs each line
// before it is written comes from.
type Overflow interface {
	// Put stores a batch durably. It is called only when the in-memory buffer
	// is full or the sink is closing, so it is off the happy path entirely.
	Put(ctx context.Context, batch []Event) error
	// Take claims the oldest stored batch. The returned function must be
	// called: nil removes the batch, an error puts it back for the next
	// attempt. A nil batch with a nil error means there is nothing stored.
	Take(ctx context.Context) ([]Event, func(error) error, error)
}

// SinkOptions configures the sink.
type SinkOptions struct {
	Client *Client
	Clock  clock.Clock
	// Overflow is where events go when the buffer is full or the process is
	// ending. Nil keeps the old behaviour: the oldest are dropped and counted.
	Overflow Overflow
	// Capacity is how many events may wait. Beyond it the oldest are dropped.
	Capacity int
	// BatchSize is how many go in one request.
	BatchSize int
	// FlushEvery is how long an event may wait before being sent.
	FlushEvery time.Duration
	// OnError receives flush failures.
	OnError func(error)
}

// NewSink builds a sink and starts its flusher.
func NewSink(opts SinkOptions) *Sink {
	c := opts.Clock
	if c == nil {
		c = clock.New()
	}
	capacity := opts.Capacity
	if capacity <= 0 {
		capacity = 10_000
	}
	batch := opts.BatchSize
	if batch <= 0 || batch > MaxBatch {
		batch = MaxBatch
	}
	flush := opts.FlushEvery
	if flush <= 0 {
		flush = 5 * time.Second
	}
	onError := opts.OnError
	if onError == nil {
		onError = func(error) {}
	}

	s := &Sink{
		client: opts.Client, clock: c, capacity: capacity,
		overflow:  opts.Overflow,
		batchSize: batch, flushEvery: flush,
		done: make(chan struct{}), onError: onError,
	}
	s.wg.Add(1)
	go s.loop()
	return s
}

// Name identifies the sink in drop counters.
func (s *Sink) Name() string { return "control-plane" }

// Deliver buffers one event. It never fails.
//
// It never blocks either, with one exception worth stating plainly: when the
// buffer is full and an Overflow is configured, the oldest batch is written to
// it here, and that is a disk write. Reaching it means the control plane has
// been unreachable long enough to accumulate ten thousand events, and the
// alternative at that point is losing them. The write is one file, not one
// event, so the cost is paid once per batch rather than once per event.
func (s *Sink) Deliver(_ context.Context, e events.Event) error {
	var spill []Event

	s.mu.Lock()
	if len(s.pending) >= s.capacity {
		// The oldest goes, not the newest. When something has gone wrong the
		// events that explain it are the recent ones, and a buffer that keeps
		// the first ten thousand and discards everything after is a buffer full
		// of the least useful events it could possibly hold.
		if s.overflow != nil {
			n := min(len(s.pending), s.batchSize)
			spill = make([]Event, n)
			copy(spill, s.pending[:n])
			s.pending = s.pending[n:]
		} else {
			copy(s.pending, s.pending[1:])
			s.pending = s.pending[:len(s.pending)-1]
			s.dropped++
		}
	}
	s.pending = append(s.pending, toWire(e))
	s.mu.Unlock()

	s.spill(context.Background(), spill)
	return nil
}

// spill hands a batch to the overflow, and counts it as dropped if even that
// fails. Called with no lock held, because it does file I/O.
func (s *Sink) spill(ctx context.Context, batch []Event) {
	if len(batch) == 0 || s.overflow == nil {
		return
	}
	if err := s.overflow.Put(ctx, batch); err != nil {
		s.mu.Lock()
		s.dropped += uint64(len(batch))
		s.mu.Unlock()
		s.onError(fmt.Errorf(
			"control plane: %d events could not be written to the spool and were dropped: %w",
			len(batch), err))
	}
}

// drainOverflow sends what an earlier process, or an earlier full buffer, left
// behind.
//
// It runs before the in-memory buffer on every flush, because spooled events
// are older than anything still in memory and the control plane's projection
// refuses an event whose sequence is behind the row it addresses. Sending the
// new ones first would make every spooled one a no-op on arrival.
//
// Bounded per call so that a large spool does not hold a flush open
// indefinitely; the next tick continues where this one stopped.
func (s *Sink) drainOverflow(ctx context.Context) error {
	if s.overflow == nil {
		return nil
	}
	s.mu.Lock()
	throttled := !s.throttledUntil.IsZero() && s.clock.Now().Before(s.throttledUntil)
	s.mu.Unlock()
	if throttled {
		// The spool is durable, so waiting costs nothing. Draining through a
		// 429 would be the one place in this package that ignores a control
		// plane asking to be left alone.
		return nil
	}

	const maxBatchesPerFlush = 32
	for range maxBatchesPerFlush {
		batch, ack, err := s.overflow.Take(ctx)
		if err != nil {
			return err
		}
		if batch == nil {
			return nil
		}
		_, sendErr := s.client.Send(ctx, batch)
		if ackErr := ack(sendErr); ackErr != nil && sendErr == nil {
			// Sent but not acknowledged: the batch will be sent again by
			// whoever picks it up. The control plane deduplicates on the event
			// identifier, so a resend is a no-op there rather than a double
			// count, which is exactly why the identifier is the idempotency
			// key.
			s.onError(fmt.Errorf("control plane: spooled batch sent but not cleared: %w", ackErr))
		}
		if sendErr != nil {
			var throttled *Throttled
			if errors.As(sendErr, &throttled) {
				s.mu.Lock()
				s.throttledUntil = s.clock.Now().Add(throttled.RetryAfter)
				s.mu.Unlock()
			}
			return sendErr
		}
	}
	return nil
}

// Dropped reports how many events were discarded because the buffer was full.
func (s *Sink) Dropped() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// Pending reports how many events are waiting.
func (s *Sink) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// Close flushes what it can and stops.
func (s *Sink) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		s.wg.Wait()
		// One last attempt, bounded, so that shutting down does not hang on an
		// unreachable control plane.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = s.Flush(ctx)

		// Whatever is still held goes to the overflow rather than to nowhere.
		// This is the moment the promise is kept or broken: the process is
		// ending, and an event that is only in memory here is an event nobody
		// will ever see again.
		s.mu.Lock()
		remaining := s.pending
		s.pending = nil
		s.mu.Unlock()
		if s.overflow != nil {
			s.spill(ctx, remaining)
			err = nil
		} else if len(remaining) > 0 {
			s.mu.Lock()
			s.dropped += uint64(len(remaining))
			s.mu.Unlock()
		}

		if dropped := s.Dropped(); dropped > 0 {
			// Reported rather than swallowed. A gap in the dashboard that
			// nobody can account for is worse than one that is explained.
			err = fmt.Errorf(
				"control plane: %d events were dropped because the buffer filled while it was unreachable",
				dropped)
		}
	})
	return err
}

func (s *Sink) loop() {
	defer s.wg.Done()
	ticker := s.clock.NewTicker(s.flushEvery)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C():
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := s.Flush(ctx); err != nil {
				s.onError(err)
			}
			cancel()
		}
	}
}

// Flush sends what is buffered.
//
// On failure the events are put back rather than discarded, so a control plane
// that was briefly unreachable loses nothing. They are put back at the front,
// because they are older than anything that arrived while the request was in
// flight and the control plane orders by sequence rather than by arrival.
func (s *Sink) Flush(ctx context.Context) error {
	// Oldest first, and what an earlier process spooled is older than anything
	// this one is holding.
	if err := s.drainOverflow(ctx); err != nil {
		return err
	}
	for {
		s.mu.Lock()
		if len(s.pending) == 0 {
			s.mu.Unlock()
			return nil
		}
		if !s.throttledUntil.IsZero() && s.clock.Now().Before(s.throttledUntil) {
			s.mu.Unlock()
			return nil
		}
		n := min(len(s.pending), s.batchSize)
		batch := make([]Event, n)
		copy(batch, s.pending[:n])
		s.pending = s.pending[n:]
		s.mu.Unlock()

		_, err := s.client.Send(ctx, batch)
		if err != nil {
			s.mu.Lock()
			var throttled *Throttled
			if errors.As(err, &throttled) {
				// Obeyed rather than retried. Retrying straight after a 429 is
				// how a busy control plane becomes an unreachable one.
				s.throttledUntil = s.clock.Now().Add(throttled.RetryAfter)
			}
			hasOverflow := s.overflow != nil
			var spill []Event
			if hasOverflow {
				// A batch that failed to send goes to disk rather than back
				// into memory, and that choice is about `kill -9` rather than
				// about the control plane. Events held only in memory are lost
				// when the process dies, so putting a failed batch back would
				// leave a whole outage's worth of events exposed to a
				// terminated command. Spilling here bounds what a kill can
				// cost to a single flush interval. It is the failure path, so
				// the disk write is free in every sense that matters.
				spill = batch
			} else {
				s.pending = append(batch, s.pending...)
				// Putting them back can exceed the capacity, so the same rule
				// applies: drop the oldest and count it.
				if over := len(s.pending) - s.capacity; over > 0 {
					s.pending = s.pending[over:]
					s.dropped += uint64(over)
				}
			}
			s.mu.Unlock()
			s.spill(ctx, spill)
			return err
		}
	}
}

// toWire converts an engine event to the ingestion form.
//
// The type is mapped rather than passed through. The engine's vocabulary is
// larger than the control plane's and includes events that exist only for the
// local dashboard, and an unmapped type is still sent so that an older control
// plane can ingest a newer engine.
func toWire(e events.Event) Event {
	payload := map[string]any{}
	for k, v := range e.Data {
		payload[k] = v
	}
	if e.Msg != "" {
		payload["message"] = e.Msg
	}
	if e.Level != "" {
		payload["level"] = string(e.Level)
	}
	return Event{
		// The engine's event identifier is the idempotency key. It is already
		// unique per event, so a resend after a timeout carries the same one
		// and the control plane drops the copy.
		ID:         e.ID,
		Type:       mapType(string(e.Type)),
		EnvID:      e.Env,
		Sequence:   e.Seq,
		OccurredAt: e.TS,
		Payload:    payload,
	}
}

// mapType translates the engine's event names to the control plane's where they
// differ, and passes everything else through unchanged.
func mapType(t string) string {
	if mapped, ok := typeMap[t]; ok {
		return mapped
	}
	return t
}

// typeMap is keyed by the events package's own constants rather than by string
// literals, and that is the whole correction.
//
// It previously held nine literals: env.up.started, env.up.ready,
// env.up.failed, env.down.done, test.started, test.finished, test.verdict,
// golden.published and net.decision. Not one of them was a type the engine can
// emit. The real constants are env.creating, env.ready, env.failed,
// env.destroyed, agent.started, agent.finished, agent.verdict, golden.ready and
// egress.decision, so every event would have missed the map, passed through
// unchanged, and arrived at the control plane as a type outside its accepted
// set. Such an event is stored and advances nothing, so every environment would
// have sat in the dashboard at the state it was first reported in, and the
// failure would have read as a control plane bug rather than as a lookup table
// nobody had ever executed.
//
// Keying by the constant makes that class of drift a compile error. Renaming an
// event type in the events package now breaks the build here instead of
// silently emptying the dashboard. The other half, that each value is a type
// the control plane actually accepts, cannot be checked by the compiler across
// two languages, so TestEveryMappedTypeIsOneTheControlPlaneAccepts reads the
// server's own list and compares.
var typeMap = map[string]string{
	string(events.EnvCreating):  "environment.creating",
	string(events.EnvReady):     "environment.ready",
	string(events.EnvFailed):    "environment.failed",
	string(events.EnvSleeping):  "environment.sleeping",
	string(events.EnvDestroyed): "environment.torn_down",

	string(events.AgentStarted):  "run.started",
	string(events.AgentFinished): "run.finished",
	string(events.AgentVerdict):  "verdict.recorded",

	string(events.GoldenReady):    "golden.published",
	string(events.EgressDecision): "network.decision",

	// Identity, and in the map anyway rather than left to fall through
	// mapType. Two reasons, and neither is tidiness.
	//
	// These three names were chosen on both sides at once, so there is nothing
	// to translate today and there is every chance of a rename tomorrow.
	// Falling through would make such a rename silent: the event would arrive
	// as a type outside the control plane's accepted set, be stored, advance
	// nothing, and the run would end as `abandoned` while the engine's own
	// terminal said it passed. That is the exact failure the nine wrong
	// literals above produced.
	//
	// And the drift gates in vocabulary_test.go are built on MappedTypes. A
	// type that is not in this map is not checked against the control plane's
	// list at all, so an identity entry is what puts these three under the
	// gate rather than beside it.
	string(events.WorkloadStarted):   "workload.started",
	string(events.WorkloadFinished):  "workload.finished",
	string(events.WorkloadCancelled): "workload.cancelled",
}

// KnownTypes lists the engine event types the control plane understands.
//
// Exported for the drift tests in vocabulary_test.go and for nothing else. It
// said "for the documentation and for the tests" until a call site sweep found
// that nothing generates any documentation from it, which is the same defect,
// in miniature, as the one this map had: a comment describing a caller that
// does not exist. If something ever does generate a reference page from this,
// the sentence can come back.
//
// Two of the control plane's accepted types have no engine event mapped to
// them, and the honest reason for both is that the capability behind them is
// not built.
//
// environment.queued is reserved for a control plane that schedules work. There
// is no such component. Said plainly because the sentence here used to assert
// one, and a reader who believes it goes looking: engine/internal/scheduler
// exists, runs in the engine rather than in the control plane, which is the
// opposite of what was claimed, and emits no event at all. Nothing produces
// environment.queued, so the queued value in the environment_state enum is
// reachable only as a column default.
//
// artifact.stored is reserved for an artifact uploader that reports what it
// stored. Nothing does.
//
// Both are listed in TestTheControlPlaneTypesWithNoEngineEventAreTheExpectedOnes
// so that a third appearing is a decision rather than a silent gap. That test
// asks whether a type is MAPPED, which is a weaker question than whether
// anything emits it, and the difference is not academic: five of the
// types this map does translate are emitted by nothing, so they pass that test
// and are still columns that are always empty. env.sleeping is one, because
// idle sleep is not implemented at all; runtime.idle_sleep is defaulted by
// manifest normalization, checked by validation, printed by af explain, and
// read by nothing that acts on it, which is the identical shape runtime.ttl had
// before the reaper existed. The whole agent run lifecycle is three more, and
// egress.decision is the fifth, where the data is collected and rendered
// locally and simply never put on the bus.
//
// TestEveryMappedTypeHasSomethingInTheEngineThatEmitsIt is the gate for that
// second question, and it names all five. They are not all the same kind of
// gap, and a reader needs to know which kind, because "reserved" and
// "abandoned" call for opposite decisions:
//
// env.sleeping is reserved for something never built. There is nothing to
// abandon: no code sleeps an environment, and runtime.idle_sleep is inert.
//
// egress.decision is not reserved and not abandoned, it is disconnected. The
// decisions are made, recorded, returned by Orchestrator.Decisions and rendered
// by af net and af ci, and a consumer exists here too: internal/hud classifies
// egress.decision as a noisy type to suppress, with a comment about an egress
// denial being the line somebody will grep for. So the producer and the
// consumer were both built and the wire between them was not. That is the most
// finishable of the five and it is the one to do first if anybody does one.
//
// The three agent types, agent.started, agent.finished and agent.verdict, are
// the one set where honestly nobody can tell from the code which it is. Agent
// runs happen, and nothing anywhere consumes these types either, so there is no
// half-built wire to point at as evidence of intent. Whether they were reserved
// for a reporting path or drafted and dropped is not recoverable from what is
// committed, and it is better to say so here than to pick and be wrong. The
// next person to touch the agent runner should settle it.
//
// None of the five is emitted today, and the gate holds that line either way.
func KnownTypes() []string {
	out := make([]string, 0, len(typeMap))
	for k := range typeMap {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// MappedTypes returns the control plane type each engine type becomes.
func MappedTypes() map[string]string {
	out := make(map[string]string, len(typeMap))
	for k, v := range typeMap {
		out[k] = v
	}
	return out
}

// TokenFromEnvironment reads a token from the environment.
//
// Deliberately the only way this package obtains one. A token written into a
// configuration file in the repository is a token in the repository, and a
// token this package could write to disk is a token this package could leak
// into a support bundle.
func TokenFromEnvironment(lookup func(string) (string, bool)) string {
	for _, name := range []string{"AF_CONTROL_PLANE_TOKEN", "ANTIFAILURE_TOKEN"} {
		if v, ok := lookup(name); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
