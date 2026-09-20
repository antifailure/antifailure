package fault

import "context"

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
