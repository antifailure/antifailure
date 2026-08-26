package redact

import "strings"

// The same rule as in fake_test.go, for the in-package tests: credential
// shaped strings are assembled at run time so that nothing in the source
// matches a scanner's pattern and no reviewer has to decide whether a literal
// is real.
func fakeKeyIn(prefix string, bodyLen int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var b strings.Builder
	b.Grow(len(prefix) + bodyLen)
	b.WriteString(prefix)
	for i := 0; i < bodyLen; i++ {
		b.WriteByte(alphabet[(i*7+3)%len(alphabet)])
	}
	return b.String()
}

var (
	stripeSecretLiveIn = "sk" + "_" + "live" + "_"
	stripePublicLiveIn = "pk" + "_" + "live" + "_"
)
