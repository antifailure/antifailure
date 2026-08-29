package manifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The mistake everybody makes first, including this repository's own example:
// counting the violations rather than returning them. count(*) returns one row
// saying zero, one row is a violation, so the invariant is red forever.
func TestABareCountInvariantIsRefusedBecauseItCanNeverHold(t *testing.T) {
	_, err := parse(t, strings.TrimSpace(`
version: 1
name: shop
services:
  - name: web
    port: 3000
invariants:
  - name: no-orphan-orders
    sql: |
      SELECT count(*) AS violations
      FROM orders o LEFT JOIN users u ON u.id = o.user_id
      WHERE u.id IS NULL
`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "can never hold")
	require.Contains(t, err.Error(), "returns no rows")
}

func TestReturningTheViolatingRowsIsAccepted(t *testing.T) {
	_, err := parse(t, strings.TrimSpace(`
version: 1
name: shop
services:
  - name: web
    port: 3000
invariants:
  - name: no-orphan-orders
    sql: |
      SELECT o.id FROM orders o
      LEFT JOIN users u ON u.id = o.user_id
      WHERE u.id IS NULL
`))
	require.NoError(t, err)
}

// A correct invariant is allowed to mention an aggregate. Refusing this would
// be worse than missing the bare-count case, so the check stays narrow.
func TestAnAggregateInASubqueryIsNotMistakenForABareCount(t *testing.T) {
	_, err := parse(t, strings.TrimSpace(`
version: 1
name: shop
services:
  - name: web
    port: 3000
invariants:
  - name: no-wild-totals
    sql: |
      SELECT id FROM orders
      WHERE total_cents > (SELECT avg(total_cents) * 1000 FROM orders)
`))
	require.NoError(t, err)
}

// A grouped aggregate returns a row per group and no rows when nothing
// matches, which is the shape that works.
func TestAGroupedAggregateIsAccepted(t *testing.T) {
	_, err := parse(t, strings.TrimSpace(`
version: 1
name: shop
services:
  - name: web
    port: 3000
invariants:
  - name: no-duplicate-emails
    sql: |
      SELECT email, count(*) FROM users GROUP BY email HAVING count(*) > 1
`))
	require.NoError(t, err)
}
