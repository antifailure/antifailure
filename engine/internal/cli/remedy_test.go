package cli_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/antifailure/antifailure/engine/internal/cli"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// Every command an error tells somebody to run has to exist.
//
// The catalog is what the ENGINE prints, not what the documentation says, so a
// remedy naming a flag that was never added is the product telling a user, at
// the moment they are already stuck, to run something that will fail. That
// costs them the time it takes to find out, and it costs the next remedy its
// credibility.
//
// AF-DB-004 said "Run 'af golden list' to see the available versions, then
// 'af up --golden <version>'". `af up` has --branch, --hud and --rebuild.
// There is no --golden, there never was, and PinGolden, the field that would
// have backed one, is set in exactly one place inside the oracle and by no
// flag anywhere. It is the second instance of this class in an hour: AF-DET-005
// told a reader to re-run with --answer when --answer had nothing to bind to,
// fixed in 7b73a470, and `self-hosting/operations.md` told an operator during
// an incident not to run `af down --all`, which is also not a flag.
//
// A class found three times is a population, so this checks all of them.

// afInvocation finds a command somebody is being told to run.
//
// Anchored on the word boundary before "af " so that "leaf", "half" and the
// middle of a path cannot start one. Deliberately permissive about what
// follows: the walk below decides what is a subcommand, what is an argument
// and what is a flag, because a pattern that tried to decide that would have
// to know the command tree, which is the thing being checked.
var afInvocation = regexp.MustCompile(`\baf(?: +[A-Za-z0-9<>_.\[\]-]+)+`)

// invocation is one command line found in prose, split into the words that
// resolved as commands and the flags named after them.
type invocation struct {
	raw   string
	words []string
}

func findInvocations(text string) []invocation {
	var out []invocation
	for _, raw := range afInvocation.FindAllString(text, -1) {
		out = append(out, invocation{raw: raw, words: strings.Fields(raw)})
	}
	return out
}

// resolve walks the command tree as far as the words go.
//
// Greedy on subcommands and then stops, which is what keeps `af token create
// ci` from being reported: `ci` is the token's NAME, an argument, and there is
// no `af token create ci` command. Anything after the deepest command that
// resolved is an argument unless it starts with a dash, and only the flags are
// checked, because a name, a path or a placeholder is not this test's business.
//
// The first version validated every word as a subcommand and reported four
// arguments as missing commands. That is the false finding a gate cannot
// afford: spend it once and people stop reading the output.
func resolve(root *cobra.Command, words []string) (cmd *cobra.Command, flags []string, unknownCommand string) {
	cmd = root
	i := 1 // words[0] is "af", the root itself
	for ; i < len(words); i++ {
		w := words[i]
		if strings.HasPrefix(w, "-") {
			break
		}
		// findChild is docexamples_test.go's, which also resolves aliases.
		child := findChild(cmd, w)
		if child == nil {
			// A word that is not a subcommand ends the command path. It is an
			// argument, unless nothing at all resolved below the root and the
			// word looks like a command name rather than a placeholder, which
			// is the case worth reporting.
			if cmd == root && isCommandLike(w) {
				unknownCommand = w
			}
			break
		}
		cmd = child
	}
	for ; i < len(words); i++ {
		if !strings.HasPrefix(words[i], "--") {
			continue
		}
		// A remedy ends in a full stop and a sentence ends in one wherever the
		// flag is the last word, so "af init --force." named a flag called
		// "force." and was reported as missing. The punctuation is not part of
		// the flag.
		name := strings.TrimRight(strings.TrimPrefix(words[i], "--"), ".,;:!?)'\"")
		if name != "" {
			flags = append(flags, name)
		}
	}
	return cmd, flags, unknownCommand
}

// isCommandLike excludes the placeholders remedies use for a value the reader
// supplies, so `af <command>` and `af [name]` are not reported as missing.
func isCommandLike(w string) bool {
	if w == "" || strings.ContainsAny(w, "<>[]") {
		return false
	}
	for _, r := range w {
		if (r < 'a' || r > 'z') && r != '-' {
			return false
		}
	}
	return true
}

