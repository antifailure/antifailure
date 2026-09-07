package masking_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

func clickhouse(t *testing.T) masking.Dialect {
	t.Helper()
	d, err := masking.DialectFor("clickhouse")
	require.NoError(t, err)
	return d
}

// clickhouseTypeCases are the type names a real ClickHouse reports, with the
// Postgres name each has to become.
//
// Every row is a type that appears in an analytics schema. The last four are
// the ones that must NOT be given a meaning: a Map or a Tuple holds whatever
// somebody put in it and no transform here can rewrite one, so it lands in the
// branch that reports a column rather than deciding about it, exactly as
// Postgres does with an array and a user defined type.
var clickhouseTypeCases = []struct{ raw, canonical string }{
	{"String", "text"},
	{"Nullable(String)", "text"},
	{"LowCardinality(String)", "text"},
	{"LowCardinality(Nullable(String))", "text"},
	{"FixedString(16)", "text"},
	{"UUID", "uuid"},
	{"Nullable(UUID)", "uuid"},
	{"Int64", "bigint"},
	{"UInt8", "smallint"},
	{"Int32", "integer"},
	{"Float64", "double precision"},
	{"Decimal(10, 2)", "numeric"},
	{"Bool", "boolean"},
	{"Date", "date"},
	{"DateTime64(6)", "timestamp with time zone"},
	{"DateTime('UTC')", "timestamp with time zone"},
	{"IPv4", "inet"},
	{"IPv6", "inet"},
	{"JSON", "jsonb"},
	{"Array(String)", "ARRAY"},
	{"Map(String, String)", "USER-DEFINED"},
	{"Tuple(String, UInt8)", "USER-DEFINED"},
	{"Enum8('a' = 1, 'b' = 2)", "USER-DEFINED"},
	{"AggregateFunction(sum, UInt64)", "AggregateFunction(sum, UInt64)"},
}

// TestClickHouseTypes_AgreeWithTheScanner is the same check the Postgres type
// lists have already, applied to the second engine.
//
// The masking classifier and the verification scanner each carry their own
// table of what a ClickHouse type means, and they carry it twice on purpose:
// verify must not import masking, because it is the check on masking. This is
// what keeps the two tables the same table. A type one calls text and the other
// calls unreadable is a column one instrument is silent about while the other
// speaks, which is exactly the state that let a bytea holding a private key be
// reported clean.
func TestClickHouseTypes_AgreeWithTheScanner(t *testing.T) {
	t.Parallel()
	d := clickhouse(t)
	for _, c := range clickhouseTypeCases {
		require.Equal(t, c.canonical, d.Canonical(c.raw),
			"masking reads %s as something else", c.raw)
		require.Equal(t, c.canonical, verify.ClickHouse.Canonical(c.raw),
			"the scanner reads %s as something else", c.raw)
	}
}

// TestClickHouse_ReadsAStringAsBytesRatherThanText is the one place the two
// packages deliberately differ, recorded so it reads as a decision.
//
// A ClickHouse String is a byte string. It is where an analytics schema keeps
// its JSON and it is also where a ciphertext would go. The scanner reads one as
// bytes and decodes it where it decodes, so a column of encrypted bytes is
// counted as unread rather than handed to the detectors as mojibake, matching
// nothing, and reported as clean.
func TestClickHouse_ReadsAStringAsBytesRatherThanText(t *testing.T) {
	t.Parallel()
	require.Equal(t, "bytea", verify.ClickHouse.Kind("String"))
	require.Equal(t, "bytea", verify.ClickHouse.Kind("LowCardinality(Nullable(String))"))
	require.Equal(t, "text", verify.ClickHouse.Kind("Array(String)"))
	require.Equal(t, "structural", verify.ClickHouse.Kind("DateTime64(6)"))
	// And Postgres text stays text, because a Postgres text column in a UTF-8
	// database is text by construction.
	require.Equal(t, "text", verify.Postgres.Kind("text"))
	require.Equal(t, "bytea", verify.Postgres.Kind("bytea"))
}

// TestAssign_AClickHouseStringIsClassifiedLikeAPostgresText is the defect this
// lane closes, stated as the two stores agreeing.
//
// Before the dialect existed, a rule saying `type: text` could not match a
// ClickHouse String, so the built in rule for a name column matched nothing,
// so nothing decided what happened to the column, so it was copied. The
// Postgres column beside it was masked. One person, two answers.
func TestAssign_AClickHouseStringIsClassifiedLikeAPostgresText(t *testing.T) {
	t.Parallel()
	rs, err := masking.NewRuleSet(nil)
	require.NoError(t, err)

	pg := rs.Assign([]masking.Table{{
		Engine: "postgres", Schema: "public", Name: "person", PrimaryKey: []string{"id"},
		Columns: []masking.ColumnInfo{
			{Name: "id", Type: "bigint"},
			{Name: "name", Type: "text", Nullable: true},
		},
	}})
	ch := rs.Assign([]masking.Table{{
		Engine: "clickhouse", Schema: "default", Name: "person", PrimaryKey: []string{"id"},
		Columns: []masking.ColumnInfo{
			{Name: "id", Type: "Int64"},
			{Name: "name", Type: "String", Nullable: true},
		},
	}})

	require.Equal(t, "name", assignmentFor(t, pg, "name").Transform)
	require.Equal(t, "name", assignmentFor(t, ch, "name").Transform,
		"the built in rule for a name on a text type did not reach the ClickHouse column, "+
			"so nothing decided about it and it keeps what production had")
	require.Equal(t,
		assignmentFor(t, pg, "name").Link, assignmentFor(t, ch, "name").Link,
		"two links means two subkeys means one person masked into two")

	// The raw type still travels, because a person reading the plan is looking
	// at their own schema and String is what they will find in it.
	require.Equal(t, "String", assignmentFor(t, ch, "name").Column.Type)
	require.Equal(t, "text", assignmentFor(t, pg, "name").Column.Type)
}

