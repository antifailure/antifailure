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
		if unbreakableValue(line) {
			continue
		}
		if len(line) > 74 {
			t.Errorf("a generated line is %d characters: %q", len(line), line)
		}
	}
}

// unbreakableValue reports whether a line carries a single value with no wrap
// point, which the width rule does not apply to. The rule is about prose the
// reader has to scan, not about a value that would be wrong if it were
// shortened.
//
// Removing the list marker is the whole of this function, and it is the repair
// rather than a widening. A licence URL is one unbreakable token, the
// generator writes it as a list item because that is what it is, and the four
// characters of "  - " put a space on the line that made every such value look
// like prose. So the exemption could not see the thing it was written to
// cover: a sha256 digest is 71 characters of exactly that value and was
// exempt, while the same digest wearing a bullet was not. Cloud Spanner
// Emulator's licence URL is 81 characters and was the first to reach the
// limit, but any licence URL over 70 would have.
//
// Only the marker is removed, which is what keeps a bulleted SENTENCE
// refusable: its words still have spaces between them afterwards, so it is
// prose and the rule still applies to it.
func unbreakableValue(line string) bool {
	content := strings.TrimSpace(line)
	content = strings.TrimSpace(strings.TrimPrefix(content, "-"))
	return !strings.Contains(content, " ")
}

// TestTheWidthExemptionCoversValuesAndNotSentences is the direction nobody
// checks. Widening an exemption until the failing case passes is easy, and an
// exemption that cannot refuse a long bulleted sentence is not an exemption
// for unbreakable values, it is a hole shaped like a bullet. Both halves are
// asserted here so the repair cannot quietly become the hole later.
//
// Every case is longer than the width, so the exemption is the only thing
// deciding the outcome and a case that stopped reaching the limit would be
// proving nothing.
func TestTheWidthExemptionCoversValuesAndNotSentences(t *testing.T) {
	const (
		url      = "https://github.com/GoogleCloudPlatform/cloud-spanner-emulator/blob/master/LICENSE"
		sentence = "The instance metadata endpoint, which hands out the node's own cloud credentials."
		image    = "gcr.io/cloud-spanner-emulator/emulator@sha256:" +
			"4987860c9f8ecf1fffbbcdac115cb88cb9d1a42bd966c235a9ab843aea34fbd1"
	)
	for _, c := range []struct {
		name   string
		line   string
		exempt bool
	}{
		{"a bulleted URL is a value with no wrap point", "  - " + url, true},
		{"a bulleted sentence is prose and stays refusable", "  - " + sentence, false},
		{"an unbulleted value was exempt before this repair and still is", image, true},
		{"an unbulleted sentence was refusable before this repair and still is", sentence, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if len(c.line) <= 74 {
				t.Fatalf("the case is %d characters, does not reach the width, and proves nothing", len(c.line))
			}
			if got := unbreakableValue(c.line); got != c.exempt {
				t.Errorf("unbreakableValue(%q) = %v, want %v", c.line, got, c.exempt)
			}
		})
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
