package security_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// codeAndAuthProfile is a change that touched a code endpoint and an auth
// boundary, which is the shape design 4 in the spec routes: an added API route
// and a new middleware guard. It carries facts directly rather than run through
// the path classifier, so a routing test reads the routing and not the rules.
func codeAndAuthProfile() *change.Profile {
	return &change.Profile{
		Files: 2,
		Facts: []change.Fact{
			{Path: "app/api/orders/[id]/route.ts", Surface: change.SurfaceCode,
				Rule: "path.code", Evidence: "it is application code"},
			{Path: "middleware.ts", Surface: change.SurfaceAuth,
				Rule: "path.auth", Evidence: "it governs authorization"},
		},
	}
}

func headersFamily() fakeFamily {
	return fakeFamily{
		name: "headers", surfaces: []change.Surface{change.SurfaceCode},
		checks: []change.Check{change.CheckHeaders},
	}
}

func dbFamily() fakeFamily {
	return fakeFamily{
		name: "db_security", surfaces: []change.Surface{change.SurfaceSchema},
		checks: []change.Check{change.CheckDBSecurity},
	}
}

func supplyFamily() fakeFamily {
	return fakeFamily{
		name: "supply_chain", surfaces: []change.Surface{change.SurfaceDependency},
		checks: []change.Check{change.CheckSupplyChain},
	}
}

func selectionByName(sels []security.Selection, name string) (security.Selection, bool) {
	for _, s := range sels {
		if s.Family.Name() == name {
			return s, true
		}
	}
	return security.Selection{}, false
}

func selectedNames(sels []security.Selection) []string {
	out := make([]string, 0, len(sels))
	for _, s := range sels {
		out = append(out, s.Family.Name())
	}
	return out
}

func targetByKind(ts []change.Target, kind change.TargetKind) (change.Target, bool) {
	for _, t := range ts {
		if t.Kind == kind {
			return t, true
		}
	}
	return change.Target{}, false
}

func TestSelect_SelectsOnlyFamiliesWhoseSurfaceTheChangeTouched(t *testing.T) {
	t.Parallel()
	reg := security.NewRegistry()
	reg.Register(authFamily())    // code + auth
	reg.Register(headersFamily()) // code
	reg.Register(dbFamily())      // schema, NOT touched
	reg.Register(supplyFamily())  // dependency, NOT touched

	sels := security.Select(reg, codeAndAuthProfile())

	// The liveness arm: authz and headers ran because code was touched.
	require.ElementsMatch(t, []string{"authz", "headers"}, selectedNames(sels),
		"a family runs only when its surface was in the diff")
	// The negative arm: a family whose surface the change did not touch is not
	// selected at all, which is the whole point of the router.
	_, hasDB := selectionByName(sels, "db_security")
	require.False(t, hasDB, "schema was not touched, so db_security must not be selected")
	_, hasSupply := selectionByName(sels, "supply_chain")
	require.False(t, hasSupply, "no dependency changed, so supply_chain must not be selected")
}

func TestSelect_RoutesEachFamilyOnlyTheTargetsItsSurfacesReach(t *testing.T) {
	t.Parallel()
	reg := security.NewRegistry()
	reg.Register(authFamily())    // code + auth
	reg.Register(headersFamily()) // code only

	sels := security.Select(reg, codeAndAuthProfile())

	authz, ok := selectionByName(sels, "authz")
	require.True(t, ok)
	// authz consumes code AND auth, so it reaches the endpoint and the auth
	// boundary both.
	_, hasEndpoint := targetByKind(authz.Targets, change.TargetEndpoint)
	_, hasAuth := targetByKind(authz.Targets, change.TargetAuthBoundary)
	require.True(t, hasEndpoint, "authz consumes code, so it reaches the endpoint target")
	require.True(t, hasAuth, "authz consumes auth, so it reaches the auth boundary target")
	require.Len(t, authz.Targets, 2)

	headers, ok := selectionByName(sels, "headers")
	require.True(t, ok)
	// headers consumes code only, so it reaches the endpoint but NOT the auth
	// boundary, even though the change touched one.
	_, headersHasAuth := targetByKind(headers.Targets, change.TargetAuthBoundary)
	require.False(t, headersHasAuth,
		"headers does not consume auth, so the auth boundary target is not routed to it")
	require.Len(t, headers.Targets, 1)
}

func TestSelect_FillsEachTargetsFamiliesFromTheRegistry(t *testing.T) {
	t.Parallel()
	reg := security.NewRegistry()
	reg.Register(authFamily())    // code + auth -> CheckAuthz
	reg.Register(headersFamily()) // code -> CheckHeaders

	sels := security.Select(reg, codeAndAuthProfile())
	authz, _ := selectionByName(sels, "authz")

	endpoint, ok := targetByKind(authz.Targets, change.TargetEndpoint)
	require.True(t, ok)
	// The endpoint is reachable by both code families, so it names both checks.
	require.ElementsMatch(t, []change.Check{change.CheckAuthz, change.CheckHeaders},
		endpoint.Families, "an endpoint names every security check routed to it")

	authBoundary, ok := targetByKind(authz.Targets, change.TargetAuthBoundary)
	require.True(t, ok)
	// The auth boundary is reachable only by the family that consumes auth.
	require.Equal(t, []change.Check{change.CheckAuthz}, authBoundary.Families,
		"the auth boundary names only the check whose family consumes auth")
}

func TestSelect_FailSafeSelectsEveryFamilyWhenTheDiffCouldNotNarrow(t *testing.T) {
	t.Parallel()
	reg := security.NewRegistry()
	reg.Register(authFamily())
	reg.Register(dbFamily())     // schema, which the (empty) facts do not touch
	reg.Register(supplyFamily()) // dependency, likewise

	// Everything is set when classification was incomplete: an unknown path, a
	// truncated diff, or an empty one. The router must then run every family,
	// the same way the built-in plan runs every check, because it cannot narrow.
	p := &change.Profile{Everything: true}
	sels := security.Select(reg, p)
	require.ElementsMatch(t, []string{"authz", "db_security", "supply_chain"},
		selectedNames(sels), "an incomplete classification narrows nothing, so all families run")
}

func TestSelect_EmptyRegistryAndNilInputsRouteNothing(t *testing.T) {
	t.Parallel()
	require.Empty(t, security.Select(security.NewRegistry(), codeAndAuthProfile()),
		"no family, no selection")
	require.Nil(t, security.Select(nil, codeAndAuthProfile()), "a nil registry routes nothing")
	reg := security.NewRegistry()
	reg.Register(authFamily())
	require.Nil(t, security.Select(reg, nil), "a nil profile routes nothing")
}

// TestSelect_DocsOnlyChangeRoutesNoFamily is the CI-fast promise: a diff that
// touched only docs selects no security family, exactly as it selects no
// workflow today.
func TestSelect_DocsOnlyChangeRoutesNoFamily(t *testing.T) {
	t.Parallel()
	reg := security.NewRegistry()
	reg.Register(authFamily())
	reg.Register(headersFamily())
	p := &change.Profile{
		Files: 1,
		Facts: []change.Fact{
			{Path: "docs/guide.md", Surface: change.SurfaceDocs, Rule: "path.docs", Evidence: "it is documentation"},
		},
	}
	require.Empty(t, security.Select(reg, p), "a docs-only change routes no security family")
}
