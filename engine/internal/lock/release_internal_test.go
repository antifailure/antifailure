package lock

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
)

// A process reading the lock must not stop its holder releasing it. On Windows
// the reader used to open the file without sharing delete, so a waiter that was
// only asking who held the lock made Release fail with a sharing violation. The
// callers drop that error in a defer, so the file stayed behind naming a live
// process and every later Acquire was refused until that process exited.
//
// The reader's handle is held open across the release here rather than raced
// against it, so the overlap the defect needs is arranged on every run instead
// of being hoped for. The second Acquire happens while the handle is STILL
// open, which is the half that proves the name went away at once rather than
// lingering until the reader closed it.
func TestRelease_SucceedsWhileAReaderHasTheLockOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	held, err := Acquire(path, clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), "af up")
	require.NoError(t, err)

	reader, err := openShared(path)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()

	require.NoError(t, held.Release(), "a reader with the lock open made the release fail")
	_, statErr := os.Stat(path)
	require.True(t, os.IsNotExist(statErr), "the released lock is still on disk: %v", statErr)

	again, err := Acquire(path, clock.NewFake(time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)), "af up")
	require.NoError(t, err, "the lock could not be taken again while the reader still had the old one open")
	require.NoError(t, again.Release())
}
