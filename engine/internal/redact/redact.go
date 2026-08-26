// Package redact removes credentials from anything the engine writes.
//
// The design rule is that redaction happens at the writer, not at the call
// site. A log line, an event, an artifact, and a screenshot caption all pass
// through the same Redactor on their way out, so forgetting to redact at one
// of the hundreds of places that produce text cannot leak anything. That is
// the difference between a control and a convention.
//
// Two kinds of rule run over every byte:
//
//   - Pattern rules match credential shapes that are recognisable without
//     knowing the value: provider key prefixes, PEM blocks, bearer tokens,
//     JSON Web Token shapes, and passwords inside connection strings.
//   - Exact rules match values the engine actually loaded. The secrets
//     subsystem registers each one the moment it is read, in plain, base64,
//     and percent encoded forms, because a value that survives a round trip
//     through an HTTP client or a container environment often arrives encoded.
//
// The redactor never logs what it redacted. Reporting "redacted a Stripe live
// key" tells an attacker reading the log which line was interesting.
package redact

import (
	"bufio"
	"encoding/base64"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Marker replaces a matched secret.
const Marker = "[redacted]"

// minExactLength is the shortest value that may be registered as an exact
// secret. Below this, a real credential is indistinguishable from a common
// substring, and registering "abc" would turn every log line containing "abc"
// into noise. Values shorter than this are ignored by Register, which returns
// false so that the caller can decide whether that is a problem.
const minExactLength = 12

// maxLineLength bounds the work done on any single line. A pathological line,
// for example a base64 encoded image on one line, is processed up to this
// length and then truncated with a marker, so that a hostile input cannot turn
// logging into a denial of service.
const maxLineLength = 1 << 20 // 1 MiB

// carryLimit is how many bytes the streaming redactor holds back across a read
// boundary so that a secret split across two reads is still matched. It is
// sized for a PEM block header plus a generous key line.
const carryLimit = 4096

// Truncated is appended when a line exceeds maxLineLength.
const Truncated = "[truncated]"

// maxPasses bounds how many times the rule set is applied to one line. Each
// pass runs only when the previous one changed the line, and the marker
// matches no rule, so real inputs converge in one or two passes. The cap
// exists so that a custom rule whose replacement re-triggers itself degrades
// into a slightly slower line rather than a hung process.
const maxPasses = 8

// Rule is one named pattern.
type Rule struct {
	// Name identifies the rule for tests and documentation. It is never
	// written to output.
	Name string
	// Pattern matches the credential shape.
	Pattern *regexp.Regexp
	// Group is the submatch index to replace. Zero replaces the whole match.
	// A rule that must preserve surrounding context, for example the user and
	// host of a connection string, replaces only the group holding the secret.
	Group int
	// Require is a set of lowercase ASCII literals, any one of which must
	// appear in the line before the pattern is run.
	//
	// This is a prefilter, not a matcher. Redaction runs on every log line and
	// every event the engine emits, and the proxy emits one per request, so
	// the cost of the no-match case is the cost that matters. A substring
	// search is roughly two orders of magnitude cheaper than a regexp scan, so
	// checking "does this line contain the four bytes akia" before running the
	// AWS pattern turns twenty scans of an ordinary line into zero.
	//
	// An empty Require means the pattern always runs.
	Require []string
}

// DefaultRules returns the built in pattern rules.
//
// The list is deliberately conservative about what it treats as a credential
// shape. A rule that also matches a UUID, a Git object hash, or a base64
// encoded image would redact so much ordinary output that operators would turn
// redaction off, which is a far worse outcome than a missed exotic format. The
// exact value rules are what catch the credentials no pattern anticipates.
func DefaultRules() []Rule {
	return []Rule{
		// Private key material, including the body, which spans lines.
		{Name: "pem-block", Require: []string{"-----begin"}, Pattern: regexp.MustCompile(
			`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)},
		{Name: "pem-header-only", Require: []string{"-----begin"}, Pattern: regexp.MustCompile(
			`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)},

		// Stripe. Live and test, secret, restricted, and publishable.
		{Name: "stripe-key", Require: []string{"sk_live_", "sk_test_", "rk_live_", "rk_test_", "pk_live_", "pk_test_"},
			Pattern: regexp.MustCompile(`\b(?:sk|rk|pk)_(?:live|test)_[A-Za-z0-9]{10,247}`)},
		{Name: "stripe-webhook-secret", Require: []string{"whsec_"},
			Pattern: regexp.MustCompile(`\bwhsec_[A-Za-z0-9]{16,}`)},

		// GitHub personal access, OAuth, app, refresh, and server tokens.
		{Name: "github-token", Require: []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_"},
			Pattern: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}`)},
		{Name: "github-fine-grained", Require: []string{"github_pat_"},
			Pattern: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`)},

		// Amazon Web Services.
		{Name: "aws-access-key-id", Require: []string{"akia", "asia", "abia", "acca"},
			Pattern: regexp.MustCompile(`\b(?:AKIA|ASIA|ABIA|ACCA)[0-9A-Z]{16}\b`)},

		// Other providers with a recognisable prefix.
		{Name: "slack-token", Require: []string{"xox"},
			Pattern: regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`)},
		{Name: "sendgrid-key", Require: []string{"sg."},
			Pattern: regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}`)},
		{Name: "anthropic-key", Require: []string{"sk-ant-"},
			Pattern: regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
		{Name: "openai-key", Require: []string{"sk-"},
			Pattern: regexp.MustCompile(`\bsk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{20,}`)},
		{Name: "google-api-key", Require: []string{"aiza"},
			Pattern: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
		{Name: "twilio-sid", Require: []string{"ac"},
			Pattern: regexp.MustCompile(`\bAC[0-9a-fA-F]{32}\b`)},
		{Name: "supabase-service-key", Require: []string{"sbp_"},
			Pattern: regexp.MustCompile(`\bsbp_[0-9a-f]{40,}`)},
		{Name: "neon-key", Require: []string{"napi_"},
			Pattern: regexp.MustCompile(`\bnapi_[a-z0-9]{20,}`)},
		{Name: "npm-token", Require: []string{"npm_"},
			Pattern: regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`)},
		{Name: "doppler-token", Require: []string{"dp.pt.", "dp.st.", "dp.sa.", "dp.ct."},
			Pattern: regexp.MustCompile(`\bdp\.(?:pt|st|sa|ct)\.[A-Za-z0-9]{20,}`)},

		// JSON Web Tokens. Three base64url segments with a JSON header start.
		{Name: "jwt", Require: []string{"eyj"}, Pattern: regexp.MustCompile(
			`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},

		// Authorization headers of every scheme, and the common API key headers.
		// Group 1 is the credential; the scheme is kept so the log still says
		// what kind of authentication was attempted.
		{Name: "authorization-header", Group: 1, Require: []string{"authorization"},
			Pattern: regexp.MustCompile(
				`(?i)\bauthorization\s*[:=]\s*(?:"|')?(?:bearer|basic|token|digest)\s+([^\s"',;]{8,})`)},
		{Name: "api-key-header", Group: 1,
			Require: []string{"api-key", "api_key", "apikey", "auth-token", "auth_token", "private-token", "private_token"},
			Pattern: regexp.MustCompile(
				`(?i)\b(?:x-api-key|x-auth-token|api[_-]?key|private[_-]?token)\s*[:=]\s*(?:"|')?([^\s"',;]{8,})`)},

		// Passwords inside connection strings. Group 1 is the password only, so
		// the scheme, user, host, and database stay readable, which is what an
		// operator actually needs from the line.
		{Name: "url-password", Group: 1, Require: []string{"://"}, Pattern: regexp.MustCompile(
			`\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]+:([^\s@/]+)@`)},
		{Name: "libpq-password", Group: 1, Require: []string{"password"}, Pattern: regexp.MustCompile(
			`(?i)\bpassword\s*=\s*([^\s;'"]{4,})`)},

		// Generic assignment of something named like a secret. Group 1 is the
		// value.
		//
		// The key half allows word character neighbours on both sides, because
		// the names that actually appear are access_token, refreshToken,
		// STRIPE_SECRET_KEY, and db_password, not the bare word. A \b anchored
		// pattern misses every one of them, since an underscore is a word
		// character and so there is no boundary inside access_token.
		//
		// The value half is bounded to twelve or more credential characters,
		// which is what keeps prose out: "password policy = strict" has a six
		// character value and is left alone, and "the password is required"
		// has no assignment at all.
		{Name: "secret-assignment", Group: 1,
			Require: []string{"secret", "passwd", "password", "token", "apikey", "api_key", "api-key",
				"access_key", "access-key", "private_key", "private-key", "credential", "client_id", "client-id"},
			Pattern: regexp.MustCompile(
				`(?i)[A-Za-z0-9_.-]*(?:secret|passwd|password|token|apikey|api[_-]key|` +
					`access[_-]key|private[_-]key|credential|client[_-]id)[A-Za-z0-9_.-]*` +
					`["']?\s*[:=]\s*["']?([A-Za-z0-9/+_.=~-]{12,})`)},
	}
}

