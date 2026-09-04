// Every test here is written the way this repository asks for: one break per
// assertion, and each case names the specific wrong behaviour it would catch.
// A test that only proves the happy path passes is a test that cannot say no.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planJSON builds a `terraform show -json` document with a non-empty prior
// state, because a plan with an empty prior state is rejected before any of
// these rules are reached and would make every case below vacuous.
func planJSON(changes string) string {
	return `{
	  "format_version": "1.2",
	  "prior_state": {"values": {"root_module": {"resources": [{"mode": "managed"}]}}},
	  "resource_changes": [` + changes + `]
	}`
}

const destroyOfTheGrant = `{
  "address": "azurerm_role_assignment.cd_deploys_the_group[0]",
  "type": "azurerm_role_assignment",
  "mode": "managed",
  "change": {"actions": ["delete"]}
}`

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The case this tool was built for. If this passes, the tool is decoration.
func TestUnacknowledgedDestroyFails(t *testing.T) {
	plan := write(t, "plan.json", planJSON(destroyOfTheGrant))
	err := run(plan, filepath.Join(t.TempDir(), "absent.tsv"), "production", "2026-09-03")
	if err == nil {
		t.Fatal("an unacknowledged destroy passed, which is the whole defect this exists to catch")
	}
	if !strings.Contains(err.Error(), "cd_deploys_the_group") {
		t.Fatalf("the failure does not name the resource being destroyed: %v", err)
	}
}

// A plan with nothing to destroy passes, or the gate is a permanent red and
// gets removed.
func TestCleanPlanPasses(t *testing.T) {
	plan := write(t, "plan.json", planJSON(`{
	  "address": "azurerm_container_app.this",
	  "type": "azurerm_container_app",
	  "mode": "managed",
	  "change": {"actions": ["update"]}
	}`))
	if err := run(plan, filepath.Join(t.TempDir(), "absent.tsv"), "production", "2026-09-03"); err != nil {
		t.Fatalf("a plan that destroys nothing was refused: %v", err)
	}
}

// A REPLACE destroys the resource and is caught. Catching only ["delete"] on
// its own would wave through a role assignment being revoked and regranted.
func TestReplaceCountsAsDestroy(t *testing.T) {
	plan := write(t, "plan.json", planJSON(`{
	  "address": "azurerm_role_assignment.cd_deploys_the_group[0]",
	  "type": "azurerm_role_assignment",
	  "mode": "managed",
	  "change": {"actions": ["delete", "create"]}
	}`))
	if err := run(plan, filepath.Join(t.TempDir(), "absent.tsv"), "production", "2026-09-03"); err == nil {
		t.Fatal("a replace passed; a replace revokes the grant and grants it again, and a deploy in that window fails")
	}
}

// The way through the gate has to actually work, or the gate gets deleted the
// first time somebody legitimately needs a destroy.
func TestAcknowledgedDestroyPasses(t *testing.T) {
	plan := write(t, "plan.json", planJSON(destroyOfTheGrant))
	ack := write(t, "ack.tsv", "azurerm_role_assignment.cd_deploys_the_group[0]\t2026-12-31\tThe second identity is federated now, so this grant is being removed on purpose.\n")
	if err := run(plan, ack, "production", "2026-09-03"); err != nil {
		t.Fatalf("an acknowledged destroy was still refused: %v", err)
	}
}

// Rule 2. An expired acknowledgement stops holding the gate open, and says so.
func TestExpiredAcknowledgementFails(t *testing.T) {
	plan := write(t, "plan.json", planJSON(destroyOfTheGrant))
	ack := write(t, "ack.tsv", "azurerm_role_assignment.cd_deploys_the_group[0]\t2026-08-01\tThe second identity is federated now, so this grant is being removed on purpose.\n")
	err := run(plan, ack, "production", "2026-09-03")
	if err == nil {
		t.Fatal("an expired acknowledgement still waved the destroy through")
	}
	if !strings.Contains(err.Error(), "expired on 2026-08-01") {
		t.Fatalf("the failure does not say the acknowledgement lapsed: %v", err)
	}
}

// Rule 1. A bare address with no expiry is not a decision.
func TestAcknowledgementWithoutExpiryFails(t *testing.T) {
	plan := write(t, "plan.json", planJSON(destroyOfTheGrant))
	ack := write(t, "ack.tsv", "azurerm_role_assignment.cd_deploys_the_group[0]\n")
	err := run(plan, ack, "production", "2026-09-03")
	if err == nil || !strings.Contains(err.Error(), "no expiry") {
		t.Fatalf("a bare address with no expiry was accepted as a decision: %v", err)
	}
}

// Rule 1, the other half. An expiry with no prose is not a reason.
func TestAcknowledgementWithoutReasonFails(t *testing.T) {
	plan := write(t, "plan.json", planJSON(destroyOfTheGrant))
	ack := write(t, "ack.tsv", "azurerm_role_assignment.cd_deploys_the_group[0]\t2026-12-31\ttidy up\n")
	err := run(plan, ack, "production", "2026-09-03")
	if err == nil || !strings.Contains(err.Error(), "no stated reason") {
		t.Fatalf("an acknowledgement with no stated reason was accepted: %v", err)
	}
}

