// Package masking rewrites a copy of production so that it is safe to hand to
// a preview environment, without making it useless.
//
// Three properties define what "without making it useless" means, and every
// design decision here follows from them.
//
// Determinism. A transform is a pure function of the project key, the column's
// identity, and the input value. The same customer therefore maps to the same
// fake customer in every table and in every refresh, which is what keeps a
// foreign key joinable and a report reproducible. It is also what makes an
// interrupted run resumable: replaying a chunk produces the same output, so a
// checkpoint is enough.
//
// Format preservation. A masked value has to satisfy the constraints the real
// one did. An email column with a unique index still needs unique emails; a
// phone column with a length check still needs that length; a foreign key
// still has to resolve. A transform that produces "xxxxx" fails all three and
// turns a masked database into one the application cannot run against.
//
// Irreversibility. The key never travels with the data. Given a masked
// database and no key, there is no way back to the original, and the key lives
// in the secrets subsystem on the machine that ran the refresh.
package masking

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// KeyLen is the length of the project key and every derived subkey.
const KeyLen = 32

// Key is the project's masking key and the subkeys derived from it.
//
// Subkeys are derived per column so that knowing the output of one column
// tells an attacker nothing about another. Derivation is HKDF with the column
// identity as the info parameter, which is the standard construction for
// exactly this: one high entropy secret, many independent purpose bound keys.
type Key struct {
	master []byte
	// cache holds derived subkeys. Derivation is cheap but not free, and a
	// masking run derives the same column's key once per chunk over millions
	// of rows.
	cache map[string][]byte
}

// NewKey returns a Key from the project secret.
//
// The secret must be at least sixteen bytes. Below that the key is guessable
// by brute force, and a guessable masking key means the masking is reversible,
// which is the one thing it must never be.
func NewKey(project secrets.Value) (*Key, error) {
	raw := []byte(project.Reveal())
	if len(raw) < 16 {
		return nil, fmt.Errorf(
			"masking: the project key is %d bytes and must be at least 16; a short key makes masking reversible by brute force",
			len(raw))
	}
	// The master is a hash of the secret rather than the secret itself, so
	// that a key supplied as a passphrase is still full width and uniformly
	// distributed.
	sum := sha256.Sum256(raw)
	return &Key{master: sum[:], cache: map[string][]byte{}}, nil
}

// NewKeyFromBytes returns a Key from raw key material, for tests and for a key
// loaded from a key manager rather than a passphrase.
func NewKeyFromBytes(raw []byte) (*Key, error) {
	if len(raw) < 16 {
		return nil, fmt.Errorf("masking: key material is %d bytes and must be at least 16", len(raw))
	}
	sum := sha256.Sum256(raw)
	return &Key{master: sum[:], cache: map[string][]byte{}}, nil
}

// Column identifies a column for key derivation.
//
// The identity includes the schema and table, so the same column name in two
// tables gets different keys, and two columns that must agree can be made to
// agree by giving them the same identity explicitly. That is how a foreign key
// survives: both sides are masked with the same derived key, so both sides map
// the same input to the same output.
type Column struct {
	Schema string
	Table  string
	Name   string
	// Link, when set, replaces the identity for key derivation. Two columns
	// with the same Link mask identically, which is what keeps an undeclared
	// relationship joinable after masking.
	Link string
}

// String renders the identity used for derivation.
func (c Column) String() string {
	if c.Link != "" {
		return "link:" + c.Link
	}
	schema := c.Schema
	if schema == "" {
		schema = "public"
	}
	return schema + "." + c.Table + "." + c.Name
}

// Sub returns the subkey for a column, deriving it if needed.
func (k *Key) Sub(c Column) []byte {
	id := c.String()
	if v, ok := k.cache[id]; ok {
		return v
	}
	out := make([]byte, KeyLen)
	r := hkdf.New(sha256.New, k.master, nil, []byte("antifailure/masking/v1/"+id))
	if _, err := io.ReadFull(r, out); err != nil {
		// HKDF over SHA-256 cannot fail for a 32 byte output; the reader is
		// infallible for lengths under 255 hash blocks. A failure here would
		// be a corrupted runtime rather than a condition to handle.
		panic("masking: HKDF failed, which cannot happen for a 32 byte output: " + err.Error())
	}
	k.cache[id] = out
	return out
}

// Fingerprint returns a short identifier for the key, so that an attestation
// can record which key produced a golden without recording the key.
func (k *Key) Fingerprint() string {
	mac := hmac.New(sha256.New, k.master)
	mac.Write([]byte("antifailure/masking/fingerprint/v1"))
	return fmt.Sprintf("%x", mac.Sum(nil)[:8])
}

// prf returns a keyed pseudorandom value for an input, as a 64 bit unsigned
// integer. It is the primitive every deterministic transform is built on.
func prf(subkey []byte, input string) uint64 {
	mac := hmac.New(sha256.New, subkey)
	mac.Write([]byte(input))
	return binary.BigEndian.Uint64(mac.Sum(nil)[:8])
}

// prfBytes returns n keyed pseudorandom bytes for an input.
func prfBytes(subkey []byte, input string, n int) []byte {
	out := make([]byte, 0, n)
	var counter byte
	for len(out) < n {
		mac := hmac.New(sha256.New, subkey)
		mac.Write([]byte{counter})
		mac.Write([]byte(input))
		out = append(out, mac.Sum(nil)...)
		counter++
	}
	return out[:n]
}

// prfStream is a deterministic source of randomness for a single value, so
// that a transform needing several draws gets independent ones without
// re-keying.
type prfStream struct {
	subkey []byte
	input  string
	buf    []byte
	pos    int
	round  byte
}

func newPRFStream(subkey []byte, input string) *prfStream {
	return &prfStream{subkey: subkey, input: input}
}

func (s *prfStream) next() byte {
	if s.pos >= len(s.buf) {
		mac := hmac.New(sha256.New, s.subkey)
		mac.Write([]byte{s.round})
		mac.Write([]byte(s.input))
		s.buf = mac.Sum(nil)
		s.pos = 0
		s.round++
	}
	b := s.buf[s.pos]
	s.pos++
	return b
}

// intn returns a value in [0, n) without modulo bias.
//
// Modulo bias matters here even though this is not cryptography in the usual
// sense: a biased draw over a name lexicon makes some names far more common
// than others, which is exactly the kind of statistical fingerprint that lets
// someone reason about the original data.
func (s *prfStream) intn(n int) int {
	if n <= 1 {
		return 0
	}
	// The bound is computed in 64 bits because 2^32 does not fit in a uint32,
	// then narrowed. Draws at or above it are discarded, which is what removes
	// the bias.
	const span = uint64(1) << 32
	limit := uint32(span - (span % uint64(n)))
	for {
		v := uint32(s.next())<<24 | uint32(s.next())<<16 | uint32(s.next())<<8 | uint32(s.next())
		if v < limit {
			return int(v % uint32(n))
		}
	}
}
