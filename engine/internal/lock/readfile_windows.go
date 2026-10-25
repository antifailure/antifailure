//go:build windows

package lock

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// readFile reads the lock file without stopping anybody from deleting it.
//
// os.ReadFile opens a file sharing read and write but not delete, and Windows
// refuses DeleteFile on a file that any handle has open without
// FILE_SHARE_DELETE. So a process that only LOOKED at the lock, a waiter in
// AcquireWithin asking who holds it, made the holder's Release fail with a
// sharing violation. Every caller drops that error in a defer, so the file
// stayed, it named a process that was still alive, and every later Acquire was
// refused as held until that process exited: for an MCP server, its whole
// life. The required Windows check caught it as AF-RUN-003 from the replay
// store's own test, which retires and imports at once in one process.
//
// Sharing delete is what lets the holder's remove go through while a reader
// has the file open. On the NTFS of every Windows this engine supports the
// remove takes the name away at once, so the holder's next CREATE_NEW and
// everybody else's succeed without waiting for the reader to close.
func readFile(path string) ([]byte, error) {
	f, err := openShared(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// openShared opens the lock file for reading, sharing read, write and delete.
func openShared(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		// Wrapped the way os.Open wraps it, so os.IsNotExist still answers for
		// a lock that is not there.
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// transient reports an error that means another process is part way through
// removing the lock.
//
// A remove on Windows is not one step. DeleteFile opens the file for delete,
// marks it, and closes it, and for that moment the name is still there but
// refuses to be opened, linked over or removed again, with ERROR_ACCESS_DENIED
// rather than "not found". Measured on Windows Server 2022 with eight
// goroutines taking and releasing one lock: 4 runs in 20 had a waiter read that
// refusal as an unreadable lock, try to reclaim it, and fail. It is the same
// fact as the file having gone, observed a moment early, so it is retried
// rather than reported.
func transient(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_DELETE_PENDING) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
