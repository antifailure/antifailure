package cli

// WHAT af mask plan PRINTS ABOUT A PRESERVED COLUMN.
//
// A preserved column is reviewed and never written, so it is counted apart from
// the rewrites, and a table whose every column is preserved gets no statement.
// Both are decisions somebody made, and the plan a person reads, and the JSON a
// script reads, still show them rather than dropping them with the statement.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/masking"
)

func renderedPlan(t *testing.T) masking.Plan {
	t.Helper()
	tables := []masking.Table{
		{
			Schema: "public", Name: "customers", PrimaryKey: []string{"id"}, Rows: 400,
			Columns: []masking.ColumnInfo{
				{Name: "id", Type: "bigint"}, {Name: "email", Type: "text"}, {Name: "theme", Type: "text"},
			},
		},
		{
			Schema: "public", Name: "currencies", PrimaryKey: []string{"code"}, Rows: 3,
			Columns: []masking.ColumnInfo{{Name: "code", Type: "text"}, {Name: "label", Type: "text"}},
		},
	}
	rules, err := masking.NewRuleSet([]masking.Rule{
		{Table: "customers", Column: "email", Transform: "email", Why: "a person's address"},
		{Table: "customers", Column: "id", Transform: "preserve", Why: "a surrogate key"},
		{Table: "customers", Column: "theme", Transform: "preserve", Why: "light or dark"},
		{Table: "currencies", Column: "code", Transform: "preserve", Why: "an ISO 4217 code"},
		{Table: "currencies", Column: "label", Transform: "preserve", Why: "the currency's name"},
	})
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, rules.Assign(tables), "h")
	require.True(t, plan.Runnable(), masking.DescribeProblems(plan.Problems))
	return plan
}

func TestRenderMaskPlan_CountsReviewedColumnsApartAndSaysATableGetsNoStatement(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf)}
	require.NoError(t, renderMaskPlan(e, renderedPlan(t), "h", "the configured source"))
	text := buf.String()

	require.Contains(t, text, "  1 columns across 1 tables, about 400 rows.\n",
		"a preserved column or a table with nothing to rewrite is counted as a rewrite")
	require.Contains(t, text,
		"  4 columns reviewed and found safe, which are not written; "+
			"1 tables have nothing else and get no statement.\n",
		"the reviewed columns are not counted apart from the rewrites")

	_, currencies, found := strings.Cut(text, "public.currencies\n")
	require.True(t, found, "the reviewed table is missing from the plan:\n%s", text)
	currencies, _, _ = strings.Cut(currencies, "\n\n")
	require.Contains(t, currencies, "no statement",
		"the plan does not say the reviewed table gets no statement:\n%s", text)
}

func TestRenderMaskPlan_JSONCarriesTheReviewedColumnsAndTheTablesNotWritten(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf)}
	e.Out.Format = FormatJSON
	require.NoError(t, renderMaskPlan(e, renderedPlan(t), "h", "the configured source"))

	var doc MaskPlanJSON
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc), buf.String())
	require.Equal(t, 1, doc.Tables)
	require.Equal(t, 1, doc.Columns, "a preserved column is counted among the columns rewritten")
	require.Equal(t, 4, doc.Reviewed, "the reviewed columns are not counted")
	require.Equal(t, 1, doc.TablesNotWritten, "the table with nothing to rewrite is not counted")

	decided := map[string]string{}
	for _, a := range doc.Assignments {
		decided[a.Table+"."+a.Column] = a.Transform
	}
	require.Equal(t, "email", decided["public.customers.email"])
	require.Equal(t, "preserve", decided["public.currencies.label"],
		"a reviewed column is missing from the assignments a script reads")
	require.Len(t, doc.Assignments, 5)
}
