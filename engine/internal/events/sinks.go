package events

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/antifailure/antifailure/engine/internal/redact"
)

// redactor is the interface a sink needs from the redaction layer. It is an
// interface rather than the concrete type so that sinks can be tested without
// the full rule set, and so that no sink can be constructed without one.
type redactor interface {
	Bytes([]byte) []byte
}

// JSONSink writes one JSON object per line to a writer.
//
// Everything goes through the redactor on its way out, so a message or a data
// field that happens to contain a credential is scrubbed at the boundary
// rather than at the hundreds of places that emit events.
type JSONSink struct {
	name string
	mu   sync.Mutex
	w    *bufio.Writer
	c    io.Closer
	r    redactor
	// minLevel filters below this level. Debug events are voluminous and are
	// off unless asked for.
	minLevel Level
}

// NewJSONSink returns a sink writing to w.
func NewJSONSink(name string, w io.Writer, r redactor, minLevel Level) *JSONSink {
	s := &JSONSink{name: name, w: bufio.NewWriterSize(w, 32*1024), r: r, minLevel: minLevel}
	if c, ok := w.(io.Closer); ok && w != os.Stdout && w != os.Stderr {
		s.c = c
	}
	return s
}

// Name identifies the sink.
func (s *JSONSink) Name() string { return s.name }

