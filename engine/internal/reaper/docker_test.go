package reaper_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
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
// structs. This puts real containers and a real network on a real daemon, runs
// the real inventory over them, and watches the real teardown remove them,
// because the failure mode this repository has shipped before is a sweeper that
// reported success and removed nothing: every part in place, no call site, and
// a green test suite over the parts.
//
// It runs against whatever else is on the daemon rather than a daemon of its
// own, because the claim that matters most for a destructive sweep is the one
// about what it leaves alone. That is also why it used to fail intermittently,
// and the way it failed is worth keeping in mind before changing any assertion
// here.
//
// The test used to assert things about environments it did not own. It
// required every environment present before the sweep to be present after it,
// and required the plan over the whole daemon to name its own expired
// environment and nothing else. On a daemon other tests are using at the same
// time, which is every CI run of `go test ./internal/...`, neither is true: the
// env and local runtime suites create and tear down environments of their own
// between this test's two inventories, and any of them whose lifetime has
// passed is correctly named by the plan. editioncheck reported the package
// COULD-NOT-LOOK on #394 because the whole suite failed it and the reaper alone
// passed it twice. It also meant this test swept the whole daemon through the
// real teardown, so an expired fixture belonging to another test would have
// been destroyed in the middle of that test.
//
// So the assertions are now about what this run can know. The plan is still
// read over the whole daemon, and everything it names must state a lifetime
// that has passed, checked against the inventory independently of the
// predicate. The sweep still runs over the whole daemon, through a destroyer
// that reaches the real teardown only for environments this run created, and
// the test requires that its own expired environment is the only one that
// reached it. An environment that disappears while the sweep runs is logged,
// never blamed on the sweep, because this run cannot tell its owner's teardown
// from anything else and the guard already proves the sweep did not do it.

const costctl = "af-costctl-"

// runs makes every invocation's prefix distinct even within one process, where
// the pid and a nanosecond clock can both repeat.
var runs atomic.Int64

// run is one invocation's claim on the shared daemon. Every environment it
// creates is named under a prefix no other invocation can share, so two runs of
// this test on one machine, which is ordinary here, cannot create the same
// container name or sweep each other's fixtures.
type run struct{ prefix string }

func newRun() run {
	return run{prefix: fmt.Sprintf("%s%d-%d-%d-", costctl, os.Getpid(), time.Now().UnixNano(), runs.Add(1))}
}

func (o run) owns(envID string) bool { return strings.HasPrefix(envID, o.prefix) }

// otherEnvID names an environment standing in for another test's, under a
// prefix no run owns.
func otherEnvID() string {
	return fmt.Sprintf("af-other-%d-%d-%d", os.Getpid(), time.Now().UnixNano(), runs.Add(1))
}

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
	return countByEnv(inv)
}

func countByEnv(inv []provider.Resource) map[string]int {
	out := map[string]int{}
	for _, res := range inv {
		if res.EnvID != "" {
			out[res.EnvID]++
		}
	}
	return out
}

// latestStatedExpiry reads an environment's lifetime off the inventory without
// going through the predicate under test, so that "everything the plan named
// had expired" is checked by something other than the plan.
func latestStatedExpiry(inv []provider.Resource, envID string) (time.Time, bool) {
	var latest time.Time
	stated := false
	for _, res := range inv {
		if res.EnvID != envID {
			continue
		}
		secs, err := strconv.ParseInt(res.Labels["expires"], 10, 64)
		if err != nil {
			continue
		}
		if at := time.Unix(secs, 0); !stated || at.After(latest) {
			latest, stated = at, true
		}
	}
	return latest, stated
}

// errNotThisRun is what the guard answers for an environment another run
// created.
var errNotThisRun = errors.New("left for its owner: this run did not create it")

// guarded destroys through the real runtime, and only what this run created.
//
// The reaper is right to remove another run's expired environment, and in a
// test that environment is another test's fixture, destroyed in the middle of
// whatever that test is asserting. So the sweep here reaches the real teardown
// for this run's own environments and is refused for everything else, and
// `reached` is the record of what it actually destroyed. `during`, when set,
// runs before each environment the sweep takes on, which is how a test puts
// somebody else's teardown in the middle of this sweep on purpose.
type guarded struct {
	r       *local.Runtime
	own     run
	during  func(envID string)
	reached []string
}

func (g *guarded) Destroy(ctx context.Context, envID string) (int, error) {
	if g.during != nil {
		g.during(envID)
	}
	if !g.own.owns(envID) {
		return 0, errNotThisRun
	}
	g.reached = append(g.reached, envID)
	td, err := g.r.Down(ctx, envID)
	return td.Removed, err
}

