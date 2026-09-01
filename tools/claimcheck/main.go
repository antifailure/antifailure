// Command claimcheck verifies that every repository path our documents point at
// is a path that exists.
//
// It exists because of a pattern this repository keeps producing. SECURITY.md
// promised security researchers an adversarial test suite in
// `tests/adversarial`; the word "adversarial" appeared in exactly one file in
// the repository, which was SECURITY.md. CONTRIBUTING.md told external provider
// authors to start at a page that was never written, and described shared test
// fakes at a path with nothing in it. Every one of those reads as a working
// part of the project to somebody deciding whether to trust it or contribute to
// it, and every one of them is a dead link that a person discovers only after
// following our instructions.
//
// This is the same failure as a function with no callers, which this repository
// has found six times: something is declared, it looks finished, and nothing
// connects it to reality. The remedy is the same too. Do not sweep for it by
// hand, because a sweep is only true on the day it is run. Make it a gate.
//
// The rule is narrow on purpose. A path in backticks that looks like it points
// into this repository must exist. Prose is not checked, because a checker that
// tried to would either be wrong or be ignored.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// documents are checked for path claims. These are the pages a person reads
// before they trust the project or contribute to it, which is where a dead
// path costs the most.
var documents = []string{
	"README.md",
	"CONTRIBUTING.md",
	"SECURITY.md",
}

// notAPath lists backticked tokens that look like repository paths and are not,
// each with the reason. An entry that no longer appears in any document fails
// the build, for the same reason a stale vulnerability suppression does: it
// describes a decision about something that is no longer there, and it would
// silently cover a future token that happened to take the same spelling.
var notAPath = map[string]string{
	"area/short-description": "the branch naming convention itself, given as a shape rather than a real branch",
	"masking/fpe-tweak":      "an example branch name in the sentence about branch naming",
	"docs/neon-provider":     "the second example branch name in that same sentence",
	"pgregory.net/rapid":     "a Go module path, resolved from the module cache and not a directory here",

	"github.com/docker/docker": "a Go module path, named in SECURITY.md's account of the open Moby advisories, and resolved from the module cache rather than being a directory here",
	"github.com/moby/moby":     "the same module under its other name, named in the same paragraph because the version check has to cover both spellings",
	"github.com/moby/moby/v2":  "where Moby published the fixes, named so the reason they cannot be taken is checkable",

	".gate-reports/": "created by `just gate` when it runs and gitignored, so it is a place output goes rather than something the repository contains",

	"THIRD_PARTY_NOTICES.md": "generated at release time from what is actually linked, so it is deliberately absent from the tree",
}

// claim is one backticked token that looks like a path into this repository.
type claim struct {
	text string
	file string
	line int
}

// pathish matches a backticked token that could be a repository path: it has a
// separator or a file extension, and no spaces.
//
// Tokens containing a placeholder such as `.changes/<pr>.internal.md` are
// excluded by the angle brackets, because the claim there is about the naming
// convention rather than about one file. The directory in the same sentence is
// still checked, which is what matters.
var pathish = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// looksLikeAPath decides whether a token is claiming a repository path.
//
// Deliberately generous. A token this wrongly accepts is one somebody adds to
// notAPath with a sentence saying why, which costs a minute and leaves the
// reason written down. A token this wrongly rejects is a dead link nobody ever
// hears about, which is the failure being prevented.
func looksLikeAPath(tok string) bool {
	switch {
	case tok == "", !pathish.MatchString(tok):
		return false
	case strings.HasPrefix(tok, "http"), strings.Contains(tok, "://"):
		return false
	case strings.HasPrefix(tok, "-"):
		// A command line flag, such as -trimpath.
		return false
	}
	// Either it has a separator, or it names a file by extension.
	if strings.Contains(tok, "/") {
		return true
	}
	switch filepath.Ext(tok) {
	case ".go", ".md", ".yaml", ".yml", ".ts", ".tsx", ".sql", ".json", ".sh":
		return true
	}
	return false
}

var backticked = regexp.MustCompile("`([^`\n]+)`")

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}

	if err := run(*root, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "\nclaimcheck: %v\n", err)
		os.Exit(1)
	}
}

func run(root string, out io.Writer) error {
	claims, err := collectClaims(root, documents)
	if err != nil {
		return err
	}
	tracked, err := trackedPaths(root)
	if err != nil {
		return err
	}
	if err := decide(tracked, claims, notAPath, out); err != nil {
		return err
	}
	if err := checkDocsURLs(root, tracked, out); err != nil {
		return err
	}
	if err := checkDocsLinks(root, tracked, out); err != nil {
		return err
	}
	if err := checkPinnedImage(root, out); err != nil {
		return err
	}
	return checkSiteClaims(root, out)
}

