package termimg

import (
	"os"
	"time"
)

// readReply reads the terminal's answer on Windows.
//
// Split from the unix version for a reason the type system enforces rather than
// a stylistic one: syscall.SetNonblock and syscall.Read take a syscall.Handle
// here and an int everywhere else, so the one file cannot serve both and the
// package simply does not compile for this platform if it tries. That is not a
// hypothetical. It shipped in a pull request with all nine required contexts
// green, because the check that compiles for the other platforms is not one of
// the nine, and every lint run on the machine it was written on was native.
//
// The deadline path is what is left, and it is the right one here: the reason
// it is not used on unix is that the Go runtime poller refuses a character
// device on macOS, which is a macOS problem rather than a Windows one. Windows
// Terminal answers the device attributes request like any other terminal, so
// detection works; a console that answers nothing costs the query deadline once
// at startup and is then reported as an unknown capability, which is the honest
// answer rather than a guess.
func readReply(in *os.File, _ int, deadline time.Time) (string, error) {
	return readWithDeadline(in, deadline)
}
