//go:build windows

package lock

import (
	"errors"

	"golang.org/x/sys/windows"
)

// processExists reports whether a process with this identifier is running.
//
// Asked of the process object's signalled state rather than of whether it can
// be opened. This used to be os.FindProcess(pid) == nil, on the stated belief
// that FindProcess fails for a process that does not exist. It does not fail
// for a process that has EXITED: Windows keeps an exited process's object for
// as long as anything holds a handle to it, an antivirus scan, the shell that
// started it, or FindProcess itself, which opened a handle here and never
// released one. So a lock whose holder had died could read as held, and af
// would wait on a dead owner instead of reclaiming the lock.
//
// Signalled, not the exit code. A process object is signalled when the process
// terminates and at no other time, so a zero timeout wait answers exactly
// "has it ended". The exit code cannot: STILL_ACTIVE is 259, a program can exit
// with 259, and a holder that did would have read as alive for ever.
//
// A process that exists and cannot be opened, one belonging to another user,
// is treated as alive, as the other platforms treat a permission error, and so
// is one whose state cannot be read, because calling a holder dead is the
// error that lets two processes take one lock.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	event, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return true
	}
	return event != windows.WAIT_OBJECT_0
}
