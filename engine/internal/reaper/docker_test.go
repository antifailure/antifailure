package reaper_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/reaper"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// A reaper that has never destroyed anything is not a reaper.
//
// Everything else in this package tests the predicate against a slice of
// structs. This one puts real containers and a real network on a real daemon,
// runs the real inventory over them, and watches the real teardown remove
// them, because the failure mode this repository has shipped before is a
// sweeper that reported success and removed nothing: every part in place, no
// call site, and a green test suite over the parts.
//
// It is deliberately run against whatever else is on the daemon rather than a
// daemon of its own. The claim that matters most for a destructive sweep is
// the one about what it leaves alone, and the honest way to test it is with
// other people's containers actually present.

const costctl = "af-costctl-"

func dockerRuntime(t *testing.T) *local.Runtime {
	t.Helper()
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	r, err := local.New(local.Options{Clock: clock.New()})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := r.Inventory(ctx); err != nil {
		_ = r.Close()
		t.Skipf("skipped: the Docker daemon did not respond: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// makeEnv creates one real environment: a network and a container, labelled
// exactly as the runtime labels them, with a stated expiry.
//
// Cleanup removes it whatever the test does, including when the assertion
// under test is the thing that failed, so a failing run does not leave
// containers on a shared machine.
func makeEnv(t *testing.T, r *local.Runtime, envID string, expires time.Time) {
	t.Helper()
	ctx := context.Background()
	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	t.Cleanup(func() {
		down, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = r.Down(down, envID)
	})

	now := time.Now().UTC()
	netLabels := dockerutil.ManagedUntil(dockerutil.KindNetwork, envID, now, expires)
	_, err = cli.NetworkCreate(ctx, envID+"-net", network.CreateOptions{
		Driver: "bridge", Labels: netLabels,
	})
	require.NoError(t, err)

	cLabels := dockerutil.ManagedUntil(dockerutil.KindService, envID, now, expires)
	cLabels[dockerutil.LabelService] = "app"
	created, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image:  "busybox:latest",
			Cmd:    []string{"sleep", "600"},
			Labels: cLabels,
		}, &container.HostConfig{}, nil, nil, envID+"-app")
	require.NoError(t, err)
	require.NoError(t, cli.ContainerStart(ctx, created.ID, container.StartOptions{}))
}

func ensureBusybox(t *testing.T) {
	t.Helper()
	cli, err := dockerutil.Client()
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := cli.ImageInspect(ctx, "busybox:latest"); err == nil {
		return
	}
	rc, err := cli.ImagePull(ctx, "busybox:latest", imagePullOptions())
	if err != nil {
		t.Skipf("skipped: busybox could not be pulled: %v", err)
	}
	defer func() { _ = rc.Close() }()
	_, _ = readAll(rc)
}

// envIDs is what the inventory attributes to each environment, for assertions
// about what is and is not still on the daemon.
func envIDs(t *testing.T, r *local.Runtime) map[string]int {
	t.Helper()
	inv, err := r.Inventory(context.Background())
	require.NoError(t, err)
	out := map[string]int{}
	for _, res := range inv {
		if res.EnvID != "" {
			out[res.EnvID]++
		}
	}
	return out
}

// downer destroys through the real runtime.
type downer struct{ r *local.Runtime }

func (d downer) Destroy(ctx context.Context, envID string) (int, error) {
	td, err := d.r.Down(ctx, envID)
	return td.Removed, err
}

func TestReaper_DestroysAnExpiredEnvironmentOnARealDaemon(t *testing.T) {
	// Not parallel: it asserts about the state of one shared daemon.
	r := dockerRuntime(t)
	ensureBusybox(t)

	const (
		expired = costctl + "expired"
		live    = costctl + "live"
	)
	now := time.Now().UTC()
	// One environment an hour past its lifetime, and one with six hours left.
	makeEnv(t, r, expired, now.Add(-time.Hour))
	makeEnv(t, r, live, now.Add(6*time.Hour))

	before := envIDs(t, r)
	require.Positive(t, before[expired], "the expired environment was not created")
	require.Positive(t, before[live], "the live environment was not created")
	t.Logf("daemon holds %d environments before the sweep: %v", len(before), keys(before))

	inv, err := r.Inventory(context.Background())
	require.NoError(t, err)

	// The plan, over the whole daemon, including everything anybody else has
	// running on it right now.
	plan := reaper.Plan(inv, nil, now)
	var named []string
	for _, e := range plan {
		named = append(named, e.EnvID)
	}
	t.Logf("plan names: %v", named)

	// The assertion that makes it safe to destroy: the plan names our expired
	// environment and nothing else at all. Every other environment on this
	// daemon, including ones this test did not create, is left out of it.
	require.Equal(t, []string{expired}, named,
		"the sweep planned to destroy something it did not state a lifetime for")

	result := reaper.Sweep(context.Background(), inv, nil, now, downer{r})
	require.Equal(t, 1, len(result.Outcomes))
	require.NoError(t, result.Outcomes[0].Err)
	require.Positive(t, result.Removed(), "the sweep reported removing nothing")
	t.Logf("sweep scanned %d environments, removed %d resources from %s",
		result.Scanned, result.Removed(), result.Outcomes[0].EnvID)

	// And the daemon agrees. This is the part a sweeper that deleted zero rows
	// forever would fail.
	after := envIDs(t, r)
	require.Zero(t, after[expired], "the expired environment is still on the daemon")
	require.Positive(t, after[live], "the sweep destroyed an environment inside its lifetime")
	t.Logf("daemon holds %d environments after the sweep: %v", len(after), keys(after))

	// Nothing else went. Checked over the whole daemon rather than over the
	// two environments this test made, because a sweep that took a bystander
	// is the failure that matters and our own two cannot show it.
	//
	// Presence and not resource count. This daemon is shared and busy: another
	// agent starting or stopping a container of their own between the two
	// inventories changes their count for reasons that have nothing to do with
	// this sweep, and asserting the counts equal makes the test fail on
	// somebody else's unrelated work. Disappearing entirely is the thing a
	// sweep does and the thing this asserts against.
	for id, n := range before {
		if id == expired {
			continue
		}
		require.Positive(t, after[id],
			"the sweep removed %s, which it was never asked to touch", id)
		if after[id] != n {
			t.Logf("bystander %s went from %d to %d resources while the sweep ran, "+
				"which is another agent's work on this shared daemon and not this sweep: "+
				"the sweep destroyed only %v", id, n, after[id], named)
		}
	}

	// The sweep's own record of what it destroyed, which is the authoritative
	// one: it names exactly one environment, and it is ours.
	var destroyed []string
	for _, out := range result.Outcomes {
		destroyed = append(destroyed, out.EnvID)
	}
	require.Equal(t, []string{expired}, destroyed)
}

func TestReaper_LeavesAnEnvironmentThatStatesNoLifetime(t *testing.T) {
	r := dockerRuntime(t)
	ensureBusybox(t)

	// Created the way a release before this feature created them: the full
	// managed label set, and no expiry.
	const legacy = costctl + "legacy"
	makeEnvUnstamped(t, r, legacy)

	inv, err := r.Inventory(context.Background())
	require.NoError(t, err)
	require.Positive(t, countFor(inv, legacy), "the environment was not created")

	// A year later. Reading "states no lifetime" as "lifetime already over"
	// would turn an upgrade into a machine wipe.
	plan := reaper.Plan(inv, nil, time.Now().UTC().Add(365*24*time.Hour))
	for _, e := range plan {
		require.NotEqual(t, legacy, e.EnvID,
			"the sweep planned to destroy an environment that stated no lifetime")
	}

	after, err := r.Inventory(context.Background())
	require.NoError(t, err)
	require.Positive(t, countFor(after, legacy))
	t.Logf("%s survived a sweep dated a year in the future", legacy)
}

func makeEnvUnstamped(t *testing.T, r *local.Runtime, envID string) {
	t.Helper()
	ctx := context.Background()
	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	t.Cleanup(func() {
		down, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = r.Down(down, envID)
	})

	now := time.Now().UTC()
	_, err = cli.NetworkCreate(ctx, envID+"-net", network.CreateOptions{
		Driver: "bridge",
		Labels: dockerutil.Managed(dockerutil.KindNetwork, envID, now),
	})
	require.NoError(t, err)
}

func countFor(inv []provider.Resource, envID string) int {
	n := 0
	for _, res := range inv {
		if res.EnvID == envID {
			n++
		}
	}
	return n
}

func keys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func imagePullOptions() image.PullOptions { return image.PullOptions{} }

func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			if strings.Contains(err.Error(), "EOF") {
				return buf, nil
			}
			return buf, err
		}
	}
}
