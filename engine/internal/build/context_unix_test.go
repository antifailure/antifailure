//go:build unix

package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestContext_SkipsSomethingThatIsNotAFile(t *testing.T) {
	t.Parallel()
	// A repository somebody is working in can hold a unix socket from a dev
	// server or a fifo from a script. Docker cannot put either in an image,
	// and trying to read one blocks forever, so the walk steps over it rather
	// than discovering that at build time.
	root := tree(t, map[string]string{"a.txt": "a"})
	require.NoError(t, unix.Mkfifo(filepath.Join(root, "pipe"), 0o644))

	c := build(t, root, "")
	require.Equal(t, []string{"a.txt"}, c.Files)
	require.Equal(t, 1, c.Excluded)
}

func TestCopyFileInto_PadsAFileThatShrankMidBuild(t *testing.T) {
	t.Parallel()
	// The header is written from the size seen during the walk. If the file
	// shrank since, writing fewer bytes than the header declares corrupts the
	// archive, and the daemon reports a confusing failure about an unexpected
	// EOF instead of anything to do with the file.
	dir := t.TempDir()
	p := filepath.Join(dir, "shrinking.txt")
	require.NoError(t, os.WriteFile(p, []byte("short"), 0o644))

	var buf countingWriter
	require.NoError(t, copyFileInto(&buf, p, 32))
	require.Equal(t, 32, buf.n, "the declared size is written, padded if need be")
}

type countingWriter struct{ n int }

func (c *countingWriter) Write(p []byte) (int, error) { c.n += len(p); return len(p), nil }
