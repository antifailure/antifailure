//go:build windows

package hostshell

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNoShell is the refusal when Git for Windows' sh cannot be found.
var ErrNoShell = errors.New("commands in antifailure.yaml run in the sh that ships with Git for Windows, " +
	"and it was not found; install Git for Windows from https://git-scm.com/download/win " +
	"or with 'winget install --id Git.Git -e', then open a new terminal")

// Find returns Git for Windows' sh. git --exec-path names git's own libexec
// directory, mingw64\libexec\git-core under the installation, and sh sits in
// bin and usr\bin two levels above it. bin\sh.exe comes first because it is the
// launcher that puts Git's own tools, grep and sort and env among them, ahead of
// Windows' on the path the command sees.
func Find() (string, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return "", ErrNoShell
	}
	out, err := exec.Command(git, "--exec-path").Output()
	if err != nil {
		return "", fmt.Errorf("%w (git --exec-path: %w)", ErrNoShell, err)
	}
	return findFrom(strings.TrimSpace(string(out)))
}

func findFrom(execPath string) (string, error) {
	if execPath == "" {
		return "", ErrNoShell
	}
	root := filepath.Clean(filepath.Join(filepath.FromSlash(execPath), "..", "..", ".."))
	for _, candidate := range []string{
		filepath.Join(root, "bin", "sh.exe"),
		filepath.Join(root, "usr", "bin", "sh.exe"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w (looked in %s)", ErrNoShell, root)
}
