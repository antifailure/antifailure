package insights_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
)

// bigSchema is a database where every rule has something real to fire on: a
// table with production's row count, a column with a type the change is not
// binary coercible from, and a view that reads one of the columns.
//
// lock_timeout is set on the server here, so that the rule about a missing one
// does not fire alongside every other rule's fixture and turn each of those
// assertions into a test of two rules at once. noLockTimeout clears it, and
// that is what the lock_timeout rule's own fixtures use.
func bigSchema() insights.Schema {
	return insights.Schema{
		Rows: map[string]int64{"orders": 40_000_000, "flags": 12},
		Columns: map[string]string{
			"orders.id":    "int4",
			"orders.email": "varchar(64)",
			"orders.note":  "text",
			"orders.total": "int4",
			"flags.name":   "text",
		},
		ViewsUsing:   map[string][]string{"orders.email": {"order_emails"}},
		IndexesUsing: map[string][]string{"orders.email": {"orders_email_idx"}},
		LockTimeout:  "5s",
	}
}

// noLockTimeout is the same database with nothing setting lock_timeout, which
// is what a stock Postgres hands a migration.
func noLockTimeout() insights.Schema {
	s := bigSchema()
	s.LockTimeout = "0"
	return s
}

func lintOne(t *testing.T, sql string) []insights.LintFinding {
	t.Helper()
	return insights.Lint(insights.Split("001_change.sql", sql), bigSchema(), 1_000_000)
}

// lintUntimed lints against a database where nothing sets lock_timeout.
func lintUntimed(t *testing.T, sql string) []insights.LintFinding {
	t.Helper()
	return insights.Lint(insights.Split("001_change.sql", sql), noLockTimeout(), 1_000_000)
}

func rulesIn(f []insights.LintFinding) []insights.Rule {
	out := make([]insights.Rule, 0, len(f))
	for _, x := range f {
		out = append(out, x.Rule)
	}
	return out
}

// Every rule has a positive and a negative fixture. The negative one is the
// half that matters: a rule that fires on the safe form as well as the unsafe
// one is a rule somebody turns off, and then the unsafe form ships too.

func TestLint_NotNullWithoutADefault(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "ALTER TABLE orders ADD COLUMN region text NOT NULL;")
	require.Equal(t, []insights.Rule{insights.RuleNotNullNoDefault}, rulesIn(f))
	require.Equal(t, "orders", f[0].Table)
	require.EqualValues(t, 40_000_000, f[0].Rows)
	require.Contains(t, f[0].Fix, "backfill")
}

func TestLint_NotNullWithADefaultIsFine(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintOne(t,
		"ALTER TABLE orders ADD COLUMN region text NOT NULL DEFAULT 'eu';"))
}

func TestLint_ColumnTypeChangeThatRewrites(t *testing.T) {
	t.Parallel()
	// int to bigint is the classic one: it looks like a widening and it
	// rewrites every row under a lock nothing can read through.
	f := lintOne(t, "ALTER TABLE orders ALTER COLUMN total TYPE bigint;")
	require.Equal(t, []insights.Rule{insights.RuleAlterColumnType}, rulesIn(f))
	require.Contains(t, f[0].Detail, "rewrites")
}

func TestLint_ColumnTypeChangeThatDoesNotRewrite(t *testing.T) {
	t.Parallel()
	// varchar to text shares an on disk representation, so Postgres skips the
	// rewrite, and warning about it would be a false alarm on the exact change
	// somebody made to avoid one.
	require.Empty(t, lintOne(t, "ALTER TABLE orders ALTER COLUMN email TYPE text;"))
	require.Empty(t, lintOne(t, "ALTER TABLE orders ALTER COLUMN email TYPE varchar(128);"))
}

func TestLint_ColumnTypeChangeThatNarrows(t *testing.T) {
	t.Parallel()
	// Shrinking a varchar rewrites AND can fail halfway through on a row that
	// does not fit, which is worse than a plain rewrite.
	f := lintOne(t, "ALTER TABLE orders ALTER COLUMN email TYPE varchar(16);")
	require.Equal(t, []insights.Rule{insights.RuleAlterColumnType}, rulesIn(f))
}

func TestLint_IndexWithoutConcurrently(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "CREATE INDEX orders_total_idx ON orders (total);")
	require.Equal(t, []insights.Rule{insights.RuleIndexNotConcurrent}, rulesIn(f))
	require.Equal(t, "orders", f[0].Table)
	require.Contains(t, f[0].Fix, "CONCURRENTLY")
}

