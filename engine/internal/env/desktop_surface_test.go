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

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The engine half of the desktop surface, and the hop that did not exist.
//
// THE DEFECT. runner/src/drivers/desktop.ts was written, unit tested, proven
// against a real Electron application and available in the registry. A
// workflow could name the surface, the engine would select the driver, and
// `runDesktop` is handed an application to open or it refuses the run. No
// manifest could name one and no job document carried one, so every desktop
// run that could ever have happened would have been refused after an
// environment had been built and paid for.
//
// That is the terminal driver's defect with the pieces rearranged: there, the
// workflows could not be named; here, they could, and the thing they are
// driven in could not. Both look like a working feature from every angle
// except the one that runs it. So every test below is about one hop of
// manifest, to job document, to the real runner, to a counted verdict.

func desktopManifest(app *schema.DesktopApplication, w ...schema.Workflow) *schema.Manifest {
	return &schema.Manifest{Name: "app", Desktop: app, Workflows: w}
}

func aDesktopWorkflow(name string) schema.Workflow {
	return schema.Workflow{
		Name:        name,
		Description: "Sign in and confirm you land on a signed in screen.",
		Surface:     schema.SurfaceDesktop,
		Persona:     "ada",
		Expect:      []string{"Welcome back"},
	}
}

func anElectronApp() *schema.DesktopApplication {
	return &schema.DesktopApplication{
		Kind:        schema.DesktopElectron,
		Application: "/opt/electron/Electron",
		Args:        []string{"./app"},
	}
}

// The application reaches the runner whole, under the names the RUNNER reads,
// which are ElectronTarget's and not the manifest's. A field it does not read
// is the same dead wiring with one more hop in it.
func TestDesktopApp_TheApplicationReachesTheRunnerUnderTheNamesItReads(t *testing.T) {
	o := orchestratorFor(t, t.TempDir(), desktopManifest(anElectronApp(), aDesktopWorkflow("sign-in")))
	app := o.desktopApp(o.workflowDocs(nil))
	require.NotNil(t, app, "a manifest whose workflow drives the desktop sent no application")

	body, err := json.Marshal(app)
	require.NoError(t, err)
	for _, key := range []string{
		`"kind":"electron"`, `"executablePath":"/opt/electron/Electron"`, `"args":["./app"]`,
	} {
		require.Containsf(t, string(body), key,
			"the runner reads %s and the document does not carry it", key)
	}
	// And NOT the manifest's own spelling, which nothing in the runner reads.
	require.NotContains(t, string(body), `"application"`)
}

// A native application is launched by its bundle and then FOUND by name,
// because opening a bundle returns before the application is ready. Both
// halves are sent, and the name is the one normalisation derived when the
// manifest left it out.
func TestDesktopApp_ANativeApplicationIsSentAsABundleAndAName(t *testing.T) {
	m := desktopManifest(&schema.DesktopApplication{
		Kind:        schema.DesktopMacOS,
		Application: "/Applications/Ledger.app",
		Process:     "Ledger",
	}, aDesktopWorkflow("sign-in"))
	app := orchestratorFor(t, t.TempDir(), m).desktopApp(orchestratorFor(t, t.TempDir(), m).workflowDocs(nil))
	require.NotNil(t, app)
	require.Equal(t, "/Applications/Ledger.app", app.BundlePath)
	require.Equal(t, "Ledger", app.Name)
	// An Electron binary is not what a native run is given, and sending one
	// would be a second answer to which of the two readers opens this.
	require.Empty(t, app.ExecutablePath)

	body, err := json.Marshal(app)
	require.NoError(t, err)
	require.Contains(t, string(body), `"bundlePath":"/Applications/Ledger.app"`)
	require.Contains(t, string(body), `"name":"Ledger"`)
}

