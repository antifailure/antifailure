// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg

// A golden's metadata, carried in Azure resource tags.
//
// The same problem cloudsql/labels.go solves and a much smaller solution,
// because Azure's limits are looser than Google's in exactly the way that
// matters. An Azure tag value takes arbitrary text up to 256 characters and a
// resource may carry 50 tags; a Cloud SQL label value takes 63 characters of
// lower case letters, digits, hyphens and underscores. So this file chunks for
// LENGTH and does not encode for charset, which is what aurora does with RDS
// tags, and the base32 encoding that cloudsql needs would be pure noise here.
//
// The reason to say that out loud rather than quietly write different code: the
// three providers look like they should share a helper, and they should not.
// The constraint each is written against is a different one, and a shared
// helper would have to satisfy the tightest of them everywhere, which would
// make every Azure and RDS tag unreadable in the portal for a limitation
// neither service has.
//
// A value too long to fit is REFUSED rather than truncated, for the reason the
// Cloud SQL file gives: a truncated attestation is a golden that lists as
// attested and carries a digest that verifies nothing.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The tag names carrying a golden's metadata.
const (
	rulesTagPrefix       = "antifailure-rules"
	provenanceTagPrefix  = "antifailure-provenance"
	attestationTagPrefix = "antifailure-attestation"
)

// tagValueMax is Azure's limit on one tag value.
const tagValueMax = 256

// maxTagChunks bounds how many numbered tags one value may use.
//
// Twelve rather than the full 50, because three values share the budget and
// this provider already spends tags on ownership and on the environment.
const maxTagChunks = 12

// chunkTag splits a value across numbered tag names.
func chunkTag(prefix, raw string) (map[string]string, error) {
	if raw == "" {
		return map[string]string{}, nil
	}
	if !utf8.ValidString(raw) {
		return nil, fmt.Errorf("azurepg: tag metadata is not valid UTF-8")
	}
	var chunks []string
	for rest := raw; rest != ""; {
		end := min(len(rest), tagValueMax)
		for end < len(rest) && !utf8.RuneStart(rest[end]) {
			end--
		}
		chunks = append(chunks, rest[:end])
		rest = rest[end:]
	}
	needed := len(chunks)
	if needed > maxTagChunks {
		return nil, fmt.Errorf(
			"azurepg: a golden's %s is %d bytes, which needs %d Azure tags and this "+
				"provider allows %d. Azure limits a tag value to %d characters and a "+
				"resource to 50 tags. It is refused rather than truncated, because a "+
				"truncated attestation is a golden that lists as attested and carries a "+
				"digest that verifies nothing", prefix, len(raw), needed, maxTagChunks, tagValueMax)
	}
	out := make(map[string]string, needed)
	for i, chunk := range chunks {
		out[prefix+"-"+strconv.Itoa(i)] = chunk
	}
	return out, nil
}

// unchunkTag reassembles what chunkTag split.
//
// It stops at the first gap rather than skipping it: a missing middle chunk
// means the value is incomplete, and joining what is left would produce a
// shorter string that looks plausible and is wrong.
func unchunkTag(prefix string, tags map[string]string) string {
	var keys []int
	for k := range tags {
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
			return ""
		}
		b.WriteString(tags[prefix+"-"+strconv.Itoa(n)])
	}
	return b.String()
}

// goldenMetadata is what a golden carries besides its version.
type goldenMetadata struct {
	RulesHash   string
	Provenance  string
	Attestation string
}

func encodeMetadata(m goldenMetadata) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range []struct{ prefix, value string }{
		{rulesTagPrefix, m.RulesHash},
		{provenanceTagPrefix, m.Provenance},
		{attestationTagPrefix, m.Attestation},
	} {
		chunks, err := chunkTag(pair.prefix, pair.value)
		if err != nil {
			return nil, err
		}
		for k, v := range chunks {
			out[k] = v
		}
	}
	return out, nil
}

func decodeMetadata(tags map[string]string) goldenMetadata {
	return goldenMetadata{
		RulesHash:   unchunkTag(rulesTagPrefix, tags),
		Provenance:  unchunkTag(provenanceTagPrefix, tags),
		Attestation: unchunkTag(attestationTagPrefix, tags),
	}
}
