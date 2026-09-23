package fault_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/fault"
)

// What a disk_fill fault is allowed to fill, and what it is not.
//
// The rule the injector enforces has two halves and only the first is obvious.
// The first is that the data directory must be a mount of its own, because a
// container's writable layer is the daemon's disk. The second is the one that
// took a measurement to find: a mount of its own can still BE the daemon's
// disk. A plain named volume has its own device number, so it satisfies a
// device check, and filling it takes space from every other container on the
// machine having satisfied the guard, which is worse than being refused.
//
// So each case here builds a real container with a real mount at the data
// directory and drives the real injector at it. Every refusal is paired with
// the liveness arm the file's older cases established: an identical call on a
// volume created with a size that must be ALLOWED, and must actually fill.
// Without it these would pass just as well against an injector that refused
// everything.

// fillDir is where each case in this file mounts something.
const fillDir = "/var/lib/data"

// boundedVolume creates a volume whose size is fixed when it is created, which
// is the only arrangement the injector will fill.
func boundedVolume(t *testing.T, cli *client.Client, name string, labels map[string]string, bytes int64) {
	t.Helper()
	_, err := cli.VolumeCreate(t.Context(), client.VolumeCreateOptions{
		Name:   name,
		Driver: "local",
		DriverOpts: map[string]string{
			"type":   "tmpfs",
			"device": "tmpfs",
			"o":      "size=" + strconv.FormatInt(bytes, 10),
		},
		Labels: labels,
	})
	require.NoErrorf(t, err, "creating the bounded volume %s", name)
	t.Cleanup(func() {
		_, _ = cli.VolumeRemove(context.Background(), name, client.VolumeRemoveOptions{Force: true})
	})
}

// plainVolume creates an ordinary volume, which is a directory on the daemon's
// own disk however its device number reads from inside a container.
func plainVolume(t *testing.T, cli *client.Client, name string, labels map[string]string) {
	t.Helper()
	_, err := cli.VolumeCreate(t.Context(), client.VolumeCreateOptions{
		Name: name, Driver: "local", Labels: labels,
	})
	require.NoErrorf(t, err, "creating the plain volume %s", name)
	t.Cleanup(func() {
		_, _ = cli.VolumeRemove(context.Background(), name, client.VolumeRemoveOptions{Force: true})
	})
}

// sleeperWithMount is sleeper with something mounted at fillDir.
func sleeperWithMount(t *testing.T, cli *client.Client, labels map[string]string, vol string) string {
	t.Helper()
	ensureImage(t, cli, sleeperImage)
	resp, err := cli.ContainerCreate(t.Context(), client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  sleeperImage,
			Labels: labels,
			Cmd:    []string{"/bin/sh", "-c", "while true; do sleep 1; done"},
		},
		HostConfig: &container.HostConfig{
			Binds:         []string{vol + ":" + fillDir},
			RestartPolicy: container.RestartPolicy{Name: "no"},
		},
		Name: fmt.Sprintf("af-fill-%d", time.Now().UnixNano()),
	})
	require.NoError(t, err, "creating the test container")
	t.Cleanup(func() {
		_ = dockerutil.RemoveContainer(context.Background(), cli, resp.ID)
	})
	_, err = cli.ContainerStart(t.Context(), resp.ID, client.ContainerStartOptions{})
	require.NoError(t, err, "starting the test container")
	return resp.ID
}

// fill is the fault every case in this file drives.
func fill(name string, headroom, max int64) fault.Fault {
	return fault.Fault{
		Name: name, Kind: fault.KindDiskFill,
		Target:        fault.Target{Role: fault.RoleDatabase},
		Path:          fillDir,
		HeadroomBytes: headroom, MaxFillBytes: max,
	}
}

// freeBytesIn reads the free space the container itself reports, which is what
// the fault reads and what Postgres would meet.
func freeBytesIn(t *testing.T, inj *fault.Injector, dir string) int64 {
	t.Helper()
	sh, err := inj.Shell(t.Context(), fault.Target{Role: fault.RoleDatabase})
	require.NoError(t, err)
	out, err := sh.Run(t.Context(), []string{"/bin/sh", "-c", "df -P -k " + dir + " | tail -1"})
	require.NoError(t, err)
	require.Zero(t, out.ExitCode, "df said %q", out.Stdout)
	cols := strings.Fields(strings.ReplaceAll(out.Stdout, "\r", ""))
	require.GreaterOrEqual(t, len(cols), 4, "df said %q, which has no available column", out.Stdout)
	kb, err := strconv.ParseInt(cols[3], 10, 64)
	require.NoErrorf(t, err, "df's available column is %q", cols[3])
	return kb * 1024
}

