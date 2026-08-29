package insights_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
)

func sqlOf(stmts []insights.Statement) []string {
	out := make([]string, 0, len(stmts))
	for _, s := range stmts {
		out = append(out, s.SQL)
	}
	return out
}

func TestSplit_TwoStatements(t *testing.T) {
	t.Parallel()
	require.Equal(t,
		[]string{"CREATE TABLE a (id int)", "CREATE TABLE b (id int)"},
		sqlOf(insights.Split("m", "CREATE TABLE a (id int);\nCREATE TABLE b (id int);\n")))
}

func TestSplit_KeepsAFunctionBodyWhole(t *testing.T) {
	t.Parallel()
	// The case this exists for. A dollar quoted body is full of semicolons
	// and is one statement, and splitting on every semicolon turns one
	// CREATE FUNCTION into a handful of syntax errors. A rehearsal that fails
	// on a migration production applies cleanly is worse than no rehearsal.
	body := `CREATE FUNCTION f() RETURNS int LANGUAGE plpgsql AS $$
BEGIN
  PERFORM 1;
  RETURN 2;
END;
$$;
CREATE TABLE after_it (id int);`
	stmts := insights.Split("m", body)
	require.Len(t, stmts, 2)
	require.Contains(t, stmts[0].SQL, "RETURN 2;")
	require.Contains(t, stmts[1].SQL, "after_it")
}

func TestSplit_HandlesATaggedDollarQuote(t *testing.T) {
	t.Parallel()
	stmts := insights.Split("m", "DO $body$ BEGIN PERFORM 1; END $body$;\nSELECT 1;")
	require.Len(t, stmts, 2)
}

func TestSplit_DoesNotTreatAParameterAsADollarQuote(t *testing.T) {
	t.Parallel()
	// $1 is a parameter and $$ is a quote, and reading the first as the second
	// swallows the rest of the file.
	stmts := insights.Split("m", "UPDATE t SET a = $1;\nSELECT 1;")
	require.Len(t, stmts, 2)
}

func TestSplit_IgnoresASemicolonInAString(t *testing.T) {
	t.Parallel()
	stmts := insights.Split("m", "INSERT INTO t VALUES ('a;b');\nSELECT 1;")
	require.Len(t, stmts, 2)
	require.Contains(t, stmts[0].SQL, "'a;b'")
}

func TestSplit_HandlesADoubledQuoteInsideAString(t *testing.T) {
	t.Parallel()
	stmts := insights.Split("m", "INSERT INTO t VALUES ('it''s; fine');\nSELECT 1;")
	require.Len(t, stmts, 2)
}

func TestSplit_IgnoresASemicolonInAQuotedIdentifier(t *testing.T) {
	t.Parallel()
	stmts := insights.Split("m", `CREATE TABLE "odd;name" (id int);
SELECT 1;`)
	require.Len(t, stmts, 2)
	require.Contains(t, stmts[0].SQL, `"odd;name"`)
}

func TestSplit_IgnoresComments(t *testing.T) {
	t.Parallel()
	stmts := insights.Split("m", "-- drop it; really\nSELECT 1;\n/* and; this */\nSELECT 2;")
	require.Len(t, stmts, 2)
	require.NotContains(t, stmts[0].SQL, "really")
}

func TestSplit_DropsEmptyStatements(t *testing.T) {
	t.Parallel()
	// A trailing semicolon and a file of nothing but comments both produce an
	// empty statement, and sending one to the server is an error for no
	// reason.
	require.Empty(t, insights.Split("m", "-- nothing here\n\n;\n"))
	require.Len(t, insights.Split("m", "SELECT 1;;\n"), 1)
}

func TestSplit_NumbersStatementsFromOneWithinTheirFile(t *testing.T) {
	t.Parallel()
	stmts := insights.Split("002_add.sql", "SELECT 1; SELECT 2;")
	require.Equal(t, 1, stmts[0].Index)
	require.Equal(t, 2, stmts[1].Index)
	require.Equal(t, "002_add.sql", stmts[1].Migration)
}
