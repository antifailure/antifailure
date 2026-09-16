package cli

import (
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// observedRoutes derives the ingress routes the run actually REACHED from the
// browser exploration, so the injection family fuzzes routes the run observed
// rather than routes a manifest merely declared. It is the producer whose
// absence left security.Input.Routes wired to nil and the whole injection
// family dormant: it reported blocked and fuzzed nothing.
//
// WHERE THE ROUTES COME FROM, AND WHY THAT IS HONEST. The runner records every
// page the browser stood on (report.Exploration.Results[].Visited), every
// navigation it made (the goto moves in Journey), and every request it actually
// reached, with its method (report.Exploration.Results[].Requests). Those are
// URLs and requests the run genuinely reached, observed rather than assumed. It
// is deliberately NOT the manifest's declared workflows or probes: a declared
// route is a claim about what should be reachable, and the injection family's
// contract is to fuzz what the run OBSERVED reaching. Visited and the goto moves
// are GET page loads, so a route sourced from them is a GET. A recorded request
// carries its own method, so a form POST or a fetch/XHR the page made is fuzzed
// as the POST it was: the runner now emits every reached request, which is where
// most real SQL injection lives, and emitting a method we did not observe is the
// declared-versus-observed mistake this producer still avoids.
//
// THE VALUE BOUNDARY. A Route is a location and never a value, the same rule the
// rest of the security layer holds: a concrete id in a path (/api/orders/123) is
// a row id, and a query value (?q=chair) is a user's input. Both are stripped
// here. Path segments that read as ids are templated to {id}, and only query
// parameter NAMES cross into a Route, never their values. So a Route can cross
// the control plane's data boundary unchanged.
//
// WHAT IS HANDED TO THE FUZZER. The consumer's send (injection.go) varies a
// query parameter on the route's path, so a route is worth fuzzing only when it
// carries at least one query parameter. A page reached with no query parameter
// is a route with nothing this consumer can vary, so it is not emitted; if the
// run reached only such pages the result is an empty, non-nil slice, which the
// injection family reads as a legitimate quiet pass, not as a block.
//
// THE THREE STATES, which the injection family reads as opposite verdicts:
//   - nil: there was no observed-route source to read. Exploration did not run
//     (it is off, or nil) or could not complete (Unavailable is set). This is
//     UNAVAILABLE, and the injection family reports blocked, never a pass: a
//     source we could not read must never look like a clean bill of health.
//   - empty, non-nil: exploration ran and we read it, and no route it reached
//     carries a fuzzable query parameter. A quiet, legitimate pass.
//   - populated: the routes to fuzz.
//
// Scope: the injection family's Probe fuzzes every route in Input.Routes and
// does not narrow by Input.Targets, and Routes is one per-run artifact shared by
// every family rather than a per-selection value, so this hands it every
// observed fuzzable route. The family was already gated by Select before it
// runs, and a finding fires only when a payload PROVES a differential, so a
// route that is not injectable yields nothing rather than a false positive.
func observedRoutes(run *report.Run) []security.Route {
	// Nil is the UNAVAILABLE state, and it is load-bearing: it must mean "no
	// source", never "a source that found nothing". Exploration absent, or
	// present but unavailable, is a source we could not read.
	if run == nil || run.Exploration == nil || run.Exploration.Unavailable != "" {
		return nil
	}

	index := map[string]*security.Route{}
	params := map[string]map[string]bool{}
	var order []string

	add := func(method, raw string) {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			return
		}
		// A browser navigation is http(s) or a relative path (empty scheme). A
		// mailto, javascript or data URL is not a route the fuzzer can reach.
		if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
			return
		}
		names := queryNames(u)
		if len(names) == 0 {
			// Nothing this consumer can vary on this route.
			return
		}
		p := u.Path
		if p == "" {
			p = "/"
		}
		if isStaticAsset(p) {
			return
		}
		// A reach with no method is a GET page load; a recorded request carries
		// its own, so a POST or fetch route is fuzzed as the method it was. The
		// method and path together key the route, so the same path reached by
		// GET and by POST is two routes and not one.
		method = strings.ToUpper(strings.TrimSpace(method))
		if method == "" {
			method = http.MethodGet
		}
		tmpl := templatePath(p)
		key := method + " " + tmpl
		if _, ok := index[key]; !ok {
			index[key] = &security.Route{Method: method, Path: tmpl}
			params[key] = map[string]bool{}
			order = append(order, key)
		}
		for _, n := range names {
			params[key][n] = true
		}
	}

	for _, x := range run.Exploration.Results {
		for _, v := range x.Visited {
			add(http.MethodGet, v)
		}
		for _, m := range x.Journey {
			if m.Kind == "goto" && m.URL != "" {
				add(http.MethodGet, m.URL)
			}
		}
		// Every reached request the runner recorded, with its own method, so a
		// POST or fetch API route reaches the fuzzer and not only a GET page.
		for _, rq := range x.Requests {
			add(rq.Method, rq.Path)
		}
	}

	// A non-nil slice even when empty: we read the source, so the absence of a
	// fuzzable route is EMPTY (a quiet pass) and never nil (UNAVAILABLE).
	out := make([]security.Route, 0, len(order))
	sort.Strings(order)
	for _, key := range order {
		r := index[key]
		names := make([]string, 0, len(params[key]))
		for n := range params[key] {
			names = append(names, n)
		}
		sort.Strings(names)
		r.Params = names
		out = append(out, *r)
	}
	return out
}