func TestLint_IndexWithConcurrentlyIsFine(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintOne(t,
		"CREATE INDEX CONCURRENTLY orders_total_idx ON orders (total);"))
	require.Empty(t, lintOne(t,
		"CREATE UNIQUE INDEX CONCURRENTLY orders_email_key ON orders (email);"))
}

func TestLint_ForeignKeyWithoutNotValid(t *testing.T) {
	t.Parallel()
	f := lintOne(t,
		"ALTER TABLE orders ADD CONSTRAINT orders_user_fk FOREIGN KEY (user_id) REFERENCES users (id);")
	require.Equal(t, []insights.Rule{insights.RuleForeignKeyNotValid}, rulesIn(f))
	require.Contains(t, f[0].Detail, "both tables")
}

func TestLint_ForeignKeyWithNotValidIsFine(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintOne(t,
		"ALTER TABLE orders ADD CONSTRAINT orders_user_fk FOREIGN KEY (user_id) "+
			"REFERENCES users (id) NOT VALID;"))
}

func TestLint_RenamingAColumnInUse(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "ALTER TABLE orders RENAME COLUMN email TO contact_email;")
	require.Equal(t, []insights.Rule{insights.RuleRenameColumnInUse}, rulesIn(f))
	require.Contains(t, f[0].Detail, "order_emails")
	require.Contains(t, f[0].Detail, "orders_email_idx")
}

func TestLint_RenamingAColumnStillWarnsAboutTheRollingDeploy(t *testing.T) {
	t.Parallel()
	// Nothing in the database reads flags.name, and the rename is still not
	// backward compatible with the instances still running the old code. That
	// is the hazard the database cannot see and the one that actually bites.
	f := insights.Lint(
		insights.Split("m", "ALTER TABLE flags RENAME COLUMN name TO label;"),
		bigSchema(), 1_000_000)
	require.Equal(t, []insights.Rule{insights.RuleRenameColumnInUse}, rulesIn(f))
	require.Contains(t, f[0].Detail, "rolling deploy")
	require.NotContains(t, f[0].Detail, "It is read by")
}

func TestLint_DroppingAColumnAViewSelects(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "ALTER TABLE orders DROP COLUMN email;")
	require.Equal(t, []insights.Rule{insights.RuleDropColumnInView}, rulesIn(f))
	require.Contains(t, f[0].Detail, "order_emails")
	require.Contains(t, f[0].Detail, "refuses")
}

func TestLint_DroppingAColumnWithCascadeSaysTheViewGoesToo(t *testing.T) {
	t.Parallel()
	// CASCADE turns a refusal into a silent success that takes the view with
	// it, which is the more dangerous of the two and reads as the safer one.
	f := lintOne(t, "ALTER TABLE orders DROP COLUMN email CASCADE;")
	require.Equal(t, []insights.Rule{insights.RuleDropColumnInView}, rulesIn(f))
	require.Contains(t, f[0].Detail, "drops the view with it")
}

func TestLint_DroppingAColumnNothingReadsIsFine(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintOne(t, "ALTER TABLE orders DROP COLUMN note;"))
}

// The lock_timeout rule is about the migration rather than about a statement,
// so it fires once and it fires alongside whatever else the statements are
// guilty of. Its fixtures use a database where nothing sets the timeout.

func TestLint_NoLockTimeoutOnAMigrationThatTakesALock(t *testing.T) {
	t.Parallel()
	f := lintUntimed(t, "ALTER TABLE orders ADD COLUMN region text;")
	require.Equal(t, []insights.Rule{insights.RuleNoLockTimeout}, rulesIn(f))
	require.Equal(t, "orders", f[0].Table)
	require.EqualValues(t, 40_000_000, f[0].Rows)
	require.Contains(t, f[0].Detail, "ACCESS EXCLUSIVE",
		"the finding has to name the lock, or it is an opinion")
	require.Contains(t, f[0].Detail, "queues behind the request")
	require.Contains(t, f[0].Fix, "lock_timeout = '3s'")
}

func TestLint_LockTimeoutSetInTheMigrationIsFine(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintUntimed(t,
		"SET lock_timeout = '3s';\nALTER TABLE orders ADD COLUMN region text;"))
	require.Empty(t, lintUntimed(t,
		"SET LOCAL lock_timeout TO '3s';\nALTER TABLE orders ADD COLUMN region text;"))
	require.Empty(t, lintUntimed(t,
		"ALTER ROLE migrator SET lock_timeout = '3s';\n"+
			"ALTER TABLE orders ADD COLUMN region text;"))
}

