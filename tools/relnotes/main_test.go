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
	if !strings.Contains(got["v1.0.0"], "first stable release") {
		t.Errorf("v1.0.0 body is %q", got["v1.0.0"])
	}
	if strings.Contains(got["v1.0.0"], "An older one") {
		t.Error("the v1.0.0 section swallowed the section below it")
	}
	if strings.Contains(got["v0.9.0"], "Preamble") {
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
	if strings.TrimSpace(got["v1.0.0"]) != "" {
		t.Fatalf("v1.0.0 is %q, want empty", got["v1.0.0"])
	}
}

// A section holding only whitespace is the same failure wearing a blank line.
func TestASectionOfWhitespaceCountsAsEmpty(t *testing.T) {
	got, err := parse("# Changelog\n\n## v1.0.0\n\n   \n\t\n\n## v0.9.0\n\nreal\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got["v1.0.0"]) != "" {
		t.Fatalf("v1.0.0 is %q, want empty after trimming", got["v1.0.0"])
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
	if !strings.Contains(got["v1.0.0"], "And that is all") {
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
	for tag, body := range got {
		if strings.TrimSpace(body) == "" {
			t.Errorf("%s has a heading and nothing under it", tag)
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
