package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

// Masking is a privacy boundary, and these two tools sit on opposite sides of
// it in two different ways.
//
// The first way is risk. inspect_data_masking reads and writes nothing;
// apply_data_masking overwrites every masked column of every masked table and
// the original is then gone. They are separate tools with separate names for
// that reason alone: a caller must never arrive at the second believing it is
// the first, and a single tool with a mode argument is exactly how that
// happens.
//
// The second way is what may be REPORTED. Everything under masking is about
// values that are real until the moment they are not, so a tool that showed a
// preview of the values it is deciding about would leak precisely the data the
// feature exists to remove, to a model, into a transcript. So none of these
// returns a value. What they return is the SHAPE of the change: which column,
// which transform, whether the value changed at all, how long it was before
// and after, and which detector still recognises something. That is what
// somebody iterating on a rule actually needs, because the failure being
// hunted is a rule that did nothing, and "changed: false" says that without
// showing what it failed to change.

// maskingReaders are the three read only masking questions.
//
// Grouped rather than passed as three arguments because they are one tool's
// dependencies, and because a constructor with three bare function parameters
// is one call site away from having two of them swapped.
type maskingReaders struct {
	// Plan says what masking would do, column by column, from the live schema.
	Plan func(ctx context.Context) (*env.PlanResult, error)
	// Sample transforms a few rows in memory and writes nothing.
	Sample func(ctx context.Context, table string, rows int) ([][]env.PreviewRow, error)
	// Verify reads the data back and reports anything that still looks real.
	Verify func(ctx context.Context) (verify.Report, error)
}

// maskingApplier rewrites the environment's data. There is one of these and it
// is reached from one tool.
type maskingApplier func(ctx context.Context) (masking.Result, error)

// The bounds on the masking documents. Each list grows with the schema, which
// in a mature application is hundreds of tables and thousands of columns.
const (
	maxMaskedTablesReported  = 40
	maxMaskedColumnsPerTable = 25
	maxUnclassifiedColumns   = 40
	maxMaskingFindings       = 40
	maxSampledRows           = 10
)

// newInspectMaskingTool builds inspect_data_masking.
func newInspectMaskingTool(p *Project, readers maskingReaders) *Tool {
	return &Tool{
		Name:  "inspect_data_masking",
		Title: "What masking does to this data",
		// Read only in the strongest sense available: the plan reads a
		// catalog, the sample transforms rows in memory, and the scan reads a
		// sample back. None of the three writes anything.
		ReadOnly: true,
		Description: "Ask what masking does to this environment's data, without changing " +
			"any of it. Three questions, chosen with the question argument. " +
			"plan says what masking WOULD do, column by column, compiled from the live " +
			"schema rather than from a checked in list, and names every column no rule " +
			"covers, which is the list somebody has to answer: left alone, a column " +
			"called customer_notes means the notes ship. " +
			"sample transforms a few rows in memory to show whether the rules actually " +
			"fire, which is what you need while writing one. " +
			"verify reads the data back and runs the same detectors that would find the " +
			"data if it leaked, which catches a rule that missed a column, a transform " +
			"that failed on a null, and a table added last week. " +
			"NO VALUE IS EVER RETURNED BY ANY OF THE THREE. Masking is a privacy " +
			"boundary, and a preview that showed the values it is deciding about would " +
			"leak exactly the data being removed. What comes back is the shape of the " +
			"change: the column, the transform, whether the value changed at all, its " +
			"length before and after, and which detector still recognises something. " +
			"That is enough to find the failure this is for, which is a rule that did " +
			"nothing. Read the values yourself with af mask preview. " +
			"This tool cannot change data. To actually rewrite it, which is " +
			"irreversible, the separate tool is apply_data_masking.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id", "question"},
			Properties: map[string]*Schema{
				"project_id": projectIDSchema(),
				"question": {
					Type: "string", MaxLength: 10, MinLength: 4,
					Enum: []string{"plan", "sample", "verify"},
					Description: "Required. plan is what masking would do to every column, " +
						"and it works before any environment exists because it can read the " +
						"schema from the configured source. sample shows whether the rules " +
						"fire on real rows, reported as shape only, and needs a running " +
						"environment. verify reads the data back and reports anything that " +
						"still looks real, which is the only one of the three that can tell " +
						"you masking did not work.",
				},
				"table": {
					Type: "string", MaxLength: 128, MinLength: 1,
					Pattern: `[A-Za-z0-9_.$-]+`,
					Description: "Optional, and only used by sample. Which table to sample, " +
						"either bare or schema qualified, defaulting to the first table " +
						"being masked. Use plan first to see which tables those are.",
				},
				"rows": {
					Type: "integer", HasMin: true, Minimum: 1, HasMax: true, Maximum: maxSampledRows,
					Description: "Optional, and only used by sample. How many rows to " +
						"transform, defaulting to 3. More rows do not show more values, " +
						"because no value is returned; they show whether a transform fires " +
						"consistently or only on some rows.",
				},
			},
		},
		Handler: func(ctx context.Context, _ *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			switch question, _ := args["question"].(string); question {
			case "plan":
				return maskingPlan(ctx, readers)
			case "sample":
				return maskingSample(ctx, readers, args)
			case "verify":
				return maskingVerification(ctx, readers)
			default:
				// Unreachable: the enum refuses anything else before a handler
				// runs. Stated rather than left as a silent nil, because a
				// handler that can return nothing is a handler that will one
				// day return nothing.
				return nil, fieldFault(FaultInvalidArgument, "question",
					"This field must be one of: plan, sample, verify.")
			}
		},
	}
}

