package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/runnerpath"
)

// af runner check answered about a runner that was not the one af test would
// run, and so it said the runner was ready on a machine where nothing could run.
//
// Run 33956075704, job 101279553597, commit b66ca628, on 2026-09-05:
//
//	af runner install       15.8
//	af runner check          0.0
//	walkthrough: af test failed after 0s: AF-AGT-003 ...
//	Cannot find package 'playwright' imported from
//	  /home/runner/work/antifailure/antifailure/runner/src/browser.ts
//
// The install put a working runner in ~/.antifailure/runner and the check read
// it and was right about it. The run resolved the CHECKOUT's runner/, which on
// a fresh clone is source with no node_modules. Every test here is written
// against that tree.

// runnerFixture builds the walkthrough's shape: a checkout with a runner at
// its top, a project underneath it, and a home directory.
type runnerFixture struct {
	root, project, home string
}

func newRunnerFixture(t *testing.T) runnerFixture {
	t.Helper()
	base := t.TempDir()
	f := runnerFixture{
		root:    filepath.Join(base, "checkout"),
		project: filepath.Join(base, "checkout", "examples", "go-api"),
		home:    filepath.Join(base, "home"),
	}
	for _, d := range []string{filepath.Join(f.root, ".git"), f.project, f.home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", f.home)
	t.Setenv("USERPROFILE", f.home)
	// Without this the browser line answers about whoever is running the
	// tests, and every case below would report differently on a laptop that
	// happens to have chromium and on one that does not.
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", filepath.Join(base, "no-browsers"))
	return f
}

// checkoutRunner puts the runner source in the checkout, which is what a fresh
// clone and tools/release/build.sh both produce: no node_modules.
func (f runnerFixture) checkoutRunner(t *testing.T) string {
	t.Helper()
	return writeRunnerSource(t, filepath.Join(f.root, "runner"))
}

// installedCheckoutRunner is a contributor's checkout with the runner's
// dependencies actually installed into it, which is the tree where the runner
// that would run is NOT the one in the home directory.
func (f runnerFixture) installedCheckoutRunner(t *testing.T) string {
	t.Helper()
	dir := f.checkoutRunner(t)
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "playwright"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// homeRunner puts a complete runner where af runner install puts one.
func (f runnerFixture) homeRunner(t *testing.T) string {
	t.Helper()
	dir := writeRunnerSource(t, filepath.Join(f.home, ".antifailure", "runner"))
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "playwright"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeRunnerSource(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		filepath.Join("src", "main.ts"): "// runner\n",
		"package.json":                  manifestJSON,
		"package-lock.json":             `{"lockfileVersion":3}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runCheck runs the real command from dir and returns its report and its error.
func runCheck(t *testing.T, dir string) (report RunnerCheckJSON, out string, err error) {
	t.Helper()
	var buf bytes.Buffer
	e := &Env{
		Out:     NewOutput(&buf, &buf),
		WorkDir: dir,
		Getenv:  func(string) string { return "" },
	}
	e.Out.Format = FormatJSON
	cmd := newRunnerCheckCommand(e)
	cmd.SetContext(t.Context())
	err = cmd.RunE(cmd, nil)
	out = buf.String()
	if jsonErr := json.Unmarshal([]byte(out), &report); jsonErr != nil {
		t.Fatalf("the command did not print a document this can read: %v\n%s", jsonErr, out)
	}
	return report, out, err
}

func item(t *testing.T, r RunnerCheckJSON, name string) RunnerCheckItemJSON {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %v", name, r.Checks)
	return RunnerCheckItemJSON{}
}

// Can it say no. Point it at a runner with playwright absent from
// node_modules, with no other runner anywhere, and it has to fail and name the
// package. This is the tree that made af test die, and the command that exists
// to find that tree used to exit 0 on it.
func TestTheCheckFailsOnARunnerMissingADeclaredPackage(t *testing.T) {
	f := newRunnerFixture(t)
	dir := f.checkoutRunner(t)
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}

	report, out, err := runCheck(t, f.project)

	if err == nil {
		t.Error("af runner check exited 0 on a runner that cannot resolve playwright")
	}
	if report.Complete {
		t.Error("the report says the runner is complete, and it cannot run")
	}
	if report.Path != dir {
		t.Errorf("it reported on %q, and af test would run %q", report.Path, dir)
	}
	deps := item(t, report, "dependencies")
	if deps.Result != SymbolFail {
		t.Errorf("dependencies reported %q, want fail:\n%s", deps.Result, out)
	}
	if !strings.Contains(deps.Detail, "playwright") {
		t.Errorf("the failure is %q, which does not name the package", deps.Detail)
	}
}

// Can it say yes. A complete runner passes, or the command is useless.
func TestTheCheckPassesOnACompleteRunner(t *testing.T) {
	f := newRunnerFixture(t)
	home := f.homeRunner(t)

	report, out, err := runCheck(t, f.project)

	if err != nil {
		t.Errorf("af runner check refused a complete runner: %v\n%s", err, out)
	}
	if !report.Complete {
		t.Errorf("the report says the runner is not complete:\n%s", out)
	}
	if report.Path != home {
		t.Errorf("it reported on %q, want the installed runner at %q", report.Path, home)
	}
	if deps := item(t, report, "dependencies"); deps.Result != SymbolOK {
		t.Errorf("dependencies reported %q on a complete tree: %s", deps.Result, deps.Detail)
	}
}

// A missing browser and a missing dependency are DIFFERENT answers.
//
// af runner install treats a failed browser download as non fatal on purpose,
// and af test comes back unverified rather than guessing, so the quickstart
// says the runner is usable the moment a browser arrives. Collapsing the two
// into one "not ready" would make the command refuse a runner that works.
func TestAMissingBrowserIsADifferentAnswerFromAMissingDependency(t *testing.T) {
	f := newRunnerFixture(t)
	f.homeRunner(t)

	report, out, err := runCheck(t, f.project)

	if err != nil {
		t.Errorf("a missing browser blocked the command: %v\n%s", err, out)
	}
	if !report.Complete {
		t.Error("a missing browser made the runner incomplete, which is stricter than af test is")
	}
	browser := item(t, report, "browser")
	if browser.Result != SymbolWarn {
		t.Errorf("browser reported %q, want warn", browser.Result)
	}
	if deps := item(t, report, "dependencies"); deps.Result != SymbolOK {
		t.Errorf("dependencies reported %q, and only the browser is missing", deps.Result)
	}
}

// Anything it cannot determine is reported as not checked rather than as ok.
// The quickstart promises exactly that sentence.
func TestWhatCannotBeDeterminedIsReportedAsNotChecked(t *testing.T) {
	f := newRunnerFixture(t)
	dir := f.homeRunner(t)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, out, _ := runCheck(t, f.project)

	deps := item(t, report, "dependencies")
	if deps.Result != SymbolSkip {
		t.Errorf("dependencies reported %q for an unreadable manifest, want skip:\n%s", deps.Result, out)
	}
	if !strings.Contains(deps.Detail, "not checked") {
		t.Errorf("the detail is %q, which does not say the question went unanswered", deps.Detail)
	}
	// The node line was the quieter half of the same lie: with no readable
	// manifest there is no range to compare against, and it printed the
	// version it found and called that ok.
	node := item(t, report, "node")
	if node.Result == SymbolOK {
		t.Errorf("node reported ok against a requirement nobody could read: %s", node.Detail)
	}
}

// The whole defect, end to end through the real command: the check reports on
// the runner af test would use, not on the one af runner install wrote.
func TestTheCheckReportsOnTheRunnerTheRunWouldUse(t *testing.T) {
	f := newRunnerFixture(t)
	stale := f.checkoutRunner(t)
	home := f.homeRunner(t)

	report, out, err := runCheck(t, f.project)

	if err != nil {
		t.Errorf("af runner check refused: %v\n%s", err, out)
	}
	if report.Path != home {
		t.Errorf("reported on %q, and a run from %q uses %q", report.Path, f.project, home)
	}
	// And it says what it went past, because reporting the truth about a
	// directory the reader did not mean is the whole failure.
	passed := item(t, report, "passed over")
	if !strings.Contains(passed.Detail, stale) {
		t.Errorf("the report does not name the runner it went past: %q", passed.Detail)
	}
	if !strings.Contains(passed.Detail, "node_modules") {
		t.Errorf("the report does not say why it went past it: %q", passed.Detail)
	}
}

// One resolution, not two that agree today.
//
// The check and the run each had their own answer to "which runner", and the
// two drifted the moment the run learned to look at the top of the checkout.
// This holds the check to runnerpath.Choose, which is what the run uses.
func TestTheCheckResolvesTheRunnerRunnerpathChose(t *testing.T) {
	f := newRunnerFixture(t)
	f.checkoutRunner(t)
	f.homeRunner(t)

	target, _, err := runnerToCheck(f.project)
	if err != nil {
		t.Fatal(err)
	}
	if want := runnerpath.Choose(f.project).Runner.Dir; target != want {
		t.Errorf("the check reports on %q and a run would use %q", target, want)
	}
}

// The chosen runner is not always the one in the home directory, and a check
// that reported the home one would be right by accident whenever they agree.
//
// A contributor with runner/ installed in their checkout runs THAT copy, and
// the command has to say so, or somebody debugging their own runner edits is
// reading a report about a different tree.
func TestTheCheckReportsOnTheCheckoutRunnerWhenThatIsTheOneThatWouldRun(t *testing.T) {
	f := newRunnerFixture(t)
	mine := f.installedCheckoutRunner(t)
	home := f.homeRunner(t)

	report, out, err := runCheck(t, f.project)

	if err != nil {
		t.Errorf("af runner check refused a complete checkout runner: %v\n%s", err, out)
	}
	if report.Path == home {
		t.Fatal("it reported on the installed home runner, and a run here uses the checkout's")
	}
	if report.Path != mine {
		t.Errorf("it reported on %q, and a run from %q uses %q", report.Path, f.project, mine)
	}
	for _, c := range report.Checks {
		if c.Name == "passed over" {
			t.Errorf("nothing needed passing over and it reported %q", c.Detail)
		}
	}
}

// The runner it reports on is not also listed as one it went past.
//
// When nothing anywhere can run, the command reports on the nearest runner
// that exists so the reader learns what is wrong with the tree they have. That
// tree is in the passed over list too, and printing it twice, once as the
// subject and once as something skipped, describes a machine that does not
// exist.
func TestTheRunnerItReportsOnIsNotAlsoListedAsPassedOver(t *testing.T) {
	f := newRunnerFixture(t)
	f.checkoutRunner(t)

	report, _, err := runCheck(t, f.project)
	if err == nil {
		t.Fatal("af runner check exited 0 with no runner that can run anywhere")
	}
	for _, c := range report.Checks {
		if c.Name == "passed over" && strings.Contains(c.Detail, report.Path) {
			t.Errorf("%s is both the runner it reported on and one it says it went past", report.Path)
		}
	}
}

// A machine that has installed af and not yet run af runner install is the
// commonest first run there is, and its answer has to stay the friendly one.
//
// The check reports on the runner a run would use, and on such a machine that
// is the release's own SOURCE beside the binary, which has no node_modules by
// design and always will. Reporting on it would answer a first run with
// "node_modules is missing" under a path nobody has heard of. The true and
// useful answer is that the runner is not installed yet, so this falls through
// to the home directory and says so.
func TestAMachineWithNoRunnerInItsCheckoutIsToldTheRunnerIsNotInstalled(t *testing.T) {
	f := newRunnerFixture(t)

	report, out, err := runCheck(t, f.project)

	if err == nil {
		t.Fatal("af runner check exited 0 with no runner installed anywhere")
	}
	want := filepath.Join(f.home, ".antifailure", "runner")
	if report.Path != want {
		t.Errorf("it reported on %q, want the place af runner install writes to, %q",
			report.Path, want)
	}
	r := item(t, report, "runner")
	if !strings.Contains(r.Detail, "no runner at") {
		t.Errorf("a first run is told %q rather than that the runner is not installed", r.Detail)
	}
	if !strings.Contains(r.Remedy, "af runner install") {
		t.Errorf("the remedy is %q, and the installer already printed af runner install:\n%s",
			r.Remedy, out)
	}
}
