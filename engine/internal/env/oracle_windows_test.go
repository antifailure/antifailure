//go:build windows

package env_test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"

	"github.com/antifailure/antifailure/engine/internal/env"
)

// Most Windows machines refuse a symbolic link to an unprivileged process, and
// a baseline that failed on the first link in a repository failed the whole
// comparison. Git for Windows answers the same refusal by checking the link out
// as a small file holding its target, so the working tree the baseline is
// compared with already looks like that, and the baseline has to match it.
func TestUntarWritesALinkAsGitForWindowsDoesWhenLinksAreRefused(t *testing.T) {
	restore := env.SymlinkForTest(func(oldname, newname string) error {
		return &os.LinkError{Op: "symlink", Old: oldname, New: newname, Err: windows.ERROR_PRIVILEGE_NOT_HELD}
	})
	t.Cleanup(restore)

	dir := t.TempDir()
	require.NoError(t, env.UntarForTest(dir, bytes.NewReader(tarOf(t,
		tar.Header{Name: "go.mod", Typeflag: tar.TypeReg, Mode: 0o644},
		tar.Header{Name: "shared/link", Typeflag: tar.TypeSymlink, Linkname: "../go.mod"},
	))))
	got, err := os.ReadFile(filepath.Join(dir, "shared", "link"))
	require.NoError(t, err)
	require.Equal(t, "../go.mod", string(got))
}

// Only that refusal is answered with a file. Any other failure to create a
// link is still a failure, so a full disk or a missing directory is not
// quietly written down as a link target.
func TestUntarStillFailsOnALinkErrorThatIsNotARefusal(t *testing.T) {
	restore := env.SymlinkForTest(func(oldname, newname string) error {
		return &os.LinkError{Op: "symlink", Old: oldname, New: newname, Err: windows.ERROR_DISK_FULL}
	})
	t.Cleanup(restore)

	err := env.UntarForTest(t.TempDir(), bytes.NewReader(tarOf(t,
		tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "go.mod"},
	)))
	require.ErrorIs(t, err, windows.ERROR_DISK_FULL)
}
