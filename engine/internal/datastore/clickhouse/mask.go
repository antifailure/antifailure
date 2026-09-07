package clickhouse

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// The masking run, and it is a different shape from the Postgres one on
// purpose.
//
// The Postgres executor reads a chunk, computes the masked values in Go and
// writes them back one row at a time inside a transaction. Ported here that
// becomes one ALTER TABLE ... UPDATE per row, which is a MUTATION: ClickHouse
// rewrites every part holding a matching row, so the cost is quadratic in the
// table. Measured on this machine against ClickHouse 25.3 on a ten thousand
// row table: 20 rows in 11.05 seconds, 1.81 rows a second. A ten thousand row
// table would take an hour and a half and a million rows would take six days.
// The same ten thousand rows through the shape below took 0.13 seconds.
//
// The shape is the one internal/masking/clickhouse.go's own doc comment names
// and leaves to the provider: a value map per column, joined into a shadow
// table, then EXCHANGE TABLES. It is correct rather than merely faster, and
// the reason is a property of masking rather than of ClickHouse: a transform
// is a pure function of the project key, the column identity and the input
// value, so within one column the same value always masks to the same output.
// The DISTINCT values of a column are therefore the whole rewrite, and the
// join applies it to every row that holds one.
//
// It also sidesteps a hazard the per row shape has here. A ClickHouse sorting
// key is not a unique key and usually is not unique at all: an events table
// ordered by (team_id, toDate(timestamp), event) has millions of rows per
// value of its first column. A statement that addresses rows by that key
// rewrites all of them with one row's masked value. This addresses no rows.

// MaskOptions configure a run.
type MaskOptions struct {
	// Key is the project's masking key. The transforms are computed in Go from
	// it and it never reaches the server.
	Key *masking.Key
	// Rules classify the columns.
	Rules *masking.RuleSet
	// RulesHash identifies them, and is carried into the plan.
	RulesHash string
	// Progress receives a line per table, and may be nil.
	Progress func(string)
	// MaxDistinct bounds the number of distinct values one column may hold
	// before this refuses rather than filling memory with them. Zero uses
	// DefaultMaxDistinct.
	MaxDistinct int
}

// DefaultMaxDistinct is where a column stops being masked and starts being
// refused.
//
// The map of a column's distinct values is held in this process, so it is
// bounded by memory rather than by the table's size. Ten million values of a
// uuid is about half a gigabyte of Go strings, which is the point at which a
// laptop starts swapping and the failure stops being legible. Refusing with
// the count is a better answer than an out of memory kill, which says nothing
// about which column caused it.
const DefaultMaxDistinct = 10_000_000

// MaskResult is what a run did.
type MaskResult struct {
	// Rows is how many rows were rewritten, which is every row of every table
	// the plan touched rather than only the ones whose values changed.
	Rows int64
	// Tables is how many tables were rewritten.
	Tables int
	// Values is how many distinct values were masked, across every column.
	// The number this shape is fast because of, so it is reported rather than
	// inferred.
	Values int64
	// Unclassified are the columns no rule covered.
	Unclassified []masking.Assignment
	// CopiedUnchanged names the columns that ship exactly as they were.
	CopiedUnchanged []string
}

