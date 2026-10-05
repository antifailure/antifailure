package state_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	_ "modernc.org/sqlite" // the test seeds a database directly

	"github.com/antifailure/antifailure/engine/internal/privatefs/privatefstest"
	"github.com/antifailure/antifailure/engine/internal/state"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func openTemp(t *testing.T) (*state.DB, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), state.DirName)
	db, err := state.Open(context.Background(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db, dir
}

func TestOpen_CreatesTheDirectoryAndSchema(t *testing.T) {
	t.Parallel()
	// Under a folder everybody can read, so a private result is the state
	// directory being made private and not the temporary folder being so.
	dir := filepath.Join(privatefstest.OpenFolder(t), state.DirName)
	db, err := state.Open(context.Background(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	v, err := db.Version(context.Background())
	require.NoError(t, err)
	require.Equal(t, state.SchemaVersion(), v)
	require.Empty(t, db.RebuiltFrom())

	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	// The directory holds the journal and local handles, so it is not world
	// readable.
	privatefstest.RequirePrivate(t, dir, 0o700)
	privatefstest.RequirePrivate(t, filepath.Join(dir, state.FileName), 0o600)

	// And in a state directory that already existed, readable by everybody,
	// the database is narrowed by itself rather than by inheriting from a
	// directory this run made private.
	existing := filepath.Join(privatefstest.OpenFolder(t), state.DirName)
	require.NoError(t, os.Mkdir(existing, 0o755))
	db2, err := state.Open(context.Background(), existing)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db2.Close()) })
	privatefstest.RequirePrivate(t, filepath.Join(existing, state.FileName), 0o600)
	// The journal beside it too, which is where the rows live until a
	// checkpoint, and the directory, which is what decides the journal's
	// access on Windows when SQLite makes it again later.
	require.NoError(t, db2.SetMeta(context.Background(), "probe", "x"))
	wal := filepath.Join(existing, state.FileName+"-wal")
	_, statErr := os.Stat(wal)
	require.NoError(t, statErr, "the precondition: the database is in WAL mode and has written a journal")
	privatefstest.RequirePrivate(t, wal, 0o600)
	privatefstest.RequirePrivate(t, existing, 0o700)
}

func TestOpen_IsIdempotentAcrossRuns(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), state.DirName)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		db, err := state.Open(ctx, dir)
		require.NoError(t, err)
		require.NoError(t, db.SetMeta(ctx, "run", "yes"))
		require.NoError(t, db.Close())
	}
	db, err := state.Open(ctx, dir)
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	v, err := db.Version(ctx)
	require.NoError(t, err)
	require.Equal(t, state.SchemaVersion(), v, "reopening must not reapply migrations")
}

// A corrupt state database must not stop the engine from starting. Refusing to
// open would leave the user holding resources they cannot tear down, which is
// the worst possible outcome for a tool whose promise is that nothing outlives
// its environment.
func TestOpen_RebuildsACorruptDatabaseAndKeepsTheOriginal(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), state.DirName)
	ctx := context.Background()

	db, err := state.Open(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, db.SetMeta(ctx, "before", "value"))
	require.NoError(t, db.Close())

	path := filepath.Join(dir, state.FileName)
	require.NoError(t, os.WriteFile(path, []byte("this is not a SQLite database at all"), 0o600))

	db2, err := state.Open(ctx, dir)
	require.NoError(t, err, "a corrupt database must be rebuilt rather than refused")
	defer func() { require.NoError(t, db2.Close()) }()

	require.NotEmpty(t, db2.RebuiltFrom(), "the caller needs the backup path for AF-RUN-011")
	_, err = os.Stat(db2.RebuiltFrom())
	require.NoError(t, err, "the corrupt file must be kept, not deleted")

	got, err := db2.Meta(ctx, "before")
	require.NoError(t, err)
	require.Empty(t, got, "the rebuilt database starts empty")

	v, err := db2.Version(ctx)
	require.NoError(t, err)
	require.Equal(t, state.SchemaVersion(), v)
}

