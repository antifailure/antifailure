package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/masking"
)

// MaskPlanJSON is the machine readable plan.
type MaskPlanJSON struct {
	RulesHash string `json:"rules_hash"`
	Runnable  bool   `json:"runnable"`
	// Which database the schema was read from. A plan is about a schema, and
	// two databases here can differ by the migration somebody is writing.
	Source       string           `json:"source,omitempty"`
	Tables       int              `json:"tables"`
	Columns      int              `json:"columns"`
	Rows         int64            `json:"rows_estimated"`
	Assignments  []AssignmentJSON `json:"assignments"`
	Unclassified []AssignmentJSON `json:"unclassified"`
	Problems     []AssignmentJSON `json:"problems"`
}

// AssignmentJSON is one column's decision.
type AssignmentJSON struct {
	Table       string `json:"table"`
	Column      string `json:"column"`
	Type        string `json:"type"`
	Transform   string `json:"transform,omitempty"`
	Link        string `json:"link,omitempty"`
	Why         string `json:"why,omitempty"`
	FromDefault bool   `json:"from_default,omitempty"`
	Problem     string `json:"problem,omitempty"`
}

func assignmentJSON(a masking.Assignment) AssignmentJSON {
	return AssignmentJSON{
		Table: a.Table.String(), Column: a.Column.Name, Type: a.Column.Type,
		Transform: a.Transform, Link: a.Link, Why: a.Why,
		FromDefault: a.FromDefault, Problem: a.Problem,
	}
}

// VerifyJSON is the machine readable verification report.
type VerifyJSON struct {
	Clean       bool          `json:"clean"`
	Tables      int           `json:"tables"`
	Columns     int           `json:"columns"`
	RowsSampled int64         `json:"rows_sampled"`
	SampleSize  int           `json:"sample_size"`
	Findings    []FindingJSON `json:"findings"`
	Skipped     []string      `json:"skipped,omitempty"`
}

// FindingJSON is one value that still looks real.
type FindingJSON struct {
	Table    string `json:"table"`
	Column   string `json:"column"`
	Detector string `json:"detector"`
	Example  string `json:"example"`
	Rows     int    `json:"rows"`
}

func newMaskCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mask",
		Short: "Plan, apply, and check the masking of this environment's data",
		Long: strings.TrimSpace(`
Masking is compiled from the live schema rather than from a list, because a
list of columns goes stale the moment somebody adds one and the failure mode is
silent: the new column holds real addresses and nothing says so.

A column no rule covers is reported rather than left alone. Left alone, for a
column called customer_notes, means the notes ship.`),
	}
	cmd.AddCommand(newMaskPlanCommand(env))
	cmd.AddCommand(newMaskApplyCommand(env))
	cmd.AddCommand(newMaskVerifyCommand(env))
	cmd.AddCommand(newMaskPreviewCommand(env))
	return cmd
}

