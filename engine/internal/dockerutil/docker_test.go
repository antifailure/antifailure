package dockerutil_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
)

// The image every test here runs. Small, present on most machines already,
// and it exits immediately, so nothing is left running if a test fails
// between create and remove.
const testImage = "alpine:3.20"

// asked and skipped count how many tests wanted the daemon and how many did
// not get it. TestMain reads them.
var asked, skipped atomic.Int64

// silentFullSkip reports whether a run proved nothing and said so quietly.
//
// A pure function so it can be tested, which a TestMain cannot be. The
// condition is narrow on purpose: some tests skipping is a machine being
// slow on one operation, and no tests asking is a package with nothing to
// skip. Every test asking and every one skipping is the case where `ok` is a
// lie, and it is the only case that fails.
func silentFullSkip(asked, skipped int64, optedOut bool) bool {
	return !optedOut && asked > 0 && skipped == asked
}

// TestMain refuses a run in which every test that needed the daemon skipped.
//
// requireDaemon skips one test at a time, and a package where every one of
// them skipped still prints ok. This package holds the ownership rule the leak
// detector rests on: whether a resource carries our label decides whether
// teardown may delete it, and a green check that never ran is worse than a red
// one. Set AF_SKIP_DOCKER to say out loud that this machine has no daemon.
func TestMain(m *testing.M) {
	code := m.Run()

	// Both checks live here because a package gets one TestMain. Leaks first:
	// a goroutine left behind by a Docker client is the failure this package
	// is most likely to introduce, and it only shows up once everything has
	// finished.
	if code == 0 {
		if err := goleak.Find(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}

	a, s := asked.Load(), skipped.Load()
	if a > 0 {
		fmt.Fprintf(os.Stderr, "dockerutil: %d of %d daemon tests ran\n", a-s, a)
	}
	if code == 0 && silentFullSkip(a, s, os.Getenv("AF_SKIP_DOCKER") != "") {
		fmt.Fprintf(os.Stderr,
			"dockerutil: every one of the %d daemon tests skipped, so this package "+
				"proved nothing about resource ownership. Start Docker, or set "+
				"AF_SKIP_DOCKER=1 to accept that.\n", a)
		code = 1
	}
	os.Exit(code)
}

func requireDaemon(t *testing.T) *client.Client {
	t.Helper()
	asked.Add(1)
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		skipped.Add(1)
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	cli, err := dockerutil.Client()
	if err != nil {
		skipped.Add(1)
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		skipped.Add(1)
		t.Skipf("skipped: the Docker daemon did not respond: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer pullCancel()
	if _, err := cli.ImageInspect(pullCtx, testImage); err != nil {
		rc, pullErr := cli.ImagePull(pullCtx, testImage, image.PullOptions{})
		if pullErr != nil {
			skipped.Add(1)
			t.Skipf("skipped: %s could not be pulled: %v", testImage, pullErr)
		}
		dockerutil.Discard(rc)
	}
	return cli
}

// create makes a container with the given labels and guarantees its removal,
// including when the assertion under test is the thing that failed.
func create(t *testing.T, cli *client.Client, labels map[string]string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := cli.ContainerCreate(ctx,
		&container.Config{Image: testImage, Cmd: []string{"true"}, Labels: labels},
		&container.HostConfig{}, nil, nil, "")
	require.NoError(t, err)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = cli.ContainerRemove(c, resp.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
	})
	return resp.ID
}

// createRunning makes a container with a command of its own, for tests about
// waiting rather than about labels, and guarantees its removal.
func createRunning(t *testing.T, cli *client.Client, cmd []string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image: testImage, Cmd: cmd,
			Labels: dockerutil.Managed(dockerutil.KindService, "await-exit", time.Now()),
		},
		&container.HostConfig{}, nil, nil, "")
	require.NoError(t, err)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = cli.ContainerRemove(c, resp.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
	})
	return resp.ID
}

func TestRemoveContainer_RemovesOurOwn(t *testing.T) {
	cli := requireDaemon(t)
	ctx := context.Background()

	id := create(t, cli, dockerutil.Managed(dockerutil.KindService, "env-remove", time.Now()))
	require.NoError(t, dockerutil.RemoveContainer(ctx, cli, id))

	_, err := cli.ContainerInspect(ctx, id)
	require.True(t, cerrdefs.IsNotFound(err), "the container is gone")
}

