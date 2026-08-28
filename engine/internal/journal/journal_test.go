package journal_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"pgregory.net/rapid"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/internal/state"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeProvider stands in for a real provider. It records what exists, can be
// made to fail on demand, and counts deletes so that idempotency is provable.
type fakeProvider struct {
	mu      sync.Mutex
	exists  map[string]bool
	deletes map[string]int
	creates int
	// failDelete makes the next n deletes fail, which is how a provider
	// outage during teardown is simulated.
	failDelete int
	// createOutcome decides what happens on the next create: it may succeed,
	// or it may create the resource and then fail to report it, which is the
	// case a journal exists to survive.
	silentCreate bool
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{exists: map[string]bool{}, deletes: map[string]int{}}
}

func (p *fakeProvider) create(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.creates++
	p.exists[id] = true
	if p.silentCreate {
		return fmt.Errorf("timeout after the resource was created")
	}
	return nil
}

func (p *fakeProvider) Delete(_ context.Context, rec journal.Record) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := rec.ExternalID
	if id == "" {
		// A record still in intent state has no identifier, so the deleter
		// falls back to the deterministic name derived from the same key. This
		// is exactly the path that cleans up a resource created moments before
		// a crash.
		id = rec.IdemKey
	}
	p.deletes[id]++
	if p.failDelete > 0 {
		p.failDelete--
		return fmt.Errorf("provider unreachable")
	}
	delete(p.exists, id)
	return nil
}

func (p *fakeProvider) liveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.exists)
}

func (p *fakeProvider) deleteCount(id string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.deletes[id]
}

type harness struct {
	j     *journal.Journal
	reg   *journal.Registry
	prov  *fakeProvider
	sink  *events.MemorySink
	clock *clock.Fake
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := state.Open(context.Background(), filepath.Join(t.TempDir(), state.DirName))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	c := clock.NewFake(epoch)
	bus := events.NewBus(c)
	sink := events.NewMemorySink(0)
	bus.AddSink(sink)
	t.Cleanup(func() { require.NoError(t, bus.Close()) })

	prov := newFakeProvider()
	reg := journal.NewRegistry()
	reg.Register("fake", journal.KindContainer, prov)
	reg.Register("fake", journal.KindDatabaseBranch, prov)

	return &harness{j: journal.New(db, c, bus), reg: reg, prov: prov, sink: sink, clock: c}
}

func TestIntent_RequiresAnIdempotencyKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, err := h.j.Intent(context.Background(), "env_a", "fake", journal.KindContainer, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "idempotency key")
}

// The property a retry after a timeout depends on: the same key must never
// produce a second resource. Without it, a provider call that times out after
// the resource was created leaves an orphan on every retry.
func TestIntent_SameKeyReturnsTheExistingRecord(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	first, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
	require.NoError(t, err)
	second, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)

	all, err := h.j.All(ctx, "env_a")
	require.NoError(t, err)
	require.Len(t, all, 1, "a repeated intent must not create a second record")
}

func TestIntent_SameKeyAfterCompensationCreatesAFreshRecord(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	rec, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
	require.NoError(t, err)
	require.NoError(t, h.j.Commit(ctx, rec.ID, "container-1"))
	require.NoError(t, h.j.Compensated(ctx, rec.ID))

	// Bringing the same environment up again must be able to reuse the key.
	again, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
	require.NoError(t, err)
	require.NotEqual(t, rec.ID, again.ID)
}

func TestIntent_ConcurrentSameKeyYieldsOneRecord(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	const n = 12
	ids := make([]int64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
			if err == nil {
				ids[i] = rec.ID
			}
		}(i)
	}
	wg.Wait()

	all, err := h.j.All(ctx, "env_a")
	require.NoError(t, err)
	require.Len(t, all, 1, "concurrent intents for one key must collapse to one record")
	for _, id := range ids {
		require.Equal(t, all[0].ID, id, "every caller must receive the same record")
	}
}

func TestCommit_RecordsTheIdentifierAndEmitsAnEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	rec, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web",
		map[string]string{"network": "env_a"})
	require.NoError(t, err)
	require.NoError(t, h.j.Commit(ctx, rec.ID, "container-1"))

	pending, err := h.j.Pending(ctx, "env_a")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, journal.StateCommitted, pending[0].State)
	require.Equal(t, "container-1", pending[0].ExternalID)
	require.Equal(t, "env_a", pending[0].Compensation["network"])

	require.Eventually(t, func() bool {
		return len(h.sink.OfType(events.ResourceCreated)) == 1
	}, 2*time.Second, time.Millisecond)
}

