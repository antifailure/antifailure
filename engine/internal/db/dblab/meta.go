package dblab

import (
	"encoding/json"
	"strings"
	"time"
)

// A golden version is a Database Lab Engine snapshot with a commit message
// this provider wrote and can read back.
//
// The engine stores that message as a ZFS user property on the snapshot,
// base64 encoded, and returns it on every listing. That makes it the right
// place for the facts the engine needs to recognise its own goldens: it
// survives the snapshot being cloned, it comes back on a single list call
// rather than one call per snapshot, and it belongs to the snapshot rather
// than to this machine, so a second developer pointed at the same instance
// sees the same goldens.
//
// What is deliberately NOT in it is the attestation. A ZFS property value is
// bounded, base64 costs a third again on top, and an attestation grows with
// the number of columns scanned. The attestation goes inside the golden's own
// data instead, in the _antifailure schema, which is also where a branch
// inherits it for free. See writeMeta in dblab.go.

// metaVersion is the format version written into new goldens.
const metaVersion = 1

// meta is what a golden's commit message carries.
type meta struct {
	// Antifailure is the key that says a snapshot is one of ours, and its
	// value is the format version. A snapshot whose message is a human's
	// commit note, or empty, or JSON from something else, leaves this zero and
	// is never branched: such a snapshot holds whatever the engine's retrieval
	// brought in, which is production data that nothing has masked. decodeMeta
	// is where that refusal happens.
	Antifailure int    `json:"antifailure"`
	Version     string `json:"version"`
	RulesHash   string `json:"rules_hash"`
	CreatedAt   string `json:"created_at"`
	// Verified is written true and never false. A refresh that fails
	// verification never reaches the snapshot at all, so an unverified golden
	// cannot be created by this provider. The field is here because reading it
	// is what makes Branch's refusal a check rather than an assumption: a
	// future format, or a snapshot written by a version of this code that
	// published before verifying, would be caught rather than trusted.
	Verified bool `json:"verified"`
	// AttestationSHA256 is a hex digest of the attestation stored inside the
	// data, so a caller can tell that the two belong together without
	// connecting to the golden.
	AttestationSHA256 string `json:"attestation_sha256,omitempty"`
}

// encodeMeta renders the commit message for a golden.
func encodeMeta(m meta) string {
	m.Antifailure = metaVersion
	encoded, err := json.Marshal(m)
	if err != nil {
		// Every field is a string or an int, so this cannot happen. Returning
		// something recognisable rather than panicking keeps a refresh from
		// dying on an impossibility.
		return `{"antifailure":1}`
	}
	return string(encoded)
}

// decodeMeta reads a snapshot's commit message.
//
// The second result reports whether the message is a golden this provider
// wrote. Anything else, including a message that is not JSON at all, is
// reported as not ours rather than as an error: a shared engine legitimately
// carries snapshots from its own retrieval and from people committing clones
// by hand, and failing the listing because of one of those would make
// ListGoldens return nothing on exactly the instances that are being used.
func decodeMeta(message string) (meta, bool) {
	trimmed := strings.TrimSpace(message)
	// The engine writes a literal "-" for a snapshot with no message, which is
	// how ZFS reports an unset property.
	if trimmed == "" || trimmed == "-" || !strings.HasPrefix(trimmed, "{") {
		return meta{}, false
	}
	var m meta
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		return meta{}, false
	}
	if m.Antifailure == 0 || m.Version == "" {
		return meta{}, false
	}
	return m, true
}

// createdAt reads the recorded creation time, falling back to the snapshot's
// own.
func (m meta) createdAt(fallback time.Time) time.Time {
	if m.CreatedAt == "" {
		return fallback
	}
	at, err := time.Parse(time.RFC3339, m.CreatedAt)
	if err != nil {
		return fallback
	}
	return at.UTC()
}

// IsGolden reports whether a snapshot's commit message says this provider
// wrote it.
//
// Exported so that a test can tell a golden from the engine's own snapshot
// using the rule the provider uses, rather than a second copy of it that can
// drift. The distinction matters most where the consequence is worst: the
// engine's own snapshot is the source every golden is built from, and nothing
// here may delete it.
func IsGolden(message string) bool {
	_, ours := decodeMeta(message)
	return ours
}
