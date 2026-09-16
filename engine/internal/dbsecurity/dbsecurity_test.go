package dbsecurity_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/dbsecurity"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// The family must satisfy the spine's contract so the registry can register it.
// A duplicate name or an empty surface panics at registration, so these are the
// two the registry enforces and this proves are met before that panic can fire.
func TestFamily_RegistersOnTheSpine(t *testing.T) {
	t.Parallel()
	f := dbsecurity.New()
	require.Equal(t, "db_security", f.Name())
	require.Equal(t, []change.Surface{change.SurfaceSchema}, f.Surfaces())
	require.Equal(t, []change.Check{change.CheckDBSecurity}, f.Checks())
	require.Empty(t, f.Licensed(), "database-security analysis is in every edition")

	reg := security.NewRegistry()
	require.NotPanics(t, func() { reg.Register(f) })
	require.Equal(t, []security.Family{f}, reg.ForSurface(change.SurfaceSchema))
}

// Every database-security lint rule has exactly one policy key, and the key is
// the security.db_security.<rule> shape the finding rule and the MCP projection
// both read. A rule with no key, or an extra key naming no rule, would be dead
// config or an invisible finding.
func TestFamily_DeclaresOneKeyPerRule(t *testing.T) {
	t.Parallel()
	keys := dbsecurity.New().Keys()
	require.Len(t, keys, len(insights.SecurityRules()))

	byKey := map[report.PolicyKey]security.KeySpec{}
	for _, k := range keys {
		require.True(t, strings.HasPrefix(string(k.Key), "security.db_security."),
			"%s is not in the family namespace", k.Key)
		require.Equal(t, "db_security", security.FamilyOf(string(k.Key)),
			"%s does not group under db_security", k.Key)
		require.NotEmpty(t, k.Title, "%s has no documentation title", k.Key)
		byKey[k.Key] = k
	}
	for _, rule := range insights.SecurityRules() {
		_, ok := byKey[dbsecurity.KeyFor(rule)]
		require.Truef(t, ok, "%s has no policy key", rule)
	}
}

// The exit a key declares must equal the exit the spine's gate produces for it,
// or the code the family documents and the code the process returns disagree.
// broad_grant is the deny-it key; the rest are verification failures.
func TestFamily_KeySpecExitMatchesTheSpine(t *testing.T) {
	t.Parallel()
	deny := map[report.PolicyKey]bool{"security.db_security.broad_grant": true}
	for _, k := range dbsecurity.New().Keys() {
		require.Equalf(t, security.ExitFor(k.Key), k.Exit,
			"%s declares exit %d but the spine maps it to %d", k.Key, k.Exit, security.ExitFor(k.Key))
		if deny[k.Key] {
			require.Equalf(t, report.ExitPolicyDenial, k.Exit, "%s should be a policy denial", k.Key)
		} else {
			require.Equalf(t, report.ExitVerification, k.Exit, "%s should be a verification failure", k.Key)
		}
	}
}

// Every database-security key defaults to fail: turning off row level security,
// opening a policy, or broadening a grant is a regression by default, and a
// project that wants one as a warning says so in its manifest.
func TestFamily_KeysDefaultToFail(t *testing.T) {
	t.Parallel()
	for _, k := range dbsecurity.New().Keys() {
		require.Equalf(t, report.LevelFail, k.Default, "%s should default to fail", k.Key)
	}
}

// KeySpecFor is the single source the gate reads a default back from. It
// answers for every key the family declares and refuses one it does not, so the
// gate can tell a database-security key from any other rule.
func TestKeySpecFor_AnswersForDeclaredKeysOnly(t *testing.T) {
	t.Parallel()
	for _, k := range dbsecurity.New().Keys() {
		got, ok := dbsecurity.KeySpecFor(k.Key)
		require.Truef(t, ok, "%s is declared but KeySpecFor does not know it", k.Key)
		require.Equal(t, k, got)
	}
	_, ok := dbsecurity.KeySpecFor("security.db_security.not_a_rule")
	require.False(t, ok, "KeySpecFor claimed a key the family never declared")
	_, ok = dbsecurity.KeySpecFor("migration_lint")
	require.False(t, ok, "KeySpecFor answered for an availability key")
}

// Probe is an intentional no-op: database-security findings come from the
// migration path, and Input carries no migration to analyse. This proves it
// stays empty rather than one day being wired to a second, divergent detector.
func TestProbe_IsAnIntentionalNoOp(t *testing.T) {
	t.Parallel()
	findings, err := dbsecurity.New().Probe(context.Background(), security.Input{})
	require.NoError(t, err)
	require.Empty(t, findings)
}
