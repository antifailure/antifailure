package runnerpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const manifest = `{
  "engines": { "node": ">=22.6" },
  "dependencies": { "playwright": "^1.49.0" }
}`

// source is a runner as it sits in a checkout: complete source, a manifest, a
// lockfile, and nothing installed. This is what a fresh clone holds and what
// tools/release/build.sh ships, which is why it is the shape everything here
// is written against.
func source(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.ts"), []byte("// runner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(`{"lockfileVersion":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// installed is what af runner install leaves behind: the same source with its
// dependencies resolved into it.
func installed(t *testing.T, dir string) string {
	t.Helper()
	source(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "playwright"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The tree the walkthrough ran on, and the reason this file exists.
func TestARunnerWithNoNodeModulesIsProvenUnableToRun(t *testing.T) {
	s := Inspect(source(t, t.TempDir()))

	if !s.Exists() {
		t.Fatal("the source is there, so the runner exists")
	}
	if !s.Blocked() {
		t.Error("a runner with no node_modules is not reported as unable to run, " +
			"which is what let af test take it and die inside node")
	}
	if !strings.Contains(s.Why(), "node_modules") {
		t.Errorf("Why() is %q, which does not name what is missing", s.Why())
	}
}

// node_modules existing is not the dependencies being installed. An
// interrupted npm install leaves the directory behind.
func TestADeclaredDependencyWithNothingUnderNodeModulesIsProvenMissing(t *testing.T) {
	dir := source(t, t.TempDir())
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := Inspect(dir)

	if !s.Blocked() {
		t.Error("an empty node_modules is not reported as unable to run")
	}
	if !strings.Contains(s.Why(), "playwright") {
		t.Errorf("Why() is %q, which does not name the package", s.Why())
	}
}

func TestAnInstalledRunnerIsNotBlocked(t *testing.T) {
	s := Inspect(installed(t, t.TempDir()))

	if s.Blocked() {
		t.Errorf("a complete runner is reported as unable to run: %s", s.Why())
	}
	if !s.Pinned {
		t.Error("the lockfile is there and Pinned says it is not")
	}
	if s.Declared != 1 {
		t.Errorf("Declared is %d, want the 1 the manifest names", s.Declared)
	}
}

// Proven unable to run and nothing could be determined are different answers,
// and a search that skipped both would pick a further runner for a reason
// nobody could see.
func TestAManifestThatCannotBeReadIsNotProofOfAnything(t *testing.T) {
	dir := source(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := Inspect(dir)

	if s.Undetermined == "" {
		t.Error("an unparseable package.json is reported as read")
	}
	if s.Blocked() {
		t.Error("a runner nothing could be said about is treated as proven broken")
	}
	if s.Why() != "" {
		t.Errorf("Why() is %q, and nothing was proven", s.Why())
	}
}

// A runner that declares no dependencies needs no node_modules, so an absent
// one proves nothing about it.
func TestARunnerThatDeclaresNoDependenciesIsNotBlockedByAnAbsentNodeModules(t *testing.T) {
	dir := t.TempDir()
	source(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"engines":{"node":">=22.6"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := Inspect(dir); s.Blocked() {
		t.Errorf("a runner declaring nothing is blocked for want of node_modules: %s", s.Why())
	}
}

// checkout builds the shape the walkthrough runs in: a checkout with a runner
// at its top and a project some directories down, plus a home directory that
// may or may not hold an installed runner.
func checkout(t *testing.T) (root, project, home string) {
	t.Helper()
	base := t.TempDir()
	home = filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	root = filepath.Join(base, "checkout")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	project = filepath.Join(root, "examples", "go-api")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, project, home
}

// The defect itself, in one test.
//
// Run 33956075704 on 2026-09-05: af runner install put a working runner in
// ~/.antifailure/runner, af runner check said the runner was ready, and the
// next command resolved the checkout's own runner/, which on a fresh clone has
// no node_modules, and died with
//
//	Cannot find package 'playwright' imported from .../runner/src/browser.ts
func TestChooseGoesPastACheckoutRunnerThatCannotRun(t *testing.T) {
	root, project, home := checkout(t)
	source(t, filepath.Join(root, "runner"))
	wanted := installed(t, filepath.Join(home, ".antifailure", "runner"))

	c := Choose(project)

	if c.Runner.Dir != wanted {
		t.Errorf("chose %q, want the installed runner at %q", c.Runner.Dir, wanted)
	}
	if len(c.PassedOver) != 1 {
		t.Fatalf("passed over %d runners, want the checkout's one: %v", len(c.PassedOver), c.PassedOver)
	}
	if c.PassedOver[0].Dir != filepath.Join(root, "runner") {
		t.Errorf("passed over %q, want the checkout runner", c.PassedOver[0].Dir)
	}
	if !strings.Contains(c.PassedOver[0].Why(), "node_modules") {
		t.Errorf("the reason is %q, which does not say why it was passed over", c.PassedOver[0].Why())
	}
}

// The nearest runner still wins when it can run, so a contributor working on
// runner/ still runs the copy they are editing.
func TestChooseTakesTheNearestRunnerWhenItCanRun(t *testing.T) {
	root, project, home := checkout(t)
	wanted := installed(t, filepath.Join(root, "runner"))
	installed(t, filepath.Join(home, ".antifailure", "runner"))

	c := Choose(project)

	if c.Runner.Dir != wanted {
		t.Errorf("chose %q, want the checkout runner at %q", c.Runner.Dir, wanted)
	}
	if len(c.PassedOver) != 0 {
		t.Errorf("passed over %v, and nothing needed passing over", c.PassedOver)
	}
}

// When the only runner anywhere cannot run, that is what the caller is told.
// Choosing it anyway is what turned an uninstalled runner into a node stack
// trace three commands later.
func TestChooseFindsNothingRunnableWhenTheOnlyRunnerIsBlocked(t *testing.T) {
	root, project, _ := checkout(t)
	source(t, filepath.Join(root, "runner"))

	c := Choose(project)

	if c.Runner.Exists() {
		t.Errorf("chose %q, and it cannot run", c.Runner.Dir)
	}
	if len(c.PassedOver) != 1 {
		t.Fatalf("passed over %d, want the one blocked runner", len(c.PassedOver))
	}
	if len(c.Looked) == 0 {
		t.Error("nothing was recorded as looked at, so a refusal can name nothing")
	}
}

// A release keeps its runner SOURCE beside the binary and expects
// af runner install to resolve it somewhere else, so that copy has no
// node_modules on every machine there has ever been. Reporting it as passed
// over would put a warning nobody can act on under every af runner check.
// Only the runners inside the checkout are worth naming.
func TestOnlyTheCheckoutsOwnRunnersAreWorthReporting(t *testing.T) {
	root, project, home := checkout(t)
	source(t, filepath.Join(root, "runner"))
	source(t, filepath.Join(home, ".antifailure", "runner"))

	c := Choose(project)

	if len(c.PassedOver) != 2 {
		t.Fatalf("passed over %d runners, want the checkout's and the installed one: %v",
			len(c.PassedOver), c.PassedOver)
	}
	reported := c.InCheckout()
	if len(reported) != 1 {
		t.Fatalf("reported %d of them, want only the one in the checkout: %v", len(reported), reported)
	}
	if reported[0].Dir != filepath.Join(root, "runner") {
		t.Errorf("reported %q, want the checkout's runner", reported[0].Dir)
	}
}

// Undetermined outranks anything else that was read.
//
// Through Inspect the two never meet, because a manifest that failed to parse
// leaves Declared at zero, so the guard in Blocked looks redundant and a
// mutation that removed it kept every other test green. Blocked is exported
// and its contract is the thing being relied on: "nothing could be determined"
// must beat a partial reading, or a later Inspect that fills a field in before
// it gives up would start proving runners broken on no evidence.
func TestNothingDeterminedOutranksAPartialReading(t *testing.T) {
	s := State{
		Dir:          "/somewhere",
		Entry:        "/somewhere/src/main.ts",
		Declared:     1,
		NoModules:    true,
		Undetermined: "package.json could not be parsed",
	}
	if s.Blocked() {
		t.Error("a runner nothing could be determined about is reported as proven broken")
	}
	if s.Why() != "" {
		t.Errorf("Why() is %q, and nothing was proven", s.Why())
	}
}
