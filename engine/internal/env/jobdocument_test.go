package env

import (
	"encoding/json"
	"strings"
	"testing"
)

// The runner reads this document. A nil Go slice marshals as null, and the
// runner read workflows without a guard, so every `af explore` ever run
// arrived as {"workflows": null} and died on
// "TypeError: Cannot read properties of null (reading 'length')" before the
// browser opened. The command was written, documented, wired into the CLI, and
// had never once worked. It was found by running it against
// examples/next-app, not by reading either side, which is the point: both
// sides compiled and both sides typechecked.
//
// So the shape is asserted on the wire rather than in either language's type
// system, which is the only place the two actually meet.
func TestTheRunnerNeverReceivesANullListWhereItExpectsOne(t *testing.T) {
	// Exactly the document af explore builds: goals and no workflows.
	job := jobDocument{
		BaseURL:   "http://127.0.0.1:46000",
		Artifacts: t.TempDir(),
		Goals:     []goalDoc{{Name: "a-goal", Goal: "Do the thing."}},
	}
	if job.Workflows != nil || job.Personas != nil {
		t.Fatal("the fixture already carries the lists, so this test cannot fail for the right reason")
	}

	// Marshalled the way invokeRunner does, through the same normalisation.
	raw, err := marshalJob(job)
	if err != nil {
		t.Fatalf("marshalJob: %v", err)
	}
	body := string(raw)

	if strings.Contains(body, `"workflows":null`) {
		t.Errorf("the job sends a null workflows list, which the runner dereferences:\n%s", body)
	}
	if strings.Contains(body, `"personas":null`) {
		t.Errorf("the job sends a null personas list:\n%s", body)
	}
	if !strings.Contains(body, `"workflows":[]`) {
		t.Errorf("an explore job should send an empty workflow list, not omit it:\n%s", body)
	}

	// And it is still readable as the document it claims to be, so the
	// normalisation cannot have been done by dropping the field.
	var back struct {
		Workflows []workflowDoc `json:"workflows"`
		Personas  []personaDoc  `json:"personas"`
		Goals     []goalDoc     `json:"goals"`
	}
	if err := json.Unmarshal([]byte(body), &back); err != nil {
		t.Fatalf("the document does not round trip: %v", err)
	}
	if back.Workflows == nil || len(back.Workflows) != 0 {
		t.Errorf("workflows came back as %#v, wanted an empty list", back.Workflows)
	}
	if len(back.Goals) != 1 {
		t.Errorf("the goals did not survive: %#v", back.Goals)
	}
}

// A test job with workflows must keep them, so the normalisation cannot be
// "always send an empty list", which would pass every assertion above and
// break af test instead.
func TestNormalisingTheJobDoesNotDiscardWorkflows(t *testing.T) {
	job := jobDocument{
		BaseURL:   "http://127.0.0.1:46000",
		Artifacts: t.TempDir(),
		Workflows: []workflowDoc{{Name: "sign-up", Expect: []string{"Welcome"}}},
		Personas:  []personaDoc{{Name: "owner"}},
	}
	raw, err := marshalJob(job)
	if err != nil {
		t.Fatalf("marshalJob: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `"name":"sign-up"`) {
		t.Errorf("the workflow was lost on the way to the runner:\n%s", body)
	}
	if !strings.Contains(body, `"name":"owner"`) {
		t.Errorf("the persona was lost on the way to the runner:\n%s", body)
	}
}
