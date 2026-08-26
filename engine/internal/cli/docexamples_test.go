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
	afLine = regexp.MustCompile(`(?m)^\s*(?:[a-z_]+=\$\()?af\s+([^\n|>&;#]*)`)
	// A long flag, with or without a value.
	longFlag = regexp.MustCompile(`--[a-zA-Z][a-zA-Z0-9-]*`)
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

		for _, match := range afLine.FindAllStringSubmatch(string(body), -1) {
			line := strings.TrimSpace(match[1])
			if line == "" {
				continue
			}
			checked++
			if problem := checkInvocation(root, line); problem != "" {
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