// docsBase is where the documentation site is served from. It is not cosmetic:
// astro.config.mjs sets `base: "/docs"`, and Starlight does NOT rewrite absolute
// links to include it. A link written as /concepts/egress/ therefore points at
// antifailure.dev/concepts/egress/, which is outside the site and 404s.
const docsBase = "/docs/"

// internalLink matches a markdown link to an absolute path on our own site.
var internalLink = regexp.MustCompile(`\]\((/[A-Za-z0-9._/-]*)\)`)

// checkDocsLinks verifies that every internal link in the documentation points
// at a page that exists, at the address the site actually serves.
//
// This found 78 broken links live. The site is served under /docs and 78 links
// were written without it, so a reader following a cross reference in the
// middle of a page got a 404. Confirmed against production rather than
// reasoned about: /concepts/agents/ returned 404 and /docs/concepts/agents/
// returned 200.
//
// The reason it went unnoticed is worth recording, because it is the same
// shape as everything else in this file: BOTH conventions were in use, 68
// links correct and 78 wrong, so any single page a person opened had a decent
// chance of looking fine.
func checkDocsLinks(root string, tracked map[string]bool, out io.Writer) error {
	dir := filepath.Join(root, "docs", "src", "content", "docs")
	if _, err := os.Stat(dir); err != nil {
		report(out, "claimcheck: no documentation tree at %s, so no links checked\n", dir)
		return nil
	}

	var wrongBase, dead []string
	checked := 0

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || (filepath.Ext(path) != ".md" && filepath.Ext(path) != ".mdx") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(body), "\n") {
			for _, m := range internalLink.FindAllStringSubmatch(line, -1) {
				target := m[1]
				checked++
				where := fmt.Sprintf("%s:%d", filepath.ToSlash(rel), i+1)

				if !strings.HasPrefix(target, docsBase) {
					wrongBase = append(wrongBase, where+" -> "+target)
					continue
				}
				page := strings.Trim(strings.TrimPrefix(target, docsBase), "/")
				if page != "" && !docPageExists(tracked, page) {
					dead = append(dead, where+" -> "+target)
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	sort.Strings(wrongBase)
	sort.Strings(dead)
	for _, w := range wrongBase {
		report(out, "BASE     %s  (the site is served under %s)\n", w, docsBase)
	}
	for _, d := range dead {
		report(out, "404      %s\n", d)
	}
	report(out, "claimcheck: %d internal documentation links, %d with the wrong base, %d pointing nowhere\n",
		checked, len(wrongBase), len(dead))

	if n := len(wrongBase) + len(dead); n > 0 {
		return fmt.Errorf("%d internal documentation links are broken. A reader following a cross "+
			"reference in the middle of a page gets a 404", n)
	}
	return nil
}

// docsURL matches a documentation address written as a literal in Go source.
//
// Only literals: `"https://antifailure.dev/docs/" + e.Entry.Docs` builds its
// path at run time from the error catalog, and tools/errcheck already proves
// every entry in that catalog has a page. What this catches is the other kind,
// a path typed by hand into a string, which nothing was checking.
var docsURL = regexp.MustCompile(`https://antifailure\.dev/docs/([A-Za-z0-9._/-]+)`)

// checkDocsURLs verifies that every documentation address hardcoded in Go
// source points at a page that exists.
//
// It is here because this class has already shipped. engine/internal/build
// stamped https://antifailure.dev/docs/guides/builds into EVERY generated
// Dockerfile, four times, and the page is guides/build, singular. That URL was
// live and it 404'd, in output handed to a user at the moment their build
// failed, which is the worst possible moment to send somebody to a missing
// page. errcheck could not see it because it is a raw string rather than a
// catalog entry.
func checkDocsURLs(root string, tracked map[string]bool, out io.Writer) error {
	var dead []string
	checked := 0

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (name == "node_modules" || name == "vendor" || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		// Test files are skipped, because this is about addresses a USER
		// follows and no test output reaches one. It is also the only way a
		// test can write a deliberately broken URL as a fixture without the
		// checker treating its own fixture as a defect, which is what
		// happened the first time.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		// Parsed rather than grepped, so that only STRING LITERALS are read.
		// A regular expression over the file text also matches comments, and
		// the first thing it matched was this file's own comment quoting the
		// broken URL as an example. Worse than the false positive is that the
		// comment above claimed it read only literals while the code read
		// everything, which is precisely the disagreement between a stated
		// rule and an implemented one that this repository keeps finding.
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			// A file that does not parse is somebody else's problem; the
			// compiler will say so more clearly than this would.
			return nil
		}
		rel, _ := filepath.Rel(root, path)

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				return true
			}
			for _, m := range docsURL.FindAllStringSubmatch(value, -1) {
				page := strings.TrimSuffix(m[1], "/")
				if page == "" || strings.Contains(page, "<") {
					continue
				}
				checked++
				if !docPageExists(tracked, page) {
					dead = append(dead, fmt.Sprintf("%s:%d -> /docs/%s",
						filepath.ToSlash(rel), fset.Position(lit.Pos()).Line, page))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		return err
	}

	sort.Strings(dead)
	for _, d := range dead {
		report(out, "404      %s\n", d)
	}
	report(out, "claimcheck: %d documentation addresses in Go source, %d pointing at a page that does not exist\n",
		checked, len(dead))

	if len(dead) > 0 {
		return fmt.Errorf("%d documentation addresses in Go source 404. A user follows these at the "+
			"moment something has already gone wrong for them", len(dead))
	}
	return nil
}

// report writes a line of the report.
//
// Write errors are deliberately not propagated per line. The verdict of this
// tool is its exit code, not its report, so a broken pipe on stdout changes
// what a person can read and not whether the build should fail. Ignoring it
// explicitly, once, with this sentence, is honest; ignoring it silently at
// eight call sites is the thing errcheck is right to object to.
func report(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

// docPageExists reports whether a documentation path has a page behind it,
// in either of the two shapes the site uses.
func docPageExists(tracked map[string]bool, page string) bool {
	const base = "docs/src/content/docs/"
	return tracked[base+page+".md"] ||
		tracked[base+page+".mdx"] ||
		tracked[base+page+"/index.md"] ||
		tracked[base+page+"/index.mdx"]
}

// trackedPaths asks git what the repository actually contains, rather than
// looking at the working tree.
//
// This distinction is the whole reason the first version of this gate passed
// locally and failed in CI. CONTRIBUTING mentions `.gate-reports/`, which is
// gitignored and created by `just gate` at runtime. On the machine of anybody
// who has run the gate it is right there on disk, so a filesystem check says
// yes; in a fresh checkout it does not exist and the same check says no. A gate
// whose answer depends on what you happen to have built is not a gate.
//
// Directories are included by implication: git lists files, so every parent of
// every tracked file is a directory the repository has.
func trackedPaths(root string) (map[string]bool, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("asking git what the repository contains: %w", err)
	}

	paths := map[string]bool{}
	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" {
			continue
		}
		paths[f] = true
		for dir := path.Dir(f); dir != "." && dir != "/"; dir = path.Dir(dir) {
			paths[dir] = true
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("git lists no files under %s, so nothing could be checked", root)
	}
	return paths, nil
}

// collectClaims reads each document and returns the path claims it makes.
// A document that does not exist is itself a failure: this list names the pages
// the project is supposed to have.
func collectClaims(root string, docs []string) ([]claim, error) {
	var claims []claim
	for _, doc := range docs {
		body, err := os.ReadFile(filepath.Join(root, doc))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", doc, err)
		}
		inFence := false
		for i, line := range strings.Split(string(body), "\n") {
			// Fenced code is example code, not a claim about this repository.
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			for _, m := range backticked.FindAllStringSubmatch(line, -1) {
				tok := strings.TrimSpace(m[1])
				if looksLikeAPath(tok) {
					claims = append(claims, claim{text: tok, file: doc, line: i + 1})
				}
			}
		}
	}
	return claims, nil
}

