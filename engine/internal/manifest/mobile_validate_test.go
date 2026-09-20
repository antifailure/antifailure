package manifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The mobile surface's manifest half.
//
// THE DEFECT THESE EXIST FOR is the one the terminal surface had before it:
// the iOS driver was written, driven against a real simulator and unreachable,
// because nothing in the manifest could ask for it. A driver nobody can name
// is not a feature, however well it works, and the first thing a person needs
// in order to reach it is a key here that the engine accepts, enforces, and
// turns into a job the runner recognises.
//
// The bounds pass keeps the schema's own constraints and
// TestEverySchemaConstraintIsEnforced proves they are live. What is here is
// the cross field part the schema cannot say.

const withAMobileWorkflow = `
version: 1
name: shop
services:
  - name: web
    port: 3000
mobile:
  platform: ios
  id: dev.antifailure.probe
  app: build/Probe.app
  device: 097F0493-B34E-4F84-97F3-6681207F76C1
mobile_workflows:
  - name: sign-in
    description: Signing in shows the greeting on the home screen.
    expect: ['"Welcome back"']
    budget:
      duration: 90s
      steps: 12
`

func TestParse_AcceptsAMobileWorkflow(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withAMobileWorkflow)
	require.NotNil(t, m.Mobile)
	require.Equal(t, "ios", m.Mobile.Platform)
	require.Equal(t, "dev.antifailure.probe", m.Mobile.ID)
	require.Equal(t, "build/Probe.app", m.Mobile.App)
	require.Len(t, m.MobileWorkflows, 1)
	w := m.MobileWorkflows[0]
	require.Equal(t, "sign-in", w.Name)
	require.Equal(t, []string{`"Welcome back"`}, w.Expect)
	require.NotNil(t, w.Budget)
	require.Equal(t, "90s", w.Budget.Duration)
	require.Equal(t, 12, w.Budget.Steps)
}

// A workflow with no budget gets the runner's own, so the number `af explain`
// prints is the number the run will use.
func TestParse_NormalisesTheMobileBudget(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`mobile:
  platform: android
  id: dev.antifailure.probe
mobile_workflows:
  - name: sign-in
    description: Signing in shows the greeting on the home screen.
    expect: ['"Welcome back"']
`)
	w := m.MobileWorkflows[0]
	require.NotNil(t, w.Budget)
	require.Equal(t, schema.DefaultMobileDuration, w.Budget.Duration)
	require.Equal(t, schema.DefaultMobileSteps, w.Budget.Steps)
}

// The DEVICE is never filled in, and the absence is the decision. It means
// "whatever this machine has booted", which is what lets one manifest run on a
// laptop with a single simulator and in a farm with many. Writing a udid in
// here would pin the manifest to whichever machine happened to normalise it.
func TestParse_AMobileRunIsNotGivenADevice(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`mobile:
  platform: ios
  id: dev.antifailure.probe
mobile_workflows:
  - name: sign-in
    description: Signing in shows the greeting on the home screen.
    expect: ['"Welcome back"']
`)
	require.Empty(t, m.Mobile.Device)
	require.Empty(t, m.Mobile.AVD)
}

// Workflows with nothing to drive, and an application nothing drives. Both
// directions are refused, because each is a manifest that says something the
// run does not do.
func TestParse_RefusesMobileWorkflowsWithNoApplication(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`mobile_workflows:
  - name: sign-in
    description: Signing in shows the greeting on the home screen.
    expect: ['"Welcome back"']
`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no application to drive")
}

func TestParse_RefusesAnApplicationNoWorkflowDrives(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`mobile:
  platform: ios
  id: dev.antifailure.probe
`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no mobile workflow drives it")
}

// ONE RUN OPENS ONE THING. A manifest carrying both lists would run its mobile
// workflows and silently not run its browser ones, reporting a verdict that
// covered half of what it declared while looking complete.
func TestParse_RefusesAManifestThatDeclaresBothSurfaces(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`workflows:
  - name: checkout
    description: A shopper buys one item and sees the receipt.
    expect: ["The receipt shows the order number"]
mobile:
  platform: ios
  id: dev.antifailure.probe
mobile_workflows:
  - name: sign-in
    description: Signing in shows the greeting on the home screen.
    expect: ['"Welcome back"']
`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "both browser workflows and mobile workflows")
}

// A field nothing will read is refused rather than ignored, because a manifest
// that says something the run does not do is worse than one that says nothing.
func TestParse_RefusesAndroidOnlyFieldsOnIOS(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"activity: com.example/.Main", "avd: Pixel_8"} {
		_, err := parse(t, minimal+`mobile:
  platform: ios
  id: dev.antifailure.probe
  `+field+`
mobile_workflows:
  - name: sign-in
    description: Signing in shows the greeting on the home screen.
    expect: ['"Welcome back"']
`)
		require.Error(t, err, "an iOS run accepted %q", field)
		require.Contains(t, strings.ToLower(err.Error()), "android")
	}
}

// The artifact has to be the KIND the platform installs. Handing a simulator
// an apk fails inside the device tooling, with a message about a bundle rather
// than about the line of the manifest that was wrong.
func TestParse_RefusesAnArtifactThePlatformCannotInstall(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ platform, app string }{
		{"ios", "build/probe.apk"},
		{"android", "build/Probe.app"},
	} {
		_, err := parse(t, minimal+`mobile:
  platform: `+c.platform+`
  id: dev.antifailure.probe
  app: `+c.app+`
mobile_workflows:
  - name: sign-in
    description: Signing in shows the greeting on the home screen.
    expect: ['"Welcome back"']
`)
		require.Error(t, err, "%s accepted %s", c.platform, c.app)
		require.Contains(t, err.Error(), "app")
	}

	// And the right pairing is accepted, so the rule above is not simply
	// refusing everything.
	m := mustParse(t, minimal+`mobile:
  platform: android
  id: dev.antifailure.probe
  app: build/probe.apk
mobile_workflows:
  - name: sign-in
    description: Signing in shows the greeting on the home screen.
    expect: ['"Welcome back"']
`)
	require.Equal(t, "build/probe.apk", m.Mobile.App)
}

// One namespace across all three lists, because a name is what --only selects
// and what the report prints against a verdict.
func TestParse_RefusesANameSharedWithAnotherList(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`terminal_workflows:
  - name: sign-in
    description: The command line tool signs in and prints the account.
    command: ./bin/cli
    expect: ['"Signed in"']
mobile:
  platform: ios
  id: dev.antifailure.probe
mobile_workflows:
  - name: sign-in
    description: Signing in shows the greeting on the home screen.
    expect: ['"Welcome back"']
`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "both named")
}
