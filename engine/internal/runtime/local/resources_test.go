package local_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The size a container is actually held to, observed from inside it.
//
// resources.cpu and resources.memory were in schemas/manifest.v1.json from
// version one and this runtime passed an empty container.Resources, so every
// container it ever created ran uncapped. A cap that is accepted and applied
// nowhere is worse than a missing feature: the author's evidence that one
// environment cannot starve another is a run where nothing was ever bounded.
//
// Everything here is asserted against the DAEMON and against the container's
// own cgroup, never against what the runtime says about itself. A runtime that
// accepted a cap and set none reports exactly what a correct one reports, and
// asking the container is the only thing that tells the two apart.

const testGiB = 1024 * 1024 * 1024

func TestResources_TheContainerIsHeldToTheMemoryCapItAskedFor(t *testing.T) {
	r := requireRuntime(t)
	requireBusybox(t)

	const want = 64 * 1024 * 1024
	// The container prints the cap its OWN process is subject to. cgroup v2
	// keeps it in memory.max and v1 in memory/memory.limit_in_bytes, and a
	// check that knew only one would report "not applied" on a machine whose
	// kernel uses the other, which is a false failure and the worst kind.
	svc := provider.ServiceSpec{
		Name: "capped", Image: busyboxRef, Kind: "worker",
		MemoryBytes: want,
		Command: "echo cap=$(cat /sys/fs/cgroup/memory.max 2>/dev/null || " +
			"cat /sys/fs/cgroup/memory/memory.limit_in_bytes 2>/dev/null || echo unreadable); sleep 120",
	}
	id := envID(t, r, "size1")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env, err := r.Up(ctx, provider.EnvSpec{EnvID: id, Services: []provider.ServiceSpec{svc}})
	require.NoError(t, err)
	require.Len(t, env.Services, 1)

	// What the runtime says, which on its own proves nothing.
	require.Equal(t, int64(want), env.Services[0].MemoryBytes,
		"the runtime does not report the cap it was asked for")

	// What the process inside says, which is the part that cannot be faked.
	got := capFromLogs(ctx, t, r, id, "capped")
	require.NotEqual(t, "max", got,
		"the container's own cgroup says max, which is no cap at all. The runtime "+
			"reported a size it never applied, which is exactly what a runtime that "+
			"applied one reports")
	require.NotEqual(t, "unreadable", got,
		"the container could not read its own cgroup, so whether the cap was applied "+
			"is unknown. Reported as a failure rather than skipped, because a check "+
			"that cannot say no is worse than no check")
	n, err := strconv.ParseInt(got, 10, 64)
	require.NoError(t, err, "the container reported its cap as %q", got)
	require.Equal(t, int64(want), n,
		"the container is held to a different number of bytes than the manifest asked for")
}

func TestResources_AServiceThatNamedNoSizeRunsUncapped(t *testing.T) {
	r := requireRuntime(t)
	requireBusybox(t)

	// The control that makes the test above mean something, and the
	// compatibility claim as well. Every manifest written before this key was
	// honoured names no size, and each has to produce the container it
	// produced before. If this one also came back capped, the cap would not be
	// something a manifest chooses.
	svc := provider.ServiceSpec{
		Name: "uncapped", Image: busyboxRef, Kind: "worker",
		Command: "echo cap=$(cat /sys/fs/cgroup/memory.max 2>/dev/null || " +
			"cat /sys/fs/cgroup/memory/memory.limit_in_bytes 2>/dev/null || echo unreadable); sleep 120",
	}
	id := envID(t, r, "size2")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env, err := r.Up(ctx, provider.EnvSpec{EnvID: id, Services: []provider.ServiceSpec{svc}})
	require.NoError(t, err)
	require.Zero(t, env.Services[0].MemoryBytes)
	require.Zero(t, env.Services[0].CPUMillis)

	got := capFromLogs(ctx, t, r, id, "uncapped")
	if n, err := strconv.ParseInt(got, 10, 64); err == nil {
		// cgroup v1 spells unlimited as a very large number rather than as a
		// word, so a number here is only a failure if it is a plausible cap.
		require.Greater(t, n, int64(64*testGiB),
			"a container that asked for no size came back capped at %d bytes", n)
	}
}

