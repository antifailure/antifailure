package masking

import (
	"fmt"
	"strings"
)

// engineClickHouse is the engine name a manifest datastore declares for
// ClickHouse.
const engineClickHouse = "clickhouse"

func init() { RegisterDialect(clickhouseDialect{}) }

// clickhouseDialect is the second engine, and the reason the boundary above it
// exists.
//
// It is the store an analytics product keeps its events in, which is where
// every row about a person actually lives: the Postgres beside it holds teams,
// dashboards and feature flags. A twin that masks the Postgres and leaves the
// events alone has masked the metadata and published the data.
//
// WHAT THIS DOES AND DOES NOT DO TODAY, said plainly, because a dialect that
// looks complete from outside is exactly how somebody ends up trusting one.
//
//   - The type vocabulary is real and is checked against a live server: the
//     conformance test reads system.columns from a running ClickHouse and
//     requires this mapping to place every type it finds.
//   - The read and the rewrite are real statements and the live test runs
//     them, so they are not text nobody has executed.
//   - Nothing here refreshes a golden, branches a store or opens a connection.
//     There is no ClickHouse provider yet and this is not one.
//   - Update emits the per row mutation form. A ClickHouse mutation is
//     asynchronous and rewrites whole parts, so one per row is correct and slow
//     in a way an UPDATE is not, and a bulk refresh will want an INSERT into a
//     shadow table followed by EXCHANGE TABLES. That shape is the provider's
//     and not this file's: it is a plan for a whole table, where everything
//     here is a statement for one row.
type clickhouseDialect struct{}

func (clickhouseDialect) Engine() string { return engineClickHouse }

// clickhouseTypes maps ClickHouse's own type names onto the Postgres names the
// classifier, the rules and the verification scanner all speak.
//
// Keyed on the base name, so that FixedString(16), DateTime64(3),
// DateTime('UTC') and Decimal(10, 2) are one entry each rather than one entry
// per parameter anybody might write.
//
// A type NOT in this table comes back unchanged, which lands it in the
// classifier's "this does not recognise the type, so nothing decided what
// happens to this column" branch. That is the fail visible answer and it is
// deliberate: a wrong guess here is a column that looks classified, and the
// list a person has to work through is the one thing that finds it.
var clickhouseTypes = map[string]string{
	"string":      "text",
	"fixedstring": "text",
	"uuid":        "uuid",
	"int8":        "smallint",
	"int16":       "smallint",
	"uint8":       "smallint",
	"uint16":      "integer",
	"int32":       "integer",
	"uint32":      "bigint",
	"int64":       "bigint",
	"uint64":      "bigint",
	"int128":      "numeric",
	"uint128":     "numeric",
	"int256":      "numeric",
	"uint256":     "numeric",
	"float32":     "real",
	"float64":     "double precision",
	"decimal":     "numeric",
	"decimal32":   "numeric",
	"decimal64":   "numeric",
	"decimal128":  "numeric",
	"decimal256":  "numeric",
	"bool":        "boolean",
	"boolean":     "boolean",
	"date":        "date",
	"date32":      "date",
	"datetime":    "timestamp with time zone",
	"datetime64":  "timestamp with time zone",
	"json":        "jsonb",
	"object":      "jsonb",
	// Postgres reports every array as ARRAY and every enum, extension and
	// domain type as USER-DEFINED, and both land in the branch that reports a
	// column rather than masking it. The same shapes here are given the same
	// two names so that one classifier reaches the same conclusion about both
	// stores.
	"array":   "ARRAY",
	"enum8":   "USER-DEFINED",
	"enum16":  "USER-DEFINED",
	"enum":    "USER-DEFINED",
	"map":     "USER-DEFINED",
	"tuple":   "USER-DEFINED",
	"nested":  "USER-DEFINED",
	"variant": "USER-DEFINED",
	"dynamic": "USER-DEFINED",
	// An IP address locates a person. Postgres calls it inet, the scanner
	// reads inet through its text form, and the ip transform is the rule that
	// covers it in both stores.
	"ipv4": "inet",
	"ipv6": "inet",
}

// clickhouseWrappers are the type constructors that decorate another type
// without changing what it holds.
var clickhouseWrappers = []string{"Nullable", "LowCardinality"}

// Canonical maps a ClickHouse type name onto the Postgres name for the same
// kind of value.
func (clickhouseDialect) Canonical(dataType string) string {
	base := strings.TrimSpace(dataType)
	for {
		inner, unwrapped := unwrapClickHouse(base)
		if !unwrapped {
			break
		}
		base = inner
	}
	// The parameters of a parameterised type say how wide or how precise it
	// is, never what kind of thing it holds, so the base name is the whole
	// question. Array(String) is the exception that proves it: an array of
	// anything is an array, and Postgres reports it as ARRAY for the same
	// reason.
	if i := strings.IndexByte(base, '('); i >= 0 {
		base = base[:i]
	}
	if canon, ok := clickhouseTypes[strings.ToLower(strings.TrimSpace(base))]; ok {
		return canon
	}
	return dataType
}

// unwrapClickHouse removes one decorating constructor, and reports whether it
// removed anything.
func unwrapClickHouse(t string) (string, bool) {
	for _, w := range clickhouseWrappers {
		if len(t) > len(w)+2 && strings.EqualFold(t[:len(w)], w) &&
			t[len(w)] == '(' && t[len(t)-1] == ')' {
			return strings.TrimSpace(t[len(w)+1 : len(t)-1]), true
		}
	}
	return t, false
}