// decide takes the exclusion set rather than reading the package level one so
// that a test can exercise the dead-path rule and the stale-exclusion rule
// independently. Reading the global here made every unit test fail on
// exclusions its one line document was never going to mention.
func decide(tracked map[string]bool, claims []claim, exclusions map[string]string, out io.Writer) error {
	var dead []claim
	used := map[string]bool{}
	checked := 0

	for _, c := range claims {
		if _, ok := exclusions[c.text]; ok {
			used[c.text] = true
			continue
		}
		checked++
		// A trailing slash is how prose writes a directory. It is not part of
		// the name git knows.
		if !tracked[strings.TrimSuffix(c.text, "/")] {
			dead = append(dead, c)
		}
	}

	var stale []string
	for tok := range exclusions {
		if !used[tok] {
			stale = append(stale, tok)
		}
	}
	sort.Strings(stale)

	for _, c := range dead {
		report(out, "MISSING  %s:%d  %s\n", c.file, c.line, c.text)
	}
	for _, tok := range stale {
		report(out, "STALE    %s is listed as not-a-path and no document mentions it\n", tok)
	}
	report(out, "\nclaimcheck: %d path claims across %d documents, %d dead, %d stale exclusions\n",
		checked, len(documents), len(dead), len(stale))

	switch {
	case len(dead) > 0 && len(stale) > 0:
		return fmt.Errorf("%d documented paths do not exist, and %d exclusions describe nothing", len(dead), len(stale))
	case len(dead) > 0:
		return fmt.Errorf("%d documented paths do not exist. Either build the thing or stop claiming it; "+
			"a reader follows these", len(dead))
	case len(stale) > 0:
		return fmt.Errorf("%d exclusions name a token no document uses any more, so they are describing nothing", len(stale))
	}
	return nil
}

