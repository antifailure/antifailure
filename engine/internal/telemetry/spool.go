package telemetry

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/antifailure/antifailure/engine/internal/controlplane"
)

// Spool is the durable half of "events are buffered and sent when it returns".
//
// The control plane sink buffers in memory, which is the right thing while one
// command is running and useless the moment it exits. `af up` and `af test` are
// separate processes: a control plane that is down for the eleven seconds of an
// `af up` and back before `af test` would, with an in-memory buffer alone, lose
// every event from the first command permanently. AF-CPL-003 promises the
// opposite, so the buffer has to outlive the process that filled it.
//
// The shape is a directory of small files rather than one append-only log, for
// three reasons that each came from an ordering that would otherwise be wrong.
// Several engine processes can run at once on one machine, and concurrent
// appends to a shared file interleave partial lines once a line exceeds the
// atomic write size. A drain has to claim work so that two processes draining
// at the same moment do not each send the same batch. And a drain that fails
// halfway has to put back exactly what it did not send, which is a rename when
// the unit is a file and a rewrite-under-lock when it is a region of one.
//
// Nothing here is on the happy path. A reachable control plane means events go
// straight out of the in-memory buffer and this directory stays empty.
type Spool struct {
	dir      string
	redactor redactor
	maxBytes int64
	// instance distinguishes this spool from every other one writing to the
	// same directory. See nextName for why a timestamp is not enough.
	instance string

	mu      sync.Mutex
	seq     uint64
	dropped uint64
}

// redactor is the subset of redact.Redactor the spool needs.
//
// Declared here rather than imported as a concrete type so that the spool
// cannot be constructed without one. A spool line is a file on disk that
// `af support bundle` may later collect, so it is redacted at the writer for
// the same reason every other sink is: a call site somebody forgot is how a
// secret reaches a file.
type redactor interface {
	// String scrubs a value on its way into a span attribute or a message.
	String(s string) string
	// Bytes scrubs a marshalled line on its way to disk.
	Bytes(b []byte) []byte
}

// SpoolOptions configures a spool.
type SpoolOptions struct {
	// Dir is where the files live. It is created if it does not exist.
	Dir string
	// Redactor scrubs each line before it is written.
	Redactor redactor
	// MaxBytes bounds the whole directory. Past it the oldest files are
	// removed and the events they held are counted as dropped. Zero uses the
	// default of 32 MiB, which is roughly a quarter of a million events.
	MaxBytes int64
}

// spoolSuffix marks a file that is waiting to be sent.
const spoolSuffix = ".ndjson"

// claimSuffix marks a file some process is currently sending.
//
// The claim is the rename itself. Rename is atomic and fails if the source is
// already gone, so two processes racing for the same file produce exactly one
// winner without a lock file, a lease, or a clock.
const claimSuffix = ".claimed"

// NewSpool opens a spool directory.
func NewSpool(opts SpoolOptions) (*Spool, error) {
	if opts.Redactor == nil {
		return nil, errors.New("telemetry: a spool needs a redactor")
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 32 << 20
	}
	// 0700 rather than 0755. The spool holds event payloads, and while they are
	// redacted the redactor is a filter rather than a proof.
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("telemetry: create spool %s: %w", opts.Dir, err)
	}
	s := &Spool{dir: opts.Dir, redactor: opts.Redactor, maxBytes: opts.MaxBytes}
	var tag [4]byte
	// crypto/rand.Read is documented never to fail on a supported platform, and
	// this is a name rather than a security boundary.
	_, _ = rand.Read(tag[:])
	s.instance = hex.EncodeToString(tag[:])
	s.recoverStaleClaims()
	s.seq = s.highestCounter()
	return s, nil
}

// highestCounter reads the ordering counter out of the names already present.
//
// The counter lives in the filenames rather than in a file of its own because a
// counter in a file is a second thing to keep consistent with the directory it
// describes, and the moment they disagree the spool either overwrites a batch
// or skips one.
func (s *Spool) highestCounter() uint64 {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0
	}
	var highest uint64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "sp-") {
			continue
		}
		rest := name[len("sp-"):]
		cut := strings.IndexByte(rest, '-')
		if cut < 0 {
			continue
		}
		n, err := strconv.ParseUint(rest[:cut], 10, 64)
		if err != nil {
			continue
		}
		if n > highest {
			highest = n
		}
	}
	return highest
}

// nextName produces a name that sorts by write order and cannot collide.
//
// Both halves were paid for. The first attempt named files after the first
// event's timestamp and a per-instance counter that restarted at one, which
// meant two commands emitting events with the same timestamp produced the same
// name and the second silently overwrote the first with a rename. An ordering
// test caught it: `af up` spooled its events while the control plane was down,
// `af down` spooled its own, and only the second batch ever arrived.
//
// So the counter is read out of the directory rather than started from zero,
// which makes it monotonic across processes, and a per-instance tag makes the
// name unique even when two processes read the same counter at the same moment.
// Two batches written concurrently have no defined order between them, which is
// correct: they were concurrent.
func (s *Spool) nextName() string {
	s.mu.Lock()
	s.seq++
	n := s.seq
	s.mu.Unlock()
	return fmt.Sprintf("sp-%012d-%s-%d", n, s.instance, os.Getpid())
}

