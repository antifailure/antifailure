package masking

import (
	"fmt"
	"sort"
	"strings"
)

// A plan is what will happen, computed before anything happens.
//
// Masking is destructive and irreversible: once a column is overwritten the
// original is gone. So the plan is produced, printed, and checked first, and
// the executor refuses to run one that has unresolved problems. The
// alternative, discovering a problem partway through, leaves a table neither
// real nor safe with nothing to say which rows are which.

// Plan is an ordered set of table updates.
type Plan struct {
	// Tables are the updates to run, in the order they run.
	Tables []TablePlan
	// Unwritten are the tables whose every assigned column is preserved. They
	// get no statement at all, and they are kept here rather than dropped so
	// the plan still shows that each one was reviewed and found safe.
	Unwritten []TablePlan
	// Unclassified are columns no rule covered that look like they hold
	// something. They are not a failure on their own; they are the list
	// somebody has to answer.
	Unclassified []Assignment
	// Problems are assignments that cannot be carried out. A plan with any is
	// refused rather than partly run.
	Problems []Assignment
	// RulesHash identifies the configuration that produced this plan, so a
	// golden can record what it was masked with and a changed rule set is
	// visible as a different golden rather than as the same one behaving
	// differently.
	RulesHash string
}

// TablePlan is one table's work.
type TablePlan struct {
	Table Table
	// Columns are the columns being rewritten, in a stable order.
	Columns []Assignment
	// Reviewed are the columns a preserve rule covers, in a stable order. They
	// were decided and the plan shows the decision, and nothing is written to
	// them: the transform returns the value it was given, and writing that back
	// is a new version of every row for no change.
	Reviewed []Assignment
	// ChunkSize is how many rows one statement covers.
	ChunkSize int
	// OrderBy is the primary key, which chunking needs to make progress
	// deterministic, and which a checkpoint needs to name a position.
	OrderBy []string
	// Skipped, when set, says why this table is not being touched.
	Skipped string
}

// Rows is the estimated row count for this table.
func (tp TablePlan) Rows() int64 { return tp.Table.Rows }

// AddressWidth is how many values address one row: one per primary key column,
// or one ctid for a table with none. A chunk's first AddressWidth columns are
// the address, and so are an update's first AddressWidth parameters.
func (tp TablePlan) AddressWidth() int {
	if len(tp.OrderBy) > 0 {
		return len(tp.OrderBy)
	}
	return 1
}

// Chunks estimates how many statements this table takes.
func (tp TablePlan) Chunks() int64 {
	if tp.ChunkSize <= 0 || tp.Table.Rows <= 0 {
		return 1
	}
	return (tp.Table.Rows + int64(tp.ChunkSize) - 1) / int64(tp.ChunkSize)
}

// DefaultChunkSize is a compromise between the number of statements and the
// length of the lock each one holds.
//
// Too small and a large table takes tens of thousands of round trips; too
// large and one statement holds a row lock long enough to matter. Twenty
// thousand rows is a fraction of a second on any table this is run against,
// which keeps a masking run interruptible: killing it loses one chunk.
const DefaultChunkSize = 20_000

// BuildPlan turns assignments into an ordered plan.
func BuildPlan(tables []Table, assignments []Assignment, rulesHash string) Plan {
	byTable := map[string][]Assignment{}
	reviewed := map[string][]Assignment{}
	for _, a := range assignments {
		key := a.Table.String()
		switch {
		case a.Rewrites():
			byTable[key] = append(byTable[key], a)
		case a.Masked():
			// Preserved: reviewed and found safe. Recorded beside the table's
			// rewrites so the plan still shows the decision, and never
			// compiled into a statement.
			reviewed[key] = append(reviewed[key], a)
		}
	}

	plan := Plan{
		RulesHash:    rulesHash,
		Unclassified: Unclassified(assignments),
		Problems:     Problems(assignments),
	}

	SortTables(tables)
	for _, t := range tables {
		cols, kept := byTable[t.String()], reviewed[t.String()]
		if len(cols) == 0 && len(kept) == 0 {
			continue
		}
		// A stable column order, so two plans for the same schema produce the
		// same statements and a diff between them means something.
		sort.Slice(cols, func(i, j int) bool { return cols[i].Column.Name < cols[j].Column.Name })
		sort.Slice(kept, func(i, j int) bool { return kept[i].Column.Name < kept[j].Column.Name })

		tp := TablePlan{
			Table: t, Columns: cols, Reviewed: kept,
			ChunkSize: DefaultChunkSize, OrderBy: t.PrimaryKey,
		}
		if len(cols) == 0 {
			// Every column a rule names here is preserved, so there is nothing
			// to write and no statement. Such a table used to be rewritten in
			// full with the values it already held.
			tp.ChunkSize = 0
			plan.Unwritten = append(plan.Unwritten, tp)
			continue
		}
		if len(t.PrimaryKey) == 0 {
			// Without a key there is no stable order, so there is no way to
			// resume and no way to be sure every row was covered exactly once.
			// The table is still masked, in one statement, and the plan says
			// so rather than pretending it was chunked.
			tp.ChunkSize = 0
		}
		plan.Tables = append(plan.Tables, tp)
	}
	return plan
}

// Runnable reports whether the plan can be executed.
func (p Plan) Runnable() bool { return len(p.Problems) == 0 }

