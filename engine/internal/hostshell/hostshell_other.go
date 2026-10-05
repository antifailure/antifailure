//go:build !windows

package hostshell

import "os/exec"

// Find returns the sh a manifest's commands run in.
func Find() (string, error) { return exec.LookPath("sh") }
