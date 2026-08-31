package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The two failures carry different codes and different exit codes on purpose.
// A dimension measured and found wanting is a fact about the environment; a
// dimension nothing could measure is a fact about what we could see, and
// reporting the second as the first is how a check stops being believed.
func TestRequirementError_TellsTheTwoFailuresApart(t *testing.T) {
	t.Parallel()

	require.NoError(t, requirementError(nil))
	require.NoError(t, requirementError([]fidelity.Requirement{
		{Dimension: schema.FidelityDatabase, Met: true, Measurable: true},
	}))

	unmet := codedError(t, requirementError([]fidelity.Requirement{
		{Dimension: schema.FidelityServices, Measurable: true, Because: "worker is absent"},
	}))
	require.Equal(t, aferrors.AFFID001, unmet.Code())
	require.Equal(t, aferrors.ExitCode(6), unmet.ExitCode(),
		"an unmet requirement is a policy denial")
	require.Contains(t, unmet.Error(), "worker is absent")
	require.Contains(t, unmet.Error(), "services")

	unmeasurable := codedError(t, requirementError([]fidelity.Requirement{
		{Dimension: schema.FidelityTraffic, Because: "the manifest does not ask for traffic"},
	}))
	require.Equal(t, aferrors.AFFID002, unmeasurable.Code())
	require.Contains(t, unmeasurable.Error(), "neither met nor broken")
}

// An unmeasurable requirement outranks an unmet one. Somebody whose gate
// cannot be evaluated needs to know that before they are told which component
// is missing, because until it can be evaluated the other answer is not the
// whole answer.
func TestRequirementError_ReportsTheUnmeasurableOneFirst(t *testing.T) {
	t.Parallel()
	err := codedError(t, requirementError([]fidelity.Requirement{
		{Dimension: schema.FidelityServices, Measurable: true, Because: "worker is absent"},
		{Dimension: schema.FidelityTraffic, Because: "the manifest does not ask for traffic"},
	}))
	require.Equal(t, aferrors.AFFID002, err.Code())
}

func codedError(t *testing.T, err error) *aferrors.Error {
	t.Helper()
	require.Error(t, err)
	coded, ok := err.(*aferrors.Error)
	require.True(t, ok, "the failure carries no catalogue code: %v", err)
	return coded
}

// The JSON says null and never zero when nothing could be measured.
//
// This is the whole discipline at the one boundary where it is easiest to
// lose. A consumer reading percent out of the document gets a number or it
// gets nothing; if an unmeasurable environment serialised as 0 it would be
// indistinguishable from an environment that reproduces none of production,
// and the two are opposite facts. The field carries no omitempty for the same
// reason: a missing key is something a careless reader defaults to zero.
func TestFidelityJSON_SaysNullRatherThanZeroWhenNothingWasMeasured(t *testing.T) {
	t.Parallel()

	nothing := fidelity.Build(fidelity.Observation{})
	out := FidelityJSON{Inventory: nothing, Score: nothing.Score()}
	require.Zero(t, out.Score.Counted)
	if pct, ok := out.Score.Percent(); ok {
		out.Percent = &pct
	}
	body, err := json.Marshal(out)
	require.NoError(t, err)
	require.Contains(t, string(body), `"percent":null`)
	require.NotContains(t, string(body), `"percent":0`)

	// And a real number when there was something to count, so the null above
	// is the unmeasurable case rather than the field never being filled.
	measured := fidelity.Build(fidelity.Observation{
		Manifest: &schema.Manifest{Services: []schema.Service{{Name: "web"}}},
		Running:  []provider.RunningService{{Name: "web", Ready: true}},
	})
	scored := FidelityJSON{Inventory: measured, Score: measured.Score()}
	pct, ok := scored.Score.Percent()
	require.True(t, ok)
	scored.Percent = &pct
	body, err = json.Marshal(scored)
	require.NoError(t, err)
	require.Contains(t, string(body), `"percent":100`)
}
