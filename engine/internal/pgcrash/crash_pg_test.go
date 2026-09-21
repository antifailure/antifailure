package pgcrash_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/fault"
	"github.com/antifailure/antifailure/engine/internal/pgcrash"
)

// This file crashes real databases. Everything it starts is labelled as this
// suite's own and is removed on the way out, and nothing here ever addresses a
// container by a name it did not itself create: the machine this runs on holds
// other people's Postgres containers, and the blast radius this package
// promises has to hold for its own tests first.

const (
	pgImage  = "postgres:17-alpine"
	pgUser   = "antifailure"
	pgDB     = "antifailure"
	pgPass   = "antifailure-local-only"
	pgData   = "/var/lib/antifailure/pgdata"
	testKind = "branch"
)

// requireDocker returns a client, or skips when this machine has no daemon.
//
// AF_REQUIRE_DOCKER turns an absent daemon into a failure rather than a skip,
// and the engine job in ci.yml sets it. That one line is the difference
// between this suite proving something in CI and looking like it did: without
// it, a runner whose daemon was unavailable would skip every crash proof, the
// package would print ok, and a durability check that measured nothing would
// read exactly like one that measured everything and found it good. That is
// the failure mode this whole feature exists to catch in somebody else's
// system, so it is not one to leave in ours.
//
// A configured daemon that will not answer a ping is a failure either way, for
// the reason engine/chaos gives: reporting a broken daemon as "no Docker" is
// how a suite that tested nothing reports success.
func requireDocker(t *testing.T) *client.Client {
	t.Helper()
	// AF_REQUIRE_DOCKER wins over AF_SKIP_DOCKER, because the one that
	// refuses to be silent has to beat the one that asks for silence.
	required := os.Getenv("AF_REQUIRE_DOCKER") != ""
	if os.Getenv("AF_SKIP_DOCKER") != "" && !required {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	cli, err := dockerutil.Client()
	if err != nil {
		if required {
			t.Fatalf("AF_REQUIRE_DOCKER is set, so this cannot be skipped: "+
				"no Docker daemon is configured: %v", err)
		}
		t.Skipf("skipped: no Docker daemon is configured: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		_ = cli.Close()
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the Docker daemon is configured and did not answer a ping in two minutes; "+
				"that is a broken environment rather than an absent one: %v", err)
		}
		if required {
			t.Fatalf("AF_REQUIRE_DOCKER is set, so this cannot be skipped: "+
				"no Docker daemon is reachable: %v", err)
		}
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

// ensureImage pulls the image this suite starts containers from, when the
// daemon does not already have it.
//
// It is here because a fresh CI runner has no images at all, and a create
// against an absent image fails with "No such image" a full second before
// anything this suite is about has run. The product's own provider does the
// same thing for the same reason, and a test that assumed the image was there
// went red on a runner rather than on a defect.
func ensureImage(t *testing.T, cli *client.Client, ref string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	present, err := dockerutil.ImagePresent(ctx, cli, ref)
	require.NoError(t, err, "asking the daemon about %s", ref)
	if present {
		return
	}
	rc, err := cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	require.NoErrorf(t, err, "pulling %s", ref)
	// Drained before the pull counts as finished, or the next call inspects an
	// image that is only partly there.
	dockerutil.Discard(rc)
	present, err = dockerutil.ImagePresent(ctx, cli, ref)
	require.NoErrorf(t, err, "asking the daemon about %s after pulling it", ref)
	require.Truef(t, present, "%s is still absent after pulling it", ref)
}

// database is one Postgres this suite started.
type database struct {
	envID string
	id    string
	name  string
	url   string
}

// startDatabase runs a Postgres container labelled as belonging to envID, and
// waits until it answers.
//
// extraEnv is appended to the container's environment, which is how a test
// asks for a cluster initialised differently, with data checksums on.
func startDatabase(t *testing.T, cli *client.Client, envID, kind, service string, extraEnv ...string) database {
	t.Helper()
	ctx := t.Context()

	ensureImage(t, cli, pgImage)

	labels := dockerutil.Managed(kind, envID, time.Now())
	if service != "" {
		labels[dockerutil.LabelService] = service
	}
	port := freePort(t)
	name := fmt.Sprintf("af-pgcrash-%s-%d", envID, port)

	hostPort := network.MustParsePort("5432/tcp")
	resp, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  pgImage,
			Labels: labels,
			Env: append([]string{
				"POSTGRES_PASSWORD=" + pgPass,
				"POSTGRES_USER=" + pgUser,
				"POSTGRES_DB=" + pgDB,
				"PGDATA=" + pgData,
			}, extraEnv...),
			ExposedPorts: network.PortSet{hostPort: struct{}{}},
		},
		HostConfig: &container.HostConfig{
			PortBindings: network.PortMap{hostPort: []network.PortBinding{{
				HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: strconv.Itoa(port),
			}}},
			RestartPolicy: container.RestartPolicy{Name: "no"},
			ShmSize:       256 << 20,
		},
		Name: name,
	})
	require.NoError(t, err, "creating the test database container")
	t.Cleanup(func() {
		// Removed by the id this test created, never by a name, and with
		// context.Background so a cancelled test still cleans up.
		_ = dockerutil.RemoveContainer(context.Background(), cli, resp.ID)
	})
	_, err = cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	require.NoError(t, err, "starting the test database container")

	db := database{
		envID: envID, id: resp.ID, name: name,
		url: fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", pgUser, pgPass, port, pgDB),
	}
	waitUntilAnswering(t, db.url, 2*time.Minute)
	return db
}

