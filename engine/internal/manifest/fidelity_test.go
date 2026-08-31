package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func TestParse_FidelityIsOnWithNothingRequired(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal)
	require.NotNil(t, m.Fidelity)
	require.NotNil(t, m.Fidelity.Enabled)
	require.True(t, *m.Fidelity.Enabled, "the inventory must be on unless somebody turns it off")
	require.Empty(t, m.Fidelity.Require)
}

func TestParse_FidelityRequireIsReadAsNamedDimensions(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`
fidelity:
  require: [database, third_party]
`)
	require.Equal(t,
		[]schema.FidelityDimension{schema.FidelityDatabase, schema.FidelityThirdParty},
		m.Fidelity.Require)
}

// A requirement nothing evaluates reads in review as a gate that is enforced,
// which is worse than no gate at all.
func TestParse_RejectsARequirementNothingWouldMeasure(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`
fidelity:
  enabled: false
  require: [database]
`)
	ps := problems(t, err)
	require.Contains(t, messages(ps), "disabled")
	require.Contains(t, messages(ps), "fidelity.require")
}

func TestParse_RejectsADimensionRequiredTwice(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`
fidelity:
  require: [database, database]
`)
	ps := problems(t, err)
	require.Contains(t, messages(ps), "required twice")
}

func TestParse_RejectsADimensionThatIsNotOne(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`
fidelity:
  require: [databse]
`)
	require.Error(t, err, "a misspelled dimension must not be accepted as a requirement")
}

func TestParse_RejectsAnUnknownKeyUnderFidelity(t *testing.T) {
	t.Parallel()
	_, err := parse(t, minimal+`
fidelity:
  requires: [database]
`)
	ps := problems(t, err)
	require.Contains(t, messages(ps), "requires")
	require.Contains(t, messages(ps), "require", "a near miss must be suggested")
}
