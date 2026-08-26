//go:build !windows

package cli

import "golang.org/x/sys/unix"

// FreeDiskBytes reports free bytes on the volume holding a path.
func (p systemProber) FreeDiskBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	// Bavail rather than Bfree: blocks free to an unprivileged user, which is
	// what actually determines whether a build succeeds.
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
