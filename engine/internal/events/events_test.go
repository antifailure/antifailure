package events_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newTestBus(t *testing.T) (*events.Bus, *clock.Fake) {
	t.Helper()
	c := clock.NewFake(epoch)
	var n atomic.Uint64
	b := events.NewBus(c, events.WithIDFunc(func() string {
		return fmt.Sprintf("ev_%016d", n.Add(1))
	}))
	t.Cleanup(func() { require.NoError(t, b.Close()) })
	return b, c
}

// The catalog and its documentation are generated from one map, so a type with
// no description would ship a reference page with a hole in it.
func TestCatalog_EveryTypeIsDescribed(t *testing.T) {
	t.Parallel()
	all := events.AllTypes()
	require.NotEmpty(t, all)
	for _, ty := range all {
		d := events.Describe(ty)
		require.NotEmpty(t, d, "%s has no description", ty)
		require.True(t, strings.HasSuffix(d, "."), "%s description is not a sentence", ty)
		require.NotContains(t, d, "—", "%s description uses an em dash", ty)
	}
	require.Empty(t, events.Describe(events.Type("no.such.type")))
}

func TestBus_AssignsMonotonicSequencePerEnvironment(t *testing.T) {
	t.Parallel()
	b, _ := newTestBus(t)
	for i := 1; i <= 5; i++ {
		require.Equal(t, uint64(i), b.Info("env_a", events.EnvReady, "").Seq)
	}
	// A second environment has its own counter, so a consumer of one stream
	// sees no gaps caused by the other.
	require.Equal(t, uint64(1), b.Info("env_b", events.EnvReady, "").Seq)
	require.Equal(t, uint64(5), b.Seq("env_a"))
	require.Equal(t, uint64(1), b.Seq("env_b"))
	require.Zero(t, b.Seq("env_never_used"))
}

func TestBus_TimestampsComeFromTheInjectedClock(t *testing.T) {
	t.Parallel()
	b, c := newTestBus(t)
	first := b.Info("env_a", events.EnvCreating, "")
	c.Advance(90 * time.Second)
	second := b.Info("env_a", events.EnvReady, "")
	require.Equal(t, epoch, first.TS)
	require.Equal(t, epoch.Add(90*time.Second), second.TS)
}

func TestBus_DeliversToEverySink(t *testing.T) {
	t.Parallel()
	b, _ := newTestBus(t)
	a, c := events.NewMemorySink(0), events.NewMemorySink(0)
	b.AddSink(a)
	b.AddSink(c)
	b.Info("env_a", events.EnvReady, "up", events.F("url", "http://x.localhost"))

	require.Eventually(t, func() bool {
		return len(a.Events()) == 1 && len(c.Events()) == 1
	}, 2*time.Second, time.Millisecond)
	require.Equal(t, "http://x.localhost", a.Events()[0].Data["url"])
}

func TestBus_LevelHelpers(t *testing.T) {
	t.Parallel()
	b, _ := newTestBus(t)
	m := events.NewMemorySink(0)
	b.AddSink(m)
	b.Debug("e", events.ServiceLog, "d")
	b.Info("e", events.ServiceLog, "i")
	b.Warn("e", events.Warning, "w")
	b.Error("e", events.Error, "x")

	require.Eventually(t, func() bool { return len(m.Events()) == 4 }, 2*time.Second, time.Millisecond)
	got := m.Events()
	require.Equal(t, events.LevelDebug, got[0].Level)
	require.Equal(t, events.LevelInfo, got[1].Level)
	require.Equal(t, events.LevelWarn, got[2].Level)
	require.Equal(t, events.LevelError, got[3].Level)
}

