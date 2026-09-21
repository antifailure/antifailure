package load

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
)

// runOf builds a result the way a real run does, through the meter and
// finish, so that Merge is tested against what Run actually produces rather
// than a struct assembled to suit the test.
func runOf(t *testing.T, took time.Duration, samples map[string][]float64, failures map[string]int) *Result {
	t.Helper()
	m := &meter{samples: map[string][]float64{}, errors: map[string]int{},
		counts: map[string]int{}, errsBy: map[string]int{}}
	for route, v := range samples {
		for _, ms := range v {
			m.record(route, ms, "")
		}
	}
	for route, n := range failures {
		for i := 0; i < n; i++ {
			m.record(route, 0, "status 500")
		}
	}
	start := time.Unix(1_790_000_000, 0)
	clk := clock.NewFake(start.Add(took))
	return finish(m, Options{Clock: clk}, start)
}

func span(from, to float64) []float64 {
	var out []float64
	for v := from; v <= to; v++ {
		out = append(out, v)
	}
	return out
}

// The mean of two p95s is not the p95 of anything. Twenty samples at 1 to 20
// and twenty at 101 to 120 pool to forty whose nearest rank p95 is the 38th,
// 118. Averaging the two runs' p95s, 19 and 119, gives 69, a latency no
// request had and a number the resolution gate would then read a band around.
func TestMergePoolsSamplesRatherThanAveragingPercentiles(t *testing.T) {
	a := runOf(t, 10*time.Second, map[string][]float64{"GET /x": span(1, 20)}, nil)
	b := runOf(t, 10*time.Second, map[string][]float64{"GET /x": span(101, 120)}, nil)
	require.Equal(t, 19.0, a.Routes[0].Latency.P95Ms)
	require.Equal(t, 119.0, b.Routes[0].Latency.P95Ms)

	got, err := Merge(a, b)
	require.NoError(t, err)
	require.Len(t, got.Routes, 1)
	require.Equal(t, 118.0, got.Routes[0].Latency.P95Ms)
	require.Equal(t, 118.0, got.Overall.P95Ms)
	require.Equal(t, 40, got.Sent)
	require.Equal(t, 40, got.Routes[0].Sent)
}

// Splitting one run's samples across several results and merging them must
// give back exactly the table the one run gave, because Merge and finish build
// it with the same function. Two tables that disagree about the same samples
// are two tables nobody trusts.
func TestMergeOfASplitRunIsTheRun(t *testing.T) {
	all := map[string][]float64{"GET /a": span(1, 90), "GET /b": span(200, 260)}
	whole := runOf(t, 30*time.Second, all, map[string]int{"GET /a": 3})

	parts := []*Result{
		runOf(t, 10*time.Second, map[string][]float64{"GET /a": span(1, 30), "GET /b": span(200, 220)},
			map[string]int{"GET /a": 1}),
		runOf(t, 10*time.Second, map[string][]float64{"GET /a": span(31, 60), "GET /b": span(221, 240)},
			map[string]int{"GET /a": 2}),
		runOf(t, 10*time.Second, map[string][]float64{"GET /a": span(61, 90), "GET /b": span(241, 260)}, nil),
	}
	got, err := Merge(parts...)
	require.NoError(t, err)
	require.Equal(t, whole.Routes, got.Routes)
	require.Equal(t, whole.Overall, got.Overall)
	require.Equal(t, whole.Sent, got.Sent)
	require.Equal(t, whole.Errors, got.Errors)
	require.InDelta(t, whole.ErrorRate, got.ErrorRate, 1e-12)
	require.Equal(t, 30*time.Second, got.Duration)
	require.InDelta(t, whole.Rate, got.Rate, 1e-9)
}

// A result that has been through a document has percentiles and no samples.
// Merge refuses it by name rather than pooling nothing and reporting the
// other parts as though they were everything.
func TestMergeRefusesAResultWithoutSamples(t *testing.T) {
	live := runOf(t, time.Second, map[string][]float64{"GET /x": span(1, 20)}, nil)
	readBack := &Result{Sent: 20, Routes: live.Routes, Overall: live.Overall}

	_, err := Merge(live, readBack)
	require.ErrorIs(t, err, ErrNoSamples)
	_, err = Merge()
	require.Error(t, err)
}
