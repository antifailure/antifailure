package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// TestJSON is the machine readable result of a run.
type TestJSON struct {
	Passed     int                  `json:"passed"`
	Failed     int                  `json:"failed"`
	Flaky      int                  `json:"flaky"`
	Blocked    int                  `json:"blocked"`
	Unverified int                  `json:"unverified"`
	Duration   string               `json:"duration"`
	Results    []env.WorkflowResult `json:"results"`
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
					Duration: report.Duration.Round(time.Millisecond).String(),
					Results:  report.Results,
				}); err != nil {
					return err
				}
				if report.AnyFailed() {
					return silent(aferrors.Coded(aferrors.AFAGT002,
						"workflow", firstFailing(report), "budget", "its attempts"))
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
				return silent(aferrors.Coded(aferrors.AFAGT002,
					"workflow", firstFailing(report), "budget", "its attempts"))
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

func firstFailing(report *env.TestReport) string {
	for _, r := range report.Results {
		if r.Outcome.Verdict == "fail" {
			return r.Workflow
		}
	}
	return "a workflow"
}
