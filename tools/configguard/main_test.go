// Every test here is written the way this repository asks for: one break per
// assertion, and each case names the specific wrong behaviour it would catch.
//
// THE FIXTURES ARE REAL PLANS, NOT SKETCHES OF ONE. testdata/no-changes.json
// is `terraform show -json` of a targeted plan of staging on 2026-09-06, made
// with the same command deploy/cd/apply-config.sh runs, after that night's
// hand apply had landed. testdata/env-and-secrets-added.json is the same plan
// from a copy of staging.tfvars with one env value edited, one variable added
// and the Stripe price set, which adds four variables and two secret
// references at once. Both were reduced to the app's before and after plus the
// address and actions of every other resource, the subscription id and the
// domain verification id were replaced, and the egress addresses were
// replaced with documentation addresses. Nothing else was edited: the shape of
// after_unknown, the no-op list, and the secret entries with their null values
// are what Terraform produced. tools/planguard shipped with a synthetic empty
// state fixture that did not look like a real one and passed a plan it should
// have refused; the refusals below are derived from a real accepted plan by
// changing one thing each, so the thing changed is the only difference.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const app = defaultTarget

// load decodes a fixture into a generic document so a test can change exactly
// one thing in it.
func load(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("fixture %s does not parse: %v", name, err)
	}
	return doc
}

func save(t *testing.T, doc any) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func changes(doc map[string]any) []any { return doc["resource_changes"].([]any) }

// appChange returns the container app's resource change from a document.
func appChange(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	for _, c := range changes(doc) {
		cm := c.(map[string]any)
		if cm["address"] == app {
			return cm
		}
	}
	t.Fatal("fixture has no container app change")
	return nil
}

func change(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	return appChange(t, doc)["change"].(map[string]any)
}

func after(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	return change(t, doc)["after"].(map[string]any)
}

func container(t *testing.T, obj map[string]any) map[string]any {
	t.Helper()
	return obj["template"].([]any)[0].(map[string]any)["container"].([]any)[0].(map[string]any)
}

