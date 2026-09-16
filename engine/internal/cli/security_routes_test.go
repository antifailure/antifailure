package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
	"github.com/antifailure/antifailure/engine/internal/security/injection"
)

// runWithVisited builds a report.Run whose exploration reached the given URLs,
// the shape ci fills before the security collector runs.
func runWithVisited(base string, visited ...string) *report.Run {
	return &report.Run{
		URL: base,
		Exploration: &report.Exploration{
			Results: []explore.Exploration{{Visited: visited}},
		},
	}
}

// TestObservedRoutes_NilWhenNoExplorationSource proves the UNAVAILABLE state:
// nil means "no source to read", which the injection family reports as blocked.
// It must never be confused with an empty slice, which means "read, found
// nothing".
func TestObservedRoutes_NilWhenNoExplorationSource(t *testing.T) {
	require.Nil(t, observedRoutes(nil), "no run is no source")
	require.Nil(t, observedRoutes(&report.Run{}), "no exploration is no source")
	require.Nil(t,
		observedRoutes(&report.Run{Exploration: &report.Exploration{Unavailable: "the runner could not explore"}}),
		"an exploration that could not complete is not a source that found nothing")
}

// TestObservedRoutes_EmptyWhenNothingFuzzable proves the quiet-pass state:
// exploration ran but no route carried a query parameter, so the result is a
// non-nil, empty slice. The injection family reads that as a legitimate pass,
// not a block, so nil and empty must be distinguishable here.
func TestObservedRoutes_EmptyWhenNothingFuzzable(t *testing.T) {
	got := observedRoutes(runWithVisited("http://twin", "http://twin/", "http://twin/orders"))
	require.NotNil(t, got, "exploration ran, so this is EMPTY, not UNAVAILABLE")
	require.Empty(t, got, "no visited route carried a fuzzable query parameter")
}

// TestObservedRoutes_TemplatesAndExtractsParams proves the two value-stripping
// rules at once: a concrete id in the path becomes {id}, and the query yields
// parameter NAMES only, never their values.
func TestObservedRoutes_TemplatesAndExtractsParams(t *testing.T) {
	got := observedRoutes(runWithVisited("http://twin",
		"http://twin/api/orders/123?q=chair&limit=10"))
	require.Equal(t, []security.Route{{
		Method: http.MethodGet,
		Path:   "/api/orders/{id}",
		Params: []string{"limit", "q"},
	}}, got)
	// The values must not appear anywhere in the produced routes.
	for _, r := range got {
		require.NotContains(t, r.Path, "123", "a row id must not survive into a Route")
		for _, p := range r.Params {
			require.NotEqual(t, "chair", p, "a query VALUE must never become a param name")
			require.NotEqual(t, "10", p)
		}
	}
}

// TestObservedRoutes_TemplatesUUIDAndHex proves the other value-shaped segments
// are stripped, and that a genuine route word survives untouched.
func TestObservedRoutes_TemplatesUUIDAndHex(t *testing.T) {
	got := observedRoutes(runWithVisited("http://twin",
		"http://twin/users/2f1c8e90-4b7a-4c21-9f3d-1a2b3c4d5e6f/edit?field=name",
		"http://twin/objects/507f1f77bcf86cd799439011?view=raw",
		"http://twin/search?q=x"))
	paths := map[string]bool{}
	for _, r := range got {
		paths[r.Path] = true
	}
	require.True(t, paths["/users/{id}/edit"], "a UUID segment is templated, the route words survive")
	require.True(t, paths["/objects/{id}"], "a long hex object id is templated")
	require.True(t, paths["/search"], "a plain route word is not templated")
}

// TestObservedRoutes_DedupsAndUnionsParams proves two reaches of the same
// templated route collapse into one, carrying the union of the parameters seen.
func TestObservedRoutes_DedupsAndUnionsParams(t *testing.T) {
	got := observedRoutes(runWithVisited("http://twin",
		"http://twin/api/orders/1?q=a",
		"http://twin/api/orders/2?sort=desc",
		"http://twin/api/orders/3?q=b"))
	require.Len(t, got, 1, "three concrete ids collapse to one templated route")
	require.Equal(t, "/api/orders/{id}", got[0].Path)
	require.Equal(t, []string{"q", "sort"}, got[0].Params, "the params are the sorted union across reaches")
}

// TestObservedRoutes_SkipsAssetsAndNonHTTP proves a stylesheet and a non-http
// scheme are not sourced as routes even when they carry a query.
func TestObservedRoutes_SkipsAssetsAndNonHTTP(t *testing.T) {
	got := observedRoutes(runWithVisited("http://twin",
		"http://twin/app.css?v=2",
		"http://twin/bundle.js?hash=abc",
		"mailto:someone@example.test?subject=hi",
		"http://twin/search?q=x"))
	require.Len(t, got, 1, "only the real route survives")
	require.Equal(t, "/search", got[0].Path)
}

