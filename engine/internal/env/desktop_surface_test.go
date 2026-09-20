package env

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The engine half of the desktop surface: turning a manifest's desktop
// workflows into the document the runner reads.
//
// THE DEFECT THESE EXIST FOR is the one the terminal lane found and this lane
// repeated one surface over. A driver can be written, tested against a real
// application and proven in both directions, and still be unreachable, because
// nothing between the manifest and the runner carries it. Everything here is
// about that hop: the workflows are built, the application is resolved, the
// surface is named, and the count a run declares includes them.

func desktopManifest() *schema.Manifest {
	return &schema.Manifest{
		Name:     "shop",
		Services: []schema.Service{{Name: "web", Port: 3000}},
		Desktop: &schema.DesktopApplication{
			Kind: "electron", Application: "bin/electron", Args: []string{"."},
		},
		DesktopWorkflows: []schema.DesktopWorkflow{
			{
				Name: "sign-in-desktop", Description: "Sign in and land on a signed in screen.",
				Expect:  []string{`"Welcome back"`},
				Answers: map[string]string{"Email address": "person@example.com"},
				Budget:  &schema.DesktopBudget{Duration: "90s", Steps: 25},
			},
			{
				Name: "open-the-inbox", Description: "Open the inbox and confirm it lists the drafts.",
				Expect: []string{`"Drafts"`},
				Budget: &schema.DesktopBudget{Duration: "30s", Steps: 10},
			},
		},
	}
}

// The budget reaches the runner as the numbers it actually enforces. A
// workflow whose duration never arrived would run to the runner's own default
// and a person who wrote 90s would be told nothing about why it stopped at two
// minutes.
func TestDesktopDocs_CarriesTheExpectationsTheAnswersAndTheBudget(t *testing.T) {
	o := orchestratorFor(t, t.TempDir(), desktopManifest())
	docs := o.desktopDocs(nil)
	require.Len(t, docs, 2)
	require.Equal(t, "sign-in-desktop", docs[0].Name)
	require.Equal(t, []string{`"Welcome back"`}, docs[0].Expect)
	require.Equal(t, "person@example.com", docs[0].Answers["Email address"])
	require.Equal(t, 25, docs[0].MaxSteps)
	require.Equal(t, int64(90_000), docs[0].MaxMs)
	require.Equal(t, int64(30_000), docs[1].MaxMs)
}

// The same --only set the other two lists obey, so a person naming one
// workflow gets that workflow whichever surface it is written for.
func TestDesktopDocs_IsFilteredByOnly(t *testing.T) {
	o := orchestratorFor(t, t.TempDir(), desktopManifest())
	picked := o.desktopDocs([]string{"open-the-inbox"})
	require.Len(t, picked, 1)
	require.Equal(t, "open-the-inbox", picked[0].Name)
	require.Empty(t, o.desktopDocs([]string{"checkout"}),
		"--only on a browser workflow still ran a desktop workflow")
}

// THE PATH IS RESOLVED HERE, ABSOLUTE. The runner is a subprocess started from
// somewhere the manifest never mentions, so a relative path resolved there
// names a different file, and the symptom is an application that could not be
// found for a reason nothing in the report could name.
func TestDesktopApp_ResolvesTheApplicationPathAgainstTheManifest(t *testing.T) {
	root := t.TempDir()
	o := orchestratorFor(t, root, desktopManifest())
	app := o.desktopApp(o.desktopDocs(nil))
	require.NotNil(t, app)
	require.Equal(t, "electron", app.Kind)
	require.Equal(t, filepath.Join(root, "bin/electron"), app.ExecutablePath)
	require.True(t, filepath.IsAbs(app.ExecutablePath))
	require.Equal(t, []string{"."}, app.Args)
	// An Electron application is launched by its binary, so it carries no
	// bundle and no name to look up.
	require.Empty(t, app.BundlePath)

	// An absolute path is left alone rather than joined onto the root, which
	// would have produced a path under the project for every system
	// application anybody named.
	m := desktopManifest()
	m.Desktop.Application = "/System/Applications/TextEdit.app"
	m.Desktop.Kind = "macos"
	m.Desktop.Process = "TextEdit"
	native := orchestratorFor(t, root, m)
	app = native.desktopApp(native.desktopDocs(nil))
	require.Equal(t, "/System/Applications/TextEdit.app", app.BundlePath)
	require.Equal(t, "TextEdit", app.Name)
	require.Empty(t, app.ExecutablePath)
}

// An application nothing opens is not sent. The validator already refuses that
// manifest; this keeps the document honest even if it ever stops, because a
// document carrying an application with no workflows invites the runner to
// launch something for no reason.
func TestDesktopApp_IsAbsentWhenNoDesktopWorkflowRuns(t *testing.T) {
	o := orchestratorFor(t, t.TempDir(), desktopManifest())
	require.Nil(t, o.desktopApp(nil))
	require.Nil(t, o.desktopApp(o.desktopDocs([]string{"checkout"})),
		"an --only that selected no desktop workflow still sent an application to launch")
}

// THE HOP THE WHOLE LANE EXISTS FOR, asserted on the source because there is
// no way to reach Test without a running environment. Test is where the
// manifest's desktop workflows are counted, sent, and allowed to decide a
// verdict; each of those lines deleted on its own leaves every other test in
// this file green.
func TestTest_SendsTheDesktopWorkflowsAndCountsThem(t *testing.T) {
	body, err := os.ReadFile("test.go")
	require.NoError(t, err)
	src := string(body)
	for _, line := range []string{
		"desktops := o.desktopDocs(opts.Only)",
		"if len(workflows)+len(terminals)+len(desktops) == 0 {",
		`o.reportRunStarted(rs, id, "workflows", runStartedAt, len(workflows)+len(terminals)+len(desktops))`,
		"Terminal: terminals, Desktop: o.desktopApp(desktops), Desktops: desktops,",
		"Surface:   surfaceFor(workflows, terminals, desktops),",
	} {
		require.Containsf(t, src, line,
			"Orchestrator.Test no longer carries %q, so desktop workflows are built and never run", line)
	}
}

// A manifest whose ONLY workflows are desktop ones must be runnable. This is
// the count that refused it: three lists exist and the guard added two of
// them, so the surface was complete, transported, and reported as a manifest
// that declares no workflows at all.
func TestTest_ADesktopOnlyManifestIsNotRefusedAsHavingNoWorkflows(t *testing.T) {
	o := orchestratorFor(t, t.TempDir(), desktopManifest())
	require.NotEmpty(t, o.desktopDocs(nil))
	require.Empty(t, o.workflowDocs(nil))
	require.Empty(t, o.terminalDocs(nil))
	// The expression the guard in Test uses, asserted directly: with the
	// desktop term missing this is zero and the run is refused.
	require.NotZero(t,
		len(o.workflowDocs(nil))+len(o.terminalDocs(nil))+len(o.desktopDocs(nil)),
		"a manifest with only desktop workflows counts as declaring none")
}
