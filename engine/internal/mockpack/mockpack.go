// Package mockpack answers a third-party API offline.
//
// Mock is the mode for a provider with no usable sandbox, and for running with
// no network at all. The bar it has to clear is higher than it looks: an
// application talking to a mock that returns a plausible but wrong shape does
// not fail at the mock, it fails three steps later somewhere that looks like
// its own bug. So a pack answers with the provider's real shapes, and it keeps
// state, because a create followed by a read that returns nothing is not a
// mock of anything.
//
// The format is data, not code. A pack is JSON, so somebody can write one for
// a provider nobody here has heard of without compiling anything, and the
// built in packs are the same format rather than a privileged path.
//
// Everything here is the standard library, because it runs inside the sidecar,
// whose image is built with no module downloads.
package mockpack

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Pack is a set of routes for one provider.
type Pack struct {
	// Name identifies the pack in output and in the decision log.
	Name string `json:"name"`
	// Hosts are the hosts this pack answers for.
	Hosts []string `json:"hosts"`
	// Routes are matched most specific first.
	Routes []Route `json:"routes"`
	// Description is one sentence for af mock list.
	Description string `json:"description,omitempty"`
}

// Route is one request shape and the answer to it.
type Route struct {
	// Method is the HTTP method. Empty matches any.
	Method string `json:"method,omitempty"`
	// Path is the request path, where a segment of {name} matches one segment
	// and captures it, and a trailing ** matches the rest.
	Path string `json:"path"`
	// Status is the HTTP status to return. Zero means 200.
	Status int `json:"status,omitempty"`
	// Body is the response, with {placeholders} filled from captures, from
	// the request body, and from the pack's state.
	Body json.RawMessage `json:"body,omitempty"`
	// Headers are added to the response.
	Headers map[string]string `json:"headers,omitempty"`
	// Store, when set, records the response under a collection so that a
	// later read can return it. This is what makes a pack stateful rather
	// than a list of canned answers.
	Store string `json:"store,omitempty"`
	// Load, when set, returns what was stored under a collection, by the id
	// captured from the path. A miss returns NotFound below.
	Load string `json:"load,omitempty"`
	// Merge, when set, applies Body over what is already stored under a
	// collection, by the id captured from the path, and stores the result. A
	// miss returns NotFound, the same as a Load.
	//
	// Store would answer the same request by REPLACING the stored object with
	// whatever the route's body happens to name, which is how cancelling a
	// subscription used to lose its customer, its period and its items: the
	// cancel route names five fields, so a read afterwards returned an object
	// with five fields. Every provider that has an update verb has this shape,
	// because an update names what changed and nothing else.
	Merge string `json:"merge,omitempty"`
	// NotFound is the response when a Load misses. It carries the provider's
	// own error shape, because an application handling a missing object
	// expects that shape and not a bare 404.
	NotFound json.RawMessage `json:"not_found,omitempty"`
	// List, when set, returns everything stored under a collection wrapped in
	// the provider's list shape.
	List string `json:"list,omitempty"`
}

// Match is a route that applies to a request.
type Match struct {
	Route    Route
	Captures map[string]string
	// Specificity ranks the match; higher wins. Literal segments beat
	// placeholders, so /v1/customers/deleted beats /v1/customers/{id} and a
	// pack author does not have to think about ordering.
	Specificity int
}

// Engine answers requests from a set of packs and remembers what was created.
type Engine struct {
	mu    sync.Mutex
	packs []Pack
	// store holds created objects, by collection then id.
	store map[string]map[string]json.RawMessage
	// order remembers insertion order, so a list is stable rather than
	// whatever a map iteration produced.
	order map[string][]string
	// counter makes generated identifiers unique within a run.
	counter int
}

// New returns an engine over the given packs.
func New(packs []Pack) *Engine {
	return &Engine{
		packs: packs,
		store: map[string]map[string]json.RawMessage{},
		order: map[string][]string{},
	}
}