// Mask applies a masking plan to a ClickHouse database.
//
// The url names the database, which is the golden candidate: nothing here ever
// writes to a source. A plan with problems is refused rather than partly run,
// which is the same rule the Postgres executor has and for the same reason.
func Mask(ctx context.Context, url secrets.Value, opts MaskOptions) (MaskResult, error) {
	var res MaskResult
	if opts.Key == nil {
		return res, fmt.Errorf("clickhouse: masking needs a key")
	}
	if opts.Rules == nil {
		return res, fmt.Errorf("clickhouse: masking needs a rule set")
	}
	if opts.MaxDistinct <= 0 {
		opts.MaxDistinct = DefaultMaxDistinct
	}
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {}
	}

	c, err := parseURL(url)
	if err != nil {
		return res, err
	}
	tables, skips, err := readCatalog(ctx, c)
	if err != nil {
		return res, err
	}
	for _, s := range skips {
		progress(fmt.Sprintf("not masking %s: %s", s.Name, s.Reason))
	}

	byName := make(map[string]table, len(tables))
	mt := make([]masking.Table, 0, len(tables))
	for _, t := range tables {
		byName[t.name] = t
		mt = append(mt, t.maskingTable(c.database))
	}

	plan := masking.BuildPlan(mt, opts.Rules.Assign(mt), opts.RulesHash)
	res.Unclassified = plan.Unclassified
	res.CopiedUnchanged = plan.CopiedUnchangedNames()
	if !plan.Runnable() {
		return res, aferrors.Coded(aferrors.AFMSK010,
			"detail", masking.DescribeProblems(plan.Problems))
	}

	for _, tp := range plan.Tables {
		rows, values, err := maskTable(ctx, c, byName[tp.Table.Name], tp, opts)
		res.Rows += rows
		res.Values += values
		if err != nil {
			return res, aferrors.Wrap(err, aferrors.AFMSK010, "detail", err.Error())
		}
		res.Tables++
		progress(fmt.Sprintf("masked %s (%d rows, %d distinct values)", tp.Table.Name, rows, values))
	}
	return res, nil
}

// maskTable rewrites one table.
//
// Every statement it runs is named here in order, because the order is the
// safety property: the shadow is filled from the live table, both the join and
// the row count are checked against it, and only then does the name move. A
// failure at any point before the exchange leaves the table exactly as it was.
func maskTable(
	ctx context.Context, c *client, t table, tp masking.TablePlan, opts MaskOptions,
) (rows int64, values int64, err error) {
	shadow := "__af_shadow_" + safeSuffix(t.name)
	maps := make([]string, len(tp.Columns))
	for i := range tp.Columns {
		maps[i] = fmt.Sprintf("__af_map_%s_%d", safeSuffix(t.name), i)
	}
	// Dropped on the way out whatever happened. A leftover map table would be
	// read as a table of the golden by the next catalog reader, which is how
	// this package's own scaffolding would end up in somebody's twin.
	defer func() {
		clean := context.WithoutCancel(ctx)
		_ = c.exec(clean, "DROP TABLE IF EXISTS "+quoteIdent(shadow), nil)
		for _, m := range maps {
			_ = c.exec(clean, "DROP TABLE IF EXISTS "+quoteIdent(m), nil)
		}
	}()

	before, err := countRows(ctx, c, t.name)
	if err != nil {
		return 0, 0, err
	}

	for i, a := range tp.Columns {
		n, err := buildValueMap(ctx, c, t, a, maps[i], opts)
		if err != nil {
			return 0, values, err
		}
		values += n
	}

	if err := c.exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", quoteIdent(shadow)), nil); err != nil {
		return 0, values, err
	}
	if err := c.exec(ctx, fmt.Sprintf("CREATE TABLE %s AS %s",
		quoteIdent(shadow), quoteIdent(t.name)), nil); err != nil {
		return 0, values, err
	}

	from := fromWithJoins(t, tp, maps)
	if err := checkEveryValueMatched(ctx, c, t, tp, maps, from); err != nil {
		return 0, values, err
	}

	cols := t.writableColumns()
	names := make([]string, 0, len(cols))
	exprs := make([]string, 0, len(cols))
	masked := map[string]int{}
	for i, a := range tp.Columns {
		masked[a.Column.Name] = i
	}
	for _, col := range cols {
		names = append(names, quoteIdent(col.name))
		if i, ok := masked[col.name]; ok {
			exprs = append(exprs, maskedExpr(col, i))
			continue
		}
		exprs = append(exprs, "s."+quoteIdent(col.name))
	}

	insert := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s %s",
		quoteIdent(shadow), strings.Join(names, ", "), strings.Join(exprs, ", "), from)
	if err := c.exec(ctx, insert, nil); err != nil {
		return 0, values, err
	}

	after, err := countRows(ctx, c, shadow)
	if err != nil {
		return 0, values, err
	}
	if after != before {
		// The one thing a join can do that a row by row update cannot: lose
		// rows or multiply them. Checked before the exchange, so a table that
		// fails this is left exactly as it was rather than replaced by a
		// wrong one.
		return 0, values, fmt.Errorf(
			"the masked copy of %s holds %d rows and the table holds %d; the rewrite is "+
				"not applied and the table is unchanged", t.name, after, before)
	}

	if err := c.exec(ctx, fmt.Sprintf("EXCHANGE TABLES %s AND %s",
		quoteIdent(shadow), quoteIdent(t.name)), nil); err != nil {
		return 0, values, err
	}
	return before, values, nil
}

