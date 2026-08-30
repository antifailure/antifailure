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

func newExploreCommand(e *Env) *cobra.Command {
	var branch, runner, seed string
	var only []string
	var headed, emit bool
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
and every finding arrives with the command that replays it.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}
			e.Out.Section("Exploring")
			report, err := o.Explore(cmd.Context(), env.ExploreOptions{
				Only: only, Seed: seed, Headed: headed, RunnerPath: runner,
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
		w, n := explore.Compile(x, personaFor(o, x))
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

// personaFor finds which persona a goal explored as, for the compiled block.
//
// Read back from the manifest rather than echoed by the runner, because the
// runner is given a persona and may fall back to the first one, and a compiled
// workflow naming a persona the exploration did not actually use would be a
// workflow that runs as somebody else.
func personaFor(o *env.Orchestrator, x explore.Exploration) string {
	for _, g := range o.Goals() {
		if g.Name == x.Name {
			return g.Persona
		}
	}
	return ""
}