// QuoteIdent quotes an identifier in backticks, doubling any it contains.
//
// ClickHouse accepts a doubled backtick inside a backtick quoted identifier as
// one literal backtick, the same rule Postgres has for the double quote. The
// live conformance test creates a column whose name contains one and reads it
// back, because a quoting rule nobody has executed is a guess.
func (clickhouseDialect) QuoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// Qualify renders a table as db.table, or as the table alone when the catalog
// did not name a database.
func (d clickhouseDialect) Qualify(t Table) string {
	if t.Schema == "" {
		return d.QuoteIdent(t.Name)
	}
	return d.QuoteIdent(t.Schema) + "." + d.QuoteIdent(t.Name)
}

// Unaddressable refuses a table with no sorting key.
//
// ClickHouse has no ctid and no other physical row identifier, so a table with
// no key has no way to say which row a statement means. Postgres masks such a
// table in one unchunked statement; here there is no such statement, and the
// refusal is at planning time because a masking run that fails halfway leaves a
// table neither real nor safe.
func (clickhouseDialect) Unaddressable(t Table) string {
	if len(t.PrimaryKey) == 0 {
		return "ClickHouse has no physical row identifier, so a table with no sorting key " +
			"cannot be rewritten one row at a time; give the table a sorting key or a rule " +
			"that leaves it alone"
	}
	return ""
}

// RowKey addresses one row through the first column of the sorting key.
//
// Rendered through toString for the same reason Postgres casts to text: one
// comparison covers an integer key, a UUID and whatever else somebody sorted
// on, and the order only has to be total and stable rather than natural.
func (d clickhouseDialect) RowKey(tp TablePlan) string {
	if len(tp.OrderBy) == 0 {
		// Unreachable through a plan, because Unaddressable refuses the table
		// before one is built. Written rather than left to panic, because a
		// TablePlan is a public value somebody can build by hand.
		return "toString(tuple())"
	}
	return "toString(" + d.QuoteIdent(tp.OrderBy[0]) + ")"
}

// SelectChunk reads one chunk of a table, every column rendered as text.
//
// A transform takes a string and returns one, and the Postgres path gets that
// from its driver: pgx decodes a uuid or a timestamp into something whose text
// form the executor takes. Rendering here instead means a ClickHouse reader
// needs no decoder per type, which matters more than it sounds: ClickHouse has
// Map, Tuple, Nested, Variant and a UUID that is sixteen bytes on the wire, and
// a reader that got one of them wrong would hand a transform bytes it would
// mask as though they were a value.
//
// toString propagates null, so a null column still arrives as a null and the
// transform still sees the absence rather than an empty string. That is the
// first of the three properties this package is built on and it does not get to
// change per engine.
func (d clickhouseDialect) SelectChunk(tp TablePlan, after string) Query {
	key := d.RowKey(tp)
	cols := make([]string, 0, len(tp.Columns)+1)
	cols = append(cols, key)
	for _, c := range tp.Columns {
		cols = append(cols, "toString("+d.QuoteIdent(c.Column.Name)+")")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "SELECT %s FROM %s", strings.Join(cols, ", "), d.Qualify(tp.Table))
	var args []any
	if after != "" {
		fmt.Fprintf(&b, " WHERE %s > %s", key, (clickhouseDialect{}).Placeholder(1))
		args = append(args, after)
	}
	fmt.Fprintf(&b, " ORDER BY %s", key)
	if tp.ChunkSize > 0 {
		fmt.Fprintf(&b, " LIMIT %d", tp.ChunkSize)
	}
	return Query{SQL: b.String(), Args: args}
}

// Update rewrites the planned columns of one row.
//
// The parameters are numbered the way the Postgres statement numbers them, the
// row key first and the values after it in column order, because that ordering
// is the executor's contract rather than either engine's.
func (d clickhouseDialect) Update(tp TablePlan) Statement {
	names := make([]string, 0, len(tp.Columns))
	sets := make([]string, 0, len(tp.Columns))
	for i, c := range tp.Columns {
		names = append(names, c.Column.Name)
		sets = append(sets, fmt.Sprintf("%s = %s",
			d.QuoteIdent(c.Column.Name), clickhouseValue(c.Column, i+2)))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "ALTER TABLE %s UPDATE %s WHERE %s = %s",
		d.Qualify(tp.Table), strings.Join(sets, ", "), d.RowKey(tp), (clickhouseDialect{}).Placeholder(1))
	return Statement{
		SQL: b.String(), Table: tp.Table.String(), Columns: names,
		Keyed: len(tp.OrderBy) > 0,
	}
}

// Placeholder renders a ClickHouse server side query parameter.
//
// Every masked value arrives as a string, because that is what a transform
// produces, so every parameter is declared String and the assignment casts
// where the column is something else.
func (clickhouseDialect) Placeholder(n int) string { return fmt.Sprintf("{p%d:String}", n) }

// clickhouseValue renders the assigned value for one column.
//
// ClickHouse will not put a String into a UUID column on its own, where
// Postgres infers the column's type from the assignment. So a column whose
// canonical type is not text carries an explicit cast to the type the catalog
// reported, which is the only place in this file the RAW type is used rather
// than the canonical one: a cast has to name the type the server has.
func clickhouseValue(c ColumnInfo, n int) string {
	param := (clickhouseDialect{}).Placeholder(n)
	if strings.EqualFold((clickhouseDialect{}).Canonical(c.Type), "text") || c.Type == "" {
		return param
	}
	return fmt.Sprintf("CAST(%s AS %s)", param, c.Type)
}