// Parse reads one pack from JSON.
func Parse(body []byte) (Pack, error) {
	var p Pack
	if err := json.Unmarshal(body, &p); err != nil {
		return p, fmt.Errorf("mockpack: %w", err)
	}
	if p.Name == "" {
		return p, fmt.Errorf("mockpack: the pack has no name")
	}
	if len(p.Routes) == 0 {
		return p, fmt.Errorf("mockpack: %s declares no routes, so it would answer nothing", p.Name)
	}
	for i, r := range p.Routes {
		if r.Path == "" {
			return p, fmt.Errorf("mockpack: %s route %d has no path", p.Name, i)
		}
		// respond picks one of these by a switch, so a route naming two gets
		// whichever the switch reaches first and the author never learns that
		// the other was ignored.
		var named []string
		for _, kind := range []struct{ name, value string }{
			{"store", r.Store}, {"load", r.Load}, {"list", r.List}, {"merge", r.Merge},
		} {
			if kind.value != "" {
				named = append(named, kind.name)
			}
		}
		if len(named) > 1 {
			return p, fmt.Errorf("mockpack: %s route %d (%s %s) names %s, and one route does one of them",
				p.Name, i, r.Method, r.Path, strings.Join(named, " and "))
		}
		if (r.Load != "" || r.Merge != "") && !strings.Contains(r.Path, "{id}") {
			return p, fmt.Errorf("mockpack: %s route %d (%s %s) reads a stored object but its path captures no {id}",
				p.Name, i, r.Method, r.Path)
		}
	}
	return p, nil
}

// Handles reports whether any pack answers for a host.
func (e *Engine) Handles(host string) bool {
	_, ok := e.PackFor(host)
	return ok
}

// PackFor returns the pack that answers for a host.
//
// The first that matches, which is the same one Answer would consult, so a
// report of which pack covers a host cannot disagree with which pack actually
// answers it.
func (e *Engine) PackFor(host string) (Pack, bool) {
	for _, p := range e.packs {
		for _, h := range p.Hosts {
			if hostMatches(host, h) {
				return p, true
			}
		}
	}
	return Pack{}, false
}

// Stateful reports whether a pack remembers what was created.
//
// The distinction is the one the package header is about: a pack that stores a
// created object and returns it on the next read is a mock of the provider,
// and a pack that does not is a list of canned answers. An inventory that
// called both "mocked" would be describing two different fidelities with one
// word.
func (p Pack) Stateful() bool {
	for _, r := range p.Routes {
		if r.Store != "" || r.Load != "" || r.List != "" {
			return true
		}
	}
	return false
}

func hostMatches(host, pattern string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	pattern = strings.ToLower(pattern)
	if pattern == "*" {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:]
		return strings.HasSuffix(host, suffix) && len(host) > len(suffix)
	}
	return host == pattern
}

// Response is what a mock answered.
type Response struct {
	Status  int
	Headers map[string]string
	Body    []byte
	// Pack and Route name what answered, for the decision log. A mock that
	// cannot say which fixture produced a response is a mock nobody can
	// debug.
	Pack  string
	Route string
}

// Answer returns the response for a request, and whether any route matched.
//
// A miss is reported rather than guessed at. Returning an empty 200 for an
// unmatched request is how an application carries on with nothing and fails
// somewhere unrelated, so the caller writes a refusal that names the request
// and offers a skeleton to fill in.
func (e *Engine) Answer(host, method, path string, body []byte) (Response, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var best *Match
	var bestPack Pack
	for _, p := range e.packs {
		matched := false
		for _, h := range p.Hosts {
			if hostMatches(host, h) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for _, r := range p.Routes {
			m, ok := matchRoute(r, method, path)
			if !ok {
				continue
			}
			if best == nil || m.Specificity > best.Specificity {
				copyOf := m
				best, bestPack = &copyOf, p
			}
		}
	}
	if best == nil {
		return Response{}, false
	}
	return e.respond(bestPack, *best, body), true
}

func (e *Engine) respond(p Pack, m Match, requestBody []byte) Response {
	r := m.Route
	status := r.Status
	if status == 0 {
		status = 200
	}
	resp := Response{
		Status: status, Headers: r.Headers,
		Pack: p.Name, Route: r.Method + " " + r.Path,
	}

	switch {
	case r.Load != "":
		id := m.Captures["id"]
		if stored, ok := e.store[r.Load][id]; ok {
			resp.Body = stored
			return resp
		}
		return e.missing(resp, r)

	case r.Merge != "":
		id := m.Captures["id"]
		stored, ok := e.store[r.Merge][id]
		if !ok {
			return e.missing(resp, r)
		}
		merged := merge(stored, e.fillWith(r.Body, m, requestBody, true))
		e.remember(r.Merge, merged)
		resp.Body = merged
		return resp

	case r.List != "":
		resp.Body = e.list(r.List, r.Body, m)
		return resp
	}

	filled := e.fill(r.Body, m, requestBody)
	if r.Store != "" {
		e.remember(r.Store, filled)
	}
	resp.Body = filled
	return resp
}

// missing answers a Load or a Merge that named an object nobody created.
//
// The provider's own error shape, because an application handling a missing
// object expects that shape and not a bare 404.
func (e *Engine) missing(resp Response, r Route) Response {
	resp.Status = 404
	resp.Body = r.NotFound
	if resp.Body == nil {
		resp.Body = []byte(`{"error":{"type":"invalid_request_error","message":"No such object"}}`)
	}
	return resp
}

// merge applies an update over a stored object, one level deep.
//
// One level rather than recursive, because a provider's update replaces a
// nested object wholesale: sending items on a subscription update replaces the
// items, it does not merge into item zero. Recursing would produce an object
// no provider would ever return.
func merge(stored, update json.RawMessage) json.RawMessage {
	var into, from map[string]json.RawMessage
	if err := json.Unmarshal(stored, &into); err != nil {
		return update
	}
	if err := json.Unmarshal(update, &from); err != nil {
		return stored
	}
	for k, v := range from {
		into[k] = v
	}
	// Marshalling a map sorts the keys, so two runs of one flow produce the
	// same bytes even though the field order stops matching the pack's.
	out, err := json.Marshal(into)
	if err != nil {
		return stored
	}
	return out
}

// remember stores an object under its own id, so a later read finds it.
func (e *Engine) remember(collection string, body json.RawMessage) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return
	}
	var id string
	if raw, ok := obj["id"]; ok {
		_ = json.Unmarshal(raw, &id)
	}
	if id == "" {
		return
	}
	if e.store[collection] == nil {
		e.store[collection] = map[string]json.RawMessage{}
	}
	if _, exists := e.store[collection][id]; !exists {
		e.order[collection] = append(e.order[collection], id)
	}
	e.store[collection][id] = body
}