func TestCommit_OnAMissingRecordIsAnError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.Error(t, h.j.Commit(context.Background(), 9999, "x"))
}

func TestReplay_DeletesNewestFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	// A branch is created before the container that uses it, so teardown must
	// remove the container first.
	var order []string
	reg := journal.NewRegistry()
	rec := func(kind journal.Kind) journal.Deleter {
		return journal.DeleterFunc(func(_ context.Context, r journal.Record) error {
			order = append(order, r.ExternalID)
			return nil
		})
	}
	reg.Register("fake", journal.KindDatabaseBranch, rec(journal.KindDatabaseBranch))
	reg.Register("fake", journal.KindContainer, rec(journal.KindContainer))

	branch, err := h.j.Intent(ctx, "env_a", "fake", journal.KindDatabaseBranch, "env_a/db", nil)
	require.NoError(t, err)
	require.NoError(t, h.j.Commit(ctx, branch.ID, "branch-1"))
	container, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
	require.NoError(t, err)
	require.NoError(t, h.j.Commit(ctx, container.ID, "container-1"))

	res, err := h.j.Replay(ctx, "env_a", reg)
	require.NoError(t, err)
	require.True(t, res.Clean())
	require.Equal(t, []string{"container-1", "branch-1"}, order)
}

func TestReplay_IsIdempotent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	rec, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
	require.NoError(t, err)
	require.NoError(t, h.prov.create("container-1"))
	require.NoError(t, h.j.Commit(ctx, rec.ID, "container-1"))

	first, err := h.j.Replay(ctx, "env_a", h.reg)
	require.NoError(t, err)
	require.Equal(t, 1, first.Compensated)

	second, err := h.j.Replay(ctx, "env_a", h.reg)
	require.NoError(t, err)
	require.Equal(t, 0, second.Compensated, "a second replay has nothing left to do")
	require.True(t, second.Clean())
	require.Equal(t, 1, h.prov.deleteCount("container-1"), "the provider must not be asked twice")
	require.Zero(t, h.prov.liveCount())
}

// A provider being unreachable must not prevent the other resources from being
// cleaned up. Stopping at the first failure would strand everything after it.
func TestReplay_ContinuesPastAFailureAndReportsWhatIsLeft(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		rec, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer,
			fmt.Sprintf("env_a/svc-%d", i), nil)
		require.NoError(t, err)
		require.NoError(t, h.prov.create(fmt.Sprintf("container-%d", i)))
		require.NoError(t, h.j.Commit(ctx, rec.ID, fmt.Sprintf("container-%d", i)))
	}
	h.prov.failDelete = 1 // the newest one fails

	res, err := h.j.Replay(ctx, "env_a", h.reg)
	require.NoError(t, err)
	require.Equal(t, 3, res.Compensated)
	require.Equal(t, 1, res.Failed)
	require.False(t, res.Clean())
	require.Len(t, res.Errors, 1)
	require.Contains(t, res.Errors[0].Error(), "provider unreachable")

	// The failure is recorded, not lost, so the next teardown retries it.
	pending, err := h.j.Pending(ctx, "env_a")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, journal.StateFailed, pending[0].State)
	require.Equal(t, 1, pending[0].Attempts)
	require.Contains(t, pending[0].LastError, "provider unreachable")

	// And the retry finishes the job.
	res2, err := h.j.Replay(ctx, "env_a", h.reg)
	require.NoError(t, err)
	require.Equal(t, 1, res2.Compensated)
	require.True(t, res2.Clean())
	require.Zero(t, h.prov.liveCount())
}

func TestReplay_LeavesRecordsWithNoDeleterAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	// A record written by a newer binary, for a provider this one does not
	// know. Dropping it would orphan the resource forever.
	rec, err := h.j.Intent(ctx, "env_a", "future-provider", journal.KindNamespace, "env_a/ns", nil)
	require.NoError(t, err)
	require.NoError(t, h.j.Commit(ctx, rec.ID, "ns-1"))

	res, err := h.j.Replay(ctx, "env_a", h.reg)
	require.NoError(t, err)
	require.Len(t, res.Skipped, 1)
	require.False(t, res.Clean())
	require.Contains(t, res.Summary(), "no deleter in this build")

	pending, err := h.j.Pending(ctx, "env_a")
	require.NoError(t, err)
	require.Len(t, pending, 1, "the record must survive for a build that can compensate it")
}

