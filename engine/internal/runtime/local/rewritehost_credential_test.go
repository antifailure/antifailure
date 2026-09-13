package local

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// AF-RUN-040 prints its cause, and the cause was url.Parse's error, which
// quotes the connection string it could not read.
func TestRewriteHostNeverQuotesThePasswordOfAValueThatDoesNotParse(t *testing.T) {
	_, err := rewriteHost("postgres://app:Lh6Pass/z@db:5432/app", "db-alias", 5432)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "Lh6Pass")
	require.Contains(t, err.Error(), "not quoted", "the refusal no longer says what is wrong")
}
