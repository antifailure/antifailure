package env

import (
	"context"
	"fmt"
	"io"
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
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/fault"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The wiring between a manifest and the durability proof, against a real
// database.
//
// Everything else in this package's chaos tests drives a translation from
// values, and translations are exactly what those tests should drive. What
// they cannot reach is crashProof, which is where a manifest's own invariants
// are handed to the proof and where what the proof said is carried back to the
// report. Those are three lines, none of them is a translation, and all three
// have the shape this repository keeps finding in its own instruments: present,
// compiling, and connected to nothing. A test that drove the translation alone
// would pass with the manifest's invariants never once reaching the proof.

const (
	chaosInvImage = "postgres:17-alpine"
	chaosInvUser  = "antifailure"
	chaosInvDB    = "antifailure"
	chaosInvPass  = "antifailure-local-only"
	chaosInvData  = "/var/lib/antifailure/pgdata"
)

// chaosInvDocker returns a client, or skips when this machine has no daemon.
//
// AF_REQUIRE_DOCKER turns an absent daemon into a failure rather than a skip,
// for the reason engine/internal/pgcrash gives: without it a runner whose
// daemon was unavailable would skip, the package would print ok, and a wiring
// check that measured nothing would read exactly like one that measured
// everything and found it good.
func chaosInvDocker(t *testing.T) *client.Client {
	t.Helper()
	required := os.Getenv("AF_REQUIRE_DOCKER") != ""
	if os.Getenv("AF_SKIP_DOCKER") != "" && !required {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	cli, err := dockerutil.Client()
	if err != nil {
		if required {
			t.Fatalf("AF_REQUIRE_DOCKER is set, so this cannot be skipped: no Docker daemon is configured: %v", err)
		}
		t.Skipf("skipped: no Docker daemon is configured: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Fatalf("a Docker daemon is configured and will not answer a ping, which is a failure and not an absence: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

// chaosInvOnce makes a Postgres container serve exactly ONCE, which is what
// turns "the database did not come back" from something these tests WAIT for
// into something they MAKE happen.
//
// What it replaces was a recovery timeout of 1ms, on the premise that no
// container restart can beat it, so the proof would always end in "the
// database did not answer". That premise is false, and CI proved it: in engine
// job 107949572824 the first of the two live tests below failed with "the
// database answered within 1ms" while the second one, the same fault against
// the same image in the same run on the same machine, passed. Two verdicts for
// one mechanism is a race, and the 1ms was never the window. waitReady in
// engine/internal/pgcrash makes one connection attempt and consults its
// deadline only AFTER that attempt, so the timeout bounds the RETRIES rather
// than the first try; and Verify stops its writers between the undo and
// waitReady, which is a grace of up to 2s that the deadline never sees at all.
// A Postgres that shut down cleanly is back inside that window on a fast
// runner and is not on a loaded one, so the test's verdict was a property of
// the machine rather than of the code.
//
// So the container refuses to serve a second time instead. The first start is
// an ordinary Postgres, with the user's own table and the row that already
// breaks the invariant. The fault stops it, the undo starts it again, the
// marker is there, and it exits at once: there is no database to answer, on
// any machine, at any speed. The assertion that the proof ended in an error
// can still say no, and now it says no about the fixture rather than about the
// runner's clock.
//
// The marker sits at the filesystem root because that is the container's own
// writable layer, which a restart keeps and which no volume this image
// declares can shadow.
const chaosInvOnce = `if [ -e /af-chaosinv-served ]; then
	echo 'af-chaosinv: this database served once and does not come back' >&2
	exit 70
fi
touch /af-chaosinv-served
exec docker-entrypoint.sh postgres`

// chaosInvDatabase starts one labelled Postgres this test owns, and returns the
// environment id the injector is built on and the URL to reach it.
//
// Labelled as this environment's own, because the injector resolves its target
// from the labels the runtime stamped and refuses everything else. The blast
// radius this feature promises has to hold for its own tests first.
func chaosInvDatabase(t *testing.T, cli *client.Client, envID string) string {
	t.Helper()
	ctx := t.Context()

	// Pulled if it is not here, and its failure left to the create below:
	// a daemon with the image cached answers this with an error on some
	// versions and creating the container is the real question either way.
	if rc, err := cli.ImagePull(ctx, chaosInvImage, client.ImagePullOptions{}); err == nil {
		_, _ = io.Copy(io.Discard, rc)
		_ = rc.Close()
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())

	hostPort := network.MustParsePort("5432/tcp")
	resp, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  chaosInvImage,
			Labels: dockerutil.Managed("branch", envID, time.Now()),
			Env: []string{
				"POSTGRES_PASSWORD=" + chaosInvPass,
				"POSTGRES_USER=" + chaosInvUser,
				"POSTGRES_DB=" + chaosInvDB,
				"PGDATA=" + chaosInvData,
			},
			ExposedPorts: network.PortSet{hostPort: struct{}{}},
			// Served once, for the reason chaosInvOnce gives at length: what
			// these tests are about is a database that does not come back, and
			// a database that does not come back has to be built rather than
			// hoped for.
			Entrypoint: []string{"/bin/sh", "-c"},
			Cmd:        []string{chaosInvOnce},
		},
		HostConfig: &container.HostConfig{
			PortBindings: network.PortMap{hostPort: []network.PortBinding{{
				HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: strconv.Itoa(port),
			}}},
			RestartPolicy: container.RestartPolicy{Name: "no"},
			ShmSize:       256 << 20,
		},
		Name: fmt.Sprintf("af-chaosinv-%s-%d", envID, port),
	})
	require.NoError(t, err, "creating the test database container")
	// Removed by the id this test created, never by a name, and with
	// context.Background so a cancelled test still cleans up.
	t.Cleanup(func() { _ = dockerutil.RemoveContainer(context.Background(), cli, resp.ID) })
	_, err = cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	require.NoError(t, err, "starting the test database container")

	// A connect timeout, because the poll that waits for a database to come
	// back has none of its own and a container that is not listening yet can
	// leave one attempt blocked past the timeout the manifest declared.
	url := fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable&connect_timeout=2",
		chaosInvUser, chaosInvPass, port, chaosInvDB)

	deadline := time.Now().Add(2 * time.Minute)
	var last error
	for time.Now().Before(deadline) {
		conn, err := pgx.Connect(ctx, url)
		if err == nil {
			_ = conn.Close(context.Background())
			return url
		}
		last = err
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("the test database did not answer within 2m: %v", last)
	return ""
}

// chaosInvExec runs one fixture statement and fails if it did not land,
// because a fixture that silently did not apply makes every assertion after it
// meaningless.
func chaosInvExec(t *testing.T, url, sql string) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()
	_, err = conn.Exec(t.Context(), sql)
	require.NoErrorf(t, err, "the fixture statement did not run: %s", sql)
}

// chaosInvOrchestrator is the orchestrator crashProof reads, with the two
// fields it reads set and nothing else, so a step that started reading
// something else panics here rather than passing against a zero value.
func chaosInvOrchestrator(t *testing.T, invs []schema.Invariant) *Orchestrator {
	t.Helper()
	return &Orchestrator{
		progress: func(line string) { t.Log(line) },
		opts: Options{
			Manifest: &schema.Manifest{Invariants: invs},
			Redactor: redact.New(),
		},
	}
}

// TestCrashProofLive_TheManifestsInvariantsReachTheProofAndComeBack is the
// wiring, end to end, against a Postgres that really stops.
//
// It is the one ordering where every link in the chain has to hold at once and
// the run still ends in an error: the database is stopped, the undo starts it
// again, and it does not come back, so the proof fails and the arm has to
// survive that. That it does not come back is made to happen rather than
// waited for, which is what chaosInvOnce is and why. What it proves, in order:
//
//   - the manifest's invariants reach the proof, because the before side was
//     asked and answered against the user's own table
//   - what the proof said reaches the report entry, because the entry carries
//     the arm rather than an empty list
//   - the proof itself is handed back on the error path, because that is the
//     only way the finding reaches the gate, and a run that returned nothing
//     here would report the project's rules as having nothing to say at the
//     exact moment they have the most to say
func TestCrashProofLive_TheManifestsInvariantsReachTheProofAndComeBack(t *testing.T) {
	cli := chaosInvDocker(t)
	envID := "civ" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	url := chaosInvDatabase(t, cli, envID)

	chaosInvExec(t, url, "CREATE TABLE accounts (id int PRIMARY KEY, balance int NOT NULL)")
	chaosInvExec(t, url, "INSERT INTO accounts VALUES (1, 10), (2, -3)")

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)

	o := chaosInvOrchestrator(t, []schema.Invariant{{
		Name: "no-negative-balance", Description: "no account may go negative",
		SQL: "SELECT id FROM accounts WHERE balance < 0",
	}})
	declared := schema.Fault{
		Name: "stop-the-database", Kind: schema.FaultKind(fault.KindContainerStop),
		Target: schema.FaultTargetDatabase, Hold: "1s",
	}
	// A recovery window a real database would be given, and it still runs out,
	// because chaosInvOnce means there is nothing left to answer. It used to be
	// 1ms and the 1ms used to be the whole mechanism, which made the verdict a
	// property of how fast the runner restarts a container.
	cr := &schema.CrashRecovery{
		Enabled: boolPtr(true), Writers: 2, CommitsBeforeFault: 10,
		RecoveryTimeout: "2s",
	}

	entry, proof := o.crashProof(t.Context(), inj, url, declared, cr,
		report.ChaosFault{Name: declared.Name}, time.Now())

	// The fault went in and the database never came back.
	require.True(t, entry.Injected, "the fault never went in, so this test measured nothing")
	require.NotEmpty(t, entry.Error,
		"the proof succeeded, so the database came back and this test is not about the run it was written for: chaosInvOnce did not stop it serving a second time")
	// The recovery timeout is what ran out, named by its own code rather than
	// by its sentence. Without this the assertion above is satisfied by any
	// error at all, including an undo that failed before the proof ever
	// reached the question this test is about.
	require.Contains(t, entry.Error, string(aferrors.AFCHS006),
		"the run failed for some other reason than the database not coming back")

	// The manifest's invariants reached the proof and were asked BEFORE it.
	require.Len(t, entry.Invariants, 1, "the arm never reached the report entry")
	got := entry.Invariants[0]
	require.Equal(t, "no-negative-balance", got.Name)
	require.Equal(t, "no account may go negative", got.Description)
	require.Empty(t, got.BeforeError, "the invariant was never asked before the fault")
	require.False(t, got.BeforeHeld, "the row that already breaks this rule was not seen before the fault")

	// And it could not be asked after, which is the honest answer and not a
	// pass.
	require.NotEmpty(t, got.AfterError, "an invariant nobody could ask was reported as a verdict")
	// And for the reason this test is about. An invariant whose statement was
	// refused by a database that is answering fine would also leave this side
	// with an error, and that is a different run with a different meaning.
	require.Contains(t, got.AfterError, "the database did not answer a query after the fault",
		"the invariant was not asked for some other reason than the database being gone")
	require.False(t, got.AfterHeld)

	// The proof is handed back on the error path, which is the only route the
	// finding has to the gate.
	require.NotNil(t, proof, "the proof was dropped, so the invariant arm never reaches a finding")
	findings := ChaosFindings(entry, proof, report.Configure(nil))
	rules := make([]string, 0, len(findings))
	for _, f := range findings {
		rules = append(rules, f.Rule)
	}
	require.Contains(t, rules, "chaos.invariant.unevaluated",
		"a database that never came back reported the project's own rules as nothing to see")
}

