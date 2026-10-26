package masking

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// engineMySQL is the engine name a manifest datastore declares for MySQL, and
// for MariaDB and Percona, which speak the same catalog and the same SQL for
// everything this file emits.
const engineMySQL = "mysql"

func init() { RegisterDialect(mysqlDialect{}) }

// mysqlDialect is the third engine, and the first that rewrites rows the way
// Postgres does.
//
// ClickHouse needed its own shape because a per row UPDATE there is a mutation
// that rewrites whole parts. MySQL is a row store with a transaction and a
// primary key index, so the Postgres shape is the right one: read a chunk in
// key order, compute the masked values in Go, and write each row back by its
// whole primary key inside the chunk's transaction. The provider runs that loop
// (internal/datastore/mysql), because the executor in this package holds a
// Postgres connection and the boundary above says it should keep doing so.
//
// Two things about MySQL shape the statements, and both are about binding.
//
//  1. MySQL has one parameter marker, `?`, bound in the order it appears in
//     the text. The executor's contract numbers its arguments: the address
//     first, then the masked values in plan order. An UPDATE names its values
//     in SET before it names its address in WHERE, so a bare `?` would bind
//     the key to the first column and the first value to the key, and the
//     statement would rewrite a row nobody chose with values meant for
//     another. So a placeholder here is `?` followed by its number in a
//     comment, which the server ignores, and ArgsInTextOrder reorders the
//     arguments to the order the markers appear in. The numbers are also what
//     lets a composite resume bound compare the same value twice.
//
//  2. MySQL compares an integer column with a string by converting both to a
//     double. A row's address travels as text, so `id = '9007199254740993'`
//     is a comparison of two doubles that cannot tell that key from its
//     neighbour, and a snowflake identifier is far past where a double stops
//     counting in ones. Every key parameter is therefore cast to its column's
//     type, which keeps the comparison exact and lets the key's index serve
//     it.
type mysqlDialect struct{}

func (mysqlDialect) Engine() string { return engineMySQL }

// mysqlTypes maps MySQL's type names onto the Postgres names the classifier,
// the rules and the verification scanner all speak.
//
// Keyed on the base name as information_schema's COLUMN_TYPE spells it, with
// the length, the precision and the modifiers removed, so varchar(255),
// int(11) unsigned zerofill and decimal(10,2) are one entry each. Signedness is
// handled beside the table rather than in it, because an unsigned integer holds
// numbers the signed type of the same width cannot, and the canonical name has
// to be wide enough for them.
//
// Independently written from the table in internal/verify and required to
// agree with it by mysql_test.go, for the reason dialect.go gives: the check
// must not take its opinion from the thing it checks.
var mysqlTypes = map[string]string{
	"char":       "character",
	"varchar":    "character varying",
	"tinytext":   "text",
	"text":       "text",
	"mediumtext": "text",
	"longtext":   "text",
	// The binary strings. A Postgres bytea is read by the scanner as bytes and
	// decoded where it decodes, which is what a column holding a ciphertext or
	// a token needs, and these are where a MySQL schema keeps exactly that.
	"binary":     "bytea",
	"varbinary":  "bytea",
	"tinyblob":   "bytea",
	"blob":       "bytea",
	"mediumblob": "bytea",
	"longblob":   "bytea",
	"tinyint":    "smallint",
	"smallint":   "smallint",
	"mediumint":  "integer",
	"int":        "integer",
	"integer":    "integer",
	"bigint":     "bigint",
	"decimal":    "numeric",
	"numeric":    "numeric",
	"dec":        "numeric",
	"fixed":      "numeric",
	"float":      "real",
	"double":     "double precision",
	"bool":       "boolean",
	"boolean":    "boolean",
	"bit":        "bit",
	"year":       "smallint",
	"date":       "date",
	// DATETIME stores the wall clock it was given and TIMESTAMP stores an
	// instant and converts it through the session's time zone, which is the
	// same distinction Postgres draws between its two timestamp types.
	"datetime":  "timestamp without time zone",
	"timestamp": "timestamp with time zone",
	"time":      "time without time zone",
	"json":      "jsonb",
	// An enum, a set, a spatial value and a vector have no Postgres name of
	// their own in information_schema either: Postgres reports an enum as
	// USER-DEFINED, and both classifiers report such a column rather than
	// guessing a transform for it.
	"enum":               "USER-DEFINED",
	"set":                "USER-DEFINED",
	"geometry":           "USER-DEFINED",
	"point":              "USER-DEFINED",
	"linestring":         "USER-DEFINED",
	"polygon":            "USER-DEFINED",
	"multipoint":         "USER-DEFINED",
	"multilinestring":    "USER-DEFINED",
	"multipolygon":       "USER-DEFINED",
	"geometrycollection": "USER-DEFINED",
	"geomcollection":     "USER-DEFINED",
	"vector":             "USER-DEFINED",
}

