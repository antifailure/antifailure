package manifest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The desktop application, which is the half of the desktop surface that makes
// it reachable rather than merely nameable.
//
// THE DEFECT THESE EXIST FOR. `surface: desktop` on a workflow selects the
// desktop driver, and that driver is given an application to open or it
// refuses the run. Nothing in the manifest could name one. So a manifest could
// be written, accepted by the schema, passed by the validator and built an
// environment for, and the run died in the runner for want of a field no
// person could have supplied. The surface was reachable and the run was dead
// one field short of working, which is the same shape as the driver that could
// not be asked for, one layer up.
//
// The bounds pass refuses a missing kind, an unknown kind and a missing path
// straight from the published schema, and TestEverySchemaConstraintIsEnforced
// measures that. What is tested here is the part no single field can see: the
// pairing of a surface in one part of the document with an application in
// another.

const desktopApp = `desktop:
  kind: electron
  application: ./node_modules/.bin/electron
  args: ["./app"]
`

const aDesktopWorkflow = `workflows:
  - name: preferences
    surface: desktop
    description: Open the preferences window and change the default currency.
    persona: alice
    expect: ["The default currency is euros."]
`

// A workflow that drives the desktop and no application to drive. Refused
// while the manifest is read, which is the whole point: the alternative is an
// environment built and paid for and then a runner refusing the job.
func TestParse_ADesktopWorkflowWithNoApplicationIsRefused(t *testing.T) {
	t.Parallel()
	_, err := parse(t, withPersonas+aDesktopWorkflow)
	msg := messages(problems(t, err))
	require.Contains(t, msg, "A workflow drives the desktop and no desktop application is declared.")
	// The remedy, said in the message rather than left to the reference page,
	// because the person reading this has already written the surface down
	// and needs one more block and not a tour of the schema.
	require.Contains(t, msg, "Add a `desktop` block")
	require.Contains(t, msg, "electron or macos")
}

// And the other direction, which is the quieter mistake. An application
// nothing drives is a block that will never be read: the run opens a browser
// instead, every workflow passes, and nothing anywhere says the application
// somebody named was never launched.
func TestParse_AnApplicationNoWorkflowDrivesIsRefused(t *testing.T) {
	t.Parallel()
	_, err := parse(t, withPersonas+desktopApp+`workflows:
  - name: checkout
    description: Buy one item and see the order confirmed on the screen.
    persona: alice
    expect: ["The order is confirmed."]
`)
	msg := messages(problems(t, err))
	require.Contains(t, msg, "A desktop application is declared and no workflow drives it.")
	require.Contains(t, msg, "surface: desktop")
}

// A manifest with neither is the ordinary one and must not be caught by either
// rule above. This is the liveness arm of the two refusals: without it both
// could be satisfied by a validator that refused every manifest in the world.
func TestParse_AManifestWithNoDesktopAtAllIsUntouched(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withPersonas+`workflows:
  - name: checkout
    description: Buy one item and see the order confirmed on the screen.
    persona: alice
    expect: ["The order is confirmed."]
`)
	require.Nil(t, m.Desktop)
	require.Equal(t, schema.SurfaceWeb, m.Workflows[0].Surface)
}

// The pairing, accepted, with every field surviving the parse under the name
// the engine reads it by. A refusal that cannot be turned off is not a check.
func TestParse_ADesktopApplicationAndItsWorkflowAreAccepted(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withPersonas+desktopApp+aDesktopWorkflow)
	require.NotNil(t, m.Desktop)
	require.Equal(t, schema.DesktopElectron, m.Desktop.Kind)
	require.Equal(t, "./node_modules/.bin/electron", m.Desktop.Application)
	require.Equal(t, []string{"./app"}, m.Desktop.Args)
	require.Equal(t, schema.SurfaceDesktop, m.Workflows[0].Surface)
}

// `process` is how a native application is FOUND after its bundle is opened,
// and an Electron application is launched directly by its binary and never
// looked up. A field nothing reads is a setting somebody will believe they
// have made, which is the same defect as a surface nothing drives, one field
// wide.
func TestParse_AnElectronApplicationMayNotCarryAProcessName(t *testing.T) {
	t.Parallel()
	_, err := parse(t, withPersonas+`desktop:
  kind: electron
  application: ./node_modules/.bin/electron
  process: Ledger
`+aDesktopWorkflow)
	msg := messages(problems(t, err))
	require.Contains(t, msg, "An Electron application carries a process name.")
	require.Contains(t, msg, "launched directly")
}

// The same key on a native application is the ordinary case, so the rule above
// is refusing a pairing and not a field.
func TestParse_ANativeApplicationMayCarryAProcessName(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withPersonas+`desktop:
  kind: macos
  application: /Applications/Ledger.app
  process: Ledger Pro
`+aDesktopWorkflow)
	require.Equal(t, "Ledger Pro", m.Desktop.Process)
}

