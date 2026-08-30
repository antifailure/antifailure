package docker_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
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

// Asking for a branch that was never made is not a missing golden.
//
// It reported one, and the report had a hole in it. A branch asked for by
// environment carries no From, and the not-found path named `b.From` in a
// message whose placeholder is the golden version, so the output read "The
// golden version  no longer exists" with nothing between the spaces, and the
// next step sent the reader to `af golden list` about a golden that was never
// involved. The reader had not run `af up` yet, which is the actual answer and
// the one thing the message did not say.
//
// Found by running `af mask plan` in a fresh checkout: the first thing anybody
// does with masking, and it could not be done.
func TestConnectingToABranchThatWasNeverMadeSaysToBringItUp(t *testing.T) {
	p, err := dockerdb.New(dockerdb.Options{Version: 17, Clock: clock.New(), PortFrom: 47400})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	defer func() { _ = p.Close() }()

	_, err = p.ConnString(context.Background(),
		provider.Branch{EnvID: "env_never_created_0001"}, provider.ConnDirect)
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB014),
		"a branch that does not exist is not a golden that does not exist, and the two "+
			"have different answers")
	require.NotErrorIs(t, err, aferrors.Coded(aferrors.AFDB004))

	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Contains(t, coded.Message(), "env_never_created_0001",
		"the environment is named, where the old message left a blank")
	require.NotContains(t, coded.Message(), "{",
		"an unfilled placeholder means the fields do not match the message")
	require.Contains(t, coded.NextStep(), "af up")
}
