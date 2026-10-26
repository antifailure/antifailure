package verify

import (
	"context"
	"fmt"
	"strings"
)

// A dialect is the part of verification that knows one datastore engine, and a
// source is the part that can talk to one.
//
// The scan itself is engine independent and always was: the detectors run over
// text, and text out of ClickHouse is the same text as out of Postgres. What
// was Postgres and did not say so was the query that lists the columns, the
// query that samples one, and the table of type names that decides which
// columns are worth reading at all.
//
// The type table here is a SECOND copy of the one in the masking package, and
// it is a copy on purpose. This package must not import that one, because it
// is the check on it: an instrument that shares its opinion of what a type
// means with the thing it is checking cannot disagree with it. Two independent
// tables that a test in the other package requires to agree can. That was
// already true of the Postgres names and it is now true of the ClickHouse ones.
//
// Both tables are written in the POSTGRES vocabulary, and each engine's names
// are mapped onto it. A third vocabulary would mean three tables to keep in
// step rather than two.

// Dialect is what a scan needs to know about one datastore engine.
type Dialect interface {
	// Engine is the name a manifest datastore declares.
	Engine() string
	// Canonical maps one of this engine's type names onto the Postgres name
	// for the same kind of value. A name it does not recognise comes back
	// unchanged, which lands the column in the list of things the scanner says
	// it could not read rather than in a wrong answer.
	Canonical(dataType string) string
	// Kind is how the scanner reads a column of this engine's type: "text"
	// through a text form, "bytea" as raw bytes decoded where they decode,
	// "structural" for a type that cannot carry a sentence, and "unread" for
	// one the scanner has no way to read.
	Kind(dataType string) string
	// Columns is the statement listing every column of every ordinary table,
	// as schema, table, column, type.
	Columns() string
	// Sample is the statement reading up to limit non null values of one
	// column.
	Sample(c Column, limit int) string
}

// Column is one column the scan has an opinion about.
type Column struct {
	Schema string
	Table  string
	Name   string
	// Type is the engine's own name for the type, which is what the report
	// prints: a person reading it is looking at their own schema.
	Type string
	// kind is how this column is read, decided from the type by the dialect.
	kind columnKind
}

// Row is one row of a result, one entry per selected column, holding the bytes
// the engine returned. A null is a nil entry.
//
// Valid until the function it was handed to returns. Nothing keeps one, which
// is what lets a sample of two thousand values of a column holding documents
// cost one value at a time rather than all of them at once.
type Row [][]byte

// Exec runs one statement and hands each row to a function.
//
// The extension point for a second engine. A ClickHouse provider supplies this
// and nothing else: the statements come from the dialect, the classification
// from the shared table, and the detectors are the same detectors.
//
// Bytes rather than strings, so that a column holding something that is not
// text is recognised as such instead of being silently mangled into one. A
// callback rather than a slice, so that the scan holds one value at a time: it
// samples two thousand rows per column and a column can hold a document, and
// the first shape of this returned them all at once.
type Exec func(ctx context.Context, sql string, yield func(Row) error) error

// Source is a datastore a scan can read.
type Source interface {
	// Engine names the datastore engine, for the report.
	Engine() string
	// Columns lists every column the scan has an opinion about. The list is a
	// schema rather than data, so it is small and is returned whole.
	Columns(ctx context.Context) ([]Column, error)
	// Sample reads up to limit non null values of one column, one at a time.
	Sample(ctx context.Context, c Column, limit int, yield func(Row) error) error
}

// NewSource pairs a dialect with something that can run its statements.
func NewSource(d Dialect, exec Exec) Source { return &sqlSource{d: d, exec: exec} }

type sqlSource struct {
	d    Dialect
	exec Exec
}

func (s *sqlSource) Engine() string { return s.d.Engine() }

