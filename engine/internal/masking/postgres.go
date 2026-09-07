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
// Nothing about the Postgres path changed when it moved here. The statements
// are the same text, the type vocabulary is the identity mapping because
// Postgres names are the canonical ones, and the row key is still ctid when
// there is no primary key. That is the point: the Postgres path is the one
// with a live suite behind it, so it is the one the boundary has to leave
// alone.
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

// RowKey is what addresses one row.
//
// A primary key when there is one. Otherwise ctid, the physical row
// identifier, which every table has and which is only meaningful inside the
// transaction that read it. That is why a table with no key cannot be
// resumed: nothing about a ctid survives the run that saw it.
func (postgresDialect) RowKey(tp TablePlan) string {
	if len(tp.OrderBy) > 0 {
		return quoteIdent(tp.OrderBy[0])
	}
	return "ctid"
}

// SelectChunk builds the read for one chunk.
func (d postgresDialect) SelectChunk(tp TablePlan, after string) Query {
	key := d.RowKey(tp)
	cols := make([]string, 0, len(tp.Columns)+1)
	// Cast on the way out, so every key type arrives as a string. A ctid read
	// natively comes back as a struct that formats as nothing the WHERE clause
	// will match, which produced an update that silently changed no rows.
	cols = append(cols, key+"::text")
	for _, c := range tp.Columns {
		cols = append(cols, quoteIdent(c.Column.Name))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "SELECT %s FROM %s", strings.Join(cols, ", "), d.Qualify(tp.Table))
	var args []any
	if after != "" && len(tp.OrderBy) > 0 {
		// Compared as text, so one code path covers integer keys, uuids, and
		// anything else somebody used. The order is not the key's natural
		// order for an integer, and it does not need to be: it only has to be
		// total and stable, so every row is visited once and a resume picks up
		// where the last chunk stopped.
		fmt.Fprintf(&b, " WHERE %s::text > %s", key, d.Placeholder(1))
		args = append(args, after)
	}
	fmt.Fprintf(&b, " ORDER BY %s::text", key)
	if tp.ChunkSize > 0 {
		fmt.Fprintf(&b, " LIMIT %d", tp.ChunkSize)
	}
	return Query{SQL: b.String(), Args: args}
}

// Update rewrites the planned columns of one row.
func (d postgresDialect) Update(tp TablePlan) Statement {
	names := make([]string, 0, len(tp.Columns))
	sets := make([]string, 0, len(tp.Columns))
	for i, c := range tp.Columns {
		names = append(names, c.Column.Name)
		sets = append(sets, fmt.Sprintf("%s = %s", quoteIdent(c.Column.Name), d.Placeholder(i+2)))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "UPDATE %s SET %s WHERE %s::text = %s",
		d.Qualify(tp.Table), strings.Join(sets, ", "), d.RowKey(tp), d.Placeholder(1))
	return Statement{
		SQL: b.String(), Table: tp.Table.String(), Columns: names,
		Keyed: len(tp.OrderBy) > 0,
	}
}