// The process name is derived from the bundle when the manifest leaves it out,
// here rather than in the runner, so `af explain` prints the name that will
// actually be looked for instead of a blank the runner fills in privately.
func TestParse_ANativeApplicationsProcessNameIsDerivedFromItsBundle(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withPersonas+`desktop:
  kind: macos
  application: /Applications/Ledger.app
`+aDesktopWorkflow)
	require.Equal(t, "Ledger", m.Desktop.Process,
		"the runner would have been asked to find an application with no name")

	// And an Electron application gets none, because nothing looks one up for
	// it. A derived value here would be the dead field the rule above refuses
	// when a person writes it by hand.
	electron := mustParse(t, withPersonas+desktopApp+aDesktopWorkflow)
	require.Empty(t, electron.Desktop.Process)
}

// What `af explain` says about it. A reader deciding whether these workflows
// prove what their names say has to know what they were driven against, and
// until this printed it the answer was in the manifest and in no output.
func TestExplain_NamesTheDesktopApplicationAndTheSurfaceItsWorkflowsDrive(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withPersonas+desktopApp+aDesktopWorkflow)
	out := explainOf(m)

	require.Contains(t, out, "Desktop application")
	require.Contains(t, out, "electron")
	require.Contains(t, out, "./node_modules/.bin/electron ./app",
		"the arguments decide which application is opened and the output did not say them")
	// Said once in the heading, because one run drives one surface: a manifest
	// whose workflows disagree is refused at validation.
	require.Contains(t, out, "Workflows, on the desktop")

	// A web manifest's heading is unchanged, so the line above is a statement
	// about this run rather than noise added to every manifest ever written.
	web := mustParse(t, withPersonas+`workflows:
  - name: checkout
    description: Buy one item and see the order confirmed on the screen.
    persona: alice
    expect: ["The order is confirmed."]
`)
	require.Contains(t, explainOf(web), "\nWorkflows\n")
	require.NotContains(t, explainOf(web), "Desktop application")

	// The name the runner will look a native application up by, which is what
	// somebody reads first when a launch reports that no window was ever
	// drawn.
	native := mustParse(t, withPersonas+`desktop:
  kind: macos
  application: /Applications/Ledger.app
`+aDesktopWorkflow)
	require.Contains(t, explainOf(native), "found as")
	require.Contains(t, explainOf(native), "Ledger")

	// The em dash ban, on the composed string rather than on the source, which
	// is the only reader that can see prose a file scanner cannot.
	require.NotContains(t, out, "—")
	require.NotContains(t, explainOf(native), "—")
}

// explainOf renders a manifest with no width limit, so nothing this file
// asserts on is truncated by the wrapping rather than missing.
func explainOf(m *schema.Manifest) string {
	return manifest.Explain(m, 0)
}

// THE THREE READINGS OF "WHICH KINDS OF APPLICATION EXIST", compared rather
// than trusted.
//
// The schema's enum decides what a person may write. The Go constants decide
// what the engine branches on. The runner's DesktopApp union decides what can
// actually be opened. They are three files in two languages and nothing
// connected them, which is the same gap surface_drift_test.go exists to close
// one level up: a kind in the schema and not the runner is one a manifest can
// ask for and nothing can drive, and a kind in the runner and not the schema
// is a reader nobody can reach.
//
// Read as text, from a shape that is small and fixed, and a parse that finds
// nothing FAILS rather than reporting agreement about an empty set. That is
// the failure mode a gate like this has: it goes quiet and the silence reads
// as a pass.
func TestTheApplicationKindsAgreeAcrossTheSchemaTheEngineAndTheRunner(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "runner", "src", "drivers", "desktop.ts"))
	require.NoError(t, err, "the runner's desktop driver is missing, so this checked nothing")
	matches := regexp.MustCompile(`readonly kind: '(\w+)'`).FindAllStringSubmatch(string(raw), -1)
	require.NotEmpty(t, matches,
		"no kind parsed out of the runner's DesktopApp union. The shape this reads has "+
			"changed, and a gate that parses nothing reports agreement about an empty set, "+
			"so fix the pattern rather than deleting this test.")
	inRunner := []string{}
	for _, m := range matches {
		inRunner = append(inRunner, m[1])
	}
	sort.Strings(inRunner)

	schemaRaw, err := os.ReadFile(filepath.Join("..", "..", "..", "schemas", "manifest.v1.json"))
	require.NoError(t, err)
	// Only this definition is decoded, as raw JSON first, because elsewhere in
	// the schema an enum holds numbers and a []string would fail on the
	// Postgres major version rather than on anything this test is about.
	var doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal(schemaRaw, &doc))
	var app struct {
		Properties struct {
			Kind struct {
				Enum []string `json:"enum"`
			} `json:"kind"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(doc.Defs["desktop_application"], &app))
	inSchema := append([]string(nil), app.Properties.Kind.Enum...)
	require.NotEmpty(t, inSchema, "the schema's desktop kinds were not found, so this checked nothing")
	sort.Strings(inSchema)

	inEngine := []string{schema.DesktopElectron, schema.DesktopMacOS}
	sort.Strings(inEngine)

	require.Equal(t, inRunner, inSchema,
		"the schema's kinds and the runner's DesktopApp union name different sets. A kind a "+
			"manifest may write and the runner cannot open is refused after an environment "+
			"has been built, and one the runner can open and the schema refuses is a reader "+
			"nobody can reach.")
	require.Equal(t, inRunner, inEngine,
		"the engine's constants and the runner's union name different sets, so a branch in "+
			"the engine decides something the runner never reads.")
}
