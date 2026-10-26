package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// What a database holds, read from information_schema.
//
// One reader for the three callers that need it: the copy, which needs the
// columns it may write; masking, which needs the types, the keys and the
// relationships; and the cross store check, which needs the same tables masking
// would see.

// table is one base table.
type table struct {
	name    string
	engine  string
	columns []column
	// primaryKey is the primary key's columns, in key order.
	primaryKey []string
	// rows is information_schema's figure, which for InnoDB is an estimate,
	// and is used only to size and report progress.
	rows int64
}

// column is one column of a table.
type column struct {
	name string
	// typ is COLUMN_TYPE, the full declaration: varchar(255), int unsigned,
	// enum('a','b').
	typ      string
	nullable bool
	// generated is a column the server computes, VIRTUAL or STORED. Nothing
	// may be written to it, and a copy that named it would be refused.
	generated bool
	unique    bool
	// foreignKey is the column this one references, as database.table.column.
	foreignKey string
}

// writableColumns are the columns a statement may name.
func (t table) writableColumns() []column {
	out := make([]column, 0, len(t.columns))
	for _, c := range t.columns {
		if !c.generated {
			out = append(out, c)
		}
	}
	return out
}

// maskingTable describes the table to masking under the schema name it is
// known by.
//
// The schema is a name and not necessarily the database the table is in. A
// golden candidate is a database called af_cand_<hash>, a new one per refresh,
// and masking derives every column's key from schema.table.column and matches
// rules against schema.table. Under the candidate's name, the same email would
// mask to a different address on every refresh, and a rule written for
// shop.customers would never match. So masking is told the source's name, and
// the executor addresses the candidate separately.
func (t table) maskingTable(schema, physical string) masking.Table {
	cols := make([]masking.ColumnInfo, 0, len(t.columns))
	for _, c := range t.columns {
		fk := c.foreignKey
		if rest, ok := strings.CutPrefix(fk, physical+"."); ok && physical != schema {
			fk = schema + "." + rest
		}
		cols = append(cols, masking.ColumnInfo{
			Name: c.name, Type: c.typ, SQLType: c.typ, Nullable: c.nullable,
			Unique: c.unique, ForeignKey: fk, Generated: c.generated,
		})
	}
	return masking.Table{
		Engine: engineName, Schema: schema, Name: t.name, Columns: cols,
		PrimaryKey: append([]string(nil), t.primaryKey...), Rows: t.rows,
	}
}