// TestCrashProofLive_AManifestWithNoInvariantsIsUnchanged is the requirement
// that a project which declares none sees no difference at all: no arm in the
// entry, and the proof still dropped on the error path the way it always was.
func TestCrashProofLive_AManifestWithNoInvariantsIsUnchanged(t *testing.T) {
	cli := chaosInvDocker(t)
	envID := "cin" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	url := chaosInvDatabase(t, cli, envID)

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)

	o := chaosInvOrchestrator(t, nil)
	declared := schema.Fault{
		Name: "stop-the-database", Kind: schema.FaultKind(fault.KindContainerStop),
		Target: schema.FaultTargetDatabase, Hold: "1s",
	}
	cr := &schema.CrashRecovery{
		Enabled: boolPtr(true), Writers: 2, CommitsBeforeFault: 10,
		RecoveryTimeout: "2s",
	}

	entry, proof := o.crashProof(t.Context(), inj, url, declared, cr,
		report.ChaosFault{Name: declared.Name}, time.Now())

	require.NotEmpty(t, entry.Error,
		"the proof succeeded, so the database came back and this test is not about the run it was written for: chaosInvOnce did not stop it serving a second time")
	require.Empty(t, entry.Invariants, "an arm appeared for a manifest that declares no invariants")
	require.Nil(t, proof, "the error path stopped dropping the proof for a project that asked for nothing")
	require.Empty(t, ChaosFindings(entry, proof, report.Configure(nil))[1:],
		"a project with no invariants gained a finding it did not have before")
}

// boolPtr is the pointer a crash_recovery block's enabled flag is, spelled
// here because the manifest package's own helper is not exported.
func boolPtr(b bool) *bool { return &b }
