//go:build windows

package env

import (
	"errors"

	"golang.org/x/sys/windows"
)

// symlinkNotPermitted reports the refusal Windows gives an unprivileged
// process that asks for a symbolic link: creating one needs Developer Mode or
// an elevated token, and most machines have neither.
func symlinkNotPermitted(err error) bool {
	return errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD)
}