// ensure io/fs stays imported for the build tag free walk helpers used in tests.
var _ fs.FileMode

// ---------------------------------------------------------------------------
// The pinned control plane image, and whether it can do what the page says.
// ---------------------------------------------------------------------------

// pinnedImage matches a published control plane image reference in a document.
var pinnedImage = regexp.MustCompile(`ghcr\.io/[A-Za-z0-9._-]+/control-plane:([A-Za-z0-9._-]+)`)

// imageEntrypoint matches a command a document tells an operator to run from
// inside that image, such as `node apps/api/src/backup-cli.ts create-org`.
//
// This is the derivation that makes the check worth having. The gate does not
// carry a list of files the procedure needs; it reads the procedure.
var imageEntrypoint = regexp.MustCompile(`node (apps/api/src/[A-Za-z0-9._/-]+\.ts)`)

// dockerfileCopy matches a COPY in the image's Dockerfile, so the mapping from
// a path inside the image back to a path in this repository is read out of the
// file that creates it rather than remembered here.
var dockerfileCopy = regexp.MustCompile(`(?m)^COPY\s+([A-Za-z0-9._/-]+)\s+\./([A-Za-z0-9._/-]+)\s*$`)

// shaTag matches the tag shape every automatic publish uses: the short commit
// the image was built from, which is what makes an offline check possible.
var shaTag = regexp.MustCompile(`^(?:main|cd)-([0-9a-f]{7,40})$`)

