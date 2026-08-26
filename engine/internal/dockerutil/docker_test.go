package dockerutil_test

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
)

// The image every test here runs. Small, present on most machines already,
// and it exits immediately, so nothing is left running if a test fails
// between create and remove.
const testImage = "alpine:3.20"

func requireDaemon(t *testing.T) *client.Client {
	t.Helper()
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	cli, err := dockerutil.Client()
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		t.Skipf("skipped: the Docker daemon did not respond: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer pullCancel()
	if _, _, err := cli.ImageInspectWithRaw(pullCtx, testImage); err != nil {
		rc, pullErr := cli.ImagePull(pullCtx, testImage, image.PullOptions{})
		if pullErr != nil {
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

func TestRemoveContainer_RemovesOurOwn(t *testing.T) {
	cli := requireDaemon(t)
	ctx := context.Background()

	id := create(t, cli, dockerutil.Managed(dockerutil.KindService, "env-remove", time.Now()))
	require.NoError(t, dockerutil.RemoveContainer(ctx, cli, id))

	_, err := cli.ContainerInspect(ctx, id)
	require.True(t, client.IsErrNotFound(err), "the container is gone")
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
