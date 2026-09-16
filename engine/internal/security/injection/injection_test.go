package injection

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
		client:  &http.Client{Timeout: 5 * time.Second},
		sleep:   50 * time.Millisecond,
		maxBody: 64 << 10,
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