// -----------------------------------------------------------------------
// the plan
// -----------------------------------------------------------------------

type maskingPlanDoc struct {
	Kind    string  `json:"kind"`
	Verdict Verdict `json:"verdict"`
	Summary string  `json:"summary"`
	// Source names the database the schema was read from. A plan is about a
	// schema, and which schema is not obvious when there are two it could
	// have been.
	Source string `json:"schema_read_from,omitempty"`
	// RulesHash identifies the rule set that produced this plan, so a golden
	// records what it was masked with and a changed rule set is visible as a
	// different plan rather than as the same one behaving differently.
	RulesHash string `json:"rules_hash,omitempty"`
	// Runnable is false when the plan has problems. A plan with any is refused
	// rather than partly run, because a half masked table is neither real nor
	// safe and nothing says which rows are which.
	Runnable bool `json:"runnable"`

	Tables       []maskedTableDoc `json:"tables"`
	TablesTotal  int              `json:"tables_total"`
	TablesShown  int              `json:"tables_shown"`
	ColumnsTotal int              `json:"columns_total"`
	RowsEstimate int64            `json:"rows_estimated"`

	// Unclassified are columns no rule named. They are not a failure on their
	// own; they are the list somebody has to answer.
	Unclassified      []unmaskedColumnDoc `json:"unclassified_columns,omitempty"`
	UnclassifiedTotal int                 `json:"unclassified_total"`
	Problems          []unmaskedColumnDoc `json:"problems,omitempty"`
	ProblemsTotal     int                 `json:"problems_total"`
	Metrics           []Metric            `json:"metrics,omitempty"`
	Note              string              `json:"note,omitempty"`
	EvidenceNote      string              `json:"evidence_note,omitempty"`
}

type maskedTableDoc struct {
	Table   string            `json:"table"`
	Rows    int64             `json:"rows_estimated"`
	Columns []maskedColumnDoc `json:"columns"`
	Total   int               `json:"columns_total"`
	// Chunked reports whether the rewrite is done in resumable chunks. A table
	// with no primary key is masked in one statement instead, because without
	// a key there is no stable order to resume from.
	Chunked bool   `json:"chunked"`
	Skipped string `json:"skipped,omitempty"`
}

type maskedColumnDoc struct {
	Column    string `json:"column"`
	Type      string `json:"type,omitempty"`
	Transform string `json:"transform"`
	// FromDefault reports that a built in rule decided this, rather than one
	// somebody wrote. Reported separately so it is visible what was decided
	// for you.
	FromDefault bool   `json:"decided_by_a_built_in_rule"`
	Why         string `json:"why,omitempty"`
}

type unmaskedColumnDoc struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	Type   string `json:"type,omitempty"`
	// Because is why this column is on this list: the rule that would have
	// applied and could not, or the fact that no rule named it at all.
	Because string `json:"because,omitempty"`
}

