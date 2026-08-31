package oracle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultIgnoredHeaders are the response headers no two runs can agree on, and
// which carry no information about the change when they disagree.
//
// Each one is here for a reason and the reasons are different, which is why
// this is a list and not a prefix rule:
//
//   - date is the clock.
//   - set-cookie is a session identifier, and a fresh session per run is
//     correct behaviour rather than a difference.
//   - etag and last-modified are derived from the body or from the clock. A
//     body that changed is already reported, so comparing them reports the
//     same fact a second time in a form nobody can read.
//   - content-length is derived from the body, same argument.
//   - the request identifier headers are a fresh value per request by
//     definition. Four spellings because four frameworks chose four.
//   - x-runtime, server, x-powered-by and age are the server describing
//     itself and how long it took, not what it decided.
//   - connection and keep-alive are the transport.
//
// Not here on purpose: cache-control, vary, location, content-type,
// content-encoding, and every application header. Those carry decisions.
var DefaultIgnoredHeaders = []string{
	"age",
	"connection",
	"content-length",
	"date",
	"etag",
	"keep-alive",
	"last-modified",
	"server",
	"set-cookie",
	"x-correlation-id",
	"x-powered-by",
	"x-request-id",
	"x-runtime",
	"x-trace-id",
}

// DefaultTimestampSkew is how far apart two timestamps may be and still be
// equal.
//
// Bounded, not unbounded, and the bound is the interesting decision. The
// obvious normaliser says "two well formed timestamps are equal", and that
// hides the bug class it most needs to catch: a change that shifts an expiry by
// a day, or a migration that rewrites every date an hour earlier, writes two
// perfectly well formed timestamps.
//
// An hour, because of what the harness itself contributes. The two probes of a
// pair are milliseconds apart, so anything the request generates agrees closely.
// The two ENVIRONMENTS are built one after the other, so a row written by the
// migrations can be minutes apart on the two sides, and on a slow build many
// minutes. An hour is comfortably above that and comfortably below any shift a
// person means. It is configurable, and the report says how large a gap it
// actually absorbed, so nobody has to take the number on trust.
const DefaultTimestampSkew = time.Hour

// DefaultFloatTolerance is the relative difference two numbers may have and
// still be equal.
//
// It is a relative tolerance rather than an absolute one because the numbers
// this compares are money in cents, latencies in milliseconds and ratios, and
// no single absolute epsilon is right for all three. 1e-9 is far below any
// difference an application means and far above the representation noise of a
// float64 that has been through a JSON round trip.
const DefaultFloatTolerance = 1e-9

// maxValue is how long a rendered value may be before it is replaced by a
// digest.
//
// A digest rather than a truncation. Two 40KB documents that differ at byte
// 39,000 compare equal if both are cut at 4KB, which is a false negative in
// exactly the case somebody needed the answer. The digest compares exactly and
// still fits in a table cell.
const maxValue = 4096

// Config is what the comparison ignores and how tolerant it is.
//
// Every field here changes what is compared, so every field is reported in
// Ignored. There is no setting that quietly changes an answer.
type Config struct {
	// IgnoreHeaders are compared header names to skip, in addition to
	// DefaultIgnoredHeaders. Case insensitive.
	IgnoreHeaders []string
	// IgnoreFields are JSON paths to skip, in the response body and in a
	// database row alike. "$.token", "$.orders[*].placed_at" and
	// "$..created_at" are all accepted.
	IgnoreFields []string
	// FloatTolerance is the relative difference two numbers may have. Zero
	// means DefaultFloatTolerance.
	FloatTolerance float64
	// TimestampSkew is how far apart two timestamps may be and still be
	// equal. Zero means DefaultTimestampSkew.
	TimestampSkew time.Duration
	// KeepTimestamps compares timestamp strings exactly instead of treating
	// two nearby timestamps as equal. It exists because an application
	// whose timestamps come from the data rather than from the clock has no
	// non-determinism to absorb, and absorbing it anyway would hide a
	// migration that shifted every date by an hour.
	KeepTimestamps bool
	// KeepUUIDs is the same argument for identifiers that are stored rather
	// than generated per request.
	KeepUUIDs bool
}

func (c Config) tolerance() float64 {
	if c.FloatTolerance <= 0 {
		return DefaultFloatTolerance
	}
	return c.FloatTolerance
}

func (c Config) skew() time.Duration {
	if c.TimestampSkew <= 0 {
		return DefaultTimestampSkew
	}
	return c.TimestampSkew
}

// ignoredHeader reports whether a header is skipped.
func (c Config) ignoredHeader(name string) bool {
	lower := strings.ToLower(name)
	for _, h := range DefaultIgnoredHeaders {
		if h == lower {
			return true
		}
	}
	for _, h := range c.IgnoreHeaders {
		if strings.ToLower(strings.TrimSpace(h)) == lower {
			return true
		}
	}
	return false
}

