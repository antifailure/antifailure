package insights

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Rule names one way a migration that succeeds in review takes production
// down. Every rule has a rationale and a fix, both carried on the finding,
// because a lint that says "unsafe" and stops is a lint people disable.
type Rule string

const (
	// RuleNotNullNoDefault is a NOT NULL column added with no default.
	RuleNotNullNoDefault Rule = "not_null_without_default"
	// RuleAlterColumnType is a type change that rewrites the table.
	RuleAlterColumnType Rule = "alter_column_type"
	// RuleIndexNotConcurrent is CREATE INDEX without CONCURRENTLY.
	RuleIndexNotConcurrent Rule = "index_not_concurrent"
	// RuleForeignKeyNotValid is a foreign key added without NOT VALID.
	RuleForeignKeyNotValid Rule = "foreign_key_not_valid"
	// RuleRenameColumnInUse is renaming a column something still reads.
	RuleRenameColumnInUse Rule = "rename_column_in_use"
	// RuleDropColumnInView is dropping a column a view still selects.
	RuleDropColumnInView Rule = "drop_column_in_view"
)

// AllRules is every rule, in the order the documentation lists them. Kept so
// the docs page and the code cannot drift: a test walks this list.
func AllRules() []Rule {
	return []Rule{
		RuleNotNullNoDefault, RuleAlterColumnType, RuleIndexNotConcurrent,
		RuleForeignKeyNotValid, RuleRenameColumnInUse, RuleDropColumnInView,
	}
}

// LintFinding is one statement a rule objected to.
type LintFinding struct {
	Rule Rule `json:"rule"`
	// Migration and Statement locate it in the repository.
	Migration string `json:"migration"`
	Statement string `json:"statement"`
	// Table is what it touches, and Rows is how many rows that table holds on
	// the branch. Rows is what turns "this rewrites the table" from a note
	// into an outage, so it is on the finding rather than in a footnote.
	Table string `json:"table,omitempty"`
	Rows  int64  `json:"rows,omitempty"`
	// Detail says what will happen. Fix says what to write instead.
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

// Schema is what the database looked like before the migrations ran.
//
// The lint needs it because most of these rules are only true about a
// particular database: adding a NOT NULL column is fine on an empty table and
// fails on a full one, and dropping a column is fine until a view selects it.
// A lint that reads only the SQL has to guess at both, and guessing produces
// exactly the false positives that get a check switched off.
type Schema struct {
	// Rows is live rows per table, unqualified, from the planner's own
	// estimate. Exact counts would mean a sequential scan of every table.
	Rows map[string]int64
	// ViewsUsing maps "table.column" to the views that select it.
	ViewsUsing map[string][]string
	// IndexesUsing maps "table.column" to the indexes built on it.
	IndexesUsing map[string][]string
	// Columns maps every "table.column" that exists to its type name. The
	// type is what makes rule two accurate: whether a change rewrites the
	// table depends on the type it is coming FROM, and the statement only
	// says what it is going to.
	Columns map[string]string
}

// CaptureSchema reads what the lint needs to know about a database.
func CaptureSchema(ctx context.Context, conn *pgx.Conn) (Schema, error) {
	s := Schema{
		Rows:         map[string]int64{},
		ViewsUsing:   map[string][]string{},
		IndexesUsing: map[string][]string{},
		Columns:      map[string]string{},
	}

	rows, err := conn.Query(ctx, `
SELECT c.relname, GREATEST(c.reltuples, 0)::bigint
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema')`)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			rows.Close()
			return s, err
		}
		s.Rows[name] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return s, err
	}

	rows, err = conn.Query(ctx, `
SELECT table_name, column_name, udt_name, COALESCE(character_maximum_length, 0)
FROM information_schema.columns
WHERE table_schema NOT IN ('pg_catalog','information_schema')`)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var table, column, udt string
		var length int
		if err := rows.Scan(&table, &column, &udt, &length); err != nil {
			rows.Close()
			return s, err
		}
		name := udt
		if length > 0 {
			name = fmt.Sprintf("%s(%d)", udt, length)
		}
		s.Columns[table+"."+column] = name
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return s, err
	}

	// view_column_usage is the catalogue's own answer to "which views read
	// this column", which is the question rule six asks. Working it out from
	// the view definition text would mean parsing SQL to answer something
	// Postgres already tracks in pg_depend.
	rows, err = conn.Query(ctx, `
SELECT DISTINCT table_name, column_name, view_name
FROM information_schema.view_column_usage
WHERE table_schema NOT IN ('pg_catalog','information_schema')`)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var table, column, view string
		if err := rows.Scan(&table, &column, &view); err != nil {
			rows.Close()
			return s, err
		}
		key := table + "." + column
		s.ViewsUsing[key] = append(s.ViewsUsing[key], view)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return s, err
	}

	rows, err = conn.Query(ctx, `
SELECT t.relname, a.attname, i.relname
FROM pg_index x
JOIN pg_class i ON i.oid = x.indexrelid
JOIN pg_class t ON t.oid = x.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(x.indkey)
WHERE n.nspname NOT IN ('pg_catalog','information_schema')`)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var table, column, index string
		if err := rows.Scan(&table, &column, &index); err != nil {
			rows.Close()
			return s, err
		}
		key := table + "." + column
		s.IndexesUsing[key] = append(s.IndexesUsing[key], index)
	}
	rows.Close()
	return s, rows.Err()
}

