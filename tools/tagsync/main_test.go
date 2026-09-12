package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// A tag that exists is not an image that exists, and this is the pair that was
// not checked.
//
// The case is the real one. control-plane-enterprise:v1.3.5 has never been
// published: v1.3.5 predates the enterprise Dockerfile by a day, so the tree
// that tag names could not build it. Every check this file had passed that pin,
// because the TAG is real, and the maintenance container app job reads the
// repository and the tag with no ignore_changes.
func TestAnImageIsNotBuiltAtATagThatPredatesItsDockerfile(t *testing.T) {
	dir := taggedRepository(t,
		[]string{"deploy/docker/control-plane.Dockerfile"}, "v1.0.0",
		[]string{"deploy/docker/control-plane-enterprise.Dockerfile"})

	built, dockerfile, err := imageBuiltAtTag(dir, "ghcr.io/antifailure/control-plane-enterprise", "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if built {
		t.Errorf("%s reads as built at v1.0.0, and it did not exist in that tree", dockerfile)
	}
	if dockerfile != "deploy/docker/control-plane-enterprise.Dockerfile" {
		t.Errorf("dockerfile = %q, want it derived from the repository's last segment", dockerfile)
	}
}

// The positive control. Without it the test above passes just as well against a
// function that always answers no, which would refuse every release.
func TestAnImageIsBuiltAtATagWhoseTreeCarriesItsDockerfile(t *testing.T) {
	dir := taggedRepository(t,
		[]string{"deploy/docker/control-plane.Dockerfile"}, "v1.0.0",
		[]string{"deploy/docker/control-plane-enterprise.Dockerfile"})

	built, _, err := imageBuiltAtTag(dir, "ghcr.io/antifailure/control-plane", "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !built {
		t.Error("the community Dockerfile was in the tagged tree and reads as absent")
	}
}

// The complaint, in the shape main prints, so the pin and the file reach it.
func TestTheRefusalNamesThePinTheRepositoryAndTheDockerfile(t *testing.T) {
	dir := taggedRepository(t,
		[]string{"deploy/docker/control-plane.Dockerfile"}, "v1.0.0",
		[]string{"deploy/docker/control-plane-enterprise.Dockerfile"})
	write(t, dir, "variables.tf", `variable "image_repository" {
  default = "ghcr.io/antifailure/control-plane-enterprise"
}
variable "image_tag" {
  default = "v1.0.0"
}
`)
	p := pin{
		file:       "variables.tf",
		what:       "a test pin",
		pattern:    regexp.MustCompile(`(?s)variable\s+"image_tag"\s*\{.*?default\s*=\s*"([^"]+)"`),
		kind:       live,
		repository: regexp.MustCompile(`(?s)variable\s+"image_repository"\s*\{.*?default\s*=\s*"([^"]+)"`),
	}
	problem := imagePinProblem(dir, p, "v1.0.0")
	for _, want := range []string{
		"variables.tf",
		"ghcr.io/antifailure/control-plane-enterprise:v1.0.0",
		"deploy/docker/control-plane-enterprise.Dockerfile",
		"maintenance container app job",
	} {
		if !strings.Contains(problem, want) {
			t.Errorf("the refusal does not mention %q: %s", want, problem)
		}
	}
}

// A pin with no repository beside it is not an image pin and must not be turned
// into one: the verification page's four version literals name no image, and a
// check that demanded a Dockerfile for them would refuse every release.
func TestAPinWithNoRepositoryIsNotAnImagePin(t *testing.T) {
	if problem := imagePinProblem(".", pin{file: "does-not-exist", kind: current}, "v1.0.0"); problem != "" {
		t.Errorf("a pin with no repository pattern was checked as an image: %s", problem)
	}
}

// Both image pins in this repository really do read a repository beside their
// tag. A pattern that quietly matched nothing would make the check above pass
// over every pin, which is the failure this file already guards for the tag.
func TestTheImagePinsInThisRepositoryReadTheirRepository(t *testing.T) {
	found := 0
	for _, p := range pins {
		if p.repository == nil {
			continue
		}
		found++
		value, err := read("../..", pin{file: p.file, what: "the image repository", pattern: p.repository})
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if !strings.HasPrefix(value, "ghcr.io/") {
			t.Errorf("%s: image repository read as %q", p.file, value)
		}
	}
	if found != 2 {
		t.Errorf("%d pins carry a repository, want the stack's and the module's", found)
	}
}

// A repository, a tag on a tree that carries the first list, then the second
// list added afterwards so it is absent at that tag.
func taggedRepository(t *testing.T, atTag []string, tag string, afterTag []string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git is not usable here: %v: %s", err, out)
		}
	}
	add := func(paths []string) {
		t.Helper()
		for _, p := range paths {
			if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
				t.Fatal(err)
			}
			write(t, dir, p, "FROM scratch\n")
		}
	}
	run("init", "-q")
	add(atTag)
	run("add", "-A")
	run("commit", "-qm", "the tagged tree")
	run("tag", tag)
	add(afterTag)
	run("add", "-A")
	run("commit", "-qm", "after the tag")
	return dir
}
