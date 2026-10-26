//go:build windows

package lock

import (
	"context"
	"path/filepath"
	"sync"
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
//
// Held for three lengths, because the wait used to be a COUNT: a hundred
// retries a millisecond apart. On a hosted runner, where a millisecond sleep
// sleeps a millisecond, they were spent in 0.20 s and the 200 ms hold failed
// (#684, job 121325668047); on a VM whose sleeps ran long it passed. 500 ms and
// 1500 ms are holds no count of that kind survives on any host, and both are
// inside settleWithin, which is what the waiter now promises.
func TestAcquire_WaitsThroughARemoveThatIsStillInProgress(t *testing.T) {
	for _, hold := range []time.Duration{200 * time.Millisecond, 500 * time.Millisecond, 1500 * time.Millisecond} {
		t.Run(hold.String(), func(t *testing.T) {
			path, release := pendingRemove(t, hold)
			defer release()
			again, err := AcquireWithin(context.Background(), path, clock.NewFake(time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)), "af up", 5*time.Second)
			require.NoError(t, err, "a waiter failed on a lock that was only part way through being removed")
			require.NoError(t, again.Release())
		})
	}
}

// The other side: a name that is still refusing after settleWithin is not a
// remove in progress, and the waiter has to stop and say what it saw rather
// than look for ever.
func TestAcquire_StopsWaitingForARemoveThatNeverFinishes(t *testing.T) {
	path, release := pendingRemove(t, settleWithin+time.Second)
	defer release()
	started := time.Now()
	_, err := Acquire(path, clock.NewFake(time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)), "af up")
	took := time.Since(started)
	require.Error(t, err)
	require.ErrorIs(t, err, windows.ERROR_ACCESS_DENIED, "the refusal does not carry what the last look saw")
	require.Contains(t, err.Error(), "still being let go after 2s")
	require.GreaterOrEqual(t, took, settleWithin, "it gave up before its own bound")
}

// pendingRemove takes the lock at a fresh path and marks it deleted through a
// handle that keeps the remove pending until hold has passed. release closes
// the handle early if the test ends first.
func pendingRemove(t *testing.T, hold time.Duration) (string, func()) {
	t.Helper()
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

	var once sync.Once
	closeIt := func() { once.Do(func() { _ = windows.CloseHandle(h) }) }
	timer := time.AfterFunc(hold, closeIt)
	return path, func() { timer.Stop(); closeIt() }
}
