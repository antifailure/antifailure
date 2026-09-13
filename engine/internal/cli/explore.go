package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// ExploreJSON is the machine readable result of an exploratory run.
type ExploreJSON struct {
	Headline     string                `json:"headline"`
	Explorations []explore.Exploration `json:"explorations"`
	Findings     []explore.Finding     `json:"findings"`
	Blocked      int                   `json:"blocked"`
}

// exploreFlags is what the command line adds to the manifest's goal.
//
// Kept as a struct rather than five locals so that a test can parse a flag
// set into it and read the options it produces without building an
// orchestrator, which needs a manifest, a state directory and a branch.
type exploreFlags struct {
	persona, start, viewport, budget, focus string
}

// options turns the flags into the orchestrator's options.
//
// The viewport and the budget are parsed here, before the orchestrator exists,
// so that a typo in either is answered before the manifest is read. The
// persona is checked later, by the orchestrator, because only it knows which
// personas the manifest declares. Both refusals are the same coded errors the
// orchestrator would return, so a caller sees one vocabulary wherever the
// mistake was caught.
func (f exploreFlags) options() (explore.Steering, error) {
	steer := explore.Steering{
		Persona: f.persona, StartPath: f.start, Viewport: f.viewport,
		Budget: f.budget, Focus: f.focus,
	}
	if _, err := explore.ParseStartPath(steer.StartPath); err != nil {
		return steer, err
	}
	if _, err := explore.ParseViewport(steer.Viewport); err != nil {
		return steer, err
	}
	if _, err := explore.ParseBudget(steer.Budget); err != nil {
		return steer, err
	}
	return steer, nil
}

