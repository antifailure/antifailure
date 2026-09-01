//go:build darwin

package secrets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// SystemKeyring is the macOS keychain, reached through the security command.
//
// Through the command rather than through the Security framework, because the
// framework needs cgo and this binary is built with CGO_ENABLED=0 so that it
// runs on a distroless image and on a distribution whose libc is older than the
// builder's. The command ships with the operating system.
//
// The value used to go through the -w flag, which put every secret this
// product stores into a child process's argv, where any other user on the
// machine can read it out of ps. A control plane bearer token was read that way
// on this project's own machine, by accident, by somebody looking at something
// else.
//
// Both halves of the comment that justified it were false, and that is the part
// worth remembering. It said the value goes through a flag "because security
// takes it that way": security reads the value from stdin when -w is given no
// argument, prompting twice, which was settled by running it. And it said the
// exposure was acceptable because "writing is only done by 'af secret set' on a
// workstation, never in CI": af login and af model set are both first run
// workstation commands and both reach this line, so the restriction it relied
// on was not enforced by anything.
//
// The product had already decided this matters. cli/model.go says in capitals
// that the key is never an argument, because a secret on a command line is in
// the shell history, is visible in ps, and is in any recording of the terminal.
// af model set refuses a --key flag and reads without echo for those reasons,
// and then handed the key to this function, which put it on a command line one
// process deeper. A promise kept at the top layer and broken underneath is
// worth less than no promise, because it is believed.
//
// keyring_linux.go has always passed its value on stdin, and its comment says
// the macOS command "is not" built to read it that way. That belief is what
// kept this unexamined. Windows uses the Win32 credential API and starts no
// child process at all, so macOS was the only affected platform.
type SystemKeyring struct{}

// keyringTimeout bounds every call to security.
//
// Without one, a keychain that blocks rather than failing has no upper bound,
// and the ErrKeyringUnavailable fallback to a file cannot fire, because a hang
// is not an error. Generous, because the first write of a session can put a
// real unlock prompt in front of a person and that is not a fault.
const keyringTimeout = 2 * time.Minute

func newSystemKeyring() Keyring { return SystemKeyring{} }

func (SystemKeyring) Get(service, name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), keyringTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "security", "find-generic-password",
		"-s", service, "-a", name, "-w").Output()
	if err != nil {
		var exit *exec.ExitError
		// 44 is "the specified item could not be found in the keychain",
		// which is a miss rather than a failure of the keychain.
		if errors.As(err, &exit) && exit.ExitCode() == 44 {
			return "", ErrNotFound
		}
		if errors.Is(err, exec.ErrNotFound) {
			return "", ErrKeyringUnavailable
		}
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func (k SystemKeyring) Set(service, name, value string) error {
	// A value with a newline in it cannot go through this route, because the
	// prompt protocol is line based: security reads a line, asks again, and
	// compares. Refused rather than truncated or silently mangled, and pointed
	// at the store that can hold it, which is the same answer the Windows
	// implementation gives to a PEM key too long for its blob limit.
	if strings.ContainsAny(value, "\n\r") {
		return fmt.Errorf(
			"secrets: this value contains a line break, which the macOS keychain command cannot "+
				"be given without putting it on a command line; keep it in the encrypted local "+
				"store instead: %w", ErrKeyringUnavailable)
	}

	// -w LAST and with no argument, which is what makes security read the value
	// from stdin rather than from argv.
	//
	// The order is load bearing and gets no help from the tool if it is wrong.
	// With -w before -U, security takes the literal string "-U" as the password
	// and exits 0, so the keychain ends up holding a flag, the caller is told
	// nothing, and the next read returns "-U" where a key should be. Found by
	// running it, not by reading the manual page.
	ctx, cancel := context.WithTimeout(context.Background(), keyringTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "security", "add-generic-password",
		"-s", service, "-a", name, "-U", "-w")

	// Twice, because the prompt asks for the value and then asks again to
	// confirm it. A single line leaves the second read waiting on a closed
	// stdin and the command fails.
	cmd.Stdin = strings.NewReader(value + "\n" + value + "\n")
	cmd.Stdout = io.Discard

	// Captured rather than discarded so a real failure still says why, and
	// rather than inherited so the two prompts do not appear in the middle of
	// whatever the command was printing.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrKeyringUnavailable
		}
		if detail := promptsRemoved(stderr.String()); detail != "" {
			return fmt.Errorf("secrets: the keychain refused the write: %s: %w", detail, err)
		}
		return err
	}
	return nil
}

// promptsRemoved strips the two prompts security writes while reading, so an
// error message carries what went wrong rather than the ordinary conversation
// that happens on every successful write.
func promptsRemoved(s string) string {
	for _, prompt := range []string{
		"password data for new item:",
		"retype password for new item:",
	} {
		s = strings.ReplaceAll(s, prompt, "")
	}
	return strings.TrimSpace(s)
}

func (SystemKeyring) Delete(service, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), keyringTimeout)
	defer cancel()
	err := exec.CommandContext(ctx, "security", "delete-generic-password",
		"-s", service, "-a", name).Run()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 {
			// Already gone. The caller wanted it gone.
			return nil
		}
		if errors.Is(err, exec.ErrNotFound) {
			return ErrKeyringUnavailable
		}
		return err
	}
	return nil
}
