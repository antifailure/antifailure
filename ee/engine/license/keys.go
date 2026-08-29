package license

// The public keys a build trusts, and where they come from.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Verification is offline, which means the trusted keys have to be in the
// binary. They are not in this file: the tree carries none, and a release
// stamps them in. That is not tidiness, it is the difference between a
// repository that contains a vendor's key material and one that does not, and
// between a build somebody made from source and a build the vendor signed.
//
// A build with no keys cannot verify any licence, and says so in those words
// rather than reporting every licence as tampered with. Those are entirely
// different problems and sending somebody to look for a corrupted key when
// their binary was simply built from source is a support ticket that ends in
// embarrassment.
//
// The environment variable exists for the two cases that are real rather than
// theoretical: an air-gapped installation that mints its own licences against
// its own key, and this repository's own tests, which have to be able to sign
// something and verify it without the vendor's private key existing anywhere
// near them.

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
)

// trustedKeys is stamped at release with -ldflags.
//
// The form is kid=base64,kid=base64. Plural from the start, because a build
// that trusts one key cannot rotate without invalidating every licence in the
// field, and the rotation will happen.
var trustedKeys = ""

// TrustedKeysEnv names the variable an operator can supply keys in.
const TrustedKeysEnv = "AF_LICENSE_PUBLIC_KEYS"

// LoadVerifier builds a verifier from this build's keys and the environment.
//
// Both, merged, with the environment's taking precedence on a shared
// identifier. An air-gapped installation that mints its own licences needs its
// own key trusted alongside the vendor's rather than instead of it, or
// installing one makes the other stop working.
//
// It returns the verifier and the number of keys, so that a caller can tell an
// installation with no keys from one whose licence is simply absent, and say
// the right sentence for each.
func LoadVerifier(getenv func(string) string) (*Verifier, int, error) {
	keys := map[string]ed25519.PublicKey{}

	if err := addKeys(keys, trustedKeys); err != nil {
		return nil, 0, fmt.Errorf("this build's stamped licence keys are not usable: %w", err)
	}
	if getenv != nil {
		if err := addKeys(keys, getenv(TrustedKeysEnv)); err != nil {
			return nil, 0, fmt.Errorf("%s is not usable: %w", TrustedKeysEnv, err)
		}
	}
	return NewVerifier(keys), len(keys), nil
}

func addKeys(into map[string]ed25519.PublicKey, spec string) error {
	for entry := range strings.SplitSeq(strings.TrimSpace(spec), ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		id, encoded, found := strings.Cut(entry, "=")
		if !found {
			return fmt.Errorf("an entry is not kid=key")
		}
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("an entry has no key identifier")
		}
		// Standard and raw URL encodings both accepted, because a key pasted
		// out of an email arrives in whichever one the sender's tool produced,
		// and refusing one of them is a support ticket rather than a security
		// property.
		raw, err := decodeBase64(strings.TrimSpace(encoded))
		if err != nil {
			return fmt.Errorf("the key for %s is not base64", id)
		}
		if len(raw) != ed25519.PublicKeySize {
			return fmt.Errorf("the key for %s is %d bytes and an ed25519 public key is %d",
				id, len(raw), ed25519.PublicKeySize)
		}
		into[id] = ed25519.PublicKey(raw)
	}
	return nil
}

func decodeBase64(s string) ([]byte, error) {
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}

// NoKeysMessage is what an installation with no trusted keys is told.
//
// Its own sentence, because "the licence key was signed by a key this build
// does not know" is true and useless here: it sends somebody to check their
// licence when the answer is that their binary carries no keys at all, which is
// what a build from source is and is not a fault.
const NoKeysMessage = "this build carries no licence signing keys, so no licence can be verified. " +
	"A build from source has none; use a released binary, or set " + TrustedKeysEnv +
	" for an installation that mints its own licences"