func mustRefuse(t *testing.T, doc map[string]any, want string) error {
	t.Helper()
	_, err := run(save(t, doc), app, "staging")
	if err == nil {
		t.Fatalf("the plan was ACCEPTED; it should have been refused for: %s", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refused for the wrong reason.\n  want a message containing %q\n  got: %v", want, err)
	}
	return err
}

// The case the tool exists to say yes to. A guard that never says yes is an
// unconditional throw that happens to look right.
func TestRealConfigurationOnlyUpdateIsAccepted(t *testing.T) {
	v, err := run(filepath.Join("testdata", "env-and-secrets-added.json"), app, "staging")
	if err != nil {
		t.Fatalf("the real configuration only plan was refused: %v", err)
	}
	if v.NoChanges {
		t.Fatal("an update was reported as no changes")
	}
	for _, want := range []string{"+AF_POSTHOG_PROJECT_KEY", "+AF_STRIPE_PRICE_TEAM", "~AF_SITE_ORIGIN", "+stripe-secret-key", "+stripe-webhook-secret", "image:   unchanged", "traffic: unchanged"} {
		if !strings.Contains(v.Summary, want) {
			t.Errorf("the summary does not say %q:\n%s", want, v.Summary)
		}
	}
}

// The steady state after every deploy. This is what the job sees on almost
// every run, and it has to be told apart from a refusal by exit status.
func TestRealNoChangesPlanIsNothingToApply(t *testing.T) {
	v, err := run(filepath.Join("testdata", "no-changes.json"), app, "staging")
	if err != nil {
		t.Fatalf("a plan with no changes was refused: %v", err)
	}
	if !v.NoChanges {
		t.Fatal("a plan with no changes was reported as an update to apply")
	}
	if !strings.Contains(v.Summary, "no configuration change") {
		t.Fatalf("the summary does not say there is nothing to do: %s", v.Summary)
	}
}

// deploy.sh owns the image. Terraform ignores it on this resource, so a plan
// that moves it was made against a state Terraform did not refresh, and
// applying it would create a revision on an older build.
func TestImageChangeIsRefused(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	container(t, after(t, doc))["image"] = "ghcr.io/antifailure/control-plane@sha256:0000000000000000000000000000000000000000000000000000000000000000"
	mustRefuse(t, doc, "serving image")
}

// deploy.sh owns the traffic. It puts a revision in at zero and shifts after
// the health gate; an apply that moves a weight bypasses the gate.
func TestTrafficWeightChangeIsRefused(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	weights := after(t, doc)["ingress"].([]any)[0].(map[string]any)["traffic_weight"].([]any)
	weights[0].(map[string]any)["percentage"] = float64(0)
	mustRefuse(t, doc, "traffic weights")
}

// A targeted plan carries the app's dependencies. One of them changing is a
// stack change, whatever it is, and a person plans and applies that.
func TestAnySecondResourceChangingIsRefused(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	const other = `module.control_plane.azurerm_key_vault_secret.seeded["github-client-secret"]`
	found := false
	for _, c := range changes(doc) {
		cm := c.(map[string]any)
		if cm["address"] == other {
			cm["change"].(map[string]any)["actions"] = []any{"update"}
			found = true
		}
	}
	if !found {
		t.Fatalf("the fixture no longer carries %s; pick another dependency", other)
	}
	err := mustRefuse(t, doc, "other than")
	if !strings.Contains(err.Error(), other) {
		t.Fatalf("the refusal does not name the resource that changed: %v", err)
	}
}

// THE MAINTENANCE JOB'S DRIFT IS OUTSIDE THE TARGET, AND THAT IS LOAD BEARING.
//
// deploy.sh points the maintenance job at the tested digest after every
// release, and the module's image_tag default trails behind it, so an
// untargeted plan always wants to move that job's image backwards to a tag.
// The app does not depend on the job, so the targeted plan never carries it:
// the real fixture proves that. And if a future change to the module made it
// a dependency, the job would enter the plan as an update and this tool
// refuses it as a second resource rather than applying the rollback.
func TestMaintenanceJobDriftStaysOutsideTheTargetedPlan(t *testing.T) {
	const job = "module.control_plane.azurerm_container_app_job.maintenance"
	for _, name := range []string{"env-and-secrets-added.json", "no-changes.json"} {
		for _, c := range changes(load(t, name)) {
			if c.(map[string]any)["address"] == job {
				t.Fatalf("%s carries %s: the targeted plan has started refreshing the maintenance job, and an apply would move its image", name, job)
			}
		}
	}

	doc := load(t, "env-and-secrets-added.json")
	doc["resource_changes"] = append(changes(doc), map[string]any{
		"address": job,
		"type":    "azurerm_container_app_job",
		"mode":    "managed",
		"change":  map[string]any{"actions": []any{"update"}},
	})
	err := mustRefuse(t, doc, "other than")
	if !strings.Contains(err.Error(), job) {
		t.Fatalf("the refusal does not name the maintenance job: %v", err)
	}
}

// A data source is read on every plan and appears in resource_changes with
// actions ["read"] when it does. That is not a change to anything, and a plan
// carrying one is still a configuration change.
func TestDataSourceReadsAreNotChanges(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	doc["resource_changes"] = append(changes(doc), map[string]any{
		"address": "module.control_plane.data.azurerm_client_config.current",
		"type":    "azurerm_client_config",
		"mode":    "data",
		"change":  map[string]any{"actions": []any{"read"}},
	})
	if _, err := run(save(t, doc), app, "staging"); err != nil {
		t.Fatalf("a data source read was refused as a change to a second resource: %v", err)
	}
}

// A create means the app does not exist and the production runbook applies.
// It is also what a plan made without state looks like, once the prior state
// check is past.
func TestCreateIsRefused(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	c := change(t, doc)
	c["actions"] = []any{"create"}
	c["before"] = nil
	mustRefuse(t, doc, "CREATE")
}

// A replace of a Multiple revision app is an outage, not a configuration
// change, however the diff reads.
func TestReplaceIsRefused(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	change(t, doc)["actions"] = []any{"delete", "create"}
	mustRefuse(t, doc, "REPLACE")
}

func TestDestroyIsRefused(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	c := change(t, doc)
	c["actions"] = []any{"delete"}
	c["after"] = nil
	mustRefuse(t, doc, "DESTROY")
}

// The whole-object rule. The three named checks above are the ones with good
// messages; this is the one that holds for the attribute nobody thought of.
func TestAnyOtherAttributeChangingIsRefusedAndNamed(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	after(t, doc)["template"].([]any)[0].(map[string]any)["min_replicas"] = float64(4)
	mustRefuse(t, doc, "template[0].min_replicas")
}

// A nested change inside the container, other than env, is named by its path.
func TestContainerResourceChangeIsRefusedAndNamed(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	container(t, after(t, doc))["memory"] = "4Gi"
	mustRefuse(t, doc, "template[0].container[0].memory")
}

// A secret reference repointed at a different vault secret is a configuration
// change, reported by name, and the vault id is not printed.
func TestSecretReferenceRepointedIsAcceptedAndNamed(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	secrets := after(t, doc)["secret"].([]any)
	var target map[string]any
	for _, s := range secrets {
		if s.(map[string]any)["name"] == "admin-database-url" {
			target = s.(map[string]any)
		}
	}
	if target == nil {
		t.Fatal("fixture has no admin-database-url secret reference")
	}
	target["key_vault_secret_id"] = "https://example.vault.azure.net/secrets/repointed-for-this-test"
	v, err := run(save(t, doc), app, "staging")
	if err != nil {
		t.Fatalf("a repointed secret reference was refused: %v", err)
	}
	if !strings.Contains(v.Summary, "~admin-database-url") {
		t.Fatalf("the summary does not report the repointed reference by name:\n%s", v.Summary)
	}
	if strings.Contains(v.Summary, "repointed-for-this-test") {
		t.Fatalf("the summary prints the vault id, and it should print names only:\n%s", v.Summary)
	}
}

// Attributes Terraform only knows after apply are absent from `after` and
// marked in after_unknown. They are not drift, on either side.
func TestAttributesUnknownAfterApplyAreNotDrift(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	c := change(t, doc)
	c["after_unknown"] = map[string]any{"latest_revision_name": true, "latest_revision_fqdn": true}
	a := c["after"].(map[string]any)
	delete(a, "latest_revision_name")
	delete(a, "latest_revision_fqdn")
	if _, err := run(save(t, doc), app, "staging"); err != nil {
		t.Fatalf("attributes unknown until apply were reported as drift: %v", err)
	}
}

// But an unknown marker does not excuse a KNOWN difference elsewhere.
func TestUnknownMarkersDoNotHideAKnownChange(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	c := change(t, doc)
	c["after_unknown"] = map[string]any{"latest_revision_name": true}
	delete(c["after"].(map[string]any), "latest_revision_name")
	after(t, doc)["max_inactive_revisions"] = float64(1)
	mustRefuse(t, doc, "max_inactive_revisions")
}

// Terraform says update and nothing this tool can see differs. That is a plan
// it does not understand, and it does not apply what it does not understand.
func TestUpdateWithNoDescribableDifferenceIsRefused(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	c := change(t, doc)
	c["after"] = c["before"]
	mustRefuse(t, doc, "cannot describe")
}

// A plan built with no state backend reads the app as a create and cannot
// contain a configuration change. It is refused as such, before anything else.
func TestEmptyStatePlanIsRefused(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	doc["prior_state"] = map[string]any{"values": map[string]any{"root_module": map[string]any{}}}
	mustRefuse(t, doc, "NO PRIOR STATE")
}

// A stateless plan is not empty: Terraform records data sources in
// prior_state. tools/planguard passed exactly this once. Only managed
// resources count as state.
func TestPriorStateWithOnlyDataSourcesIsRefused(t *testing.T) {
	doc := load(t, "env-and-secrets-added.json")
	doc["prior_state"] = map[string]any{"values": map[string]any{"root_module": map[string]any{
		"child_modules": []any{map[string]any{"resources": []any{map[string]any{"mode": "data"}}}},
	}}}
	mustRefuse(t, doc, "NO PRIOR STATE")
}

func TestErroredPlanIsRefused(t *testing.T) {
	doc := load(t, "no-changes.json")
	doc["errored"] = true
	mustRefuse(t, doc, "errored")
}

// A plan that does not carry the target was not made with the target, and
// "nothing changed" would be the wrong reading of it.
func TestPlanWithoutTheTargetIsRefused(t *testing.T) {
	doc := load(t, "no-changes.json")
	var kept []any
	for _, c := range changes(doc) {
		if c.(map[string]any)["address"] != app {
			kept = append(kept, c)
		}
	}
	doc["resource_changes"] = kept
	mustRefuse(t, doc, "does not mention")
}

// "Could not read it" and "found nothing" must not share an exit code.
func TestUnparseablePlanIsAnErrorNotANoop(t *testing.T) {
	p := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(p, []byte("this is the human readable plan, not JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(p, app, "staging"); err == nil {
		t.Fatal("a plan this tool could not parse was reported as a no-op")
	}
}

func TestJSONWithoutFormatVersionIsRefused(t *testing.T) {
	p := save(t, map[string]any{"resource_changes": []any{}})
	_, err := run(p, app, "staging")
	if err == nil || !strings.Contains(err.Error(), "format_version") {
		t.Fatalf("a JSON document that is not a plan was accepted: %v", err)
	}
}

// The tool reads names and prints names. A secret's value, an env value and
// the vault ids stay out of every message, accepted or refused, because the
// message goes into a job log.
func TestValuesNeverAppearInAnyMessage(t *testing.T) {
	const canary = "canary-value-that-must-not-print"
	doc := load(t, "env-and-secrets-added.json")
	c := change(t, doc)
	for _, side := range []string{"before", "after"} {
		obj := c[side].(map[string]any)
		obj["secret"].([]any)[0].(map[string]any)["value"] = canary
		for _, e := range container(t, obj)["env"].([]any) {
			if e.(map[string]any)["name"] == "AF_SITE_ORIGIN" {
				e.(map[string]any)["value"] = canary + "-" + side
			}
		}
	}
	v, err := run(save(t, doc), app, "staging")
	if err != nil {
		t.Fatalf("setup: the plan should still be accepted: %v", err)
	}
	if strings.Contains(v.Summary, canary) {
		t.Fatalf("an accepted summary printed a value:\n%s", v.Summary)
	}

	container(t, c["after"].(map[string]any))["image"] = "ghcr.io/example/other@sha256:" + strings.Repeat("1", 64)
	_, err = run(save(t, doc), app, "staging")
	if err == nil {
		t.Fatal("setup: the image change should have been refused")
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("a refusal printed a value:\n%v", err)
	}
}

// The fixtures in this directory are the real plans described in the package
// comment and they have to keep the properties the tests above rely on.
func TestFixturesAreRealTargetedPlans(t *testing.T) {
	for _, name := range []string{"env-and-secrets-added.json", "no-changes.json"} {
		doc := load(t, name)
		if doc["format_version"] == nil {
			t.Errorf("%s has no format_version", name)
		}
		if n := len(changes(doc)); n < 20 {
			t.Errorf("%s carries %d resource changes; a targeted plan of the app carries its dependencies too, and the real one carried 29", name, n)
		}
		ac := appChange(t, doc)
		if ac["type"] != "azurerm_container_app" {
			t.Errorf("%s: the target is a %v", name, ac["type"])
		}
	}
	if actions := change(t, load(t, "env-and-secrets-added.json"))["actions"].([]any); len(actions) != 1 || actions[0] != "update" {
		t.Errorf("env-and-secrets-added.json is not an update: %v", actions)
	}
	if actions := change(t, load(t, "no-changes.json"))["actions"].([]any); len(actions) != 1 || actions[0] != "no-op" {
		t.Errorf("no-changes.json is not a no-op: %v", actions)
	}
}
