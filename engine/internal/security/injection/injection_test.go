package injection

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// allFail is a policy that fails on every injection key, so a proven vector
// becomes a fail finding.
func allFail() report.Policy {
	m := map[report.PolicyKey]report.Level{}
	for _, k := range keys() {
		m[k.Key] = report.LevelFail
	}
	return report.Policy{Security: m}
}

// Pure oracle unit tests: fast, deterministic, mutation targets.

func TestAssessTemplate(t *testing.T) {
	control := Response{Body: "you said: af_control"}
	evaluated := Response{Body: "result: " + tmplProduct}
	ok, effect := assessTemplate(control, evaluated)
	require.True(t, ok)
	require.NotEmpty(t, effect)

	// A reflected expression that did not evaluate is not a finding.
	reflected := Response{Body: "you said: " + tmplExpr}
	ok, _ = assessTemplate(control, reflected)
	require.False(t, ok, "a reflected expression is not an evaluated one")

	// A control that already carries the product cannot prove anything.
	ok, _ = assessTemplate(Response{Body: tmplProduct}, Response{Body: tmplProduct})
	require.False(t, ok)

	// The product AND the raw expression both present is reflection, not
	// evaluation: the expression survived, so the engine did not render it.
	ok, _ = assessTemplate(control, Response{Body: tmplProduct + " from " + tmplExpr})
	require.False(t, ok, "the expression surviving alongside the product is reflection")
}

func TestAssessTraversal(t *testing.T) {
	ok, effect := assessTraversal(Response{Body: "you said: af_control"}, Response{Body: "root:x:0:0:root:/root"})
	require.True(t, ok)
	require.NotEmpty(t, effect)
	// The path being echoed back is not the file being read.
	ok, _ = assessTraversal(Response{Body: "af_control"}, Response{Body: "../../../etc/passwd not found"})
	require.False(t, ok, "an echoed path is not a leaked file")

	// A page that always shows passwd like content proves nothing: the control
	// already carries the mark, so the payload changed nothing.
	ok, _ = assessTraversal(Response{Body: "root:x:0:0"}, Response{Body: "root:x:0:0"})
	require.False(t, ok, "the mark present in the control proves nothing")
}

func TestHasSQLError(t *testing.T) {
	require.True(t, hasSQLError("ERROR: syntax error at or near \"'\""))
	require.True(t, hasSQLError("psycopg2.errors.SyntaxError"))
	require.False(t, hasSQLError("you said: af_control"))
}

func TestMovedByAtLeast(t *testing.T) {
	c := Response{Latency: 10 * time.Millisecond}
	slow := Response{Latency: 3100 * time.Millisecond}
	require.True(t, movedByAtLeast(c, slow, 3*time.Second), "a full injected sleep moved the time")
	fast := Response{Latency: 40 * time.Millisecond}
	require.False(t, movedByAtLeast(c, fast, 3*time.Second), "ordinary variance does not trip the time oracle")
}

func TestDeniedThenAllowed(t *testing.T) {
	require.True(t, deniedThenAllowed(Response{Status: 403}, Response{Status: 200}))
	require.False(t, deniedThenAllowed(Response{Status: 200}, Response{Status: 200}))
	require.False(t, deniedThenAllowed(Response{Status: 500}, Response{Status: 200}), "a server error is not a denial")
}

func TestGrewFromEmpty(t *testing.T) {
	require.True(t, grewFromEmpty(Response{Body: "[]"}, Response{Body: strings.Repeat("x", 64)}))
	require.False(t, grewFromEmpty(Response{Body: strings.Repeat("x", 64)}, Response{Body: strings.Repeat("x", 128)}),
		"a body that was already populated did not grow from empty")
}

func TestReordered(t *testing.T) {
	require.True(t, reordered("alpha beta gamma", "gamma beta alpha"), "same tokens, different order")
	require.False(t, reordered("alpha beta gamma", "alpha beta gamma"), "identical bodies are not reordered")
	require.False(t, reordered("alpha beta gamma", "alpha beta delta"), "a different token set is a different response")
}

// End to end Probe against a live server.

// vulnerableServer evaluates templates, leaks a system file on traversal, and
// prints a database error on a stray quote. It is deterministic and content
// based, so the end to end test needs no timing.
func vulnerableServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		switch {
		case strings.Contains(q, tmplExpr):
			// Evaluated server side; the product is returned and the expression
			// does not survive.
			fmt.Fprintf(w, "result: %d", tmplLeft*tmplRight)
		case strings.Contains(q, "'"):
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `ERROR: syntax error at or near "'"`)
		case strings.Contains(q, "passwd"):
			fmt.Fprint(w, "root:x:0:0:root:/root:/bin/bash")
		default:
			fmt.Fprint(w, "you said: af_control")
		}
	}))
}

// safeServer echoes the input verbatim, which is the worst realistic case that
// is still not exploitable: a reflected payload must never read as a proven one.
func safeServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "you said: %s", r.URL.Query().Get("q"))
	}))
}

