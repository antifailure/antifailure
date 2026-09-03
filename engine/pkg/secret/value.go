// Package secret holds the type that carries a credential without printing it.
//
// It is public, and it is public for one reason: engine/pkg/provider names
// this type in the signatures a provider has to implement. Database.ConnString
// returns one, GoldenSpec.Load and GoldenSpec.Verify take one, and EnvSpec
// carries several. A provider is meant to be written outside this repository,
// so every type its interfaces name has to be one an outside package can name.
//
// This type lived in engine/internal/secrets until 1.0.0, and the effect was
// that the interface could not be implemented at all from outside the module.
// Naming the return type of ConnString needed the internal import, and the Go
// toolchain refuses that import by path, so an out of module provider failed to
// compile on a line the author had no way to write differently. The promise
// that providers are an integration surface was not true, and nothing said so.
// tools/surfacecheck is the thing that says so now.
//
// A Value carries a secret string but renders as "[redacted]" through every
// path the standard library uses to turn a value into text: fmt verbs, the
// Stringer interface, JSON and YAML marshalling, and slog attributes. Reading
// the plaintext requires calling Reveal, which is easy to grep for and easy to
// review. The rest of the engine passes Value around freely without any call
// site needing to remember to redact.
package secret

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
)

// Redacted is what a Value renders as anywhere text is produced.
const Redacted = "[redacted]"

// Value is a secret string that does not print itself.
//
// The zero Value is empty and safe to use. Value is comparable only through
// Equal, which is constant time.
type Value struct {
	// v is unexported and never embedded in a struct that gets marshalled
	// directly, so the only way to observe it is Reveal.
	v string
	// source records where the value came from, for audit events. It names a
	// source such as "keyring" or "env" and never contains the secret.
	source string
}

// New returns a Value holding s.
func New(s string) Value { return Value{v: s} }

// NewFrom returns a Value holding s, tagged with the source that produced it.
// The source appears in audit events; the value never does.
func NewFrom(s, source string) Value { return Value{v: s, source: source} }

// Reveal returns the plaintext.
//
// Every call site is a place a secret can escape. Keep them few, keep them
// close to the boundary that needs the plaintext (a provider client, a
// subprocess environment, a connection string), and never pass the result on.
func (v Value) Reveal() string { return v.v }

// Source reports where the value was loaded from, or the empty string when it
// was not tagged.
func (v Value) Source() string { return v.source }

// IsZero reports whether the Value holds no secret.
func (v Value) IsZero() bool { return v.v == "" }

// Len reports the length of the secret in bytes. It is safe to log: a length
// is not a secret and it helps diagnose a truncated credential.
func (v Value) Len() int { return len(v.v) }

// Equal reports whether two values hold the same secret, in constant time so
// that comparing does not leak the contents through timing.
func (v Value) Equal(other Value) bool {
	return subtle.ConstantTimeCompare([]byte(v.v), []byte(other.v)) == 1
}

// Fingerprint returns the first eight hex characters of the SHA-256 of the
// secret, or the empty string for a zero Value.
//
// It exists so that an operator can tell whether the credential in Key Vault
// changed without ever printing it. Eight hex characters is thirty-two bits:
// enough to notice a rotation, far too little to attack the preimage.
func (v Value) Fingerprint() string {
	if v.v == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(v.v))
	return hex.EncodeToString(sum[:])[:8]
}

// String renders the redaction marker.
func (v Value) String() string { return Redacted }

// GoString renders the redaction marker, so %#v is safe too.
func (v Value) GoString() string { return Redacted }

// Format renders the redaction marker for every fmt verb.
//
// Implementing fmt.Formatter rather than only fmt.Stringer matters: with only
// a Stringer, %x hex encodes the marker and %d prints a badly formatted verb
// notice. Neither leaks, but neither is readable either. Taking over Format
// means the marker appears verbatim no matter what verb a call site reaches
// for, including a wrong one.
func (v Value) Format(f fmt.State, verb rune) {
	if verb == 'q' {
		_, _ = io.WriteString(f, `"`+Redacted+`"`)
		return
	}
	_, _ = io.WriteString(f, Redacted)
}

// MarshalJSON renders the redaction marker as a JSON string.
func (v Value) MarshalJSON() ([]byte, error) {
	return []byte(`"` + Redacted + `"`), nil
}

// UnmarshalJSON reads a secret from JSON. Round tripping a marshalled Value
// therefore yields the marker rather than the secret, which is intended: a
// secret must be loaded from a secret source, never from a document that has
// already been through a marshaller.
func (v *Value) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	v.v = s
	return nil
}

// MarshalYAML renders the redaction marker.
func (v Value) MarshalYAML() (any, error) { return Redacted, nil }

// LogValue renders the redaction marker for log/slog.
func (v Value) LogValue() slog.Value { return slog.StringValue(Redacted) }
