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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// receiptName is the file `-generate` leaves behind to say it ran, and the
// only evidence `run` will accept that anything was rebuilt.
//
// The failure it exists for is the one that survived #363. That change made
// both callers run the ledger, so the three lists cannot disagree any more.
// It did not couple the two halves: `go run ./tools/gendrift .` on its own
// still ran no generator and still printed "N generated paths match their
// generators", which is a sentence about committed bytes compared against
// themselves. Drop or reorder the `-generate` step in ci.yml and the gate
// goes back to printing that line about a tree nobody rebuilt, which is
// exactly the state main was in for the thirteen commits
// engine/internal/manifest/manifest.v1.json sat stale.
//
// So "I could not check" is now a different answer from "I checked and it is
// clean", and it is a failure rather than a pass. A gate whose reassuring
// sentence can be true of a tree it never looked at is worse than no gate,
// because the sentence is what stops anybody asking.
//
// It is ignored by git and skipped by changedPaths, for two independent
// reasons that would each be enough: a receipt in a diff is noise, and a
// receipt reported as drift by the tool that wrote it is a gate arguing with
// itself.
const receiptName = ".gendrift-receipt.json"

// receiptVersion invalidates every receipt written by an older shape of this
// file. A field added here without it would be read as its zero value out of
// an old receipt and compared as if somebody had checked it.
const receiptVersion = 1

// receipt is what one complete run of `-generate` attests to.
//
// Head and Ledger are what make it about THIS tree rather than about some
// earlier one. A receipt left by a run at another commit says nothing about
// this one, and a ledger that gained a row since is a ledger whose new
// generator has never run, which is the whole defect one layer up.
type receipt struct {
	Version int    `json:"version"`
	Head    string `json:"head"`
	Ledger  string `json:"ledger"`
}

