package manifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The desktop surface's manifest half.
//
// THE DEFECT THESE EXIST FOR, and it is the same one the terminal list was
// built to close, one surface over. The desktop driver was written, tested
// against a real Electron application and a real native one, and unreachable:
// nothing in the manifest could ask for it, so the engine sent the runner a
// document with no desktop workflows in it on every run that has ever
// happened. A driver nobody can reach is not a feature.
//
// The bounds pass keeps the schema's own constraints and
// TestEverySchemaConstraintIsEnforced proves every one of them is live. What is
// here is the cross field part the schema cannot say, and the normalisation
// that makes `af explain` print the numbers the run will really use.

const withADesktopWorkflow = `
version: 1
name: shop
services:
  - name: web
    port: 3000
desktop:
  kind: electron
  application: ./node_modules/electron/dist/Electron.app/Contents/MacOS/Electron
  args: ["."]
desktop_workflows:
  - name: sign-in-desktop
    description: Sign in to the desktop application and land on a signed in screen.
    expect: ['"Welcome back"']
    answers:
      Email address: person@example.com
    budget:
      duration: 90s
      steps: 25
`

func TestParse_AcceptsADesktopWorkflow(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withADesktopWorkflow)
	require.NotNil(t, m.Desktop)
	require.Equal(t, "electron", m.Desktop.Kind)
	require.Equal(t, []string{"."}, m.Desktop.Args)
	require.Len(t, m.DesktopWorkflows, 1)
	w := m.DesktopWorkflows[0]
	require.Equal(t, "sign-in-desktop", w.Name)
	require.Equal(t, []string{`"Welcome back"`}, w.Expect)
	require.Equal(t, "person@example.com", w.Answers["Email address"])
	require.NotNil(t, w.Budget)
	require.Equal(t, "90s", w.Budget.Duration)
	require.Equal(t, 25, w.Budget.Steps)
}

// A workflow with no budget gets the runner's own, so the number `af explain`
// prints is the number the run will use rather than a blank the runner fills
// in privately.
func TestParse_NormalisesTheDesktopBudget(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`desktop:
  kind: electron
  application: ./bin/app
desktop_workflows:
  - name: open-it
    description: Open the application and confirm the inbox is listed.
    expect: ['"Inbox"']
`)
	w := m.DesktopWorkflows[0]
	require.NotNil(t, w.Budget)
	require.Equal(t, schema.DefaultDesktopDuration, w.Budget.Duration)
	require.Equal(t, schema.DefaultDesktopSteps, w.Budget.Steps)
}

// The process name is DERIVED rather than left blank, because the runner has
// to find the application after opening its bundle: opening returns before the
// application is ready. Deriving it here means `af explain` shows the name that
// will actually be looked for.
func TestParse_DesktopDerivesTheMacProcessNameFromTheBundle(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`desktop:
  kind: macos
  application: /System/Applications/TextEdit.app
desktop_workflows:
  - name: write-a-note
    description: Start a new document and confirm a blank window opens.
    expect: ['"Untitled"']
`)
	require.Equal(t, "TextEdit", m.Desktop.Process)

	// And a stated one is kept, which is the case the derivation cannot serve:
	// Visual Studio Code.app runs as Code.
	stated := mustParse(t, minimal+`desktop:
  kind: macos
  application: /Applications/Visual Studio Code.app
  process: Code
desktop_workflows:
  - name: open-a-folder
    description: Open a folder and confirm the explorer lists its files.
    expect: ['"EXPLORER"']
`)
	require.Equal(t, "Code", stated.Desktop.Process)
}

// Workflows with nothing to drive, and an application nothing drives. Two
// different mistakes, and each is refused by name: the first is a list that
// cannot run at all, the second a block that will never be read.
func TestValidate_RefusesDesktopWorkflowsWithNoApplicationAndTheReverse(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`desktop_workflows:
  - name: orphan
    description: Do something in an application nobody named.
    expect: ['"Anything"']
`)
	require.Contains(t, messages(problems(t, err)), "no desktop application to drive")

	_, err = parse(t, minimal+`desktop:
  kind: electron
  application: ./bin/app
`)
	require.Contains(t, messages(problems(t, err)), "no desktop workflow drives it")
}

