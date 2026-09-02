package cli_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/antifailure/antifailure/engine/internal/cli"
)

// A local flag may not take a name or a letter that a persistent flag already
// owns, because the local one silently wins and nothing anywhere says so.
//
// `af ci` carried a local --output meaning "a file to write", shadowing the
// persistent -o that means text or json on every other command. That was found
// and renamed to --report, and dogfood note 24 reads as though the class was
// closed. One instance was. `af oracle` still had the identical defect, with
// two consequences and no message for either: `af oracle -o json` wrote the
// report to a file called `json`, and `af oracle` had no machine readable
// output at all, on a command whose whole purpose is a comparison something
// else reads.
//
// Scoped by POINTER rather than by name, which is the only test that works.
// cobra's Flags() returns inherited persistent flags alongside local ones, so
// "the command has a flag called output" is true of every command in the tree
// and says nothing about whether it declared one. Comparing the flag pointer
// against the ancestor's is what separates the two, and a check written on
// names would report all 78 commands, which is the kind of result that gets a
// gate deleted rather than read.

// shadow is one local flag that takes a name or a letter an ancestor already
// owns.
type shadow struct {
	command string
	detail  string
}

// findShadows walks the tree and asks, of every flag a command declares
// itself, whether an ancestor's persistent set already has that name or that
// shorthand under a different pointer.
func findShadows(root *cobra.Command) []shadow {
	var out []shadow
	walkCommands(root, func(cmd *cobra.Command) {
		if cmd == root {
			return
		}
		local, merged := localFlags(cmd)
		if !merged {
			// pflag refuses to merge a persistent flag whose shorthand a local
			// flag has already given to a different name, and it refuses by
			// panicking. That is the half fixed shape: --report keeping -o.
			// Turned into a finding rather than allowed to kill the run,
			// because a gate that dies with a stack trace where a message
			// belongs is a worse gate, and this is still exactly the defect
			// the sweep is for.
			out = append(out, shadow{
				command: cmd.CommandPath(),
				detail: "declares a shorthand that an ancestor's persistent flag already gives to " +
					"a different name, which pflag refuses at merge time",
			})
			return
		}
		local.VisitAll(func(f *pflag.Flag) {
			// help is contributed by cobra to every command, and a command's
			// own help is not a shadow of its parent's.
			if f.Name == "help" {
				return
			}
			for anc := cmd.Parent(); anc != nil; anc = anc.Parent() {
				p := anc.PersistentFlags()
				if byName := p.Lookup(f.Name); byName != nil && byName != f {
					out = append(out, shadow{
						command: cmd.CommandPath(),
						detail: fmt.Sprintf("declares --%s, which %s already has as a persistent flag (%s)",
							f.Name, anc.CommandPath(), byName.Usage),
					})
				}
				if f.Shorthand == "" {
					continue
				}
				if byShort := p.ShorthandLookup(f.Shorthand); byShort != nil && byShort != f {
					out = append(out, shadow{
						command: cmd.CommandPath(),
						detail: fmt.Sprintf("declares -%s for --%s, and %s already gives -%s to --%s",
							f.Shorthand, f.Name, anc.CommandPath(), f.Shorthand, byShort.Name),
					})
				}
			}
		})
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].command != out[j].command {
			return out[i].command < out[j].command
		}
		return out[i].detail < out[j].detail
	})
	return out
}

// localFlags reads a command's own flags, reporting rather than propagating
// the panic pflag raises when the merge is impossible.
func localFlags(cmd *cobra.Command) (set *pflag.FlagSet, merged bool) {
	defer func() {
		if recover() != nil {
			set, merged = nil, false
		}
	}()
	return cmd.LocalFlags(), true
}

func walkCommands(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, child := range cmd.Commands() {
		walkCommands(child, fn)
	}
}

func TestNoLocalFlagShadowsAPersistentOne(t *testing.T) {
	root := cli.RootForDocs()
	var lines []string
	for _, s := range findShadows(root) {
		lines = append(lines, fmt.Sprintf("%s %s", s.command, s.detail))
	}
	if len(lines) > 0 {
		t.Errorf("a local flag silently wins over the persistent one and nothing says so:\n  %s",
			strings.Join(lines, "\n  "))
	}
}

