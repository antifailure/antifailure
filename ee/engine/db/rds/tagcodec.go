// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds

// Reading back the tags tagvalue encoded.
//
// A live run against real RDS had the golden's CreateDBSnapshot refused,
// because the attestation in its tags was a document and a tag value allows
// letters, digits, whitespace and _ . : / = + - @ only. The encoding, the
// character check and AWS's refusal live once, in ee/engine/db/managed/tagvalue,
// shared with the Aurora provider. What stays here is this provider's own key
// spelling, antifailure:attestation.N with .count and .sha256 beside it, and
// which of its tags hold free text:
// the attestation, the rules hash and the provenance, always encoded.

import (
	"strconv"

	"github.com/antifailure/antifailure/ee/engine/db/managed/tagvalue"
)

// decodedTag reads a free text tag for reporting, where a value that does not
// decode reads as absent rather than as whatever bytes it holds.
func decodedTag(stored string) string {
	plain, err := tagvalue.Decode(stored)
	if err != nil {
		return ""
	}
	return plain
}

// readAttestation reads a golden's attestation back through tagvalue.Assemble:
// the count of chunks, every chunk it names under antifailure:attestation.1 and
// on, and the digest of the whole.
//
// It answers the attestation, or the empty string and why there is none. It
// used to join chunks up to the first missing one, and because padded base64
// cut at a whole chunk decodes cleanly, a lost chunk read as a shorter
// attestation that was still verified. A golden whose tags are not all there,
// do not decode, or do not match their digest now reads as unverified with the
// reason, and Branch refuses it naming that reason.
func readAttestation(tags map[string]string) (string, string) {
	plain, err := tagvalue.Assemble(tags[tagAttestationCount], tags[tagAttestationDigest], attestationChunks,
		func(i int) (string, bool) {
			chunk, ok := tags[tagAttestation+"."+strconv.Itoa(i+1)]
			return chunk, ok
		})
	if err != nil {
		return "", err.Error()
	}
	if plain == "" {
		return "", tagvalue.ErrNoChunks.Error()
	}
	return plain, ""
}
