// Command constcheck fails the build when prose miscounts a closed set that is
// declared only in Go.
//
// The manifest schema, the error catalogue, the transform registry and the
// command tree all already have a gate, because each of them is declared in a
// machine readable file that a checker can read. The sets declared as Go
// constants had none, and that is where the worst instance of this defect was
// living: seventeen DDL lint rules, every one of which becomes a pull request
// finding, described in eleven places as six.
//
// Every instance of this defect found so far runs the same direction. Six for
// seventeen lint rules, twelve for thirteen analyzers, seven for nine
// migration tools, two for four database providers, three for four build
// strategies, five for six egress modes, twenty three for twenty four
// transforms. Not one overstates. That is not carelessness about truth, which
// would scatter both ways. It is documentation written accurately at one
// version and never revisited, so every later capability lands in the code and
// never in the sentence. It stays invisible because an overstatement is
// reported by the first user it fails, while an understatement produces a
// happy user who never learns the feature exists.
//
// WHAT THIS DELIBERATELY DOES NOT CHECK. It does not scan prose for the
// members of a set. That was measured on the manifest reference and does not
// work: 19 of 80 enum values on one page appear as ordinary English, and
// backticks do not separate them because key names are backticked too. Such a
// gate fires on correct prose and is deleted within a week, which is worse
// than no gate. It also does not flag a sentence that names some members
// without claiming to name all of them, which is the restraint that keeps a
// gate believed.
//
// Two shapes are checked, and each is one somebody can only have meant as a
// complete claim:
//
//  1. A stated count. "the six lint rules" is checkable against the length of
//     the constant block, and was wrong in eight places this can see.
//  2. A documentation table that IS the reference for a set. Structural, not a
//     value scan: the table under a named heading must have one row per
//     member. This is the half a count rule cannot do, because the page that
//     lists all seventeen rules correctly states no number at all, so an
//     eighteenth rule would land in the code and in no sentence anywhere and
//     rule 1 would stay silent.
package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// trees are the places prose about these sets lives.
//
// engine is included, unlike in modecheck, because a Go comment can be wrong
// about a constant sitting in the same repository and three of them were:
// gate.go called the lint rules six twice and manifest.go once. Only the
// constant itself cannot be wrong about itself.
var trees = []string{
	"docs/src/content/docs", "docs/plan/STATUS.md", "www", "console",
	"README.md", "engine", "schemas",
}

// decl says where one closed set is declared and what prose calls it.
//
// The split is the point. Membership is read from the Go source on every run,
// so it can never be stale. Only the noun a document uses for the set is
// written down, and that is the stable half. Where the Go declaration cannot
// be found the tool fails rather than reporting success over a set it never
// read, because a check examining nothing exits green and looks exactly like a
// check finding nothing.
type decl struct {
	// name is for the report, so a reader knows which set was miscounted.
	name string
	// file, kind and symbol locate the declaration.
	file   string
	kind   shape
	symbol string
	// noun matches the head noun prose uses. It is deliberately not anchored
	// on the member values.
	noun *regexp.Regexp
	// context must also match the sentence, and is what stops "eleven alert
	// rules" and "af doctor runs ten checks" from being read as claims about
	// these sets. A set with a noun nothing else in the repository uses, such
	// as "analyzers", needs none.
	context *regexp.Regexp
	// table is the documentation table that IS the reference for this set,
	// where one exists. Rule 2 reads it.
	table *tableRef
}

type tableRef struct {
	file, heading string
}