// buildValueMap reads the distinct values of one column, masks each once, and
// writes the pairs into a table the rewrite joins against.
func buildValueMap(
	ctx context.Context, c *client, t table, a masking.Assignment, name string, opts MaskOptions,
) (int64, error) {
	transform, ok := masking.Lookup(a.Transform)
	if !ok {
		return 0, fmt.Errorf("no transform called %s", a.Transform)
	}
	// The identity a key is derived from, and it is built exactly the way the
	// Postgres executor builds it: the plan's own table and column, and the
	// link where the rules declared one. One customer masks to one fake
	// customer across both stores because this identity does not name the
	// store, and a field spelled differently here would be the whole cross
	// store guarantee quietly broken for ClickHouse only.
	col := masking.Column{
		Schema: a.Table.Schema, Table: a.Table.Name, Name: a.Column.Name, Link: a.Link,
	}

	nullable := a.Column.Nullable
	var buf bytes.Buffer
	count := int64(0)
	read := fmt.Sprintf("SELECT DISTINCT toString(%s) FROM %s WHERE isNotNull(%s)",
		quoteIdent(a.Column.Name), quoteIdent(t.name), quoteIdent(a.Column.Name))
	err := c.rows(ctx, read, nil, func(r [][]byte) error {
		if len(r) != 1 {
			return fmt.Errorf("reading the values of %s returned %d columns", a.Column.Name, len(r))
		}
		if r[0] == nil {
			// isNotNull already excluded these. A null arriving here would
			// mean the filter did not hold, and masking a null as though it
			// were a value would turn every absent value into a present one.
			return fmt.Errorf("the values of %s included a null after they were filtered out",
				a.Column.Name)
		}
		count++
		if count > int64(opts.MaxDistinct) {
			return fmt.Errorf(
				"%s.%s holds more than %d distinct values, which is the point at which this "+
					"stops holding the map of them in memory; mask it with a transform that "+
					"does not depend on the value, or raise the limit",
				t.name, a.Column.Name, opts.MaxDistinct)
		}
		in := string(r[0])
		out, err := transform.Apply(opts.Key, col, &in)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", t.name, a.Column.Name, err)
		}
		if out == nil && !nullable {
			// The write would put an empty string in and call it masked. The
			// Postgres path gets a not null violation from the server here,
			// which is the same refusal arriving from the other side.
			return fmt.Errorf(
				"the %s transform emptied a value of %s.%s and the column does not accept "+
					"null, so there is nothing to write in its place",
				a.Transform, t.name, a.Column.Name)
		}
		writeMapRow(&buf, in, out)
		return nil
	})
	if err != nil {
		return count, err
	}

	if err := c.exec(ctx, "DROP TABLE IF EXISTS "+quoteIdent(name), nil); err != nil {
		return count, err
	}
	// hit is what tells an unmatched row from a value masked to null. A LEFT
	// JOIN that finds nothing fills every column with its default, and the
	// default of a Nullable(String) is null, which is exactly what the nullify
	// transform produces. Without this column the two are the same answer.
	if err := c.exec(ctx, fmt.Sprintf(
		"CREATE TABLE %s (k String, v Nullable(String), hit UInt8) ENGINE = MergeTree ORDER BY k",
		quoteIdent(name)), nil); err != nil {
		return count, err
	}
	if count == 0 {
		return 0, nil
	}
	write := fmt.Sprintf("INSERT INTO %s (k, v, hit) FORMAT RowBinary", quoteIdent(name))
	if err := c.insert(ctx, write, &buf); err != nil {
		return count, err
	}
	return count, nil
}

