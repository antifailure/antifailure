package docker_test

// THE DATA DIRECTORY'S OWN FILESYSTEM, AGAINST A REAL DAEMON.
//
// The claim this file settles is not "a volume was created". It is that a
// branch whose manifest asked for the data directory to have a filesystem of
// its own comes up holding the golden's data, on a filesystem whose size is
// the declared one, and that the data is still there after the container has
// been stopped and started, which is what a container_kill fault does to it.
//
// The last of those is the reason the anchor container exists and it is the
// only one that had to be measured rather than reasoned about: the local
// volume driver unmounts a memory backed volume when the last container using
// it stops, so a branch that stopped and started without an anchor came back
// with an empty data directory that the image's entrypoint had cheerfully
// initialised. Every durability assertion after that would have been reading a
// new database and reporting the old one as lost. The falsification arm at the
// bottom of TestABranchWithItsOwnFilesystemKeepsItsDataAcrossAStopAndStart
// stops the anchor and shows exactly that.

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// storageDataDir is where the provider puts PGDATA, and therefore where the
// declared filesystem has to be mounted.
const storageDataDir = "/var/lib/antifailure/pgdata"

// storageSeed is enough rows to be worth copying and few enough to be quick.
const storageSeed = `CREATE TABLE ledger (id bigserial primary key, v text);
	INSERT INTO ledger (v) SELECT 'row' || g FROM generate_series(1, 20000) g;`

// inBranch runs a shell command inside the branch container and returns what
// it printed.
//
// Read from the container rather than from the provider, for the reason every
// assertion in this package is written that way: a provider reporting on the
// filesystem it asked for is a provider reporting on its own intentions.
func inBranch(t *testing.T, ctx context.Context, envID, script string) (string, int) {
	t.Helper()
	cli, err := dockerutil.Client()
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()
	created, err := cli.ExecCreate(ctx, "af-db-"+envID, client.ExecCreateOptions{
		Cmd: []string{"/bin/sh", "-c", script}, AttachStdout: true, AttachStderr: true, TTY: true,
	})
	require.NoError(t, err, "creating an exec in the branch")
	attached, err := cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: true})
	require.NoError(t, err, "attaching to an exec in the branch")
	// Read to the end of the stream rather than once. A single Read returns
	// whatever the daemon has framed so far, which for a command printing two
	// lines is regularly the first line alone, and an assertion about the
	// second would then be measuring the read rather than the container.
	out, err := io.ReadAll(io.LimitReader(attached.Reader, 64<<10))
	attached.Close()
	require.NoError(t, err, "reading the exec output")
	for {
		insp, err := cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		require.NoError(t, err)
		if !insp.Running {
			return strings.TrimSpace(strings.ReplaceAll(string(out), "\r", "")), insp.ExitCode
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestABranchWithItsOwnFilesystemKeepsItsDataAcrossAStopAndStart is the whole
// claim in one run.
func TestABranchWithItsOwnFilesystemKeepsItsDataAcrossAStopAndStart(t *testing.T) {
	const size = 512 << 20
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	e := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 47900,
		SeedSQL:      storageSeed,
		StorageBytes: size,
	})
	gv := e.refresh(ctx, provider.GoldenSpec{Version: 17, RulesHash: "storage1", Provenance: "storage-own-filesystem"})
	envID := "env_storage0000001"
	b := e.branch(ctx, gv.ID, envID)
	conn := e.open(ctx, b)

	// The data arrived. A branch on a filesystem the provider populated itself
	// rather than on one the daemon copied on write, so this is the assertion
	// that separates a working copy from an immaculate empty Postgres.
	require.Equal(t, int64(20000), scan[int64](t, conn, `SELECT count(*) FROM ledger`),
		"the golden's rows are not in a branch whose data directory has its own filesystem")

	// It is a filesystem of its own, and it is the declared size. Both, because
	// either alone is satisfied by the layout this key exists to replace: the
	// writable layer is not its own mount, and a plain volume is its own mount
	// with the daemon's whole disk behind it.
	devs, code := inBranch(t, ctx, envID,
		"stat -c %d "+storageDataDir+" && stat -c %d "+storageDataDir+"/..")
	require.Zero(t, code, "reading the device numbers said %q", devs)
	parts := strings.Fields(devs)
	require.Len(t, parts, 2)
	require.NotEqual(t, parts[0], parts[1],
		"the data directory shares a filesystem with its parent, so disk_fill would still be refused")

	df, code := inBranch(t, ctx, envID, "df -P -k "+storageDataDir+" | tail -1")
	require.Zero(t, code, "df said %q", df)
	cols := strings.Fields(df)
	require.GreaterOrEqual(t, len(cols), 2, "df said %q", df)
	total, err := strconv.ParseInt(cols[1], 10, 64)
	require.NoError(t, err)
	require.Equal(t, int64(size/1024), total,
		"the data directory's filesystem is %d KiB and the manifest asked for %d", total, size/1024)

	// The stop and start a container_kill fault's undo performs. The rows have
	// to survive it, or every durability check after such a fault would be
	// reading a database this provider had just destroyed.
	cli, err := dockerutil.Client()
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()
	_, err = cli.ContainerStop(ctx, "af-db-"+envID, client.ContainerStopOptions{})
	require.NoError(t, err, "stopping the branch")
	_, err = cli.ContainerStart(ctx, "af-db-"+envID, client.ContainerStartOptions{})
	require.NoError(t, err, "starting the branch again")
	requireBranchReady(t, ctx, envID)
	after := e.open(ctx, b)
	require.Equal(t, int64(20000), scan[int64](t, after, `SELECT count(*) FROM ledger`),
		"the branch came back from a stop and start without its rows")

	// The falsification arm, and the reason the anchor is in the product. With
	// the anchor stopped, the same stop and start loses the data directory
	// entirely. If this arm ever stops failing, the anchor has become
	// unnecessary and should be deleted rather than left as decoration.
	_, err = cli.ContainerStop(ctx, "af-pgdata-anchor-"+envID, client.ContainerStopOptions{})
	require.NoError(t, err, "stopping the anchor")
	_, err = cli.ContainerStop(ctx, "af-db-"+envID, client.ContainerStopOptions{})
	require.NoError(t, err)
	_, err = cli.ContainerStart(ctx, "af-db-"+envID, client.ContainerStartOptions{})
	require.NoError(t, err)
	requireBranchReady(t, ctx, envID)
	// Asked of the SERVER, not of a directory listing. Without the anchor the
	// volume driver unmounted the data directory and the image's entrypoint
	// initialised a new one, so the container comes back healthy, accepts
	// connections, and does not have the table. A listing would show a data
	// directory either way.
	out, code := inBranch(t, ctx, envID,
		"psql -U antifailure -d antifailure -tAc 'SELECT count(*) FROM ledger' 2>&1")
	require.NotZero(t, code,
		"this arm is meant to show the data directory being lost without the anchor, and the rows are still there: %q", out)
	require.Contains(t, out, "does not exist",
		"the branch came back without the table for some reason other than a data directory that was thrown away: %q", out)
}

