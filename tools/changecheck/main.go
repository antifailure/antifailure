package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// categories are the four a fragment's first line may take. README.md names
// exactly these, and the site renders one label per category, so a fifth
// spelling is a fragment the changelog cannot file. One already existed:
// `# docs`, written once and never noticed, because nothing read these files.
var categories = map[string]bool{
	"added": true, "fixed": true, "changed": true, "security": true,
}

// escapeHatch is the trailer that says a change deliberately carries no
// fragment, and the reason is required rather than decorative: it is what a
// reviewer reads and what `git log --grep` finds later.
const escapeHatch = "Changelog-None:"

func main() {
	rangeFlag := flag.String("range", "", "base..head, instead of deriving one")
	flag.Parse()
	root := "."
	if args := flag.Args(); len(args) > 0 {
		root = args[0]
	}

	if err := validateFragments(filepath.Join(root, ".changes")); err != nil {
		fmt.Fprintf(os.Stderr, "changecheck: %v\n", err)
		os.Exit(1)
	}

	base, head, why, err := resolveRange(root, *rangeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "changecheck: %v\n", err)
		os.Exit(1)
	}
	if base == "" {
		// Nothing to compare against: a brand new branch with no shared
		// history, or a shallow checkout that cannot see one. Say so rather
		// than passing quietly, because a gate that examined nothing and
		// printed ok is the failure this repository names most often.
		fmt.Printf("changecheck: no base to compare against (%s), so nothing was examined\n", why)
		return
	}

	changed, err := git(root, "diff", "--name-only", "--diff-filter=ACMRD", base, head)
	if err != nil {
		fmt.Fprintf(os.Stderr, "changecheck: reading the diff: %v\n", err)
		os.Exit(1)
	}
	paths := lines(changed)

	var hits []finding
	for _, p := range paths {
		if s, ok := Requires(p); ok {
			hits = append(hits, finding{path: p, why: s.why})
		}
	}
	if len(hits) == 0 {
		fmt.Printf("changecheck: %d changed %s, none of it behaviour anybody outside can see\n",
			len(paths), plural(len(paths), "path", "paths"))
		return
	}

	// Added, not modified. A branch that edits a fragment somebody else
	// landed has not described its own change, and accepting a modification
	// hands a free pass to every other path in the same branch. That was not
	// hypothetical: the first run of this gate's own proof passed because an
	// unrelated commit in the range had corrected a category heading.
	added, err := git(root, "diff", "--name-only", "--diff-filter=A", base, head, "--", ".changes")
	if err != nil {
		fmt.Fprintf(os.Stderr, "changecheck: reading .changes: %v\n", err)
		os.Exit(1)
	}
	var fragments []string
	for _, f := range lines(added) {
		if strings.HasSuffix(f, ".md") {
			fragments = append(fragments, f)
		}
	}
	if len(fragments) > 0 {
		fmt.Printf("changecheck: %d %s a user could notice, described by %s\n",
			len(hits), plural(len(hits), "change", "changes"), strings.Join(fragments, ", "))
		return
	}

	if reason, sha := exemption(root, base, head); reason != "" {
		fmt.Printf("changecheck: %d %s a user could notice, and %s says why there is no fragment:\n  %s\n",
			len(hits), plural(len(hits), "change", "changes"), sha, reason)
		return
	}

	refuse(hits)
}

type finding struct{ path, why string }

// refuse prints what changed, which surface says it counts, and the two ways
// out. The order matters: the fragment first, because it is nearly always the
// right answer, and the trailer second, spelled out, because an escape hatch
// nobody can find is one people work around by weakening the gate.
func refuse(hits []finding) {
	fmt.Fprintf(os.Stderr, "changecheck: this changes something a user can see and says nothing about it.\n\n")

	bySurface := map[string][]string{}
	for _, h := range hits {
		bySurface[h.why] = append(bySurface[h.why], h.path)
	}
	var whys []string
	for w := range bySurface {
		whys = append(whys, w)
	}
	sort.Strings(whys)
	for _, w := range whys {
		fmt.Fprintf(os.Stderr, "  %s\n", w)
		for _, p := range bySurface[w] {
			fmt.Fprintf(os.Stderr, "      %s\n", p)
		}
	}

	fmt.Fprintf(os.Stderr, `
Write a fragment. One file, named for the change, first line one of
"# added", "# fixed", "# changed" or "# security", then prose that says what
changed and what it means for somebody using it:

    .changes/<slug>.md

If the change is real but genuinely invisible to a user, name it
.changes/<slug>.internal.md instead. Those are kept and never published.

If it needs no fragment at all, say so in the commit and say why:

    git commit -s --trailer "Changelog-None: a lint fix with no behaviour change"

The reason is the point. It is what a reviewer reads and what the next person
finds with git log --grep, and it is why this is a trailer rather than a label.

CONTRIBUTING.md has promised this gate since the first week and there was
none, which is how 65 of the first 80 commits ended up with no sign-off
either. The published changelog at /changelog is built from these files, so a
change with no fragment is a change nobody outside this repository hears about.
`)
	os.Exit(1)
}

