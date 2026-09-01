package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// The second return value of Load was discarded at the one call site that had
// it, so 'af ci --load' said the same thing whether the safe list let through
// every route or one out of forty. Nothing in the engine's test files matched
// --load or withLoad, which is how it went unnoticed.
func TestRefusedRoutes_NamesWhatTheShapeWouldNotSend(t *testing.T) {
	t.Parallel()
	require.Nil(t, refusedRoutes(nil), "an empty list must leave the report's line out entirely")
	// Sorted rather than in the order the shape happened to carry them, so two
	// runs of the same manifest produce the same line and a diff of two
	// comments is about what changed.
	require.Equal(t,
		[]string{"DELETE /api/items/1", "POST /api/payments"},
		refusedRoutes([]load.Route{
			{Method: "POST", Path: "/api/payments"},
			{Method: "DELETE", Path: "/api/items/1"},
		}))
}

// And it reaches the reader rather than sitting in a struct.
func TestReport_SaysWhatWasNotSent(t *testing.T) {
	t.Parallel()
	l := &report.Load{Sent: 400, Rate: 40, P95Ms: 12, Refused: []string{"POST /api/payments"}}
	md := (&report.Run{Environment: "e", Branch: "b", Load: l}).Markdown()
	require.Contains(t, md, "POST /api/payments")
	require.Contains(t, md, "1 route was not sent, because nothing in the manifest named them safe")

	quiet := (&report.Run{Environment: "e", Branch: "b", Load: &report.Load{Sent: 400}}).Markdown()
	require.NotContains(t, quiet, "not sent")
}
