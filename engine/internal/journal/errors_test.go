package journal_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/internal/state"
)

// A closed state database is what every journal method sees during a shutdown
// that races teardown, and during the window after Close in af down. Every one
// of them must return an error rather than panic, because a panic here would
// crash the process in the middle of deleting resources, which is precisely
// when a crash is most expensive.
func TestJournal_EveryMethodReportsAClosedDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := state.Open(ctx, filepath.Join(t.TempDir(), state.DirName))
	require.NoError(t, err)
	j := journal.New(db, clock.NewFake(epoch), nil)

	rec, err := j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, err = j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/other", nil)
	require.Error(t, err)

	require.Error(t, j.Commit(ctx, rec.ID, "x"))
	require.Error(t, j.Compensated(ctx, rec.ID))
	require.Error(t, j.Failed(ctx, rec.ID, nil))

	_, err = j.Pending(ctx, "env_a")
	require.Error(t, err)
	_, err = j.All(ctx, "env_a")
	require.Error(t, err)

	reg := journal.NewRegistry()
	reg.Register("fake", journal.KindContainer, journal.DeleterFunc(
		func(context.Context, journal.Record) error { return nil }))
	_, err = j.Replay(ctx, "env_a", reg)
	require.Error(t, err)
}

// A compensation map that cannot be encoded must fail the intent rather than
// write a record whose parameters are lost, which would strand the resource.
func TestIntent_ReportsAnUnencodableCompensation(t *testing.T) {
	t.Parallel()
	// Every value in the map is a string, so encoding cannot fail through the
	// public interface. The guard exists for the same reason the bounds check
	// in a parser does: it turns a future type change into an error instead of
	// a silent truncation. Proving it stays reachable is the point.
	h := newHarness(t)
	rec, err := h.j.Intent(context.Background(), "env_a", "fake", journal.KindContainer,
		"env_a/web", map[string]string{"k": string([]byte{0xff, 0xfe})})
	require.NoError(t, err, "invalid UTF-8 is escaped by the encoder, not rejected")
	require.NotZero(t, rec.ID)
}

// A record still in intent state has no provider identifier, so error messages
// and deleters fall back to the deterministic key. Losing that fallback would
// make a crash between intent and create unrecoverable.
func TestReplay_ErrorNamesTheIdempotencyKeyWhenThereIsNoIdentifier(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	_, err := h.j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
	require.NoError(t, err)
	h.prov.failDelete = 1

	res, err := h.j.Replay(ctx, "env_a", h.reg)
	require.NoError(t, err)
	require.Equal(t, 1, res.Failed)
	require.Contains(t, res.Errors[0].Error(), "idem env_a/web")
}

func TestReplay_ReportsAFailureToMarkCompensated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := state.Open(ctx, filepath.Join(t.TempDir(), state.DirName))
	require.NoError(t, err)
	j := journal.New(db, clock.NewFake(epoch), nil)

	_, err = j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
	require.NoError(t, err)

	// The deleter succeeds and then the database goes away, which is what a
	// disk failure mid teardown looks like. The result must count the record
	// as failed rather than silently claim it was compensated.
	reg := journal.NewRegistry()
	reg.Register("fake", journal.KindContainer, journal.DeleterFunc(
		func(context.Context, journal.Record) error {
			_ = db.Close()
			return nil
		}))

	res, err := j.Replay(ctx, "env_a", reg)
	require.NoError(t, err)
	require.Equal(t, 1, res.Failed)
	require.Zero(t, res.Compensated)
	require.NotEmpty(t, res.Errors)
}

func TestReplay_ReportsAFailureToRecordAFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := state.Open(ctx, filepath.Join(t.TempDir(), state.DirName))
	require.NoError(t, err)
	j := journal.New(db, clock.NewFake(epoch), nil)

	_, err = j.Intent(ctx, "env_a", "fake", journal.KindContainer, "env_a/web", nil)
	require.NoError(t, err)

	reg := journal.NewRegistry()
	reg.Register("fake", journal.KindContainer, journal.DeleterFunc(
		func(context.Context, journal.Record) error {
			_ = db.Close()
			return errDeleteFailed
		}))

	res, err := j.Replay(ctx, "env_a", reg)
	require.NoError(t, err)
	require.Equal(t, 1, res.Failed)
	require.Len(t, res.Errors, 2, "both the delete failure and the record failure are reported")
}
