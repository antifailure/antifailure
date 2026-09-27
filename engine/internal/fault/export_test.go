package fault

import (
	"context"
	"testing"
	"time"
)

// ThawConfirmWindowForTest shortens the thaw read back's window for the arms
// that end on its expiry, and restores it afterwards.
//
// The window is ten seconds in production for a reason stated beside it, and a
// test that waits that long three times over is a test somebody will delete.
// Shortening it is safe for those arms because what they assert is the SENTENCE
// the expiry produces and not the duration: the still-frozen and
// could-not-be-confirmed paths are reached by the window ending, whatever it was
// set to. The arm that proves the wait actually WAITS deliberately does not use
// this, because a window shorter than the lag it is meant to absorb would make
// that test pass for the wrong reason.
func ThawConfirmWindowForTest(t *testing.T, d time.Duration) {
	t.Helper()
	was := thawConfirmWindow
	thawConfirmWindow = d
	t.Cleanup(func() { thawConfirmWindow = was })
}

// InjectionForTest builds an injection whose undo is the given function.
//
// The constructor is here rather than in the test package because Injection's
// undo is deliberately unexported: an injection that a caller could give a new
// undo to would be an injection whose undo is not the one the fault registered.
// A test still has to be able to drive the idempotence, so the door is exactly
// this wide and it is only open in a test build.
func InjectionForTest(undo func() error) *Injection {
	return &Injection{undo: func(context.Context) error { return undo() }}
}
