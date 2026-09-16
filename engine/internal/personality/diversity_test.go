package personality

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func enabled(d *schema.Diversity) *schema.Diversity {
	d.Enabled = true
	return d
}

// TestCatalogueCoversTheVocabulary. The catalogue and
// schema.BuiltInPersonalityIDs must be the same set, so the manifest validator
// and the resolver cannot disagree about which ids exist. Break it by deleting
// or renaming a catalogue entry and this goes red.
func TestCatalogueCoversTheVocabulary(t *testing.T) {
	got := map[string]bool{}
	for _, d := range catalogue {
		got[d.ID] = true
	}
	if len(got) != len(catalogue) {
		t.Fatalf("catalogue has duplicate ids: %d entries, %d unique", len(catalogue), len(got))
	}
	want := map[string]bool{}
	for _, id := range schema.BuiltInPersonalityIDs {
		want[id] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalogue ids %v != schema.BuiltInPersonalityIDs %v", keys(got), keys(want))
	}
	// Every built in carries the load-bearing field: a reasoning prompt.
	for _, d := range catalogue {
		if d.ReasoningPrompt == "" {
			t.Errorf("personality %q has no reasoning prompt, so it has no effect on the model", d.ID)
		}
	}
}

// TestDisabledYieldsEmptyPlan. An absent or off block must be exactly today's
// behavior: no assignments, which the caller reads as one neutral agent.
func TestDisabledYieldsEmptyPlan(t *testing.T) {
	wf := []WorkflowRef{{Name: "checkout"}}
	if r := Resolve(nil, wf, "run-1"); len(r.Assignments) != 0 {
		t.Errorf("nil diversity produced %d assignments, want 0", len(r.Assignments))
	}
	if r := Resolve(&schema.Diversity{Enabled: false}, wf, "run-1"); len(r.Assignments) != 0 {
		t.Errorf("disabled diversity produced %d assignments, want 0", len(r.Assignments))
	}
}

// TestResolveIsDeterministic. The whole plan is a pure function of the inputs,
// so two resolutions of the same inputs are identical. This is the replay
// guarantee. Break newSeeded to depend on anything other than its string, or
// seed profileFor with a wall-clock value, and this goes red.
func TestResolveIsDeterministic(t *testing.T) {
	d := enabled(&schema.Diversity{Seed: "release-9", AgentsPerWorkflow: 4, Mix: schema.MixAggressive, Variance: schema.VarianceHigh})
	wf := []WorkflowRef{{Name: "checkout"}, {Name: "signup"}}

	a := Resolve(d, wf, "run-1")
	b := Resolve(d, wf, "run-1")
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("two resolutions of the same inputs differ:\n%#v\n%#v", a, b)
	}
}

// TestChangingTheSeedChangesThePlan. The falsification arm of determinism: if
// the seed did nothing, this would pass while TestResolveIsDeterministic also
// passed, and neither would prove the seed drives anything. Same inputs but a
// different seed must produce a different plan.
func TestChangingTheSeedChangesThePlan(t *testing.T) {
	wf := []WorkflowRef{{Name: "checkout"}, {Name: "signup"}}
	base := enabled(&schema.Diversity{Seed: "seed-A", AgentsPerWorkflow: 4})
	other := enabled(&schema.Diversity{Seed: "seed-B", AgentsPerWorkflow: 4})

	a := Resolve(base, wf, "run-1")
	b := Resolve(other, wf, "run-1")
	if reflect.DeepEqual(a.Assignments, b.Assignments) {
		t.Fatalf("changing the seed left the plan identical, so the seed drives nothing")
	}
}

