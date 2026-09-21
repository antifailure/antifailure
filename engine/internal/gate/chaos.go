package gate

import "github.com/antifailure/antifailure/engine/internal/report"

// What a chaos run means, decided in one place for every front end that asks.
//
// THIS LIVED IN engine/internal/cli AND COULD NOT STAY THERE. The command line
// read it, af ci read it to choose between two exit codes, and the MCP server
// could not read it at all, because engine/internal/cli imports the MCP package
// to start the server and the import cannot go back the other way. The choices
// were a second copy inside the MCP package or this move, and a second copy of
// a classification is the shape this repository keeps finding in its own
// instruments: two implementations drift, and the one that drifts is the one a
// model believed. The package comment above already says so about the migration
// evaluator, which made the same move for the same reason.
//
// The rule names are here rather than in engine/internal/env for the same
// reason the classification is: they are the vocabulary the classification is
// written in, and a name that lives beside its only producer is a name the
// reader of a finding has to import the producer to recognise. env aliases
// these, so there is one spelling of each string in the tree.
const (
	// RuleFaultRefused is a fault that would not go in. Nothing measured after
	// it means anything, because the system under test never broke.
	RuleFaultRefused = "chaos.fault.refused"
	// RuleFaultUnsafe is a fault the injector turned down BEFORE it acted,
	// because its effect would have reached past this environment. What it was
	// declared to establish was not established, which is why it is a rule the
	// run could not look at. Nothing else in the run is touched by it, which
	// is why it is not RuleFaultRefused: that rule's sentence invalidates what
	// came after, and a refusal leaves the environment exactly as it was.
	RuleFaultUnsafe = "chaos.fault.unsafe"
	// RuleFaultNotUndone is a fault that would not come out. The environment
	// is still broken, so whatever ran next was measured against it.
	RuleFaultNotUndone = "chaos.fault.not_undone"
)

// ChaosUnverified reports whether a rule means "I could not look" rather than
// "I looked and it is wrong".
//
// A list rather than a level, because the level is a policy choice: a project
// that raised chaos_unverified to fail has not thereby turned an unverified run
// into a verified one, and a project that lowered it to ignore has not made one
// verified either.
//
// Written out rather than derived from a prefix. A rule added to pgcrash and
// classified nowhere would take the default, and the default is "this was found
// to be wrong", which is the wrong answer for a rule that means the run never
// established anything. The test beside this holds every rule pgcrash declares
// to being on exactly one of the two lists, so a new rule fails to be covered
// rather than being silently absorbed.
func ChaosUnverified(rule string) bool {
	switch rule {
	case "chaos.recovery.no_crash", "chaos.recovery.no_replay",
		"chaos.recovery.control_unreadable", "chaos.integrity.amcheck_unavailable",
		"chaos.integrity.checksums_off", "chaos.durability.inconsistent_ledger",
		RuleFaultRefused, RuleFaultUnsafe, RuleFaultNotUndone:
		return true
	}
	return false
}

// ChaosHolds reads a run's findings for the two answers a caller needs.
//
// Two answers rather than one, and the second is not the negation of the first.
// Held says nothing was found to be wrong. Verified says the run established
// what it set out to. A run that is held and not verified has not passed, it
// has not looked, and collapsing the two is the exact defect this whole feature
// exists to catch in somebody else's system.
//
// Held is read from the LEVEL, because whether a finding stops a merge is the
// project's decision to make. Verified is read from the RULE, because whether
// the run looked is a fact about the run and no policy can change it.
func ChaosHolds(findings []report.Finding) (held, verified bool) {
	held, verified = true, true
	for _, f := range findings {
		if f.Level == report.LevelFail {
			held = false
		}
		if ChaosUnverified(f.Rule) {
			verified = false
		}
	}
	return held, verified
}
