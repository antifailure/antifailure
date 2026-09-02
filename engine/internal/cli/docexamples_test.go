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
	// A command inside an inline code span or inside single quotes, which is
	// how prose names one and how the generated error reference renders every
	// remedy.
	//
	// afLine alone was not enough, and the gap was invisible for the reason
	// Standard 28a names: it is a correct instrument pointed at an assumption
	// about SHAPE. Anchoring on ^ says a command occupies its own line, which
	// is true of a fenced example and false of a sentence, so all 127 remedies
	// on reference/errors.md were outside what this gate could see. Three
	// instances of a command that does not exist accumulated underneath it,
	// one of them printed by the engine itself: AF-DB-004 told a reader to run
	// af up --golden, and af up has no such flag.
	//
	// Delimited on both sides on purpose. An unterminated span is prose with an
	// apostrophe in it, and reading to the end of the line would turn "the
	// environment's af doctor output" into a command.
	afInBackticks = regexp.MustCompile("`af\\s+([^`\n]*)`")
	afInQuotes    = regexp.MustCompile(`'af\s+([^'\n]*)'`)
)

func docsDir() string {
	return filepath.Join("..", "..", "..", "docs", "src", "content", "docs")
}

func TestEveryCommandInTheDocsExists(t *testing.T) {
	pages := markdownPages(t, docsDir())
	require.NotEmpty(t, pages, "found no documentation to check")

	root := cli.RootForDocs()
	var problems []string
	checked := 0

	for _, page := range pages {
		body, err := os.ReadFile(page)
		require.NoError(t, err)

		rel, _ := filepath.Rel(docsDir(), page)
		// The generated reference is the command tree written down, so
		// checking it against the command tree proves nothing and its usage
		// lines are not examples.
		if rel == filepath.Join("reference", "cli.md") {
			continue
		}

		enterprisePage := strings.HasPrefix(rel, "enterprise"+string(filepath.Separator))

		var matches [][]string
		for _, re := range []*regexp.Regexp{afLine, afInBackticks, afInQuotes} {
			matches = append(matches, re.FindAllStringSubmatch(string(body), -1)...)
		}
		for _, match := range matches {
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
	// consumed == 0 with a leading flag means the command is the root itself,
	// as in `af --help`, which is a real invocation. Only a leading word that
	// resolves to no subcommand is a command that does not exist. The flag
	// check below still runs either way, so `af --nonsense` is still caught.
	if consumed == 0 && !strings.HasPrefix(fields[0], "-") {
		return fmt.Sprintf("%q is not a command", fields[0])
	}

	// Cobra registers --help lazily, when a command is executed rather than
	// when it is built, so without this the flag every command really does
	// accept looks like one none of them have.
	cmd.InitDefaultHelpFlag()
	root.InitDefaultHelpFlag()

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