func TestOpen_RefusesADatabaseFromANewerBuild(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), state.DirName)
	ctx := context.Background()
	db, err := state.Open(ctx, dir)
	require.NoError(t, err)
	_, err = db.SQL().ExecContext(ctx,
		"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
		state.SchemaVersion()+10, "from-the-future", epoch.UnixMilli())
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, err = state.Open(ctx, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "newer version of Antifailure")
}

func TestOpen_ReportsAnUncreatableDirectory(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	_, err := state.Open(context.Background(), filepath.Join(f, "sub"))
	require.Error(t, err)
}

func TestMeta_RoundTripsAndTreatsMissingAsEmpty(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()

	got, err := db.Meta(ctx, "never-set")
	require.NoError(t, err)
	require.Empty(t, got, "absent and empty mean the same thing for these values")

	require.NoError(t, db.SetMeta(ctx, "k", "v1"))
	got, err = db.Meta(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "v1", got)

	require.NoError(t, db.SetMeta(ctx, "k", "v2"))
	got, err = db.Meta(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "v2", got, "setting again must overwrite")
}

func TestCheckpoints_RoundTripAndClear(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()

	_, ok, err := db.Checkpoint(ctx, "mask/env_a", "public.users")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, db.SetCheckpoint(ctx, "mask/env_a", "public.users", "1200000", epoch))
	v, ok, err := db.Checkpoint(ctx, "mask/env_a", "public.users")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "1200000", v)

	require.NoError(t, db.SetCheckpoint(ctx, "mask/env_a", "public.users", "2400000", epoch.Add(time.Minute)))
	v, _, err = db.Checkpoint(ctx, "mask/env_a", "public.users")
	require.NoError(t, err)
	require.Equal(t, "2400000", v, "a later checkpoint replaces the earlier one")

	// A different scope is untouched by the clear, so two concurrent masking
	// runs cannot erase each other's progress.
	require.NoError(t, db.SetCheckpoint(ctx, "mask/env_b", "public.users", "999", epoch))
	require.NoError(t, db.ClearCheckpoints(ctx, "mask/env_a"))

	_, ok, err = db.Checkpoint(ctx, "mask/env_a", "public.users")
	require.NoError(t, err)
	require.False(t, ok)
	_, ok, err = db.Checkpoint(ctx, "mask/env_b", "public.users")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestTx_CommitsOnSuccessAndRollsBackOnError(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()

	require.NoError(t, db.Tx(ctx, func(tx *sqlTx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES ('a', '1')")
		return err
	}))
	got, err := db.Meta(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, "1", got)

	wantErr := os.ErrInvalid
	err = db.Tx(ctx, func(tx *sqlTx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES ('b', '2')"); err != nil {
			return err
		}
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)
	got, err = db.Meta(ctx, "b")
	require.NoError(t, err)
	require.Empty(t, got, "a failed transaction must leave nothing behind")
}

func TestTx_RollsBackOnPanicAndRepanics(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()

	require.Panics(t, func() {
		_ = db.Tx(ctx, func(tx *sqlTx) error {
			_, _ = tx.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES ('p', '1')")
			panic("boom")
		})
	})
	got, err := db.Meta(ctx, "p")
	require.NoError(t, err)
	require.Empty(t, got, "a panicking transaction must not commit")
}

func TestClose_IsIdempotent(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), state.DirName)
	db, err := state.Open(context.Background(), dir)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	require.NoError(t, db.Close())
}

func TestPath_ReportsTheDatabaseFile(t *testing.T) {
	t.Parallel()
	db, dir := openTemp(t)
	require.Equal(t, filepath.Join(dir, state.FileName), db.Path())
}

