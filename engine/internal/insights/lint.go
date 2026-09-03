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
	// RuleNoLockTimeout is a migration that takes a blocking lock with no
	// lock_timeout set, so a lock wait becomes an outage.
	RuleNoLockTimeout Rule = "no_lock_timeout"
	// RuleSetNotNull is SET NOT NULL on a column that already exists.
	RuleSetNotNull Rule = "set_not_null_existing_column"
	// RuleCheckNotValid is a CHECK constraint added without NOT VALID.
	RuleCheckNotValid Rule = "check_constraint_not_valid"
	// RuleUniqueConstraint is ADD CONSTRAINT ... UNIQUE, which builds its
	// index in place.
	RuleUniqueConstraint Rule = "unique_constraint_builds_index"
	// RuleBackfillWithDDL is a backfill in the same transaction as the schema
	// change it belongs to.
	RuleBackfillWithDDL Rule = "backfill_in_ddl_transaction"
	// RuleDropIndexNotConcurrent is DROP INDEX without CONCURRENTLY.
	RuleDropIndexNotConcurrent Rule = "drop_index_not_concurrent"
	// RuleReindexNotConcurrent is REINDEX without CONCURRENTLY.
	RuleReindexNotConcurrent Rule = "reindex_not_concurrent"
	// RuleVacuumFull is VACUUM FULL, which rewrites the table offline.
	RuleVacuumFull Rule = "vacuum_full"
	// RuleCluster is CLUSTER, which rewrites the table offline.
	RuleCluster Rule = "cluster"
	// RuleDropTable is dropping a table.
	RuleDropTable Rule = "drop_table"
	// RuleTruncate is truncating a table.
	RuleTruncate Rule = "truncate"
)

// AllRules is every rule, in the order the documentation lists them. Kept so
// the catalogue and the code cannot drift: TestEveryRuleIsInAllRules walks it
// against the constants, and tools/lintcheck walks the constants against
// lintcatalog.yaml.
func AllRules() []Rule {
	return []Rule{
		RuleNoLockTimeout,
		RuleNotNullNoDefault, RuleSetNotNull, RuleAlterColumnType,
		RuleIndexNotConcurrent, RuleDropIndexNotConcurrent, RuleReindexNotConcurrent,
		RuleForeignKeyNotValid, RuleCheckNotValid, RuleUniqueConstraint,
		RuleBackfillWithDDL,
		RuleRenameColumnInUse, RuleDropColumnInView,
		RuleVacuumFull, RuleCluster, RuleDropTable, RuleTruncate,
	}
}

// LintFinding is one statement a rule objected to.
type LintFinding struct {
	// ID is the stable identifier, LINT-004 and its kind. It is assigned once
	// and never reused, and it is what a filter or a suppression should match
	// on. Rule beside it is prose: rules are renamed as they sharpen, and a
	// name that cannot be improved is a rule that cannot be improved.
	ID   FindingID `json:"id"`
	Rule Rule      `json:"rule"`
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
	// LockTimeout is what the server hands a session that sets nothing, as
	// current_setting reports it: "0" when there is none.
	//
	// It is read because a great many projects set lock_timeout on the role
	// or on the database rather than in the migration file, and warning those
	// projects that they have no lock_timeout would be false and would be the
	// first finding they learned to ignore.
	LockTimeout string
}

