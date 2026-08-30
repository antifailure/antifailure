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
)

// af ci is the whole check in one command, because that is how it is used.
//
// A workflow file that calls five commands and threads their output together
// is a workflow file every user copies, edits slightly, and gets subtly wrong.
// One command that brings the environment up, runs the agents, writes the
// comment, and tears down whatever happens is a workflow file nobody has to
// understand.
//
// Teardown is not a later step, because a step that runs after a failing step
// does not run, and an environment that outlives its pull request is the leak
// this product exists to prevent. It is called explicitly before the report is
// written, with a deferred call behind it as the safety net for a panic or an
// interrupt. Deferred alone was the old shape, and it put the teardown after
// the report, which is why a run that could not remove its database reported
// pass.

func newCICommand(e *Env) *cobra.Command {
	var branch, output, docsBase, runner, baseline, saveBaseline string
	var skipTeardown, withLoad bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "ci",
		Short: "Bring an environment up, run everything, write a report, tear it down",
		Long: strings.TrimSpace(`
The whole check in one command, for a pull request.

The agents drive the workflows, the invariants are asked of the data, the
migrations are rehearsed against a throwaway branch of the golden, and what the
environment reached for is summarised. Every finding is ranked by the manifest's
policy block, which decides what fails the check and what is only reported.

Teardown happens whatever the outcome, including a failure and including an
interrupt, because an environment that outlives its pull request is the leak
this product exists to prevent. It happens before the report is written, so a
teardown that left something behind is in the report rather than after it.

Only a real finding exits non zero. A blocked run says what was missing and
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

			o, m, err := orchestratorWithManifest(e, branch)
			if err != nil {
				return err
			}
			gate := report.Configure(m.Policy)
			run := report.Run{
				Environment: o.EnvID(), Branch: branchName(e, branch),
				Commit: commitSHA(e), DocsBase: docsBase,
			}
			started := e.Clock.Now()
			var migration []report.Finding

			// Teardown runs at most once and it runs BEFORE the report is
			// written, which is the change that makes a failed cleanup mean
			// anything. It used to be a deferred call after the report, so a
			// run that could not remove its database reported pass and exited
			// zero while the branch stayed up.
			tornDown := false
			tearDown := func() {
				if skipTeardown || tornDown {
					return
				}
				tornDown = true
				c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
				defer cancel()
				td, downErr := o.Down(c)
				cl := &report.Cleanup{}
				if td != nil {
					cl.Removed = td.Removed
					for _, p := range td.Pending {
						cl.Pending = append(cl.Pending,
							fmt.Sprintf("%s %s: %s", p.Kind, p.ID, p.Reason))
					}
				}
				if downErr != nil {
					cl.Error = downErr.Error()
				}
				run.Cleanup = cl
				e.Out.Printf("  torn down, %d resources removed\n", cl.Removed)
			}
			// The safety net, for a panic or an interrupt that never reaches
			// finish below. Idempotent, so the two together tear down once.
			defer tearDown()

			finish := func() {
				tearDown()
				// Assembled in one place and in a deliberate order, because
				// findings at the same level are shown in the order they were
				// added: what the change does to the database, what it reached
				// for, what its data looked like, what it cost under load, and
				// what we failed to clean up.
				run.Findings = append(run.Findings, migration...)
				for _, f := range []*report.Finding{
					egressFinding(run.Egress, gate),
					maskingFinding(run.Verification, gate),
					loadFinding(run.Load, gate),
					cleanupFinding(run.Cleanup, gate),
				} {
					if f != nil {
						run.Findings = append(run.Findings, *f)
					}
				}
				run.Duration = e.Clock.Since(started).Round(time.Second).String()
				writeReport(e, run, output)
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
				finish()
				return upErr
			}

			// Before the workflows, not after. The scan is about what came out
			// of the golden, and an agent that types a plausible email address
			// into a signup form would otherwise be indistinguishable from a
			// masking rule that missed.
			run.Verification = verifyMasking(ctx, e, o, gate, &run)

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

			// After the workflows, because the query regression and the plan
			// statements come from what this environment actually ran.
			migration = readDatabase(ctx, e, o, gate,
				insights.Configure(m.Insights), &run, baseline, saveBaseline)

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

			finish()
			return ciExit(run)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Write the report here as well as to the terminal")
	cmd.Flags().BoolVar(&skipTeardown, "keep", false, "Leave the environment up, for debugging a failure")
	cmd.Flags().BoolVar(&withLoad, "load", false, "Generate load as well as running the workflows")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Give up after this long")
	cmd.Flags().StringVar(&docsBase, "docs", "", "Where documentation links point")
	cmd.Flags().StringVar(&runner, "runner", "", "Path to the runner's entry point")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to check, defaulting to the checked out one")
	cmd.Flags().StringVar(&baseline, "baseline", "",
		"Compare queries and plans against a report saved on the base branch")
	cmd.Flags().StringVar(&saveBaseline, "save-baseline", "",
		"Save this run's queries and plans, to compare a later branch against")
	return cmd
}

// ciExit turns the verdict into an exit status.
//
// Only fail is non zero. Blocked exits zero and that is deliberate and load
// bearing: it means the runner or the environment could not evaluate
// something, and a gap in our tooling must never fail somebody's build. Warn
// exits zero because that is what warn means. Flaky and unverified exit zero
// as they always have.
//
// The code names the worst finding, because the exit code is the only part of
// this a pipeline keeps and "1" tells it nothing.
func ciExit(run report.Run) error {
	if run.Verdict() != report.VerdictFail {
		return nil
	}
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
	for _, w := range run.Workflows {
		if w.Verdict == report.VerdictFail {
			return silent(aferrors.Coded(aferrors.AFAGT002,
				"workflow", "a workflow", "budget", "its attempts"))
		}
	}
	if worst, ok := run.Worst(); ok && worst.Level == report.LevelFail {
		return silent(gateError(worst))
	}
	// Unreachable while Verdict only says fail for these three, and a return
	// rather than a panic because being wrong about that must not crash a
	// check that has already written its report.
	return silent(aferrors.Coded(aferrors.AFAGT002,
		"workflow", "a workflow", "budget", "its attempts"))
}

// verifyMasking reads the environment's own branch back.
//
// It is the branch rather than the golden it came from, because the branch is
// the database the agents used and it is the one somebody is being asked to
// trust. report.Verification existed with zero producers before this, which
// is exactly the shape that reads as a working feature from every angle except
// the one that counts: the field was declared, the renderer handled it, and no
// run ever filled it.
//
// A scan costs a sample of every text column, so policy.masking set to ignore
// skips it entirely rather than running it and discarding the answer.
func verifyMasking(
	ctx context.Context, e *Env, o *env.Orchestrator, gate report.Policy, run *report.Run,
) *report.Verification {
	if gate.Masking == report.LevelIgnore {
		return nil
	}
	e.Out.Section("Reading the branch back")
	rep, err := o.MaskVerify(ctx)
	if err != nil {
		// Not a finding. A scan that could not run is a fact about us, and
		// counting it against the change is the thing blocked exists to
		// prevent.
		return &report.Verification{Unavailable: err.Error()}
	}
	v := &report.Verification{
		Clean: rep.Clean(), Columns: rep.Columns, RowsSampled: rep.RowsSampled,
	}
	for _, f := range rep.Findings {
		v.Findings = append(v.Findings, f.String())
	}
	if len(rep.Skipped) > 0 {
		// A column nobody could read is not a column that passed.
		run.Notes = append(run.Notes, fmt.Sprintf("%d columns could not be read back: %s",
			len(rep.Skipped), strings.Join(rep.Skipped, ", ")))
	}
	if v.Clean {
		e.Out.Status(e.Out.S(StyleGood, SymbolOK), "masking verified",
			fmt.Sprintf("%d columns, %d rows sampled", v.Columns, v.RowsSampled))
	}
	return v
}

// readDatabase runs the Postgres native checks and turns them into findings.
//
// This is the headline promise reaching the place a customer sees it. The
// package has rehearsed migrations on a throwaway branch, sampled pg_locks and
// diffed query plans since phase 3; af ci never called it, so none of it ever
// reached a pull request and the only way to see a lock finding was to run
// af insights by hand.
//
// It always runs, including on a change with no migrations, and that is a
// deliberate decision rather than an oversight. There is no cheap way to know
// whether migrations are pending: af up applies the branch's migrations to the
// environment's own database, so asking that database what is pending returns
// nothing on exactly the pull requests that have migrations. Knowing costs a
// branch of the golden, which is the same branch the rehearsal needs, so the
// check is run rather than the question asked. A change with nothing pending
// pays for one branch and says so in a line. insights.migration_rehearsal
// turns it off.
//
// A failure here is a note rather than a finding. The database checks not
// running is a fact about us.
func readDatabase(
	ctx context.Context, e *Env, o *env.Orchestrator, gate report.Policy,
	cfg insights.Config, run *report.Run, baselinePath, savePath string,
) []report.Finding {
	if !cfg.Enabled {
		// Said rather than passed over, and said before opening anything. A
		// report that silently omits a check reads exactly like a check that
		// found nothing.
		run.Notes = append(run.Notes,
			"the database checks did not run, because insights.enabled is false")
		return nil
	}
	e.Out.Section("Reading the database")
	opts := env.InsightsOptions{Limit: 20}
	if baselinePath != "" {
		body, rErr := os.ReadFile(baselinePath)
		var prior insights.Baseline
		switch {
		case rErr != nil:
			run.Notes = append(run.Notes,
				"there is no baseline at "+baselinePath+", so queries and plans are not compared")
		case json.Unmarshal(body, &prior) != nil:
			run.Notes = append(run.Notes,
				"the baseline at "+baselinePath+" could not be read, so queries and plans are not compared")
		default:
			opts.Baseline = &prior
		}
	}

	full, err := o.RunInsights(ctx, opts)
	if err != nil {
		run.Notes = append(run.Notes, "the database checks did not run: "+err.Error())
		return nil
	}
	if savePath != "" {
		body, mErr := json.MarshalIndent(
			insights.Baseline{Report: full.Stats, Plans: full.Plans}, "", "  ")
		if mErr == nil {
			if wErr := os.WriteFile(savePath, body, 0o644); wErr != nil {
				e.Out.Printf("  could not save the baseline: %v\n", wErr)
			} else {
				e.Out.Printf("  baseline saved to %s\n", savePath)
			}
		}
	}
	if opts.Baseline == nil && gate.QueryRegression != report.LevelIgnore {
		run.Notes = append(run.Notes,
			"no baseline, so query counts are not compared. Save one on the base branch with "+
				"'af ci --save-baseline baseline.json' and pass it here with --baseline")
	}

	findings, migration := migrationFindings(full, gate)
	run.Migration = migration
	return findings
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
