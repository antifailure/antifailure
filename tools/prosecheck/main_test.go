package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAnEmDashIsFound(t *testing.T) {
	got := Check("x.md", "This is prose — with an em dash.\n")
	if len(got) != 1 {
		t.Fatalf("want one finding, got %d", len(got))
	}
	if !strings.Contains(got[0].what, "em dash") {
		t.Errorf("what = %q", got[0].what)
	}
	// The advice is part of the rule: a checker that only forbids teaches
	// people to work around it.
	if got[0].instead == "" {
		t.Error("a finding must say what to write instead")
	}
}

func TestAnEnDashIsFound(t *testing.T) {
	if len(Check("x.md", "Pages 3–10 explain it.\n")) != 1 {
		t.Error("an en dash should be found")
	}
}

func TestADoubleHyphenAsPunctuationIsFound(t *testing.T) {
	if len(Check("x.md", "This is prose -- with a double hyphen.\n")) != 1 {
		t.Error("a spaced double hyphen is punctuation and should be found")
	}
}

// The whole difficulty of this rule. `--flag` is how a command line option is
// spelled and it appears constantly in these documents.
func TestACommandLineFlagIsNotADefect(t *testing.T) {
	for _, s := range []string{
		"Run `go test --count=1` to be sure.\n",
		"Pass `--no-audit` and `--no-fund`.\n",
		"```\ngo build --tags=foo -- extra\n```\n",
	} {
		if got := Check("x.md", s); len(got) != 0 {
			t.Errorf("%q should be clean, got %+v", s, got)
		}
	}
}

// Fenced code is somebody's transcript, not our prose.
func TestFencedCodeIsExempt(t *testing.T) {
	body := "Prose here.\n\n```sh\necho \"a — b\"\n```\n\nMore prose.\n"
	if got := Check("x.md", body); len(got) != 0 {
		t.Errorf("a fence should be exempt, got %+v", got)
	}
}

// Emptying inline code rather than skipping the line means a defect elsewhere
// on the same line is still found.
func TestADefectBesideInlineCodeIsStillFound(t *testing.T) {
	got := Check("x.md", "Run `--flag` first — then check.\n")
	if len(got) != 1 {
		t.Fatalf("want the em dash found beside the flag, got %d", len(got))
	}
}

func TestTheLineAndNumberAreReported(t *testing.T) {
	got := Check("x.md", "one\ntwo\nthree — four\n")
	if len(got) != 1 || got[0].num != 3 {
		t.Fatalf("want line 3, got %+v", got)
	}
}

// The one that matters: this repository's own prose is clean, and stays clean.
func TestTheRealDocumentsAreClean(t *testing.T) {
	root := filepath.Join("..", "..")
	files, err := markdown(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 20 {
		t.Fatalf("found %d documents, which suggests this is looking in the wrong place", len(files))
	}
	var out strings.Builder
	if err := run(root, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}
