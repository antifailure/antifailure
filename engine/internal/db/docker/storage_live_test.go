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
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
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

// requireBranchReady waits for Postgres to answer again after a restart, over
// TCP rather than over the unix socket, and the difference is the whole point.
//
// THIS COST MAIN TWO RED COMMITS AND BLOCKED cd TWICE. The gate polled
// `pg_isready` with no host, which uses the unix socket, and returned as soon as
// SOMETHING answered on it. On the falsification arm below the data directory is
// deliberately empty, so the image's entrypoint runs `initdb` and starts a
// TEMPORARY server to do it. The official entrypoint starts that one with
// `-c listen_addresses=”`, so it accepts unix socket connections and listens on
// no TCP port at all. It then SHUTS IT DOWN and starts the real server. So the
// gate was satisfied by a server that was about to stop, and the assertion after
// it landed in the gap, which is why the two failures read as two unrelated
// faults:
//
//	psql: error: connection to server on socket "/var/run/postgresql/.s.PGSQL.5432"
//	failed: No such file or directory          <- the socket after the temp server went
//	FATAL: terminating connection due to administrator command
//	                                           <- the connection when it went
//
// MEASURED RATHER THAN REASONED, on postgres:17-alpine against an empty volume,
// probing both every 500 ms:
//
//	t=1000ms  socket=no  tcp=no
//	t=1500ms  socket=OK  tcp=no     <- the temporary init server, socket only
//	t=2000ms  socket=OK  tcp=OK     <- the real server
//
// That window is about half a second on an idle machine and far wider on a
// loaded CI runner, which is why this failed there and passed on a laptop. TCP
// is refused for the whole of it, so requiring TCP is a discriminator with a
// mechanism behind it rather than a longer sleep.
//
// The probe is also a QUERY rather than `pg_isready`, because a query is what
// every caller does next, and a gate should prove the capability the caller
// needs rather than a nearby one.
//
// AND IT NEEDS NO PASSWORD, WHICH IS A PROPERTY WORTH STATING RATHER THAN
// RELYING ON. A review read this as a defect, on the reasonable ground that the
// product's own TCP connection string carries the managed password and this
// probe carries none. The difference is WHERE it connects FROM. `initdb` in the
// stock image writes these host lines, read out of a running container rather
// than assumed:
//
//	host  all  all  127.0.0.1/32  trust
//	host  all  all  ::1/128       trust
//	host  all  all  all           scram-sha-256
//
// This probe runs INSIDE the branch through `docker exec`, so it arrives from
// 127.0.0.1 and matches the trust line. The product connects from outside the
// container, matches the last line, and needs the password. So the two are not
// the same connection and only one of them is passwordless.
//
// If the provider ever sets `POSTGRES_HOST_AUTH_METHOD`, those trust lines go
// and this probe stops working. That is the one change that would break it, and
// it is named here so the next person does not have to rediscover it.
func requireBranchReady(t *testing.T, ctx context.Context, envID string) {
	t.Helper()
	require.NoError(t, branchReady(t, ctx, envID, 2*time.Minute),
		"the branch did not start accepting connections over TCP again")
}

// branchReady is requireBranchReady's body with the deadline exposed, so a test
// can prove the gate REFUSES a server that only listens on the socket. Without
// that arm the TCP requirement above is an assertion nothing checks.
func branchReady(t *testing.T, ctx context.Context, envID string, within time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(within)
	last := ""
	for {
		out, code := inBranch(t, ctx, envID,
			`psql -h 127.0.0.1 -U antifailure -d antifailure -tAc 'SELECT 1' 2>&1`)
		if code == 0 && strings.Contains(out, "1") {
			return nil
		}
		last = strings.TrimSpace(out)
		if !time.Now().Before(deadline) {
			return fmt.Errorf("no answer over TCP within %s; the last attempt said %q", within, last)
		}
		time.Sleep(500 * time.Millisecond)
	}
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

// THE GATE MUST REFUSE A SERVER THAT ONLY LISTENS ON THE SOCKET, because that
// is the state the image's entrypoint passes through while it initialises a data
// directory, and satisfying the gate there is what reddened main twice.
//
// This is the falsification arm for requireBranchReady rather than for the
// product. It plants exactly the condition the old gate could not tell from
// readiness: a Postgres started with `listen_addresses=”`, which accepts unix
// socket connections and listens on no TCP port. `pg_isready` with no host
// answers YES to it, and the gate must answer NO.
//
// The window is made permanent instead of being waited for. Racing the real
// entrypoint's half second of init would be a test that passes for timing
// reasons, and this repository has three of those already tonight.
func TestBranchReadinessRefusesTheSocketOnlyServerTheEntrypointPassesThrough(t *testing.T) {
	requireImage(t, "postgres:17-alpine")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	// inBranch addresses a container by the name the product gives a branch, so
	// the planted server takes that shape rather than the helper being widened
	// for a test.
	envID := fmt.Sprintf("socketonly-%d", time.Now().UnixNano()%1e9)
	name := "af-db-" + envID
	created, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  "postgres:17-alpine",
			Labels: dockerutil.Managed("db-test", name, time.Now()),
			Env: []string{
				"POSTGRES_USER=antifailure",
				"POSTGRES_DB=antifailure",
				"POSTGRES_PASSWORD=socketonly",
			},
			// The entrypoint passes these to the real server too, so the server
			// that ends up running is socket only for the whole test rather
			// than for the half second the init server lives.
			Cmd: []string{"postgres", "-c", "listen_addresses="},
		},
		Name: name,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		clean, cancelClean := context.WithTimeout(context.Background(), time.Minute)
		defer cancelClean()
		// Reported rather than discarded. This container's kind is `db-test`,
		// which the provider's own candidate cleanup does not sweep, so a
		// removal that failed silently would leave a Postgres running and the
		// test would pass over the top of it. A leak nobody is told about is
		// found days later by whoever runs out of memory.
		if err := dockerutil.RemoveContainer(clean, cli, created.ID); err != nil {
			t.Errorf("the planted socket only server was left behind: %v", err)
		}
	})
	_, err = cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{})
	require.NoError(t, err)

	// The control, and it is what makes the refusal below mean something: the
	// server really is up and really does answer, on the socket, which is the
	// probe the old gate used.
	var socketOK bool
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); {
		if _, code := inBranch(t, ctx, envID, "pg_isready -U antifailure -d antifailure"); code == 0 {
			socketOK = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.True(t, socketOK,
		"the planted server never answered on the socket either, so this test is not describing the case it names")

	// And the gate says no, because nothing is listening on TCP.
	err = branchReady(t, ctx, envID, 5*time.Second)
	require.Error(t, err,
		"the readiness gate accepted a server listening on no TCP port, which is the state the entrypoint passes through while it initialises a data directory")
	require.Contains(t, err.Error(), "no answer over TCP",
		"the refusal does not say what it could not reach")
}