// validateFragments reads every fragment in the tree, not only the ones this
// range touched.
//
// Reading only the new ones would catch a bad fragment at creation and never
// notice one that arrived some other way, and the corpus is valid the day this
// lands, so the only thing that can make this fire is a fragment somebody
// wrote wrong. The renderer at www/lib/changelog.ts parses exactly this
// grammar, so a fragment that fails here is one the site cannot file.
func validateFragments(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	var problems []string
	seen := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		seen++
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		for _, p := range fragmentProblems(string(body)) {
			problems = append(problems, fmt.Sprintf("%s: %s", e.Name(), p))
		}
	}
	if seen == 0 {
		return fmt.Errorf("found no fragments in %s at all, which cannot be right", dir)
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("%d %s the changelog cannot file:\n  %s\n\n"+
		"The first line is one of \"# added\", \"# fixed\", \"# changed\" or \"# security\",\n"+
		"and every heading is followed by prose. www/lib/changelog.ts reads this\n"+
		"grammar and nothing else, so a fragment outside it is one the published\n"+
		"changelog silently drops",
		len(problems), plural(len(problems), "fragment", "fragments"),
		strings.Join(problems, "\n  "))
}

// fragmentProblems returns what is wrong with one fragment, in the terms the
// author can act on. Empty means it parses.
func fragmentProblems(body string) []string {
	var out []string
	all := strings.Split(body, "\n")
	if len(all) == 0 || !strings.HasPrefix(all[0], "# ") {
		return []string{"the first line is not a category heading"}
	}
	headings := 0
	prose := map[int]bool{}
	current := -1
	for _, line := range all {
		if strings.HasPrefix(line, "# ") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "# "))
			if !categories[name] {
				out = append(out, fmt.Sprintf("%q is not one of added, fixed, changed, security", name))
			}
			headings++
			current = headings
			continue
		}
		if strings.HasPrefix(line, "#") {
			out = append(out, fmt.Sprintf("%q is a heading the changelog has no place for; a fragment is one level deep", strings.TrimSpace(line)))
			continue
		}
		if strings.TrimSpace(line) != "" && current > 0 {
			prose[current] = true
		}
	}
	for i := 1; i <= headings; i++ {
		if !prose[i] {
			out = append(out, fmt.Sprintf("heading %d has no prose under it", i))
		}
	}
	return out
}

// exemption looks for the escape hatch on any commit in the range, and returns
// the reason and the commit that carried it.
//
// Any commit, not the tip, because a branch is amended and rebased and the
// commit that needed the exemption is rarely the last one. A trailer with no
// reason after it is not an exemption: it is the label this deliberately
// refused to accept, spelled differently.
func exemption(root, base, head string) (reason, sha string) {
	out, err := git(root, "log", "--format=%H%x1f%B%x1e", base+".."+head)
	if err != nil {
		return "", ""
	}
	for _, record := range strings.Split(out, "\x1e") {
		parts := strings.SplitN(record, "\x1f", 2)
		if len(parts) != 2 {
			continue
		}
		commit := strings.TrimSpace(parts[0])
		for _, line := range lines(parts[1]) {
			if !strings.HasPrefix(line, escapeHatch) {
				continue
			}
			r := strings.TrimSpace(strings.TrimPrefix(line, escapeHatch))
			if len(r) >= 10 {
				return r, short(commit)
			}
		}
	}
	return "", ""
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// resolveRange works out what this change is, from whichever of the three
// places knows: the flag, the workflow's environment, or the local branch.
//
// The pull request case is the one worth reading. The checkout is the MERGE
// commit, so HEAD^1 is the base branch tip and HEAD^2 is the branch, and the
// merge base of those two is where the branch started. Diffing from the merge
// base rather than from HEAD^1 is what keeps main's own commits out of this
// branch's file list; the sign-off gate learned the same lesson from the other
// direction, when a moving base made it blame authors for somebody else's
// commit.
func resolveRange(root, explicit string) (base, head, why string, err error) {
	if explicit != "" {
		parts := strings.SplitN(explicit, "..", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", "", fmt.Errorf("-range wants base..head, got %q", explicit)
		}
		mb, err := mergeBase(root, parts[0], parts[1])
		if err != nil {
			return "", "", "", err
		}
		return mb, parts[1], "the range given on the command line", nil
	}

	if prBase := os.Getenv("AF_CHANGECHECK_BASE"); prBase != "" {
		if _, err := git(root, "rev-parse", "--verify", "HEAD^2"); err == nil {
			mb, err := mergeBase(root, "HEAD^1", "HEAD^2")
			if err != nil {
				return "", "", "", err
			}
			return mb, "HEAD^2", "the pull request, from where it left the base branch", nil
		}
		mb, err := mergeBase(root, prBase, "HEAD")
		if err != nil {
			return "", "", "", err
		}
		return mb, "HEAD", "the pull request's base sha", nil
	}

	if before := os.Getenv("AF_CHANGECHECK_BEFORE"); before != "" &&
		before != strings.Repeat("0", 40) {
		if _, err := git(root, "cat-file", "-e", before+"^{commit}"); err == nil {
			return before, "HEAD", "the commits this push added", nil
		}
	}

	// Locally, and on a first push of a new branch, the base is main.
	for _, ref := range []string{"origin/main", "main"} {
		if mb, err := mergeBase(root, ref, "HEAD"); err == nil && mb != "" {
			return mb, "HEAD", "where this branch left " + ref, nil
		}
	}
	return "", "", "no ref named main or origin/main to branch from", nil
}

func mergeBase(root, a, b string) (string, error) {
	out, err := git(root, "merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("no merge base between %s and %s: %w", a, b, err)
	}
	return strings.TrimSpace(out), nil
}

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimRight(l, "\r"))
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
