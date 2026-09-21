package fault_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/fault"
)

// The blast radius is the one claim this package makes that cannot be argued
// from reading it, because the thing being claimed is what does NOT happen.
// So every refusal in here is driven at a real container on a real daemon, and
// each one is paired with a liveness arm: an identical call that must be
// ALLOWED. A refusal with no liveness arm beside it would pass just as well if
// the injector refused everything, which is a control that is gone rather than
// a control that is working.

// sleeperImage is what every container in this file runs. Small, present in
// every registry mirror, and the command is overridden anyway.
const sleeperImage = "alpine:3"

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

// sleeper starts a container that does nothing but stay up, with the labels it
// is given.
//
// A shell loop rather than a database, because every refusal in this file is
// about the labels and not about what is inside: using Postgres would make
// each case a minute slower and would prove exactly the same thing.
func sleeper(t *testing.T, cli *client.Client, labels map[string]string) string {
	t.Helper()
	ensureImage(t, cli, sleeperImage)
	resp, err := cli.ContainerCreate(t.Context(), client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  sleeperImage,
			Labels: labels,
			Cmd:    []string{"/bin/sh", "-c", "while true; do sleep 1; done"},
		},
		HostConfig: &container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: "no"},
		},
		Name: fmt.Sprintf("af-blast-%d", time.Now().UnixNano()),
	})
	require.NoError(t, err, "creating the test container")
	t.Cleanup(func() {
		_ = dockerutil.RemoveContainer(context.Background(), cli, resp.ID)
	})
	_, err = cli.ContainerStart(t.Context(), resp.ID, client.ContainerStartOptions{})
	require.NoError(t, err, "starting the test container")
	return resp.ID
}

// ours builds the labels a container of this environment carries.
func ours(envID, kind, service string) map[string]string {
	l := dockerutil.Managed(kind, envID, time.Now())
	if service != "" {
		l[dockerutil.LabelService] = service
	}
	return l
}

// pause is the fault every case in this file uses.
//
// Reversible, visible in the container's own state, and harmless to a sleeping
// shell. The refusals being tested are about whether the fault is allowed at
// all, and using a destructive one would make a bug in a refusal destroy the
// evidence of it.
func pause(name string, target fault.Target) fault.Fault {
	return fault.Fault{Name: name, Kind: fault.KindContainerPause, Target: target}
}

func envName(prefix string) string {
	return prefix + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
}

// TestInjectInto_RefusesAContainerAntifailureDidNotCreate is the refusal that
// matters most on a shared machine: a developer's own Postgres, or another
// team's, carries no Antifailure label and must be untouchable.
func TestInjectInto_RefusesAContainerAntifailureDidNotCreate(t *testing.T) {
	cli := requireDocker(t)
	envID := envName("blastA")

	stranger := sleeper(t, cli, map[string]string{"com.example.owner": "somebody-else"})
	mine := sleeper(t, cli, ours(envID, "branch", ""))

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)

	// The refusal.
	_, err = inj.InjectInto(t.Context(), stranger, pause("at-a-stranger", fault.Target{Role: fault.RoleDatabase}))
	require.Error(t, err, "a container with no Antifailure label was accepted as a target")
	require.ErrorIs(t, err, fault.ErrNotOurs)
	requireNotPaused(t, cli, stranger, "a container Antifailure did not create was frozen")

	// The liveness arm, which is the same call on the same daemon at the same
	// moment, differing only in the labels of the container it names. Without
	// it, an injector that refused everything would pass the assertion above.
	in, err := inj.InjectInto(t.Context(), mine, pause("at-my-own", fault.Target{Role: fault.RoleDatabase}))
	require.NoError(t, err, "the injector refused a container this environment owns, so the refusal above proves nothing")
	requirePaused(t, cli, mine, "the fault was accepted and the container is not frozen")
	require.NoError(t, in.Undo(t.Context()))
	requireNotPaused(t, cli, mine, "the undo ran and the container is still frozen")
}

