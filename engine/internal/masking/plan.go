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
	// ChunkSize is how many rows one statement covers.
	ChunkSize int
	// OrderBy is the primary key, which chunking needs to make progress
	// deterministic and resumable.
	OrderBy []string
	// Skipped, when set, says why this table is not being touched.
	Skipped string
}

// Rows is the estimated row count for this table.
func (tp TablePlan) Rows() int64 { return tp.Table.Rows }

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
	for _, a := range assignments {
		if !a.Masked() {
			continue
		}
		key := a.Table.String()
		byTable[key] = append(byTable[key], a)
	}

	plan := Plan{
		RulesHash:    rulesHash,
		Unclassified: Unclassified(assignments),
		Problems:     Problems(assignments),
	}

	SortTables(tables)
	for _, t := range tables {
		cols := byTable[t.String()]
		if len(cols) == 0 {
			continue
		}
		// A stable column order, so two plans for the same schema produce the
		// same statements and a diff between them means something.
		sort.Slice(cols, func(i, j int) bool { return cols[i].Column.Name < cols[j].Column.Name })

		tp := TablePlan{Table: t, Columns: cols, ChunkSize: DefaultChunkSize, OrderBy: t.PrimaryKey}
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
	// Keyed reports whether it is chunked, which decides whether the executor
	// can resume it.
	Keyed bool
}

// Compile turns a table plan into the statement that rewrites it.
//
// The values are computed in Go and sent as parameters rather than built into
// SQL, for two reasons. The transforms are deterministic functions of a key
// that never goes near the database, so the database cannot compute them. And
// a statement that interpolated values would be a statement where a masked
// value containing a quote changes what the statement means.
func (tp TablePlan) Compile() Statement {
	names := make([]string, 0, len(tp.Columns))
	sets := make([]string, 0, len(tp.Columns))
	for i, c := range tp.Columns {
		names = append(names, c.Column.Name)
		sets = append(sets, fmt.Sprintf("%s = $%d", quoteIdent(c.Column.Name), i+2))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "UPDATE %s SET %s WHERE %s::text = $1",
		tp.Table.Qualified(), strings.Join(sets, ", "), tp.keyExpr())
	return Statement{
		SQL: b.String(), Table: tp.Table.String(), Columns: names,
		Keyed: len(tp.OrderBy) > 0,
	}
}

// keyExpr is what addresses one row.
//
// A primary key when there is one. Otherwise ctid, the physical row
// identifier, which every table has and which is only meaningful inside the
// transaction that read it. That is why a table with no key cannot be
// resumed: nothing about a ctid survives the run that saw it.
func (tp TablePlan) keyExpr() string {
	if len(tp.OrderBy) > 0 {
		return quoteIdent(tp.OrderBy[0])
	}
	return "ctid"
}

// Explain renders a plan for a person.
func (p Plan) Explain() string {
	var b strings.Builder
	if len(p.Tables) == 0 {
		b.WriteString("Nothing to mask. No column in this database matched a rule.\n")
	}
	for _, t := range p.Tables {
		fmt.Fprintf(&b, "%s\n", t.Table)
		for _, c := range t.Columns {
			source := ""
			if c.FromDefault {
				source = " (default rule)"
			}
			link := ""
			if c.Link != "" {
				link = ", linked to " + c.Link
			}
			fmt.Fprintf(&b, "  %-24s %s%s%s\n", c.Column.Name, c.Transform, link, source)
			if c.Why != "" {
				fmt.Fprintf(&b, "  %-24s %s\n", "", c.Why)
			}
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