// CopiedUnchanged returns the unclassified columns that ship as they are.
//
// Two different things share the Unclassified list. Most of it is emptied by
// the fail closed default, which is a question with a safe answer already in
// place. The rest is copied unchanged: a NOT NULL text column, a bytea, an
// enum, an array, anything the default has no way to empty. That is a
// question with NO answer in place, and it was printed once, at the bottom of
// af mask plan, and read by nothing downstream. Every command that publishes
// or lists a golden now carries this count, so the number of columns that
// hold exactly what production held travels with the copy it describes.
func (p Plan) CopiedUnchanged() []Assignment {
	var out []Assignment
	for _, a := range p.Unclassified {
		if a.Transform == "" {
			out = append(out, a)
		}
	}
	return out
}

// CopiedUnchangedNames returns the same columns as schema.table.column, which
// is the form the verification scan and the attestation carry.
func (p Plan) CopiedUnchangedNames() []string {
	cols := p.CopiedUnchanged()
	out := make([]string, 0, len(cols))
	for _, a := range cols {
		out = append(out, a.Table.String()+"."+a.Column.Name)
	}
	return out
}

// Columns counts the columns being rewritten.
func (p Plan) Columns() int {
	n := 0
	for _, t := range p.Tables {
		n += len(t.Columns)
	}
	return n
}

// Rows estimates the rows being rewritten.
func (p Plan) Rows() int64 {
	var n int64
	for _, t := range p.Tables {
		n += t.Rows()
	}
	return n
}

// Statement is one chunked update.
type Statement struct {
	// SQL is the statement text, with placeholders for the bounds.
	SQL string
	// Table is which table it covers.
	Table string
	// Columns are the columns it rewrites.
	Columns []string
	// Keyed reports whether it is chunked, which decides whether a checkpoint
	// can name a position in it.
	Keyed bool
}

// Compile turns a table plan into the statement that rewrites it.
//
// The values are computed in Go and sent as parameters rather than built into
// SQL, for two reasons. The transforms are deterministic functions of a key
// that never goes near the database, so the database cannot compute them. And
// a statement that interpolated values would be a statement where a masked
// value containing a quote changes what the statement means.
//
// The text itself comes from the table's dialect, because the two engines
// disagree about all three of quoting, casting and the shape of a rewrite,
// and about nothing else here.
func (tp TablePlan) Compile() Statement {
	return tp.Table.dialect().Update(tp)
}

// Reviewed returns every column a preserve rule covers, beside a table's
// rewrites or on a table with nothing else. None of them is written.
func (p Plan) Reviewed() []Assignment {
	var out []Assignment
	for _, t := range p.Tables {
		out = append(out, t.Reviewed...)
	}
	for _, t := range p.Unwritten {
		out = append(out, t.Reviewed...)
	}
	return out
}

// reviewedNote is how a preserved column reads in a plan.
const reviewedNote = "preserve, reviewed and found safe, not written"

// explainColumn renders one column's decision.
func explainColumn(b *strings.Builder, c Assignment, decision string) {
	source := ""
	if c.FromDefault {
		source = " (default rule)"
	}
	link := ""
	if c.Link != "" && c.Transform != PreserveTransform {
		link = ", linked to " + c.Link
	}
	fmt.Fprintf(b, "  %-24s %s%s%s\n", c.Column.Name, decision, link, source)
	if c.Why != "" {
		fmt.Fprintf(b, "  %-24s %s\n", "", c.Why)
	}
}

// Explain renders a plan for a person.
//
// A preserved column is shown with its decision and marked as not written, and a
// table with nothing else is shown with no statement, so the review stays
// visible where no work happens.
func (p Plan) Explain() string {
	var b strings.Builder
	if len(p.Tables) == 0 && len(p.Unwritten) == 0 {
		b.WriteString("Nothing to mask. No column in this database matched a rule.\n")
	}
	for _, t := range p.Unwritten {
		fmt.Fprintf(&b, "%s\n", t.Table)
		for _, c := range t.Reviewed {
			explainColumn(&b, c, reviewedNote)
		}
		fmt.Fprintf(&b, "  %-24s no statement: every column a rule names here was reviewed and found safe\n", "")
		b.WriteString("\n")
	}
	for _, t := range p.Tables {
		fmt.Fprintf(&b, "%s\n", t.Table)
		for _, c := range t.Columns {
			explainColumn(&b, c, c.Transform)
		}
		for _, c := range t.Reviewed {
			explainColumn(&b, c, reviewedNote)
		}
		if t.ChunkSize > 0 {
			fmt.Fprintf(&b, "  %-24s about %d rows in %d chunks\n", "", t.Rows(), t.Chunks())
		} else {
			fmt.Fprintf(&b, "  %-24s about %d rows in one statement (no primary key to chunk on)\n",
				"", t.Rows())
		}
		b.WriteString("\n")
	}
	return b.String()
}

// DescribeProblems renders the reasons a plan will not run.
func DescribeProblems(problems []Assignment) string {
	parts := make([]string, 0, len(problems))
	for _, p := range problems {
		parts = append(parts, fmt.Sprintf("%s.%s: %s", p.Table, p.Column.Name, p.Problem))
	}
	return strings.Join(parts, "; ")
}

// DescribeColumns names a few columns, and says how many more there are.
//
// A list of forty column names in an error message is a list nobody reads, and
// a count with no examples is one nobody can act on.
func DescribeColumns(assignments []Assignment, limit int) string {
	names := make([]string, 0, len(assignments))
	for _, a := range assignments {
		names = append(names, a.Table.String()+"."+a.Column.Name)
	}
	if len(names) <= limit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:limit], ", "), len(names)-limit)
}
