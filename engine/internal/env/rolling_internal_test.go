package env

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// What this file proves is the decision the rolling check makes BEFORE it
// spends anything. Everything past that point needs a daemon, a golden and a
// browser, and is proved by running it rather than here.
//
// It is worth its own test because both ways of getting it wrong are invisible
// in the output and expensive in opposite directions. Running when nothing
// narrowed doubles every pipeline that ships an additive migration; not
// running when something did silently skips the check on the migration it
// exists for. Both produce a report that looks fine.

func rehearsalOf(sql ...string) *insights.Rehearsal {
	r := &insights.Rehearsal{}
	for i, s := range sql {
		r.Pending = append(r.Pending, insights.Migration{
			Version: string(rune('a' + i)), Name: "000x.sql", SQL: s,
		})
	}
	return r
}

func decide(t *testing.T, when string, r *insights.Rehearsal) rollingDecision {
	t.Helper()
	cfg := insights.Configure(&schema.Insights{
		RollingCompatibility: &schema.RollingCompatibility{When: when},
	})
	return decideRolling(cfg.Rolling, r)
}

func TestDecideRolling_AdditiveMigrationsDoNotPayForTheCheck(t *testing.T) {
	t.Parallel()
	d := decide(t, "risky", rehearsalOf(
		"CREATE TABLE refunds (id serial PRIMARY KEY)",
		"ALTER TABLE orders ADD COLUMN note text",
		"ALTER TABLE orders ADD COLUMN currency text NOT NULL DEFAULT 'usd'",
		"CREATE INDEX CONCURRENTLY orders_placed_idx ON orders (placed_at)",
	))
	require.False(t, d.run)
	require.Equal(t, insights.RollingOff, d.verdict)
	require.Contains(t, d.reason, "only add things the previous release cannot notice")

	// Not in the report's "turned off in the manifest" list. The manifest did
	// not turn this off; the migration did not need it. It printed there once,
	// and the sentence was both duplicated and untrue.
	require.False(t, d.manifestOff)
}

func TestDecideRolling_NarrowingMigrationsRunTheCheck(t *testing.T) {
	t.Parallel()
	for _, sql := range []string{
		"ALTER TABLE customers DROP COLUMN email",
		"ALTER TABLE customers RENAME COLUMN email TO email_address",
		"DROP TABLE orders",
		"ALTER TABLE orders ALTER COLUMN total_cents TYPE bigint",
		"ALTER TABLE orders ALTER COLUMN phone SET NOT NULL",
		"ALTER TABLE orders ADD COLUMN currency text NOT NULL",
	} {
		d := decide(t, "risky", rehearsalOf(sql))
		require.True(t, d.run, sql)
		require.Len(t, d.statements, 1, sql)
	}
}

func TestDecideRolling_AlwaysRunsForAnAdditiveMigrationToo(t *testing.T) {
	t.Parallel()
	d := decide(t, "always", rehearsalOf("ALTER TABLE orders ADD COLUMN note text"))
	require.True(t, d.run)
}

func TestDecideRolling_NeverIsTheOneReasonThatBelongsInTheManifestList(t *testing.T) {
	t.Parallel()
	d := decide(t, "never", rehearsalOf("ALTER TABLE customers DROP COLUMN email"))
	require.False(t, d.run)
	require.Equal(t, insights.RollingOff, d.verdict)
	require.Equal(t, "insights.rolling_compatibility.when is never", d.reason)
	require.True(t, d.manifestOff)
}

func TestDecideRolling_AFailedOrAbsentRehearsalIsBlockedRatherThanOff(t *testing.T) {
	t.Parallel()
	// Blocked, not off, and the difference matters: off says there was nothing
	// to check, blocked says the check could not be made. Reporting the second
	// as the first would hide a migration that did not apply behind a line
	// that reads like a clean bill of health.
	d := decide(t, "risky", nil)
	require.False(t, d.run)
	require.Equal(t, insights.RollingBlocked, d.verdict)
	require.Contains(t, d.reason, "not rehearsed")
	require.False(t, d.manifestOff)

	failed := rehearsalOf("ALTER TABLE customers DROP COLUMN email")
	failed.Failed = true
	d = decide(t, "risky", failed)
	require.False(t, d.run)
	require.Equal(t, insights.RollingBlocked, d.verdict)
	require.Contains(t, d.reason, "did not apply")
}

func TestDecideRolling_NothingPendingIsOff(t *testing.T) {
	t.Parallel()
	d := decide(t, "always", &insights.Rehearsal{})
	require.False(t, d.run)
	require.Equal(t, insights.RollingOff, d.verdict)
	require.Contains(t, d.reason, "no migration pending")
}

func TestRollingStatements_PrefersTheFileOverTheRecordedTiming(t *testing.T) {
	t.Parallel()
	// A statement timing is normalised for display and cut at 160 characters,
	// so an ALTER TABLE with several actions can lose one to the cut. The file
	// is complete. The recorded statements are what a repository whose
	// migrations are Ruby or Python has instead of a file, and they are the
	// only reason this check works for those tools at all.
	r := rehearsalOf("ALTER TABLE customers DROP COLUMN email; ALTER TABLE orders DROP COLUMN note")
	r.Statements = []insights.StatementTiming{{SQL: "something the server saw"}}
	stmts := rollingStatements(r)
	require.Len(t, stmts, 2)
	require.Equal(t, "ALTER TABLE customers DROP COLUMN email", stmts[0].SQL)

	fromServer := rollingStatements(&insights.Rehearsal{
		Statements: []insights.StatementTiming{
			{Migration: "0002", SQL: "ALTER TABLE customers DROP COLUMN email"},
		},
	})
	require.Len(t, fromServer, 1)
	require.Equal(t, 1, fromServer[0].Index)
	require.True(t, insights.Narrowing(fromServer))

	require.Empty(t, rollingStatements(nil))
}