// mysqlUnsigned is the canonical name of an unsigned integer, which is the
// next signed width up: an int unsigned reaches four billion and a Postgres
// integer stops at two.
var mysqlUnsigned = map[string]string{
	"tinyint":   "smallint",
	"smallint":  "integer",
	"mediumint": "integer",
	"int":       "bigint",
	"integer":   "bigint",
	"bigint":    "numeric",
}

// mysqlCanonicalNames are the names this dialect produces. Canonical has to be
// a projection, so a name that is already one of these comes back unchanged
// even where MySQL would read the same word differently: MySQL's REAL is a
// double, and Postgres's is not, but nothing reads REAL out of a MySQL
// catalog, which reports it as double.
var mysqlCanonicalNames = func() map[string]string {
	out := map[string]string{}
	for _, canon := range mysqlTypes {
		out[strings.ToLower(canon)] = canon
	}
	for _, canon := range mysqlUnsigned {
		out[strings.ToLower(canon)] = canon
	}
	return out
}()

// mysqlBase splits a COLUMN_TYPE into its base name and whether it is
// unsigned. The modifiers come after the parenthesised part, and only there:
// an enum value is free text and may itself say unsigned.
func mysqlBase(dataType string) (base string, unsigned bool) {
	t := strings.ToLower(strings.TrimSpace(dataType))
	rest := ""
	if open := strings.IndexByte(t, '('); open >= 0 {
		base = t[:open]
		if end := strings.LastIndexByte(t, ')'); end > open {
			rest = t[end+1:]
		}
	} else if space := strings.IndexByte(t, ' '); space >= 0 {
		base, rest = t[:space], t[space:]
	} else {
		base = t
	}
	for _, word := range strings.Fields(rest) {
		if word == "unsigned" {
			unsigned = true
		}
	}
	return strings.TrimSpace(base), unsigned
}

// Canonical maps a MySQL type name onto the Postgres name for the same kind of
// value. A name it does not recognise comes back as it went in.
func (mysqlDialect) Canonical(dataType string) string {
	if canon, ok := mysqlCanonicalNames[strings.ToLower(strings.TrimSpace(dataType))]; ok {
		return canon
	}
	base, unsigned := mysqlBase(dataType)
	if unsigned {
		if canon, ok := mysqlUnsigned[base]; ok {
			return canon
		}
	}
	if canon, ok := mysqlTypes[base]; ok {
		return canon
	}
	return dataType
}

func (mysqlDialect) QuoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func (d mysqlDialect) Qualify(t Table) string {
	if t.Schema == "" {
		return d.QuoteIdent(t.Name)
	}
	return d.QuoteIdent(t.Schema) + "." + d.QuoteIdent(t.Name)
}

// Placeholder is MySQL's one marker, numbered in a comment. See point 1 on the
// type: the number is what ArgsInTextOrder binds by.
func (mysqlDialect) Placeholder(n int) string { return fmt.Sprintf("?/*%d*/", n) }

// mysqlMarker finds a numbered placeholder in a statement this dialect wrote.
var mysqlMarker = regexp.MustCompile(`\?/\*([0-9]+)\*/`)

// ArgsInTextOrder reorders arguments numbered the way the executor numbers
// them into the order a MySQL statement's markers appear in, which is the only
// order MySQL binds by. A number may appear more than once, which is how a
// composite resume bound compares one value twice.
//
// It refuses a statement whose markers name an argument that does not exist,
// and one carrying a bare `?` it did not number, rather than binding the
// arguments it has to whichever markers happen to be first.
func ArgsInTextOrder(sql string, args []any) ([]any, error) {
	matches := mysqlMarker.FindAllStringSubmatchIndex(sql, -1)
	if bare := bareMarkers(sql); bare != len(matches) {
		return nil, fmt.Errorf(
			"masking: the statement has %d parameter markers and %d of them are numbered, so "+
				"there is no way to know which argument an unnumbered one takes: %s",
			bare, len(matches), sql)
	}
	out := make([]any, 0, len(matches))
	for _, m := range matches {
		n, err := strconv.Atoi(sql[m[2]:m[3]])
		if err != nil || n < 1 || n > len(args) {
			return nil, fmt.Errorf(
				"masking: the statement names argument %s and there are %d: %s",
				sql[m[2]:m[3]], len(args), sql)
		}
		out = append(out, args[n-1])
	}
	return out, nil
}

