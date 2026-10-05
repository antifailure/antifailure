//go:build !windows

package live

import (
	"net"
	"os"
	"path/filepath"
)

// Address is the endpoint for a run whose private directory is dir: a socket
// file inside it. What keeps another user off the socket is the directory,
// which the caller creates with os.MkdirTemp and therefore mode 0700, so the
// directory must be one only this user can enter. A short one, because a unix
// socket path has a hard length limit (104 bytes on macOS) that a deep
// artifacts path can exceed.
func Address(dir string) (string, error) {
	return filepath.Join(dir, "l.sock"), nil
}

func listen(addr string) (net.Listener, error) {
	// A stale socket file from a crashed run would refuse the bind. Removing it
	// first is safe: the path is run scoped and nobody else owns it.
	_ = os.Remove(addr)
	return net.Listen("unix", addr)
}

func release(addr string) { _ = os.Remove(addr) }
