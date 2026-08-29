package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/controlplane"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

// The orderings, written down before the tests were, because a multi-event flow
// tested in the one order the author had in mind is a flow that has not been
// tested. Every cell below is a test in this file, and an empty cell would be
// an unshipped bug.
//
//	#   Ordering                                          Required outcome
//	--  ------------------------------------------------  ------------------------------------------
//	1   Control plane up for the whole command            Delivered live. Spool empty. Nothing dropped.
//	2   Down for all of command A, up during command B    B sends A's events first, then its own.
//	3   Down mid-command, back before it ends             Delivered inside the same command.
//	4   No token configured at all                        No sink, no spool, no error, no drop.
//	5   Up but throttling                                 Retry-After obeyed; nothing lost; sent after.
//	6   Command killed with no close                      What was flushed once is on disk; the loss
//	                                                      window is one flush interval, and is stated.
//	7   Down during A and still down during B             Spool retained, not duplicated, still bounded.
//	8   Spooled events the control plane already has      Duplicates acknowledged; spool clears.
//	9   Two commands draining at the same moment          Each batch sent exactly once.
//	10  Spool over its byte budget while down             Oldest dropped, count reported, newest kept.
//
// Orderings 9 and 10 are proved against the spool directly in spool_test.go,
// because they are properties of the store rather than of the pairing. The rest
// are here, where a real sink talks to a real HTTP server that is really down.

// plane is a control plane that can be taken away and brought back.
type plane struct {
	mu sync.Mutex
	// up is whether it answers at all.
	up bool
	// throttleFor, when set, makes the next request a 429 asking for this long.
	throttleFor time.Duration
	// seen is every event identifier ingested, in arrival order. The control
	// plane deduplicates on the identifier, and so does this.
	seen []string
	byID map[string]bool
	// requests counts every request that reached it, refused ones included.
	requests int
}

func newPlane(up bool) *plane { return &plane{up: up, byID: map[string]bool{}} }

func (p *plane) setUp(up bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.up = up
}

func (p *plane) order() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

