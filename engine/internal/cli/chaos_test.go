package cli_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/errors"
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
		require.Equalf(t, couldNotLook[rule], cli.UnverifiedRuleForTest(rule),
			"the command classifies %q differently from this test", rule)
	}
	// The two rules the env package owns are about the fault rather than the
	// recovery, and both mean the run could not look.
	for _, rule := range []string{env.RuleFaultRefused, env.RuleFaultNotUndone} {
		require.Truef(t, cli.UnverifiedRuleForTest(rule), "%q is not classified", rule)
	}
}

// TestAChaosFindingRoutesToAChaosExitCode holds the other half: a finding that
// stops a check has to name a code a script can branch on, and the two codes
// are two different facts.
func TestAChaosFindingRoutesToAChaosExitCode(t *testing.T) {
	for _, rule := range pgcrash.Rules() {
		err := cli.GateErrorForTest(report.Finding{
			Rule: rule, Level: report.LevelFail, Title: "a title", Where: "fault f",
		})
		require.Error(t, err)
		want := string(errors.AFCHS008)
		if cli.UnverifiedRuleForTest(rule) {
			want = string(errors.AFCHS009)
		}
		require.Containsf(t, err.Error(), want,
			"a finding on %q does not carry the code that says what it means", rule)
	}

	// The liveness arm: a rule outside this namespace must NOT take a chaos
	// code, or the routing above would be proving nothing about the prefix.
	err := cli.GateErrorForTest(report.Finding{
		Rule: "migration.rewrite", Level: report.LevelFail, Title: "a title",
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), string(errors.AFCHS008))
	require.NotContains(t, err.Error(), string(errors.AFCHS009))
}

// TestHolds_SeparatesFoundFromNotLookedAt drives the two answers the command
// prints and returns in its JSON.
func TestHolds_SeparatesFoundFromNotLookedAt(t *testing.T) {
	held, verified := cli.HoldsForTest(nil)
	require.True(t, held)
	require.True(t, verified)

	held, verified = cli.HoldsForTest([]report.Finding{
		{Rule: pgcrash.RuleLostCommit, Level: report.LevelFail},
	})
	require.False(t, held, "a lost commit at fail level still held")
	require.True(t, verified, "a run that found a lost commit looked at it")

	held, verified = cli.HoldsForTest([]report.Finding{
		{Rule: pgcrash.RuleNoCrash, Level: report.LevelWarn},
	})
	require.True(t, held, "a run that could not look was reported as having found something")
	require.False(t, verified)

	// A project that turned the unverified level off has chosen not to be
	// stopped. It has not thereby made the run verified.
	held, verified = cli.HoldsForTest([]report.Finding{
		{Rule: pgcrash.RuleNoCrash, Level: report.LevelIgnore},
	})
	require.True(t, held)
	require.False(t, verified, "an ignored level turned an unverified run into a verified one")
}
