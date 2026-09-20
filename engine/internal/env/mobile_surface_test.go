package env

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The hop that makes the mobile drivers reachable.
//
// THE DEFECT THIS EXISTS FOR is the one the terminal surface had before it and
// the one this repository keeps rediscovering: the iOS driver was written,
// driven end to end against a real simulator, and unreachable. Nothing in a
// manifest could ask for it, so the engine sent a job document that never
// named the surface and the driver never ran outside a test. A capability
// nobody can name is not a capability, and `available: true` on a surface no
// manifest can reach is the same false promise as a flag over a dead code
// path.

func mobileManifest(platform, app string) *schema.Manifest {
	return &schema.Manifest{
		Name: "shop",
		Mobile: &schema.Mobile{
			Platform: platform, ID: "dev.antifailure.probe", App: app,
		},
		MobileWorkflows: []schema.MobileWorkflow{{
			Name:        "sign-in",
			Description: "Signing in shows the greeting on the home screen.",
			Expect:      []string{`"Welcome back"`},
			Budget:      &schema.MobileBudget{Duration: "90s", Steps: 12},
		}},
	}
}

func TestMobileDocs_TheManifestReachesTheRunnerUnderTheNamesItReads(t *testing.T) {
	o := &Orchestrator{opts: Options{Manifest: mobileManifest("ios", "build/Probe.app"), Root: "/srv/shop"}}

	docs := o.mobileWorkflowDocs(nil)
	require.Len(t, docs, 1)
	// A mobile workflow IS a workflowDoc. That is the design rather than a
	// convenience: the planner reads a Snapshot and cannot tell what produced
	// it, so the same document drives a page and an application.
	require.Equal(t, "sign-in", docs[0].Name)
	require.Equal(t, []string{`"Welcome back"`}, docs[0].Expect)
	require.Equal(t, 12, docs[0].MaxSteps)
	require.Equal(t, int64(90_000), docs[0].MaxMs)
	// And the fields a browser workflow has and an application cannot: an app
	// has no addresses and signs in through its own screens.
	require.Empty(t, docs[0].StartPath)
	require.Empty(t, docs[0].Persona)

	cfg := o.mobileConfigDoc()
	require.NotNil(t, cfg)
	require.Equal(t, "dev.antifailure.probe", cfg.ID)
}

// The artifact is resolved HERE and sent absolute, for the reason terminalDocs
// resolves a working directory: the runner is a subprocess started from
// somewhere the manifest never mentions, so a relative path resolved there
// would name a different file than the author wrote down, and the symptom
// would be an install failing for a reason nothing in the report could name.
func TestMobileDocs_TheApplicationPathIsResolvedBeforeItIsSent(t *testing.T) {
	o := &Orchestrator{opts: Options{Manifest: mobileManifest("ios", "build/Probe.app"), Root: "/srv/shop"}}
	require.Equal(t, filepath.Join("/srv/shop", "build/Probe.app"), o.mobileConfigDoc().App)

	// An absolute path is left exactly as written.
	abs := mobileManifest("ios", "/opt/artifacts/Probe.app")
	o2 := &Orchestrator{opts: Options{Manifest: abs, Root: "/srv/shop"}}
	require.Equal(t, "/opt/artifacts/Probe.app", o2.mobileConfigDoc().App)

	// And a run against an application already on the device sends no path at
	// all rather than the manifest's own directory.
	none := mobileManifest("android", "")
	o3 := &Orchestrator{opts: Options{Manifest: none, Root: "/srv/shop"}}
	require.Empty(t, o3.mobileConfigDoc().App)
}

func TestMobileDocs_OnlySelectsAcrossTheMobileList(t *testing.T) {
	m := mobileManifest("ios", "")
	m.MobileWorkflows = append(m.MobileWorkflows, schema.MobileWorkflow{
		Name:        "delete-account",
		Description: "Deleting the account returns to the sign in screen.",
		Expect:      []string{`"Signed out"`},
		Budget:      &schema.MobileBudget{Duration: "30s", Steps: 5},
	})
	o := &Orchestrator{opts: Options{Manifest: m, Root: "/srv/shop"}}
	docs := o.mobileWorkflowDocs([]string{"delete-account"})
	require.Len(t, docs, 1)
	require.Equal(t, "delete-account", docs[0].Name)
}

// The surface is what tells the runner which device to open, and getting it
// wrong is not a cosmetic error: "" would open a browser against an
// application that has no address.
func TestSurfaceFor_NamesTheMobilePlatform(t *testing.T) {
	mobiles := []workflowDoc{{Name: "sign-in"}}
	term := []terminalDoc{{Name: "deploy"}}

	require.Equal(t, "ios", surfaceFor(nil, nil, mobiles, "ios"))
	require.Equal(t, "android", surfaceFor(nil, nil, mobiles, "android"))
	// Terminal workflows open nothing, so they do not change which device is
	// opened. A run may have both.
	require.Equal(t, "ios", surfaceFor(nil, term, mobiles, "ios"))
	// No mobile workflows means no mobile surface however the manifest is
	// configured, so a stale mobile block cannot take a browser run's device.
	require.Equal(t, "", surfaceFor(nil, nil, nil, "ios"))
	// And an unknown platform is never guessed at.
	require.Equal(t, "", surfaceFor(nil, nil, mobiles, "windows-phone"))
}

// THE HOP ITSELF, asserted on the source because reaching Test needs a running
// environment and a booted device. Each of these lines deleted on its own
// leaves every other test in this package green while making the mobile
// surface unreachable again, which is exactly the defect this file is named
// for.
func TestTest_SendsTheMobileWorkflowsAndCountsThem(t *testing.T) {
	body, err := os.ReadFile("test.go")
	require.NoError(t, err)
	src := string(body)
	for _, line := range []string{
		"mobiles := o.mobileWorkflowDocs(opts.Only)",
		"surface := surfaceFor(workflows, terminals, mobiles, mobilePlatform)",
		// Counted before the guard that refuses a run with nothing to do. A
		// manifest declaring only mobile workflows has an empty browser list
		// and an empty terminal list, so without this the whole surface is
		// refused as declaring nothing to run.
		"workflows = mobiles",
		"Mobile:    o.mobileConfigDoc(),",
	} {
		require.Containsf(t, src, line,
			"Orchestrator.Test no longer carries %q, so mobile workflows are built and never run", line)
	}
}

// A manifest whose only workflows are mobile ones is a run, not an empty one.
// The guard that refuses "no workflows to run" counts the browser and terminal
// lists, and mobile workflows are in neither until Test assigns them, so this
// is the ordering that makes a mobile-only manifest runnable at all.
func TestTest_AMobileOnlyManifestIsNotRefusedAsEmpty(t *testing.T) {
	o := &Orchestrator{opts: Options{Manifest: mobileManifest("ios", ""), Root: "/srv/shop"}}
	require.Empty(t, o.workflowDocs(nil), "the fixture accidentally declares browser workflows")
	require.Empty(t, o.terminalDocs(nil), "the fixture accidentally declares terminal workflows")
	// So the only thing that can make the run non empty is the mobile list.
	require.NotEmpty(t, o.mobileWorkflowDocs(nil))
}
