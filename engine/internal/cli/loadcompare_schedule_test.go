package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/load"
)

// The seam between what was typed and what it means, through the real
// command's real flags. `--warmup 0s` is the arm that shows what the warm-up
// is for, and reading the flag's value alone would have made it send the
// default warm-up instead, because the flag's zero is also its unset state.
func TestAWarmupOfZeroMeansNoneOnlyWhenItWasTyped(t *testing.T) {
	cases := []struct {
		args     []string
		warmup   time.Duration
		noWarmup bool
		rounds   int
	}{
		{nil, 0, false, 0},
		{[]string{"--warmup", "0s"}, 0, true, 0},
		{[]string{"--warmup", "5s"}, 5 * time.Second, false, 0},
		{[]string{"--rounds", "1", "--warmup", "0s"}, 0, true, 1},
	}
	for _, c := range cases {
		cmd := newLoadCompareCommand(&Env{})
		require.NoError(t, cmd.ParseFlags(c.args), "%v", c.args)
		warmup, err := cmd.Flags().GetDuration("warmup")
		require.NoError(t, err)
		rounds, err := cmd.Flags().GetInt("rounds")
		require.NoError(t, err)
		require.Equal(t, c.warmup, warmup, "%v", c.args)
		require.Equal(t, c.rounds, rounds, "%v", c.args)
		require.Equal(t, c.noWarmup, noWarmup(cmd.Flags(), warmup), "%v", c.args)
	}
}

// Round k of the base is paired with round k of this build, and a route a
// round did not measure, or measured only failures of, is absent rather than
// zero: a zero would enter the log ratio as an infinitely fast round.
func TestRoundsArePairedInOrderAndAZeroIsAbsent(t *testing.T) {
	side := func(p95 float64) *load.Result {
		return &load.Result{Routes: []load.RouteResult{
			{Route: "GET /a", Latency: load.Latency{P95Ms: p95}},
			{Route: "GET /failing", Latency: load.Latency{P95Ms: 0}},
		}}
	}
	res := &env.LoadCompareResult{
		BaselineRounds:  []*load.Result{side(10), side(11), side(12)},
		CandidateRounds: []*load.Result{side(20), side(21)},
	}
	got := roundP95s(res)
	require.Len(t, got, 2, "an unpaired trailing round is not a pair")
	require.Equal(t, 10.0, got[0].Base["GET /a"])
	require.Equal(t, 20.0, got[0].Candidate["GET /a"])
	require.Equal(t, 11.0, got[1].Base["GET /a"])
	require.Equal(t, 21.0, got[1].Candidate["GET /a"])
	_, present := got[0].Base["GET /failing"]
	require.False(t, present)
}
