package masking_test

// NOTHING IS WRITTEN FOR A PRESERVED COLUMN, ON A REAL DATABASE.
//
// A comparison of values before and after cannot see the defect, because the old
// statement set a preserved column to the value it already held. Two instruments
// that can see it are used instead. An UPDATE OF trigger fires when a listed
// column is in a statement's SET list whether or not its value changes, and a
// row's xmin changes whenever a new version of the row is written, even when
// every byte of it is the same.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

// preserveSchema is one table with a column to rewrite beside two preserved ones,
// the primary key among them, and one table whose every column is preserved.
const preserveSchema = `
CREATE TABLE preserve_people (
  id    integer PRIMARY KEY,
  email text NOT NULL,
  theme text NOT NULL
);
CREATE TABLE preserve_currencies (
  code  text PRIMARY KEY,
  label text NOT NULL
);
CREATE TABLE preserve_writes (
  tbl  text NOT NULL,
  cols text NOT NULL
);
CREATE FUNCTION note_preserved_write() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO preserve_writes VALUES (TG_TABLE_NAME, TG_ARGV[0]);
  RETURN NULL;
END $$;
CREATE TRIGGER people_preserved AFTER UPDATE OF id, theme ON preserve_people
  FOR EACH ROW EXECUTE FUNCTION note_preserved_write('id or theme');
CREATE TRIGGER currencies_written AFTER UPDATE ON preserve_currencies
  FOR EACH ROW EXECUTE FUNCTION note_preserved_write('any column');
INSERT INTO preserve_people (id, email, theme)
  SELECT i, 'person' || i || '@realcorp.com', CASE WHEN i % 2 = 0 THEN 'dark' ELSE 'light' END
  FROM generate_series(1, 25) i;
INSERT INTO preserve_currencies (code, label)
  VALUES ('EUR', 'Euro'), ('GBP', 'Pound sterling'), ('USD', 'US dollar');
`

func queryText(t *testing.T, conn *pgx.Conn, sql string) string {
	t.Helper()
	var out string
	require.NoError(t, conn.QueryRow(context.Background(), sql).Scan(&out))
	return out
}

func TestApply_NothingIsWrittenForAPreservedColumn(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	_, err := conn.Exec(ctx, preserveSchema)
	require.NoError(t, err)

	const people = `SELECT string_agg(id || ':' || theme, ',' ORDER BY id) FROM preserve_people`
	const currencies = `SELECT string_agg(xmin::text || ':' || code || ':' || label, ',' ORDER BY code)
		FROM preserve_currencies`
	peopleBefore := queryText(t, conn, people)
	currenciesBefore := queryText(t, conn, currencies)

	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)
	rules, err := masking.NewRuleSet([]masking.Rule{
		{Table: "preserve_people", Column: "email", Transform: "email", Why: "a person's address"},
		{Table: "preserve_people", Column: "id", Transform: "preserve", Why: "a surrogate key"},
		{Table: "preserve_people", Column: "theme", Transform: "preserve", Why: "light or dark"},
		{Table: "preserve_currencies", Column: "code", Transform: "preserve", Why: "an ISO 4217 code"},
		{Table: "preserve_currencies", Column: "label", Transform: "preserve", Why: "the currency's name"},
	})
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, rules.Assign(tables), "test")
	require.True(t, plan.Runnable(), masking.DescribeProblems(plan.Problems))

	// This file's tables only, so the run measures them and not the package's
	// shared schema. The filter keeps anything this plan chose to write here.
	var mine []masking.TablePlan
	for _, tp := range plan.Tables {
		if strings.HasPrefix(tp.Table.Name, "preserve_") {
			mine = append(mine, tp)
		}
	}
	plan.Tables = mine

	exec, err := masking.NewExecutor(masking.ExecutorOptions{Key: testKey(t), Clock: clock.New()})
	require.NoError(t, err)
	res, err := exec.Apply(ctx, conn, plan)
	require.NoError(t, err)

	require.Zero(t, queryOne(t, conn, `SELECT count(*) FROM preserve_writes`),
		"an UPDATE named a preserved column, or touched a table whose every column is preserved")
	require.Equal(t, 1, res.Tables, "a table whose every column is preserved was counted as rewritten")
	require.Equal(t, currenciesBefore, queryText(t, conn, currencies),
		"a row of the table whose every column is preserved has a new version, so it was written")
	require.Equal(t, peopleBefore, queryText(t, conn, people))
	require.Zero(t, queryOne(t, conn, `SELECT count(*) FROM preserve_people WHERE email LIKE '%@realcorp%'`),
		"the column beside the preserved ones was not rewritten")

	// Covered. Verification counts a column as ruled unless masking copied it
	// unchanged for want of a rule, and a preserved column had a rule.
	report, err := verify.Scan(ctx, conn, verify.Options{Unruled: plan.CopiedUnchangedNames()})
	require.NoError(t, err)
	for _, preserved := range []string{
		"public.preserve_people.id", "public.preserve_people.theme",
		"public.preserve_currencies.code", "public.preserve_currencies.label",
	} {
		require.NotContains(t, report.Unruled, preserved,
			"a preserved column is reported as copied unchanged with no rule")
	}
}
