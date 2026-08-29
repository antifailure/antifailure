package subset

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Execute copies the plan's rows from the source into the target.
//
// The order of operations is the guarantee, in the same way the golden's copy,
// mask, verify, publish order is:
//
//  1. Everything read from the source happens inside ONE read-only repeatable
//     read transaction. Repeatable read because a parent selected from one
//     snapshot and a child from a later one is a subset whose references do
//     not resolve, through no fault of the plan; read only because the seed
//     predicate is SQL from a manifest, and a manifest is a file somebody can
//     open a pull request against.
//  2. Rows are streamed table by table with COPY in both directions, binary,
//     never through an INSERT and never through the caller's memory.
//  3. Constraint enforcement is suspended on the TARGET for the load, so that
//     a cycle and a self reference can be loaded at all, and the references
//     that could not be satisfied by copy order are repaired afterwards.
//  4. Sequences are moved past what was copied, because a subset that loads
//     cleanly and then collides on the application's first insert is a subset
//     that failed in the least useful place.
//  5. Every foreign key is then checked explicitly. Not the constraints
//     revalidated, which after step 3 would prove only that Postgres was not
//     watching: a query per key asking whether any row's reference is absent.
//     That check is the property this package exists to provide, so it runs
//     whether or not anything was repaired, and its result is returned.
func Execute(ctx context.Context, opts Options) (*Stats, error) {
	if opts.Progress == nil {
		opts.Progress = func(string) {}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	started := opts.Now()
	stats := &Stats{}

	src, err := pgx.Connect(ctx, opts.SourceURL)
	if err != nil {
		return nil, fmt.Errorf("subset: connect to the source: %w", err)
	}
	defer func() { _ = src.Close(context.WithoutCancel(ctx)) }()

	dst, err := pgx.Connect(ctx, opts.TargetURL)
	if err != nil {
		return nil, fmt.Errorf("subset: connect to the candidate: %w", err)
	}
	defer func() { _ = dst.Close(context.WithoutCancel(ctx)) }()

	// One snapshot for every read. Without it, a customer selected at the top
	// of the run and an order selected at the bottom come from different
	// states of a live database, and an order whose customer arrived after the
	// customers were chosen is an orphan the plan never had a chance to avoid.
	if _, err := src.Exec(ctx,
		"BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		return nil, fmt.Errorf("subset: begin the read only snapshot: %w", err)
	}
	defer func() { _, _ = src.Exec(context.WithoutCancel(ctx), "ROLLBACK") }()

	if err := checkSeed(ctx, src, opts.Plan); err != nil {
		return nil, err
	}

	// Suspended on the candidate only, which is a database this process
	// created and nothing else can see. A cycle cannot be loaded in any order
	// with enforcement on, and a self reference cannot be loaded at all.
	enforced := true
	if _, err := dst.Exec(ctx, "SET session_replication_role = replica"); err == nil {
		enforced = false
	} else {
		// Not fatal. Enforcement stays on, which is fine for a schema with no
		// cycle and will fail loudly on one that has, rather than silently
		// producing a database missing the rows it refused.
		stats.Notes = append(stats.Notes,
			"the candidate would not let constraint enforcement be suspended, so a "+
				"cycle or a self reference will fail the load rather than be repaired: "+err.Error())
	}

	for i := range opts.Plan.Steps {
		step := opts.Plan.Steps[i]
		n, err := copyStep(ctx, src, dst, opts.Plan, i)
		if err != nil {
			return stats, err
		}
		stats.Tables = append(stats.Tables, TableStat{Table: step.Table.Ref(), Rows: n})
		stats.Rows += n
		opts.Progress(fmt.Sprintf("copied %s (%d rows)", step.Table.Ref(), n))
	}

	repairs, err := repair(ctx, dst, opts.Plan)
	if err != nil {
		return stats, err
	}
	stats.Repairs = repairs
	for _, r := range repairs {
		if r.Rows > 0 {
			opts.Progress(fmt.Sprintf("%s: %s", r.Key, r.Detail))
		}
	}

	seqs, err := advanceSequences(ctx, src, dst, opts.Plan)
	if err != nil {
		return stats, err
	}
	stats.Sequences = seqs

	if !enforced {
		if _, err := dst.Exec(ctx, "SET session_replication_role = origin"); err != nil {
			return stats, fmt.Errorf("subset: restore constraint enforcement: %w", err)
		}
	}

	orphans, err := CheckIntegrity(ctx, dst, opts.Plan.Relationships)
	if err != nil {
		return stats, err
	}
	stats.Orphans = orphans
	stats.Coverage, err = coverage(ctx, dst, opts.Plan)
	if err != nil {
		return stats, err
	}
	for _, t := range stats.Tables {
		if t.Rows == 0 {
			stats.Empty = append(stats.Empty, t.Table)
		}
	}
	stats.Duration = opts.Now().Sub(started)

	if len(orphans) > 0 {
		// The one thing this package promises. Reaching here means the repair
		// did not close it, which is a bug in the repair rather than something
		// to report and continue past.
		return stats, fmt.Errorf(
			"subset: the copy finished and %d foreign keys do not resolve, so the "+
				"result is not loadable:\n  %s",
			len(orphans), strings.Join(describeOrphans(orphans), "\n  "))
	}
	return stats, nil
}

// Options is what Execute needs.
type Options struct {
	// SourceURL is read only and is read inside one snapshot.
	SourceURL string
	// TargetURL is the candidate, which already has the schema and no rows.
	TargetURL string
	Plan      Plan
	Progress  func(string)
	Now       func() time.Time
}

// Stats is what a run took and what it produced.
type Stats struct {
	Tables    []TableStat
	Rows      int64
	Repairs   []Repair
	Coverage  []Coverage
	Orphans   []Orphan
	Sequences int
	// Empty names the tables the subset left with no rows at all. A table that
	// arrives empty and should not have is a bug somebody finds three days
	// later in a test that returns nothing.
	Empty    []string
	Notes    []string
	Duration time.Duration
}

// TableStat is one table's row count.
type TableStat struct {
	Table string
	Rows  int64
}

// Repair is one deferred key put right after the load.
type Repair struct {
	Key    string
	Rows   int64
	Detail string
}

// Coverage is how much of a relationship survived the subset, which is what
// says whether a slice is production shaped or merely small.
type Coverage struct {
	Key string
	// Rows is how many rows in the child table hold a reference at all, and
	// Resolved how many of those find their parent. They are equal in a subset
	// that is correct; the pair is reported rather than a ratio because "100
	// percent of nothing" and "100 percent of a million" read the same.
	Rows     int64
	Resolved int64
}

// checkSeed runs the seed predicate before anything is copied.
//
// A column name that does not exist is the ordinary mistake here, and finding
// it after twenty minutes of copying, from an error naming a generated query
// nobody wrote, is the difference between a typo and an afternoon.
func checkSeed(ctx context.Context, src *pgx.Conn, plan Plan) error {
	for i, s := range plan.Steps {
		if len(s.Conditions) == 0 {
			continue
		}
		q := fmt.Sprintf("SELECT 1 FROM (%s) af_probe LIMIT 0", plan.Query(i))
		if _, err := src.Exec(ctx, q); err != nil {
			return fmt.Errorf(
				"subset: the selection for %s is not a query this database will run: %w\n"+
					"The condition was: %s",
				s.Table.Ref(), err, strings.Join(s.Conditions, " AND "))
		}
	}
	return nil
}

// copyStep streams one table from the source into the candidate.
//
// Binary rather than text, because binary is the database's own
// representation: a timestamp, a float and a numeric all survive it exactly,
// where text goes through a formatter and a parser whose agreement depends on
// two servers sharing a DateStyle and an extra_float_digits.
//
// The columns are named explicitly rather than left to the table's own order,
// because a generated column is in that order and COPY refuses to write one.
func copyStep(ctx context.Context, src, dst *pgx.Conn, plan Plan, i int) (int64, error) {
	step := plan.Steps[i]
	cols := step.Table.Writable()
	if len(cols) == 0 {
		return 0, nil
	}
	out := fmt.Sprintf("COPY (%s) TO STDOUT (FORMAT BINARY)", plan.Query(i))
	in := fmt.Sprintf("COPY %s (%s) FROM STDIN (FORMAT BINARY)",
		step.Table.Qualified(), strings.Join(quoteAll(cols), ", "))

	r, w := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		_, err := src.PgConn().CopyTo(ctx, w, out)
		// Closing with the error is what makes the reader fail rather than see
		// a truncated stream and load half a table successfully.
		_ = w.CloseWithError(err)
		errc <- err
	}()

	tag, inErr := dst.PgConn().CopyFrom(ctx, r, in)
	_ = r.CloseWithError(inErr)
	outErr := <-errc

	if outErr != nil {
		return 0, fmt.Errorf("subset: reading %s from the source: %w", step.Table.Ref(), outErr)
	}
	if inErr != nil {
		return 0, fmt.Errorf("subset: writing %s into the candidate: %w", step.Table.Ref(), inErr)
	}
	return tag.RowsAffected(), nil
}

