package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/verify"
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
	// CopiedUnchanged counts the unclassified columns the default could not
	// empty, which ship holding exactly what production holds. It is the
	// number that used to be discoverable only by reading the unclassified
	// list to the end and counting the ones that did not say emptied.
	CopiedUnchanged int              `json:"copied_unchanged"`
	Problems        []AssignmentJSON `json:"problems"`
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
	// Unread names the columns the scanner could not read, with the type
	// that made them unreadable, so clean is read as a claim about the
	// columns that were opened and not about these.
	Unread []UnreadJSON `json:"unread,omitempty"`
	// Unruled names the columns masking copied unchanged because no rule
	// covered them, and UnruledCount is how many. The count is a field of
	// its own so a script can read it without counting a list.
	Unruled      []string `json:"unruled,omitempty"`
	UnruledCount int      `json:"unruled_count"`
}

// UnreadJSON is one column the scanner could not read.
type UnreadJSON struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	Type   string `json:"type"`
	Reason string `json:"reason"`
	Ruled  bool   `json:"ruled"`
}

// verifyJSON renders a report for every command that carries one, so the
// four of them cannot drift into four different documents.
func verifyJSON(report verify.Report) VerifyJSON {
	doc := VerifyJSON{
		Clean: report.Clean(), Tables: report.Tables, Columns: report.Columns,
		RowsSampled: report.RowsSampled, SampleSize: report.SampleSize,
		Skipped: report.Skipped, Unruled: report.Unruled, UnruledCount: len(report.Unruled),
	}
	for _, f := range report.Findings {
		doc.Findings = append(doc.Findings, FindingJSON{
			Table: f.Schema + "." + f.Table, Column: f.Column,
			Detector: f.Detector, Example: f.Example, Rows: f.Rows,
		})
	}
	for _, u := range report.Unread {
		doc.Unread = append(doc.Unread, UnreadJSON{
			Table: u.Schema + "." + u.Table, Column: u.Column, Type: u.Type,
			Reason: u.Reason, Ruled: u.Ruled,
		})
	}
	return doc
}

// verifyFailure is the error for a report that is not clean.
//
// A finding first, because a column the scan read and disliked is the more
// specific answer, and the unread finding gets its own code because the fix
// it asks for is a rule rather than a detector. Then a skip. This used to
// index Findings[0] on any unclean report, and a report whose only problem
// is a skipped column has no findings, so the strict Clean() that counts a
// skip turned a silent pass into an index out of range.
func verifyFailure(report verify.Report) error {
	if len(report.Findings) > 0 {
		f := report.Findings[0]
		if f.Detector == verify.DetectorUnreadSensitive {
			return aferrors.Coded(aferrors.AFMSK013,
				"table", f.Schema+"."+f.Table, "column", f.Column, "type", f.Example)
		}
		return aferrors.Coded(aferrors.AFMSK002,
			"detector", f.Detector, "table", f.Schema+"."+f.Table, "column", f.Column)
	}
	if len(report.Skipped) > 0 {
		where, detail, _ := strings.Cut(report.Skipped[0], ": ")
		table, column := where, ""
		if i := strings.LastIndex(where, "."); i >= 0 {
			table, column = where[:i], where[i+1:]
		}
		return aferrors.Coded(aferrors.AFMSK011, "table", table, "column", column, "detail", detail)
	}
	return nil
}

// printVerifyCoverage says what the scan did not read and what masking left
// alone, after the verdict line of every command that verifies.
//
// Printed on a clean result as well as a failed one, which is the point: a
// clean line followed by nothing used to be the whole story, and the story
// had a bytea column in it that the scan never opened.
func printVerifyCoverage(env *Env, report verify.Report) {
	for _, s := range report.Skipped {
		env.Out.Printf("  %s could not be read: %s\n", env.Out.S(StyleWarn, SymbolWarn), s)
	}
	for _, u := range report.Unread {
		ruled := "no rule, copied unchanged"
		if u.Ruled {
			ruled = "masked by its rule"
		}
		env.Out.Printf("  %s %s (%s)\n", env.Out.S(StyleWarn, SymbolWarn), u, ruled)
	}
	env.Out.Printf("  %d columns copied unchanged with no rule.\n", len(report.Unruled))
}

// printVerifyRefusal shows every reason a scan will not let a golden through.
//
// One printer for the three commands that refuse, because they had three
// copies of it and one of them was missing a half. `af golden refresh` printed
// its findings and stopped, then told the reader to add a rule for each column
// above. On a report whose only problem was a column the scan COULD NOT READ
// there were no findings, so the refusal printed nothing at all above an
// instruction pointing at a list that was never there, and the error naming
// the column went out through silent() with its message discarded. The golden
// was correctly refused and the reason reached nobody, which is the half of
// "say no" this product exists to get right.
func printVerifyRefusal(env *Env, report verify.Report) {
	for _, f := range report.Findings {
		env.Out.Printf("  %s %s\n", env.Out.S(StyleBad, SymbolFail), f)
	}
	printVerifyCoverage(env, report)
}

// refusalAdvice is what to do about a refusal, which depends on which kind it
// is.
//
// "Add a rule" is the wrong instruction for a column nobody could read: no
// rule makes an unreadable column readable, and a reader who follows it writes
// a rule, refreshes, and is refused again for the same reason.
func refusalAdvice(report verify.Report) string {
	const preamble = "The golden was not published, so nothing can branch from it. "
	if len(report.Findings) > 0 {
		return preamble + "Add a rule for each column above and refresh again."
	}
	return preamble + "A column the scan could not read is not a column that passed, so " +
		"grant the verifier access to each column above, or remove it, and refresh again."
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
	cmd.AddCommand(newMaskInitCommand(env))
	cmd.AddCommand(newMaskPlanCommand(env))
	cmd.AddCommand(newMaskApplyCommand(env))
	cmd.AddCommand(newMaskVerifyCommand(env))
	cmd.AddCommand(newMaskPreviewCommand(env))
	cmd.AddCommand(newMaskCrossStoreCommand(env))
	return cmd
}

