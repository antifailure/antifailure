package manifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The terminal surface's manifest half.
//
// THE DEFECT THESE EXIST FOR. The terminal driver was written, unit tested and
// unreachable: nothing in the manifest could ask for it, so the engine sent
// the runner a document with no terminal workflows in it on every run that has
// ever happened. A driver nobody can reach is not a feature, and the first
// thing a person needs in order to reach it is a key in this file's schema
// that the engine both accepts and enforces.
//
// The bounds pass keeps the schema's own constraints, and
// TestEverySchemaConstraintIsEnforced proves all forty four of them are live.
// What is here is the cross field part the schema cannot say.

const withATerminalWorkflow = `
version: 1
name: shop
services:
  - name: web
    port: 3000
terminal_workflows:
  - name: deploy-plan
    description: The deploy command shows the plan and asks before applying it.
    command: ./bin/deploy
    args: ["--plan"]
    input: ["y", "<enter>"]
    expect: ['"Applied 3 changes"']
    screen:
      rows: 30
      cols: 100
    cwd: tools
    budget:
      duration: 45s
`

func TestParse_AcceptsATerminalWorkflow(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withATerminalWorkflow)
	require.Len(t, m.TerminalWorkflows, 1)
	w := m.TerminalWorkflows[0]
	require.Equal(t, "deploy-plan", w.Name)
	require.Equal(t, "./bin/deploy", w.Command)
	require.Equal(t, []string{"--plan"}, w.Args)
	require.Equal(t, []string{"y", "<enter>"}, w.Input)
	require.Equal(t, []string{`"Applied 3 changes"`}, w.Expect)
	require.Equal(t, "tools", w.Cwd)
	require.NotNil(t, w.Screen)
	require.Equal(t, 30, w.Screen.Rows)
	require.Equal(t, 100, w.Screen.Cols)
	require.NotNil(t, w.Budget)
	require.Equal(t, "45s", w.Budget.Duration)
}

// A screen declared without a size gets the terminal every program has been
// written against, and a workflow with no budget gets the runner's own, so the
// number `af explain` prints is the number the run will use.
func TestParse_NormalisesTheScreenAndTheBudget(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`terminal_workflows:
  - name: menu
    description: The inbox lists the drafts and opens the one selected.
    command: ./bin/inbox
    expect: ['"Drafts"']
    screen: {}
`)
	w := m.TerminalWorkflows[0]
	require.NotNil(t, w.Screen)
	require.Equal(t, schema.DefaultTerminalRows, w.Screen.Rows)
	require.Equal(t, schema.DefaultTerminalCols, w.Screen.Cols)
	require.NotNil(t, w.Budget)
	require.Equal(t, schema.DefaultTerminalDuration, w.Budget.Duration)
}

// The absence of a screen is a DECISION and never a gap to fill in. It says
// the program prints rather than draws, and it is what puts the workflow on a
// pipe rather than on a pseudo terminal that would echo the driver's own
// keystrokes back at it. Defaulting it would turn every line oriented workflow
// into one whose expectations its own input could satisfy.
func TestParse_AWorkflowWithNoScreenIsNotGivenOne(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`terminal_workflows:
  - name: migrate
    description: The migrate command applies the pending migration and says so.
    command: ./bin/migrate
    expect: ['"3 migrations applied"']
`)
	require.Nil(t, m.TerminalWorkflows[0].Screen,
		"a workflow that declared no screen was given one, which puts it on a terminal that echoes what the driver types")
}

// A terminal workflow needs a program. The refusal comes from the schema's own
// required list through the bounds pass, and this is the behavioural half of
// that claim: a manifest is refused rather than a run reaching the runner with
// nothing to start.
func TestParse_RefusesATerminalWorkflowWithNoCommand(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`terminal_workflows:
  - name: nothing
    description: This workflow names no program at all to run.
    expect: ['"anything"']
`)
	require.Contains(t, messages(problems(t, err)), "command")
}

// An expectation is required here and is not on a browser workflow, because a
// terminal workflow with nothing to expect can only ever report that nothing
// confirmed or contradicted it, which is blocked. A workflow that cannot pass
// is not a workflow.
func TestParse_RefusesATerminalWorkflowThatExpectsNothing(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`terminal_workflows:
  - name: nothing
    description: This workflow runs a program and expects nothing at all.
    command: ./bin/run
    expect: []
`)
	require.Contains(t, messages(problems(t, err)), "expect")
}

// Names are one namespace across both lists, because a name is what --only
// selects and what the report prints beside a verdict.
func TestParse_RefusesANameSharedWithABrowserWorkflow(t *testing.T) {
	t.Parallel()
	_, err := parse(t, `
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
    description: Buy one item and see the order confirmed on the screen.
    expect: ["The order is confirmed."]
terminal_workflows:
  - name: checkout
    description: The checkout command prints the order it created.
    command: ./bin/checkout
    expect: ['"Order confirmed"']
`)
	msg := messages(problems(t, err))
	require.Contains(t, msg, `Two workflows are both named "checkout"`)
	require.Contains(t, msg, "terminal_workflows[0].name")
}

