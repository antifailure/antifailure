package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/fidelity"
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
