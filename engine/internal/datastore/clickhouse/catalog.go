package clickhouse

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/masking"
)

// The catalog reader, which is what turns a ClickHouse database into the
// tables the masking planner classifies.
//
// It is here rather than in the masking package for the reason that package's
// dialect comment gives: masking knows what a type NAME means and how a
// statement is written, and the thing that opens a connection is the provider.
// This is the provider's half.

// table is one table of a database, as this package needs it.
//
// masking.Table carries what the planner needs, and this carries what the
// rewrite needs on top of it: every column rather than only the ones a rule
// might match, because the shadow table is written from all of them, and the
// engine, because only the MergeTree family can have its partitions attached
// to a branch.
type table struct {
	name string
	// engine is ClickHouse's own engine name, such as MergeTree.
	engine string
	// columns are every column in position order, including the ones no rule
	// will ever match. writable() is the subset a statement may write.
	columns []column
	// sortingKey is the plain column names of the sorting key, in order, with
	// the expressions dropped.
	sortingKey []string
	rows       int64
}

// column is one column of a table.
type column struct {
	name string
	// typ is ClickHouse's own name for the type, verbatim, which is what a
	// CAST has to name and what a person reading a report will find in their
	// own schema.
	typ string
	// defaultKind is DEFAULT, MATERIALIZED, ALIAS or empty. The last two are
	// computed by the server, so a statement may neither write them nor read
	// them back as data.
	defaultKind string
}

func (c column) nullable() bool {
	_, n := unwrapType(c.typ)
	return n
}

// writable reports whether a statement may insert into this column.
func (c column) writable() bool {
	switch strings.ToUpper(strings.TrimSpace(c.defaultKind)) {
	case "ALIAS", "MATERIALIZED":
		return false
	default:
		return true
	}
}

// writableColumns is the subset of a table's columns a shadow table is written
// from.
func (t table) writableColumns() []column {
	out := make([]column, 0, len(t.columns))
	for _, c := range t.columns {
		if c.writable() {
			out = append(out, c)
		}
	}
	return out
}

// maskingTable is the shape the planner classifies.
//
// Only the writable columns, because a masking plan that assigned a transform
// to an ALIAS column would produce a statement nothing can run: the server
// computes the value and refuses the write. A person who wants an alias masked
// has to mask what it is computed from, which is a real column.
func (t table) maskingTable(database string) masking.Table {
	cols := make([]masking.ColumnInfo, 0, len(t.columns))
	for _, c := range t.writableColumns() {
		cols = append(cols, masking.ColumnInfo{
			Name: c.name, Type: c.typ, Nullable: c.nullable(),
		})
	}
	return masking.Table{
		Engine: engineName, Schema: database, Name: t.name,
		Columns: cols, PrimaryKey: append([]string(nil), t.sortingKey...), Rows: t.rows,
	}
}

// skipped is a table the golden does not hold, and why.
//
// Recorded rather than dropped. A store whose contents are a subset of
// production's is a legitimate golden and an undeclared one is exactly the
// blank ClickHouse this whole wave exists to stop, so the list travels with
// the version and is printed when one is made.
type skipped struct {
	Name   string `json:"name"`
	Engine string `json:"engine"`
	Reason string `json:"reason"`
}

// readCatalog lists the tables of one database.
//
// Scoped to a database because everything this package does is: a golden is a
// database and a branch is a database, and a reader that walked the server
// would read somebody else's.
func readCatalog(ctx context.Context, c *client) ([]table, []skipped, error) {
	type entry struct {
		t       *table
		skipped string
	}
	byName := map[string]*entry{}
	var order []string

	err := c.rows(ctx, `
SELECT name, engine, sorting_key, toString(ifNull(total_rows, 0))
FROM system.tables
WHERE database = currentDatabase()
ORDER BY name`, nil, func(r [][]byte) error {
		name, engine := string(r[0]), string(r[1])
		rows, _ := strconv.ParseInt(string(r[3]), 10, 64)
		e := &entry{t: &table{
			name: name, engine: engine,
			sortingKey: plainSortingKeyColumns(string(r[2])),
			rows:       rows,
		}}
		e.skipped = whyNotCopyable(name, engine)
		byName[name] = e
		order = append(order, name)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	err = c.rows(ctx, `
SELECT table, name, type, default_kind
FROM system.columns
WHERE database = currentDatabase()
ORDER BY table, position`, nil, func(r [][]byte) error {
		e, ok := byName[string(r[0])]
		if !ok {
			return nil
		}
		e.t.columns = append(e.t.columns, column{
			name: string(r[1]), typ: string(r[2]), defaultKind: string(r[3]),
		})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	var tables []table
	var skips []skipped
	for _, name := range order {
		e := byName[name]
		if e.skipped != "" {
			skips = append(skips, skipped{Name: name, Engine: e.t.engine, Reason: e.skipped})
			continue
		}
		if len(e.t.writableColumns()) == 0 {
			skips = append(skips, skipped{Name: name, Engine: e.t.engine,
				Reason: "every column of it is computed by the server, so there is nothing to write"})
			continue
		}
		tables = append(tables, *e.t)
	}
	return tables, skips, nil
}

// whyNotCopyable says why a table is not part of a golden, or returns empty
// when it is.
//
// The MergeTree family and nothing else, and that is a deliberate limit rather
// than an oversight. A branch here is made by attaching the golden's
// partitions, which only that family has; a view has no rows of its own; a
// Kafka or a Distributed engine is a pointer at something outside the server
// and copying it would copy the pointer rather than the data, which is the
// worst of the three outcomes because it would look like it worked.
func whyNotCopyable(name, engine string) string {
	if strings.HasPrefix(name, ".") {
		return "it is the inner table of a materialized view, which is copied with the view " +
			"rather than on its own"
	}
	if strings.HasSuffix(engine, "MergeTree") {
		return ""
	}
	return "its engine is " + engine + ", and a golden holds the MergeTree family, whose " +
		"partitions can be attached to a branch"
}

// plainSortingKeyColumns takes the column names out of a sorting key and drops
// the expressions.
//
// A ClickHouse sorting key is a list of EXPRESSIONS, and an analytics table's
// is usually mostly expressions: ORDER BY (team_id, toDate(timestamp), event,
// cityHash64(distinct_id)). Only the plain names are column names, and handing
// the planner "toDate(timestamp)" as a column would produce a statement naming
// an identifier that does not exist.
func plainSortingKeyColumns(key string) []string {
	var out []string
	for _, part := range splitTopLevel(key) {
		part = strings.TrimSpace(part)
		if part == "" || !isPlainIdentifier(part) {
			continue
		}
		out = append(out, part)
	}
	return out
}

// splitTopLevel splits on commas that are not inside brackets, so that
// cityHash64(a, b) stays one part.
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// isPlainIdentifier reports whether a sorting key part is a bare column name.
func isPlainIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// countRows reads the exact row count of a table, which the row count check
// after a rewrite compares against.
//
// Exact rather than the estimate in system.tables, because the check exists to
// notice a rewrite that lost rows and an estimate cannot tell a lost row from
// a stale statistic.
func countRows(ctx context.Context, c *client, name string) (int64, error) {
	v, err := c.value(ctx, "SELECT toString(count()) FROM "+quoteIdent(name), nil)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("clickhouse: the row count of %s came back as %q", name, v)
	}
	return n, nil
}