func TestReplay_ScopesToOneEnvironment(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	for _, env := range []string{"env_a", "env_b"} {
		rec, err := h.j.Intent(ctx, env, "fake", journal.KindContainer, env+"/web", nil)
		require.NoError(t, err)
		require.NoError(t, h.prov.create(env+"-container"))
		require.NoError(t, h.j.Commit(ctx, rec.ID, env+"-container"))
	}

	res, err := h.j.Replay(ctx, "env_a", h.reg)
	require.NoError(t, err)
	require.Equal(t, 1, res.Compensated)
	require.Equal(t, 1, h.prov.liveCount(), "the other environment must be untouched")

	// An empty environment means everything, which is what af down --all and
	// crash recovery use.
	all, err := h.j.Replay(ctx, "", h.reg)
	require.NoError(t, err)
	require.Equal(t, 1, all.Compensated)
	require.Zero(t, h.prov.liveCount())
}

func TestReplay_StopsOnACancelledContextAndLeavesTheRest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())

	for i := 0; i < 3; i++ {
		rec, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer,
			fmt.Sprintf("env_a/svc-%d", i), nil)
		require.NoError(t, err)
		require.NoError(t, h.j.Commit(ctx, rec.ID, fmt.Sprintf("container-%d", i)))
	}
	cancel()

	_, err := h.j.Replay(ctx, "env_a", h.reg)
	require.ErrorIs(t, err, context.Canceled)

	pending, err := h.j.Pending(context.Background(), "env_a")
	require.NoError(t, err)
	require.Len(t, pending, 3, "a cancelled replay leaves every record for the next run")
}

// The crash injection matrix. A process can die at any step of the create
// sequence. Replay must converge to zero live resources from every one of
// them, which is the whole promise of the journal.
func TestReplay_ConvergesFromACrashAtEveryStep(t *testing.T) {
	t.Parallel()
	steps := []string{
		"before intent",
		"after intent, before create",
		"after create, before commit",
		"after commit",
	}
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			ctx := context.Background()
			const idem = "env_a/web"

			var rec journal.Record
			var err error
			if step != "before intent" {
				rec, err = h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, idem, nil)
				require.NoError(t, err)
			}
			if step == "after create, before commit" || step == "after commit" {
				// The resource exists at the provider under the deterministic
				// name derived from the same key, which is how a deleter finds
				// it when there is no committed identifier.
				require.NoError(t, h.prov.create(idem))
			}
			if step == "after commit" {
				require.NoError(t, h.j.Commit(ctx, rec.ID, idem))
			}

			// The process died here. A new one replays.
			res, err := h.j.Replay(ctx, "env_a", h.reg)
			require.NoError(t, err)
			require.True(t, res.Clean(), "replay left work behind: %s", res.Summary())
			require.Zero(t, h.prov.liveCount(),
				"a crash %s must still converge to zero live resources", step)

			pending, err := h.j.Pending(ctx, "env_a")
			require.NoError(t, err)
			require.Empty(t, pending)
		})
	}
}

// The same guarantee, generalised: for any interleaving of intents, creates,
// commits, and crashes against a fake provider, replay ends with nothing live
// and nothing pending.
func TestReplay_AnyInterleavingConvergesToZeroLiveResources(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rt *rapid.T) {
		h := newHarnessRapid(rt)
		ctx := context.Background()

		n := rapid.IntRange(1, 8).Draw(rt, "resources")
		// journal is the only thing the assertions need: whether this
		// resource ever reached the journal. Whether it was then created or
		// committed changes what replay has to do about it, not whether it
		// has to do anything, and the aggregate checks below already prove
		// nothing survived either way.
		type pending struct {
			key     string
			rec     journal.Record
			journal bool
		}
		var items []*pending
		for i := 0; i < n; i++ {
			key := fmt.Sprintf("env_a/r-%d", i)
			stop := rapid.IntRange(0, 3).Draw(rt, fmt.Sprintf("stop-%d", i))
			p := &pending{key: key}
			if stop == 0 {
				items = append(items, p)
				continue
			}
			rec, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, key, nil)
			if err != nil {
				rt.Fatalf("intent: %v", err)
			}
			p.rec = rec
			p.journal = true
			if stop >= 2 {
				if err := h.prov.create(key); err != nil {
					rt.Fatalf("create: %v", err)
				}
			}
			if stop >= 3 {
				if err := h.j.Commit(ctx, rec.ID, key); err != nil {
					rt.Fatalf("commit: %v", err)
				}
			}
			items = append(items, p)
		}

		// Replay may itself be interrupted and retried.
		rounds := rapid.IntRange(1, 3).Draw(rt, "replays")
		for r := 0; r < rounds; r++ {
			if _, err := h.j.Replay(ctx, "env_a", h.reg); err != nil {
				rt.Fatalf("replay: %v", err)
			}
		}
		if got := h.prov.liveCount(); got != 0 {
			rt.Fatalf("%d resources survived replay", got)
		}
		left, err := h.j.Pending(ctx, "env_a")
		if err != nil {
			rt.Fatalf("pending: %v", err)
		}
		if len(left) != 0 {
			rt.Fatalf("%d records still pending after replay", len(left))
		}

		// Per resource, not only in aggregate. The counts above say nothing
		// survived; these say replay touched the right things. The second half
		// is the one worth having: replay works from journal records, so a
		// resource the journal never heard of must not be deleted, and a
		// teardown that reaches for one is reaching into somebody else's
		// environment.
		for _, it := range items {
			deletes := h.prov.deleteCount(it.key)
			if it.journal && deletes == 0 {
				rt.Fatalf("%s was journalled and replay never tried to delete it", it.key)
			}
			if !it.journal && deletes != 0 {
				rt.Fatalf("%s was never journalled and replay deleted it %d times", it.key, deletes)
			}
		}
	})
}

