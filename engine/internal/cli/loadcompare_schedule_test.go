package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
