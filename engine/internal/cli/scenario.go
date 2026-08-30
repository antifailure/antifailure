package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/load"
)

// ScenarioJSON is the machine readable result of a scenario run.
type ScenarioJSON struct {
	Verdict   string                `json:"verdict"`
	Seed      int64                 `json:"seed"`
	Scenarios []load.ScenarioResult `json:"scenarios"`
}

func newLoadScenarioCommand(e *Env) *cobra.Command {
	var branch string
	var seed int64
	var concurrency int
	var only []string

	cmd := &cobra.Command{
		Use:   "scenario",
		Short: "Run the declared journeys against the environment",
		Long: strings.TrimSpace(`
A scenario is an ordered journey rather than a mix: open the billing page, ask
for the subscription, submit, and submit again three hundred milliseconds later
because the first one felt slow. Sessions walk it at once, and one scenario can
start after another so a burst arrives while something else is already running.

The requests are HTTP. Clicking a button is 'af test' and the browser agents;
this is what the load generator can send, at the concurrency load runs at.

Every step is checked against load.safe_routes before anything is sent, so a
scenario that names an undeclared route is blocked rather than run.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}
			e.Out.Section("Running scenarios")

			results, err := o.Scenarios(cmd.Context(), env.ScenarioOptions{
				Only: only, Seed: seed, Concurrency: concurrency,
				Progress: func(p load.Progress) {
					e.Out.Printf("  %s  %d sent, %d failed, p95 %.0fms, %d in flight\n",
						p.Elapsed, p.Sent, p.Errors, p.P95Ms, p.Inflight)
				},
			})
			if err != nil {
				return err
			}

			verdict := scenarioVerdict(results)
			if e.Out.Format == FormatJSON {
				if err := e.Out.JSON(ScenarioJSON{
					Verdict: verdict, Seed: seed, Scenarios: results,
				}); err != nil {
					return err
				}
				// Quiet in JSON mode. The document above is what a script
				// parses, and a second one describing the error would land in
				// the middle of the stream it is reading.
				if err := scenarioExit(results); err != nil {
					return silent(err)
				}
				return nil
			}

			e.Out.Println("")
			for _, r := range results {
				renderScenario(e, r)
			}
			return scenarioExit(results)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to send at, defaulting to the checked out one")
	cmd.Flags().Int64Var(&seed, "seed", 1, "Makes two runs send the same schedule")
	cmd.Flags().IntVar(&concurrency, "concurrency", 20, "Ceiling on requests in flight")
	cmd.Flags().StringSliceVar(&only, "only", nil, "Run just these scenarios, by name")
	return cmd
}

// renderScenario prints one scenario the way a person reads it: the answer
// first, then what it measured, then the assertions.
//
// The same shape 'af test' uses for a workflow, and the same verdict words,
// because a scenario result and a workflow result answer the same question
// about the same run and two layouts for that would be two things to learn.
func renderScenario(e *Env, r load.ScenarioResult) {
	symbol, word := verdictStyle(e, r.Verdict)
	e.Out.Status(symbol, r.Scenario, word)
	if r.Description != "" {
		e.Out.Printf("      %s\n", e.Out.S(StyleDim, e.Out.Wrap(r.Description, 6)))
	}
	e.Out.Printf("      %s\n", e.Out.Wrap(r.Detail, 6))

	if r.Sent > 0 {
		e.Out.Printf("      %s\n", e.Out.S(StyleDim, fmt.Sprintf(
			"%d sessions x %d iterations, %d requests, p50 %.0fms, p95 %.0fms, p99 %.0fms",
			r.Sessions, r.Iterations, r.Sent,
			r.Overall.P50Ms, r.Overall.P95Ms, r.Overall.P99Ms)))
	}

	// The schedule against the clock. A run that took much longer than the
	// plan asked for is a run the application could not keep up with, which is
	// the finding a load test exists to produce and which a latency percentile
	// on its own does not say.
	if r.ScheduledMs > 0 && r.DurationMs > r.ScheduledMs*1.1 {
		e.Out.Printf("      %s the schedule asked for %.1fs and the run took %.1fs\n",
			e.Out.S(StyleWarn, SymbolWarn), r.ScheduledMs/1000, r.DurationMs/1000)
	}
	if len(r.Refused) > 0 {
		e.Out.Printf("      %s not named in safe_routes: %s\n",
			e.Out.S(StyleWarn, SymbolWarn), strings.Join(r.Refused, ", "))
	}

	// Indented lines rather than a table. A table starts at the left margin,
	// and one per scenario would break the block a reader is inside.
	width := 0
	for _, step := range r.Steps {
		if len(step.Route) > width {
			width = len(step.Route)
		}
	}
	for _, step := range r.Steps {
		line := fmt.Sprintf("%-*s  %5d sent  p95 %5.0fms", width, step.Route, step.Sent, step.Latency.P95Ms)
		if step.Errors > 0 {
			line += fmt.Sprintf("  %d failed", step.Errors)
		}
		e.Out.Printf("      %s\n", e.Out.S(StyleDim, line))
	}

	for _, a := range r.Assertions {
		mark, _ := verdictStyle(e, a.Verdict)
		e.Out.Printf("      %s %s: %s\n", mark, a.Name, e.Out.Wrap(a.Detail, 6))
	}
	e.Out.Println("")
}

// scenarioVerdict is the one word answer across every scenario, in the same
// precedence report.Run.Verdict uses.
func scenarioVerdict(results []load.ScenarioResult) string {
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Verdict]++
	}
	switch {
	case counts[load.VerdictFail] > 0:
		return load.VerdictFail
	case counts[load.VerdictBlocked] > 0:
		return load.VerdictBlocked
	case counts[load.VerdictUnverified] > 0:
		return load.VerdictUnverified
	case len(results) == 0:
		return load.VerdictBlocked
	default:
		return load.VerdictPass
	}
}

// scenarioExit turns the run into an error, or nil.
//
// It returns the coded error rather than a silent one, so the human path
// prints the code, the sentence and the next step under the detail the
// renderer already showed. The JSON path wraps it, because a second document
// in a stream a script is parsing is worse than no message at all.
//
// A failed assertion and a blocked scenario exit differently, and the
// difference is the point. A failure is the application; a block is the
// manifest not saying a route may be called, which is a configuration problem
// and is fixed in a different file by a different person. Exiting zero on
// either would be worse than both: a check that ran nothing and reported green
// is a check everybody believes is running.
func scenarioExit(results []load.ScenarioResult) error {
	failed := 0
	for _, r := range results {
		for _, a := range r.Assertions {
			if a.Verdict == load.VerdictFail {
				failed++
			}
		}
	}
	if failed > 0 {
		return aferrors.Coded(aferrors.AFLOD014, "count", fmt.Sprint(failed))
	}
	for _, r := range results {
		if r.Verdict == load.VerdictBlocked {
			return aferrors.Coded(aferrors.AFLOD015,
				"scenario", r.Scenario, "detail", r.Detail)
		}
	}
	// An assertion that could not be measured is a typo in a step name, and a
	// typo that exits zero is a check that reports green forever. A scenario
	// that declares no assertions at all is a different thing: its author
	// asked for traffic and nothing more, and it is not an error.
	for _, r := range results {
		for _, a := range r.Assertions {
			if a.Verdict == load.VerdictUnverified {
				return aferrors.Coded(aferrors.AFLOD015,
					"scenario", r.Scenario,
					"detail", fmt.Sprintf("the assertion %s could not be measured: %s", a.Name, a.Detail))
			}
		}
	}
	return nil
}
