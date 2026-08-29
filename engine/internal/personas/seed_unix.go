//go:build !windows

package personas

import (
	"os/exec"
	"syscall"
)

// A seed command is killed as a process group, not as a process.
//
// `sh -c "..."` may exec the command directly, replacing itself, or it may
// fork and wait. Which one happens depends on the shell and on the command,
// and the difference decides whether a timeout works: killing only the shell
// leaves the grandchild running, still holding the write end of the pipe the
// engine is reading, so Wait blocks until that grandchild finishes on its own.
//
// The timeout then bounds nothing at all, which is the opposite of what it is
// for. This was not theoretical: the test asserting a hung seed command is cut
// off passed on macOS, where the shell execs, and failed on Linux, where it
// forks. The bound was 300ms and the command took the full 30 seconds.
func isolateProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup ends the command and everything it started.
//
// The negative pid is the whole group. Falling back to the process alone
// matters for the race where it has already exited: the group is gone, and
// killing the process is then a no-op rather than an error worth reporting.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