// list wraps what was stored in the pack's own list shape.
func (e *Engine) list(collection string, shape json.RawMessage, m Match) json.RawMessage {
	items := make([]json.RawMessage, 0, len(e.order[collection]))
	for _, id := range e.order[collection] {
		items = append(items, e.store[collection][id])
	}
	data, err := json.Marshal(items)
	if err != nil {
		data = []byte("[]")
	}
	if len(shape) == 0 {
		return data
	}
	filled := e.fill(shape, m, nil)
	// The shape names where the items go with a placeholder, which is
	// replaced with the array rather than with a string.
	return json.RawMessage(strings.ReplaceAll(string(filled), `"{items}"`, string(data)))
}

// fill substitutes placeholders in a body.
//
// Three sources, in order: what the path captured, what the request body
// carried, and generated values. The order matters because a pack that names
// {id} in both a path and a body means the path's.
func (e *Engine) fill(body json.RawMessage, m Match, requestBody []byte) json.RawMessage {
	return e.fillWith(body, m, requestBody, false)
}

// unfilledMark stands in for a field the request said nothing about, between
// substitution and the pass that removes it. A string rather than a sentinel
// value so that the body is still JSON while it is in there.
const unfilledMark = `"__af_unfilled__"`

// fillWith is fill, with a choice about a field the request did not supply.
//
// Everywhere but a merge, an unsupplied field becomes empty or null: a create
// answers with the object it created, and a field the caller omitted is a field
// the provider has no value for.
//
// A merge is the opposite. An update names what CHANGED, so a route listing
// email and name has to leave email exactly as it was when the caller sent only
// a name. Blanking it would make every update a partial wipe, and renaming a
// customer would clear its address.
func (e *Engine) fillWith(
	body json.RawMessage, m Match, requestBody []byte, dropUnsupplied bool,
) json.RawMessage {
	if len(body) == 0 {
		return body
	}
	fields := fieldsOf(requestBody)
	out := string(body)
	for name, value := range m.Captures {
		out = strings.ReplaceAll(out, "{"+name+"}", value)
	}
	for name, value := range fields {
		out = strings.ReplaceAll(out, "{request."+name+"}", value)
	}
	blank := ""
	if dropUnsupplied {
		// Only where the placeholder is the WHOLE value. A placeholder inside a
		// longer string has no field to remove, so it blanks as it always did.
		out = markWholeUnfilled(out)
		blank = unfilledMark
	}
	// Anything still unfilled from the request becomes empty rather than
	// staying as a brace, because a literal {request.email} in a response is
	// the placeholder leakage that proves nobody looked at the output.
	out = blankUnfilled(out, "{request.")
	out = scalars(out, m.Captures, fields, blank)
	out = e.generate(out)
	if dropUnsupplied {
		out = removeUnfilled(out)
	}
	return json.RawMessage(out)
}

