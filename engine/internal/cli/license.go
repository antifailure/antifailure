package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// af license exists in the community edition and says the honest thing.
//
// The command is here rather than only in the enterprise binary because
// somebody who reads about enterprise features and types af license should get
// an answer, not "unknown command". "Unknown command" reads as a broken install
// and sends them to the issue tracker; this reads as a product decision, which
// is what it is.
//
// The wording matters more than the mechanism. This edition is not a trial and
// not a crippled version: it masks data, seals the network, runs agents, and
// tears everything down, completely and permanently, for free. What a license
// adds is the set of things a large company asks for before a rollout. Saying
// that plainly is the difference between a user who feels well served and one
// who feels upsold.

func newLicenseCommand(e *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "license",
		Short: "Show the license status of this installation",
		Long: strings.TrimSpace(`
This is the community edition. It has no license and needs none.

Everything the engine does is here and stays here: masked environments, sealed
egress, captured mail, agents, load, insights, and teardown. None of it expires
and none of it phones home.

A license adds the enterprise edition, which is a separate binary built from
the ee directory of the same repository: single sign on, SCIM, custom roles and
approvals, SIEM streaming, organization wide policy enforcement, customer owned
runtime clusters, enterprise secret managers, and billing.`),
	}
	cmd.AddCommand(newLicenseStatusCommand(e))
	cmd.AddCommand(newLicenseInstallCommand(e))
	cmd.AddCommand(newLicenseRemoveCommand(e))
	return cmd
}

// LicenseJSON is the machine readable status.
type LicenseJSON struct {
	Edition string `json:"edition"`
	State   string `json:"state"`
	// Extensions names anything plugged into the community extension points, so
	// that an operator can tell an unmodified build from one that is not.
	Extensions []string `json:"extensions"`
	Message    string   `json:"message"`
}

func newLicenseStatusCommand(e *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "What this installation is licensed for",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			const message = "This is the community edition. It has no license and needs none."
			registered := extension.Default.Registered()

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(LicenseJSON{
					Edition: "community", State: "none",
					Extensions: registered, Message: message,
				})
			}

			e.Out.Println("  " + message)
			e.Out.Println("")
			e.Out.Println("  Everything the engine does is here and does not expire.")
			if len(registered) > 0 {
				// Worth printing. A build with something registered at the
				// extension points is not the stock community build, and an
				// operator debugging a refused environment needs to know that
				// before anything else.
				e.Out.Println("")
				e.Out.Println("  Registered extensions:")
				for _, name := range registered {
					e.Out.Printf("    %s\n", name)
				}
			}
			e.Out.Println("")
			e.Out.Println("  Enterprise features need the enterprise binary, built from ee/.")
			e.Out.Println("  See https://antifailure.dev/docs/enterprise/licensing")
			return nil
		},
	}
}

func newLicenseInstallCommand(e *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "install <key>",
		Short: "Install an enterprise license key",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, _ []string) error {
			// Refused rather than accepted and ignored. Storing a key this
			// binary can never act on would leave somebody believing their
			// enterprise features are on, and they would find out otherwise
			// during the rollout they bought the license for.
			e.Out.Println("  This binary is the community edition and cannot use a license key.")
			e.Out.Println("")
			e.Out.Println("  Nothing was stored. Install the enterprise binary and run this again;")
			e.Out.Println("  it reads the same key and every setting you have carries over.")
			e.Out.Println("")
			e.Out.Println("  https://antifailure.dev/docs/enterprise/licensing")
			return nil
		},
	}
}

func newLicenseRemoveCommand(e *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove the installed license key",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			e.Out.Println("  There is no license installed. This is the community edition.")
			return nil
		},
	}
}