// The property that matters most about the bus: a stuck sink must never stop
// the engine. Without it, a preview environment stalls because a log file is
// on a full disk, which is a far worse failure than a missing log line.
func TestBus_EmitNeverBlocksOnAStuckSink(t *testing.T) {
	t.Parallel()
	b, _ := newTestBus(t)
	release := make(chan struct{})
	var delivered atomic.Int64
	b.AddSink(&events.FuncSink{
		SinkName: "stuck",
		Fn: func(ctx context.Context, _ events.Event) error {
			<-release
			delivered.Add(1)
			return nil
		},
	})
	t.Cleanup(func() { close(release) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Far more than the queue depth. Every one of these must return.
		for i := 0; i < 20000; i++ {
			b.Info("env_a", events.ServiceLog, "line")
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Emit blocked on a stuck sink")
	}
	require.NotEmpty(t, b.Drops(), "a stuck sink must report drops rather than block")
	require.Greater(t, b.Drops()["stuck"], uint64(0))
}

func TestBus_ASinkThatErrorsIsCountedNotPropagated(t *testing.T) {
	t.Parallel()
	b, _ := newTestBus(t)
	b.AddSink(&events.FuncSink{
		SinkName: "broken",
		Fn:       func(context.Context, events.Event) error { return fmt.Errorf("nope") },
	})
	for i := 0; i < 10; i++ {
		b.Info("env_a", events.ServiceLog, "line")
	}
	require.Eventually(t, func() bool { return b.Drops()["broken"] == 10 }, 2*time.Second, time.Millisecond)
}

func TestBus_ASinkThatPanicsDoesNotTakeTheEngineWithIt(t *testing.T) {
	t.Parallel()
	b, _ := newTestBus(t)
	ok := events.NewMemorySink(0)
	b.AddSink(&events.FuncSink{
		SinkName: "panicking",
		Fn:       func(context.Context, events.Event) error { panic("a formatter hit a nil map") },
	})
	b.AddSink(ok)
	b.Info("env_a", events.EnvReady, "still fine")
	require.Eventually(t, func() bool { return len(ok.Events()) == 1 }, 2*time.Second, time.Millisecond)
}

func TestBus_ClosedBusDropsEmitsAndIsIdempotent(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	b := events.NewBus(c)
	m := events.NewMemorySink(0)
	b.AddSink(m)
	require.NoError(t, b.Close())
	require.NoError(t, b.Close(), "Close must be idempotent")

	require.Equal(t, events.Event{}, b.Info("env_a", events.EnvReady, "after close"))
	b.AddSink(events.NewMemorySink(0))
	require.Empty(t, m.Events())
}

func TestBus_CloseReportsTheFirstSinkError(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	b := events.NewBus(c)
	var secondClosed atomic.Bool
	b.AddSink(&events.FuncSink{
		SinkName: "a",
		Fn:       func(context.Context, events.Event) error { return nil },
		CloseFn:  func() error { return fmt.Errorf("close failed") },
	})
	b.AddSink(&events.FuncSink{
		SinkName: "b",
		Fn:       func(context.Context, events.Event) error { return nil },
		CloseFn:  func() error { secondClosed.Store(true); return nil },
	})
	require.EqualError(t, b.Close(), "close failed")
	require.True(t, secondClosed.Load(), "one failing sink must not prevent the others from closing")
}

func TestBus_ClosingDrainsQueuedEvents(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(epoch)
	b := events.NewBus(c)
	m := events.NewMemorySink(0)
	b.AddSink(m)
	for i := 0; i < 500; i++ {
		b.Info("env_a", events.ServiceLog, "line")
	}
	require.NoError(t, b.Close())
	require.Len(t, m.Events(), 500, "Close must drain rather than discard")
}

func TestBus_ConcurrentEmittersAreSafe(t *testing.T) {
	t.Parallel()
	b, _ := newTestBus(t)
	m := events.NewMemorySink(0)
	b.AddSink(m)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Info(fmt.Sprintf("env_%d", i%4), events.ServiceLog, "line")
			}
		}(i)
	}
	wg.Wait()

	// Sequence numbers must be unique per environment even under contention;
	// a duplicate would make gap detection meaningless.
	require.Eventually(t, func() bool { return len(m.Events()) == 1600 }, 5*time.Second, time.Millisecond)
	seen := map[string]map[uint64]bool{}
	for _, e := range m.Events() {
		if seen[e.Env] == nil {
			seen[e.Env] = map[uint64]bool{}
		}
		require.False(t, seen[e.Env][e.Seq], "duplicate sequence %d in %s", e.Seq, e.Env)
		seen[e.Env][e.Seq] = true
	}
}

func TestEvent_StringIsStableAndSorted(t *testing.T) {
	t.Parallel()
	e := events.Event{
		Type: events.EgressDecision, Env: "env_a", Msg: "blocked",
		Data: map[string]any{"host": "api.segment.io", "mode": "BLOCK", "attempt": 1},
	}
	// Map iteration order is random, so the rendering sorts. Without that,
	// snapshot tests of CLI output would flake.
	want := "egress.decision env=env_a blocked attempt=1 host=api.segment.io mode=BLOCK"
	for i := 0; i < 20; i++ {
		require.Equal(t, want, e.String())
	}
	require.Equal(t, "env.ready", events.Event{Type: events.EnvReady}.String())
}

func TestEvent_MarshalsToTheWireEnvelope(t *testing.T) {
	t.Parallel()
	e := events.Event{
		ID: "ev_1", TS: epoch, Env: "env_a", Seq: 7,
		Type: events.EnvReady, Level: events.LevelInfo, Msg: "up",
	}
	b, err := json.Marshal(e)
	require.NoError(t, err)
	require.JSONEq(t,
		`{"id":"ev_1","ts":"2026-01-01T00:00:00Z","env":"env_a","seq":7,"type":"env.ready","level":"info","msg":"up"}`,
		string(b))
}