func maskingPlan(ctx context.Context, readers maskingReaders) (any, *Fault) {
	res, err := readers.Plan(ctx)
	if err != nil {
		return nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The masking plan could not be built, so this says nothing about what " +
				"masking would do. A plan needs a schema to read, which is this " +
				"environment's branch when one is up and the configured source database " +
				"when one is not. The server log says which failed.",
			Retryable: true,
			wrapped:   err,
		}
	}

	plan := res.Plan
	out := &maskingPlanDoc{
		Kind: "masking_plan", Source: safeProse(res.Source, 200),
		RulesHash: neutralize(res.RulesHash, 128), Runnable: plan.Runnable(),
		Tables: []maskedTableDoc{}, TablesTotal: len(plan.Tables),
		ColumnsTotal: plan.Columns(), RowsEstimate: plan.Rows(),
		UnclassifiedTotal: len(plan.Unclassified), ProblemsTotal: len(plan.Problems),
	}

	for i, tp := range plan.Tables {
		if i >= maxMaskedTablesReported {
			out.Note = fmt.Sprintf(
				"%d tables are masked and the first %d are shown. The counts above cover "+
					"all of them; run af mask plan for the rest.",
				len(plan.Tables), maxMaskedTablesReported)
			break
		}
		table, _ := safeIdentifier(tp.Table.String())
		doc := maskedTableDoc{
			Table: table, Rows: tp.Rows(), Total: len(tp.Columns),
			Chunked: tp.ChunkSize > 0, Skipped: safeProse(tp.Skipped, 200),
			Columns: []maskedColumnDoc{},
		}
		for j, a := range tp.Columns {
			if j >= maxMaskedColumnsPerTable {
				break
			}
			column, _ := safeIdentifier(a.Column.Name)
			doc.Columns = append(doc.Columns, maskedColumnDoc{
				Column: column, Type: neutralize(a.Column.Type, 64),
				Transform: neutralize(a.Transform, 64), FromDefault: a.FromDefault,
				Why: safeProse(a.Why, 200),
			})
		}
		out.Tables = append(out.Tables, doc)
	}
	out.TablesShown = len(out.Tables)

	out.Unclassified = describeUnmasked(plan.Unclassified, maxUnclassifiedColumns)
	out.Problems = describeUnmasked(plan.Problems, maxUnclassifiedColumns)

	// A plan that will not run is not a masking failure found, it is a plan
	// that cannot be carried out, so it is INCONCLUSIVE rather than FAIL.
	// Unclassified columns are a question rather than a verdict, which is why
	// they do not decide this either.
	if plan.Runnable() {
		out.Verdict = VerdictPass
	} else {
		out.Verdict = VerdictInconclusive
	}

	zero := 0.0
	out.Metrics = []Metric{
		{Name: "columns_masked", Value: float64(plan.Columns()), Unit: "columns"},
		{Name: "tables_masked", Value: float64(len(plan.Tables)), Unit: "tables"},
		{Name: "rows_to_rewrite", Value: float64(plan.Rows()), Unit: "rows"},
		{
			Name: "columns_no_rule_covers", Value: float64(len(plan.Unclassified)),
			Unit: "columns",
		},
		{
			Name: "columns_that_cannot_be_masked", Value: float64(len(plan.Problems)),
			Unit: "columns", Threshold: &zero, Breached: len(plan.Problems) > 0,
		},
	}
	out.Summary = maskingPlanSummary(out)
	out.EvidenceNote = "Run af mask plan for the same plan without these bounds. No column " +
		"value appears in either."
	return out, nil
}

func describeUnmasked(in []masking.Assignment, limit int) []unmaskedColumnDoc {
	out := make([]unmaskedColumnDoc, 0, limit)
	for i, a := range in {
		if i >= limit {
			break
		}
		table, _ := safeIdentifier(a.Table.String())
		column, _ := safeIdentifier(a.Column.Name)
		because := a.Problem
		if because == "" {
			because = a.Why
		}
		out = append(out, unmaskedColumnDoc{
			Table: table, Column: column, Type: neutralize(a.Column.Type, 64),
			Because: safeProse(because, 200),
		})
	}
	return out
}