// markWholeUnfilled replaces `"{request.x}"`, the whole string literal, with the
// mark that removeUnfilled deletes the field for.
func markWholeUnfilled(s string) string {
	from := 0
	for {
		start := strings.Index(s[from:], `"{request.`)
		if start < 0 {
			return s
		}
		start += from
		end := strings.Index(s[start:], `}"`)
		if end < 0 {
			return s
		}
		// A brace before the closing one means the placeholder ends earlier and
		// the literal carries more text, which is not a field to remove.
		if strings.Contains(s[start+1:start+end], `}`) {
			from = start + 1
			continue
		}
		s = s[:start] + unfilledMark + s[start+end+2:]
		from = start + len(unfilledMark)
	}
}

// removeUnfilled deletes every field the request said nothing about.
//
// A field whose value is the mark goes, and so does the whole branch
// CONTAINING one. That second rule is what makes an update of a nested
// structure behave: Stripe changes a plan with items[0][price], so the pack's
// update route names the whole items list, and an update that says only
// cancel_at_period_end would otherwise replace the subscription's items with an
// item whose price has no id. A partially supplied nested structure is not
// something a provider would ever apply, so it is not applied here either.
func removeUnfilled(s string) string {
	if !strings.Contains(s, unfilledMark) {
		return s
	}
	var parsed any
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return s
	}
	pruned, _ := prune(parsed)
	out, err := json.Marshal(pruned)
	if err != nil {
		return s
	}
	return string(out)
}

// prune returns the value with unsupplied fields removed, and whether it
// contained one at any depth. The root is never dropped however marked it is;
// a response with nothing in it is still a response.
func prune(v any) (any, bool) {
	switch value := v.(type) {
	case map[string]any:
		marked := false
		for k, inner := range value {
			if str, isString := inner.(string); isString && `"`+str+`"` == unfilledMark {
				delete(value, k)
				marked = true
				continue
			}
			cleaned, innerMarked := prune(inner)
			if innerMarked {
				delete(value, k)
				marked = true
				continue
			}
			value[k] = cleaned
		}
		return value, marked
	case []any:
		marked := false
		for i, inner := range value {
			cleaned, innerMarked := prune(inner)
			if innerMarked {
				marked = true
			}
			value[i] = cleaned
		}
		return value, marked
	default:
		return v, false
	}
}

// scalars replaces "{json:name}", the whole JSON string literal including its
// quotes, with a bare number or boolean.
//
// A pack cannot write an unquoted placeholder: a pack file has to be valid
// JSON before anything substitutes into it, and the decoder will not scan past
// `"created": {now}`. So a non-string field is written as a string and this is
// what takes the quotes off, the same way a list route already replaces
// `"{items}"` with an array rather than with a string.
//
// It earns its place. Without it every numeric field in the Stripe pack came
// back as "1767225600", sitting next to a current_period_end that was a real
// number in the same object, and an application with a typed client rejects
// that response while a curl and a grep call it fine. That is precisely the
// plausible but wrong shape this package exists to avoid, and it fails three
// steps later in something that looks like the application's own bug.
//
// A name nothing supplied, or a value that is neither a JSON number nor a
// boolean, becomes null, which is what a provider returns for a field it has no
// value for. `{json:name|1}` says the provider defaults it instead, which is the
// honest answer for a field like a subscription item's quantity: Stripe fills in
// one when a caller omits it and never returns null there, so null would be a
// new wrong shape rather than a missing value.
func scalars(s string, captures, fields map[string]string, unsupplied string) string {
	if unsupplied == "" {
		unsupplied = "null"
	}
	const open = `"{json:`
	for {
		start := strings.Index(s, open)
		if start < 0 {
			return s
		}
		end := strings.Index(s[start:], `}"`)
		if end < 0 {
			return s
		}
		name := s[start+len(open) : start+end]
		s = s[:start] + scalarFor(name, captures, fields, unsupplied) + s[start+end+2:]
	}
}

func scalarFor(name string, captures, fields map[string]string, unsupplied string) string {
	name, fallback, hasFallback := strings.Cut(name, "|")
	var raw string
	switch {
	case name == "now":
		raw = fixedNow
	case strings.HasPrefix(name, "request."):
		raw = fields[strings.TrimPrefix(name, "request.")]
	default:
		raw = captures[name]
	}
	if raw == "" && hasFallback {
		raw = fallback
	}
	if raw == "true" || raw == "false" {
		return raw
	}
	// Checked against the JSON number grammar rather than parsed loosely.
	// ParseFloat accepts Inf and NaN, and either of them written into a
	// response produces a body that is not JSON at all, which is a worse
	// failure than the wrong number.
	if raw == "" || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) || !json.Valid([]byte(raw)) {
		return unsupplied
	}
	return raw
}