// Deliver writes one event.
func (s *JSONSink) Deliver(_ context.Context, e Event) error {
	if !levelAtLeast(e.Level, s.minLevel) {
		return nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("events: marshal %s: %w", e.Type, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.Write(s.r.Bytes(b)); err != nil {
		return fmt.Errorf("events: write %s: %w", s.name, err)
	}
	if err := s.w.WriteByte('\n'); err != nil {
		return fmt.Errorf("events: write %s: %w", s.name, err)
	}
	return nil
}

// Close flushes and closes the underlying writer if it owns one.
func (s *JSONSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.w.Flush()
	if s.c != nil {
		if cerr := s.c.Close(); err == nil {
			err = cerr
		}
	}
	return err
}

// Flush writes buffered events out without closing. The command boundary calls
// it before printing a summary so that the log is complete on disk.
func (s *JSONSink) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Flush()
}

func levelAtLeast(got, min Level) bool {
	return levelRank(got) >= levelRank(min)
}

func levelRank(l Level) int {
	switch l {
	case LevelDebug:
		return 0
	case LevelInfo:
		return 1
	case LevelWarn:
		return 2
	case LevelError:
		return 3
	default:
		return 1
	}
}

// FileSink writes the per environment NDJSON log with size based rotation.
//
// The log lives beside the rest of the local state so that it disappears with
// the environment. Rotation keeps a bounded number of files so that a chatty
// environment cannot fill a laptop's disk; when the disk does fill, the sink
// disables itself and reports rather than failing every subsequent write.
type FileSink struct {
	mu       sync.Mutex
	dir      string
	env      string
	r        redactor
	maxBytes int64
	maxFiles int

	f        *os.File
	w        *bufio.Writer
	written  int64
	disabled bool
	minLevel Level
}

// FileSinkOptions configures a FileSink. The zero value uses the defaults.
type FileSinkOptions struct {
	// MaxBytes is the size at which the current file rotates. Default 64 MiB.
	MaxBytes int64
	// MaxFiles is how many rotated files to keep, newest first. Default 5.
	MaxFiles int
	// MinLevel filters below this level. Default info.
	MinLevel Level
}

// NewFileSink opens the log for an environment under dir.
func NewFileSink(dir, env string, r redactor, opts FileSinkOptions) (*FileSink, error) {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 64 << 20
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 5
	}
	if opts.MinLevel == "" {
		opts.MinLevel = LevelInfo
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("events: create %s: %w", dir, err)
	}
	s := &FileSink{
		dir: dir, env: env, r: r,
		maxBytes: opts.MaxBytes, maxFiles: opts.MaxFiles, minLevel: opts.MinLevel,
	}
	if err := s.open(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileSink) path() string { return filepath.Join(s.dir, s.env+".ndjson") }

func (s *FileSink) open() error {
	f, err := os.OpenFile(s.path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("events: open %s: %w", s.path(), err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("events: stat %s: %w", s.path(), err)
	}
	s.f, s.w, s.written = f, bufio.NewWriterSize(f, 32*1024), info.Size()
	return nil
}

// Name identifies the sink.
func (s *FileSink) Name() string { return "file" }

// Deliver appends one event, rotating first if the file is full.
func (s *FileSink) Deliver(_ context.Context, e Event) error {
	if !levelAtLeast(e.Level, s.minLevel) {
		return nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("events: marshal %s: %w", e.Type, err)
	}
	b = append(s.r.Bytes(b), '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled {
		return errSinkDisabled
	}
	// Reopened rather than refused, because a sink outlives the bus that
	// closed it.
	//
	// A sink is attached to each session as that session opens and closed with
	// it, which is right for a dashboard: one command, one session. `af ci`
	// has two, because its teardown runs after the lifecycle that failed, and
	// the second session's events were being written into a bufio.Writer over
	// a closed file and lost with no error. The symptom was a log that
	// recorded a run failing and never recorded it being torn down, which is
	// the one line somebody reading that log most needs.
	if s.f == nil {
		if err := s.open(); err != nil {
			s.disableLocked()
			return err
		}
	}
	if s.written+int64(len(b)) > s.maxBytes {
		if err := s.rotateLocked(); err != nil {
			s.disableLocked()
			return err
		}
	}
	n, err := s.w.Write(b)
	s.written += int64(n)
	if err != nil {
		// A full disk must not turn every later event into an error that the
		// bus counts one at a time. The sink disables itself once and reports
		// through Disabled, which af status surfaces.
		s.disableLocked()
		return fmt.Errorf("events: write %s: %w", s.path(), err)
	}
	return nil
}

var errSinkDisabled = fmt.Errorf("events: the file sink is disabled after a write failure")

func (s *FileSink) disableLocked() {
	s.disabled = true
	if s.w != nil {
		_ = s.w.Flush()
	}
	if s.f != nil {
		_ = s.f.Close()
		s.f = nil
	}
}

// Disabled reports whether the sink stopped writing after a failure. The
// status command surfaces it, because a silently missing log is worse than a
// noisy one.
func (s *FileSink) Disabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disabled
}

func (s *FileSink) rotateLocked() error {
	if err := s.w.Flush(); err != nil {
		return fmt.Errorf("events: flush before rotate: %w", err)
	}
	if err := s.f.Close(); err != nil {
		return fmt.Errorf("events: close before rotate: %w", err)
	}
	// Shift the numbered files up, dropping the oldest.
	oldest := fmt.Sprintf("%s.%d", s.path(), s.maxFiles)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("events: remove %s: %w", oldest, err)
	}
	for i := s.maxFiles - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", s.path(), i)
		to := fmt.Sprintf("%s.%d", s.path(), i+1)
		if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("events: rotate %s: %w", from, err)
		}
	}
	if err := os.Rename(s.path(), s.path()+".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("events: rotate %s: %w", s.path(), err)
	}
	return s.open()
}

// Close flushes and closes the log.
func (s *FileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled || s.f == nil {
		return nil
	}
	err := s.w.Flush()
	if cerr := s.f.Close(); err == nil {
		err = cerr
	}
	s.f = nil
	return err
}

// MemorySink keeps events in memory. Tests use it; so does the pull request
// comment renderer, which needs the whole run before it can write a summary.
type MemorySink struct {
	mu     sync.Mutex
	events []Event
	// limit bounds retention so that a long run cannot exhaust memory. Zero
	// means unbounded, which only tests should use.
	limit int
}

// NewMemorySink returns a sink retaining at most limit events, oldest evicted.
// A limit of zero retains everything.
func NewMemorySink(limit int) *MemorySink { return &MemorySink{limit: limit} }

// Name identifies the sink.
func (s *MemorySink) Name() string { return "memory" }

// Deliver appends one event.
func (s *MemorySink) Deliver(_ context.Context, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	if s.limit > 0 && len(s.events) > s.limit {
		s.events = s.events[len(s.events)-s.limit:]
	}
	return nil
}

// Close is a no-op.
func (s *MemorySink) Close() error { return nil }