// Lint checks statements against every rule.
//
// largeRows is the manifest's large_table_rows. It decides severity rather
// than whether a rule fires at all, because a rewrite of a small table is
// still a rewrite and a reader deciding what to do with it wants the row
// count, not our opinion of it.
func Lint(stmts []Statement, schema Schema, largeRows int) []LintFinding {
	var out []LintFinding
	for _, st := range stmts {
		out = append(out, lintStatement(st, schema, largeRows)...)
	}
	return out
}

func lintStatement(st Statement, schema Schema, largeRows int) []LintFinding {
	upper := fold(st.SQL)
	var out []LintFinding

	if strings.HasPrefix(upper, "CREATE INDEX") || strings.HasPrefix(upper, "CREATE UNIQUE INDEX") {
		if !strings.Contains(upper, "CONCURRENTLY") {
			table := tableAfter(st.SQL, " ON ")
			out = append(out, LintFinding{
				Rule: RuleIndexNotConcurrent, Migration: st.Migration, Statement: st.SQL,
				Table: table, Rows: schema.Rows[table],
				Detail: "CREATE INDEX takes a SHARE lock on the table, which blocks every " +
					"INSERT, UPDATE and DELETE until the index is built. On a large table " +
					"that is the whole build, not a moment.",
				Fix: "CREATE INDEX CONCURRENTLY. It cannot run inside a transaction, so the " +
					"migration has to be split or the tool told not to wrap it, and a failed " +
					"build leaves an invalid index to drop and retry.",
			})
		}
	}

	if !strings.HasPrefix(upper, "ALTER TABLE") {
		return out
	}
	table := tableAfter(st.SQL, "ALTER TABLE")
	rows := schema.Rows[table]

	switch {
	case strings.Contains(upper, "ADD COLUMN") && strings.Contains(upper, "NOT NULL") &&
		!strings.Contains(upper, "DEFAULT"):
		detail := "Adding a NOT NULL column with no default is refused outright on a table " +
			"that has any rows, because every existing row would violate it."
		if rows > int64(largeRows) {
			detail += fmt.Sprintf(" This table holds about %d rows.", rows)
		}
		out = append(out, LintFinding{
			Rule: RuleNotNullNoDefault, Migration: st.Migration, Statement: st.SQL,
			Table: table, Rows: rows, Detail: detail,
			Fix: "Add the column nullable, backfill it in batches, then add the NOT NULL " +
				"constraint as NOT VALID and validate it separately. A constant DEFAULT also " +
				"works from Postgres 11 and does not rewrite the table.",
		})

	case strings.Contains(upper, "ALTER COLUMN") && strings.Contains(upper, " TYPE "):
		column := wordAfter(st.SQL, "ALTER COLUMN")
		from := schema.Columns[table+"."+column]
		if to, safe := rewritesTable(from, upper); !safe {
			if from == "" {
				from = "its previous type"
			}
			out = append(out, LintFinding{
				Rule: RuleAlterColumnType, Migration: st.Migration, Statement: st.SQL,
				Table: table, Rows: rows,
				Detail: fmt.Sprintf(
					"Changing a column to %s rewrites the whole table under an ACCESS "+
						"EXCLUSIVE lock, so nothing can read it either. This table holds about "+
						"%d rows.", to, rows),
				Fix: "Add a new column of the new type, backfill it, switch reads and writes " +
					"over, then drop the old one. Postgres only skips the rewrite when the " +
					"types are binary coercible, which " + from + " to " + to + " is not.",
			})
		}

	case strings.Contains(upper, "ADD CONSTRAINT") && strings.Contains(upper, "FOREIGN KEY"),
		strings.Contains(upper, "ADD FOREIGN KEY"):
		if !strings.Contains(upper, "NOT VALID") {
			out = append(out, LintFinding{
				Rule: RuleForeignKeyNotValid, Migration: st.Migration, Statement: st.SQL,
				Table: table, Rows: rows,
				Detail: "Adding a foreign key scans every existing row to validate it, holding " +
					"a SHARE ROW EXCLUSIVE lock on both tables for the whole scan, which blocks " +
					"writes to the table being referenced as well.",
				Fix: "ADD CONSTRAINT ... NOT VALID, then ALTER TABLE ... VALIDATE CONSTRAINT in " +
					"a second migration. Validation takes only a SHARE UPDATE EXCLUSIVE lock and " +
					"new rows are checked from the moment the constraint exists either way.",
			})
		}

	case strings.Contains(upper, "RENAME COLUMN"):
		column := renamedColumn(st.SQL)
		key := table + "." + column
		users := append(append([]string{}, schema.ViewsUsing[key]...), schema.IndexesUsing[key]...)
		detail := "A rename is not backward compatible. Between the migration and the last " +
			"old instance shutting down, the running application asks for a column that no " +
			"longer exists, and a rolling deploy guarantees that window exists."
		if len(users) > 0 {
			detail += " In the database it is read by " + strings.Join(users, ", ") + "."
		}
		out = append(out, LintFinding{
			Rule: RuleRenameColumnInUse, Migration: st.Migration, Statement: st.SQL,
			Table: table, Rows: rows, Detail: detail,
			Fix: "Add the new column, write to both, migrate readers, then drop the old one. " +
				"Four deploys instead of one, and none of them has a window where the " +
				"application is wrong.",
		})

	case strings.Contains(upper, "DROP COLUMN"):
		column := droppedColumn(st.SQL)
		views := schema.ViewsUsing[table+"."+column]
		if len(views) > 0 {
			detail := "The view " + strings.Join(views, ", ") + " selects this column. " +
				"Postgres refuses the drop unless CASCADE is given"
			if strings.Contains(upper, "CASCADE") {
				detail = "The view " + strings.Join(views, ", ") + " selects this column, and " +
					"CASCADE drops the view with it, silently and without a further error"
			}
			out = append(out, LintFinding{
				Rule: RuleDropColumnInView, Migration: st.Migration, Statement: st.SQL,
				Table: table, Rows: rows, Detail: detail + ".",
				Fix: "Change or drop the view first, in its own migration, so the dependency is " +
					"removed deliberately rather than as a side effect.",
			})
		}
	}
	return out
}

