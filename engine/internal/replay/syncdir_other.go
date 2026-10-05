//go:build !windows

package replay

import "os"

// syncDir makes a rename or link inside dir durable. On POSIX systems the new
// name lives in the directory, and the directory is only on disk once it is
// synced itself.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
