package explore_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The taxonomy lives in two languages and one documentation page.
//
// The runner decides what a finding is and emits it; the engine names the
// kinds, sorts them and renders them; the reference page tells somebody what
// each one means. Nothing connected the three, so a kind renamed in
// TypeScript would arrive in Go as a string the switch does not recognise and
// on the page as an entry describing something the software no longer emits.
// That is the same shape as a function with no callers, and the remedy is the
// same: read the other files rather than trusting that somebody remembered.

func repoRoot() string {
	// Four levels up from engine/internal/explore to the repository root.
	return filepath.Join("..", "..", "..")
}

func readRunner(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(), "runner", "src", "explore.ts"))
	require.NoError(t, err, "the runner half of this feature is missing")
	return string(raw)
}

var quoted = regexp.MustCompile(`'([a-z_]+)'`)

func TestTheTaxonomyIsTheSameInBothLanguages(t *testing.T) {
	src := readRunner(t)
	start := strings.Index(src, "export function allKinds()")
	require.GreaterOrEqual(t, start, 0, "runner/src/explore.ts no longer exports allKinds")
	end := strings.Index(src[start:], "}")
	require.Greater(t, end, 0)

	var fromRunner []string
	for _, m := range quoted.FindAllStringSubmatch(src[start:start+end], -1) {
		fromRunner = append(fromRunner, m[1])
	}

	var fromEngine []string
	for _, k := range explore.AllKinds() {
		fromEngine = append(fromEngine, string(k))
	}

	assert.Equal(t, fromRunner, fromEngine,
		"the runner emits kinds the engine does not name, or the reverse")
}

func TestEveryKindIsDocumented(t *testing.T) {
	page := filepath.Join(repoRoot(), "docs", "src", "content", "docs", "concepts", "exploration.md")
	raw, err := os.ReadFile(page)
	require.NoError(t, err, "the page this taxonomy is published on is missing")

	for _, k := range explore.AllKinds() {
		assert.Contains(t, string(raw), string(k),
			"kind %q is emitted and not documented, so somebody who sees it has nowhere to look", k)
		assert.NotEmpty(t, k.Title(), "every kind reads as words in a report")
	}
}

// tsFields pulls the top level field names out of one TypeScript interface.
//
// Depth counted rather than matched with a pattern, because Exploration
// carries a nested object literal for its evidence and a pattern that stopped
// at the first closing brace would read half the interface.
func tsFields(t *testing.T, src, name string) []string {
	t.Helper()
	start := strings.Index(src, "export interface "+name+" {")
	require.GreaterOrEqual(t, start, 0, "runner/src/explore.ts no longer declares %s", name)
	body := src[start+strings.Index(src[start:], "{")+1:]

	var out []string
	depth := 0
	field := regexp.MustCompile(`^\s*(?:readonly\s+)?([A-Za-z_][A-Za-z0-9_]*)\??\s*:`)
	for _, line := range strings.Split(body, "\n") {
		if depth == 0 {
			if m := field.FindStringSubmatch(line); m != nil {
				out = append(out, m[1])
			}
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth < 0 {
			break
		}
	}
	return out
}

func goJSONFields(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		out = append(out, strings.Split(tag, ",")[0])
	}
	return out
}

func TestTheFindingCrossesTheBoundaryWithEveryFieldIntact(t *testing.T) {
	// The one boundary in this product where a Go struct and a TypeScript
	// interface describe the same JSON document and nothing compared them. A
	// field renamed on one side arrives on the other as a zero value, which is
	// silent: a finding with no control reads as a finding about a whole page.
	src := readRunner(t)
	for _, c := range []struct {
		name string
		typ  reflect.Type
	}{
		{"Finding", reflect.TypeOf(explore.Finding{})},
		{"Exploration", reflect.TypeOf(explore.Exploration{})},
	} {
		ts := tsFields(t, src, c.name)
		sort.Strings(ts)
		g := goJSONFields(c.typ)
		sort.Strings(g)
		assert.Equal(t, ts, g, "%s has drifted between the runner and the engine", c.name)
	}
}