// TestDiskFill_RefusesAVolumeWithNoSizeFixedAtCreation is the refusal a device
// check cannot make.
//
// The volume is ours, it belongs to this environment, and it is a mount of its
// own: everything a filesystem check can see says yes. What it has no size,
// so the space it would take is the daemon's disk, and filling it would take
// the machine down having passed every other guard.
func TestDiskFill_RefusesAVolumeWithNoSizeFixedAtCreation(t *testing.T) {
	cli := requireDocker(t)
	envID := envName("fillA")
	vol := "af-fill-plain-" + envID
	plainVolume(t, cli, vol, dockerutil.Managed(dockerutil.KindVolume, envID, time.Now()))
	id := sleeperWithMount(t, cli, ours(envID, "branch", ""), vol)

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)

	// The device check passes on this container, which is the whole point of
	// the case: it is asserted here so that a later change making the device
	// check refuse it would show up as this test measuring the wrong thing.
	sh, err := inj.Shell(t.Context(), fault.Target{Role: fault.RoleDatabase})
	require.NoError(t, err)
	devs, err := sh.Run(t.Context(), []string{"/bin/sh", "-c",
		"stat -c %d " + fillDir + " && stat -c %d " + fillDir + "/.."})
	require.NoError(t, err)
	cols := strings.Fields(strings.ReplaceAll(devs.Stdout, "\r", ""))
	require.Len(t, cols, 2)
	require.NotEqual(t, cols[0], cols[1],
		"the mount is not its own filesystem, so this case is not testing the refusal it names")

	_, err = inj.InjectInto(t.Context(), id, fill("fill-a-plain-volume", 1<<20, 1<<40))
	require.Error(t, err, "a fill was accepted on a volume that has the daemon's whole disk behind it")
	require.Contains(t, err.Error(), "AF-CHS-005")
	require.Contains(t, err.Error(), "no size fixed at creation")
	requireNoFillFile(t, sh)
}

// TestDiskFill_RefusesAVolumeOfAnotherEnvironment keeps one environment's
// fault out of another's storage.
//
// The volume has a size and is one Antifailure created, so the refusal is
// about whose it is rather than about what it is.
func TestDiskFill_RefusesAVolumeOfAnotherEnvironment(t *testing.T) {
	cli := requireDocker(t)
	envID := envName("fillB")
	other := envName("fillBother")
	vol := "af-fill-other-" + envID
	boundedVolume(t, cli, vol, dockerutil.Managed(dockerutil.KindVolume, other, time.Now()), 64<<20)
	id := sleeperWithMount(t, cli, ours(envID, "branch", ""), vol)

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	_, err = inj.InjectInto(t.Context(), id, fill("fill-another-environment", 1<<20, 1<<40))
	require.Error(t, err, "a fill was accepted on another environment's volume")
	require.Contains(t, err.Error(), "AF-CHS-005")
	require.Contains(t, err.Error(), "belongs to something other than this environment")
}

// TestDiskFill_RefusesAVolumeNobodyLabelled is the same refusal reached from
// the other direction: a bounded volume that Antifailure did not create.
func TestDiskFill_RefusesAVolumeNobodyLabelled(t *testing.T) {
	cli := requireDocker(t)
	envID := envName("fillC")
	vol := "af-fill-unlabelled-" + envID
	boundedVolume(t, cli, vol, nil, 64<<20)
	id := sleeperWithMount(t, cli, ours(envID, "branch", ""), vol)

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	_, err = inj.InjectInto(t.Context(), id, fill("fill-a-strangers-volume", 1<<20, 1<<40))
	require.Error(t, err, "a fill was accepted on a volume nothing here created")
	require.Contains(t, err.Error(), "AF-CHS-005")
	require.Contains(t, err.Error(), "belongs to something other than this environment")
}