// fieldsOf flattens a request body one level, and also reads form encoding,
// because half of these providers take forms.
func fieldsOf(body []byte) map[string]string {
	out := map[string]string{}
	if len(body) == 0 {
		return out
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err == nil {
		flatten("", obj, out)
		return out
	}
	for _, pair := range strings.Split(string(body), "&") {
		k, v, found := strings.Cut(pair, "=")
		if !found {
			continue
		}
		out[unescapeForm(k)] = unescapeForm(v)
	}
	return out
}

func flatten(prefix string, obj map[string]any, out map[string]string) {
	for k, v := range obj {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch value := v.(type) {
		case string:
			out[key] = value
		case float64:
			out[key] = strconv.FormatFloat(value, 'f', -1, 64)
		case bool:
			out[key] = strconv.FormatBool(value)
		case map[string]any:
			flatten(key, value, out)
		}
	}
}

func unescapeForm(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// blankUnfilled empties placeholders nothing supplied a value for.
func blankUnfilled(s, prefix string) string {
	for {
		start := strings.Index(s, prefix)
		if start < 0 {
			return s
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			return s
		}
		s = s[:start] + s[start+end+1:]
	}
}

// A fixed instant rather than the real one. A pack that returned the current
// time would produce a different response on every run, and two runs of one
// workflow could not be compared.
const fixedNow = "1767225600"

// generate fills the placeholders a pack uses for identifiers and timestamps.
func (e *Engine) generate(s string) string {
	// One identifier per prefix per response: a body that names {id:cs_} twice
	// means one checkout session, not two.
	//
	// It used to mean two, and that was the sharpest defect in the Stripe pack.
	// The session's url carried a DIFFERENT identifier from the session's own
	// id, so an application that redirected a browser to the url and later read
	// the session back by the identifier in it was told no such session exists.
	// The payment intent had the same shape: its client_secret named an intent
	// that had never been created. Neither could be seen from the pack file,
	// and the pack's own test asserted only that the url contained
	// checkout.stripe.com.
	minted := map[string]string{}
	for strings.Contains(s, "{id:") {
		start := strings.Index(s, "{id:")
		end := strings.Index(s[start:], "}")
		if end < 0 {
			break
		}
		prefix := s[start+4 : start+end]
		id, already := minted[prefix]
		if !already {
			e.counter++
			id = prefix + "mock" + pad(e.counter)
			minted[prefix] = id
		}
		s = s[:start] + id + s[start+end+1:]
	}
	if strings.Contains(s, "{now}") {
		s = strings.ReplaceAll(s, "{now}", fixedNow)
	}
	return s
}

func pad(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 14 {
		s = "0" + s
	}
	return s
}

// matchRoute reports whether a route applies, and what its path captured.
func matchRoute(r Route, method, path string) (Match, bool) {
	if r.Method != "" && !strings.EqualFold(r.Method, method) {
		return Match{}, false
	}
	captures := map[string]string{}
	score := 0
	if r.Method != "" {
		score += 1
	}

	want := strings.Split(strings.Trim(r.Path, "/"), "/")
	have := strings.Split(strings.Trim(path, "/"), "/")

	for i, seg := range want {
		if seg == "**" {
			// Matches the rest, and scores nothing, so a specific route always
			// wins over a catch all.
			return Match{Route: r, Captures: captures, Specificity: score}, true
		}
		if i >= len(have) {
			return Match{}, false
		}
		switch {
		case strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}"):
			captures[seg[1:len(seg)-1]] = have[i]
			score += 2
		case seg == have[i]:
			// A literal beats a placeholder, so /v1/customers/deleted wins
			// over /v1/customers/{id} without the author thinking about order.
			score += 10
		default:
			return Match{}, false
		}
	}
	if len(have) != len(want) {
		return Match{}, false
	}
	return Match{Route: r, Captures: captures, Specificity: score}, true
}

// Names returns the loaded pack names, sorted.
func (e *Engine) Names() []string {
	out := make([]string, 0, len(e.packs))
	for _, p := range e.packs {
		out = append(out, p.Name)
	}
	sort.Strings(out)
	return out
}

// Skeleton returns a route somebody can paste into a pack for a request that
// matched nothing.
//
// Handing back the shape to fill in is the difference between a dead end and
// a two minute fix.
func Skeleton(host, method, path string) string {
	return fmt.Sprintf(`{
  "name": "%s",
  "hosts": ["%s"],
  "routes": [
    {"method": "%s", "path": "%s", "status": 200, "body": {}}
  ]
}`, strings.ReplaceAll(host, ".", "-"), host, strings.ToUpper(method), path)
}