func newMaskPlanCommand(env *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show what masking would do, column by column",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			res, err := o.MaskPlan(cmd.Context())
			if err != nil {
				return err
			}
			plan := res.Plan

			if env.Out.Format == FormatJSON {
				doc := MaskPlanJSON{
					RulesHash: res.RulesHash, Runnable: plan.Runnable(), Source: res.Source,
					Tables: len(plan.Tables), Columns: plan.Columns(), Rows: plan.Rows(),
				}
				for _, t := range plan.Tables {
					for _, a := range t.Columns {
						doc.Assignments = append(doc.Assignments, assignmentJSON(a))
					}
				}
				for _, a := range plan.Unclassified {
					doc.Unclassified = append(doc.Unclassified, assignmentJSON(a))
				}
				for _, a := range plan.Problems {
					doc.Problems = append(doc.Problems, assignmentJSON(a))
				}
				return env.Out.JSON(doc)
			}

			env.Out.Section("Masking plan")
			// Which database this describes. A plan read from the source and a
			// plan read from a branch can differ by exactly the migration
			// somebody is working on, and a plan that does not say which it is
			// is a plan that can be trusted for the wrong schema.
			if res.Source != "" {
				env.Out.Printf("  Read from %s.\n", res.Source)
			}
			env.Out.Printf("  %d columns across %d tables, about %d rows.\n\n",
				plan.Columns(), len(plan.Tables), plan.Rows())
			env.Out.Raw(plan.Explain())

			if len(plan.Unclassified) > 0 {
				env.Out.Section("Columns no rule covers")
				// Which of these actually shipped, per row.
				//
				// This said "so they ship as they are" about the whole list, and
				// most of the list is emptied by the default rather than shipped.
				// The two are genuinely different outcomes and a person reading
				// their own schema could not tell which had happened to which
				// column, which is the one thing the list is for.
				env.Out.Println(env.Out.Wrap(
					"Nothing decided what happens to these. Each one says what the default did "+
						"with it. Add a rule for each, or decide that what happened is fine.", 0))
				env.Out.Println("")
				for _, a := range plan.Unclassified {
					did := "COPIED UNCHANGED"
					if a.Transform != "" {
						did = "emptied by default (" + a.Transform + ")"
					}
					env.Out.Printf("  %s.%s  (%s)  %s\n",
						a.Table, a.Column.Name, a.Column.Type, did)
				}
				env.Out.Println("")
			}
			if len(plan.Problems) > 0 {
				env.Out.Section("Problems")
				for _, a := range plan.Problems {
					env.Out.Printf("  %s %s.%s: %s\n",
						env.Out.S(StyleBad, SymbolFail), a.Table, a.Column.Name, a.Problem)
				}
				env.Out.Println("")
				env.Out.Println(env.Out.Wrap(
					"This plan will not be run. A masking run that fails partway leaves a table "+
						"neither real nor safe, with nothing to say which rows are which.", 0))
				return aferrors.Coded(aferrors.AFMSK010,
					"detail", masking.DescribeProblems(plan.Problems))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to plan against, defaulting to the checked out one")
	return cmd
}

func newMaskApplyCommand(env *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Rewrite this environment's data according to the plan",
		Long: strings.TrimSpace(`
Applies the plan to the branch this environment is using.

This is irreversible: once a column is overwritten the original is gone. It is
safe here because the branch is a copy, and it is exactly how a golden is
produced, so trying it on a branch first is the way to iterate on rules.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			res, err := o.MaskApply(cmd.Context())
			if err != nil {
				return err
			}
			if env.Out.Format == FormatJSON {
				return env.Out.JSON(map[string]any{
					"tables": res.Tables, "rows": res.Rows,
					"duration": res.Duration.Round(time.Millisecond).String(),
					"resumed":  res.Resumed,
				})
			}
			env.Out.Status(env.Out.S(StyleGood, SymbolOK), "masked",
				fmt.Sprintf("%d rows across %d tables in %s",
					res.Rows, res.Tables, res.Duration.Round(time.Second)))
			env.Out.Hint("Check it with", "af mask verify")
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to mask, defaulting to the checked out one")
	return cmd
}

func newMaskVerifyCommand(env *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Read the data back and report anything that still looks real",
		Long: strings.TrimSpace(`
Reads a sample of every text column and runs the same detectors that would find
the data if it leaked.

Masking that is not checked is masking somebody believes in. A rule that missed
a column, a transform that failed on a null, a table added last week: each
produces data that looks masked and is not, and none of them announces itself.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			report, err := o.MaskVerify(cmd.Context())
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
				for _, s := range report.Skipped {
					env.Out.Printf("  %s could not be read: %s\n", env.Out.S(StyleWarn, SymbolWarn), s)
				}
				return nil
			}

			env.Out.Section("Data that still looks real")
			for _, f := range report.Findings {
				env.Out.Printf("  %s %s\n", env.Out.S(StyleBad, SymbolFail), f)
			}
			env.Out.Println("")
			env.Out.Println(env.Out.Wrap(
				"A golden in this state cannot be branched. Add a rule for each column above "+
					"with 'af mask plan' to see what is covered.", 0))
			return aferrors.Coded(aferrors.AFMSK002,
				"detector", report.Findings[0].Detector,
				"table", report.Findings[0].Schema+"."+report.Findings[0].Table,
				"column", report.Findings[0].Column)
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to check, defaulting to the checked out one")
	return cmd
}

func newMaskPreviewCommand(env *Env) *cobra.Command {
	var branch, table string
	var rows int
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Show what a few rows would look like after masking",
		Long: strings.TrimSpace(`
Reads a few rows, transforms them in memory, and writes nothing.

Somebody iterating on rules has to see the output before committing to it, and
the alternative, applying and then looking, is irreversible on a branch they may
want to keep.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(env, branch, false)
			if err != nil {
				return err
			}
			preview, err := o.MaskPreview(cmd.Context(), table, rows)
			if err != nil {
				return err
			}
			if env.Out.Format == FormatJSON {
				return env.Out.JSON(preview)
			}
			if len(preview) == 0 {
				env.Out.Println("Nothing is being masked. Run 'af mask plan' to see why.")
				return nil
			}
			for i, row := range preview {
				env.Out.Printf("\n  Row %d\n", i+1)
				for _, cell := range row {
					env.Out.Printf("    %-20s %s\n", cell.Column, env.Out.S(StyleDim, cell.Before))
					env.Out.Printf("    %-20s %s\n", "", env.Out.S(StyleGood, cell.After))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&table, "table", "", "Preview one table, defaulting to the first being masked")
	cmd.Flags().IntVar(&rows, "rows", 3, "How many rows to show")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to read, defaulting to the checked out one")
	return cmd
}