// checkPinnedImage verifies that the image the documentation tells an operator
// to run can complete the procedure the same page describes.
//
// WHY THIS EXISTS. README and two self-hosting pages pinned
// control-plane:v0.1.1 for months. That image records revision 6a2ee3d, eight
// hundred commits behind main, and `web/apps/api/src/backup-cli.ts` does not
// exist at that commit. Steps 3 and 4 of a four step bring-up whose own text
// says "the order is not optional" were `node apps/api/src/backup-cli.ts`, so
// an operator following the page got as far as a running server with no
// organization and no owner and no explanation. The image also has no /readyz,
// which docs/reference/control-plane.md names as the deploy gate. Every
// documentation gate was green the whole time, because a version tag is not a
// path and nothing here had ever asked whether a sentence was true.
//
// A VERSION TAG IS REFUSED, and that is the important design decision. The
// v0.1.1 IMAGE and the v0.1.1 GIT TAG are different trees: the tag was cut for
// a CLI release before the Dockerfile existed, and the image was published
// later from a different ref by workflow_dispatch, which
// control-plane-image.yml explains in its own comments. So a version tag tells
// a reader nothing about what is inside the image and tells this gate nothing
// it can check without a network. A `main-<sha>` or `cd-<sha>` tag names the
// commit it was built from, which is exactly what makes both possible.
func checkPinnedImage(root string, out io.Writer) error {
	docs, err := publishedDocs(root)
	if err != nil {
		return err
	}

	type pin struct{ tag, where string }
	var pins []pin
	var entrypoints []claim

	for _, doc := range docs {
		body, err := os.ReadFile(filepath.Join(root, doc))
		if err != nil {
			return fmt.Errorf("reading %s: %w", doc, err)
		}
		lines := strings.Split(string(body), "\n")
		hasPin := false
		for i, line := range lines {
			for _, m := range pinnedImage.FindAllStringSubmatch(line, -1) {
				hasPin = true
				pins = append(pins, pin{tag: m[1], where: fmt.Sprintf("%s:%d", doc, i+1)})
			}
		}
		if !hasPin {
			// A page that runs an entrypoint without naming an image is
			// describing a host install, not the image, so it is not this
			// check's business.
			continue
		}
		for i, line := range lines {
			for _, m := range imageEntrypoint.FindAllStringSubmatch(line, -1) {
				entrypoints = append(entrypoints, claim{text: m[1], file: doc, line: i + 1})
			}
		}
	}

	if len(pins) == 0 {
		report(out, "claimcheck: no published page pins a control plane image, so none was checked\n")
		return nil
	}

	// One tag, everywhere. Two pages pinning different images is how a reader
	// ends up following half of one procedure against the other's image.
	tag := pins[0].tag
	var disagree []string
	for _, p := range pins[1:] {
		if p.tag != tag {
			disagree = append(disagree, p.where+" pins "+p.tag)
		}
	}
	if len(disagree) > 0 {
		report(out, "\nclaimcheck: the published pages do not agree on which image to run.\n")
		report(out, "  %s pins %s\n", pins[0].where, tag)
		for _, d := range disagree {
			report(out, "  %s\n", d)
		}
		return fmt.Errorf("%d pages pin a different control plane image", len(disagree)+1)
	}

	commit, err := imageCommit(root, tag)
	if err != nil {
		report(out, "\nclaimcheck: the pinned image %s cannot be resolved to a commit in this repository.\n", tag)
		report(out, "  %v\n", err)
		report(out, "  Pin a tag this repository publishes automatically, main-<short sha> or\n")
		report(out, "  cd-<sha>, which names the commit the image was built from. A version tag\n")
		report(out, "  is refused: the only one ever published was built from a different ref\n")
		report(out, "  than the git tag of the same name, so it describes nothing checkable.\n")
		report(out, "  If the tag looks right, this job needs the full history: fetch-depth: 0.\n")
		return fmt.Errorf("the pinned control plane image %s names no commit here", tag)
	}

	prefix, err := imagePathPrefix(root)
	if err != nil {
		return err
	}

	var missing []string
	for _, e := range entrypoints {
		repoPath := prefix + strings.TrimPrefix(e.text, "apps/api/")
		if !existsAt(root, commit, repoPath) {
			missing = append(missing, fmt.Sprintf("%s:%d runs %s, and %s does not exist at %s",
				e.file, e.line, e.text, repoPath, tag))
		}
	}

	// The readiness endpoint is checked by name rather than derived, because
	// it is named as the deploy gate in docs/reference/control-plane.md and an
	// image without it fails that gate rather than the procedure above. The
	// v0.1.1 image is exactly this case: no /readyz at all.
	if !mentionsAt(root, commit, "readyz", "web/apps/api/src") {
		missing = append(missing, fmt.Sprintf(
			"docs/reference/control-plane.md names /readyz as the deploy gate, and nothing under web/apps/api/src answers it at %s", tag))
	}

	if len(missing) > 0 {
		report(out, "\nclaimcheck: the pinned control plane image cannot complete the documented procedure.\n")
		report(out, "  %s was built from %s\n", tag, commit[:min(12, len(commit))])
		for _, m := range missing {
			report(out, "  %s\n", m)
		}
		report(out, "  Publish a newer image and repin, or change the procedure.\n")
		return fmt.Errorf("%d documented step(s) the pinned image cannot run", len(missing))
	}

	report(out, "claimcheck: %d pin(s) of %s, built from %s, and all %d documented step(s) exist there\n",
		len(pins), tag, commit[:min(12, len(commit))], len(entrypoints))
	return nil
}

// publishedDocs lists the pages a customer reads. docs/plan is deliberately
// absent: it is this project's own working notes, it discusses the wrong pin
// as a known problem, and a gate that failed on a note describing a defect
// would teach people to stop writing the notes down.
func publishedDocs(root string) ([]string, error) {
	docs := []string{"README.md"}
	dir := filepath.Join(root, "docs", "src", "content", "docs")
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || (filepath.Ext(p) != ".md" && filepath.Ext(p) != ".mdx") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		docs = append(docs, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking the documentation tree: %w", err)
	}
	sort.Strings(docs)
	return docs, nil
}

// imageCommit resolves a published image tag to the commit it was built from.
func imageCommit(root, tag string) (string, error) {
	m := shaTag.FindStringSubmatch(tag)
	if m == nil {
		return "", fmt.Errorf("%q is not a main-<sha> or cd-<sha> tag", tag)
	}
	cmd := exec.Command("git", "-C", root, "rev-parse", "--verify", m[1]+"^{commit}")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git does not know the commit %s", m[1])
	}
	return strings.TrimSpace(string(out)), nil
}

