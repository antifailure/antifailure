package env

import (
	"strings"
	"testing"
)

// The other half of the null-list fix, and the half a test of the shape alone
// cannot reach: "always send an empty list" would satisfy every assertion in
// runnerdocument_test.go and would break af test, which is the command that
// actually has workflows to send.
//
// So this drives the same function with both lists populated and reads the
// bytes back. It fails if normalising ever starts replacing content rather
// than filling in an absence.
func TestNormalisingTheJobDoesNotDiscardWorkflows(t *testing.T) {
	job := jobDocument{
		BaseURL:   "http://127.0.0.1:46000",
		Artifacts: t.TempDir(),
		Workflows: []workflowDoc{{Name: "sign-up", Expect: []string{"Welcome"}}},
		Personas:  []personaDoc{{Name: "owner"}},
	}
	raw, err := (&Orchestrator{}).runnerDocument(job)
	if err != nil {
		t.Fatalf("runnerDocument: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `"name":"sign-up"`) {
		t.Errorf("the workflow was lost on the way to the runner:\n%s", body)
	}
	if !strings.Contains(body, `"name":"owner"`) {
		t.Errorf("the persona was lost on the way to the runner:\n%s", body)
	}
}