// The path is resolved here and sent absolute. The runner is started from
// somewhere the manifest never mentions, so a relative path resolved there
// would name a different file, and the symptom is an application that could
// not be found for a reason nothing in the report could name.
func TestDesktopApp_ARelativePathIsResolvedBeforeItIsSent(t *testing.T) {
	root := t.TempDir()
	relative := anElectronApp()
	relative.Application = "node_modules/.bin/electron"
	app := orchestratorFor(t, root, desktopManifest(relative, aDesktopWorkflow("sign-in"))).
		desktopApp(orchestratorFor(t, root, desktopManifest(relative, aDesktopWorkflow("sign-in"))).workflowDocs(nil))
	require.Equal(t, filepath.Join(root, "node_modules", ".bin", "electron"), app.ExecutablePath,
		"a relative application reached the runner unresolved")

	absolute := anElectronApp()
	absolute.Application = filepath.Join(root, "elsewhere", "Electron")
	o := orchestratorFor(t, root, desktopManifest(absolute, aDesktopWorkflow("sign-in")))
	require.Equal(t, filepath.Join(root, "elsewhere", "Electron"), o.desktopApp(o.workflowDocs(nil)).ExecutablePath,
		"an absolute application was resolved against the root a second time")
}

// A run with nothing to drive sends no application, so the runner is never
// asked to launch one for a run with no use for it. The --only filter is the
// case that makes this more than a restatement of the validator: the manifest
// is legal and this particular RUN has no desktop workflow in it.
func TestDesktopApp_ARunWithNoDesktopWorkflowSendsNoApplication(t *testing.T) {
	root := t.TempDir()
	m := desktopManifest(anElectronApp(), aDesktopWorkflow("sign-in"))
	m.TerminalWorkflows = []schema.TerminalWorkflow{{
		Name: "deploy-plan", Description: "The deploy command shows the plan.",
		Command: "./bin/deploy", Expect: []string{`"Applied"`},
	}}
	o := orchestratorFor(t, root, m)

	require.NotNil(t, o.desktopApp(o.workflowDocs(nil)), "the unfiltered run drives it")
	require.Nil(t, o.desktopApp(o.workflowDocs([]string{"deploy-plan"})),
		"a run that drives no desktop workflow was still told to launch an application")

	// And a manifest with no application at all sends none however its
	// workflows are filtered, which is what keeps the nil above meaningful.
	web := orchestratorFor(t, root, desktopManifest(nil, schema.Workflow{
		Name: "checkout", Description: "Buy one item.", Surface: schema.SurfaceWeb,
	}))
	require.Nil(t, web.desktopApp(web.workflowDocs(nil)))
}

// The desktop surface is what the runner is told to dispatch, read from the
// manifest. Paired with the document above: one says WHICH driver and the
// other says what it opens, and a run needs both.
func TestSurfaceFor_ADesktopManifestSelectsTheDesktopDriver(t *testing.T) {
	o := orchestratorFor(t, t.TempDir(), desktopManifest(anElectronApp(), aDesktopWorkflow("sign-in")))
	require.Equal(t, "desktop", surfaceFor(o.workflowDocs(nil), nil))
}

// THE COUNT THAT WOULD HAVE MADE ALL OF IT INERT, checked rather than assumed.
//
// Test refuses a run that declares no workflows, and that guard counts two
// lists. The question this answers is whether a manifest whose only workflows
// drive the desktop is refused by it as declaring none, which is the shape of
// defect this whole seam exists to close: a schema that accepts it, a
// validator that passes it, documents that get built, and a run that stops one
// line before any of it matters.
//
// The answer is no, and the reason is structural rather than lucky. A desktop
// workflow is a `workflows` entry with a surface on it, so it is built by
// workflowDocs and counted by the same len() the browser ones are. The
// assertion is on the count the guard actually reads, plus the guard's own
// line, because either one alone can go stale without the other saying so.
func TestTest_ADesktopOnlyRunIsNotRefusedAsDeclaringNoWorkflows(t *testing.T) {
	o := orchestratorFor(t, t.TempDir(), desktopManifest(anElectronApp(), aDesktopWorkflow("sign-in")))
	workflows := o.workflowDocs(nil)
	terminals := o.terminalDocs(nil)
	require.NotZero(t, len(workflows)+len(terminals),
		"a manifest whose only workflow drives the desktop counted as declaring none, "+
			"so Test would refuse it before anything it declared could run")

	body, err := os.ReadFile("test.go")
	require.NoError(t, err)
	src := string(body)
	for _, line := range []string{
		"if len(workflows)+len(terminals) == 0 {",
		`o.reportRunStarted(rs, id, "workflows", runStartedAt, len(workflows)+len(terminals))`,
		"Desktop:   o.desktopApp(workflows),",
		"Desktop:   job.Desktop,",
	} {
		require.Containsf(t, src, line,
			"Orchestrator.Test no longer carries %q, so a desktop run is built and never driven", line)
	}
}

