package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These assert on the judgement about a changelog rather than on the exit code
// around it, which is the same reason ldcheck's tests are shaped this way.

func TestSplitsSectionsAtTheHeadings(t *testing.T) {
	got, err := parse(`# Changelog

Preamble that belongs to no release.

## v1.0.0

The first stable release.

## v0.9.0

An older one.
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d sections, want 2: %v", len(got), keys(got))
	}
	if !strings.Contains(got["v1.0.0"].published, "first stable release") {
		t.Errorf("v1.0.0 body is %q", got["v1.0.0"].published)
	}
	if strings.Contains(got["v1.0.0"].published, "An older one") {
		t.Error("the v1.0.0 section swallowed the section below it")
	}
	if strings.Contains(got["v0.9.0"].published, "Preamble") {
		t.Error("text above the first heading was attributed to a release")
	}
}

// The failure this whole command exists to make loud. A heading with nothing
// under it reads as finished in the diff and publishes a release that says
// nothing, so it has to fail differently from a section that is simply absent.
func TestAnEmptySectionIsFound(t *testing.T) {
	got, err := parse(`# Changelog

## v1.0.0

## v0.9.0

An older one.
`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["v1.0.0"]; !ok {
		t.Fatal("the empty section was not parsed at all, so nothing would report it")
	}
	if strings.TrimSpace(got["v1.0.0"].published) != "" {
		t.Fatalf("v1.0.0 is %q, want empty", got["v1.0.0"].published)
	}
}

// A section holding only whitespace is the same failure wearing a blank line.
func TestASectionOfWhitespaceCountsAsEmpty(t *testing.T) {
	got, err := parse("# Changelog\n\n## v1.0.0\n\n   \n\t\n\n## v0.9.0\n\nreal\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got["v1.0.0"].published) != "" {
		t.Fatalf("v1.0.0 is %q, want empty after trimming", got["v1.0.0"].published)
	}
}

// A fenced block can contain anything, including a line that looks exactly like
// a heading. Splitting on one would cut a release's notes in half at a place
// nobody meant and attribute the rest to a version that does not exist.
func TestAHeadingInsideAFenceIsNotASection(t *testing.T) {
	got, err := parse("# Changelog\n\n## v1.0.0\n\nUpgrade with:\n\n```md\n## v0.9.0\n```\n\nAnd that is all.\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d sections, want 1: %v", len(got), keys(got))
	}
	if !strings.Contains(got["v1.0.0"].published, "And that is all") {
		t.Error("the section was cut short at a heading inside a code fence")
	}
}

// Two headings for one version mean somebody wrote the second not knowing the
// first was there. Merging them would publish half of what each person wrote.
func TestRefusesTwoSectionsForOneVersion(t *testing.T) {
	_, err := parse("## v1.0.0\n\nfirst\n\n## v1.0.0\n\nsecond\n")
	if err == nil {
		t.Fatal("a repeated heading was accepted")
	}
	if !strings.Contains(err.Error(), "v1.0.0") {
		t.Errorf("the error does not name the version: %v", err)
	}
}

// Prose that merely mentions a version is not a section.
func TestDoesNotSplitOnAHeadingWithTrailingProse(t *testing.T) {
	got, err := parse("## v1.0.0\n\nreal\n\n## v0.9.0 and everything before it\n\nnot a section\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d sections, want 1: %v", len(got), keys(got))
	}
}

// The preamble is the reason this is a command and not three lines of shell.
// action-gh-release prefers body_path and falls back to body only when the path
// cannot be read, so a notes file that reads successfully drops the body block
// silently with the step still green. If this ever stops carrying the
// verify-blob command, every release note we publish tells somebody to trust a
// signature and never shows them how to check it.
func TestThePreambleCarriesTheVerificationCommand(t *testing.T) {
	got := preamble("antifailure/antifailure", "refs/tags/v1.0.0")
	for _, want := range []string{
		"cosign verify-blob",
		"--bundle checksums.txt.sigstore.json",
		"--certificate-identity",
		"https://github.com/antifailure/antifailure/.github/workflows/release.yml@refs/tags/v1.0.0",
		"--certificate-oidc-issuer",
		"https://token.actions.githubusercontent.com",
		"sha256sum --check --ignore-missing checksums.txt",
		"sbom.spdx.json",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the preamble does not carry %q", want)
		}
	}
}