// Redactor applies pattern and exact rules to text.
//
// A Redactor is safe for concurrent use. Registering a secret while other
// goroutines are redacting is expected: the secrets subsystem registers as it
// loads, which happens while the engine is already logging.
type Redactor struct {
	rules []Rule
	// ruleFilter matches every Require literal of every rule in one pass. The
	// literal at index i belongs to the rule at ruleOfLit[i].
	ruleFilter *matcher
	ruleOfLit  []int
	// alwaysRun holds the indexes of rules that declare no Require literal.
	alwaysRun []int

	// exact holds the registered secrets and the matcher that finds them, as
	// one immutable value behind one pointer.
	//
	// The two must be swapped together. Holding them in two atomics would let
	// a reader pair a freshly stored matcher, whose literal indexes refer to
	// the new slice, with the previous slice, and index past its end. Keeping
	// them in a single struct makes that impossible by construction rather
	// than by a bounds check that hides the race.
	//
	// The value is copy on write: Register builds a whole new exactSet and
	// stores it, so a reader never observes one being sorted or appended to.
	// Redaction runs on every line the engine writes, so the read path takes
	// no lock at all.
	exact atomic.Pointer[exactSet]

	mu sync.Mutex
	// seen deduplicates registrations, which happen repeatedly as the same
	// credential is resolved for several services.
	seen map[string]struct{}
}