// Ignored is everything the comparison declined to compare, in the form the
// report prints.
//
// This type is the reason the package is usable. The list is assembled while
// comparing rather than described in documentation, so it reports what actually
// happened on this run: which normalisers fired, how often, and at which paths.
// A reader who disagrees with an omission can see it and turn it off.
type Ignored struct {
	// Headers are the response headers not compared, defaults included.
	Headers []string `json:"headers"`
	// Fields are the JSON path patterns the manifest asked to skip.
	Fields []string `json:"fields,omitempty"`
	// Normalisers counts how many value pairs each normaliser absorbed, with
	// a few example paths each.
	Normalisers []NormaliserUse `json:"normalisers,omitempty"`
	// FloatTolerance is the relative tolerance that was in force.
	FloatTolerance float64 `json:"float_tolerance"`
}

// NormaliserUse is one normaliser's activity during a run.
type NormaliserUse struct {
	Name string `json:"name"`
	// Count is how many pairs of values it made equal. A pair it did not make
	// equal is a finding and is not counted here.
	Count int `json:"count"`
	// Examples are up to three paths where it fired, so a reader can go and
	// look at one.
	Examples []string `json:"examples,omitempty"`
	// Widest is the largest gap this normaliser absorbed, for the ones that
	// have a bound. It is what turns "timestamps are normalised" from a claim
	// into a number somebody can disagree with: an absorbed gap of four
	// milliseconds is the harness, and one of fifty minutes is worth a look.
	Widest string `json:"widest,omitempty"`
}

// maxExamples is how many paths each normaliser records.
//
// Three. One is not enough to see a pattern and a hundred is a wall of text
// that says the same thing as three.
const maxExamples = 3

// Describe renders the ignore list as prose for a report.
//
// Always rendered, even when nothing was ignored, because the sentence
// "nothing was ignored" is information and its absence is not.
func (i Ignored) Describe() string {
	var b strings.Builder
	b.WriteString("Not compared: ")
	b.WriteString(plural(len(i.Headers), "response header", "response headers"))
	b.WriteString(" (")
	b.WriteString(strings.Join(i.Headers, ", "))
	b.WriteString(")")
	if len(i.Fields) > 0 {
		fmt.Fprintf(&b, ", and %s (%s)",
			plural(len(i.Fields), "field pattern", "field patterns"),
			strings.Join(i.Fields, ", "))
	}
	b.WriteString(".\n")
	if len(i.Normalisers) == 0 {
		fmt.Fprintf(&b, "No value normaliser fired. Numbers were equal within %g relative.\n",
			i.FloatTolerance)
		return b.String()
	}
	for _, n := range i.Normalisers {
		fmt.Fprintf(&b, "The %s normaliser made %s equal",
			n.Name, plural(n.Count, "value", "values"))
		if n.Widest != "" {
			fmt.Fprintf(&b, ", the widest gap %s", n.Widest)
		}
		if len(n.Examples) > 0 {
			fmt.Fprintf(&b, ", at %s", strings.Join(n.Examples, ", "))
			if n.Count > len(n.Examples) {
				b.WriteString(" and elsewhere")
			}
		}
		b.WriteString(".\n")
	}
	return b.String()
}

// collector accumulates what the normalisers absorbed while a comparison runs.
type collector struct {
	uses   map[string]*NormaliserUse
	widest map[string]time.Duration
}

func newCollector() *collector {
	return &collector{uses: map[string]*NormaliserUse{}, widest: map[string]time.Duration{}}
}

func (c *collector) record(name, path string) {
	if c == nil {
		return
	}
	u, ok := c.uses[name]
	if !ok {
		u = &NormaliserUse{Name: name}
		c.uses[name] = u
	}
	u.Count++
	if len(u.Examples) < maxExamples {
		u.Examples = append(u.Examples, path)
	}
}

// recordGap is record for a normaliser with a bound, keeping the widest gap it
// absorbed.
func (c *collector) recordGap(name, path string, gap time.Duration) {
	if c == nil {
		return
	}
	c.record(name, path)
	if gap > c.widest[name] {
		c.widest[name] = gap
	}
}