// TestDiskFill_FillsABoundedVolumeAndTheUndoGivesTheSpaceBack is the liveness
// arm, and it is what makes the three refusals above mean anything.
//
// It asserts the effect rather than the command: the free space the container
// reports afterwards is under the headroom that was asked for, and it is back
// above it once the undo has run.
func TestDiskFill_FillsABoundedVolumeAndTheUndoGivesTheSpaceBack(t *testing.T) {
	cli := requireDocker(t)
	envID := envName("fillD")
	vol := "af-fill-bounded-" + envID
	const size = 64 << 20
	const headroom = 8 << 20
	boundedVolume(t, cli, vol, dockerutil.Managed(dockerutil.KindVolume, envID, time.Now()), size)
	id := sleeperWithMount(t, cli, ours(envID, "branch", ""), vol)

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	before := freeBytesIn(t, inj, fillDir)
	require.Greater(t, before, int64(headroom),
		"the volume already has less than the headroom free, so a fill would change nothing")

	in, err := inj.InjectInto(t.Context(), id, fill("fill-a-bounded-volume", headroom, size))
	require.NoError(t, err, "a fill was refused on a volume this environment created with a size")
	require.Contains(t, in.Evidence, "filled "+fillDir,
		"the evidence does not say what was filled")

	during := freeBytesIn(t, inj, fillDir)
	require.Less(t, during, before, "the fault reported a fill and the free space did not move")
	require.LessOrEqual(t, during, int64(headroom),
		"the fill stopped above the headroom it was asked for, so a writer would not meet ENOSPC")

	require.NoError(t, in.Undo(t.Context()))
	after := freeBytesIn(t, inj, fillDir)
	require.Greater(t, after, int64(headroom),
		"the undo ran and the space did not come back, so the environment is left broken")
	sh, err := inj.Shell(t.Context(), fault.Target{Role: fault.RoleDatabase})
	require.NoError(t, err)
	requireNoFillFile(t, sh)
}

// TestInjectInto_RefusesTheStorageAnchor protects the container that holds the
// data directory mounted.
//
// It belongs to this environment and passes every other check, exactly as the
// egress sidecar does, and it is refused on its kind alone. Stopping it would
// not crash the database: the volume driver would unmount the data directory,
// so the next start would initialise an empty one and every durability check
// after it would report an environment that had lost everything.
func TestInjectInto_RefusesTheStorageAnchor(t *testing.T) {
	cli := requireDocker(t)
	envID := envName("fillE")
	anchor := sleeper(t, cli, ours(envID, dockerutil.KindStorage, ""))
	branch := sleeper(t, cli, ours(envID, "branch", ""))

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	_, err = inj.InjectInto(t.Context(), anchor, pause("at-the-anchor", fault.Target{Role: fault.RoleDatabase}))
	require.Error(t, err, "the container holding the data directory was accepted as a fault target")
	require.ErrorIs(t, err, fault.ErrProtected)
	require.Contains(t, err.Error(), "data directory",
		"the refusal does not say why the anchor is protected")
	requireNotPaused(t, cli, anchor, "the container holding the data directory was frozen")

	// The liveness arm. The branch of the same environment, reached through
	// the same call, must be allowed.
	in, err := inj.InjectInto(t.Context(), branch, pause("at-the-branch", fault.Target{Role: fault.RoleDatabase}))
	require.NoError(t, err, "the branch was refused, so the refusal above proves nothing")
	requirePaused(t, cli, branch, "the fault was accepted and the branch is not frozen")
	require.NoError(t, in.Undo(t.Context()))
}

// requireNoFillFile asserts the fault left nothing behind.
//
// Every refusal in this file happens before anything is written, and the undo
// in the liveness arm removes what was. A fill file surviving either is a
// fault this package left in somebody's data directory.
func requireNoFillFile(t *testing.T, sh *fault.Shell) {
	t.Helper()
	out, err := sh.Run(t.Context(), []string{"/bin/sh", "-c", "ls -a " + fillDir})
	require.NoError(t, err)
	require.NotContains(t, out.Stdout, "antifailure-fault-fill",
		"a fill file was left in %s", fillDir)
}