// queryNames is the sorted, de-duplicated set of query parameter NAMES on a
// URL, never their values. It is what a fuzzer varies, and the values are a
// user's input that must not cross into a Route.
func queryNames(u *url.URL) []string {
	q := u.Query()
	names := make([]string, 0, len(q))
	for name := range q {
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// templatePath replaces the value-bearing segments of a path with {id}, so a
// concrete /api/orders/123 becomes the location /api/orders/{id} and no row id
// crosses into a Route. Dedup then collapses /api/orders/1 and /api/orders/2
// into one route, and the fuzzer exercises the location once.
func templatePath(p string) string {
	if p == "" || p == "/" {
		return "/"
	}
	segments := strings.Split(p, "/")
	for i, seg := range segments {
		if idSegment(seg) {
			segments[i] = "{id}"
		}
	}
	return strings.Join(segments, "/")
}

// idSegment reports whether a path segment is a value rather than a route word.
// Conservative on purpose: a route word like "orders" or "search" must survive,
// so only the segments that clearly carry a value are templated: an all-numeric
// id, a UUID, or a long hexadecimal token such as an object id or a hash.
func idSegment(seg string) bool {
	if seg == "" {
		return false
	}
	if isAllDigits(seg) {
		return true
	}
	if isUUID(seg) {
		return true
	}
	// A long hex run is an object id or a digest, never a route word.
	if len(seg) >= 16 && isHex(seg) {
		return true
	}
	return false
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// isUUID matches the 8-4-4-4-12 hexadecimal form, with or without hyphens
// having been stripped, so a canonical UUID segment reads as a value.
func isUUID(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) != 5 {
		return false
	}
	lengths := [5]int{8, 4, 4, 4, 12}
	for i, part := range parts {
		if len(part) != lengths[i] || !isHex(part) {
			return false
		}
	}
	return true
}

// isStaticAsset reports whether a path is a static frontend file rather than an
// application route. A subresource is not something the runner records as a
// Visited page, but a goto move can name a file, and a stylesheet, a script, a
// font or an image is not an injection surface. It is deliberately a short,
// unambiguous list: a data extension such as .json or .xml is left OFF it,
// because an API route can end in one, and dropping a real injectable route to
// silence a little noise trades a missed finding for tidiness. The extension
// check is a convention, like the rest of the routing rules.
func isStaticAsset(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".js", ".mjs", ".cjs", ".css", ".map",
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp",
		".woff", ".woff2", ".ttf", ".eot":
		return true
	}
	return false
}
