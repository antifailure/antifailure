package telemetry

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/state"
)

// attached stands in for one `af` command: its own bus, its own telemetry,
// sharing the state directory that every command on this machine shares.
type attached struct {
	bus *events.Bus
	tel *Telemetry
	dir string
	db  *state.DB
}

func attach(t *testing.T, dir string, env map[string]string) *attached {
	t.Helper()
	db, err := state.Open(t.Context(), dir)
	require.NoError(t, err)

	bus := events.NewBus(clock.NewFake(time.Unix(1700000000, 0).UTC()))
	tel, err := Attach(t.Context(), bus, Options{
		StateDir: dir,
		EnvID:    "shop-main-a1b2",
		Redactor: redact.New(),
		Clock:    clock.NewFake(time.Unix(1700000000, 0).UTC()),
		State:    db,
		Getenv:   func(k string) string { return env[k] },
	})
	require.NoError(t, err)
	return &attached{bus: bus, tel: tel, dir: dir, db: db}
}

func (a *attached) close(t *testing.T) {
	t.Helper()
	require.NoError(t, a.tel.Close(context.Background()))
	require.NoError(t, a.db.Close())
}

func readLog(t *testing.T, dir string) []events.Event {
	t.Helper()
	path := filepath.Join(dir, LogDir, "shop-main-a1b2.ndjson")
	f, err := os.Open(path)
	require.NoError(t, err, "the NDJSON event log was never written")
	defer func() { _ = f.Close() }()

	var out []events.Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e events.Event
		require.NoError(t, json.Unmarshal([]byte(line), &e))
		out = append(out, e)
	}
	require.NoError(t, sc.Err())
	return out
}

// Until this lane, no production code in the engine had ever called AddSink and
// no sink constructor had a caller outside a test. This is the assertion that
// says so: an event emitted on an attached bus is on disk afterwards.
func TestAnEmittedEventReachesTheLocalLog(t *testing.T) {
	dir := t.TempDir()
	a := attach(t, dir, nil)
	a.bus.Info("shop-main-a1b2", events.EnvReady, "the environment is ready",
		events.F("preview_url", "http://localhost:8080"))
	a.close(t)

	log := readLog(t, dir)
	require.Len(t, log, 1)
	require.Equal(t, events.EnvReady, log[0].Type)
	require.Equal(t, "http://localhost:8080", log[0].Data["preview_url"])
}

// The bug that would have made the control plane integration useless the moment
// it was wired, proved through the real pairing rather than against the
// reserver alone. `af up` and `af test` are two processes; the second must not
// restart its numbering, or every event it sends is refused by a projection
// that only advances when the sequence is ahead.
func TestASecondCommandsEventsAreNumberedAheadOfTheFirsts(t *testing.T) {
	dir := t.TempDir()

	up := attach(t, dir, nil)
	up.bus.Info("shop-main-a1b2", events.EnvCreating, "creating")
	up.bus.Info("shop-main-a1b2", events.EnvReady, "ready")
	lastOfUp := up.bus.Seq("shop-main-a1b2")
	up.close(t)
	require.Equal(t, uint64(2), lastOfUp)

	test := attach(t, dir, nil)
	test.bus.Info("shop-main-a1b2", events.AgentStarted, "running the workflows")
	firstOfTest := test.bus.Seq("shop-main-a1b2")
	test.close(t)

	require.Greater(t, firstOfTest, lastOfUp,
		"the second command restarted its sequence, so the control plane would refuse "+
			"every event it sent and the environment would sit in the dashboard forever "+
			"in the state the first command left it in")

	down := attach(t, dir, nil)
	down.bus.Info("shop-main-a1b2", events.EnvDestroyed, "gone")
	lastOfDown := down.bus.Seq("shop-main-a1b2")
	down.close(t)
	require.Greater(t, lastOfDown, firstOfTest)
}

// The whole log is one stream, so a reader can order it and see a gap.
func TestTheLogFromThreeCommandsReadsAsOneOrderedStream(t *testing.T) {
	dir := t.TempDir()
	for _, step := range []struct {
		ty  events.Type
		msg string
	}{
		{events.EnvCreating, "creating"},
		{events.EnvReady, "ready"},
		{events.AgentStarted, "testing"},
		{events.EnvDestroyed, "gone"},
	} {
		a := attach(t, dir, nil)
		a.bus.Info("shop-main-a1b2", step.ty, step.msg)
		a.close(t)
	}

	log := readLog(t, dir)
	require.Len(t, log, 4)
	for i := 1; i < len(log); i++ {
		require.Greaterf(t, log[i].Seq, log[i-1].Seq,
			"event %d (%s) is not ahead of event %d (%s)", i, log[i].Type, i-1, log[i-1].Type)
	}
}

