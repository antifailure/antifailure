package load_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/load"
)

func TestSafe_TreatsEveryRouteAsUnsafeUntilTold(t *testing.T) {
	t.Parallel()
	// A generator that discovers POST /checkout in an access log and exercises
	// it four hundred times is a generator that charges four hundred cards.
	shape := load.Shape{Routes: []load.Route{
		{Method: "GET", Path: "/", Weight: 10},
		{Method: "GET", Path: "/api/items", Weight: 5},
		{Method: "POST", Path: "/api/checkout", Weight: 1},
		{Method: "DELETE", Path: "/api/items/1", Weight: 1},
	}}

	kept, refused := shape.Safe([]string{"GET /**"}, nil)
	require.Len(t, kept.Routes, 2)
	require.Len(t, refused, 2, "anything that writes is left out unless named")

	none, allRefused := shape.Safe(nil, nil)
	require.Empty(t, none.Routes, "with no safe list, nothing is sent")
	require.Len(t, allRefused, 4)
}

func TestSafe_AnUnsafePatternOverridesASafeOne(t *testing.T) {
	t.Parallel()
	shape := load.Shape{Routes: []load.Route{
		{Method: "GET", Path: "/api/items", Weight: 1},
		{Method: "GET", Path: "/api/export", Weight: 1},
	}}
	kept, refused := shape.Safe([]string{"GET /api/**"}, []string{"GET /api/export"})
	require.Len(t, kept.Routes, 1)
	require.Equal(t, "/api/items", kept.Routes[0].Path)
	require.Len(t, refused, 1)
}

func TestSafe_AMethodPatternDoesNotCoverAnotherMethod(t *testing.T) {
	t.Parallel()
	// A pattern meant to allow reads must not allow the delete that shares
	// its path.
	shape := load.Shape{Routes: []load.Route{
		{Method: "GET", Path: "/api/items/1", Weight: 1},
		{Method: "DELETE", Path: "/api/items/1", Weight: 1},
	}}
	kept, _ := shape.Safe([]string{"GET /api/items/*"}, nil)
	require.Len(t, kept.Routes, 1)
	require.Equal(t, "GET", kept.Routes[0].Method)
}

func TestPicker_IsDeterministicForASeed(t *testing.T) {
	t.Parallel()
	// Two runs of one shape must send the same sequence, or a comparison
	// between them compares two different traffic mixes rather than the
	// application.
	routes := []load.Route{
		{Method: "GET", Path: "/a", Weight: 5},
		{Method: "GET", Path: "/b", Weight: 1},
	}
	first, err := load.NewPicker(routes, 42)
	require.NoError(t, err)
	second, err := load.NewPicker(routes, 42)
	require.NoError(t, err)

	for i := 0; i < 50; i++ {
		require.Equal(t, first.Next(), second.Next())
	}
}

func TestPicker_OrderOfRoutesDoesNotChangeTheSequence(t *testing.T) {
	t.Parallel()
	forward := []load.Route{{Method: "GET", Path: "/a", Weight: 3}, {Method: "GET", Path: "/b", Weight: 1}}
	backward := []load.Route{{Method: "GET", Path: "/b", Weight: 1}, {Method: "GET", Path: "/a", Weight: 3}}

	a, err := load.NewPicker(forward, 7)
	require.NoError(t, err)
	b, err := load.NewPicker(backward, 7)
	require.NoError(t, err)
	for i := 0; i < 30; i++ {
		require.Equal(t, a.Next(), b.Next(),
			"two sources that agree about the shape must generate the same traffic")
	}
}

func TestPicker_RespectsTheWeights(t *testing.T) {
	t.Parallel()
	// The mix is the whole point: the page nobody thinks about that is nine
	// percent of requests is what breaks.
	p, err := load.NewPicker([]load.Route{
		{Method: "GET", Path: "/hot", Weight: 90},
		{Method: "GET", Path: "/cold", Weight: 10},
	}, 1)
	require.NoError(t, err)

	hot := 0
	const n = 4000
	for i := 0; i < n; i++ {
		if p.Next().Path == "/hot" {
			hot++
		}
	}
	require.InDelta(t, 0.9, float64(hot)/n, 0.03)
}

func TestPicker_ARouteWithNoWeightIsStillSent(t *testing.T) {
	t.Parallel()
	// Dropping it would silently remove an endpoint somebody listed.
	p, err := load.NewPicker([]load.Route{
		{Method: "GET", Path: "/listed", Weight: 0},
	}, 1)
	require.NoError(t, err)
	require.Equal(t, "/listed", p.Next().Path)
}

