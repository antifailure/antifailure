package cli

import "github.com/antifailure/antifailure/engine/internal/report"

// The three doors this package opens for its own tests, and no wider.
//
// holds, unverifiedRule and gateError are unexported because nothing outside
// the command should be deciding what a finding means. A test still has to be
// able to hold the classification against the set of rules pgcrash declares,
// because that is the pairing that rots: a rule added on one side and
// classified on neither takes the default, and the default is the wrong answer
// for a rule that means "I could not look".

func UnverifiedRuleForTest(rule string) bool { return unverifiedRule(rule) }

func HoldsForTest(findings []report.Finding) (held, verified bool) { return holds(findings) }

func GateErrorForTest(f report.Finding) error { return gateError(f) }
