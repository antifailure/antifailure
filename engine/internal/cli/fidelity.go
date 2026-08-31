package cli

import (
	"strings"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/fidelity"
)

// FidelityJSON is the machine readable inventory.
//
// The score is a field rather than something a reader computes, and the
// exclusions travel with it in the same object, so that a caller cannot
// serialise the number without the list of what it left out.
type FidelityJSON struct {
	Inventory    fidelity.Inventory     `json:"inventory"`
	Score        fidelity.Score         `json:"score"`
	Percent      *int                   `json:"percent"`
	Requirements []fidelity.Requirement `json:"requirements,omitempty"`
}

func newFidelityCommand(e *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "fidelity",
		Short: "What this environment reproduces, component by component, and what it does not",
		Long: strings.TrimSpace(`
An inventory of the copy against the thing it is a copy of.

Every line comes from something the engine already knew: the runtime says what
is running, the database provider says which golden the branch came from and
whether its attestation still checks out, the branch says how much it holds and
whether the personas exist in it, and the manifest says which third party hosts
the policy names and what answers for each.

There is a headline number and it is defined on the page it prints: how many of
the measured components are production's own thing rather than a substitution,
a refusal or an absence. What could not be measured is excluded from it and
named, never counted as either a pass or a failure, because a percentage that
quietly absorbs an unknown is worth less than no percentage at all.

The per dimension verdict is the part to read. A change to billing cares about
the third party hosts and not about traffic; a migration cares about the data
and about neither. One averaged number hides whichever of those is yours.

Set fidelity.require in the manifest to fail this command when a dimension is
not fully reproduced.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, m, err := orchestratorWithManifest(e, branch)
			if err != nil {
				return err
			}
			if m.Fidelity != nil && m.Fidelity.Enabled != nil && !*m.Fidelity.Enabled {
				e.Out.Println(e.Out.Wrap(
					"The inventory is turned off by fidelity.enabled in the manifest. "+
						"Nothing was measured, which is not the same as everything having passed.", 0))
				return nil
			}

			inventory, err := o.Fidelity(cmd.Context())
			if err != nil {
				return err
			}

			var require []fidelity.Requirement
			if m.Fidelity != nil {
				require = inventory.Check(m.Fidelity.Require)
			}

			if e.Out.Format == FormatJSON {
				out := FidelityJSON{
					Inventory: inventory, Score: inventory.Score(), Requirements: require,
				}
				if pct, ok := out.Score.Percent(); ok {
					out.Percent = &pct
				}
				if err := e.Out.JSON(out); err != nil {
					return err
				}
				return requirementError(require)
			}

			e.Out.Section("Fidelity of " + inventory.EnvID)
			e.Out.Raw(inventory.Explain())
			if text := fidelity.ExplainRequirements(require); text != "" {
				e.Out.Println("")
				e.Out.Raw(text)
			}
			return requirementError(require)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "",
		"Branch to inventory, defaulting to the checked out one")
	return cmd
}

// requirementError turns an unmet requirement into the exit code for it.
//
// Two codes rather than one, because the two outcomes are different facts. A
// dimension that was measured and found wanting is a statement about the
// environment; a dimension that could not be measured is a statement about
// what we could see. Reporting the second as the first is how a report stops
// being believed, and it is exactly the failure this whole feature exists to
// avoid.
//
// The unmeasurable one is reported first when both are present. Somebody whose
// gate cannot be evaluated needs to know that before they are told which
// component is missing, because until it can be evaluated the other answer is
// not the whole answer.
func requirementError(reqs []fidelity.Requirement) error {
	for _, r := range reqs {
		if !r.Met && !r.Measurable {
			return aferrors.Coded(aferrors.AFFID002,
				"dimension", string(r.Dimension), "detail", r.Because)
		}
	}
	for _, r := range reqs {
		if !r.Met {
			return aferrors.Coded(aferrors.AFFID001,
				"dimension", string(r.Dimension), "detail", r.Because)
		}
	}
	return nil
}