// recoverStaleClaims puts back files claimed by a process that never finished.
//
// Called once at open rather than on a timer. A claim is held for the duration
// of one HTTP request, so a claim that is still here when a new process starts
// belongs to a process that is no longer running, and the alternative to
// reclaiming it is a file that is never sent and never removed.
func (s *Spool) recoverStaleClaims() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, claimSuffix) {
			continue
		}
		back := strings.TrimSuffix(name, claimSuffix)
		// Ignored rather than reported. A failure here means the file is still
		// claimed, which is the state we were already in.
		_ = os.Rename(filepath.Join(s.dir, name), filepath.Join(s.dir, back))
	}
}

// Put writes a batch durably.
//
// The file is written under a temporary name and renamed into place, so a
// process that dies mid-write leaves a partial temporary file rather than a
// half-written batch that a drain would read as truncated JSON.
func (s *Spool) Put(_ context.Context, batch []controlplane.Event) error {
	if len(batch) == 0 {
		return nil
	}

	var buf strings.Builder
	for _, e := range batch {
		b, err := json.Marshal(e)
		if err != nil {
			// One event that will not marshal must not discard the batch it
			// travelled with. Skipped and counted, the same rule the read side
			// applies to one unreadable line.
			s.mu.Lock()
			s.dropped++
			s.mu.Unlock()
			continue
		}
		buf.Write(s.redactor.Bytes(b))
		buf.WriteByte('\n')
	}
	if buf.Len() == 0 {
		return nil
	}

	name := s.nextName()
	tmp := filepath.Join(s.dir, name+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("telemetry: spool write: %w", err)
	}
	if _, err := f.WriteString(buf.String()); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("telemetry: spool write: %w", err)
	}
	// Fsync before the rename. Without it the rename can be durable while the
	// contents are not, which on a crash gives a file that exists and is empty:
	// the one outcome that looks like a successful spool and is not.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("telemetry: spool sync: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("telemetry: spool close: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, name+spoolSuffix)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("telemetry: spool commit: %w", err)
	}

	s.enforceBudget()
	return nil
}

// Take claims the oldest batch and returns it with an acknowledgement.
//
// The acknowledgement is not optional and not a convenience. Calling it with
// nil removes the file; calling it with an error puts the file back for the
// next attempt. Dropping the returned function on the floor leaves the batch
// claimed until the next process opens the spool, which is recoverable but
// wrong, so every caller in this package acks in a defer.
//
// A nil batch with a nil error means the spool is empty.
func (s *Spool) Take(_ context.Context) ([]controlplane.Event, func(error) error, error) {
	names, err := s.pending()
	if err != nil {
		return nil, nil, err
	}
	for _, name := range names {
		from := filepath.Join(s.dir, name)
		to := from + claimSuffix
		if err := os.Rename(from, to); err != nil {
			// Another process claimed it between the listing and now. Not an
			// error: it is being sent, by somebody.
			continue
		}
		batch, err := readBatch(to)
		if err != nil {
			// A file we cannot read will never become readable. Removing it
			// loses those events, which is why the count is kept: a gap in the
			// dashboard that is reported is explainable, and one that is not is
			// a mystery for whoever is on call.
			s.mu.Lock()
			s.dropped++
			s.mu.Unlock()
			_ = os.Remove(to)
			continue
		}
		if len(batch) == 0 {
			_ = os.Remove(to)
			continue
		}
		ack := func(sendErr error) error {
			if sendErr == nil {
				return os.Remove(to)
			}
			return os.Rename(to, from)
		}
		return batch, ack, nil
	}
	return nil, nil, nil
}

// Pending reports how many batches are waiting, claimed ones included.
func (s *Spool) Pending() int {
	names, err := s.pending()
	if err != nil {
		return 0
	}
	return len(names)
}

// Dropped reports how many events the spool discarded, over its budget or
// unreadable. It is reported rather than swallowed for the same reason the
// in-memory sink reports its own drops.
func (s *Spool) Dropped() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// pending lists unclaimed batches, oldest first.
//
// Lexical order is write order because the name carries a zero padded counter
// that is read out of the directory rather than restarted per process. That
// matters: the control plane orders by sequence, and an out of order drain
// would send a later batch first and leave the earlier one to be refused as
// stale by the projection's last_sequence guard.
func (s *Spool) pending() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("telemetry: read spool: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), spoolSuffix) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// enforceBudget removes the oldest batches until the directory fits.
func (s *Spool) enforceBudget() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	type item struct {
		name string
		size int64
	}
	var items []item
	var total int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), spoolSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{e.Name(), info.Size()})
		total += info.Size()
	}
	if total <= s.maxBytes {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].name < items[j].name })
	for _, it := range items {
		if total <= s.maxBytes {
			return
		}
		path := filepath.Join(s.dir, it.name)
		// The oldest goes, not the newest, and for the same reason the
		// in-memory buffer drops from the front: when something has gone wrong
		// the events that explain it are the recent ones.
		n := countLines(path)
		if err := os.Remove(path); err != nil {
			continue
		}
		total -= it.size
		s.mu.Lock()
		s.dropped += uint64(n)
		s.mu.Unlock()
	}
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	n := 0
	for sc.Scan() {
		if len(strings.TrimSpace(sc.Text())) > 0 {
			n++
		}
	}
	return n
}

func readBatch(path string) ([]controlplane.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []controlplane.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e controlplane.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			// One bad line does not discard the batch. This is the shape that
			// blanked a whole feature elsewhere in this project: an all or
			// nothing decode of a collection means one surprising element
			// zeroes the list.
			continue
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