// TestInjectInto_RefusesAnotherEnvironmentsContainer is the refusal that keeps
// two rehearsals on one machine apart. Both containers are Antifailure's; only
// one belongs to the environment the injector is bound to.
func TestInjectInto_RefusesAnotherEnvironmentsContainer(t *testing.T) {
	cli := requireDocker(t)
	mineEnv, theirsEnv := envName("blastB"), envName("blastC")

	theirs := sleeper(t, cli, ours(theirsEnv, "branch", ""))
	mine := sleeper(t, cli, ours(mineEnv, "branch", ""))

	inj, err := fault.New(cli, mineEnv)
	require.NoError(t, err)

	_, err = inj.InjectInto(t.Context(), theirs, pause("at-another-env", fault.Target{Role: fault.RoleDatabase}))
	require.Error(t, err, "a container belonging to another environment was accepted as a target")
	require.ErrorIs(t, err, fault.ErrOtherEnvironment)
	require.Contains(t, err.Error(), theirsEnv, "the refusal does not say which environment it belongs to")
	requireNotPaused(t, cli, theirs, "another environment's container was frozen")

	in, err := inj.InjectInto(t.Context(), mine, pause("at-my-own", fault.Target{Role: fault.RoleDatabase}))
	require.NoError(t, err, "the injector refused its own environment's container")
	require.NoError(t, in.Undo(t.Context()))
}

// TestInjectInto_RefusesTheEgressSidecar is the refusal that keeps a chaos
// feature from being a way out of the network policy.
//
// The sidecar belongs to this very environment and passes every other check.
// It is refused on its kind alone, because stopping it would switch off the
// control that decides what the environment may reach.
func TestInjectInto_RefusesTheEgressSidecar(t *testing.T) {
	cli := requireDocker(t)
	envID := envName("blastD")

	sidecar := sleeper(t, cli, ours(envID, "sidecar", ""))
	emulator := sleeper(t, cli, ours(envID, "emulator", ""))
	service := sleeper(t, cli, ours(envID, "service", "api"))

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)

	_, err = inj.InjectInto(t.Context(), sidecar, pause("at-the-sidecar", fault.Target{Role: fault.RoleService, Service: "api"}))
	require.Error(t, err, "the egress sidecar was accepted as a fault target")
	require.ErrorIs(t, err, fault.ErrProtected)
	require.Contains(t, err.Error(), "egress policy",
		"the refusal does not say why the sidecar is protected")
	requireNotPaused(t, cli, sidecar, "the egress sidecar was frozen")

	_, err = inj.InjectInto(t.Context(), emulator, pause("at-an-emulator", fault.Target{Role: fault.RoleService, Service: "api"}))
	require.Error(t, err, "an emulator was accepted as a fault target")
	require.ErrorIs(t, err, fault.ErrProtected)
	requireNotPaused(t, cli, emulator, "an emulator was frozen")

	// The liveness arm. A service container of the same environment, reached
	// through the same call, must be allowed: the refusal is about the kind
	// and not about the environment.
	in, err := inj.InjectInto(t.Context(), service, pause("at-a-service", fault.Target{Role: fault.RoleService, Service: "api"}))
	require.NoError(t, err, "a service container was refused, so the two refusals above prove nothing")
	requirePaused(t, cli, service, "the fault was accepted and the service is not frozen")
	require.NoError(t, in.Undo(t.Context()))
}

// TestResolve_NamesWhatTheEnvironmentHasWhenATargetIsMissing checks the
// refusal a person actually meets, which is a fault aimed at a service that is
// not there.
func TestResolve_NamesWhatTheEnvironmentHasWhenATargetIsMissing(t *testing.T) {
	cli := requireDocker(t)
	envID := envName("blastE")
	sleeper(t, cli, ours(envID, "service", "api"))
	sleeper(t, cli, ours(envID, "branch", ""))

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)

	_, err = inj.Resolve(t.Context(), fault.Target{Role: fault.RoleService, Service: "worker"})
	require.Error(t, err)
	require.ErrorIs(t, err, fault.ErrNoSuchTarget)
	require.Contains(t, err.Error(), "service api",
		"the refusal does not say what the environment does have")

	// And the one that is there resolves, so the refusal is about the name.
	c, err := inj.Resolve(t.Context(), fault.Target{Role: fault.RoleService, Service: "api"})
	require.NoError(t, err)
	require.Equal(t, "api", c.Service)
	require.Equal(t, envID, c.EnvID)
}