func TestParse_RefusesTwoTerminalWorkflowsWithOneName(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`terminal_workflows:
  - name: deploy
    description: The deploy command applies the plan and reports what it did.
    command: ./bin/deploy
    expect: ['"Applied"']
  - name: deploy
    description: The deploy command refuses a plan it cannot apply cleanly.
    command: ./bin/deploy
    expect: ['"Refused"']
`)
	require.Contains(t, messages(problems(t, err)), `Two workflows are both named "deploy"`)
}

// The same floor the browser workflows have. The description is what a reader
// of the report is told the workflow was for.
func TestParse_RefusesATerminalDescriptionTooShortToRead(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`terminal_workflows:
  - name: deploy
    description: runs deploy
    command: ./bin/deploy
    expect: ['"Applied"']
`)
	require.Contains(t, messages(problems(t, err)), "too short to read in a report")
}

// Refused by the schema's own pattern through the bounds pass, which is the
// only place that rule lives. A hand written second opinion reading
// ParseDuration was written here first and deleted: the pattern is strictly
// narrower than the parser, so the second check could never fire, and a
// mutation of it left this test green. See the note in validate.go.
func TestParse_RefusesATerminalBudgetThatIsNotADuration(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`terminal_workflows:
  - name: deploy
    description: The deploy command applies the plan and reports what it did.
    command: ./bin/deploy
    expect: ['"Applied"']
    budget:
      duration: 45q
`)
	require.Contains(t, messages(problems(t, err)), "budget.duration")
}

// THE CHECK THAT COULD NOT SAY NO, refused before anybody can write it.
//
// A pseudo terminal echoes what is typed into it, so on a screen the driver's
// own keystrokes are drawn before the program has done anything. An
// expectation naming exactly what the workflow types is therefore satisfied by
// the workflow itself: it passes against a program that printed nothing, and
// against one that is completely broken. That is worse than having no
// expectation, because it looks like one.
func TestParse_RefusesAnExpectationTheWorkflowTypesItself(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`terminal_workflows:
  - name: deploy
    description: The deploy command applies the plan and reports what it did.
    command: ./bin/deploy
    input: ["Applied 3 changes"]
    expect: ['"Applied 3 changes"']
    screen:
      rows: 24
      cols: 80
`)
	msg := messages(problems(t, err))
	require.Contains(t, msg, "and also types it")
	require.Contains(t, msg, "Expect something the program draws")
}

// And only on a screen. Without one the program is driven through a pipe,
// nothing echoes, and a program that prints back what it was given is an
// ordinary thing to expect.
func TestParse_AllowsAnExpectationTheWorkflowTypesWhenThereIsNoScreen(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`terminal_workflows:
  - name: echo
    description: The command repeats the name it was given back to the operator.
    command: ./bin/echo
    input: ["orders-api"]
    expect: ['"orders-api"']
`)
	require.Len(t, m.TerminalWorkflows, 1)
}

// af explain is where a person checks that the manifest says what they meant,
// and a block it does not mention is a block nobody proofreads. The screen is
// named because it is the fact that decides how the program is driven.
func TestExplain_NamesTheTerminalWorkflowsAndHowTheyAreDriven(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withATerminalWorkflow+`  - name: migrate
    description: The migrate command applies the pending migration and says so.
    command: ./bin/migrate
    expect: ['"3 migrations applied"']
`)
	out := strings.Join(strings.Fields(manifest.Explain(m, 0)), " ")
	require.Contains(t, out, "Terminal workflows")
	require.Contains(t, out, "deploy-plan")
	require.Contains(t, out, "./bin/deploy")
	require.Contains(t, out, "on a 30 by 100 screen")
	require.Contains(t, out, "up to 45s")
	require.Contains(t, out, "through a pipe")
	// And the one line summary, which is what a reader sees before they read
	// anything else.
	require.Contains(t, manifest.Summary(m), "2 terminal workflows")
	// The em dash ban, on the composed string rather than on the source,
	// because this prose is assembled at run time and no file scanner can see
	// it. The double hyphen half is not asserted here: a program's own flags
	// are in this output, and the manifest above wrote one down.
	require.NotContains(t, out, "—")
}

// The surface a workflow drives, and the two refusals that keep the set
// honest.
//
// THE DEFECT THESE EXIST FOR is the one this lane was created to fix,
// recreated one surface over. A driver can be finished, registered and
// correct, and still be unreachable because no manifest can name the surface
// it drives. So the manifest names every surface the product knows, including
// the ones this build cannot drive, and the engine refuses those BY NAME
// against what the build actually carries.

