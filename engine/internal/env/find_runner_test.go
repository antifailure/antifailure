package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// emptyHome gives a test a home directory of its own.
//
// The search legitimately looks in ~/.antifailure/runner for an installed
// runner, and on the machine of anyone who has run `af runner install` there
// is one there. Without this, these tests would pass or fail on whether the
// person running them had ever installed a runner, which is a test that
// answers a question about the developer rather than about the code.
func emptyHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// A checkout with one runner at its top and a project some directories down
// inside it, which is the shape every example in this repository has and the
// shape a customer has whenever their manifest is not at the root.
func runnerCheckout(t *testing.T, depth int) (root, project string) {
	t.Helper()
	emptyHome(t)
	root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "runner", "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.ts"), []byte("//\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project = root
	for i := 0; i < depth; i++ {
		project = filepath.Join(project, "d")
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, project
}

// The runner is found from a manifest that is not at the top of the checkout.
//
// This is the failure itself. Root is the directory holding the manifest, and
// the search looked there and exactly one directory above it, so a manifest at
// examples/django-api never reached the runner at the top of the very checkout
// it was running inside. Every example leg of the nightly failed here with
// AF-AGT-004, naming four paths and not the one the runner was in, and so did
// any customer keeping their project in a subdirectory.
//
// Depth 2 is examples/<name>, which is the corpus. Depth 4 is there because
// the number of levels was never the rule, and a fix that counted them would
// pass this at 2 and fail at 4.
func TestFindRunnerFindsTheCheckoutRunnerFromASubdirectory(t *testing.T) {
	for _, depth := range []int{0, 1, 2, 4} {
		root, project := runnerCheckout(t, depth)
		o := &Orchestrator{opts: Options{Root: project}}
		got, err := o.findRunner("")
		if err != nil {
			t.Fatalf("depth %d: %v", depth, err)
		}
		want := filepath.Join(root, "runner", "src", "main.ts")
		if got != want {
			t.Errorf("depth %d: got %q, want %q", depth, got, want)
		}
	}
}

// The ascent stops at the checkout rather than at the filesystem root.
//
// A runner outside the checkout belongs to something else. Running it would
// succeed, which is worse than refusing: the wrong runner is only visible
// later, as a workflow that will not start for a reason nobody can read.
func TestFindRunnerDoesNotClimbOutOfTheCheckout(t *testing.T) {
	emptyHome(t)
	outside := t.TempDir()
	src := filepath.Join(outside, "runner", "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.ts"), []byte("//\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(outside, "checkout")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "examples", "go-api")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	o := &Orchestrator{opts: Options{Root: project}}
	got, err := o.findRunner("")
	if err == nil {
		t.Fatalf("ran %q from outside the checkout, and should have found nothing", got)
	}
	if strings.Contains(err.Error(), filepath.Join(outside, "runner")) {
		t.Errorf("the runner outside the checkout was searched: %v", err)
	}
}

// An explicit --runner is still the only thing searched, so a wider search
// cannot quietly substitute a different runner for the one asked for.
func TestFindRunnerHonoursAnExplicitOverride(t *testing.T) {
	root, project := runnerCheckout(t, 2)
	other := filepath.Join(t.TempDir(), "main.ts")
	if err := os.WriteFile(other, []byte("//\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	o := &Orchestrator{opts: Options{Root: project}}
	got, err := o.findRunner(other)
	if err != nil {
		t.Fatal(err)
	}
	if got != other {
		t.Errorf("got %q, want the explicit %q, not the checkout at %q", got, other, root)
	}
}

// An override that is not there is refused by name.
//
// It used to fall through to the search, so `--runner /a/typo` ran whatever
// the checkout happened to hold and reported nothing about the path the
// caller actually named.
func TestFindRunnerRefusesAnOverrideThatIsNotThere(t *testing.T) {
	_, project := runnerCheckout(t, 2)
	missing := filepath.Join(t.TempDir(), "nowhere", "main.ts")

	o := &Orchestrator{opts: Options{Root: project}}
	got, err := o.findRunner(missing)
	if err == nil {
		t.Fatalf("got %q, and an override that does not exist must be refused", got)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("the refusal does not name the path that was asked for: %v", err)
	}
}
