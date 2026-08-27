//go:build windows

package personas

import "os/exec"

// Windows has no process groups in the POSIX sense, so the command is killed
// on its own and WaitDelay is what bounds the wait for a grandchild that
// inherited the pipe. See seed_unix.go for why that bound has to exist.
func isolateProcessGroup(*exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