// TestSeedDefaultsToRunID. With no seed, the run id is used and recorded, so a
// run with no explicit seed still replays within itself.
func TestSeedDefaultsToRunID(t *testing.T) {
	wf := []WorkflowRef{{Name: "checkout"}}
	r := Resolve(enabled(&schema.Diversity{AgentsPerWorkflow: 2}), wf, "run-xyz")
	if r.Seed != "run-xyz" {
		t.Errorf("seed = %q, want the run id run-xyz", r.Seed)
	}
	// And the run id genuinely drives the plan: a different run id, no explicit
	// seed, gives a different plan.
	other := Resolve(enabled(&schema.Diversity{AgentsPerWorkflow: 2}), wf, "run-abc")
	if reflect.DeepEqual(r.Assignments, other.Assignments) {
		t.Errorf("two different run ids with no explicit seed produced the same plan")
	}
}

// TestAgentsPerWorkflow. The count is honored, and zero means one.
func TestAgentsPerWorkflow(t *testing.T) {
	wf := []WorkflowRef{{Name: "checkout"}}
	for _, tc := range []struct{ set, want int }{{0, 1}, {1, 1}, {3, 3}, {7, 7}} {
		r := Resolve(enabled(&schema.Diversity{Seed: "s", AgentsPerWorkflow: tc.set}), wf, "run")
		if got := len(r.Assignments["checkout"]); got != tc.want {
			t.Errorf("agents_per_workflow %d produced %d agents, want %d", tc.set, got, tc.want)
		}
	}
}

// TestPinnedPersonalityWins. A workflow pin fixes the personality on every
// agent, but their profiles still differ so the pin does not collapse variance.
func TestPinnedPersonalityWins(t *testing.T) {
	wf := []WorkflowRef{{Name: "checkout", Personality: "skeptic"}}
	r := Resolve(enabled(&schema.Diversity{Seed: "s", AgentsPerWorkflow: 3, Variance: schema.VarianceHigh}), wf, "run")
	agents := r.Assignments["checkout"]
	profiles := map[uint32]bool{}
	for _, a := range agents {
		if a.Personality.ID != "skeptic" {
			t.Errorf("agent %d got personality %q, want the pinned skeptic", a.AgentIndex, a.Personality.ID)
		}
		profiles[a.Profile.Seed] = true
	}
	if len(profiles) != len(agents) {
		t.Errorf("pinned agents share a profile seed: %d distinct over %d agents", len(profiles), len(agents))
	}
}

// TestWeightSelectionRespectsThePool. A pool of a single personality draws only
// that one; a two personality pool draws both across enough agents. Break the
// cumulative-weight draw and one of these goes red.
func TestWeightSelectionRespectsThePool(t *testing.T) {
	wf := []WorkflowRef{{Name: "checkout"}}
	one := Resolve(enabled(&schema.Diversity{
		Seed: "s", AgentsPerWorkflow: 8,
		Personalities: []schema.Personality{{ID: "edge_case", Weight: 10}},
	}), wf, "run")
	for _, a := range one.Assignments["checkout"] {
		if a.Personality.ID != "edge_case" {
			t.Fatalf("single-personality pool drew %q", a.Personality.ID)
		}
	}
	two := Resolve(enabled(&schema.Diversity{
		Seed: "s", AgentsPerWorkflow: 12,
		Personalities: []schema.Personality{{ID: "explorer", Weight: 50}, {ID: "skeptic", Weight: 50}},
	}), wf, "run")
	seen := map[string]bool{}
	for _, a := range two.Assignments["checkout"] {
		seen[a.Personality.ID] = true
		if a.Personality.ID != "explorer" && a.Personality.ID != "skeptic" {
			t.Fatalf("two-personality pool drew an outsider %q", a.Personality.ID)
		}
	}
	if !seen["explorer"] || !seen["skeptic"] {
		t.Errorf("a 50/50 pool over 12 agents drew only %v", keys(seen))
	}
}