// TestContainers_ListsOnlyThisEnvironment holds the enumeration the resolver
// is built on: another environment's containers and a stranger's are both
// absent from it, and this environment's are all in it.
func TestContainers_ListsOnlyThisEnvironment(t *testing.T) {
	cli := requireDocker(t)
	mineEnv, theirsEnv := envName("blastF"), envName("blastG")

	sleeper(t, cli, ours(theirsEnv, "branch", ""))
	sleeper(t, cli, map[string]string{"com.example.owner": "somebody-else"})
	// The one the daemon's own filter cannot exclude. EnvFilter matches on the
	// environment label alone, so a container carrying this environment's id
	// and NO managed label comes back from the daemon and has to be dropped
	// here. Without it in the fixture, the Go predicate could be deleted
	// outright and this test would stay green, because the filter would be
	// doing all the work.
	sleeper(t, cli, map[string]string{dockerutil.LabelEnv: mineEnv, "com.example.owner": "somebody-else"})
	sleeper(t, cli, ours(mineEnv, "branch", ""))
	sleeper(t, cli, ours(mineEnv, "service", "api"))

	inj, err := fault.New(cli, mineEnv)
	require.NoError(t, err)
	got, err := inj.Containers(t.Context())
	require.NoError(t, err)
	require.Len(t, got, 2,
		"the listing is not exactly this environment's two OWNED containers; "+
			"a container carrying this environment's id and no managed label is not ours")
	for _, c := range got {
		require.Equal(t, mineEnv, c.EnvID)
		require.NotEmpty(t, c.Kind, "a container with no kind label was listed as a fault target")
	}
}

// TestProcessKill_RefusesAPatternThatMatchesNothing is the "a fault that
// changed nothing is not a fault that was survived" rule, at the one kind
// where a typo silently produces it.
func TestProcessKill_RefusesAPatternThatMatchesNothing(t *testing.T) {
	cli := requireDocker(t)
	envID := envName("blastH")
	id := sleeper(t, cli, ours(envID, "branch", ""))

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)

	_, err = inj.InjectInto(t.Context(), id, fault.Fault{
		Name: "typo", Kind: fault.KindProcessKill,
		Target: fault.Target{Role: fault.RoleDatabase}, Process: "postgres: chekpointer",
	})
	require.Error(t, err, "a process pattern that matches nothing was reported as a fault that was injected")
	require.Contains(t, err.Error(), "AF-CHS-004")
	require.Contains(t, err.Error(), "nothing would have been killed")

	// The liveness arm: a pattern that does match kills, and the container's
	// own process table is what says so.
	in, err := inj.InjectInto(t.Context(), id, fault.Fault{
		Name: "real", Kind: fault.KindProcessKill,
		Target: fault.Target{Role: fault.RoleDatabase}, Process: "sleep 1",
	})
	require.NoError(t, err, "a pattern that matches a running process was refused")
	require.Contains(t, in.Evidence, "SIGKILL")
	require.Contains(t, in.Evidence, "sleep 1")
}

// TestDiskFill_RefusesAFilesystemItWouldShareWithTheMachine is the storage
// fault's containment rule.
//
// A container's writable layer is the daemon's disk, so filling a directory on
// it fills the machine and every container on it. The refusal is what keeps
// the blast radius inside the environment, and it fires on the ordinary case
// rather than on an exotic one.
func TestDiskFill_RefusesAFilesystemItWouldShareWithTheMachine(t *testing.T) {
	cli := requireDocker(t)
	envID := envName("blastI")
	id := sleeper(t, cli, ours(envID, "branch", ""))

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	sh, err := inj.Shell(t.Context(), fault.Target{Role: fault.RoleDatabase})
	require.NoError(t, err)
	// The directory exists, so the refusal is about the filesystem it is on
	// and not about the path being absent.
	_, err = sh.Run(t.Context(), []string{"/bin/sh", "-c", "mkdir -p /var/lib/data"})
	require.NoError(t, err)

	_, err = inj.InjectInto(t.Context(), id, fault.Fault{
		Name: "fill-the-layer", Kind: fault.KindDiskFill,
		Target: fault.Target{Role: fault.RoleDatabase}, Path: "/var/lib/data",
		HeadroomBytes: 1 << 20, MaxFillBytes: 1 << 20,
	})
	require.Error(t, err, "a fill was accepted on a directory sharing the daemon's own disk")
	require.Contains(t, err.Error(), "AF-CHS-005")
	require.Contains(t, err.Error(), "every other container on this machine")
}