// imagePathPrefix reads the Dockerfile to learn where in this repository the
// image's apps/api directory comes from, so the mapping cannot drift from the
// COPY that creates it.
func imagePathPrefix(root string) (string, error) {
	const dockerfile = "deploy/docker/control-plane.Dockerfile"
	body, err := os.ReadFile(filepath.Join(root, dockerfile))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", dockerfile, err)
	}
	for _, m := range dockerfileCopy.FindAllStringSubmatch(string(body), -1) {
		if strings.TrimSuffix(m[2], "/") != "apps/api" {
			continue
		}
		// The Dockerfile copies package.json into ./apps/api/ first, to warm
		// the dependency layer, and the whole directory later. Only the second
		// one describes where a source file comes from, and the difference
		// between them is that its source is a directory. Picking by position
		// would silently break the first time somebody adds a layer.
		src := strings.TrimSuffix(m[1], "/")
		info, err := os.Stat(filepath.Join(root, src))
		if err != nil || !info.IsDir() {
			continue
		}
		return src + "/", nil
	}
	return "", fmt.Errorf("%s no longer copies anything to ./apps/api, so a documented "+
		"`node apps/api/...` command cannot be traced back to a file here", dockerfile)
}

// existsAt reports whether a path exists in the tree of one commit.
func existsAt(root, commit, p string) bool {
	cmd := exec.Command("git", "-C", root, "cat-file", "-e", commit+":"+p)
	return cmd.Run() == nil
}

// mentionsAt reports whether a string appears anywhere under a path at one
// commit. Used for behaviour that is not a file: an endpoint the server
// answers.
func mentionsAt(root, commit, needle, under string) bool {
	cmd := exec.Command("git", "-C", root, "grep", "-q", "-F", needle, commit, "--", under)
	return cmd.Run() == nil
}

// ---------------------------------------------------------------------------
// What the site says about the code.
// ---------------------------------------------------------------------------

// siteTrees are the surfaces a customer reads. www is here because until now
// NOTHING opened a www file to ask whether a sentence in it was true: this
// tool read three markdown documents, and the site's own forbidden-token scan
// takes only *.md and *.mdx, of which www has one. Four of the false claims
// this rule set encodes were on www and every documentation gate was green.
var siteTrees = []string{
	"www", "docs/src/content/docs", "docs/src/pages", "docs/adr",
	"README.md", "ee/README.md", "CONTRIBUTING.md", "SECURITY.md",
}

// siteClaim is one sentence the site must not make, and the code that settles
// it.
//
// THE SHAPE IS TWO SIDED ON PURPOSE, and the second side is the part that
// makes this more than a list of banned words. `premise` names a file and a
// string that must be present for the rule to mean anything. If the engine
// grows real mutual TLS, or the sampling is removed, or the hosted control
// plane is withdrawn, the premise stops holding and THIS GATE FAILS rather
// than going on refusing a sentence that has become true. A ban with no expiry
// condition is how a gate ends up enforcing yesterday's world.
//
// `requires` is the other half: a page may make the claim in `forbidden` only
// if it also carries this. It exists for claims that are true as far as they
// go and misleading alone.
type siteClaim struct {
	name      string
	forbidden *regexp.Regexp
	requires  *regexp.Regexp
	premise   [2]string
	reason    string
}

