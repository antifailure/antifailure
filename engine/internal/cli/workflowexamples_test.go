package cli_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
)

// Every af command inside a workflow file has to be a command that exists.
//
// TestEveryCommandInTheDocsExists next door checks the prose. It reads
// docs/src/content/docs and nothing else, and that blind spot shipped:
// examples/github-workflow.yml, the file the documentation tells every
// customer to copy into .github/workflows/antifailure.yml, ran
// `af ci --output report.md`. That flag is called --report. It was renamed
// deliberately, because a local --output shadowed the persistent one and
// `af ci -o json` wrote the pull request comment to a file called `json`. The
// rename fixed the CLI and left the example naming a flag the binary refuses,
// so the copied workflow died on a usage error at the one step the whole file
// exists for.
//
// A workflow is an example that a reader does not merely paste, it is one they
// paste and then never look at again, which makes it the worst place for a
// flag that does not exist and the best place for this check.
func TestEveryCommandInTheWorkflowsExists(t *testing.T) {
	files := workflowFiles(t)
	require.NotEmpty(t, files, "found no workflow files to check")

	root := cli.RootForDocs()
	var problems []string
	checked := 0

	for _, file := range files {
		body, err := os.ReadFile(file)
		require.NoError(t, err)

		rel, _ := filepath.Rel(repoRoot(), file)
		for _, match := range afLine.FindAllStringSubmatch(string(body), -1) {
			line := strings.TrimSpace(match[1])
			if line == "" {
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

	// The example workflow alone runs four commands, so anything at or below
	// three means the pattern has stopped matching rather than that the
	// workflows have stopped calling af. A gate that reports zero findings
	// over zero subjects is the shape this repository has been bitten by most.
	require.Greater(t, checked, 3,
		"only %d af invocations were found in the workflows; the pattern has probably stopped matching",
		checked)

	sort.Strings(problems)
	require.Empty(t, problems,
		"these workflow steps name something the command tree does not have:\n  %s",
		strings.Join(problems, "\n  "))
}

// checkFlagValues catches the flag that exists and will not accept what the
// example gives it.
//
// checkInvocation next door asks only whether a flag is declared, and that is
// exactly why `af ci --output report.md` survived: --output IS declared, as a
// persistent flag naming the output FORMAT, so a check for existence passes an
// invocation the binary refuses at PersistentPreRunE with "the output format
// \"report.md\" is not recognised". The step dies before af ci does any work,
// which for the example workflow means the report is never written and the
// pull request never gets a comment.
//
// So this parses the flags the way cobra does and then runs the root's own
// PersistentPreRunE, rather than restating anywhere which values a flag
// accepts. A second copy of that list would be a second thing to keep in step,
// and the whole class of defect here is two places disagreeing about one flag.
//
// A fresh command tree per invocation, because ParseFlags writes into the
// variables the tree closed over and one line's value would otherwise be read
// as the next line's.
func checkFlagValues(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	root := cli.RootForDocs()
	cmd, rest, err := root.Find(fields)
	if err != nil {
		// Not a command. checkInvocation has already said so, and saying it
		// twice about one line is how a gate's output stops being read.
		return ""
	}
	if err := cmd.ParseFlags(rest); err != nil {
		// pflag reports --help as an error so that a caller stops parsing, not
		// because anything is wrong with the line. `af --help` is a real
		// invocation and documenting it is not a defect.
		if errors.Is(err, pflag.ErrHelp) {
			return ""
		}
		return err.Error()
	}
	// -C names a directory that has to exist, and an example is entitled to
	// write a path that does not exist on this machine. Skipping the whole
	// pre-run for those lines rather than pattern matching the error, because
	// matching on message text is a gate that goes quiet when somebody rewords
	// an error.
	if cmd.Flags().Changed("directory") {
		return ""
	}
	if pre := preRunFor(cmd); pre != nil {
		if err := pre(cmd, nil); err != nil {
			return err.Error()
		}
	}
	return ""
}

// preRunFor finds the PersistentPreRunE cobra would actually run, which is the
// nearest one up the tree rather than the root's, because cobra runs only one.
func preRunFor(cmd *cobra.Command) func(*cobra.Command, []string) error {
	for c := cmd; c != nil; c = c.Parent() {
		if c.PersistentPreRunE != nil {
			return c.PersistentPreRunE
		}
	}
	return nil
}

func repoRoot() string { return filepath.Join("..", "..", "..") }

// workflowFiles are the YAML files that may run af: the examples a customer
// copies, and this repository's own workflows, which dogfood the same commands.
func workflowFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, dir := range []string{
		filepath.Join(repoRoot(), "examples"),
		filepath.Join(repoRoot(), ".github", "workflows"),
	} {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				// node_modules holds other projects' workflow files, which say
				// nothing about this command tree.
				if info.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".yaml") {
				out = append(out, path)
			}
			return nil
		})
		require.NoError(t, err)
	}
	sort.Strings(out)
	return out
}
