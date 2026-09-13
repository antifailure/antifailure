package oracle

import (
	"math"
	"strings"
	"unicode"
)

// digestHint says which ignore entries would quiet a changed row whose
// differing columns hold a salted secret or a chain digest, or nothing.
//
// THE FAILURE. A password hash under a random salt, and an audit chain's hash
// over a timestamp, differ on every build: each side computes its own, and no
// normaliser can tell a new salt from a real change. This repository's own
// manifest reported both as major findings on an identical build, and a
// customer seeing the same has no way to learn from the report that the answer
// is a line in oracle.ignore.fields rather than a regression.
//
// So the finding is kept, always: a column that looks like a digest can still
// be one the change broke, and suppressing it would be the oracle deciding
// that for the reader. What is added is the sentence naming the exact entry,
// and it is added only when both halves of the evidence are there. The column
// is named like a digest, with hash, salt, digest or mac as a word of its name,
// and both values look like one: the same length, an encoding a digest is
// written in, and the character spread a random value has. A name that changed
// from one word to another is not a digest whatever its column is called, and
// a random looking value in a column called external_ref is not called one.
func digestHint(base, cand map[string]any, changed []string) string {
	var cols []string
	for _, col := range changed {
		if !namedLikeDigest(col) {
			continue
		}
		b, bok := base[col].(string)
		c, cok := cand[col].(string)
		if bok && cok && randomEncodingPair(b, c) {
			cols = append(cols, col)
		}
	}
	if len(cols) == 0 {
		return ""
	}
	entries := make([]string, len(cols))
	for i, col := range cols {
		entries[i] = "`$." + col + "`"
	}
	subject := strings.Join(cols, " and ") + " holds values"
	if len(cols) > 1 {
		subject = strings.Join(cols, " and ") + " hold values"
	}
	return subject + " shaped like a salted secret or a chain digest, which each side " +
		"computes for itself and which then differ on every build, the same change or not. " +
		"If that is what they are, add " + strings.Join(entries, " and ") +
		" to oracle.ignore.fields. The entry applies to that column in every table and to " +
		"that field in every response, so confirm nothing else by that name matters first."
}

// namedLikeDigest reports whether one of the words in a column's name is hash,
// salt, digest or mac, split on anything that is not a letter or digit and on
// a lower case letter followed by an upper case one.
func namedLikeDigest(col string) bool {
	var words []string
	var word []rune
	prev := rune(0)
	flush := func() {
		if len(word) > 0 {
			words = append(words, strings.ToLower(string(word)))
			word = word[:0]
		}
	}
	for _, r := range col {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && unicode.IsLower(prev):
			flush()
			word = append(word, r)
		default:
			word = append(word, r)
		}
		prev = r
	}
	flush()
	for _, w := range words {
		switch w {
		case "hash", "salt", "digest", "mac":
			return true
		}
	}
	return false
}

// minDigestChars is the shortest encoded value treated as a digest. Sixteen
// hexadecimal characters is a 64 bit value, shorter than any salt or hash a
// person would store, and it keeps short codes and flags out.
const minDigestChars = 16

// minBitsPerChar is the character spread, in bits, a random encoding clears
// and a word does not. Random hexadecimal carries close to 4 bits a character
// and random base64 close to 6; English text sits near 4 only over long
// passages and short repeated values sit near 0.
const minBitsPerChar = 3.0

// randomEncodingPair reports whether two different values both look like a
// random value in the same encoding at the same length.
func randomEncodingPair(a, b string) bool {
	a, b = stripEncodingPrefix(a), stripEncodingPrefix(b)
	if a == b || len(a) != len(b) || len(a) < minDigestChars {
		return false
	}
	return encodedLikeDigest(a) && encodedLikeDigest(b) &&
		bitsPerChar(a) >= minBitsPerChar && bitsPerChar(b) >= minBitsPerChar
}

// stripEncodingPrefix removes the marker Postgres writes before a bytea value
// in hexadecimal and the one people write before a hexadecimal literal.
func stripEncodingPrefix(s string) string {
	for _, p := range []string{`\x`, "0x"} {
		if strings.HasPrefix(s, p) {
			return s[len(p):]
		}
	}
	return s
}

// encodedLikeDigest reports whether every character belongs to hexadecimal or
// to base64 in either alphabet, padding included.
func encodedLikeDigest(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r == '+', r == '/', r == '-', r == '_', r == '=':
		default:
			return false
		}
	}
	return true
}

// bitsPerChar is the Shannon entropy of a string's characters.
func bitsPerChar(s string) float64 {
	counts := map[rune]int{}
	n := 0
	for _, r := range s {
		counts[r]++
		n++
	}
	bits := 0.0
	for _, c := range counts {
		p := float64(c) / float64(n)
		bits -= p * math.Log2(p)
	}
	return bits
}
