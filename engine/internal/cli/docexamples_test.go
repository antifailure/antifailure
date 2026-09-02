package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
)

// Every af command shown in the documentation has to be a command that exists.
//
// This was written after two flags in a freshly written guide turned out not to
// exist: af inbox wait --link and af net log --webhooks. Both read perfectly
// well. Both would have sent somebody to a usage error out of a page that was
// supposed to be helping them, which is worse than not having written the page.
//
// A reader trusts an example more than prose, because an example looks like
// something you can paste. So the examples are checked against the same command
// tree the binary serves.

var (
	// A command line inside a fenced block: a line beginning with af, before
	// any shell operator that would make the rest something else.
	//
	// The optional `run:` is not decoration. Without it this pattern requires
	// af to be the first word on the line, and a GitHub Actions step that runs
	// one command writes it as `run: af ci ...` on a single line. Six such
	// invocations were invisible to both this gate and the workflow one next
	// door, across examples/github-workflow.yml and three documentation pages,
	// and one of them was `af ci --output report.md` on the page that teaches
	// the pull request integration. That is the SAME defect this gate's
	// neighbour was written to catch, surviving in the one form neither
	// pattern could see. A `- ` is allowed only in front of `run:`, so a
	// markdown list item that merely mentions af in prose is still not read as
	// an invocation.
	afLine = regexp.MustCompile(`(?m)^\s*(?:(?:-\s+)?run:\s+)?(?:[a-z_]+=\$\()?af\s+([^\n|>&;#]*)`)
	// A long flag, with or without a value.
	longFlag = regexp.MustCompile(`--[a-zA-Z][a-zA-Z0-9-]*`)
)

func docsDir() string {
	return filepath.Join("..", "..", "..", "docs", "src", "content", "docs")
}

// The workflows in examples/ are documentation that happens not to be
// markdown, and the getting started page tells a reader to copy one rather
// than to type the commands out. That makes the template the version most
// people actually run, and it was outside every gate: `af ci --output
// report.md` sat in examples/github-workflow.yml after --output was renamed to
// --report, so the one command a new GitHub user runs exited 2 with a usage
// error, and the comment step behind it would have posted the change analysis
// left in report.md by the previous step as if it were the run's report.
func exampleWorkflowsDir() string {
	return filepath.Join("..", "..", "..", "examples")
}

func TestEveryCommandInTheDocsExists(t *testing.T) {
	pages := markdownPages(t, docsDir())
	require.NotEmpty(t, pages, "found no documentation to check")

	root := cli.RootForDocs()
	var problems []string
	checked := 0

	workflows := exampleWorkflows(t, exampleWorkflowsDir())
	require.NotEmpty(t, workflows, "found no example workflows to check")
	pages = append(pages, workflows...)

	for _, page := range pages {
		body, err := os.ReadFile(page)
		require.NoError(t, err)

		rel, _ := filepath.Rel(docsDir(), page)
		if strings.HasPrefix(rel, "..") {
			rel, _ = filepath.Rel(filepath.Join("..", "..", ".."), page)
		}
		// The generated reference is the command tree written down, so
		// checking it against the command tree proves nothing and its usage
		// lines are not examples.
		if rel == filepath.Join("reference", "cli.md") {
			continue
		}

		enterprisePage := strings.HasPrefix(rel, "enterprise"+string(filepath.Separator))

		for _, match := range afLine.FindAllStringSubmatch(string(body), -1) {
			line := strings.TrimSpace(match[1])
			if line == "" {
				continue
			}
			// A page about the enterprise edition may show a command the
			// enterprise binary contributes, and this tree does not have those
			// by construction: the community build cannot resolve the module
			// they live in. Allowed by name rather than by page, so that an
			// enterprise page showing a misspelled community command is still
			// caught, and only on an enterprise page, so that a community page
			// cannot quietly document something most users do not have.
			//
			// The allowance is not a free pass. ee/engine has a test asserting
			// that every name here is a command the enterprise binary actually
			// contributes, so an entry for something that does not exist fails
			// there rather than silently excusing a typo.
			if enterprisePage && enterpriseOnly[firstWord(line)] {
				continue
			}
			checked++
			problem := checkInvocation(root, line)
			if problem == "" {
				problem = checkFlagValues(line)
			}
			if problem != "" {
				problems = append(problems, fmt.Sprintf("%s: af %s\n    %s", rel, line, problem))
			}
		}
	}

	require.Greater(t, checked, 15,
		"only %d commands were found in the docs; the pattern has probably stopped matching", checked)

	sort.Strings(problems)
	require.Empty(t, problems,
		"these examples name something the command tree does not have:\n  %s",
		strings.Join(problems, "\n  "))
}

