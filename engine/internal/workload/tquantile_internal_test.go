package workload

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// The t quantile against published values, including the tails a Bonferroni
// level reaches, and converging on the normal quantile as df grows.
func TestTQuantileMatchesPublishedValues(t *testing.T) {
	for _, c := range []struct {
		p    float64
		df   int
		want float64
	}{
		{0.95, 1, 6.313752},
		{0.95, 7, 1.894579},
		{0.975, 7, 2.364624},
		{0.975, 30, 2.042272},
		{0.995, 15, 2.946713},
		{0.95, 1000, 1.646379},
	} {
		require.InDelta(t, c.want, tQuantile(c.p, c.df), 1e-5, "p=%v df=%d", c.p, c.df)
	}
	require.True(t, math.IsInf(tQuantile(0.95, 0), 1))
}