// sweepAndCheck creates this run's expired and live environments, plans and
// sweeps over the whole daemon, and asserts only what this run can know.
func sweepAndCheck(t *testing.T, r *local.Runtime, own run, during func(envID string)) {
	expired := own.prefix + "expired"
	live := own.prefix + "live"
	now := time.Now().UTC()
	// One environment an hour past its lifetime, and one with six hours left.
	makeEnv(t, r, expired, now.Add(-time.Hour))
	makeEnv(t, r, live, now.Add(6*time.Hour))

	inv, err := r.Inventory(context.Background())
	require.NoError(t, err)
	before := countByEnv(inv)
	require.Positive(t, before[expired], "the expired environment was not created")
	require.Positive(t, before[live], "the live environment was not created")
	t.Logf("daemon holds %d environments before the sweep", len(before))

	// The plan, over the whole daemon, including everything anybody else has
	// running on it right now.
	plan := reaper.Plan(inv, nil, now)
	var named []string
	for _, e := range plan {
		named = append(named, e.EnvID)
	}
	t.Logf("plan names: %v", named)
	require.Contains(t, named, expired,
		"the sweep did not plan to destroy an environment an hour past its lifetime")
	require.NotContains(t, named, live,
		"the sweep planned to destroy an environment inside its lifetime")
	// Everything else it named, whoever it belongs to, has to have said its
	// own lifetime was over. This is the claim that makes a whole daemon sweep
	// safe, and another run's expired environment satisfies it honestly.
	for _, id := range named {
		at, stated := latestStatedExpiry(inv, id)
		require.True(t, stated, "the sweep planned to destroy %s, which states no lifetime", id)
		require.True(t, at.Before(now),
			"the sweep planned to destroy %s, whose stated lifetime runs until %s", id, at)
	}

	g := &guarded{r: r, own: own, during: during}
	result := reaper.Sweep(context.Background(), inv, nil, now, g)
	require.Equal(t, []string{expired}, g.reached,
		"the sweep reached the real teardown for something other than this run's expired environment")
	for _, out := range result.Outcomes {
		if out.EnvID == expired {
			require.NoError(t, out.Err)
			require.Positive(t, out.Removed, "the sweep reported removing nothing")
			continue
		}
		require.ErrorIs(t, out.Err, errNotThisRun)
	}
	t.Logf("sweep scanned %d environments and removed %d resources from %v",
		result.Scanned, result.Removed(), g.reached)

	// And the daemon agrees. This is the part a sweeper that deleted zero rows
	// forever would fail.
	after := envIDs(t, r)
	require.Zero(t, after[expired], "the expired environment is still on the daemon")
	require.Positive(t, after[live], "the sweep destroyed an environment inside its lifetime")

	// Everybody else's environments are not this run's to account for. One
	// that left the daemon while the sweep ran was removed by its owner: the
	// guard above is what proves the sweep did not reach it.
	for id := range before {
		if own.owns(id) || after[id] > 0 {
			continue
		}
		t.Logf("%s left the daemon while the sweep ran, and the sweep's teardown reached only %v",
			id, g.reached)
	}
}

func TestReaper_DestroysAnExpiredEnvironmentOnARealDaemon(t *testing.T) {
	r := dockerRuntime(t)
	ensureBusybox(t)
	sweepAndCheck(t, r, newRun(), nil)
}

// The interleaving that failed #394's edition boundary, constructed rather than
// waited for: another test tears its environment down in the middle of this
// sweep. The environment it removes was present in the first inventory and is
// gone from the second, and nothing this run did removed it.
func TestReaper_ABystanderTornDownDuringTheSweepIsNotBlamedOnIt(t *testing.T) {
	r := dockerRuntime(t)
	ensureBusybox(t)
	own := newRun()
	other := otherEnvID()
	makeEnv(t, r, other, time.Now().UTC().Add(30*time.Minute))

	tornDown := false
	sweepAndCheck(t, r, own, func(envID string) {
		if envID != own.prefix+"expired" || tornDown {
			return
		}
		tornDown = true
		down, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		td, err := r.Down(down, other)
		require.NoError(t, err)
		require.Positive(t, td.Removed,
			"the bystander was not torn down, so this constructed nothing")
	})
	require.True(t, tornDown, "the sweep never reached this run's environment, so the interleaving never happened")
}

// The other half: an environment another run created, whose lifetime has
// passed, is on the daemon when this run sweeps. The plan is right to name it,
// and this run must leave it for its owner.
func TestReaper_AnotherRunsExpiredEnvironmentIsLeftForItsOwner(t *testing.T) {
	r := dockerRuntime(t)
	ensureBusybox(t)
	other := otherEnvID()
	makeEnv(t, r, other, time.Now().UTC().Add(-2*time.Hour))
	// And another that states no lifetime at all, so the check that everything
	// the plan named had said its own lifetime was over has something to
	// refuse the day the predicate stops requiring one.
	makeEnvUnstamped(t, r, otherEnvID())

	sweepAndCheck(t, r, newRun(), nil)
	require.Positive(t, envIDs(t, r)[other],
		"this run's sweep destroyed another run's environment, which was that run's fixture")
}

// Two runs of this test on one daemon at once, which is how every lane on this
// machine runs it. Each has to create its environments without colliding and
// sweep without taking the other's.
func TestReaper_TwoRunsOnOneDaemonDoNotCollide(t *testing.T) {
	r := dockerRuntime(t)
	ensureBusybox(t)
	for _, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sweepAndCheck(t, r, newRun(), nil)
		})
	}
}

func TestReaper_LeavesAnEnvironmentThatStatesNoLifetime(t *testing.T) {
	r := dockerRuntime(t)
	ensureBusybox(t)

	// Created the way a release before this feature created them: the full
	// managed label set, and no expiry.
	legacy := newRun().prefix + "legacy"
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
