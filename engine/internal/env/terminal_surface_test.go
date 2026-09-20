package env

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The engine half of the terminal surface, and the hop that did not exist.
//
// THE DEFECT. runner/src/drivers/terminal.ts was written, unit tested and
// reachable from nothing. The runner read `doc.terminal`, the engine's job
// document had no such field, and the manifest had no key that could have
// filled one, so the terminal driver ran on exactly zero customer runs while
// looking, from the inside, like a working feature. Every test below is about
// one hop of manifest, to job document, to the real runner, to a counted
// verdict, because "the driver has tests" was already true and was not enough.

func terminalManifest(w ...schema.TerminalWorkflow) *schema.Manifest {
	return &schema.Manifest{Name: "app", TerminalWorkflows: w}
}

func aDeployWorkflow() schema.TerminalWorkflow {
	return schema.TerminalWorkflow{
		Name:        "deploy-plan",
		Description: "The deploy command shows the plan and asks before applying it.",
		Command:     "./bin/deploy",
		Args:        []string{"--plan"},
		Input:       []string{"y", "<enter>"},
		Expect:      []string{`"Applied 3 changes"`},
		Screen:      &schema.TerminalScreen{Rows: 30, Cols: 100},
		Budget:      &schema.TerminalBudget{Duration: "45s"},
	}
}

func TestTerminalDocs_TheManifestReachesTheRunnerWholeAndUnderTheNamesItReads(t *testing.T) {
	o := orchestratorFor(t, t.TempDir(), terminalManifest(aDeployWorkflow()))
	docs := o.terminalDocs(nil)
	require.Len(t, docs, 1)
	require.Equal(t, "deploy-plan", docs[0].Name)
	require.Equal(t, "./bin/deploy", docs[0].Command)
	require.Equal(t, []string{"--plan"}, docs[0].Args)
	require.Equal(t, []string{"y", "<enter>"}, docs[0].Input)
	require.Equal(t, []string{`"Applied 3 changes"`}, docs[0].Expect)
	require.NotNil(t, docs[0].Screen)
	require.Equal(t, 30, docs[0].Screen.Rows)
	require.Equal(t, 100, docs[0].Screen.Cols)
	require.Equal(t, int64(45_000), docs[0].MaxMs)

	// The names the RUNNER reads, because a field it does not read is the same
	// dead wiring with one more hop in it. These are the keys in
	// runner/src/drivers/terminal.ts, not the Go field names.
	body, err := json.Marshal(docs[0])
	require.NoError(t, err)
	for _, key := range []string{
		`"name":"deploy-plan"`, `"command":"./bin/deploy"`, `"args":["--plan"]`,
		`"screen":{"rows":30,"cols":100}`, `"maxMs":45000`,
	} {
		require.Containsf(t, string(body), key, "the runner reads %s and the document does not carry it", key)
	}
	// input is checked after a decode rather than in the bytes, because Go's
	// encoder writes a key name as \u003center\u003e. That is the same string
	// to every JSON reader and it is not the same string to strings.Contains,
	// so asserting on the raw bytes would be asserting on Go's escaping habits
	// rather than on what the runner receives.
	var back struct {
		Input []string `json:"input"`
	}
	require.NoError(t, json.Unmarshal(body, &back))
	require.Equal(t, []string{"y", "<enter>"}, back.Input,
		"the key names a workflow types did not survive the wire")
}

// A workflow that declared no screen must reach the runner with no screen, so
// it is driven through a pipe. Sending one would put it on a pseudo terminal
// that echoes what the driver types, and its expectations could then be met by
// its own input.
func TestTerminalDocs_NoScreenIsSentAsNoScreen(t *testing.T) {
	w := aDeployWorkflow()
	w.Screen = nil
	docs := orchestratorFor(t, t.TempDir(), terminalManifest(w)).terminalDocs(nil)
	require.Nil(t, docs[0].Screen)
	body, err := json.Marshal(docs[0])
	require.NoError(t, err)
	require.NotContains(t, string(body), `"screen"`)
}

// The working directory is resolved here and sent absolute. The runner is
// started from somewhere the manifest never mentions, so a relative path
// resolved there would name a different directory than the author wrote down.
func TestTerminalDocs_TheWorkingDirectoryIsResolvedBeforeItIsSent(t *testing.T) {
	root := t.TempDir()
	relative := aDeployWorkflow()
	relative.Cwd = "tools/cli"
	absolute := aDeployWorkflow()
	absolute.Name = "elsewhere"
	absolute.Cwd = filepath.Join(root, "somewhere", "else")
	none := aDeployWorkflow()
	none.Name = "default-directory"

	docs := orchestratorFor(t, root, terminalManifest(relative, absolute, none)).terminalDocs(nil)
	require.Equal(t, filepath.Join(root, "tools", "cli"), docs[0].Cwd,
		"a relative directory reached the runner unresolved")
	require.Equal(t, filepath.Join(root, "somewhere", "else"), docs[1].Cwd,
		"an absolute directory was resolved against the root a second time")
	require.Equal(t, root, docs[2].Cwd,
		"a workflow with no directory of its own did not get the project root")
}