// writeMapRow appends one pair in RowBinary.
//
// RowBinary rather than a text format because the values are production's own:
// a tab, a newline, a backslash or a quote in one of them is data, and a
// format that escapes would be one more place for an escaping rule to be
// wrong. A length prefix has no escaping rule.
func writeMapRow(buf *bytes.Buffer, k string, v *string) {
	writeBinaryString(buf, k)
	if v == nil {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
		writeBinaryString(buf, *v)
	}
	buf.WriteByte(1) // hit
}

func writeBinaryString(buf *bytes.Buffer, s string) {
	var n [binary.MaxVarintLen64]byte
	buf.Write(n[:binary.PutUvarint(n[:], uint64(len(s)))])
	buf.WriteString(s)
}

// fromWithJoins is the FROM clause the check and the rewrite share.
//
// One string used by both, because the check is only worth anything if it
// checks the join the rewrite performs. Two copies would be two joins that
// agree until somebody edits one.
func fromWithJoins(t table, tp masking.TablePlan, maps []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s AS s", quoteIdent(t.name))
	for i, a := range tp.Columns {
		fmt.Fprintf(&b, " LEFT JOIN %s AS m%d ON toString(s.%s) = m%d.k",
			quoteIdent(maps[i]), i, quoteIdent(a.Column.Name), i)
	}
	return b.String()
}

// checkEveryValueMatched refuses a rewrite in which any row's value did not
// find its masked form.
//
// Without it, a join that matched nothing would write null over every value of
// a nullable column and an empty string over every value of the rest, and both
// would look like masking. It is the instrument that can say no about the one
// thing this shape can get wrong that the row by row shape cannot.
func checkEveryValueMatched(
	ctx context.Context, c *client, t table, tp masking.TablePlan, maps []string, from string,
) error {
	if len(tp.Columns) == 0 {
		return nil
	}
	sums := make([]string, 0, len(tp.Columns))
	for i, a := range tp.Columns {
		sums = append(sums, fmt.Sprintf("toString(countIf(isNotNull(s.%s) AND m%d.hit = 0))",
			quoteIdent(a.Column.Name), i))
	}
	var missed []string
	err := c.rows(ctx, "SELECT "+strings.Join(sums, ", ")+" "+from, nil, func(r [][]byte) error {
		for i, v := range r {
			n, _ := strconv.ParseInt(string(v), 10, 64)
			if n > 0 {
				missed = append(missed, fmt.Sprintf("%s (%d rows)", tp.Columns[i].Column.Name, n))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(missed) > 0 {
		return fmt.Errorf(
			"the masked values of %s did not reach every row of %s, so the rewrite would "+
				"have written an empty value over real data; the table is unchanged",
			strings.Join(missed, ", "), t.name)
	}
	return nil
}

// maskedExpr is what one masked column becomes in the rewrite.
//
// Always a CAST to the column's own type, including where the type is text. A
// masked value is a String and the column may be a UUID, a LowCardinality or
// an IPv4, and ClickHouse does not put a String into any of those on its own.
// Casting unconditionally means there is one rule here rather than a list of
// types that need one, and a list is what goes stale.
//
// assumeNotNull for a column that does not accept null, and it asserts nothing
// the caller has not already proved: every non null value of the column is in
// the map, checkEveryValueMatched has shown that every row found its entry,
// and buildValueMap refuses a transform that empties a value of a column with
// no null to put there.
func maskedExpr(col column, i int) string {
	value := fmt.Sprintf("m%d.v", i)
	if !col.nullable() {
		value = "assumeNotNull(" + value + ")"
	}
	return fmt.Sprintf("CAST(%s AS %s)", value, col.typ)
}

// safeSuffix turns a table name into something that can be part of an
// identifier this package generates.
//
// A hash rather than a sanitisation, because two tables whose names differ
// only in a character this would have replaced must not produce one name.
func safeSuffix(name string) string {
	return shortHash(name)
}