// TestReadOnlyData_RefusesWhenTheWriteWouldStillSucceed is the storage fault
// proving its own effect rather than its own command.
//
// A directory owned by root cannot be made read only by its mode, because root
// ignores the mode. The chmod succeeds, the mode changes, and a write still
// works: a fault that reported success on the strength of the chmod would have
// changed nothing while claiming to have made the data directory read only,
// and every assertion after it would describe a database that was never
// constrained. This is the arm that catches that, and it is beside the arm
// where the same fault works.
func TestReadOnlyData_RefusesWhenTheWriteWouldStillSucceed(t *testing.T) {
	cli := requireDocker(t)
	envID := envName("blastJ")
	id := sleeper(t, cli, ours(envID, "branch", ""))

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	sh, err := inj.Shell(t.Context(), fault.Target{Role: fault.RoleDatabase})
	require.NoError(t, err)

	_, err = sh.Run(t.Context(), []string{"/bin/sh", "-c",
		"mkdir -p /rootdata && chown root:root /rootdata && chmod 755 /rootdata"})
	require.NoError(t, err)

	_, err = inj.InjectInto(t.Context(), id, fault.Fault{
		Name: "read-only-root", Kind: fault.KindReadOnlyData,
		Target: fault.Target{Role: fault.RoleDatabase}, Path: "/rootdata",
	})
	require.Error(t, err, "a read only fault was accepted on a directory its owner can still write")
	// 004, applied and changed nothing, rather than 005, which is refused
	// before acting. The mode was changed and put back, which the assertion
	// below proves, so this fault acted.
	require.Contains(t, err.Error(), "AF-CHS-004")
	require.Contains(t, err.Error(), "root ignores it")
	// And it put the mode back on the way out, so a refused fault leaves
	// nothing behind.
	require.Equal(t, "755", mode(t, sh, "/rootdata"),
		"a refused read only fault left the directory with the mode it had changed it to")
}

// TestReadOnlyData_MakesTheDirectoryReadOnlyAndPutsItBack is the arm where the
// fault works: a directory owned by an unprivileged user, which is what a
// Postgres data directory is in every image that does not run as root.
func TestReadOnlyData_MakesTheDirectoryReadOnlyAndPutsItBack(t *testing.T) {
	cli := requireDocker(t)
	envID := envName("blastK")
	id := sleeper(t, cli, ours(envID, "branch", ""))

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	sh, err := inj.Shell(t.Context(), fault.Target{Role: fault.RoleDatabase})
	require.NoError(t, err)

	_, err = sh.Run(t.Context(), []string{"/bin/sh", "-c",
		"mkdir -p /data && chown nobody /data && chmod 755 /data"})
	require.NoError(t, err)
	require.Equal(t, "755", mode(t, sh, "/data"))
	require.Zero(t, writeAs(t, sh, "nobody", "/data"),
		"the owner cannot write before the fault, so the fault would prove nothing")

	in, err := inj.InjectInto(t.Context(), id, fault.Fault{
		Name: "read-only", Kind: fault.KindReadOnlyData,
		Target: fault.Target{Role: fault.RoleDatabase}, Path: "/data",
	})
	require.NoError(t, err)
	require.Equal(t, "555", mode(t, sh, "/data"), "the directory is still writable after the fault")
	require.Contains(t, in.Evidence, "a write by its owner nobody is now refused")
	require.NotZero(t, writeAs(t, sh, "nobody", "/data"),
		"a write into a read only directory succeeded for the user that owns it")

	require.NoError(t, in.Undo(t.Context()))
	require.Equal(t, "755", mode(t, sh, "/data"), "the undo did not restore the mode it recorded")
	require.Zero(t, writeAs(t, sh, "nobody", "/data"),
		"a write is still refused after the undo")
}

// writeAs attempts a write into dir as user and returns the exit code.
func writeAs(t *testing.T, sh *fault.Shell, user, dir string) int {
	t.Helper()
	out, err := sh.Run(t.Context(), []string{"/bin/sh", "-c",
		"su -s /bin/sh " + user + " -c 'touch " + dir + "/probe && rm -f " + dir + "/probe'"})
	require.NoError(t, err)
	return out.ExitCode
}

// mode reads a directory's permission bits from inside the container.
func mode(t *testing.T, sh *fault.Shell, dir string) string {
	t.Helper()
	out, err := sh.Run(t.Context(), []string{"stat", "-c", "%a", dir})
	require.NoError(t, err)
	require.Zero(t, out.ExitCode, "stat said %q", out.Stdout)
	return trimTTY(out.Stdout)
}