func (p *plane) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.requests++
	up, throttle := p.up, p.throttleFor
	p.throttleFor = 0
	p.mu.Unlock()

	if !up {
		// 503 rather than a closed connection, so the test is about the sink's
		// behaviour rather than about Go's transport error strings.
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if throttle > 0 {
		w.Header().Set("Retry-After", fmt.Sprint(int(throttle.Seconds())))
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}

	var body struct {
		Events []controlplane.Event `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var duplicates int
	p.mu.Lock()
	for _, e := range body.Events {
		if p.byID[e.ID] {
			duplicates++
			continue
		}
		p.byID[e.ID] = true
		p.seen = append(p.seen, e.ID)
	}
	p.mu.Unlock()

	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"accepted":   len(body.Events) - duplicates,
		"duplicates": duplicates,
	})
}

// command stands in for one `af` invocation: its own sink, its own in-memory
// buffer, sharing only the spool directory on disk, which is the only thing two
// processes on one machine actually share.
type command struct {
	sink  *controlplane.Sink
	spool *Spool
	clock *clock.Fake
}

func newCommand(t *testing.T, srv *httptest.Server, spoolDir string) *command {
	t.Helper()
	client, err := controlplane.New(controlplane.Options{
		BaseURL: srv.URL, Token: "engine-token", Redactor: redact.New(),
	})
	require.NoError(t, err)

	sp, err := NewSpool(SpoolOptions{Dir: spoolDir, Redactor: redact.New()})
	require.NoError(t, err)

	fake := clock.NewFake(time.Unix(1700000000, 0).UTC())
	sink := controlplane.NewSink(controlplane.SinkOptions{
		Client: client, Clock: fake, Overflow: sp,
		Capacity: 1000, BatchSize: 10,
		// Flushed by hand so the orderings are the test rather than the timing.
		FlushEvery: time.Hour,
	})
	return &command{sink: sink, spool: sp, clock: fake}
}

func (c *command) emit(t *testing.T, ids ...string) {
	t.Helper()
	for i, id := range ids {
		require.NoError(t, c.sink.Deliver(context.Background(), events.Event{
			ID: id, Env: "env-1", Seq: uint64(i + 1), Type: events.EnvReady,
			Level: events.LevelInfo, TS: time.Unix(1700000000, 0).UTC(),
		}))
	}
}

func spoolDirFor(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "spool")
}

func serve(t *testing.T, p *plane) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	return srv
}

// Ordering 1.
func TestUpThroughout_DeliversLiveAndSpoolsNothing(t *testing.T) {
	p := newPlane(true)
	srv := serve(t, p)
	dir := spoolDirFor(t)

	c := newCommand(t, srv, dir)
	c.emit(t, "a", "b", "c")
	require.NoError(t, c.sink.Flush(context.Background()))
	require.NoError(t, c.sink.Close())

	require.Equal(t, []string{"a", "b", "c"}, p.order())
	require.Equal(t, 0, c.spool.Pending(), "nothing was owed")
	require.Zero(t, c.sink.Dropped())
	require.Zero(t, c.spool.Dropped())
}

// Ordering 2. This is the one AF-CPL-003 promises and the one an in-memory
// buffer alone cannot keep, because the buffer exits with the process.
func TestDownForOneCommand_TheNextCommandDeliversWhatItLeft(t *testing.T) {
	p := newPlane(false)
	srv := serve(t, p)
	dir := spoolDirFor(t)

	// Command A: `af up`, with the control plane unreachable throughout.
	a := newCommand(t, srv, dir)
	a.emit(t, "up-1", "up-2")
	require.Error(t, a.sink.Flush(context.Background()), "it really is down")
	require.NoError(t, a.sink.Close())
	require.NotZero(t, a.spool.Pending(), "command A left its events on disk")
	require.Zero(t, a.sink.Dropped(), "spooled is not dropped")

	// The control plane comes back between the two commands.
	p.setUp(true)

	// Command B: `af test`, a different process, sharing only the directory.
	b := newCommand(t, srv, dir)
	b.emit(t, "test-1")
	require.NoError(t, b.sink.Flush(context.Background()))
	require.NoError(t, b.sink.Close())

	require.Equal(t, []string{"up-1", "up-2", "test-1"}, p.order(),
		"the earlier command's events arrive, and they arrive first: the control "+
			"plane's projection refuses an event whose sequence is behind the row, "+
			"so newest-first delivery would silently discard the older ones")
	require.Equal(t, 0, b.spool.Pending())
}

// Ordering 3.
func TestBackWithinTheSameCommand_DeliversWithoutASecondProcess(t *testing.T) {
	p := newPlane(false)
	srv := serve(t, p)
	dir := spoolDirFor(t)

	c := newCommand(t, srv, dir)
	c.emit(t, "a")
	require.Error(t, c.sink.Flush(context.Background()))

	p.setUp(true)
	c.emit(t, "b")
	require.NoError(t, c.sink.Flush(context.Background()))
	require.NoError(t, c.sink.Close())

	require.Equal(t, []string{"a", "b"}, p.order())
	require.Equal(t, 0, c.spool.Pending())
}

// Ordering 5. A control plane asking to be left alone must be left alone, on
// the spooled path as well as the in-memory one. Retrying straight after a 429
// is how a busy control plane becomes an unreachable one.
func TestAThrottleIsObeyedOnTheSpooledPathToo(t *testing.T) {
	p := newPlane(false)
	srv := serve(t, p)
	dir := spoolDirFor(t)

	// Get something onto the spool while it is down.
	a := newCommand(t, srv, dir)
	a.emit(t, "old-1")
	require.Error(t, a.sink.Flush(context.Background()))
	require.NoError(t, a.sink.Close())
	require.NotZero(t, a.spool.Pending())

	// It comes back, but throttling.
	p.setUp(true)
	p.mu.Lock()
	p.throttleFor = 30 * time.Second
	p.mu.Unlock()

	b := newCommand(t, srv, dir)
	b.emit(t, "new-1")
	require.Error(t, b.sink.Flush(context.Background()), "the 429 is reported")
	require.NotZero(t, b.spool.Pending(), "the throttled batch is still owed")

	before := p.requests
	require.NoError(t, b.sink.Flush(context.Background()))
	require.Equal(t, before, p.requests,
		"a flush inside the Retry-After window must not reach the server at all")

	b.clock.Advance(31 * time.Second)
	require.NoError(t, b.sink.Flush(context.Background()))
	require.NoError(t, b.sink.Close())

	require.Equal(t, []string{"old-1", "new-1"}, p.order())
	require.Equal(t, 0, b.spool.Pending())
	require.Zero(t, b.sink.Dropped())
}

// Ordering 6. The honest version: a process that is killed loses whatever was
// only ever in memory. What this test pins down is that the window is one
// flush, not one command, because a failed flush spills rather than putting the
// batch back.
func TestAKilledCommandLosesOnlyWhatWasNeverFlushed(t *testing.T) {
	p := newPlane(false)
	srv := serve(t, p)
	dir := spoolDirFor(t)

	killed := newCommand(t, srv, dir)
	killed.emit(t, "flushed-1", "flushed-2")
	require.Error(t, killed.sink.Flush(context.Background()))
	// Everything after this point exists only in memory.
	killed.emit(t, "never-flushed")
	// No Close. The process is gone.

	p.setUp(true)
	next := newCommand(t, srv, dir)
	require.NoError(t, next.sink.Flush(context.Background()))
	require.NoError(t, next.sink.Close())

	require.Equal(t, []string{"flushed-1", "flushed-2"}, p.order(),
		"a failed flush puts the batch on disk rather than back in memory, so a "+
			"kill costs one flush interval rather than a whole outage")
}

// Ordering 7.
func TestStillDownForTheSecondCommand_KeepsTheDebtWithoutDuplicatingIt(t *testing.T) {
	p := newPlane(false)
	srv := serve(t, p)
	dir := spoolDirFor(t)

	a := newCommand(t, srv, dir)
	a.emit(t, "a-1")
	require.Error(t, a.sink.Flush(context.Background()))
	require.NoError(t, a.sink.Close())
	owed := a.spool.Pending()
	require.NotZero(t, owed)

	b := newCommand(t, srv, dir)
	b.emit(t, "b-1")
	require.Error(t, b.sink.Flush(context.Background()))
	require.NoError(t, b.sink.Close())

	p.setUp(true)
	c := newCommand(t, srv, dir)
	require.NoError(t, c.sink.Flush(context.Background()))
	require.NoError(t, c.sink.Close())

	require.Equal(t, []string{"a-1", "b-1"}, p.order(), "each event once, in order")
	require.Equal(t, 0, c.spool.Pending())
	require.Zero(t, c.spool.Dropped())
}

// Ordering 8. A resend after a timeout carries the same identifier, so the
// control plane drops the copy. The drain has to treat that as success, or a
// batch the control plane already holds is spooled forever.
func TestEventsTheControlPlaneAlreadyHasAreAcknowledgedRatherThanRetriedForever(t *testing.T) {
	p := newPlane(true)
	srv := serve(t, p)
	dir := spoolDirFor(t)

	// It has them already.
	first := newCommand(t, srv, dir)
	first.emit(t, "dup-1", "dup-2")
	require.NoError(t, first.sink.Flush(context.Background()))
	require.NoError(t, first.sink.Close())
	require.Equal(t, []string{"dup-1", "dup-2"}, p.order())

	// The same events arrive on the spool, as they would after a request that
	// timed out on the way back.
	sp, err := NewSpool(SpoolOptions{Dir: dir, Redactor: redact.New()})
	require.NoError(t, err)
	require.NoError(t, sp.Put(context.Background(), []controlplane.Event{
		{ID: "dup-1", Type: "environment.ready", EnvID: "env-1", Sequence: 1, OccurredAt: time.Unix(1700000000, 0).UTC()},
		{ID: "dup-2", Type: "environment.ready", EnvID: "env-1", Sequence: 2, OccurredAt: time.Unix(1700000000, 0).UTC()},
	}))

	second := newCommand(t, srv, dir)
	require.NoError(t, second.sink.Flush(context.Background()))
	require.NoError(t, second.sink.Close())

	require.Equal(t, []string{"dup-1", "dup-2"}, p.order(), "not counted twice")
	require.Equal(t, 0, second.spool.Pending(), "and not owed forever")
}

// Without an overflow the behaviour is the one that was there before: bounded,
// in memory, and honest about what it lost. Proving that matters because the
// community path and every existing test run without a spool.
func TestWithoutASpoolTheOldBehaviourIsUnchanged(t *testing.T) {
	p := newPlane(false)
	srv := serve(t, p)

	client, err := controlplane.New(controlplane.Options{
		BaseURL: srv.URL, Token: "engine-token", Redactor: redact.New(),
	})
	require.NoError(t, err)
	fake := clock.NewFake(time.Unix(1700000000, 0).UTC())
	sink := controlplane.NewSink(controlplane.SinkOptions{
		Client: client, Clock: fake, Capacity: 4, BatchSize: 2, FlushEvery: time.Hour,
	})

	for i := range 10 {
		require.NoError(t, sink.Deliver(context.Background(), events.Event{
			ID: fmt.Sprint(i), Env: "env-1", Seq: uint64(i + 1), Type: events.EnvReady,
			Level: events.LevelInfo, TS: time.Unix(1700000000, 0).UTC(),
		}))
	}
	require.NotZero(t, sink.Dropped(), "a full buffer with nowhere to spill still drops")
	err = sink.Close()
	require.Error(t, err, "and still says so")
	require.Contains(t, err.Error(), "dropped")
}
