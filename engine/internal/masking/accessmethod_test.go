package masking

// A TABLE STORED SOMEWHERE OTHER THAN THE HEAP IS REFUSED, AND NOTHING ELSE IS.
//
// This dialect's Unaddressable used to return empty for every table, under a
// comment reasoning that every table has a ctid. That is true of the HEAP
// rather than of Postgres. A table created `USING <am>` from an extension is
// still relkind 'r' and still a BASE TABLE in information_schema, so it was
// catalogued, planned and written to by both of this dialect's addressing
// schemes, neither of which an access method has to implement.
//
// Measured against citus columnar on Postgres 17.2, in the live test in
// engine/internal/db/docker: `SELECT ctid::text FROM t` and `UPDATE t SET ...
// WHERE id = 2` are both refused with "UPDATE and CTID scans not supported for
// ColumnarScan", and the table accepts a PRIMARY KEY regardless, so nothing
// about its shape warns anybody first.
//
// These are the same facts settled as values rather than against a container,
// so that the refusal can be broken one assertion at a time. The live test is
// what proves the catalog actually reads relam; this is what proves the plan
// does the right thing with what it read.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestATableStoredOutsideTheHeapCannotBeRewrittenARowAtATime(t *testing.T) {
	t.Parallel()

	var d postgresDialect

	require.Empty(t, d.Unaddressable(Table{Schema: "app", Name: "people", AccessMethod: "heap"}),
		"an ordinary table must be addressable, or every masking run there is stops")
	require.Empty(t, d.Unaddressable(Table{Schema: "app", Name: "people"}),
		"an empty access method is a partitioned parent whose leaves are all heap, or a Table "+
			"built by hand rather than read from a catalog; refusing those refuses everything")

	why := d.Unaddressable(Table{Schema: "app", Name: "archived", AccessMethod: "columnar"})
	require.NotEmpty(t, why,
		"a table outside the heap was accepted, so masking would discover this partway through "+
			"and leave the table neither real nor safe")
	require.Contains(t, why, "columnar",
		"the refusal does not name the access method, so nobody reading it can tell what to do")

	// A primary key does not rescue it. That is the part nothing about the
	// table's shape would tell you: citus columnar accepts a PRIMARY KEY and
	// still implements no UPDATE, so a keyed rewrite fails exactly as a ctid
	// one does.
	require.NotEmpty(t, d.Unaddressable(Table{
		Schema: "app", Name: "archived", AccessMethod: "columnar",
		PrimaryKey: []string{"id"},
	}), "a primary key was read as evidence the rewrite would work, and it is not")
}

// TestTheRefusalReachesOnlyAColumnMaskingWouldRewrite is the narrowness, and
// it is what makes a custom access method carryable rather than merely
// diagnosable.
//
// Assign consults the dialect once per table and applies the answer per
// COLUMN, gated on whether that column would actually be written. So a
// columnar table whose columns are preserved, or that holds nothing any rule
// matches, goes through untouched and its rows reach the golden.
func TestTheRefusalReachesOnlyAColumnMaskingWouldRewrite(t *testing.T) {
	t.Parallel()

	columnar := Table{
		Schema: "app", Name: "archived", AccessMethod: "columnar",
		Columns: []ColumnInfo{
			{Name: "id", Type: "integer"},
			{Name: "email", Type: "text", SQLType: "text", Nullable: true},
		},
	}
	rules, err := NewRuleSet([]Rule{
		{Table: "archived", Column: "email", Transform: "email", Why: "rewritten"},
		{Table: "archived", Column: "id", Transform: PreserveTransform, Why: "a counter"},
	})
	require.NoError(t, err)

	problems := map[string]string{}
	for _, a := range rules.Assign([]Table{columnar}) {
		if a.Problem != "" {
			problems[a.Column.Name] = a.Problem
		}
	}

	require.Contains(t, problems, "email",
		"a column masking would rewrite was planned against storage that cannot be rewritten")
	require.NotContains(t, problems, "id",
		"a preserved column was refused, which would make a custom access method impossible to "+
			"carry at all rather than impossible to rewrite")
}
