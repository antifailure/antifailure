package controlplane

import (
	"context"
	"errors"
	"fmt"
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
type Sink struct {
	client *Client
	clock  clock.Clock

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

// SinkOptions configures the sink.
type SinkOptions struct {
	Client *Client
	Clock  clock.Clock
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
		batchSize: batch, flushEvery: flush,
		done: make(chan struct{}), onError: onError,
	}
	s.wg.Add(1)
	go s.loop()
	return s
}

// Name identifies the sink in drop counters.
func (s *Sink) Name() string { return "control-plane" }

// Deliver buffers one event. It never blocks and never fails.
func (s *Sink) Deliver(_ context.Context, e events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.pending) >= s.capacity {
		// The oldest goes, not the newest. When something has gone wrong the
		// events that explain it are the recent ones, and a buffer that keeps
		// the first ten thousand and discards everything after is a buffer full
		// of the least useful events it could possibly hold.
		copy(s.pending, s.pending[1:])
		s.pending = s.pending[:len(s.pending)-1]
		s.dropped++
	}
	s.pending = append(s.pending, toWire(e))
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
			s.pending = append(batch, s.pending...)
			// Putting them back can exceed the capacity, so the same rule
			// applies: drop the oldest and count it.
			if over := len(s.pending) - s.capacity; over > 0 {
				s.pending = s.pending[over:]
				s.dropped += uint64(over)
			}
			s.mu.Unlock()
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

var typeMap = map[string]string{
	"env.up.started":   "environment.creating",
	"env.up.ready":     "environment.ready",
	"env.up.failed":    "environment.failed",
	"env.down.done":    "environment.torn_down",
	"test.started":     "run.started",
	"test.finished":    "run.finished",
	"test.verdict":     "verdict.recorded",
	"golden.published": "golden.published",
	"net.decision":     "network.decision",
}

// KnownTypes lists the engine event types that the control plane understands,
// for the documentation and for a test that keeps this map honest.
func KnownTypes() []string {
	out := make([]string, 0, len(typeMap))
	for k := range typeMap {
		out = append(out, k)
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
