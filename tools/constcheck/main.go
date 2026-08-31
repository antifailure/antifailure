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
	sentencesRead := 0
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return err
		}
		fs, n := checkCounts(f, string(body), members)
		found = append(found, fs...)
		sentencesRead += n
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
	report("\nconstcheck: %d closed sets read from Go source\n", len(sets))
	for _, s := range sets {
		report("  %-28s %2d  %s %s\n", s.name, len(members[s.name]), s.file, s.symbol)
	}
	report("constcheck: %d files scanned, %d sentences stating a count considered\n",
		len(files), sentencesRead)
	report("constcheck: %d reference tables checked row for row\n", tablesRead)
	report("constcheck: %d miscounted claims\n", len(found))

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