func maskingPlanSummary(doc *maskingPlanDoc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Masking would rewrite %d %s across %d %s, about %d %s in total. ",
		doc.ColumnsTotal, plural(doc.ColumnsTotal, "column", "columns"),
		doc.TablesTotal, plural(doc.TablesTotal, "table", "tables"),
		doc.RowsEstimate, plural(int(min64(doc.RowsEstimate, 2)), "row", "rows"))

	if doc.UnclassifiedTotal > 0 {
		fmt.Fprintf(&b,
			"%d %s covered by no rule and %s emptied by the fail closed default rather "+
				"than by a decision anybody made. That is the list to answer: a column "+
				"nobody looked at is a question, not an answer. ",
			doc.UnclassifiedTotal,
			plural(doc.UnclassifiedTotal, "column is", "columns are"),
			plural(doc.UnclassifiedTotal, "is", "are"))
	}
	if !doc.Runnable {
		fmt.Fprintf(&b,
			"This plan will NOT run: %d %s cannot be masked as assigned, and a plan with "+
				"any is refused rather than partly applied, because a half masked table is "+
				"neither real nor safe. ", doc.ProblemsTotal,
			plural(doc.ProblemsTotal, "column", "columns"))
	}
	fmt.Fprintf(&b, "The schema was read from %s. No column value is reported here.",
		orUnnamedSource(doc.Source))
	return b.String()
}

func orUnnamedSource(s string) string {
	if s == "" {
		return "an unnamed database"
	}
	return s
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// -----------------------------------------------------------------------
// the sample
// -----------------------------------------------------------------------

type maskingSampleDoc struct {
	Kind    string  `json:"kind"`
	Verdict Verdict `json:"verdict"`
	Summary string  `json:"summary"`
	Table   string  `json:"table,omitempty"`
	Rows    int     `json:"rows_sampled"`
	// ValuesWithheld is always true and is stated rather than implied, so that
	// a caller looking for the before and after values learns why they are not
	// here instead of concluding the transform produced nothing.
	ValuesWithheld bool            `json:"values_withheld"`
	Columns        []sampledColumn `json:"columns"`
	Metrics        []Metric        `json:"metrics,omitempty"`
	EvidenceNote   string          `json:"evidence_note,omitempty"`
}

// sampledColumn is one column across the sampled rows, as shape only.
//
// Unchanged is the field this whole tool exists to produce. A rule that names
// a column and then leaves its value exactly as it was is the failure somebody
// is hunting while they iterate, and it is invisible in a plan, which says
// what was ASSIGNED rather than what happened.
type sampledColumn struct {
	Column string `json:"column"`
	// Changed and Unchanged count the sampled rows in which the value did and
	// did not differ after the transform.
	Changed   int `json:"rows_changed"`
	Unchanged int `json:"rows_unchanged"`
	// EmptiedFromNonEmpty counts rows where a value that was there became
	// empty, which is what a transform failing on a type looks like.
	EmptiedFromNonEmpty int `json:"rows_emptied"`
	// WereEmptyAlready counts rows where there was nothing to mask, so an
	// unchanged value there is not evidence of anything.
	WereEmptyAlready int `json:"rows_already_empty"`
	// BeforeBytes and AfterBytes are the total lengths across the sampled
	// rows. A length is shape rather than content, and it is what distinguishes
	// a transform that produced a plausible replacement from one that produced
	// an empty string.
	BeforeBytes int `json:"total_bytes_before"`
	AfterBytes  int `json:"total_bytes_after"`
}

func maskingSample(ctx context.Context, readers maskingReaders, args map[string]any) (any, *Fault) {
	table, _ := args["table"].(string)
	rows := 3
	if raw, present := args["rows"]; present {
		if n, err := toInt(raw); err == nil {
			rows = n
		}
	}

	sampled, err := readers.Sample(ctx, table, rows)
	if err != nil {
		return nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The rows could not be sampled, so this says nothing about whether the " +
				"rules fire. Sampling reads this environment's branch, so it needs one to " +
				"be running; bring one up with af up, or ask the plan question instead, " +
				"which needs no environment. The server log says what failed.",
			Retryable: true,
			wrapped:   err,
		}
	}

	out := &maskingSampleDoc{
		Kind: "masking_sample", Rows: len(sampled), ValuesWithheld: true,
		Table: neutralize(table, 128), Columns: []sampledColumn{},
	}
	if len(sampled) == 0 {
		// No rows is a real state and not a failure: an empty table, or a
		// plan that masks nothing. It is not evidence that the rules work, so
		// it is INCONCLUSIVE rather than a pass.
		out.Verdict = VerdictInconclusive
		out.Summary = "No rows were sampled, so this says nothing about whether the rules " +
			"fire. Either the table is empty, or no table is being masked at all; ask the " +
			"plan question to see which."
		return out, nil
	}

	// Accumulated per column across the rows, in the order the first row
	// presented them, so the output is stable between calls on one schema.
	order := make([]string, 0, 16)
	byColumn := map[string]*sampledColumn{}
	for _, row := range sampled {
		for _, cell := range row {
			name, _ := safeIdentifier(cell.Column)
			col, seen := byColumn[name]
			if !seen {
				col = &sampledColumn{Column: name}
				byColumn[name] = col
				order = append(order, name)
			}
			// The lengths and the comparison, never the values. Nothing below
			// this line writes cell.Before or cell.After anywhere.
			col.BeforeBytes += len(cell.Before)
			col.AfterBytes += len(cell.After)
			switch {
			case cell.Before == "":
				col.WereEmptyAlready++
			case cell.After == cell.Before:
				col.Unchanged++
			case cell.After == "":
				col.EmptiedFromNonEmpty++
				col.Changed++
			default:
				col.Changed++
			}
		}
	}
	for _, name := range order {
		out.Columns = append(out.Columns, *byColumn[name])
	}

	var unchanged, emptied int
	for _, c := range out.Columns {
		if c.Unchanged > 0 {
			unchanged++
		}
		if c.EmptiedFromNonEmpty > 0 {
			emptied++
		}
	}
	zero := 0.0
	out.Metrics = []Metric{
		{
			Name: "columns_a_rule_did_not_change", Value: float64(unchanged), Unit: "columns",
			Threshold: &zero, Breached: unchanged > 0,
		},
		{Name: "columns_emptied", Value: float64(emptied), Unit: "columns"},
		{Name: "columns_sampled", Value: float64(len(out.Columns)), Unit: "columns"},
		{Name: "rows_sampled", Value: float64(len(sampled)), Unit: "rows"},
	}

	// A column a rule named and did not change is the failure this question
	// exists to find, so it decides the verdict. Nothing here is a claim about
	// the whole table: it is a claim about the rows sampled, which the summary
	// says.
	if unchanged > 0 {
		out.Verdict = VerdictFail
	} else {
		out.Verdict = VerdictPass
	}
	out.Summary = maskingSampleSummary(out, unchanged, emptied)
	out.EvidenceNote = "Run af mask preview to see the values themselves, which this result " +
		"withholds because they are real until masking runs."
	return out, nil
}