// bareMarkers counts the `?` characters MySQL would read as parameters: those
// outside a quoted identifier, a string and a comment. A column may be called
// anything, `what?` included, and a question mark inside its quoting is a name
// rather than a parameter.
func bareMarkers(sql string) int {
	n := 0
	for i := 0; i < len(sql); i++ {
		switch c := sql[i]; {
		case c == '`' || c == '\'' || c == '"':
			// To the matching quote. A doubled quote is the quote character
			// itself and continues the run, and a backslash escapes the next
			// byte inside a string.
			for i++; i < len(sql); i++ {
				if sql[i] == '\\' && c != '`' {
					i++
					continue
				}
				if sql[i] == c {
					if i+1 < len(sql) && sql[i+1] == c {
						i++
						continue
					}
					break
				}
			}
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				return n
			}
			i += end + 3
		case c == '?':
			n++
		}
	}
	return n
}

// Unaddressable refuses a table with no primary key.
//
// InnoDB keeps a hidden row identifier for such a table, and nothing outside
// the server can read it or address a row by it, so a statement cannot rewrite
// one row of the table and know it rewrote that row and no other. A unique key
// would do in principle; a primary key is what the planner orders and resumes
// by, and asking for one is the smaller and the more honest refusal.
func (mysqlDialect) Unaddressable(t Table) string {
	if len(t.PrimaryKey) == 0 {
		return "MySQL has no row identifier a statement can address, so a table with no " +
			"primary key cannot be rewritten one row at a time; give the table a primary key, " +
			"or give its columns rules that leave them as they are"
	}
	return ""
}

// RowKey reads every primary key column as it is stored.
//
// Not cast to text on the way out, unlike Postgres: the driver hands back the
// text form of a number or a date already, and a binary key cast to CHAR would
// be read through the connection's character set and stop being the bytes the
// row is addressed by.
func (d mysqlDialect) RowKey(tp TablePlan) []string {
	out := make([]string, len(tp.OrderBy))
	for i, k := range tp.OrderBy {
		out[i] = d.QuoteIdent(k)
	}
	return out
}

// mysqlNumericCast matches the precision and scale of a decimal type.
var mysqlNumericCast = regexp.MustCompile(`^\(\s*([0-9]+)\s*(?:,\s*([0-9]+)\s*)?\)`)

// mysqlFraction matches the fractional seconds precision of a temporal type.
var mysqlFraction = regexp.MustCompile(`^\(\s*([0-9])\s*\)`)

// keyParameter renders the nth parameter for a key column, cast to the column's
// type wherever comparing the text form would not be exact. See point 2 on the
// type.
func (d mysqlDialect) keyParameter(c ColumnInfo, n int) string {
	p := d.Placeholder(n)
	sqlType := strings.ToLower(strings.TrimSpace(c.SQLType))
	if sqlType == "" {
		sqlType = strings.ToLower(strings.TrimSpace(c.Type))
	}
	base, unsigned := mysqlBase(sqlType)
	args := ""
	if open := strings.IndexByte(sqlType, '('); open >= 0 {
		args = sqlType[open:]
	}
	switch base {
	case "tinyint", "smallint", "mediumint", "int", "integer", "bigint", "year":
		if unsigned {
			return "CAST(" + p + " AS UNSIGNED)"
		}
		return "CAST(" + p + " AS SIGNED)"
	case "decimal", "numeric", "dec", "fixed":
		if m := mysqlNumericCast.FindStringSubmatch(args); m != nil {
			if m[2] != "" {
				return fmt.Sprintf("CAST(%s AS DECIMAL(%s,%s))", p, m[1], m[2])
			}
			return fmt.Sprintf("CAST(%s AS DECIMAL(%s))", p, m[1])
		}
		return "CAST(" + p + " AS DECIMAL(65,30))"
	case "date":
		return "CAST(" + p + " AS DATE)"
	case "datetime", "timestamp":
		if m := mysqlFraction.FindStringSubmatch(args); m != nil {
			return fmt.Sprintf("CAST(%s AS DATETIME(%s))", p, m[1])
		}
		return "CAST(" + p + " AS DATETIME)"
	case "time":
		if m := mysqlFraction.FindStringSubmatch(args); m != nil {
			return fmt.Sprintf("CAST(%s AS TIME(%s))", p, m[1])
		}
		return "CAST(" + p + " AS TIME)"
	}
	// A string, a binary string, an enum: compared as stored, with the
	// column's own collation, which is what the index is built with.
	return p
}

