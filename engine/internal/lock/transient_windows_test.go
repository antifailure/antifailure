//go:build windows

package lock

import (
	"context"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"

	"github.com/antifailure/antifailure/engine/internal/clock"
)

// The moment a remove is still in progress, held open so it cannot be missed.
//
// Windows does not remove a file in one step. While a delete is under way the
// name is still there and refuses to be opened, linked over or removed, with
// ERROR_ACCESS_DENIED rather than "not found". A waiter that met that refusal
// read the lock as unreadable, tried to reclaim it, was refused again and
// failed, where it should have waited a moment and taken the lock. Measured on
// Windows Server 2022, eight goroutines taking and releasing one lock met it in
// 4 runs of 20.
//
// The delete here is marked the old way, which keeps the name pending until
// the handle closes, so the refusal lasts as long as the test wants instead of
// a few microseconds. Closing the handle finishes the remove, and the waiter
// has to be the one that then takes the lock.
func TestAcquire_WaitsThroughARemoveThatIsStillInProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	// Held, and never released: the remove below is somebody else's.
	_, err := Acquire(path, clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), "af up")
	require.NoError(t, err)

	p, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	h, err := windows.CreateFile(p, windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	disposition := struct{ DeleteFile bool }{DeleteFile: true}
	require.NoError(t, windows.SetFileInformationByHandle(h, windows.FileDispositionInfo,
		(*byte)(unsafe.Pointer(&disposition)), uint32(unsafe.Sizeof(disposition))))

	// The precondition, proved: the name is there and refuses to be read.
	_, readErr := readFile(path)
	require.ErrorIs(t, readErr, windows.ERROR_ACCESS_DENIED, "the remove in progress was not arranged, so this checks nothing")

	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = windows.CloseHandle(h)
	}()
	again, err := AcquireWithin(context.Background(), path, clock.NewFake(time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)), "af up", 5*time.Second)
	require.NoError(t, err, "a waiter failed on a lock that was only part way through being removed")
	require.NoError(t, again.Release())
}
