package masking

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The catalog is read from the database rather than declared in the manifest,
// and that is the whole design.
//
// A masking configuration that lists the columns to mask is a configuration
// that goes stale the moment somebody adds one, and the failure mode of a
// stale list is silent: the new column holds real email addresses and nothing
// says so. Reading the live schema means a column that appeared yesterday is
// classified today, and a column nobody wrote a rule for is reported rather
// than skipped.

// Table is one table and its columns.
type Table struct {
	Schema string
	Name   string
	// Columns are in ordinal order, which is the order a person reading the
	// table would see them.
	Columns []ColumnInfo
	// PrimaryKey is the columns of the primary key, in order. Empty when the
	// table has none, which matters because chunked updates need something to
	// order by.
	PrimaryKey []string
	// Rows is the planner's estimate, used to size chunks and to report
	// progress. It is an estimate and is treated as one.
	Rows int64
}

// ColumnNamed returns a column by name, and the zero value when there is
// none, which is what a caller checking a field wants rather than an error.
func (t Table) ColumnNamed(name string) ColumnInfo {
	for _, c := range t.Columns {
		if c.Name == name {
			return c
		}
	}
	return ColumnInfo{}
}

// Qualified returns the schema qualified, quoted name.
func (t Table) Qualified() string { return quoteIdent(t.Schema) + "." + quoteIdent(t.Name) }

// String returns the readable name, for messages.
func (t Table) String() string { return t.Schema + "." + t.Name }

// ColumnInfo is one column as the database describes it.
type ColumnInfo struct {
	Name string
	// Type is the Postgres type name, such as text or timestamptz.
	Type string
	// Nullable reports whether the column accepts null, which decides whether
	// nullify is available for it.
	Nullable bool
	// Unique reports whether a unique constraint or index covers this column
	// alone. A transform that does not preserve uniqueness cannot be used on
	// one, because the update would fail halfway through and leave the table
	// half masked.
	Unique bool
	// ForeignKey names the column this one references, as schema.table.column,
	// when it does. Two columns joined by a foreign key have to mask
	// identically or the join breaks, and this is how the planner knows.
	ForeignKey string
	// Generated reports whether the database computes this column, in which
	// case it cannot be written to at all.
	Generated bool
}

