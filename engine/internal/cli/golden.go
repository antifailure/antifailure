package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// A golden is a masked, verified copy of production that branches are made
// from. Refreshing one is the only place unmasked data is ever touched, and it
// happens on the machine that already has the credential for it.
//
// The order is the guarantee: copy, mask, verify, and only then publish. A
// golden that fails verification is never published, so it cannot be branched,
// so no environment can hold it.

// GoldenJSON is one golden version.
type GoldenJSON struct {
	Version   string `json:"version"`
	Verified  bool   `json:"verified"`
	CreatedAt string `json:"created_at"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	RulesHash string `json:"rules_hash,omitempty"`
}

// RefreshJSON is the result of a refresh.
type RefreshJSON struct {
	Version     string      `json:"version"`
	Verified    bool        `json:"verified"`
	Tables      int         `json:"tables_masked"`
	Rows        int64       `json:"rows_masked"`
	Duration    string      `json:"duration"`
	Verify      VerifyJSON  `json:"verification"`
	Attestation interface{} `json:"attestation,omitempty"`
}

func newGoldenCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "golden",
		Short: "Manage the masked copies branches are made from",
		Long: strings.TrimSpace(`
A refresh copies production, masks it, reads it back to check the masking, and
publishes it only if that check passes.

A golden that fails verification is never published, so it cannot be branched,
so no environment can ever hold it. That is enforced by the provider rather
than by remembering to check.`),
	}
	cmd.AddCommand(newGoldenRefreshCommand(env))
	cmd.AddCommand(newGoldenListCommand(env))
	cmd.AddCommand(newGoldenGCCommand(env))
	cmd.AddCommand(newGoldenVerifyCommand(env))
	return cmd
}

func newGoldenRefreshCommand(env *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Copy production, mask it, verify it, and publish it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			env.Out.Section("Refreshing the golden")
			res, refreshErr := o.RefreshGolden(cmd.Context())

			if env.Out.Format == FormatJSON && res != nil {
				doc := RefreshJSON{
					Version: res.Version, Verified: res.Verified,
					Tables: res.Tables, Rows: res.Rows,
					Duration: res.Duration.Round(time.Second).String(),
					Verify: VerifyJSON{
						Clean: res.Report.Clean(), Tables: res.Report.Tables,
						Columns: res.Report.Columns, RowsSampled: res.Report.RowsSampled,
						SampleSize: res.Report.SampleSize, Skipped: res.Report.Skipped,
					},
				}
				for _, f := range res.Report.Findings {
					doc.Verify.Findings = append(doc.Verify.Findings, FindingJSON{
						Table: f.Schema + "." + f.Table, Column: f.Column,
						Detector: f.Detector, Example: f.Example, Rows: f.Rows,
					})
				}
				if err := env.Out.JSON(doc); err != nil {
					return err
				}
				if refreshErr != nil {
					return silent(refreshErr)
				}
				return nil
			}

			if refreshErr != nil {
				if res != nil && !res.Report.Clean() {
					env.Out.Println("")
					for _, f := range res.Report.Findings {
						env.Out.Printf("  %s %s\n", env.Out.S(StyleBad, SymbolFail), f)
					}
					env.Out.Println("")
					env.Out.Println(env.Out.Wrap(
						"The golden was not published, so nothing can branch from it. Add a rule for "+
							"each column above and refresh again.", 0))
					return silent(refreshErr)
				}
				return refreshErr
			}

			env.Out.Println("")
			env.Out.Status(env.Out.S(StyleGood, SymbolOK), res.Version,
				fmt.Sprintf("%d rows across %d tables masked in %s",
					res.Rows, res.Tables, res.Duration.Round(time.Second)))
			env.Out.Printf("  Verified %d columns across %d tables, %d rows sampled.\n",
				res.Report.Columns, res.Report.Tables, res.Report.RowsSampled)
			env.Out.Println("  Bring an environment up from it with: af up")
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch context to use, defaulting to the checked out one")
	return cmd
}

func newGoldenListCommand(env *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the goldens that exist",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			goldens, err := o.Goldens(cmd.Context())
			if err != nil {
				return err
			}
			if env.Out.Format == FormatJSON {
				docs := make([]GoldenJSON, 0, len(goldens))
				for _, g := range goldens {
					docs = append(docs, GoldenJSON{
						Version: g.ID, Verified: g.Verified,
						CreatedAt: g.CreatedAt.UTC().Format(time.RFC3339),
						SizeBytes: g.SizeBytes, RulesHash: g.RulesHash,
					})
				}
				return env.Out.JSON(docs)
			}
			if len(goldens) == 0 {
				env.Out.Println("No goldens yet. Make one with 'af golden refresh'.")
				return nil
			}
			rows := make([][]string, 0, len(goldens))
			for _, g := range goldens {
				state := env.Out.S(StyleBad, "unverified")
				if g.Verified {
					state = env.Out.S(StyleGood, "verified")
				}
				rows = append(rows, []string{
					g.ID, state, g.CreatedAt.Local().Format("2006-01-02 15:04"),
					humanBytes(uint64(g.SizeBytes)), g.RulesHash,
				})
			}
			env.Out.Table([]string{"VERSION", "STATE", "CREATED", "SIZE", "RULES"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch context to use, defaulting to the checked out one")
	return cmd
}

func newGoldenGCCommand(env *Env) *cobra.Command {
	var branch string
	var keep int
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Remove old goldens, keeping the newest",
		Long: strings.TrimSpace(`
A golden that something is still branched from is never removed, and that is
reported rather than forced: taking away the copy an environment is running on
breaks the environment rather than tidying it.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			goldens, err := o.Goldens(cmd.Context())
			if err != nil {
				return err
			}
			if len(goldens) <= keep {
				env.Out.Printf("Nothing to remove: %d goldens, keeping %d.\n", len(goldens), keep)
				return nil
			}

			removed, kept := 0, 0
			var pending []string
			for i, g := range goldens {
				if i < keep {
					kept++
					continue
				}
				if err := o.DestroyGolden(cmd.Context(), g.ID); err != nil {
					pending = append(pending, g.ID+": "+err.Error())
					continue
				}
				removed++
			}
			if env.Out.Format == FormatJSON {
				return env.Out.JSON(map[string]any{
					"removed": removed, "kept": kept, "pending": pending,
				})
			}
			env.Out.Printf("Removed %d, kept %d.\n", removed, kept)
			for _, p := range pending {
				env.Out.Printf("  %s %s\n", env.Out.S(StyleWarn, SymbolWarn), p)
			}
			if len(pending) > 0 {
				return aferrors.Coded(aferrors.AFRUN030, "count", fmt.Sprint(len(pending)))
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&keep, "keep", 3, "How many of the newest goldens to keep")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch context to use, defaulting to the checked out one")
	return cmd
}