// hasFlag asks the command whether it would accept the flag, inherited
// persistent flags included, because that is what the reader's shell will do.
func hasFlag(cmd *cobra.Command, name string) bool {
	// cobra adds --help when it executes a command, not when the tree is
	// built, so an un-executed tree says every command lacks the one flag all
	// of them have. Asked for here so that this answers what the binary would
	// accept rather than what the tree happens to hold right now: without it,
	// "af --help lists every command" was reported as naming a flag that does
	// not exist.
	cmd.InitDefaultHelpFlag()
	if cmd.Flags().Lookup(name) != nil {
		return true
	}
	return cmd.InheritedFlags().Lookup(name) != nil
}

// finding is one remedy that names something that does not exist.
type finding struct {
	code   string
	field  string
	raw    string
	reason string
}

func sweepCatalog(root *cobra.Command) []finding {
	var found []finding
	for _, e := range aferrors.All() {
		for _, part := range []struct{ field, text string }{
			{"next_step", e.NextStep},
			{"message", e.Message},
		} {
			for _, inv := range findInvocations(part.text) {
				cmd, flags, unknown := resolve(root, inv.words)
				if unknown != "" {
					found = append(found, finding{
						code: string(e.Code), field: part.field, raw: inv.raw,
						reason: fmt.Sprintf("there is no 'af %s' command", unknown),
					})
					continue
				}
				for _, f := range flags {
					if !hasFlag(cmd, f) {
						found = append(found, finding{
							code: string(e.Code), field: part.field, raw: inv.raw,
							reason: fmt.Sprintf("'%s' has no --%s flag; it has %s",
								cmd.CommandPath(), f, flagNames(cmd)),
						})
					}
				}
			}
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].code < found[j].code })
	return found
}

func flagNames(cmd *cobra.Command) string {
	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name != "help" {
			names = append(names, "--"+f.Name)
		}
	})
	if len(names) == 0 {
		return "no flags of its own"
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func TestEveryRemedyInTheCatalogNamesSomethingThatExists(t *testing.T) {
	for _, f := range sweepCatalog(cli.RootForDocs()) {
		t.Errorf("%s %s names %q, and %s", f.code, f.field, f.raw, f.reason)
	}
}

// The sweep has to be able to see the defect it was written for. A check whose
// failure nobody has watched is decoration, and this one is easy to write in a
// way that finds nothing: an extractor that never matches looks exactly like a
// catalog with no problems in it.
func TestTheSweepFindsAnInventedFlagAndAnInventedCommand(t *testing.T) {
	root := cli.RootForDocs()

	cmd, flags, unknown := resolve(root, strings.Fields("af up --golden"))
	if unknown != "" {
		t.Fatalf("af up did not resolve: %s", unknown)
	}
	if len(flags) != 1 || flags[0] != "golden" {
		t.Fatalf("--golden was not read out of the line: %v", flags)
	}
	if hasFlag(cmd, "golden") {
		t.Error("af up is reported as having a --golden flag, so the sweep cannot see the defect it was written for")
	}

	if _, _, unknown := resolve(root, strings.Fields("af summon")); unknown != "summon" {
		t.Errorf("an invented command resolved as %q, want it reported as unknown", unknown)
	}

	// And the two shapes that must NOT be reported, because a gate that cries
	// wolf gets switched off. `ci` is the token's name and `<version>` is a
	// placeholder the reader fills in.
	if _, _, unknown := resolve(root, strings.Fields("af token create ci")); unknown != "" {
		t.Errorf("an argument was reported as a command: %q", unknown)
	}
	if _, _, unknown := resolve(root, strings.Fields("af golden verify <version>")); unknown != "" {
		t.Errorf("a placeholder was reported as a command: %q", unknown)
	}
}

// A remedy that names a real command with a real flag passes, so the sweep is
// measuring the catalog rather than agreeing with everything.
func TestARealRemedyPasses(t *testing.T) {
	root := cli.RootForDocs()
	cmd, flags, unknown := resolve(root, strings.Fields("af env prune --older-than"))
	if unknown != "" {
		t.Fatalf("af env prune did not resolve: %s", unknown)
	}
	if cmd.CommandPath() != "af env prune" {
		t.Errorf("resolved to %q, want af env prune", cmd.CommandPath())
	}
	if len(flags) != 1 || !hasFlag(cmd, flags[0]) {
		t.Errorf("--older-than was not recognised on af env prune: %v", flags)
	}
}

// The extractor has to find a command wherever a remedy puts one, and the
// catalog puts them in three shapes. Written from what is actually in the file
// rather than from what a remedy ought to look like.
func TestTheExtractorFindsCommandsInEveryShapeTheCatalogUses(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"Run 'af golden list' to see the available versions.", []string{"af golden list"}},
		{"Run 'af down' on those environments first, or leave it.", []string{"af down"}},
		{"Install it with 'af runner install', or point at a checkout.", []string{"af runner install"}},
		{"Run 'af golden list', then 'af up'.", []string{"af golden list", "af up"}},
		// The word af inside another word must not start one.
		{"The leaf node and half the rows were affected.", nil},
	}
	for _, c := range cases {
		var got []string
		for _, inv := range findInvocations(c.text) {
			// Trailing punctuation is not part of the command.
			got = append(got, strings.TrimRight(inv.raw, ".,;:"))
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("from %q got %v, want %v", c.text, got, c.want)
		}
	}
}

