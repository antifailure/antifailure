// Package load generates traffic shaped like production's.
//
// The shape is the point. A load test that hammers one endpoint at a fixed
// rate proves the endpoint is fast, which nobody doubted. What breaks under
// real traffic is the mix: the page nobody thinks about that is nine percent
// of requests, the endpoint that is fine alone and holds a lock the hot path
// wants. So the traffic here is a weighted mix read from what production
// actually served, and the answer it gives is a comparison rather than a
// number.
//
// Nothing here writes. Every route is treated as unsafe until the manifest
// says otherwise, because a load generator that discovers a POST route and
// exercises it a thousand times is a load generator that charges a thousand
// cards.
package load

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"time"
)

// Route is one endpoint and how much of production's traffic it carries.
type Route struct {
	// Method is the HTTP method.
	Method string `json:"method"`
	// Path is the request path, with parameters already substituted.
	Path string `json:"path"`
	// Weight is the share of requests this route takes, relative to the
	// others. It does not have to sum to anything.
	Weight float64 `json:"weight"`
	// P95Ms is what production serves it in, when that is known. It is the
	// baseline a comparison is made against.
	P95Ms float64 `json:"p95_ms,omitempty"`
}

// String renders a route the way a report does.
func (r Route) String() string { return r.Method + " " + r.Path }

// Shape is the mix of routes and the rate they arrive at.
type Shape struct {
	// Routes are the endpoints, with their weights.
	Routes []Route `json:"routes"`
	// RequestsPerSecond is production's rate, before scaling.
	RequestsPerSecond float64 `json:"requests_per_second"`
	// Source says where this came from, so a report can say whether it is
	// real traffic or a guess.
	Source string `json:"source"`
}

// Safe returns the shape with only the routes a generator may call.
//
// Unsafe by default. A route is included only when it matches one of the safe
// patterns, because the alternative is a generator that finds POST /checkout
// in an access log and runs it four hundred times.
func (s Shape) Safe(safe, unsafe []string) (Shape, []Route) {
	out := Shape{RequestsPerSecond: s.RequestsPerSecond, Source: s.Source}
	var refused []Route
	for _, r := range s.Routes {
		if matchesAny(r, unsafe) || !matchesAny(r, safe) {
			refused = append(refused, r)
			continue
		}
		out.Routes = append(out.Routes, r)
	}
	return out, refused
}

// matchesAny reports whether a route matches one of the patterns.
//
// A pattern is a method and a path glob, as in "GET /api/*", or a bare glob,
// which matches any method. Methods are compared exactly, because a pattern
// meant to allow reads should not allow the delete that shares its path.
func matchesAny(r Route, patterns []string) bool {
	for _, p := range patterns {
		method, path, hasMethod := strings.Cut(strings.TrimSpace(p), " ")
		if !hasMethod {
			path, method = method, ""
		}
		if method != "" && !strings.EqualFold(method, r.Method) {
			continue
		}
		if globMatch(strings.TrimSpace(path), r.Path) {
			return true
		}
	}
	return false
}

// globMatch matches a path against a pattern where * covers one segment and
// ** covers the rest.
func globMatch(pattern, path string) bool {
	if pattern == "" {
		return false
	}
	if pattern == "*" || pattern == "/**" || pattern == "**" {
		return true
	}
	want := strings.Split(strings.Trim(pattern, "/"), "/")
	have := strings.Split(strings.Trim(path, "/"), "/")
	for i, seg := range want {
		if seg == "**" {
			return true
		}
		if i >= len(have) {
			return false
		}
		if seg != "*" && seg != have[i] {
			return false
		}
	}
	return len(have) == len(want)
}

// Picker chooses routes according to their weights.
//
// Seeded rather than random, so two runs of the same shape send the same
// sequence and a comparison between them is a comparison of the application
// rather than of two different traffic mixes.
type Picker struct {
	routes []Route
	// cumulative holds the running total of weights, so a choice is a binary
	// search rather than a scan. It matters at ten thousand requests a second.
	cumulative []float64
	total      float64
	rng        *rand.Rand
}

// NewPicker returns a picker over a shape.
func NewPicker(routes []Route, seed int64) (*Picker, error) {
	if len(routes) == 0 {
		return nil, fmt.Errorf("load: there are no routes to send")
	}
	// Sorted, so the same set of routes in a different order produces the same
	// sequence. Without it a shape read from two sources that agree would
	// still generate differently.
	sorted := append([]Route(nil), routes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].String() < sorted[j].String() })

	p := &Picker{routes: sorted, rng: rand.New(rand.NewSource(seed))}
	for _, r := range sorted {
		w := r.Weight
		if w <= 0 {
			// A route with no weight is still traffic, just the least of it.
			// Dropping it would silently remove an endpoint somebody listed.
			w = 0.0001
		}
		p.total += w
		p.cumulative = append(p.cumulative, p.total)
	}
	return p, nil
}

// Next returns the next route to send.
func (p *Picker) Next() Route {
	target := p.rng.Float64() * p.total
	i := sort.SearchFloat64s(p.cumulative, target)
	if i >= len(p.routes) {
		i = len(p.routes) - 1
	}
	return p.routes[i]
}

// Routes returns the routes in the order the picker holds them.
func (p *Picker) Routes() []Route { return p.routes }

