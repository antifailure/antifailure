//go:build windows

package lock

import (
	"errors"

	"golang.org/x/sys/windows"
)

// processExists reports whether a process with this identifier is running.
//
// Asked of the process's exit code rather than of whether it can be opened.
// This used to be os.FindProcess(pid) == nil, on the stated belief that
// FindProcess fails for a process that does not exist. It does not fail for a
// process that has EXITED: Windows keeps an exited process's object for as long
// as anything holds a handle to it, an antivirus scan, the shell that started
// it, or FindProcess itself, which opened a handle here and never released one.
// So a lock whose holder had died could read as held, and `af` would wait on a
// dead owner instead of reclaiming the lock. TestAlive_IsTheOneLivenessRule
// caught it intermittently on the Windows runners, which is the shape that
// race takes.
//
// A process that exists and cannot be queried, one belonging to another user,
// is treated as alive, as the other platforms treat a permission error.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		// Opened and then unreadable is not evidence of an exit, and calling
		// a holder dead lets two processes take one lock.
		return true
	}
	// A process that genuinely exited with code 259 reads as alive here,
	// which errs the safe way for a lock: it waits rather than steals.
	return code == stillActive
}

// stillActive is STILL_ACTIVE from the Windows SDK, the exit code
// GetExitCodeProcess reports for a process that has not exited.
const stillActive = 259
