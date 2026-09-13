package cli

// The Docker check's API version floor.
//
// The engine moved from github.com/docker/docker to github.com/moby/moby/client,
// and the new client will not negotiate below its MinAPIVersion. The old one
// fell back as far as 1.24. So a machine that worked before can stop working
// after, and the only honest place to say that is the check that exists to
// say what is wrong with the machine. These tests hold both directions: a
// daemon below the floor fails and names its version, a daemon at the floor
// passes, and a version that could not be read is not reported as fine.

import (
	"context"
	"errors"
	"testing"

	dockerclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

func dockerOnPath(api string, apiErr error) fakeProber {
	return fakeProber{
		lookPath:      map[string]string{"docker": "/usr/bin/docker"},
		dockerVersion: "18.09.9", dockerOS: "linux",
		dockerAPI: api, dockerAPIErr: apiErr,
	}
}

func TestCheckDocker_ADaemonBelowTheClientsFloorFailsAndNamesItsVersion(t *testing.T) {
	t.Parallel()
	r := checkDocker(context.Background(), nil, dockerOnPath("1.39", nil))
	require.Equal(t, CheckFail, r.Status,
		"a daemon the client cannot negotiate with must not read as a working Docker")
	require.Contains(t, r.Detail, "1.39", "the version found is what the reader needs to act")
	require.Contains(t, r.Detail, dockerclient.MinAPIVersion, "and so is the version it has to reach")
	require.Contains(t, r.Remediation, dockerclient.MinAPIVersion)
}

func TestCheckDocker_ADaemonAtTheFloorPasses(t *testing.T) {
	t.Parallel()
	r := checkDocker(context.Background(), nil, dockerOnPath(dockerclient.MinAPIVersion, nil))
	require.Equal(t, CheckPass, r.Status, "the floor is inclusive: the client negotiates down to it")
	require.Equal(t, "version 18.09.9, linux containers", r.Detail)
}

func TestCheckDocker_ACurrentDaemonPasses(t *testing.T) {
	t.Parallel()
	r := checkDocker(context.Background(), nil, dockerOnPath("1.51", nil))
	require.Equal(t, CheckPass, r.Status)
}

// A comparison of dotted versions as strings would put 1.100 below 1.40, and
// the day Docker's API reaches three digit minors every current daemon would
// start failing this check.
func TestCheckDocker_TheFloorComparesNumbersNotStrings(t *testing.T) {
	t.Parallel()
	r := checkDocker(context.Background(), nil, dockerOnPath("1.100", nil))
	require.Equal(t, CheckPass, r.Status)
}

func TestCheckDocker_AnUnreadableAPIVersionIsNotReportedAsFine(t *testing.T) {
	t.Parallel()
	r := checkDocker(context.Background(), nil,
		dockerOnPath("", errors.New("docker version: exit status 1")))
	require.Equal(t, CheckWarn, r.Status,
		"a check that could not look must say so rather than pass")
	require.Contains(t, r.Detail, "not checked")
	require.Contains(t, r.Remediation, dockerclient.MinAPIVersion)
}

// af start reads the same check, so the floor has to reach its Docker rung
// too, and in the right direction each way: below the floor blocks and points
// at af doctor, and a version that merely could not be read does not stop a
// daemon that answered.
func TestStart_ADaemonBelowTheFloorBlocksTheDockerRung(t *testing.T) {
	t.Parallel()
	p := startProbeFor(t, t.TempDir())
	p.Prober = dockerOnPath("1.39", nil)
	s := dockerState(context.Background(), nil, p)
	require.Equal(t, StageBlocked, s.state)
	require.Equal(t, "af doctor", s.command)
	require.Contains(t, s.detail, "1.39")
}

func TestStart_AnUnreadableAPIVersionDoesNotBlockADaemonThatAnswered(t *testing.T) {
	t.Parallel()
	p := startProbeFor(t, t.TempDir())
	p.Prober = dockerOnPath("", errors.New("docker version: exit status 1"))
	s := dockerState(context.Background(), nil, p)
	require.Equal(t, StageDone, s.state)
	require.Contains(t, s.detail, "not checked", "the note survives into the rung")
}
