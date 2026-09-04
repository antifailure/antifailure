package report_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/report"
)

func TestIncompleteLoadPriority(t *testing.T) {
	for _, tc := range []struct {
		name, workflow string
		level          report.Level
		verdict        string
	}{
		{"warning", "pass", report.LevelWarn, report.VerdictBlocked},
		{"intermittent", "flaky", report.LevelIgnore, report.VerdictBlocked},
		{"real failure", "pass", report.LevelFail, report.VerdictFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := report.Run{
				Workflows: []report.Workflow{{Name: "read", Verdict: tc.workflow}},
				Findings:  []report.Finding{{Level: tc.level}},
				Load:      &report.Load{Unavailable: "interrupted"},
			}
			require.Equal(t, tc.verdict, run.Verdict())
		})
	}
}
