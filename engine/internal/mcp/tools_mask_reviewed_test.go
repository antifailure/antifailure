package mcp

// A PRESERVED COLUMN IS A DECISION, AND THE PLAN DOCUMENT REPORTS IT AS ONE.
//
// The plan used to carry preserved columns among the columns a table's statement
// rewrites. They are no longer written, and a document read off the rewrites
// alone would drop them: a reviewed column would disappear from what an agent
// sees, and a table whose every column was reviewed would read exactly like a
// table nobody looked at.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/masking"
)

func TestMaskPlan_ReportsReviewedColumnsAndTablesThatGetNoStatement(t *testing.T) {
	t.Parallel()
	res := samplePlan()
	customers := res.Plan.Tables[0].Table
	res.Plan.Tables[0].Reviewed = []masking.Assignment{{
		Table: customers, Column: customers.Columns[0],
		Transform: masking.PreserveTransform, Why: "a surrogate key",
	}}
	currencies := masking.Table{
		Schema: "public", Name: "currencies", Rows: 3, PrimaryKey: []string{"code"},
		Columns: []masking.ColumnInfo{{Name: "code", Type: "text"}},
	}
	res.Plan.Unwritten = []masking.TablePlan{{
		Table: currencies,
		Reviewed: []masking.Assignment{{
			Table: currencies, Column: currencies.Columns[0],
			Transform: masking.PreserveTransform, Why: "an ISO 4217 code",
		}},
	}}

	out, fault := callMask(t, planReaders(res, nil), args("question", "plan"))
	require.Nil(t, fault)
	doc := out.(*maskingPlanDoc)

	require.Len(t, doc.Tables, 1)
	require.Len(t, doc.Tables[0].Reviewed, 1, "a reviewed column beside a rewrite is missing from the document")
	require.Equal(t, "id", doc.Tables[0].Reviewed[0].Column)
	require.Equal(t, masking.PreserveTransform, doc.Tables[0].Reviewed[0].Transform)

	require.Len(t, doc.TablesNotWritten, 1,
		"a table whose every column is preserved vanished from the document")
	require.Equal(t, "public.currencies", doc.TablesNotWritten[0].Table)
	require.Contains(t, doc.TablesNotWritten[0].Skipped, "no statement")
	require.Len(t, doc.TablesNotWritten[0].Reviewed, 1,
		"a table reported as reviewed does not carry the columns that were reviewed")
}