func maskingSampleSummary(doc *maskingSampleDoc, unchanged, emptied int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Transformed %d %s of %d %s in memory and wrote nothing. ",
		len(doc.Columns), plural(len(doc.Columns), "column", "columns"),
		doc.Rows, plural(doc.Rows, "row", "rows"))

	switch {
	case unchanged > 0:
		fmt.Fprintf(&b,
			"%d %s the same value after masking as before it, which means the rule named "+
				"the column and then did nothing to it. That is the failure this question "+
				"exists to find. ", unchanged,
			plural(unchanged, "column kept", "columns kept"))
	default:
		b.WriteString("Every sampled column changed, so the rules fire on these rows. ")
	}
	if emptied > 0 {
		fmt.Fprintf(&b,
			"%d %s emptied a value that was there, which is either what the rule asks for "+
				"or a transform failing on the column's type. ",
			emptied, plural(emptied, "column", "columns"))
	}
	b.WriteString("This covers the rows sampled and not the table. No value, before or " +
		"after, is reported: the before value is real data and the after value can be the " +
		"same data when a rule did nothing.")
	return b.String()
}

// -----------------------------------------------------------------------
// the verification
// -----------------------------------------------------------------------

type maskingVerifyDoc struct {
	Kind    string  `json:"kind"`
	Verdict Verdict `json:"verdict"`
	Summary string  `json:"summary"`
	// Clean is the package's own answer, and a skipped column makes it false.
	// A column nobody could read is not a column that passed.
	Clean       bool  `json:"clean"`
	Tables      int   `json:"tables_scanned"`
	Columns     int   `json:"columns_scanned"`
	RowsSampled int64 `json:"rows_sampled"`
	// SampleSize is the per column limit, recorded so a reader knows exactly
	// what clean covered.
	SampleSize int `json:"sample_size_per_column"`

	Findings      []maskingFindingDoc `json:"findings"`
	FindingsTotal int                 `json:"findings_total"`
	// Skipped names columns that could not be read. They are reported apart
	// from findings because they are a different fact: a finding is a column
	// the scan read and disliked, a skip is one it never saw.
	Skipped      []string `json:"skipped,omitempty"`
	SkippedTotal int      `json:"skipped_total"`
	Metrics      []Metric `json:"metrics,omitempty"`
	// ExamplesWithheld is always true. The scanner keeps a redacted excerpt of
	// each value it recognised, and even a redacted excerpt of real data is
	// real data.
	ExamplesWithheld bool   `json:"examples_withheld"`
	EvidenceNote     string `json:"evidence_note,omitempty"`
}

