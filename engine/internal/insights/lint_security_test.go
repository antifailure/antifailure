package insights_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
)

// The database-security rules follow the same discipline as the DDL-safety
// ones: every rule has a positive fixture that must fire and a negative fixture
// that must not, because a rule that fires on the safe form is one somebody
// turns off, and then the unsafe form ships too. They lint against bigSchema so
// the lock_timeout rule, which fires when nothing sets one, does not answer
// alongside them.

func TestLintSecurity_RLSDisabled(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "ALTER TABLE profiles DISABLE ROW LEVEL SECURITY;")
	require.Equal(t, []insights.Rule{insights.RuleRLSDisabled}, rulesIn(f))
	require.Equal(t, "profiles", f[0].Table)
	require.Contains(t, f[0].Fix, "row level security")
}

func TestLintSecurity_RLSEnabledIsFine(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintOne(t, "ALTER TABLE profiles ENABLE ROW LEVEL SECURITY;"))
}

func TestLintSecurity_PermissivePolicyUsingTrue(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "CREATE POLICY read_all ON profiles FOR SELECT USING (true);")
	require.Equal(t, []insights.Rule{insights.RuleRLSPolicyPermissive}, rulesIn(f))
	require.Equal(t, "profiles", f[0].Table)
}

func TestLintSecurity_PermissivePolicyOneEqualsOne(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "CREATE POLICY read_all ON profiles FOR SELECT USING (1 = 1);")
	require.Equal(t, []insights.Rule{insights.RuleRLSPolicyPermissive}, rulesIn(f))
}

func TestLintSecurity_TenantScopedPolicyIsFine(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintOne(t,
		"CREATE POLICY tenant ON profiles FOR SELECT USING (org_id = current_setting('app.org')::uuid);"))
}

func TestLintSecurity_BroadGrantToPublic(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "GRANT SELECT ON profiles TO PUBLIC;")
	require.Equal(t, []insights.Rule{insights.RuleBroadGrant}, rulesIn(f))
	require.Equal(t, "profiles", f[0].Table)
	require.Contains(t, f[0].Detail, "PUBLIC")
}

func TestLintSecurity_BroadGrantToAnon(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "GRANT SELECT, UPDATE ON profiles TO anon, authenticated;")
	require.Equal(t, []insights.Rule{insights.RuleBroadGrant}, rulesIn(f))
}

func TestLintSecurity_GrantToANamedRoleIsFine(t *testing.T) {
	t.Parallel()
	require.Empty(t, lintOne(t, "GRANT SELECT ON profiles TO app_reader;"))
}

func TestLintSecurity_RoleMembershipGrantIsNotJudgedHere(t *testing.T) {
	t.Parallel()
	// A GRANT with no ON clause is a role membership grant, whose danger
	// depends on the absolute role catalogue this family refuses to read.
	require.Empty(t, lintOne(t, "GRANT admin TO app_user;"))
}

func TestLintSecurity_TenantColumnDropped(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "ALTER TABLE profiles DROP COLUMN org_id;")
	require.Equal(t, []insights.Rule{insights.RuleTenantColumnRemoved}, rulesIn(f))
	require.Equal(t, "profiles", f[0].Table)
}

func TestLintSecurity_DroppingAnOrdinaryColumnIsNotATenantRemoval(t *testing.T) {
	t.Parallel()
	// note is not a tenant column and no view reads it, so nothing fires.
	require.Empty(t, lintOne(t, "ALTER TABLE profiles DROP COLUMN note;"))
}

func TestLintSecurity_RoleGivenBypassRLS(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "ALTER ROLE app_worker BYPASSRLS;")
	require.Equal(t, []insights.Rule{insights.RuleDBRolePrivBroadened}, rulesIn(f))
	require.Equal(t, "app_worker", f[0].Table)
	require.Contains(t, f[0].Detail, "BYPASSRLS")
}

func TestLintSecurity_RoleGivenSuperuser(t *testing.T) {
	t.Parallel()
	f := lintOne(t, "ALTER ROLE app_worker SUPERUSER;")
	require.Equal(t, []insights.Rule{insights.RuleDBRolePrivBroadened}, rulesIn(f))
	require.Contains(t, f[0].Detail, "superuser")
}

func TestLintSecurity_RemovingBypassRLSIsFine(t *testing.T) {
	t.Parallel()
	// NOBYPASSRLS and NOSUPERUSER remove the attribute; they contain the word
	// as a substring and must not match it.
	require.Empty(t, lintOne(t, "ALTER ROLE app_worker NOSUPERUSER NOBYPASSRLS;"))
}

// Class routes a finding to its policy key, and getting it backwards would send
// a security regression to migration_lint or an availability finding to
// security.db_security. Every security rule is security class and every other
// rule is availability class.
func TestClass_SecurityRulesAreSecurityClass(t *testing.T) {
	t.Parallel()
	security := map[insights.Rule]bool{}
	for _, r := range insights.SecurityRules() {
		security[r] = true
		require.Equal(t, insights.ClassSecurity, r.Class(), "%s should be security class", r)
	}
	for _, r := range insights.AllRules() {
		if security[r] {
			continue
		}
		require.Equal(t, insights.ClassAvailability, r.Class(),
			"%s is not a database-security rule and should be availability class", r)
	}
}

// SecurityRules is a subset of AllRules and carries no availability rule, so a
// caller walking it to declare a key per rule declares a key for exactly the
// security namespace.
func TestSecurityRulesAreAllRegistered(t *testing.T) {
	t.Parallel()
	all := map[insights.Rule]bool{}
	for _, r := range insights.AllRules() {
		all[r] = true
	}
	require.NotEmpty(t, insights.SecurityRules())
	for _, r := range insights.SecurityRules() {
		require.True(t, all[r], "%s is a security rule missing from AllRules", r)
	}
}