// One builder, so that the resolution above cannot be skipped by a second one.
//
// A desktopAppDoc built anywhere but desktopApp would reach the runner with
// whatever path the manifest happened to carry and, for a native application,
// no name to find it by. That is not hypothetical: there are three places in
// this package that build a job document, and the two that are not Test send
// browser work. A fourth, or an edit to either of those two, is exactly how a
// surface comes to work from one entry point and not another.
//
// Asserted on the source because the alternative is asserting on a document
// somebody has already built, which is the thing being questioned.
func TestDesktopApp_TheDocumentIsBuiltInOnePlace(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	var builders []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, readErr := os.ReadFile(name)
		require.NoError(t, readErr)
		for i, line := range strings.Split(string(body), "\n") {
			if desktopAppDocLiteral.MatchString(line) {
				builders = append(builders, fmt.Sprintf("%s:%d", name, i+1))
			}
		}
	}
	// Two, because a native application and an Electron one are returned from
	// different branches of the same function. Both are in desktopApp and the
	// assertion below is what says so.
	require.Lenf(t, builders, 2,
		"a desktop application built anywhere but desktopApp would reach the runner "+
			"with an unresolved path and, for a native application, no name to be found by: %v", builders)
	for _, at := range builders {
		require.Truef(t, strings.HasPrefix(at, "test.go:"), "built outside test.go, at %s", at)
	}
}

var desktopAppDocLiteral = regexp.MustCompile(`(^|[^\w\]])desktopAppDoc\{`)

