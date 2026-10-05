//go:build !windows

// Package privatefstest gives tests a folder that every user can read, which
// is the folder a private file has to be private in.
package privatefstest

import (
	"os"
	"testing"
)

// OpenFolder returns a directory everybody can read and enter.
func OpenFolder(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
