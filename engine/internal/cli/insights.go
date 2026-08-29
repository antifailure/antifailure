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
	var branch, baseline, save string
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

It says what it could not measure, and it names any check the manifest turned
off. A report that silently omits a check reads exactly like a check that found
nothing.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}

			opts := env.InsightsOptions{Limit: limit, SkipRehearsal: skipRehearsal}
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
	return cmd
}