// The sweep has to have looked at the tree. A walk that visited nothing, or a
// pointer comparison that matched everything, both report zero shadows, and
// only one of those is the truth.
func TestTheShadowSweepWalkedTheWholeTree(t *testing.T) {
	root := cli.RootForDocs()
	commands, flags := 0, 0
	walkCommands(root, func(cmd *cobra.Command) {
		commands++
		local, merged := localFlags(cmd)
		if !merged {
			return
		}
		local.VisitAll(func(f *pflag.Flag) {
			if f.Name != "help" {
				flags++
			}
		})
	})
	// Deliberately a floor rather than a count. A number written next to code
	// is wrong the first time somebody adds a command, and this only needs to
	// prove the instrument is pointed at something.
	if commands < 50 {
		t.Errorf("the walk found %d commands, so it is not reaching the tree", commands)
	}
	if flags < 50 {
		t.Errorf("the walk found %d local flags, so LocalFlags is not returning them", flags)
	}
}

// Proof the check can fire, built rather than reasoned about. A command with a
// local --output must be reported, and the sibling that only inherits the
// persistent one must not, which is the distinction the pointer test exists to
// make and the one a name comparison gets wrong.
func TestTheShadowSweepSeesOneAndNotTheOther(t *testing.T) {
	root := &cobra.Command{Use: "af"}
	var value string
	root.PersistentFlags().StringVarP(&value, "output", "o", "text", "Output format: text or json")

	shadowing := &cobra.Command{Use: "shadowing"}
	var file string
	shadowing.Flags().StringVarP(&file, "output", "o", "", "Write the report here")
	root.AddCommand(shadowing)

	innocent := &cobra.Command{Use: "innocent"}
	var keep bool
	innocent.Flags().BoolVar(&keep, "keep", false, "Leave it up")
	root.AddCommand(innocent)

	found := findShadows(root)
	if len(found) == 0 {
		t.Fatal("a local --output over a persistent --output was not reported")
	}
	for _, s := range found {
		if strings.HasSuffix(s.command, "innocent") {
			t.Errorf("a command that only inherits the persistent flag was reported: %s", s.detail)
		}
	}
	// Both the name and the letter are separate findings, because a rename to
	// --report that kept -o would fix one and leave the other.
	var byName, byShort bool
	for _, s := range found {
		if strings.Contains(s.detail, "declares --output") {
			byName = true
		}
		if strings.Contains(s.detail, "declares -o") {
			byShort = true
		}
	}
	if !byName || !byShort {
		t.Errorf("the name and the letter are not reported separately: %v", found)
	}
}

// The half fix is caught too: renaming --output to --report while keeping -o.
//
// It is reported in one of two ways and the test accepts either, which is not
// looseness. pflag refuses to merge an ancestor's -o into a flag set that has
// already given that letter to --report, and it refuses by panicking, so on a
// tree where the merge has not happened yet the sweep sees a panic. On a tree
// where something has already merged the persistent set, the merge is not
// attempted again and the sweep sees an ordinary shorthand collision. Which one
// you get depends on what ran before, which is why findShadows handles both:
// asserting only the panic passed in isolation and failed inside the full
// suite, and the first version of this test claimed the panic was the only
// outcome because that is the one it happened to observe.
//
// Either way the half fix is a finding, and either way it is worse than it
// looks: the shorthand is not a subtler shadow than the original defect, it is
// a binary that can die when cobra merges the sets.
func TestARenameThatKeepsTheLetterIsStillAFinding(t *testing.T) {
	root := &cobra.Command{Use: "af"}
	var value string
	root.PersistentFlags().StringVarP(&value, "output", "o", "text", "Output format: text or json")

	half := &cobra.Command{Use: "half"}
	var file string
	half.Flags().StringVarP(&file, "report", "o", "", "Write the report here")
	root.AddCommand(half)

	found := findShadows(root)
	if len(found) != 1 {
		t.Fatalf("the half fix produced %d findings, want 1: %v", len(found), found)
	}
	detail := found[0].detail
	if !strings.Contains(detail, "pflag refuses") &&
		!strings.Contains(detail, "already gives -o to --output") {
		t.Errorf("the finding names neither outcome of keeping the letter: %s", detail)
	}
}

// And the recover is not decoration: pflag really does panic on that shape when
// nothing has merged the set yet, which is what a fresh command tree is.
func TestTheSweepSurvivesTheMergePflagRefuses(t *testing.T) {
	root := &cobra.Command{Use: "af"}
	var value string
	root.PersistentFlags().StringVarP(&value, "output", "o", "text", "Output format: text or json")

	half := &cobra.Command{Use: "half"}
	var file string
	half.Flags().StringVarP(&file, "report", "o", "", "Write the report here")
	root.AddCommand(half)

	if _, merged := localFlags(half); merged {
		// Not a failure of the product, and worth saying rather than skipping:
		// it means something merged the set before this test ran, so this run
		// exercised the other branch.
		t.Skip("the persistent set was already merged, so the refusing branch is not reachable here")
	}
}