func TestLint_LockTimeoutSetToZeroIsNotSet(t *testing.T) {
	t.Parallel()
	// Writing the line and turning it off is the state that reads safest and
	// is not, so it has to fire.
	f := lintUntimed(t,
		"SET lock_timeout = '0';\nALTER TABLE orders ADD COLUMN region text;")
	require.Equal(t, []insights.Rule{insights.RuleNoLockTimeout}, rulesIn(f))
}

func TestLint_LockTimeoutSetOnTheServerIsFine(t *testing.T) {
	t.Parallel()
	// Most projects that set it set it on the role or on the database rather
	// than in the file, and telling those projects they have no lock_timeout
	// would be false.
	require.Empty(t, lintOne(t, "ALTER TABLE orders ADD COLUMN region text;"))
}

func TestLint_NoLockTimeoutSaysNothingWhenNothingTakesALock(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintUntimed(t, "INSERT INTO flags (name) VALUES ('beta');"))
	require.Empty(t, lintUntimed(t, "CREATE TABLE regions (id int primary key, name text);"))
	// A migration written the way the other rules ask waits for nothing, so
	// there is nothing for a timeout to save it from.
	require.Empty(t, lintUntimed(t,
		"CREATE INDEX CONCURRENTLY orders_total_idx ON orders (total);"))
	require.Empty(t, lintUntimed(t, "DROP INDEX CONCURRENTLY orders_email_idx;"))
}

func TestLint_SetNotNullOnAColumnThatAlreadyExists(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "ALTER TABLE orders ALTER COLUMN note SET NOT NULL;")
	require.Equal(t, []insights.Rule{insights.RuleSetNotNull}, rulesIn(f))
	require.Contains(t, f[0].Detail, "ACCESS EXCLUSIVE")
	require.Contains(t, f[0].Detail, "40000000")
	require.Contains(t, f[0].Fix, "CHECK (note IS NOT NULL) NOT VALID",
		"the fix names the column, or somebody has to work out the constraint themselves")
	require.Contains(t, f[0].Fix, "Postgres 12")
}

func TestLint_DroppingNotNullIsFine(t *testing.T) {
	t.Parallel()
	// Removing the constraint is a catalogue change with no scan behind it.
	require.Empty(t, lintOne(t, "ALTER TABLE orders ALTER COLUMN note DROP NOT NULL;"))
}

func TestLint_CheckConstraintWithoutNotValid(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "ALTER TABLE orders ADD CONSTRAINT orders_total_positive CHECK (total > 0);")
	require.Equal(t, []insights.Rule{insights.RuleCheckNotValid}, rulesIn(f))
	require.Contains(t, f[0].Detail, "ACCESS EXCLUSIVE")
	require.Contains(t, f[0].Fix, "VALIDATE CONSTRAINT")
}

func TestLint_CheckConstraintWithNotValidIsFine(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintOne(t,
		"ALTER TABLE orders ADD CONSTRAINT orders_total_positive CHECK (total > 0) NOT VALID;"))
	require.Empty(t, lintOne(t, "ALTER TABLE orders VALIDATE CONSTRAINT orders_total_positive;"))
}

func TestLint_UniqueConstraintBuildsItsIndexInPlace(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "ALTER TABLE orders ADD CONSTRAINT orders_email_key UNIQUE (email);")
	require.Equal(t, []insights.Rule{insights.RuleUniqueConstraint}, rulesIn(f))
	require.Contains(t, f[0].Detail, "ACCESS EXCLUSIVE")
	require.Contains(t, f[0].Fix, "CREATE UNIQUE INDEX CONCURRENTLY")
	require.Contains(t, f[0].Fix, "USING INDEX")
}

func TestLint_UniqueConstraintOnAnExistingIndexIsFine(t *testing.T) {
	t.Parallel()
	// This is the second half of the fix the rule recommends. Firing on it
	// would warn somebody about the safe form of the change they made to
	// avoid the unsafe one, which is how a check loses its reader.
	require.Empty(t, lintOne(t,
		"ALTER TABLE orders ADD CONSTRAINT orders_email_key UNIQUE USING INDEX orders_email_key;"))
}