func newTestFamily() *family {
	return &family{
		client:       &http.Client{Timeout: 5 * time.Second},
		sleep:        50 * time.Millisecond,
		maxBody:      64 << 10,
		retryUnit:    10 * time.Millisecond,
		maxRetryWait: 50 * time.Millisecond,
	}
}

// inputWithRoutes builds an Input carrying the base URL and the observed routes
// the way the router's securityFindings does, through WithRunArtifacts.
func inputWithRoutes(base string, routes []security.Route, pol report.Policy) security.Input {
	return security.Input{Env: security.Environment{BaseURL: base}, Policy: pol}.
		WithRunArtifacts(security.RunArtifacts{Routes: routes})
}

func TestProbe_ProvesInjectionAgainstAVulnerableServer(t *testing.T) {
	srv := vulnerableServer()
	defer srv.Close()
	f := newTestFamily()
	in := inputWithRoutes(srv.URL, []security.Route{{Method: http.MethodGet, Path: "/", Params: []string{"q"}}}, allFail())
	findings, err := f.Probe(context.Background(), in)
	require.NoError(t, err)
	rules := map[string]int{}
	for _, fd := range findings {
		rules[fd.Rule] += fd.Count
		require.NotContains(t, fd.Detail, tmplExpr, "a finding must not carry the payload value")
		require.NotContains(t, fd.Detail, "passwd")
	}
	require.Contains(t, rules, string(RuleTemplate), "the evaluated template is proven")
	require.Contains(t, rules, string(RulePathTraversal), "the leaked file is proven")
	require.Contains(t, rules, string(RuleSQL), "the database error is proven")
}

func TestProbe_SafeServerYieldsNoFindings(t *testing.T) {
	// The liveness arm: a server that merely reflects input must produce no
	// finding, or the check cannot say no.
	srv := safeServer()
	defer srv.Close()
	f := newTestFamily()
	in := inputWithRoutes(srv.URL, []security.Route{{Method: http.MethodGet, Path: "/", Params: []string{"q"}}}, allFail())
	findings, err := f.Probe(context.Background(), in)
	require.NoError(t, err)
	require.Empty(t, findings, "a reflecting but safe server proves nothing")
}

func TestProbe_NoRouteSourceIsBlockedNeverAPass(t *testing.T) {
	f := New().(*family)
	// An Input with no observed routes attached returns nil from Routes, which
	// is UNAVAILABLE and a blocked probe, never a pass.
	findings, err := f.Probe(context.Background(), security.Input{
		Env:    security.Environment{BaseURL: "http://example.invalid"},
		Policy: allFail(),
	})
	require.Error(t, err, "no route source is a blocked probe, never a pass")
	require.Nil(t, findings)
}

func TestProbe_EmptyRoutesAreQuiet(t *testing.T) {
	f := newTestFamily()
	in := inputWithRoutes("http://example.invalid", []security.Route{}, allFail())
	findings, err := f.Probe(context.Background(), in)
	require.NoError(t, err, "a change that touched no fuzzable endpoint is quiet, not blocked")
	require.Empty(t, findings)
}

func TestKeys_DeclaredExitMatchesExitFor(t *testing.T) {
	for _, k := range keys() {
		require.Equalf(t, security.ExitFor(k.Key), k.Exit,
			"the declared exit for %s must equal security.ExitFor", k.Key)
		require.Equal(t, report.ExitVerification, k.Exit, "every injection key is a proven vulnerability")
	}
}

func TestFamily_ShapeIsRegisterable(t *testing.T) {
	f := New()
	require.Equal(t, "injection", f.Name())
	require.NotEmpty(t, f.Surfaces())
	require.Empty(t, f.Licensed())
	require.Len(t, f.Keys(), 6, "one key per payload class")
	reg := security.NewRegistry()
	require.NotPanics(t, func() { reg.Register(f) })
}

// limitedStaticServer is the console as the dogfood run met it: a static file
// that ignores its query string entirely, behind a per address token bucket.
// Once the burst is spent, a request is refused 429 with a Retry-After unless a
// token has trickled back in, which the trickle models deterministically as
// every fifth request. A client that waits as it was told finds the bucket
// refilled. Nothing about the parameter's VALUE changes any answer, so every
// finding against it is false.
func limitedStaticServer(burst int) *httptest.Server {
	var mu sync.Mutex
	tokens := burst
	n := 0
	var refusedAt time.Time
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		if tokens == 0 && !refusedAt.IsZero() && time.Since(refusedAt) >= 5*time.Millisecond {
			tokens = burst
		}
		allowed := tokens > 0 || n%5 == 0
		if tokens > 0 {
			tokens--
		}
		if !allowed {
			refusedAt = time.Now()
		}
		mu.Unlock()
		if !allowed {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":"Too many requests.","retryAfterSeconds":1}`)
			return
		}
		fmt.Fprint(w, `1:"$Sreact.fragment"`+"\n"+`0:{"b":"build","f":[["",{"children":["network"]}]]}`)
	}))
}

