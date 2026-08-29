package docker_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// Branching a version that is not there says what IS there.
//
// For a person that is the difference between "gone" and knowing what they
// could have asked for. It is also the evidence the suite's intermittent
// "the golden version no longer exists" has never carried: a listing taken at
// the instant it happened, so the next occurrence is a bug with data attached
// rather than a third guess.
func TestBranchingAMissingGoldenNamesTheOnesThatExist(t *testing.T) {
	p, err := dockerdb.New(dockerdb.Options{Version: 17, Clock: clock.New(), PortFrom: 47300})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	defer func() { _ = p.Close() }()

	_, err = p.Branch(context.Background(), "gv_19700101000000_deadbeef", "env_missing00001")
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB004),
		"still the coded error, so callers matching on it keep working")
	require.Contains(t, err.Error(), "Available:",
		"and it says what exists, whether that is a list or a statement that there are none")
	require.NotContains(t, err.Error(), "{",
		"no unfilled placeholder reaches a reader")

}
