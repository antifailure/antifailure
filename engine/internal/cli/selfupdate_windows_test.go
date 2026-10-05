//go:build windows

package cli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestSelfUpdateHelperProcess is not a test. It is the program the test below
// runs and then replaces, so that the binary being replaced really is the
// image of a running process, which is the whole difficulty on Windows.
func TestSelfUpdateHelperProcess(t *testing.T) {
	if os.Getenv("AF_SELFUPDATE_HELPER") != "sleep" {
		t.Skip("helper process only")
	}
	time.Sleep(2 * time.Minute)
}

func TestRunningExecutableIsReplacedOnWindows(t *testing.T) {
	executable, runner := updateInstallationFor(t, "windows")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := os.Create(executable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	_ = src.Close()
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(executable, "-test.run=^TestSelfUpdateHelperProcess$")
	helper.Env = append(os.Environ(), "AF_SELFUPDATE_HELPER=sleep")
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = helper.Process.Kill()
			_ = helper.Wait()
		}
	}
	t.Cleanup(stop)
	// Give the loader time to map the image, so the file is in use.
	time.Sleep(time.Second)

	stage := stagedUpdate(t)
	// The premise, measured rather than assumed: the unix commit, a rename
	// over the binary, really is refused while that binary is running. If
	// Windows ever allows it this arm fails and says the workaround is no
	// longer needed, rather than the test below passing for the wrong reason.
	if err := os.Rename(filepath.Join(stage, "af"), executable); err == nil {
		t.Fatal("Windows allowed a rename over a running image; the premise of the Windows commit no longer holds")
	}
	result, err := commitUpdate(updateResult{InstalledPath: executable}, stage, runner, "windows", os.Rename)
	if err != nil {
		t.Fatalf("the update could not replace a running af.exe: %v", err)
	}
	if !result.Applied {
		t.Fatal("the commit was not reported as applied")
	}
	if b, _ := os.ReadFile(executable); string(b) != "new binary" {
		t.Fatal("af.exe is not the new binary")
	}
	moved, _ := filepath.Glob(filepath.Join(filepath.Dir(executable), "af.exe.old-*"))
	if len(moved) != 1 {
		t.Fatalf("want one moved aside binary, found %v", moved)
	}
	// While the old binary is still running it cannot be removed, and the
	// sweep must leave it rather than fail.
	sweepReplacedExecutable("windows", executable)
	if _, err := os.Stat(moved[0]); err != nil {
		t.Fatal("the moved aside binary vanished while it was still running")
	}
	stop()
	sweepReplacedExecutable("windows", executable)
	if _, err := os.Stat(moved[0]); !os.IsNotExist(err) {
		t.Fatal("the moved aside binary survived the sweep after it stopped running")
	}
}
