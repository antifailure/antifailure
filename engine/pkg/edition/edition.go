// Package edition is how a binary says which edition it is.
//
// MIT, like the rest of the engine. Nothing here is enterprise code and nothing
// here imports any: this is a struct of strings and a context key.
//
// It exists because af license status has to tell the truth in two binaries
// built from one command tree. The community binary attaches nothing and the
// command says what it has always said, which is that this edition has no
// licence and needs none. The enterprise binary attaches what it worked out at
// startup, and the command reports that instead.
//
// A context value rather than an option, for the same reason the enterprise
// edition puts its licence in one: the answer is decided in main and consumed
// several frames down, and threading a parameter through every frame between
// them is a change nobody makes and a report that therefore stays wrong.
//
// The default is the honest one. A binary that attaches nothing is described as
// the community edition, so a build that forgets this does not claim to be
// licensed.
package edition

import "context"

// Status is what a binary reports about its own licensing.
//
// Strings rather than the licence types, and that is what keeps this package
// free of enterprise code. The evaluation lives in the enterprise module, which
// the community build cannot resolve; what crosses into the community build is
// only the rendered answer.
type Status struct {
	// Name is the edition, as "community" or "enterprise".
	Name string
	// State is what the licence is doing: none, active, grace, expired,
	// revoked, wrong_org, clock_rollback.
	State string
	// Org is who the licence was issued to, empty when there is none.
	Org string
	// Plan names the tier, for display.
	Plan string
	// Features are what is permitted right now, sorted. Empty is a complete
	// answer and means nothing is: an expired licence permits nothing and
	// preserves every setting.
	Features []string
	// Message is the sentence describing the state, always present.
	Message string
	// Warning is what to draw attention to, empty when there is nothing.
	Warning string
	// ExpiresAt is when the licence stops being current, as a date somebody
	// reads. Empty when there is no licence.
	ExpiresAt string
}

type contextKey struct{}

// With attaches a status to a context.
func With(ctx context.Context, s Status) context.Context {
	return context.WithValue(ctx, contextKey{}, s)
}

// From reads the status, reporting whether one was attached.
//
// The second return value matters: a zero Status and no Status are different
// things, and only the caller can decide what to print for the second.
func From(ctx context.Context) (Status, bool) {
	s, ok := ctx.Value(contextKey{}).(Status)
	return s, ok
}

// The feature names the engine itself gates on.
//
// Strings rather than an import of the licence's own type, for the reason this
// package exists: the community build cannot resolve the enterprise module. The
// enterprise module carries a test that these spellings match its own
// constants, because a rename on one side and not the other would detach the
// gate silently and the symptom would be a feature that became free.
const (
	// FeatureMultiRuntime is placing an environment across more than one
	// runtime target, matched on tags.
	FeatureMultiRuntime = "multi_runtime"
)

// Permits reports whether the licence on this context turns a feature on.
//
// This is the engine side of a licence check, and it is deliberately the only
// shape one takes here. The enterprise binary decides what is permitted at
// startup and attaches the answer; everything below reads it. A build that
// attaches nothing permits nothing, so the community edition and an expired
// licence and a licence for another organization all arrive at the same no
// without this package knowing the difference between them.
//
// Enabled is asked per call rather than once at startup for the reason the
// policy hook already documents: a licence can lapse while the process runs,
// and a feature that keeps working until a restart is a feature the customer
// stopped paying for and cannot turn off.
func Permits(ctx context.Context, feature string) bool {
	s, ok := From(ctx)
	if !ok {
		return false
	}
	return s.Permits(feature)
}

// Permits reports whether a status carries a feature.
//
// A method as well as the function, because the two callers are different: a
// command that has already read the status asks the value, and code deep in a
// lifecycle asks the context.
func (s Status) Permits(feature string) bool {
	for _, f := range s.Features {
		if f == feature {
			return true
		}
	}
	return false
}
