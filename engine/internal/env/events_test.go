package env_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// The engine has published events onto a bus since the journal was written,
// and until af up --hud there was no way for anything to subscribe to it. A
// bus with no subscribers is not a feature with a small audience, it is a
// feature with none: every event was built, formatted, and delivered to
// nothing.
//
// These tests are the call site check. They prove a sink handed to AddSink is
// attached to the session the next command opens, receives the lifecycle, and
// is closed and drained before the command returns.
//
// A refusing policy hook is what makes them run with no Docker daemon: the
// check happens after the session is open, which is where the bus lives, and
// before anything is created.
func TestAddSink_ReceivesTheLifecycleAndIsDrainedBeforeUpReturns(t *testing.T) {
	registry := extension.NewRegistry()
	registry.AddPolicy(&refusingHook{err: errors.New("refused by the test")})

	o := newOrchestrator(t, registry)
	sink := events.NewMemorySink(64)
	o.AddSink(sink)

	_, err := o.Up(context.Background())
	require.Error(t, err)

	// Read with no sleep and no polling. Close drains every sink queue and
	// waits for its goroutine before it returns, so a delivered event is
	// already here by the time Up has returned. If that were not true this
	// assertion would be flaky, which is the point of making it.
	got := sink.Events()
	require.NotEmpty(t, got, "nothing reached the sink, so AddSink is not attached to the session bus")

	require.Len(t, sink.OfType(events.EnvCreating), 1,
		"the run should announce itself before it does anything")
	require.Len(t, sink.OfType(events.EnvFailed), 1,
		"a refused run should say so on the stream, not only in the returned error")
}

// The failure reaches the stream with the reason, at error level. Level is
// what puts a line in the dashboard's error pane rather than only in the
// scrolling tail, so a failure that arrived at info level would be a run that
// looks fine on screen and returned an error.
func TestAddSink_ReportsTheFailureWithItsReasonAtErrorLevel(t *testing.T) {
	registry := extension.NewRegistry()
	registry.AddPolicy(&refusingHook{err: errors.New("organization policy refuses this")})

	o := newOrchestrator(t, registry)
	sink := events.NewMemorySink(64)
	o.AddSink(sink)

	_, err := o.Up(context.Background())
	require.Error(t, err)

	failed := sink.OfType(events.EnvFailed)
	require.Len(t, failed, 1)
	require.Equal(t, events.LevelError, failed[0].Level)
	require.Contains(t, failed[0].Msg, "organization policy refuses this")
	require.Equal(t, o.EnvID(), failed[0].Env,
		"an event with no environment cannot be filtered by the display")
}

// Every event carries the environment it belongs to, and a run announces
// itself with the branch, because a dashboard watching the wrong environment
// is a dashboard that says nothing is happening while a great deal is.
func TestAddSink_StampsTheEnvironmentAndBranch(t *testing.T) {
	registry := extension.NewRegistry()
	registry.AddPolicy(&refusingHook{err: errors.New("no")})

	o := newOrchestrator(t, registry)
	sink := events.NewMemorySink(64)
	o.AddSink(sink)
	_, _ = o.Up(context.Background())

	creating := sink.OfType(events.EnvCreating)
	require.Len(t, creating, 1)
	require.Equal(t, o.EnvID(), creating[0].Env)
	require.Equal(t, "feature/hooks", creating[0].Data["branch"])

	for _, e := range sink.Events() {
		require.NotEmpty(t, e.Type, "an event with no type cannot be routed to a pane")
		require.NotZero(t, e.Seq, "an event with no sequence cannot be reordered")
	}
}

// A nil sink is ignored rather than attached. Nothing in the engine passes
// one, but a caller that builds a sink conditionally can, and a nil in the
// list would panic on the first event of a run rather than at the call.
func TestAddSink_IgnoresNil(t *testing.T) {
	registry := extension.NewRegistry()
	registry.AddPolicy(&refusingHook{err: errors.New("no")})

	o := newOrchestrator(t, registry)
	o.AddSink(nil)
	sink := events.NewMemorySink(8)
	o.AddSink(sink)

	_, err := o.Up(context.Background())
	require.Error(t, err)
	require.NotEmpty(t, sink.Events(), "the real sink should still be attached")
}