func TestLint_BackfillInTheSameTransactionAsTheSchemaChange(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "ALTER TABLE orders ADD COLUMN currency text;\n"+
		"UPDATE orders SET currency = 'usd';")
	require.Equal(t, []insights.Rule{insights.RuleBackfillWithDDL}, rulesIn(f))
	require.Equal(t, "orders", f[0].Table)
	require.Contains(t, f[0].Detail, "one file in one transaction")
	require.Contains(t, f[0].Detail, "ACCESS EXCLUSIVE")
	require.Contains(t, f[0].Fix, "batches")
}

func TestLint_ABackfillInItsOwnMigrationIsFine(t *testing.T) {
	t.Parallel()
	// The whole fix is that the update happens in a file of its own, so the
	// rule has to be quiet about exactly that.
	require.Empty(t, lintOne(t, "UPDATE orders SET currency = 'usd';"))

	split := append(
		insights.Split("001_schema.sql", "ALTER TABLE orders ADD COLUMN currency text;"),
		insights.Split("002_backfill.sql", "UPDATE orders SET currency = 'usd';")...)
	require.Empty(t, insights.Lint(split, bigSchema(), 1_000_000))
}

func TestLint_DroppingAnIndexWithoutConcurrently(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "DROP INDEX orders_email_idx;")
	require.Equal(t, []insights.Rule{insights.RuleDropIndexNotConcurrent}, rulesIn(f))
	require.Equal(t, "orders", f[0].Table,
		"the lock is on the table the index is built on, so that is the table to name")
	require.EqualValues(t, 40_000_000, f[0].Rows)
	require.Contains(t, f[0].Fix, "DROP INDEX CONCURRENTLY")
}

func TestLint_DroppingAnIndexConcurrentlyIsFine(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintOne(t, "DROP INDEX CONCURRENTLY orders_email_idx;"))
}

func TestLint_ReindexWithoutConcurrently(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "REINDEX INDEX orders_email_idx;")
	require.Equal(t, []insights.Rule{insights.RuleReindexNotConcurrent}, rulesIn(f))
	require.Equal(t, "orders", f[0].Table)
	require.Contains(t, f[0].Detail, "SHARE lock")
	require.Contains(t, f[0].Fix, "_ccnew")

	byTable := lintOne(t, "REINDEX TABLE orders;")
	require.Equal(t, []insights.Rule{insights.RuleReindexNotConcurrent}, rulesIn(byTable))
	require.Equal(t, "orders", byTable[0].Table)
}

func TestLint_ReindexConcurrentlyIsFine(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintOne(t, "REINDEX INDEX CONCURRENTLY orders_email_idx;"))
	require.Empty(t, lintOne(t, "REINDEX TABLE CONCURRENTLY orders;"))
}

func TestLint_VacuumFull(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "VACUUM FULL orders;")
	require.Equal(t, []insights.Rule{insights.RuleVacuumFull}, rulesIn(f))
	require.Equal(t, "orders", f[0].Table)
	require.Contains(t, f[0].Detail, "ACCESS EXCLUSIVE")
	require.Contains(t, f[0].Fix, "pg_repack")

	// The parenthesised option list is the spelling a generated migration
	// tends to use, and reading only the bare keyword would miss it.
	parens := lintOne(t, "VACUUM (FULL, ANALYZE) orders;")
	require.Equal(t, []insights.Rule{insights.RuleVacuumFull}, rulesIn(parens))
	require.Equal(t, "orders", parens[0].Table)
}

func TestLint_PlainVacuumIsFine(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintOne(t, "VACUUM ANALYZE orders;"))
	require.Empty(t, lintOne(t, "VACUUM (ANALYZE, VERBOSE) orders;"))
}

func TestLint_Cluster(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "CLUSTER orders USING orders_email_idx;")
	require.Equal(t, []insights.Rule{insights.RuleCluster}, rulesIn(f))
	require.Equal(t, "orders", f[0].Table)
	require.Contains(t, f[0].Detail, "not maintained afterwards")
}

func TestLint_TheWordClusterInAValueIsNotACluster(t *testing.T) {
	t.Parallel()
	// Keywords are matched at the start of the statement, because folding
	// upper cases string literals too and a rule that searched the whole
	// statement would fire on a row of data.
	require.Empty(t, lintOne(t, "INSERT INTO flags (name) VALUES ('cluster');"))
	require.Empty(t, lintOne(t, "INSERT INTO flags (name) VALUES ('truncate');"))
	require.Empty(t, lintOne(t, "CREATE TABLE drop_table_log (id int primary key);"))
}

