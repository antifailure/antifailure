package security_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// fakeFamily is a family the tests register. The spine ships none, so the
// contract is exercised through one written here, exactly as a real family
// will be written when it lands.
type fakeFamily struct {
	name     string
	surfaces []change.Surface
	checks   []change.Check
	keys     []security.KeySpec
	licensed string
}

func (f fakeFamily) Name() string               { return f.name }
func (f fakeFamily) Surfaces() []change.Surface { return f.surfaces }
func (f fakeFamily) Checks() []change.Check     { return f.checks }
func (f fakeFamily) Keys() []security.KeySpec   { return f.keys }
func (f fakeFamily) Licensed() string           { return f.licensed }
func (f fakeFamily) Probe(context.Context, security.Input) ([]report.Finding, error) {
	return nil, nil
}

func authFamily() fakeFamily {
	return fakeFamily{
		name:     "authz",
		surfaces: []change.Surface{change.SurfaceCode, change.SurfaceAuth},
		checks:   []change.Check{change.CheckAuthz},
		keys: []security.KeySpec{{
			Key: "security.authz.idor", Default: report.LevelFail,
			Title: "an object was reached across a tenant boundary",
			Docs:  "concepts/security", Exit: report.ExitVerification,
		}},
	}
}

func TestDefault_IsEmptyButWired(t *testing.T) {
	t.Parallel()
	reg := security.Default()
	require.NotNil(t, reg, "Default must return a usable registry, not nil")
	require.Empty(t, reg.Families(), "the spine registers no family, so the plan gains no security check")
	require.Empty(t, reg.Keys(), "no family, so no policy key is declared yet")
	require.Empty(t, reg.ForSurface(change.SurfaceCode), "nothing routes until a family registers")
}

func TestRegister_ADuplicateNamePanics(t *testing.T) {
	t.Parallel()
	reg := security.NewRegistry()
	reg.Register(authFamily())
	require.PanicsWithValue(t,
		`security: the family "authz" is registered twice`,
		func() { reg.Register(authFamily()) },
		"two families under one name is a programming error, like a duplicate tool name")
}

func TestRegister_AFamilyWithNoSurfacePanics(t *testing.T) {
	t.Parallel()
	reg := security.NewRegistry()
	f := authFamily()
	f.surfaces = nil
	require.Panics(t, func() { reg.Register(f) },
		"a family that consumes no surface never runs and must not sit in the plan looking live")
}

func TestForSurface_ReturnsOnlyFamiliesThatConsumeIt(t *testing.T) {
	t.Parallel()
	reg := security.NewRegistry()
	reg.Register(authFamily()) // code + auth
	reg.Register(fakeFamily{
		name: "headers", surfaces: []change.Surface{change.SurfaceCode},
		checks: []change.Check{change.CheckHeaders},
	})
	code := names(reg.ForSurface(change.SurfaceCode))
	require.ElementsMatch(t, []string{"authz", "headers"}, code)
	auth := names(reg.ForSurface(change.SurfaceAuth))
	require.Equal(t, []string{"authz"}, auth, "only the family that declared auth routes to auth")
	require.Empty(t, reg.ForSurface(change.SurfaceDocs), "no family declared docs, so nothing routes there")
}

func TestKeys_UnionsEveryRegisteredFamily(t *testing.T) {
	t.Parallel()
	reg := security.NewRegistry()
	reg.Register(authFamily())
	reg.Register(fakeFamily{
		name: "headers", surfaces: []change.Surface{change.SurfaceCode},
		keys: []security.KeySpec{{Key: "security.headers.missing_hsts", Default: report.LevelWarn}},
	})
	var got []report.PolicyKey
	for _, k := range reg.Keys() {
		got = append(got, k.Key)
	}
	require.ElementsMatch(t,
		[]report.PolicyKey{"security.authz.idor", "security.headers.missing_hsts"}, got)
}

func TestExitFor_ProvenIsVerificationRefusedIsPolicyDenial(t *testing.T) {
	t.Parallel()
	// A family that PROVED a vulnerability exits with the verification code.
	for _, key := range []report.PolicyKey{
		"security.authz.idor",
		// Reserved alongside the four named authz keys: a control evaded by an
		// absent rule is proven by exercising it, so it is a verification
		// failure like the rest of its family. The open namespace gives it the
		// right exit before the authz family declares its KeySpec.
		"security.authz.policy_bypass",
		"security.injection.sql",
		"security.canary_leak.cross_tenant",
		"security.side_effect.destructive_on_read",
		"security.db_security.rls_disabled",
		"security.ssrf.internal_host",
		"security.secret_exposure.in_response",
	} {
		require.Equalf(t, report.ExitVerification, security.ExitFor(key),
			"%s proves a vulnerability, so it is a verification failure (7)", key)
	}
	// A family that REFUSED a change on policy grounds exits with the policy
	// denial code, including the two deny-it keys inside otherwise prove-it
	// families.
	for _, key := range []report.PolicyKey{
		"security.headers.permissive_cors",
		"security.supply_chain.known_vuln",
		"security.side_effect.external_call",
		"security.db_security.broad_grant",
	} {
		require.Equalf(t, report.ExitPolicyDenial, security.ExitFor(key),
			"%s refuses a change on policy grounds, so it is a policy denial (6)", key)
	}
}

func TestFamilyOf_ReadsTheFamilyFromARule(t *testing.T) {
	t.Parallel()
	require.Equal(t, "authz", security.FamilyOf("security.authz.idor"))
	require.Equal(t, "headers", security.FamilyOf("security.headers.missing_csp"))
	require.Equal(t, "", security.FamilyOf("migration_failed"),
		"a rule outside the security namespace belongs to no family")
	require.Equal(t, "security.", security.Prefix())
}

func names(fs []security.Family) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Name())
	}
	return out
}
