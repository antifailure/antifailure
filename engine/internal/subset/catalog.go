package subset

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Catalog is the shape of a database, as subsetting needs to see it.
//
// It is read here rather than taken from internal/masking, which has a catalog
// of its own, because the two need different things and the difference is not
// cosmetic. Masking needs to know, per column, whether some other column is
// joined to it, so that two columns either side of a key mask identically; a
// flat "this column references that one" is exactly right for that. Subsetting
// needs the constraint as a unit, because a composite key (order_id, line_no)
// referencing (id, line_no) is one condition and not two. Splitting it into two
// independent conditions takes rows whose first column matches one parent and
// whose second matches a different one, which is a subset that passes a naive
// check and violates the constraint it was built from.
type Catalog struct {
	Tables  []Table
	Keys    []ForeignKey
	Seqs    []Sequence
	Version int
}

// Table is one table's shape.
type Table struct {
	Schema string
	Name   string
	// Columns are in ordinal order, which is the order COPY uses.
	Columns []Column
	// PrimaryKey is the columns of the primary key, in order. Empty when there
	// is none, which decides whether the table can be truncated deterministically.
	PrimaryKey []string
	// Rows is the planner's estimate. It is an estimate and is treated as one:
	// it decides whether to refuse a table, never how many rows to take.
	Rows int64
	// Partitioned marks a partitioned parent. Its partitions are not in the
	// catalog at all: rows are routed through the parent, so a plan that
	// listed both would copy everything twice.
	Partitioned bool
}

// Ref is the qualified name, unquoted, as it appears in a plan and a message.
func (t Table) Ref() string { return t.Schema + "." + t.Name }

// Qualified is the quoted name, as it appears in SQL.
func (t Table) Qualified() string { return quote(t.Schema) + "." + quote(t.Name) }

// Column is one column's shape.
type Column struct {
	Name string
	Type string
	// Nullable decides whether a reference that does not resolve can be
	// repaired by nulling it or has to be repaired by removing the row.
	Nullable bool
	// Generated marks a column the database computes. COPY refuses to write
	// one at all ("generated columns cannot be used in COPY"), so a copy that
	// enumerated every column would fail on the first table that had one.
	// Identity columns are not generated in this sense and COPY does accept a
	// value for them, which was checked against a real Postgres rather than
	// assumed, because the two look alike in the catalog and behave differently.
	Generated bool
}

// ForeignKey is one constraint, whole.
type ForeignKey struct {
	// Name is the constraint's name, or "virtual" for a declared one.
	Name string
	// From is the table holding the key, To the table it points at, both
	// qualified.
	From        string
	FromColumns []string
	To          string
	ToColumns   []string
	// Virtual marks a relationship the manifest declared rather than one the
	// schema enforces. They are followed identically and reported separately,
	// because a wrong virtual relationship produces a broken subset and the
	// schema cannot catch it.
	Virtual bool
}

// String renders a key for a plan.
func (k ForeignKey) String() string {
	arrow := "->"
	if k.Virtual {
		arrow = "~>"
	}
	return fmt.Sprintf("%s(%s) %s %s(%s)",
		k.From, strings.Join(k.FromColumns, ", "), arrow,
		k.To, strings.Join(k.ToColumns, ", "))
}

// SelfReference reports whether a key points at its own table. A self
// reference cannot be satisfied by copy order, because the rows it needs are
// the rows being copied, so it is always deferred and repaired afterwards.
func (k ForeignKey) SelfReference() bool { return k.From == k.To }

// Sequence is a sequence and the column that owns it, when one does.
type Sequence struct {
	// Ref is the qualified sequence name.
	Ref string
	// Table and Column name the owning column, empty for a standalone sequence.
	Table  string
	Column string
}

// ReadCatalog reads the shape of a database.
//
// Only what the user owns: system schemas are excluded, and so are partitions,
// which are reached through their parent.
func ReadCatalog(ctx context.Context, conn *pgx.Conn) (*Catalog, error) {
	cat := &Catalog{}
	if err := conn.QueryRow(ctx, "SHOW server_version_num").Scan(&cat.Version); err != nil {
		// Not fatal. The version is used for a message, not for a decision.
		cat.Version = 0
	}
	if err := readTables(ctx, conn, cat); err != nil {
		return nil, err
	}
	if err := readForeignKeys(ctx, conn, cat); err != nil {
		return nil, err
	}
	if err := readSequences(ctx, conn, cat); err != nil {
		return nil, err
	}
	return cat, nil
}

// systemSchemas is the exclusion every query below shares.
const systemSchemas = `n.nspname NOT IN ('pg_catalog', 'information_schema')
	  AND n.nspname NOT LIKE 'pg_toast%' AND n.nspname NOT LIKE 'pg_temp%'`

