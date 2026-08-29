package insights_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
)

// bigSchema is a database where every rule has something real to fire on: a
// table with production's row count, a column with a type the change is not
// binary coercible from, and a view that reads one of the columns.
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
	}
}

func lintOne(t *testing.T, sql string) []insights.LintFinding {
	t.Helper()
	return insights.Lint(insights.Split("001_change.sql", sql), bigSchema(), 1_000_000)
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
		insights.RuleNotNullNoDefault:   "ALTER TABLE orders ADD COLUMN region text NOT NULL;",
		insights.RuleAlterColumnType:    "ALTER TABLE orders ALTER COLUMN total TYPE bigint;",
		insights.RuleIndexNotConcurrent: "CREATE INDEX i ON orders (total);",
		insights.RuleForeignKeyNotValid: "ALTER TABLE orders ADD CONSTRAINT f FOREIGN KEY (u) REFERENCES users (id);",
		insights.RuleRenameColumnInUse:  "ALTER TABLE orders RENAME COLUMN email TO contact;",
		insights.RuleDropColumnInView:   "ALTER TABLE orders DROP COLUMN email;",
	}
	for _, rule := range insights.AllRules() {
		sql, ok := fixtures[rule]
		require.Truef(t, ok, "%s has no fixture, so nothing proves it can fire", rule)
		f := lintOne(t, sql)
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