// END TO END, from a real antifailure.yaml through the real runner into the
// real Electron application, and back out as a counted verdict.
//
// This is the test that would have caught the defect, and it is the only one
// here that can: every hop between the manifest and the verdict is a real one.
// It reads a manifest off disk with the engine's own loader, builds the job
// document the way Test does, hands it to af-runner over the actual JSON
// boundary, and reads the outcome out of the actual report. Both arms, because
// a pass whose fail cannot be produced proves nothing: the two runs differ in
// one expectation and in nothing else.
//
// The application is runner/test/fixtures/ledger, which the desktop driver was
// proven against. Electron is deliberately not a dependency of this repository,
// so a machine that wants to run this points AF_ELECTRON_BINARY at one;
// without it this SKIPS with the reason named, and AF_REQUIRE_DESKTOP turns
// that skip into a failure for a machine that claims to support the surface.
func TestTest_ARealElectronApplicationIsDrivenFromAManifestAndCounted(t *testing.T) {
	runner := requireRunner(t)
	binary := os.Getenv("AF_ELECTRON_BINARY")
	switch {
	case binary == "":
		requireDesktop(t, "AF_ELECTRON_BINARY is not set, so there is no Electron runtime to drive. "+
			"This repository does not depend on Electron: point that variable at one "+
			"(node_modules/electron/dist/Electron.app/Contents/MacOS/Electron on macOS).")
	case !exists(binary):
		requireDesktop(t, "AF_ELECTRON_BINARY is set to "+binary+" and there is no such file.")
	}

	fixture, err := filepath.Abs(filepath.Join("..", "..", "..", "runner", "test", "fixtures", "ledger"))
	require.NoError(t, err)
	if !exists(filepath.Join(fixture, "main.js")) {
		t.Fatal("the Electron fixture is missing, so this proved nothing: " + fixture)
	}

	drive := func(expect string) TestReport {
		t.Helper()
		// A real manifest, written to disk and read back by the engine's own
		// loader, rather than a Manifest built in Go. The parser, the
		// normaliser and the validator are three of the hops this lane is
		// about, and a struct literal skips all of them: it would prove the
		// document builder correct about a manifest nobody could have
		// written.
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "antifailure.yaml"), []byte(`version: 1
name: ledger
services:
  - name: web
    port: 3000
personas:
  - name: ada
    email: ada@example.test
desktop:
  kind: electron
  application: `+binary+`
  args: ["`+fixture+`"]
workflows:
  - name: sign-in
    surface: desktop
    persona: ada
    description: Sign in to Ledger and confirm you land on the signed in screen.
    expect: [`+expect+`]
    budget:
      steps: 12
`), 0o644))

		m, loadErr := manifest.Load(filepath.Join(root, "antifailure.yaml"))
		require.NoError(t, loadErr, "the manifest this surface exists for is not a manifest the engine accepts")
		require.Equal(t, schema.SurfaceDesktop, m.Workflows[0].Surface)

		o := orchestratorFor(t, root, m)
		workflows := o.workflowDocs(nil)
		require.Len(t, workflows, 1)
		app := o.desktopApp(workflows)
		require.NotNil(t, app, "the manifest named an application and the document carried none")

		out, runErr := o.invokeRunner(context.Background(), runner, jobDocument{
			BaseURL:   "http://127.0.0.1:45999",
			Artifacts: filepath.Join(t.TempDir(), "artifacts"),
			Workflows: workflows,
			Desktop:   app,
			Surface:   surfaceFor(workflows, nil),
			WorkDir:   root,
			Headless:  true,
		})
		require.NoError(t, runErr,
			"the runner produced no report at all, which is what a document it cannot read looks like")
		var rep TestReport
		require.NoError(t, json.Unmarshal(out, &rep))
		return rep
	}

	passed := drive(`"Welcome back"`)
	require.Len(t, passed.Results, 1)
	require.Equal(t, "pass", passed.Results[0].Outcome.Verdict, passed.Results[0].Outcome.Detail)
	require.Equal(t, "sign-in", passed.Results[0].Workflow)
	// Counted like a browser workflow, which is the point of the driver
	// returning the same result shape: a desktop pass is a pass in the
	// verdict, and a run made only of desktop workflows is not a run that
	// verified nothing.
	require.Equal(t, 1, passed.Passed)
	require.Equal(t, 0, passed.Failed)
	require.False(t, passed.NothingVerified(),
		"a run whose only workflow passed reported that nothing was verified")
	// It did the whole thing through accessible names, which is the evidence
	// that an application was really driven rather than a document really
	// parsed.
	steps := strings.Join(passed.Results[0].Steps, " | ")
	require.Contains(t, steps, "Fill Email address")
	require.Contains(t, steps, "Press Sign in")

	failed := drive(`'"Your order has shipped."'`)
	require.Equal(t, "fail", failed.Results[0].Outcome.Verdict, failed.Results[0].Outcome.Detail)
	require.Equal(t, 1, failed.Failed)
	require.True(t, failed.AnyFailed(),
		"a desktop workflow that failed did not count against the application")
}

// requireDesktop ends a test that could not look, loudly or quietly.
//
// Quietly by default, because a machine with no Electron runtime genuinely
// cannot run the check and a skip that names its reason is the honest answer.
// Loudly under AF_REQUIRE_DESKTOP, following the precedent the runner's own
// desktop suite sets with the same variable: a machine that says it supports
// this surface and then skips its only end to end proof is reporting a pass it
// did not earn.
func requireDesktop(t *testing.T, reason string) {
	t.Helper()
	if os.Getenv("AF_REQUIRE_DESKTOP") != "" {
		t.Fatalf("AF_REQUIRE_DESKTOP is set, so this cannot be skipped: %s", reason)
	}
	t.Skip("NOT CHECKED: " + reason)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