func (c *collector) result() []NormaliserUse {
	if c == nil || len(c.uses) == 0 {
		return nil
	}
	out := make([]NormaliserUse, 0, len(c.uses))
	for name, u := range c.uses {
		use := *u
		if gap, ok := c.widest[name]; ok {
			use.Widest = gap.Round(time.Millisecond).String()
		}
		out = append(out, use)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Ignored renders the configuration and what fired into the report's shape.
func (c *collector) Ignored(cfg Config) Ignored {
	headers := append([]string(nil), DefaultIgnoredHeaders...)
	for _, h := range cfg.IgnoreHeaders {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" && !containsString(headers, h) {
			headers = append(headers, h)
		}
	}
	sort.Strings(headers)
	return Ignored{
		Headers:        headers,
		Fields:         append([]string(nil), cfg.IgnoreFields...),
		Normalisers:    c.result(),
		FloatTolerance: cfg.tolerance(),
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// uuidPattern is the canonical 8-4-4-4-12 hexadecimal form, any version.
//
// Anchored and exact. A looser pattern would swallow an application
// identifier that happens to contain hyphens, and swallowing a real identifier
// is how an oracle stops reporting the thing it was installed for.
var uuidPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// timestampLayouts are the string forms this package recognises as a clock
// reading.
//
// Strings only. A number is never treated as an epoch, however much it looks
// like one, because the alternative is deciding from the name of the field it
// sits under, and a rule that reads names would silently ignore an expiry that
// moved by a day in a field called expires_at. A project whose timestamps are
// numeric ignores them by path, which is visible in the report.
//
// The list is what a Postgres timestamptz, a Go time.Time, a JavaScript
// Date.toISOString and a Rails to_json actually produce, and nothing else.
var timestampLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999-07",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

// parseTimestamp reads a string as an instant under one of the layouts.
//
// The length guard is not an optimisation. "2006" parses under no layout here,
// but a bare four digit year would under a shorter one somebody adds later,
// and a version string is not a clock. Nineteen characters is the shortest real
// form, "2006-01-02 15:04:05".
func parseTimestamp(s string) (time.Time, bool) {
	if len(s) < 19 || len(s) > 40 {
		return time.Time{}, false
	}
	for _, layout := range timestampLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// looksLikeTimestamp reports whether a string is an instant at all, without
// saying which.
func looksLikeTimestamp(s string) bool {
	_, ok := parseTimestamp(s)
	return ok
}

// normaliseScalar decides whether two scalar values are equal, and records why
// when a normaliser is what made them so.
//
// The contract that keeps this honest: a normaliser only ever makes two values
// EQUAL. It never rewrites one side into something that could then match a
// third value, and it never fires when only one side matches its shape. A
// timestamp against the string "pending" is a difference and is reported as
// one.
func normaliseScalar(cfg Config, c *collector, path string, base, cand any) (equal bool) {
	// Identical after JSON decoding, which is the common case and costs one
	// comparison.
	if sameScalar(base, cand) {
		return true
	}

	bs, bok := base.(string)
	cs, cok := cand.(string)
	if bok && cok {
		if !cfg.KeepTimestamps {
			bt, bts := parseTimestamp(bs)
			ct, cts := parseTimestamp(cs)
			// Both sides have to be instants. A timestamp against "pending" is
			// a difference, and this is the line that keeps the normaliser from
			// being a hole rather than a tolerance.
			if bts && cts {
				gap := ct.Sub(bt)
				if gap < 0 {
					gap = -gap
				}
				if gap <= cfg.skew() {
					c.recordGap("timestamp", path, gap)
					return true
				}
				// Past the skew it is reported, and the caller shows the two
				// values. Nothing is recorded here: a normaliser that did not
				// absorb anything has not ignored anything.
				return false
			}
		}
		if !cfg.KeepUUIDs && uuidPattern.MatchString(bs) && uuidPattern.MatchString(cs) {
			c.record("uuid", path)
			return true
		}
		return false
	}

	bn, bnok := asNumber(base)
	cn, cnok := asNumber(cand)
	if bnok && cnok {
		if withinTolerance(bn, cn, cfg.tolerance()) {
			// Recorded only when the two literals actually differed, which
			// sameScalar has already established. "1.0" against "1" is a
			// difference in the document and not in the value, and counting
			// it would fill the report with a normaliser doing nothing
			// interesting.
			c.record("number tolerance", path)
			return true
		}
		return false
	}

	return false
}

// sameScalar compares two decoded JSON scalars for exact equality.
func sameScalar(a, b any) bool {
	switch av := a.(type) {
	case nil:
		return b == nil
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case json.Number:
		bv, ok := b.(json.Number)
		return ok && string(av) == string(bv)
	default:
		return false
	}
}

func asNumber(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	if err != nil {
		return 0, false
	}
	return f, true
}

// withinTolerance compares two numbers relatively.
//
// The absolute fallback is for values near zero, where a relative comparison
// against a denominator of zero says nothing.
func withinTolerance(a, b, tol float64) bool {
	if a == b {
		return true
	}
	if math.IsNaN(a) || math.IsNaN(b) || math.IsInf(a, 0) || math.IsInf(b, 0) {
		return false
	}
	diff := math.Abs(a - b)
	scale := math.Max(math.Abs(a), math.Abs(b))
	if scale == 0 {
		return diff <= tol
	}
	return diff/scale <= tol
}

// render turns a decoded JSON value into the string a report shows.
//
// Long values become a digest rather than a prefix, for the reason maxValue
// explains: a prefix that matches is not evidence that the values match.
func render(v any) string {
	var s string
	switch tv := v.(type) {
	case nil:
		return "null"
	case string:
		s = strconv.Quote(tv)
	case bool:
		return strconv.FormatBool(tv)
	case json.Number:
		return string(tv)
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		s = string(body)
	}
	if len(s) <= maxValue {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%d bytes, sha256:%s", len(s), hex.EncodeToString(sum[:])[:16])
}

// typeName is the JSON type of a decoded value, for a type change finding.
func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}
