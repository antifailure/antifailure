// Package injection fires injection payloads at the endpoints a change touched,
// inside the sanitized twin, and reports only the ones it can PROVE altered how
// a query or command was interpreted.
//
// The twin is what lets this be behavioral rather than a regex over source: the
// golden is masked and the environment is torn down, so a payload that actually
// runs is safe to run. Every class here has a behavioral oracle, not a
// signature. A SQL payload is a finding when an injected sleep moves the
// response time or a database error surfaces the schema, not when the request
// returned a 500. A template payload is a finding when the response contains the
// EVALUATED product rather than the expression. A path traversal payload is a
// finding when the response carries a file the endpoint should never read. A
// NoSQL operator is a finding when it turned a denial into an answer. The
// difference between "the payload was reflected" and "the payload changed the
// behavior" is the whole point: only the second is reported.
//
// Each finding names the endpoint and the class and the observed effect, and
// never the payload value, so a finding cannot be mistaken for a live exploit
// string in a log. The comparison is always control against payload: a benign
// value is sent first and the payload's effect is measured as the difference,
// which is what defeats an endpoint that is simply slow or simply echoes its
// input.
package injection

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// The policy keys this family owns, one per payload class. Every one is a
// verification failure (exit 7): the family proved the vulnerability by
// exercising the running application, so it is a proven hole and not a policy
// note. A test proves each declared exit equals security.ExitFor(key).
const (
	RuleSQL           = report.PolicyKey("security.injection.sql")
	RuleCommand       = report.PolicyKey("security.injection.command")
	RuleTemplate      = report.PolicyKey("security.injection.template")
	RuleNoSQL         = report.PolicyKey("security.injection.nosql")
	RulePathTraversal = report.PolicyKey("security.injection.path_traversal")
	RuleDynamicQuery  = report.PolicyKey("security.injection.dynamic_query")
)

// Response is what one request returned, reduced to the three facts the oracles
// read: the status, a bounded body, and how long it took. The body is bounded
// on read so a large response never sits in memory and never reaches a finding.
type Response struct {
	Status  int
	Body    string
	Latency time.Duration
}

// vector is one payload with the control value it is measured against and the
// behavioral oracle that decides whether it proved its class. The oracle reads
// both responses so a finding is a DIFFERENCE the payload caused, never a
// property of the payload alone.
type vector struct {
	class   report.PolicyKey
	control string
	payload string
	// assess reports whether the payload proved the class, and a bounded phrase
	// describing the observed effect. It never returns the body or the payload.
	assess func(control, payload Response) (bool, string)
}

// Distinctive markers the oracles look for. The template product is a pair of
// primes whose product is large enough that an application is very unlikely to
// contain it by coincidence, and the sentinel is a token planted nowhere a
// benign response would carry it.
const (
	tmplLeft    = 8191
	tmplRight   = 8209
	tmplProduct = "67239919" // 8191 * 8209
	tmplExpr    = "8191*8209"
	passwdMark  = "root:x:0:0"
)

