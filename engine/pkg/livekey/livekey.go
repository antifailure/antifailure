// Package livekey recognises credentials that work against production.
//
// An environment holds a copy of production data and runs unreviewed code. The
// one thing it must never hold is a key that can act on production, because
// the whole point of the sandbox is that a mistake inside it stays inside it.
// A live Stripe key in a preview environment is a real charge on a real card.
//
// So this is not a redaction problem, it is a refusal problem: a request
// carrying one of these is stopped and reported, rather than forwarded with
// the key hidden in the logs. Redaction protects the logs; this protects the
// customer.
//
// The distinction it draws is between live and test, not between secret and
// not. sk_test_ is a secret and belongs in an environment; sk_live_ is a
// secret and does not. A detector that could not tell them apart would refuse
// every sandbox request and be turned off within a day.
//
// It is a published package rather than an internal one for two reasons: the
// repository scan in CI has to use the same detector the proxy uses, or the
// two disagree about what a credential is, and anybody writing a provider has
// the same question to answer.
//
// Everything here is the standard library, because this also runs inside the
// sidecar, whose image is built with no module downloads.
package livekey

import (
	"strings"
)

// Finding is one live credential that was recognised.
type Finding struct {
	// Provider is who the credential belongs to, for the message.
	Provider string
	// Prefix is the marker that identified it, never the credential. For most
	// providers it is the credential's own prefix. For the two Azure shapes
	// that have no prefix of their own it is the field name that carried it,
	// which is still a marker and still not the value.
	Prefix string
	// Where says which part of the request carried it: a header name, or the
	// body.
	Where string
}

// String renders a finding for a message, and deliberately never carries the
// credential itself. A refusal that echoed the key back would put it in the
// logs of the thing refusing it.
func (f Finding) String() string {
	return f.Provider + " (" + f.Prefix + ") in " + f.Where
}

// pattern is one credential shape.
type pattern struct {
	provider string
	prefix   string
	// minTail is how many characters must follow the prefix. It exists to
	// keep prose from matching: the word "akia" in a sentence is not a key,
	// and refusing a request because somebody wrote about one would make this
	// the first thing a user disables.
	minTail int
	// tail says what those characters may be.
	tail func(rune) bool
	// skip passes over characters between the prefix and the tail. Only one
	// shape needs it and it is the reason it exists: a GCP service account key
	// is a PEM block, so the base64 body starts after a line break, and in the
	// JSON file the same break is written as an escape rather than a newline.
	// Without this the tail would be measured as zero characters long and the
	// most valuable credential Google issues would be the one shape that never
	// matched.
	skip func(rune) bool
	// also are markers that must appear near the match for it to count. A
	// shape with no prefix of its own is only identifiable in context, and
	// naming the wrong provider is worse than naming none: a bare TLS key is
	// not a Google credential and reporting it as one sends somebody to rotate
	// the wrong thing.
	also []string
	// unless are markers whose presence near the match means the credential is
	// a published emulator or test one. This is the live versus test rule
	// applied to a provider that draws no distinction in the credential
	// itself: Azurite's development account key is live SHAPED and is exactly
	// what a sandbox is supposed to hold, and it is told apart by the account
	// name sitting beside it in the same connection string.
	unless []string
}

// nearby bounds how far from a match the also and unless markers are looked
// for. A whole file would be the wrong unit in both directions: a marker at
// the other end of a large file does not corroborate a key, and one down there
// must not excuse it either. 4096 is sized for a GCP service account key,
// whose client_email follows a private key body of about 1700 characters, with
// room to spare.
const nearby = 4096

func alnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func base62(r rune) bool { return alnum(r) || r == '_' || r == '-' }