// A field nothing reads is a setting a person will believe they have made.
// `process` is how a NATIVE application is found after its bundle is opened,
// and an Electron application is launched directly by its binary.
func TestValidate_RefusesADesktopProcessNameOnAnElectronApplication(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`desktop:
  kind: electron
  application: ./bin/app
  process: SomethingElse
desktop_workflows:
  - name: open-it
    description: Open the application and confirm the inbox is listed.
    expect: ['"Inbox"']
`)
	require.Contains(t, messages(problems(t, err)), "An Electron application carries a process name")

	// And it is allowed on the surface that reads it, or the rule would be
	// refusing the case it exists to serve.
	require.NotNil(t, mustParse(t, minimal+`desktop:
  kind: macos
  application: /Applications/Ledger.app
  process: Ledger Desktop
desktop_workflows:
  - name: open-it
    description: Open the application and confirm the inbox is listed.
    expect: ['"Inbox"']
`).Desktop)
}

// One namespace across all THREE lists now, because a name is what --only
// selects and what the report prints against a verdict.
func TestValidate_RefusesADesktopNameAlreadyUsedByAnotherList(t *testing.T) {
	t.Parallel()
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
personas:
  - name: owner
    email: owner@example.com
workflows:
  - name: checkout
    description: Buy something and confirm the receipt shows the order number.
terminal_workflows:
  - name: deploy-plan
    description: The deploy command shows the plan and asks before applying it.
    command: ./bin/deploy
    expect: ['"Applied"']
desktop:
  kind: electron
  application: ./bin/app
desktop_workflows:
  - name: %s
    description: Open the application and confirm the inbox is listed.
    expect: ['"Inbox"']
`
	for _, taken := range []string{"checkout", "deploy-plan"} {
		_, err := parse(t, strings.Replace(body, "%s", taken, 1))
		require.Containsf(t, messages(problems(t, err)),
			`Two workflows are both named "`+taken+`"`,
			"a desktop workflow reused the name %q from another list and was allowed", taken)
	}

	// A name of its own is fine, which is the arm that stops the rule being a
	// refusal of every desktop workflow.
	require.Len(t, mustParse(t, strings.Replace(body, "%s", "sign-in-desktop", 1)).DesktopWorkflows, 1)
}

// The same floor the other two lists have. The description is what a reader of
// the report is told this workflow was for, and it is also read by the planner,
// which presses a control whose whole visible label appears in it.
func TestValidate_RefusesADesktopDescriptionTooShortToRead(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`desktop:
  kind: electron
  application: ./bin/app
desktop_workflows:
  - name: open-it
    description: Open it now.
    expect: ['"Inbox"']
`)
	require.Contains(t, messages(problems(t, err)), "too short to read in a report")
}

// AND THE RULE THAT IS DELIBERATELY ABSENT, asserted so that adding it later
// is a decision rather than an accident.
//
// The terminal list refuses an expectation the workflow also types, because a
// pseudo terminal echoes its input and such an expectation is satisfied by the
// workflow rather than by the program. The same hole existed on this surface
// and was closed one layer down instead: runner/src/drivers/ax.ts leaves a
// field's own value out of the text expectations are judged against. With the
// value excluded, an expectation naming an answer can only be met when the
// application RENDERED those words, and a confirmation screen reading back the
// address somebody typed is exactly the evidence such a workflow is written to
// find. Refusing it here would refuse a correct workflow.
func TestValidate_AllowsADesktopExpectationThatNamesAnAnswer(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`desktop:
  kind: electron
  application: ./bin/app
desktop_workflows:
  - name: confirm-the-address
    description: Sign up and confirm the page reads the address back to you.
    expect: ['"person@example.com"']
    answers:
      Email address: person@example.com
`)
	require.Len(t, m.DesktopWorkflows, 1)
}