// skipped is a source object a golden does not hold, with the reason. It is
// carried into the golden's metadata, so a golden that is a subset of
// production says so.
type skipped struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// querier is a pool, a connection or a transaction.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// readCatalog reads the base tables of one database.
func readCatalog(ctx context.Context, q querier, database string) ([]table, []skipped, error) {
	byName := map[string]*table{}
	var order []string
	var skips []skipped

	err := each(ctx, q, `
SELECT TABLE_NAME, TABLE_TYPE, COALESCE(ENGINE, ''), COALESCE(TABLE_ROWS, 0)
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ?
ORDER BY TABLE_NAME`, []any{database}, func(r *sql.Rows) error {
		var name, kind, engine string
		var rows int64
		if err := r.Scan(&name, &kind, &engine, &rows); err != nil {
			return err
		}
		if kind != "BASE TABLE" {
			// A view is a statement over tables, and its definition names the
			// source's database and its definer, neither of which exists
			// where a golden lives. Said rather than dropped.
			skips = append(skips, skipped{Name: name, Kind: strings.ToLower(kind),
				Reason: "a view is not copied: its definition names the source's database and " +
					"definer, so the golden holds the tables it reads and not the view"})
			return nil
		}
		byName[name] = &table{name: name, engine: engine, rows: rows}
		order = append(order, name)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("datastore.mysql: listing the tables of %s: %w", database, err)
	}

	err = each(ctx, q, `
SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, EXTRA
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = ?
ORDER BY TABLE_NAME, ORDINAL_POSITION`, []any{database}, func(r *sql.Rows) error {
		var tbl, name, typ, nullable, extra string
		if err := r.Scan(&tbl, &name, &typ, &nullable, &extra); err != nil {
			return err
		}
		t, ok := byName[tbl]
		if !ok {
			return nil
		}
		// VIRTUAL GENERATED and STORED GENERATED are computed. DEFAULT_GENERATED
		// is an ordinary column whose default is an expression, and it is
		// written like any other.
		upper := strings.ToUpper(extra)
		generated := strings.Contains(upper, "VIRTUAL GENERATED") ||
			strings.Contains(upper, "STORED GENERATED")
		t.columns = append(t.columns, column{
			name: name, typ: typ, nullable: nullable == "YES", generated: generated,
		})
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("datastore.mysql: listing the columns of %s: %w", database, err)
	}

	// The primary key, and every unique index of exactly one column. An index
	// over an expression has no COLUMN_NAME for that part, so it is neither:
	// uniqueness of an expression says nothing about the column inside it.
	type index struct {
		table, name string
		columns     []string
		expression  bool
	}
	indexes := map[string]*index{}
	var indexOrder []string
	err = each(ctx, q, `
SELECT TABLE_NAME, INDEX_NAME, COALESCE(COLUMN_NAME, '')
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ? AND NON_UNIQUE = 0
ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`, []any{database}, func(r *sql.Rows) error {
		var tbl, name, col string
		if err := r.Scan(&tbl, &name, &col); err != nil {
			return err
		}
		id := tbl + "\x00" + name
		ix, ok := indexes[id]
		if !ok {
			ix = &index{table: tbl, name: name}
			indexes[id] = ix
			indexOrder = append(indexOrder, id)
		}
		if col == "" {
			ix.expression = true
		} else {
			ix.columns = append(ix.columns, col)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("datastore.mysql: listing the keys of %s: %w", database, err)
	}
	for _, id := range indexOrder {
		ix := indexes[id]
		t, ok := byName[ix.table]
		if !ok || ix.expression {
			continue
		}
		if ix.name == "PRIMARY" {
			t.primaryKey = append([]string(nil), ix.columns...)
		}
		if len(ix.columns) == 1 {
			t.markUnique(ix.columns[0])
		}
	}

	err = each(ctx, q, `
SELECT TABLE_NAME, COLUMN_NAME, REFERENCED_TABLE_SCHEMA, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
FROM information_schema.KEY_COLUMN_USAGE
WHERE TABLE_SCHEMA = ? AND REFERENCED_TABLE_NAME IS NOT NULL
ORDER BY TABLE_NAME, CONSTRAINT_NAME, ORDINAL_POSITION`, []any{database}, func(r *sql.Rows) error {
		var tbl, col, refSchema, refTable, refCol string
		if err := r.Scan(&tbl, &col, &refSchema, &refTable, &refCol); err != nil {
			return err
		}
		if t, ok := byName[tbl]; ok {
			t.setForeignKey(col, refSchema+"."+refTable+"."+refCol)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf(
			"datastore.mysql: listing the foreign keys of %s: %w", database, err)
	}

	tables := make([]table, 0, len(order))
	for _, name := range order {
		t := byName[name]
		if len(t.writableColumns()) == 0 {
			skips = append(skips, skipped{Name: name, Kind: "table",
				Reason: "every column of it is computed by the server, so there is nothing to write"})
			continue
		}
		tables = append(tables, *t)
	}
	sort.Slice(skips, func(i, j int) bool { return skips[i].Name < skips[j].Name })
	return tables, skips, nil
}

func (t *table) markUnique(name string) {
	for i := range t.columns {
		if t.columns[i].name == name {
			t.columns[i].unique = true
		}
	}
}

func (t *table) setForeignKey(name, ref string) {
	for i := range t.columns {
		if t.columns[i].name == name {
			t.columns[i].foreignKey = ref
		}
	}
}

// each runs a query and hands every row to fn.
func each(ctx context.Context, q querier, query string, args []any, fn func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Catalog reads a MySQL database's tables the way masking sees them, with the
// names of what it could not read as tables, for the cross store check. The
// address names the database.
func Catalog(ctx context.Context, url secrets.Value) (tables []masking.Table, skippedNames []string, err error) {
	s, err := parseURL(url)
	if err != nil {
		return nil, nil, err
	}
	if s.database == "" {
		return nil, nil, fmt.Errorf(
			"datastore.mysql: the address names no database, so there is no schema to read")
	}
	db, err := s.open(sessionPlain)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = db.Close() }()
	read, skips, err := readCatalog(ctx, db, s.database)
	if err != nil {
		return nil, nil, s.refuseUnverified(err)
	}
	out := make([]masking.Table, 0, len(read))
	for _, t := range read {
		out = append(out, t.maskingTable(s.database, s.database))
	}
	for _, sk := range skips {
		skippedNames = append(skippedNames, sk.Name)
	}
	return out, skippedNames, nil
}