func TestLint_DropTable(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "DROP TABLE orders;")
	require.Equal(t, []insights.Rule{insights.RuleDropTable}, rulesIn(f))
	require.Equal(t, "orders", f[0].Table)
	require.Contains(t, f[0].Detail, "rolling deploy")
	require.Contains(t, f[0].Fix, "rename")
}

func TestLint_Truncate(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "TRUNCATE TABLE orders;")
	require.Equal(t, []insights.Rule{insights.RuleTruncate}, rulesIn(f))
	require.Equal(t, "orders", f[0].Table,
		"TRUNCATE TABLE and TRUNCATE name the same thing and both have to resolve")
	require.Contains(t, f[0].Detail, "ACCESS EXCLUSIVE")

	bare := lintOne(t, "TRUNCATE orders;")
	require.Equal(t, []insights.Rule{insights.RuleTruncate}, rulesIn(bare))
	require.Equal(t, "orders", bare[0].Table)
}

func TestLint_AnUnrelatedStatementIsNotAFinding(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintOne(t, "INSERT INTO flags (name) VALUES ('beta');"))
	require.Empty(t, lintOne(t, "CREATE TABLE regions (id int primary key, name text);"))
}

func TestLint_EveryRuleCarriesARationaleAndAFix(t *testing.T) {
	t.Parallel()
	// A lint that says "unsafe" and stops is a lint people disable, and the
	// documentation promises a rationale and a fix for every rule.
	fixtures := map[insights.Rule]string{
		insights.RuleNoLockTimeout:          "ALTER TABLE orders ADD COLUMN region text;",
		insights.RuleNotNullNoDefault:       "ALTER TABLE orders ADD COLUMN region text NOT NULL;",
		insights.RuleSetNotNull:             "ALTER TABLE orders ALTER COLUMN note SET NOT NULL;",
		insights.RuleAlterColumnType:        "ALTER TABLE orders ALTER COLUMN total TYPE bigint;",
		insights.RuleIndexNotConcurrent:     "CREATE INDEX i ON orders (total);",
		insights.RuleDropIndexNotConcurrent: "DROP INDEX orders_email_idx;",
		insights.RuleReindexNotConcurrent:   "REINDEX INDEX orders_email_idx;",
		insights.RuleForeignKeyNotValid:     "ALTER TABLE orders ADD CONSTRAINT f FOREIGN KEY (u) REFERENCES users (id);",
		insights.RuleCheckNotValid:          "ALTER TABLE orders ADD CONSTRAINT c CHECK (total > 0);",
		insights.RuleUniqueConstraint:       "ALTER TABLE orders ADD CONSTRAINT u UNIQUE (email);",
		insights.RuleBackfillWithDDL: "ALTER TABLE orders ADD COLUMN currency text;\n" +
			"UPDATE orders SET currency = 'usd';",
		insights.RuleRenameColumnInUse: "ALTER TABLE orders RENAME COLUMN email TO contact;",
		insights.RuleDropColumnInView:  "ALTER TABLE orders DROP COLUMN email;",
		insights.RuleVacuumFull:        "VACUUM FULL orders;",
		insights.RuleCluster:           "CLUSTER orders USING orders_email_idx;",
		insights.RuleDropTable:         "DROP TABLE orders;",
		insights.RuleTruncate:          "TRUNCATE orders;",
	}
	for _, rule := range insights.AllRules() {
		sql, ok := fixtures[rule]
		require.Truef(t, ok, "%s has no fixture, so nothing proves it can fire", rule)
		// Every fixture but one is linted against a database that already sets
		// lock_timeout, so that each of them tests the one rule it is for.
		lint := lintOne
		if rule == insights.RuleNoLockTimeout {
			lint = lintUntimed
		}
		f := lint(t, sql)
		require.Lenf(t, f, 1, "%s did not fire on its own fixture", rule)
		require.Equal(t, rule, f[0].Rule)
		require.NotEmptyf(t, f[0].Detail, "%s says nothing about what will happen", rule)
		require.NotEmptyf(t, f[0].Fix, "%s says nothing about what to write instead", rule)
		require.NotEmptyf(t, rule.Title(), "%s has no title", rule)
	}
}

func TestLint_ReportsTheRowCountThatMakesItAnOutage(t *testing.T) {
	t.Parallel()
	// Every one of these is fine on an empty table. The row count is what
	// turns a note into a decision, so it has to be on the finding.
	f := lintOne(t, "ALTER TABLE orders ALTER COLUMN total TYPE bigint;")
	require.EqualValues(t, 40_000_000, f[0].Rows)
}