func TestARunnerDocumentDecodesIntoTheEngineTypes(t *testing.T) {
	// The shape the runner actually writes, decoded the way the engine reads
	// it. Reasoning about the shape is not the same as reading one.
	const doc = `{
	  "name": "upgrade",
	  "goal": "Upgrade the workspace to the paid plan.",
	  "seed": "s1",
	  "outcome": {"verdict":"pass","cause":"explored","detail":"d","reproduction":["one"]},
	  "reached": true,
	  "steps": ["Open /"],
	  "journey": [{"kind":"goto","url":"http://x/"},{"kind":"click","control":"Plans"}],
	  "findings": [{"kind":"no_effect","url":"http://x/","control":"Save",
	    "step":2,"confidence":"high","detail":"d","fix":"f","measuredMs":0}],
	  "visited": ["http://x/"],
	  "missing": [],
	  "evidence": {"video":"v","trace":"t","screenshot":"s","console":[],"failed":[]},
	  "durationMs": 1200
	}`
	var e explore.Exploration
	require.NoError(t, json.Unmarshal([]byte(doc), &e))

	assert.Equal(t, "upgrade", e.Name)
	assert.Equal(t, "explored", e.Outcome.Cause)
	assert.True(t, e.Reached)
	require.Len(t, e.Findings, 1)
	assert.Equal(t, explore.KindNoEffect, e.Findings[0].Kind)
	assert.Equal(t, "Save", e.Findings[0].Control)
	assert.Equal(t, 2, e.Findings[0].Step)
	assert.Equal(t, "t", e.Evidence.Trace)
	assert.Equal(t, int64(1200), e.DurationMs)
	require.Len(t, e.Journey, 2)
	assert.Equal(t, `Press "Plans"`, e.Journey[1].Sentence())
}

func finding(kind explore.Kind, confidence string, step int) explore.Finding {
	return explore.Finding{Kind: kind, Confidence: confidence, Step: step, URL: "/x"}
}

func TestFindingsAreWorstFirst(t *testing.T) {
	// Somebody reading a pull request comment reads the top of the list and
	// stops, so a measured fact has to outrank an inference.
	r := explore.Report{Explorations: []explore.Exploration{{
		Findings: []explore.Finding{
			finding(explore.KindGoalUnreached, "medium", 9),
			finding(explore.KindDeadEnd, "high", 4),
			finding(explore.KindRevisit, "medium", 1),
			finding(explore.KindNoEffect, "high", 7),
		},
	}}}
	var got []string
	for _, f := range r.Findings() {
		got = append(got, string(f.Kind))
	}
	assert.Equal(t, []string{"no_effect", "dead_end", "revisit", "goal_unreached"}, got)
}

func TestAnExplorationNeverCountsAgainstTheChange(t *testing.T) {
	// The decision this whole feature rests on. An exploration wanders pages
	// nobody wrote a workflow for, so nothing declared what should have
	// happened there, and a red mark on a pull request that is fine is the
	// comment people mute.
	r := explore.Report{Explorations: []explore.Exploration{{
		Findings: []explore.Finding{finding(explore.KindNoEffect, "high", 1)},
	}}}
	assert.False(t, r.CountsAgainstTheApplication())
	assert.Contains(t, r.Headline(), "None of it counts against this change.")
}

func TestABlockedExplorationDoesNotReadAsACleanRun(t *testing.T) {
	// The same rule an invariant follows. An exploration that never opened a
	// page has not found that the application is fine.
	r := explore.Report{Explorations: []explore.Exploration{blocked()}}
	assert.Equal(t, 1, r.Blocked())
	assert.Empty(t, r.Findings())
	assert.Contains(t, r.Headline(), "nothing was explored")
	assert.NotContains(t, r.Headline(), "found nothing worth reporting")
}

func blocked() explore.Exploration {
	var e explore.Exploration
	e.Name = "upgrade"
	e.Outcome.Verdict = "blocked"
	e.Missing = []string{"Nothing was explored: the page never loaded."}
	return e
}