// --only selects across both lists, so a person naming one workflow gets that
// workflow whichever surface it is written for.
func TestTerminalDocs_OnlySelectsAcrossBothLists(t *testing.T) {
	m := terminalManifest(aDeployWorkflow())
	m.TerminalWorkflows = append(m.TerminalWorkflows, schema.TerminalWorkflow{
		Name: "migrate", Description: "The migrate command applies the pending migration.",
		Command: "./bin/migrate", Expect: []string{`"applied"`},
	})
	m.Workflows = []schema.Workflow{{Name: "checkout", Description: "Buy one item."}}
	o := orchestratorFor(t, t.TempDir(), m)

	require.Len(t, o.terminalDocs(nil), 2, "no filter runs every terminal workflow")
	picked := o.terminalDocs([]string{"migrate"})
	require.Len(t, picked, 1)
	require.Equal(t, "migrate", picked[0].Name)
	require.Empty(t, o.workflowDocs([]string{"migrate"}),
		"--only on a terminal workflow still ran a browser workflow")
	require.Empty(t, o.terminalDocs([]string{"checkout"}),
		"--only on a browser workflow still ran a terminal workflow")
}

// Which surface the runner is told about, meaning whether it opens a browser.
// A run with both kinds is a web run that also has terminal workflows in it;
// saying otherwise would stop the browser half from running at all.
func TestSurfaceFor_TerminalOnlyWhenThereIsNothingForABrowserToDo(t *testing.T) {
	web := []workflowDoc{{Name: "checkout"}}
	term := []terminalDoc{{Name: "deploy"}}
	require.Equal(t, "terminal", surfaceFor(nil, term, nil, ""))
	require.Equal(t, "", surfaceFor(web, term, nil, ""),
		"a run with browser workflows was told it was a terminal run, so the browser half would never run")
	require.Equal(t, "", surfaceFor(web, nil, nil, ""))
}

// Every command that runs declared workflows reaches the runner through
// Orchestrator.Test, which builds the terminal documents only through
// terminalDocs. A second place that built one would send a workflow without
// its resolved directory or its budget, which is the shape this package's
// workflow budget test already refuses for the browser half.
func TestEveryTerminalDocumentIsBuiltByTerminalDocs(t *testing.T) {
	root, err := filepath.Abs(".")
	require.NoError(t, err)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	var builders []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(root, e.Name()))
		require.NoError(t, readErr)
		for i, line := range strings.Split(string(body), "\n") {
			if terminalDocLiteral.MatchString(line) {
				builders = append(builders, fmt.Sprintf("%s:%d", e.Name(), i+1))
			}
		}
	}
	require.Len(t, builders, 1,
		"a terminal document built anywhere but terminalDocs would reach the runner without its resolved directory or its budget: %v", builders)
	require.True(t, strings.HasPrefix(builders[0], "test.go:"), builders[0])
}

var terminalDocLiteral = regexp.MustCompile(`(^|[^\w\]])terminalDoc\{`)

// THE HOP THE WHOLE LANE EXISTS FOR, asserted on the source because there is
// no way to reach Test without a running environment, and paired below with
// the real runner driving a real program. Test is where the manifest's
// terminal workflows are counted, sent, and allowed to decide a verdict; each
// of those three lines deleted on its own leaves every other test in this file
// green.
func TestTest_SendsTheTerminalWorkflowsAndCountsThem(t *testing.T) {
	body, err := os.ReadFile("test.go")
	require.NoError(t, err)
	src := string(body)
	for _, line := range []string{
		"terminals := o.terminalDocs(opts.Only)",
		"if len(workflows)+len(terminals) == 0 {",
		`o.reportRunStarted(rs, id, "workflows", runStartedAt, len(workflows)+len(terminals))`,
		// Two lines rather than one since the mobile surface arrived: the
		// surface is computed above the guard that counts the workflows,
		// because a manifest declaring only mobile workflows has an empty
		// browser list and an empty terminal list. Both halves are asserted,
		// so deleting either still reds this test.
		"surfaceFor(workflows, terminals,",
		"Terminal: terminals, Surface: surface,",
	} {
		require.Containsf(t, src, line,
			"Orchestrator.Test no longer carries %q, so terminal workflows are built and never run", line)
	}
}

