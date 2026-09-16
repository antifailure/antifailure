package authz_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
	"github.com/antifailure/antifailure/engine/internal/security/authz"
)

// The family's identity is the contract the spine routes, levels and licenses
// it by. Name is the namespace of every key and rule it owns.
func TestFamily_Identity(t *testing.T) {
	t.Parallel()
	f := authz.New()
	require.Equal(t, "authz", f.Name())
	require.ElementsMatch(t, []change.Surface{change.SurfaceAuth, change.SurfaceCode}, f.Surfaces())
	require.Equal(t, []change.Check{change.CheckAuthz}, f.Checks())
	require.Equal(t, "", f.Licensed(), "authorization is a check every edition gets")
}

// The family declares the six keys the reconciled contract assigns it, each
// defaulting to fail, so a project that says nothing gets the posture that does
// not lie.
func TestFamily_DeclaresItsKeys(t *testing.T) {
	t.Parallel()
	f := authz.New()
	var got []report.PolicyKey
	for _, k := range f.Keys() {
		got = append(got, k.Key)
		require.Equal(t, report.LevelFail, k.Default, "%s defaults to fail", k.Key)
		require.Equal(t, "concepts/security", k.Docs, "%s points at the security concept page", k.Key)
		require.NotEmpty(t, k.Title, "%s has a title for the generated policy docs", k.Key)
	}
	require.ElementsMatch(t, []report.PolicyKey{
		authz.RuleMissingAuthorization,
		authz.RuleIDOR,
		authz.RuleCrossTenant,
		authz.RuleUnauthenticatedAccess,
		authz.RulePrivilegeEscalation,
		authz.RulePolicyBypass,
	}, got)
}

// A KeySpec's declared exit MUST equal the exit the spine derives for its key,
// or the gate and the family would disagree about what a finding does to the
// process. Every authz key is a proven vulnerability, so every one exits with
// the verification code.
func TestFamily_ExitMatchesTheSpine(t *testing.T) {
	t.Parallel()
	for _, k := range authz.New().Keys() {
		require.Equalf(t, security.ExitFor(k.Key), k.Exit,
			"%s: the declared exit must equal the spine's ExitFor", k.Key)
		require.Equalf(t, report.ExitVerification, k.Exit,
			"%s: an authz finding is a proven vulnerability, so it is a verification failure", k.Key)
	}
}

// The family registers against the spine without panicking: it names a
// non-empty surface set and a stable name, which is what the registry demands.
func TestFamily_RegistersCleanly(t *testing.T) {
	t.Parallel()
	reg := security.NewRegistry()
	require.NotPanics(t, func() { reg.Register(authz.New()) })
	require.Equal(t, []string{"authz"}, namesOf(reg.Families()))
	require.Equal(t, []string{"authz"}, namesOf(reg.ForSurface(change.SurfaceAuth)))
	require.Equal(t, []string{"authz"}, namesOf(reg.ForSurface(change.SurfaceCode)))
	require.Empty(t, reg.ForSurface(change.SurfaceSchema), "the family does not consume the schema surface")
}

func namesOf(fs []security.Family) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Name())
	}
	return out
}
