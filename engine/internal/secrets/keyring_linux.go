//go:build linux

package secrets

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// SystemKeyring is the freedesktop Secret Service, reached through secret-tool.
//
// Through the command rather than through D-Bus directly, and that is a
// decision worth defending rather than an expedient.
//
// The Secret Service is a D-Bus API, and speaking it means an external D-Bus
// library, SASL authentication against the session bus, the Session/OpenSession
// negotiation, and the transfer-encryption dance that exists so a secret does
// not cross the bus in the clear. That is a few thousand lines of somebody
// else's code inside the one package in this engine that holds plaintext
// credentials, added to a dependency set the module deliberately keeps small
// and auditable. secret-tool is the reference client for the same API, ships in
// libsecret-tools on every distribution that has a keyring at all, and speaks
// the protocol correctly by construction.
//
// It is also the same shape as the macOS implementation, which reaches the
// keychain through the security command for the same reason: this binary is
// built CGO_ENABLED=0 so that it runs on a distroless image and on a
// distribution whose libc is older than the builder's, and the native paths on
// both platforms need cgo.
//
// Where secret-tool is absent, newSystemKeyring returns nil and the chain
// reports "not configured" and skips it. That reads better than a source that
// is present and broken, and it is honest: a machine with no libsecret is
// usually a machine with no keyring daemon either.
//
// The attributes are service and account, matching the macOS -s and -a, so the
// two platforms agree about what an entry is called.
type SystemKeyring struct{ tool string }

func newSystemKeyring() Keyring {
	// Resolved once, here, rather than per call. A machine either has the tool
	// or does not, and probing three times per variable is three processes per
	// variable.
	tool, err := exec.LookPath("secret-tool")
	if err != nil {
		return nil
	}
	return SystemKeyring{tool: tool}
}

func (k SystemKeyring) Get(service, name string) (string, error) {
	cmd := exec.Command(k.tool, "lookup", "service", service, "account", name)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return "", err
		}
		// secret-tool exits 1 both for "no such item" and for "the bus is not
		// there", and the exit code alone cannot tell them apart. Standard
		// error can: a miss is silent, and every real failure says something.
		//
		// Getting this the wrong way round matters in one specific direction.
		// Reporting a broken keyring as a miss would fall through to nothing
		// and produce "the variable is not set" for a machine whose keyring is
		// merely locked, which is the message that sends somebody looking in
		// the wrong four places. Reporting a miss as a failure would stop the
		// chain dead on the very common case of a name that simply is not
		// stored. So the quiet case is the miss.
		if strings.TrimSpace(stderr.String()) == "" {
			return "", ErrNotFound
		}
		return "", &keyringError{tool: "secret-tool", detail: firstLine(stderr.String())}
	}

	// Deliberately not trimmed. secret-tool writes the secret and nothing else
	// when standard output is not a terminal, unlike the macOS security
	// command, which always appends a newline and has to have it removed. A
	// value that genuinely ends in a newline survives here and there is a test
	// that proves it, because a private key read out of a file and stored
	// verbatim is exactly that value.
	return stdout.String(), nil
}

func (k SystemKeyring) Set(service, name, value string) error {
	// The label is what a person sees in a keyring browser, so it says which
	// tool put it there and what it is for.
	cmd := exec.Command(k.tool, "store",
		"--label=antifailure: "+service+"/"+name,
		"service", service, "account", name)
	// Through standard input, not through an argument. An argument is visible
	// in the process list to every other user on the machine for as long as the
	// process runs. secret-tool is built to read it this way; the macOS
	// security command is not, which is why that implementation carries a
	// warning this one does not need.
	cmd.Stdin = strings.NewReader(value)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return &keyringError{tool: "secret-tool", detail: firstLine(stderr.String())}
		}
		return err
	}
	// A store of the same attributes replaces rather than adding a second item,
	// which is what 'af secret set' needs: without it the name could only ever
	// be written once and a lookup afterwards would return whichever of the two
	// the daemon reached first.
	return nil
}

func (k SystemKeyring) Delete(service, name string) error {
	cmd := exec.Command(k.tool, "clear", "service", service, "account", name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return err
		}
		// Clearing something already absent succeeds, for the same reason every
		// teardown in this product does: the caller wanted it gone and it is
		// gone. secret-tool is silent in that case.
		if strings.TrimSpace(stderr.String()) == "" {
			return nil
		}
		return &keyringError{tool: "secret-tool", detail: firstLine(stderr.String())}
	}
	return nil
}

// keyringError carries what the tool said.
//
// The text reaches a user: KeyringSource caches it as the reason the source was
// unavailable, and AF-SEC-001 prints it beside the source's name. "Cannot
// autolaunch D-Bus without X11 $DISPLAY" is a sentence somebody can act on, and
// it only helps if it survives the trip.
type keyringError struct {
	tool   string
	detail string
}

func (e *keyringError) Error() string {
	if e.detail == "" {
		return e.tool + " failed"
	}
	return e.detail
}

// Unwrap reports the error as an unavailable keyring, so that a caller which
// only wants to know whether the store can be used at all can ask with
// errors.Is rather than by reading the message.
func (e *keyringError) Unwrap() error { return ErrKeyringUnavailable }

// firstLine keeps a multi-line diagnostic to the sentence worth printing.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
