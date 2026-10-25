//go:build !windows

package lock

import "os"

// readFile reads the lock file. Off Windows an open file never stops another
// process removing it, so the standard reader is the right one.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path) //nolint:gosec // the path is inside our own state directory
}

// openShared opens the lock file the way readFile does.
func openShared(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec // the path is inside our own state directory
}

// transient reports an error that means another process is part way through
// removing the lock. Off Windows a remove is a single step, so nothing is.
func transient(error) bool { return false }