// END TO END, through the real runner, against a program that genuinely draws
// a screen.
//
// This is the test that would have caught the defect. It starts from a
// manifest, builds the job document the way Test does, hands it to the actual
// af-runner over the actual JSON boundary, and reads the verdict back out of
// the actual report. Both arms, because a pass whose fail cannot be produced
// proves nothing: the two runs differ in one expectation and in nothing else.
func TestTest_ARealFullScreenProgramIsDrivenAndCounted(t *testing.T) {
	runner := requireRunner(t)
	tui, err := filepath.Abs(filepath.Join("..", "..", "..", "runner", "test", "fixtures", "menu-tui.mjs"))
	require.NoError(t, err)
	if _, statErr := os.Stat(tui); statErr != nil {
		t.Fatal("the full screen fixture is missing, so this proved nothing: " + statErr.Error())
	}
	drive := func(expect string) TestReport {
		t.Helper()
		m := terminalManifest(schema.TerminalWorkflow{
			Name:        "inbox",
			Description: "The inbox opens on Drafts, the arrows move down it, and Enter opens a row.",
			Command:     "node",
			Args:        []string{tui},
			Input:       []string{"<down>", "<down>", "<enter>", "q"},
			Expect:      []string{expect},
			Screen:      &schema.TerminalScreen{Rows: 12, Cols: 50},
			Budget:      &schema.TerminalBudget{Duration: "30s"},
		})
		o := orchestratorFor(t, t.TempDir(), m)
		terminals := o.terminalDocs(nil)
		require.Len(t, terminals, 1)
		out, runErr := o.invokeRunner(context.Background(), runner, jobDocument{
			BaseURL:   "http://127.0.0.1:45999",
			Artifacts: filepath.Join(t.TempDir(), "artifacts"),
			Workflows: o.workflowDocs(nil),
			Terminal:  terminals,
			Surface:   surfaceFor(o.workflowDocs(nil), terminals, nil, ""),
			WorkDir:   o.opts.Root,
			Headless:  true,
		})
		require.NoError(t, runErr,
			"the runner produced no report at all, which is what a document it cannot read looks like")
		var rep TestReport
		require.NoError(t, json.Unmarshal(out, &rep))
		return rep
	}

	passed := drive(`"Eleven posts are live."`)
	require.Len(t, passed.Results, 1)
	require.Equal(t, "pass", passed.Results[0].Outcome.Verdict, passed.Results[0].Outcome.Detail)
	require.Equal(t, "inbox", passed.Results[0].Workflow)
	// Counted like a browser workflow, which is the point of returning the
	// same result shape: a terminal pass is a pass in the verdict, and a run
	// made only of terminal workflows is not a run that verified nothing.
	require.Equal(t, 1, passed.Passed)
	require.Equal(t, 0, passed.Failed)
	require.False(t, passed.NothingVerified(),
		"a run whose only workflow passed reported that nothing was verified")
	require.False(t, passed.AnyFailed())
	// The rendered screen is the evidence, and the report carries it.
	steps := strings.Join(passed.Results[0].Steps, "\n")
	require.Contains(t, steps, "> Published")
	require.Contains(t, steps, "Eleven posts are live.")

	failed := drive(`"Forty posts are live."`)
	require.Equal(t, "fail", failed.Results[0].Outcome.Verdict, failed.Results[0].Outcome.Detail)
	require.Equal(t, 1, failed.Failed)
	require.True(t, failed.AnyFailed(),
		"a terminal workflow that failed did not count against the application")
}

// The environment's address reaches the program, which is how a command line
// tool under test talks to the thing being rehearsed rather than to whatever
// the developer's shell points at.
func TestTest_TheEnvironmentAddressReachesTheProgram(t *testing.T) {
	runner := requireRunner(t)
	m := terminalManifest(schema.TerminalWorkflow{
		Name:        "reads-the-address",
		Description: "The command prints the address of the environment it was pointed at.",
		Command:     "node",
		Args:        []string{"-e", `process.stdout.write("base=" + process.env.AF_BASE_URL)`},
		Expect:      []string{`"base=http://127.0.0.1:45999"`},
	})
	o := orchestratorFor(t, t.TempDir(), m)
	terminals := o.terminalDocs(nil)
	out, err := o.invokeRunner(context.Background(), runner, jobDocument{
		BaseURL:   "http://127.0.0.1:45999",
		Artifacts: filepath.Join(t.TempDir(), "artifacts"),
		Terminal:  terminals,
		Surface:   surfaceFor(nil, terminals, nil, ""),
		WorkDir:   o.opts.Root,
		Headless:  true,
	})
	require.NoError(t, err)
	var rep TestReport
	require.NoError(t, json.Unmarshal(out, &rep))
	require.Len(t, rep.Results, 1)
	require.Equal(t, "pass", rep.Results[0].Outcome.Verdict, rep.Results[0].Outcome.Detail)
}