// TestProfileFoldsInTheWorkflow. The same personality at the same agent index
// in two different workflows must get different profile seeds, so a workflow
// does not inherit another's exact behavior. This is why the profile seed folds
// in the workflow name; delete that from the seed string and this goes red.
func TestProfileFoldsInTheWorkflow(t *testing.T) {
	a := profileFor("skeptic", 0, "s", "checkout", schema.MixBalanced, schema.VarianceMedium)
	b := profileFor("skeptic", 0, "s", "signup", schema.MixBalanced, schema.VarianceMedium)
	if a.Seed == b.Seed {
		t.Errorf("same personality and index in different workflows share a profile seed %d", a.Seed)
	}
}

// TestDiagnosticsCountUniqueness. buildDiversityDiagnostics port: uniqueness is
// distinct profile keys over agents, and the persona count is distinct ids.
func TestDiagnosticsCountUniqueness(t *testing.T) {
	if d := diagnostics(nil); d != (Diagnostics{}) {
		t.Errorf("empty diagnostics = %#v, want zero", d)
	}
	in := []Assignment{
		{Personality: Spec{ID: "a"}, Profile: Profile{ProfileKey: "k1", StrategyArchetype: "explorer"}},
		{Personality: Spec{ID: "a"}, Profile: Profile{ProfileKey: "k1", StrategyArchetype: "explorer"}},
		{Personality: Spec{ID: "b"}, Profile: Profile{ProfileKey: "k2", StrategyArchetype: "skeptic"}},
	}
	d := diagnostics(in)
	if d.PersonaCount != 2 {
		t.Errorf("persona count = %d, want 2", d.PersonaCount)
	}
	if d.StrategyCount != 2 {
		t.Errorf("strategy count = %d, want 2", d.StrategyCount)
	}
	if d.UniquenessScore != round3(2.0/3.0) {
		t.Errorf("uniqueness = %v, want %v", d.UniquenessScore, round3(2.0/3.0))
	}
}

// TestReasoningPromptReachesTheSpec. The personality's whole effect is its
// reasoning prompt, so a resolved assignment must carry it. A resolver that
// dropped it would produce assignments that look complete and change nothing.
func TestReasoningPromptReachesTheSpec(t *testing.T) {
	wf := []WorkflowRef{{Name: "checkout", Personality: "skeptic"}}
	r := Resolve(enabled(&schema.Diversity{Seed: "s", AgentsPerWorkflow: 1}), wf, "run")
	got := r.Assignments["checkout"][0].Personality.ReasoningPrompt
	want, _ := BuiltIn("skeptic")
	if got != want.ReasoningPrompt || got == "" {
		t.Errorf("resolved reasoning prompt does not match the catalogue")
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestResolvedMarshalsToTheShapeTheRunnerReads. The engine sends the resolved
// plan across a JSON boundary to the runner, whose ResolvedDiversity interface
// reads specific keys. A key renamed on one side and not the other is a plan
// the runner silently ignores, so a personality run would quietly become a
// neutral one. This pins the wire keys. If a json tag changes here, change
// runner/src/personality.ts to match, and this test is the reminder.
func TestResolvedMarshalsToTheShapeTheRunnerReads(t *testing.T) {
	wf := []WorkflowRef{{Name: "checkout"}}
	r := Resolve(enabled(&schema.Diversity{Seed: "s", AgentsPerWorkflow: 1}), wf, "run")
	blob, err := jsonMarshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"seed"`, `"assignments"`, `"diagnostics"`, `"uniquenessScore"`, `"strategyCount"`, `"personaCount"`,
		`"workflow"`, `"agentIndex"`, `"personality"`, `"id"`, `"name"`, `"reasoningPrompt"`,
		`"profile"`, `"personaId"`, `"strategyArchetype"`, `"riskStyle"`, `"pacingStyle"`,
		`"attentionBias"`, `"errorResponseStyle"`, `"cognitiveStyle"`, `"noveltyBias"`, `"profileKey"`,
	} {
		if !contains(blob, key) {
			t.Errorf("resolved plan is missing the wire key %s the runner reads", key)
		}
	}
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func contains(b []byte, sub string) bool { return strings.Contains(string(b), sub) }
