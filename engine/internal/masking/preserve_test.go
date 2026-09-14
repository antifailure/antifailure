package masking_test

// A PRESERVE RULE RECORDS A DECISION, AND NOTHING IS WRITTEN FOR IT.
//
// preserve returns its input unchanged. The plan still put a preserved column in
// its table's column list, so the per row UPDATE wrote every preserved value back
// over itself. On the demo schema the statement was
// SET "email" = $2, "id" = $3, "name" = $4, "phone" = $5, three of those four
// writing a value with the value it already held, the primary key among them.
// Every row got a new version and every index on those columns a new entry, for
// no change at all, and a table whose every column was preserved was rewritten in
// full to change nothing.
//
// The same reading refused two plans outright. A preserve rule on a generated
// column was refused because the database computes the column and it cannot be
// written, and a preserve rule on a ClickHouse table with no sorting key was
// refused because no row of it can be addressed. Neither is written, so neither
// is a reason to refuse.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/masking"
)

func preserveTables() []masking.Table {
	return []masking.Table{
		{
			Schema: "public", Name: "customers", PrimaryKey: []string{"id"}, Rows: 400,
			Columns: []masking.ColumnInfo{
				{Name: "id", Type: "bigint"},
				{Name: "email", Type: "text"},
				{Name: "theme", Type: "text"},
			},
		},
		{
			Schema: "public", Name: "currencies", PrimaryKey: []string{"code"}, Rows: 3,
			Columns: []masking.ColumnInfo{
				{Name: "code", Type: "text"},
				{Name: "label", Type: "text"},
			},
		},
	}
}

func preserveRules(t *testing.T) *masking.RuleSet {
	t.Helper()
	rules, err := masking.NewRuleSet([]masking.Rule{
		{Table: "customers", Column: "email", Transform: "email", Why: "a person's address"},
		{Table: "customers", Column: "id", Transform: "preserve", Why: "a surrogate key"},
		{Table: "customers", Column: "theme", Transform: "preserve", Why: "light or dark"},
		{Table: "currencies", Column: "code", Transform: "preserve", Why: "an ISO 4217 code"},
		{Table: "currencies", Column: "label", Transform: "preserve", Why: "the currency's name"},
	})
	require.NoError(t, err)
	return rules
}

func preservePlan(t *testing.T) masking.Plan {
	t.Helper()
	tables := preserveTables()
	plan := masking.BuildPlan(tables, preserveRules(t).Assign(tables), "h")
	require.True(t, plan.Runnable(), masking.DescribeProblems(plan.Problems))
	return plan
}

func assignmentNames(assignments []masking.Assignment) []string {
	names := make([]string, 0, len(assignments))
	for _, a := range assignments {
		names = append(names, a.Column.Name)
	}
	return names
}

// explainLine returns the first line of a rendered plan that names a column.
func explainLine(t *testing.T, explain, column string) string {
	t.Helper()
	for _, line := range strings.Split(explain, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == column {
			return line
		}
	}
	t.Fatalf("the plan names no column %s:\n%s", column, explain)
	return ""
}

// explainSection returns what a rendered plan says under one table's heading.
func explainSection(t *testing.T, explain, table string) string {
	t.Helper()
	_, section, found := strings.Cut(explain, table+"\n")
	require.True(t, found, "the plan has no heading for %s:\n%s", table, explain)
	section, _, _ = strings.Cut(section, "\n\n")
	return section
}

func TestBuildPlan_APreservedColumnIsNotInTheStatement(t *testing.T) {
	t.Parallel()
	plan := preservePlan(t)

	var customers *masking.TablePlan
	for i := range plan.Tables {
		if plan.Tables[i].Table.Name == "customers" {
			customers = &plan.Tables[i]
		}
	}
	require.NotNil(t, customers, "a table with a column to rewrite has no statement")

	require.Equal(t, []string{"email"}, assignmentNames(customers.Columns),
		"a preserved column is among the columns the statement writes")
	require.Equal(t, []string{"id", "theme"}, assignmentNames(customers.Reviewed),
		"a preserved column left the statement and was not recorded as reviewed")
	stmt := customers.Compile()
	require.Equal(t, []string{"email"}, stmt.Columns)
	set, _, _ := strings.Cut(stmt.SQL, " WHERE ")
	require.NotContains(t, set, `"id"`, "the statement writes the primary key back over itself")
	require.NotContains(t, set, `"theme"`, "the statement writes a preserved value back over itself")

	// Reviewed, and still said so: the decision is in the plan a person reads.
	require.Contains(t, explainLine(t, plan.Explain(), "theme"), "not written",
		"a preserved column is not described as reviewed and left alone")
}