// CaptureSchema reads what the lint needs to know about a database.
func CaptureSchema(ctx context.Context, conn *pgx.Conn) (Schema, error) {
	s := Schema{
		Rows:         map[string]int64{},
		ViewsUsing:   map[string][]string{},
		IndexesUsing: map[string][]string{},
		Columns:      map[string]string{},
	}

	// A failure here is not fatal. The value only decides whether one rule
	// stays quiet, and a rule that fires when it should not costs a reader ten
	// seconds, while a schema capture that fails costs them every rule.
	if err := conn.QueryRow(ctx, "SELECT current_setting('lock_timeout')").
		Scan(&s.LockTimeout); err != nil {
		s.LockTimeout = ""
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
	// The lock_timeout rule is about the migration rather than about any one
	// statement, and it comes first because it is the one whose absence turns
	// every other finding here from a pause into an outage.
	if f, ok := lintLockTimeout(stmts, schema); ok {
		out = append(out, f)
	}
	locking := lockingDDLPerMigration(stmts, schema)
	for _, st := range stmts {
		out = append(out, lintStatement(st, schema, largeRows, locking)...)
	}
	// The identifier is stamped here rather than at each rule, so that a rule
	// added tomorrow cannot ship a finding with an empty one by forgetting a
	// field. It comes from the generated catalogue, and tools/lintcheck fails
	// the build when a rule has no entry there.
	for i := range out {
		out[i].ID = out[i].Rule.ID()
	}
	return out
}

func lintStatement(
	st Statement, schema Schema, largeRows int, locking map[string]blockingDDL,
) []LintFinding {
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

	out = append(out, lintMaintenance(st, upper, schema)...)
	out = append(out, lintBackfill(st, upper, schema, locking)...)

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

	case strings.Contains(upper, "ALTER COLUMN") && strings.Contains(upper, "SET NOT NULL"):
		column := wordAfter(st.SQL, "ALTER COLUMN")
		out = append(out, LintFinding{
			Rule: RuleSetNotNull, Migration: st.Migration, Statement: st.SQL,
			Table: table, Rows: rows,
			Detail: fmt.Sprintf(
				"SET NOT NULL reads every row of %s to prove none of them is null, and it "+
					"holds an ACCESS EXCLUSIVE lock for the whole scan, so nothing can read "+
					"the table either. This table holds about %d rows.", table, rows),
			Fix: fmt.Sprintf(
				"Add CHECK (%s IS NOT NULL) NOT VALID first, VALIDATE CONSTRAINT it in a "+
					"second migration under a SHARE UPDATE EXCLUSIVE lock that reads and "+
					"writes pass through, and only then SET NOT NULL. From Postgres 12 a "+
					"validated CHECK of that exact shape is proof enough, so the scan is "+
					"skipped and the ACCESS EXCLUSIVE lock is held for a catalogue update "+
					"rather than for a read of %d rows. Drop the CHECK afterwards.",
				column, rows),
		})

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

	case strings.Contains(upper, "ADD CONSTRAINT") && strings.Contains(upper, "CHECK"),
		strings.Contains(upper, "ADD CHECK"):
		if !strings.Contains(upper, "NOT VALID") {
			out = append(out, LintFinding{
				Rule: RuleCheckNotValid, Migration: st.Migration, Statement: st.SQL,
				Table: table, Rows: rows,
				Detail: fmt.Sprintf(
					"Adding a CHECK constraint reads every existing row to validate it, under "+
						"an ACCESS EXCLUSIVE lock held for the whole scan, so nothing can read "+
						"or write %s while it runs. This table holds about %d rows.", table, rows),
				Fix: "ADD CONSTRAINT ... CHECK (...) NOT VALID, which holds the lock only long " +
					"enough to write the catalogue row, then ALTER TABLE ... VALIDATE CONSTRAINT " +
					"in a second migration under a SHARE UPDATE EXCLUSIVE lock that reads and " +
					"writes pass through. New rows are checked from the moment the constraint " +
					"exists either way, so nothing invalid gets in during the gap.",
			})
		}

	case strings.Contains(upper, "ADD CONSTRAINT") && strings.Contains(upper, "UNIQUE"),
		strings.Contains(upper, "ADD UNIQUE"):
		// USING INDEX is the second half of the fix this rule recommends, and
		// firing on it would warn somebody about the safe form of the change
		// they made to avoid the unsafe one.
		if strings.Contains(upper, "USING INDEX") {
			break
		}
		out = append(out, LintFinding{
			Rule: RuleUniqueConstraint, Migration: st.Migration, Statement: st.SQL,
			Table: table, Rows: rows,
			Detail: fmt.Sprintf(
				"A unique constraint is an index with a catalogue entry, and ADD CONSTRAINT "+
					"builds that index in place, without CONCURRENTLY, under an ACCESS "+
					"EXCLUSIVE lock held for the whole build. Nothing can read or write %s "+
					"until it finishes, and it holds about %d rows.", table, rows),
			Fix: "CREATE UNIQUE INDEX CONCURRENTLY in one migration, which cannot run inside a " +
				"transaction, then ALTER TABLE ... ADD CONSTRAINT ... UNIQUE USING INDEX in a " +
				"second, which takes the ACCESS EXCLUSIVE lock only long enough to rewrite the " +
				"catalogue row. Give the index the name you want the constraint to have: USING " +
				"INDEX renames the index to the constraint's name, and finding out afterwards " +
				"is how the two come to disagree in a schema dump.",
		})

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
		case "IF", "NOT", "EXISTS", "ONLY", "CONCURRENTLY", "TABLE", "VERBOSE":
			continue
		}
		return bareTable(unquote(word))
	}
	return ""
}

