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
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
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
	return decide(tracked, claims, notAPath, out)
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
		fmt.Fprintf(out, "MISSING  %s:%d  %s\n", c.file, c.line, c.text)
	}
	for _, tok := range stale {
		fmt.Fprintf(out, "STALE    %s is listed as not-a-path and no document mentions it\n", tok)
	}
	fmt.Fprintf(out, "\nclaimcheck: %d path claims across %d documents, %d dead, %d stale exclusions\n",
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
