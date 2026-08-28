package chaos_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/state"
)

// Scenario 3: a killed engine reconciles through the journal.
//
// internal/journal opens with "records every external resource before it is
// created, so that a crash at any instant leaves the system recoverable" and
// calls the compensating deletion "the rule the whole product rests on".
// STATUS.md lists it proven at 95 percent with crash injection at every step.
//
// It was written and never read. Journal.Replay had zero callers in the engine,
// journal.NewRegistry had zero, so there was no deleter registry for a replay
// to consult even in principle, and Journal.Commit had zero, so no record had
// ever left the intent state or carried a provider's identifier.
//
// It was invisible because teardown works: `af down` sweeps the daemon for the
// environment's labels and removes what it finds. That is why the fault below
// is injected the way it is. The resources are created WITHOUT the labels, so
// the sweep cannot see them and only the journal can. A test that created them
// with labels would have passed against the broken engine, reported a
// reconciliation that did not exist, and been worse than no test at all. That
// draft was written first.
//
// The negative control: removing the o.reconcile call from Orchestrator.Down
// makes every assertion below fail, which was run before this comment was
// written.

const chaosManifest = `
version: 1
name: chaostest
services:
  - name: web
    kind: web
    command: node server.js
    port: 3000
`

// requireDocker skips only when there is genuinely no daemon, and fails for
// every other reason.
//
// The distinction is the whole point. "No Docker on this machine" is a skip,
// because the person running the suite could not have run it. "Docker answered
// and then something went wrong" is a failure, because it is a result. One
// t.Skipf covering both is a way for this suite to pass, and a suite that
// proves failure paths work is the worst possible place for that.
//
// The ping timeout is deliberately generous rather than snappy. Measured on
// this machine with eleven agents on it, `docker ps -a` took over two minutes
// to return. A short timeout would turn machine load into a skip, and load is
// heaviest exactly when tests are being run in bulk, so the skip would arrive
// precisely when nobody is watching.
func requireDocker(t *testing.T) *client.Client {
	t.Helper()
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	cli, err := dockerutil.Client()
	if err != nil {
		t.Skipf("skipped: no Docker daemon is configured: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		if errors.Is(err, context.DeadlineExceeded) {
			// Not a skip. A daemon that is configured and does not answer a
			// ping in three minutes is a broken environment, and reporting it
			// as "no Docker" would hide it.
			t.Fatalf("the Docker daemon is configured and did not answer a ping in three "+
				"minutes; this is a failure rather than a skip, because the daemon exists: %v", err)
		}
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

// newEnvironment builds an orchestrator over a temporary repository and returns
// it with its environment identifier and its state directory.
func newEnvironment(t *testing.T, branch string) (*env.Orchestrator, string, string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(strings.TrimSpace(chaosManifest)+"\n"), 0o644))

	m, err := manifest.Load(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)

	o, err := env.New(env.Options{
		Root: dir, Manifest: m, Branch: branch,
		Clock: clock.New(), Redactor: redact.New(),
		Progress: func(string) {},
		Getenv:   func(string) string { return "" },
	})
	require.NoError(t, err)
	return o, o.EnvID(), filepath.Join(dir, env.StateDir)
}

// journalAs writes records exactly as the engine does, then closes the state
// database, which is what a killed process leaves behind: the records
// committed, the resources present, and nothing holding the file.
func journalAs(t *testing.T, stateDir, envID string, records ...journal.Record) {
	t.Helper()
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	db, err := state.Open(t.Context(), stateDir)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	bus := events.NewBus(clock.New())
	defer func() { _ = bus.Close() }()
	j := journal.New(db, clock.New(), bus)

	for _, r := range records {
		rec, err := j.Intent(t.Context(), envID, r.Provider, r.Kind, r.IdemKey, r.Compensation)
		require.NoError(t, err)
		if r.ExternalID != "" {
			require.NoError(t, j.Commit(t.Context(), rec.ID, r.ExternalID))
		}
	}
}

func journalRecords(t *testing.T, stateDir, envID string) []journal.Record {
	t.Helper()
	db, err := state.Open(t.Context(), stateDir)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	bus := events.NewBus(clock.New())
	defer func() { _ = bus.Close() }()

	recs, err := journal.New(db, clock.New(), bus).All(t.Context(), envID)
	require.NoError(t, err)
	return recs
}

func networkExists(t *testing.T, cli *client.Client, name string) bool {
	t.Helper()
	_, err := cli.NetworkInspect(t.Context(), name, network.InspectOptions{})
	return err == nil
}

func volumeExists(t *testing.T, cli *client.Client, name string) bool {
	t.Helper()
	_, err := cli.VolumeInspect(t.Context(), name)
	return err == nil
}

