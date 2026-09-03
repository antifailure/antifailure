package env_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
)

// The column is what follows the LAST dot, not the first.
//
// verify.Scan writes "schema.table.column: reason". Cutting on the first dot
// reports table "public" and column "customers.notes", which names a column
// that does not exist in an error telling somebody to grant access to it.
func TestDescribeSkip(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ line, table, column, detail string }{
		{
			"public.customers.notes: permission denied for table customers",
			"public.customers", "notes", "permission denied for table customers",
		},
		{
			// A reason carrying its own colon, which the detail must keep whole.
			"app.orders.note: ERROR: canceling statement due to statement timeout",
			"app.orders", "note", "ERROR: canceling statement due to statement timeout",
		},
		{
			// Degenerate, so the split cannot panic on something unexpected.
			"weird", "weird", "", "",
		},
	} {
		table, column, detail := env.DescribeSkipForTest(c.line)
		require.Equal(t, c.table, table, c.line)
		require.Equal(t, c.column, column, c.line)
		require.Equal(t, c.detail, detail, c.line)
	}
}
