package env

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The engine half of the phone surface, and the hop that did not exist.
//
// The runner's iOS driver refuses a run whose job document does not name the
// application's identifier, and until this there was no field in the job
// document to carry one. So every iOS run ever submitted was refused in the
// runner, after its environment was built. desktop_surface_test.go is the same
// hop for the desktop and says why each test below is about one hop of
// manifest to job document, under the names the RUNNER reads.

func mobileManifest(app *schema.MobileApplication, w ...schema.Workflow) *schema.Manifest {
	return &schema.Manifest{Name: "app", Mobile: app, Workflows: w}
}

func aPhoneWorkflow(name string) schema.Workflow {
	return schema.Workflow{
		Name:        name,
		Description: "Open the journal on the phone and check that it lists transfers.",
		Surface:     schema.SurfaceIOS,
		Persona:     "ada",
		Expect:      []string{"transfer.posted"},
	}
}

// Under the names the runner's MobileDoc reads, and not the manifest's own
// spelling where the two differ. A field it does not read is dead wiring.
func TestMobileApp_TheApplicationReachesTheRunnerUnderTheNamesItReads(t *testing.T) {
	root := t.TempDir()
	o := orchestratorFor(t, root, mobileManifest(&schema.MobileApplication{
		ID: "com.example.ledger", App: "/builds/Ledger.app", Device: "A1B2-C3D4",
	}, aPhoneWorkflow("read")))
	app := o.mobileApp(o.workflowDocs(nil))
	require.NotNil(t, app, "a manifest whose workflow drives a phone sent no application")

	body, err := json.Marshal(app)
	require.NoError(t, err)
	for _, key := range []string{
		`"id":"com.example.ledger"`, `"app":"/builds/Ledger.app"`, `"device":"A1B2-C3D4"`,
	} {
		require.Containsf(t, string(body), key, "the runner reads %s and the document does not carry it", key)
	}
}

// Resolved here and sent absolute, for the reason the desktop application is:
// the runner is started from somewhere the manifest never mentions.
func TestMobileApp_ARelativeAppIsResolvedBeforeItIsSent(t *testing.T) {
	root := t.TempDir()
	o := orchestratorFor(t, root, mobileManifest(&schema.MobileApplication{
		ID: "com.example.ledger", App: "build/Ledger.app",
	}, aPhoneWorkflow("read")))
	require.Equal(t, filepath.Join(root, "build", "Ledger.app"), o.mobileApp(o.workflowDocs(nil)).App,
		"a relative app reached the runner unresolved")

	abs := filepath.Join(root, "elsewhere", "Ledger.app")
	o = orchestratorFor(t, root, mobileManifest(&schema.MobileApplication{
		ID: "com.example.ledger", App: abs,
	}, aPhoneWorkflow("read")))
	require.Equal(t, abs, o.mobileApp(o.workflowDocs(nil)).App,
		"an absolute app was resolved against the root a second time")
}

// An application already on the device is driven where it is, so no app is
// sent, and an empty one is not resolved into the project root either.
func TestMobileApp_NoAppMeansTheInstalledOneIsDriven(t *testing.T) {
	o := orchestratorFor(t, t.TempDir(), mobileManifest(&schema.MobileApplication{ID: "com.example.ledger"},
		aPhoneWorkflow("read")))
	app := o.mobileApp(o.workflowDocs(nil))
	require.NotNil(t, app)
	require.Empty(t, app.App, "an application that was never named was sent as the project root")
	body, err := json.Marshal(app)
	require.NoError(t, err)
	require.NotContains(t, string(body), `"app"`)
}

// A run with no phone workflow in it sends no application, so the runner is
// never asked to boot a simulator for a run that has no use for one. --only is
// what makes this more than a restatement of the validator: the manifest is
// legal, and this RUN drives no phone.
func TestMobileApp_ARunWithNoPhoneWorkflowSendsNoApplication(t *testing.T) {
	o := orchestratorFor(t, t.TempDir(), mobileManifest(&schema.MobileApplication{ID: "com.example.ledger"},
		aPhoneWorkflow("read")))
	require.Nil(t, o.mobileApp(o.workflowDocs([]string{"something-else"})))

	none := orchestratorFor(t, t.TempDir(), mobileManifest(nil, aPhoneWorkflow("read")))
	require.Nil(t, none.mobileApp(none.workflowDocs(nil)))
}
