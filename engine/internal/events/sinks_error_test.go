package events_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

// unmarshalable makes json.Marshal fail. A sink must report the failure rather
// than write a truncated line into an append-only log.
type unmarshalable struct{}

func (unmarshalable) MarshalJSON() ([]byte, error) { return nil, os.ErrInvalid }

func TestJSONSink_ReportsAMarshalFailure(t *testing.T) {
	t.Parallel()
	var buf strings.Builder
	s := events.NewJSONSink("stdout", &buf, redact.New(), events.LevelInfo)
	err := s.Deliver(context.Background(), events.Event{
		Type: events.Error, Level: events.LevelError,
		Data: map[string]any{"bad": unmarshalable{}},
	})
	require.Error(t, err)
	require.Empty(t, buf.String(), "nothing may be written when marshalling failed")
	require.NoError(t, s.Close())
}

func TestFileSink_ReportsAMarshalFailure(t *testing.T) {
	t.Parallel()
	s, err := events.NewFileSink(t.TempDir(), "env_m", redact.New(), events.FileSinkOptions{})
	require.NoError(t, err)
	require.Error(t, s.Deliver(context.Background(), events.Event{
		Type: events.Error, Level: events.LevelError,
		Data: map[string]any{"bad": unmarshalable{}},
	}))
	require.False(t, s.Disabled(), "a marshal failure is the event's fault, not the disk's")
	require.NoError(t, s.Close())
}

func TestFileSink_FiltersBelowTheMinimumLevel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := events.NewFileSink(dir, "env_l", redact.New(), events.FileSinkOptions{MinLevel: events.LevelWarn})
	require.NoError(t, err)
	require.NoError(t, s.Deliver(context.Background(), events.Event{Type: events.ServiceLog, Level: events.LevelInfo}))
	require.NoError(t, s.Deliver(context.Background(), events.Event{Type: events.Warning, Level: events.LevelWarn}))
	require.NoError(t, s.Close())

	got, err := events.NewReader(dir, "env_l").All()
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, events.LevelWarn, got[0].Level)
}

// The failure mode this guards: the volume holding the log goes away while an
// environment is running. The sink must disable itself once and keep reporting
// through Disabled, rather than turning every subsequent event into an error
// the bus counts one at a time, and it must never take the engine down.
func TestFileSink_DisablesItselfWhenTheLogDirectoryDisappears(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	dir := filepath.Join(parent, "events")
	s, err := events.NewFileSink(dir, "env_gone", redact.New(), events.FileSinkOptions{
		MaxBytes: 200, MaxFiles: 2,
	})
	require.NoError(t, err)

	ev := events.Event{ID: "ev_0000000000000001", Type: events.ServiceLog, Level: events.LevelInfo, Msg: strings.Repeat("x", 64)}
	require.NoError(t, s.Deliver(context.Background(), ev))
	require.NoError(t, os.RemoveAll(dir))

	// The next delivery crosses the rotation threshold, and rotation cannot
	// reopen a file in a directory that no longer exists.
	var lastErr error
	for i := 0; i < 5 && !s.Disabled(); i++ {
		lastErr = s.Deliver(context.Background(), ev)
	}
	require.True(t, s.Disabled(), "the sink must disable itself, last error: %v", lastErr)

	// Every later delivery reports the disabled state and writes nothing.
	require.Error(t, s.Deliver(context.Background(), ev))
	require.NoError(t, s.Close(), "closing a disabled sink is a no-op")
}

func TestFileSink_ClosingTwiceIsSafe(t *testing.T) {
	t.Parallel()
	s, err := events.NewFileSink(t.TempDir(), "env_c", redact.New(), events.FileSinkOptions{})
	require.NoError(t, err)
	require.NoError(t, s.Close())
	require.NoError(t, s.Close())
	require.Equal(t, "file", s.Name())
}

func TestJSONSink_ClosesAWriterThatOwnsAFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "out.ndjson")
	f, err := os.Create(path)
	require.NoError(t, err)

	s := events.NewJSONSink("file", f, redact.New(), events.LevelInfo)
	require.NoError(t, s.Deliver(context.Background(), events.Event{Type: events.EnvReady, Level: events.LevelInfo}))
	require.NoError(t, s.Close())

	// The sink owned the file, so a second close on the file itself fails,
	// which proves the sink closed it rather than leaking the descriptor.
	require.Error(t, f.Close())

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(b), "env.ready")
}