func TestRemoveContainer_RefusesAContainerThatIsNotOurs(t *testing.T) {
	cli := requireDaemon(t)
	ctx := context.Background()

	// The shape of a developer's own container. Removing this is the failure
	// the label check exists to prevent, and it is unrecoverable when it
	// happens, so the refusal is loud rather than silent.
	id := create(t, cli, map[string]string{"com.docker.compose.project": "their-app"})

	err := dockerutil.RemoveContainer(ctx, cli, id)
	require.Error(t, err)
	require.True(t, errors.Is(err, dockerutil.ErrNotOurs))
	require.Contains(t, err.Error(), dockerutil.ShortID(id))

	_, inspectErr := cli.ContainerInspect(ctx, id)
	require.NoError(t, inspectErr, "the container somebody else owns is untouched")
}

func TestRemoveContainer_TreatsAlreadyGoneAsDone(t *testing.T) {
	cli := requireDaemon(t)
	ctx := context.Background()
	// Teardown runs after crashes, so "already gone" is the normal case. If it
	// were an error, every second teardown would report a failure.
	require.NoError(t, dockerutil.RemoveContainer(ctx, cli,
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
}

func TestFilter_FindsOnlyWhatItNames(t *testing.T) {
	cli := requireDaemon(t)
	ctx := context.Background()

	mine := create(t, cli, dockerutil.Managed(dockerutil.KindService, "env-filter-a", time.Now()))
	other := create(t, cli, dockerutil.Managed(dockerutil.KindService, "env-filter-b", time.Now()))
	theirs := create(t, cli, map[string]string{"dev.antifailure.env": "env-filter-a"})

	list, err := cli.ContainerList(ctx, container.ListOptions{
		All: true, Filters: dockerutil.EnvFilter("env-filter-a"),
	})
	require.NoError(t, err)

	var ids []string
	for _, c := range list {
		ids = append(ids, c.ID)
	}
	require.Contains(t, ids, mine)
	require.NotContains(t, ids, other, "another environment's container is not matched")
	require.NotContains(t, ids, theirs,
		"an env label without the managed label is not a claim of ownership")
}

func TestDiscard_DrainsAStreamAndSurvivesANilOne(t *testing.T) {
	t.Parallel()
	// Closing a Docker stream without reading it leaves the daemon holding the
	// operation open, which surfaces much later as a hang somewhere else.
	r := &countingReader{Reader: strings.NewReader(strings.Repeat("x", 4096))}
	dockerutil.Discard(r)
	require.True(t, r.closed)
	require.Zero(t, r.Len(), "the stream is drained, not just closed")

	dockerutil.Discard(nil)
}

type countingReader struct {
	*strings.Reader
	closed bool
}

func (c *countingReader) Close() error { c.closed = true; return nil }

var _ io.ReadCloser = (*countingReader)(nil)

func TestFilter_FindsAResourceStampedByAnOlderRelease(t *testing.T) {
	cli := requireDaemon(t)
	ctx := context.Background()

	// The value is deliberately not the one this release stamps. Ownership is
	// decided by the label being present, so the leak detector keeps working
	// across a change to what goes in it. A filter that matched on the value
	// would go blind on exactly the resources nobody is watching any more.
	id := create(t, cli, map[string]string{
		dockerutil.LabelManaged: "some-future-value",
		dockerutil.LabelKind:    dockerutil.KindService,
		dockerutil.LabelEnv:     "env-legacy",
	})

	list, err := cli.ContainerList(ctx, container.ListOptions{
		All: true, Filters: dockerutil.EnvFilter("env-legacy"),
	})
	require.NoError(t, err)
	var ids []string
	for _, c := range list {
		ids = append(ids, c.ID)
	}
	require.Contains(t, ids, id)
	require.NoError(t, dockerutil.RemoveContainer(ctx, cli, id),
		"and it can be removed, which is the point of finding it")
}

// The network and volume halves of the same invariant.
//
// RemoveContainer has refused a container it does not own since it was written.
// Its two counterparts did not exist, so callers that needed them reached for
// cli.NetworkRemove and cli.VolumeRemove, which remove whatever the name
// resolves to. The engine's journal replay was one of those callers.
//
// The volume case is the one that cannot be undone. A container and a network
// can be recreated from a manifest; the data in somebody else's volume cannot
// be recreated from anything.

func createNetwork(t *testing.T, cli *client.Client, labels map[string]string) string {
	t.Helper()
	ctx := context.Background()
	res, err := cli.NetworkCreate(ctx, "af-test-net-"+strings.ToLower(t.Name()),
		network.CreateOptions{Labels: labels})
	require.NoError(t, err)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = cli.NetworkRemove(c, res.ID)
	})
	return res.ID
}