// The same class again, one level over: a command named in a string the engine
// PRINTS, rather than in the catalog or in a page.
//
// `af init` writes a README into .antifailure telling the reader that if they
// delete the journal while an environment is running they should run
// `af down --all`. There is no --all. That one is the most durable of the three
// instances of that exact flag, because it is written to a file on disk rather
// than printed once, and neither the catalog sweep nor the documentation sweep
// could see it: it is a Go string literal.
//
// Scoped to FLAGS on purpose, and the limit is written here rather than in a
// report. A bare invented command name cannot be separated from prose in a
// string literal: "run af down again", "af golden refresh has nothing to
// compile" and "af down clears it" are all ordinary sentences that start with a
// real command, and a rule that read the next word as a subcommand would report
// every one of them. Measured rather than assumed: over these files the naive
// version produced roughly thirty findings and about a third were sentences.
// A flag is unambiguous, because prose does not contain --words, and the flags
// are where both live defects were.
//
// So this cannot see `af summon` written into a string. The catalog sweep and
// the documentation sweep can, in the places they cover.

// goStringLiterals returns every string constant in a package's non test files,
// read through go/parser rather than with a regular expression so that raw
// strings, escapes and backticks are what Go says they are.
func goStringLiterals(t *testing.T, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	// One file at a time rather than the directory helper go/parser used to
	// offer, which is deprecated because it does not consider build tags when
	// grouping files into packages. Every literal in the directory's non test
	// files is wanted here whatever package they claim, so that grouping was
	// never used.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parsing %s: %v", path, perr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, uerr := strconv.Unquote(lit.Value); uerr == nil {
				out = append(out, s)
			}
			return true
		})
	}
	return out
}

func TestEveryCommandTheEnginePrintsNamesAFlagThatExists(t *testing.T) {
	root := cli.RootForDocs()
	var problems []string
	invocations := 0

	for _, dir := range []string{".", filepath.Join("..", "env"), filepath.Join("..", "errors")} {
		for _, s := range goStringLiterals(t, dir) {
			for _, in := range findInvocations(s) {
				invocations++
				cmd, flags, _ := resolve(root, in.words)
				for _, f := range flags {
					if !hasFlag(cmd, f) {
						problems = append(problems, fmt.Sprintf(
							"%s: %q names --%s, and '%s' has %s",
							dir, in.raw, f, cmd.CommandPath(), flagNames(cmd)))
					}
				}
			}
		}
	}

	// A floor, because a walk that parsed nothing reports no problems and looks
	// exactly like a clean tree. There were over a hundred of these when this
	// was written.
	if invocations < 40 {
		t.Errorf("only %d commands were found in the engine's strings, so the scan has stopped matching",
			invocations)
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Errorf("the engine prints an instruction naming a flag that does not exist:\n  %s",
			strings.Join(problems, "\n  "))
	}
}