type maskingFindingDoc struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	// Detector is what recognised the value, such as an email address or a
	// credit card number.
	Detector string `json:"detector"`
	// Rows is how many of the sampled rows matched.
	Rows int `json:"rows_matched"`
}

func maskingVerification(ctx context.Context, readers maskingReaders) (any, *Fault) {
	rep, err := readers.Verify(ctx)
	if err != nil {
		return nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The data could not be read back, so nothing here says whether masking " +
				"worked. That is not a clean result: a scan that could not run has found " +
				"nothing because it looked at nothing. It reads this environment's branch, " +
				"so it needs one to be running. The server log says what failed.",
			Retryable: true,
			wrapped:   err,
		}
	}

	out := &maskingVerifyDoc{
		Kind: "masking_verification", Clean: rep.Clean(),
		Tables: rep.Tables, Columns: rep.Columns, RowsSampled: rep.RowsSampled,
		SampleSize: rep.SampleSize, Findings: []maskingFindingDoc{},
		FindingsTotal: len(rep.Findings), SkippedTotal: len(rep.Skipped),
		ExamplesWithheld: true,
	}
	for i, f := range rep.Findings {
		if i >= maxMaskingFindings {
			break
		}
		table, _ := safeIdentifier(f.Schema + "." + f.Table)
		column, _ := safeIdentifier(f.Column)
		out.Findings = append(out.Findings, maskingFindingDoc{
			Table: table, Column: column,
			Detector: neutralize(f.Detector, 64), Rows: f.Rows,
			// f.Example is deliberately not carried. It is a redacted excerpt
			// of a value that still looks real, and an excerpt of real data is
			// still real data.
		})
	}
	for i, s := range rep.Skipped {
		if i >= maxMaskingFindings {
			break
		}
		out.Skipped = append(out.Skipped, safeProse(s, 300))
	}

	zero := 0.0
	out.Metrics = []Metric{
		{
			Name: "columns_that_still_look_real", Value: float64(len(rep.Findings)),
			Unit: "columns", Threshold: &zero, Breached: len(rep.Findings) > 0,
		},
		{
			Name: "columns_that_could_not_be_read", Value: float64(len(rep.Skipped)),
			Unit: "columns", Threshold: &zero, Breached: len(rep.Skipped) > 0,
		},
		{Name: "columns_scanned", Value: float64(rep.Columns), Unit: "columns"},
		{Name: "rows_sampled", Value: float64(rep.RowsSampled), Unit: "rows"},
	}

	// A finding is a fact about the data and outranks a skip, which is a gap
	// in what could be seen. A skip on its own is INCONCLUSIVE rather than a
	// pass, which is the strict direction and the one this package's own Clean
	// takes: a scan that cannot read a column is a scan whose answer is that
	// it does not know.
	switch {
	case len(rep.Findings) > 0:
		out.Verdict = VerdictFail
	case len(rep.Skipped) > 0:
		out.Verdict = VerdictInconclusive
	default:
		out.Verdict = VerdictPass
	}
	out.Summary = maskingVerifySummary(rep, out)
	out.EvidenceNote = "Run af mask verify for the same scan with the redacted excerpt of " +
		"each value, which this result withholds."
	return out, nil
}