func discovery() explore.Exploration {
	var e explore.Exploration
	e.Name = "upgrade-a-plan"
	e.Goal = "Upgrade the workspace to the paid plan"
	e.Seed = "s1"
	e.Reached = true
	e.Journey = []explore.Move{
		{Kind: "goto", URL: "http://app.test/settings/billing?tab=plan"},
		{Kind: "click", Control: "Upgrade plan"},
		{Kind: "fill", Field: "Card number", Value: "4242424242424242"},
		{Kind: "click", Control: "Pay now"},
	}
	e.Findings = []explore.Finding{{
		Kind: explore.KindNoEffect, URL: "http://app.test/", Control: "Notify me",
		Step: 1, Confidence: "high", Detail: "d", Fix: "f",
	}}
	e.Missing = []string{`Did not press "Delete workspace"`}
	return e
}

func TestADiscoveryCompilesIntoAWorkflowTheManifestAccepts(t *testing.T) {
	// The claim this feature is for: a run that found something turns into a
	// check that runs on every pull request. Asserting the fields is not
	// enough, because the manifest is what has to accept it, and it refuses a
	// description under four words, an unknown persona and a name that is not
	// a slug. So the compiled block is put through the real parser.
	w, notes := explore.Compile(discovery(), "owner")

	assert.Equal(t, "upgrade-a-plan", w.Name)
	assert.Equal(t, "owner", w.Persona)
	assert.Equal(t, "/settings/billing?tab=plan", w.StartPath,
		"start_path comes from where the browser actually was")
	assert.Equal(t, []string{"Upgrade the workspace to the paid plan."}, w.Expect)
	assert.Contains(t, w.Description, "seed s1")
	assert.Contains(t, w.Description, `press "Upgrade plan"`)
	// The journey carries the preview environment's host, which differs on
	// every run and belongs in nobody's repository. A description pasted into a
	// manifest with a 127.0.0.1 in it reads as a mistake within a day.
	assert.NotContains(t, w.Description, "http://app.test",
		"the compiled description leaked the preview host")
	assert.Contains(t, w.Description, "open /settings/billing?tab=plan")
	require.NotNil(t, w.Budget)
	assert.Greater(t, w.Budget.Steps, len(discovery().Journey))

	body, err := yamlManifest(w)
	require.NoError(t, err)
	m, err := manifest.Parse(body, "antifailure.yaml", "")
	require.NoError(t, err, "the compiled workflow is not one the engine would accept:\n%s", body)
	require.Len(t, m.Workflows, 1)
	assert.Equal(t, "upgrade-a-plan", m.Workflows[0].Name)

	// The notes have to say what the workflow will not assert. A compiled
	// block that quietly dropped the friction would leave somebody believing
	// the check covers the thing they were told about.
	joined := strings.Join(notes, "\n")
	assert.Contains(t, joined, "unverified")
	assert.Contains(t, joined, "did nothing")
	assert.Contains(t, joined, "Notify me")
	assert.Contains(t, joined, "left unexplored")
}

func TestAnExplorationThatMissedTheGoalSaysSoInTheNotes(t *testing.T) {
	e := discovery()
	e.Reached = false
	w, notes := explore.Compile(e, "owner")
	assert.Contains(t, strings.Join(notes, "\n"), "never reached the goal")
	// The description must not claim it arrived. A compiled workflow that lies
	// about its own provenance is worse than none.
	assert.NotContains(t, w.Description, "got there by")
	assert.Contains(t, w.Description, "which walked")
}

// yamlManifest wraps a compiled workflow in the smallest manifest that parses.
//
// Marshalled from the schema type rather than written out by hand, so the YAML
// this test validates is the YAML 'af explore --emit-workflow' prints. A test
// against a hand written block would prove a block nobody ships.
func yamlManifest(w schema.Workflow) ([]byte, error) {
	return yaml.Marshal(struct {
		Version   int               `yaml:"version"`
		Name      string            `yaml:"name"`
		Services  []map[string]any  `yaml:"services"`
		Personas  []map[string]any  `yaml:"personas"`
		Workflows []schema.Workflow `yaml:"workflows"`
	}{
		Version:   schema.ManifestVersion,
		Name:      "example",
		Services:  []map[string]any{{"name": "web", "command": "./serve", "port": 3000}},
		Personas:  []map[string]any{{"name": "owner"}},
		Workflows: []schema.Workflow{w},
	})
}
