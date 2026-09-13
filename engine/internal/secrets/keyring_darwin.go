//go:build darwin

package secrets

import (
	"bytes"
	"context"
	"encoding/hex"
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
// else. bf9f394ca moved it to security's password prompt, fed on stdin, and that
// had two defects of its own, both described on Set. The value now goes in
// security's interactive command stream, on stdin, as hex.
//
// The product had already decided argv matters. cli/model.go says in capitals
// that the key is never an argument, because a secret on a command line is in
// the shell history, is visible in ps, and is in any recording of the terminal.
// af model set refuses a --key flag and reads without echo for those reasons,
// and a promise kept at the top layer and broken underneath is worth less than
// no promise, because it is believed.
//
// Windows uses the Win32 credential API and starts no child process at all.
// keyring_linux.go gives secret-tool the value on stdin, which secret-tool reads
// when stdin is not a terminal, and here it never is.
type SystemKeyring struct{}

// keyringTimeout bounds every call to security.
//
// Without one, a keychain that blocks rather than failing has no upper bound,
// and the ErrKeyringUnavailable fallback to a file cannot fire, because a hang
// is not an error. Generous, because a locked keychain puts SecurityAgent's
// unlock dialog in front of a person, measured on a locked keychain from inside
// a terminal and from outside one, and answering it is not a fault.
const keyringTimeout = 2 * time.Minute

// maxCommandLine is the longest command security reads in interactive mode,
// its newline included.
//
// MAX_LINE_LEN is 4096 in SecurityTool's security.c, and it was measured rather
// than trusted: a 4096 byte line stores its value, a 4098 byte line is cut at
// the buffer, and the cut is not refused. The first piece runs as a command and
// stores a truncated value, the rest runs as a second command and fails, and
// security exits 1 having written the truncation.
const maxCommandLine = 4096

func newSystemKeyring() Keyring { return SystemKeyring{} }

// Get reads with -g, which prints the password on stderr, rather than with -w.
//
// -w prints a value as text when every byte is printable and as bare hex
// otherwise, so a stored "tab<TAB>x" read back as "7461620978", and nothing in
// the output says which of the two happened: the value "deadbeef" and the value
// whose bytes are de ad be ef print identically. -g marks the difference, with
// a quoted string for the first and a 0x prefix for the second.
func (SystemKeyring) Get(service, name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), keyringTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "security", "find-generic-password",
		"-s", service, "-a", name, "-g")
	// The item's attributes, which are not the secret and are not needed.
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		// 44 is "the specified item could not be found in the keychain",
		// which is a miss rather than a failure of the keychain.
		if errors.As(err, &exit) && exit.ExitCode() == 44 {
			return "", ErrNotFound
		}
		if errors.Is(err, exec.ErrNotFound) {
			return "", ErrKeyringUnavailable
		}
		return "", keychainError("read", stderr.String(), err)
	}
	return passwordFromListing(stderr.String())
}

// passwordFromListing reads the password line find-generic-password -g prints.
//
// The shapes, every one of them taken from a real listing:
//
//	password: "plain"                      every byte printable, verbatim between the quotes
//	password: 0x7461620978  "tab\011x"     otherwise hex, then a rendering to ignore
//	password:                              the empty value
//
// The quoted form does not escape a quote inside the value, so it is read as
// everything between the first and last character rather than parsed. It cannot
// hold a line break, because a line break is not printable and takes the hex
// form. Anything else is refused, because a guess here returns part of a line
// as somebody's secret.
func passwordFromListing(listing string) (string, error) {
	for _, line := range strings.Split(listing, "\n") {
		rest, ok := strings.CutPrefix(line, "password:")
		if !ok {
			continue
		}
		rest = strings.TrimPrefix(rest, " ")
		switch {
		case rest == "":
			return "", nil
		case strings.HasPrefix(rest, "0x"):
			digits, _, _ := strings.Cut(rest[len("0x"):], " ")
			value, err := hex.DecodeString(digits)
			if err != nil {
				return "", fmt.Errorf("secrets: the keychain printed a password this cannot decode: %w", err)
			}
			return string(value), nil
		case len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"':
			return rest[1 : len(rest)-1], nil
		}
		return "", errors.New("secrets: the keychain printed a password line in a shape this does not recognise")
	}
	return "", errors.New("secrets: the keychain found the entry and printed no password line for it")
}