// rscRoutes is the shape the dogfood run fuzzed: twenty seven Next.js page
// payload routes, each reached with the framework's own _rsc cache key.
func rscRoutes(n int) []security.Route {
	routes := make([]security.Route, 0, n)
	for i := 0; i < n; i++ {
		routes = append(routes, security.Route{Method: http.MethodGet, Path: fmt.Sprintf("/page%d.txt", i), Params: []string{"_rsc"}})
	}
	return routes
}

func TestProbe_ARateLimitIsNotARefusalOfTheValue(t *testing.T) {
	// The false finding the dogfood run reported on three nights out of five on
	// one unchanged commit: a control refused 429 because the prober itself had
	// spent the bucket, then a payload let through by the token that arrived in
	// between, read as "an operator turned a refusal into an answer".
	srv := limitedStaticServer(20)
	defer srv.Close()
	f := newTestFamily()
	findings, err := f.Probe(context.Background(), inputWithRoutes(srv.URL, rscRoutes(27), allFail()))
	require.NoError(t, err, "a client that waits as asked measures every comparison")
	require.Empty(t, findings, "a static file behind a rate limiter proves no injection")
}

// flappingNoSQLServer refuses every other request, whatever it carries. Its
// denials are a property of time, never of the value.
func flappingNoSQLServer() *httptest.Server {
	var mu sync.Mutex
	n := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		odd := n%2 == 1
		mu.Unlock()
		if odd {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, "forbidden")
			return
		}
		fmt.Fprint(w, "ok")
	}))
}

func TestProbe_ADenialThatDoesNotReproduceIsNotAFinding(t *testing.T) {
	srv := flappingNoSQLServer()
	defer srv.Close()
	f := newTestFamily()
	findings, err := f.Probe(context.Background(), inputWithRoutes(srv.URL, rscRoutes(3), allFail()))
	require.NoError(t, err)
	require.Empty(t, findings, "a denial that flips with time, not with the value, proves nothing")
}

// nosqlServer is a real operator smuggling hole: the value is decoded as JSON
// and an object reaches the filter, so {"$ne":null} matches every row.
func nosqlServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Query().Get("token"), `{"$ne"`) {
			fmt.Fprint(w, `{"user":"admin","orders":[1,2,3]}`)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "no such token")
	}))
}

func TestProbe_ProvesARealNoSQLOperatorSmuggling(t *testing.T) {
	srv := nosqlServer()
	defer srv.Close()
	f := newTestFamily()
	in := inputWithRoutes(srv.URL, []security.Route{{Method: http.MethodGet, Path: "/session", Params: []string{"token"}}}, allFail())
	findings, err := f.Probe(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	require.Equal(t, string(RuleNoSQL), findings[0].Rule)
}

func TestProbe_ProvesNoSQLBehindARateLimiter(t *testing.T) {
	// Waiting must not cost the check its ability to say yes.
	inner := nosqlServer()
	defer inner.Close()
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		refuse := n%3 == 0
		mu.Unlock()
		if refuse {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		resp, err := http.Get(inner.URL + r.URL.RequestURI())
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer srv.Close()
	f := newTestFamily()
	in := inputWithRoutes(srv.URL, []security.Route{{Method: http.MethodGet, Path: "/session", Params: []string{"token"}}}, allFail())
	findings, err := f.Probe(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	require.Equal(t, string(RuleNoSQL), findings[0].Rule)
}

func TestProbe_ARateLimitThatNeverLiftsIsBlockedNeverAPass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	f := newTestFamily()
	findings, err := f.Probe(context.Background(), inputWithRoutes(srv.URL, rscRoutes(1), allFail()))
	require.Error(t, err, "a prober refused every time measured nothing and must say so")
	require.Contains(t, err.Error(), "rate limit")
	require.Nil(t, findings)
}

func TestRateLimitWait(t *testing.T) {
	f := newTestFamily()
	wait, limited := f.rateLimitWait(http.StatusTooManyRequests, "3")
	require.True(t, limited, "a 429 refuses the rate")
	require.Equal(t, 30*time.Millisecond, wait, "the header's seconds, in the family's unit")

	wait, limited = f.rateLimitWait(http.StatusTooManyRequests, "")
	require.True(t, limited, "a 429 with no header still refuses the rate")
	require.Equal(t, f.retryUnit, wait, "one unit when the header is absent")

	wait, _ = f.rateLimitWait(http.StatusTooManyRequests, "3600")
	require.Equal(t, f.maxRetryWait, wait, "a wait is capped")

	_, limited = f.rateLimitWait(http.StatusServiceUnavailable, "2")
	require.True(t, limited, "a 503 that says when to return is a request to slow down")

	_, limited = f.rateLimitWait(http.StatusServiceUnavailable, "")
	require.False(t, limited, "a bare 503 is the application failing, which is its answer")

	_, limited = f.rateLimitWait(http.StatusForbidden, "2")
	require.False(t, limited, "a refusal of the content is never retried away")
}