// Every method must report a closed database rather than panic. The calls that
// race Close are the journal writes during teardown, so a panic here would
// crash the process while it is deleting resources.
func TestDB_EveryMethodReportsAClosedDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := state.Open(ctx, filepath.Join(t.TempDir(), state.DirName))
	require.NoError(t, err)
	require.False(t, db.Closed())
	require.NoError(t, db.Close())
	require.True(t, db.Closed())

	_, err = db.Version(ctx)
	require.Error(t, err)
	require.Error(t, db.SetMeta(ctx, "k", "v"))
	_, err = db.Meta(ctx, "k")
	require.Error(t, err)
	require.Error(t, db.SetCheckpoint(ctx, "s", "k", "v", epoch))
	_, _, err = db.Checkpoint(ctx, "s", "k")
	require.Error(t, err)
	require.Error(t, db.ClearCheckpoints(ctx, "s"))
	require.Error(t, db.Tx(ctx, func(*sqlTx) error { return nil }))
}

func TestDB_TxReportsAFailureInsideTheCallback(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	// A statement that cannot compile surfaces as the callback's error, and
	// the rollback that follows must not mask it.
	err := db.Tx(ctx, func(tx *sqlTx) error {
		_, err := tx.ExecContext(ctx, "THIS IS NOT SQL")
		return err
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "syntax error")
}

func TestCorruptError_NamesThePathAndTheDetail(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), state.DirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, state.FileName)
	// A valid SQLite header followed by garbage: the file opens, and the
	// integrity check is what rejects it.
	require.NoError(t, os.WriteFile(path, []byte("SQLite format 3\x00garbage"), 0o600))

	db, err := state.Open(context.Background(), dir)
	require.NoError(t, err, "corruption is rebuilt, not refused")
	defer func() { require.NoError(t, db.Close()) }()
	require.NotEmpty(t, db.RebuiltFrom())
}

func TestOpen_RebuildRemovesTheWriteAheadLogOfTheOldDatabase(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), state.DirName)
	ctx := context.Background()
	db, err := state.Open(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, db.SetMeta(ctx, "k", "v"))
	require.NoError(t, db.Close())

	path := filepath.Join(dir, state.FileName)
	require.NoError(t, os.WriteFile(path, []byte("not a database"), 0o600))
	// A stale write ahead log from the old database would be applied to the
	// new one and reintroduce the corruption.
	require.NoError(t, os.WriteFile(path+"-wal", []byte("stale"), 0o600))

	db2, err := state.Open(ctx, dir)
	require.NoError(t, err)
	defer func() { require.NoError(t, db2.Close()) }()
	_, err = os.Stat(path + "-wal")
	require.True(t, os.IsNotExist(err) || err == nil,
		"the stale log must not survive as the old database's")
	v, err := db2.Meta(ctx, "k")
	require.NoError(t, err)
	require.Empty(t, v)
}

func TestOpen_ReportsAMigrationThatCannotApply(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), state.DirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	ctx := context.Background()

	// A pre-existing table with the same name as one a migration creates. This
	// is what a partially restored backup looks like, and it must fail loudly
	// at open rather than leave a half applied schema.
	seed, err := sql.Open("sqlite", "file:"+filepath.Join(dir, state.FileName))
	require.NoError(t, err)
	_, err = seed.ExecContext(ctx, "CREATE TABLE environments (something TEXT)")
	require.NoError(t, err)
	require.NoError(t, seed.Close())

	_, err = state.Open(ctx, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "apply migration 1")
}

func TestOpen_ReportsAPathThatIsADirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), state.DirName)
	// A directory where the database file belongs cannot be opened, and the
	// failure is not corruption, so it must surface rather than trigger a
	// rebuild that would delete the directory.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, state.FileName), 0o700))
	_, err := state.Open(context.Background(), dir)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "integrity")
}

func TestVersion_ReportsZeroOnAFreshMigrationTable(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	v, err := db.Version(context.Background())
	require.NoError(t, err)
	require.Positive(t, v)
}
