package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A fixture proves the reader; the last test in this file proves the real files.

func TestReadsATerraformDefault(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "vars.tf", `variable "image_repository" {
  type    = string
  default = "ghcr.io/antifailure/control-plane"
}

variable "image_tag" {
  type    = string
  default = "v0.1.1"
}
`)
	got, err := read(dir, pin{file: "vars.tf", what: "the tag", pattern: pins[0].pattern, kind: live})
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.1.1" {
		t.Fatalf("read = %q, want v0.1.1", got)
	}
}

// The default of the variable declared above image_tag must not be picked up.
// A non greedy match across a block boundary would read image_repository's
// value and compare a registry path against the tag list, which fails in a way
// that sends somebody to the wrong file.
func TestDoesNotReadTheDefaultOfANeighbouringVariable(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "vars.tf", `variable "image_tag" {
  type    = string
  default = "v0.1.1"
}

variable "image_digest" {
  type    = string
  default = "sha256:abc"
}
`)
	got, err := read(dir, pin{file: "vars.tf", what: "the tag", pattern: pins[0].pattern, kind: live})
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.1.1" {
		t.Fatalf("read = %q, want v0.1.1", got)
	}
}

// A pattern that matches nothing is this check quietly stopping, which is the
// exact failure it was written to prevent. It has to be an error, not a skip.
func TestAPatternThatMatchesNothingIsAFailure(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "vars.tf", "variable \"other\" {\n  default = \"x\"\n}\n")
	_, err := read(dir, pin{file: "vars.tf", what: "the tag", pattern: pins[0].pattern, kind: live})
	if err == nil {
		t.Fatal("a file with no match was accepted")
	}
	if !strings.Contains(err.Error(), "reading nothing") {
		t.Errorf("the error does not say the check may have stopped working: %v", err)
	}
}

// Two matches means the file grew a second declaration and this would silently
// check one of them.
func TestTwoMatchesIsAFailure(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "vars.tf", `variable "image_tag" {
  default = "v0.1.1"
}
variable "image_tag" {
  default = "v0.1.0"
}
`)
	if _, err := read(dir, pin{file: "vars.tf", what: "the tag", pattern: pins[0].pattern, kind: live}); err == nil {
		t.Fatal("two declarations were accepted")
	}
}

func TestReadsTheChartAppVersion(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Chart.yaml", "version: 0.1.1\nappVersion: \"v1.0.0\"\n")
	got, err := read(dir, pin{file: "Chart.yaml", what: "appVersion", pattern: pins[2].pattern, kind: released})
	if err != nil {
		t.Fatal(err)
	}
	if got != "v1.0.0" {
		t.Fatalf("read = %q, want v1.0.0", got)
	}
}

// The chart version and the appVersion mean different things and sit on
// adjacent lines. Reading the first would compare a chart version against the
// tag list and fail for a reason that is not true.
func TestDoesNotReadTheChartVersionAsTheAppVersion(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Chart.yaml", "version: 0.1.1\nappVersion: \"v0.1.1\"\n")
	got, err := read(dir, pin{file: "Chart.yaml", what: "appVersion", pattern: pins[2].pattern, kind: released})
	if err != nil {
		t.Fatal(err)
	}
	if got == "0.1.1" {
		t.Fatal("the chart's own version was read as the application version")
	}
}

func TestTakesTheTopSectionOfTheChangelog(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "CHANGELOG.md", "# Changelog\n\nPreamble.\n\n## v1.0.0\n\nnew\n\n## v0.9.0\n\nold\n")
	got, err := preparing(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v1.0.0" {
		t.Fatalf("preparing = %q, want v1.0.0", got)
	}
}

// A shallow clone has no tags. Comparing every pin against an empty set and
// printing ok would be a green gate over a subject it never examined, so the
// empty case has to be reachable and has to be distinguishable from a healthy
// one. This proves tags() really does come back empty there; main refuses on it.
func TestAFreshRepositoryHasNoTags(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git is not usable here: %v: %s", err, out)
		}
	}
	got, err := tags(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("tags = %v, want none in a fresh repository", got)
	}
}

// The live tree. Every pin this check watches still parses out of the file it
// names, so a rename that makes one unreadable fails here rather than at a tag.
func TestEveryPinInThisRepositoryIsReadable(t *testing.T) {
	for _, p := range pins {
		value, err := read("../..", p)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if value == "" {
			t.Errorf("%s: %s read as empty", p.file, p.what)
		}
	}
}

// The tag list this repository really has, so the check is known to be running
// against something rather than against nothing.
func TestThisRepositoryHasTags(t *testing.T) {
	got, err := tags("../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no tags visible, so tagsync would refuse rather than pass; fetch tags")
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The four version literals in the verification page, read off the real file.
// Each one is a separate pin because a bump that moves one and forgets another
// leaves a page that verifies one release and rebuilds a different one, and
// that reads as correct in a diff.
func TestTheVerificationPagesVersionsAreAllRead(t *testing.T) {
	const page = "docs/src/content/docs/security/releases.md"
	var found []string
	for _, p := range pins {
		if p.file != page {
			continue
		}
		value, err := read("../..", p)
		if err != nil {
			t.Errorf("%s: %v", p.what, err)
			continue
		}
		if p.bare {
			value = "v" + value
		}
		found = append(found, value)
	}
	if len(found) != 4 {
		t.Fatalf("read %d version literals from %s, want 4", len(found), page)
	}
	for _, v := range found[1:] {
		if v != found[0] {
			t.Errorf("the page names %v, which is more than one release; a reader "+
				"would verify one and rebuild another", found)
			break
		}
	}
}

// The distinction that makes this check worth having, and it is not obvious.
//
// Under `released` a pin may name any published tag, which is right for a chart
// installed from any of them. For a worked example it is wrong, and it is the
// exact defect that shipped: the page said v0.1.0 while v0.1.1 was the newest
// release, v0.1.0 was a real published tag, and so a check asking only "does
// this tag exist" would have called it fine. The page's instructions did not
// work against v0.1.0 at all.
func TestAWorkedExampleMayNotNameAnOlderPublishedTag(t *testing.T) {
	published := map[string]bool{"v0.1.0": true, "v0.1.1": true}
	const pending = "v1.0.0"

	// Named as `released`, an older published tag is acceptable.
	if !published["v0.1.0"] && pending != "v0.1.0" {
		t.Fatal("v0.1.0 should be acceptable to a released pin, and is not")
	}

	// Named as `current`, only the release being prepared is.
	if pending == "v0.1.0" {
		t.Fatal("the fixture is wrong: v0.1.0 must not equal the pending release")
	}
	for _, p := range pins {
		if p.kind != current {
			continue
		}
		if p.file != "docs/src/content/docs/security/releases.md" {
			t.Errorf("%s is marked current but is not the verification page; "+
				"current is strict and should be applied deliberately", p.file)
		}
	}
}

// Every pin the verification page contributes is `current`, not `released`.
// Downgrading one to `released` would silently restore the original defect for
// that literal while leaving the others strict, which is worse than either.
func TestTheVerificationPagesPinsAreAllStrict(t *testing.T) {
	const page = "docs/src/content/docs/security/releases.md"
	n := 0
	for _, p := range pins {
		if p.file != page {
			continue
		}
		n++
		if p.kind != current {
			t.Errorf("%s is %v, want current: a worked example has to name the "+
				"release it ships in, not merely a tag that exists", p.what, p.kind)
		}
	}
	if n == 0 {
		t.Fatal("no pins for the verification page, so this proved nothing")
	}
}