// requireBranchReady waits for Postgres to answer again after a restart.
func requireBranchReady(t *testing.T, ctx context.Context, envID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if _, code := inBranch(t, ctx, envID, "pg_isready -U antifailure -d antifailure"); code == 0 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("the branch did not accept connections again within two minutes")
}

// TestTheDataDirectoryIsOnTheWritableLayerWhenNoSizeIsDeclared is the control.
//
// Without it the test above would pass on a provider that gave every branch
// its own filesystem regardless, which would be a silent change to the layout
// of every environment rather than the opt in this is.
func TestTheDataDirectoryIsOnTheWritableLayerWhenNoSizeIsDeclared(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	e := newExtensionProvider(t, dockerdb.Options{Version: 17, PortFrom: 47940, SeedSQL: storageSeed})
	gv := e.refresh(ctx, provider.GoldenSpec{Version: 17, RulesHash: "storage2", Provenance: "storage-default-layout"})
	envID := "env_storage0000002"
	_ = e.branch(ctx, gv.ID, envID)

	devs, code := inBranch(t, ctx, envID,
		"stat -c %d "+storageDataDir+" && stat -c %d "+storageDataDir+"/..")
	require.Zero(t, code, "reading the device numbers said %q", devs)
	parts := strings.Fields(devs)
	require.Len(t, parts, 2)
	require.Equal(t, parts[0], parts[1],
		"a manifest that declared no size got a data directory on a filesystem of its own")
}

