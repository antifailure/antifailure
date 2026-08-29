package pgcopy

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// pg_dump refuses to read a server newer than itself, which makes choosing the
// right binary a correctness question rather than a convenience. These tests
// are about that choice. They do not need a database.

func TestToolMajor_ReadsWhatTheRealBinariesSay(t *testing.T) {
	// Against the binaries actually installed here, because the format of
	// --version is the thing being parsed and a fixture would only prove the
	// parser agrees with the fixture.
	for _, name := range []string{"pg_dump", "pg_restore"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("skipped: %s is not installed on this machine", name)
		}
		major := toolMajor(path)
		if major < 9 || major > 99 {
			out, _ := exec.Command(path, "--version").Output()
			t.Fatalf("%s at %s reported major %d, from %q", name, path, major, strings.TrimSpace(string(out)))
		}
	}
}

func TestToolMajor_ParsesTheFormsTheseBinariesActuallyPrint(t *testing.T) {
	// The Debian and Ubuntu packages append their own version in brackets,
	// which is why this reads from the right rather than taking the third
	// field. A parser that took field three would return 16 for the first of
	// these and nothing for the second.
	if runtime.GOOS == "windows" {
		t.Skip("skipped: the shim below is a shell script")
	}
	dir := t.TempDir()
	for _, tc := range []struct {
		name   string
		output string
		want   int
	}{
		{"plain", "pg_dump (PostgreSQL) 17.2", 17},
		{"debian", "pg_dump (PostgreSQL) 16.15 (Ubuntu 16.15-1.pgdg24.04+2)", 16},
		{"homebrew", "pg_restore (PostgreSQL) 18.0", 18},
		{"nonsense", "not a version at all", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			script := "#!/bin/sh\necho '" + tc.output + "'\n"
			if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			if got := toolMajor(path); got != tc.want {
				t.Fatalf("%q parsed as %d, wanted %d", tc.output, got, tc.want)
			}
		})
	}
}

func TestToolFor_SaysWhatIsMissingRatherThanFailingLater(t *testing.T) {
	// A binary that is not there at all is a setup problem, and the message
	// has to say which binary and what to do, because the alternative is a
	// failed refresh twenty minutes in.
	_, _, err := toolFor("pg_dump_that_is_not_installed_anywhere", 17)
	if err == nil {
		t.Fatal("a tool that does not exist should not resolve")
	}
	for _, want := range []string{"pg_dump_that_is_not_installed_anywhere", "Install the Postgres client tools"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the message does not mention %q: %v", want, err)
		}
	}
}

func TestTooOld_NamesTheGapAndThePackageThatClosesIt(t *testing.T) {
	// The message Postgres itself gives is "aborting because of server version
	// mismatch", which names neither the fix nor the package. This is the
	// whole reason the error is rewritten rather than passed through.
	err := tooOld("pg_dump", 16, 17)
	for _, want := range []string{
		"Postgres 17",                          // what the source is
		"newest pg_dump on this machine is 16", // what is here
		"postgresql-client-17",                 // the package on Debian and Ubuntu
		"brew install libpq",                   // and on macOS
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the message does not mention %q: %v", want, err)
		}
	}
}
