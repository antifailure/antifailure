package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"sync/atomic"

	"github.com/antifailure/antifailure/engine/internal/clock"
)

// Sink consumes events.
//
// A sink must not block for long. The bus gives each sink its own bounded
// queue and its own goroutine, so one slow sink cannot stall the others or the
// engine, but a sink that blocks forever will still fill its queue and start
// dropping.
type Sink interface {
	// Name identifies the sink in drop counters and error messages.
	Name() string
	// Deliver handles one event. An error is counted and reported through a
	// SinkDropped event; it never propagates back to the emitter, because the
	// engine's correctness does not depend on a log line being written.
	Deliver(ctx context.Context, e Event) error
	// Close flushes and releases the sink's resources.
	Close() error
}

// defaultQueueSize is how many events a sink may fall behind by before the bus
// starts dropping. It is sized so that a burst of build output or proxy
// decisions rides through a slow flush, and small enough that a dead sink
// cannot grow memory without bound.
const defaultQueueSize = 4096

// Bus fans events out to sinks.
//
// Emit never blocks. If a sink's queue is full the event is dropped for that
// sink and a counter is incremented; the counter surfaces in the dashboard and
// as a SinkDropped event, so a user sees "the log file fell behind by 812
// events" rather than an environment that mysteriously stalled.
type Bus struct {
	clock clock.Clock

	mu       sync.RWMutex
	sinks    []*sinkState
	closed   bool
	seqByEnv map[string]uint64

	// idFn produces event identifiers. It is injectable so that tests get
	// deterministic identifiers without a global random source.
	idFn func() string

	wg sync.WaitGroup
}

type sinkState struct {
	sink    Sink
	queue   chan Event
	dropped atomic.Uint64
	done    chan struct{}
}

// Option configures a Bus.
type Option func(*Bus)

// WithIDFunc replaces the event identifier generator. Tests use it to get
// stable identifiers.
func WithIDFunc(f func() string) Option {
	return func(b *Bus) { b.idFn = f }
}

// NewBus returns a bus with no sinks.
func NewBus(c clock.Clock, opts ...Option) *Bus {
	b := &Bus{
		clock:    c,
		seqByEnv: make(map[string]uint64),
		idFn:     randomID,
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

func randomID() string {
	var buf [12]byte
	// crypto/rand.Read is documented never to fail on any supported platform,
	// and an identifier is not a security boundary, so a failure here would be
	// a platform bug rather than something to propagate through every emit
	// call site in the engine.
	_, _ = rand.Read(buf[:])
	return "ev_" + hex.EncodeToString(buf[:])
}

// AddSink registers a sink and starts its delivery goroutine.
//
// Sinks added after events have been emitted do not receive the earlier ones;
// replay is the NDJSON reader's job, not the bus's.
func (b *Bus) AddSink(s Sink) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	st := &sinkState{
		sink:  s,
		queue: make(chan Event, defaultQueueSize),
		done:  make(chan struct{}),
	}
	b.sinks = append(b.sinks, st)
	b.wg.Add(1)
	go b.run(st)
}

func (b *Bus) run(st *sinkState) {
	defer b.wg.Done()
	defer close(st.done)
	// A sink that panics must not take the engine with it. The panic becomes a
	// dropped event count and the sink stops; the alternative, a crashed
	// process because a log formatter hit a nil map, is strictly worse.
	defer func() {
		if r := recover(); r != nil {
			st.dropped.Add(1)
		}
	}()
	ctx := context.Background()
	for e := range st.queue {
		if err := st.sink.Deliver(ctx, e); err != nil {
			st.dropped.Add(1)
		}
	}
}

// Emit publishes an event. It never blocks and never returns an error.
func (b *Bus) Emit(env string, t Type, level Level, msg string, fields ...Field) Event {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return Event{}
	}
	b.seqByEnv[env]++
	e := Event{
		ID:    b.idFn(),
		TS:    b.clock.Now(),
		Env:   env,
		Seq:   b.seqByEnv[env],
		Type:  t,
		Level: level,
		Msg:   msg,
		Data:  fieldsToMap(fields),
	}
	sinks := b.sinks
	b.mu.Unlock()

	for _, st := range sinks {
		select {
		case st.queue <- e:
		default:
			// The sink is behind. Dropping is deliberate: an environment must
			// not stall because a consumer is slow.
			st.dropped.Add(1)
		}
	}
	return e
}

// Info emits at info level.
func (b *Bus) Info(env string, t Type, msg string, fields ...Field) Event {
	return b.Emit(env, t, LevelInfo, msg, fields...)
}

// Warn emits at warn level.
func (b *Bus) Warn(env string, t Type, msg string, fields ...Field) Event {
	return b.Emit(env, t, LevelWarn, msg, fields...)
}

// Error emits at error level.
func (b *Bus) Error(env string, t Type, msg string, fields ...Field) Event {
	return b.Emit(env, t, LevelError, msg, fields...)
}

// Debug emits at debug level.
func (b *Bus) Debug(env string, t Type, msg string, fields ...Field) Event {
	return b.Emit(env, t, LevelDebug, msg, fields...)
}

// Drops reports how many events each sink has dropped, by sink name.
//
// The dashboard shows this and af status prints it, because a silent drop is
// indistinguishable from nothing having happened.
func (b *Bus) Drops() map[string]uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[string]uint64, len(b.sinks))
	for _, st := range b.sinks {
		if n := st.dropped.Load(); n > 0 {
			out[st.sink.Name()] = n
		}
	}
	return out
}

// Seq reports the last sequence number issued for an environment.
func (b *Bus) Seq(env string) uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.seqByEnv[env]
}

// Close stops accepting events, drains every sink queue, and closes each sink.
//
// It reports the first close error, after attempting all of them, so that one
// failing sink does not leave the others open.
func (b *Bus) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	sinks := b.sinks
	b.mu.Unlock()

	for _, st := range sinks {
		close(st.queue)
	}
	b.wg.Wait()

	var firstErr error
	for _, st := range sinks {
		if err := st.sink.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
