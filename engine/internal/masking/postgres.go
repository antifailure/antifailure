package masking

import (
	"fmt"
	"strings"
)

// enginePostgres is the engine name a manifest datastore declares for the
// store this package was written against.
const enginePostgres = "postgres"

func init() { RegisterDialect(postgresDialect{}) }

// postgresDialect is the dialect every statement in this package used to be,
// written down rather than assumed.
//
// The type vocabulary is the identity mapping because Postgres names are the
// canonical ones, and the row key is ctid when there is no primary key.
type postgresDialect struct{}

func (postgresDialect) Engine() string { return enginePostgres }

// Canonical is the identity, because the canonical vocabulary IS the Postgres
// one. Written out rather than left implicit so that the projection law the
// conformance suite checks holds here too.
func (postgresDialect) Canonical(dataType string) string { return dataType }

func (postgresDialect) QuoteIdent(name string) string { return quoteIdent(name) }

func (postgresDialect) Qualify(t Table) string { return t.Qualified() }

// Placeholder is Postgres's own positional parameter.
func (postgresDialect) Placeholder(n int) string { return fmt.Sprintf("$%d", n) }

// Unaddressable is always empty for Postgres.
//
// Every table has a ctid, so every table can be rewritten one row at a time.
// A table with no primary key cannot be RESUMED, which is a different fact and
// the plan already says it: the chunk size is zero and the plan prints "in one
// statement (no primary key to chunk on)".
func (postgresDialect) Unaddressable(Table) string { return "" }

// RowKey reads a row's address out of a chunk: every column of the primary key,
// in key order, each rendered as text on the way out.
//
// EVERY column, not the first. A row of a table keyed on (tenant_id, id) is not
// named by its tenant, and addressing it by one used to rewrite every row of the
// tenant with one row's masked values and page past the rest of the tenant
// unvisited.
//
// Text on the way out only. A ctid read natively arrives as a struct that
// formats as nothing a comparison will match, which once produced an update
// that silently changed no rows, and one string per column is what the executor
// carries whatever the key's types are. The comparisons below never cast the
// column.
//
// A table with no primary key is addressed by ctid, the physical row
// identifier, which is only meaningful inside the transaction that read it.
// That is why such a table cannot be resumed.
func (postgresDialect) RowKey(tp TablePlan) []string {
	if len(tp.OrderBy) == 0 {
		return []string{"ctid::text"}
	}
	out := make([]string, len(tp.OrderBy))
	for i, k := range tp.OrderBy {
		out[i] = quoteIdent(k) + "::text"
	}
	return out
}

// keyColumns renders the primary key as the table stores it, for a comparison
// or an order. One column is itself; several are a row value, which Postgres
// compares column by column, in order, and serves from the key's index.
func (postgresDialect) keyColumns(tp TablePlan) string {
	names := make([]string, len(tp.OrderBy))
	for i, k := range tp.OrderBy {
		names[i] = quoteIdent(k)
	}
	if len(names) == 1 {
		return names[0]
	}
	return "(" + strings.Join(names, ", ") + ")"
}

// keyParameters renders one parameter per key column, numbered from first, each
// cast to its column's type.
//
// THE PARAMETER IS CAST, NEVER THE COLUMN. key::text compared with a text
// parameter is an expression no btree serves, so every chunk read and every row
// update used to scan the whole table: 78 milliseconds a chunk and 13 a row on
// two hundred thousand rows. The type is the catalog's format_type, with its
// length or precision, so an enum, a domain or char(3) is named the way a cast
// accepts, and a value read out of the column always fits it. A key column the
// catalog did not describe is left uncast, and Postgres infers the parameter's
// type from the column it is compared with.
func (d postgresDialect) keyParameters(tp TablePlan, first int) string {
	params := make([]string, len(tp.OrderBy))
	for i, k := range tp.OrderBy {
		p := d.Placeholder(first + i)
		if typ := tp.Table.ColumnNamed(k).SQLType; typ != "" {
			p += "::" + typ
		}
		params[i] = p
	}
	if len(params) == 1 {
		return params[0]
	}
	return "(" + strings.Join(params, ", ") + ")"
}

// SelectChunk builds the read for one chunk.
//
// Keyset pagination on the whole key: the rows after the last address masked,
// in the key's own order. A boundary that falls inside a run of rows sharing
// their first key column resumes inside that run rather than after it, which is
// the page the first column alone used to skip.
func (d postgresDialect) SelectChunk(tp TablePlan, after []string) Query {
	cols := make([]string, 0, len(tp.Columns)+len(tp.OrderBy)+1)
	cols = append(cols, d.RowKey(tp)...)
	for _, c := range tp.Columns {
		cols = append(cols, quoteIdent(c.Column.Name))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "SELECT %s FROM %s", strings.Join(cols, ", "), d.Qualify(tp.Table))
	if len(tp.OrderBy) == 0 {
		// No key, so no order a resume could rely on. The plan masks such a
		// table in one statement, and a preview only needs some rows.
		if tp.ChunkSize > 0 {
			fmt.Fprintf(&b, " LIMIT %d", tp.ChunkSize)
		}
		return Query{SQL: b.String()}
	}

	var args []any
	if len(after) == len(tp.OrderBy) {
		fmt.Fprintf(&b, " WHERE %s > %s", d.keyColumns(tp), d.keyParameters(tp, 1))
		for _, v := range after {
			args = append(args, v)
		}
	}
	// QUALIFIED WITH THE TABLE, because this read selects each key column cast
	// to text, and Postgres resolves a bare name in ORDER BY to an OUTPUT column
	// before a table column. "tenant_id"::text is output under the name
	// tenant_id, so ORDER BY "tenant_id" sorted the text of the key while the
	// bound above compared the key as stored. That was a Sort over a Seq Scan on
	// every chunk, and a page cut off by LIMIT in one order and bounded in
	// another, which skips rows. A qualified name can only mean the table's own
	// column.
	order := make([]string, len(tp.OrderBy))
	for i, k := range tp.OrderBy {
		order[i] = d.Qualify(tp.Table) + "." + quoteIdent(k)
	}
	fmt.Fprintf(&b, " ORDER BY %s", strings.Join(order, ", "))
	if tp.ChunkSize > 0 {
		fmt.Fprintf(&b, " LIMIT %d", tp.ChunkSize)
	}
	return Query{SQL: b.String(), Args: args}
}

// Update rewrites the planned columns of one row, addressed by its whole
// primary key, or by ctid when it has none.
//
// The address is the first parameters, one per key column, and the masked
// values follow in the plan's column order. ctid is compared as a tid, which is
// a TID scan, where ctid::text was a scan of the whole table to find one row.
func (d postgresDialect) Update(tp TablePlan) Statement {
	width := tp.AddressWidth()
	names := make([]string, 0, len(tp.Columns))
	sets := make([]string, 0, len(tp.Columns))
	for i, c := range tp.Columns {
		names = append(names, c.Column.Name)
		sets = append(sets, fmt.Sprintf("%s = %s", quoteIdent(c.Column.Name), d.Placeholder(width+i+1)))
	}

	where := "ctid = " + d.Placeholder(1) + "::tid"
	if len(tp.OrderBy) > 0 {
		where = d.keyColumns(tp) + " = " + d.keyParameters(tp, 1)
	}
	return Statement{
		SQL: fmt.Sprintf("UPDATE %s SET %s WHERE %s",
			d.Qualify(tp.Table), strings.Join(sets, ", "), where),
		Table: tp.Table.String(), Columns: names,
		Keyed: len(tp.OrderBy) > 0,
	}
}
