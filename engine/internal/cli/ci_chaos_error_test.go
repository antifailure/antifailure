package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/report"
)

func TestCIChaosSetupErrorIsAnUnverifiedFinding(t *testing.T) {
	for _, level := range []report.Level{report.LevelWarn, report.LevelFail} {
		run := report.Run{Declared: 1, Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}}}
		require.Equal(t, report.VerdictPass, run.Verdict(), "fixture must reproduce the formerly green run")
		findings := recordChaosFailure(&run, nil, errors.New("injector unavailable"),
			report.Policy{ChaosUnverified: level})
		require.NotNil(t, run.Chaos)
		require.Contains(t, run.Chaos.Skipped, "injector unavailable")
		require.Len(t, findings, 1)
		require.Equal(t, level, findings[0].Level)
		run.Findings = append(run.Findings, findings...)
		require.NotEqual(t, report.VerdictPass, run.Verdict())
	}
}
