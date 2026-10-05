//go:build windows

package hostshell

import (
	"errors"
	"os"
	"os/exec"
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

// Found through git and not through PATH. On a machine whose PATH reaches git
// and no sh, which is how the Git for Windows installer leaves it by default,
// a PATH lookup finds nothing and this still finds Git's own sh. A lookup that
// happened to work because Git's bin directory was on PATH, as it is on a
// GitHub runner, would pass every other test here.
func TestTheShellIsFoundWithoutItBeingOnThePath(t *testing.T) {
	git, err := exec.LookPath("git")
	require.NoError(t, err)
	gitDir := filepath.Dir(git)
	if _, err := os.Stat(filepath.Join(gitDir, "sh.exe")); err == nil {
		t.Skipf("git and sh share %s here, so a PATH without sh cannot be made", gitDir)
	}
	t.Setenv("PATH", gitDir+string(os.PathListSeparator)+os.Getenv("SystemRoot")+`\System32`)
	_, err = exec.LookPath("sh")
	require.Error(t, err, "the precondition: sh is not on this PATH")

	sh, err := Find()
	require.NoError(t, err)
	require.True(t, strings.EqualFold(filepath.Base(sh), "sh.exe"), sh)
}
