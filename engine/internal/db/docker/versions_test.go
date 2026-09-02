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

// Postgres 18 has been the current release for a year, and a project whose
// production is on it could not say so.
//
// Setting database.version to 18 was refused with AF-DB-003, whose next step
// was "Use a provider that supports Postgres 18, or upgrade the source". No
// provider in this build supported 18, so the first half named nothing, and
// the second half is the wrong direction: a source is not upgraded to reach a
// version older than the one it is already on. Leaving it at 17 instead copied
// an 18 source into a 17 golden without saying so, which is an environment
// running a Postgres the application does not.
//
// A golden here is the stock postgres image with the data committed into it,
// so the set this provider handles is the registry's rather than its own.
func TestDockerProvider_HandlesEveryMajorTheImageIsPublishedFor(t *testing.T) {
	p, err := dockerdb.New(dockerdb.Options{Version: 17, Clock: clock.New(), PortFrom: 47400})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	defer func() { _ = p.Close() }()

	for _, major := range []int{14, 15, 16, 17, 18} {
		require.True(t, p.Capabilities().Supports(major),
			"a project on Postgres %d cannot say so", major)
	}
}

// The refusal for a version this provider really cannot build has to name an
// edit somebody can make.
func TestDockerProvider_AnUnbuildableVersionNamesTheOnesItCanBuild(t *testing.T) {
	p, err := dockerdb.New(dockerdb.Options{Version: 17, Clock: clock.New(), PortFrom: 47410})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	defer func() { _ = p.Close() }()

	_, err = p.RefreshGolden(context.Background(), provider.GoldenSpec{Version: 13})
	require.Error(t, err)

	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Equal(t, aferrors.AFDB003, coded.Code())
	require.Contains(t, coded.NextStep(), "database.version",
		"the reader has to be told which key to edit")
	require.Contains(t, coded.NextStep(), "18",
		"and what the highest value it may hold is")
	require.NotContains(t, coded.NextStep(), "upgrade the source",
		"a source is never upgraded to reach an older major")
}
