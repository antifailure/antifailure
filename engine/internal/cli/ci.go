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

	"github.com/antifailure/antifailure/engine/internal/egress"
	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/load"
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
	var branch, output, jsonOutput, docsBase, runner, baseline, saveBaseline string
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

			// Before the orchestrator, which is before anything at all.
			// A pull request from a fork the policy does not allow gets a
			// report saying nothing ran and no environment, and the order
			// is the security property: an orchestrator resolves providers
			// and names an environment, so refusing after building one is
			// refusing after doing part of what was refused.
			if fork := forkGate(e); fork.Refused {
				announceComment(e)
				return skippedRun(e, forkRun(e, branch, docsBase, fork), output, jsonOutput)
			}

			o, m, err := orchestratorWithManifest(e, branch)
			if err != nil {
				return err
			}
			// github.comment, which nothing read until now. Resolved once
			// here rather than at each of the three places a report is
			// written, so the two exits from this command cannot disagree
			// about whether there is a comment.
			announceComment(e)
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
				reportTeardown(e, td, downErr)
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
					workflowsUnverifiedFinding(run, gate),
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
				writeReport(e, run, output, jsonOutput)
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
				// What the run noticed that belongs to no single workflow.
				// A synthesized response nobody's window claimed is the case
				// this exists for, and dropping it here would put the run
				// back where it started: the fact reaching the engine and
				// stopping there.
				run.Notes = append(run.Notes, test.Notes...)
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
				// DefaultDuration rather than Duration, so the manifest's own
				// load.duration still decides. The hardcoded 30s used to sit
				// in the slot that overrides it.
				res, refused, lErr := o.Load(ctx, env.LoadOptions{
					DefaultDuration: 30 * time.Second, DefaultScale: 1,
				})
				switch {
				case lErr != nil:
					// The header with nothing under it is how this read
					// before: a load run that failed outright produced the
					// section and no finding, and silence is read as success.
					e.Out.Printf("  %s %s\n", e.Out.S(StyleWarn, SymbolWarn), lErr.Error())
					run.Notes = append(run.Notes, "the load run did not complete: "+lErr.Error())
				default:
					p95, errorRate := o.Thresholds()
					run.Load = loadReport(res, refused, p95, errorRate, &run)
				}
			}

			finish()
			return ciExit(run)
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
	// The same report, as JSON, for something that has to read it rather than
	// display it. Not -o json, which is the whole terminal's format: a step
	// that prints progress and captures a machine readable result cannot use
	// one switch for both, and redirecting stdout to a file gives up the
	// progress a person watching the job is there for.
	//
	// It exists because the hosted control plane's pull request check needs
	// the counts and the environment name, and reading them back out of the
	// Markdown would be a parser for prose.
	cmd.Flags().StringVar(&jsonOutput, "report-json", "",
		"Write the same report here as JSON, for a program to read")
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

	// From the same run rather than a second one. The statistics and the
	// migration findings come out of one report, and asking for insights twice
	// would rehearse the migrations `af up` has already applied.
	run.Insights = summariseInsights(full.Stats)

	findings, migration := migrationFindings(full, gate)
	run.Migration = migration
	return findings
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
func writeReport(e *Env, run report.Run, output, jsonOutput string) {
	body := run.Comment()
	if output != "" {
		if err := os.WriteFile(output, []byte(body), 0o644); err != nil {
			e.Out.Printf("  could not write the report to %s: %v\n", output, err)
		}
	}
	if jsonOutput != "" {
		// Marshalled and then written, rather than encoded into the file. A
		// failed marshal that had already truncated the file would leave
		// whatever reads this looking at half a report, which is worse than
		// looking at none: half a report decodes, and the counts it carries
		// are wrong rather than absent.
		if encoded, err := json.MarshalIndent(run, "", "  "); err != nil {
			e.Out.Printf("  could not encode the report as JSON: %v\n", err)
		} else if err := os.WriteFile(jsonOutput, append(encoded, '\n'), 0o644); err != nil {
			e.Out.Printf("  could not write the report to %s: %v\n", jsonOutput, err)
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
// summariseEgress delegates to the shared reader.
func summariseEgress(decisions []local.Decision) *report.Egress {
	return egress.Summarise(decisions)
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

// loadReport turns a load result into the report's section, against BOTH
// thresholds the manifest sets.
//
// The error rate threshold used to be thrown away here. The call was
// `p95, _ := o.Thresholds()` and then `res.Breaches(p95, 0)`, and Breaches
// short circuits on `errorRate > 0`, so a zero limit builds no error rate
// breach at all. A change that failed every request under load produced an
// empty Regressed list, never reached policy.load_regression, and merged
// green, while `af load run` on the same manifest and the same result exited
// non zero. Two commands, one manifest, opposite answers.
//
// It was invisible for a specific reason worth recording. `p95_increase` is
// refused under the `access_log` and `none` sources, so those projects passed
// (0, 0) and got nil back from a function that had nothing to compare. This
// repository's OWN manifest is `source: none` with `error_rate: 0.02`, so the
// dogfooding that would have caught it could not.
//
// The inert case is reported rather than passed over, which is the same
// argument `af load run` already makes: a threshold that was in force and
// measured nothing is not a threshold that held, and a report that omits it
// reads exactly like one that checked.
// The `refused` return was the third defect in that block and it was DISCARDED
// at the call site, so `af ci --load` said the same thing whether the safe list
// let through every route or one out of forty. `af load run` has always
// reported it. Found by `loadgolden`, which had the other half of this block.
func loadReport(
	res *load.Result, refused []load.Route, p95Increase, errorRate float64, run *report.Run,
) *report.Load {
	l := &report.Load{
		Sent: res.Sent, Rate: res.Rate,
		ErrorRate: res.ErrorRate, P95Ms: res.Overall.P95Ms,
		Refused: refusedRoutes(refused),
	}
	for _, b := range res.Breaches(p95Increase, errorRate) {
		l.Regressed = append(l.Regressed, b.What)
	}
	if res.InertP95(p95Increase) {
		run.Notes = append(run.Notes,
			"load.thresholds sets p95_increase and no route had a baseline to compare against, "+
				"so nothing was measured against it")
	}
	return l
}

// refusedRoutes names the routes the generator would not send.
//
// nil for an empty list rather than an empty slice, so the report's line drops
// out entirely instead of printing a heading over nothing. A section saying
// "0 routes were not sent" is a line the reader pays for and learns nothing
// from.
func refusedRoutes(refused []load.Route) []string {
	if len(refused) == 0 {
		return nil
	}
	out := make([]string, 0, len(refused))
	for _, r := range refused {
		out = append(out, r.String())
	}
	sort.Strings(out)
	return out
}
