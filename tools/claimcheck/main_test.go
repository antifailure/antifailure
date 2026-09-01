package main

import (
	"os"
	"os/exec"
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

// ---------------------------------------------------------------------------
// The pinned control plane image.
// ---------------------------------------------------------------------------

// A tag shape the gate can check offline is the whole premise, so it is the
// first thing tested. main-<sha> and cd-<sha> name the commit they were built
// from; a version tag does not, because the only one ever published came from
// a ref that is not the git tag of the same name.
func TestOnlyACommitNamingTagIsAccepted(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, tag := range []string{"v0.1.1", "latest", "v1", "1.2.3", "main", "main-zzzzzzz"} {
		if _, err := imageCommit(root, tag); err == nil {
			t.Errorf("%q resolved to a commit, and it should not have", tag)
		}
	}
}

// The real pin resolves, which is the half of the check a broken tag would
// fail. It also proves the job running this has the history it needs: a
// shallow checkout fails here rather than silently passing.
func TestTheRealPinResolvesToACommit(t *testing.T) {
	root := filepath.Join("..", "..")
	docs, err := publishedDocs(root)
	if err != nil {
		t.Fatalf("listing published documents: %v", err)
	}
	var tag string
	for _, doc := range docs {
		body, err := os.ReadFile(filepath.Join(root, doc))
		if err != nil {
			t.Fatalf("reading %s: %v", doc, err)
		}
		if m := pinnedImage.FindStringSubmatch(string(body)); m != nil {
			tag = m[1]
			break
		}
	}
	if tag == "" {
		t.Skip("no published page pins a control plane image")
	}
	commit, err := imageCommit(root, tag)
	if err != nil {
		t.Fatalf("the pinned image %s does not resolve: %v", tag, err)
	}
	if len(commit) != 40 {
		t.Fatalf("resolving %s gave %q, which is not a commit", tag, commit)
	}
}

// The mapping from a path inside the image back to a path here is read out of
// the Dockerfile. The trap it has to avoid is the package.json layer: the
// Dockerfile copies web/apps/api/package.json to ./apps/api/ before it copies
// the directory, and taking the first match gives a prefix that is a file.
func TestTheImagePathPrefixIsTheDirectoryCopyNotThePackageJSON(t *testing.T) {
	root := filepath.Join("..", "..")
	prefix, err := imagePathPrefix(root)
	if err != nil {
		t.Fatalf("reading the Dockerfile: %v", err)
	}
	if prefix != "web/apps/api/" {
		t.Fatalf("prefix is %q, want %q", prefix, "web/apps/api/")
	}
	info, err := os.Stat(filepath.Join(root, strings.TrimSuffix(prefix, "/")))
	if err != nil || !info.IsDir() {
		t.Fatalf("%q is not a directory in this repository", prefix)
	}
}

// The check, run against the repository as it stands. This is the one that
// goes red if somebody repins to an image that cannot do what the page says.
func TestTheRealPinnedImageCanRunTheDocumentedSteps(t *testing.T) {
	var out strings.Builder
	if err := checkPinnedImage(filepath.Join("..", ".."), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

// Every published page has to name the same image. Two pages pinning two
// images is how a reader follows half of one procedure against the other's
// image, which is a failure that looks like a bug in the product.
func TestPagesPinningDifferentImagesFail(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs", "src", "content", "docs")
	if err := os.MkdirAll(docs, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"),
		[]byte("run ghcr.io/antifailure/control-plane:main-aaaaaaa\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "other.md"),
		[]byte("run ghcr.io/antifailure/control-plane:main-bbbbbbb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := checkPinnedImage(root, &out); err == nil {
		t.Fatalf("two different pins passed:\n%s", out.String())
	}
}

// A page with no pin at all is not this check's business. Said out loud
// because the alternative, failing on every page, is how a gate gets disabled.
func TestAPageWithNoPinIsNotChecked(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "src", "content", "docs"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"),
		[]byte("node apps/api/src/nothing-like-this.ts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := checkPinnedImage(root, &out); err != nil {
		t.Fatalf("a page with no pin should not fail: %v", err)
	}
}

// ---------------------------------------------------------------------------
// What the site says about the code.
// ---------------------------------------------------------------------------

// The repository as it stands. This is the assertion that goes red the day
// somebody writes one of these sentences back onto a page.
func TestTheRealSiteContradictsNothing(t *testing.T) {
	var out strings.Builder
	if err := checkSiteClaims(filepath.Join("..", ".."), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

// Every rule is proved able to fail, by writing the claim it forbids into a
// www file and requiring a red. A rule nobody has watched fail is a rule
// nobody knows the shape of.
func TestEveryRuleCanFail(t *testing.T) {
	real := filepath.Join("..", "..")
	for _, sample := range []struct {
		rule, text string
	}{
		{"mutual TLS to the control plane", "The agent authenticates with short-lived mTLS."},
		{"there is no hosted control plane", "There is no hosted control plane yet."},
		{"the build runs inside the sandbox", "It builds and runs your services inside a sandbox."},
		{"the scanner reads every row", "A scanner reads back every column of every table."},
		{"a documentation page count nothing counted", "all 41 documentation pages as one file"},
	} {
		root := siteFixture(t, real, "www/lib/fixture.ts", sample.text+"\n")
		var out strings.Builder
		err := checkSiteClaims(root, &out)
		if err == nil {
			t.Errorf("rule %q did not fire on %q:\n%s", sample.rule, sample.text, out.String())
			continue
		}
		if !strings.Contains(out.String(), sample.rule) {
			t.Errorf("a rule fired on %q but not %q:\n%s", sample.text, sample.rule, out.String())
		}
	}
}

// The read-back rule is the two sided one: a page may describe the scan, and
// has to say the rows are a sample when it does. Both halves are tested,
// because a rule that fired on the corrected sentence too would be answered by
// deleting the sentence.
func TestTheReadBackRuleAcceptsTheDisclosedVersion(t *testing.T) {
	root := siteFixture(t, filepath.Join("..", ".."), "www/lib/fixture.ts",
		"A scanner reads back every column of every table, sampling rows rather than all of them.\n")
	var out strings.Builder
	if err := checkSiteClaims(root, &out); err != nil {
		t.Fatalf("the disclosed version was refused: %v\n%s", err, out.String())
	}
}

// A rule whose premise has gone must STOP THE BUILD rather than go on
// refusing a sentence that may have become true. A ban with no expiry
// condition ends up enforcing yesterday's world, and nobody notices because a
// green gate looks the same either way.
func TestARuleWhosePremiseIsGoneFails(t *testing.T) {
	root := t.TempDir()
	// A repository with none of the premise files in it.
	mustRun(t, root, "git", "init", "-q")
	mustWrite(t, filepath.Join(root, "www", "lib", "x.ts"), "nothing to see\n")
	mustRun(t, root, "git", "add", "-A")
	var out strings.Builder
	if err := checkSiteClaims(root, &out); err == nil {
		t.Fatalf("every premise was missing and the check passed:\n%s", out.String())
	} else if !strings.Contains(out.String(), "rests on code that has changed") {
		t.Fatalf("failed for the wrong reason: %v\n%s", err, out.String())
	}
}

// An exception that excuses nothing is a licence nobody granted, and it would
// silently cover a future line that happens to match it.
func TestAStaleExceptionFails(t *testing.T) {
	saved := siteClaimExceptions
	defer func() { siteClaimExceptions = saved }()
	siteClaimExceptions = append(append([]claimException{}, saved...), claimException{
		file: "www/lib/does-not-exist.ts", rule: "mutual TLS to the control plane",
		line: "nothing matches this", reason: "a deliberately stale entry, for this test",
	})
	var out strings.Builder
	if err := checkSiteClaims(filepath.Join("..", ".."), &out); err == nil {
		t.Fatalf("a stale exception passed:\n%s", out.String())
	}
}

// Every exception says why, in a sentence rather than a word. Same rule the
// path exclusions carry, for the same reason: reading the list has to tell you
// why a hit was allowed and not only that somebody allowed it.
func TestEverySiteExceptionStatesWhy(t *testing.T) {
	for _, e := range siteClaimExceptions {
		if len(strings.Fields(e.reason)) < 8 {
			t.Errorf("the exception for %s (%s) says %q, which is too short to explain itself",
				e.file, e.rule, e.reason)
		}
	}
}

// siteFixture copies the real premise files into a scratch repository and adds
// one file of its own, so a rule can be driven without its premise lapsing
// first and reporting the wrong failure.
func siteFixture(t *testing.T, real, name, body string) string {
	t.Helper()
	root := t.TempDir()
	mustRun(t, root, "git", "init", "-q")
	for _, c := range siteClaims {
		src, err := os.ReadFile(filepath.Join(real, c.premise[0]))
		if err != nil {
			t.Fatalf("reading premise %s: %v", c.premise[0], err)
		}
		mustWrite(t, filepath.Join(root, filepath.FromSlash(c.premise[0])), string(src))
	}
	// AuthScreen is a premise and also a file the real tree exempts, so the
	// exemption has something to excuse in the fixture too.
	for _, f := range []string{
		"www/components/AuthScreen.tsx", "www/components/AuthModal.tsx",
		"www/lib/routes.ts", "www/components/pages/product/Architecture.tsx",
	} {
		src, err := os.ReadFile(filepath.Join(real, filepath.FromSlash(f)))
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		mustWrite(t, filepath.Join(root, filepath.FromSlash(f)), string(src))
	}
	mustWrite(t, filepath.Join(root, filepath.FromSlash(name)), body)
	mustRun(t, root, "git", "add", "-A")
	return root
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
