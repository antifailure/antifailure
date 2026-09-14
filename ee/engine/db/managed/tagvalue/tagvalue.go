// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

// Package tagvalue is the one encoding of free text into AWS tag values that the
// Aurora and RDS providers share, and the refusal AWS gives for a value outside
// its character set.
//
// A live run against real RDS on 2026-09-13 masked and verified a candidate,
// and then the golden's CreateDBSnapshot was refused with InvalidParameterValue
// and the words in Refusal. The attestation a provider stores in tags is a
// document the engine's scanner writes, and a JSON document has braces, quotes
// and commas, which a tag value does not allow. Both providers' fakes had
// accepted any value, so no test could see it.
//
// Free text is stored as padded standard base64 (RFC 4648 section 4), whose
// alphabet sits inside the allowed set, and always encoded, whether or not the
// plain value would have fitted, so a reader never has to guess which form a
// value is in. It exists once, here, so the two providers cannot drift: a
// golden's tags read the same whichever provider wrote them.
//
// An attestation is written as its chunks, a count of them and a Digest of the
// text before encoding, and read back only through Assemble. Every full chunk
// is Limit characters, a multiple of four, and padded base64 cut at any
// multiple of four decodes without error to a prefix of the original. So a
// lost chunk, a shortened one or a rewritten one used to join into a shorter
// attestation that decoded cleanly and read as verified. The count names a
// missing chunk and the digest names any other change, each with its own
// sentence, and a golden whose attestation fails either reads as unverified.
//
// It imports only the standard library, so either provider can import it
// without an import cycle through the managed registration.
package tagvalue

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Limit is how many characters AWS allows in one tag value.
const Limit = 256

// Refusal is AWS's message for a tag value outside its character set, word for
// word as a live CreateDBSnapshot returned it.
const Refusal = "Tag values may only contain unicode letters, digits, whitespace, or one of these symbols: _ . : / = + - @"

// ErrNoChunks is what Join answers when there is nothing to join.
var ErrNoChunks = errors.New("it carries no attestation tags")

// ErrUndecodable is what Decode and Join answer for a value this encoding did
// not produce.
var ErrUndecodable = errors.New("its attestation tags do not decode as the base64 this provider writes, so this provider did not write them")

// ErrNoCount is what Assemble answers when there are attestation tags and the
// count of chunks is absent, or is not a count this provider writes.
var ErrNoCount = errors.New("its attestation tags do not record a chunk count this provider writes")

// ErrNoDigest is what Assemble answers when every chunk is there and the digest
// to check them against is not.
var ErrNoDigest = errors.New("its attestation tags do not record a digest to check it against")

// ErrDigestMismatch is what Assemble answers when the chunks decode to text
// whose Digest is not the one recorded.
var ErrDigestMismatch = errors.New("its attestation does not match the digest this provider recorded")

// IncompleteError is what Assemble answers when the recorded count names chunks
// that are not all there.
type IncompleteError struct {
	Present, Recorded int
}

func (e IncompleteError) Error() string {
	return fmt.Sprintf("its attestation tags are incomplete: %d of %d chunks are present", e.Present, e.Recorded)
}

// Valid reports whether every character of a value is one AWS allows in a tag
// value: a unicode letter, digit or space, or one of _ . : / = + - @.
func Valid(value string) bool {
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsSpace(r) && !strings.ContainsRune("_.:/=+-@", r) {
			return false
		}
	}
	return true
}

// Encode stores free text inside the characters a tag value allows.
func Encode(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

// Decode reads back what Encode stored, or answers ErrUndecodable.
func Decode(value string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", ErrUndecodable
	}
	return string(raw), nil
}

// Chunk encodes a whole value and splits the encoded text into pieces of at most
// Limit characters, in order. An empty value is no chunks.
//
// It REFUSES rather than truncating when the encoded value needs more than
// maxChunks pieces, and the bound is on the encoded length, which is what the
// tags hold. provider prefixes the sentence, so a refusal names who refused.
func Chunk(provider, value string, maxChunks int) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	encoded := Encode(value)
	if len(encoded) > Limit*maxChunks {
		return nil, fmt.Errorf(
			"%s: the verification attestation is %d characters, %d once encoded for tag values, "+
				"and does not fit in the %d tags of %d characters this provider reserves for it. It is "+
				"refused rather than truncated: half an attestation still reads as verified",
			provider, len(value), len(encoded), maxChunks, Limit)
	}
	var out []string
	for encoded != "" {
		n := min(Limit, len(encoded))
		out = append(out, encoded[:n])
		encoded = encoded[n:]
	}
	return out, nil
}

// Join concatenates chunks in order and decodes them. No chunks is ErrNoChunks,
// and chunks that do not decode are ErrUndecodable.
func Join(chunks []string) (string, error) {
	if len(chunks) == 0 {
		return "", ErrNoChunks
	}
	return Decode(strings.Join(chunks, ""))
}

// Digest is the lowercase hex sha256 of an attestation before it is encoded.
func Digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Assemble checks and joins an attestation stored as Chunk's chunks, a count of
// them and a Digest of the text.
//
// count and digest are the stored tag values, empty when a tag is absent.
// chunk answers the i-th chunk, counting from zero, and whether its tag exists;
// the provider maps i to its own key spelling. The first check that fails
// decides the answer, in this order: no attestation tags at all, a count this
// provider does not write, a missing chunk, a missing digest, chunks that do
// not decode, and a digest that does not match. Chunks past the count are not
// read.
func Assemble(count, digest string, maxChunks int, chunk func(i int) (string, bool)) (string, error) {
	if _, ok := chunk(0); !ok && count == "" && digest == "" {
		return "", ErrNoChunks
	}
	// Chunks with no count, and a count that is not strictly the decimal Itoa
	// writes, are refused here: "+3" and "03" are refused, and so is a count
	// past the bound, so a forged one cannot make the gathering below run long.
	n, err := strconv.Atoi(count)
	if err != nil || n < 1 || n > maxChunks || strconv.Itoa(n) != count {
		return "", ErrNoCount
	}
	chunks := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if c, ok := chunk(i); ok {
			chunks = append(chunks, c)
		}
	}
	if len(chunks) < n {
		return "", IncompleteError{Present: len(chunks), Recorded: n}
	}
	if digest == "" {
		return "", ErrNoDigest
	}
	plain, err := Join(chunks)
	if err != nil {
		return "", err
	}
	if Digest(plain) != digest {
		return "", ErrDigestMismatch
	}
	return plain, nil
}
