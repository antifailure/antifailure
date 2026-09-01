package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/load"
)

// The exit is tested rather than the command, because the command needs an
// environment and the decision is the part that was wrong. A load run used to
// have exactly one way to fail, and a threshold that measured nothing exited
// zero looking identical to a threshold that measured everything and found
// nothing.

func TestLoadExit_AThresholdThatMeasuredNothingFailsTheRun(t *testing.T) {
	t.Parallel()
	measuredNothing := &load.Result{Routes: []load.RouteResult{
		{Route: "GET /", Sent: 400, Latency: load.Latency{P95Ms: 900}, HasBaseline: false},
	}}
	err := loadExit(measuredNothing, nil, 0.25)
	require.Equal(t, aferrors.AFLOD016, codeOfErr(t, err))
	require.Contains(t, err.Error(), "no baseline for any of the 1 route")
}

func TestLoadExit_AThresholdThatMeasuredSomethingAndFoundNothingPasses(t *testing.T) {
	t.Parallel()
	// The pass this whole change exists to distinguish from the one above.
	measured := &load.Result{Routes: []load.RouteResult{
		{Route: "GET /", Sent: 400, Latency: load.Latency{P95Ms: 44},
			BaselineP95Ms: 41, HasBaseline: true, P95Increase: 0.07},
	}}
	require.NoError(t, loadExit(measured, nil, 0.25))

	// And a source that carries no baselines no longer arrives with a
	// threshold at all, which normalizeLoad guarantees and this states.
	require.NoError(t, loadExit(&load.Result{Routes: []load.RouteResult{
		{Route: "GET /", Sent: 400, HasBaseline: false},
	}}, nil, 0))
}

func TestLoadExit_ABreachIsReportedBeforeAnUnmeasuredThreshold(t *testing.T) {
	t.Parallel()
	// A run with both has a measured regression, which is the more actionable
	// half, and one exit code can only carry one of them.
	res := &load.Result{ErrorRate: 0.2, Routes: []load.RouteResult{
		{Route: "GET /", Sent: 400, HasBaseline: false},
	}}
	breaches := res.Breaches(0.25, 0.05)
	require.Len(t, breaches, 1)
	require.Equal(t, aferrors.AFLOD011, codeOfErr(t, loadExit(res, breaches, 0.25)))
}
