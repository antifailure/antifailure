// Package state is the engine's crash safe local store.
//
// Everything the engine needs to survive a crash lives here: the journal of
// external resources it created, masking checkpoints so an interrupted run
// resumes rather than restarts, environment records, and golden reference
// counts. It is SQLite in write ahead logging mode under .antifailure, opened
// with a pure Go driver so that the shipped binary needs no C toolchain and
// keeps CGO_ENABLED=0.
//
// The store is deliberately boring. It holds references, never secrets, and
// never customer data. If the file is lost the worst case is that the leak
// detector has to reconcile against provider inventory instead of a ledger,
// which is exactly why the leak detector exists.
package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go driver, no cgo

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// DirName is the per repository state directory.
const DirName = ".antifailure"

// FileName is the state database inside DirName.
const FileName = "state.db"

// DB is an open state store.
type DB struct {
	db          *sql.DB
	path        string
	rebuiltFrom string
	// closed makes Close idempotent without clearing db.
	//
	// Clearing the handle would be tidier, and it would also turn every later
	// call into a nil pointer panic. That matters here more than anywhere
	// else: the calls that race Close are the journal writes during teardown,
	// so a panic would crash the process in the middle of deleting resources,
	// which is exactly when a crash is most expensive. Keeping the closed
	// handle means database/sql returns sql.ErrConnDone and the caller reports
	// it.
	closed bool
}

// Migration is one schema step. Migrations are append only and never edited
// once released, so that a database written by an older binary opens under a
// newer one.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// migrations is the full schema history.
var migrations = []Migration{
	{
		Version: 1,
		Name:    "initial",
		SQL: `
CREATE TABLE environments (
    id           TEXT PRIMARY KEY,
    branch       TEXT NOT NULL,
    repo         TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL,
    golden       TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    expires_at   INTEGER,
    last_seen_at INTEGER,
    url          TEXT NOT NULL DEFAULT '',
    manifest_sha TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX environments_branch ON environments(branch);
CREATE INDEX environments_status ON environments(status);

-- The journal. Every external resource is written here as an intent before it
-- is created, so that a crash at any instant leaves a record to compensate.
CREATE TABLE journal (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    env           TEXT NOT NULL DEFAULT '',
    provider      TEXT NOT NULL,
    kind          TEXT NOT NULL,
    idem_key      TEXT NOT NULL,
    external_id   TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL,
    compensation  TEXT NOT NULL DEFAULT '{}',
    attempts      INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;

-- One live record per idempotency key. A retry after a timeout finds the
-- existing intent instead of creating a duplicate resource.
CREATE UNIQUE INDEX journal_idem ON journal(provider, kind, idem_key)
    WHERE state != 'compensated';
CREATE INDEX journal_env ON journal(env);
CREATE INDEX journal_state ON journal(state);

-- Golden versions and the environments that reference them. Reference counts
-- are transactional with branch creation so that collection can never remove a
-- version an environment is about to branch from.
CREATE TABLE goldens (
    version     TEXT PRIMARY KEY,
    provider    TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    verified    INTEGER NOT NULL DEFAULT 0,
    attestation TEXT NOT NULL DEFAULT '',
    rules_sha   TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE golden_refs (
    version TEXT NOT NULL,
    env     TEXT NOT NULL,
    PRIMARY KEY (version, env)
) STRICT;

-- Resumable work. A masking run checkpoints per chunk so an interruption
-- resumes rather than starting a twenty gigabyte rewrite again.
CREATE TABLE checkpoints (
    scope      TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (scope, key)
) STRICT;

-- Small opaque values: the last golden refresh time, the detected schema
-- fingerprint, the installed version. Never secrets.
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
`,
	},
	{
		Version: 2,
		Name:    "env_leases",
		SQL: `
-- An extension somebody asked for on one environment's lifetime.
--
-- Separate from the expiry stamped on the resources because a container's
-- labels cannot be changed after it is created. An environment's stated
-- lifetime is therefore whatever its resources say unless a row here says
-- otherwise, and the reaper consults both.
--
-- ceiling_at is what stops this from being a way to live forever. It is set
-- once, from the environment's creation time plus runtime.max_ttl, and every
-- later extension is clamped to it. Stored rather than recomputed so that the
-- bound is enforced by the reaper as well as by the command that takes the
-- lease: an environment cannot buy unbounded life by being extended through a
-- build whose manifest says something more generous, or by a hand-edited row.
CREATE TABLE env_leases (
    env        TEXT PRIMARY KEY,
    expires_at INTEGER NOT NULL,
    ceiling_at INTEGER NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
`,
	},
}