var siteClaims = []siteClaim{
	{
		name:      "mutual TLS to the control plane",
		forbidden: regexp.MustCompile(`(?i)\bmutual TLS\b|\bmTLS\b`),
		// TWO SIDED, and it started as a plain ban. `analytics` hit the same
		// trap on a different gate: a substring cannot tell a claim from its
		// negation, and a pattern a true sentence can contain pushes whoever
		// hits it AWAY from the true wording. "this is ordinary TLS rather
		// than mutual TLS" is the sentence this page has to be able to write,
		// and the first version of this rule refused it. A lookbehind would
		// paper over that one phrasing and fail on the next, so the rule asks
		// for the denial instead: a page may name mutual TLS only where it
		// also says the engine does not use it.
		requires: regexp.MustCompile(`(?i)not a client certificate|rather than mutual TLS|never used mutual TLS`),
		premise: [2]string{"engine/internal/controlplane/client.go",
			`req.Header.Set("authorization", "Bearer "+c.token)`},
		reason: "the engine authenticates with a bearer token, not a client certificate, " +
			"and the token is good for ninety days (CLI_TOKEN_TTL_MS). The architecture " +
			"page claimed short-lived mTLS four times and both halves were false. If " +
			"client certificates are ever really used, delete this rule; the premise " +
			"beside it is what tells you the day that happens",
	},
	{
		name:      "there is no hosted control plane",
		forbidden: regexp.MustCompile(`(?i)no hosted control plane`),
		premise:   [2]string{"www/components/AuthScreen.tsx", "invitation only"},
		reason: "the hosted control plane is deployed and invitation only, and " +
			"AuthScreen says so while offering sign-in with GitHub. The sentence " +
			"survived as the meta description of /signin and /signup and inside the " +
			"modal the header's Log in button opens, so an invited customer pressing " +
			"Log in was told the thing they were invited to does not exist",
	},
	{
		name:      "the build runs inside the sandbox",
		forbidden: regexp.MustCompile(`builds and runs your services inside a sandbox`),
		premise:   [2]string{"engine/internal/build/docker.go", "dockerbuild.ImageBuildOptions{"},
		reason: "ImageBuildOptions sets no NetworkMode and the buildpack path runs " +
			"`npm ci` and `pip install`, so a build necessarily has a route out. The " +
			"containment is real and applies to the running services. This string " +
			"feeds JSON-LD and llms.txt, so the loose word travelled furthest",
	},
	{
		name:      "the scanner reads every row",
		forbidden: regexp.MustCompile(`every column of every table`),
		requires:  regexp.MustCompile(`(?i)sampl`),
		premise:   [2]string{"engine/internal/verify/scan.go", "DefaultSampleSize"},
		reason: "columns are exhaustive and ROWS ARE SAMPLED, up to DefaultSampleSize " +
			"per column. The engine discloses it in the attestation and no customer " +
			"facing page did. A page may describe the read-back, and it has to say " +
			"that the rows are a sample when it does",
	},
	{
		name:      "there is no production deployment",
		forbidden: regexp.MustCompile(`(?i)no production deployment`),
		// Two sided for the same reason as the rule above: the correction has
		// to quote the sentence it is correcting, and an honest-disclosure
		// page that cannot name the item it got wrong is worth less than one
		// that can.
		requires: regexp.MustCompile(`(?i)production was deployed|production is deployed`),
		premise:  [2]string{".github/workflows/cd.yml", "PRODUCTION_URL"},
		reason: "app.antifailure.dev/readyz returns ready, and staging is a separate, " +
			"newer deployment at app.dev.antifailure.dev serving a different commit. The " +
			"sentence sat on the DPA inside the paragraph explaining that a security " +
			"review will find every gap on the list, so a reviewer checking the address " +
			"our own README gives them disproved it in thirty seconds and then had cause " +
			"to doubt every other line on a page whose only asset is that it can be checked",
	},
	{
		name:      "a documentation page count nothing counted",
		forbidden: regexp.MustCompile(`\b[0-9]+ documentation pages\b`),
		premise:   [2]string{"www/lib/docs-facts.ts", "documentationPageCount"},
		reason: "llms.txt said 41 and there were 81, and the same number sat in a " +
			"comment in docs/src/pages/llms-full.txt.ts. The audience is what makes it " +
			"expensive: llms.txt is read by models deciding whether one fetch will " +
			"answer a question, so understating by half is advice to crawl the site " +
			"instead. Interpolate documentationPageCount() rather than typing a number",
	},
}

// checkSiteClaims fails the build on a sentence the code contradicts.
//
// It is deliberately narrow. It does not try to read prose for meaning; every
// rule here is a claim that was actually published, actually false, and can be
// matched exactly, with the file that settles it named beside it. A gate that
// guessed would fire on correct sentences and be deleted within a week, which
// is the reasoning constcheck writes down for the same restraint.
func checkSiteClaims(root string, out io.Writer) error {
	files, err := siteFiles(root)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no site files found under %s, so this check is looking in the wrong place",
			strings.Join(siteTrees, ", "))
	}

	// The premises first. A rule whose premise has gone is a rule about a world
	// that no longer exists, and it must stop the build rather than keep
	// refusing a sentence that may have become true.
	var lapsed []string
	for _, c := range siteClaims {
		body, err := os.ReadFile(filepath.Join(root, c.premise[0]))
		if err != nil {
			lapsed = append(lapsed, fmt.Sprintf("%s: %s is gone", c.name, c.premise[0]))
			continue
		}
		if !strings.Contains(string(body), c.premise[1]) {
			lapsed = append(lapsed, fmt.Sprintf("%s: %s no longer contains %q",
				c.name, c.premise[0], c.premise[1]))
		}
	}
	if len(lapsed) > 0 {
		report(out, "\nclaimcheck: a rule about the site rests on code that has changed.\n")
		for _, l := range lapsed {
			report(out, "  %s\n", l)
		}
		report(out, "  The claim may now be true. Re-read the rule's reason and either\n")
		report(out, "  update its premise or delete the rule.\n")
		return fmt.Errorf("%d site claim rule(s) rest on code that is gone", len(lapsed))
	}

	var hits []string
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return fmt.Errorf("reading %s: %w", f, err)
		}
		text := string(body)
		lines := strings.Split(text, "\n")
		for _, c := range siteClaims {
			for i, line := range lines {
				if !c.forbidden.MatchString(line) {
					continue
				}
				if c.requires != nil && c.requires.MatchString(text) {
					continue
				}
				if allowed(f, c.name, line) {
					continue
				}
				hits = append(hits, fmt.Sprintf("%s:%d  %s\n      %s\n      %s",
					f, i+1, c.name, strings.TrimSpace(line), c.reason))
			}
		}
	}

	if len(hits) > 0 {
		report(out, "\nclaimcheck: the site says something the code contradicts.\n")
		for _, h := range hits {
			report(out, "  %s\n", h)
		}
		return fmt.Errorf("%d claim(s) the code contradicts", len(hits))
	}

	var stale []string
	for i, e := range siteClaimExceptions {
		if !usedException[i] {
			stale = append(stale, fmt.Sprintf("%s (%s) excusing %q", e.file, e.rule, e.line))
		}
	}
	if len(stale) > 0 {
		report(out, "\nclaimcheck: an exception that excuses nothing.\n")
		for _, st := range stale {
			report(out, "  %s\n", st)
		}
		report(out, "  Delete it. An exception nobody needs is a licence nobody granted,\n")
		report(out, "  and it would silently cover a future line that happens to match.\n")
		return fmt.Errorf("%d stale site claim exception(s)", len(stale))
	}

	report(out, "claimcheck: %d site files, %d claim rules, %d line exceptions, 0 contradicted\n",
		len(files), len(siteClaims), len(siteClaimExceptions))
	return nil
}

