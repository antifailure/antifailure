package telemetry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/controlplane"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

func newTestSpool(t *testing.T, opts ...func(*SpoolOptions)) (*Spool, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "spool")
	o := SpoolOptions{Dir: dir, Redactor: redact.New()}
	for _, f := range opts {
		f(&o)
	}
	s, err := NewSpool(o)
	require.NoError(t, err)
	return s, dir
}

func evt(id string, seq uint64, at time.Time) controlplane.Event {
	return controlplane.Event{
		ID:         id,
		Type:       "environment.ready",
		EnvID:      "env-1",
		Sequence:   seq,
		OccurredAt: at,
		Payload:    map[string]any{"message": "ready"},
	}
}

func TestSpoolRoundTripsABatch(t *testing.T) {
	s, _ := newTestSpool(t)
	ctx := context.Background()
	base := time.Unix(1700000000, 0).UTC()

	require.NoError(t, s.Put(ctx, []controlplane.Event{evt("a", 1, base), evt("b", 2, base)}))
	require.Equal(t, 1, s.Pending())

	batch, ack, err := s.Take(ctx)
	require.NoError(t, err)
	require.Len(t, batch, 2)
	require.Equal(t, "a", batch[0].ID)
	require.Equal(t, uint64(2), batch[1].Sequence)
	require.Equal(t, "ready", batch[0].Payload["message"])

	require.NoError(t, ack(nil))
	require.Equal(t, 0, s.Pending())
}

// A spool that only works while the process that filled it is alive would be
// the in-memory buffer with extra steps. This is the property AF-CPL-003
// actually promises: a second process finds what the first one could not send.
func TestASecondProcessDrainsWhatTheFirstCouldNotSend(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	ctx := context.Background()
	base := time.Unix(1700000000, 0).UTC()

	first, err := NewSpool(SpoolOptions{Dir: dir, Redactor: redact.New()})
	require.NoError(t, err)
	require.NoError(t, first.Put(ctx, []controlplane.Event{evt("a", 1, base)}))
	// The first process ends here. Nothing is closed, nothing is handed over.

	second, err := NewSpool(SpoolOptions{Dir: dir, Redactor: redact.New()})
	require.NoError(t, err)
	batch, ack, err := second.Take(ctx)
	require.NoError(t, err)
	require.Len(t, batch, 1)
	require.Equal(t, "a", batch[0].ID)
	require.NoError(t, ack(nil))
}

func TestAFailedSendPutsTheBatchBack(t *testing.T) {
	s, _ := newTestSpool(t)
	ctx := context.Background()
	base := time.Unix(1700000000, 0).UTC()

	require.NoError(t, s.Put(ctx, []controlplane.Event{evt("a", 1, base)}))

	batch, ack, err := s.Take(ctx)
	require.NoError(t, err)
	require.Len(t, batch, 1)
	require.NoError(t, ack(errors.New("control plane is still unreachable")))

	require.Equal(t, 1, s.Pending(), "a batch that failed to send is still owed")

	again, ack2, err := s.Take(ctx)
	require.NoError(t, err)
	require.Len(t, again, 1)
	require.Equal(t, "a", again[0].ID)
	require.NoError(t, ack2(nil))
	require.Equal(t, 0, s.Pending())
}

// Ordering: two engine processes draining the same directory at the same
// moment. Exactly one may send each batch, or the control plane sees every
// event twice and the only thing standing between that and a corrupt dashboard
// is idempotency on the far side.
func TestConcurrentDrainsClaimEachBatchExactlyOnce(t *testing.T) {
	s, _ := newTestSpool(t)
	ctx := context.Background()
	base := time.Unix(1700000000, 0).UTC()

	const batches = 24
	for i := range batches {
		require.NoError(t, s.Put(ctx, []controlplane.Event{evt(string(rune('a'+i%26)), uint64(i), base.Add(time.Duration(i)*time.Millisecond))}))
	}

	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				batch, ack, err := s.Take(ctx)
				if err != nil || batch == nil {
					return
				}
				mu.Lock()
				for _, e := range batch {
					seen[e.ID+"/"+string(rune(e.Sequence))]++
				}
				mu.Unlock()
				_ = ack(nil)
			}
		}()
	}
	wg.Wait()

	require.Len(t, seen, batches, "every batch was taken")
	for k, n := range seen {
		require.Equalf(t, 1, n, "batch %s was taken %d times", k, n)
	}
	require.Equal(t, 0, s.Pending())
}

