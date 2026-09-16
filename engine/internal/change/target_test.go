package change

import (
	"testing"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// A file whose path or name is about who may do what is the auth surface, not
// plain code. The rule is broad on purpose: middleware, guards, the policy and
// entitlement packages and licence gating all route here.
func TestClassify_WhoMayDoWhatIsTheAuthSurface(t *testing.T) {
	for _, path := range []string{
		"middleware.ts",
		"src/auth/session.ts",
		"packages/rbac/index.ts",
		"app/policies/order_policy.rb",
		"internal/entitlements/catalogue.go",
		"src/licensing/gate.ts",
		"server/authz/check.go",
	} {
		facts := classify(File{Path: path, Status: StatusModified}, nil, nil)
		if got := baseOf(facts); got != SurfaceAuth {
			t.Errorf("classify(%q) base surface = %q, want %q", path, got, SurfaceAuth)
		}
	}
}

// The rules that come before path.auth still win: an auth test is a test, a
// doc about auth is prose. First rule wins is the whole discipline.
func TestClassify_TestAndDocsStillOutrankAuth(t *testing.T) {
	cases := map[string]Surface{
		"internal/auth/session_test.go": SurfaceTest,
		"docs/auth/overview.md":         SurfaceDocs,
		".github/workflows/authz.yml":   SurfacePipeline,
	}
	for path, want := range cases {
		facts := classify(File{Path: path, Status: StatusModified}, nil, nil)
		if got := baseOf(facts); got != want {
			t.Errorf("classify(%q) base surface = %q, want %q", path, got, want)
		}
	}
}

// Ordinary source that is not about auth stays code, so the broad auth rule did
// not swallow the whole tree.
func TestClassify_PlainCodeIsStillCode(t *testing.T) {
	facts := classify(File{Path: "src/checkout/total.ts", Status: StatusModified}, nil, nil)
	if got := baseOf(facts); got != SurfaceCode {
		t.Errorf("plain source classified as %q, want %q", got, SurfaceCode)
	}
}

// Targets derives the routed units from the facts and the manifest: an auth
// boundary, an endpoint, a screen and a database, each carrying its facts and
// the personas that may drive it, and none carrying a family, because no family
// is registered in the spine.
func TestTargets_DerivesUnitsWithPersonasAndNoFamilies(t *testing.T) {
	m := &schema.Manifest{
		Personas: []schema.Persona{{Name: "owner"}, {Name: "other-tenant"}, {Name: "owner"}},
	}
	p := Analyze(Options{
		Manifest: m,
		Files: []File{
			{Path: "middleware.ts", Status: StatusModified},
			{Path: "app/api/orders/[id]/route.ts", Status: StatusAdded},
			{Path: "app/dashboard/page.tsx", Status: StatusModified},
			{Path: "db/migrate/0002_orders.sql", Status: StatusAdded},
		},
	})
	targets := p.Targets()

	byRef := map[string]Target{}
	for _, tg := range targets {
		byRef[tg.Ref] = tg
		if len(tg.Families) != 0 {
			t.Errorf("target %q carries families %v, but no family is registered so it must carry none",
				tg.Ref, tg.Families)
		}
	}

	assertKind(t, byRef, "middleware.ts", TargetAuthBoundary)
	assertKind(t, byRef, "app/api/orders/[id]/route.ts", TargetEndpoint)
	assertKind(t, byRef, "app/dashboard/page.tsx", TargetScreen)
	assertKind(t, byRef, "db/migrate/0002_orders.sql", TargetDatabase)

	// Persona-driven targets carry the declared personas, de-duplicated and
	// sorted; the database target carries none, because a schema object is
	// driven by SQL rather than a browser.
	ep := byRef["app/api/orders/[id]/route.ts"]
	if want := []string{"other-tenant", "owner"}; !equalStrings(ep.Personas, want) {
		t.Errorf("endpoint personas = %v, want %v", ep.Personas, want)
	}
	if db := byRef["db/migrate/0002_orders.sql"]; len(db.Personas) != 0 {
		t.Errorf("database target carries personas %v, want none", db.Personas)
	}
	// Every target names the facts that produced it.
	if len(ep.Because) == 0 {
		t.Error("the endpoint target names no fact, so its routing cannot be audited")
	}
}

// A profile decoded from JSON, with no manifest, still produces targets, just
// with no personas: the personas are a fact about the manifest that produced
// the analysis, not about the document.
func TestTargets_NoManifestMeansNoPersonas(t *testing.T) {
	p := Analyze(Options{Files: []File{{Path: "app/api/x/route.ts", Status: StatusAdded}}})
	targets := p.Targets()
	if len(targets) == 0 {
		t.Fatal("expected a target for the endpoint")
	}
	for _, tg := range targets {
		if len(tg.Personas) != 0 {
			t.Errorf("target %q carries personas %v with no manifest, want none", tg.Ref, tg.Personas)
		}
	}
}

func baseOf(facts []Fact) Surface {
	for _, f := range facts {
		if f.Surface != SurfaceService && f.Surface != SurfaceEgress {
			return f.Surface
		}
	}
	return SurfaceUnknown
}

func assertKind(t *testing.T, byRef map[string]Target, ref string, want TargetKind) {
	t.Helper()
	tg, ok := byRef[ref]
	if !ok {
		t.Errorf("no target for %q", ref)
		return
	}
	if tg.Kind != want {
		t.Errorf("target %q kind = %q, want %q", ref, tg.Kind, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
