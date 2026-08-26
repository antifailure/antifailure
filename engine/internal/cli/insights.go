package cli

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/insights"
)

func newInsightsCommand(e *Env) *cobra.Command {
	var branch, baseline, save string
	var limit int
	cmd := &cobra.Command{
		Use:   "insights",
		Short: "Show what the database noticed while the environment ran",
		Long: strings.TrimSpace(`
The bugs this looks for are the ones no test catches, because the test passes:
the endpoint that now runs four hundred queries instead of two, the index that
stopped being used, the sequential scan on a table that grew. Each is correct
and slow, and correct and slow is what takes a site down under load rather than
in review.

  af insights --save baseline.json     on main
  af insights --baseline baseline.json on the branch

It says what it could not measure. pg_stat_statements is an extension somebody
has to install, and an insight that silently reports nothing because it is
missing looks exactly like a clean bill of health.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}
			report, err := o.Insights(cmd.Context(), limit)
			if err != nil {
				return err
			}

			if save != "" {
				body, mErr := json.MarshalIndent(report, "", "  ")
				if mErr == nil {
					if wErr := os.WriteFile(save, body, 0o644); wErr != nil {
						e.Out.Printf("  could not save the baseline: %v\n", wErr)
					} else {
						e.Out.Printf("  baseline saved to %s\n", save)
					}
				}
			}

			var diff insights.Diff
			compared := false
			if baseline != "" {
				body, rErr := os.ReadFile(baseline)
				if rErr != nil {
					e.Out.Printf("  no baseline at %s, so nothing is compared\n", baseline)
				} else {
					var prior insights.Report
					if json.Unmarshal(body, &prior) == nil {
						diff = report.CompareTo(prior, 0, 0)
						compared = true
					}
				}
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(map[string]any{
					"report": report, "diff": diff, "compared": compared,
				})
			}

			e.Out.Section("Database insights")
			e.Out.Raw(report.Explain())

			if !compared {
				e.Out.Println(e.Out.Wrap(
					"Nothing to compare against. Save a baseline on main with --save, then pass "+
						"it here with --baseline: a query running 412 times means nothing without "+
						"knowing it ran 4 times before.", 0))
				return nil
			}
			if diff.Empty() {
				e.Out.Status(e.Out.S(StyleGood, SymbolOK), "no regressions", "against the baseline")
				return nil
			}

			e.Out.Section("What got worse")
			for _, c := range diff.Busier {
				e.Out.Printf("  %s %.0f times more often (%0.f then, %0.f now)\n    %s\n",
					e.Out.S(StyleWarn, SymbolWarn), c.Factor, c.Before, c.After, c.Text)
			}
			for _, c := range diff.Slower {
				e.Out.Printf("  %s %.1f times slower (%.2fms then, %.2fms now)\n    %s\n",
					e.Out.S(StyleWarn, SymbolWarn), c.Factor, c.Before, c.After, c.Text)
			}
			for _, q := range diff.NewQueries {
				e.Out.Printf("  %s new: %d calls, %.1fms total\n    %s\n",
					e.Out.S(StyleDim, SymbolSkip), q.Calls, q.TotalMs, q.Text)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "How many queries to show")
	cmd.Flags().StringVar(&baseline, "baseline", "", "Compare against a report saved earlier")
	cmd.Flags().StringVar(&save, "save", "", "Save this report to compare against later")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to read, defaulting to the checked out one")
	return cmd
}