func newExploreCommand(e *Env) *cobra.Command {
	var branch, runner, seed string
	var only []string
	var headed, emit bool
	var steer exploreFlags
	cmd := &cobra.Command{
		Use:   "explore",
		Short: "Send agents at a goal with no declared workflow",
		Long: strings.TrimSpace(`
An exploration is a goal without a script. The agent reads each page through
the accessibility tree, chooses somewhere to go, and writes down every place
the application cost it effort. It answers the question a workflow cannot ask:
nothing broke, so why would somebody give up here.

It cannot fail your build. Nobody declared what should happen on the pages it
wanders onto, so a finding is an observation and never a red mark. Only a run
that could not start is reported as blocked.

Every choice comes from the goal's seed, so the same seed takes the same path
and every finding arrives with the command that replays it.

The manifest's goal is the default and the flags below override it for one run,
without writing anything: explore as a different persona, from a different
page, in a different window, for a different budget. A viewport of phone is
390x844 with a mobile user agent and a touch screen, tablet is 768x1024,
desktop is 1440x900, and WIDTHxHEIGHT is any size between 320 and 3840 a side.
A budget is a step count such as 8 or a duration such as 5m. A persona the
manifest does not declare is refused, and the refusal names the ones it does.
The report and the artifacts record the persona, the start path and the
viewport that were actually used.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			steering, err := steer.options()
			if err != nil {
				return err
			}
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}
			e.Out.Section("Exploring")
			report, err := o.Explore(cmd.Context(), env.ExploreOptions{
				Only: only, Seed: seed, Headed: headed, RunnerPath: runner, Steer: steering,
			})
			if err != nil {
				return err
			}

			if emit {
				return emitWorkflows(e, o, report)
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(ExploreJSON{
					Headline:     report.Headline(),
					Explorations: report.Explorations,
					Findings:     report.Findings(),
					Blocked:      report.Blocked(),
				})
			}

			printExplorations(e, report)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&only, "only", nil, "Explore just these goals, by name")
	cmd.Flags().StringVar(&seed, "seed", "",
		"Replay with this seed rather than the one the manifest declares")
	cmd.Flags().BoolVar(&headed, "headed", false, "Show the browser rather than running it hidden")
	cmd.Flags().BoolVar(&emit, "emit-workflow", false,
		"Print the workflow block that replays what was explored, instead of the report")
	cmd.Flags().StringVar(&runner, "runner", "", "Path to the runner's entry point")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to run against, defaulting to the checked out one")
	cmd.Flags().StringVar(&steer.persona, "persona", "",
		"Explore as this declared persona rather than the goal's")
	cmd.Flags().StringVar(&steer.start, "start", "",
		"Begin at this path rather than the goal's start_path, such as /settings/billing")
	cmd.Flags().StringVar(&steer.viewport, "viewport", "",
		"Window to explore in: phone (390x844, mobile), tablet (768x1024), desktop (1440x900), or WIDTHxHEIGHT")
	cmd.Flags().StringVar(&steer.budget, "budget", "",
		"Most this run may spend: a step count such as 8, or a duration such as 5m")
	cmd.Flags().StringVar(&steer.focus, "focus", "",
		"A sentence about what to attend to; its words decide which controls are pressed first")
	return cmd
}

// printExplorations writes what each goal found.
//
// The findings are printed under the exploration that produced them rather
// than collected into one list, because the first thing somebody asks about a
// finding is which run saw it and what it was trying to do at the time.
func printExplorations(e *Env, report *explore.Report) {
	e.Out.Println("")
	for _, x := range report.Explorations {
		symbol, style := verdictStyle(e, x.Outcome.Verdict)
		e.Out.Status(symbol, x.Name, style)
		// Who, where from, and in what window, before what happened. A finding
		// on a phone is a different finding from the same one on a desktop,
		// and the reader has to know which this was before reading it. A
		// runner that predates the fields reports neither, and a line saying
		// "signed out" about a run that signed in would be false.
		if x.StartPath != "" || x.Viewport.Width > 0 {
			e.Out.Printf("      %s\n", e.Out.S(StyleDim, x.Setting()))
		}
		e.Out.Printf("      %s\n", e.Out.Wrap(x.Outcome.Detail, 6))

		for _, f := range (explore.Report{Explorations: []explore.Exploration{x}}).Findings() {
			where := f.URL
			if f.Control != "" {
				where = fmt.Sprintf("%q on %s", f.Control, f.URL)
			}
			e.Out.Printf("      %s %s at step %d, %s\n",
				e.Out.S(StyleWarn, SymbolWarn),
				e.Out.S(StyleAccent, f.Kind.Title()), f.Step,
				e.Out.S(StyleDim, where))
			e.Out.Printf("        %s\n", e.Out.Wrap(f.Detail, 8))
			e.Out.Printf("        %s\n", e.Out.S(StyleDim, e.Out.Wrap(f.Fix, 8)))
		}

		// What was not explored, before the evidence. A corner nobody looked
		// at has to read as unexplored rather than as clean, and putting it
		// after the trace would leave it below where people stop reading.
		for _, m := range x.Missing {
			e.Out.Printf("      %s %s\n", e.Out.S(StyleDim, "not explored"), e.Out.Wrap(m, 6))
		}
		if x.Evidence.Trace != "" {
			e.Out.Printf("      %s %s\n",
				e.Out.S(StyleDim, "trace"), e.Out.S(StyleDim, x.Evidence.Trace))
		}
		for _, line := range x.Outcome.Reproduction {
			e.Out.Printf("      %s\n", e.Out.S(StyleDim, line))
		}
	}

	e.Out.Println("")
	e.Out.Println("  " + e.Out.Wrap(report.Headline(), 2))
	if report.Blocked() > 0 {
		e.Out.Println(e.Out.Wrap(
			"  Blocked means the runner or the environment could not carry the exploration "+
				"through, so nothing was explored rather than nothing being found.", 2))
	}
}

// emitWorkflows prints the manifest block that replays what was explored.
//
// This is the point of the whole feature. A report is read once; a workflow
// runs on every pull request, so a discovery that stays a report is a
// discovery that stops mattering the week after somebody read it.
func emitWorkflows(e *Env, o *env.Orchestrator, report *explore.Report) error {
	var workflows []schema.Workflow
	var notes []string
	for _, x := range report.Explorations {
		if x.Outcome.Verdict == "blocked" {
			// Nothing was explored, so there is no path to compile. Emitting
			// an empty workflow would look like a result.
			continue
		}
		// The persona the runner signed in as, when it said. A steered run
		// explored as somebody the manifest's goal did not name, and a
		// compiled workflow naming the goal's persona would run as the wrong
		// person. The manifest is the fallback for a runner that predates the
		// field.
		persona := x.Persona
		if persona == "" {
			persona = personaFor(o, x)
		}
		w, n := explore.Compile(x, persona)
		workflows = append(workflows, w)
		notes = append(notes, n...)
	}
	if len(workflows) == 0 {
		return aferrors.Coded(aferrors.AFAGT020,
			"detail", "no exploration got far enough to compile into a workflow")
	}

	body, err := yaml.Marshal(struct {
		Workflows []schema.Workflow `yaml:"workflows"`
	}{Workflows: workflows})
	if err != nil {
		return err
	}
	e.Out.Println("")
	e.Out.Raw(string(body))
	e.Out.Println("")
	// On the terminal rather than as comments inside the block, because a
	// comment pasted into somebody's manifest stays there forever.
	for _, n := range notes {
		e.Out.Printf("  %s %s\n", e.Out.S(StyleDim, "note"), e.Out.Wrap(n, 7))
	}
	return nil
}

// personaFor finds which persona a goal declares, for the compiled block when
// the runner did not say which persona it signed in as.
func personaFor(o *env.Orchestrator, x explore.Exploration) string {
	for _, g := range o.Goals() {
		if g.Name == x.Name {
			return g.Persona
		}
	}
	return ""
}