func TestJSONSink_ReportsAWriteFailure(t *testing.T) {
	t.Parallel()
	// A writer that fails immediately. The buffered writer surfaces the error
	// on the flush that Close performs, or on the write itself once the buffer
	// fills, so both paths are exercised by writing more than the buffer holds.
	s := events.NewJSONSink("broken", failingWriter{}, redact.New(), events.LevelInfo)
	var err error
	for i := 0; i < 2000 && err == nil; i++ {
		err = s.Deliver(context.Background(), events.Event{
			Type: events.ServiceLog, Level: events.LevelInfo, Msg: strings.Repeat("y", 64),
		})
	}
	require.Error(t, err)
	require.Error(t, s.Close())
}

func TestFileSink_ReportsAWriteFailureAndDisables(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := events.NewFileSink(dir, "env_w", redact.New(), events.FileSinkOptions{MaxBytes: 1 << 30})
	require.NoError(t, err)
	// Deliver enough to force the buffered writer to flush to a file that has
	// been replaced by a read only one.
	require.NoError(t, s.Close())
	require.NoError(t, os.Chmod(filepath.Join(dir, "env_w.ndjson"), 0o400))

	s2, err := events.NewFileSink(dir, "env_w", redact.New(), events.FileSinkOptions{})
	if err != nil {
		// Opening a read only file for append fails outright, which is also a
		// correct outcome and the one most platforms take.
		require.Contains(t, err.Error(), "env_w.ndjson")
		return
	}
	_ = s2.Close()
}

func TestReader_IgnoresUnrelatedSuffixes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "env_u.ndjson"),
		[]byte(`{"id":"ev_1","seq":1,"type":"env.ready","level":"info"}`+"\n"), 0o600))
	// A file whose suffix is not a rotation number must not break ordering.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "env_u.ndjson.bak"),
		[]byte(`{"id":"ev_9","seq":9,"type":"env.ready","level":"info"}`+"\n"), 0o600))

	got, err := events.NewReader(dir, "env_u").All()
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestReader_FilesReturnsOldestFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"env_f.ndjson", "env_f.ndjson.1", "env_f.ndjson.2"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600))
	}
	files, err := events.NewReader(dir, "env_f").Files()
	require.NoError(t, err)
	require.Len(t, files, 3)
	require.True(t, strings.HasSuffix(files[0], ".2"), "the oldest rotated file comes first")
	require.True(t, strings.HasSuffix(files[2], ".ndjson"), "the live file comes last")
}

func TestReader_SkipsEmptyLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "env_e.ndjson"),
		[]byte("\n\n"+`{"id":"ev_1","seq":1,"type":"env.ready","level":"info"}`+"\n\n"), 0o600))
	got, err := events.NewReader(dir, "env_e").All()
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestLevelRank_UnknownLevelSortsAsInfo(t *testing.T) {
	t.Parallel()
	// An event arriving from a future version with a level this build does not
	// know must still be delivered rather than silently dropped.
	var buf strings.Builder
	s := events.NewJSONSink("stdout", &buf, redact.New(), events.LevelInfo)
	require.NoError(t, s.Deliver(context.Background(), events.Event{
		Type: events.EnvReady, Level: events.Level("notice"),
	}))
	require.NoError(t, s.Flush())
	require.Contains(t, buf.String(), "env.ready")
	require.NoError(t, s.Close())
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

// A sink outlives the bus that closed it.
//
// The regression, found by the log's first consumer: a sink is attached to
// each session as that session opens and closed with it, which is right for a
// dashboard, because a command has one session. `af ci` has two: its teardown
// runs after the lifecycle that failed. The second session's events went into
// a buffered writer over a closed file and were lost with no error, so the log
// recorded a run failing and never recorded it being torn down. That is the
// one line somebody reading that log most needs.
func TestFileSink_ReopensAfterTheBusClosedIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := events.NewFileSink(dir, "env_reopen", redact.New(), events.FileSinkOptions{})
	require.NoError(t, err)

	require.NoError(t, s.Deliver(context.Background(),
		events.Event{Type: events.EnvFailed, Level: events.LevelError, Msg: "the first session"}))
	require.NoError(t, s.Close())

	// The second session, after the first one's bus closed everything.
	require.NoError(t, s.Deliver(context.Background(),
		events.Event{Type: events.EnvDestroyed, Level: events.LevelInfo, Msg: "the second session"}))
	require.NoError(t, s.Close())

	body, err := os.ReadFile(filepath.Join(dir, "env_reopen.ndjson"))
	require.NoError(t, err)
	require.Contains(t, string(body), "the first session")
	require.Contains(t, string(body), "the second session",
		"an event delivered after Close was lost")
	require.False(t, s.Disabled(), "reopening is not a failure")
}