// TestADatabaseThatDoesNotFitIsRefusedByName is the failure a person will
// actually meet, because the size has to hold the whole data directory.
//
// Refused rather than truncated: half a data directory is a database that
// starts and is missing rows, which is the one outcome worse than not starting.
func TestADatabaseThatDoesNotFitIsRefusedByName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// Smaller than an empty Postgres data directory, which is about forty
	// megabytes, so the copy runs out of room rather than failing to start.
	e := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 47970, SeedSQL: storageSeed, StorageBytes: 20 << 20,
	})
	gv := e.refresh(ctx, provider.GoldenSpec{Version: 17, RulesHash: "storage3", Provenance: "storage-too-small"})
	envID := "env_storage0000003"
	_, err := e.p.Branch(ctx, gv.ID, envID)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_ = e.p.Destroy(clean, provider.Branch{EnvID: envID})
	})
	require.Error(t, err, "a branch came up on a filesystem too small to hold its data directory")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB043))
	require.Contains(t, err.Error(), "does not fit")
}

// TestASizeLargerThanTheDaemonsMemoryIsRefused keeps the containment promise
// on the other axis.
//
// The filesystem is held in memory, which is what stops a fill reaching the
// machine's disk. One larger than the machine would move the same problem to
// the memory, and a daemon killed for memory takes every environment on it.
func TestASizeLargerThanTheDaemonsMemoryIsRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()
	info, err := cli.Info(ctx, client.InfoOptions{})
	require.NoError(t, err)
	require.Positive(t, info.Info.MemTotal, "the daemon reports no memory, so this case cannot be built")

	e := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 47990, StorageBytes: info.Info.MemTotal,
	})
	// A real golden, so that the refusal is the memory guard rather than the
	// branch failing earlier for want of an image to start.
	gv := e.refresh(ctx, provider.GoldenSpec{Version: 17, RulesHash: "storage5", Provenance: "storage-too-large"})
	envID := "env_storage0000004"
	_, err = e.p.Branch(ctx, gv.ID, envID)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_ = e.p.Destroy(clean, provider.Branch{EnvID: envID})
	})
	require.Error(t, err, "a filesystem the size of the daemon's whole memory was accepted")
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB042))
	require.NotContains(t, err.Error(), "AF-DB-004",
		"the branch failed for want of a golden, so the memory guard was never reached")
}

// TestDestroyTakesTheAnchorAndTheVolumeWithTheBranch is the leak arm.
//
// A container that outlives its environment holds a volume open, and that
// volume holds somebody's data directory. Both are asserted against the daemon
// rather than against the provider's return value.
func TestDestroyTakesTheAnchorAndTheVolumeWithTheBranch(t *testing.T) {
	const size = 256 << 20
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	e := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 48020, SeedSQL: storageSeed, StorageBytes: size,
	})
	gv := e.refresh(ctx, provider.GoldenSpec{Version: 17, RulesHash: "storage4", Provenance: "storage-teardown"})
	envID := "env_storage0000005"
	b, err := e.p.Branch(ctx, gv.ID, envID)
	require.NoError(t, err)

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	// Present first, so that the absence asserted below is a removal rather
	// than something that was never created.
	_, err = cli.ContainerInspect(ctx, "af-pgdata-anchor-"+envID, client.ContainerInspectOptions{})
	require.NoError(t, err, "the anchor was not created, so its removal proves nothing")
	_, err = cli.VolumeInspect(ctx, "af-pgdata-"+envID, client.VolumeInspectOptions{})
	require.NoError(t, err, "the volume was not created, so its removal proves nothing")

	// It is in the inventory the leak detector reads. A resource nothing can
	// see is one that leaks silently.
	inv, err := e.p.Inventory(ctx)
	require.NoError(t, err)
	var sawAnchor, sawVolume bool
	for _, r := range inv {
		if r.Kind == "container/"+dockerutil.KindStorage && r.EnvID == envID {
			sawAnchor = true
		}
		if r.Kind == "volume/data" && r.ID == "af-pgdata-"+envID {
			sawVolume = true
		}
	}
	require.True(t, sawAnchor, "the anchor container is not in the provider's inventory")
	require.True(t, sawVolume, "the data directory volume is not in the provider's inventory")

	require.NoError(t, e.p.Destroy(ctx, b))

	_, err = cli.ContainerInspect(ctx, "af-pgdata-anchor-"+envID, client.ContainerInspectOptions{})
	require.Error(t, err, "the anchor is still running after the branch was destroyed")
	_, err = cli.VolumeInspect(ctx, "af-pgdata-"+envID, client.VolumeInspectOptions{})
	require.Error(t, err, "the data directory volume is still there after the branch was destroyed")
}
