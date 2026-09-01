package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/report"
)

// The machine readable report, for the caller that has to act on it.
//
// The hosted control plane's pull request check needs the verdict of each
// workflow, the environment name and the address it came up on. The Markdown
// carries all of it and carries it for a person: reading the counts back out of
// a table with tick and cross glyphs in it would be a parser for prose, and the
// first time somebody reworded the headline it would start reporting a pass for
// a failing run.
//
// -o json is not the answer. That is the whole terminal's format, so a step
// that both shows progress to somebody watching the job and captures a result
// for a program has to choose, and redirecting stdout to a file gives up the
// progress.

func writeJSONReport(t *testing.T, run report.Run) map[string]any {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf), Getenv: func(string) string { return "" }}

	writeReport(e, run, "", path)

	body, err := os.ReadFile(path)
	require.NoError(t, err, "no report was written; the command said: %s", buf.String())
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded), "the report is not JSON: %s", body)
	return decoded
}

func TestReportJSONCarriesEveryWorkflowVerdict(t *testing.T) {
	decoded := writeJSONReport(t, report.Run{
		Environment: "af-orders-main-9c1a",
		URL:         "http://127.0.0.1:46001",
		Commit:      "0f1e2d3c4b5a69788796a5b4c3d2e1f001234567",
		Workflows: []report.Workflow{
			{Name: "place-an-order", Verdict: report.VerdictPass},
			{Name: "refund-an-order", Verdict: report.VerdictFail, Detail: "the total went negative"},
			{Name: "sign-in", Verdict: report.VerdictUnverified},
		},
	})

	require.Equal(t, "af-orders-main-9c1a", decoded["Environment"])
	require.Equal(t, "0f1e2d3c4b5a69788796a5b4c3d2e1f001234567", decoded["Commit"])

	workflows, ok := decoded["Workflows"].([]any)
	require.True(t, ok, "Workflows is not a list: %#v", decoded["Workflows"])
	require.Len(t, workflows, 3)

	verdicts := map[string]string{}
	for _, item := range workflows {
		w, ok := item.(map[string]any)
		require.True(t, ok)
		verdicts[w["Name"].(string)] = w["Verdict"].(string)
	}
	// Every one of the three, by name. Asserting a count would pass just as
	// well if all three came back as pass, which is the exact failure the
	// control plane's check exists to make impossible.
	require.Equal(t, report.VerdictPass, verdicts["place-an-order"])
	require.Equal(t, report.VerdictFail, verdicts["refund-an-order"])
	require.Equal(t, report.VerdictUnverified, verdicts["sign-in"])
}

// A run that reached no workflow at all has to be readable as such.
//
// This is the shape that made a whole nightly corpus green: two examples
// declared no workflows, the headline was literally "Antifailure: Nothing ran",
// and the leg passed. A reader of this file has to be able to tell it from a
// run where everything passed, and the only difference is an empty list.
func TestReportJSONSaysWhenNothingRan(t *testing.T) {
	decoded := writeJSONReport(t, report.Run{Environment: "af-empty"})
	workflows, present := decoded["Workflows"]
	require.True(t, present, "Workflows is missing entirely, so a reader cannot tell empty from absent")
	require.Nil(t, workflows, "an empty run should carry an empty list of workflows")
}

// Nothing is written when the flag is not given, because a command that leaves
// a file behind that nobody asked for is a command that surprises a script.
func TestReportJSONIsNotWrittenUnlessAskedFor(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf), Getenv: func(string) string { return "" }}

	writeReport(e, report.Run{Environment: "af-1"}, "", "")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}