// blockingDDL is a statement that takes a lock ordinary traffic cannot pass
// through: which table waits for it, and under which mode.
type blockingDDL struct {
	Table string
	Mode  string
	SQL   string
}

// classify reports the table a statement locks against ordinary traffic and
// the mode it locks it in.
//
// The CONCURRENTLY spellings deliberately return false. They take a SHARE
// UPDATE EXCLUSIVE lock, which reads and writes pass through, and the whole
// point of the rules that recommend them is that a migration written that way
// is one nothing else has to wait for.
func classify(sql, upper string, schema Schema) (blockingDDL, bool) {
	hit := func(table, mode string) (blockingDDL, bool) {
		return blockingDDL{Table: table, Mode: mode, SQL: sql}, true
	}
	switch {
	case strings.HasPrefix(upper, "ALTER TABLE"):
		return hit(tableAfter(sql, "ALTER TABLE"), "ACCESS EXCLUSIVE")
	case strings.HasPrefix(upper, "DROP TABLE"):
		return hit(tableAfter(sql, "DROP TABLE"), "ACCESS EXCLUSIVE")
	case strings.HasPrefix(upper, "TRUNCATE"):
		return hit(tableAfter(sql, "TRUNCATE"), "ACCESS EXCLUSIVE")
	case strings.HasPrefix(upper, "CLUSTER"):
		return hit(tableAfter(sql, "CLUSTER"), "ACCESS EXCLUSIVE")
	case strings.HasPrefix(upper, "DROP INDEX"):
		if strings.Contains(upper, "CONCURRENTLY") {
			return blockingDDL{}, false
		}
		return hit(tableOfIndex(schema, tableAfter(sql, "DROP INDEX")), "ACCESS EXCLUSIVE")
	case strings.HasPrefix(upper, "CREATE INDEX"), strings.HasPrefix(upper, "CREATE UNIQUE INDEX"):
		if strings.Contains(upper, "CONCURRENTLY") {
			return blockingDDL{}, false
		}
		return hit(tableAfter(sql, " ON "), "SHARE")
	case strings.HasPrefix(upper, "REINDEX"):
		kind, name := reindexTarget(upper)
		if strings.Contains(upper, "CONCURRENTLY") {
			return blockingDDL{}, false
		}
		switch kind {
		case "TABLE":
			return hit(name, "SHARE")
		case "INDEX":
			return hit(tableOfIndex(schema, name), "SHARE")
		}
		// SCHEMA, DATABASE and SYSTEM name no one table, and SYSTEM cannot be
		// made concurrent at all, so there is nothing useful to say about
		// which table waits.
		return blockingDDL{}, false
	case strings.HasPrefix(upper, "VACUUM"):
		table, full := vacuumTarget(upper)
		if !full {
			return blockingDDL{}, false
		}
		return hit(table, "ACCESS EXCLUSIVE")
	}
	return blockingDDL{}, false
}

