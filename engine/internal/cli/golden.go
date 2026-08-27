package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/golden"
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
	cmd.AddCommand(newGoldenPullCommand(env))
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

			// What this project publishes, and when it refreshes next. Both
			// are configuration that used to be invisible: a store nobody can
			// list is a store nobody trusts, and a cron expression with no
			// daemon behind it needs to say when it will actually fire.
			published, storeName, storeErr := o.PublishedGoldens(cmd.Context())
			switch {
			case storeErr != nil:
				env.Out.Printf("\n  %s could not be listed: %s\n",
					env.Out.S(StyleWarn, SymbolWarn), storeErr.Error())
			case storeName != "" && len(published) == 0:
				env.Out.Printf("\nNothing published to %s yet.\n", storeName)
			case storeName != "":
				env.Out.Printf("\nPublished to %s, newest first:\n", storeName)
				for _, v := range published {
					env.Out.Printf("  %s  %s  %s\n", v.Name,
						v.Modified.Local().Format("2006-01-02 15:04"),
						humanBytes(uint64(v.Size)))
				}
				env.Out.Println("  Bring one onto this machine with: af golden pull")
			}

			policy, policyErr := o.GoldenPolicy()
			if policyErr == nil && !policy.Schedule.Zero() && len(goldens) > 0 {
				next := policy.Schedule.Next(goldens[0].CreatedAt)
				if !next.IsZero() {
					env.Out.Printf("\nNext scheduled refresh: %s (%s)\n",
						next.In(policy.Schedule.Location()).Format("2006-01-02 15:04 MST"),
						policy.Schedule)
				}
			}
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
How many to keep comes from database.golden.retain in the manifest, so that
every machine and every runner collects the same way. --keep overrides it for
one run.

Two versions are never removed. One is any version an environment is still
branched from: taking away the copy something is running on breaks the
environment rather than tidying it, and that refusal comes from the provider,
which is the only thing that knows. The other is the newest verified golden,
whatever the count says, because a project with nothing left to branch cannot
bring an environment up at all, which is worse than the disk it saved.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			policy, err := o.GoldenPolicy()
			if err != nil {
				return err
			}
			effective := keep
			source := "--keep"
			if !cmd.Flags().Changed("keep") {
				effective, source = policy.Retain, "database.golden.retain"
			}

			goldens, err := o.Goldens(cmd.Context())
			if err != nil {
				return err
			}
			versions := make([]golden.Version, 0, len(goldens))
			for _, g := range goldens {
				versions = append(versions, golden.Version{
					ID: g.ID, CreatedAt: g.CreatedAt, Verified: g.Verified,
				})
			}
			decisions := golden.Sweep(versions, effective)

			removed, kept := 0, 0
			var refused []string
			for _, d := range decisions {
				if !d.Remove {
					kept++
					continue
				}
				if err := o.DestroyGolden(cmd.Context(), d.Version.ID); err != nil {
					// Almost always AF-DB-005: something is still branched from
					// it. Reported with the version so somebody can run af down
					// on the environment holding it.
					refused = append(refused, d.Version.ID+": "+err.Error())
					continue
				}
				removed++
			}

			if env.Out.Format == FormatJSON {
				return env.Out.JSON(map[string]any{
					"removed": removed, "kept": kept, "keep": effective,
					"keep_from": source, "refused": refused,
				})
			}
			env.Out.Printf("Removed %d, kept %d, keeping %d from %s.\n",
				removed, kept, effective, source)
			for _, d := range decisions {
				if !d.Remove {
					env.Out.Printf("  %s %s: %s\n",
						env.Out.S(StyleGood, SymbolOK), d.Version.ID, d.Reason)
				}
			}
			for _, r := range refused {
				env.Out.Printf("  %s %s\n", env.Out.S(StyleWarn, SymbolWarn), r)
			}
			if len(refused) > 0 {
				return aferrors.Coded(aferrors.AFRUN030, "count", fmt.Sprint(len(refused)))
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&keep, "keep", 0,
		"How many of the newest goldens to keep, overriding database.golden.retain")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch context to use, defaulting to the checked out one")
	return cmd
}

func newGoldenPullCommand(env *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "pull [version]",
		Short: "Bring a published golden onto this machine",
		Long: strings.TrimSpace(`
One machine holds the production credential and refreshes; every other machine
pulls what it published and never reads production at all. That is what
database.golden.storage and storage_url are for.

With no version, the newest complete one is taken. A version is complete when
its attestation is in the store: the dump is written first and the attestation
second, so a version with only a dump is a publish that did not finish, and it
is invisible here rather than offered.

A pulled golden is NOT trusted because it came from the store. The verification
scan runs again, here, against the database that actually arrived. A pull that
skipped it would make the store a way to get an unverified database branched.`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			want := ""
			if len(args) == 1 {
				want = args[0]
			}
			env.Out.Section("Pulling a published golden")
			res, pullErr := o.PullGolden(cmd.Context(), want)

			if env.Out.Format == FormatJSON {
				doc := map[string]any{}
				if res != nil {
					doc = map[string]any{
						"version": res.Version, "from": res.From,
						"verified": res.Verified, "bytes": res.Bytes,
					}
				}
				if err := env.Out.JSON(doc); err != nil {
					return err
				}
				if pullErr != nil {
					return silent(pullErr)
				}
				return nil
			}
			if pullErr != nil {
				return pullErr
			}
			env.Out.Println("")
			env.Out.Status(env.Out.S(StyleGood, SymbolOK), res.Version,
				fmt.Sprintf("restored %s from the store and verified it here", res.From))
			env.Out.Printf("  Verified %d columns across %d tables, %d rows sampled.\n",
				res.Report.Columns, res.Report.Tables, res.Report.RowsSampled)
			env.Out.Println("  Bring an environment up from it with: af up")
			return nil
		},
	}
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
