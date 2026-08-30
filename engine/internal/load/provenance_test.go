package load_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/load"
)

func TestFromAccessLog_CountsTheArrivalRateRatherThanAssumingIt(t *testing.T) {
	t.Parallel()
	// A combined format line carries a timestamp, so the rate can be counted.
	// Assuming it and presenting the assumption as production's rate is how a
	// load run reports a number nobody measured.
	shape := load.FromAccessLog([]string{
		`1.2.3.4 - - [01/Jun/2026:12:00:00 +0000] "GET /a HTTP/1.1" 200 12`,
		`1.2.3.4 - - [01/Jun/2026:12:00:10 +0000] "GET /a HTTP/1.1" 200 12`,
		`1.2.3.4 - - [01/Jun/2026:12:00:20 +0000] "GET /b HTTP/1.1" 200 12`,
		`1.2.3.4 - - [01/Jun/2026:12:00:40 +0000] "GET /b HTTP/1.1" 200 12`,
	})
	require.InDelta(t, 0.1, shape.RequestsPerSecond, 0.001, "four requests across forty seconds")

	// A log with no readable timestamps returns zero, so the caller can say
	// the rate is unknown instead of inventing one.
	none := load.FromAccessLog([]string{`"GET /a HTTP/1.1" 200 12`})
	require.Zero(t, none.RequestsPerSecond)
	require.Len(t, none.Routes, 1, "the request is still counted; only the rate is unknown")

	// One second of log must not report its whole contents as a per second
	// rate multiplied by nothing.
	burst := load.FromAccessLog([]string{
		`1.2.3.4 - - [01/Jun/2026:12:00:00 +0000] "GET /a HTTP/1.1" 200 12`,
		`1.2.3.4 - - [01/Jun/2026:12:00:00 +0000] "GET /a HTTP/1.1" 200 12`,
	})
	require.Equal(t, 0.0, burst.RequestsPerSecond, "a window of nothing is not a rate")
}

func TestRun_CarriesWhereTheShapeCameFromIntoTheResult(t *testing.T) {
	t.Parallel()
	// Shape.Source has claimed since it was written that it exists "so a
	// report can say whether it is real traffic or a guess", and nothing
	// carried it out of the run. The comment described an intention.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	res, err := load.Run(context.Background(), load.Options{
		BaseURL: server.URL,
		Shape: load.Shape{
			Source: "otel", RequestsPerSecond: 40,
			Routes: []load.Route{{Method: "GET", Path: "/", Weight: 1}},
		},
		Scale: 0.5, Duration: 300 * time.Millisecond, Seed: 1, Clock: clock.New(),
	})
	require.NoError(t, err)
	require.Equal(t, "otel", res.Source)
	require.Equal(t, 20.0, res.TargetRate, "the rate asked for is the shape's times the scale")
	require.NotEqual(t, res.TargetRate, res.Rate,
		"the achieved rate is still reported separately, which is the number that matters")
}

func TestDefaultShape_SaysItIsAGuess(t *testing.T) {
	t.Parallel()
	require.Equal(t, "default", load.DefaultShape().Source,
		"a shape that is a guess and production's shape must never be mistaken for each other")
}