// vectors is the payload catalog. The sleep duration is injected so a test can
// use a short one; production uses a sleep long enough to clear network noise.
func vectors(sleep time.Duration) []vector {
	secs := int(sleep.Seconds())
	if secs < 1 {
		secs = 1
	}
	return []vector{
		// SQL, time based: a sleep the query runs moves the response time well
		// past the control's. Proven by latency, defeated by a control arm.
		{
			class:   RuleSQL,
			control: "af_control",
			payload: fmt.Sprintf("' OR pg_sleep(%d) OR '1'='1", secs),
			assess: func(c, p Response) (bool, string) {
				if movedByAtLeast(c, p, sleep) {
					return true, "an injected sleep moved the response time, so the value ran as SQL"
				}
				return false, ""
			},
		},
		// SQL, error based: a broken string literal surfaces a database error
		// naming the engine or the schema. Proven by a signature the control
		// response did not carry.
		{
			class:   RuleSQL,
			control: "af_control",
			payload: `af_control'"`,
			assess: func(c, p Response) (bool, string) {
				if !hasSQLError(c.Body) && hasSQLError(p.Body) {
					return true, "a database error surfaced the schema, so the value reached the query unescaped"
				}
				return false, ""
			},
		},
		// Command injection, time based: a chained shell sleep moves the time.
		{
			class:   RuleCommand,
			control: "af_control",
			payload: fmt.Sprintf("af_control; sleep %d", secs),
			assess: func(c, p Response) (bool, string) {
				if movedByAtLeast(c, p, sleep) {
					return true, "an injected shell sleep moved the response time, so the value ran as a command"
				}
				return false, ""
			},
		},
		// Template injection: the response carries the evaluated product rather
		// than the expression. Proven by the product appearing where the control
		// did not carry it and the raw expression did not survive.
		{
			class:   RuleTemplate,
			control: "af_control",
			payload: "{{" + tmplExpr + "}}",
			assess:  assessTemplate,
		},
		{
			class:   RuleTemplate,
			control: "af_control",
			payload: "${" + tmplExpr + "}",
			assess:  assessTemplate,
		},
		// NoSQL operator smuggling: an operator turns a denial into an answer.
		// Proven by a status that went from refused to allowed, or a body that
		// grew from empty to populated.
		{
			class:   RuleNoSQL,
			control: "af_control",
			payload: `{"$ne":null}`,
			assess: func(c, p Response) (bool, string) {
				if deniedThenAllowed(c, p) {
					return true, "an operator turned a refusal into an answer, so the value reached the query as an operator"
				}
				if grewFromEmpty(c, p) {
					return true, "an operator returned records the control value did not, so it changed the query filter"
				}
				return false, ""
			},
		},
		// Path traversal: the response carries a file outside the application
		// root. Proven by a sentinel the endpoint should never read.
		{
			class:   RulePathTraversal,
			control: "af_control",
			payload: "../../../../../../etc/passwd",
			assess:  assessTraversal,
		},
		{
			class:   RulePathTraversal,
			control: "af_control",
			payload: "..%2f..%2f..%2f..%2f..%2f..%2fetc%2fpasswd",
			assess:  assessTraversal,
		},
		// Dynamic query reflection: an order or column parameter reflected into
		// SQL changes the ORDER of the result. Proven by the same items coming
		// back in a different sequence.
		{
			class:   RuleDynamicQuery,
			control: "id",
			payload: "id desc",
			assess: func(c, p Response) (bool, string) {
				if reordered(c.Body, p.Body) {
					return true, "an order parameter reflected into SQL changed the result order, so it reached the query unescaped"
				}
				return false, ""
			},
		},
	}
}

// movedByAtLeast reports whether the payload response took at least a sleep's
// worth longer than the control, the time oracle for SQL and command sleeps. It
// requires a majority of the injected sleep so a merely slow endpoint does not
// trip it.
func movedByAtLeast(c, p Response, sleep time.Duration) bool {
	threshold := sleep * 3 / 5
	return p.Latency-c.Latency >= threshold
}

// hasSQLError reports whether a body carries a database error that named the
// engine or the schema, the error oracle for SQL. The signatures are the ones a
// leaked driver error prints; none appears in an ordinary response.
func hasSQLError(body string) bool {
	low := strings.ToLower(body)
	for _, sig := range []string{
		"syntax error at or near",
		"unterminated quoted string",
		"sqlstate",
		"pg_query",
		"psqlexception",
		"psycopg2",
		"you have an error in your sql syntax",
		"sqlite3.operationalerror",
		"ora-0",
	} {
		if strings.Contains(low, sig) {
			return true
		}
	}
	return false
}

// assessTemplate reports whether a template payload was evaluated server side.
// The product must appear in the payload response, must NOT already be in the
// control response, and the raw expression must NOT have survived, so a merely
// reflected expression is not mistaken for an evaluated one.
func assessTemplate(c, p Response) (bool, string) {
	if strings.Contains(c.Body, tmplProduct) {
		return false, ""
	}
	if strings.Contains(p.Body, tmplProduct) && !strings.Contains(p.Body, tmplExpr) {
		return true, "the response carried the evaluated product, so the template rendered the value server side"
	}
	return false, ""
}

