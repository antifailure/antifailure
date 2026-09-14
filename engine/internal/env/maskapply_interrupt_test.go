package env

// AN INTERRUPTED af mask apply SAYS THE BRANCH HAS TO BE RECREATED.
//
// The first version of AF-MSK-016 told every interrupted run to run again from
// the beginning. That is right for a refresh, which copies the source into a
// fresh candidate. It is wrong for af mask apply: the branch keeps the chunks
// that committed, and masking it again masks those rows a second time, so a
// value masked twice stops matching the same value masked once in every other
// table and every join across them breaks without an error.

import (
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/masking"
)

func TestApplyFailure_AnInterruptedApplySaysToRecreateTheBranch(t *testing.T) {
	t.Parallel()
	err := applyFailure(&masking.InterruptedError{Table: "public.customers", Rows: 6})

	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded), "an interrupted apply returned an error with no code: %v", err)
	require.Equal(t, "AF-MSK-016", string(coded.Code()))
	require.Contains(t, coded.Message(), "recreate it before masking again",
		"an interrupted apply does not say the partly masked branch has to be recreated")
	require.NotContains(t, coded.Message(), "fresh copy of the source",
		"an interrupted apply was given the refresh's advice, which masks a partly masked branch twice")
	require.NotContains(t, coded.NextStep(), "Run the same command again",
		"the catalog still tells an interrupted run to repeat itself on the same copy")
}
