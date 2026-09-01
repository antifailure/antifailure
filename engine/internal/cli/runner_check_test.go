package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// af runner check reported "ok runner" on a tree with no node_modules.
//
// It stat'ed src/main.ts and stopped there, and install.sh put the runner
// SOURCE at that path, so on every machine installed with curl | sh the check
// somebody runs specifically to find out whether the runner works said yes,
// and the failure surfaced four steps later inside af test as a node error
// about a module it could not resolve.
//
// Every test here is written against that tree.

const manifestJSON = `{
  "engines": { "node": ">=22.6" },
  "dependencies": { "playwright": "^1.49.0" }
}`

// brokenTree is the shape install.sh used to leave behind: the runner source,
// complete and readable, with nothing installed into it.
func brokenTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("// runner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifestJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func installedTree(t *testing.T) string {
	t.Helper()
	dir := brokenTree(t)
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "playwright"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func find(t *testing.T, results []runnerCheck, label string) runnerCheck {
	t.Helper()
	for _, r := range results {
		if r.label == label {
			return r
		}
	}
	t.Fatalf("no check named %q in %v", label, results)
	return runnerCheck{}
}

func TestTheTreeInstallShLeftBehindDoesNotPass(t *testing.T) {
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", t.TempDir())
	results := checkRunner(t.Context(), brokenTree(t))

	deps := find(t, results, "dependencies")
	if deps.symbol != SymbolFail {
		t.Errorf("dependencies reported %q on a tree with no node_modules, want fail", deps.symbol)
	}
	if !deps.blocker {
		t.Error("missing dependencies is not treated as a blocker, so the command exits 0")
	}
	if !strings.Contains(deps.detail, "node_modules") {
		t.Errorf("detail %q does not say what is missing", deps.detail)
	}
	if !strings.Contains(deps.remedy, "af runner install") {
		t.Errorf("remedy %q does not say what to do", deps.remedy)
	}
	// The half truth that made this survive: the source really is there, and
	// saying so is right. Saying only that was the defect.
	if r := find(t, results, "runner"); r.symbol != SymbolOK {
		t.Errorf("runner reported %q, want ok: the source is present", r.symbol)
	}
}

// node_modules existing is not the same as the dependencies being installed.
// An interrupted npm install leaves the directory behind, and a check that
// stops there is the same shape of lie as the one that stopped at main.ts.
func TestAnEmptyNodeModulesDoesNotPass(t *testing.T) {
	dir := brokenTree(t)
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := dependencyCheck(dir, runnerManifest{Dependencies: map[string]string{"playwright": "^1.49.0"}})
	if got.symbol != SymbolFail {
		t.Errorf("reported %q for an empty node_modules, want fail", got.symbol)
	}
	if !strings.Contains(got.detail, "playwright") {
		t.Errorf("detail %q does not name the missing dependency", got.detail)
	}
}

func TestAFullyInstalledTreePasses(t *testing.T) {
	browsers := t.TempDir()
	if err := os.MkdirAll(filepath.Join(browsers, "chromium-1234"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", browsers)

	results := checkRunner(t.Context(), installedTree(t))
	for _, r := range results {
		if r.label == "node" {
			continue // depends on the machine, covered by nodeCheck's own tests
		}
		if r.symbol != SymbolOK {
			t.Errorf("%s reported %q on a complete install: %s", r.label, r.symbol, r.detail)
		}
	}
}

func TestNoRunnerAtAllIsOneClearFailure(t *testing.T) {
	results := checkRunner(t.Context(), filepath.Join(t.TempDir(), "absent"))
	if len(results) != 1 {
		t.Fatalf("want one finding when there is no runner, got %d: %v", len(results), results)
	}
	if results[0].symbol != SymbolFail || !results[0].blocker {
		t.Errorf("no runner reported %q, blocker=%v", results[0].symbol, results[0].blocker)
	}
}

// A package.json that cannot be read is a question this did not answer, and
// answering ok anyway is the defect being fixed rather than a smaller version.
func TestAnUnreadableManifestIsReportedAsUnchecked(t *testing.T) {
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", t.TempDir())
	dir := brokenTree(t)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := find(t, checkRunner(t.Context(), dir), "dependencies")
	if deps.symbol != SymbolSkip {
		t.Errorf("reported %q for an unparseable package.json, want skip", deps.symbol)
	}
	if deps.blocker {
		t.Error("an unanswered question must not be a blocker; it is not a known failure")
	}
	if !strings.Contains(deps.detail, "not checked") {
		t.Errorf("detail %q does not say the question went unanswered", deps.detail)
	}
}

// The old line printed the version it found and called it ok, so a node too
// old to run the runner passed a check named after readiness.
func TestNodeIsComparedAgainstWhatTheRunnerDeclares(t *testing.T) {
	m := runnerManifest{}
	m.Engines.Node = ">=22.6"

	cases := []struct {
		found  string
		symbol string
		says   string
	}{
		{found: "v24.2.0", symbol: SymbolOK, says: ">=22.6"},
		{found: "v22.6.0", symbol: SymbolOK, says: ">=22.6"},
		{found: "v22.5.1", symbol: SymbolFail, says: ">=22.6"},
		{found: "v18.20.4", symbol: SymbolFail, says: ">=22.6"},
		{found: "", symbol: SymbolFail, says: ">=22.6"},
	}
	for _, c := range cases {
		got := nodeCheck(c.found, m)
		if got.symbol != c.symbol {
			t.Errorf("node %q reported %q, want %q (%s)", c.found, got.symbol, c.symbol, got.detail)
		}
		if !strings.Contains(got.detail, c.says) {
			t.Errorf("node %q said %q, which does not mention %q", c.found, got.detail, c.says)
		}
		if c.symbol == SymbolFail && got.remedy == "" {
			t.Errorf("node %q failed with no remedy", c.found)
		}
	}
}

// A range this cannot read is reported as unread. Treating it as satisfied
// would put the check back where it started.
func TestAnUnreadableNodeRangeIsNotTreatedAsSatisfied(t *testing.T) {
	m := runnerManifest{}
	m.Engines.Node = "^22 || ^24"
	got := nodeCheck("v24.2.0", m)
	if got.symbol != SymbolSkip {
		t.Errorf("reported %q for a range it cannot parse, want skip", got.symbol)
	}
	if !strings.Contains(got.detail, "^22 || ^24") {
		t.Errorf("detail %q does not quote the requirement it could not read", got.detail)
	}
}

func TestNodeSatisfies(t *testing.T) {
	cases := []struct {
		found, want            string
		ok, comparableExpected bool
	}{
		{"v22.6.0", ">=22.6", true, true},
		{"v22.6", ">=22.6", true, true},
		{"v22.5.9", ">=22.6", false, true},
		{"v23.0.0", ">=22.6", true, true},
		{"v22.6.1", ">=22.6", true, true},
		{"v22.60.0", ">=22.6", true, true},
		{"v24.2.0", "^24", false, false},
		{"not-a-version", ">=22.6", false, false},
		{"v22.6.0", ">=not.a.version", false, false},
	}
	for _, c := range cases {
		ok, comparable := nodeSatisfies(c.found, c.want)
		if ok != c.ok || comparable != c.comparableExpected {
			t.Errorf("nodeSatisfies(%q, %q) = (%v, %v), want (%v, %v)",
				c.found, c.want, ok, comparable, c.ok, c.comparableExpected)
		}
	}
}

// A missing browser is not a blocker, because af runner install treats a failed
// download as non fatal and af test returns unverified rather than a wrong
// answer without one. It still has to be visible: finding out from a run full
// of unverified verdicts is finding out late.
func TestAMissingBrowserWarnsWithoutBlocking(t *testing.T) {
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", filepath.Join(t.TempDir(), "never-downloaded"))
	got := browserCheck()
	if got.symbol != SymbolWarn {
		t.Errorf("reported %q for a missing browser, want warn", got.symbol)
	}
	if got.blocker {
		t.Error("a missing browser blocks the run, which is stricter than af test is")
	}
	if !strings.Contains(got.remedy, "af runner install") {
		t.Errorf("remedy %q does not say what to do", got.remedy)
	}
}

func TestAnInstalledBrowserIsNamed(t *testing.T) {
	browsers := t.TempDir()
	if err := os.MkdirAll(filepath.Join(browsers, "chromium-1234"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", browsers)
	got := browserCheck()
	if got.symbol != SymbolOK || got.detail != "chromium-1234" {
		t.Errorf("reported %q / %q, want ok / chromium-1234", got.symbol, got.detail)
	}
}

// Every failing finding carries its own remedy. The old command printed
// "Install it with: af runner install" under every failure, including a
// missing node, where installing again does nothing.
func TestEveryFailureCarriesARemedyThatFitsIt(t *testing.T) {
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", filepath.Join(t.TempDir(), "none"))
	for _, r := range checkRunner(t.Context(), brokenTree(t)) {
		if r.symbol == SymbolOK || r.symbol == SymbolSkip {
			continue
		}
		if r.remedy == "" {
			t.Errorf("%s reported %q with no remedy", r.label, r.symbol)
		}
	}
	m := runnerManifest{}
	m.Engines.Node = ">=22.6"
	if got := nodeCheck("", m).remedy; !strings.Contains(got, "nodejs.org") {
		t.Errorf("a missing node advises %q, which does not say where to get node", got)
	}
	if got := nodeCheck("", m).remedy; strings.Contains(got, "af runner install") {
		t.Errorf("a missing node advises %q, and installing the runner again does not add node", got)
	}
}