// Interval is how long to wait between requests for a target rate.
//
// Poisson rather than uniform, because real traffic arrives in bursts and a
// perfectly even stream never fills a connection pool. A system that survives
// even traffic and falls over on bursty traffic passes a uniform test and
// fails in production, which is precisely the failure this exists to catch.
func (p *Picker) Interval(perSecond float64) time.Duration {
	if perSecond <= 0 {
		return 0
	}
	mean := 1.0 / perSecond
	// The exponential distribution, sampled by inversion. Clamped away from
	// zero so a draw of exactly one does not produce an infinite wait.
	u := math.Max(p.rng.Float64(), 1e-9)
	return time.Duration(-math.Log(u) * mean * float64(time.Second))
}

// DefaultShape is what a project with no traffic source gets.
//
// A guess, and it says so. It exercises the root and a health path, which
// every application has, at a rate low enough to be pointless as a load test
// and useful as a smoke test. The report says the source was a default so
// nobody mistakes it for production's shape.
func DefaultShape() Shape {
	return Shape{
		Source:            "default",
		RequestsPerSecond: 5,
		Routes: []Route{
			{Method: "GET", Path: "/", Weight: 10},
		},
	}
}

// FromAccessLog reads a shape out of a combined format access log.
//
// The commonest source by far, because every reverse proxy writes one and
// nobody has to install anything. Paths are normalised so that /users/4821 and
// /users/9130 count as one route rather than as two with a weight of one each,
// which would produce a shape with ten thousand routes and no signal in it.
func FromAccessLog(lines []string) Shape {
	counts := map[string]int{}
	methods := map[string]string{}
	paths := map[string]string{}
	total := 0
	var first, last time.Time

	for _, line := range lines {
		method, path, ok := parseAccessLine(line)
		if !ok {
			continue
		}
		normalised := NormalisePath(path)
		key := method + " " + normalised
		counts[key]++
		methods[key] = method
		paths[key] = normalised
		total++

		if at, ok := parseAccessTime(line); ok {
			if first.IsZero() || at.Before(first) {
				first = at
			}
			if at.After(last) {
				last = at
			}
		}
	}

	shape := Shape{Source: "access_log", RequestsPerSecond: accessRate(total, first, last)}
	for key, n := range counts {
		shape.Routes = append(shape.Routes, Route{
			Method: methods[key], Path: paths[key], Weight: float64(n),
		})
	}
	sort.Slice(shape.Routes, func(i, j int) bool {
		if shape.Routes[i].Weight != shape.Routes[j].Weight {
			return shape.Routes[i].Weight > shape.Routes[j].Weight
		}
		return shape.Routes[i].String() < shape.Routes[j].String()
	})
	return shape
}

// accessRate is the arrival rate the log observed.
//
// Before this the rate was assumed, and the assumption reached the report as
// production's rate. A combined format line carries a timestamp, so the rate
// can be counted rather than guessed, and a log with no readable timestamps
// returns zero so the caller can say the rate is unknown instead of inventing
// one. The window is clamped at a second for the same reason the trace
// reader's is: a log covering one second must not report its whole contents
// as a per second rate multiplied by nothing.
func accessRate(total int, first, last time.Time) float64 {
	if total == 0 || first.IsZero() || !last.After(first) {
		return 0
	}
	window := last.Sub(first)
	if window < time.Second {
		window = time.Second
	}
	return float64(total) / window.Seconds()
}

// accessTimeLayout is the combined format's timestamp, as every reverse proxy
// writes it.
const accessTimeLayout = "02/Jan/2006:15:04:05 -0700"

// parseAccessTime pulls the timestamp out of a combined format line.
func parseAccessTime(line string) (time.Time, bool) {
	start := strings.IndexByte(line, '[')
	if start < 0 {
		return time.Time{}, false
	}
	end := strings.IndexByte(line[start:], ']')
	if end < 0 {
		return time.Time{}, false
	}
	at, err := time.Parse(accessTimeLayout, line[start+1:start+end])
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// parseAccessLine pulls the method and path out of a combined format line.
func parseAccessLine(line string) (string, string, bool) {
	start := strings.Index(line, `"`)
	if start < 0 {
		return "", "", false
	}
	end := strings.Index(line[start+1:], `"`)
	if end < 0 {
		return "", "", false
	}
	fields := strings.Fields(line[start+1 : start+1+end])
	if len(fields) < 2 {
		return "", "", false
	}
	method, path := fields[0], fields[1]
	if !strings.HasPrefix(path, "/") {
		return "", "", false
	}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		// The query is dropped. Keeping it would make every search a route of
		// its own, and the shape would be a list of what people searched for.
		path = path[:i]
	}
	return strings.ToUpper(method), path, true
}

// NormalisePath collapses identifiers into a placeholder.
//
// Without it a log of a hundred thousand requests produces a hundred thousand
// routes, each with a weight of one, and the mix that was the whole point is
// gone.
func NormalisePath(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if looksLikeIdentifier(seg) {
			segments[i] = "{id}"
		}
	}
	return strings.Join(segments, "/")
}

func looksLikeIdentifier(seg string) bool {
	if seg == "" {
		return false
	}
	digits, hex, other := 0, 0, 0
	for _, r := range seg {
		switch {
		case r >= '0' && r <= '9':
			digits++
			hex++
		case (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F'):
			hex++
		case r == '-':
			hex++
		default:
			other++
		}
	}
	switch {
	case other == 0 && digits == len(seg):
		return true // a plain number
	case len(seg) >= 16 && hex == len(seg):
		return true // a uuid or a hash
	case len(seg) >= 20 && other <= len(seg)/4:
		return true // an opaque token
	}
	return false
}
