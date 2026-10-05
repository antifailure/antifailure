//go:build windows

package pgcopy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The PostgreSQL installer for Windows leaves pg_dump.exe under Program Files
// and off PATH. The search looked only in Unix directories and for a file
// named pg_dump with no extension, so a Windows machine with the client
// installed was told it had none.
func TestTheWindowsInstallerLocationIsSearched(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "PostgreSQL", "18", "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "pg_dump.exe"), nil, 0o755))
	t.Setenv("ProgramFiles", root)
	t.Setenv("ProgramW6432", "")
	t.Setenv("PATH", t.TempDir())

	found, err := toolsFound("pg_dump")
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, filepath.Join(bin, "pg_dump.exe"), found[0].path)
}
