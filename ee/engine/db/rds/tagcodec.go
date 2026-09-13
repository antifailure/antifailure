// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds

// Reading back the tags tagvalue encoded.
//
// A live run against real RDS had the golden's CreateDBSnapshot refused,
// because the attestation in its tags was a document and a tag value allows
// letters, digits, whitespace and _ . : / = + - @ only. The encoding, the
// character check and AWS's refusal live once, in ee/engine/db/managed/tagvalue,
// shared with the Aurora provider. What stays here is this provider's own key
// spelling, antifailure:attestation.N, and which of its tags hold free text:
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

// readAttestation gathers a golden's attestation chunks in numbered order,
// stopping at the first missing one, and decodes them.
//
// It answers the attestation, or the empty string and why there is none. A
// golden whose chunks do not decode was not published by this provider, and it
// reads as unverified with that reason rather than as a scan of garbage.
func readAttestation(tags map[string]string) (string, string) {
	var chunks []string
	for i := 1; i <= attestationChunks; i++ {
		chunk, ok := tags[tagAttestation+"."+strconv.Itoa(i)]
		if !ok {
			break
		}
		chunks = append(chunks, chunk)
	}
	plain, err := tagvalue.Join(chunks)
	if err != nil {
		return "", err.Error()
	}
	if plain == "" {
		return "", tagvalue.ErrNoChunks.Error()
	}
	return plain, ""
}