func TestPicker_RefusesAnEmptyShape(t *testing.T) {
	t.Parallel()
	_, err := load.NewPicker(nil, 1)
	require.Error(t, err)
}

func TestInterval_IsBurstyRatherThanEven(t *testing.T) {
	t.Parallel()
	// A perfectly even stream never fills a connection pool. A system that
	// survives even traffic and falls over on bursty traffic passes a uniform
	// test and fails in production, which is the failure this exists to catch.
	p, err := load.NewPicker([]load.Route{{Method: "GET", Path: "/", Weight: 1}}, 3)
	require.NoError(t, err)

	var total time.Duration
	seen := map[time.Duration]bool{}
	const n = 500
	for i := 0; i < n; i++ {
		d := p.Interval(100)
		total += d
		seen[d.Round(time.Millisecond)] = true
	}
	require.Greater(t, len(seen), 5, "the gaps vary rather than being identical")
	mean := total / n
	require.InDelta(t, float64(10*time.Millisecond), float64(mean), float64(4*time.Millisecond))
	require.Zero(t, p.Interval(0), "no rate means no waiting")
}

func TestRun_MeasuresARealServer(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		require.Equal(t, "1", r.Header.Get("X-Antifailure-Load"),
			"generated traffic is marked, so an access log can tell it apart")
		if r.URL.Path == "/slow" {
			time.Sleep(25 * time.Millisecond)
		}
		if r.URL.Path == "/broken" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	res, err := load.Run(context.Background(), load.Options{
		BaseURL: server.URL,
		Shape: load.Shape{
			RequestsPerSecond: 200,
			Routes: []load.Route{
				{Method: "GET", Path: "/", Weight: 6, P95Ms: 5},
				{Method: "GET", Path: "/slow", Weight: 3, P95Ms: 5},
				{Method: "GET", Path: "/broken", Weight: 1},
			},
		},
		Duration: 700 * time.Millisecond, Concurrency: 8, Seed: 5, Clock: clock.New(),
	})
	require.NoError(t, err)
	require.Positive(t, res.Sent)
	require.EqualValues(t, res.Sent, hits.Load())
	require.Positive(t, res.Rate)
	require.Positive(t, res.Overall.P95Ms)

	byRoute := map[string]load.RouteResult{}
	for _, r := range res.Routes {
		byRoute[r.Route] = r
	}
	require.Greater(t, byRoute["GET /slow"].Latency.P95Ms, byRoute["GET /"].Latency.P95Ms,
		"the slow route measures slower")

	// A 500 is an error, and it is counted against the route that returned it.
	require.Positive(t, res.ErrorRate)
	require.Positive(t, byRoute["GET /broken"].Errors)
	require.Contains(t, res.Errors, "500")

	// The worst regression sorts first, because that is the line somebody is
	// looking for and scrolling to find it is the same as not showing it.
	require.Equal(t, "GET /slow", res.Routes[0].Route)
	require.True(t, res.Routes[0].HasBaseline)
	require.Greater(t, res.Routes[0].P95Increase, 1.0)
}

func TestBreaches_ARouteWithNoBaselineIsNeverABreach(t *testing.T) {
	t.Parallel()
	// Comparing against nothing and calling the answer a regression is how a
	// check becomes noise that people turn off.
	res := &load.Result{
		ErrorRate: 0.01,
		Routes: []load.RouteResult{
			{Route: "GET /new", Latency: load.Latency{P95Ms: 900}, HasBaseline: false},
			{Route: "GET /old", Latency: load.Latency{P95Ms: 40}, BaselineP95Ms: 10,
				HasBaseline: true, P95Increase: 3},
		},
	}
	breaches := res.Breaches(0.5, 0.05)
	require.Len(t, breaches, 1)
	require.Equal(t, "GET /old", breaches[0].What)
	require.Contains(t, breaches[0].Detail, "baseline of 10ms")

	require.Empty(t, res.Breaches(0, 0), "no thresholds means no breaches")
}