// rewritesTable reports the target type and whether changing to it avoids
// rewriting the table.
//
// Postgres skips the rewrite when the two types share an on disk
// representation and the new one cannot be narrower, which is a short list:
// widening or unbounding a varchar, varchar or char to text, widening a
// numeric, and timestamp to timestamptz when the session is UTC. Everything
// else, int to bigint most notably, rewrites every row under an ACCESS
// EXCLUSIVE lock.
//
// An unknown source type is treated as a rewrite. That is the safe direction
// to be wrong in: a false warning costs a reader ten seconds and a missed
// rewrite costs an outage.
func rewritesTable(from, upperSQL string) (to string, safe bool) {
	i := strings.Index(upperSQL, " TYPE ")
	if i < 0 {
		return "", false
	}
	fields := strings.Fields(upperSQL[i+len(" TYPE "):])
	if len(fields) == 0 {
		return "", false
	}
	to = strings.ToLower(fields[0])
	if len(fields) > 1 && strings.HasPrefix(fields[1], "(") {
		to += strings.ToLower(fields[1])
	}
	if from == "" {
		return to, false
	}

	fromBase, fromLen := splitType(strings.ToLower(from))
	toBase, toLen := splitType(to)

	switch {
	case toBase == "text" && (fromBase == "varchar" || fromBase == "bpchar" || fromBase == "char"):
		return to, true
	case fromBase == "varchar" && toBase == "varchar":
		// Unbounded is wider than any bound; a smaller bound rewrites and can
		// fail partway through.
		return to, toLen == 0 || (fromLen != 0 && toLen >= fromLen)
	case fromBase == "text" && toBase == "varchar":
		return to, toLen == 0
	case fromBase == "numeric" && toBase == "numeric":
		return to, toLen == 0
	case fromBase == "timestamp" && toBase == "timestamptz":
		return to, true
	default:
		return to, false
	}
}

// splitType cuts "varchar(64)" into its base name and its length.
func splitType(t string) (string, int) {
	base, rest, found := strings.Cut(t, "(")
	if !found {
		return base, 0
	}
	n := 0
	for i := 0; i < len(rest) && rest[i] >= '0' && rest[i] <= '9'; i++ {
		n = n*10 + int(rest[i]-'0')
	}
	return base, n
}

// tableAfter pulls the identifier following a keyword, skipping the noise
// words that can sit between it and the name.
func tableAfter(sql, keyword string) string {
	upper := fold(sql)
	i := strings.Index(upper, fold(keyword))
	if i < 0 {
		return ""
	}
	// fold collapses whitespace, so an index into it is not an index into sql.
	// Work on the folded text and take the identifier from there; identifiers
	// are returned lower cased anyway.
	rest := strings.Fields(upper[i+len(fold(keyword)):])
	for _, word := range rest {
		switch word {
		case "IF", "NOT", "EXISTS", "ONLY", "CONCURRENTLY":
			continue
		}
		return bareTable(unquote(word))
	}
	return ""
}

func renamedColumn(sql string) string { return wordAfter(sql, "RENAME COLUMN") }

func droppedColumn(sql string) string {
	upper := fold(sql)
	i := strings.Index(upper, "DROP COLUMN")
	if i < 0 {
		return ""
	}
	for _, word := range strings.Fields(upper[i+len("DROP COLUMN"):]) {
		if word == "IF" || word == "EXISTS" {
			continue
		}
		return unquote(word)
	}
	return ""
}

func wordAfter(sql, keyword string) string {
	upper := fold(sql)
	i := strings.Index(upper, keyword)
	if i < 0 {
		return ""
	}
	fields := strings.Fields(upper[i+len(keyword):])
	if len(fields) == 0 {
		return ""
	}
	return unquote(fields[0])
}