func maskingVerifySummary(rep verify.Report, doc *maskingVerifyDoc) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Read up to %d rows of each of %d %s across %d %s, %d rows in all. ",
		rep.SampleSize, rep.Columns, plural(rep.Columns, "column", "columns"),
		rep.Tables, plural(rep.Tables, "table", "tables"), rep.RowsSampled)

	if len(rep.Findings) > 0 {
		var where []string
		for i, f := range doc.Findings {
			if i >= 5 {
				break
			}
			where = append(where, f.Column+" holds "+f.Detector)
		}
		fmt.Fprintf(&b, "%d %s still look real: %s. ",
			len(rep.Findings), plural(len(rep.Findings), "column", "columns"),
			strings.Join(where, ", "))
	}
	if len(rep.Skipped) > 0 {
		fmt.Fprintf(&b,
			"%d %s not read at all, so %s neither passed nor failed. A column nobody "+
				"could read is not a column that passed, which is why this is not clean. ",
			len(rep.Skipped), plural(len(rep.Skipped), "column was", "columns were"),
			plural(len(rep.Skipped), "it", "they"))
	}
	if rep.Clean() {
		b.WriteString("Nothing that still looks real, and nothing unreadable, in what was " +
			"sampled. ")
	}
	b.WriteString("The values are not reported, not even the redacted excerpts the " +
		"scanner keeps, because an excerpt of real data is real data.")
	return b.String()
}

// -----------------------------------------------------------------------
// the apply
// -----------------------------------------------------------------------

// applyAcknowledgement is the sentence a caller has to send to run the apply.
//
// One value in an enum, which makes it a deliberate act rather than a default.
// A model that reaches this tool while meaning to preview cannot satisfy it by
// accident: the field is required, its only accepted value says out loud what
// is about to happen, and the schema refuses everything else. That is cheaper
// and harder to get past than any wording in a description, which a caller may
// never read.
const applyAcknowledgement = "yes, overwrite this environment's data irreversibly"

// newApplyMaskingTool builds apply_data_masking.
func newApplyMaskingTool(p *Project, eng *Engine, apply maskingApplier) *Tool {
	return &Tool{
		Name:  "apply_data_masking",
		Title: "Overwrite this environment's data, irreversibly",
		// Emphatically not read only. It rewrites every masked column of every
		// masked table in place.
		ReadOnly: false,
		Description: "IRREVERSIBLE. This REWRITES this environment's data in place, and " +
			"once a column is overwritten the original is gone. There is no undo and " +
			"nothing here restores it. " +
			"It is safe in the sense that matters, which is that the branch it rewrites is " +
			"a copy rather than production, and it is exactly how a golden is produced. It " +
			"is not safe in the sense a caller might assume: a branch somebody was working " +
			"in loses its data. " +
			"THIS IS NOT A PREVIEW. If you want to know what masking would do, or whether " +
			"a rule fires, or whether anything still looks real, the read only tool is " +
			"inspect_data_masking and it changes nothing. Use it first: it names every " +
			"column no rule covers, and a plan with unresolved problems is refused here " +
			"rather than partly applied. " +
			"It rewrites every row of every masked table, so it takes minutes and returns " +
			"a run_id immediately: poll it with get_rehearsal_run. " +
			"It reports counts only, and no column value passes through it in either " +
			"direction.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id", "acknowledge_irreversible"},
			Properties: map[string]*Schema{
				"project_id":      projectIDSchema(),
				"idempotency_key": idempotencyKeySchema(),
				"acknowledge_irreversible": {
					Type: "string", MaxLength: 64, MinLength: 8,
					Enum: []string{applyAcknowledgement},
					Description: "Required, and there is exactly one accepted value: " +
						`"` + applyAcknowledgement + `". ` +
						"It exists so that this tool cannot be reached by accident by a " +
						"caller that meant to preview. Send it only when you have decided " +
						"that this environment's current data is expendable.",
				},
			},
		},
		Handler: func(_ context.Context, call *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			// Checked again here rather than left to the schema alone. The
			// schema does enforce it, and a required irreversible action is
			// the wrong place to depend on exactly one mechanism.
			if ack, _ := args["acknowledge_irreversible"].(string); ack != applyAcknowledgement {
				return nil, fieldFault(FaultInvalidArgument, "acknowledge_irreversible",
					"This field must be exactly %q. Nothing was changed.", applyAcknowledgement)
			}
			return eng.Submit(call, "apply_data_masking", args,
				func(ctx context.Context, runID string) (string, *ResultBody, *Fault) {
					return runMaskApply(ctx, eng, apply, runID)
				})
		},
	}
}

