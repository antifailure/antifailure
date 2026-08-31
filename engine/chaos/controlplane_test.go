package chaos_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/controlplane"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/telemetry"
)

// Scenario 1: the control plane goes away mid run, and events are buffered and
// sent when it returns.
//
// AF-CPL-003's next_step says exactly that, and STATUS.md repeats it. It was
// false twice over. Nothing in the engine had ever attached the control plane
// sink to a bus, so no engine event had reached a control plane at all; and the
// sink's buffer was in memory, so even attached it would have lost an outage's
// events when the command exited. `af up`, `af test` and `af down` are three
// processes, and "when it returns" almost always means during a later one.
//
// The orderings are proved against the sink and the spool directly in
// engine/internal/telemetry/orderings_test.go, which is where the ten-cell
// table lives. What is proved here is the other half: that a real command,
// through the real orchestrator, with the real telemetry wiring, actually puts
// its events on that path. A unit test of a sink nobody attaches is the exact
// failure this scenario is about.
//
// The negative control, and the first attempt at it was wrong in a way worth
// recording. Setting the session's telemetry handle to nil did NOT make these
// tests fail, because Attach registers the sinks on the bus before it returns
// and the session closes the bus either way; nulling the handle only skipped a
// Close. Skipping the Attach call itself turns both tests red with the messages
// they carry. A negative control that does not go red has told you something
// about the control, not about the code.

// deadOrLivePlane is a control plane that can be taken away and brought back
// while the engine is running against it.
type deadOrLivePlane struct {
	mu     sync.Mutex
	up     bool
	seen   []string
	events []controlplane.Event
	byID   map[string]bool
}

func newDeadOrLivePlane(up bool) *deadOrLivePlane {
	return &deadOrLivePlane{up: up, byID: map[string]bool{}}
}

func (p *deadOrLivePlane) setUp(up bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.up = up
}

func (p *deadOrLivePlane) types() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

// first returns the first event of a type the plane received, or false. The
// payload is what this scenario is about: the type arriving proves the sink is
// attached, and only the payload proves the control plane was told enough to
// record the environment at all.
func (p *deadOrLivePlane) first(kind string) (controlplane.Event, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.events {
		if e.Type == kind {
			return e, true
		}
	}
	return controlplane.Event{}, false
}

func (p *deadOrLivePlane) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	up := p.up
	p.mu.Unlock()
	if !up {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Events []controlplane.Event `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	for _, e := range body.Events {
		if p.byID[e.ID] {
			continue
		}
		p.byID[e.ID] = true
		p.seen = append(p.seen, e.Type)
		p.events = append(p.events, e)
	}
	p.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"accepted":0}`))
}

// newReportingEnvironment builds an orchestrator that reports to the given
// control plane, over a repository that lives for the whole test rather than
// for one command, because the point is that two commands share a state
// directory.
func newReportingEnvironment(t *testing.T, dir, url, branch string) *env.Orchestrator {
	t.Helper()
	m, err := manifest.Load(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)

	vars := map[string]string{
		"AF_CONTROL_PLANE_URL":   url,
		"AF_CONTROL_PLANE_TOKEN": "chaos-engine-token",
	}
	o, err := env.New(env.Options{
		Root: dir, Manifest: m, Branch: branch,
		// Set here rather than resolved from git, because a t.TempDir has no
		// remote and the point of the scenario is what reaches the wire.
		Repository: "antifailure/chaostest",
		Clock:      clock.New(), Redactor: redact.New(),
		Progress: func(string) {},
		Getenv:   func(k string) string { return vars[k] },
	})
	require.NoError(t, err)
	return o
}

func chaosRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(strings.TrimSpace(chaosManifest)+"\n"), 0o644))
	return dir
}

func spoolFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, env.StateDir, telemetry.SpoolDir))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestACommandRunAgainstALiveControlPlaneReportsToIt(t *testing.T) {
	scenario(t)
	requireDocker(t)
	plane := newDeadOrLivePlane(true)
	srv := httptest.NewServer(plane)
	t.Cleanup(srv.Close)

	dir := chaosRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	_, err := newReportingEnvironment(t, dir, srv.URL, "chaos/live-plane").Down(ctx)
	require.NoError(t, err)

	require.Contains(t, plane.types(), "environment.torn_down",
		"the command ran and the control plane heard nothing, so nothing is attaching the sink")
	require.Empty(t, spoolFiles(t, dir), "nothing is owed when it was reachable throughout")
}

func TestEventsFromACommandRunWhileTheControlPlaneWasDownArriveWithTheNextOne(t *testing.T) {
	scenario(t)
	requireDocker(t)
	plane := newDeadOrLivePlane(false)
	srv := httptest.NewServer(plane)
	t.Cleanup(srv.Close)

	dir := chaosRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// The first command runs with the control plane unreachable throughout. It
	// must still succeed: an environment that will not tear down because a
	// dashboard is down would be a worse failure than a missing graph.
	_, err := newReportingEnvironment(t, dir, srv.URL, "chaos/late-delivery").Down(ctx)
	require.NoError(t, err, "a control plane outage must not fail a command")
	require.Empty(t, plane.types(), "it really was unreachable")
	require.NotEmpty(t, spoolFiles(t, dir),
		"the events were lost at exit rather than kept, so AF-CPL-003's promise is not kept")

	// It comes back between the two commands, which is the ordinary shape: an
	// outage that spans one command and not the next.
	plane.setUp(true)

	_, err = newReportingEnvironment(t, dir, srv.URL, "chaos/late-delivery").Down(ctx)
	require.NoError(t, err)

	require.Contains(t, plane.types(), "environment.torn_down",
		"the earlier command's events never arrived, so buffered means lost")
	require.Empty(t, spoolFiles(t, dir), "and the debt is cleared rather than resent forever")
}

// Scenario 4: the control plane is told which repository and branch an
// environment belongs to, by a real command.
//
// Its environments table joins repositories on a NOT NULL column and carries a
// NOT NULL branch, so an event that does not name both cannot create a row.
// Until this scenario, no event named either except env.creating, which named
// only the branch, and the control plane's projection was consequently an
// UPDATE that matched nothing for the life of the product.
//
// Proved here rather than only in a unit test of the field builder for the
// same reason scenario 1 exists: a field set nothing attaches to an event is
// the same defect as a sink nothing attaches to a bus. This runs a real
// command, through the real orchestrator and the real sink, and reads what
// came off the wire.
//
// af down rather than af up, because Down is the command that can run on its
// own in a scratch repository, and because torn_down is the harder case: it is
// the event most likely to be the FIRST one a control plane ever hears about
// an environment, when the run that created it never reached the network.
func TestTheControlPlaneIsToldWhichRepositoryAnEnvironmentBelongsTo(t *testing.T) {
	scenario(t)
	requireDocker(t)
	plane := newDeadOrLivePlane(true)
	srv := httptest.NewServer(plane)
	t.Cleanup(srv.Close)

	// A manifest that declares its own lifetime rather than inheriting the
	// default. The assertion is that the number the manifest says travels to
	// the control plane, and pinning it to whatever the default happens to be
	// makes this test fail the day somebody changes that default, which tells
	// nobody anything about whether the field arrived.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"),
		[]byte(strings.TrimSpace(chaosManifest)+"\nruntime:\n  ttl: 12h\n"), 0o644))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	o := newReportingEnvironment(t, dir, srv.URL, "chaos/identity")
	_, err := o.Down(ctx)
	require.NoError(t, err)

	got, ok := plane.first("environment.torn_down")
	require.True(t, ok, "the command ran and the control plane heard nothing")

	require.Equal(t, "antifailure/chaostest", got.Payload["repository"],
		"without this the control plane cannot create the environments row at all")
	require.Equal(t, "chaos/identity", got.Payload["branch"])
	// The lifetime the manifest declares, in seconds, which is what the
	// control plane adds to created_at to fill the expiry the console shows.
	require.Equal(t, float64(12*3600), got.Payload["ttl_seconds"])
}
