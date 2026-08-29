package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
)

// InvariantsJSON is the machine readable result of asking the data.
type InvariantsJSON struct {
	Held       bool                  `json:"held"`
	Violated   int                   `json:"violated"`
	Blocked    int                   `json:"blocked"`
	Invariants []env.InvariantResult `json:"invariants"`
}

func newInvariantsCommand(e *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "invariants",
		Short: "Ask the data the questions the manifest declares",
		Long: strings.TrimSpace(`
An invariant is a read only statement that must return no rows, asked of the
branch, so that a flow which appears to succeed while corrupting data is caught
by the data rather than by the screen.

They are asked automatically after the workflows in 'af test' and 'af ci'. This
runs them on their own, which is what you want while writing one, or after a
migration, or when a run failed and you want to know whether the data is the
reason.

Every statement runs inside a transaction opened READ ONLY, so a write is
refused by Postgres rather than trusted not to happen, and each one has its own
timeout. Rows returned means the invariant is violated, and the rows are the
evidence: they are printed, because a check that tells you something is wrong
without telling you which rows has told you to go and do the work yourself.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}

			results, err := o.RunInvariants(cmd.Context())
			if err != nil {
				return err
			}
			if len(results) == 0 {
				if e.Out.Format == FormatJSON {
					return e.Out.JSON(InvariantsJSON{Held: true})
				}
				e.Out.Println("")
				e.Out.Println(e.Out.Wrap(
					"  This manifest declares no invariants. They are the assertions the "+
						"application cannot make from the outside: see "+
						"https://antifailure.dev/docs/guides/invariants/.", 2))
				return nil
			}

			report := &env.TestReport{Invariants: results}
			violated, blocked := report.InvariantsViolated(), report.InvariantsBlocked()

			if e.Out.Format == FormatJSON {
				if err := e.Out.JSON(InvariantsJSON{
					Held: violated == 0 && blocked == 0, Violated: violated,
					Blocked: blocked, Invariants: results,
				}); err != nil {
					return err
				}
				if violated > 0 {
					return silent(failure(report))
				}
				return nil
			}

			e.Out.Section("Asking the data")
			printInvariants(e, report)

			e.Out.Println("")
			e.Out.Printf("  %d held, %d violated, %d could not be checked\n",
				len(results)-violated-blocked, violated, blocked)
			if blocked > 0 {
				e.Out.Println(e.Out.Wrap(
					"  An invariant that could not be checked has not found anything, and is "+
						"not counted against the change.", 2))
			}
			if violated > 0 {
				return silent(failure(report))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to ask, defaulting to the checked out one")
	return cmd
}