// A claim held by a process that died is a batch nobody will ever send again
// unless somebody puts it back.
func TestAClaimLeftByADeadProcessIsRecovered(t *testing.T) {
	s, dir := newTestSpool(t)
	ctx := context.Background()
	base := time.Unix(1700000000, 0).UTC()

	require.NoError(t, s.Put(ctx, []controlplane.Event{evt("a", 1, base)}))
	batch, _, err := s.Take(ctx)
	require.NoError(t, err)
	require.Len(t, batch, 1)
	// The acknowledgement is never called: this process is gone.
	require.Equal(t, 0, s.Pending(), "while claimed it is not pending")

	recovered, err := NewSpool(SpoolOptions{Dir: dir, Redactor: redact.New()})
	require.NoError(t, err)
	require.Equal(t, 1, recovered.Pending(), "the next process finds it")

	again, ack, err := recovered.Take(ctx)
	require.NoError(t, err)
	require.Len(t, again, 1)
	require.NoError(t, ack(nil))
}

// Oldest first, because the control plane's projection refuses an event whose
// sequence is behind the row's last_sequence. Draining newest first would make
// every earlier batch a no-op on arrival.
func TestBatchesDrainOldestFirst(t *testing.T) {
	s, _ := newTestSpool(t)
	ctx := context.Background()
	base := time.Unix(1700000000, 0).UTC()

	for i := 1; i <= 5; i++ {
		require.NoError(t, s.Put(ctx, []controlplane.Event{
			evt("e", uint64(i), base.Add(time.Duration(i)*time.Second)),
		}))
	}

	var order []uint64
	for {
		batch, ack, err := s.Take(ctx)
		require.NoError(t, err)
		if batch == nil {
			break
		}
		order = append(order, batch[0].Sequence)
		require.NoError(t, ack(nil))
	}
	require.Equal(t, []uint64{1, 2, 3, 4, 5}, order)
}

func TestTheSpoolIsBoundedAndSaysWhatItDropped(t *testing.T) {
	s, _ := newTestSpool(t, func(o *SpoolOptions) { o.MaxBytes = 2048 })
	ctx := context.Background()
	base := time.Unix(1700000000, 0).UTC()

	for i := range 200 {
		require.NoError(t, s.Put(ctx, []controlplane.Event{
			evt("e", uint64(i), base.Add(time.Duration(i)*time.Millisecond)),
		}))
	}

	require.NotZero(t, s.Dropped(), "a full spool drops and reports it")
	require.Less(t, s.Pending(), 200)

	// The newest survived: the events that explain a failure are the recent
	// ones, so a buffer that keeps the first N and discards the rest is a
	// buffer full of the least useful events it could hold.
	var highest uint64
	for {
		batch, ack, err := s.Take(ctx)
		require.NoError(t, err)
		if batch == nil {
			break
		}
		for _, e := range batch {
			if e.Sequence > highest {
				highest = e.Sequence
			}
		}
		require.NoError(t, ack(nil))
	}
	require.Equal(t, uint64(199), highest)
}

// The spool is a file on disk that `af support bundle` may collect, so the same
// rule applies to it as to every other writer in the engine.
func TestASecretInAnEventNeverReachesTheSpoolFile(t *testing.T) {
	r := redact.New()
	const password = "s3cr3t-pgpassword-never-on-disk"
	r.Register(password)

	dir := filepath.Join(t.TempDir(), "spool")
	s, err := NewSpool(SpoolOptions{Dir: dir, Redactor: r})
	require.NoError(t, err)

	base := time.Unix(1700000000, 0).UTC()
	e := evt("a", 1, base)
	e.Payload = map[string]any{
		"dsn":     "postgres://app:" + password + "@db.internal:5432/app",
		"message": "branched " + password,
	}
	require.NoError(t, s.Put(context.Background(), []controlplane.Event{e}))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	for _, entry := range entries {
		b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)
		require.NotContains(t, string(b), password,
			"a registered secret reached %s", entry.Name())
	}
}

// One unreadable line must not discard the batch it travelled with. This is the
// all-or-nothing decode that blanked a whole feature elsewhere in this project.
func TestOneCorruptLineDoesNotDiscardTheBatch(t *testing.T) {
	s, dir := newTestSpool(t)
	ctx := context.Background()
	base := time.Unix(1700000000, 0).UTC()

	require.NoError(t, s.Put(ctx, []controlplane.Event{evt("a", 1, base), evt("b", 2, base)}))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	path := filepath.Join(dir, entries[0].Name())
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("{not json\n"+string(raw)), 0o600))

	batch, ack, err := s.Take(ctx)
	require.NoError(t, err)
	require.Len(t, batch, 2, "the two good lines survived the bad one")
	require.NoError(t, ack(nil))
}

func TestAnEmptySpoolIsNotAnError(t *testing.T) {
	s, _ := newTestSpool(t)
	batch, ack, err := s.Take(context.Background())
	require.NoError(t, err)
	require.Nil(t, batch)
	require.Nil(t, ack)
}

func TestASpoolWithoutARedactorIsRefused(t *testing.T) {
	_, err := NewSpool(SpoolOptions{Dir: t.TempDir()})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "redactor"))
}
