package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecognisesAPathClaim(t *testing.T) {
	for _, tok := range []string{
		"tools/scanrepo",
		"docs/security/incidents/",
		"engine/internal/testutil/fakes",
		".golangci.yml",
		"SECURITY.md",
		"engine/internal/errors/catalog.yaml",
	} {
		if !looksLikeAPath(tok) {
			t.Errorf("%q should be treated as a path claim", tok)
		}
	}
}

func TestIgnoresThingsThatAreNotPaths(t *testing.T) {
	for _, tok := range []string{
		"af version",                   // a command, and it has a space
		"-trimpath",                    // a flag
		"https://antifailure.dev/docs", // a URL
		"clock.Clock",                  // a Go identifier with no path separator
		"any",                          // a bare word
		"",                             // nothing
	} {
		if looksLikeAPath(tok) {
			t.Errorf("%q should not be treated as a path claim", tok)
		}
	}
}

// A token with a placeholder is a claim about a naming convention, not about
// one file, and demanding that `.changes/<pr>.internal.md` exist would be
// demanding a file whose name is a variable.
func TestIgnoresPlaceholders(t *testing.T) {
	if looksLikeAPath(".changes/<pr>.internal.md") {
		t.Error("a token containing a placeholder is not a claim about one file")
	}
}