// sets is every closed set declared in Go alone that prose has a settled noun
// for.
//
// It is deliberately not every closed set the engine declares. oracle.Kind has
// nineteen members and no page counts or enumerates them, change.Check shares
// the word "checks" with af doctor's ten and with the three checks the
// manifest configures, and RollingVerdict shares "verdicts" with the six run
// verdicts, which is a different set of a different size. Declaring those
// would buy nothing and would spend the thing that makes a gate worth having,
// which is that a finding is always real.
var sets = []decl{
	{
		name:   "DDL lint rules",
		file:   "engine/internal/insights/lint.go",
		kind:   constType,
		symbol: "Rule",
		noun:   regexp.MustCompile(`(?i)\brules?\b`),
		// "lint" is what separates these from the alert rules, the masking
		// rules, the change rules and the persona rules, all of which are
		// counted correctly elsewhere in these same trees.
		context: regexp.MustCompile(`(?i)\blint`),
		table: &tableRef{
			file:    "docs/src/content/docs/concepts/insights.md",
			heading: "The lint rules",
		},
	},
	{
		name:   "detection analyzers",
		file:   "engine/internal/detect/detect.go",
		kind:   sliceFunc,
		symbol: "DefaultAnalyzers",
		noun:   regexp.MustCompile(`(?i)\banalyz(?:er|ers)\b`),
	},
	{
		name:    "migration tools",
		file:    "engine/internal/insights/migrations.go",
		kind:    constType,
		symbol:  "Tool",
		noun:    regexp.MustCompile(`(?i)\btools?\b`),
		context: regexp.MustCompile(`(?i)\bmigration`),
	},
	{
		name:    "exploration finding kinds",
		file:    "engine/internal/explore/explore.go",
		kind:    constType,
		symbol:  "Kind",
		noun:    regexp.MustCompile(`(?i)\bkinds?\b`),
		context: regexp.MustCompile(`(?i)\b(explor\w+|friction)\b`),
		table: &tableRef{
			file:    "docs/src/content/docs/concepts/exploration.md",
			heading: "The taxonomy",
		},
	},
	{
		name:   "fidelity states",
		file:   "engine/internal/fidelity/fidelity.go",
		kind:   constType,
		symbol: "State",
		noun:   regexp.MustCompile(`(?i)\bstates?\b`),
		// Only "fidelity". "component" was tried and is what STATUS.md and the
		// README both use for their own proven/written/planned vocabulary,
		// which is a different set of three, so it produced two false alarms
		// on correct sentences. Losing it means inventory.md's own "one of
		// five states" is out of rule 1's reach, which costs nothing: rule 2
		// checks that page's table row for row, which is the stronger check.
		context: regexp.MustCompile(`(?i)\bfidelity\b`),
		table: &tableRef{
			file:    "docs/src/content/docs/concepts/inventory.md",
			heading: "The states",
		},
	},
	{
		name:    "journal resource kinds",
		file:    "engine/internal/journal/journal.go",
		kind:    constType,
		symbol:  "Kind",
		noun:    regexp.MustCompile(`(?i)\bkinds? of resource\b|\bresource kinds?\b`),
		context: regexp.MustCompile(`(?i)\b(journal|teardown|compensat\w+)\b`),
	},
	{
		name:    "destructive change kinds",
		file:    "engine/internal/insights/rolling.go",
		kind:    constType,
		symbol:  "ChangeKind",
		noun:    regexp.MustCompile(`(?i)\bkinds?\b`),
		context: regexp.MustCompile(`(?i)\brolling.?compatib\w+|\bdestructive\b`),
	},
	{
		name:    "change surfaces",
		file:    "engine/internal/change/change.go",
		kind:    constType,
		symbol:  "Surface",
		noun:    regexp.MustCompile(`(?i)\bsurfaces?\b`),
		context: regexp.MustCompile(`(?i)\b(change|classif\w+)\b`),
	},
	{
		name:    "plan regression kinds",
		file:    "engine/internal/insights/plan.go",
		kind:    constType,
		symbol:  "PlanChange",
		noun:    regexp.MustCompile(`(?i)\b(plan )?regressions?\b`),
		context: regexp.MustCompile(`(?i)\bplan\b`),
	},
	{
		name:    "database providers",
		file:    "engine/pkg/schema/manifest.go",
		kind:    constType,
		symbol:  "DBProvider",
		noun:    regexp.MustCompile(`(?i)\bproviders?\b`),
		context: regexp.MustCompile(`(?i)\bdatabase\b`),
	},
	{
		name:    "third parties detected",
		file:    "engine/internal/detect/thirdparty.go",
		kind:    sliceVar,
		symbol:  "thirdParties",
		noun:    regexp.MustCompile(`(?i)\bthird part(?:y|ies)\b`),
		context: regexp.MustCompile(`(?i)\b(detect\w+|recognis\w+|recogniz\w+)\b`),
	},
	{
		name:    "webhook providers",
		file:    "engine/internal/webhook/webhook.go",
		kind:    mapVar,
		symbol:  "Providers",
		noun:    regexp.MustCompile(`(?i)\bproviders?\b`),
		context: regexp.MustCompile(`(?i)\bwebhooks?\b`),
	},
}