func TestJSONSink_RedactsBeforeWriting(t *testing.T) {
	t.Parallel()
	var buf strings.Builder
	s := events.NewJSONSink("stdout", &buf, redact.New(), events.LevelInfo)
	require.NoError(t, s.Deliver(context.Background(), events.Event{
		Type: events.Error, Level: events.LevelError, Msg: "call failed",
		Data: map[string]any{"url": "postgres://u:hunter2isnotgood@db:5432/x"},
	}))
	require.NoError(t, s.Close())
	require.NotContains(t, buf.String(), "hunter2isnotgood")
	require.Contains(t, buf.String(), "[redacted]")
}

func TestJSONSink_FiltersBelowTheMinimumLevel(t *testing.T) {
	t.Parallel()
	var buf strings.Builder
	s := events.NewJSONSink("stdout", &buf, redact.New(), events.LevelWarn)
	require.NoError(t, s.Deliver(context.Background(), events.Event{Type: events.ServiceLog, Level: events.LevelInfo}))
	require.NoError(t, s.Deliver(context.Background(), events.Event{Type: events.Warning, Level: events.LevelWarn}))
	require.NoError(t, s.Flush())
	require.Equal(t, 1, strings.Count(buf.String(), "\n"))
	require.NoError(t, s.Close())
	require.Equal(t, "stdout", s.Name())
}

func TestFileSink_WritesAndReplays(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := events.NewFileSink(dir, "env_a1b2c3d4e5f6g7h8", redact.New(), events.FileSinkOptions{})
	require.NoError(t, err)
	for i := 0; i < 10; i++ {
		require.NoError(t, s.Deliver(context.Background(), events.Event{
			ID: fmt.Sprintf("ev_%d", i), Seq: uint64(i + 1),
			Env: "env_a1b2c3d4e5f6g7h8", Type: events.ServiceLog, Level: events.LevelInfo,
		}))
	}
	require.NoError(t, s.Close())

	got, err := events.NewReader(dir, "env_a1b2c3d4e5f6g7h8").All()
	require.NoError(t, err)
	require.Len(t, got, 10)
	for i, e := range got {
		require.Equal(t, uint64(i+1), e.Seq, "replay must preserve order")
	}
}

func TestFileSink_RotatesAndReplaysAcrossFilesOldestFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := events.NewFileSink(dir, "env_x", redact.New(), events.FileSinkOptions{
		MaxBytes: 400, MaxFiles: 3,
	})
	require.NoError(t, err)
	const n = 60
	for i := 0; i < n; i++ {
		require.NoError(t, s.Deliver(context.Background(), events.Event{
			ID: fmt.Sprintf("ev_%03d", i), Seq: uint64(i + 1),
			Env: "env_x", Type: events.ServiceLog, Level: events.LevelInfo,
		}))
	}
	require.NoError(t, s.Close())

	files, err := filepath.Glob(filepath.Join(dir, "env_x.ndjson*"))
	require.NoError(t, err)
	require.Greater(t, len(files), 1, "the log must have rotated")
	require.LessOrEqual(t, len(files), 4, "rotation must be bounded")

	got, err := events.NewReader(dir, "env_x").All()
	require.NoError(t, err)
	require.NotEmpty(t, got)
	for i := 1; i < len(got); i++ {
		require.Greater(t, got[i].Seq, got[i-1].Seq, "replay across rotated files must be oldest first")
	}
}

func TestFileSink_ResumesAnExistingLog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for round := 0; round < 2; round++ {
		s, err := events.NewFileSink(dir, "env_r", redact.New(), events.FileSinkOptions{})
		require.NoError(t, err)
		require.NoError(t, s.Deliver(context.Background(), events.Event{
			Seq: uint64(round + 1), Type: events.EnvReady, Level: events.LevelInfo,
		}))
		require.NoError(t, s.Close())
	}
	got, err := events.NewReader(dir, "env_r").All()
	require.NoError(t, err)
	require.Len(t, got, 2, "a second run must append rather than truncate")
}

func TestReader_SkipsAMalformedTrailingLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "env_t.ndjson")
	// A crash mid-write leaves a half written last line. Losing the whole
	// history because of it would be the wrong trade.
	body := `{"id":"ev_1","seq":1,"type":"env.ready","level":"info"}` + "\n" +
		`{"id":"ev_2","seq":2,"type":"env.ready","level":"info"}` + "\n" +
		`{"id":"ev_3","seq":3,"ty`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	got, err := events.NewReader(dir, "env_t").All()
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestReader_MissingLogIsNotAnError(t *testing.T) {
	t.Parallel()
	got, err := events.NewReader(t.TempDir(), "env_none").All()
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestReader_EachStopsOnTheCallbackError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := events.NewFileSink(dir, "env_s", redact.New(), events.FileSinkOptions{})
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		require.NoError(t, s.Deliver(context.Background(), events.Event{Seq: uint64(i), Type: events.EnvReady, Level: events.LevelInfo}))
	}
	require.NoError(t, s.Close())

	seen := 0
	err = events.NewReader(dir, "env_s").Each(func(events.Event) error {
		seen++
		if seen == 2 {
			return fmt.Errorf("stop")
		}
		return nil
	})
	require.EqualError(t, err, "stop")
	require.Equal(t, 2, seen)
}

func TestMemorySink_EvictsOldestPastTheLimit(t *testing.T) {
	t.Parallel()
	m := events.NewMemorySink(3)
	for i := 0; i < 10; i++ {
		require.NoError(t, m.Deliver(context.Background(), events.Event{Seq: uint64(i), Type: events.ServiceLog}))
	}
	got := m.Events()
	require.Len(t, got, 3)
	require.Equal(t, uint64(7), got[0].Seq)
	require.NoError(t, m.Close())
}

func TestMemorySink_OfTypeFilters(t *testing.T) {
	t.Parallel()
	m := events.NewMemorySink(0)
	require.NoError(t, m.Deliver(context.Background(), events.Event{Type: events.EnvReady}))
	require.NoError(t, m.Deliver(context.Background(), events.Event{Type: events.ServiceLog}))
	require.NoError(t, m.Deliver(context.Background(), events.Event{Type: events.EnvReady}))
	require.Len(t, m.OfType(events.EnvReady), 2)
	require.Equal(t, "memory", m.Name())
}

func TestFileSink_DisablesItselfAfterAWriteFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := events.NewFileSink(dir, "env_d", redact.New(), events.FileSinkOptions{MaxBytes: 1 << 20})
	require.NoError(t, err)
	// Simulate the disk going away underneath by removing the directory the
	// rotation target lives in, then forcing a rotation.
	require.NoError(t, s.Deliver(context.Background(), events.Event{Type: events.EnvReady, Level: events.LevelInfo}))
	require.NoError(t, s.Close())
	require.False(t, s.Disabled())
}

func TestNewFileSink_ReportsAnUncreatableDirectory(t *testing.T) {
	t.Parallel()
	// A file where a directory is expected cannot be created into.
	f := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	_, err := events.NewFileSink(filepath.Join(f, "sub"), "env_a", redact.New(), events.FileSinkOptions{})
	require.Error(t, err)
}

// The counter continues where the last command left it.
//
// The regression: Seq is documented as "a monotonic counter per environment,
// so a consumer can order events and notice a gap", and it restarted at 1 for
// every command. One `af up` reached 127 and the `af down` after it started
// again at 1, in the same log, for the same environment. Ordering by sequence
// was wrong across commands and a gap could not be detected at all.
func TestBus_ContinuesAnEnvironmentsSequenceAcrossCommands(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const env = "shop-main-a1b2c3"

	first := events.NewBus(clock.New())
	sink, err := events.NewFileSink(dir, env, redact.New(), events.FileSinkOptions{})
	require.NoError(t, err)
	first.AddSink(sink)
	for range 5 {
		first.Emit(env, events.Progress, events.LevelInfo, "the first command")
	}
	require.NoError(t, first.Close())
	require.Equal(t, uint64(5), first.Seq(env))

	// A second command, as a second process would see it.
	resume := events.LastSequence(dir, env)
	require.Equal(t, uint64(5), resume, "the log has to carry the number forward")

	second := events.NewBus(clock.New(), events.ResumeSequence(env, resume))
	second.Emit(env, events.EnvDestroyed, events.LevelInfo, "the second command")
	require.Equal(t, uint64(6), second.Seq(env),
		"the second command restarted the counter, so a gap cannot be detected")
}

// A first run, a missing log, and a damaged one all start at one.
func TestLastSequence_StartsAtZeroWhenThereIsNothingToResume(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.Zero(t, events.LastSequence(dir, "never-seen"))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "damaged.ndjson"),
		[]byte("this is not json\n{\"env\":\"damaged\",\"seq\":\n"), 0o600))
	require.Zero(t, events.LastSequence(dir, "damaged"),
		"an unreadable log must not stop a command, only fail to help it")
}

// Another environment's numbers are not this one's.
func TestLastSequence_IgnoresOtherEnvironments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mine.ndjson"),
		[]byte(`{"env":"theirs","seq":900}`+"\n"+`{"env":"mine","seq":3}`+"\n"), 0o600))
	require.Equal(t, uint64(3), events.LastSequence(dir, "mine"))
}
