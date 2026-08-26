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
// Everything here is the standard library, because this runs inside the
// sidecar, whose image is built with no module downloads.
package livekey

import (
	"strings"
)

// Finding is one live credential that was recognised.
type Finding struct {
	// Provider is who the credential belongs to, for the message.
	Provider string
	// Prefix is the marker that identified it, never the credential.
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
}

func alnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func base62(r rune) bool { return alnum(r) || r == '_' || r == '-' }

func hexish(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
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
			if !hasTail(text[i:], p.minTail, p.tail) {
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

func hasTail(s string, min int, ok func(rune) bool) bool {
	n := 0
	for _, r := range s {
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