// assessTraversal reports whether a traversal payload read a file outside the
// application root. Proven by the passwd sentinel appearing where the control
// did not carry it.
func assessTraversal(c, p Response) (bool, string) {
	if !strings.Contains(c.Body, passwdMark) && strings.Contains(p.Body, passwdMark) {
		return true, "the response carried a system file, so the path escaped the application root"
	}
	return false, ""
}

// deniedThenAllowed reports whether the control was refused and the payload
// allowed, the auth bypass signal for a smuggled operator.
func deniedThenAllowed(c, p Response) bool {
	return c.Status >= 400 && c.Status < 500 && p.Status >= 200 && p.Status < 300
}

// grewFromEmpty reports whether the control returned an empty or tiny body and
// the payload returned a populated one, the filter bypass signal for a smuggled
// operator. The threshold is deliberately large so ordinary variation does not
// trip it.
func grewFromEmpty(c, p Response) bool {
	return len(strings.TrimSpace(c.Body)) < 8 && len(strings.TrimSpace(p.Body)) >= 32
}

// reordered reports whether two bodies carry the same set of non trivial tokens
// in a different sequence, the signal that an order parameter reached the query.
// Equal bodies are not reordered, and bodies with different token sets are a
// different response rather than a reordering.
func reordered(control, payload string) bool {
	if control == payload || strings.TrimSpace(control) == "" {
		return false
	}
	cTokens := tokenize(control)
	pTokens := tokenize(payload)
	if len(cTokens) < 2 || len(cTokens) != len(pTokens) {
		return false
	}
	if !sameMultiset(cTokens, pTokens) {
		return false
	}
	// Same multiset; a different sequence is a reordering.
	for i := range cTokens {
		if cTokens[i] != pTokens[i] {
			return true
		}
	}
	return false
}

// tokenize splits a body into comparable tokens on non alphanumeric runs.
func tokenize(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !isAlnum(r) })
}

// isAlnum reports whether a rune is an ASCII letter or digit.
func isAlnum(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

// sameMultiset reports whether two token slices contain the same tokens with the
// same multiplicities, ignoring order.
func sameMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[string]int{}
	for _, t := range a {
		counts[t]++
	}
	for _, t := range b {
		counts[t] -= 1
		if counts[t] < 0 {
			return false
		}
	}
	return true
}

// keys declares one policy key per payload class. All are verification failures,
// and a test proves each declared exit equals security.ExitFor(key).
func keys() []security.KeySpec {
	mk := func(k report.PolicyKey, title string) security.KeySpec {
		return security.KeySpec{Key: k, Default: report.LevelFail, Title: title, Docs: "concepts/security", Exit: report.ExitVerification}
	}
	return []security.KeySpec{
		mk(RuleSQL, "a value reached a SQL query unescaped"),
		mk(RuleCommand, "a value ran as a shell command"),
		mk(RuleTemplate, "a value was evaluated by a server side template"),
		mk(RuleNoSQL, "an operator was smuggled into a NoSQL query"),
		mk(RulePathTraversal, "a path escaped the application root"),
		mk(RuleDynamicQuery, "a value was reflected into a dynamic query"),
	}
}

var (
	familySurfaces = []change.Surface{change.SurfaceCode, change.SurfaceService, change.SurfaceSchema}
	familyChecks   = []change.Check{change.CheckInjection}
)

var _ security.Family = (*family)(nil)

// family is the injection prober as a registered security family.
type family struct {
	client  *http.Client
	sleep   time.Duration
	maxBody int64
}

// New builds the injection family. The router registers it with a bare New and
// supplies the per run routes through security.Input.Routes.
func New() security.Family {
	return &family{
		client:  &http.Client{Timeout: 30 * time.Second},
		sleep:   3 * time.Second,
		maxBody: 64 << 10,
	}
}

func (f *family) Name() string               { return "injection" }
func (f *family) Surfaces() []change.Surface { return familySurfaces }
func (f *family) Checks() []change.Check     { return familyChecks }
func (f *family) Keys() []security.KeySpec   { return keys() }
func (f *family) Licensed() string           { return "" }