func readTables(ctx context.Context, conn *pgx.Conn, cat *Catalog) error {
	// relkind 'r' is an ordinary table and 'p' a partitioned one. A partition
	// is an ordinary table with relispartition set, and is excluded: its rows
	// arrive through the parent, and copying both would copy them twice.
	//
	// attgenerated is '' for an ordinary column and 's' for a stored generated
	// one. Identity columns are deliberately not treated as generated: COPY
	// accepts a value for an identity column and refuses one for a generated
	// column, which is the whole reason this flag exists.
	const query = `
SELECT n.nspname, c.relname, c.relkind = 'p' AS partitioned,
       COALESCE(c.reltuples, 0)::bigint AS rows,
       a.attname, format_type(a.atttypid, a.atttypmod) AS type,
       NOT a.attnotnull AS nullable, a.attgenerated <> '' AS generated
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
WHERE c.relkind IN ('r', 'p')
  AND NOT c.relispartition
  AND ` + systemSchemas + `
ORDER BY n.nspname, c.relname, a.attnum`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("subset: reading the catalog: %w", err)
	}
	defer rows.Close()

	byRef := map[string]*Table{}
	var order []string
	for rows.Next() {
		var schema, name, col, typ string
		var partitioned, nullable, generated bool
		var estimate int64
		if err := rows.Scan(&schema, &name, &partitioned, &estimate,
			&col, &typ, &nullable, &generated); err != nil {
			return fmt.Errorf("subset: reading the catalog: %w", err)
		}
		ref := schema + "." + name
		if _, ok := byRef[ref]; !ok {
			byRef[ref] = &Table{Schema: schema, Name: name, Partitioned: partitioned, Rows: estimate}
			order = append(order, ref)
		}
		byRef[ref].Columns = append(byRef[ref].Columns, Column{
			Name: col, Type: typ, Nullable: nullable, Generated: generated,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("subset: reading the catalog: %w", err)
	}

	if err := readPrimaryKeys(ctx, conn, byRef); err != nil {
		return err
	}
	if err := readEstimates(ctx, conn, byRef); err != nil {
		return err
	}
	for _, ref := range order {
		cat.Tables = append(cat.Tables, *byRef[ref])
	}
	return nil
}

func readPrimaryKeys(ctx context.Context, conn *pgx.Conn, byRef map[string]*Table) error {
	const query = `
SELECT n.nspname, c.relname, a.attname, k.ord
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN LATERAL unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord) ON TRUE
JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum
WHERE con.contype = 'p' AND ` + systemSchemas + `
ORDER BY n.nspname, c.relname, k.ord`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("subset: reading primary keys: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var schema, table, col string
		var ord int
		if err := rows.Scan(&schema, &table, &col, &ord); err != nil {
			return fmt.Errorf("subset: reading primary keys: %w", err)
		}
		if t, ok := byRef[schema+"."+table]; ok {
			t.PrimaryKey = append(t.PrimaryKey, col)
		}
	}
	return rows.Err()
}

// readEstimates replaces the reltuples estimate with a live count for tables
// the planner has never analyzed, where reltuples is -1.
//
// It matters because the estimate decides whether a table with no primary key
// is refused, and refusing a freshly restored table that has never been
// analyzed, purely because nobody has run ANALYZE yet, would make the first
// run of any new source fail for a reason that has nothing to do with the data.
func readEstimates(ctx context.Context, conn *pgx.Conn, byRef map[string]*Table) error {
	var unanalyzed []*Table
	for _, t := range byRef {
		if t.Rows < 0 {
			unanalyzed = append(unanalyzed, t)
		}
	}
	sort.Slice(unanalyzed, func(i, j int) bool { return unanalyzed[i].Ref() < unanalyzed[j].Ref() })
	for _, t := range unanalyzed {
		var n int64
		// Bounded, because the point is to decide whether the table is small,
		// and counting two billion rows to find out that it is not is the
		// thing being avoided.
		q := fmt.Sprintf("SELECT count(*) FROM (SELECT 1 FROM %s LIMIT %d) s",
			t.Qualified(), countCeiling)
		if err := conn.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Rows = 0
			continue
		}
		t.Rows = n
	}
	return nil
}

// countCeiling is where the bounded count stops. Anything at the ceiling is
// reported as at least that many, which is all the decision needs.
const countCeiling = 200000

