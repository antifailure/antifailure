package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// TestJSON is the machine readable result of a run.
type TestJSON struct {
	Passed     int                   `json:"passed"`
	Failed     int                   `json:"failed"`
	Flaky      int                   `json:"flaky"`
	Blocked    int                   `json:"blocked"`
	Unverified int                   `json:"unverified"`
	Duration   string                `json:"duration"`
	Results    []env.WorkflowResult  `json:"results"`
	Invariants []env.InvariantResult `json:"invariants,omitempty"`
}

func newTestCommand(e *Env) *cobra.Command {
	var branch, runner string
	var only []string
	var attempts int
	var headed bool
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Run the manifest's workflows against the environment",
		Long: strings.TrimSpace(`
Agents drive the application the way a person does, through the accessibility
tree, and return a verdict with a video, a trace, and steps to reproduce it.

Five verdicts, not two. The one that matters is blocked: a browser that
crashed, a page that never loaded, or a persona with no password is not
evidence about the application, and charging it to the application is how
people learn to ignore the results. Only a real failure exits non zero.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if fork := forkGate(e); fork.Refused {
				return refuseFork(fork)
			}
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}
			e.Out.Section("Running workflows")
			report, err := o.Test(cmd.Context(), env.TestOptions{
				Only: only, Attempts: attempts, Headed: headed, RunnerPath: runner,
			})
			if err != nil {
				return err
			}

			if e.Out.Format == FormatJSON {
				if err := e.Out.JSON(TestJSON{
					Passed: report.Passed, Failed: report.Failed, Flaky: report.Flaky,
					Blocked: report.Blocked, Unverified: report.Unverified,
					Duration:   report.Duration.Round(time.Millisecond).String(),
					Results:    report.Results,
					Invariants: report.Invariants,
				}); err != nil {
					return err
				}
				if report.AnyFailed() {
					return silent(failure(report))
				}
				return nil
			}

			e.Out.Println("")
			for _, r := range report.Results {
				symbol, style := verdictStyle(e, r.Outcome.Verdict)
				e.Out.Status(symbol, r.Workflow,
					fmt.Sprintf("%s in %s", style, (time.Duration(r.DurationMs)*time.Millisecond).Round(time.Millisecond)))
				if r.Outcome.Verdict != "pass" {
					e.Out.Printf("      %s\n", e.Out.Wrap(r.Outcome.Detail, 6))
				}
				for _, line := range r.Outcome.Reproduction {
					e.Out.Printf("      %s\n", e.Out.S(StyleDim, line))
				}
				if r.Evidence.Trace != "" {
					e.Out.Printf("      %s %s\n", e.Out.S(StyleDim, "trace"), e.Out.S(StyleDim, r.Evidence.Trace))
				}
				if len(r.Evidence.Failed) > 0 {
					// Usually the egress policy, and saying so beats leaving
					// somebody to guess why a page half loaded.
					e.Out.Printf("      %s %d requests the page could not make, the first was %s\n",
						e.Out.S(StyleDim, "network"), len(r.Evidence.Failed), r.Evidence.Failed[0])
				}
			}

			printInvariants(e, report)

			e.Out.Println("")
			e.Out.Printf("  %d passed, %d failed, %d flaky, %d blocked, %d unverified, in %s\n",
				report.Passed, report.Failed, report.Flaky, report.Blocked,
				report.Unverified, report.Duration.Round(time.Second))
			if report.Blocked > 0 {
				e.Out.Println(e.Out.Wrap(
					"  Blocked means the runner or the environment could not carry the workflow "+
						"through, so it is not counted against the application.", 2))
			}
			if report.AnyFailed() {
				return silent(failure(report))
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&only, "only", nil, "Run just these workflows, by name")
	cmd.Flags().IntVar(&attempts, "attempts", 2, "How many times to try a workflow before deciding")
	cmd.Flags().BoolVar(&headed, "headed", false, "Show the browser rather than running it hidden")
	cmd.Flags().StringVar(&runner, "runner", "", "Path to the runner's entry point")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to run against, defaulting to the checked out one")
	return cmd
}

func verdictStyle(e *Env, verdict string) (string, string) {
	// A word the engine cannot read is blocked, which is what report.Verdict
	// rolls it up as. This arm used to render it as unverified, so a runner
	// ahead of this engine produced a terminal line and a pull request comment
	// that disagreed about the same workflow.
	if !report.Known(verdict) {
		verdict = "blocked"
	}
	switch verdict {
	case "pass":
		return e.Out.S(StyleGood, SymbolOK), "passed"
	case "fail":
		return e.Out.S(StyleBad, SymbolFail), "failed"
	case "flaky":
		return e.Out.S(StyleWarn, SymbolWarn), "flaky"
	case "blocked":
		return e.Out.S(StyleAccent, SymbolSkip), "blocked"
	default:
		return e.Out.S(StyleDim, SymbolSkip), "unverified"
	}
}

// printInvariants writes what the data said, under the workflows.
//
// The violating rows are printed rather than summarised, because the rows are
// the diagnosis. "no-orphan-orders does not hold" tells somebody to go and
// look; the two order ids that have no user tell them where.
func printInvariants(e *Env, report *env.TestReport) {
	if len(report.Invariants) == 0 {
		return
	}
	e.Out.Println("")
	e.Out.Printf("  %s\n", e.Out.S(StyleDim, "invariants"))
	for _, inv := range report.Invariants {
		switch {
		case inv.Error != "":
			e.Out.Status(e.Out.S(StyleAccent, SymbolSkip), inv.Name, "could not be checked")
			e.Out.Printf("      %s\n", e.Out.Wrap(inv.Error, 6))
		case inv.Held:
			e.Out.Status(e.Out.S(StyleGood, SymbolOK), inv.Name,
				fmt.Sprintf("held in %s", (time.Duration(inv.DurationMs)*time.Millisecond).Round(time.Millisecond)))
		default:
			e.Out.Status(e.Out.S(StyleBad, SymbolFail), inv.Name, "does not hold")
			if inv.Description != "" {
				e.Out.Printf("      %s\n", e.Out.Wrap(inv.Description, 6))
			}
			if len(inv.Columns) > 0 {
				e.Out.Printf("      %s\n", e.Out.S(StyleDim, strings.Join(inv.Columns, "  ")))
			}
			for _, row := range inv.Rows {
				e.Out.Printf("      %s\n", strings.Join(row, "  "))
			}
			if inv.More {
				e.Out.Printf("      %s\n", e.Out.S(StyleDim, "and more; run the statement to see them all"))
			}
		}
	}
}

// firstViolated names an invariant that does not hold, for the error message.
func firstViolated(report *env.TestReport) string {
	for _, inv := range report.Invariants {
		if inv.Violated() {
			return inv.Name
		}
	}
	return ""
}

// failure turns a failed run into the error that names its cause.
//
// A run can fail two ways now, and saying "workflow X exhausted its budget"
// when every workflow passed and the data is broken sends somebody to read a
// trace that shows nothing wrong. The workflows are named first because a
// broken flow usually explains a broken invariant, and not the other way
// round.
func failure(report *env.TestReport) error {
	if report.Failed > 0 {
		return aferrors.Coded(aferrors.AFAGT002,
			"workflow", firstFailing(report), "budget", "its attempts")
	}
	name := firstViolated(report)
	detail := "the statement returned rows"
	for _, inv := range report.Invariants {
		if inv.Name == name && inv.Description != "" {
			detail = inv.Description
			break
		}
	}
	return aferrors.Coded(aferrors.AFAGT012, "invariant", name, "detail", detail)
}

func firstFailing(report *env.TestReport) string {
	for _, r := range report.Results {
		if r.Outcome.Verdict == "fail" {
			return r.Workflow
		}
	}
	return "a workflow"
}
