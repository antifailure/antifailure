//go:build windows

package cli

import (
	"errors"
	"os"
)

func acquireUpdateLock(string) (*os.File, error) {
	return nil, errors.New("self-update is not supported on Windows")
}
