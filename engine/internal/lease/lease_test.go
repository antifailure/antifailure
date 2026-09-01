package lease_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/lease"
	"github.com/antifailure/antifailure/engine/internal/state"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// Every time in this file is derived from epoch. Nothing here reads a real
// clock, because the whole subject is what happens at a boundary and a test
// that has to wait a week to reach one is a test nobody runs.
var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const (
	day  = 24 * time.Hour
	week = 7 * day
)

// store opens a state database and returns both it and a store over it. The
// database comes back as well because two tests need it: one writes a row the
// API cannot write, and one opens a second store over the same database at a
// later time.
func store(t *testing.T, now time.Time) (*lease.Store, *state.DB) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), state.DirName)
	db, err := state.Open(context.Background(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return lease.NewStore(db, clock.NewFake(now)), db
}

func TestExtend_GrantsWhatWasAskedForInsideTheCeiling(t *testing.T) {
	t.Parallel()
	s, _ := store(t, epoch.Add(20*time.Hour))

	got, err := s.Extend(context.Background(), "af-a",
		epoch.Add(2*day), epoch, week, "still bisecting")
	require.NoError(t, err)
	require.Equal(t, epoch.Add(2*day), got.ExpiresAt)
	require.Equal(t, epoch.Add(week), got.CeilingAt)
	require.Equal(t, "still bisecting", got.Reason)
	require.False(t, got.AtCeiling())
}

func TestExtend_ClampsToTheCeilingAndSaysSo(t *testing.T) {
	t.Parallel()
	s, _ := store(t, epoch)

	// A year, asked for on an environment whose maximum lifetime is a week.
	got, err := s.Extend(context.Background(), "af-a",
		epoch.Add(365*day), epoch, week, "")
	// The lease is still granted, up to the ceiling, and the caller is told
	// why it is less than they asked for. Granting it silently is how somebody
	// comes back to find the environment gone at a time they thought they had
	// moved.
	require.ErrorIs(t, err, lease.ErrPastCeiling)
	require.Equal(t, epoch.Add(week), got.ExpiresAt)
	require.True(t, got.AtCeiling())
}

// The property that makes this a lifetime rather than a chore nobody does.
func TestExtend_RepeatedExtensionsCannotWalkTheCeilingForward(t *testing.T) {
	t.Parallel()
	_, db := store(t, epoch)
	ctx := context.Background()

	// Twenty extensions, each asking for another week, each taken later than
	// the last. If the ceiling were measured from "now" instead of from the
	// environment's creation, this loop would buy twenty weeks.
	var last time.Time
	for i := 1; i <= 20; i++ {
		at := epoch.Add(time.Duration(i) * 6 * time.Hour)
		later := lease.NewStore(db, clock.NewFake(at))
		got, err := later.Extend(ctx, "af-a", at.Add(week), epoch, week, "")
		require.ErrorIs(t, err, lease.ErrPastCeiling)
		require.Equal(t, epoch.Add(week), got.CeilingAt,
			"the ceiling moved on extension %d", i)
		last = got.ExpiresAt
	}
	require.Equal(t, epoch.Add(week), last,
		"twenty extensions bought more than the maximum lifetime")
}

func TestExtend_ALaterManifestCannotRaiseACeilingAlreadySet(t *testing.T) {
	t.Parallel()
	s, _ := store(t, epoch)
	ctx := context.Background()

	_, err := s.Extend(ctx, "af-a", epoch.Add(2*day), epoch, week, "")
	require.NoError(t, err)

	// The same environment extended again from a checkout whose manifest says
	// a year. The ceiling was fixed at the first extension and does not move.
	got, err := s.Extend(ctx, "af-a", epoch.Add(300*day), epoch, 365*day, "")
	require.ErrorIs(t, err, lease.ErrPastCeiling)
	require.Equal(t, epoch.Add(week), got.CeilingAt)
	require.Equal(t, epoch.Add(week), got.ExpiresAt)
}

func TestExpiries_ReadsAHandEditedRowBackAsTheCeiling(t *testing.T) {
	t.Parallel()
	s, db := store(t, epoch)
	ctx := context.Background()

	_, err := s.Extend(ctx, "af-a", epoch.Add(2*day), epoch, week, "")
	require.NoError(t, err)

	// Somebody sets expires_at past the ceiling directly in the database. The
	// bound is applied on the read the destruction decision is made from, so
	// it holds anyway.
	require.NoError(t, db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE env_leases SET expires_at = ? WHERE env = 'af-a'`,
			epoch.Add(365*day).Unix())
		return err
	}))

	got, err := s.Expiries(ctx)
	require.NoError(t, err)
	require.Equal(t, epoch.Add(week), got["af-a"])
}

func TestDrop_LeavesNoExtensionForTheNextEnvironmentOfTheSameName(t *testing.T) {
	t.Parallel()
	s, _ := store(t, epoch)
	ctx := context.Background()

	_, err := s.Extend(ctx, "af-a", epoch.Add(2*day), epoch, week, "")
	require.NoError(t, err)
	require.NoError(t, s.Drop(ctx, "af-a"))

	_, found, err := s.Get(ctx, "af-a")
	require.NoError(t, err)
	require.False(t, found)

	// Dropping one that is not there is what teardown does on every
	// environment nobody extended, which is most of them.
	require.NoError(t, s.Drop(ctx, "af-a"))
}

func TestExtend_RefusesAnEmptyEnvironment(t *testing.T) {
	t.Parallel()
	s, _ := store(t, epoch)
	_, err := s.Extend(context.Background(), "", epoch.Add(day), epoch, week, "")
	require.Error(t, err)
}
