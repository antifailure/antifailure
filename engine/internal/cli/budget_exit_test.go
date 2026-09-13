package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// budgetStopped is a runner result for a workflow its time budget stopped.
func budgetStopped(name string) env.WorkflowResult {
	r := env.WorkflowResult{Workflow: name}
	r.Outcome.Verdict = "blocked"
	r.Outcome.Cause = "budget-exhausted"
	r.Outcome.Detail = "Stopped at its time budget of 2s, 2.1s into the workflow on attempt 1, after: Open /billing."
	return r
}

func codeOfError(t *testing.T, err error) (aferrors.Code, string) {
	t.Helper()
	require.Error(t, err)
	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded), "expected a coded error, got %v", err)
	return coded.Code(), coded.Error()
}

// af test. A run whose only workflow its budget stopped verified nothing, and
// the reason is the budget: its own code, naming the workflow and the budget,
// rather than the general code for a run that proved nothing.
func TestTest_ARunStoppedByABudgetSaysSoWithItsOwnCode(t *testing.T) {
	report := &env.TestReport{Results: []env.WorkflowResult{budgetStopped("checkout")}, Blocked: 1}
	require.True(t, report.NothingVerified(), "a budget-stopped run verified nothing")
	require.False(t, report.AnyFailed(), "a budget-stopped run is not a failure of the change")

	code, message := codeOfError(t, nothingVerified(report))
	require.Equal(t, aferrors.AFAGT024, code)
	require.Contains(t, message, "checkout")
	require.Contains(t, message, "time budget of 2s")
}

// af test. A failed workflow says it failed. AF-AGT-002 used to render
// "exhausted its budget of its attempts" for every failure, so a plain failure
// read as a budget problem.
func TestTest_AFailedWorkflowSaysItFailedAndNothingAboutABudget(t *testing.T) {
	failed := env.WorkflowResult{Workflow: "checkout"}
	failed.Outcome.Verdict = "fail"
	failed.Outcome.Cause = "expectation-not-met"
	report := &env.TestReport{Results: []env.WorkflowResult{failed}, Failed: 1}

	code, message := codeOfError(t, failure(report))
	require.Equal(t, aferrors.AFAGT002, code)
	require.Contains(t, message, "Workflow checkout failed")
	require.NotContains(t, message, "budget")
}
