//go:build windows

// Package privatefstest gives tests a folder that every user can read, which
// is the folder a private file has to be private in.
package privatefstest

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// OpenFolder returns a directory whose access list lets Everyone read
// everything created inside it, which is what a folder under C:\ gives the
// Users group by default. The user temporary directory is already private to
// its owner, so a test run there could not tell a private file from one that
// merely inherited privacy from where it was put.
func OpenFolder(t testing.TB) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "open")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString(
		"D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}
