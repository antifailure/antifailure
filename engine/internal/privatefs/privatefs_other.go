//go:build !windows

package privatefs

import (
	"fmt"
	"os"
)

// MkdirAll creates dir and any missing parents, with mode 0700.
func MkdirAll(dir string) error { return os.MkdirAll(dir, 0o700) }

// WriteFile writes data to name with mode 0600. An existing file is removed
// first, because os.WriteFile applies a mode only when it creates the file and
// would keep a wider one it found, such as a temporary file a crash left behind.
func WriteFile(name string, data []byte) error {
	if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(name, data, 0o600)
}

// CreateExclusive creates name for writing with mode 0600, failing if it
// already exists.
func CreateExclusive(name string) (*os.File, error) {
	return os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
}

// Restrict narrows an existing file or directory to its owner.
func Restrict(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info.IsDir() {
		mode = 0o700
	}
	return os.Chmod(path, mode)
}

// Check reports whether only the owner can read path, wrapping ErrExposed with
// the mode when anybody else can.
func Check(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%w: mode %04o", ErrExposed, perm)
	}
	return nil
}

// RestrictCommand is what a person runs to do what Restrict does.
func RestrictCommand(path string, dir bool) string {
	if dir {
		return "chmod 700 '" + path + "'"
	}
	return "chmod 600 '" + path + "'"
}