// exactSet pairs registered secret values with the matcher built over them.
// The two are always swapped together; see Redactor.exact.
type exactSet struct {
	// values are the registered secrets, longest first, so that a longer
	// secret containing a shorter one is replaced whole rather than leaving a
	// fragment behind.
	values []string
	// filter finds which of values appear in a line, in one pass.
	filter *matcher
}

// New returns a Redactor with the default pattern rules.
func New() *Redactor {
	return NewWithRules(DefaultRules())
}

// NewWithRules returns a Redactor with the given pattern rules. Tests use it
// to isolate one rule; production uses New.
func NewWithRules(rules []Rule) *Redactor {
	r := &Redactor{rules: rules, seen: make(map[string]struct{})}
	var lits []string
	for i, rule := range rules {
		if len(rule.Require) == 0 {
			r.alwaysRun = append(r.alwaysRun, i)
			continue
		}
		for _, l := range rule.Require {
			lits = append(lits, l)
			r.ruleOfLit = append(r.ruleOfLit, i)
		}
	}
	r.ruleFilter = newMatcher(lits, true)
	r.exact.Store(&exactSet{filter: newMatcher(nil, false)})
	return r
}

// Register adds a secret value to be replaced wherever it appears, in plain,
// base64 standard, base64 URL, and percent encoded forms.
//
// It reports whether the value was long enough to register. A shorter value is
// ignored rather than registered, because a short exact match produces false
// positives at a rate that makes output useless.
func (r *Redactor) Register(secret string) bool {
	if len(secret) < minExactLength {
		return false
	}
	forms := []string{
		secret,
		base64.StdEncoding.EncodeToString([]byte(secret)),
		base64.RawStdEncoding.EncodeToString([]byte(secret)),
		base64.URLEncoding.EncodeToString([]byte(secret)),
		base64.RawURLEncoding.EncodeToString([]byte(secret)),
		url.QueryEscape(secret),
		url.PathEscape(secret),
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	added := false
	next := append([]string(nil), r.loadExact().values...)
	// Every encoding above is length preserving or longer than its input, so a
	// form derived from a value that passed the minExactLength check above
	// cannot fall below it. No second length check is needed here.
	for _, f := range forms {
		if _, ok := r.seen[f]; ok {
			continue
		}
		r.seen[f] = struct{}{}
		next = append(next, f)
		added = true
	}
	if !added {
		return true
	}
	// Longest first so that a secret that is a prefix of another does not leave
	// the remainder visible.
	sort.SliceStable(next, func(i, j int) bool {
		return len(next[i]) > len(next[j])
	})
	r.exact.Store(&exactSet{values: next, filter: newMatcher(next, false)})
	return true
}

// loadExact returns the current exact set, which callers must treat as read
// only. It never returns nil, so callers need no guard.
func (r *Redactor) loadExact() *exactSet {
	if p := r.exact.Load(); p != nil {
		return p
	}
	// Reached only by a Redactor built as a zero value rather than through a
	// constructor. Returning an empty set keeps that usable instead of panicking.
	return emptyExactSet
}

var emptyExactSet = &exactSet{filter: newMatcher(nil, false)}

// RegisteredCount reports how many exact forms are registered. It exists for
// tests and for the doctor command; it never reveals the values.
func (r *Redactor) RegisteredCount() int {
	return len(r.loadExact().values)
}

// String returns s with every match replaced by the marker.
//
// Redaction is idempotent: applying it to already redacted text changes
// nothing, because the marker matches no rule.
func (r *Redactor) String(s string) string {
	if len(s) > maxLineLength {
		s = s[:maxLineLength] + Truncated
	}

	// Exact values first. A registered value may sit inside a larger structure
	// that a pattern rule would otherwise replace only partly.
	//
	// The filter answers "which registered secrets are in this line" in one
	// pass, so a line with none costs a single scan instead of one scan per
	// registered form. That is the case for almost every line.
	if es := r.loadExact(); !es.filter.empty {
		for _, idx := range es.filter.matchSet(s) {
			s = strings.ReplaceAll(s, es.values[idx], Marker)
		}
	}

	for _, i := range r.alwaysRun {
		s = applyRule(r.rules[i], s)
	}
	if r.ruleFilter == nil || r.ruleFilter.empty {
		return s
	}
	// One pass finds every rule whose prefilter literal is present. A line that
	// looks nothing like a credential runs zero regexps.
	//
	// The pass repeats while the line keeps changing, because a replacement
	// can expose a credential that a previous rule's window straddled. It
	// converges because the marker matches no rule, and maxPasses is a hard
	// stop so that no pattern set, including one a user adds, can turn a log
	// line into an unbounded loop.
	for pass := 0; pass < maxPasses; pass++ {
		hits := r.ruleFilter.matchSet(s)
		if len(hits) == 0 {
			return s
		}
		changed := false
		lastRule := -1
		for _, li := range hits {
			ri := r.ruleOfLit[li]
			if ri == lastRule {
				continue // several literals of the same rule matched
			}
			lastRule = ri
			out := applyRule(r.rules[ri], s)
			if out != s {
				s = out
				changed = true
			}
		}
		if !changed {
			return s
		}
	}
	return s
}

// Bytes returns b with every match replaced by the marker. The input is not
// modified.
func (r *Redactor) Bytes(b []byte) []byte {
	return []byte(r.String(string(b)))
}

func applyRule(rule Rule, s string) string {
	if rule.Group == 0 {
		return rule.Pattern.ReplaceAllString(s, Marker)
	}
	// Replace only the credential group, preserving the surrounding context,
	// by rebuilding from submatch indexes. ReplaceAllStringFunc alone cannot
	// do this because it does not expose group positions.
	locs := rule.Pattern.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for _, loc := range locs {
		gi := 2 * rule.Group
		if gi+1 >= len(loc) || loc[gi] < 0 {
			continue
		}
		b.WriteString(s[last:loc[gi]])
		b.WriteString(Marker)
		last = loc[gi+1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// Writer wraps w so that everything written to it is redacted.
//
// It buffers across write boundaries so that a secret split by an unlucky
// flush is still matched: up to carryLimit bytes with no newline are held back
// until either a newline arrives or the carry is full. Close flushes the
// remainder, and callers that produce output must call it.
func (r *Redactor) Writer(w io.Writer) io.WriteCloser {
	return &redactWriter{r: r, w: w}
}

type redactWriter struct {
	r     *Redactor
	w     io.Writer
	mu    sync.Mutex
	carry []byte
}

func (rw *redactWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	n := len(p)
	rw.carry = append(rw.carry, p...)
	for {
		i := indexByte(rw.carry, '\n')
		if i < 0 {
			break
		}
		line := rw.carry[:i+1]
		rw.carry = rw.carry[i+1:]
		if _, err := rw.w.Write(rw.r.Bytes(line)); err != nil {
			return n, err
		}
	}
	// A carry that has grown past the limit with no newline is flushed so that
	// an endless line cannot grow memory without bound. The tail that is held
	// back keeps a secret straddling the flush point matchable.
	if len(rw.carry) > carryLimit {
		cut := len(rw.carry) - carryLimit/2
		out := rw.carry[:cut]
		rw.carry = append([]byte(nil), rw.carry[cut:]...)
		if _, err := rw.w.Write(rw.r.Bytes(out)); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (rw *redactWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if len(rw.carry) == 0 {
		return nil
	}
	out := rw.r.Bytes(rw.carry)
	rw.carry = nil
	_, err := rw.w.Write(out)
	return err
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// Stream copies src to dst, redacting line by line.
//
// It is what the artifact scrubber and the subprocess log collector use. A
// line longer than maxLineLength is truncated rather than buffered whole.
func (r *Redactor) Stream(dst io.Writer, src io.Reader) error {
	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineLength)
	for sc.Scan() {
		if _, err := dst.Write(r.Bytes(sc.Bytes())); err != nil {
			return err
		}
		if _, err := dst.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	return sc.Err()
}
