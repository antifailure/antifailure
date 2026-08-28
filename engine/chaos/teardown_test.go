package chaos_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

// Scenario 4: the network is partitioned during teardown, AF-RUN-030 is
// reported, and a second run finishes the job.
//
// AF-RUN-030's next_step reads: "Run 'af down' again once the provider is
// reachable; the journal remembers what is left." Two claims, and before this
// lane only the first was true. The engine did report AF-RUN-030 and a second
// `af down` did finish the job, but what finished it was the label sweep
// finding the resources again. The journal remembered nothing, because nothing
// read it. The message named a mechanism that was not running.
//
// The fault is injected at the layer the claim is about: the Docker daemon is
// made genuinely unreachable by pointing DOCKER_HOST at a socket that does not
// exist, rather than by a fake provider returning an error. A fake would prove
// that the error handling works on the error the fake was written to produce.
//
// The negative control is the same one scenario 3 uses: without the reconcile
// call the second teardown reports clean while the resource is still there.

func orchestratorOver(t *testing.T, dir, branch string) *env.Orchestrator {
	t.Helper()
	m, err := manifest.Load(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	o, err := env.New(env.Options{
		Root: dir, Manifest: m, Branch: branch,
		Clock: clock.New(), Redactor: redact.New(),
		Progress: func(string) {},
		Getenv:   func(string) string { return "" },
	})
	require.NoError(t, err)
	return o
}

func TestATeardownAgainstAnUnreachableProviderSaysSoAndTheNextOneFinishesIt(t *testing.T) {
	scenario(t)
	cli := requireDocker(t)
	dir := chaosRepo(t)
	envID := env.EnvID("chaostest", "chaos/partition")

	netName := "af-chaos-partition-" + envID
	t.Cleanup(func() { _ = cli.NetworkRemove(context.Background(), netName) })
	_, err := cli.NetworkCreate(t.Context(), netName, network.CreateOptions{Labels: ownedByUs(envID)})
	require.NoError(t, err)

	journalAs(t, filepath.Join(dir, env.StateDir), envID,
		journal.Record{Provider: "local", Kind: journal.KindNetwork, IdemKey: netName})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// The partition. Every Docker call in this process now goes to a socket
	// that is not there.
	// A short path, deliberately. t.TempDir() produces a path built from the
	// test's name, which for a name this long exceeds the 104 byte limit on a
	// unix socket, and the client then fails while being constructed rather
	// than while connecting. Both are unreachable, but only one of them is the
	// partition this test claims to inject.
	dead, derr := os.MkdirTemp("", "afp")
	require.NoError(t, derr)
	t.Cleanup(func() { _ = os.RemoveAll(dead) })
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(dead, "dead.sock"))

	partitioned, perr := orchestratorOver(t, dir, "chaos/partition").Down(ctx)

	// Either shape is acceptable and both are honest: the teardown may refuse
	// to start at all when it cannot reach the daemon, or it may run and report
	// what it could not remove. What is not acceptable is reporting success.
	if perr == nil {
		require.NotEmpty(t, partitioned.Pending,
			"the teardown reported a clean run against an unreachable daemon: %s",
			pendingSummary(partitioned))
		t.Logf("teardown ran and reported: %s", pendingSummary(partitioned))
	} else {
		t.Logf("teardown refused to start: %v", perr)
	}

	require.True(t, networkExists(t, cli, netName),
		"the resource was removed while the daemon was unreachable, which means the "+
			"partition was not injected and this test is proving nothing")

	// The partition heals. t.Setenv restores the previous value at the end of
	// the test, so it is undone here by setting it back explicitly.
	t.Setenv("DOCKER_HOST", "")

	healed, err := orchestratorOver(t, dir, "chaos/partition").Down(ctx)
	require.NoError(t, err)
	require.Empty(t, healed.Pending,
		"the second teardown did not finish the job: %s", pendingSummary(healed))
	require.False(t, networkExists(t, cli, netName),
		"the journal did not remember what was left, which is what AF-RUN-030's message promises")

	for _, rec := range journalRecords(t, filepath.Join(dir, env.StateDir), envID) {
		require.Equal(t, journal.StateCompensated, rec.State)
	}
}

// AF-RUN-030 is what the user sees, and its exit code is what CI sees. A
// teardown that leaves work behind must not exit zero, or a pipeline that tears
// down after every run accumulates resources while every job goes green.
func TestAnIncompleteTeardownCarriesTheCodeAndTheExitStatusThatSayItIsIncomplete(t *testing.T) {
	scenario(t)
	err := aferrors.Coded(aferrors.AFRUN030, "count", "3")
	require.Equal(t, aferrors.ExitInterruptedDirty, aferrors.ExitCodeOf(err),
		"an incomplete teardown that exits zero is a leak with a green tick over it")
	require.True(t, aferrors.IsRetryable(err),
		"AF-RUN-030 tells the user to run it again, so it had better be marked retryable")
	require.Contains(t, err.Error(), "3")
}
