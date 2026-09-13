package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/report"
)

func passed(name string) env.WorkflowResult {
	r := env.WorkflowResult{Workflow: name}
	r.Outcome.Verdict = "pass"
	r.Outcome.Cause = "succeeded"
	return r
}

// af ci. A workflow its budget stopped, beside one that passed, is blocked in
// the verdict, in the exit code and in the comment. Not a pass, because nothing
// about it was verified, and not a failure, because an unfinished run is no
// evidence against the change.
func TestCI_AWorkflowStoppedByItsBudgetIsBlockedNotAPassAndNotAFailure(t *testing.T) {
	run := report.Run{Workflows: reportWorkflows([]env.WorkflowResult{passed("sign-in"), budgetStopped("checkout")})}
	require.Equal(t, "budget-exhausted", run.Workflows[1].Cause, "the cause reaches the report")

	require.Equal(t, report.VerdictBlocked, run.Verdict())
	require.NoError(t, ciExit(run), "a blocked workflow must not fail the build")

	md := run.Markdown()
	require.Contains(t, md, "| `checkout` | blocked | Stopped at its time budget of 2s")
	require.NotContains(t, md, "FAILED")
}

// af ci. A run whose only workflow its budget stopped verified nothing, so the
// gate refuses it as it refuses any run that verified nothing, with the budget
// named and the budget's own code rather than the general one or a failure's.
func TestCI_ARunOnlyABudgetStoppedIsRefusedWithTheBudgetCode(t *testing.T) {
	run := report.Run{Workflows: reportWorkflows([]env.WorkflowResult{budgetStopped("checkout")})}
	require.Equal(t, report.VerdictBlocked, run.Verdict(), "before the gate, the run is blocked")

	f := workflowsUnverifiedFinding(run, defaultGate())
	require.NotNil(t, f, "a run that verified nothing must produce a finding")
	require.Equal(t, report.LevelFail, f.Level)
	require.Equal(t, budgetStoppedTitle, f.Title)
	require.Equal(t, "checkout", f.Where)
	require.Contains(t, f.Detail, "time budget of 2s")

	code, message := codeOfError(t, gateError(*f))
	require.Equal(t, aferrors.AFAGT024, code)
	require.Contains(t, message, "checkout")

	run.Findings = append(run.Findings, *f)
	require.Equal(t, aferrors.ExitInterruptedClean, exitCodeOfSilent(t, ciExit(run)),
		"the budget's exit code, not a failed workflow's")
	require.Contains(t, run.Markdown(), "| `checkout` | blocked |", "the row still reads blocked")
}