// TestAssign_RefusesAnEngineNobodyHasADialectFor is the fail closed half.
//
// Falling back to Postgres would mean classifying a store with the wrong type
// vocabulary, and the result of that is not an error, it is a column that looks
// classified and was not.
func TestAssign_RefusesAnEngineNobodyHasADialectFor(t *testing.T) {
	t.Parallel()
	rs, err := masking.NewRuleSet(nil)
	require.NoError(t, err)

	tables := []masking.Table{{
		Engine: "cassandra", Schema: "app", Name: "person", PrimaryKey: []string{"id"},
		Columns: []masking.ColumnInfo{{Name: "email", Type: "text", Nullable: true}},
	}}
	plan := masking.BuildPlan(tables, rs.Assign(tables), "h")
	require.False(t, plan.Runnable(), "a store nobody has a dialect for was planned anyway")
	require.Contains(t, masking.DescribeProblems(plan.Problems), "no dialect for the engine")
	require.Contains(t, masking.DescribeProblems(plan.Problems), "cassandra")
}

// TestAssign_RefusesAClickHouseTableWithNoSortingKey is the other refusal, and
// it is at planning time for the reason every refusal in this package is: a
// masking run that fails halfway leaves a table neither real nor safe.
//
// Postgres has ctid, so it can address any row of any table. ClickHouse has no
// row identifier at all.
func TestAssign_RefusesAClickHouseTableWithNoSortingKey(t *testing.T) {
	t.Parallel()
	rs, err := masking.NewRuleSet(nil)
	require.NoError(t, err)

	tables := []masking.Table{{
		Engine: "clickhouse", Schema: "default", Name: "log",
		Columns: []masking.ColumnInfo{{Name: "email", Type: "String", Nullable: true}},
	}}
	plan := masking.BuildPlan(tables, rs.Assign(tables), "h")
	require.False(t, plan.Runnable())
	require.Contains(t, masking.DescribeProblems(plan.Problems), "no physical row identifier")

	// And the same table in Postgres is planned, because there it can be
	// addressed. The refusal is the engine's, not a new rule about keys.
	pgTables := []masking.Table{{
		Engine: "postgres", Schema: "public", Name: "log",
		Columns: []masking.ColumnInfo{{Name: "email", Type: "text", Nullable: true}},
	}}
	pgPlan := masking.BuildPlan(pgTables, rs.Assign(pgTables), "h")
	require.True(t, pgPlan.Runnable(), masking.DescribeProblems(pgPlan.Problems))
}

// TestClickHouse_StatementsAreClickHouseAndNotPostgres is the text itself.
//
// Checked here as well as through the conformance suite because the suite
// checks properties every dialect shares, and these are the specifics: a
// ClickHouse rewrite is a mutation, its parameters are named and typed, and a
// value going into a column that is not a String needs a cast that Postgres
// infers on its own.
func TestClickHouse_StatementsAreClickHouseAndNotPostgres(t *testing.T) {
	t.Parallel()
	d := clickhouse(t)
	tables := []masking.Table{{
		Engine: "clickhouse", Schema: "default", Name: "events",
		PrimaryKey: []string{"uuid"},
		Columns: []masking.ColumnInfo{
			{Name: "uuid", Type: "UUID"},
			{Name: "email", Type: "String", Nullable: true},
			{Name: "person_id", Type: "Nullable(UUID)", Nullable: true},
		},
	}}
	rs, err := masking.NewRuleSet([]masking.Rule{
		{Column: "person_id", Transform: "uuid_remap", Why: "The person a row is about."},
	})
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, rs.Assign(tables), "h")
	require.True(t, plan.Runnable(), masking.DescribeProblems(plan.Problems))
	require.Len(t, plan.Tables, 1)

	stmt := plan.Tables[0].Compile()
	require.Contains(t, stmt.SQL, "ALTER TABLE `default`.`events` UPDATE",
		"a ClickHouse rewrite is a mutation, not an UPDATE statement")
	require.NotContains(t, stmt.SQL, "$1", "the Postgres parameter shape reached ClickHouse")
	require.Contains(t, stmt.SQL, "{p1:String}")
	require.Contains(t, stmt.SQL, "WHERE toString(`uuid`) = {p1:String}")
	// The plan orders columns by name, so email is the second argument and
	// person_id the third.
	require.Contains(t, stmt.SQL, "`email` = {p2:String}")
	require.NotContains(t, stmt.SQL, "CAST({p2", "a String column needs no cast")
	require.Contains(t, stmt.SQL, "CAST({p3:String} AS Nullable(UUID))",
		"a String going into a UUID column needs the cast Postgres infers for itself")

	read := d.SelectChunk(plan.Tables[0], "")
	require.Contains(t, read.SQL, "SELECT toString(`uuid`)")
	require.NotContains(t, read.SQL, "::text", "the Postgres cast reached ClickHouse")
}

func assignmentFor(t *testing.T, in []masking.Assignment, column string) masking.Assignment {
	t.Helper()
	for _, a := range in {
		if a.Column.Name == column {
			return a
		}
	}
	t.Fatalf("no assignment for %s", column)
	return masking.Assignment{}
}
