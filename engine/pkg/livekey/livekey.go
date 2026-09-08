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
	"encoding/base64"
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

// pemHeaderFor opens a private key of one algorithm. Assembled for the same
// reason as the constant above, and separate from it because the algorithms
// each get their own header line and a scanner that only knew PKCS#8 would
// miss every key ssh-keygen and openssl ever wrote by default.
func pemHeaderFor(algorithm string) string {
	return "-----BEGIN " + algorithm + " PRIVATE KEY-----"
}

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

	// A private key belonging to nobody the detector can name, which is the
	// one shape this package used to answer nothing about.
	//
	// The rule above says a bare PEM is not evidence of Google, and that is
	// still right: reporting one as a service account key sends somebody to
	// rotate a credential their project does not have. What was wrong was the
	// step after it. "This is not Google's" was implemented as "this is not a
	// credential", so a 2048 bit RSA key sat in this repository's own
	// antifailure.yaml while the required check that reads this package
	// reported no credentials in the tree. A private key is a credential
	// whoever issued it; the honest finding names the shape and declines to
	// name an owner.
	//
	// These sit AFTER the service account entry and the first of them shares
	// its prefix, which is what gives Google precedence: Scan records one
	// finding per prefix, so where the corroborating marker is beside the key
	// the finding says GCP service account key, and where it is not it says
	// private key rather than nothing.
	//
	// minTail is 60 for the same reason it is 60 above. A PEM body wraps at 64
	// characters, so one line of real key material clears it, and the redacted
	// examples that fill documentation, REDACTED or an ellipsis where the body
	// goes, do not.
	{provider: "Private key", prefix: pemPrivateKeyHeader, minTail: 60,
		tail: base64ish, skip: pemGap},
	{provider: "Private key", prefix: pemHeaderFor("RSA"), minTail: 60,
		tail: base64ish, skip: pemGap},
	{provider: "Private key", prefix: pemHeaderFor("EC"), minTail: 60,
		tail: base64ish, skip: pemGap},
	{provider: "Private key", prefix: pemHeaderFor("DSA"), minTail: 60,
		tail: base64ish, skip: pemGap},
	{provider: "Private key", prefix: pemHeaderFor("OPENSSH"), minTail: 60,
		tail: base64ish, skip: pemGap},
	{provider: "Private key", prefix: pemHeaderFor("ENCRYPTED"), minTail: 60,
		tail: base64ish, skip: pemGap},

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
//
// Two passes, because one of them reads what is written and the other reads
// what was hidden. A base64 wrapped key is the same credential with the
// markers this file matches on encoded away, and it is not a hypothetical: the
// manifest in this repository carried one, in base64 rather than PEM, and the
// comment beside it said the encoding was chosen because the validator refuses
// a literal beginning with BEGIN.
func Scan(text, where string) []Finding {
	out := scanPatterns(text, where)
	seen := map[string]bool{}
	for _, f := range out {
		seen[f.Prefix] = true
	}
	for _, f := range scanEncoded(text, where) {
		if seen[f.Prefix] {
			continue
		}
		seen[f.Prefix] = true
		out = append(out, f)
	}
	return out
}

// scanPatterns is the literal pass, over the text exactly as it arrived.
func scanPatterns(text, where string) []Finding {
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

// encodedBeginMarkers are how the opening of a PEM block reads once the whole
// block has been base64 encoded.
//
// Three of them, because base64 works on groups of three bytes and the reading
// depends on where the block starts inside the group. A key encoded on its own,
// which is the common case and the one this repository shipped, is the first.
// The other two are the same header a few bytes into a larger blob.
//
// They are triggers rather than findings. What follows a hit is a decode and
// then the ordinary pass over the result, so the length and corroboration
// rules that keep prose out of the literal pass apply to the decoded text as
// well, and nothing is reported on the strength of a prefix alone.
var encodedBeginMarkers = []string{
	"LS0tLS1CRUdJ",
	"LS0tQkVHSU4g",
	"LS0tLUJFR0lO",
}

// base64Rune reports whether a character can appear inside a base64 body.
//
// Padding is included, unlike base64ish above, because here the run is being
// delimited rather than measured: the padding is part of the value that has to
// be handed to the decoder.
func base64Rune(r rune) bool { return base64ish(r) || r == '=' }

// maxEncoded bounds the run that is decoded. A private key is under three
// kilobytes encoded; this is generous and it stops a megabyte of base64 in a
// fixture from being decoded because it happens to open with the marker.
const maxEncoded = 1 << 16

// scanEncoded finds a credential that was base64 encoded before it was written.
//
// The evasion this closes was not adversarial and it is worse for that. The
// manifest in this repository held a real RSA private key as a single base64
// literal, and the comment beside it explained the encoding as the way to get
// past a validator that refuses a value beginning with BEGIN. Every instrument
// that could have objected read the encoded form and saw an opaque string, and
// the required check named "no credentials in the tree" passed over it for as
// long as it was there.
func scanEncoded(text, where string) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, marker := range encodedBeginMarkers {
		for i := 0; ; {
			idx := strings.Index(text[i:], marker)
			if idx < 0 {
				break
			}
			at := i + idx
			i = at + len(marker)

			lo, hi := base64Run(text, at)
			if hi-lo > maxEncoded {
				continue
			}
			decoded, ok := decodeRun(text[lo:hi])
			if !ok {
				continue
			}
			for _, f := range scanPatterns(decoded, where) {
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

// base64Run returns the bounds of the base64 run containing at.
func base64Run(text string, at int) (int, int) {
	lo := at
	for lo > 0 && base64Rune(rune(text[lo-1])) {
		lo--
	}
	hi := at
	for hi < len(text) && base64Rune(rune(text[hi])) {
		hi++
	}
	return lo, hi
}

// decodeRun decodes a base64 run whose start may not be the start of the
// encoding.
//
// Four attempts, because the run found by walking backwards over base64
// characters can begin up to three characters inside a group: a marker for one
// of the two offset alignments sits behind bytes that are themselves base64
// characters. Whichever attempt yields a PEM private key is the right one, and
// nothing else is accepted, so a blob that decodes to arbitrary bytes is not a
// finding.
func decodeRun(run string) (string, bool) {
	for k := 0; k < 4 && k < len(run); k++ {
		body := run[k:]
		body = body[:len(body)-len(body)%4]
		if len(body) < 4 {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			// A run that carries padding in the middle, which happens when two
			// values sit side by side, decodes up to that point. Everything
			// before the padding is still worth reading.
			if cut := strings.IndexByte(body, '='); cut > 0 {
				body = body[:cut-cut%4]
				if len(body) < 4 {
					continue
				}
				decoded, err = base64.StdEncoding.DecodeString(body)
			}
			if err != nil {
				continue
			}
		}
		text := string(decoded)
		if strings.Contains(text, "-----BEGIN") && strings.Contains(text, "PRIVATE KEY") {
			return text, true
		}
	}
	return "", false
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
