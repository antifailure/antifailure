package gate_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/gate"
	"github.com/antifailure/antifailure/engine/internal/pgcrash"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// TestEveryChaosRuleIsClassifiedAsFoundOrNotLookedAt holds the split the whole
// feature rests on, from the side that rots.
//
// A rule added to pgcrash and classified nowhere would take the default, which
// is "this was found to be wrong". That is the wrong default for a rule that
// means "I could not look", and the mistake is invisible: the run still exits,
// the report still renders, and the only symptom is a project being stopped by
// a check that never checked anything. So every declared rule has to be named
// on one side or the other here.
func TestEveryChaosRuleIsClassifiedAsFoundOrNotLookedAt(t *testing.T) {
	// The rules that mean "I could not look". Written out rather than derived,
	// so that adding a rule to pgcrash makes this list fail to cover it rather
	// than silently absorbing it.
	couldNotLook := map[string]bool{
		pgcrash.RuleNoCrash:            true,
		pgcrash.RuleNoReplay:           true,
		pgcrash.RuleControlUnreadable:  true,
		pgcrash.RuleAmcheckUnavailable: true,
		pgcrash.RuleChecksumsOff:       true,
		pgcrash.RuleInconsistentLedger: true,
	}
	found := map[string]bool{
		pgcrash.RuleLostCommit:      true,
		pgcrash.RulePhantomCommit:   true,
		pgcrash.RuleReplayShort:     true,
		pgcrash.RuleNotInProduction: true,
		pgcrash.RuleTimelineMoved:   true,
		pgcrash.RuleRelationDamaged: true,
	}
	for _, rule := range pgcrash.Rules() {
		require.Truef(t, couldNotLook[rule] != found[rule],
			"the rule %q is on both lists or on neither, so nothing decides what a finding on it means", rule)
		require.Equalf(t, couldNotLook[rule], gate.ChaosUnverified(rule),
			"the gate classifies %q differently from this test", rule)
	}
	// The three rules the env package owns are about the fault rather than
	// the recovery, and all three mean the run could not look at what that
	// fault was declared to establish.
	for _, rule := range []string{env.RuleFaultRefused, env.RuleFaultUnsafe, env.RuleFaultNotUndone} {
		require.Truef(t, gate.ChaosUnverified(rule), "%q is not classified", rule)
	}
}

// TestHeldAndVerifiedAreTwoAnswers holds the distinction the whole feature
// rests on, from the side a caller reads.
//
// Held is a policy question and verified is a fact about the run, so a project
// that lowered chaos_unverified to ignore still gets verified false, and a
// project that raised it to fail still gets verified false rather than a run
// that suddenly looked. Collapsing the two is what this exists to stop.
func TestHeldAndVerifiedAreTwoAnswers(t *testing.T) {
	held, verified := gate.ChaosHolds(nil)
	require.True(t, held, "no finding is nothing found to be wrong")
	require.True(t, verified, "no finding is nothing that failed to look")

	held, verified = gate.ChaosHolds([]report.Finding{
		{Rule: pgcrash.RuleLostCommit, Level: report.LevelFail},
	})
	require.False(t, held, "a lost commit is a failure")
	require.True(t, verified, "a lost commit is something the run DID establish")

	held, verified = gate.ChaosHolds([]report.Finding{
		{Rule: pgcrash.RuleNoCrash, Level: report.LevelWarn},
	})
	require.True(t, held, "a run that could not look found nothing wrong")
	require.False(t, verified, "and it has not passed either")

	// The policy cannot turn an unverified run into a verified one, in either
	// direction. This is the assertion that stops verified being read off the
	// level as a shortcut.
	_, verified = gate.ChaosHolds([]report.Finding{
		{Rule: pgcrash.RuleNoCrash, Level: report.LevelIgnore},
	})
	require.False(t, verified, "ignoring an unverified run does not make it verified")
	_, verified = gate.ChaosHolds([]report.Finding{
		{Rule: pgcrash.RuleNoCrash, Level: report.LevelFail},
	})
	require.False(t, verified, "failing on an unverified run does not make it verified")

	// And a fault that would not go in, which is env's rule rather than
	// pgcrash's and means the same thing.
	held, verified = gate.ChaosHolds([]report.Finding{
		{Rule: env.RuleFaultRefused, Level: report.LevelWarn},
	})
	require.True(t, held)
	require.False(t, verified)
}