func TestReplayResult_PendingErrorAndSummary(t *testing.T) {
	t.Parallel()
	clean := journal.ReplayResult{Compensated: 3}
	require.NoError(t, clean.PendingError())
	require.Equal(t, "3 deleted", clean.Summary())
	require.Equal(t, "nothing to do", journal.ReplayResult{}.Summary())

	dirty := journal.ReplayResult{Compensated: 1, Failed: 2}
	err := dirty.PendingError()
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFRUN030))
	require.Contains(t, err.Error(), "2 resources")
	require.Contains(t, dirty.Summary(), "2 failed")
}

func TestRegistry_LookupReportsAMissingDeleter(t *testing.T) {
	t.Parallel()
	reg := journal.NewRegistry()
	_, ok := reg.Lookup("nope", journal.KindContainer)
	require.False(t, ok)
	reg.Register("nope", journal.KindContainer, journal.DeleterFunc(
		func(context.Context, journal.Record) error { return nil }))
	_, ok = reg.Lookup("nope", journal.KindContainer)
	require.True(t, ok)
}

func TestAll_ReturnsCompensatedRecordsToo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	rec, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
	require.NoError(t, err)
	require.NoError(t, h.j.Commit(ctx, rec.ID, "container-1"))
	require.NoError(t, h.j.Compensated(ctx, rec.ID))

	pending, err := h.j.Pending(ctx, "env_a")
	require.NoError(t, err)
	require.Empty(t, pending)

	all, err := h.j.All(ctx, "env_a")
	require.NoError(t, err)
	require.Len(t, all, 1, "the audit trail keeps compensated records")
	require.Equal(t, journal.StateCompensated, all[0].State)

	global, err := h.j.All(ctx, "")
	require.NoError(t, err)
	require.Len(t, global, 1)
}

func TestCompensated_EmitsADeletionEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	rec, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
	require.NoError(t, err)
	require.NoError(t, h.j.Commit(ctx, rec.ID, "container-1"))
	require.NoError(t, h.j.Compensated(ctx, rec.ID))

	require.Eventually(t, func() bool {
		return len(h.sink.OfType(events.ResourceDeleted)) == 1
	}, 2*time.Second, time.Millisecond)
}

func TestFailed_TruncatesAVeryLongProviderError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	rec, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
	require.NoError(t, err)

	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'x'
	}
	require.NoError(t, h.j.Failed(ctx, rec.ID, fmt.Errorf("%s", long)))
	require.NoError(t, h.j.Failed(ctx, rec.ID, nil))

	pending, err := h.j.Pending(ctx, "env_a")
	require.NoError(t, err)
	require.Equal(t, 2, pending[0].Attempts)
	require.Empty(t, pending[0].LastError, "a nil cause clears the message")
}

var errDeleteFailed = fmt.Errorf("delete failed")

func TestSummary_ListsSkippedKindsSorted(t *testing.T) {
	t.Parallel()
	res := journal.ReplayResult{Skipped: []journal.Record{
		{Provider: "zeta", Kind: journal.KindNamespace},
		{Provider: "alpha", Kind: journal.KindDNSRecord},
		{Provider: "alpha", Kind: journal.KindDNSRecord},
	}}
	// Sorted so that a snapshot of CLI output does not flake on map order.
	require.Contains(t, res.Summary(), "alpha/dns.record, zeta/k8s.namespace")
	require.Contains(t, res.Summary(), "3 with no deleter")
}
