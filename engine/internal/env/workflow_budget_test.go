package env

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func budgetManifest(duration string) *schema.Manifest {
	return &schema.Manifest{Name: "app", Workflows: []schema.Workflow{{
		Name: "welcome", Description: "Open the welcome page.",
		Expect:    []string{"The account is created and the session is signed in."},
		StartPath: "/", Budget: &schema.Budget{Steps: 12, Duration: duration},
	}}}
}

func TestWorkflowDocs_TheDeclaredBudgetIsSentToTheRunner(t *testing.T) {
	o := orchestratorFor(t, t.TempDir(), budgetManifest("2s"))
	docs := o.workflowDocs(nil)
	require.Len(t, docs, 1)
	require.Equal(t, 12, docs[0].MaxSteps, "the declared step budget")
	require.Equal(t, int64(2_000), docs[0].MaxMs, "the declared time budget")

	// The names the runner reads, because a field it does not read is the
	// same dead budget with one more hop in it.
	body, err := json.Marshal(docs[0])
	require.NoError(t, err)
	require.Contains(t, string(body), `"maxSteps":12`)
	require.Contains(t, string(body), `"maxMs":2000`)

	// The declared step count is sent as written at either side of the
	// runner's own forty, so neither a small budget nor a large one is
	// replaced by the default on the way.
	for _, steps := range []int{5, 60} {
		m := budgetManifest("2s")
		m.Workflows[0].Budget.Steps = steps
		sent := orchestratorFor(t, t.TempDir(), m).workflowDocs(nil)[0]
		require.Equalf(t, steps, sent.MaxSteps, "a declared budget of %d steps", steps)
	}

	// A workflow that declares no budget leaves both halves to the runner.
	bare := orchestratorFor(t, t.TempDir(), &schema.Manifest{Name: "app", Workflows: []schema.Workflow{{
		Name: "welcome", Expect: []string{"Welcome"},
	}}})
	d := bare.workflowDocs(nil)[0]
	require.Zero(t, d.MaxSteps)
	require.Zero(t, d.MaxMs)
}

// Every command that runs declared workflows, af test, af ci, the MCP tool and
// the hosted workload runner, reaches the runner through Orchestrator.Test,
// which builds the workflows only through workflowDocs. None of them carries a
// budget of its own: TestOptions has no field for one. So the budget is sent in
// exactly one place, and this refuses a second place that would build a
// workflow document without it.
//
// The type is unexported, so only this package can build one, and this
// package's directory is the whole of what has to be read. The mcp package has
// a type of the same name that describes a result; it cannot reach the runner.
func TestEveryWorkflowDocumentIsBuiltByWorkflowDocs(t *testing.T) {
	root, err := filepath.Abs(".")
	require.NoError(t, err)
	var builders []string
	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(body), "\n") {
			// A literal of the type itself. Not the empty []workflowDoc{} a
			// marshaller sends so the list is never null, and not another type
			// whose name only ends in workflowDoc.
			if workflowDocLiteral.MatchString(line) {
				rel, _ := filepath.Rel(root, path)
				builders = append(builders, fmt.Sprintf("%s:%d", rel, i+1))
			}
		}
		return nil
	}))
	require.Len(t, builders, 1,
		"a workflow document built anywhere but workflowDocs would reach the runner without its budget: %v", builders)
	require.True(t, strings.HasPrefix(builders[0], "test.go:"), builders[0])
}

var workflowDocLiteral = regexp.MustCompile(`(^|[^\w\]])workflowDoc\{`)

const welcomePage = `<html><body><h1>Welcome</h1>
<p>Your account is created and you are signed in.</p></body></html>`

// runBudgetedWorkflow sends the engine's own workflow documents to the real
// runner, against a page that answers after delay, and returns the one result.
func runBudgetedWorkflow(t *testing.T, duration string, delay time.Duration) (WorkflowResult, time.Duration) {
	t.Helper()
	runner := requireRunner(t)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-release:
			return
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, welcomePage)
	}))
	defer srv.Close()
	defer close(release)

	o := orchestratorFor(t, t.TempDir(), budgetManifest(duration))
	started := time.Now()
	out, err := o.invokeRunner(context.Background(), runner, jobDocument{
		BaseURL:   srv.URL,
		Artifacts: filepath.Join(t.TempDir(), "artifacts"),
		Workflows: o.workflowDocs(nil),
		Personas:  []personaDoc{{Name: "visitor", Email: "visitor@example.test", Login: "none"}},
		WorkDir:   o.opts.Root,
		Attempts:  2,
		Headless:  true,
	})
	elapsed := time.Since(started)
	require.NoError(t, err)
	var rep TestReport
	require.NoError(t, json.Unmarshal(out, &rep))
	require.Len(t, rep.Results, 1)
	return rep.Results[0], elapsed
}

func TestTest_ADeclaredTimeBudgetStopsASlowWorkflowInTheRealRunner(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil && os.Getenv("AF_REQUIRE_RUNNER") == "" {
		t.Skip("node is not installed; set AF_REQUIRE_RUNNER to make this a failure")
	}
	r, elapsed := runBudgetedWorkflow(t, "2s", 8*time.Second)
	require.Equal(t, "blocked", r.Outcome.Verdict, r.Outcome.Detail)
	require.Equal(t, "budget-exhausted", r.Outcome.Cause, r.Outcome.Detail)
	require.Contains(t, r.Outcome.Detail, "time budget of 2s")
	require.Less(t, elapsed, 8*time.Second, "the declared budget did not cap the workflow")
}

func TestTest_AWorkflowInsideItsDeclaredTimeBudgetStillPasses(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil && os.Getenv("AF_REQUIRE_RUNNER") == "" {
		t.Skip("node is not installed; set AF_REQUIRE_RUNNER to make this a failure")
	}
	r, _ := runBudgetedWorkflow(t, "20s", 0)
	require.Equal(t, "pass", r.Outcome.Verdict, r.Outcome.Detail)
}