// freePort asks the kernel for a port nothing is listening on.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

// waitUntilAnswering blocks until the database answers a query.
func waitUntilAnswering(t *testing.T, url string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	var last error
	for time.Now().Before(deadline) {
		conn, err := pgx.Connect(t.Context(), url)
		if err == nil {
			_ = conn.Close(context.Background())
			return
		}
		last = err
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("the test database did not answer within %s: %v", within, last)
}

// shell is the runner pgcrash reads evidence through, which is the same
// ownership guarded shell the product uses.
func shell(t *testing.T, cli *client.Client, db database) *fault.Shell {
	t.Helper()
	inj, err := fault.New(cli, db.envID)
	require.NoError(t, err)
	sh, err := inj.Shell(t.Context(), fault.Target{Role: fault.RoleDatabase})
	require.NoError(t, err)
	return sh
}

// runner adapts a fault.Shell to what pgcrash reads through.
type runner struct{ sh *fault.Shell }

func (r runner) Run(ctx context.Context, argv []string) (string, int, error) {
	out, err := r.sh.Run(ctx, argv)
	return out.Stdout, out.ExitCode, err
}

func (r runner) Logs(ctx context.Context, since time.Time) (string, error) {
	return r.sh.Logs(ctx, since)
}

// TestVerify_ASigkilledPostgresLosesNoAcknowledgedCommit is the claim the
// whole feature is for: a database killed under a concurrent write workload
// replays its write ahead log and every commit the client was told about is
// still there.
func TestVerify_ASigkilledPostgresLosesNoAcknowledgedCommit(t *testing.T) {
	cli := requireDocker(t)
	envID := "pgc" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	db := startDatabase(t, cli, envID, testKind, "")

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	sh := shell(t, cli, db)

	f := fault.Fault{
		Name:    "postgres-crash",
		Kind:    fault.KindProcessKill,
		Target:  fault.Target{Role: fault.RoleDatabase},
		Process: "postgres: checkpointer",
	}

	res, err := pgcrash.Verify(t.Context(), pgcrash.Options{
		URL: db.url, Runner: runner{sh}, DataDir: pgData,
		Workload:    pgcrash.WorkloadOptions{Writers: 8},
		WarmCommits: 300, WarmTimeout: 60 * time.Second,
		Settle: 2 * time.Second, ReadyTimeout: 2 * time.Minute,
		ExpectCrash: true, FaultName: f.Name,
		Inject: func(ctx context.Context) (pgcrash.Injected, error) {
			in, err := inj.Inject(ctx, f)
			if err != nil {
				return pgcrash.Injected{}, err
			}
			return pgcrash.Injected{Evidence: in.Evidence, KilledSignal: in.KilledSignal}, nil
		},
	})
	require.NoError(t, err)
	report(t, res)

	// The crash happened. Each of these is a separate assertion because each
	// one fails for a different reason, and a single compound assertion would
	// stop at the first and say nothing about the rest.
	require.True(t, res.Recovery.Crashed, "the log carries no process killed by a signal, so nothing crashed")
	require.Equal(t, 9, res.Recovery.Signal, "the process died on a signal other than SIGKILL")
	require.Zero(t, res.KilledSignal,
		"the container itself died, so this run measured a node going away rather than a process crashing inside one")
	require.True(t, res.Recovery.Reinitialised, "the postmaster did not say it was reinitialising")
	require.True(t, res.Recovery.Unclean, "the startup process did not record an unclean shutdown")

	// The write ahead log replayed, and it replayed from where the control
	// file said it would.
	require.True(t, res.Recovery.Replayed(), "the log carries no redo start and end")
	start, err := pgcrash.ParseLSN(res.Recovery.RedoStart)
	require.NoError(t, err)
	end, err := pgcrash.ParseLSN(res.Recovery.RedoEnd)
	require.NoError(t, err)
	require.Greater(t, end, start, "recovery ended at or before where it started, so it replayed nothing")
	wantFrom, err := pgcrash.ParseLSN(res.Before.RedoLSN)
	require.NoError(t, err)
	require.GreaterOrEqual(t, start, wantFrom,
		"recovery started before the checkpoint the control file named")

	// The control file moved through the states it has to move through.
	require.Equal(t, "in production", res.Before.State)
	require.Equal(t, "in production", res.After.State)
	require.Equal(t, res.Before.TimeLine, res.After.TimeLine, "crash recovery moved the timeline")
	afterCheckpoint, err := pgcrash.ParseLSN(res.After.CheckpointLSN)
	require.NoError(t, err)
	beforeCheckpoint, err := pgcrash.ParseLSN(res.Before.CheckpointLSN)
	require.NoError(t, err)
	require.Greater(t, afterCheckpoint, beforeCheckpoint,
		"the checkpoint did not move, so the end of recovery checkpoint never happened")

	// The durability claim.
	require.Greater(t, res.Reconciliation.Acknowledged, 300,
		"too few commits were acknowledged for the run to mean anything")
	require.Zero(t, res.Reconciliation.LostCount,
		"acknowledged commits were lost: %v", res.Reconciliation.LostSample)
	require.Zero(t, res.Reconciliation.PhantomCount,
		"rows appeared that no client wrote: %v", res.Reconciliation.PhantomSample)
	require.True(t, res.Reconciliation.Consistent, "the reconciliation does not add up")

	// The relations survived.
	require.True(t, res.Relations.Checked, "the relations could not be checked: %s", res.Relations.Why)
	require.True(t, res.Relations.Agreed,
		"the heap counted %d and the index counted %d", res.Relations.HeapRows, res.Relations.IndexRows)

	require.Empty(t, problemRules(res.Problems), "the run reported problems")
}

// TestVerify_ReportsALostCommitWhenOneIsGenuinelyLost is the arm that proves
// the durability check can say no.
//
// With synchronous_commit off, Postgres acknowledges a commit before its write
// ahead log record has reached the operating system, so a crash that discards
// shared memory loses acknowledged commits for real. Nothing is simulated
// here: the same fault, the same assertions, one setting different, and the
// check reports the loss. A check that has only ever been green has not been
// shown able to be anything else.
func TestVerify_ReportsALostCommitWhenOneIsGenuinelyLost(t *testing.T) {
	cli := requireDocker(t)
	envID := "pgl" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	db := startDatabase(t, cli, envID, testKind, "")

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	sh := shell(t, cli, db)

	res, err := pgcrash.Verify(t.Context(), pgcrash.Options{
		URL: db.url, Runner: runner{sh}, DataDir: pgData,
		Workload: pgcrash.WorkloadOptions{Writers: 8, SynchronousCommit: "off"},
		// More commits than the durable arm, because the window of loss is the
		// write ahead log still in shared buffers and it takes a busy workload
		// to have anything in it.
		WarmCommits: 2000, WarmTimeout: 90 * time.Second,
		Settle: 2 * time.Second, ReadyTimeout: 2 * time.Minute,
		ExpectCrash: true, FaultName: "postgres-crash-async",
		Inject: func(ctx context.Context) (pgcrash.Injected, error) {
			in, err := inj.Inject(ctx, fault.Fault{
				Name: "postgres-crash-async", Kind: fault.KindProcessKill,
				Target: fault.Target{Role: fault.RoleDatabase}, Process: "postgres: checkpointer",
			})
			if err != nil {
				return pgcrash.Injected{}, err
			}
			return pgcrash.Injected{Evidence: in.Evidence, KilledSignal: in.KilledSignal}, nil
		},
	})
	require.NoError(t, err)
	report(t, res)

	require.True(t, res.Crashed(), "the same fault did not crash this database")
	require.Positive(t, res.Reconciliation.LostCount,
		"synchronous_commit was off and a SIGKILL discarded shared memory, and the check found no lost commit; "+
			"either the loss did not happen on this machine or the check cannot see one")
	require.Contains(t, problemRules(res.Problems), pgcrash.RuleLostCommit,
		"the lost commits were counted and no finding was raised for them")
	require.False(t, res.Held(), "the run reported a lost commit and still held")
	// The loss must be reported as loss, not as invention.
	require.Zero(t, res.Reconciliation.PhantomCount, "a lost commit was reported as a phantom")
	require.True(t, res.Reconciliation.Consistent,
		"the reconciliation stopped adding up on the arm where it matters most")
}

// TestVerify_AnUninjuredDatabaseIsNotReportedAsRecovered is the liveness arm.
//
// The same run with a fault that does nothing must come back clean AND must
// not claim a recovery it never saw. A recovery check that reports the same
// thing whether or not anything crashed is the defect this whole package is
// about, so it is tested from the side that would hide it.
func TestVerify_AnUninjuredDatabaseIsNotReportedAsRecovered(t *testing.T) {
	cli := requireDocker(t)
	envID := "pgn" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	db := startDatabase(t, cli, envID, testKind, "")
	sh := shell(t, cli, db)

	res, err := pgcrash.Verify(t.Context(), pgcrash.Options{
		URL: db.url, Runner: runner{sh}, DataDir: pgData,
		Workload:    pgcrash.WorkloadOptions{Writers: 4},
		WarmCommits: 200, WarmTimeout: 60 * time.Second,
		Settle: time.Second, ReadyTimeout: time.Minute,
		ExpectCrash: false, FaultName: "no-fault",
		Inject: func(context.Context) (pgcrash.Injected, error) {
			return pgcrash.Injected{Evidence: "no fault was injected"}, nil
		},
	})
	require.NoError(t, err)
	report(t, res)

	require.False(t, res.Crashed(), "a run with no fault reported a crash")
	require.False(t, res.Recovery.Unclean, "a run with no fault reported an unclean shutdown")
	require.Empty(t, res.Recovery.RedoStart, "a run with no fault reported a replay")
	require.Zero(t, res.Reconciliation.LostCount)
	require.Zero(t, res.Reconciliation.PhantomCount)
	require.True(t, res.Held(), "an undisturbed database did not hold: %v", problemRules(res.Problems))
	require.NotContains(t, problemRules(res.Unverified), pgcrash.RuleNoCrash,
		"a run that expected no crash was reported as one that failed to crash")
}

// TestVerify_ACrashThatNeverHappenedIsUnverifiedRatherThanPassed is the third
// arm, and the one that catches the instrument rather than the database.
//
// The fault is a container pause, which stalls the database and crashes
// nothing, declared with ExpectCrash. Everything downstream then describes a
// database that never broke, and the run has to say so rather than report a
// clean recovery.
func TestVerify_ACrashThatNeverHappenedIsUnverifiedRatherThanPassed(t *testing.T) {
	cli := requireDocker(t)
	envID := "pgp" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	db := startDatabase(t, cli, envID, testKind, "")

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	sh := shell(t, cli, db)

	var injection *fault.Injection
	res, err := pgcrash.Verify(t.Context(), pgcrash.Options{
		URL: db.url, Runner: runner{sh}, DataDir: pgData,
		Workload:    pgcrash.WorkloadOptions{Writers: 4},
		WarmCommits: 200, WarmTimeout: 60 * time.Second,
		Settle: 2 * time.Second, ReadyTimeout: time.Minute,
		ExpectCrash: true, FaultName: "pause-not-a-crash",
		Inject: func(ctx context.Context) (pgcrash.Injected, error) {
			in, err := inj.Inject(ctx, fault.Fault{
				Name: "pause-not-a-crash", Kind: fault.KindContainerPause,
				Target: fault.Target{Role: fault.RoleDatabase},
			})
			if err != nil {
				return pgcrash.Injected{}, err
			}
			injection = in
			return pgcrash.Injected{Evidence: in.Evidence, KilledSignal: in.KilledSignal}, nil
		},
		Recover: func(ctx context.Context) error { return injection.Undo(ctx) },
	})
	require.NoError(t, err)
	report(t, res)

	require.False(t, res.Crashed(), "a pause killed a process")
	require.Contains(t, problemRules(res.Unverified), pgcrash.RuleNoCrash,
		"a fault that was declared as a crash and crashed nothing was not reported as unverified")
	require.False(t, res.Verified(), "a run that never crashed anything reported itself verified")
	// And it must still be honest about the data: a pause loses nothing.
	require.Zero(t, res.Reconciliation.LostCount, "a pause lost acknowledged commits")
}

// TestVerify_AKilledContainerComesBackAndKeepsItsCommits is the node level
// fault rather than the process level one: the container's main process is
// killed, the container stops, and the undo starts it again.
func TestVerify_AKilledContainerComesBackAndKeepsItsCommits(t *testing.T) {
	cli := requireDocker(t)
	envID := "pgk" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	db := startDatabase(t, cli, envID, testKind, "")

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	sh := shell(t, cli, db)

	var injection *fault.Injection
	res, err := pgcrash.Verify(t.Context(), pgcrash.Options{
		URL: db.url, Runner: runner{sh}, DataDir: pgData,
		Workload:    pgcrash.WorkloadOptions{Writers: 8},
		WarmCommits: 300, WarmTimeout: 60 * time.Second,
		Settle: 2 * time.Second, ReadyTimeout: 2 * time.Minute,
		ExpectCrash: true, FaultName: "node-down",
		Inject: func(ctx context.Context) (pgcrash.Injected, error) {
			in, err := inj.Inject(ctx, fault.Fault{
				Name: "node-down", Kind: fault.KindContainerKill,
				Target: fault.Target{Role: fault.RoleDatabase},
			})
			if err != nil {
				return pgcrash.Injected{}, err
			}
			injection = in
			return pgcrash.Injected{Evidence: in.Evidence, KilledSignal: in.KilledSignal}, nil
		},
		Recover: func(ctx context.Context) error { return injection.Undo(ctx) },
	})
	require.NoError(t, err)
	report(t, res)

	require.Contains(t, res.Evidence, "exit code 137",
		"a SIGKILLed container did not exit 137, so the kill is not what stopped it")
	// The postmaster cannot log its own death, so the log carries no signal
	// line here and the crash is proved from the container's exit status. A
	// check that read only the log would call the most complete crash it can
	// cause no crash at all.
	require.False(t, res.Recovery.Crashed,
		"a killed postmaster wrote a log line about its own death, which would mean this assertion is testing something else")
	require.Equal(t, 9, res.KilledSignal, "the container's exit status does not record a SIGKILL")
	require.True(t, res.Crashed(), "a container killed with SIGKILL was not recorded as a crash")
	require.NotContains(t, problemRules(res.Unverified), pgcrash.RuleNoCrash,
		"a real node level crash was reported as one that never happened")
	require.True(t, res.Recovery.Unclean,
		"the database came back without recording the unclean shutdown a killed container causes")
	require.True(t, res.Recovery.Ready, "the database never said it was accepting connections again")
	require.Zero(t, res.Reconciliation.LostCount,
		"a killed container lost acknowledged commits: %v", res.Reconciliation.LostSample)
	require.Zero(t, res.Reconciliation.PhantomCount)
	require.Positive(t, res.Downtime, "the database was never unreachable")
}

// TestVerify_ACleanStopDoesNoRecoveryAndSaysSo is the contrast that makes the
// crash arm mean something.
//
// A container that is stopped rather than killed shuts Postgres down cleanly,
// so there is no replay on the way back. Declared as a crash, the run reports
// that it could not establish one, which is what separates this instrument
// from one that calls every successful reconnection a recovery.
func TestVerify_ACleanStopDoesNoRecoveryAndSaysSo(t *testing.T) {
	cli := requireDocker(t)
	envID := "pgs" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	db := startDatabase(t, cli, envID, testKind, "")

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	sh := shell(t, cli, db)

	var injection *fault.Injection
	res, err := pgcrash.Verify(t.Context(), pgcrash.Options{
		URL: db.url, Runner: runner{sh}, DataDir: pgData,
		Workload:    pgcrash.WorkloadOptions{Writers: 4},
		WarmCommits: 200, WarmTimeout: 60 * time.Second,
		Settle: time.Second, ReadyTimeout: 2 * time.Minute,
		ExpectCrash: true, FaultName: "clean-stop",
		Inject: func(ctx context.Context) (pgcrash.Injected, error) {
			in, err := inj.Inject(ctx, fault.Fault{
				Name: "clean-stop", Kind: fault.KindContainerStop,
				Target: fault.Target{Role: fault.RoleDatabase},
			})
			if err != nil {
				return pgcrash.Injected{}, err
			}
			injection = in
			return pgcrash.Injected{Evidence: in.Evidence, KilledSignal: in.KilledSignal}, nil
		},
		Recover: func(ctx context.Context) error { return injection.Undo(ctx) },
	})
	require.NoError(t, err)
	report(t, res)

	require.False(t, res.Verified(),
		"a clean shutdown replays nothing, and the run reported itself as having verified a recovery")
	require.Zero(t, res.Reconciliation.LostCount, "a clean shutdown lost acknowledged commits")
}

// TestCheckRelations_ATornPageOnAChecksummedClusterStopsTheReadBack is the
// premise the terminal's pages line rests on. That line claims no page of the
// writers' table failed its checksum whenever the cluster has checksums on and
// the read back finished, which is only true if a page that DOES fail stops
// the read back rather than being counted through. So a page is damaged on
// disk, under a cluster initialised with checksums on, and the read back has
// to stop at it and leave amcheck unasked, which is what makes the line say
// not checked instead of a pass.
//
// The same table is read back once before the damage, and must pass, so the
// refusal below is about the page and not about a read back that could never
// have succeeded here.
func TestCheckRelations_ATornPageOnAChecksummedClusterStopsTheReadBack(t *testing.T) {
	cli := requireDocker(t)
	envID := "pgt" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	db := startDatabase(t, cli, envID, testKind, "", "POSTGRES_INITDB_ARGS=--data-checksums")
	sh := shell(t, cli, db)
	ctx := t.Context()

	out, err := sh.Run(ctx, []string{"pg_controldata", "-D", pgData})
	require.NoError(t, err)
	control, err := pgcrash.ParseControl(out.Stdout)
	require.NoError(t, err)
	require.True(t, control.ChecksumsEnabled(),
		"the cluster came up without data checksums, so nothing below tests a checksum")

	// The writers' table, by the name the proof reads, filled past one page
	// and frozen, then checkpointed. Frozen so that no later read sets a hint
	// bit and dirties the page, and checkpointed so the page on disk is the
	// one the damage lands on and nothing in memory writes over it.
	conn, err := pgx.Connect(ctx, db.url)
	require.NoError(t, err)
	for _, stmt := range []string{
		"CREATE SCHEMA antifailure_chaos",
		"CREATE TABLE antifailure_chaos.commits (id bigint PRIMARY KEY, payload text NOT NULL)",
		"INSERT INTO antifailure_chaos.commits SELECT g, repeat('x', 64) FROM generate_series(1, 2000) g",
		"VACUUM (FREEZE) antifailure_chaos.commits",
		"CHECKPOINT",
	} {
		_, err := conn.Exec(ctx, stmt)
		require.NoError(t, err, stmt)
	}
	var path string
	require.NoError(t, conn.QueryRow(ctx,
		"SELECT pg_relation_filepath('antifailure_chaos.commits')").Scan(&path))
	require.NoError(t, conn.Close(ctx))

	intact := pgcrash.CheckRelationsForTest(ctx, db.url)
	t.Logf("intact: checked=%v heap=%d index=%d amcheck=%q why=%q",
		intact.Checked, intact.HeapRows, intact.IndexRows, intact.Amcheck, intact.Why)
	require.True(t, intact.Checked, "the undamaged table could not be read back: %s", intact.Why)
	require.EqualValues(t, 2000, intact.HeapRows)

	// Eight bytes in the middle of the second page, written straight into the
	// relation's file. The bytes are what a torn write leaves: a page whose
	// contents no longer match the checksum Postgres stored in its header.
	out, err = sh.Run(ctx, []string{"sh", "-c",
		"printf 'TORNPAGE' | dd of=" + pgData + "/" + path + " bs=1 seek=12288 conv=notrunc"})
	require.NoError(t, err)
	require.Zero(t, out.ExitCode, "the page could not be damaged: %s", out.Stdout)

	// A restart empties shared buffers, so the next read comes from the file.
	_, err = cli.ContainerRestart(ctx, db.id, client.ContainerRestartOptions{})
	require.NoError(t, err)
	waitUntilAnswering(t, db.url, 2*time.Minute)

	torn := pgcrash.CheckRelationsForTest(ctx, db.url)
	t.Logf("torn: checked=%v heap=%d index=%d amcheck=%q why=%q",
		torn.Checked, torn.HeapRows, torn.IndexRows, torn.Amcheck, torn.Why)
	require.False(t, torn.Checked, "a table with a page that fails its checksum was read back as checked")
	require.Empty(t, torn.Amcheck,
		"amcheck was asked after the read back met a bad page, so the pages line would print a pass")
	require.Contains(t, torn.Why, "invalid page",
		"the read back stopped for a reason other than the damaged page")
}

// report prints what a run established, so a failing assertion is read beside
// the evidence rather than beside a boolean.
func report(t *testing.T, res pgcrash.Result) {
	t.Helper()
	rec := res.Reconciliation
	t.Logf("fault=%q evidence=%q", res.Fault, res.Evidence)
	t.Logf("control before: state=%q checkpoint=%s redo=%s timeline=%d checksums=%d",
		res.Before.State, res.Before.CheckpointLSN, res.Before.RedoLSN, res.Before.TimeLine, res.Before.ChecksumVersion)
	t.Logf("control after:  state=%q checkpoint=%s redo=%s timeline=%d",
		res.After.State, res.After.CheckpointLSN, res.After.RedoLSN, res.After.TimeLine)
	t.Logf("crash proved=%v via log=%v via exit status=%d", res.Crashed(), res.Recovery.Crashed, res.KilledSignal)
	t.Logf("recovery: crashed=%v signal=%d reinitialised=%v unclean=%v redo=%s..%s ready=%v",
		res.Recovery.Crashed, res.Recovery.Signal, res.Recovery.Reinitialised,
		res.Recovery.Unclean, res.Recovery.RedoStart, res.Recovery.RedoEnd, res.Recovery.Ready)
	t.Logf("ledger: acknowledged=%d unresolved=%d present=%d lost=%d phantom=%d inFlightLanded=%d consistent=%v flush=%s",
		rec.Acknowledged, rec.Unresolved, rec.Present, rec.LostCount, rec.PhantomCount,
		rec.UnresolvedLanded, rec.Consistent, res.FlushLSN)
	t.Logf("relations: checked=%v agreed=%v heap=%d index=%d amcheck=%q why=%q",
		res.Relations.Checked, res.Relations.Agreed, res.Relations.HeapRows,
		res.Relations.IndexRows, res.Relations.Amcheck, res.Relations.Why)
	t.Logf("downtime=%s writeErrors=%d lastWriteError=%q", res.Downtime, res.WriteErrors, res.LastWriteError)
	for _, p := range res.Problems {
		t.Logf("PROBLEM    %s: %s | %s", p.Rule, p.Title, p.Detail)
	}
	for _, p := range res.Unverified {
		t.Logf("UNVERIFIED %s: %s | %s", p.Rule, p.Title, p.Detail)
	}
	for _, line := range res.Recovery.Lines {
		t.Logf("log | %s", line)
	}
}

// problemRules is the rules a list of problems carries.
func problemRules(ps []pgcrash.Problem) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Rule)
	}
	return out
}
