package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The platform list is the whole reason this tool changed, so the tests are
// mostly about it: where it comes from, that it cannot silently come back
// empty, and that it still agrees with the release it is supposed to describe.

func TestReadsThePlatformsOutOfTheBuildMatrix(t *testing.T) {
	path := write(t, `
    strategy:
      matrix:
        include:
          - { os: darwin, arch: arm64 }
          - { os: linux,  arch: amd64 }
`)
	targets, err := released(path)
	if err != nil {
		t.Fatalf("released: %v", err)
	}
	if got, want := names(targets), "darwin/arm64, linux/amd64"; got != want {
		t.Errorf("released = %s, want %s", got, want)
	}
}

func TestReadsAMatrixEntryWhoseKeysAreTheOtherWayRound(t *testing.T) {
	// Reordering two keys in a YAML mapping changes nothing about the release
	// and must change nothing here. A regex that only matched one order would
	// turn that edit into a notices file with no modules in it.
	path := write(t, "          - { arch: amd64, os: linux }\n")
	targets, err := released(path)
	if err != nil {
		t.Fatalf("released: %v", err)
	}
	if got, want := names(targets), "linux/amd64"; got != want {
		t.Errorf("released = %s, want %s", got, want)
	}
}

func TestSortsAndDeduplicatesThePlatforms(t *testing.T) {
	// The order of the file must not reach the generated output, or a matrix
	// somebody reordered shows up as a diff in a legal notice.
	path := write(t, `
          - { os: linux,  arch: amd64 }
          - { os: darwin, arch: arm64 }
          - { os: linux,  arch: amd64 }
          - { os: darwin, arch: amd64 }
`)
	targets, err := released(path)
	if err != nil {
		t.Fatalf("released: %v", err)
	}
	want := "darwin/amd64, darwin/arm64, linux/amd64"
	if got := names(targets); got != want {
		t.Errorf("released = %s, want %s", got, want)
	}
}

func TestAMatrixItCannotReadIsAFailureAndNotAnEmptyList(t *testing.T) {
	// This is the one that matters. An empty list of platforms produces a
	// notices file listing nothing, and every step downstream stays green over
	// a file that attributes nobody. So the shape this regex does not
	// understand has to be loud rather than absent.
	path := write(t, `
    strategy:
      matrix:
        include:
          - operating_system: darwin
            architecture: arm64
`)
	_, err := released(path)
	if err == nil {
		t.Fatal("a matrix shape this cannot read produced no error")
	}
	if !strings.Contains(err.Error(), "attributes nobody") {
		t.Errorf("the error does not say what the damage would be: %v", err)
	}
}

func TestAMissingWorkflowIsAFailure(t *testing.T) {
	if _, err := released(filepath.Join(t.TempDir(), "gone.yml")); err == nil {
		t.Fatal("a workflow that is not there was read as no platforms")
	}
}

func TestThePlatformsAreStillTheOnesTheReleasePublishes(t *testing.T) {
	// Reading the real file, because the point of reading the workflow at all
	// is that this stays true when somebody edits the matrix. A fixture would
	// pass forever while the release grew an architecture nothing attributed.
	//
	// This opens a file outside the tools module, which Go's test cache cannot
	// see, so `just test-tools` runs with -count=1.
	targets, err := released(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("released: %v", err)
	}
	want := "darwin/amd64, darwin/arm64, linux/amd64, linux/arm64"
	if got := names(targets); got != want {
		t.Errorf("the release builds %s, and this file was written against %s.\n"+
			"Regenerate THIRD_PARTY_NOTICES.md and update this test together: a "+
			"platform the release ships and the notices do not cover is an "+
			"under attribution.", got, want)
	}
}

func TestTheModuleListIsRenderedSortedAndCounted(t *testing.T) {
	out := render(
		[]target{{os: "linux", arch: "amd64"}},
		[]module{
			{Path: "example.com/a", Version: "v1.0.0"},
			{Path: "example.com/b", Version: "v2.0.0"},
		},
	)
	if !strings.Contains(out, "## Go modules (2)") {
		t.Errorf("the heading does not count the modules:\n%s", out)
	}
	if !strings.Contains(out, "Platforms: linux/amd64.") {
		t.Errorf("the platforms the list covers are not stated:\n%s", out)
	}
	a := strings.Index(out, "example.com/a")
	b := strings.Index(out, "example.com/b")
	if a < 0 || b < 0 || a > b {
		t.Errorf("the modules are not in order:\n%s", out)
	}
}

func TestTheGeneratedProseStaysInsideItsWidth(t *testing.T) {
	// Six platforms is more than the release has today and less than it could
	// have. The line that names them is the only one here that grows.
	var many []target
	for _, o := range []string{"darwin", "linux", "windows"} {
		for _, a := range []string{"amd64", "arm64"} {
			many = append(many, target{os: o, arch: a})
		}
	}
	for _, line := range strings.Split(render(many, nil), "\n") {
		if len(line) > 74 {
			t.Errorf("a generated line is %d characters: %q", len(line), line)
		}
	}
}

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func names(targets []target) string {
	out := make([]string, len(targets))
	for i, t := range targets {
		out[i] = t.String()
	}
	return strings.Join(out, ", ")
}

func TestOutLeavesTheFileAloneUntilTheWholeOfItExists(t *testing.T) {
	// The reason -out exists. A shell redirect empties the target first, so a
	// generator that fails leaves the tree holding an empty legal notice.
	dir := t.TempDir()
	path := filepath.Join(dir, "THIRD_PARTY_NOTICES.md")
	if err := os.WriteFile(path, []byte("previous contents\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replace(path, "next contents\n"); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "next contents\n" {
		t.Errorf("file holds %q", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// 0600 is what CreateTemp hands back, and this file is committed.
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("mode is %v, want 0644", perm)
	}

	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		t.Errorf("a temporary file was left behind: %v", left)
	}
}