func TestBuildPlan_ATableWhoseEveryColumnIsPreservedGetsNoStatement(t *testing.T) {
	t.Parallel()
	plan := preservePlan(t)

	for _, tp := range plan.Tables {
		require.NotEqual(t, "currencies", tp.Table.Name,
			"a table whose every column is preserved is planned as a rewrite")
	}
	require.Equal(t, 1, plan.Columns(), "a preserved column is counted as a column being rewritten")
	require.Len(t, plan.Unwritten, 1, "a table with nothing to rewrite vanished instead of being recorded")
	require.Equal(t, "currencies", plan.Unwritten[0].Table.Name)
	require.Equal(t, []string{"code", "label"}, assignmentNames(plan.Unwritten[0].Reviewed),
		"the reviewed table does not carry the columns that were reviewed")

	section := explainSection(t, plan.Explain(), "public.currencies")
	require.Contains(t, section, "no statement",
		"the plan does not say that a reviewed table gets no statement")
	require.Contains(t, explainLine(t, section, "label"), "reviewed and found safe")
}

func TestAssign_APreserveRuleOnAGeneratedColumnIsNotRefused(t *testing.T) {
	t.Parallel()
	// The database computes these columns, so nothing can be written to them. A
	// rule saying one is fine as it is asks for nothing to be written; a rule
	// asking for a rewrite still cannot be carried out.
	rules, err := masking.NewRuleSet([]masking.Rule{
		{Table: "people", Column: "search_name", Transform: "preserve", Why: "derived from masked columns"},
		{Table: "people", Column: "display", Transform: "free_text", Why: "a rewrite nobody can carry out"},
	})
	require.NoError(t, err)
	tables := []masking.Table{{
		Schema: "public", Name: "people", PrimaryKey: []string{"id"},
		Columns: []masking.ColumnInfo{
			{Name: "id", Type: "bigint"},
			{Name: "search_name", Type: "text", Nullable: true, Generated: true},
			{Name: "display", Type: "text", Nullable: true, Generated: true},
		},
	}}

	assignments := rules.Assign(tables)
	require.Empty(t, find(t, assignments, "people", "search_name").Problem,
		"a preserve rule on a generated column was refused as if it had to be written")
	require.NotEmpty(t, find(t, assignments, "people", "display").Problem,
		"a rewrite of a column the database computes was planned")
}

func TestAssign_APreservedClickHouseTableWithNoSortingKeyIsNotRefused(t *testing.T) {
	t.Parallel()
	// No row of it can be addressed, and no row of it has to be.
	rules, err := masking.NewRuleSet([]masking.Rule{
		{Table: "page_views", Column: "path", Transform: "preserve", Why: "public URLs"},
		{Table: "page_views", Column: "at", Transform: "preserve", Why: "when it happened"},
	})
	require.NoError(t, err)
	tables := []masking.Table{{
		Engine: "clickhouse", Schema: "default", Name: "page_views",
		Columns: []masking.ColumnInfo{
			{Name: "path", Type: "String"},
			{Name: "at", Type: "DateTime"},
		},
	}}

	plan := masking.BuildPlan(tables, rules.Assign(tables), "h")
	require.True(t, plan.Runnable(), masking.DescribeProblems(plan.Problems))
	require.Empty(t, plan.Tables, "a table with nothing to rewrite was planned as a rewrite")
}

func TestAssign_AClickHouseTableWithNoSortingKeyIsStillRefusedARewrite(t *testing.T) {
	t.Parallel()
	rules, err := masking.NewRuleSet([]masking.Rule{
		{Table: "page_views", Column: "path", Transform: "preserve", Why: "public URLs"},
		{Table: "page_views", Column: "email", Transform: "email", Why: "a person's address"},
	})
	require.NoError(t, err)
	tables := []masking.Table{{
		Engine: "clickhouse", Schema: "default", Name: "page_views",
		Columns: []masking.ColumnInfo{
			{Name: "path", Type: "String"},
			{Name: "email", Type: "String", Nullable: true},
		},
	}}

	assignments := rules.Assign(tables)
	require.NotEmpty(t, find(t, assignments, "page_views", "email").Problem,
		"a rewrite of a table no statement can address was planned")
	require.Empty(t, find(t, assignments, "page_views", "path").Problem,
		"a preserved column was refused as if it had to be written")
}
