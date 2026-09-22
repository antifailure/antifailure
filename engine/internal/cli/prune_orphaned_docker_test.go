package cli

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// The chain --orphaned depends on, against the real daemon rather than a fake
// of it: the runtime's inventory reads each network's attachments off inspect,
// groupEnvironments turns that into an environment it calls orphaned only when
// nothing is on its networks, and the teardown af env prune --yes runs then
// removes both networks. A fake daemon would test that the runtime agrees with
// our idea of Docker; the fact this depends on, that the list endpoint reports
// no containers and only inspect does, is Docker's, so it is checked here.
//
// It creates two networks and one small container of its own, under an
// environment id nothing else uses, and removes exactly those. It never
// exhausts a pool and never touches another environment.
func TestOrphanedNetworksAreFoundAndRemovedOnTheRealDaemon(t *testing.T) {
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cli, err := dockerutil.Client()
	if err != nil {
		t.Skipf("skipped: no Docker daemon: %v", err)
	}
	// Cleanups rather than defers, registered first so they run last: a defer
	// runs before every t.Cleanup, and the teardown below dials through both
	// clients. Closing them first leaves its connections open past the test,
	// which the package's leak detector reports.
	t.Cleanup(func() { _ = cli.Close() })
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("skipped: the Docker daemon did not answer: %v", err)
	}
	if _, err := cli.ImageInspect(ctx, "busybox:1.36"); err != nil {
		t.Skip("skipped: busybox:1.36 is not on this daemon, and this test does not pull")
	}
	rt, err := local.New(local.Options{Clock: clock.New()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	envID := fmt.Sprintf("orphantest%d", time.Now().UnixNano()%1_000_000_000)
	countOurs := func() int {
		nets, err := cli.NetworkList(ctx, client.NetworkListOptions{Filters: dockerutil.EnvFilter(envID)})
		require.NoError(t, err)
		return len(nets.Items)
	}
	t.Cleanup(func() {
		// Whatever the assertions did, nothing of this test's survives it.
		c, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		_, _ = rt.Down(c, envID)
	})

	var ids []string
	for _, name := range []string{"af-net-" + envID, "af-edge-" + envID} {
		res, err := cli.NetworkCreate(ctx, name, client.NetworkCreateOptions{
			Driver: "bridge", Internal: true,
			Labels: dockerutil.Managed(dockerutil.KindNetwork, envID, time.Now().UTC()),
		})
		require.NoError(t, err)
		ids = append(ids, res.ID)
	}
	require.Equal(t, 2, countOurs())

	find := func() environment {
		items, err := rt.Inventory(ctx)
		require.NoError(t, err)
		for _, env := range groupEnvironments(items) {
			if env.ID == envID {
				return env
			}
		}
		t.Fatalf("the inventory did not report %s", envID)
		return environment{}
	}

	// A running container on one network: an environment in use.
	created, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: "busybox:1.36", Cmd: []string{"sleep", "300"},
			Labels: dockerutil.Managed(dockerutil.KindService, envID, time.Now().UTC()),
		},
		HostConfig: &container.HostConfig{NetworkMode: container.NetworkMode(ids[0])},
		Name:       "af-svc-" + envID,
	})
	require.NoError(t, err)
	_, err = cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{})
	require.NoError(t, err)

	inUse := find()
	require.Equal(t, 2, inUse.Networks)
	require.Equal(t, 1, inUse.Attached, "the attachment was not read off the daemon")
	require.False(t, inUse.orphaned(), "an environment with a container attached was called orphaned")

	// Gone, the way a killed run leaves it: networks and nothing on them.
	require.NoError(t, dockerutil.RemoveContainer(ctx, cli, created.ID))
	orphan := find()
	require.Zero(t, orphan.Uncounted)
	require.Zero(t, orphan.Attached)
	require.True(t, orphan.orphaned(), "two networks with nothing on them were not called orphaned")

	td, err := runtimePruner{rt: rt}.down(ctx, envID)
	require.NoError(t, err)
	require.Empty(t, td.Pending)
	require.Equal(t, 0, countOurs(), "the teardown --yes runs left a network behind")
}