// TestObservedRoutes_SourcesJourneyGotoMoves proves a navigation recorded only
// as a goto move (not a Visited page) is still sourced.
func TestObservedRoutes_SourcesJourneyGotoMoves(t *testing.T) {
	run := &report.Run{
		URL: "http://twin",
		Exploration: &report.Exploration{
			Results: []explore.Exploration{{
				Journey: []explore.Move{
					{Kind: "goto", URL: "http://twin/report?range=week"},
					{Kind: "click", Control: "Submit"},
				},
			}},
		},
	}
	got := observedRoutes(run)
	require.Len(t, got, 1)
	require.Equal(t, "/report", got[0].Path)
	require.Equal(t, []string{"range"}, got[0].Params)
}

// TestObservedRoutes_FeedsInjectionProbeToAFinding is the end-to-end proof of
// the whole seam this lane closes: an OBSERVED route with a query parameter is
// turned into a security.Route by observedRoutes, handed to the REAL injection
// family exactly as the collector hands it, and fuzzed against an endpoint that
// concatenates the parameter into SQL, which emits a proven finding. This is
// the producer -> Input.Routes -> consumer path firing live against HTTP, the
// path that was dead while Routes was nil.
func TestObservedRoutes_FeedsInjectionProbeToAFinding(t *testing.T) {
	srv := sqlInjectableServer()
	defer srv.Close()

	// The exploration reached the injectable route, as the browser would record
	// it: an absolute URL carrying the query parameter.
	run := runWithVisited(srv.URL, srv.URL+"/api/search?q=chair")
	routes := observedRoutes(run)
	require.Equal(t, []security.Route{{
		Method: http.MethodGet, Path: "/api/search", Params: []string{"q"},
	}}, routes, "the observed route is templated and carries the param name only")

	fam := injection.New()
	in := security.Input{
		Env:    security.Environment{BaseURL: run.URL},
		Policy: failOn(fam.Keys()),
	}.WithRunArtifacts(security.RunArtifacts{Routes: routes})

	findings, err := fam.Probe(context.Background(), in)
	require.NoError(t, err, "a wired route source is not blocked")
	require.NotEmpty(t, findings, "the injection family fuzzed the observed route and proved the SQL injection")
	for _, f := range findings {
		require.Equal(t, "/api/search", f.Where, "the finding names the observed route")
		require.NotContains(t, f.Detail, "chair", "no observed value crosses into a finding")
		require.NotContains(t, f.Detail, "pg_sleep", "no payload value crosses into a finding")
	}
}

// TestObservedRoutes_SafeServerFeedsNoFinding is the liveness arm of the seam:
// the identical wiring against a route that escapes its input must produce no
// finding, so the end-to-end path can say no.
func TestObservedRoutes_SafeServerFeedsNoFinding(t *testing.T) {
	srv := reflectingServer()
	defer srv.Close()

	run := runWithVisited(srv.URL, srv.URL+"/api/search?q=chair")
	routes := observedRoutes(run)
	require.NotEmpty(t, routes, "the route is still sourced")

	fam := injection.New()
	in := security.Input{
		Env:    security.Environment{BaseURL: run.URL},
		Policy: failOn(fam.Keys()),
	}.WithRunArtifacts(security.RunArtifacts{Routes: routes})

	findings, err := fam.Probe(context.Background(), in)
	require.NoError(t, err)
	require.Empty(t, findings, "a route that escapes its input proves nothing")
}

// failOn builds a policy that fails on every one of a family's keys, so a
// proven vector becomes a fail finding.
func failOn(specs []security.KeySpec) report.Policy {
	m := map[report.PolicyKey]report.Level{}
	for _, k := range specs {
		m[k.Key] = report.LevelFail
	}
	return report.Policy{Security: m}
}

// sqlInjectableServer concatenates the q parameter into a query, so a stray
// quote breaks the statement and leaks a Postgres error. It is the shape of a
// real SQL-injection-vulnerable endpoint, reduced to what the error-based
// vector proves.
func sqlInjectableServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if strings.Contains(q, "'") {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `ERROR: syntax error at or near "'"`)
			return
		}
		fmt.Fprint(w, "you said: af_control")
	}))
}

// reflectingServer echoes its input verbatim: the worst case that is still not
// exploitable, so a reflected payload must never read as a proven one.
func reflectingServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "you said: %s", r.URL.Query().Get("q"))
	}))
}