// unchecked are closed sets declared only in Go that this tool deliberately
// does not check, each with the reason.
//
// It is printed on every run because silence is not coverage, and the next
// person to read this output should not have to work out whether nineteen
// oracle kinds are checked and clean or simply absent. An unchecked set is a
// decision, and a decision has to be visible to be revisited.
var unchecked = []struct{ name, why string }{
	{"oracle difference kinds (19)",
		"no page counts or enumerates them, so there is no claim to check"},
	{"change checks (7)",
		`"checks" also names af doctor's ten and the three the manifest configures`},
	{"the environment lifecycle events (7 declared, 5 emitted)",
		"the set prose describes is the events the engine emits, and that is " +
			"not the const block. EnvWaking has no call site anywhere and " +
			"EnvSleeping's only one is a control plane mapping, so a correct " +
			"sentence saying five would be flagged as wrong by two. Declaring " +
			"it was tried and produced four findings, all false, one of them on " +
			"a sentence somebody had just corrected"},
	{"whether a declared value is produced ANYWHERE (as opposed to by one named function)",
		"already gated, by go test ./internal/events -run Emit, which holds every " +
			"catalog type to being emitted or exempt with a written reason. Do not " +
			"rebuild it here. A reference is not a production and a reference count " +
			"gets that backwards, calling events.EnvSleeping live because the control " +
			"plane type map names it; that test narrows to the ARGUMENT POSITION of a " +
			"call to a known emit function, which a map key cannot occupy. reach.go " +
			"does the smaller syntactic case of one block against one function"},
	{"any count whose noun is elided",
		`"governs all six together" names no noun, and the noun is the only ` +
			"thing that says which set is meant. Tried with a proximity window " +
			"and it read a corrected sentence as two other sets miscounted. " +
			"Counted claims are NOT safe by construction; this shape needs a reader"},
	{"verdicts, all three sets of them (5, 5 and 6)",
		"five workflow verdicts, six run verdicts (warn is one) and five rolling " +
			"verdicts share one noun. Declaring them was tried and produced two " +
			"false alarms on correct prose and no true finding, because the only " +
			"discriminators available are run, runner and workflow, and a sentence " +
			"about workflow verdicts says \"a run that could not answer\""},
	{"journal states, change statuses, doctor statuses, policy levels (3 to 4 each)",
		"no prose counts them, and their nouns are among the most reused words in the tree"},
}

// covered are the closed sets that already have a gate, named here so that
// their absence from the list above does not read as a gap.
var covered = "error codes (errcheck), event types and masking transforms and the " +
	"command tree (generated, `just generated` diffs them), manifest enums (modecheck)"

type finding struct {
	file, why string
	line      int
	text      string
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}
	if err := run(*root, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "\nconstcheck: %v\n", err)
		os.Exit(1)
	}
}

