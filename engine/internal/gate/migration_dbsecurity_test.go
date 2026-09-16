package gate_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dbsecurity"
	"github.com/antifailure/antifailure/engine/internal/gate"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// lintFull wraps one lint finding in the shape gate.MigrationFindings reads, so
// a test can drive the deterministic evaluator with a single rule.
func lintFull(rule insights.Rule, statement, table string) insights.Full {
	return insights.Full{Rehearsal: &insights.Rehearsal{
		Pending: []insights.Migration{{Name: "001_change.sql"}},
		Lint: []insights.LintFinding{{
			ID: rule.ID(), Rule: rule, Migration: "001_change.sql",
			Statement: statement, Table: table,
			Detail: "detail", Fix: "fix",
		}},
	}}
}

// findRule returns the finding whose rule matches, and whether it was found.
func findRule(fs []report.Finding, rule string) (report.Finding, bool) {
	for _, f := range fs {
		if f.Rule == rule {
			return f, true
		}
	}
	return report.Finding{}, false
}

// The end-to-end claim of the whole family: a real database-security lint
// finding reaches the gate carrying the security namespace, at the level the
// family declares, and the spine's exit map turns that key into the right
// process exit. This is the observable escape, not the presence of the scanner.

func TestMigrationFindings_BroadGrantIsADenyWithTheSecurityRule(t *testing.T) {
	t.Parallel()
	full := lintFull(insights.RuleBroadGrant, "GRANT SELECT ON profiles TO PUBLIC;", "profiles")
	findings, _ := gate.MigrationFindings(full, report.Configure(nil))

	f, ok := findRule(findings, "security.db_security.broad_grant")
	require.True(t, ok, "the broadened grant did not reach the gate as a security finding")
	require.Equal(t, report.LevelFail, f.Level, "broad_grant defaults to fail")
	require.Equal(t, "profiles", f.Where)
	// The one deny-it key in the family: refused on policy grounds, exit 6.
	require.Equal(t, report.ExitPolicyDenial, security.ExitFor(report.PolicyKey(f.Rule)))
	// The plain availability rule name must NOT appear: it routed to the
	// security namespace, not to migration_lint.
	_, plain := findRule(findings, "broad_grant")
	require.False(t, plain, "the finding kept its bare rule name instead of the security key")
}

func TestMigrationFindings_RLSDisabledIsAVerificationFinding(t *testing.T) {
	t.Parallel()
	full := lintFull(insights.RuleRLSDisabled,
		"ALTER TABLE profiles DISABLE ROW LEVEL SECURITY;", "profiles")
	findings, _ := gate.MigrationFindings(full, report.Configure(nil))

	f, ok := findRule(findings, "security.db_security.rls_disabled")
	require.True(t, ok, "disabling row level security did not reach the gate as a security finding")
	require.Equal(t, report.LevelFail, f.Level)
	// Not a deny-it key: a proven regression, exit 7.
	require.Equal(t, report.ExitVerification, security.ExitFor(report.PolicyKey(f.Rule)))
}

func TestMigrationFindings_AvailabilityRuleStillRoutesToMigrationLint(t *testing.T) {
	t.Parallel()
	full := lintFull(insights.RuleDropTable, "DROP TABLE orders;", "orders")
	p := report.Configure(nil) // migration_lint defaults to warn
	findings, _ := gate.MigrationFindings(full, p)

	f, ok := findRule(findings, "drop_table")
	require.True(t, ok, "an availability rule must keep its bare name and route to migration_lint")
	require.Equal(t, p.MigrationLint, f.Level)
	_, sec := findRule(findings, "security.db_security.drop_table")
	require.False(t, sec, "an availability rule must not enter the security namespace")
}

// The manifest is the one place a severity is decided. An override lowers a
// database-security finding, and an explicit ignore silences it, without the
// default reasserting itself.
func TestMigrationFindings_ManifestOverrideWins(t *testing.T) {
	t.Parallel()
	full := lintFull(insights.RuleRLSDisabled,
		"ALTER TABLE profiles DISABLE ROW LEVEL SECURITY;", "profiles")

	warn := report.Configure(nil)
	warn.Security = map[report.PolicyKey]report.Level{
		"security.db_security.rls_disabled": report.LevelWarn,
	}
	f, ok := findRule(mustFindings(t, full, warn), "security.db_security.rls_disabled")
	require.True(t, ok)
	require.Equal(t, report.LevelWarn, f.Level, "the manifest override to warn was ignored")

	ignore := report.Configure(nil)
	ignore.Security = map[report.PolicyKey]report.Level{
		"security.db_security.rls_disabled": report.LevelIgnore,
	}
	f, ok = findRule(mustFindings(t, full, ignore), "security.db_security.rls_disabled")
	require.True(t, ok, "the finding is still produced; the level decides whether it counts")
	require.Equal(t, report.LevelIgnore, f.Level,
		"an explicit ignore in the manifest was overridden by the family default")
}

func mustFindings(t *testing.T, full insights.Full, p report.Policy) []report.Finding {
	t.Helper()
	fs, _ := gate.MigrationFindings(full, p)
	return fs
}

// The default the gate applies when the manifest is silent is the SAME level
// the db_security family declares in its KeySpec. If the two ever drift, a
// project reading the documented default and a run applying a different one
// would disagree about whether a merge is blocked.
func TestMigrationFindings_SilentDefaultMatchesTheKeySpec(t *testing.T) {
	t.Parallel()
	statements := map[insights.Rule]string{
		insights.RuleRLSDisabled:         "ALTER TABLE profiles DISABLE ROW LEVEL SECURITY;",
		insights.RuleRLSPolicyPermissive: "CREATE POLICY p ON profiles FOR SELECT USING (true);",
		insights.RuleBroadGrant:          "GRANT SELECT ON profiles TO PUBLIC;",
		insights.RuleTenantColumnRemoved: "ALTER TABLE profiles DROP COLUMN org_id;",
		insights.RuleDBRolePrivBroadened: "ALTER ROLE app BYPASSRLS;",
	}
	for _, rule := range insights.SecurityRules() {
		key := dbsecurity.KeyFor(rule)
		spec, ok := dbsecurity.KeySpecFor(key)
		require.Truef(t, ok, "%s declares no KeySpec", rule)

		full := lintFull(rule, statements[rule], "profiles")
		f, found := findRule(mustFindings(t, full, report.Configure(nil)), string(key))
		require.Truef(t, found, "%s did not reach the gate", rule)
		require.Equalf(t, spec.Default, f.Level,
			"%s: gate applied %s but the family declares %s", rule, f.Level, spec.Default)
	}
}
