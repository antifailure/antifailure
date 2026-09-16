package env

import (
	"testing"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// accessManifest is a two-persona application with one object owned by a persona
// and one owned by an explicit background identity, so both owner-resolution
// branches are exercised.
func accessManifest() *schema.Manifest {
	return &schema.Manifest{
		Personas: []schema.Persona{
			{Name: "alice", Role: "member", Tenant: "org_a"},
			{Name: "bob", Role: "member", Tenant: "org_a"},
		},
		Security: &schema.Security{
			Access: &schema.SecurityAccess{
				Objects: []schema.AccessObject{
					{
						Route:       "/api/orders/{id}",
						ID:          "1001",
						Owner:       schema.AccessOwner{Persona: "alice"},
						ObjectClass: "another customer's order",
						Canary:      "CANARY-1",
					},
					{
						Route:       "/api/invoices/{id}",
						ID:          "9",
						Owner:       schema.AccessOwner{Tenant: "org_b", User: "carol", Role: "admin"},
						ObjectClass: "another tenant's invoice",
						Canary:      "CANARY-2",
					},
				},
			},
		},
	}
}

func accessOrchestrator(m *schema.Manifest) *Orchestrator {
	return &Orchestrator{opts: Options{Manifest: m}}
}

// A persona owner resolves to that persona's user, role and tenant, so the
// ownership is one source of truth and a cross-owner reach can be attributed.
// This is the producer's half of the identity comparison the authz family
// decides idor on.
func TestAccessProbeDocs_ResolvesPersonaOwner(t *testing.T) {
	docs := accessOrchestrator(accessManifest()).accessProbeDocs()
	if len(docs) != 2 {
		t.Fatalf("want 2 access probe docs, got %d", len(docs))
	}
	d := docs[0]
	if d.Route != "/api/orders/{id}" || d.ID != "1001" || d.Canary != "CANARY-1" {
		t.Errorf("object fields not carried: %+v", d)
	}
	if d.Owner.User != "alice" || d.Owner.Role != "member" || d.Owner.Tenant != "org_a" {
		t.Errorf("persona owner not resolved from the persona: %+v", d.Owner)
	}
}

// An explicit owner is passed through verbatim, for an owner that seeds data but
// never signs in, so a cross-tenant reach against a background account can be
// declared.
func TestAccessProbeDocs_PassesExplicitOwner(t *testing.T) {
	docs := accessOrchestrator(accessManifest()).accessProbeDocs()
	d := docs[1]
	if d.Owner.Tenant != "org_b" || d.Owner.User != "carol" || d.Owner.Role != "admin" {
		t.Errorf("explicit owner not passed through: %+v", d.Owner)
	}
}

// A tenant on the fixture wins over the persona's own, so a cross-tenant reach
// can be expressed for personas that carry no tenant. Without this a persona
// owner could never be given a tenant the persona itself lacks and cross_tenant
// could not fire against declared personas.
func TestAccessProbeDocs_FixtureTenantOverridesPersona(t *testing.T) {
	m := accessManifest()
	m.Security.Access.Objects[0].Owner.Tenant = "org_pinned"
	d := accessOrchestrator(m).accessProbeDocs()[0]
	if d.Owner.Tenant != "org_pinned" {
		t.Errorf("fixture tenant did not win over the persona's: got %q", d.Owner.Tenant)
	}
	if d.Owner.User != "alice" {
		t.Errorf("the persona's user should still resolve: got %q", d.Owner.User)
	}
}

// No access block yields no docs, so the manifest stays off by default and the
// pass, and the second environment it would drive, is skipped for nothing.
func TestAccessProbeDocs_AbsentBlockYieldsNothing(t *testing.T) {
	if docs := accessOrchestrator(&schema.Manifest{}).accessProbeDocs(); docs != nil {
		t.Errorf("a manifest with no access block must produce no docs, got %+v", docs)
	}
	if HasAccessProbes(&schema.Manifest{}) {
		t.Error("HasAccessProbes must be false with no access block")
	}
	if !HasAccessProbes(accessManifest()) {
		t.Error("HasAccessProbes must be true when objects are declared")
	}
}

// A persona's tenant is carried into the persona document, so the runner can
// stamp the acting tenant on an observation and a cross-tenant reach can be
// decided. Without it the actor tenant is always empty and cross_tenant never
// fires.
func TestPersonaDocs_CarriesTenant(t *testing.T) {
	docs := accessOrchestrator(accessManifest()).personaDocs(nil)
	var alice *personaDoc
	for i := range docs {
		if docs[i].Name == "alice" {
			alice = &docs[i]
		}
	}
	if alice == nil {
		t.Fatal("alice persona doc missing")
	}
	if alice.Tenant != "org_a" {
		t.Errorf("persona tenant not carried into the doc: got %q", alice.Tenant)
	}
}
