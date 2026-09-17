package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The manifest half of the authenticated authorization differential: a security
// access block that declares an ownership-scoped object, who owns it and the
// canary the app seeded into it. The bounds pass keeps the lengths and the
// route pattern from the schema; these tests are the cross-field part, that an
// object names a real owner, a real persona, and is not declared twice.

const withPersonas = `
version: 1
name: shop
services:
  - name: web
    port: 3000
personas:
  - name: alice
    tenant: org_a
  - name: bob
    tenant: org_a
`

// A fully populated, valid access block parses, and the values survive
// normalization into the typed manifest. A collector that could never see these
// values would be building the golden on nothing.
func TestParse_AcceptsAValidAccessBlock(t *testing.T) {
	t.Parallel()
	body := withPersonas + `security:
  access:
    objects:
      - route: /api/orders/{id}
        id: "1001"
        owner:
          persona: alice
        object_class: another customer's order
        canary: CANARY-ALICE
`
	m := mustParse(t, body)
	require.NotNil(t, m.Security)
	require.NotNil(t, m.Security.Access)
	require.Len(t, m.Security.Access.Objects, 1)
	o := m.Security.Access.Objects[0]
	require.Equal(t, "/api/orders/{id}", o.Route)
	require.Equal(t, "1001", o.ID)
	require.Equal(t, "alice", o.Owner.Persona)
	require.Equal(t, "CANARY-ALICE", o.Canary)
	// Normalization defaults the kind, so a fixture that plants a canary without
	// saying what it is is another party's data.
	require.Equal(t, schema.CanaryPII, o.CanaryKind)
}

// An explicit canary kind survives, so a planted credential lands on the secret
// key rather than the pii one.
func TestParse_AcceptsAnExplicitCanaryKind(t *testing.T) {
	t.Parallel()
	body := withPersonas + `security:
  access:
    objects:
      - route: /api/keys/{id}
        id: "k1"
        owner:
          persona: alice
        object_class: another tenant's api key
        canary: CANARY-KEY
        canary_kind: secret
`
	m := mustParse(t, body)
	require.Equal(t, schema.CanarySecret, m.Security.Access.Objects[0].CanaryKind)
}

// An owner that names a persona the manifest does not declare is refused rather
// than silently probed as an unknown identity, because a probe attributed to
// nobody would decide no boundary and the author would never learn it did
// nothing. Break the personas[owner.Persona] check and this goes green on a lie.
func TestParse_RefusesAnUnknownOwnerPersona(t *testing.T) {
	t.Parallel()
	body := withPersonas + `security:
  access:
    objects:
      - route: /api/orders/{id}
        id: "1001"
        owner:
          persona: carol
        object_class: another customer's order
        canary: CANARY-ALICE
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The owner "carol" is not a declared persona.`)
}

// The same object declared twice is refused: a second entry with the same route
// and id only re-asks the same question and would inflate a finding's count.
func TestParse_RefusesADuplicateObject(t *testing.T) {
	t.Parallel()
	body := withPersonas + `security:
  access:
    objects:
      - route: /api/orders/{id}
        id: "1001"
        owner:
          persona: alice
        object_class: another customer's order
        canary: CANARY-A
      - route: /api/orders/{id}
        id: "1001"
        owner:
          persona: bob
        object_class: another customer's order
        canary: CANARY-B
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `both declare the object "1001" at "/api/orders/{id}"`)
}

// An object with no owner at all is refused: without an owner a reach cannot be
// told from a self read, so the whole differential would be meaningless.
func TestParse_RefusesAnObjectWithNoOwner(t *testing.T) {
	t.Parallel()
	body := withPersonas + `security:
  access:
    objects:
      - route: /api/orders/{id}
        id: "1001"
        owner: {}
        object_class: another customer's order
        canary: CANARY-A
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "An access object names no owner.")
}

// An explicit owner with no persona is accepted, for an owner that seeds data
// but never signs in, so a cross-tenant reach against a background account can
// be declared.
func TestParse_AcceptsAnExplicitOwner(t *testing.T) {
	t.Parallel()
	body := withPersonas + `security:
  access:
    objects:
      - route: /api/invoices/{id}
        id: "9"
        owner:
          tenant: org_b
          user: carol
          role: admin
        object_class: another tenant's invoice
        canary: CANARY-INV
`
	m := mustParse(t, body)
	require.Equal(t, "org_b", m.Security.Access.Objects[0].Owner.Tenant)
	require.Equal(t, "carol", m.Security.Access.Objects[0].Owner.User)
}