// SchemaVersion is the version a new database is created at.
func SchemaVersion() int { return migrations[len(migrations)-1].Version }

// Open opens or creates the state database under dir.
//
// The directory is created with mode 0700 because it holds the journal and
// local handles. A database that fails its integrity check is backed up and
// rebuilt rather than refused, because refusing to start leaves the user with
// resources they cannot tear down.
func Open(ctx context.Context, dir string) (*DB, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN010, "path", dir, "needed", "the state directory")
	}
	path := filepath.Join(dir, FileName)

	db, err := open(ctx, path)
	if err == nil {
		return db, nil
	}
	var corrupt *CorruptError
	if !errors.As(err, &corrupt) {
		return nil, err
	}
	backup, rebuildErr := rebuild(ctx, path)
	if rebuildErr != nil {
		return nil, rebuildErr
	}
	db, err = open(ctx, path)
	if err != nil {
		return nil, err
	}
	db.rebuiltFrom = backup
	return db, nil
}

// CorruptError reports that the state database failed its integrity check.
type CorruptError struct {
	Path   string
	Detail string
}

// corruptionMarkers are the messages SQLite produces for a file that is not a
// usable database. Matching on the text is unpleasant, and the alternative,
// importing the driver's error type, would leak the driver choice into this
// package's interface for no gain.
var corruptionMarkers = []string{
	"file is not a database",
	"database disk image is malformed",
	"file is encrypted or is not a database",
	"database corruption",
	"malformed database schema",
}

func isCorruption(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, m := range corruptionMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("state: %s failed its integrity check: %s", e.Path, e.Detail)
}

func open(ctx context.Context, path string) (*DB, error) {
	// _txlock=immediate takes the write lock at BEGIN rather than on the first
	// write, which turns SQLITE_BUSY under concurrency into a clean wait
	// instead of a mid transaction failure.
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)" +
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("state: open %s: %w", path, err)
	}
	// SQLite tolerates one writer. Bounding the pool keeps contention inside
	// the driver's busy timeout rather than surfacing as random failures.
	sqldb.SetMaxOpenConns(1)
	sqldb.SetMaxIdleConns(1)
	sqldb.SetConnMaxLifetime(0)

	if err := sqldb.PingContext(ctx); err != nil {
		_ = sqldb.Close()
		// Corruption surfaces here as often as it does at the integrity check:
		// a file that is not a database at all fails on the first read, before
		// any pragma runs. Classifying it as corruption is what lets Open
		// rebuild rather than refuse, which matters because refusing leaves the
		// user holding resources they can no longer tear down.
		if isCorruption(err) {
			return nil, &CorruptError{Path: path, Detail: err.Error()}
		}
		return nil, fmt.Errorf("state: connect %s: %w", path, err)
	}

	var check string
	if err := sqldb.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil {
		_ = sqldb.Close()
		return nil, &CorruptError{Path: path, Detail: err.Error()}
	}
	if check != "ok" {
		_ = sqldb.Close()
		return nil, &CorruptError{Path: path, Detail: check}
	}

	db := &DB{db: sqldb, path: path}
	if err := db.migrate(ctx); err != nil {
		_ = sqldb.Close()
		if isCorruption(err) {
			return nil, &CorruptError{Path: path, Detail: err.Error()}
		}
		return nil, err
	}
	// Restrict permissions after creation; the file holds the journal.
	if err := os.Chmod(path, 0o600); err != nil && !os.IsNotExist(err) {
		_ = sqldb.Close()
		return nil, fmt.Errorf("state: secure %s: %w", path, err)
	}
	return db, nil
}

// rebuild moves a corrupt database aside and returns the backup path.
func rebuild(_ context.Context, path string) (string, error) {
	backup := path + ".corrupt." + time.Now().UTC().Format("20060102T150405Z")
	if err := os.Rename(path, backup); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("state: move the corrupt database aside: %w", err)
	}
	// The write ahead log and shared memory files belong to the old database.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("state: remove %s: %w", path+suffix, err)
		}
	}
	return backup, nil
}

