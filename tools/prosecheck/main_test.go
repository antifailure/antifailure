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

// The inversion that let the whole site through. In Markdown a backtick span is
// code and is emptied before the scan; in TypeScript it is a template literal,
// which is a string, which is the copy a reader sees.
func TestATemplateLiteralIsNotExempt(t *testing.T) {
	src := "const title = `${name} — ${SITE_NAME}`;\n"
	if got := Check("lib/site.ts", src); len(got) != 1 {
		t.Fatalf("want the em dash found inside the template literal, got %d", len(got))
	}
	// The same characters in a Markdown backtick span are a code sample.
	if got := Check("x.md", "Write `a — b` in the config.\n"); len(got) != 0 {
		t.Errorf("markdown inline code should stay exempt, got %+v", got)
	}
}

// A comment is a sentence somebody wrote, and it is where the tell survives
// longest because nobody proofreads a comment.
func TestACommentInSourceIsChecked(t *testing.T) {
	if got := Check("components/Card.tsx", "// wider than the card — a long message — is clipped\n"); len(got) != 1 {
		t.Errorf("want one finding in a comment, got %+v", got)
	}
}

// The one construct in TypeScript that could be mistaken for punctuation.
func TestTheDecrementOperatorIsNotADefect(t *testing.T) {
	for _, s := range []string{
		"for (let i = n; i > 0; i--) {\n",
		"--count;\n",
		"const flag = \"--no-audit\";\n",
	} {
		if got := Check("lib/x.ts", s); len(got) != 0 {
			t.Errorf("%q should be clean, got %+v", s, got)
		}
	}
}

// A scan for a character cannot see the character spelled as an escape, and
// www/scripts/markdown-twins.mjs was exactly that: the last em dash under www,
// invisible after every literal one was gone.
func TestAnEscapedEmDashInSourceIsFound(t *testing.T) {
	for _, s := range []string{
		`title.replace(/\s+\u2014\s+Antifailure$/, "")` + "\n",
		"<p>a &mdash; b</p>\n",
		"<p>a &#8212; b</p>\n",
	} {
		if got := Check("app/page.tsx", s); len(got) != 1 {
			t.Errorf("%q should be one finding, got %+v", s, got)
		}
	}
	// An en dash escape carries its own advice, which is different.
	got := Check("app/page.tsx", `const r = /\u2013/;`+"\n")
	if len(got) != 1 || !strings.Contains(got[0].what, "en dash") {
		t.Fatalf("want an en dash finding, got %+v", got)
	}
}

// Documentation writes about escapes and entities rather than rendering them,
// so the escape rules are for source only.
func TestAnEscapeInMarkdownIsNotADefect(t *testing.T) {
	if got := Check("x.md", `The codepoint is \u2014 and the entity is &mdash;.`+"\n"); len(got) != 0 {
		t.Errorf("markdown should not be scanned for escapes, got %+v", got)
	}
}

// The one that matters for the site: www and console are actually reached, and
// they are clean. The count guards against the walk quietly finding nothing,
// which is how this checker reported green over sixty-eight em dashes.
func TestTheRealSourceIsClean(t *testing.T) {
	root := filepath.Join("..", "..")
	files, err := source(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 100 {
		t.Fatalf("found %d source files, which suggests this is looking in the wrong place", len(files))
	}
	var www, console bool
	for _, f := range files {
		www = www || strings.HasPrefix(f, "www/")
		console = console || strings.HasPrefix(f, "console/")
	}
	if !www || !console {
		t.Fatalf("www reached: %v, console reached: %v", www, console)
	}
	// Nothing from a dependency or a build directory, which would make the
	// check both slow and somebody else's problem.
	for _, f := range files {
		if strings.Contains(f, "node_modules/") || strings.Contains(f, "/.next/") || strings.Contains(f, "/out/") {
			t.Errorf("%s is not ours to style", f)
		}
	}
}