func TestParse_DefaultsAWorkflowToTheBrowser(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withPersonas+`workflows:
  - name: checkout
    description: Buy one item and see the order confirmed on the screen.
    persona: alice
    expect: ["The order is confirmed."]
`)
	require.Equal(t, schema.SurfaceWeb, m.Workflows[0].Surface,
		"a workflow that named no surface was left empty, so every reader downstream has to decide what an absence means")
}

func TestParse_AcceptsTheSurfaceThisBuildDrives(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withPersonas+`workflows:
  - name: checkout
    surface: web
    description: Buy one item and see the order confirmed on the screen.
    persona: alice
    expect: ["The order is confirmed."]
`)
	require.Equal(t, schema.SurfaceWeb, m.Workflows[0].Surface)
}

// A surface the product knows and this build cannot drive. It is refused, and
// the refusal names the surfaces the build has rather than the ones the schema
// allows, because those are different lists and only one of them can help.
func TestParse_RefusesASurfaceThisBuildHasNoDriverFor(t *testing.T) {
	t.Parallel()
	// android only, since #508 built the iOS driver and DriveableSurfaces now
	// carries SurfaceIOS. Kept as a loop rather than collapsed to one case: the
	// next surface to be finished is one entry to move, and the entry that
	// moves is the evidence the sentence below has to change with it.
	for _, surface := range []string{"android"} {
		_, err := parse(t, withPersonas+`workflows:
  - name: checkout
    surface: `+surface+`
    description: Buy one item and see the order confirmed on the screen.
    persona: alice
    expect: ["The order is confirmed."]
`)
		msg := messages(problems(t, err))
		require.Containsf(t, msg, "this build has no driver for it",
			"surface %q was not refused as undriveable", surface)
		require.Containsf(t, msg, surface, "the refusal for %q does not name it", surface)
		require.Containsf(t, msg, "This build drives: web, terminal, desktop, ios.",
			"the refusal for %q does not say what this build can drive", surface)
	}
}

// And a value that is not a surface at all gets a different sentence, because
// a typo and an unbuilt driver are different facts and the remedy differs.
func TestParse_RefusesAValueThatIsNotASurfaceAtAll(t *testing.T) {
	t.Parallel()
	_, err := parse(t, withPersonas+`workflows:
  - name: checkout
    surface: telepathy
    description: Buy one item and see the order confirmed on the screen.
    persona: alice
    expect: ["The order is confirmed."]
`)
	msg := messages(problems(t, err))
	require.Contains(t, msg, "surface")
	require.NotContains(t, msg, "this build has no driver for it",
		"a value that is not a surface was reported as an unbuilt driver, which tells somebody to wait for a release that is never coming")
}

// terminal is in the enum so that writing it here is answered with where it
// belongs rather than with a list it is missing from, which reads like a typo.
func TestParse_RefusesTerminalOnABrowserWorkflowAndSaysWhereItGoes(t *testing.T) {
	t.Parallel()
	_, err := parse(t, withPersonas+`workflows:
  - name: deploy
    surface: terminal
    description: Run the deploy command and confirm it reports what it applied.
    persona: alice
    expect: ["Applied"]
`)
	msg := messages(problems(t, err))
	require.Contains(t, msg, "sets surface to terminal")
	require.Contains(t, msg, "terminal_workflows")
	require.NotContains(t, msg, "this build has no driver for it",
		"terminal was reported as undriveable, and this build drives it")
}

// The runner dispatches ONE driver per run and hands it the whole workflow
// list, so a manifest whose workflows disagree has no single answer to give
// it. Refused here rather than left to the runner, which would drive them all
// as whichever surface won and fail for a reason nothing could name.
func TestParse_RefusesWorkflowsThatDriveDifferentSurfaces(t *testing.T) {
	t.Parallel()
	_, err := parse(t, withPersonas+`workflows:
  - name: checkout
    surface: web
    description: Buy one item and see the order confirmed on the screen.
    persona: alice
    expect: ["The order is confirmed."]
  - name: preferences
    surface: desktop
    description: Open the preferences window and change the default currency.
    persona: alice
    expect: ["The default currency is euros."]
`)
	msg := messages(problems(t, err))
	require.Contains(t, msg, "One run drives one surface")
	// Both sides named, so a reader does not have to find the other one.
	require.Contains(t, msg, `"preferences"`)
	require.Contains(t, msg, `"checkout"`)
}

// Two workflows on the SAME surface are the ordinary case and must not be
// caught by the rule above.
func TestParse_AcceptsSeveralWorkflowsOnOneSurface(t *testing.T) {
	t.Parallel()
	m := mustParse(t, withPersonas+`desktop:
  kind: electron
  application: ./node_modules/.bin/electron
workflows:
  - name: checkout
    surface: desktop
    description: Buy one item and see the order confirmed on the screen.
    persona: alice
    expect: ["The order is confirmed."]
  - name: preferences
    surface: desktop
    description: Open the preferences window and change the default currency.
    persona: alice
    expect: ["The default currency is euros."]
`)
	require.Len(t, m.Workflows, 2)
}
