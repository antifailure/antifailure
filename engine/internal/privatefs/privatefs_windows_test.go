//go:build windows

package privatefs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/privatefs"
	"github.com/antifailure/antifailure/engine/internal/privatefs/privatefstest"
)

// A directory MkdirAll creates is private to everything made inside it later,
// including a file written by code that knows nothing about this package. That
// is what keeps a database's journal and lock files private without each of
// them being created through here.
func TestAFilePlainlyWrittenInsideAPrivateDirectoryIsPrivate(t *testing.T) {
	dir := filepath.Join(privatefstest.OpenFolder(t), "state")
	require.NoError(t, privatefs.MkdirAll(dir))
	path := filepath.Join(dir, "state.db-wal")
	require.NoError(t, os.WriteFile(path, []byte("wal"), 0o644))
	require.NoError(t, privatefs.Check(path))
}

// Check names who can read it, so the warning a person sees says what to fix.
func TestCheckNamesTheAccountThatCanRead(t *testing.T) {
	path := filepath.Join(privatefstest.OpenFolder(t), "plain")
	require.NoError(t, os.WriteFile(path, []byte("secret"), 0o644))
	err := privatefs.Check(path)
	require.ErrorIs(t, err, privatefs.ErrExposed)
	require.Contains(t, err.Error(), "Everyone")
}