// keyEquals renders `k1 = p1 AND k2 = p2` for the first n key columns.
func (d mysqlDialect) keyEquals(tp TablePlan, n, first int) []string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		k := tp.OrderBy[i]
		out[i] = d.QuoteIdent(k) + " = " + d.keyParameter(tp.Table.ColumnNamed(k), first+i)
	}
	return out
}

// SelectChunk reads one chunk in key order, resuming after an address.
//
// The bound on a composite key is spelled out column by column rather than as
// a row comparison. MySQL accepts `(a, b) > (?, ?)` and does not use the key's
// index for it, so every chunk would read the table from the start; the
// expanded form is a union of ranges on the index prefix, which it does use.
func (d mysqlDialect) SelectChunk(tp TablePlan, after []string) Query {
	cols := make([]string, 0, len(tp.Columns)+len(tp.OrderBy))
	cols = append(cols, d.RowKey(tp)...)
	for _, c := range tp.Columns {
		cols = append(cols, d.QuoteIdent(c.Column.Name))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "SELECT %s FROM %s", strings.Join(cols, ", "), d.Qualify(tp.Table))
	var args []any
	if len(tp.OrderBy) > 0 && len(after) == len(tp.OrderBy) {
		alternatives := make([]string, len(tp.OrderBy))
		for i, k := range tp.OrderBy {
			terms := d.keyEquals(tp, i, 1)
			terms = append(terms, d.QuoteIdent(k)+" > "+
				d.keyParameter(tp.Table.ColumnNamed(k), i+1))
			alternatives[i] = "(" + strings.Join(terms, " AND ") + ")"
		}
		fmt.Fprintf(&b, " WHERE %s", strings.Join(alternatives, " OR "))
		for _, v := range after {
			args = append(args, v)
		}
	}
	if len(tp.OrderBy) > 0 {
		order := make([]string, len(tp.OrderBy))
		for i, k := range tp.OrderBy {
			order[i] = d.QuoteIdent(k)
		}
		fmt.Fprintf(&b, " ORDER BY %s", strings.Join(order, ", "))
	}
	if tp.ChunkSize > 0 {
		fmt.Fprintf(&b, " LIMIT %d", tp.ChunkSize)
	}
	return Query{SQL: b.String(), Args: args}
}

// Update rewrites the planned columns of one row, addressed by its whole
// primary key. The markers are numbered the executor's way, address first, and
// ArgsInTextOrder binds them in the order the text puts them.
func (d mysqlDialect) Update(tp TablePlan) Statement {
	width := tp.AddressWidth()
	names := make([]string, 0, len(tp.Columns))
	sets := make([]string, 0, len(tp.Columns))
	for i, c := range tp.Columns {
		names = append(names, c.Column.Name)
		sets = append(sets, fmt.Sprintf("%s = %s",
			d.QuoteIdent(c.Column.Name), d.Placeholder(width+i+1)))
	}
	where := strings.Join(d.keyEquals(tp, len(tp.OrderBy), 1), " AND ")
	if where == "" {
		// Unaddressable refuses such a table at planning time, so this is only
		// reached by a plan built by hand. A statement that matches nothing is
		// the safe thing to hand back; one that matches every row is not.
		where = "FALSE"
	}
	return Statement{
		SQL: fmt.Sprintf("UPDATE %s SET %s WHERE %s",
			d.Qualify(tp.Table), strings.Join(sets, ", "), where),
		Table: tp.Table.String(), Columns: names,
		Keyed: len(tp.OrderBy) > 0,
	}
}
