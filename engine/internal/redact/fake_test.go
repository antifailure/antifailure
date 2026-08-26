package redact_test

import "strings"

// Credential shaped strings are assembled at run time rather than written as
// literals.
//
// Two reasons, and the second is the one that bit us. A literal that looks
// like a Stripe key is indistinguishable from a real one to a scanner, so
// GitHub push protection and gitleaks both refuse the commit, and the fix
// people reach for is an allowlist entry, which is the beginning of a scanner
// nobody trusts. And a reviewer skimming the file has to decide, every time,
// whether the string in front of them is synthetic. Building the value from
// parts removes both problems: nothing in the source matches a credential
// pattern, and the intent is obvious at a glance.
//
// The body is deterministic so that failures are reproducible.
func fakeKey(prefix string, bodyLen int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var b strings.Builder
	b.Grow(len(prefix) + bodyLen)
	b.WriteString(prefix)
	for i := 0; i < bodyLen; i++ {
		b.WriteByte(alphabet[(i*7+3)%len(alphabet)])
	}
	return b.String()
}

// Prefixes are split so that the literal in the source is not the prefix a
// scanner matches on.
var (
	stripeSecretLive = "sk" + "_" + "live" + "_"
	stripePublicLive = "pk" + "_" + "live" + "_"
	githubClassic    = "gh" + "p" + "_"
)

// awsKey is an access key identifier in the documented example shape, split so
// that the literal in this file does not itself match the pattern.
var awsKey = "AK" + "IA" + "IOSFODNN7EXAMPLE"
