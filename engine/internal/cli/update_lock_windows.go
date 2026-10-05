//go:build windows

package cli

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// acquireUpdateLock takes the installation lock with LockFileEx, the Windows
// counterpart of flock. The lock belongs to the handle, so a second handle in
// the same process is refused just as another process is, and closing the
// handle, or the process ending, releases it.
func acquireUpdateLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another update holds the installation lock: %w", err)
	}
	return f, nil
}