func (s *sqlSource) Columns(ctx context.Context) ([]Column, error) {
	var out []Column
	err := s.exec(ctx, s.d.Columns(), func(r Row) error {
		if len(r) < 4 {
			return fmt.Errorf(
				"listing columns returned %d values for a row and this needs "+
					"the schema, the table, the column and the type", len(r))
		}
		c := Column{
			Schema: string(r[0]), Table: string(r[1]),
			Name: string(r[2]), Type: string(r[3]),
		}
		switch s.d.Kind(c.Type) {
		case "structural":
			// Not read and not listed. A listing of every bigint in the schema
			// as "not readable" is a listing nobody reads, which is the same
			// as no listing.
			return nil
		case "bytea":
			c.kind = kindBytea
		case "unread":
			c.kind = kindUnread
		default:
			c.kind = kindText
		}
		out = append(out, c)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("verify: listing columns: %w", err)
	}
	return out, nil
}

func (s *sqlSource) Sample(ctx context.Context, c Column, limit int, yield func(Row) error) error {
	return s.exec(ctx, s.d.Sample(c, limit), yield)
}

// postgresDialect is what every statement in this package used to be.
type postgresDialect struct{}

func (postgresDialect) Engine() string { return "postgres" }

// Canonical is the identity: the canonical vocabulary is the Postgres one.
func (postgresDialect) Canonical(dataType string) string { return dataType }

func (postgresDialect) Kind(dataType string) string { return KindOf(dataType) }

// Columns lists every column of every ordinary table.
//
// Everything, with the structural types dropped afterwards rather than in the
// statement. This used to be six text types named in the WHERE clause, and the
// cost of that was not the columns it skipped, it was that the report did not
// say it had skipped them: a bytea holding a sealed private key and an enum
// were equally invisible, and "clean" covered both.
func (postgresDialect) Columns() string {
	return `
SELECT c.table_schema, c.table_name, c.column_name, c.data_type
FROM information_schema.columns c
JOIN information_schema.tables t
  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
WHERE t.table_type = 'BASE TABLE'
  AND c.table_schema NOT IN ('pg_catalog', 'information_schema')
  AND c.table_schema NOT LIKE 'pg_toast%'
ORDER BY c.table_schema, c.table_name, c.ordinal_position`
}

func (postgresDialect) Sample(c Column, limit int) string {
	expr := quoteIdent(c.Name) + "::text"
	if c.kind == kindBytea {
		// Raw, not cast. A bytea cast to text is its hex form, "\x6162",
		// which no detector matches, and which is how a secret in a bytea
		// column would have passed a scan that read it.
		expr = quoteIdent(c.Name)
	}
	return fmt.Sprintf(
		`SELECT %s FROM %s.%s WHERE %s IS NOT NULL LIMIT %d`,
		expr, quoteIdent(c.Schema), quoteIdent(c.Table),
		quoteIdent(c.Name), limit)
}

// clickhouseTypes maps ClickHouse's type names onto the Postgres ones this
// scanner classifies by.
//
// Independently written from the table in the masking package and required to
// agree with it by a test that lives over there, for the reason at the top of
// this file: the check must not take its opinion from the thing it checks.
var clickhouseTypes = map[string]string{
	"string": "text", "fixedstring": "text",
	"uuid": "uuid",
	"int8": "smallint", "int16": "smallint", "uint8": "smallint",
	"uint16": "integer", "int32": "integer",
	"uint32": "bigint", "int64": "bigint", "uint64": "bigint",
	"int128": "numeric", "uint128": "numeric",
	"int256": "numeric", "uint256": "numeric",
	"float32": "real", "float64": "double precision",
	"decimal": "numeric", "decimal32": "numeric", "decimal64": "numeric",
	"decimal128": "numeric", "decimal256": "numeric",
	"bool": "boolean", "boolean": "boolean",
	"date": "date", "date32": "date",
	"datetime": "timestamp with time zone", "datetime64": "timestamp with time zone",
	"json": "jsonb", "object": "jsonb",
	"array":   "ARRAY",
	"enum8":   "USER-DEFINED",
	"enum16":  "USER-DEFINED",
	"enum":    "USER-DEFINED",
	"map":     "USER-DEFINED",
	"tuple":   "USER-DEFINED",
	"nested":  "USER-DEFINED",
	"variant": "USER-DEFINED", "dynamic": "USER-DEFINED",
	"ipv4": "inet", "ipv6": "inet",
}

// clickhouseDialect reads a ClickHouse.
//
// Nothing in the engine opens a ClickHouse connection yet. This is the half of
// a second store's verification that does not need one: the statements and the
// type vocabulary. A provider supplies the Exec, and the detectors, the sample
// size, the unread list and the attestation are then the same ones the
// Postgres golden is published on.
type clickhouseDialect struct{}

func (clickhouseDialect) Engine() string { return "clickhouse" }

func (clickhouseDialect) Canonical(dataType string) string {
	base := strings.TrimSpace(dataType)
	for {
		inner, unwrapped := unwrapClickHouse(base)
		if !unwrapped {
			break
		}
		base = inner
	}
	if i := strings.IndexByte(base, '('); i >= 0 {
		base = base[:i]
	}
	if canon, ok := clickhouseTypes[strings.ToLower(strings.TrimSpace(base))]; ok {
		return canon
	}
	return dataType
}

func unwrapClickHouse(t string) (string, bool) {
	for _, w := range []string{"Nullable", "LowCardinality"} {
		if len(t) > len(w)+2 && strings.EqualFold(t[:len(w)], w) &&
			t[len(w)] == '(' && t[len(t)-1] == ')' {
			return strings.TrimSpace(t[len(w)+1 : len(t)-1]), true
		}
	}
	return t, false
}

// Kind reads a ClickHouse String as bytes rather than as text, which is the
// one place the shared classification is not the whole answer.
//
// A Postgres text column in a UTF-8 database is text by construction. A
// ClickHouse String is a byte string: it is where an analytics schema puts
// JSON, and it is also where it would put a ciphertext. Read as text, a
// column of encrypted bytes would be handed to the detectors as mojibake,
// match nothing, and be counted as read and clean. Read as bytes it is
// decoded where it decodes and counted as unread where it does not, which is
// the same treatment bytea gets and for the same reason.
func (d clickhouseDialect) Kind(dataType string) string {
	canon := d.Canonical(dataType)
	if strings.EqualFold(canon, "text") {
		return "bytea"
	}
	return KindOf(canon)
}

// Columns lists every column of every ordinary table.
//
// system.columns is ClickHouse's own catalog. The engine filter drops views
// and the dictionary and merge engines, which have no rows of their own, and
// the database filter drops the server's own catalogs the way the Postgres
// statement drops pg_catalog.
func (clickhouseDialect) Columns() string {
	return `
SELECT c.database, c.table, c.name, c.type
FROM system.columns c
INNER JOIN system.tables t ON t.database = c.database AND t.name = c.table
WHERE c.database NOT IN ('system', 'INFORMATION_SCHEMA', 'information_schema')
  AND t.engine NOT LIKE '%View'
  AND t.engine NOT IN ('Dictionary', 'Merge', 'Distributed')
ORDER BY c.database, c.table, c.position`
}

// Sample reads one column, with the nullability stripped from the type rather
// than from the values.
//
// assumeNotNull rather than the bare column, so that the result type is String
// and not Nullable(String). The rows are already filtered to the ones that are
// not null, so nothing is being asserted that the statement has not already
// enforced, and what it buys is a result a reader does not have to decode a
// null flag out of. A scan reads values to look at them; the null it might have
// found is the one thing it has no opinion about.
func (d clickhouseDialect) Sample(c Column, limit int) string {
	inner := "assumeNotNull(" + d.quoteIdent(c.Name) + ")"
	expr := "toString(" + inner + ")"
	if c.kind == kindBytea {
		// Raw, not rendered. A ClickHouse String is bytes, and rendering one
		// through toString would still be bytes, but the point of the bytes
		// path is that what comes back is decoded here rather than by the
		// server.
		expr = inner
	}
	return fmt.Sprintf(
		`SELECT %s FROM %s.%s WHERE isNotNull(%s) LIMIT %d`,
		expr, d.quoteIdent(c.Schema), d.quoteIdent(c.Table),
		d.quoteIdent(c.Name), limit)
}

func (clickhouseDialect) quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// mysqlTypes maps MySQL's type names onto the Postgres ones this scanner
// classifies by, keyed on the base name of information_schema's COLUMN_TYPE.
//
// Independently written from the table in the masking package and required to
// agree with it by a test that lives over there, for the reason at the top of
// this file.
var mysqlTypes = map[string]string{
	"char": "character", "varchar": "character varying",
	"tinytext": "text", "text": "text", "mediumtext": "text", "longtext": "text",
	"binary": "bytea", "varbinary": "bytea",
	"tinyblob": "bytea", "blob": "bytea", "mediumblob": "bytea", "longblob": "bytea",
	"tinyint": "smallint", "smallint": "smallint", "year": "smallint",
	"mediumint": "integer", "int": "integer", "integer": "integer",
	"bigint":  "bigint",
	"decimal": "numeric", "numeric": "numeric", "dec": "numeric", "fixed": "numeric",
	"float": "real", "double": "double precision",
	"bool": "boolean", "boolean": "boolean",
	"bit":  "bit",
	"date": "date", "datetime": "timestamp without time zone",
	"timestamp": "timestamp with time zone", "time": "time without time zone",
	"json": "jsonb",
	"enum": "USER-DEFINED", "set": "USER-DEFINED", "vector": "USER-DEFINED",
	"geometry": "USER-DEFINED", "point": "USER-DEFINED", "linestring": "USER-DEFINED",
	"polygon": "USER-DEFINED", "multipoint": "USER-DEFINED",
	"multilinestring": "USER-DEFINED", "multipolygon": "USER-DEFINED",
	"geometrycollection": "USER-DEFINED", "geomcollection": "USER-DEFINED",
}

// mysqlUnsignedTypes is the canonical name of an unsigned integer, one signed
// width up, because the unsigned type holds numbers the signed one cannot.
var mysqlUnsignedTypes = map[string]string{
	"tinyint": "smallint", "smallint": "integer", "mediumint": "integer",
	"int": "bigint", "integer": "bigint", "bigint": "numeric",
}

// mysqlSpatial are the types whose value is a binary encoding of a shape.
var mysqlSpatial = map[string]bool{
	"geometry": true, "point": true, "linestring": true, "polygon": true,
	"multipoint": true, "multilinestring": true, "multipolygon": true,
	"geometrycollection": true, "geomcollection": true,
}

// mysqlDialect reads a MySQL, and a MariaDB or a Percona through the same
// catalog.
type mysqlDialect struct{}

func (mysqlDialect) Engine() string { return "mysql" }

// splitMySQLType returns a COLUMN_TYPE's base name and whether it is unsigned,
// reading the modifiers only after the parenthesised part, because an enum's
// values are free text.
func splitMySQLType(dataType string) (string, bool) {
	t := strings.ToLower(strings.TrimSpace(dataType))
	base, rest := t, ""
	if open := strings.IndexByte(t, '('); open >= 0 {
		base = t[:open]
		if end := strings.LastIndexByte(t, ')'); end > open {
			rest = t[end+1:]
		}
	} else if space := strings.IndexByte(t, ' '); space >= 0 {
		base, rest = t[:space], t[space:]
	}
	unsigned := false
	for _, w := range strings.Fields(rest) {
		unsigned = unsigned || w == "unsigned"
	}
	return strings.TrimSpace(base), unsigned
}

func (mysqlDialect) Canonical(dataType string) string {
	lower := strings.ToLower(strings.TrimSpace(dataType))
	// A name that is already canonical stays as it is, so feeding the output
	// back in is safe.
	for _, table := range []map[string]string{mysqlTypes, mysqlUnsignedTypes} {
		for _, canon := range table {
			if strings.ToLower(canon) == lower {
				return canon
			}
		}
	}
	base, unsigned := splitMySQLType(dataType)
	if canon, ok := mysqlUnsignedTypes[base]; ok && unsigned {
		return canon
	}
	if canon, ok := mysqlTypes[base]; ok {
		return canon
	}
	return dataType
}

// Kind reads a spatial column as unread rather than as text.
//
// A geometry is USER-DEFINED the way a PostGIS one is, and the shared table
// reads that through its text form. A MySQL geometry has no text form short of
// a function call: it arrives as the server's binary encoding, which no
// detector matches, so read as text it would be counted as read and clean when
// it holds a location. Unread is what it is, and the report says so.
func (d mysqlDialect) Kind(dataType string) string {
	if base, _ := splitMySQLType(dataType); mysqlSpatial[base] {
		return "unread"
	}
	return KindOf(d.Canonical(dataType))
}

// Columns lists every column of every base table outside the server's own
// schemas, with information_schema's COLUMN_TYPE, which carries the length,
// the signedness and an enum's values where DATA_TYPE carries the base name
// alone.
func (mysqlDialect) Columns() string {
	return `
SELECT c.TABLE_SCHEMA, c.TABLE_NAME, c.COLUMN_NAME, c.COLUMN_TYPE
FROM information_schema.COLUMNS c
JOIN information_schema.TABLES t
  ON t.TABLE_SCHEMA = c.TABLE_SCHEMA AND t.TABLE_NAME = c.TABLE_NAME
WHERE t.TABLE_TYPE = 'BASE TABLE'
  AND c.TABLE_SCHEMA NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
ORDER BY c.TABLE_SCHEMA, c.TABLE_NAME, c.ORDINAL_POSITION`
}

// Sample reads one column as it is stored. The driver returns every type in
// its text form already, and a binary string as its bytes, which is the bytea
// path's whole point.
func (d mysqlDialect) Sample(c Column, limit int) string {
	return fmt.Sprintf(
		"SELECT %s FROM %s.%s WHERE %s IS NOT NULL LIMIT %d",
		d.quoteIdent(c.Name), d.quoteIdent(c.Schema), d.quoteIdent(c.Table),
		d.quoteIdent(c.Name), limit)
}

func (mysqlDialect) quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// Postgres, ClickHouse and MySQL name the dialects for a caller assembling a
// source. They are values rather than a registry because this package has no
// manifest to read an engine name out of: whatever chose the engine is the
// thing holding the connection.
var (
	Postgres   Dialect = postgresDialect{}
	ClickHouse Dialect = clickhouseDialect{}
	MySQL      Dialect = mysqlDialect{}
)