// The local log is a file `af support bundle` collects, so the rule that holds
// everywhere else holds here: redaction is at the writer.
func TestASecretInAnEventNeverReachesTheLocalLog(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(t.Context(), dir)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	r := redact.New()
	const password = "log-secret-4a91cc"
	r.Register(password)

	bus := events.NewBus(clock.NewFake(time.Unix(1700000000, 0).UTC()))
	tel, err := Attach(t.Context(), bus, Options{
		StateDir: dir, EnvID: "shop-main-a1b2", Redactor: r, State: db,
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)

	bus.Info("shop-main-a1b2", events.DBBranched, "branched",
		events.F("url", "postgres://app:"+password+"@db:5432/app"),
		events.F("also", "postgres://app:never-registered-either@db:5432/app"))
	require.NoError(t, tel.Close(context.Background()))

	raw, err := os.ReadFile(filepath.Join(dir, LogDir, "shop-main-a1b2.ndjson"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), password)
	require.NotContains(t, string(raw), "never-registered-either")
	require.Contains(t, string(raw), "db:5432", "the host survives, or the log explains nothing")
}

// No token is the ordinary case and must cost nothing: no client, no spool
// directory, no warning, no error.
func TestWithNoControlPlaneTokenNothingIsAttachedAndNothingComplains(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(t.Context(), dir)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	var warnings []string
	bus := events.NewBus(clock.NewFake(time.Unix(1700000000, 0).UTC()))
	tel, err := Attach(t.Context(), bus, Options{
		StateDir: dir, EnvID: "shop-main-a1b2", Redactor: redact.New(), State: db,
		Getenv:    func(string) string { return "" },
		OnWarning: func(m string) { warnings = append(warnings, m) },
	})
	require.NoError(t, err)

	bus.Info("shop-main-a1b2", events.EnvReady, "ready")
	require.NoError(t, tel.Close(context.Background()))

	require.Empty(t, warnings, "a laptop with no control plane is not a problem to report")
	require.Nil(t, tel.Spool())
	_, err = os.Stat(filepath.Join(dir, SpoolDir))
	require.True(t, os.IsNotExist(err), "no spool directory is created for a control plane nobody configured")
	require.NotEmpty(t, readLog(t, dir), "the local log still works without one")
}

// An environment must not fail to come up because a log directory is read only.
// The failure is reported and survived, which is the rule for everything in
// this package.
func TestALogThatCannotBeOpenedIsReportedRatherThanFatal(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(t.Context(), dir)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// A file where the log directory should be. Creating the directory then
	// fails, and nothing else may.
	require.NoError(t, os.WriteFile(filepath.Join(dir, LogDir), []byte("in the way"), 0o600))

	var warnings []string
	bus := events.NewBus(clock.NewFake(time.Unix(1700000000, 0).UTC()))
	tel, err := Attach(t.Context(), bus, Options{
		StateDir: dir, EnvID: "shop-main-a1b2", Redactor: redact.New(), State: db,
		Getenv:    func(string) string { return "" },
		OnWarning: func(m string) { warnings = append(warnings, m) },
	})
	require.NoError(t, err, "an unwritable log is not a reason to refuse an environment")
	bus.Info("shop-main-a1b2", events.EnvReady, "ready")
	require.NoError(t, tel.Close(context.Background()))

	require.NotEmpty(t, warnings, "and it is said out loud rather than swallowed")
	require.Contains(t, strings.Join(warnings, "\n"), "event log")
}

// The line an operator sees when a command ends owing events. Without it a
// control plane outage is indistinguishable from a dashboard that has silently
// stopped updating, which is the difference between a shrug and a page.
func TestACommandThatEndsOwingEventsSaysSo(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(t.Context(), dir)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// A control plane that is configured and refuses everything.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	var warnings []string
	bus := events.NewBus(clock.NewFake(time.Unix(1700000000, 0).UTC()))
	tel, err := Attach(t.Context(), bus, Options{
		StateDir: dir, EnvID: "shop-main-a1b2", Redactor: redact.New(), State: db,
		ControlPlaneURL: srv.URL,
		Getenv: func(k string) string {
			if k == "AF_CONTROL_PLANE_TOKEN" {
				return "a-token"
			}
			return ""
		},
		OnWarning: func(m string) { warnings = append(warnings, m) },
	})
	require.NoError(t, err)

	bus.Info("shop-main-a1b2", events.EnvReady, "ready")
	require.NoError(t, tel.Close(context.Background()))

	joined := strings.Join(warnings, "\n")
	require.Contains(t, joined, "waiting for the control plane",
		"the command ended owing events and said nothing: %v", warnings)
	require.NotZero(t, tel.Spool().Pending())
}

func TestAttachRefusesWithoutARedactor(t *testing.T) {
	bus := events.NewBus(clock.NewFake(time.Unix(1700000000, 0).UTC()))
	defer func() { _ = bus.Close() }()
	_, err := Attach(context.Background(), bus, Options{StateDir: t.TempDir()})
	require.Error(t, err)
	require.Contains(t, err.Error(), "redactor")
}
