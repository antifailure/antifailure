package workload_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/workload"
)

// Two real builds, two real servers, real traffic, and the verdict at the end.
//
// The unit tests beside this one hand Judge a Comparison built by hand, which
// is the right way to prove the arithmetic of every unmeasurable case. What
// they cannot prove is that the whole path holds together: that a build which
// is genuinely slower produces a load result whose percentiles differ, that
// the projection carries those percentiles into a document Compare can read,
// that Compare attributes the difference to the right route, and that Judge
// turns it into a failing verdict. Every one of those steps is a place a
// comparison can silently become a comparison of nothing.
//
// So these send real HTTP at two real servers, one of which sleeps. The
// numbers are measured rather than asserted into existence, which is also why
// the margins are wide: the point is a forty fold difference being caught, not
// a millisecond being resolved.
//
// What this does NOT cover, said here rather than implied: bringing the second
// environment up from a base revision. That is Orchestrator.LoadCompare, it is
// the oracle's baseline mechanism reused, and it needs Docker and a golden.
//
// ONE threshold line for both arms, and it is declared here rather than per
// test so that nobody can accuse these of being tuned twice. The regression
// arm must fail it and the identical arm must pass it. The numbers it sits
// between were measured rather than guessed, by the noise floor test beside
// this one: two builds of identical code differ by up to 52 percent at the
// tail over a run this length, and the deliberate regression moves the same
// number by about 515 percent. The line goes in the order of magnitude
// between them.

// slowRoutes maps a path to how long that route takes to answer.
func buildServer(t *testing.T, slowRoutes map[string]time.Duration) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d, ok := slowRoutes[r.URL.Path]; ok {
			time.Sleep(d)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(s.Close)
	return s
}

// proofThresholds sit above this machine's measured noise floor and an order
// of magnitude below the deliberate regression.
func proofThresholds() workload.ComparisonThresholds {
	return workload.ComparisonThresholds{P95Increase: 1.0, ThroughputDrop: 0.25}
}

func mixShape() load.Shape {
	return load.Shape{
		Source:            "otel",
		RequestsPerSecond: 200,
		Routes: []load.Route{
			{Method: "GET", Path: "/orders", Weight: 1},
			{Method: "GET", Path: "/health", Weight: 1},
		},
	}
}

// sendMix runs one side. The SAME seed and the SAME duration on both sides is
// the whole point, so they are parameters of the test rather than of this.
func sendMix(t *testing.T, url string, concurrency int) *load.Result {
	return sendMixFor(t, url, concurrency, 2*time.Second)
}

func sendMixFor(t *testing.T, url string, concurrency int, d time.Duration) *load.Result {
	return sendMixAt(t, url, concurrency, d, mixShape())
}

// sendMixAt takes the shape too, so a test that needs enough samples to place
// a limit can ask for them rather than hoping the default is dense enough.
func sendMixAt(
	t *testing.T, url string, concurrency int, d time.Duration, shape load.Shape,
) *load.Result {
	t.Helper()
	res, err := load.Run(context.Background(), load.Options{
		BaseURL:     url,
		Shape:       shape,
		Scale:       1,
		Duration:    d,
		Concurrency: concurrency,
		Seed:        20260920,
		Clock:       clock.New(),
	})
	require.NoError(t, err)
	require.Positive(t, res.Sent, "a side that sent nothing measures nothing")
	return res
}

func compareSides(t *testing.T, base, cand *load.Result) *workload.Comparison {
	t.Helper()
	baseline := workload.ProjectLoad(base, workload.ProjectLoadOptions{
		Branch: "main", Command: "af load run", ManifestDigest: "sha256:same",
	})
	candidate := workload.ProjectLoad(cand, workload.ProjectLoadOptions{
		Branch: "slower", Command: "af load run", ManifestDigest: "sha256:same",
	})
	c, err := workload.Compare(baseline, candidate)
	require.NoError(t, err)
	return c
}