// lintLockTimeout is the rule about the migration rather than about one
// statement, and it is the one that takes sites down.
//
// A lock request that cannot be granted immediately does not wait politely to
// one side. It joins the queue for the table, and every statement that arrives
// after it queues behind the request rather than behind the table, including
// the SELECTs that would otherwise have been allowed straight through. So an
// ALTER TABLE that would have taken four milliseconds, blocked behind one long
// running transaction, stops all traffic on that table for as long as that
// transaction runs.
//
// The fix is one line, which is why the absence of it is worth a finding of its
// own rather than a sentence inside every other one.
func lintLockTimeout(stmts []Statement, schema Schema) (LintFinding, bool) {
	if configured(schema.LockTimeout) {
		return LintFinding{}, false
	}
	for _, st := range stmts {
		if setsLockTimeout(fold(st.SQL)) {
			return LintFinding{}, false
		}
	}
	for _, st := range stmts {
		ddl, ok := classify(st.SQL, fold(st.SQL), schema)
		if !ok {
			continue
		}
		// The table is unknown when the statement names an index the branch's
		// catalogue does not have, which is every index the migrations
		// themselves created. The rule is still right; only the name is
		// missing, and a sentence with a hole in it reads like a bug.
		locked, its := "the table it touches", "that table"
		if ddl.Table != "" {
			locked, its = ddl.Table, ddl.Table
		}
		detail := fmt.Sprintf(
			"Nothing in these migrations sets lock_timeout, and this statement locks %s in %s "+
				"mode. A lock request that is not granted immediately queues, and every "+
				"query that arrives after it queues behind the request rather than behind the "+
				"table, so a statement that would have taken milliseconds stops all traffic on "+
				"%s for as long as whatever it is waiting for runs.",
			locked, ddl.Mode, its)
		if rows := schema.Rows[ddl.Table]; rows > 0 {
			detail += fmt.Sprintf(" That table holds about %d rows.", rows)
		}
		return LintFinding{
			Rule: RuleNoLockTimeout, Migration: st.Migration, Statement: st.SQL,
			Table: ddl.Table, Rows: schema.Rows[ddl.Table], Detail: detail,
			Fix: "SET lock_timeout = '3s' before the first statement, or SET LOCAL " +
				"lock_timeout = '3s' inside the transaction, and have the deploy retry the " +
				"migration. The statement then gives up instead of queueing, which turns a " +
				"stalled table into a failed migration somebody runs again. Setting it on the " +
				"migration role with ALTER ROLE ... SET lock_timeout covers every migration " +
				"rather than this one, and this rule reads that setting from the server.",
		}, true
	}
	return LintFinding{}, false
}

// configured reports whether a server setting is set to anything at all.
// current_setting returns the string "0" for a timeout that is off.
func configured(setting string) bool {
	setting = strings.TrimSpace(setting)
	return setting != "" && setting != "0"
}

// setsLockTimeout reports whether a statement sets lock_timeout to something
// other than zero.
//
// SET, SET LOCAL and SET SESSION all count, and so do the ALTER ROLE and ALTER
// DATABASE spellings, because the rule asks whether the migration will give up
// rather than where somebody chose to say so.
func setsLockTimeout(upper string) bool {
	if !strings.Contains(upper, "LOCK_TIMEOUT") {
		return false
	}
	if !strings.HasPrefix(upper, "SET ") &&
		!strings.HasPrefix(upper, "ALTER ROLE") && !strings.HasPrefix(upper, "ALTER DATABASE") {
		return false
	}
	rest := strings.TrimSpace(upper[strings.Index(upper, "LOCK_TIMEOUT")+len("LOCK_TIMEOUT"):])
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "="))
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "TO "))
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return false
	}
	value := strings.Trim(fields[0], `'";,`)
	return value != "" && value != "0" && value != "DEFAULT"
}

// lockingDDLPerMigration is the first blocking statement in each migration
// file, which is what the backfill rule needs: every tool in the list runs one
// migration file in one transaction, so a lock taken anywhere in the file is
// held until the file commits.
func lockingDDLPerMigration(stmts []Statement, schema Schema) map[string]blockingDDL {
	out := map[string]blockingDDL{}
	for _, st := range stmts {
		if _, seen := out[st.Migration]; seen {
			continue
		}
		if ddl, ok := classify(st.SQL, fold(st.SQL), schema); ok {
			out[st.Migration] = ddl
		}
	}
	return out
}

// lintBackfill objects to changing rows in the same file as the schema.
//
// The rule is about the transaction boundary rather than about how many rows
// the statement touches. The lock the DDL took is held until the transaction
// commits, so a five minute UPDATE turns a four millisecond ALTER into a five
// minute ACCESS EXCLUSIVE lock, and the row count on the finding is what tells
// a reader which of those two this is.
func lintBackfill(
	st Statement, upper string, schema Schema, locking map[string]blockingDDL,
) []LintFinding {
	var table string
	switch {
	case strings.HasPrefix(upper, "UPDATE "):
		table = tableAfter(st.SQL, "UPDATE")
	case strings.HasPrefix(upper, "DELETE FROM"):
		table = tableAfter(st.SQL, "DELETE FROM")
	default:
		return nil
	}
	ddl, ok := locking[st.Migration]
	if !ok {
		return nil
	}
	return []LintFinding{{
		Rule: RuleBackfillWithDDL, Migration: st.Migration, Statement: st.SQL,
		Table: table, Rows: schema.Rows[table],
		Detail: fmt.Sprintf(
			"This migration file also runs %s, and every tool here applies one file in one "+
				"transaction. The %s lock that statement takes on %s is held until the file "+
				"commits, which means it is held for the length of this row change rather "+
				"than for the length of the schema change. %s holds about %d rows.",
			ddl.SQL, ddl.Mode, ddl.Table, table, schema.Rows[table]),
		Fix: "Put the row change in a migration of its own, after the one that changes the " +
			"schema, and run it in batches with a commit between them. Nothing then holds a " +
			"lock, or a snapshot, across the whole table at once.",
	}}
}

