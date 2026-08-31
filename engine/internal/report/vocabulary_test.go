package report_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/report"
)

// One vocabulary, five declarations, three languages, and nothing that made
// them agree.
//
// A workflow's verdict is decided by the runner in TypeScript, rolled up by the
// engine in Go, printed by the engine again in the terminal, and stored by the
// control plane in a Postgres enum. Every one of those was a separate list of
// the same five words written out by hand. The failure that was live: Go's
// switches had no shared idea of the set, so a word from a runner one version
// ahead fell through Verdict to "pass" and through the terminal to
// "unverified". The run exited zero, the comment said every workflow passed,
// and the table under that line said unverified for the workflow that had just
// produced the unknown word. The control plane, meanwhile, would have refused
// the same word outright: verdict_value is an enum and an insert outside it is
// a constraint violation. Three copies, three different answers, one input.
//
// These read the other declarations out of their own source rather than
// restating them, because a copy of a list is one more thing to keep in step
// and that is the problem being solved. This is the same shape as
// internal/controlplane/vocabulary_test.go, which exists for the same reason.

const (
	runnerVerdictPath = "../../../runner/src/verdict.ts"
	dbSchemaPath      = "../../../web/packages/db/src/schema.ts"
)

var (
	// export type Verdict = 'pass' | 'fail' | ... ;
	verdictUnion = regexp.MustCompile(`export type Verdict =([^;]*);`)
	// export const verdictValue = pgEnum('verdict_value', [ ... ])
	verdictEnum  = regexp.MustCompile(`(?s)pgEnum\('verdict_value',\s*\[(.*?)\]`)
	singleQuoted = regexp.MustCompile(`'([^']+)'`)
)

// wordsIn pulls the single quoted strings out of one declaration.
func wordsIn(t *testing.T, path string, block *regexp.Regexp, what string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	m := block.FindSubmatch(b)
	if m == nil {
		t.Fatalf("%s no longer declares %s in a form this test can read. "+
			"Fix the test rather than deleting it: the drift it guards produced a run "+
			"that exited zero while reporting a workflow it could not read.", path, what)
	}
	var out []string
	for _, q := range singleQuoted.FindAllSubmatch(m[1], -1) {
		out = append(out, string(q[1]))
	}
	if len(out) == 0 {
		t.Fatalf("%s parsed as empty, which would make every assertion below vacuous", what)
	}
	return out
}

func sorted(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

func TestTheRunnerAndTheEngineNameTheSameVerdicts(t *testing.T) {
	fromRunner := wordsIn(t, runnerVerdictPath, verdictUnion, "the Verdict union")
	if !slices.Equal(sorted(fromRunner), sorted(report.Verdicts)) {
		t.Errorf("the runner produces %v and the engine reads %v.\n"+
			"A word only the runner knows is rolled up as blocked, so a real failure "+
			"would be reported as our own gap; a word only the engine knows is a branch "+
			"nothing can reach. Change both lists together.",
			sorted(fromRunner), sorted(report.Verdicts))
	}
}

func TestTheControlPlaneStoresEveryVerdictTheRunnerCanProduce(t *testing.T) {
	fromRunner := wordsIn(t, runnerVerdictPath, verdictUnion, "the Verdict union")
	stored := wordsIn(t, dbSchemaPath, verdictEnum, "the verdict_value enum")
	for _, v := range fromRunner {
		if !slices.Contains(stored, v) {
			t.Errorf("the runner can produce %q and verdict_value in %s does not hold it. "+
				"That column is a Postgres enum, so the insert is a constraint violation "+
				"rather than a wrong row: the run happened and the control plane has no "+
				"record of what it found.", v, dbSchemaPath)
		}
	}
	for _, v := range stored {
		if !slices.Contains(fromRunner, v) {
			t.Errorf("verdict_value holds %q and nothing produces it. An enum member with "+
				"no producer is a filter in the dashboard that is always empty.", v)
		}
	}
}

// A guard on the guards. If either parse silently returned the wrong thing,
// the tests above would pass while comparing nothing, which is the failure this
// whole file exists to prevent.
func TestTheParsedVocabulariesLookLikeVerdicts(t *testing.T) {
	for _, c := range []struct {
		what  string
		words []string
	}{
		{"the runner's union", wordsIn(t, runnerVerdictPath, verdictUnion, "the Verdict union")},
		{"the control plane's enum", wordsIn(t, dbSchemaPath, verdictEnum, "the verdict_value enum")},
	} {
		if len(c.words) != 5 {
			t.Fatalf("%s parsed to %d words (%v); there are five verdicts", c.what, len(c.words), c.words)
		}
		if !slices.Contains(c.words, "pass") || !slices.Contains(c.words, "fail") {
			t.Fatalf("%s parsed to %v, which does not contain pass and fail, so it is not the list",
				c.what, c.words)
		}
		for _, w := range c.words {
			if strings.ContainsAny(w, " |.") {
				t.Fatalf("%s parsed %q, so the regexp is picking up the wrong quotes", c.what, w)
			}
		}
	}
}

// The rollup must not turn a word it cannot read into a green run.
//
// This is the behaviour the vocabulary tests above protect. It is asserted
// separately because the vocabulary tests only fire when somebody edits a list,
// and this one fires if anybody reintroduces the fall through.
func TestAnUnreadableVerdictDoesNotPass(t *testing.T) {
	run := report.Run{Workflows: []report.Workflow{
		{Name: "checkout", Verdict: "pass"},
		{Name: "signup", Verdict: "regressed"},
	}}
	if got := run.Verdict(); got != "blocked" {
		t.Errorf("a run holding a verdict this engine cannot read came out %q, want blocked. "+
			"Anything but blocked here means the check went green on a result nobody read.", got)
	}
	if head := run.Headline(); strings.Contains(head, "passed") {
		t.Errorf("the headline says %q for a run holding an unreadable verdict", head)
	}
	if md := run.Markdown(); strings.Contains(md, "| `signup` | passed |") {
		t.Error("the comment table renders an unreadable verdict as passed")
	}
}