func hexish(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// base64ish is the standard base64 alphabet without the padding. Azure emits
// its keys this way and so does the body of a PEM block. The padding is left
// out on purpose: it only ever appears at the end, so counting it would say
// nothing about length, and a tail that stops at the first padding character
// measures the key rather than the key plus its terminator.
func base64ish(r rune) bool { return alnum(r) || r == '+' || r == '/' }

// tokenish is what a Google OAuth access token is made of. The dot is the
// reason it is separate from base62: real tokens carry several.
func tokenish(r rune) bool { return base62(r) || r == '.' }

// secretish is the character set Microsoft documents for an Entra client
// secret: letters, digits, dashes, underscores, dots and tildes.
func secretish(r rune) bool { return base62(r) || r == '.' || r == '~' }

// pemGap is what separates a PEM header from the base64 that follows it. The
// backslash is in here because a service account key is JSON, where the line
// break inside the private key is written as two characters rather than one.
func pemGap(r rune) bool {
	return r == '\n' || r == '\r' || r == ' ' || r == '\t' || r == '\\'
}

// pemPrivateKeyHeader opens the key material inside a GCP service account
// file. It is assembled rather than written out for the same reason the
// package's tests assemble their fixtures: this file is itself scanned by the
// repository check that imports this package, and a literal here that matched
// one of these patterns would fail that check on the detector's own source.
const pemPrivateKeyHeader = "-----BEGIN " + "PRIVATE KEY-----"

// patterns are live credentials only.
//
// Every entry here has a test mode counterpart that is deliberately absent:
// sk_test_, pk_test_, rk_test_, and the sandbox keys of the others are exactly
// what an environment is supposed to carry.
var patterns = []pattern{
	{provider: "Stripe secret key", prefix: "sk_live_", minTail: 16, tail: base62},
	{provider: "Stripe restricted key", prefix: "rk_live_", minTail: 16, tail: base62},
	{provider: "Stripe publishable key", prefix: "pk_live_", minTail: 16, tail: base62},
	{provider: "GitHub personal token", prefix: "ghp_", minTail: 30, tail: base62},
	{provider: "GitHub app token", prefix: "ghs_", minTail: 30, tail: base62},
	{provider: "GitHub fine grained token", prefix: "github_pat_", minTail: 30, tail: base62},
	{provider: "AWS access key", prefix: "AKIA", minTail: 16, tail: func(r rune) bool {
		return (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}},
	{provider: "Slack bot token", prefix: "xoxb-", minTail: 20, tail: base62},
	{provider: "Slack user token", prefix: "xoxp-", minTail: 20, tail: base62},
	{provider: "SendGrid key", prefix: "SG.", minTail: 30, tail: base62},
	{provider: "Anthropic key", prefix: "sk-ant-api", minTail: 20, tail: base62},
	{provider: "OpenAI project key", prefix: "sk-proj-", minTail: 20, tail: base62},
	{provider: "Supabase service key", prefix: "sbp_", minTail: 30, tail: hexish},
	{provider: "Neon key", prefix: "napi_", minTail: 20, tail: base62},
	{provider: "npm token", prefix: "npm_", minTail: 30, tail: base62},
	{provider: "Twilio account", prefix: "AC", minTail: 32, tail: hexish},
	{provider: "Postmark server token", prefix: "POSTMARK_API_TEST", minTail: 0, tail: base62},

	// Google Cloud. Three shapes, and the third is not a prefix at all.
	//
	// None of the three has a test counterpart, which is the same situation as
	// the AWS key above rather than a departure from the package's rule: where
	// a provider issues no sandbox credential, every credential it issues acts
	// on production. Google's sandbox is a separate emulator reached by an
	// endpoint variable, and an emulator wants no credential, so a request
	// that carries one of these is talking to the real project.
	//
	// The API key is 39 characters, the prefix and 35 more, which is Google's
	// own documented shape and the same one the redactor already carries.
	{provider: "Google API key", prefix: "AIza", minTail: 35, tail: base62},

	// An access token minted against a real Google project, usually by
	// exchanging a service account key. Real ones run past a hundred
	// characters; 30 is well under that and still far past anything a sentence
	// about the prefix could reach.
	{provider: "Google OAuth access token", prefix: "ya29.", minTail: 30, tail: tokenish},

	// The service account key, which is the one that matters most and the one
	// with nothing to match on its own. It is a JSON file whose private_key
	// field holds a PEM block, so the credential is recognised by two facts
	// together: real key material, and the marker that says whose it is.
	//
	// 60 is what makes it able to say no. A PEM body is wrapped at 64
	// characters, so one line of a real key clears it, and every redacted
	// example in every piece of documentation, which writes REDACTED or an
	// ellipsis where the body goes, does not.
	{provider: "GCP service account key", prefix: pemPrivateKeyHeader, minTail: 60,
		tail: base64ish, skip: pemGap, also: []string{"gserviceaccount.com"}},

	// Azure. Three shapes and none of them has a prefix of its own, because
	// Azure keys are plain base64 and are told apart by the field that carries
	// them. The field name is the marker, and it is what the finding reports.
	//
	// The storage account key is 88 characters: 86 of base64 and two of
	// padding. Requiring all 86 is what keeps AccountKey= in a sentence about
	// connection strings from being a finding.
	//
	// devstoreaccount1 is Azurite's development account and its key is
	// published in Microsoft's own documentation. It is live shaped and it is
	// the credential a sandbox is SUPPOSED to hold, so the account name beside
	// it in the connection string is what tells the two apart. Refusing it
	// would refuse the emulator this product runs Azure against, which is the
	// exact failure the package doc describes.
	{provider: "Azure storage account key", prefix: "AccountKey=", minTail: 86, tail: base64ish,
		unless: []string{"devstoreaccount1", "UseDevelopmentStorage=true"}},

	// Service Bus, Event Hubs, IoT Hub and Relay all sign with a shared access
	// key in a connection string. The key is 32 bytes of base64, so 44
	// characters with padding; 40 clears every one of them and no prose.
	{provider: "Azure shared access key", prefix: "SharedAccessKey=", minTail: 40, tail: base64ish},

	// The Entra client secret. Microsoft documents the value as up to 40
	// characters of letters, digits, dashes, underscores, dots and tildes,
	// which on its own would match most base64 in the world. So it is matched
	// only where the field naming it is Azure's own spelling: AppSecret in a
	// connection string, which is the shape Microsoft's own high confidence
	// example uses, and AZURE_CLIENT_SECRET, which is what the Azure SDKs read
	// from the environment.
	//
	// A bare client_secret is deliberately absent. Every OAuth provider in
	// existence uses that name, and a finding that said Azure about a GitHub
	// app's secret would send somebody to rotate a credential that does not
	// exist.
	{provider: "Azure client secret", prefix: "AppSecret=", minTail: 30, tail: secretish},
	{provider: "Azure client secret", prefix: "AZURE_CLIENT_SECRET=", minTail: 30, tail: secretish},
}

// Scan reports every live credential in a piece of text.
//
// Case sensitive on purpose. Every prefix here is emitted in a fixed case by
// the provider that issues it, and folding case turns "AC" into a match for
// the word "ac" in a URL.
func Scan(text, where string) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, p := range patterns {
		// Postmark's test token is the one entry that is a literal rather
		// than a prefix, and it means the opposite: its presence is proof the
		// caller is in test mode, so it is never a finding.
		if p.provider == "Postmark server token" {
			continue
		}
		for i := 0; ; {
			idx := strings.Index(text[i:], p.prefix)
			if idx < 0 {
				break
			}
			at := i + idx
			i = at + len(p.prefix)
			if !hasTail(text[i:], p.minTail, p.tail, p.skip) {
				continue
			}
			// A shape that needs corroboration gets none by default, so a
			// missing marker is a miss rather than a match. An excusing marker
			// works the other way round and is checked second, because it has
			// to be able to overrule a match that already looks real.
			if len(p.also) > 0 && !near(text, at, p.also) {
				continue
			}
			if near(text, at, p.unless) {
				continue
			}
			if seen[p.prefix] {
				continue
			}
			seen[p.prefix] = true
			out = append(out, Finding{Provider: p.provider, Prefix: p.prefix, Where: where})
			break
		}
	}
	return out
}

func hasTail(s string, min int, ok func(rune) bool, skip func(rune) bool) bool {
	n := 0
	for _, r := range s {
		// Only leading characters are skipped. A gap in the middle would let
		// two short runs of base64 either side of a comment add up to a key
		// that is not there.
		if n == 0 && skip != nil && skip(r) {
			continue
		}
		if !ok(r) {
			break
		}
		n++
		if n >= min {
			return true
		}
	}
	return n >= min
}

// near reports whether any marker appears within nearby characters of a match.
//
// Slicing by byte can cut a multi byte character in half at either edge. That
// is harmless here and worth saying so nobody fixes it into something slower:
// every marker is ASCII, and a broken character at the boundary cannot spell
// one.
func near(text string, at int, markers []string) bool {
	if len(markers) == 0 {
		return false
	}
	lo := at - nearby
	if lo < 0 {
		lo = 0
	}
	hi := at + nearby
	if hi > len(text) {
		hi = len(text)
	}
	window := text[lo:hi]
	for _, m := range markers {
		if strings.Contains(window, m) {
			return true
		}
	}
	return false
}

// ScanHeaders looks through a header map, naming the header that carried it.
//
// Headers are where credentials actually travel, and naming the one that
// carried it is the difference between a user fixing it in a minute and
// hunting for it.
func ScanHeaders(headers map[string][]string) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for name, values := range headers {
		for _, v := range values {
			for _, f := range Scan(v, "the "+name+" header") {
				if seen[f.Prefix] {
					continue
				}
				seen[f.Prefix] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// Describe renders findings for a message.
func Describe(findings []Finding) string {
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		parts = append(parts, f.String())
	}
	return strings.Join(parts, ", ")
}