func routeRow(t *testing.T, c *workload.Comparison, name string) workload.RouteDifference {
	t.Helper()
	for _, r := range c.Routes {
		if r.Route == name {
			return r
		}
	}
	t.Fatalf("no route row for %s", name)
	return workload.RouteDifference{}
}

func TestARealBuildThatGotSlowerFailsTheBaseBranchLatencyThreshold(t *testing.T) {
	// A real sleep on one route of a real server, which is the regression a
	// person would introduce by adding a query inside a loop.
	//
	// Both sides take 5ms to answer /health. Answered immediately, its p95 was
	// a fraction of a millisecond and on windows-latest it moved by more than
	// the declared doubling in 5 runs of 20 with nothing changed, 0.3ms to
	// 1.4ms at worst, so "exactly one route breached" failed on a neighbour
	// that only jittered. A floor of 5ms puts that jitter at a fifth of the
	// limit rather than past it, and is the same on both sides, so it is not
	// a regression of its own.
	neighbour := 5 * time.Millisecond
	base := buildServer(t, map[string]time.Duration{"/health": neighbour})
	// A hundred milliseconds, sized against the slowest floor measured rather
	// than the fastest. The base side's p95 is almost all scheduling: 7ms to
	// 26ms on a contended Mac, and 28.9ms on windows-latest, where a 40ms
	// sleep moved p95 to 42.8ms, +48 per cent, under the declared doubling.
	// Against 29ms this is more than four times the floor.
	cand := buildServer(t, map[string]time.Duration{"/orders": 100 * time.Millisecond, "/health": neighbour})

	// Fifty in flight rather than twenty. A hundred requests a second to a
	// route that takes a tenth of a second keeps about ten in flight, and
	// under a ceiling of twenty its bursts queued /health behind it: on
	// windows-latest /health went from 0.5ms to 1.8ms and breached on its own,
	// which made "exactly one route breached" a question about the generator's
	// ceiling rather than about attribution.
	baseRes := sendMix(t, base.URL, 50)
	candRes := sendMix(t, cand.URL, 50)
	c := compareSides(t, baseRes, candRes)

	// The regression is attributed to the route that actually slowed down, and
	// the route beside it is not dragged along with it.
	orders := routeRow(t, c, "GET /orders")
	health := routeRow(t, c, "GET /health")
	// Logged before the assertions rather than after them, so the numbers are
	// there on the run that fails, which is the only run anybody reads them on.
	for _, r := range []workload.RouteDifference{orders, health} {
		if r.P95Ratio != nil {
			t.Logf("%s: base p95 %.1fms, candidate p95 %.1fms, ratio %+.1f%%",
				r.Route, *r.P95Baseline, *r.P95Candidate, *r.P95Ratio*100)
		}
	}
	require.Equal(t, workload.DirectionWorse, orders.Direction)
	require.NotNil(t, orders.P95Ratio)
	// Against the DECLARED limit rather than against a tighter number of its
	// own. This assertion read "more than fourfold" and flaked on a contended
	// machine: the base side is a server that answers immediately, so its p95
	// is almost all scheduling, and it was measured anywhere between 7ms and
	// 26ms for identical work. The sleep that was 40ms then was a sixfold
	// regression against the low reading and barely a doubling against the
	// high one, and fell under it once on Windows, which is why it is now
	// 100ms. Asserting a ratio the machine controls, rather than the verdict
	// this code decides, is testing the laptop.
	require.Greater(t, *orders.P95Ratio, proofThresholds().P95Increase,
		"the regression must clear the limit the test declares, whatever the host noise")

	// The untouched route moves too, and that is a real measurement rather
	// than a defect in the comparison. Both routes share the generator's
	// concurrency ceiling, so requests for /health queue behind the sleeping
	// /orders requests and their measured latency includes that wait. A slow
	// endpoint really does slow its neighbours down when they share a pool,
	// which is one of the things a load run exists to show.
	//
	// So the claim worth asserting is attribution, not innocence: the route
	// that changed carries the breach, and it moved by an order of magnitude
	// more than the one that did not.
	require.NotNil(t, health.P95Ratio)
	require.Greater(t, *orders.P95Ratio, *health.P95Ratio*3,
		"the route that actually slowed down must move far more than the "+
			"neighbour that only queued behind it")

	rows := workload.Judge(c, workload.ComparisonThresholds{
		P95Increase: proofThresholds().P95Increase,
	})
	require.Equal(t, workload.VerdictFail, workload.ComparisonOutcome(rows))

	breaches := workload.ComparisonBreaches(rows)
	require.Len(t, breaches, 1, "exactly one route breached")
	require.Equal(t, "GET /orders", breaches[0].Scope)
	require.Contains(t, breaches[0].Detail, "slower against a limit")

}