func TestInertP95_AThresholdThatMeasuredNothingIsNotACleanRun(t *testing.T) {
	t.Parallel()
	// Breaches is right to skip a route with no baseline, and silence is the
	// wrong answer when it skipped all of them: the threshold was in force,
	// evaluated zero routes, and the run reported green having compared
	// nothing.
	none := &load.Result{Routes: []load.RouteResult{
		{Route: "GET /", Latency: load.Latency{P95Ms: 900}, HasBaseline: false},
		{Route: "GET /orders", Latency: load.Latency{P95Ms: 40}, HasBaseline: false},
	}}
	require.Empty(t, none.Breaches(0.25, 0), "nothing to compare, so nothing is a breach")
	require.True(t, none.InertP95(0.25))

	// One baseline is enough for the threshold to have done something, even
	// when it found nothing wrong. That is a real pass, not a silent skip.
	some := &load.Result{Routes: []load.RouteResult{
		{Route: "GET /", Latency: load.Latency{P95Ms: 900}, HasBaseline: false},
		{Route: "GET /orders", Latency: load.Latency{P95Ms: 40}, BaselineP95Ms: 38,
			HasBaseline: true, P95Increase: 0.05},
	}}
	require.False(t, some.InertP95(0.25))

	// No threshold means nothing was asked for, so nothing went unmeasured.
	require.False(t, none.InertP95(0))

	// A run that sent nothing has a louder problem than an unmeasured
	// threshold, and reporting both would bury it.
	require.False(t, (&load.Result{}).InertP95(0.25))
}

func TestBreaches_ReportsAnErrorRateOverTheLimit(t *testing.T) {
	t.Parallel()
	res := &load.Result{ErrorRate: 0.2}
	breaches := res.Breaches(0, 0.05)
	require.Len(t, breaches, 1)
	require.Contains(t, breaches[0].Detail, "20.0 percent")
}

func TestRun_StopsWhenTheContextIsCancelled(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	// A run that ignored cancellation would keep generating load against an
	// environment somebody is trying to tear down.
	started := time.Now()
	res, err := load.Run(ctx, load.Options{
		BaseURL:  server.URL,
		Shape:    load.Shape{RequestsPerSecond: 50, Routes: []load.Route{{Method: "GET", Path: "/", Weight: 1}}},
		Duration: 30 * time.Second, Seed: 1, Clock: clock.New(),
	})
	require.Error(t, err)
	require.NotNil(t, res, "what it measured before stopping still comes back")
	require.Less(t, time.Since(started), 5*time.Second)
}

func TestFromAccessLog_ProducesAMixRatherThanAList(t *testing.T) {
	t.Parallel()
	// Without normalising, a log of a hundred thousand requests produces a
	// hundred thousand routes each with a weight of one, and the mix that was
	// the whole point is gone.
	lines := []string{
		`1.2.3.4 - - [01/Jun/2026:12:00:00 +0000] "GET /users/4821 HTTP/1.1" 200 12`,
		`1.2.3.4 - - [01/Jun/2026:12:00:01 +0000] "GET /users/9130 HTTP/1.1" 200 12`,
		`1.2.3.4 - - [01/Jun/2026:12:00:02 +0000] "GET /users/7 HTTP/1.1" 200 12`,
		`1.2.3.4 - - [01/Jun/2026:12:00:03 +0000] "GET /search?q=shoes HTTP/1.1" 200 12`,
		`1.2.3.4 - - [01/Jun/2026:12:00:04 +0000] "GET /search?q=hats HTTP/1.1" 200 12`,
		`1.2.3.4 - - [01/Jun/2026:12:00:05 +0000] "POST /api/orders HTTP/1.1" 201 4`,
		`garbage that is not a log line`,
	}
	shape := load.FromAccessLog(lines)
	require.Equal(t, "access_log", shape.Source)

	byRoute := map[string]float64{}
	for _, r := range shape.Routes {
		byRoute[r.String()] = r.Weight
	}
	require.Equal(t, 3.0, byRoute["GET /users/{id}"], "three identifiers, one route")
	require.Equal(t, 2.0, byRoute["GET /search"], "the query is dropped, or every search is a route")
	require.Equal(t, 1.0, byRoute["POST /api/orders"])
	require.Len(t, shape.Routes, 3, "the unparseable line is skipped rather than counted")
	require.Equal(t, "GET /users/{id}", shape.Routes[0].String(), "the busiest route is first")
}

func TestNormalisePath_CollapsesIdentifiersAndNothingElse(t *testing.T) {
	t.Parallel()
	require.Equal(t, "/users/{id}", load.NormalisePath("/users/4821"))
	require.Equal(t, "/orders/{id}/items", load.NormalisePath(
		"/orders/9f8e7d6c-5b4a-3210-9f8e-7d6c5b4a3210/items"))
	// Words are not identifiers, or the shape collapses to /{id}/{id}.
	require.Equal(t, "/api/products/featured", load.NormalisePath("/api/products/featured"))
	require.Equal(t, "/", load.NormalisePath("/"))
}
