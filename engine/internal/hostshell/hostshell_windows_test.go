//go:build windows

package hostshell

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Found through git, so the sh is Git for Windows' and not whichever sh came
// first on the path.
func TestTheShellIsGitForWindowsOwn(t *testing.T) {
	sh, err := Find()
	require.NoError(t, err)
	require.True(t, strings.EqualFold(filepath.Base(sh), "sh.exe"), sh)
	_, err = os.Stat(sh)
	require.NoError(t, err)
}

// A machine without Git for Windows is told what to install rather than
// handed "executable file not found".
func TestAMissingShellSaysWhatToInstall(t *testing.T) {
	_, err := findFrom(filepath.Join(t.TempDir(), "mingw64", "libexec", "git-core"))
	require.True(t, errors.Is(err, ErrNoShell), "%v", err)
	require.Contains(t, err.Error(), "Git for Windows")

	_, err = findFrom("")
	require.ErrorIs(t, err, ErrNoShell)
}

// The launcher in bin is preferred over usr\bin\sh.exe, because it is the one
// that puts Git's own tools ahead of Windows' on the path.
func TestTheLauncherIsPreferred(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{`bin\sh.exe`, `usr\bin\sh.exe`, `mingw64\libexec\git-core\.keep`} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, p), nil, 0o644))
	}
	sh, err := findFrom(filepath.ToSlash(filepath.Join(root, "mingw64", "libexec", "git-core")))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "bin", "sh.exe"), sh)
}