// trimTTY removes the carriage returns a terminal exec adds.
func trimTTY(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

func requirePaused(t *testing.T, cli *client.Client, id, why string) {
	t.Helper()
	res, err := cli.ContainerInspect(t.Context(), id, client.ContainerInspectOptions{})
	require.NoError(t, err)
	require.NotNil(t, res.Container.State)
	require.True(t, res.Container.State.Paused, why)
}

func requireNotPaused(t *testing.T, cli *client.Client, id, why string) {
	t.Helper()
	res, err := cli.ContainerInspect(t.Context(), id, client.ContainerInspectOptions{})
	require.NoError(t, err)
	require.NotNil(t, res.Container.State)
	require.False(t, res.Container.State.Paused, why)
}

// TestNetworkPartition_DetachesAndReattachesWithTheAliasesItHad is the network
// fault, driven against two containers on one network so that "partitioned"
// means something a test can read: the one that stays can no longer resolve
// the one that goes.
//
// The aliases are the part worth holding. A reconnect without them leaves a
// container that is on the network and that nothing in the environment can
// find, which looks exactly like a partition that never healed.
func TestNetworkPartition_DetachesAndReattachesWithTheAliasesItHad(t *testing.T) {
	cli := requireDocker(t)
	envID := envName("blastL")

	netName := fmt.Sprintf("af-blast-net-%d", time.Now().UnixNano())
	created, err := cli.NetworkCreate(t.Context(), netName, client.NetworkCreateOptions{
		Labels: dockerutil.Managed("network", envID, time.Now()),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerutil.RemoveNetwork(context.Background(), cli, created.ID) })

	db := sleeper(t, cli, ours(envID, "branch", ""))
	api := sleeper(t, cli, ours(envID, "service", "api"))
	for _, c := range []struct{ id, alias string }{{db, "db"}, {api, "api"}} {
		_, err := cli.NetworkConnect(t.Context(), created.ID, client.NetworkConnectOptions{
			Container: c.id, EndpointConfig: &network.EndpointSettings{Aliases: []string{c.alias}},
		})
		require.NoError(t, err)
	}

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	apiShell, err := inj.Shell(t.Context(), fault.Target{Role: fault.RoleService, Service: "api"})
	require.NoError(t, err)

	// Before: api can reach db by name. Without this the assertion after the
	// fault would pass on an environment where the name never worked, which is
	// the control arm this file insists on everywhere else.
	code, said := reaches(t, apiShell, "db")
	require.Zerof(t, code,
		"api could not reach db by name BEFORE the partition, so the partition would prove nothing. The probe said: %s", said)

	in, err := inj.Inject(t.Context(), fault.Fault{
		Name: "partition-the-database", Kind: fault.KindNetworkPartition,
		Target: fault.Target{Role: fault.RoleDatabase},
	})
	require.NoError(t, err)
	require.Contains(t, in.Evidence, netName, "the evidence does not name the network it detached from")
	code, said = reaches(t, apiShell, "db")
	require.NotZerof(t, code,
		"the database was detached and the service can still reach it by name. The probe said: %s", said)

	require.NoError(t, in.Undo(t.Context()))
	// The alias came back, which is the half a reconnect without it would fail.
	code, said = reaches(t, apiShell, "db")
	require.Zerof(t, code,
		"the database was reattached and the service cannot reach it by name, so the alias was not restored. The probe said: %s", said)
}

// reaches reports whether one container can reach another by name, and what
// the probe said about it.
//
// ping rather than nslookup, and the difference is not cosmetic. busybox's
// nslookup runs its own DNS client and exits non zero on conditions that have
// nothing to do with whether the name resolves, including a server it cannot
// reverse resolve; on a GitHub runner it reported failure for a name that
// worked, and the control arm of this test refused to proceed, which is the
// control arm doing its job. ping goes through the resolver the way a service
// does and then proves the address is reachable, which is the thing a
// partition actually removes.
//
// The output is returned as well as the code, because an assertion that says
// only "expected 0, got 1" about a network probe sends the next person to
// reproduce it by hand.
func reaches(t *testing.T, sh *fault.Shell, host string) (int, string) {
	t.Helper()
	out, err := sh.Run(t.Context(), []string{"/bin/sh", "-c",
		"ping -c 1 -W 2 " + host + " 2>&1"})
	require.NoError(t, err)
	return out.ExitCode, strings.TrimSpace(out.Stdout)
}