func createVolume(t *testing.T, cli *client.Client, labels map[string]string) string {
	t.Helper()
	ctx := context.Background()
	v, err := cli.VolumeCreate(ctx, volume.CreateOptions{
		Name: "af-test-vol-" + strings.ToLower(t.Name()), Labels: labels,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = cli.VolumeRemove(c, v.Name, true)
	})
	return v.Name
}

func TestRemoveNetwork_RemovesOurOwn(t *testing.T) {
	cli := requireDaemon(t)
	ctx := context.Background()

	id := createNetwork(t, cli, dockerutil.Managed(dockerutil.KindNetwork, "env-net", time.Now()))
	require.NoError(t, dockerutil.RemoveNetwork(ctx, cli, id))

	_, err := cli.NetworkInspect(ctx, id, network.InspectOptions{})
	require.True(t, cerrdefs.IsNotFound(err), "the network is gone")
}

func TestRemoveNetwork_RefusesANetworkThatIsNotOurs(t *testing.T) {
	cli := requireDaemon(t)
	ctx := context.Background()

	id := createNetwork(t, cli, map[string]string{"com.docker.compose.project": "their-app"})

	err := dockerutil.RemoveNetwork(ctx, cli, id)
	require.Error(t, err)
	require.True(t, errors.Is(err, dockerutil.ErrNotOurs))

	_, inspectErr := cli.NetworkInspect(ctx, id, network.InspectOptions{})
	require.NoError(t, inspectErr, "the network somebody else owns is untouched")
}

func TestRemoveVolume_RemovesOurOwn(t *testing.T) {
	cli := requireDaemon(t)
	ctx := context.Background()

	name := createVolume(t, cli, dockerutil.Managed(dockerutil.KindVolume, "env-vol", time.Now()))
	require.NoError(t, dockerutil.RemoveVolume(ctx, cli, name))

	_, err := cli.VolumeInspect(ctx, name)
	require.True(t, cerrdefs.IsNotFound(err), "the volume is gone")
}

func TestRemoveVolume_RefusesAVolumeThatIsNotOurs(t *testing.T) {
	cli := requireDaemon(t)
	ctx := context.Background()

	name := createVolume(t, cli, map[string]string{"com.docker.compose.project": "their-app"})

	err := dockerutil.RemoveVolume(ctx, cli, name)
	require.Error(t, err)
	require.True(t, errors.Is(err, dockerutil.ErrNotOurs))

	_, inspectErr := cli.VolumeInspect(ctx, name)
	require.NoError(t, inspectErr, "the data somebody else owns is untouched")
}

// A resource that is already gone is the ordinary state a compensating delete
// finds, and both must treat it as success or a journal record describing a
// resource that does not exist stays live forever.
func TestRemoveNetworkAndVolume_TreatAlreadyGoneAsSuccess(t *testing.T) {
	cli := requireDaemon(t)
	ctx := context.Background()

	require.NoError(t, dockerutil.RemoveNetwork(ctx, cli, "af-test-net-that-never-existed"))
	require.NoError(t, dockerutil.RemoveVolume(ctx, cli, "af-test-vol-that-never-existed"))
}

// The guard's own decision, checked directly. A TestMain cannot be tested, so
// the condition it acts on is a function that can be.
func TestSilentFullSkipOnlyFiresWhenNothingRan(t *testing.T) {
	cases := []struct {
		name           string
		asked, skipped int64
		optedOut       bool
		want           bool
	}{
		{"everything skipped", 5, 5, false, true},
		{"everything ran", 5, 0, false, false},
		{"some skipped", 5, 2, false, false},
		{"nothing asked", 0, 0, false, false},
		{"everything skipped, said out loud", 5, 5, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := silentFullSkip(c.asked, c.skipped, c.optedOut); got != c.want {
				t.Errorf("silentFullSkip(%d, %d, %v) = %v, want %v",
					c.asked, c.skipped, c.optedOut, got, c.want)
			}
		})
	}
}
