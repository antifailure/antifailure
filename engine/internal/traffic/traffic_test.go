package traffic_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/traffic"
)

// The arithmetic that decides whether a run exercises production, and the
// sentence it produces.
//
// Every case here is written from the failure measured on this repository on
// 2026-09-06: four hand written safe_routes ran through thirty seconds of an
// exclusive lock on nine relations and reported 0.0 percent failed, because
// none of the four reads the locked table. The numbers below are that case and
// the ones either side of it.

func collected() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) }

func profile(routes ...traffic.Route) traffic.Profile {
	p := traffic.Profile{
		CollectedAt: collected(),
		Source:      "otel export telemetry/traces.json",
		From:        collected().Add(-24 * time.Hour),
		To:          collected(),
		Routes:      routes,
	}
	for _, r := range routes {
		p.Requests += r.Requests
	}
	return p
}

func route(method, path string, requests int64) traffic.Route {
	return traffic.Route{Method: method, Path: path, Requests: requests}
}

func sent(pairs ...string) []traffic.Endpoint {
	out := make([]traffic.Endpoint, 0, len(pairs))
	for _, p := range pairs {
		method, path := p[:index(p, ' ')], p[index(p, ' ')+1:]
		out = append(out, traffic.Endpoint{Method: method, Path: path})
	}
	return out
}

