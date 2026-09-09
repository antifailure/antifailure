// Command gendrift names the generated files that no longer match their
// generator, and the command that regenerates each one.
//
// The failure it exists for, measured on 2026-09-08: eleven of the sixteen
// open pull requests whose engine check was red failed one step, and the step
// is named "Generated files are current". It was WRONG about three of them.
// That step runs the generators and then compares, and four of the generators
// are `go test <pkg> -update-something`, which runs the whole package. So a
// test in engine/internal/cli that has nothing to do with the committed
// reference fails, and the pull request is told its generated files are stale
// when they are not. Three lanes were sent to look at the wrong thing.
//
// The other eight were real drift, and there the step printed a diff and
// stopped. A diff says WHICH file moved. It does not say which of the thirteen
// generators owns it, and `just generate` is the whole set, several minutes of
// npm and Docker, for a lane that needed one of them. Every one of those eight
// branches had edited documentation and never regenerated
// engine/internal/docs/pages.gen.go, which `go run ./tools/docsembed` rewrites
// in about a second.
//
// So the ledger below is the answer to "what do I run", and it is a table
// rather than a sentence in a comment because a sentence cannot be checked.
// Each row was read out of the generator that writes the path, not recalled:
// engine/internal/events/stream.register.json is written by
// `go run ./tools/eventcheck -freeze .` and NOT by the neighbouring
// `go test ./internal/events -update-schema`, which is the pairing anybody
// reading the justfile in order would guess wrong.
//
// This also replaces a hand maintained list. `just _generated` compared a
// literal list of paths spelled out in the justfile, and CI compared the whole
// tree, so the two gates asked different questions and only one of them could
// notice a generator that started writing somewhere new. Now both ask this.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// generator is one command and everything it writes.
//
// A path is a repository relative path, and a directory stands for everything
// under it: schemadoc renders a page per schema, so naming the directory is
// what lets a new schema arrive without editing this file, and hud writes one
// golden frame per case for the same reason.
type generator struct {
	command string
	paths   []string
}

// ledger is every generated artifact this repository commits.
//
// The order is the order the generators run in, so that somebody reading this
// beside the workflow or the justfile can follow both at once.
var ledger = []generator{
	{"go run ./tools/errgen", []string{
		"www/public/errors.v1.json",
		"engine/internal/errors/codes.gen.go",
		"docs/src/content/docs/reference/errors.md",
	}},
	{"go run ./tools/lintgen", []string{
		"www/public/lint-findings.v1.json",
		"engine/internal/insights/findings.gen.go",
		"engine/internal/insights/findings.register.json",
		"docs/src/content/docs/reference/lint-findings.md",
	}},
	{"go run ./tools/proxysrc", []string{
		"engine/internal/proxyimage/sources.gen.go",
	}},
	{"go run ./tools/schemadoc .", []string{
		"docs/src/content/docs/reference/schemas",
	}},
	{"go run ./tools/notices -out THIRD_PARTY_NOTICES.md", []string{
		"THIRD_PARTY_NOTICES.md",
	}},
	{"cp schemas/manifest.v1.json engine/internal/manifest/manifest.v1.json", []string{
		"engine/internal/manifest/manifest.v1.json",
	}},
	{"cd engine && go test ./internal/policy -update-vectors", []string{
		"schemas/policy-vectors.json",
	}},
	{"cd engine && go test ./internal/mockpack -update-vectors", []string{
		"schemas/mockpack-vectors.json",
	}},
	{"cd engine && go test ./internal/webhook -update-vectors", []string{
		"schemas/webhook-vectors.json",
	}},
	{"cd engine && go test ./internal/cli -update-reference", []string{
		"docs/src/content/docs/reference/cli.md",
	}},
	{"cd engine && go test ./internal/events -update-schema", []string{
		"schemas/events.v1.json",
	}},
	{"go run ./tools/eventcheck -freeze .", []string{
		"engine/internal/events/stream.register.json",
	}},
	{"cd engine && go test ./internal/masking -update-transforms", []string{
		"docs/src/content/docs/reference/transforms.md",
	}},
	{"cd engine && go test ./internal/hud -update-frames", []string{
		"engine/internal/hud/testdata",
		"docs/src/content/docs/guides/dashboard.md",
	}},
	{"go run ./tools/docsembed", []string{
		"engine/internal/docs/pages.gen.go",
	}},
}

func main() {
	strict := flag.Bool("strict", false,
		"also fail on a changed path no generator owns, for a clean checkout")
	flag.Parse()
	root := "."
	if args := flag.Args(); len(args) > 0 {
		root = args[0]
	}

	if err := run(root, *strict, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "gendrift: %v\n", err)
		os.Exit(1)
	}
}

