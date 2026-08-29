package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/state"
)

func openState(t *testing.T) *state.DB {
	t.Helper()
	db, err := state.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// The bug this whole type exists for. One environment is the work of several
// commands, the bus counts in memory and restarts at zero, and the control
// plane refuses an event whose sequence is not ahead of the row it addresses.
// Without a durable counter, `af test` numbers its events 1, 2, 3 against a row
// already at 7 and the dashboard never moves again.
func TestASecondCommandContinuesRatherThanRestarting(t *testing.T) {
	db := openState(t)
	r := NewSequenceReserver(db)
	ctx := context.Background()

	base, err := r.Reserve(ctx, "env-1")
	require.NoError(t, err)
	require.Equal(t, uint64(0), base, "the first command starts at the beginning")
	require.NoError(t, r.Settle(ctx, "env-1", 7))

	next, err := r.Reserve(ctx, "env-1")
	require.NoError(t, err)
	require.Equal(t, uint64(7), next,
		"the second command continues from where the first actually reached")
	require.NoError(t, r.Settle(ctx, "env-1", 12))

	third, err := r.Reserve(ctx, "env-1")
	require.NoError(t, err)
	require.Equal(t, uint64(12), third)
}

// A killed command never settles. The next one must not reuse the numbers the
// dead one issued, so it starts from the reserved ceiling rather than from
// whatever was last written.
func TestAKilledCommandCannotCauseAReuse(t *testing.T) {
	db := openState(t)
	r := NewSequenceReserver(db)
	ctx := context.Background()

	base, err := r.Reserve(ctx, "env-1")
	require.NoError(t, err)
	require.Equal(t, uint64(0), base)
	// This command issues 1..40000 and is killed. Nothing is settled.

	next, err := r.Reserve(ctx, "env-1")
	require.NoError(t, err)
	require.GreaterOrEqual(t, next, uint64(BlockSize),
		"the next command starts past every number the dead one could have issued")
}

func TestEnvironmentsDoNotShareACounter(t *testing.T) {
	db := openState(t)
	r := NewSequenceReserver(db)
	ctx := context.Background()

	require.NoError(t, r.Settle(ctx, "env-a", 100))

	b, err := r.Reserve(ctx, "env-b")
	require.NoError(t, err)
	require.Equal(t, uint64(0), b, "a different environment is a different counter")

	a, err := r.Reserve(ctx, "env-a")
	require.NoError(t, err)
	require.Equal(t, uint64(100), a)
}

// Reserve is a read and a write, and two of them racing would each read the same
// value and hand out the same numbers, which is the bug arrived at from the
// other direction. The file lock in the orchestrator makes this impossible for
// one environment in practice; the transaction makes it impossible in principle.
func TestConcurrentReservationsNeverOverlap(t *testing.T) {
	db := openState(t)
	r := NewSequenceReserver(db)
	ctx := context.Background()

	const n = 16
	bases := make([]uint64, n)
	errs := make([]error, n)
	done := make(chan int, n)
	for i := range n {
		go func() {
			bases[i], errs[i] = r.Reserve(ctx, "env-1")
			done <- i
		}()
	}
	for range n {
		<-done
	}

	seen := map[uint64]bool{}
	for i := range n {
		require.NoError(t, errs[i])
		require.Falsef(t, seen[bases[i]], "two reservations both started at %d", bases[i])
		seen[bases[i]] = true
	}
}

// A counter that cannot be read must not be read as zero. Zero would reuse
// every number already issued, which is the one outcome worse than failing.
func TestAnUnreadableCounterIsRefusedRatherThanTreatedAsZero(t *testing.T) {
	db := openState(t)
	ctx := context.Background()
	require.NoError(t, db.SetMeta(ctx, seqKey("env-1"), "not-a-number"))

	_, err := NewSequenceReserver(db).Reserve(ctx, "env-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a number")
}

func TestAReserverWithNoDatabaseIsInert(t *testing.T) {
	var r *SequenceReserver
	base, err := r.Reserve(context.Background(), "env-1")
	require.NoError(t, err)
	require.Zero(t, base)
	require.NoError(t, r.Settle(context.Background(), "env-1", 5))
}
