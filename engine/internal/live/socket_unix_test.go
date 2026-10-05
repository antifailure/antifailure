//go:build !windows

package live

import (
	"net"
	"path/filepath"
	"testing"
)

func dialLive(addr string) (net.Conn, error) { return net.Dial("unix", addr) }

// unlistenable is a socket path in a directory that does not exist, which no
// bind can satisfy.
func unlistenable(t *testing.T) string {
	return filepath.Join(t.TempDir(), "missing", "l.sock")
}
