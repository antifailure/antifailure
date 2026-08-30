package cli

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// af ci is the whole check in one command, because that is how it is used.
//
// A workflow file that calls five commands and threads their output together
// is a workflow file every user copies, edits slightly, and gets subtly wrong.
// One command that brings the environment up, runs the agents, writes the
// comment, and tears down whatever happens is a workflow file nobody has to
// understand.
//
// Teardown is in a deferred path rather than a later step, because a step that
// runs after a failing step does not run, and an environment that outlives its
// pull request is the leak this product exists to prevent.

func newCICommand(e *Env) *cobra.Command {
	var branch, output, docsBase, runner string
	var skipTeardown, withLoad bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "ci",
		Short: "Bring an environment up, run everything, write a report, tear it down",
		Long: strings.TrimSpace(`
The whole check in one command, for a pull request.

Teardown happens whatever the outcome, including a failure and including an
interrupt, because an environment that outlives its pull request is the leak
this product exists to prevent.

Only a real failure exits non zero. A blocked run says what was missing and
exits zero, so an incomplete environment is not indistinguishable from a broken
change.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}

			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}
			run := report.Run{
				Environment: o.EnvID(), Branch: branchName(e, branch),
				Commit: commitSHA(e), DocsBase: docsBase,
			}
			started := e.Clock.Now()

			if !skipTeardown {
				// Deferred rather than a later step. A step that runs after a
				// failing step does not run.
				defer func() {
					c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
					defer cancel()
					td, downErr := o.Down(c)
					reportTeardown(e, td, downErr)
				}()
			}

			e.Out.Section("Bringing up " + o.EnvID())
			up, upErr := o.Up(ctx)
			if up != nil {
				run.URL, run.Golden = up.URL, up.Golden
			}
			if upErr != nil {
				// The environment did not come up, which is not evidence about
				// the change. The report says so and the exit code agrees.
				run.Workflows = nil
				run.Duration = e.Clock.Since(started).Round(time.Second).String()
				writeReport(e, run, output)
				return upErr
			}

			e.Out.Section("Running workflows")
			test, testErr := o.Test(ctx, env.TestOptions{Attempts: 2, RunnerPath: runner})
			if testErr != nil {
				e.Out.Printf("  %s %s\n", e.Out.S(StyleWarn, SymbolWarn), testErr.Error())
			}
			if test != nil {
				for _, r := range test.Results {
					run.Workflows = append(run.Workflows, report.Workflow{
						Name: r.Workflow, Verdict: r.Outcome.Verdict, Detail: r.Outcome.Detail,
						Steps: r.Outcome.Reproduction, Trace: r.Evidence.Trace,
					})
				}
				for _, i := range test.Invariants {
					run.Invariants = append(run.Invariants, report.Invariant{
						Name: i.Name, Description: i.Description, Held: i.Held,
						Columns: i.Columns, Rows: i.Rows, More: i.More, Error: i.Error,
					})
				}
			}

			if decisions, dErr := o.Decisions(ctx, 500); dErr == nil && len(decisions) > 0 {
				run.Egress = summariseEgress(decisions)
			}

			if withLoad {
				e.Out.Section("Generating load")
				if res, _, lErr := o.Load(ctx, env.LoadOptions{Duration: 30 * time.Second}); lErr == nil {
					l := &report.Load{
						Sent: res.Sent, Rate: res.Rate,
						ErrorRate: res.ErrorRate, P95Ms: res.Overall.P95Ms,
					}
					p95, _ := o.Thresholds()
					for _, b := range res.Breaches(p95, 0) {
						l.Regressed = append(l.Regressed, b.What)
					}
					run.Load = l
				}
			}

			run.Duration = e.Clock.Since(started).Round(time.Second).String()
			writeReport(e, run, output)

			if run.Verdict() == "fail" {
				// Named, because a run can now fail two ways and the exit
				// message is the only part a script keeps.
				if run.InvariantsViolated() > 0 {
					var first string
					for _, i := range run.Invariants {
						if i.Violated() {
							first = i.Name
							break
						}
					}
					return silent(aferrors.Coded(aferrors.AFAGT012,
						"invariant", first, "detail", "the statement returned rows"))
				}
				return silent(aferrors.Coded(aferrors.AFAGT002,
					"workflow", "a workflow", "budget", "its attempts"))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Write the report here as well as to the terminal")
	cmd.Flags().BoolVar(&skipTeardown, "keep", false, "Leave the environment up, for debugging a failure")
	cmd.Flags().BoolVar(&withLoad, "load", false, "Generate load as well as running the workflows")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Give up after this long")
	cmd.Flags().StringVar(&docsBase, "docs", "", "Where documentation links point")
	cmd.Flags().StringVar(&runner, "runner", "", "Path to the runner's entry point")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to check, defaulting to the checked out one")
	return cmd
}

// writeReport prints the comment and writes it where a workflow can post it.
func writeReport(e *Env, run report.Run, output string) {
	body := run.Comment()
	if output != "" {
		if err := os.WriteFile(output, []byte(body), 0o644); err != nil {
			e.Out.Printf("  could not write the report to %s: %v\n", output, err)
		}
	}
	// Also appended to the job summary when there is one, so a run shows its
	// result without anybody opening the log.
	if summary := e.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		if f, err := os.OpenFile(summary, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			_, _ = f.WriteString(run.Markdown())
			_ = f.Close()
		}
	}
	if e.Out.Format == FormatJSON {
		_ = e.Out.JSON(run)
		return
	}
	e.Out.Println("")
	e.Out.Raw(run.Markdown())
}

// summariseEgress counts what the environment reached.
func summariseEgress(decisions []local.Decision) *report.Egress {
	out := &report.Egress{}
	surprises := map[string]bool{}
	for _, d := range decisions {
		switch d.Mode {
		case "allow", "sandbox":
			out.Allowed++
		case "capture":
			out.Captured++
		case "mock":
			out.Mocked++
		default:
			out.Refused++
			if d.Rule == "" && d.Host != "" {
				// No rule matched at all, which means the manifest does not
				// mention this host. Usually a dependency somebody added
				// without noticing.
				surprises[d.Host] = true
			}
		}
	}
	for host := range surprises {
		out.Surprises = append(out.Surprises, host)
	}
	sort.Strings(out.Surprises)
	return out
}

func branchName(e *Env, override string) string {
	if override != "" {
		return override
	}
	// The head ref in a pull request, because HEAD is a detached merge commit
	// there and reporting that helps nobody.
	if ref := e.Getenv("GITHUB_HEAD_REF"); ref != "" {
		return ref
	}
	return currentBranch(e.WorkDir)
}

func commitSHA(e *Env) string {
	if sha := e.Getenv("GITHUB_SHA"); sha != "" {
		return sha
	}
	out, err := exec.Command("git", "-C", e.WorkDir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// reportTeardown says what the deferred teardown did, including when it did
// nothing.
//
// This used to be one line inside the defer, guarded by `if downErr == nil`, so
// the two outcomes that matter were both silent. A teardown that could not
// reach the daemon printed nothing whatsoever, which on a green run is
// indistinguishable from --keep. A teardown that ran and left containers behind
// printed only how many it removed, and never that anything stayed. Either way
// a pull request went green with an environment still on the runner, which is
// the leak this command exists to prevent, reported as a success.
//
// It does not change the exit code, and that is a decision rather than an
// oversight. af ci's exit code is the verdict on the change under test; a
// container the daemon refused to remove is a fact about the runner, and
// failing somebody's correct pull request for it sends them to read their own
// diff. The leak check that does gate is G10, which counts what is left after
// the suites rather than trusting any one command's report. What this owes the
// reader is the sentence, and the sentence was missing.
func reportTeardown(e *Env, td *env.Teardown, err error) {
	if err != nil {
		// Named with the same next step the error catalog gives for AF-RUN-030,
		// because the situation is the same one and somebody reading a CI log
		// needs the command, not the diagnosis.
		e.Out.Printf("  %s teardown did not run: %v\n", e.Out.S(StyleBad, SymbolFail), err)
		e.Out.Printf("    The environment is still up. Run 'af down' where this ran.\n")
		return
	}
	if td == nil {
		return
	}
	e.Out.Printf("  torn down, %d resources removed\n", td.Removed)
	if len(td.Pending) == 0 {
		return
	}
	e.Out.Printf("  %s %d resources are still there. Run 'af down' where this ran; the journal remembers what is left.\n",
		e.Out.S(StyleBad, SymbolFail), len(td.Pending))
	for _, p := range td.Pending {
		e.Out.Printf("    %s/%s: %s\n", p.Kind, p.ID, p.Reason)
	}
}