func run(root string, out io.Writer) error {
	// Read every set before scanning anything. A renamed type or a registry
	// this tool no longer understands has to stop the run, because the
	// alternative is a green exit over a set that was never checked.
	members := map[string][]string{}
	for _, s := range sets {
		vals, err := goSet(filepath.Join(root, s.file), s.kind, s.symbol)
		if err != nil {
			return err
		}
		if len(vals) < 3 {
			return fmt.Errorf("%s: read only %d members of %s, so this check is "+
				"reading the wrong symbol", s.file, len(vals), s.symbol)
		}
		members[s.name] = vals
	}

	files, err := prose(root)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("found no prose under %s, so this check is looking in the wrong place", root)
	}

	var found []finding
	// Per set rather than in total, because a total cannot answer the question
	// a reader of this output actually has. See the coverage note below.
	reached := map[string]int{}
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return err
		}
		found = append(found, checkCounts(f, string(body), members, reached)...)
	}

	// Rule 2 runs against named tables rather than against every file, so a
	// missing heading is an error and not a silent skip.
	tablesRead := 0
	for _, s := range sets {
		if s.table == nil {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, s.table.file))
		if err != nil {
			return err
		}
		rows, err := tableUnder(string(body), s.table.heading)
		if err != nil {
			return fmt.Errorf("%s: %w. This tool names that heading as the reference "+
				"for the %s, so it cannot check the table it was pointed at",
				s.table.file, err, s.name)
		}
		tablesRead++
		if f, ok := checkTable(s.table.file, s.name, rows, members[s.name]); ok {
			found = append(found, f)
		}
	}

	// Reachability, which is a different property from a count and a worse
	// defect when it fails. See reach.go for what it deliberately does not do.
	for _, r := range reachable {
		f, err := checkReachable(root, r)
		if err != nil {
			return err
		}
		found = append(found, f...)
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].line < found[j].line
	})
	report := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format, args...) }
	for _, f := range found {
		report("%s:%d  %s\n    %s\n", f.file, f.line, f.why, f.text)
	}

	// What was examined, not only what was wrong. A check that has stopped
	// reaching its subject reports zero findings and exits green, which is
	// indistinguishable from a clean tree unless it says what it read.
	report("\nconstcheck: %d closed sets read from Go source. Sizes come from the\n"+
		"  source on every run and are never written down here, and a set that\n"+
		"  cannot be located fails this command rather than being skipped, so a\n"+
		"  renamed type breaks the gate loudly instead of quietly emptying it.\n",
		len(sets))
	sentencesRead := 0
	for _, s := range sets {
		sentencesRead += reached[s.name]
		held := fmt.Sprintf("%d counted sentences", reached[s.name])
		if reached[s.name] == 1 {
			held = "1 counted sentence"
		}
		if s.table != nil {
			held += " and a reference table"
		}
		report("  %-28s %2d  %s %s\n      held by %s\n",
			s.name, len(members[s.name]), s.file, s.symbol, held)
	}
	report("constcheck: %d files scanned, %d sentences stating a count considered\n",
		len(files), sentencesRead)
	// A set held by nothing is reported and never fails.
	//
	// Silence would otherwise look like coverage: a rewrite that removes the
	// last sentence counting a set leaves this command green and leaves the
	// set unwatched, and the two are indistinguishable from the total alone.
	//
	// It does not fail, and that is deliberate rather than timid. Failing
	// would force somebody to keep a count sentence alive to satisfy the
	// gate, and a count is the weaker form. The best fix for a miscounted
	// sentence is to rewrite it so it states a property and carries no number,
	// which is what happened to four of these on the marketing site: the word
	// analyzers left the page entirely. Failing on zero would punish exactly
	// that rewrite and push people back towards a number somebody has to keep
	// true forever.
	report("constcheck: %d reference tables checked row for row\n", tablesRead)
	for _, r := range reachable {
		report("constcheck: every one of the %d %s constants in %s is returned by %s, "+
			"and %s returns nothing else\n", r.want, r.name, r.file, r.fn, r.fn)
	}
	report("constcheck: %d miscounted claims\n", len(found))
	report("constcheck: deliberately not checked, so that silence is not read as coverage:\n")
	for _, u := range unchecked {
		report("  %s\n      %s\n", u.name, u.why)
	}
	report("  already gated elsewhere: %s\n", covered)

	if len(found) > 0 {
		return fmt.Errorf("%d places state a count for a closed set that is not its real size", len(found))
	}
	return nil
}

// prose walks the trees for the documents a claim can live in.
func prose(root string) ([]string, error) {
	var out []string
	for _, tree := range trees {
		full := filepath.Join(root, tree)
		info, err := os.Stat(full)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", tree, err)
		}
		if !info.IsDir() {
			out = append(out, tree)
			continue
		}
		err = filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", ".next", "dist", ".astro", "testdata":
					return fs.SkipDir
				}
				return nil
			}
			switch filepath.Ext(p) {
			case ".md", ".mdx", ".tsx", ".ts", ".json", ".go":
			default:
				return nil
			}
			// Test files are excluded because a test legitimately says "three
			// findings" about its own fixture, which is not a claim about the
			// size of the set.
			//
			// Generated files are excluded because nobody writes a claim in
			// one. proxyimage/sources.gen.go embeds the whole engine source as
			// string literals, so every comment in the engine appears twice
			// and every finding in one would be reported twice. The original
			// is checked, and `just generated` fails if the copy is stale
			// against it, so nothing is lost by reading only the original.
			if strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, ".test.ts") ||
				strings.HasSuffix(p, ".gen.go") {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			out = append(out, rel)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}