// Events returns a copy of the retained events.
func (s *MemorySink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

// OfType returns the retained events of one type.
func (s *MemorySink) OfType(t Type) []Event {
	var out []Event
	for _, e := range s.Events() {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// FuncSink adapts a function into a sink. The dashboard and the control plane
// client use it.
type FuncSink struct {
	SinkName string
	Fn       func(context.Context, Event) error
	CloseFn  func() error
}

// Name identifies the sink.
func (s *FuncSink) Name() string { return s.SinkName }

// Deliver calls the function.
func (s *FuncSink) Deliver(ctx context.Context, e Event) error { return s.Fn(ctx, e) }

// Close calls the close function if there is one.
func (s *FuncSink) Close() error {
	if s.CloseFn != nil {
		return s.CloseFn()
	}
	return nil
}

// Reader replays a recorded NDJSON stream.
//
// af logs uses it, and so does the pull request comment renderer when it runs
// in a later job than the one that produced the events. Rotated files are read
// oldest first so that a replay is in the original order.
type Reader struct {
	dir string
	env string
}

// NewReader returns a replay reader for an environment's logs under dir.
func NewReader(dir, env string) *Reader { return &Reader{dir: dir, env: env} }

// Files returns the log files for the environment, oldest first.
func (r *Reader) Files() ([]string, error) {
	base := filepath.Join(r.dir, r.env+".ndjson")
	matches, err := filepath.Glob(base + "*")
	if err != nil {
		return nil, fmt.Errorf("events: list logs: %w", err)
	}
	// base.N is older than base.N-1, and base itself is newest, so sorting by
	// the numeric suffix descending puts the oldest first.
	sort.Slice(matches, func(i, j int) bool {
		return suffixNum(matches[i], base) > suffixNum(matches[j], base)
	})
	return matches, nil
}

func suffixNum(path, base string) int {
	if path == base {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimPrefix(path, base), ".%d", &n); err != nil {
		return 0
	}
	return n
}

// Each calls fn for every recorded event, oldest first.
//
// A malformed line is skipped rather than ending the replay. A log is
// append-only and can be truncated by a crash mid-write, and losing the whole
// history because the last line is half written would be the wrong trade.
func (r *Reader) Each(fn func(Event) error) error {
	files, err := r.Files()
	if err != nil {
		return err
	}
	for _, path := range files {
		if err := r.eachFile(path, fn); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reader) eachFile(path string, fn func(Event) error) error {
	f, err := os.Open(path) //nolint:gosec // the path comes from our own state directory
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("events: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("events: read %s: %w", path, err)
	}
	return nil
}

// All returns every recorded event, oldest first.
func (r *Reader) All() ([]Event, error) {
	var out []Event
	err := r.Each(func(e Event) error {
		out = append(out, e)
		return nil
	})
	return out, err
}

// Compile time proof that the concrete redactor satisfies the sink interface.
var _ redactor = (*redact.Redactor)(nil)

// LastSequence reads the highest sequence number an environment's log holds.
//
// It is how a new command continues the counter the last one left, so that
// `Event.Seq` means what its own documentation says: monotonic per
// environment, and a gap in it is a gap rather than a restart.
//
// A missing, empty, or unreadable log returns zero, which starts the counter
// at one. That is the right answer for a first run and the safe answer for a
// damaged file: numbering from the beginning is confusing, and refusing to run
// a command because its log could not be read would be worse.
func LastSequence(dir, env string) uint64 {
	f, err := os.Open(filepath.Join(dir, env+".ndjson"))
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()

	// Read from the end. A log is append only and rotates at 64 MiB, so the
	// answer is in the last line and scanning the whole file to find it would
	// cost a command a megabyte of reading before it did anything.
	info, err := f.Stat()
	if err != nil {
		return 0
	}
	const window = 64 << 10
	start := info.Size() - window
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return 0
	}
	body, err := io.ReadAll(io.LimitReader(f, window+1))
	if err != nil {
		return 0
	}

	var highest uint64
	for _, line := range strings.Split(string(body), "\n") {
		var e struct {
			Env string `json:"env"`
			Seq uint64 `json:"seq"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			// A partial first line, from seeking into the middle of one.
			continue
		}
		if e.Env == env && e.Seq > highest {
			highest = e.Seq
		}
	}
	return highest
}
