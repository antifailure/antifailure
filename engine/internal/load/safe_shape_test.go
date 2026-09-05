package load_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/load"
)

func TestSafeShapeConcreteReads(t *testing.T) {
	for _, tc := range []struct {
		name   string
		safe   []string
		routes []load.Route
	}{
		{"get", []string{"GET /runs"}, []load.Route{{Method: "GET", Path: "/runs", Weight: 1}}},
		{"head", []string{"HEAD /health"}, []load.Route{{Method: "HEAD", Path: "/health", Weight: 1}}},
		{"methodless", []string{"/runs"}, []load.Route{{Method: "GET", Path: "/runs", Weight: 1}}},
		{"duplicates", []string{"GET /runs", "get /runs", "/runs"}, []load.Route{{Method: "GET", Path: "/runs", Weight: 1}}},
		{"malformed beside valid", []string{"GET bad", "GET /bad path", "GET /bad#fragment", "GET /bad\\path", "", "GET /runs"}, []load.Route{{Method: "GET", Path: "/runs", Weight: 1}}},
		{"bad escape beside valid", []string{"GET /bad%zz", "GET /runs"}, []load.Route{{Method: "GET", Path: "/runs", Weight: 1}}},
		{"absolute url beside valid", []string{"GET http://elsewhere.example/x", "GET /runs"}, []load.Route{{Method: "GET", Path: "/runs", Weight: 1}}},
		{"lowercase method", []string{"get /runs"}, []load.Route{{Method: "GET", Path: "/runs", Weight: 1}}},
		{"writes and globs", []string{"POST /charge", "DELETE /runs", "GET /runs/*", "GET /runs"}, []load.Route{{Method: "GET", Path: "/runs", Weight: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shape, _ := load.ShapeFromSafeRoutes(tc.safe)
			require.Equal(t, tc.routes, shape.Routes)
		})
	}
}

func TestLoadSmokeMissingPageIsAnError(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	for _, tc := range []struct {
		source    string
		errorRate float64
	}{
		{"safe_routes", 1}, {"default", 1}, {"access_log", 0},
	} {
		t.Run(tc.source, func(t *testing.T) {
			res, err := load.Run(context.Background(), load.Options{BaseURL: server.URL, Shape: load.Shape{Source: tc.source, RequestsPerSecond: 500, Routes: []load.Route{{Method: "GET", Path: "/missing", Weight: 1}}}, Duration: 100 * time.Millisecond})
			require.NoError(t, err)
			require.Equal(t, tc.errorRate, res.ErrorRate)
		})
	}
}

func TestLoadCannotFollowAnUnapprovedRedirect(t *testing.T) {
	for _, kind := range []string{"mix", "scenario"} {
		t.Run(kind, func(t *testing.T) {
			var outside atomic.Int64
			var inside atomic.Int64
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { outside.Add(1); w.WriteHeader(http.StatusOK) }))
			defer destination.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				inside.Add(1)
				http.Redirect(w, r, destination.URL, http.StatusFound)
			}))
			defer server.Close()
			var err error
			if kind == "mix" {
				_, err = load.Run(context.Background(), load.Options{BaseURL: server.URL, Shape: load.DefaultShape(), Scale: 100, Duration: 100 * time.Millisecond})
			} else {
				_, err = load.RunScenarios(context.Background(), load.ScenarioOptions{
					BaseURL: server.URL, SafeRoutes: []string{"GET /"},
					Runs: []load.ScenarioRun{{Scenario: mustParse(t, "scenario: redirect\nsteps:\n  - request: GET /\n"), Sessions: 1, Iterations: 1}},
				})
			}
			require.NoError(t, err)
			require.Positive(t, inside.Load())
			require.Zero(t, outside.Load())
		})
	}
}

func TestSafeShapeFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		routes []string
	}{
		{"empty", nil}, {"glob", []string{"GET /**"}}, {"write", []string{"POST /charge"}}, {"malformed", []string{"bad"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := load.ShapeFromSafeRoutes(tc.routes)
			require.False(t, ok)
		})
	}
}

func TestSafeShapeSendsDeclaredReads(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.Method+" "+r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	shape, _ := load.ShapeFromSafeRoutes([]string{"GET /runs", "HEAD /health", "POST /charge", "GET /blocked"})
	shape, _ = shape.Safe([]string{"/**"}, []string{"GET /blocked"})
	result, err := load.Run(context.Background(), load.Options{BaseURL: server.URL, Shape: shape, Scale: 100, Duration: 300 * time.Millisecond, Seed: 5})
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.Positive(t, hits["GET /runs"])
	require.Positive(t, hits["HEAD /health"])
	require.Zero(t, hits["POST /charge"])
	require.Zero(t, hits["GET /blocked"])
	require.Equal(t, hits["GET /runs"]+hits["HEAD /health"], result.Sent)
	require.Equal(t, "safe_routes", result.Source)
	require.Equal(t, float64(500), result.TargetRate)
}