// Probe fuzzes each routed endpoint and returns the proven findings. A nil route
// slice is UNAVAILABLE, a blocked probe: no observed-route source was wired, and
// a family that could not reach an endpoint has proven nothing and must not read
// as a pass. An empty non-nil slice means the change touched no fuzzable
// endpoint, which is quiet rather than blocked.
func (f *family) Probe(ctx context.Context, in security.Input) ([]report.Finding, error) {
	routes := in.Routes()
	if routes == nil {
		return nil, fmt.Errorf(
			"the injection prober found no observed route source for this run, so it fuzzed nothing")
	}
	base := strings.TrimRight(in.Env.BaseURL, "/")
	if base == "" {
		return nil, fmt.Errorf("the injection prober has no base URL for the twin, so it fuzzed nothing")
	}

	catalog := vectors(f.sleep)
	// Dedup a proven class per endpoint: one finding per endpoint and class,
	// with the count of proven vectors.
	type key struct {
		ref   string
		class report.PolicyKey
	}
	proven := map[key]*report.Finding{}
	var order []key

	for _, route := range routes {
		for _, param := range route.Params {
			for _, v := range catalog {
				if in.Policy.Level(v.class) == report.LevelIgnore {
					continue
				}
				control, cErr := f.send(ctx, route, base, param, v.control)
				if cErr != nil {
					continue
				}
				payload, pErr := f.send(ctx, route, base, param, v.payload)
				if pErr != nil {
					continue
				}
				ok, effect := v.assess(control, payload)
				if !ok {
					continue
				}
				k := key{ref: route.Path, class: v.class}
				fnd, exists := proven[k]
				if !exists {
					fnd = &report.Finding{
						Rule:   string(v.class),
						Level:  in.Policy.Level(v.class),
						Title:  titleFor(v.class),
						Detail: fmt.Sprintf("at %s parameter %q: %s", route.Path, param, effect),
						Fix:    fixFor(v.class),
						Count:  0,
						Where:  route.Path,
					}
					proven[k] = fnd
					order = append(order, k)
				}
				fnd.Count++
			}
		}
	}

	if len(order) == 0 {
		return nil, nil
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].ref != order[j].ref {
			return order[i].ref < order[j].ref
		}
		return order[i].class < order[j].class
	})
	out := make([]report.Finding, 0, len(order))
	for _, k := range order {
		out = append(out, *proven[k])
	}
	return out, nil
}

// send issues one request with the parameter set to a value and returns the
// bounded response. The body is read up to maxBody so a large response never
// sits in memory, and neither the value nor the body ever reaches a finding.
func (f *family) send(ctx context.Context, route security.Route, base, param, value string) (Response, error) {
	method := route.Method
	if method == "" {
		method = http.MethodGet
	}
	target, err := url.Parse(base + route.Path)
	if err != nil {
		return Response{}, err
	}
	q := target.Query()
	q.Set(param, value)
	target.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return Response{}, err
	}
	start := time.Now()
	resp, err := f.client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, f.maxBody))
	return Response{Status: resp.StatusCode, Body: string(body), Latency: time.Since(start)}, nil
}

func titleFor(class report.PolicyKey) string {
	switch class {
	case RuleSQL:
		return "a value reached a SQL query unescaped"
	case RuleCommand:
		return "a value ran as a shell command"
	case RuleTemplate:
		return "a value was evaluated by a server side template"
	case RuleNoSQL:
		return "an operator was smuggled into a NoSQL query"
	case RulePathTraversal:
		return "a path escaped the application root"
	case RuleDynamicQuery:
		return "a value was reflected into a dynamic query"
	}
	return "an injection payload changed how the request was interpreted"
}

func fixFor(class report.PolicyKey) string {
	switch class {
	case RuleSQL, RuleDynamicQuery:
		return "Parameterise the query and allow list any column or order value; never concatenate a request value into SQL."
	case RuleCommand:
		return "Run the command through an argument vector with no shell, and reject a value carrying shell metacharacters."
	case RuleTemplate:
		return "Render user values as data, not as a template; never pass a request value to the template engine."
	case RuleNoSQL:
		return "Coerce a request value to the expected scalar type and reject an object where a scalar belongs, so an operator cannot be smuggled in."
	case RulePathTraversal:
		return "Resolve the path against the application root and reject any result outside it; never join a request value onto a filesystem path."
	}
	return "Treat the request value as data and validate it against the shape the endpoint expects."
}
