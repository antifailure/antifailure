//go:build !windows

package lock

import (
	"errors"
	"os"
	"syscall"
)

// processExists reports whether a process with this identifier is running.
//
// Signal zero performs the permission and existence checks without delivering
// anything. A permission error means the process exists and belongs to someone
// else, which for lock purposes is the same as alive.
func processExists(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, os.ErrPermission)
}
