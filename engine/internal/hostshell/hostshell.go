// Package hostshell runs a command a manifest wrote, on the machine af runs on.
//
// A manifest's database.seed and persona seed commands are shell, written the
// way a README writes them: pipes, redirects, $DATABASE_URL. On Unix that is
// sh. Windows has no sh, and Command Prompt and PowerShell would each read the
// same line differently, so the contract is the one Git for Windows already
// keeps: the commands run in the sh that ships with Git, which every Windows
// machine running af has, because af needs git. It is found through git itself
// rather than through PATH, where a stray sh from some other toolchain would
// otherwise decide what a seed command means.
package hostshell

import (
	"context"
	"os/exec"
)

// Command returns a command that runs script in the host's sh, or an error
// that says what is missing and how to get it.
func Command(ctx context.Context, script string) (*exec.Cmd, error) {
	sh, err := Find()
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, sh, "-c", script), nil
}