func TestAKilledEngineIsReconciledFromTheJournal(t *testing.T) {
	scenario(t)
	cli := requireDocker(t)
	o, envID, stateDir := newEnvironment(t, "chaos/killed-engine")

	// Owned by Antifailure and invisible to the sweep. See ownedByUs: the
	// sweep needs the environment label as well as the managed one, so these
	// carry only the managed one. The journal is the only thing that can find
	// them, which is what this scenario is about.
	netName := "af-chaos-net-" + envID
	volName := "af-chaos-vol-" + envID
	t.Cleanup(func() {
		_ = cli.NetworkRemove(context.Background(), netName)
		_ = cli.VolumeRemove(context.Background(), volName, true)
	})

	_, err := cli.NetworkCreate(t.Context(), netName, network.CreateOptions{Labels: ownedByUs(envID)})
	require.NoError(t, err)
	_, err = cli.VolumeCreate(t.Context(), volume.CreateOptions{Name: volName, Labels: ownedByUs(envID)})
	require.NoError(t, err)
	require.True(t, networkExists(t, cli, netName))
	require.True(t, volumeExists(t, cli, volName))

	journalAs(t, stateDir, envID,
		journal.Record{Provider: "local", Kind: journal.KindNetwork, IdemKey: netName},
		journal.Record{Provider: "local", Kind: journal.KindVolume, IdemKey: volName},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	td, err := o.Down(ctx)
	require.NoError(t, err)

	require.Falsef(t, networkExists(t, cli, netName),
		"the network is still there, so nothing replayed the journal. %s", pendingSummary(td))
	require.Falsef(t, volumeExists(t, cli, volName),
		"the volume is still there, so nothing replayed the journal. %s", pendingSummary(td))

	for _, rec := range journalRecords(t, stateDir, envID) {
		require.Equalf(t, journal.StateCompensated, rec.State,
			"%s %s is still %s, so the record would be replayed again forever and the "+
				"journal table would grow without bound", rec.Kind, rec.IdemKey, rec.State)
	}
}

// Replay is run after a sweep that has usually already removed everything, and
// after crashes that may have left nothing at all. Deleting something that is
// already gone has to succeed, or a record stays live forever describing a
// resource that does not exist.
func TestReconcilingAResourceThatIsAlreadyGoneSucceeds(t *testing.T) {
	scenario(t)
	requireDocker(t)
	o, envID, stateDir := newEnvironment(t, "chaos/already-gone")

	journalAs(t, stateDir, envID,
		journal.Record{Provider: "local", Kind: journal.KindNetwork, IdemKey: "af-chaos-never-existed"},
		journal.Record{Provider: "local", Kind: journal.KindVolume, IdemKey: "af-chaos-also-never-existed"},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	td, err := o.Down(ctx)
	require.NoError(t, err)
	require.Empty(t, td.Pending, "not found is the expected case, not a failure: %s", pendingSummary(td))

	for _, rec := range journalRecords(t, stateDir, envID) {
		require.Equal(t, journal.StateCompensated, rec.State)
	}
}

// Running teardown twice is not an error and must not undo the first one's
// bookkeeping. This is the ordering a user reaches by pressing up-enter.
func TestReconcilingTwiceIsNotAnError(t *testing.T) {
	scenario(t)
	cli := requireDocker(t)
	o, envID, stateDir := newEnvironment(t, "chaos/twice")

	netName := "af-chaos-twice-" + envID
	t.Cleanup(func() { _ = cli.NetworkRemove(context.Background(), netName) })
	_, err := cli.NetworkCreate(t.Context(), netName, network.CreateOptions{Labels: ownedByUs(envID)})
	require.NoError(t, err)

	journalAs(t, stateDir, envID,
		journal.Record{Provider: "local", Kind: journal.KindNetwork, IdemKey: netName})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	first, err := o.Down(ctx)
	require.NoError(t, err)
	require.Empty(t, first.Pending, pendingSummary(first))
	require.False(t, networkExists(t, cli, netName))

	second, err := o.Down(ctx)
	require.NoError(t, err)
	require.Empty(t, second.Pending, "a second teardown found work that is already done: %s",
		pendingSummary(second))
}

// A record this build has no deleter for is left alone rather than dropped, so
// that a downgrade does not orphan resources. Left alone must also mean
// reported: a resource nothing can remove and nobody is told about is the leak
// the journal exists to prevent, with a record of itself.
func TestAResourceThisBuildCannotDeleteIsReportedRatherThanForgotten(t *testing.T) {
	scenario(t)
	requireDocker(t)
	o, envID, stateDir := newEnvironment(t, "chaos/unknown-kind")

	journalAs(t, stateDir, envID, journal.Record{
		Provider: "some-future-cloud", Kind: journal.KindDNSRecord,
		IdemKey: "preview.example.test",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	td, err := o.Down(ctx)
	require.NoError(t, err)

	require.NotEmpty(t, td.Pending,
		"a record with no deleter was silently treated as done, which reports a clean "+
			"teardown over a resource that is still there")
	require.Contains(t, pendingSummary(td), "preview.example.test")

	var found bool
	for _, rec := range journalRecords(t, stateDir, envID) {
		if rec.IdemKey == "preview.example.test" {
			found = true
			require.NotEqual(t, journal.StateCompensated, rec.State,
				"a record nothing deleted must not be marked compensated")
		}
	}
	require.True(t, found, "the record was dropped rather than kept")
}

func pendingSummary(td *env.Teardown) string {
	if td == nil || len(td.Pending) == 0 {
		return "nothing was reported as pending"
	}
	var b strings.Builder
	b.WriteString("pending: ")
	for i, p := range td.Pending {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s %s: %s", p.Kind, p.ID, p.Reason)
	}
	return b.String()
}

// ownedByUs is the label set the engine puts on everything it creates, WITHOUT
// the environment label the teardown sweep filters on.
//
// The distinction is the whole point of these fixtures and it took a CI failure
// to get right. These resources have to be invisible to the label sweep, or the
// sweep passes the test and says nothing about whether the journal did
// anything. The first version achieved that by creating them with NO labels at
// all, which also made them, correctly, not Antifailure's: the compensating
// delete now refuses a resource it does not own, and refused these.
//
// dockerutil.EnvFilter requires BOTH the managed label and the environment
// label, so carrying only the first is invisible to the sweep and still owned.
// It is also the more honest fixture. The real engine passes its labels to
// NetworkCreate and VolumeCreate in the same call that makes the resource, so
// there is no instant in which a crash could leave an unlabelled resource
// behind. A test that simulated a crash by producing one was simulating a state
// the engine cannot reach.
func ownedByUs(envID string) map[string]string {
	labels := dockerutil.Managed(dockerutil.KindNetwork, envID, time.Now())
	delete(labels, dockerutil.LabelEnv)
	return labels
}
