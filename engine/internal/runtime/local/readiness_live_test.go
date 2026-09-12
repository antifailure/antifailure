package local_test

import (
	"context"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Readiness that can say no, and mounts that arrive before the process starts,
// against a real daemon.
//
// Every test here is the SMALLEST environment that exercises its behaviour: the
// sidecar and one or two services, three containers at the most, and none of
// them runs in parallel with another, because this daemon is shared and small.
// envID removes every container, network and volume a test made, whether or not
// the assertion under test is what failed.

func runningNamed(t *testing.T, env provider.Env, name string) provider.RunningService {
	t.Helper()
	for _, s := range env.Services {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no service called %s was reported, so nothing about it was checked", name)
	return provider.RunningService{}
}

func upContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// THE FALSE PASS. A service with no port that dies two seconds after it starts,
// which is the shape of a process refused an outbound call at startup. On main
// before this change Up returned this environment with no error and the service
// marked ready, because readiness for a portless service was a single look taken
// the instant the container existed.
func TestReadiness_APortlessServiceThatExitsDuringStartupIsReportedFailed(t *testing.T) {
	r := requireRuntime(t)
	id := envID(t, r, "rdyexit")

	env, err := r.Up(upContext(t), provider.EnvSpec{
		EnvID: id, Egress: containedPolicy(),
		Services: []provider.ServiceSpec{{
			Name: "dies", Image: proberImage, Kind: "worker", Command: "sleep 2; exit 3",
		}},
	})
	require.Error(t, err, "a service that exited two seconds after starting was reported as up")
	require.Contains(t, err.Error(), "AF-RUN-005")
	svc := runningNamed(t, env, "dies")
	require.False(t, svc.Ready)
	require.Equal(t, provider.ReadinessFailed, svc.Readiness)
}

// A service with nothing to check is running and UNPROVED, never ready, and Up
// does not fail on it: an environment of workers came up, and it proved nothing.
func TestReadiness_APortlessServiceWithNoCommandIsUnprovedNotReady(t *testing.T) {
	r := requireRuntime(t)
	id := envID(t, r, "rdyunk")
	ctx := upContext(t)

	env, err := r.Up(ctx, provider.EnvSpec{
		EnvID: id, Egress: containedPolicy(),
		Services: []provider.ServiceSpec{{
			Name: "idle", Image: proberImage, Kind: "worker", Command: "sleep 300",
		}},
	})
	require.NoError(t, err)
	svc := runningNamed(t, env, "idle")
	require.False(t, svc.Ready, "a service with nothing to check was reported ready")
	require.Equal(t, provider.ReadinessUnproved, svc.Readiness)

	// And Status, which sees a container rather than a spec, says the same.
	st, err := r.Status(ctx, id)
	require.NoError(t, err)
	got := runningNamed(t, st, "idle")
	require.False(t, got.Ready, "Status reported a service with nothing to check as ready")
	require.Equal(t, provider.ReadinessUnproved, got.Readiness)
}

// ORDERING: the command check does not pass yet when the container starts, and
// passes four seconds later. Up must wait for it, which is the whole point:
// still starting is not ready.
func TestReadiness_AStillStartingServiceIsNotReportedReadyUntilItsCommandPasses(t *testing.T) {
	r := requireRuntime(t)
	id := envID(t, r, "rdyslow")

	start := time.Now()
	env, err := r.Up(upContext(t), provider.EnvSpec{
		EnvID: id, Egress: containedPolicy(),
		Services: []provider.ServiceSpec{{
			Name: "slow", Image: proberImage, Kind: "worker",
			Command:       "sleep 4; touch /tmp/ok; sleep 300",
			HealthCommand: "test -f /tmp/ok",
		}},
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(start), 4*time.Second,
		"Up returned before the health command could have passed")
	svc := runningNamed(t, env, "slow")
	require.True(t, svc.Ready)
	require.Equal(t, provider.ReadinessProved, svc.Readiness)
}

// ORDERING: the command never passes. Refused with the command named, inside
// the timeout, rather than waited on forever or reported ready.
func TestReadiness_ACommandThatNeverPassesIsRefusedAtItsTimeout(t *testing.T) {
	r := requireRuntime(t)
	id := envID(t, r, "rdynever")

	env, err := r.Up(upContext(t), provider.EnvSpec{
		EnvID: id, Egress: containedPolicy(),
		Services: []provider.ServiceSpec{{
			Name: "never", Image: proberImage, Kind: "worker", Command: "sleep 300",
			HealthCommand: "test -f /tmp/never", HealthTimeout: 6 * time.Second,
		}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF-RUN-050")
	// The command is in the next step rather than in the message, which is the
	// shape AF-RUN-004 already uses for the health path: the message says what
	// happened and the next step says what to look at.
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Contains(t, coded.NextStep(), "test -f /tmp/never")
	require.Equal(t, provider.ReadinessFailed, runningNamed(t, env, "never").Readiness)
}

// ORDERING: the check passes, and the process exits after it did while a later
// service is still coming up. The last look before Up returns catches it.
func TestReadiness_AServiceThatPassesThenExitsBeforeUpReturnsIsReportedFailed(t *testing.T) {
	r := requireRuntime(t)
	id := envID(t, r, "rdyflap")

	env, err := r.Up(upContext(t), provider.EnvSpec{
		EnvID: id, Egress: containedPolicy(),
		Services: []provider.ServiceSpec{
			{Name: "flaky", Image: proberImage, Kind: "worker",
				Command: "sleep 3; exit 4", HealthCommand: "true"},
			{Name: "after", Image: proberImage, Kind: "worker",
				Command: "sleep 300", DependsOn: []string{"flaky"}},
		},
	})
	require.Error(t, err, "a service that passed its check and then exited was reported as up")
	require.Contains(t, err.Error(), "AF-RUN-005")
	require.Contains(t, err.Error(), "flaky")
	svc := runningNamed(t, env, "flaky")
	require.False(t, svc.Ready)
	require.Equal(t, provider.ReadinessFailed, svc.Readiness)
}

// ORDERING: a service with BOTH a port and a command. The forwarder accepts a
// connection on the published port whether or not anything behind it listens,
// so a port alone would pass at once; nothing here listens at all. The command
// decides, and Up waits for it.
func TestReadiness_TheCommandDecidesForAServiceWithAPortAndACommand(t *testing.T) {
	r := requireRuntime(t)
	id := envID(t, r, "rdyboth")

	start := time.Now()
	env, err := r.Up(upContext(t), provider.EnvSpec{
		EnvID: id, Egress: containedPolicy(),
		Services: []provider.ServiceSpec{{
			Name: "db", Image: proberImage, Kind: "web", Port: 5432,
			Command:       "sleep 4; touch /tmp/ok; sleep 300",
			HealthCommand: "test -f /tmp/ok",
		}},
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(start), 4*time.Second,
		"the port decided readiness, and a Postgres still running its init scripts would have been reported ready")
	require.Equal(t, provider.ReadinessProved, runningNamed(t, env, "db").Readiness)
}

// A file and a directory, placed BEFORE the process starts: the service's own
// command reads the file as its first act, and the check proves what it saw.
// Then the containment half: no container in the environment carries a bind.
func TestMounts_AFileAndADirectoryArriveBeforeTheProcessStarts(t *testing.T) {
	r := requireRuntime(t)
	id := envID(t, r, "mntfile")
	ctx := upContext(t)

	env, err := r.Up(ctx, provider.EnvSpec{
		EnvID: id, Egress: containedPolicy(),
		Services: []provider.ServiceSpec{{
			Name: "reader", Image: proberImage, Kind: "worker",
			Command: "cat /etc/af/conf.txt > /tmp/seen 2>&1; sleep 300",
			HealthCommand: "grep -q token-7f3a /tmp/seen && test -f /init.d/roles.sql && " +
				"test -f /init.d/sub/data.sql && test -x /init.d/run.sh",
			HealthTimeout: 20 * time.Second,
			Mounts: []provider.MountSpec{
				{At: "/etc/af/conf.txt", Files: []provider.MountFile{{Mode: 0o644, Data: []byte("token-7f3a\n")}}},
				{At: "/init.d", Files: []provider.MountFile{
					{Rel: "roles.sql", Mode: 0o644, Data: []byte("create role anon;")},
					{Rel: "sub/data.sql", Mode: 0o644, Data: []byte("select 1;")},
					{Rel: "run.sh", Mode: 0o755, Data: []byte("#!/bin/sh\n")},
				}},
			},
		}},
	})
	require.NoError(t, err, "the mounted files were not where the process looked when it started")
	require.Equal(t, provider.ReadinessProved, runningNamed(t, env, "reader").Readiness)

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	list, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: dockerutil.EnvFilter(id)})
	require.NoError(t, err)
	require.NotEmpty(t, list)
	for _, c := range list {
		insp, inspErr := cli.ContainerInspect(ctx, c.ID)
		require.NoError(t, inspErr)
		require.Empty(t, insp.HostConfig.Binds, "%s has a bind mount", c.Names)
		require.Empty(t, insp.Mounts, "%s has a mount, and a copied file must not be one", c.Names)
	}
}

// A named volume on first run and on second run, with the service container
// removed in between, which is what a restart of the service is. The second
// container's check passes only if it can read what the first one wrote. Then
// the containment half, and then Down, after which the volume must be gone.
func TestMounts_ANamedVolumeKeepsWhatTheServiceWroteAcrossARestart(t *testing.T) {
	r := requireRuntime(t)
	id := envID(t, r, "mntvol")
	ctx := upContext(t)
	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	vol := "af-vol-" + id + "-data"
	// Belt and braces for a shared daemon: if Down were broken, the volume
	// would outlive this test, so it is removed by name whatever happens.
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = dockerutil.RemoveVolume(c, cli, vol)
	})

	spec := func(check string) provider.EnvSpec {
		return provider.EnvSpec{
			EnvID: id, Egress: containedPolicy(),
			Services: []provider.ServiceSpec{{
				Name: "store", Image: proberImage, Kind: "worker",
				Command: "if [ -f /data/first ]; then echo again > /data/second; " +
					"else echo once > /data/first; fi; sleep 300",
				HealthCommand: check,
				HealthTimeout: 20 * time.Second,
				Mounts:        []provider.MountSpec{{At: "/data", Volume: "data"}},
			}},
		}
	}

	// First run: the volume is empty, so the service writes /data/first.
	env, err := r.Up(ctx, spec("test -f /data/first"))
	require.NoError(t, err)
	first := runningNamed(t, env, "store")

	insp, err := cli.ContainerInspect(ctx, first.ContainerID)
	require.NoError(t, err)
	require.Empty(t, insp.HostConfig.Binds, "the service has a bind mount")
	require.Len(t, insp.Mounts, 1)
	require.Equal(t, "volume", string(insp.Mounts[0].Type), "the mount is not a named volume")
	require.Equal(t, vol, insp.Mounts[0].Name, "the volume is not scoped to this environment")

	// The restart: the container goes, the volume stays.
	require.NoError(t, dockerutil.RemoveContainer(ctx, cli, first.ContainerID))

	// Second run: the check passes only if the new container found /data/first.
	env, err = r.Up(ctx, spec("test -f /data/second"))
	require.NoError(t, err, "the second container did not find what the first one wrote")
	second := runningNamed(t, env, "store")
	require.NotEqual(t, first.ContainerID, second.ContainerID, "the container was not replaced, so nothing restarted")

	// And Down removes it, which is the half nobody checks.
	_, err = r.Down(ctx, id)
	require.NoError(t, err)
	_, err = cli.VolumeInspect(ctx, vol)
	require.True(t, err != nil && cerrdefs.IsNotFound(err),
		"the named volume survived af down, so the state a mount kept outlives the environment: %v", err)
}
