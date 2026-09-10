// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql

// A golden's metadata, carried in Cloud SQL user labels.
//
// WHY THIS FILE EXISTS AND AURORA HAS NO EQUIVALENT.
//
// A golden version has to carry its provenance, its rules hash and its
// attestation, and they have to survive this process: ListGoldens is built from
// the project rather than from memory precisely so that a golden created by
// another machine is still listed. Aurora keeps them in RDS tags, whose values
// take more or less arbitrary text, and chunks only the attestation for length.
//
// CLOUD SQL USER LABELS CANNOT HOLD ARBITRARY TEXT. Google constrains them to
// lower case letters, digits, hyphens and underscores, at most 63 characters
// each, at most 64 per resource. A provenance string is a sentence somebody
// wrote and an attestation is a digest; neither survives that charset. Writing
// them in raw would not error, which is the dangerous part: Google would refuse
// the request, or worse, a value would be silently unusable, and the golden
// would list with an empty provenance. The engine then cannot tell whose golden
// it is and REFUSES TO BRANCH IT, which is a failure a long way from its cause.
//
// So the values are base32 encoded, which lands inside the permitted charset by
// construction rather than by hoping the input behaves, and then chunked across
// numbered labels the way aurora chunks its attestation. Lower case base32
// without padding uses exactly [a-z2-7], a strict subset of what a label
// accepts.
//
// The budget is worth writing down because it is small and it is a real limit
// rather than a theoretical one: 64 labels of 63 characters is about 4 kibibytes
// of base32, so roughly 2.5 kibibytes of raw metadata, minus the labels this
// provider already uses for ownership. encodeMetadata refuses rather than
// truncating when a value does not fit, because a silently truncated
// attestation is a golden that looks attested and is not.

import (
	"encoding/base32"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The label keys carrying a golden's metadata.
const (
	rulesLabelPrefix       = "af-rules"
	provenanceLabelPrefix  = "af-prov"
	attestationLabelPrefix = "af-att"
)

// labelValueMax is Google's limit on one label value.
const labelValueMax = 63

// maxChunks bounds how many numbered labels one value may use.
//
// Sixteen rather than the full 64, because three values share the budget and
// this provider already spends labels on ownership. A value needing more than
// sixteen chunks is refused with the number it needed, which is a better answer
// than a create call failing on a limit the caller cannot see.
const maxChunks = 16

// lowerBase32 is base32 without padding, lower cased.
//
// Standard base32's alphabet is upper case, which a label rejects, so the
// encoding is lower cased on the way in and upper cased on the way out. It is
// not a custom alphabet: keeping the standard one and changing case is what
// makes this reversible with the standard decoder.
var lowerBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func encodeValue(raw string) string {
	return strings.ToLower(lowerBase32.EncodeToString([]byte(raw)))
}

func decodeValue(encoded string) (string, bool) {
	out, err := lowerBase32.DecodeString(strings.ToUpper(encoded))
	if err != nil {
		return "", false
	}
	return string(out), true
}

// chunk splits an encoded value across numbered label keys.
//
// It REFUSES rather than truncating. A truncated attestation is a golden that
// lists as attested and carries a digest that verifies nothing, which is worse
// than a golden that failed to publish.
func chunk(prefix, raw string) (map[string]string, error) {
	if raw == "" {
		return map[string]string{}, nil
	}
	encoded := encodeValue(raw)
	needed := (len(encoded) + labelValueMax - 1) / labelValueMax
	if needed > maxChunks {
		return nil, fmt.Errorf(
			"cloudsql: a golden's %s is %d bytes, which needs %d Cloud SQL labels and "+
				"this provider allows %d. Google limits a label value to %d characters "+
				"and a resource to 64 labels, so this cannot be stored on the instance. "+
				"It is refused rather than truncated, because a truncated attestation is "+
				"a golden that lists as attested and carries a digest that verifies "+
				"nothing", prefix, len(raw), needed, maxChunks, labelValueMax)
	}
	out := make(map[string]string, needed)
	for i := 0; i < needed; i++ {
		start := i * labelValueMax
		end := start + labelValueMax
		if end > len(encoded) {
			end = len(encoded)
		}
		out[prefix+"-"+strconv.Itoa(i)] = encoded[start:end]
	}
	return out, nil
}

// unchunk reassembles what chunk split.
//
// It stops at the first gap rather than skipping it. A missing middle chunk
// means the value is incomplete, and joining what is left would produce a
// shorter string that decodes to something plausible and wrong.
func unchunk(prefix string, labels map[string]string) string {
	var keys []int
	for k := range labels {
		rest, ok := strings.CutPrefix(k, prefix+"-")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(rest); err == nil {
			keys = append(keys, n)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Ints(keys)
	var b strings.Builder
	for i, n := range keys {
		if n != i {
			// A gap: the value is incomplete and joining the rest would
			// decode to something plausible and wrong.
			return ""
		}
		b.WriteString(labels[prefix+"-"+strconv.Itoa(n)])
	}
	decoded, ok := decodeValue(b.String())
	if !ok {
		return ""
	}
	return decoded
}

// goldenMetadata is what a golden carries besides its version.
type goldenMetadata struct {
	RulesHash   string
	Provenance  string
	Attestation string
}

// encodeMetadata turns a golden's metadata into labels.
func encodeMetadata(m goldenMetadata) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range []struct {
		prefix string
		value  string
	}{
		{rulesLabelPrefix, m.RulesHash},
		{provenanceLabelPrefix, m.Provenance},
		{attestationLabelPrefix, m.Attestation},
	} {
		chunks, err := chunk(pair.prefix, pair.value)
		if err != nil {
			return nil, err
		}
		for k, v := range chunks {
			out[k] = v
		}
	}
	return out, nil
}

// decodeMetadata reads a golden's metadata back out of its labels.
func decodeMetadata(labels map[string]string) goldenMetadata {
	return goldenMetadata{
		RulesHash:   unchunk(rulesLabelPrefix, labels),
		Provenance:  unchunk(provenanceLabelPrefix, labels),
		Attestation: unchunk(attestationLabelPrefix, labels),
	}
}