// enterpriseOnly are commands the enterprise binary contributes, which this
// command tree does not have and cannot have: they live in a separate module
// that the community build has no import path to.
//
// Kept in step by a test in ee/engine, which asserts that every name here is
// contributed by that binary.
var enterpriseOnly = map[string]bool{
	"compliance": true,
}

func firstWord(line string) string {
	if i := strings.IndexByte(line, ' '); i > 0 {
		return line[:i]
	}
	return line
}

// checkInvocation resolves a command path and its flags, returning what is
// wrong or the empty string.
func checkInvocation(root *cobra.Command, line string) string {
	fields := strings.Fields(line)

	cmd := root
	consumed := 0
	for _, field := range fields {
		if strings.HasPrefix(field, "-") {
			break
		}
		child := findChild(cmd, field)
		if child == nil {
			// Not a subcommand, so it is a positional argument and the command
			// path has ended. Whether the argument is valid is the command's
			// business, not this test's.
			break
		}
		cmd = child
		consumed++
	}
	if consumed == 0 {
		return fmt.Sprintf("%q is not a command", fields[0])
	}

	for _, flag := range longFlag.FindAllString(line, -1) {
		name := strings.TrimPrefix(flag, "--")
		if cmd.Flags().Lookup(name) != nil || cmd.InheritedFlags().Lookup(name) != nil {
			continue
		}
		if root.PersistentFlags().Lookup(name) != nil {
			continue
		}
		return fmt.Sprintf("%q has no flag %s", commandPath(cmd), flag)
	}

	// A flag that exists is not a flag that accepts what the example gives it,
	// and the difference cost a broken workflow template. --output is the one
	// flag in the tree with a closed set of values, and it is also the one an
	// example is most likely to get wrong, because `af ci` used to carry a
	// local --output meaning "a file to write" before it was renamed to
	// --report. Both spellings parse; only one runs.
	//
	// The set is read from the constants the command validates against rather
	// than written out here, so a third format arrives in both places at once.
	//
	// Only when --output resolves to the persistent flag. `af oracle` defines
	// its own local --output meaning "write the report here", which shadows the
	// persistent one, so `af oracle -o oracle.md` is correct and this check
	// reported it as broken the first time it ran. A gate's whole value is that
	// every finding is real.
	// Pointer identity, not presence: cobra's Flags() returns the complete set
	// including every inherited persistent flag, so looking "output" up on the
	// command finds the root's one and this check would never run at all.
	if cmd.Flags().Lookup("output") == root.PersistentFlags().Lookup("output") {
		if problem := checkOutputFormat(fields); problem != "" {
			return problem
		}
	}
	return ""
}

// checkOutputFormat reports an -o or --output whose value is a literal the
// command would refuse. A value that is a shell expansion is left alone: the
// example is showing how to pass one, not what it will be.
func checkOutputFormat(fields []string) string {
	valid := map[string]bool{
		string(cli.FormatText): true,
		string(cli.FormatJSON): true,
	}
	for i, f := range fields {
		var value string
		switch {
		case f == "--output" || f == "-o":
			if i+1 >= len(fields) {
				continue
			}
			value = fields[i+1]
		case strings.HasPrefix(f, "--output="):
			value = strings.TrimPrefix(f, "--output=")
		default:
			continue
		}
		value = strings.Trim(value, `"'`)
		if value == "" || strings.ContainsAny(value, "$`{") || strings.HasPrefix(value, "-") {
			continue
		}
		if !valid[value] {
			return fmt.Sprintf("--output takes text or json, not %q", value)
		}
	}
	return ""
}

func findChild(cmd *cobra.Command, name string) *cobra.Command {
	for _, child := range cmd.Commands() {
		if child.Name() == name {
			return child
		}
		for _, alias := range child.Aliases {
			if alias == name {
				return child
			}
		}
	}
	return nil
}

func commandPath(cmd *cobra.Command) string {
	var parts []string
	for c := cmd; c != nil; c = c.Parent() {
		parts = append([]string{c.Name()}, parts...)
	}
	return strings.Join(parts, " ")
}

// exampleWorkflows returns the YAML under examples/ that runs commands.
func exampleWorkflows(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".yaml")) {
			out = append(out, path)
		}
		return nil
	})
	require.NoError(t, err)
	sort.Strings(out)
	return out
}

func markdownPages(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".mdx")) {
			out = append(out, path)
		}
		return nil
	})
	require.NoError(t, err)
	sort.Strings(out)
	return out
}
