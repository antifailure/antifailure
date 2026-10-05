package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runningImageRename behaves as Windows does for the image of a running
// process: it can be renamed, but nothing can be renamed over it. That is the
// one property that made the unix commit impossible there, so the logic of the
// Windows commit is checked against it on every platform, and against the real
// thing by TestRunningExecutableIsReplacedOnWindows on a Windows machine.
func runningImageRename(running string) func(string, string) error {
	return func(from, to string) error {
		if to == running {
			if _, err := os.Stat(to); err == nil {
				return errors.New("Access is denied: the target is a running image")
			}
		}
		return os.Rename(from, to)
	}
}

func stagedUpdate(t *testing.T) string {
	t.Helper()
	stage := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stage, "runner", "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "runner", "src", "main.ts"), []byte("new runner"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "af"), []byte("new binary"), 0755); err != nil {
		t.Fatal(err)
	}
	return stage
}

func TestWindowsCommitMovesTheRunningBinaryAside(t *testing.T) {
	executable, runner := updateInstallationFor(t, "windows")
	stage := stagedUpdate(t)
	result, err := commitUpdate(updateResult{InstalledPath: executable}, stage, runner, "windows", runningImageRename(executable))
	if err != nil {
		t.Fatalf("a Windows update could not replace the binary it was running from: %v", err)
	}
	if !result.Applied {
		t.Fatal("a completed Windows commit was not reported as applied")
	}
	if b, _ := os.ReadFile(executable); string(b) != "new binary" {
		t.Fatalf("af.exe is not the new binary: %q", b)
	}
	moved, _ := filepath.Glob(filepath.Join(filepath.Dir(executable), "af.exe.old-*"))
	if len(moved) != 1 {
		t.Fatalf("want exactly one moved aside binary, found %v", moved)
	}
	if b, _ := os.ReadFile(moved[0]); string(b) != "old binary" {
		t.Fatalf("the moved aside binary is not the old one: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(runner, "src", "main.ts")); string(b) != "new runner" {
		t.Fatal("the runner source was not replaced with the binary")
	}
}

func TestWindowsCommitRestoresBothWhenTheNewBinaryCannotMoveIn(t *testing.T) {
	executable, runner := updateInstallationFor(t, "windows")
	stage := stagedUpdate(t)
	inner := runningImageRename(executable)
	_, err := commitUpdate(updateResult{InstalledPath: executable}, stage, runner, "windows", func(from, to string) error {
		if from == filepath.Join(stage, "af") {
			return errors.New("injected failure moving the new binary in")
		}
		return inner(from, to)
	})
	if err == nil {
		t.Fatal("a failed commit reported success")
	}
	if b, _ := os.ReadFile(executable); string(b) != "old binary" {
		t.Fatalf("the old binary was not put back where it was: %q", b)
	}
	if moved, _ := filepath.Glob(filepath.Join(filepath.Dir(executable), "af.exe.old-*")); len(moved) != 0 {
		t.Fatalf("a rolled back commit left a moved aside binary: %v", moved)
	}
	if b, _ := os.ReadFile(filepath.Join(runner, "old-source")); string(b) != "old runner" {
		t.Fatal("the old runner source was not restored")
	}
}

func TestSweepRemovesOnlyBinariesAWindowsUpdateMovedAside(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "af.exe")
	files := map[string]bool{
		"af.exe":                    false,
		"af.exe.old-1a2b3c4d5e6f":   true,
		"AF.EXE.OLD-4D5E6F7A8B9C":   true,
		"af.exe.old-backup":         false,
		"af.exe.old-1a2b3c":         false,
		"af.exe.old-zzzzzzzzzzzz":   false,
		"tool.exe.old-1a2b3c4d5e6f": false,
		"af.exe.older":              false,
		"notes.txt":                 false,
	}
	for name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "af.exe.old-0d0d0d0d0d0d"), 0755); err != nil {
		t.Fatal(err)
	}
	sweepReplacedExecutable("linux", executable)
	for name := range files {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("a sweep on a platform that never moves binaries aside removed %s", name)
		}
	}
	sweepReplacedExecutable("windows", filepath.Join(dir, "tool.exe"))
	if _, err := os.Stat(filepath.Join(dir, "tool.exe.old-1a2b3c4d5e6f")); err != nil {
		t.Fatal("a sweep from a binary that is not af removed a file")
	}
	sweepReplacedExecutable("windows", executable)
	for name, removed := range files {
		_, err := os.Stat(filepath.Join(dir, name))
		if removed && !os.IsNotExist(err) {
			t.Fatalf("%s was moved aside by an update and survived the sweep", name)
		}
		if !removed && err != nil {
			t.Fatalf("the sweep removed %s, which no update put there", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "af.exe.old-0d0d0d0d0d0d")); err != nil {
		t.Fatal("the sweep removed a directory")
	}
}

// TestStartingAfSweepsWhatAWindowsUpdateMovedAside is the wiring half: the
// sweep above can be right and never run, and the only moment it can run is
// the next start, because the update itself is the process using the file.
func TestStartingAfSweepsWhatAWindowsUpdateMovedAside(t *testing.T) {
	executable, _ := updateInstallationFor(t, "windows")
	moved := filepath.Join(filepath.Dir(executable), "af.exe.old-0a0b0c0d0e0f")
	if err := os.WriteFile(moved, []byte("old binary"), 0755); err != nil {
		t.Fatal(err)
	}
	savedGOOS, savedExecutable := hostGOOS, hostExecutable
	t.Cleanup(func() { hostGOOS, hostExecutable = savedGOOS, savedExecutable })
	hostGOOS = "windows"
	hostExecutable = func() (string, error) { return executable, nil }
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), nil, []string{"version"}, Options{Stdout: &stdout, Stderr: &stderr}); code != 0 {
		t.Fatalf("af version exited %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(moved); !os.IsNotExist(err) {
		t.Fatal("starting af left the binary an earlier update moved aside")
	}
}

func TestOnlyAnInstallerLayoutIsReplacedOnWindows(t *testing.T) {
	for _, tc := range []struct {
		path, prefix string
		managed      bool
	}{
		{filepath.Join("C:", "Users", "u", "antifailure", "bin", "af.exe"), "", true},
		{filepath.Join("C:", "Users", "u", "antifailure", "BIN", "AF.EXE"), "", true},
		{filepath.Join("C:", "Users", "u", "antifailure", "bin", "af"), "", false},
		{filepath.Join("C:", "tools", "af.exe"), "", false},
		{filepath.Join("C:", "tools", "af.exe"), filepath.Join("C:", "Users", "u", "antifailure"), true},
	} {
		if got := installerManaged("windows", tc.path, tc.prefix); got != tc.managed {
			t.Errorf("installerManaged(windows, %s, %q) = %v, want %v", tc.path, tc.prefix, got, tc.managed)
		}
	}
	if installerManaged("linux", filepath.Join("opt", "BIN", "AF"), "") {
		t.Error("case was forgiven on a platform whose file names are case sensitive")
	}
	executable := filepath.Join(t.TempDir(), "af.exe")
	_, err := performUpdate(context.Background(), executable, "", "v1.0.0", "windows", "amd64", "http://127.0.0.1:1", "http://127.0.0.1:1", nil, false)
	if err == nil || !strings.Contains(err.Error(), "install.ps1") {
		t.Fatalf("a Windows binary outside an installation was not pointed at install.ps1: %v", err)
	}
}
