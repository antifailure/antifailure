//go:build darwin

package secrets

import (
	"errors"
	"os/exec"
	"strings"
)

// SystemKeyring is the macOS keychain, reached through the security command.
//
// Through the command rather than through the Security framework, because the
// framework needs cgo and this binary is built with CGO_ENABLED=0 so that it
// runs on a distroless image and on a distribution whose libc is older than the
// builder's. The command ships with the operating system.
//
// The value goes through a flag rather than stdin because security takes it
// that way, which means it is briefly visible in a process listing. That is
// worth knowing and is why writing is only done by 'af secret set' on a
// workstation, never in CI, where the passphrase comes from the environment.
type SystemKeyring struct{}

func newSystemKeyring() Keyring { return SystemKeyring{} }

func (SystemKeyring) Get(service, name string) (string, error) {
	out, err := exec.Command("security", "find-generic-password",
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
	// -U updates in place. Without it a second write for the same account
	// fails rather than replacing, and the caller has to delete first.
	cmd := exec.Command("security", "add-generic-password",
		"-s", service, "-a", name, "-w", value, "-U")
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrKeyringUnavailable
		}
		return err
	}
	return nil
}

func (SystemKeyring) Delete(service, name string) error {
	err := exec.Command("security", "delete-generic-password",
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
