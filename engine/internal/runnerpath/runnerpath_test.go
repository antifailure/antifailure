package runnerpath

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func emptyHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// The two callers search the same places, and differ in exactly one.
//
// This is the guard against the defect that made this package. The search was
// written twice, `af runner install` learned to walk up to the top of the
// checkout, `af ci` did not, and nothing anywhere could tell that the two had
// stopped agreeing. Now there is one list, and this fails if a candidate is
// ever added to one caller and not the other, or if the one deliberate
// difference stops being the only one.
func TestTheRunAndInstallSearchesDifferOnlyByTheInstallTarget(t *testing.T) {
	emptyHome(t)
	dir := t.TempDir()

	home, err := Home()
	if err != nil {
		t.Fatal(err)
	}
	run, install := ToRun(dir), ToInstallFrom(dir)

	if !slices.Contains(run, home) {
		t.Errorf("a run cannot see an installed runner: %v does not contain %q", run, home)
	}
	if slices.Contains(install, home) {
		t.Errorf("install offers its own target as a source, so it can copy a stale "+
			"runner onto itself: %v contains %q", install, home)
	}

	withoutHome := slices.DeleteFunc(slices.Clone(run), func(c string) bool { return c == home })
	if !slices.Equal(withoutHome, install) {
		t.Errorf("the two searches have drifted apart.\n  run, without the install target: %v\n  install:                         %v",
			withoutHome, install)
	}
}

// The ascent reaches the top of the checkout and stops there.
//
// Outside a checkout there is no top to stop at, so only the two nearest
// directories are offered rather than every directory up to the filesystem
// root, which would bury the two that matter and could find an unrelated
// runner belonging to something else.
func TestTheCheckoutSearchAscendsToTheTopAndNoFurther(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "examples", "django-api")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	got := inCheckout(deep)
	want := []string{
		filepath.Join(deep, "runner"),
		filepath.Join(root, "examples", "runner"),
		filepath.Join(root, "runner"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	loose := filepath.Join(t.TempDir(), "a", "b", "c")
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	got = inCheckout(loose)
	want = []string{
		filepath.Join(loose, "runner"),
		filepath.Join(filepath.Dir(loose), "runner"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("outside a checkout: got %v, want %v", got, want)
	}
}