// lintMaintenance covers the statements that are dangerous on their own rather
// than as part of an ALTER TABLE. None of them is subtle; their absence from a
// lint is what is conspicuous, because anybody who has read strong_migrations
// will look for exactly these.
func lintMaintenance(st Statement, upper string, schema Schema) []LintFinding {
	find := func(rule Rule, table, detail, fix string) []LintFinding {
		return []LintFinding{{
			Rule: rule, Migration: st.Migration, Statement: st.SQL,
			Table: table, Rows: schema.Rows[table], Detail: detail, Fix: fix,
		}}
	}
	rowsOf := func(table string) string {
		if n := schema.Rows[table]; n > 0 {
			return fmt.Sprintf(" %s holds about %d rows.", table, n)
		}
		return ""
	}
	// The table is named where one is known. DROP INDEX and REINDEX name an
	// index, and the table behind it is only known when the branch's catalogue
	// had that index in it.
	named := func(table string) string {
		if table == "" {
			return "the table"
		}
		return table
	}

	switch {
	case strings.HasPrefix(upper, "DROP INDEX"):
		if strings.Contains(upper, "CONCURRENTLY") {
			return nil
		}
		table := tableOfIndex(schema, tableAfter(st.SQL, "DROP INDEX"))
		on := "the table"
		if table != "" {
			on = table + ", the table"
		}
		return find(RuleDropIndexNotConcurrent, table,
			"DROP INDEX takes its ACCESS EXCLUSIVE lock on "+on+" the index is "+
				"built on, not on the index alone, so every read and every write there waits. "+
				"The drop itself is a catalogue change and takes no time; the wait for the "+
				"lock is the whole cost, and nothing bounds it."+rowsOf(table),
			"DROP INDEX CONCURRENTLY, which takes a SHARE UPDATE EXCLUSIVE lock that reads "+
				"and writes pass through. Like the CONCURRENTLY build it cannot run inside a "+
				"transaction, so the migration has to be split or the tool told not to wrap it.")

	case strings.HasPrefix(upper, "REINDEX"):
		if strings.Contains(upper, "CONCURRENTLY") {
			return nil
		}
		kind, name := reindexTarget(upper)
		table := name
		if kind == "INDEX" {
			table = tableOfIndex(schema, name)
		}
		return find(RuleReindexNotConcurrent, table,
			"REINDEX takes an ACCESS EXCLUSIVE lock on the index and a SHARE lock on "+
				named(table)+", so every INSERT, UPDATE and DELETE waits for the whole "+
				"rebuild, which on a large index is minutes rather than moments."+rowsOf(table),
			"REINDEX CONCURRENTLY, from Postgres 12. It builds a replacement alongside the "+
				"old index and swaps it in, taking the strong lock only for the swap. A run "+
				"that fails partway leaves an invalid index named with a _ccnew suffix, which "+
				"has to be dropped before the retry.")

	case strings.HasPrefix(upper, "VACUUM"):
		table, full := vacuumTarget(upper)
		if !full {
			return nil
		}
		return find(RuleVacuumFull, table,
			"VACUUM FULL does not reclaim space in place. It copies "+named(table)+" into a "+
				"new file and rebuilds every index on it, under an ACCESS EXCLUSIVE lock held "+
				"for the whole copy, so nothing can read the table either. It also needs as "+
				"much free disk as the table and its indexes already occupy."+rowsOf(table),
			"Plain VACUUM makes the dead space reusable without a rewrite and without "+
				"blocking readers or writers, which is what almost every case actually wants. "+
				"Where the file itself has to shrink, pg_repack performs the same rewrite and "+
				"holds the strong lock only at the start and at the end.")

	case strings.HasPrefix(upper, "CLUSTER"):
		table := tableAfter(st.SQL, "CLUSTER")
		return find(RuleCluster, table,
			"CLUSTER rewrites "+named(table)+" in index order under an ACCESS EXCLUSIVE lock "+
				"held for the whole rewrite, so nothing can read or write it meanwhile. The "+
				"ordering is also not maintained afterwards: new rows go wherever there is "+
				"room, so the benefit decays and somebody schedules the outage again."+
				rowsOf(table),
			"pg_repack --order-by does the same reordering while holding the strong lock only "+
				"at the start and at the end. Where the physical order matters permanently, an "+
				"index that covers the query is usually the cheaper answer than one that has "+
				"to be re-established on a schedule.")

	case strings.HasPrefix(upper, "DROP TABLE"):
		table := tableAfter(st.SQL, "DROP TABLE")
		return find(RuleDropTable, table,
			"Dropping "+named(table)+" takes an ACCESS EXCLUSIVE lock and the rows are gone "+
				"when the transaction commits. A rolling deploy also means instances running "+
				"the old code are still reading this table between the migration and the last "+
				"one shutting down, and they will fail rather than degrade."+rowsOf(table),
			"Stop the application reading it and deploy that first. Rename the table out of "+
				"the way in the next migration, so a rollback is a rename back, and drop it "+
				"only once nothing has referred to it for a release.")

	case strings.HasPrefix(upper, "TRUNCATE"):
		table := tableAfter(st.SQL, "TRUNCATE")
		return find(RuleTruncate, table,
			"TRUNCATE takes an ACCESS EXCLUSIVE lock on "+named(table)+" and removes every "+
				"row at once. Unlike DELETE it leaves nothing to recover from: the only undo "+
				"is rolling back the transaction it ran in."+rowsOf(table),
			"Decide explicitly whether these rows are meant to be gone in production, because "+
				"a migration is applied there too, and a TRUNCATE written to reset a "+
				"development database deletes production's rows the first time it reaches it. "+
				"Where the table is genuinely being reloaded, truncate and reload in the same "+
				"transaction so a failure leaves the old rows in place.")
	}
	return nil
}