// maskingWritten is what af mask init did, for the two commands that report it.
type maskingWritten struct {
	Path    string `json:"path"`
	Source  string `json:"source"`
	Tables  int    `json:"tables"`
	Columns int    `json:"columns"`
	Rules   int    `json:"rules"`
}

func newMaskInitCommand(env *Env) *cobra.Command {
	var branch string
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Read the schema and write masking.yaml with a rule for every column",
		Long: strings.TrimSpace(`
Reads the schema of the database source, or of this environment's branch when
one is up, decides every column the way the built in rules would, and writes
the result to masking.yaml as one explicit rule per column.

The file it writes leaves the plan with nothing to ask. A column a built in
rule recognises gets that rule restated with its reason. A column nothing
recognises gets a rule that empties it, with a reason saying it was
unrecognised and is emptied until somebody says otherwise. Numbers, times and
identifiers get no rule, because nothing is done to them.

It refuses to replace a file that is already there unless --force is passed,
because the rules somebody edited are the most valuable thing in it.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			written, err := writeMaskingRules(cmd.Context(), env, branch, force)
			if err != nil {
				return err
			}
			if env.Out.Format == FormatJSON {
				return env.Out.JSON(written)
			}
			env.Out.Status(env.Out.S(StyleGood, SymbolOK), "masking rules",
				fmt.Sprintf("written from %d tables, %d columns", written.Tables, written.Columns))
			env.Out.Printf("  %s, read from %s\n", short(env.WorkDir, written.Path), written.Source)
			env.Out.Hint("Read it, then see what it does column by column with", "af mask plan")
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch whose environment to read, defaulting to the checked out one")
	cmd.Flags().BoolVar(&force, "force", false, "Replace a masking file that is already there")
	return cmd
}

// writeMaskingRules is the work behind af mask init, shared with af init.
func writeMaskingRules(ctx context.Context, env *Env, branch string, force bool) (*maskingWritten, error) {
	o, err := orchestrator(env, branch, false)
	if err != nil {
		return nil, err
	}
	path := o.MaskingRulesPath()
	if _, statErr := os.Stat(path); statErr == nil && !force {
		return nil, aferrors.Coded(aferrors.AFMSK012, "path", path)
	}
	res, err := o.MaskDraft(ctx)
	if err != nil {
		return nil, err
	}
	rules := masking.DraftRules(res.Tables, res.Assignments)
	columns := 0
	for _, t := range res.Tables {
		columns += len(t.Columns)
	}
	body := masking.RulesFile(rules, res.Source, len(res.Tables), columns)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mask init: create %s: %w", filepath.Dir(path), err)
	}
	if err := writeAtomic(path, []byte(body), 0o644); err != nil {
		return nil, err
	}
	return &maskingWritten{
		Path: path, Source: res.Source, Tables: len(res.Tables), Columns: columns, Rules: len(rules),
	}, nil
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
					CopiedUnchanged: len(plan.CopiedUnchanged()),
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
			env.Out.Printf("  %d columns across %d tables, about %d rows.\n",
				plan.Columns(), len(plan.Tables), plan.Rows())
			// The count that matters, at the top, beside the count that
			// reassures. It used to be discoverable only by reading the
			// unclassified list at the very end and counting the rows that
			// did not say emptied, which on this repository was 145 lines
			// after several hundred lines of assignments.
			env.Out.Printf("  %d columns have no rule, and %d of those are copied unchanged.\n\n",
				len(plan.Unclassified), len(plan.CopiedUnchanged()))
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
					"duration":         res.Duration.Round(time.Millisecond).String(),
					"resumed":          res.Resumed,
					"copied_unchanged": len(res.CopiedUnchanged),
					"unruled":          res.CopiedUnchanged,
				})
			}
			env.Out.Status(env.Out.S(StyleGood, SymbolOK), "masked",
				fmt.Sprintf("%d rows across %d tables in %s",
					res.Rows, res.Tables, res.Duration.Round(time.Second)))
			env.Out.Printf("  %d columns copied unchanged with no rule.\n", len(res.CopiedUnchanged))
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
Reads a sample of every column it can read as text and runs the same detectors
that would find the data if it leaked. Strings, JSON, arrays and enums are read
through their text form; a bytea column is decoded as UTF-8 where it decodes.
A column of a type the scanner cannot read is listed as not readable rather
than passed over, and when no masking rule covers such a column and its name
says it holds a secret, the check fails.

The count of columns masking copied unchanged because no rule covered them is
printed beside the verdict, whichever way the verdict went.

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
				if err := env.Out.JSON(verifyJSON(report)); err != nil {
					return err
				}
				if !report.Clean() {
					return silent(verifyFailure(report))
				}
				return nil
			}

			if report.Clean() {
				env.Out.Status(env.Out.S(StyleGood, SymbolOK), "clean",
					fmt.Sprintf("%d columns across %d tables, %d rows sampled",
						report.Columns, report.Tables, report.RowsSampled))
				printVerifyCoverage(env, report)
				return nil
			}

			env.Out.Section("What this branch cannot be trusted about")
			printVerifyRefusal(env, report)
			env.Out.Println("")
			env.Out.Println(env.Out.Wrap(
				"A golden in this state cannot be branched. Add a rule for each column above "+
					"with 'af mask plan' to see what is covered.", 0))
			return verifyFailure(report)
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