// Rule 3. This is the rule that keeps the file honest: permission left behind
// after the destroy happened would wave through a future destroy of the same
// address that nobody agreed to.
func TestStaleAcknowledgementFails(t *testing.T) {
	plan := write(t, "plan.json", planJSON(`{
	  "address": "azurerm_container_app.this",
	  "type": "azurerm_container_app",
	  "mode": "managed",
	  "change": {"actions": ["update"]}
	}`))
	ack := write(t, "ack.tsv", "azurerm_role_assignment.cd_deploys_the_group[0]\t2026-12-31\tThe second identity is federated now, so this grant is being removed on purpose.\n")
	err := run(plan, ack, "production", "2026-09-03")
	if err == nil || !strings.Contains(err.Error(), "does not propose") {
		t.Fatalf("an acknowledgement that matched nothing was left in place: %v", err)
	}
}

// The check that keeps this check honest. A plan built with no state backend
// reads every resource as a create and CANNOT contain a destroy, so passing one
// would be a green check that examined nothing.
func TestEmptyStatePlanIsRefusedRatherThanPassed(t *testing.T) {
	plan := write(t, "plan.json", `{
	  "format_version": "1.2",
	  "prior_state": {"values": {"root_module": {}}},
	  "resource_changes": [{
	    "address": "azurerm_role_assignment.cd_deploys_the_group[0]",
	    "type": "azurerm_role_assignment",
	    "mode": "managed",
	    "change": {"actions": ["create"]}
	  }]
	}`)
	err := run(plan, filepath.Join(t.TempDir(), "absent.tsv"), "production", "2026-09-03")
	if err == nil {
		t.Fatal("an empty-state plan PASSED; that is a green check that examined nothing")
	}
	if !strings.Contains(err.Error(), "NO PRIOR STATE") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
}

// THE REGRESSION THIS TOOL SHIPPED WITH AND THEN CAUGHT IN ITSELF.
//
// The shape below is copied from a real `terraform plan -var-file=production.tfvars`
// run with no state backend at all. It is not empty: Terraform reads data
// sources during the plan and records them in prior_state, so a stateless plan
// still carries module.control_plane.data.azurerm_client_config.current. The
// first version of this guard counted prior-state resources without looking at
// their mode, found one, concluded the plan had real state, and PASSED a plan
// in which a destroy was impossible. The synthetic empty-state test above did
// not catch it because it was written from what an empty plan ought to look
// like rather than from one.
func TestPlanWithOnlyDataSourcesInPriorStateIsRefused(t *testing.T) {
	plan := write(t, "plan.json", `{
	  "format_version": "1.2",
	  "prior_state": {"values": {"root_module": {"child_modules": [{
	    "address": "module.control_plane",
	    "resources": [{"mode": "data", "address": "module.control_plane.data.azurerm_client_config.current"}]
	  }]}}},
	  "resource_changes": []
	}`)
	err := run(plan, filepath.Join(t.TempDir(), "absent.tsv"), "production", "2026-09-03")
	if err == nil {
		t.Fatal("a stateless plan whose prior state holds only a data source was accepted as having real state")
	}
	if !strings.Contains(err.Error(), "NO PRIOR STATE") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
}

// Resources nested in modules count as prior state. Counting only the root
// module would call every real control-plane plan empty, because almost
// everything in that stack lives inside module.control_plane.
func TestPriorStateCountsNestedModules(t *testing.T) {
	plan := write(t, "plan.json", `{
	  "format_version": "1.2",
	  "prior_state": {"values": {"root_module": {
	    "child_modules": [{"resources": [{"mode": "managed"}, {"mode": "managed"}]}]
	  }}},
	  "resource_changes": []
	}`)
	if err := run(plan, filepath.Join(t.TempDir(), "absent.tsv"), "production", "2026-09-03"); err != nil {
		t.Fatalf("a plan whose state lives in a child module was called empty: %v", err)
	}
}

// "Could not read it" and "found nothing" must not share an exit code. Eight
// tfsec findings once sat behind a green check in this repository for exactly
// this reason.
func TestUnparseablePlanIsAnErrorNotAnEmptyResult(t *testing.T) {
	plan := write(t, "plan.json", "this is the human readable plan, not JSON")
	if err := run(plan, filepath.Join(t.TempDir(), "absent.tsv"), "production", "2026-09-03"); err == nil {
		t.Fatal("a plan this tool could not parse was reported as destroying nothing")
	}
}

// JSON that parses but is not a plan document must not report zero destroys.
func TestJSONWithoutFormatVersionIsRefused(t *testing.T) {
	plan := write(t, "plan.json", `{"resource_changes": []}`)
	err := run(plan, filepath.Join(t.TempDir(), "absent.tsv"), "production", "2026-09-03")
	if err == nil || !strings.Contains(err.Error(), "format_version") {
		t.Fatalf("a JSON document that is not a plan was accepted: %v", err)
	}
}

// The acknowledgement file shipped in this repository has to parse, and its
// steady state is that it acknowledges nothing.
func TestShippedAcknowledgementFileIsWellFormedAndEmpty(t *testing.T) {
	acks, err := readAcknowledgements("destroys-acknowledged.tsv")
	if err != nil {
		t.Fatalf("the acknowledgement file in this repository does not parse: %v", err)
	}
	if len(acks) != 0 {
		t.Fatalf("this repository acknowledges %d destroy(s); the steady state is none: %v", len(acks), acks)
	}
}
