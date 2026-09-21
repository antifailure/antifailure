package env

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/load"
)

// modelHost is a host whose drift and cold start are known exactly, standing
// in for two environments on one machine. Every request's latency is a spread
// the seed decides, identical on both sides for the same seed, plus a drift
// that grows with wall time across the whole comparison, times a cold start
// factor while an environment is younger than coldFor. The two sides run the
// SAME code, so any difference the comparison reports is bias, and the right
// answer is zero.
type modelHost struct {
	driftMsPerSec float64
	coldFactor    float64
	coldFor       time.Duration
	// age is how long each side has been serving. The candidate starts old,
	// the way this build's environment usually has been up for a while; the
	// base starts at zero, the way the comparison brings it up.
	age   map[compareSide]time.Duration
	clock time.Duration
	calls []modelCall
}

type modelCall struct {
	side compareSide
	d    time.Duration
	seed int64
}

const modelRate = 20 // requests a second

func newModelHost(drift, coldFactor float64, coldFor time.Duration) *modelHost {
	return &modelHost{driftMsPerSec: drift, coldFactor: coldFactor, coldFor: coldFor,
		age: map[compareSide]time.Duration{sideBase: 0, sideCandidate: time.Hour}}
}

func (h *modelHost) send(_ context.Context, side compareSide, d time.Duration, seed int64) (
	[]float64, []load.Route, error,
) {
	h.calls = append(h.calls, modelCall{side, d, seed})
	n := int(d.Seconds() * modelRate)
	out := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		at := time.Duration(i) * time.Second / modelRate
		spread := 10 + 20*math.Mod(float64(i)*0.6180339887+float64(seed)*0.1, 1)
		ms := spread + h.driftMsPerSec*(h.clock+at).Seconds()
		if h.age[side]+at < h.coldFor {
			ms *= h.coldFactor
		}
		out = append(out, ms)
	}
	h.clock += d
	h.age[side] += d
	return out, nil, nil
}

func pool(parts ...[]float64) ([]float64, error) {
	var out []float64
	for _, p := range parts {
		out = append(out, p...)
	}
	return out, nil
}

// bias runs a comparison of identical code on the model host and returns this
// build's pooled p95 against the base's, as a fraction. Zero is the truth.
func bias(t *testing.T, h *modelHost, opts LoadCompareOptions) float64 {
	t.Helper()
	plan := comparePlanFor(opts, 32*time.Second)
	got, err := interleaved(context.Background(), plan, 7, h.send, pool, func(string) {})
	require.NoError(t, err)
	base, cand := load.Percentiles(got.base).P95Ms, load.Percentiles(got.cand).P95Ms
	return cand/base - 1
}

// THE DEFECT, reproduced: one pass each, base first, on a host that drifts.
// The old way reported identical code as about 40 percent slower on this
// build, and the default schedule has to bring that to well under one percent.
// Measured through comparePlanFor with no options, so the DEFAULT round count
// is what is under test: at four rounds the same model leaves about seven
// percent, which this assertion refuses.
func TestTheDefaultScheduleKeepsADriftOutOfTheP95(t *testing.T) {
	old := bias(t, newModelHost(0.5, 1, 0), LoadCompareOptions{Rounds: 1, NoWarmup: true})
	require.Greater(t, math.Abs(old), 0.20, "the model must reproduce the defect, or it proves nothing")

	now := bias(t, newModelHost(0.5, 1, 0), LoadCompareOptions{NoWarmup: true})
	require.Less(t, math.Abs(now), 0.01, "identical code must not move beyond a percent: got %+.2f%%", now*100)
}

// The cold start is the other half, and only the warm-up removes it. A base
// environment answering eight times slower for its first five seconds reads,
// with no warm-up, as this build being far faster, WHATEVER the order; with
// the default warm-up it reads as nothing. Asserted both ways so that neither
// mechanism can be credited with the other's work.
func TestOnlyTheWarmupRemovesAColdStart(t *testing.T) {
	cold := func() *modelHost { return newModelHost(0, 8, 5*time.Second) }

	interleavedNoWarmup := bias(t, cold(), LoadCompareOptions{NoWarmup: true})
	require.Less(t, interleavedNoWarmup, -0.50,
		"interleaving alone must NOT hide a cold start: got %+.2f%%", interleavedNoWarmup*100)

	warmedSinglePass := bias(t, cold(), LoadCompareOptions{Rounds: 1})
	require.InDelta(t, 0, warmedSinglePass, 0.001)

	defaults := bias(t, cold(), LoadCompareOptions{})
	require.InDelta(t, 0, defaults, 0.001)
}

