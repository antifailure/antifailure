package events_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/events"
)

// gateSink holds every delivery until it is opened, so a test decides exactly
// when the bus's goroutine gets to finish an event.
type gateSink struct {
	open chan struct{}
	mu   sync.Mutex
	got  []events.Event
}

func (s *gateSink) Name() string { return "gate" }

func (s *gateSink) Deliver(_ context.Context, e events.Event) error {
	<-s.open
	s.mu.Lock()
	s.got = append(s.got, e)
	s.mu.Unlock()
	return nil
}

func (s *gateSink) Close() error { return nil }

func (s *gateSink) delivered() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

// Emit only queues, so an event emitted just before a flush could still be on
// the bus when the sink flushed, and it missed the flush: one of three
// artifact.stored events did on CI on 2026-10-25. Settle is the barrier. While
// the sink is held it must not return, and once released it must not return
// before every event emitted before it was called has been delivered.
func TestBus_SettleWaitsForEveryEmittedEventToBeDelivered(t *testing.T) {
	bus, _ := newTestBus(t)
	sink := &gateSink{open: make(chan struct{})}
	bus.AddSink(sink)
	for i := 0; i < 3; i++ {
		bus.Info("env", events.ArtifactStored, "stored")
	}

	held, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, bus.Settle(held), context.DeadlineExceeded,
		"Settle returned while the sink still held every event")
	require.Equal(t, 0, sink.delivered())

	close(sink.open)
	settled, cancelSettled := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelSettled()
	require.NoError(t, bus.Settle(settled), "Settle never saw the released events handled")
	require.Equal(t, 3, sink.delivered(), "Settle returned before the bus had delivered what was emitted")
}

// panicSink stops its goroutine on the first event, which the bus counts as a
// drop. A Settle that waited for it would wait for ever.
type panicSink struct{}

func (panicSink) Name() string                                { return "panics" }
func (panicSink) Deliver(context.Context, events.Event) error { panic("a broken formatter") }
func (panicSink) Close() error                                { return nil }

func TestBus_SettleDoesNotWaitForASinkThatHasStopped(t *testing.T) {
	bus, _ := newTestBus(t)
	bus.AddSink(panicSink{})
	for i := 0; i < 3; i++ {
		bus.Info("env", events.ArtifactStored, "stored")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, bus.Settle(ctx), "Settle waited on a sink whose goroutine had stopped")
}