func newGoldenVerifyCommand(env *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "verify <version>",
		Short: "Re-check a published golden",
		Long: strings.TrimSpace(`
Branches the golden, reads it back with the detectors, and removes the branch
whether or not the check passed.

Worth doing because a golden published under one set of rules is not verified
under another, and because a golden that arrives by import was never checked
here at all.`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			env.Out.Section("Verifying " + args[0])
			report, err := o.VerifyGolden(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if env.Out.Format == FormatJSON {
				doc := VerifyJSON{
					Clean: report.Clean(), Tables: report.Tables, Columns: report.Columns,
					RowsSampled: report.RowsSampled, SampleSize: report.SampleSize,
					Skipped: report.Skipped,
				}
				for _, f := range report.Findings {
					doc.Findings = append(doc.Findings, FindingJSON{
						Table: f.Schema + "." + f.Table, Column: f.Column,
						Detector: f.Detector, Example: f.Example, Rows: f.Rows,
					})
				}
				if err := env.Out.JSON(doc); err != nil {
					return err
				}
				if !report.Clean() {
					return silent(aferrors.Coded(aferrors.AFMSK002,
						"detector", report.Findings[0].Detector,
						"table", report.Findings[0].Schema+"."+report.Findings[0].Table,
						"column", report.Findings[0].Column))
				}
				return nil
			}
			if report.Clean() {
				env.Out.Status(env.Out.S(StyleGood, SymbolOK), "clean",
					fmt.Sprintf("%d columns across %d tables, %d rows sampled",
						report.Columns, report.Tables, report.RowsSampled))
				return nil
			}
			for _, f := range report.Findings {
				env.Out.Printf("  %s %s\n", env.Out.S(StyleBad, SymbolFail), f)
			}
			return aferrors.Coded(aferrors.AFMSK002,
				"detector", report.Findings[0].Detector,
				"table", report.Findings[0].Schema+"."+report.Findings[0].Table,
				"column", report.Findings[0].Column)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch context to use, defaulting to the checked out one")
	return cmd
}