// Set writes through `security -i`, which reads commands from stdin, and gives
// the value to add-generic-password's -X flag as hex inside that stream.
//
// What it replaced, and why each part is shaped the way it is.
//
// bf9f394ca ran `add-generic-password ... -U -w` with nothing after -w, which
// makes security prompt for the value, and fed the prompt the value twice on
// stdin. Its comment said the route was settled by running it, and it was run
// without a terminal, which is the one case where it works. security reads a
// prompted password through readpassphrase, and readpassphrase reads from the
// controlling terminal whenever the process has one. Every person running af
// login or af model set has one. So the command printed "password data for new
// item:" on their terminal, ignored the stdin it was given, and waited for them
// to type a token they had never seen, until they killed it; nothing was stored
// and, for af login, the approval they had just given was spent. go test runs
// with no terminal, so every test here passed throughout.
//
// And where it did work, it truncated. readpassphrase keeps 128 bytes and drops
// the rest without an error, so security exited 0 having stored the first 128
// bytes. The credential af login stores is JSON well past that, and an OpenAI
// project key is too; both went into the keychain cut short.
//
// The interactive stream has neither problem. It is ordinary stdin, read whether
// or not there is a terminal, measured from inside a pseudo terminal. It is not
// argv: ps shows `security -i` and nothing else. And -X takes hex, so a line
// break, a quote, a NUL or a leading dash is two hex digits like any other byte,
// and there is nothing to escape in the value at all. The one limit is the line
// buffer, enforced below before security sees the line.
//
// And every write is read back and compared before Set returns, in
// verifyStored, because an exit status of 0 is what the truncation reported.
func (k SystemKeyring) Set(service, name, value string) error {
	line, err := addCommand(service, name, value)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), keyringTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "security", "-i")
	cmd.Stdin = strings.NewReader(line)
	cmd.Stdout = io.Discard

	// Captured rather than discarded so a real failure still says why, and
	// rather than inherited so nothing from security appears in the middle of
	// whatever the command was printing. With stdin not a terminal, security
	// prints no "security>" prompt, so a successful write leaves this empty.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// security -i exits with the status of the last command it ran, and this
	// stream holds exactly one, so a refused write is a nonzero exit.
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrKeyringUnavailable
		}
		return keychainError("refused the write", stderr.String(), err)
	}
	return verifyStored(k, service, name, value)
}

// storedEntry is the part of a keyring the read back needs.
type storedEntry interface {
	Get(service, name string) (string, error)
	Delete(service, name string) error
}

// verifyStored reads what a write left and compares it byte for byte.
//
// Because the defect this file had for its whole life was a write that
// succeeded: the prompt route exited 0 having stored the first 128 bytes, af
// login said "Signed in", and the credential could never be read. An exit
// status is security's opinion of the write. What the keychain holds is the
// fact, so it is read back through the same unambiguous listing Get uses, and
// anything but the exact bytes is an error.
//
// An error rather than ErrKeyringUnavailable, so nothing falls through to the
// next store: the keychain is read before the file on every later command, so a
// wrong value left there would shadow a right one written anywhere else. The
// wrong value is deleted for the same reason. The message names the entry and
// the two lengths and never the value, because it is printed.
func verifyStored(ring storedEntry, service, name, value string) error {
	got, err := ring.Get(service, name)
	if err != nil {
		return fmt.Errorf("secrets: the macOS keychain accepted %q under %q and it could not be read back, "+
			"so the write is not trusted: %w", name, service, err)
	}
	if got != value {
		_ = ring.Delete(service, name)
		return fmt.Errorf("secrets: the macOS keychain stored something other than what was written to %q "+
			"under %q (%d bytes written, %d read back); the entry was removed rather than left holding a "+
			"wrong value", name, service, len(value), len(got))
	}
	return nil
}

// addCommand is the one line Set gives security.
//
// The names are quoted because security splits the line on white space, and
// inside a double quoted word a backslash takes the next byte literally, which
// is the whole of its quoting (split_line in security.c). A line break or a NUL
// cannot be carried in a line at all, and no name this product writes has one,
// so such a name is refused rather than mangled.
//
// An empty value goes as -w with an empty word, because -X refuses an empty
// hex string with a usage error; -w followed by an argument is not the prompt.
func addCommand(service, name, value string) (string, error) {
	for _, field := range []string{service, name} {
		if strings.ContainsAny(field, "\n\r\x00") {
			return "", fmt.Errorf("secrets: the keychain entry name %q holds a line break or a NUL, "+
				"which the macOS keychain command cannot be given: %w", field, ErrKeyringUnavailable)
		}
	}
	password := `-w ""`
	if value != "" {
		password = "-X " + hex.EncodeToString([]byte(value))
	}
	line := commandPrefix(service, name) + password + "\n"
	if len(line) > maxCommandLine {
		// Pointed at the next store rather than failed, which is what the
		// Windows implementation does with a PEM key too long for its blob
		// limit: auth.Store falls back to its file and model.Store to the
		// encrypted local store.
		return "", fmt.Errorf("secrets: this value is %d bytes, and the macOS keychain command takes "+
			"at most %d for this entry without putting it on a command line: %w",
			len(value), valueLimit(service, name), ErrKeyringUnavailable)
	}
	return line, nil
}

func commandPrefix(service, name string) string {
	return "add-generic-password -s " + quoted(service) + " -a " + quoted(name) + " -U "
}

// valueLimit is the longest value, in bytes, addCommand accepts for this entry.
func valueLimit(service, name string) int {
	fixed := len(commandPrefix(service, name)) + len("-X ") + len("\n")
	return (maxCommandLine - fixed) / 2
}

func quoted(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// keychainError keeps what security said, which is the only account of why.
func keychainError(what, stderr string, err error) error {
	if detail := strings.TrimSpace(stderr); detail != "" {
		return fmt.Errorf("secrets: the keychain %s: %s: %w", what, detail, err)
	}
	return err
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