// ReadCatalog reads the tables and columns of a database.
//
// It reads only what the user owns: system schemas are excluded, because
// masking pg_catalog is neither possible nor desirable, and a plan that listed
// them would bury the tables somebody actually has to look at.
func ReadCatalog(ctx context.Context, conn *pgx.Conn) ([]Table, error) {
	const query = `
SELECT c.table_schema, c.table_name, c.column_name, c.data_type,
       c.is_nullable = 'YES' AS nullable,
       c.is_generated <> 'NEVER' OR c.identity_generation IS NOT NULL AS generated,
       c.ordinal_position
FROM information_schema.columns c
JOIN information_schema.tables t
  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
WHERE t.table_type = 'BASE TABLE'
  AND c.table_schema NOT IN ('pg_catalog', 'information_schema')
  AND c.table_schema NOT LIKE 'pg_toast%'
ORDER BY c.table_schema, c.table_name, c.ordinal_position`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("masking: reading the catalog: %w", err)
	}
	defer rows.Close()

	byTable := map[string]*Table{}
	var order []string
	for rows.Next() {
		var schema, table, column, dataType string
		var nullable, generated bool
		var position int
		if err := rows.Scan(&schema, &table, &column, &dataType, &nullable, &generated, &position); err != nil {
			return nil, fmt.Errorf("masking: reading the catalog: %w", err)
		}
		key := schema + "." + table
		if _, ok := byTable[key]; !ok {
			byTable[key] = &Table{Schema: schema, Name: table}
			order = append(order, key)
		}
		byTable[key].Columns = append(byTable[key].Columns, ColumnInfo{
			Name: column, Type: dataType, Nullable: nullable, Generated: generated,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("masking: reading the catalog: %w", err)
	}

	if err := readKeys(ctx, conn, byTable); err != nil {
		return nil, err
	}
	if err := readUnique(ctx, conn, byTable); err != nil {
		return nil, err
	}
	if err := readEstimates(ctx, conn, byTable); err != nil {
		return nil, err
	}

	out := make([]Table, 0, len(order))
	for _, key := range order {
		out = append(out, *byTable[key])
	}
	return out, nil
}

// readKeys fills in primary keys and foreign key references.
func readKeys(ctx context.Context, conn *pgx.Conn, tables map[string]*Table) error {
	const query = `
-- contype is Postgres's internal single byte char type, which does not scan
-- into a string, so it is cast rather than decoded specially.
SELECT ns.nspname, cl.relname, con.contype::text,
       att.attname,
       fns.nspname, fcl.relname, fatt.attname,
       k.ord
FROM pg_constraint con
JOIN pg_class cl ON cl.oid = con.conrelid
JOIN pg_namespace ns ON ns.oid = cl.relnamespace
JOIN LATERAL unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord) ON TRUE
JOIN pg_attribute att ON att.attrelid = con.conrelid AND att.attnum = k.attnum
LEFT JOIN pg_class fcl ON fcl.oid = con.confrelid
LEFT JOIN pg_namespace fns ON fns.oid = fcl.relnamespace
LEFT JOIN LATERAL unnest(con.confkey) WITH ORDINALITY AS fk(attnum, ord) ON fk.ord = k.ord
LEFT JOIN pg_attribute fatt ON fatt.attrelid = con.confrelid AND fatt.attnum = fk.attnum
WHERE con.contype IN ('p', 'f')
  AND ns.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY ns.nspname, cl.relname, con.contype, k.ord`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("masking: reading keys: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, table, contype, column string
		var fSchema, fTable, fColumn *string
		var ord int
		if err := rows.Scan(&schema, &table, &contype, &column,
			&fSchema, &fTable, &fColumn, &ord); err != nil {
			return fmt.Errorf("masking: reading keys: %w", err)
		}
		t, ok := tables[schema+"."+table]
		if !ok {
			continue
		}
		switch contype {
		case "p":
			t.PrimaryKey = append(t.PrimaryKey, column)
		case "f":
			if fSchema == nil || fTable == nil || fColumn == nil {
				continue
			}
			for i := range t.Columns {
				if t.Columns[i].Name == column {
					t.Columns[i].ForeignKey = *fSchema + "." + *fTable + "." + *fColumn
				}
			}
		}
	}
	return rows.Err()
}

// readUnique marks columns covered by a single column unique constraint.
func readUnique(ctx context.Context, conn *pgx.Conn, tables map[string]*Table) error {
	const query = `
SELECT ns.nspname, cl.relname, att.attname
FROM pg_index idx
JOIN pg_class cl ON cl.oid = idx.indrelid
JOIN pg_namespace ns ON ns.oid = cl.relnamespace
JOIN pg_attribute att ON att.attrelid = idx.indrelid AND att.attnum = idx.indkey[0]
WHERE idx.indisunique AND array_length(idx.indkey::int[], 1) = 1
  AND ns.nspname NOT IN ('pg_catalog', 'information_schema')`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("masking: reading unique constraints: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, table, column string
		if err := rows.Scan(&schema, &table, &column); err != nil {
			return fmt.Errorf("masking: reading unique constraints: %w", err)
		}
		t, ok := tables[schema+"."+table]
		if !ok {
			continue
		}
		for i := range t.Columns {
			if t.Columns[i].Name == column {
				t.Columns[i].Unique = true
			}
		}
	}
	return rows.Err()
}

// readEstimates fills in the planner's row counts.
//
// The estimate rather than a count, because counting every row of every table
// to decide how to chunk them would read the whole database before masking any
// of it, and the number is only used to size the work.
func readEstimates(ctx context.Context, conn *pgx.Conn, tables map[string]*Table) error {
	const query = `
SELECT ns.nspname, cl.relname, GREATEST(cl.reltuples, 0)::bigint
FROM pg_class cl
JOIN pg_namespace ns ON ns.oid = cl.relnamespace
WHERE cl.relkind = 'r' AND ns.nspname NOT IN ('pg_catalog', 'information_schema')`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("masking: reading row estimates: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, table string
		var estimate int64
		if err := rows.Scan(&schema, &table, &estimate); err != nil {
			return fmt.Errorf("masking: reading row estimates: %w", err)
		}
		if t, ok := tables[schema+"."+table]; ok {
			t.Rows = estimate
		}
	}
	return rows.Err()
}

// quoteIdent quotes an identifier for use in a statement.
//
// Every identifier that reaches SQL goes through this, including ones that
// came from the catalog. They are not user input today, and a table named by
// somebody who read this code and added a path from the manifest would be, so
// the quoting is unconditional rather than reasoned about per call site.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// SortTables orders tables so that a person reading a plan sees them the same
// way twice.
func SortTables(tables []Table) {
	sort.Slice(tables, func(i, j int) bool {
		if tables[i].Schema != tables[j].Schema {
			return tables[i].Schema < tables[j].Schema
		}
		return tables[i].Name < tables[j].Name
	})
}
