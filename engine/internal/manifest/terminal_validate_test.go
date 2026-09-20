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
