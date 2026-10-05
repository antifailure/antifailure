package privatefstest

import (
	"os"
	"runtime"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/privatefs"
)

// RequirePrivate fails the test unless only the owner can read path. On Unix
// that is the exact mode the caller names, so a file that is private but
// unexpectedly executable still fails; on Windows, where Go reports every file
// as 0666, it is the file's access list.
func RequirePrivate(t testing.TB, path string, mode os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		if err := privatefs.Check(path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != mode {
		t.Fatalf("%s has mode %04o, want %04o", path, got, mode)
	}
}