func TestResources_StatusReportsTheSizeTheDaemonIsHolding(t *testing.T) {
	r := requireRuntime(t)
	requireBusybox(t)

	// Read back off the daemon's own record of the container rather than off
	// the spec that asked for it, which is the whole point: this is the place
	// somebody who wrote resources.memory looks to find out whether anything
	// happened, and the manifest and the runtime agreed even when nothing was
	// applied.
	svc := provider.ServiceSpec{
		Name: "sized", Image: busyboxRef, Kind: "worker",
		CPUMillis: 500, MemoryBytes: 64 * 1024 * 1024,
		Command: "sleep 120",
	}
	id := envID(t, r, "size3")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	_, err := r.Up(ctx, provider.EnvSpec{EnvID: id, Services: []provider.ServiceSpec{svc}})
	require.NoError(t, err)

	after, err := r.Status(ctx, id)
	require.NoError(t, err)
	var found bool
	for _, s := range after.Services {
		if s.Name != "sized" {
			continue
		}
		found = true
		require.Equal(t, int64(500), s.CPUMillis,
			"Status reports a different CPU share than the daemon was given. NanoCPUs "+
				"comes back in billionths and the manifest is written in thousandths, so "+
				"a factor of a million between them is the mistake to look for")
		require.Equal(t, int64(64*1024*1024), s.MemoryBytes)
	}
	require.True(t, found, "the service is not in the status report at all")
}

func TestResources_RefusesAnEnvironmentThisMachineCannotHold(t *testing.T) {
	r := requireRuntime(t)
	requireBusybox(t)

	// Refused BEFORE the network is created, so an environment this machine
	// cannot hold leaves nothing behind for af down to find. Without the
	// check, the daemon fails several seconds in with a message about a cgroup
	// rather than about the line somebody wrote.
	//
	// Sixteen terabytes rather than a number near this machine's own, because
	// a threshold test written against the machine it runs on passes or fails
	// for reasons that have nothing to do with the code.
	svc := provider.ServiceSpec{
		Name: "enormous", Image: busyboxRef, Kind: "worker",
		MemoryBytes: 16 * 1024 * testGiB,
		Command:     "sleep 120",
	}
	id := envID(t, r, "size4")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err := r.Up(ctx, provider.EnvSpec{EnvID: id, Services: []provider.ServiceSpec{svc}})
	require.Error(t, err, "an environment asking for 16TiB of memory was accepted")
	require.Contains(t, err.Error(), "AF-RUN-047")
	require.Contains(t, err.Error(), `service "enormous" asks for 16384Gi of memory per instance`,
		"the refusal has to name the service and the size, because the point of "+
			"refusing early is that the reader can find the key it came from")

	// And it left nothing behind. A refusal plus something for af down to find
	// is two problems.
	after, err := r.Status(ctx, id)
	require.NoError(t, err)
	require.Empty(t, after.Services)
}

// capFromLogs reads the cgroup cap the service printed about itself.
//
// Polled, because a container that has been created and started has not
// necessarily reached its first line yet, and an empty log read too early says
// nothing about the cap.
func capFromLogs(ctx context.Context, t *testing.T, r interface {
	Logs(context.Context, string, string, int) ([]provider.LogLine, error)
}, envID, service string) string {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		lines, err := r.Logs(ctx, envID, service, 200)
		require.NoError(t, err)
		for _, l := range lines {
			if _, after, found := strings.Cut(l.Text, "cap="); found {
				return strings.TrimSpace(after)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("service %q never printed the cap its own process is subject to, so "+
				"whether the cap was applied at all is unknown", service)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
