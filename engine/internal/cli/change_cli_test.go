package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// changeProject writes the smallest project af change needs, plus a diff to
// read. The diff is a file rather than a git history because this is a test of
// the command, and the git path has its own tests next to the parser.
func changeProject(t *testing.T, diff string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "api"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"), []byte(`
version: 1
name: shop
services:
  - name: web
    path: api
    port: 3000
personas:
  - name: shopper
    email: shopper@example.com
workflows:
  - name: checkout
    persona: shopper
    description: Sign in, put the sample item in the basket, pay with the test card and see the order confirmation.
`), 0o600))
	path := filepath.Join(dir, "pr.diff")
	require.NoError(t, os.WriteFile(path, []byte(diff), 0o600))
	return dir
}

const codeDiff = `diff --git a/api/handler.go b/api/handler.go
index 1111111..2222222 100644
--- a/api/handler.go
+++ b/api/handler.go
@@ -1,0 +2,1 @@ package api
+func Handle() {}
`

const proseDiff = `diff --git a/README.md b/README.md
index 1111111..2222222 100644
--- a/README.md
+++ b/README.md
@@ -1,0 +2,1 @@ # shop
+A sentence.
`

func TestChange_ExplainsTheDiffAndTheReasoning(t *testing.T) {
	t.Parallel()
	dir := changeProject(t, codeDiff)

	res := runCLI(t, dir, nil, "change", "--diff", filepath.Join(dir, "pr.diff"))
	require.Zero(t, res.code, res.stderr)
	assert.Contains(t, res.stdout, "the web service")
	assert.Contains(t, res.stdout, "manifest.service")
	assert.Contains(t, res.stdout, "What this cannot see")
	assert.Contains(t, res.stdout, "run   workflows")
	assert.Contains(t, res.stdout, "skip  migration")
}

func TestChange_WritesTheReportSectionAndTheJobOutputs(t *testing.T) {
	t.Parallel()
	dir := changeProject(t, codeDiff)
	section := filepath.Join(dir, "section.md")
	outputs := filepath.Join(dir, "outputs.txt")

	res := runCLI(t, dir, map[string]string{"GITHUB_OUTPUT": outputs},
		"change", "--diff", filepath.Join(dir, "pr.diff"), "--write", section)
	require.Zero(t, res.code, res.stderr)

	body, err := os.ReadFile(section)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(body), "<!-- antifailure:report -->"),
		"without the marker a workflow leaves a second comment instead of updating the first")
	assert.Contains(t, string(body), "**What this change touches.**")
	assert.Contains(t, string(body), "| `workflows` | selected | yes |")

	// The outputs are what makes this actionable rather than a note on the
	// run: a later step reads them and skips work this change does not need.
	written, err := os.ReadFile(outputs)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(written)), "\n")
	assert.Contains(t, lines, "workflows=true")
	assert.Contains(t, lines, "migration=false")
	assert.Contains(t, lines, "invariants=false",
		"the manifest declares no invariants, so a step must not be told to run them")
	assert.Contains(t, lines, "load=false",
		"a code change selects load and this manifest has none, so the value is the conjunction and not the selection")
	assert.Contains(t, lines, "selected=environment,workflows")
}

// The saving the whole command exists to make, proved at the command boundary
// rather than only in the analyser.
func TestChange_AProseOnlyDiffTellsAJobToRunNothing(t *testing.T) {
	t.Parallel()
	dir := changeProject(t, proseDiff)
	outputs := filepath.Join(dir, "outputs.txt")

	res := runCLI(t, dir, map[string]string{"GITHUB_OUTPUT": outputs},
		"change", "--diff", filepath.Join(dir, "pr.diff"))
	require.Zero(t, res.code, res.stderr)

	written, err := os.ReadFile(outputs)
	require.NoError(t, err)
	assert.Contains(t, string(written), "environment=false")
	assert.Contains(t, string(written), "selected=\n")
}

func TestChange_RendersJSONForAScript(t *testing.T) {
	t.Parallel()
	dir := changeProject(t, codeDiff)

	res := runCLI(t, dir, nil, "change", "--diff", filepath.Join(dir, "pr.diff"), "-o", "json")
	require.Zero(t, res.code, res.stderr)

	var profile struct {
		Files int `json:"files"`
		Facts []struct {
			Path, Rule, Evidence string
		} `json:"facts"`
		Plan []struct {
			Check     string
			Selected  bool
			Available bool
		} `json:"plan"`
		Blind []string `json:"blind"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &profile))
	assert.Equal(t, 1, profile.Files)
	assert.Len(t, profile.Plan, 7)
	assert.NotEmpty(t, profile.Blind)
	for _, f := range profile.Facts {
		assert.NotEmpty(t, f.Rule, "%s has no rule", f.Path)
		assert.NotEmpty(t, f.Evidence, "%s has no evidence", f.Path)
	}
}

// A diff file that is not a diff is a user error with a code, not a panic and
// not an empty profile that looks like a change touching nothing.
func TestChange_ReportsAMissingDiffFile(t *testing.T) {
	t.Parallel()
	dir := changeProject(t, codeDiff)

	res := runCLI(t, dir, nil, "change", "--diff", filepath.Join(dir, "absent.diff"))
	require.NotZero(t, res.code)
	assert.Contains(t, res.stderr, "AF-DET-011")
}
