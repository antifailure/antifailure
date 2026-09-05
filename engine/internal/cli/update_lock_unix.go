//go:build !windows

package cli

import (
	"fmt"
	"os"
	"syscall"
)

func acquireUpdateLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another update holds the installation lock: %w", err)
	}
	return f, nil
}