func TestARealBuildThatGotSlowerFailsTheThroughputThreshold(t *testing.T) {
	// The number nothing in this product has ever compared. A tight
	// concurrency ceiling is what turns a slower response into fewer requests
	// served: the generator aims at the same rate on both sides and the slow
	// side cannot keep up, which is the queue growing.
	base := buildServer(t, nil)
	cand := buildServer(t, map[string]time.Duration{
		"/orders": 60 * time.Millisecond,
		"/health": 60 * time.Millisecond,
	})

	// A concurrency ceiling of two against a sixty millisecond response caps
	// the slow side near thirty requests a second whatever rate is asked for,
	// while the fast side serves the whole mix. The gap is deliberately an
	// order of magnitude clear of the measured floor.
	baseRes := sendMix(t, base.URL, 2)
	candRes := sendMix(t, cand.URL, 2)
	require.Less(t, candRes.Rate, baseRes.Rate, "the slow side must actually have served fewer")

	c := compareSides(t, baseRes, candRes)
	rows := workload.Judge(c, workload.ComparisonThresholds{
		ThroughputDrop: proofThresholds().ThroughputDrop,
	})
	require.Equal(t, workload.VerdictFail, workload.ComparisonOutcome(rows))
	require.Equal(t, "throughput_drop", rows[0].Name)
	require.Greater(t, *rows[0].Observed, proofThresholds().ThroughputDrop)

	t.Logf("base %.1f req/s, candidate %.1f req/s, drop %.1f%%",
		baseRes.Rate, candRes.Rate, *rows[0].Observed*100)
}

// denseShape sends the same two routes far harder, so a run of a few seconds
// puts thousands of samples in each tail rather than a hundred.
func denseShape() load.Shape {
	s := mixShape()
	s.RequestsPerSecond = 600
	return s
}

