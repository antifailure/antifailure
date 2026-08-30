package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/verify"
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
					if td, downErr := o.Down(c); downErr == nil {
						e.Out.Printf("  torn down, %d resources removed\n", td.Removed)
					}
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
			}

			if decisions, dErr := o.Decisions(ctx, 500); dErr == nil && len(decisions) > 0 {
				run.Egress = summariseEgress(decisions)
			}

			// The masking result and what the database noticed. Both were
			// fields on the report that nothing ever filled in, so the two
			// sections a reviewer most needs could not appear: the one that
			// says the data was proved masked, and the one that says how the
			// change behaves against real volume. A section that cannot be
			// rendered is the same as a section that does not exist, and this
			// one was the product's central promise.
			run.Verification = attestedMasking(ctx, o, run.Golden)
			if rep, iErr := o.Insights(ctx, 25); iErr == nil {
				run.Insights = summariseInsights(rep)
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
				return silent(aferrors.Coded(aferrors.AFAGT002,
					"workflow", "a workflow", "budget", "its attempts"))
			}
			return nil
		},
	}
	// --report, not --output.
	//
	// A local --output shadows the persistent one, so on this command alone
	// -o meant "a file to write" while everywhere else it means "text or
	// json". `af ci -o json` wrote the pull request comment to a file called
	// `json`, silently, and af ci had no machine readable output at all,
	// which is notable for the one command written for CI. Renaming it frees
	// -o to mean here what it means everywhere.
	cmd.Flags().StringVar(&output, "report", "", "Write the report here as well as to the terminal")
	cmd.Flags().BoolVar(&skipTeardown, "keep", false, "Leave the environment up, for debugging a failure")
	cmd.Flags().BoolVar(&withLoad, "load", false, "Generate load as well as running the workflows")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Give up after this long")
	cmd.Flags().StringVar(&docsBase, "docs", "", "Where documentation links point")
	cmd.Flags().StringVar(&runner, "runner", "", "Path to the runner's entry point")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to check, defaulting to the checked out one")
	return cmd
}

// attestedMasking reads the golden's own attestation rather than rescanning.
//
// The scan already ran, at refresh, and its result is signed and stored on the
// version. Reading it back is what makes the pull request comment report the
// same fact the branch rule enforced, rather than a second opinion that could
// disagree with it. The signature is checked here for the same reason it
// exists: this is a different process from the one that produced it.
func attestedMasking(ctx context.Context, o *env.Orchestrator, golden string) *report.Verification {
	if golden == "" {
		return nil
	}
	versions, err := o.Goldens(ctx)
	if err != nil {
		return nil
	}
	for _, v := range versions {
		if v.ID != golden || v.Attestation == "" {
			continue
		}
		var a verify.Attestation
		if json.Unmarshal([]byte(v.Attestation), &a) != nil {
			return nil
		}
		if !a.Verify() {
			// Present and unreadable is worse than absent, so it is said
			// rather than dropped.
			return &report.Verification{
				Findings: []string{"The golden's attestation does not verify against its own key."},
			}
		}
		out := &report.Verification{
			Clean: a.Report.Clean(), Columns: a.Report.Columns,
			RowsSampled: a.Report.RowsSampled,
		}
		for _, f := range a.Report.Findings {
			// The finding, never the value that produced it.
			out.Findings = append(out.Findings,
				fmt.Sprintf("%s.%s holds %s.", f.Table, f.Column, f.Detector))
		}
		return out
	}
	return nil
}

// summariseInsights reduces the full report to what fits in a comment.
//
// Three facts, because the audience has thirty seconds: which tables the run
// read end to end, what the slowest statement cost, and how many indexes
// nothing touched. Everything else is what `af insights` is for.
func summariseInsights(r insights.Report) *report.Insights {
	out := &report.Insights{Missing: r.Missing}
	for _, s := range r.Scans {
		out.Sequential = append(out.Sequential, report.Scan{
			Table: s.Table, Scans: s.SeqScans, Rows: s.LiveRows,
		})
		if len(out.Sequential) == 3 {
			break
		}
	}
	for _, q := range r.Queries {
		if q.TotalMs > out.SlowestMs {
			out.Slowest, out.SlowestMs = q.Text, q.TotalMs
		}
	}
	for _, i := range r.Unused {
		out.Unused = append(out.Unused, i.Table+"."+i.Name)
	}
	if len(out.Sequential) == 0 && out.Slowest == "" && len(out.Unused) == 0 && len(out.Missing) == 0 {
		// Nothing to say and nothing missing. A section reading "no table
		// read end to end" is still worth printing, because it is the
		// difference between checked and not checked.
		return out
	}
	return out
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