// repair puts right the references the load could not satisfy.
//
// The deferred keys are where it starts: a self reference, and one edge of
// every cycle, were never conditions on the selection, so a row can point at a
// row the budget or the closure left out.
//
// It runs over EVERY relationship rather than only those, because a repair can
// create work for another key and the first version of this did not and was
// wrong. Concretely: a project whose lead was not in the subset has a required
// reference that cannot be cleared, so the project is removed; an employee
// whose primary project was that project then points at nothing, through a key
// that WAS a condition and so looked settled. A test on a two table cycle
// caught it. Passing over every key is one cheap counting query each per pass
// and it is what makes the guarantee hold rather than usually hold.
//
// Nulling is preferred to deleting and deleting is a last resort, and the
// whole thing runs to a fixed point because each delete can orphan the next
// row.
func repair(ctx context.Context, dst *pgx.Conn, plan Plan) ([]Repair, error) {
	if len(plan.Relationships) == 0 {
		return nil, nil
	}
	byKey := map[string]*Repair{}
	var order []string

	for pass := 0; pass < maxRepairPasses; pass++ {
		changed := int64(0)
		for _, k := range plan.Relationships {
			child, ok := tableOf(plan, k.From)
			if !ok {
				continue
			}
			parent, ok := tableOf(plan, k.To)
			if !ok {
				continue
			}
			pred := orphanPredicate(k, child, parent)
			nullable := true
			for _, c := range k.FromColumns {
				if col, found := child.ColumnNamed(c); !found || !col.Nullable {
					nullable = false
					break
				}
			}
			var stmt, detail string
			if nullable {
				sets := make([]string, 0, len(k.FromColumns))
				for _, c := range k.FromColumns {
					sets = append(sets, quote(c)+" = NULL")
				}
				stmt = fmt.Sprintf("UPDATE %s AS c SET %s WHERE %s",
					child.Qualified(), strings.Join(sets, ", "), pred)
				detail = "the reference was optional, so it was cleared"
			} else {
				stmt = fmt.Sprintf("DELETE FROM %s AS c WHERE %s", child.Qualified(), pred)
				detail = "the reference was required and its row was not in the subset, so the row was removed"
			}
			tag, err := dst.Exec(ctx, stmt)
			if err != nil {
				return nil, fmt.Errorf("subset: repairing %s: %w", k, err)
			}
			n := tag.RowsAffected()
			if n == 0 {
				continue
			}
			changed += n
			id := k.String()
			if byKey[id] == nil {
				byKey[id] = &Repair{Key: id, Detail: detail}
				order = append(order, id)
			}
			byKey[id].Rows += n
		}
		if changed == 0 {
			break
		}
	}

	out := make([]Repair, 0, len(order))
	for _, id := range order {
		out = append(out, *byKey[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// maxRepairPasses bounds the fixed point. Each pass can only remove rows, so
// it terminates; the bound is there so that a bug terminates too.
const maxRepairPasses = 32

// orphanPredicate matches rows whose reference is set and whose parent is not
// there. A reference that is not set is not an orphan: a null foreign key
// satisfies the constraint.
func orphanPredicate(k ForeignKey, child, parent Table) string {
	var set []string
	for _, c := range k.FromColumns {
		set = append(set, "c."+quote(c)+" IS NOT NULL")
	}
	var join []string
	for i := range k.FromColumns {
		join = append(join, fmt.Sprintf("p.%s = c.%s",
			quote(k.ToColumns[i]), quote(k.FromColumns[i])))
	}
	return fmt.Sprintf("%s AND NOT EXISTS (SELECT 1 FROM %s AS p WHERE %s)",
		strings.Join(set, " AND "), parent.Qualified(), strings.Join(join, " AND "))
}

func tableOf(plan Plan, ref string) (Table, bool) {
	for _, s := range plan.Steps {
		if s.Table.Ref() == ref {
			return s.Table, true
		}
	}
	return Table{}, false
}

// sequenceMargin is how far past the largest copied key a sequence is moved.
//
// Past rather than to, because a subset is not the whole table: the rows above
// the largest one copied still exist in production and will exist in the next
// refresh, and a golden whose sequence sits exactly on its own maximum hands
// the application identifiers that a later refresh will collide with. The
// margin makes the environment's own rows distinguishable from production's,
// which is worth something the first time somebody is looking at a bug report
// and wondering which is which.
const sequenceMargin = 1000

// advanceSequences moves every sequence past what was copied.
func advanceSequences(ctx context.Context, src, dst *pgx.Conn, plan Plan) (int, error) {
	moved := 0
	for _, s := range plan.Sequences {
		ref := qualifyRef(s.Ref)
		if s.Table == "" {
			// A standalone sequence owns no column, so there is no maximum to
			// move past. Its source value is carried across instead, which is
			// the only honest answer: the application's next value continues
			// from where production's did.
			var last int64
			var called bool
			q := fmt.Sprintf("SELECT last_value, is_called FROM %s", ref)
			if err := src.QueryRow(ctx, q).Scan(&last, &called); err != nil {
				continue
			}
			if _, err := dst.Exec(ctx,
				fmt.Sprintf("SELECT setval(%s, $1, $2)", literal(s.Ref)), last, called); err != nil {
				return moved, fmt.Errorf("subset: setting %s: %w", s.Ref, err)
			}
			moved++
			continue
		}
		table, ok := tableOf(plan, s.Table)
		if !ok {
			continue
		}
		if _, found := table.ColumnNamed(s.Column); !found {
			continue
		}
		// GREATEST with the sequence's own minimum, because a table that ended
		// up empty has no maximum and setval refuses a value below the minimum.
		q := fmt.Sprintf(
			"SELECT setval(%s, GREATEST(COALESCE((SELECT max(%s) FROM %s), 0) + %d, 1), true)",
			literal(s.Ref), quote(s.Column), table.Qualified(), sequenceMargin)
		if _, err := dst.Exec(ctx, q); err != nil {
			return moved, fmt.Errorf("subset: advancing %s: %w", s.Ref, err)
		}
		moved++
	}
	return moved, nil
}

// coverage reports, per relationship, how many referencing rows survived and
// how many of them found their parent.
func coverage(ctx context.Context, dst *pgx.Conn, plan Plan) ([]Coverage, error) {
	var out []Coverage
	for _, k := range plan.Relationships {
		child, ok := tableOf(plan, k.From)
		if !ok {
			continue
		}
		parent, ok := tableOf(plan, k.To)
		if !ok {
			continue
		}
		var set []string
		for _, c := range k.FromColumns {
			set = append(set, "c."+quote(c)+" IS NOT NULL")
		}
		var join []string
		for i := range k.FromColumns {
			join = append(join, fmt.Sprintf("p.%s = c.%s",
				quote(k.ToColumns[i]), quote(k.FromColumns[i])))
		}
		q := fmt.Sprintf(`
SELECT count(*) FILTER (WHERE %s),
       count(*) FILTER (WHERE %s AND EXISTS (SELECT 1 FROM %s AS p WHERE %s))
FROM %s AS c`,
			strings.Join(set, " AND "),
			strings.Join(set, " AND "), parent.Qualified(), strings.Join(join, " AND "),
			child.Qualified())
		var rows, resolved int64
		if err := dst.QueryRow(ctx, q).Scan(&rows, &resolved); err != nil {
			return nil, fmt.Errorf("subset: measuring %s: %w", k, err)
		}
		out = append(out, Coverage{Key: k.String(), Rows: rows, Resolved: resolved})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// qualifyRef turns schema.name into a quoted identifier pair.
func qualifyRef(ref string) string {
	schema, name, ok := strings.Cut(ref, ".")
	if !ok {
		return quote(ref)
	}
	return quote(schema) + "." + quote(name)
}

// literal renders a string as an SQL literal, for regclass arguments that
// cannot be parameters.
func literal(s string) string {
	schema, name, ok := strings.Cut(s, ".")
	if !ok {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	q := quote(schema) + "." + quote(name)
	return "'" + strings.ReplaceAll(q, "'", "''") + "'"
}