// RebuiltFrom returns the backup path if this database replaced a corrupt one,
// and the empty string otherwise.
//
// The command boundary turns a non empty value into AF-RUN-011, which tells
// the user what was lost and to run af down to reconcile.
func (d *DB) RebuiltFrom() string { return d.rebuiltFrom }

func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at INTEGER NOT NULL
) STRICT;`); err != nil {
		return fmt.Errorf("state: create the migration table: %w", err)
	}

	var current int
	if err := d.db.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current); err != nil {
		return fmt.Errorf("state: read the schema version: %w", err)
	}
	if current > SchemaVersion() {
		return fmt.Errorf(
			"state: %s was written by a newer version of Antifailure (schema %d, this build understands %d)",
			d.path, current, SchemaVersion())
	}

	for _, m := range migrations {
		if m.Version <= current {
			continue
		}
		// Each migration is one transaction, so a failure leaves the schema at
		// the previous version rather than half applied.
		tx, err := d.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("state: begin migration %d: %w", m.Version, err)
		}
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("state: apply migration %d (%s): %w", m.Version, m.Name, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
			m.Version, m.Name, time.Now().UTC().UnixMilli()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("state: record migration %d: %w", m.Version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("state: commit migration %d: %w", m.Version, err)
		}
	}
	return nil
}

// Version reports the applied schema version.
func (d *DB) Version(ctx context.Context) (int, error) {
	var v int
	err := d.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("state: read the schema version: %w", err)
	}
	return v, nil
}

// Path returns the database file path.
func (d *DB) Path() string { return d.path }

// SQL exposes the underlying handle to packages in this module that own their
// own tables, such as journal. It is not part of any public interface.
func (d *DB) SQL() *sql.DB { return d.db }

// Close closes the database. It is idempotent, and calls made after it return
// an error rather than panicking; see the closed field.
func (d *DB) Close() error {
	if d.db == nil || d.closed {
		return nil
	}
	d.closed = true
	if err := d.db.Close(); err != nil {
		return fmt.Errorf("state: close %s: %w", d.path, err)
	}
	return nil
}

// Closed reports whether Close has been called.
func (d *DB) Closed() bool { return d.closed }

// Tx runs fn inside a transaction, committing on success and rolling back on
// any error or panic.
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("state: begin: %w", err)
	}
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			panic(r)
		}
	}()
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			// Both wrapped. A caller asking errors.Is about the original
			// failure has to get an answer even when the rollback also
			// failed, and it is the original that says what went wrong.
			return fmt.Errorf("state: rollback after %w: %w", err, rbErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("state: commit: %w", err)
	}
	return nil
}

// SetMeta stores a small opaque value. Never a secret.
func (d *DB) SetMeta(ctx context.Context, key, value string) error {
	_, err := d.db.ExecContext(ctx,
		"INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value)
	if err != nil {
		return fmt.Errorf("state: set meta %s: %w", key, err)
	}
	return nil
}

// Meta reads a value stored by SetMeta. A missing key returns the empty string
// and no error, because absent and empty mean the same thing for these values.
func (d *DB) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := d.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("state: get meta %s: %w", key, err)
	}
	return v, nil
}

// SetCheckpoint records resumable progress for a scope, for example a masking
// run's position in a table.
func (d *DB) SetCheckpoint(ctx context.Context, scope, key, value string, now time.Time) error {
	_, err := d.db.ExecContext(ctx, `
INSERT INTO checkpoints (scope, key, value, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT(scope, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		scope, key, value, now.UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("state: set checkpoint %s/%s: %w", scope, key, err)
	}
	return nil
}

// Checkpoint reads a checkpoint. The boolean reports whether one existed.
func (d *DB) Checkpoint(ctx context.Context, scope, key string) (string, bool, error) {
	var v string
	err := d.db.QueryRowContext(ctx,
		"SELECT value FROM checkpoints WHERE scope = ? AND key = ?", scope, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("state: get checkpoint %s/%s: %w", scope, key, err)
	}
	return v, true, nil
}

// ClearCheckpoints removes every checkpoint in a scope, which a completed run
// does so that the next run starts clean.
func (d *DB) ClearCheckpoints(ctx context.Context, scope string) error {
	if _, err := d.db.ExecContext(ctx, "DELETE FROM checkpoints WHERE scope = ?", scope); err != nil {
		return fmt.Errorf("state: clear checkpoints %s: %w", scope, err)
	}
	return nil
}
