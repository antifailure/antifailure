package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/report"
)

// A repository with only the workflow file gets a check, not AF-MAN-001.
//
// The whole point of the workflow being something a customer never writes is
// lost if the next thing they meet is a file they have to write. So af ci and
// af change draft a manifest in memory the way af init would, run on it, and
// say so at the top of the report.

// af ci with no manifest and something to draft one from runs on the draft.
// Docker is not on this machine's test path, so the environment fails to come
// up; what this proves is that the draft was made, the section was printed,
// and the report that was written opens with the sentence naming af init.
func TestCI_NoManifestDraftsOneAndSaysSoInTheReport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	nodeProject(t, dir)
	out := filepath.Join(dir, "report.md")

	got := runCLI(t, dir, nil, "ci", "--report", out, "--timeout", "20s")
	require.NotContains(t, got.stderr, "AF-MAN-001",
		"a repository with no manifest was refused instead of drafted")
	require.Contains(t, got.stdout, "No antifailure.yaml here, so one was drafted")

	body, err := os.ReadFile(out)
	require.NoError(t, err, "no report was written; the command said: %s", got.stderr)
	said := prose(string(body))
	require.Contains(t, said, report.DraftedSentence)
	require.Less(t, strings.Index(said, "### Antifailure:"), strings.Index(said, report.DraftedSentence),
		"the sentence belongs right under the headline")
}

// Nothing to draft from is a skipped run: exit zero, a report saying nothing
// ran, and af init named as the next command.
func TestCI_NothingToDraftFromIsASkippedRunNamingAfInit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# nothing here"), 0o600))
	out := filepath.Join(dir, "report.md")
	outJSON := filepath.Join(dir, "report.json")
	outputs := filepath.Join(dir, "outputs.txt")

	got := runCLI(t, dir, map[string]string{"GITHUB_OUTPUT": outputs},
		"ci", "--report", out, "--report-json", outJSON)
	require.Zero(t, got.code, "nothing was learned about the change, so there is nothing to fail it for: %s", got.stderr)

	body, err := os.ReadFile(out)
	require.NoError(t, err)
	said := prose(string(body))
	require.Contains(t, said, "Nothing ran.")
	require.Contains(t, said, "**This check did not run.**")
	require.Contains(t, said, "could not be drafted")
	require.Contains(t, said, "`af init`")
	require.NotContains(t, said, "passed")
	require.NotContains(t, said, report.DraftedSentence,
		"no draft was used, so the sentence saying one was would be false")
	require.Contains(t, string(body), "**This check did not run.**")
	require.FileExists(t, outJSON, "the JSON report is what a control plane reads, and a skipped run is the one it most needs")

	written, err := os.ReadFile(outputs)
	require.NoError(t, err)
	require.Contains(t, string(written), "comment=true",
		"the workflow's comment step reads this, and a skipped run still leaves the comment")
}

// af change with no manifest drafts one too, so the plan a job reads agrees
// with the check af ci then runs.
func TestChange_NoManifestDraftsOneAndWritesItsOutputs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	nodeProject(t, dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "api"), 0o755))
	diff := filepath.Join(dir, "pr.diff")
	require.NoError(t, os.WriteFile(diff, []byte(codeDiff), 0o600))
	section := filepath.Join(dir, "section.md")
	outputs := filepath.Join(dir, "outputs.txt")

	got := runCLI(t, dir, map[string]string{"GITHUB_OUTPUT": outputs},
		"change", "--diff", diff, "--write", section)
	require.Zero(t, got.code, got.stderr)
	require.Contains(t, got.stdout, "No antifailure.yaml here, so one was drafted")
	require.NotContains(t, got.stdout, "no manifest was loaded",
		"the draft is a manifest, so no check may claim there is none")

	body, err := os.ReadFile(section)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(body), report.Marker+"\n"+report.DraftedSentence),
		"the section has to open with the sentence naming af init:\n%s", body)

	written, err := os.ReadFile(outputs)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(written)), "\n")
	require.Contains(t, lines, "environment=true",
		"the drafted manifest has a web service, so a code change selects the environment")
	require.Contains(t, lines, "source_url_env=")
	require.Contains(t, lines, "secrets=")
}