// run compares the working tree against HEAD and reports drift.
//
// strict is the difference between the two callers. CI runs on a clean
// checkout, so anything at all that changed was written by a generator, and a
// changed path this ledger does not claim means a generator has started
// writing somewhere nothing is watching. A developer's `just gate` runs in the
// middle of an edit, where unowned changes are the edit itself, so there it
// looks only at what it owns. That difference is the reason the two gates
// could previously ask different questions, and naming it is what keeps them
// asking the same one.
func run(root string, strict bool, out io.Writer) error {
	if err := checkLedger(root); err != nil {
		return err
	}

	changed, err := changedPaths(root)
	if err != nil {
		return err
	}

	owned := map[string][]string{}
	var unowned []string
	for _, p := range changed {
		if g, ok := ownerOf(p); ok {
			owned[g] = append(owned[g], p)
			continue
		}
		unowned = append(unowned, p)
	}

	if len(owned) == 0 && (!strict || len(unowned) == 0) {
		// The write is returned rather than discarded. A gate whose only
		// output is a claim that it looked has to notice when that claim did
		// not reach anybody.
		_, err := fmt.Fprintf(out, "gendrift: %d generated %s match their generators\n",
			countPaths(), plural(countPaths(), "path", "paths"))
		return err
	}

	var b strings.Builder
	if len(owned) > 0 {
		b.WriteString("These committed files do not match what their generator just wrote.\n")
		b.WriteString("Run the command beside each one, or `just generate` to run them all.\n\n")
		for _, g := range ledger {
			hits, ok := owned[g.command]
			if !ok {
				continue
			}
			sort.Strings(hits)
			b.WriteString("  " + g.command + "\n")
			for _, p := range hits {
				b.WriteString("      " + p + "\n")
			}
		}
	}
	if strict && len(unowned) > 0 {
		if len(owned) > 0 {
			b.WriteString("\n")
		}
		sort.Strings(unowned)
		b.WriteString("These changed on a clean checkout and no generator in tools/gendrift\n")
		b.WriteString("claims them, so something writes them and nothing compares them:\n\n")
		for _, p := range unowned {
			b.WriteString("      " + p + "\n")
		}
		b.WriteString("\nAdd each one to the ledger beside the generator that writes it.\n")
	}
	return fmt.Errorf("%s", b.String())
}

// checkLedger refuses a ledger that has gone stale.
//
// A generated file that is renamed and not renamed here leaves a row pointing
// at nothing, and a row pointing at nothing can never report drift. The check
// would go on printing a number and would have stopped looking at that file,
// which is the exact defect this repository keeps finding in its own
// instruments. So a missing path is a failure and not a skip.
func checkLedger(root string) error {
	var missing []string
	for _, g := range ledger {
		for _, p := range g.paths {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
				missing = append(missing, p+" (from `"+g.command+"`)")
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("the ledger names %d %s that is not in the tree, so nothing compares it:\n      %s",
		len(missing), plural(len(missing), "path", "paths"), strings.Join(missing, "\n      "))
}

// changedPaths is everything git reports as modified, added or untracked.
//
// Untracked is included because a generator that writes a NEW file is the case
// a plain `git diff` cannot see at all: the file is not in the index, so the
// comparison passes and the artifact is never committed.
func changedPaths(root string) ([]string, error) {
	cmd := exec.Command("git", "status", "--porcelain=v1", "--untracked-files=all")
	cmd.Dir = root
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("reading the working tree: %w", err)
	}
	var paths []string
	for _, line := range strings.Split(string(stdout), "\n") {
		if len(line) < 4 {
			continue
		}
		p := strings.TrimSpace(line[3:])
		// A rename is reported as "old -> new" and the new name is the one
		// a generator wrote.
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		paths = append(paths, strings.Trim(p, `"`))
	}
	return paths, nil
}

// ownerOf is the generator that writes a path, if any owns it.
//
// A ledger entry that names a directory owns everything beneath it, and the
// separator is required so that `docs/reference/schemas` does not claim a
// sibling called `docs/reference/schemas-old`.
func ownerOf(path string) (string, bool) {
	for _, g := range ledger {
		for _, p := range g.paths {
			if path == p || strings.HasPrefix(path, p+"/") {
				return g.command, true
			}
		}
	}
	return "", false
}

func countPaths() int {
	n := 0
	for _, g := range ledger {
		n += len(g.paths)
	}
	return n
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