func readForeignKeys(ctx context.Context, conn *pgx.Conn, cat *Catalog) error {
	// conkey and confkey are parallel arrays, so they are unnested together on
	// ordinality and reassembled per constraint. Reading them one column at a
	// time is what loses a composite key.
	const query = `
SELECT con.conname, n.nspname, c.relname, fn.nspname, fc.relname,
       a.attname, fa.attname, k.ord
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_class fc ON fc.oid = con.confrelid
JOIN pg_namespace fn ON fn.oid = fc.relnamespace
JOIN LATERAL unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord) ON TRUE
JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum
JOIN LATERAL unnest(con.confkey) WITH ORDINALITY AS f(attnum, ord) ON f.ord = k.ord
JOIN pg_attribute fa ON fa.attrelid = con.confrelid AND fa.attnum = f.attnum
WHERE con.contype = 'f' AND ` + systemSchemas + `
ORDER BY n.nspname, c.relname, con.conname, k.ord`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("subset: reading foreign keys: %w", err)
	}
	defer rows.Close()

	byName := map[string]*ForeignKey{}
	var order []string
	for rows.Next() {
		var name, schema, table, fSchema, fTable, col, fCol string
		var ord int
		if err := rows.Scan(&name, &schema, &table, &fSchema, &fTable, &col, &fCol, &ord); err != nil {
			return fmt.Errorf("subset: reading foreign keys: %w", err)
		}
		// Constraint names are unique per table, not per database, so the key
		// carries the table too.
		id := schema + "." + table + "." + name
		if _, ok := byName[id]; !ok {
			byName[id] = &ForeignKey{
				Name: name, From: schema + "." + table, To: fSchema + "." + fTable,
			}
			order = append(order, id)
		}
		k := byName[id]
		k.FromColumns = append(k.FromColumns, col)
		k.ToColumns = append(k.ToColumns, fCol)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("subset: reading foreign keys: %w", err)
	}
	for _, id := range order {
		cat.Keys = append(cat.Keys, *byName[id])
	}
	return nil
}

func readSequences(ctx context.Context, conn *pgx.Conn, cat *Catalog) error {
	// deptype 'a' is a sequence owned by a column through OWNED BY, and 'i' is
	// the internal dependency an identity column has on its sequence. Both are
	// sequences whose value has to move past the largest copied key, and
	// leaving out the identity case is how a subset loads cleanly and then
	// fails on the first insert the application makes.
	const query = `
SELECT n.nspname, s.relname, COALESCE(tn.nspname, ''), COALESCE(t.relname, ''), COALESCE(a.attname, '')
FROM pg_class s
JOIN pg_namespace n ON n.oid = s.relnamespace
LEFT JOIN pg_depend d
  ON d.objid = s.oid AND d.classid = 'pg_class'::regclass AND d.deptype IN ('a', 'i')
LEFT JOIN pg_class t ON t.oid = d.refobjid
LEFT JOIN pg_namespace tn ON tn.oid = t.relnamespace
LEFT JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = d.refobjsubid
WHERE s.relkind = 'S' AND ` + systemSchemas + `
ORDER BY n.nspname, s.relname`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("subset: reading sequences: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var schema, name, tSchema, tName, col string
		if err := rows.Scan(&schema, &name, &tSchema, &tName, &col); err != nil {
			return fmt.Errorf("subset: reading sequences: %w", err)
		}
		seq := Sequence{Ref: schema + "." + name}
		if tName != "" && col != "" {
			seq.Table = tSchema + "." + tName
			seq.Column = col
		}
		cat.Seqs = append(cat.Seqs, seq)
	}
	return rows.Err()
}

// TableNamed resolves a name that may or may not carry a schema.
//
// An unqualified name is accepted because that is what people write, and it
// resolves only when it is unambiguous: two tables of the same name in
// different schemas is a real situation, and picking one of them silently is
// how a subset of the wrong database gets taken.
func (c *Catalog) TableNamed(name string) (Table, error) {
	if name == "" {
		return Table{}, fmt.Errorf("subset: no seed table is configured, so there is nothing to start from")
	}
	var matches []Table
	for _, t := range c.Tables {
		if t.Ref() == name || (!strings.Contains(name, ".") && t.Name == name) {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Table{}, fmt.Errorf(
			"subset: the seed table %q is not in this database; it has %s",
			name, c.describe(8))
	default:
		refs := make([]string, 0, len(matches))
		for _, m := range matches {
			refs = append(refs, m.Ref())
		}
		return Table{}, fmt.Errorf(
			"subset: %q is ambiguous: there is one in each of %s. Qualify it with the schema",
			name, strings.Join(refs, " and "))
	}
}

// Table returns a table by its qualified name.
func (c *Catalog) Table(ref string) (Table, bool) {
	for _, t := range c.Tables {
		if t.Ref() == ref {
			return t, true
		}
	}
	return Table{}, false
}

func (c *Catalog) describe(limit int) string {
	names := make([]string, 0, len(c.Tables))
	for _, t := range c.Tables {
		names = append(names, t.Ref())
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "no tables at all"
	}
	if len(names) <= limit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:limit], ", "), len(names)-limit)
}

// Writable returns the columns COPY can carry, in ordinal order.
//
// Generated columns are excluded because COPY refuses them outright, and the
// database recomputes them from the columns that were copied, so nothing is
// lost by leaving them out.
func (t Table) Writable() []string {
	out := make([]string, 0, len(t.Columns))
	for _, c := range t.Columns {
		if c.Generated {
			continue
		}
		out = append(out, c.Name)
	}
	return out
}

// ColumnNamed returns a column by name, and false when there is none.
func (t Table) ColumnNamed(name string) (Column, bool) {
	for _, c := range t.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = quote(n)
	}
	return out
}
