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

	".gate-reports/": "created by `just gate` when it runs and gitignored, so it is a place output goes rather than something the repository contains",

	"antifailure/antifailure-foss": "a GitHub repository name, not a path in this checkout",

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
	return checkDocsLinks(root, tracked, out)
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
