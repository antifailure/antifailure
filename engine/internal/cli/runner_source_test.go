package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A checkout with a runner at its root, and a project some levels down inside
// it, which is the shape that made `af runner install` claim no runner source
// existed while standing inside a checkout that had one. The walkthrough runs
// from examples/<name>, two levels below the root, so one level of ascent was
// not enough and the number of levels was never the real rule.
func writeCheckout(t *testing.T, depth int) (root, work string) {
	t.Helper()
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
	work = root
	for i := 0; i < depth; i++ {
		work = filepath.Join(work, "d")
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, work
}

func TestRunnerSourceFindsTheCheckoutRunnerFromASubdirectory(t *testing.T) {
	for _, depth := range []int{0, 1, 2, 4} {
		root, work := writeCheckout(t, depth)
		got, err := runnerSource(&Env{WorkDir: work}, "")
		if err != nil {
			t.Fatalf("depth %d: %v", depth, err)
		}
		want := filepath.Join(root, "runner")
		if got != want {
			t.Errorf("depth %d: got %q, want %q", depth, got, want)
		}
	}
}

// The ascent has to stop somewhere, and the check is that it stops at the
// checkout rather than at the filesystem root. A runner outside the checkout
// belongs to something else, and copying it would succeed, which is worse than
// failing: the wrong runner only shows up later as a test that will not run.
func TestRunnerSourceDoesNotClimbOutOfTheCheckout(t *testing.T) {
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
	work := filepath.Join(root, "examples", "go-api")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := runnerSource(&Env{WorkDir: work}, "")
	if err == nil {
		t.Fatalf("found %q outside the checkout, and should have found nothing", got)
	}
	if strings.Contains(err.Error(), filepath.Join(outside, "runner")) {
		t.Errorf("the runner outside the checkout was searched: %v", err)
	}
}

// An explicit --from still wins, and is still the only thing searched, so the
// ascent cannot quietly substitute a different runner for the one asked for.
func TestRunnerSourceHonoursAnExplicitFrom(t *testing.T) {
	root, work := writeCheckout(t, 2)
	other := t.TempDir()
	src := filepath.Join(other, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.ts"), []byte("//\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := runnerSource(&Env{WorkDir: work}, other)
	if err != nil {
		t.Fatal(err)
	}
	if got != other {
		t.Errorf("got %q, want the explicit %q, not the checkout at %q", got, other, root)
	}
}

// Outside a checkout the search is the pair it always was, so the error names
// the two directories that could plausibly hold a runner rather than every
// directory between here and the filesystem root.
func TestCheckoutRunnersStopsAtTwoWithoutACheckout(t *testing.T) {
	work := filepath.Join(t.TempDir(), "a", "b", "c")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	got := checkoutRunners(work)
	want := []string{
		filepath.Join(work, "runner"),
		filepath.Join(filepath.Dir(work), "runner"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
			break
		}
	}
}