// ledgerFingerprint is a hash over every command and every path, in order.
//
// Order is included on purpose. docsembed has to run last or a single pass
// cannot converge, so a reordered ledger is a ledger that produces different
// bytes, and a receipt from before the reorder is not evidence about after it.
func ledgerFingerprint() string {
	var lines []string
	for _, g := range ledger {
		lines = append(lines, g.command)
		for _, p := range g.paths {
			lines = append(lines, "\t"+p)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// headSHA is the commit the generators ran against.
//
// A failure to read it is returned rather than swallowed. A receipt carrying
// an empty head would match another receipt carrying an empty head, so
// "git could not answer" would quietly become a passing comparison, which is
// the shape of every defect this tool is about.
func headSHA(root string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("reading HEAD, which a receipt has to name: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("git named no commit, so there is nothing a receipt could be about")
	}
	return sha, nil
}

// writeReceipt records a completed run of every generator in the ledger.
func writeReceipt(root string) error {
	head, err := headSHA(root)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(receipt{
		Version: receiptVersion,
		Head:    head,
		Ledger:  ledgerFingerprint(),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, receiptName), append(body, '\n'), 0o644)
}

// removeReceipt drops any earlier receipt.
//
// It runs BEFORE the generators rather than after a failure, and the ordering
// is the point. A run that dies at the eighth of fifteen generators leaves a
// half written tree, and if an earlier receipt survived that, the comparison
// would answer with full confidence about it. There is no ordering in which
// this file exists and the generators have not just finished.
func removeReceipt(root string) error {
	err := os.Remove(filepath.Join(root, receiptName))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clearing the previous receipt: %w", err)
	}
	return nil
}

// requireReceipt refuses to compare a tree no generator has rebuilt.
//
// The three refusals say different things because they are different facts,
// and this repository has been bitten every time two of those were printed
// under one sentence.
func requireReceipt(root string) error {
	body, err := os.ReadFile(filepath.Join(root, receiptName))
	if err != nil {
		return fmt.Errorf("no generator has run in this checkout, so NOTHING has been compared.\n" +
			"      `go run ./tools/gendrift -generate .` leaves a receipt naming the commit and\n" +
			"      the ledger it ran against, and this refuses to answer without one. Run it,\n" +
			"      or `just generate`, and then run this again.\n" +
			"      Without that step the comparison is the committed bytes against themselves,\n" +
			"      and it prints the same sentence a rebuilt clean tree prints. That sentence\n" +
			"      was printed on thirteen commits while\n" +
			"      engine/internal/manifest/manifest.v1.json sat stale on main")
	}

	var r receipt
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("the receipt at %s is not readable, so it says nothing about this tree: %w",
			receiptName, err)
	}
	if r.Version != receiptVersion {
		return fmt.Errorf("the receipt at %s was written by version %d of this tool and this is version %d,\n"+
			"      so what it attests to is not what is being asked. Run `-generate` again",
			receiptName, r.Version, receiptVersion)
	}

	head, err := headSHA(root)
	if err != nil {
		return err
	}
	if r.Head != head {
		return fmt.Errorf("the generators last ran against commit %s and this tree is at %s,\n"+
			"      so the receipt is about a different tree. Run `-generate` again",
			short(r.Head), short(head))
	}
	if r.Ledger != ledgerFingerprint() {
		return fmt.Errorf("the ledger has changed since the generators last ran, so at least one\n" +
			"      generator in it has never run here and the file it writes has never been\n" +
			"      compared. Run `-generate` again")
	}
	return nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func main() {
	strict := flag.Bool("strict", false,
		"also fail on a changed path no generator owns, for a clean checkout")
	generate := flag.Bool("generate", false,
		"run every generator in the ledger, in order, and compare nothing")
	flag.Parse()
	root := "."
	if args := flag.Args(); len(args) > 0 {
		root = args[0]
	}

	if *generate {
		if err := generateAll(root, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "gendrift: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := run(root, *strict, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "gendrift: %v\n", err)
		os.Exit(1)
	}
}

// generateAll runs every generator the ledger names, in the order it names
// them, and compares nothing.
//
// This mode exists because the list of generators was written out three times.
// The ledger below named fifteen; `just _generated` ran fifteen; ci.yml ran
// TWELVE. Three of the ledger's rows had no generator on the CI side, so on a
// clean checkout the files those rows name were never rewritten, never showed
// as changed, and could never be reported. `engine/internal/manifest/manifest.v1.json`
// was one of them, and it sat stale on main for thirteen commits while the
// step called "Generated files are current" passed on every one of them: the
// ledger row was decorative, because gendrift only ever asked `git status` and
// something else had to have done the writing.
//
// A ledger that names a generator and a workflow that runs a different set is
// the disagreement no gate here could see, and the way to make it unsayable is
// to stop saying it twice. Both callers run this now, so a row added to the
// ledger is a generator BOTH of them run, and the only remaining way to have a
// generated file nothing compares is to leave it out of the ledger entirely,
// which -strict is what catches.
//
// It stops at the first failure and says which command failed, because a
// generator that did not finish leaves a tree the comparison would read as
// drift. That is the reading that sent three lanes to the wrong file on
// 2026-09-08, and it is the reason this is a separate mode rather than a step
// folded into the comparison: the two answer different questions and must be
// able to fail with different words.
func generateAll(root string, out io.Writer) error {
	// Before anything, so that there is no window in which a stale receipt
	// vouches for a tree this run is part way through rewriting.
	if err := removeReceipt(root); err != nil {
		return err
	}
	for _, g := range ledger {
		if _, err := fmt.Fprintf(out, "  %s\n", g.command); err != nil {
			return err
		}
		cmd := exec.Command("bash", "-c", g.command)
		cmd.Dir = root
		cmd.Stdout = out
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			// The explanation goes to the writer and the error stays one
			// line. staticcheck's ST1005 refuses an error string that ends in
			// punctuation or a newline, and main prints this as
			// `gendrift: <err>`, which a paragraph does not sit under anyway.
			//
			// The write is returned rather than discarded, for the reason the
			// clean case below gives: the whole value of this sentence is that
			// somebody reads it, so a write that did not arrive has to be
			// noticed rather than swallowed.
			if _, werr := fmt.Fprintf(out,
				"\nThat is a generator that did not finish, and NOT a stale file. Nothing\n"+
					"has been compared. Fix the failure above and run this again before\n"+
					"reading anything into what the tree looks like now.\n\n"); werr != nil {
				return werr
			}
			return fmt.Errorf("the generator `%s` failed: %w", g.command, err)
		}
	}
	// Only now, with every generator finished. This is what the comparison
	// reads, and it is the difference between "I checked and it is clean" and
	// "I could not check", which printed the same sentence until it existed.
	if err := writeReceipt(root); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "gendrift: ran %d %s over %d generated %s, receipt in %s\n",
		len(ledger), plural(len(ledger), "generator", "generators"),
		countPaths(), plural(countPaths(), "path", "paths"), receiptName)
	return err
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
	// First, because every other answer below is a statement about a tree the
	// generators have just rewritten, and without this there is nothing
	// saying they did. checkLedger and the comparison both go on producing
	// confident output on a checkout where no generator ever ran.
	//
	// In both modes. A developer running this by hand is comparing committed
	// bytes against themselves exactly as CI would be, and a local pass that
	// means less than the CI pass is how the two gates start disagreeing
	// again.
	if err := requireReceipt(root); err != nil {
		return err
	}
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
		p = strings.Trim(p, `"`)
		// The tool's own receipt is not an artifact anybody generated, and
		// -strict would otherwise report it as a changed path no generator
		// claims. It is in .gitignore as well, so this is the second of two
		// independent reasons it stays out of the comparison rather than the
		// only one.
		if p == receiptName {
			continue
		}
		paths = append(paths, p)
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
