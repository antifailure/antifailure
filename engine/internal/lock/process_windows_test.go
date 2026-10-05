//go:build windows

package lock

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// A process that has exited is not alive, even while something still holds a
// handle to it. That something is the ordinary case on Windows, an antivirus
// scan or the shell that started it, and while it holds one the process object
// survives the exit. The old check asked only whether the process could be
// opened, so it answered alive for a dead lock holder whenever anything had the
// object open. This test holds the handle itself, so the case is arranged
// rather than waited for, and it fails every time against the old check.
func TestAnExitedProcessIsNotAliveWhileAHandleKeepsItsObject(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit 0")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	held, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	require.NoError(t, err, "opening a handle to the child before it exits")
	t.Cleanup(func() { _ = windows.CloseHandle(held) })
	require.NoError(t, cmd.Wait())

	// The precondition, proved: the object is still there to be opened.
	again, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	require.NoError(t, err, "the exited process's object is gone, so this machine does not reproduce the case")
	_ = windows.CloseHandle(again)

	require.False(t, processExists(pid), "an exited process read as alive because a handle kept its object")
	require.True(t, processExists(int(windows.GetCurrentProcessId())), "this process read as not alive")
	require.False(t, processExists(0), "pid 0 read as a live lock holder")
}

// 259 is STILL_ACTIVE, the exit code Windows reports for a process that has not
// ended. A check that read the exit code would call a holder that exited with
// 259 alive for ever, and the lock it held would never be reclaimed.
func TestAProcessThatExitedWith259IsNotAlive(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit 259")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	held, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	require.NoError(t, err)
	t.Cleanup(func() { _ = windows.CloseHandle(held) })
	err = cmd.Wait()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 259, exit.ExitCode(), "the child did not exit with 259, so this is not the case it names")

	require.False(t, processExists(pid), "a process that exited with 259 read as alive")
}