type maskApplyDoc struct {
	Tables int   `json:"tables_rewritten"`
	Rows   int64 `json:"rows_rewritten"`
	// Resumed reports that the run picked up from a checkpoint left by an
	// earlier attempt, so these counts are this attempt's and not the total.
	Resumed    bool  `json:"resumed_from_a_checkpoint"`
	DurationMS int64 `json:"duration_ms"`
	// Irreversible is stated in the result as well as in the description,
	// because a result is what gets read back later.
	Irreversible bool `json:"irreversible"`
}

func runMaskApply(
	ctx context.Context, eng *Engine, apply maskingApplier, runID string,
) (string, *ResultBody, *Fault) {
	if eng.Cancelled(ctx, runID) {
		return "", nil, faultf(FaultRunNotCancellable, "This run was cancelled before it started.")
	}
	eng.Phase(ctx, runID, "rewriting this environment's data according to the plan")

	res, err := apply(ctx)
	if err != nil {
		// A refused plan and a failed connection both land here and both mean
		// the same thing to a caller: the data was not rewritten as asked. The
		// executor refuses a plan with problems before it writes anything, so
		// this is not a half applied table.
		return "", nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The data was not rewritten. A plan with unresolved problems is refused " +
				"before anything is written rather than partly run, because a half masked " +
				"table is neither real nor safe. Ask inspect_data_masking the plan question " +
				"to see what could not be assigned. The server log says what failed.",
			Retryable: true,
			wrapped:   err,
		}
	}

	body := &ResultBody{
		Metrics: []Metric{
			{Name: "tables_rewritten", Value: float64(res.Tables), Unit: "tables"},
			{Name: "rows_rewritten", Value: float64(res.Rows), Unit: "rows"},
			{Name: "duration_ms", Value: float64(res.Duration.Milliseconds()), Unit: "ms"},
		},
		Evidence: []Evidence{{
			URI: "af://mask/verify", Kind: "next_step",
			Note: "Masking that is not checked is masking somebody believes in. Ask " +
				"inspect_data_masking the verify question now, which reads the data back " +
				"and reports anything that still looks real.",
		}},
		Detail: &maskApplyDoc{
			Tables: res.Tables, Rows: res.Rows, Resumed: res.Resumed,
			DurationMS: res.Duration.Milliseconds(), Irreversible: true,
		},
		Findings: boundFindings(nil),
	}
	body.Summary = fmt.Sprintf(
		"Rewrote %d %s across %d %s in %dms. This is irreversible and the previous values "+
			"are gone. It is not proof that the data is now safe: run the verify question "+
			"of inspect_data_masking, which reads the data back and runs the same "+
			"detectors that would find it if it leaked.%s",
		res.Rows, plural(int(min64(res.Rows, 2)), "row", "rows"),
		res.Tables, plural(res.Tables, "table", "tables"),
		res.Duration.Milliseconds(), resumedNote(res.Resumed))
	return report.VerdictPass, body, nil
}

func resumedNote(resumed bool) string {
	if !resumed {
		return ""
	}
	return " This run resumed from a checkpoint an earlier attempt left, so these counts " +
		"cover this attempt rather than the whole table."
}

// maskingReaders builds the three read only masking questions.
//
// An orchestrator per call rather than one held open, for the reason the
// factory's own comment gives: it holds no connection until something asks it
// to, and the branch can change under a long lived server.
func (f *orchestratorFactory) maskingReaders() maskingReaders {
	return maskingReaders{
		Plan: func(ctx context.Context) (*env.PlanResult, error) {
			o, err := f.build()
			if err != nil {
				return nil, err
			}
			return o.MaskPlan(ctx)
		},
		Sample: func(ctx context.Context, table string, rows int) ([][]env.PreviewRow, error) {
			o, err := f.build()
			if err != nil {
				return nil, err
			}
			return o.MaskPreview(ctx, table, rows)
		},
		Verify: func(ctx context.Context) (verify.Report, error) {
			o, err := f.build()
			if err != nil {
				return verify.Report{}, err
			}
			return o.MaskVerify(ctx)
		},
	}
}

// maskApply rewrites the environment's data.
//
// The one write in this whole domain, reached from one tool, which requires an
// acknowledgement the schema will not let a caller omit.
func (f *orchestratorFactory) maskApply(ctx context.Context) (masking.Result, error) {
	o, err := f.build()
	if err != nil {
		return masking.Result{}, err
	}
	return o.MaskApply(ctx)
}
