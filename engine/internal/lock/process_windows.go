//go:build windows

package lock

import "os"

// processExists reports whether a process with this identifier is running.
//
// On Windows, FindProcess fails outright for a process that does not exist,
// which is the check the other platforms need a signal for.
func processExists(pid int) bool {
	_, err := os.FindProcess(pid)
	return err == nil
}