// tableOfIndex finds the table an index is built on, so a finding about
// dropping or rebuilding an index can name the table whose traffic waits and
// how many rows it holds. IndexesUsing is keyed the other way round because
// the rename rule asks the opposite question of it.
func tableOfIndex(schema Schema, index string) string {
	for key, names := range schema.IndexesUsing {
		for _, name := range names {
			if name == index {
				table, _, _ := strings.Cut(key, ".")
				return table
			}
		}
	}
	return ""
}

// reindexTarget returns what kind of object a REINDEX names and its name.
func reindexTarget(upper string) (kind, name string) {
	fields := strings.Fields(upper)
	for i, f := range fields {
		switch f {
		case "INDEX", "TABLE", "SCHEMA", "DATABASE", "SYSTEM":
			for _, word := range fields[i+1:] {
				if word == "CONCURRENTLY" {
					continue
				}
				return f, bareTable(unquote(word))
			}
			return f, ""
		}
	}
	return "", ""
}

// vacuumTarget splits a VACUUM into whether it is a FULL one and the table it
// names. Both spellings of the options have to be read, the bare keywords and
// the parenthesised list, because FULL is the word that turns a maintenance
// command into a table rewrite under a lock nothing can read through.
func vacuumTarget(upper string) (table string, full bool) {
	rest := strings.TrimSpace(strings.TrimPrefix(upper, "VACUUM"))
	if strings.HasPrefix(rest, "(") {
		end := strings.Index(rest, ")")
		if end < 0 {
			return "", false
		}
		for _, opt := range strings.Fields(rest[1:end]) {
			if strings.Trim(opt, ",") == "FULL" {
				full = true
			}
		}
		rest = rest[end+1:]
	}
	for _, word := range strings.Fields(rest) {
		switch word {
		case "FULL":
			full = true
		case "FREEZE", "VERBOSE", "ANALYZE":
		default:
			return bareTable(unquote(word)), full
		}
	}
	return "", full
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
