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

  af insights --save baseline.json     on main
  af insights --baseline baseline.json on the branch

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

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(full)
			}

			e.Out.Section("Database insights")
			e.Out.Raw(full.Explain())

			if opts.Baseline == nil {
				e.Out.Println(e.Out.Wrap(
					"No baseline, so query counts are not compared. Save one on main with "+
						"--save and pass it here with --baseline: a query running 412 times "+
						"means nothing without knowing it ran 4 times before.", 0))
			}
			if full.Rehearsal != nil && full.Rehearsal.Failed {
				// The report is printed first and the command still fails. A
				// migration that fails on a branch with production's shape is
				// one that would have failed in production, so exiting zero
				// would turn the whole check into a note nobody reads.
				return aferrors.Coded(aferrors.AFDB030, "detail", full.Rehearsal.Error)
			}
			if full.Rolling.Failed() {
				// Non zero for the same reason, and only for a proven break.
				// A rolling check that could not run exits zero and says so,
				// because a blocked check and a broken change must never be
				// the same exit code.
				return aferrors.Coded(aferrors.AFDB031, "detail", rollingDetail(full.Rolling))
			}
			if full.Clean() {
				e.Out.Status(e.Out.S(StyleGood, SymbolOK), "nothing to report",
					"from the checks that ran")
			}
			return nil
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
