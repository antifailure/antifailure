// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// pgx prints the inner half of net/url's error for a URL that does not parse,
// and for a password holding a slash that half is `invalid port ":<the start of
// the password>" after host`. This variable holds the control plane
// database's credential, and the error is printed to stderr.
func TestTheEvidenceDatabaseURLIsRefusedWithoutItsPassword(t *testing.T) {
	t.Setenv(databaseEnv, "postgres://auditor:Ev1dPass/9q@cp.example.test:5432/app")

	_, err := gatherEvidence(context.Background(), "org", time.Now(), time.Now())
	require.Error(t, err)
	require.NotContains(t, err.Error(), "Ev1dPass")
	require.Contains(t, err.Error(), databaseEnv, "the refusal no longer names the variable to fix")
}
