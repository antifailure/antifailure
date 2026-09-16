package env_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
)

// baselineMeasured is the fail-closed rule that keeps a spuriously low base from
// reading as effects the change added. side_effect counts one class of effect
// from the message log and another from the decision log, so a base is a
// faithful measurement only when its workflows ran AND both logs were read. Any
// one failure means the base is undercounted, and an undercounted base compared
// against the candidate reports effects the change did not make, which is the
// exact "zero means did not measure" defect this family exists to avoid.
//
// The orderings enumerated: all good; workflows failed; the decision log unread;
// the message log unread. Each is one break, so a require that stops at the
// first failure cannot hide a later assertion.
func TestBaselineMeasured_FailsClosedUnlessTheBaseWasFaithfullyRead(t *testing.T) {
	t.Parallel()

	require.NoError(t, env.BaselineMeasuredForTest(nil, nil, nil),
		"workflows ran and both logs were read, so the base is a measurement")

	testErr := env.BaselineMeasuredForTest(errors.New("runner exited 1"), nil, nil)
	require.Error(t, testErr, "a base whose workflows did not run is not comparable")
	require.Contains(t, testErr.Error(), "workflows did not run")

	decErr := env.BaselineMeasuredForTest(nil, errors.New("sidecar unreachable"), nil)
	require.Error(t, decErr, "a base whose egress log could not be read is undercounted")
	require.Contains(t, decErr.Error(), "egress log could not be read")

	msgErr := env.BaselineMeasuredForTest(nil, nil, errors.New("sidecar unreachable"))
	require.Error(t, msgErr, "a base whose captured messages could not be read is undercounted")
	require.Contains(t, msgErr.Error(), "captured messages could not be read")
}

// ErrBaselineSameCommit is a distinct, checkable sentinel, so the collector can
// tell "the branch is level with its base" (a legitimate state, a note) from
// "the base could not be built" (a fact about our tooling, a different note).
func TestErrBaselineSameCommit_IsAStableSentinel(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, env.ErrBaselineSameCommit, env.ErrBaselineSameCommit)
	require.Contains(t, env.ErrBaselineSameCommit.Error(), "same commit")
}
