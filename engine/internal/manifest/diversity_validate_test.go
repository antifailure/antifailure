package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The manifest half of the personality engine: a diversity block that names a
// personality, and a workflow that pins one. The bounds pass keeps the ranges
// and enums from the schema; these tests are the cross field part, that an id
// must name a built in and is not listed twice.

const withWorkflow = `
version: 1
name: shop
services:
  - name: web
    port: 3000
personas:
  - name: member
workflows:
  - name: checkout
    description: buy one item and reach the receipt page
    persona: member
`

// A fully populated, valid diversity block parses, and the values survive
// normalization into the typed manifest. A resolver that could never see these
// values would be building on nothing.
func TestParse_AcceptsAValidDiversityBlock(t *testing.T) {
	t.Parallel()
	body := withWorkflow + `diversity:
  enabled: true
  seed: release-9
  mix: aggressive_diversity
  variance: high
  agents_per_workflow: 3
  personalities:
    - id: skeptic
      weight: 40
    - id: explorer
`
	m := mustParse(t, body)
	require.NotNil(t, m.Diversity)
	require.True(t, m.Diversity.Enabled)
	require.Equal(t, schema.MixAggressive, m.Diversity.Mix)
	require.Equal(t, schema.VarianceHigh, m.Diversity.Variance)
	require.Equal(t, 3, m.Diversity.AgentsPerWorkflow)
	require.Len(t, m.Diversity.Personalities, 2)
	require.Equal(t, "skeptic", m.Diversity.Personalities[0].ID)
	require.Equal(t, 40.0, m.Diversity.Personalities[0].Weight)
}

// An unknown personality id is refused rather than silently ignored, because a
// silently ignored one would draw nothing and the author would never learn the
// selection did nothing. Break the schema.IsBuiltInPersonality check and this
// goes green on a lie.
func TestParse_RefusesAnUnknownPersonalityID(t *testing.T) {
	t.Parallel()
	body := withWorkflow + "diversity:\n  enabled: true\n  personalities:\n    - id: skeptical\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The personality "skeptical" is not a built in personality.`)
	require.Contains(t, msg, "skeptic")
}

// The same personality listed twice is refused: a second entry is either a
// mistake or an attempt to reweight that should have set the weight instead.
func TestParse_RefusesADuplicatePersonality(t *testing.T) {
	t.Parallel()
	body := withWorkflow + "diversity:\n  enabled: true\n  personalities:\n    - id: skeptic\n    - id: skeptic\n      weight: 5\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `The personality "skeptic" is listed twice.`)
}

// A workflow may pin a personality, and the pin must name a built in for the
// same reason the selection must.
func TestParse_RefusesAnUnknownWorkflowPersonalityPin(t *testing.T) {
	t.Parallel()
	body := `
version: 1
name: shop
services:
  - name: web
    port: 3000
personas:
  - name: member
workflows:
  - name: checkout
    description: buy one item and reach the receipt page
    persona: member
    personality: hurried
`
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, `Workflow "checkout" pins the personality "hurried", which is not a built in personality.`)
}

// A valid pin is accepted and reaches the typed workflow, so the resolver can
// honor it.
func TestParse_AcceptsAValidWorkflowPersonalityPin(t *testing.T) {
	t.Parallel()
	body := withWorkflow + "diversity:\n  enabled: true\n"
	m := mustParse(t, body)
	require.Equal(t, "member", m.Workflows[0].Persona)

	pinned := mustParse(t, `
version: 1
name: shop
services:
  - name: web
    port: 3000
personas:
  - name: member
workflows:
  - name: checkout
    description: buy one item and reach the receipt page
    persona: member
    personality: skeptic
`)
	require.Equal(t, "skeptic", pinned.Workflows[0].Personality)
}
