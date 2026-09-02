//go:build !windows

// The fifo test lives here because `syscall.Mkfifo` does not exist on Windows.
// A `runtime.GOOS` skip cannot save it: the skip runs at run time and the
// missing symbol is a COMPILE error, so the whole package failed to typecheck
// on Windows while the test read as if the platform had been handled. The
// package already separates platform code this way in open_unix.go and
// open_windows.go, and a build tag is the only guard that acts early enough.

package mcp

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveInRoot_RejectsAFifoWithoutHanging(t *testing.T) {
	t.Parallel()
	root, _ := checkout(t)

	// A fifo with no writer blocks a blocking open forever. If this test ever
	// hangs rather than failing, the nonblocking flag has been dropped and
	// the server can be stalled indefinitely by one call.
	fifo := filepath.Join(root, "pipe")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))

	_, fault := resolveInRoot(root, "pipe")
	require.NotNil(t, fault)
	require.Equal(t, FaultPathRejected, fault.Code)
	require.Contains(t, fault.Detail, "regular file")
}