// The identity has to be this repository at this tag. Without it a reader is
// verifying that somebody signed the release rather than that we did, and the
// published instruction would be worth nothing while looking exactly the same.
func TestThePreambleBindsTheIdentityToTheTagBeingReleased(t *testing.T) {
	got := preamble("antifailure/antifailure", "refs/tags/v9.9.9")
	if strings.Contains(got, "refs/tags/v1.0.0") {
		t.Error("the identity is pinned to a version other than the one being released")
	}
	if !strings.Contains(got, "release.yml@refs/tags/v9.9.9") {
		t.Error("the identity does not name the tag being released")
	}
}

// The changelog in this repository parses with the command that publishes it.
// A fixture proves the parser; this proves the file.
func TestTheRealChangelogParsesAndHasNoEmptySection(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("../..", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := parse(string(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no sections were found in the real changelog")
	}
	for tag, release := range got {
		if strings.TrimSpace(release.published) == "" {
			t.Errorf("%s would publish a heading and nothing under it", tag)
		}
	}
}

func keys(m map[string]section) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The detail markers, and what has to stay true about them.
//
// Everything below exists because the omitted region is the one thing on this
// path that can quietly remove content from a published release. The gate has
// to fail on every shape of that, not only on the shape somebody remembered.

// The pointer stands where the detail stood, not at the end. A release whose
// omitted region sits between two published parts would otherwise print its
// link under whatever heading happened to come last.
func TestTheOmittedRegionIsReplacedInPlace(t *testing.T) {
	got, err := parse("## v1.0.0\n\nWhat it means.\n\n<!-- relnotes:omit -->\n### Added\n\nA thing.\n<!-- relnotes:end -->\n\n### Security\n\nA fix.\n")
	if err != nil {
		t.Fatal(err)
	}
	release := got["v1.0.0"]
	if !release.omitted {
		t.Fatal("the section is not marked as having omitted anything")
	}
	if strings.Contains(release.notes, "A thing") {
		t.Error("the omitted detail is still in the published notes")
	}
	for _, want := range []string{"What it means", "https://antifailure.dev/changelog", "A fix"} {
		if !strings.Contains(release.notes, want) {
			t.Errorf("the notes do not carry %q", want)
		}
	}
	if strings.Index(release.notes, "antifailure.dev/changelog") > strings.Index(release.notes, "A fix") {
		t.Error("the pointer was appended at the end rather than left where the detail was")
	}
	if strings.Contains(release.published, "antifailure.dev/changelog") {
		t.Error("the emptiness check would count the pointer as published content")
	}
}

// A section with no markers publishes every byte of itself, which is what
// every section in this file did before the markers existed.
func TestASectionWithNoMarkersIsUnchanged(t *testing.T) {
	got, err := parse("## v1.0.0\n\nAll of it.\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["v1.0.0"].omitted {
		t.Error("a section with no markers claims to have omitted something")
	}
	if got["v1.0.0"].notes != got["v1.0.0"].published {
		t.Errorf("notes %q and published %q differ with no markers in the section",
			got["v1.0.0"].notes, got["v1.0.0"].published)
	}
}

// Unclosed is the dangerous one: it silently drops the rest of the section,
// including a Security heading, and the release still publishes.
func TestRefusesAnUnclosedRegion(t *testing.T) {
	_, err := parse("## v1.0.0\n\nKept.\n\n<!-- relnotes:omit -->\n### Added\n\nA thing.\n")
	if err == nil {
		t.Fatal("an unclosed region was accepted")
	}
	if !strings.Contains(err.Error(), "never closed") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
}

func TestRefusesACloseWithNothingOpen(t *testing.T) {
	_, err := parse("## v1.0.0\n\nKept.\n\n<!-- relnotes:end -->\n")
	if err == nil {
		t.Fatal("a close with nothing open was accepted")
	}
}

// Two regions would print the same link twice, under two headings, which reads
// as a page that could not decide where it went.
func TestRefusesASecondRegion(t *testing.T) {
	_, err := parse("## v1.0.0\n\nKept.\n\n<!-- relnotes:omit -->\nOne.\n<!-- relnotes:end -->\n\nAlso kept.\n\n<!-- relnotes:omit -->\nTwo.\n<!-- relnotes:end -->\n")
	if err == nil {
		t.Fatal("a second region was accepted")
	}
	if !strings.Contains(err.Error(), "second") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
}

// A pair with nothing between them publishes a link to detail that is not
// there, which is a promise the changelog cannot keep.
func TestRefusesAnEmptyRegion(t *testing.T) {
	_, err := parse("## v1.0.0\n\nKept.\n\n<!-- relnotes:omit -->\n\n<!-- relnotes:end -->\n")
	if err == nil {
		t.Fatal("an empty region was accepted")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
}

// The marker is content inside a fence, exactly as a heading is. Documenting
// the convention in the changelog must not change what the changelog publishes.
func TestAMarkerInsideAFenceIsNotAMarker(t *testing.T) {
	got, err := parse("## v1.0.0\n\nKept.\n\n```md\n<!-- relnotes:omit -->\n```\n\nAlso kept.\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["v1.0.0"].omitted {
		t.Error("a marker inside a code fence opened a region")
	}
	if !strings.Contains(got["v1.0.0"].published, "Also kept") {
		t.Error("the section was cut short at a marker inside a code fence")
	}
}

// The whole point of reading `published` rather than the body. A section that
// omits all of itself is not empty and would publish a heading and a link.
func TestASectionThatOmitsEverythingPublishesNothing(t *testing.T) {
	got, err := parse("## v1.0.0\n\n<!-- relnotes:omit -->\nAll of it.\n<!-- relnotes:end -->\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got["v1.0.0"].published) != "" {
		t.Fatalf("published is %q, want empty so the gate refuses it", got["v1.0.0"].published)
	}
	if !strings.Contains(got["v1.0.0"].notes, "antifailure.dev/changelog") {
		t.Error("the notes carry no pointer, so this test is not describing the case it names")
	}
}

// The release this repository is about to cut, measured rather than asserted
// about. A section that has stopped omitting anything is a section that has
// gone back to publishing its whole catalogue, and nothing else would say so.
func TestTheRealV100SectionPointsAtTheChangelogAndStaysUnderTheLimit(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("../..", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := parse(string(source))
	if err != nil {
		t.Fatal(err)
	}
	release, ok := got["v1.0.0"]
	if !ok {
		t.Skip("no v1.0.0 section in this tree")
	}
	if !release.omitted {
		t.Error("the v1.0.0 section publishes its whole catalogue again")
	}
	notes := preamble("antifailure/antifailure", "refs/tags/v1.0.0") + "\n" +
		strings.TrimSpace(release.notes) + "\n"
	// GitHub refuses a release body over 125000 characters. This is nowhere
	// near it and the number is here so that a section growing back towards it
	// is visible as a number rather than as a wall somebody notices later.
	if len(notes) > 30000 {
		t.Errorf("the v1.0.0 release body is %d bytes, which is back to being a wall", len(notes))
	}
	for _, want := range []string{
		"cosign verify-blob",
		"### What 1.0 means",
		"### Behaviour you may depend on that changed",
		"### Security",
		"https://antifailure.dev/changelog",
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("the release body does not carry %q", want)
		}
	}
}