func index(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func TestCompare_TheRouteThatReadsTheLockedTableIsNamed(t *testing.T) {
	t.Parallel()
	// The 2026-09-06 shape. Four routes are sent, the heaviest route in
	// production is not one of them, and the run reported nothing wrong.
	c := traffic.Compare(
		sent("GET /environments", "GET /runs", "GET /audit", "GET /network"),
		profile(
			route("GET", "/events", 840_000),
			route("POST", "/events/ingest", 610_000),
			route("GET", "/runs", 12_000),
			route("GET", "/environments", 4_100),
			route("GET", "/audit", 900),
			route("GET", "/network", 120),
		))

	require.False(t, c.Covers(),
		"a run that never sends the two heaviest routes in production was called complete")
	share, ok := c.Share()
	require.True(t, ok)
	require.InDelta(t, 17_120.0/1_467_120.0, share, 1e-9)
	require.Equal(t, "1.1 percent", traffic.Percent(share))

	missed := c.Uncovered()
	require.Len(t, missed, 2)
	require.Equal(t, "GET /events", missed[0].Route.String(),
		"the heaviest route the run never sends has to be the one named first")
	require.Contains(t, c.Describe(), "this run sends 4 of the 6 routes production served")
	require.Contains(t, c.Describe(), "The heaviest it never sends is GET /events")
	require.Contains(t, c.Describe(), "carrying 1.1 percent of its requests")
}

func TestCompare_ARunThatSendsEverythingProductionLeansOnCovers(t *testing.T) {
	t.Parallel()
	c := traffic.Compare(
		sent("GET /events", "POST /events/ingest", "GET /runs"),
		profile(
			route("GET", "/events", 840_000),
			route("POST", "/events/ingest", 610_000),
			route("GET", "/runs", 12_000),
		))

	require.True(t, c.Covers())
	require.Empty(t, c.Uncovered())
	share, ok := c.Share()
	require.True(t, ok)
	require.Equal(t, 1.0, share)
}

func TestCompare_ARouteBelowTheNoiseFloorDoesNotDecideTheVerdict(t *testing.T) {
	t.Parallel()
	// A crawler asked for it four times in a week. Requiring every such route
	// to be exercised would fail every real profile forever, and a check that
	// can never pass is one somebody widens until it can never fail.
	c := traffic.Compare(
		sent("GET /events"),
		profile(
			route("GET", "/events", 1_000_000),
			route("GET", "/.well-known/security.txt", 4),
		))

	require.True(t, c.Covers(),
		"four requests in a million decided the verdict")
	require.Len(t, c.Uncovered(), 1,
		"a route below the floor still has to be named, it just does not fail the run")
}

func TestCompare_ARouteAtTheFloorDoesDecideTheVerdict(t *testing.T) {
	t.Parallel()
	// One request in a thousand exactly. The boundary is a fact worth pinning,
	// because it is the only thing separating the case above from a real
	// finding.
	c := traffic.Compare(
		sent("GET /events"),
		profile(
			route("GET", "/events", 999_000),
			route("GET", "/exports", 1_000),
		))

	require.False(t, c.Covers(),
		"a route carrying exactly one request in a thousand was treated as noise")
}

// Two concrete paths a manifest names are one route, so a route production
// does not have is named once rather than once per identifier somebody typed.
func TestCompare_AnInventedRouteIsNamedOnceHoweverManyPathsReachIt(t *testing.T) {
	t.Parallel()
	c := traffic.Compare(
		sent("GET /invented/1", "GET /invented/2", "GET /invented/3"),
		profile(route("GET", "/events", 1_000)))
	require.Equal(t, []traffic.Endpoint{{Method: "GET", Path: "/invented/{id}"}}, c.Invented)
}

func TestCompare_ARouteTheRunSendsAndProductionNeverServedIsNamed(t *testing.T) {
	t.Parallel()
	// The other half of the same defect. A hand written list is not only thin,
	// it also spends the run's budget on pages production does not have.
	c := traffic.Compare(
		sent("GET /events", "GET /invented"),
		profile(route("GET", "/events", 1_000)))

	require.Equal(t, []traffic.Endpoint{{Method: "GET", Path: "/invented"}}, c.Invented)
	require.Contains(t, c.Describe(), "It also sends GET /invented, which production never served")
	require.Equal(t, int64(1_000), c.Covered,
		"a route production never served must not be counted as covering anything")
}

func TestCompare_AnIdentifierInAPathIsTheSameRouteOnBothSides(t *testing.T) {
	t.Parallel()
	// A manifest names a concrete URL because a glob cannot be sent, and
	// telemetry names the template. Matching them literally would report the
	// route as both never sent and invented, and both halves would be wrong.
	c := traffic.Compare(
		sent("GET /runs/4821"),
		profile(route("GET", "/runs/{id}", 5_000)))

	require.Empty(t, c.Uncovered())
	require.Empty(t, c.Invented)
	require.True(t, c.Covers())
}

func TestCompare_AMethodIsPartOfTheRoute(t *testing.T) {
	t.Parallel()
	// A run that reads a collection has not exercised the write to it, and the
	// write is the one that takes the lock.
	c := traffic.Compare(
		sent("GET /events"),
		profile(
			route("GET", "/events", 1_000),
			route("POST", "/events", 1_000),
		))

	require.False(t, c.Covers())
	require.Equal(t, "POST /events", c.Uncovered()[0].Route.String())
}

func TestCompare_AProfileThatCountedNothingIsNotAScore(t *testing.T) {
	t.Parallel()
	c := traffic.Compare(sent("GET /events"), traffic.Profile{CollectedAt: collected()})
	_, ok := c.Share()
	require.False(t, ok, "a comparison against nothing reported a share")
	require.False(t, c.Covers())
	require.Contains(t, c.Describe(), "counted no request")
}

func TestRate_IsCountedOverTheWindowRatherThanAssumed(t *testing.T) {
	t.Parallel()
	p := traffic.Profile{
		CollectedAt: collected(),
		From:        collected().Add(-2 * time.Hour),
		To:          collected(),
		Requests:    720_000,
		Routes:      []traffic.Route{route("GET", "/events", 720_000)},
	}
	rate, ok := p.Rate()
	require.True(t, ok)
	require.InDelta(t, 100.0, rate, 1e-9)
}

func TestRate_AWindowTooShortToDivideByIsNotARate(t *testing.T) {
	t.Parallel()
	p := traffic.Profile{CollectedAt: collected(), Requests: 40}
	_, ok := p.Rate()
	require.False(t, ok, "a profile with no window reported a rate")
}

func TestRateComparison_ARunAtTwoPercentOfProductionIsNotProduction(t *testing.T) {
	t.Parallel()
	cmp := traffic.RateComparison{Run: 5, Production: 240, PeakConcurrency: 90}
	require.False(t, cmp.Reaches())
	share, ok := cmp.Share()
	require.True(t, ok)
	require.InDelta(t, 5.0/240.0, share, 1e-9)
	require.Contains(t, cmp.Describe(), "5.0 requests a second against production's 240 requests a second")
	require.Contains(t, cmp.Describe(), "Production had 90 requests in flight at once at its peak")
}

func TestRateComparison_ARunAtProductionsRateReachesIt(t *testing.T) {
	t.Parallel()
	require.True(t, traffic.RateComparison{Run: 240, Production: 240}.Reaches())
	require.True(t, traffic.RateComparison{Run: 480, Production: 240}.Reaches(),
		"a run faster than production is a harder test, not a failed one")
	require.False(t, traffic.RateComparison{Run: 239, Production: 240}.Reaches(),
		"there is no tolerance below production's rate, because half the rate is half the contention")
}

func TestFind_MatchesTheWayTheComparisonDoes(t *testing.T) {
	t.Parallel()
	p := profile(traffic.Route{Method: "GET", Path: "/runs/{id}", Requests: 10, P95Ms: 41})
	got, ok := p.Find("get", "/runs/4821")
	require.True(t, ok, "the baseline lookup and the coverage comparison have to agree")
	require.Equal(t, 41.0, got.P95Ms)
}
