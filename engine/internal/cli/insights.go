package cli

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/insights"
)

func newInsightsCommand(e *Env) *cobra.Command {
	var branch, baseline, save, against, runner string
	var limit int
	var skipRehearsal bool
	cmd := &cobra.Command{
		Use:   "insights",
		Short: "What Postgres can tell you about this change before anybody clicks anything",
		Long: strings.TrimSpace(`
A branch is a real database with production's shape in it, which makes some
questions answerable without running the application at all.

The migrations are rehearsed against a throwaway branch and every statement is
timed, so a migration that takes four seconds on an empty test database and
ninety on production row counts is visible before the deploy window rather than
during it. The plans on that branch are compared before and after, which is how
a sequential scan appearing where an index scan was gets found. And the queries
this environment ran are compared against a report saved on the base branch.

Where the migrations take something away, the previous release is built and run
against the migrated branch as well, because a rolling deploy leaves both
releases talking to the same database for the length of the window and nothing
else here checks that. It exits non zero only when a workflow passes without
the migrations and fails with them.

It says what it could not measure, and it names any check the manifest turned
off. A report that silently omits a check reads exactly like a check that found
nothing.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}

			opts := env.InsightsOptions{
				Limit: limit, SkipRehearsal: skipRehearsal,
				Against: against, RunnerPath: runner,
			}
			if baseline != "" {
				body, rErr := os.ReadFile(baseline)
				switch {
				case rErr != nil:
					e.Out.Printf("  no baseline at %s, so nothing is compared\n", baseline)
				default:
					var prior insights.Baseline
					if json.Unmarshal(body, &prior) != nil {
						e.Out.Printf("  the baseline at %s could not be read\n", baseline)
					} else {
						opts.Baseline = &prior
					}
				}
			}

			full, err := o.RunInsights(cmd.Context(), opts)
			if err != nil {
				return err
			}

			if save != "" {
				body, mErr := json.MarshalIndent(
					insights.Baseline{Report: full.Stats, Plans: full.Plans}, "", "  ")
				if mErr == nil {
					if wErr := os.WriteFile(save, body, 0o644); wErr != nil {
						e.Out.Printf("  could not save the baseline: %v\n", wErr)
					} else {
						e.Out.Printf("  baseline saved to %s\n", save)
					}
				}
			}

			if e.Out.Format != FormatJSON {
				e.Out.Section("Database insights")
				e.Out.Raw(full.Explain())
			}

			if e.Out.Format != FormatJSON && opts.Baseline == nil {
				e.Out.Println(e.Out.Wrap(
					"No baseline, so query counts are not compared. Save one on main with "+
						"--save and pass it here with --baseline: a query running 412 times "+
						"means nothing without knowing it ran 4 times before.", 0))
			}
			return insightsResult(e, full)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "How many queries to show")
	cmd.Flags().StringVar(&baseline, "baseline", "", "Compare against a report saved earlier")
	cmd.Flags().StringVar(&save, "save", "", "Save this report to compare against later")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to read, defaulting to the checked out one")
	cmd.Flags().BoolVar(&skipRehearsal, "no-rehearsal", false,
		"Skip the migration rehearsal, which is the only check that makes a second branch")
	cmd.Flags().StringVar(&against, "against", "",
		"Which commit the previous release is, overriding the manifest")
	cmd.Flags().StringVar(&runner, "runner", "", "Path to the runner's entry point")
	return cmd
}

// insightsResult renders the run in whichever format was asked for and
// returns the exit, which is the same exit either way.
//
// The format used to decide both. `af insights -o json` returned the moment
// the document was written, so every exit below it was unreachable and the
// command reported SUCCESS on a migration that had failed on a branch with
// production's shape. Adding `-o json` turned a break into a pass. `af ci` in
// this package already keeps these apart, writeReport rendering and ciExit
// deciding, and two commands in one tool must not disagree about whether a
// break is a break.
//
// Order matters and it is the reverse of what reads naturally. A proven break
// is reported before a blocked check, because a run that both broke and could
// not measure something is a break, and the break is the more urgent.
func insightsResult(e *Env, full insights.Full) error {
	if e.Out.Format == FormatJSON {
		if err := e.Out.JSON(full); err != nil {
			return err
		}
		if err := insightsBreaks(full); err != nil {
			return err
		}
		// No summary line in JSON: the document carries blocked itself. The
		// exit still has to say so, which is what this returns.
		_, err := insightsSummary(full)
		return err
	}
	if err := insightsBreaks(full); err != nil {
		return err
	}
	return printInsightsSummary(e, full)
}

// insightsBreaks is the proven breaks, and only the proven breaks.
func insightsBreaks(full insights.Full) error {
	if full.Rehearsal != nil && full.Rehearsal.Failed {
		// A migration that fails on a branch with production's shape is one
		// that would have failed in production, so exiting zero would turn
		// the whole check into a note nobody reads.
		return aferrors.Coded(aferrors.AFDB030, "detail", full.Rehearsal.Error)
	}
	if full.Rolling.Failed() {
		// Non zero for the same reason, and only for a proven break. A
		// rolling check that could not run exits zero and says so, because a
		// blocked check and a broken change must never be the same exit code.
		return aferrors.Coded(aferrors.AFDB032, "detail", rollingDetail(full.Rolling))
	}
	return nil
}

// printInsightsSummary writes the last line, or writes nothing at all.
//
// The guard is the whole point and it is why this is a function rather than
// four lines inside RunE: an empty line from insightsSummary must produce no
// output, not a bare ok with nothing after it. That is testable here against a
// buffer and was not testable inside a cobra RunE that needs a database.
func printInsightsSummary(e *Env, full insights.Full) error {
	line, err := insightsSummary(full)
	if line != "" {
		e.Out.Status(e.Out.S(StyleGood, SymbolOK), line, "from the checks that ran")
	}
	return err
}

// insightsSummary is the last line and the exit code, decided together.
//
// They are decided together because they were decided apart, and that is the
// defect this function exists for. af insights said "the migrations were not
// rehearsed: no migration tool was recognised in this repository" in its body
// and then printed
//
//	ok  nothing to report
//
// and exited zero. Every individual sentence was honest and the last line was
// not, and the last line is the one a developer reads.
//
// Three states, three answers, and the middle one is the new one:
//
//   - a proven break exits 5 or 8 and is handled by the caller before this,
//     because a run that both broke and could not measure something is a
//     break and the break is the more urgent fact;
//   - a check that was asked for and did not run exits 7 and prints no ok,
//     because a blocked check and a passing one must not be the same exit
//     code;
//   - everything else is a pass and says so.
//
// Exit 7 rather than reusing a failure code, for the reason already written
// into the rolling check above: a blocked check and a broken change must
// never be the same exit code, or people learn to ignore the one that cries
// wolf. 7 is the code this catalog already gives to "could not verify".
//
// An empty line means print nothing, which is the case where a check found
// something and has already printed it.
func insightsSummary(full insights.Full) (string, error) {
	if full.IsBlocked() {
		return "", aferrors.Coded(aferrors.AFDB033, "detail",
			strings.Join(full.Blocked, "; "))
	}
	if full.Clean() {
		return "nothing to report", nil
	}
	return "", nil
}

// rollingDetail is the one sentence the failure carries out to the exit code.
//
// The named workflow and the named object, because a code with "a workflow
// failed" in it is a code somebody has to open the log to understand, and the
// log is the part that scrolls away in CI.
func rollingDetail(r *insights.Rolling) string {
	for _, w := range r.Workflows {
		if w.Verdict != insights.RollingFail {
			continue
		}
		if w.Cause != nil {
			return w.Name + " fails against the migrated schema, and " +
				w.Cause.Sentence(shortAgainst(r))
		}
		return w.Name + " fails against the migrated schema and passes without the " +
			"migrations, on " + shortAgainst(r)
	}
	return "the previous release fails against the migrated schema"
}

func shortAgainst(r *insights.Rolling) string {
	if len(r.Against) > 12 {
		return r.Against[:12]
	}
	return r.Against
}
