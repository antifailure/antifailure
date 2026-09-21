package cli_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
	"github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/gate"
	"github.com/antifailure/antifailure/engine/internal/pgcrash"
	"github.com/antifailure/antifailure/engine/internal/report"
)

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
		if gate.ChaosUnverified(rule) {
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
