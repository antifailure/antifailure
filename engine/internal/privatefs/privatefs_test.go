package privatefs_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/privatefs"
	"github.com/antifailure/antifailure/engine/internal/privatefs/privatefstest"
)

// Every test writes into a folder that every user on the machine can read,
// because that is the case the package exists for: a repository checked out
// under C:\ on Windows, or a umask of 022 on Unix. A private file in a private
// folder proves nothing about the file.

// The control first. A plain write into that folder is readable by others, so
// Check can say no; without this arm every pass below could be Check passing
// everything.
func TestCheckSaysNoToAPlainFileInAnOpenFolder(t *testing.T) {
	path := filepath.Join(privatefstest.OpenFolder(t), "plain")
	require.NoError(t, os.WriteFile(path, []byte("secret"), 0o644))
	err := privatefs.Check(path)
	require.ErrorIs(t, err, privatefs.ErrExposed)
}

func TestWriteFileIsPrivateEvenInAnOpenFolder(t *testing.T) {
	path := filepath.Join(privatefstest.OpenFolder(t), "secret")
	require.NoError(t, privatefs.WriteFile(path, []byte("secret")))
	require.NoError(t, privatefs.Check(path))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "secret", string(got), "private to its owner, and still readable by them")
}

// Rewriting a file that was readable by others replaces it rather than
// truncating it, because truncating keeps the access it already had.
func TestWriteFileOverAnExposedFileLeavesItPrivate(t *testing.T) {
	path := filepath.Join(privatefstest.OpenFolder(t), "secret")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))
	require.ErrorIs(t, privatefs.Check(path), privatefs.ErrExposed)

	require.NoError(t, privatefs.WriteFile(path, []byte("new")))
	require.NoError(t, privatefs.Check(path))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new", string(got))
}

func TestMkdirAllMakesAPrivateDirectory(t *testing.T) {
	dir := filepath.Join(privatefstest.OpenFolder(t), "state", "nested")
	require.NoError(t, privatefs.MkdirAll(dir))
	require.NoError(t, privatefs.Check(dir))
	// And again, which is how every caller uses it.
	require.NoError(t, privatefs.MkdirAll(dir))
}

func TestCreateExclusiveRefusesAFileThatExists(t *testing.T) {
	path := filepath.Join(privatefstest.OpenFolder(t), "lock")
	f, err := privatefs.CreateExclusive(path)
	require.NoError(t, err)
	_, err = f.WriteString("pid")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, privatefs.Check(path))

	_, err = privatefs.CreateExclusive(path)
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrExist), "the caller tells a held lock from a failure by this: %v", err)
}

func TestRestrictNarrowsAnExposedFileAndDirectory(t *testing.T) {
	open := privatefstest.OpenFolder(t)
	path := filepath.Join(open, "db")
	require.NoError(t, os.WriteFile(path, []byte("journal"), 0o644))
	require.ErrorIs(t, privatefs.Check(path), privatefs.ErrExposed)
	require.NoError(t, privatefs.Restrict(path))
	require.NoError(t, privatefs.Check(path))

	dir := filepath.Join(open, "dir")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.ErrorIs(t, privatefs.Check(dir), privatefs.ErrExposed)
	require.NoError(t, privatefs.Restrict(dir))
	require.NoError(t, privatefs.Check(dir))
}