// siteFiles lists the customer facing source and prose, tracked by git so the
// answer does not depend on what happens to be on disk.
func siteFiles(root string) ([]string, error) {
	args := append([]string{"-C", root, "ls-files", "-z", "--"}, siteTrees...)
	outBytes, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("asking git for the site's files: %w", err)
	}
	var files []string
	for _, f := range strings.Split(string(outBytes), "\x00") {
		if f == "" {
			continue
		}
		switch filepath.Ext(f) {
		case ".ts", ".tsx", ".md", ".mdx", ".mjs", ".js", ".json", ".txt":
		default:
			continue
		}
		// www/out is the static export: a copy of prose the source has already
		// corrected, which is why constcheck prunes it too.
		if strings.HasPrefix(f, "www/out/") || strings.Contains(f, "/node_modules/") {
			continue
		}
		files = append(files, f)
	}
	sort.Strings(files)
	return files, nil
}

// claimException allows one line to say a forbidden thing.
//
// The reason this exists at all is that the honest fix for a false claim is
// usually a sentence saying what the claim used to be and why it was wrong.
// The architecture page has to write "ordinary TLS rather than mutual TLS" to
// make the correction legible, and a gate that made that unsayable would push
// every correction towards being made silently, which is the opposite of what
// this repository wants.
//
// It is scoped to a LINE, not a file. `line` must appear in the offending line
// for the exception to apply, so a genuinely new claim elsewhere in the same
// file still fails. A file-wide exemption would have quietly re-opened every
// page that ever carried a correction.
type claimException struct {
	file, rule, line, reason string
}

// siteClaimExceptions are checked for staleness: one that excuses nothing is a
// licence nobody granted, and it fails the build. Same rule as notAPath above,
// for the same reason.
var siteClaimExceptions = []claimException{
	{
		file: "www/components/AuthScreen.tsx",
		rule: "there is no hosted control plane",
		line: "The page said",
		reason: "the header comment recording what this screen used to say and why it " +
			"changed, which is the note that stops somebody restoring it",
	},
	{
		file: "www/components/AuthModal.tsx",
		rule: "there is no hosted control plane",
		line: "This said",
		reason: "the same retraction on the modal, which is the surface the header's " +
			"Log in button opens and where the claim did the most damage",
	},
	{
		file: "www/lib/docs-facts.ts",
		rule: "a documentation page count nothing counted",
		line: "offered \"all 41 documentation pages",
		reason: "the header recording the number this file exists to stop anybody " +
			"typing again, which has to quote it to be worth reading",
	},
	{
		file: "www/lib/routes.ts",
		rule: "there is no hosted control plane",
		line: "Both descriptions used to say",
		reason: "the note recording that these two meta descriptions carried the claim " +
			"while the visible page said the opposite",
	},
}

// used records which exceptions actually excused something, so a stale one can
// be reported.
var usedException = map[int]bool{}

func allowed(file, rule, line string) bool {
	for i, e := range siteClaimExceptions {
		if e.file == file && e.rule == rule && strings.Contains(line, e.line) {
			usedException[i] = true
			return true
		}
	}
	return false
}
