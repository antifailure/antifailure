package main

import (
	"os"
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
	// The trees by name, not only a count. A list that quietly stops covering
	// one of them still clears a threshold, which is how the plan notes went
	// unread while this test was green: `docs/src/content/docs` is what Astro
	// renders, so it looked like the documentation, and the ADRs, the RFCs and
	// the design notes beside it belonged to no list at all.
	for _, tree := range []string{"docs/src/content/docs/", "docs/plan/", "docs/adr/"} {
		if !reaches(files, tree) {
			t.Errorf("%s is reached by nothing, so its prose is checked by nothing", tree)
		}
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
	// One assertion per tree rather than one condition over all three, so that
	// a tree dropping out of the list is reported as itself instead of hiding
	// behind whichever check ran first.
	for _, tree := range []string{"www/", "console/", "docs/"} {
		if !reaches(files, tree) {
			t.Errorf("%s is reached by nothing, so its TypeScript is checked by nothing", tree)
		}
	}
	// Nothing from a dependency or a build directory, which would make the
	// check both slow and somebody else's problem.
	for _, f := range files {
		if strings.Contains(f, "node_modules/") || strings.Contains(f, "/.next/") || strings.Contains(f, "/out/") {
			t.Errorf("%s is not ours to style", f)
		}
	}
}

// The hole that made "the site is scanned" only two thirds true. This checker
// reads www and console TypeScript and it read Markdown in the documentation
// trees, so a `.md` under the site belonged to neither list and no gate in the
// repository had an opinion about it.
func TestMarkdownUnderTheSiteAndTheConsoleIsInScope(t *testing.T) {
	root := t.TempDir()
	write(t, root, "docs/src/content/docs/page.md", "Documentation prose.\n")
	write(t, root, "www/README.md", "Site prose.\n")
	write(t, root, "console/NOTES.md", "Console prose.\n")

	files, err := markdown(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"docs/src/content/docs/page.md", "www/README.md", "console/NOTES.md"} {
		if !contains(files, want) {
			t.Errorf("%s is not in scope; collected %v", want, files)
		}
	}
}

// The exemption has to be exactly as wide as the generator and no wider, or it
// becomes a way to smuggle prose past the gate by naming a file CLAUDE.md.
func TestOnlyTheGeneratorsOwnBlockIsExemptFromTheMarkdownScan(t *testing.T) {
	root := t.TempDir()
	const marker = "<!-- BEGIN:nextjs-agent-rules -->\n"
	write(t, root, "www/AGENTS.md", marker+"Written by the tool.\n")
	write(t, root, "www/CLAUDE.md", "Written by a person.\n")
	write(t, root, "console/AGENTS.md", "Written by a person too.\n")

	files, err := markdown(root)
	if err != nil {
		t.Fatal(err)
	}
	if contains(files, "www/AGENTS.md") {
		t.Error("the generated block is not ours to style and should be skipped")
	}
	for _, want := range []string{"www/CLAUDE.md", "console/AGENTS.md"} {
		if !contains(files, want) {
			t.Errorf("%s carries no generator marker and must still be checked; collected %v", want, files)
		}
	}
}

// The documentation site is the one place where "the documentation" and "the
// site" are the same tree, which is how it fell between the two lists. Astro
// builds it, so its pages are TypeScript, and `docs/src/pages/llms-full.txt.ts`
// writes the plain text twin of the entire manual. The sentence at the top of
// that file saying what the route serves carried an em dash through every green
// run this gate has ever had.
//
// `www/README.md` and `www/page.tsx` are controls and neither is decoration.
// `run` refuses a tree it found no Markdown or no TypeScript in, deliberately,
// so without a file outside docs in each walk, narrowing either list back down
// would make `run` return an error for a reason that has nothing to do with
// what this test is asking, and the test would pass on it. The assertion on
// file and line closes the same hole from the other side. Both were watched
// failing before they were trusted.
func TestTheDocumentationSitesTypeScriptIsInScope(t *testing.T) {
	root := t.TempDir()
	write(t, root, "www/README.md", "Site prose.\n")
	write(t, root, "www/page.tsx", "export const ok = 1;\n")
	write(t, root, "docs/src/pages/llms-full.txt.ts",
		"/**\n * /docs/llms-full.txt \u2014 the complete documentation as one file.\n */\n")

	var out strings.Builder
	if err := run(root, &out); err == nil {
		t.Fatalf("the documentation site's TypeScript was read by nothing:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "docs/src/pages/llms-full.txt.ts:2") {
		t.Errorf("the defect was not reported against its file and line:\n%s", out.String())
	}
}

// The other half of the same seam. `docs/src/content/docs` is what Astro
// renders, so it read as "the documentation", and the ADRs, the RFCs, the plan
// notes and the design documents sitting beside it were prose that no gate in
// this repository had an opinion about. Both rules are checked here, because
// what those files actually carried was eighteen defects of both shapes.
func TestDocumentationMarkdownOutsideTheContentCollectionIsInScope(t *testing.T) {
	root := t.TempDir()
	write(t, root, "www/README.md", "Site prose.\n")
	write(t, root, "www/page.tsx", "export const ok = 1;\n")
	write(t, root, "docs/src/content/docs/page.md", "Rendered documentation.\n")
	write(t, root, "docs/plan/notes/dogfood.md",
		"This is the dead code shape \u2014 the parts are all there.\n")
	write(t, root, "docs/adr/0001-language-choices.md",
		"One runtime -- and nothing else was considered.\n")

	var out strings.Builder
	if err := run(root, &out); err == nil {
		t.Fatalf("documentation Markdown outside the collection was read by nothing:\n%s", out.String())
	}
	for _, want := range []string{"docs/plan/notes/dogfood.md:1", "docs/adr/0001-language-choices.md:1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("%s was not reported:\n%s", want, out.String())
		}
	}
}

// A limit, asserted so that widening this checker has to argue with a test
// rather than find out in CI.
//
// `--` is the SQL line comment, and this rule matches the sequence with
// whitespace on both sides, which is exactly how a migration writes an indented
// one. 1120 of the 1242 lines that would be flagged outside this checker's
// reach are SQL comments: 853 in the migrations and 267 more in SQL written as
// template literals in the api's TypeScript. They cannot be rewritten, because
// there the sequence is the syntax and not a decision somebody should have made
// differently. So the migrations stay out of reach on purpose, which is why
// `sources` names three trees by hand instead of taking the repository.
//
// The comment in the fixture is INDENTED, and that is the whole test. A `--` at
// the start of a line has no character before it, so `\s--\s` cannot match it
// and an unindented SQL comment is invisible to this rule already. Written flush
// left first, this test passed with `.sql` added to the extensions and `web`
// added to the trees, which is the mutation it exists to catch. It was asserting
// a property of column zero and not a property of the scope.
func TestTheDoubleHyphenRuleDoesNotFollowSQLIntoTheMigrations(t *testing.T) {
	root := t.TempDir()
	write(t, root, "www/README.md", "Site prose.\n")
	write(t, root, "www/page.tsx", "export const ok = 1;\n")
	write(t, root, "web/packages/db/migrations/0001_init.sql",
		"CREATE TABLE members (\n  -- Stored lowercased: providers disagree about case.\n  email text\n);\n")

	var out strings.Builder
	if err := run(root, &out); err != nil {
		t.Fatalf("a SQL comment is syntax and must not be a prose defect: %v\n%s", err, out.String())
	}
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// reaches reports whether the walk produced anything at all from a tree. The
// question a count cannot answer.
func reaches(files []string, prefix string) bool {
	for _, f := range files {
		if strings.HasPrefix(f, prefix) {
			return true
		}
	}
	return false
}

func contains(files []string, want string) bool {
	for _, f := range files {
		if f == want {
			return true
		}
	}
	return false
}