func writeDoc(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// trackedSet builds the "what the repository contains" set a test needs,
// without a git repository.
func trackedSet(paths ...string) map[string]bool {
	m := map[string]bool{}
	for _, p := range paths {
		m[p] = true
	}
	return m
}

func claimsIn(t *testing.T, root string) []claim {
	t.Helper()
	c, err := collectClaims(root, []string{"README.md"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Fenced code is an example, not a claim that this repository contains it.
func TestFencedCodeIsNotAClaim(t *testing.T) {
	root := writeDoc(t, "Text.\n\n```go\nimport \"some/other/module\"\n```\n\nMore text.\n")
	if got := claimsIn(t, root); len(got) != 0 {
		t.Errorf("fenced code should make no claims, got %v", got)
	}
}

func TestAMissingPathIsReported(t *testing.T) {
	root := writeDoc(t, "The fakes live in `engine/internal/testutil/fakes`.\n")

	var out strings.Builder
	err := decide(trackedSet(), claimsIn(t, root), nil, &out)
	if err == nil {
		t.Fatal("a documented path that does not exist must fail")
	}
	if !strings.Contains(out.String(), "MISSING") {
		t.Errorf("the report should mark it MISSING, got %q", out.String())
	}
}

func TestAPathThatExistsPasses(t *testing.T) {
	root := writeDoc(t, "See `sub/dir`.\n")
	var out strings.Builder
	if err := decide(trackedSet("sub/dir"), claimsIn(t, root), nil, &out); err != nil {
		t.Fatalf("an existing path should pass: %v", err)
	}
}

// Prose writes a directory with a trailing slash. That is not part of the name
// git knows, and treating it as one would fail every correctly written document.
func TestATrailingSlashIsNotPartOfTheName(t *testing.T) {
	root := writeDoc(t, "Incidents live in `incidents/`.\n")
	var out strings.Builder
	if err := decide(trackedSet("incidents"), claimsIn(t, root), nil, &out); err != nil {
		t.Fatalf("a directory written with a trailing slash should pass: %v", err)
	}
}

// The same discipline as the vulnerability policy and the gate exemptions: an
// exclusion that excludes nothing is describing something that is not there,
// and it would silently cover a future token of the same spelling.
func TestAnExclusionThatMatchesNothingIsStale(t *testing.T) {
	root := writeDoc(t, "Nothing here mentions the excluded token.\n")

	var out strings.Builder
	stale := map[string]string{"never/mentioned": "an exclusion nothing uses"}
	err := decide(trackedSet(), claimsIn(t, root), stale, &out)
	if err == nil {
		t.Fatal("exclusions that match nothing must fail")
	}
	if !strings.Contains(out.String(), "STALE") {
		t.Errorf("the report should mark them STALE, got %q", out.String())
	}
}

// A document named here that does not exist is itself a failure: the list names
// the pages the project is supposed to have.
func TestAMissingDocumentIsAFailure(t *testing.T) {
	if _, err := collectClaims(t.TempDir(), []string{"README.md"}); err == nil {
		t.Fatal("a document that does not exist must be an error")
	}
}

// The real repository must pass, and the documents this checks must be the ones
// a reader actually meets first.
func TestTheRealRepositoryPasses(t *testing.T) {
	root := filepath.Join("..", "..")
	claims, err := collectClaims(root, documents)
	if err != nil {
		t.Fatalf("collecting claims: %v", err)
	}
	if len(claims) < 5 {
		t.Fatalf("found %d path claims, which suggests the matcher has stopped matching", len(claims))
	}
	tracked, err := trackedPaths(root)
	if err != nil {
		t.Fatalf("asking git: %v", err)
	}
	var out strings.Builder
	if err := decide(tracked, claims, notAPath, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

func TestEveryExclusionStatesWhy(t *testing.T) {
	for tok, reason := range notAPath {
		if len(strings.Fields(reason)) < 5 {
			t.Errorf("the exclusion for %q says %q, which is too short to explain why it is not a path", tok, reason)
		}
	}
}

// The bug this gate shipped with, pinned so it cannot come back. `.gate-reports/`
// is gitignored and created by `just gate`, so a filesystem check passes on any
// machine that has run the gate and fails in a fresh checkout. Asking git makes
// the two agree.
func TestAnUntrackedButPresentDirectoryDoesNotCount(t *testing.T) {
	root := writeDoc(t, "Reports land in `build-output/`.\n")
	if err := os.MkdirAll(filepath.Join(root, "build-output"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := decide(trackedSet(), claimsIn(t, root), nil, &out); err == nil {
		t.Fatal("a directory that exists on disk but is not in the repository must not satisfy a claim")
	}
}

// Every parent of a tracked file is a directory the repository has, because git
// lists files and never directories.
func TestAParentOfATrackedFileCounts(t *testing.T) {
	root := filepath.Join("..", "..")
	tracked, err := trackedPaths(root)
	if err != nil {
		t.Skipf("no git here: %v", err)
	}
	for _, dir := range []string{"tools", "tools/claimcheck", "engine/internal"} {
		if !tracked[dir] {
			t.Errorf("%q should be known as a directory the repository contains", dir)
		}
	}
}

// Documentation addresses hardcoded in Go source.

func TestADocsURLIsFoundInAStringLiteral(t *testing.T) {
	got := docsURL.FindStringSubmatch(`"see https://antifailure.dev/docs/guides/build for more"`)
	if got == nil {
		t.Fatal("a docs address in a literal must be found")
	}
	if got[1] != "guides/build" {
		t.Errorf("captured %q, want the path", got[1])
	}
}

func TestDocPageExistsAcceptsBothShapesTheSiteUses(t *testing.T) {
	tracked := trackedSet(
		"docs/src/content/docs/guides/build.md",
		"docs/src/content/docs/providers/index.md",
	)
	for _, page := range []string{"guides/build", "providers"} {
		if !docPageExists(tracked, page) {
			t.Errorf("%q should resolve", page)
		}
	}
	if docPageExists(tracked, "guides/builds") {
		t.Error("a page that does not exist must not resolve")
	}
}

// The reason this reads literals rather than the file text: the first version
// grepped the whole file and its first hit was this tool's own comment quoting
// the broken URL as an example. The doc comment claimed it read only literals
// while the code read everything, which is the same disagreement between a
// stated rule and an implemented one that this repository keeps finding.
func TestAURLInACommentIsNotAClaim(t *testing.T) {
	root := t.TempDir()
	src := "package p\n\n// It stamped https://antifailure.dev/docs/nope/missing into the output.\nconst X = 1\n"
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := checkDocsURLs(root, trackedSet(), &out); err != nil {
		t.Fatalf("a URL in a comment must not fail the build: %v\n%s", err, out.String())
	}
}

func TestAURLInALiteralPointingNowhereFails(t *testing.T) {
	root := t.TempDir()
	src := "package p\n\nconst X = \"https://antifailure.dev/docs/nope/missing\"\n"
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := checkDocsURLs(root, trackedSet(), &out); err == nil {
		t.Fatal("a literal pointing at a missing page must fail")
	}
	if !strings.Contains(out.String(), "404") {
		t.Errorf("the report should mark it, got %q", out.String())
	}
}

// A placeholder describes the shape of a URL rather than naming one.
func TestAPlaceholderPathIsIgnored(t *testing.T) {
	root := t.TempDir()
	src := "package p\n\nconst X = \"https://antifailure.dev/docs/<path>\"\n"
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := checkDocsURLs(root, trackedSet(), &out); err != nil {
		t.Fatalf("a placeholder must not fail the build: %v", err)
	}
}

// The one that matters: every address the real engine prints must resolve.
// engine/internal/build stamped /docs/guides/builds into every generated
// Dockerfile, four times, and the page is guides/build. That URL was live and
// it 404'd, in output handed to somebody at the moment their build failed.
func TestEveryDocsURLTheRealEnginePrintsResolves(t *testing.T) {
	root := filepath.Join("..", "..")
	tracked, err := trackedPaths(root)
	if err != nil {
		t.Skipf("no git here: %v", err)
	}
	var out strings.Builder
	if err := checkDocsURLs(root, tracked, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "documentation addresses in Go source") {
		t.Errorf("the summary should say how many were checked, got %q", out.String())
	}
}

// Internal documentation links.

func writeDocsTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		full := filepath.Join(root, "docs", "src", "content", "docs", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The bug this found live: astro.config.mjs sets base "/docs" and Starlight does
// not rewrite absolute links to include it, so /concepts/egress/ points outside
// the site. 78 links were written that way and 404'd for real readers.
func TestALinkWithoutTheDocsBaseIsReported(t *testing.T) {
	root := writeDocsTree(t, map[string]string{
		"index.md": "See [egress](/concepts/egress/).\n",
	})
	tracked := trackedSet("docs/src/content/docs/concepts/egress.md")

	var out strings.Builder
	if err := checkDocsLinks(root, tracked, &out); err == nil {
		t.Fatal("a link missing the site base must fail; it 404s for a real reader")
	}
	if !strings.Contains(out.String(), "BASE") {
		t.Errorf("the report should mark it, got %q", out.String())
	}
}

func TestALinkWithTheBaseAndARealPagePasses(t *testing.T) {
	root := writeDocsTree(t, map[string]string{
		"index.md": "See [egress](/docs/concepts/egress/) and [masking](/docs/concepts/masking).\n",
	})
	tracked := trackedSet(
		"docs/src/content/docs/concepts/egress.md",
		"docs/src/content/docs/concepts/masking.md",
	)

	var out strings.Builder
	if err := checkDocsLinks(root, tracked, &out); err != nil {
		t.Fatalf("correct links should pass: %v\n%s", err, out.String())
	}
}

// Both spellings the site accepts, with and without a trailing slash. An
// earlier negative control of mine silently substituted nothing because it
// assumed the trailing slash, which taught me about my control rather than
// about the code.
func TestATrailingSlashIsOptionalOnALink(t *testing.T) {
	tracked := trackedSet("docs/src/content/docs/concepts/egress.md")
	for _, link := range []string{"/docs/concepts/egress/", "/docs/concepts/egress"} {
		root := writeDocsTree(t, map[string]string{"index.md": "[x](" + link + ")\n"})
		var out strings.Builder
		if err := checkDocsLinks(root, tracked, &out); err != nil {
			t.Errorf("%q should pass: %v", link, err)
		}
	}
}

func TestALinkToAPageThatDoesNotExistIsReported(t *testing.T) {
	root := writeDocsTree(t, map[string]string{
		"index.md": "[gone](/docs/concepts/nonexistent/)\n",
	})
	var out strings.Builder
	if err := checkDocsLinks(root, trackedSet(), &out); err == nil {
		t.Fatal("a link to a missing page must fail")
	}
	if !strings.Contains(out.String(), "404") {
		t.Errorf("the report should mark it, got %q", out.String())
	}
}

// The one that matters: every cross reference on the real site resolves.
func TestEveryInternalDocumentationLinkResolves(t *testing.T) {
	root := filepath.Join("..", "..")
	tracked, err := trackedPaths(root)
	if err != nil {
		t.Skipf("no git here: %v", err)
	}
	var out strings.Builder
	if err := checkDocsLinks(root, tracked, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "internal documentation links") {
		t.Errorf("the summary should say how many were checked, got %q", out.String())
	}
}