// Round k is the same request sequence at both sides: both of its sends carry
// seed plus k. The warm-up comes first, one per side, and is not a round.
func TestEachRoundSendsTheSameSeedAtBothSides(t *testing.T) {
	h := newModelHost(0, 1, 0)
	plan := comparePlanFor(LoadCompareOptions{Rounds: 4, Warmup: 3 * time.Second}, 32*time.Second)
	_, err := interleaved(context.Background(), plan, 100, h.send, pool, func(string) {})
	require.NoError(t, err)

	require.Len(t, h.calls, 2+8)
	require.Equal(t, modelCall{sideBase, 3 * time.Second, 100}, h.calls[0])
	require.Equal(t, modelCall{sideCandidate, 3 * time.Second, 100}, h.calls[1])
	rounds := h.calls[2:]
	for k := 0; k < 4; k++ {
		a, b := rounds[2*k], rounds[2*k+1]
		require.NotEqual(t, a.side, b.side, "round %d must send at both sides", k)
		require.Equal(t, int64(100+k), a.seed)
		require.Equal(t, int64(100+k), b.seed)
		require.Equal(t, 8*time.Second, a.d)
	}
}

// The warm-up is sent and its samples are thrown away: nothing it measured
// may appear in either side's pooled result.
func TestTheWarmupIsDiscarded(t *testing.T) {
	marked := func(_ context.Context, side compareSide, d time.Duration, _ int64) (
		[]float64, []load.Route, error,
	) {
		if d == 2*time.Second {
			return []float64{9999}, nil, nil
		}
		return []float64{10}, nil, nil
	}
	plan := comparePlanFor(LoadCompareOptions{Rounds: 2, Warmup: 2 * time.Second}, 8*time.Second)
	got, err := interleaved(context.Background(), plan, 1, marked, pool, func(string) {})
	require.NoError(t, err)
	require.Equal(t, []float64{10, 10}, got.base)
	require.Equal(t, []float64{10, 10}, got.cand)
}

// A warm-up that cannot reach an environment stops the comparison before any
// round, because every round would fail the same way.
func TestAWarmupThatCannotReachStopsTheComparison(t *testing.T) {
	calls := 0
	failing := func(_ context.Context, _ compareSide, _ time.Duration, _ int64) (
		[]float64, []load.Route, error,
	) {
		calls++
		return nil, nil, errors.New("connection refused")
	}
	plan := comparePlanFor(LoadCompareOptions{}, 32*time.Second)
	_, err := interleaved(context.Background(), plan, 1, failing, pool, func(string) {})
	require.ErrorContains(t, err, "connection refused")
	require.Equal(t, 1, calls)
}

// The order gives every pair one of each side, and balances each side's slot
// sum and slot sum of squares at four rounds. That is all it is claimed to do.
func TestTheOrderPairsTheSidesAndBalancesTheirSlots(t *testing.T) {
	order := compareOrder(4)
	require.Len(t, order, 8)
	var sum, sq [2]int
	for n, side := range order {
		sum[side] += n
		sq[side] += n * n
		if n%2 == 1 {
			require.NotEqual(t, order[n-1], side, "slots %d and %d must be one of each side", n-1, n)
		}
	}
	require.Equal(t, sum[sideBase], sum[sideCandidate])
	require.Equal(t, sq[sideBase], sq[sideCandidate])
}

// The plan: defaults when nothing is said, none when the caller said none, and
// the measured total split across the rounds rather than multiplied by them.
func TestComparePlanFor(t *testing.T) {
	p := comparePlanFor(LoadCompareOptions{}, 32*time.Second)
	require.Equal(t, comparePlan{rounds: DefaultCompareRounds,
		perRound: 32 * time.Second / DefaultCompareRounds, warmup: DefaultCompareWarmup}, p)

	p = comparePlanFor(LoadCompareOptions{Rounds: 1, NoWarmup: true}, 30*time.Second)
	require.Equal(t, comparePlan{rounds: 1, perRound: 30 * time.Second, warmup: 0}, p)
}

// The notes describe how THIS run was sent. A single pass must not borrow the
// interleaved run's sentence, and no warm-up must be said out loud.
func TestTheNotesDescribeHowTheRunWasSent(t *testing.T) {
	single := loadCompareNotes(&LoadCompareResult{Rounds: 1, Golden: "gv_x"})
	require.Contains(t, single[0], "measured once each")
	require.Contains(t, single[1], "cannot see the noise between two runs")
	require.Contains(t, single[2], "no warm-up was sent")

	many := loadCompareNotes(&LoadCompareResult{Rounds: 8, RoundDuration: 4 * time.Second,
		Warmup: 10 * time.Second, Golden: "gv_x"})
	require.Contains(t, many[0], "8 rounds of 4s")
	require.NotContains(t, many[0], "measured once each")
	require.Contains(t, many[1], "measured round against round")
	require.Contains(t, many[2], "10s of the same mix and that was discarded")
}