func TestTwoIdenticalBuildsNeverReadAsARegression(t *testing.T) {
	// Without this arm the two above prove nothing: a threshold that fires on
	// a real regression and also fires on two identical builds is a check that
	// always says no, which is as useless as one that can never say no.
	//
	// Judged round against round, which is how `af load compare` judges a
	// comparison and the only method in this package that measures the noise
	// BETWEEN runs. This test used to judge one pooled run of each side by the
	// single run band, which compareresolution_rounds.go records as describing
	// only the noise inside a run, and that is the claim windows-latest broke:
	// two identical builds read GET /health, a sub-millisecond route, as plus
	// 257.7 percent against a band of 89.4. Thirty runs on windows-latest put
	// that route's identical build ratio anywhere from minus 54 to plus 61
	// percent while the band read 2 to 28, and the host's sleep granularity
	// there is about half a millisecond, which is longer than the route takes.
	// The single run band was being asked a question its own file says it
	// cannot answer, and it answered on this machine only by the margin
	// between its noise and the limit.
	base := buildServer(t, map[string]time.Duration{"/orders": 5 * time.Millisecond})
	cand := buildServer(t, map[string]time.Duration{"/orders": 5 * time.Millisecond})

	// Six rounds, each side back to back inside a round as af load compare
	// sends them, so a round's pair shares whatever the host was doing.
	const rounds = 6
	var baseParts, candParts []*load.Result
	var pairs []workload.RoundP95
	perRoute := func(r *load.Result) map[string]float64 {
		out := map[string]float64{}
		for _, rr := range r.Routes {
			if rr.Latency.P95Ms > 0 {
				out[workload.UnitKey("", rr.Route)] = rr.Latency.P95Ms
			}
		}
		return out
	}
	for k := 0; k < rounds; k++ {
		b := sendMixAt(t, base.URL, 40, time.Second, denseShape())
		c := sendMixAt(t, cand.URL, 40, time.Second, denseShape())
		baseParts, candParts = append(baseParts, b), append(candParts, c)
		pairs = append(pairs, workload.RoundP95{Base: perRoute(b), Candidate: perRoute(c)})
	}
	baseRes, err := load.Merge(baseParts...)
	require.NoError(t, err)
	candRes, err := load.Merge(candParts...)
	require.NoError(t, err)
	c := compareSides(t, baseRes, candRes)
	workload.ResolveByRounds(c, pairs)

	rows := workload.Judge(c, proofThresholds())
	outcome := workload.ComparisonOutcome(rows)
	breaches := workload.ComparisonBreaches(rows)
	// Logged before the assertions, so the run that fails carries them.
	for _, r := range c.Routes {
		if r.P95Ratio == nil {
			continue
		}
		low, high := "?", "?"
		if r.Resolution.ChangeLow != nil && r.Resolution.ChangeHigh != nil {
			low = fmt.Sprintf("%+.1f%%", *r.Resolution.ChangeLow*100)
			high = fmt.Sprintf("%+.1f%%", *r.Resolution.ChangeHigh*100)
		}
		t.Logf("  %-14s pooled ratio %+7.1f%%  rounds %d  change interval %s to %s",
			r.Route, *r.P95Ratio*100, r.Resolution.Rounds, low, high)
	}
	for _, j := range rows {
		t.Logf("  judged %-14s %-10s unresolvable=%v", j.Scope, j.Value, j.Unresolvable)
	}
	for _, b := range breaches {
		t.Logf("unexpected breach: %s on %q, observed %+.1f%%", b.Name, b.Scope, *b.Observed*100)
	}

	// The precondition: the rounds were what decided, on every route, or the
	// assertion below would be about the single run band again.
	for _, r := range c.Routes {
		require.Equalf(t, workload.ResolutionRounds, r.Resolution.Method,
			"%s was not judged round against round", r.Route)
	}

	// The claim that matters: two identical builds NEVER produce a breach. A
	// false regression is the harmful direction, because it is the one that
	// sends somebody to open a pull request about a change that does not
	// exist. It deliberately does NOT assert a pass: a host whose rounds
	// disagree cannot place a hundred percent limit, and refusing to decide is
	// the correct answer from it.
	require.NotEqual(t, workload.VerdictFail, outcome,
		"two identical builds must never read as a regression")
	require.Empty(t, breaches)
	t.Logf("outcome: %s", outcome)
}

func TestASideThatMeasuredNothingProjectsAsUnverifiedRatherThanZero(t *testing.T) {
	t.Parallel()
	// The projection's fail closed case. A side whose run produced no result
	// must not arrive as a document full of zeroes: differenced against a real
	// run, zero requests and a zero p95 would report the whole of the other
	// side as a regression, and a reader would be told a build collapsed when
	// in fact nothing was measured.
	res := workload.ProjectLoad(nil, workload.ProjectLoadOptions{Branch: "main"})
	require.Equal(t, workload.VerdictUnverified, res.Verdict)
	require.Equal(t, workload.StateFailed, res.State)
	require.Nil(t, res.Measured.Requests, "no count at all, rather than a count of zero")
	require.Nil(t, res.Measured.AchievedRate)
	require.Equal(t, workload.ObservedLoad, res.Kind)

	// And Compare says so rather than silently differencing against it.
	real := workload.ProjectLoad(&load.Result{
		Sent: 10, Rate: 5, Overall: load.Latency{P95Ms: 10},
	}, workload.ProjectLoadOptions{Branch: "feature"})
	c, err := workload.Compare(res, real)
	require.NoError(t, err)
	joined := ""
	for _, n := range c.Notes {
		joined += n + "\n"
	}
	require.Contains(t, joined, "measured nothing")
}
