package traffic_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/traffic"
)

// Recording a profile out of the two files a team already has, and saying what
// each of them cannot answer.
//
// testdata/production-traces.json is an OTLP/JSON export of the shape a
// collector's file exporter writes: sixty three spans over four routes, two of
// them with enough samples for a p95 and two without, three of the ten capture
// bursts overlapping a health check so the peak is a count rather than an
// estimate, one client span, and one server span with nothing on it that names
// a route.

func recordedAt() time.Time { return time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC) }

func exportBody(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "production-traces.json"))
	require.NoError(t, err)
	return body
}

func TestFromOTLP_CountsWhatProductionServed(t *testing.T) {
	t.Parallel()
	p, err := traffic.FromOTLP(exportBody(t), "otel export telemetry/traces.json", recordedAt())
	require.NoError(t, err)

	require.Equal(t, recordedAt(), p.CollectedAt)
	require.EqualValues(t, 61, p.Requests,
		"the export holds 61 server spans that name a route, and the profile has to hold that many requests")
	require.Len(t, p.Routes, 4)

	// Busiest first, which is the order every report reads it in.
	require.Equal(t, "POST /capture", p.Routes[0].String())
	require.EqualValues(t, 30, p.Routes[0].Requests)
	require.Equal(t, "GET /health", p.Routes[1].String())
	require.EqualValues(t, 25, p.Routes[1].Requests)

	// The window is the first start to the last end, and the rate is counted
	// over it rather than assumed.
	require.Equal(t, 6760*time.Millisecond, p.Window())
	rate, ok := p.Rate()
	require.True(t, ok)
	require.InDelta(t, 61.0/6.76, rate, 0.01)
}

func TestFromOTLP_ThePeakIsCountedRatherThanEstimated(t *testing.T) {
	t.Parallel()
	p, err := traffic.FromOTLP(exportBody(t), "otel export telemetry/traces.json", recordedAt())
	require.NoError(t, err)
	require.Equal(t, 4, p.PeakConcurrency,
		"three capture spans overlap a health check in the first burst, and the peak is four")
}

func TestFromOTLP_ARouteWithTooFewSamplesHasNoP95AndSaysSo(t *testing.T) {
	t.Parallel()
	p, err := traffic.FromOTLP(exportBody(t), "otel export telemetry/traces.json", recordedAt())
	require.NoError(t, err)

	health, ok := p.Find("GET", "/health")
	require.True(t, ok)
	require.Positive(t, health.P95Ms, "a route with twenty five samples carries a baseline")

	insights, ok := p.Find("GET", "/api/projects/{id}/insights")
	require.True(t, ok)
	require.Zero(t, insights.P95Ms,
		"a route with five samples was given a p95, which a threshold would then fire on noise")
	require.Contains(t, strings.Join(p.Missing, "\n"),
		"2 routes carried too few requests in this window for a p95")
	require.Contains(t, strings.Join(p.Missing, "\n"), "GET /api/projects/{id}/insights")
}

func TestFromOTLP_AnIdentifierInAPathIsCollapsedToOneRoute(t *testing.T) {
	t.Parallel()
	p, err := traffic.FromOTLP(exportBody(t), "otel export telemetry/traces.json", recordedAt())
	require.NoError(t, err)
	_, ok := p.Find("GET", "/api/projects/{id}/events")
	require.True(t, ok,
		"a raw path with an identifier in it was recorded as its own route, which is how a "+
			"profile ends up with ten thousand routes and no signal in it")
}

func TestFromOTLP_WhatItDidNotCountIsNamed(t *testing.T) {
	t.Parallel()
	p, err := traffic.FromOTLP(exportBody(t), "otel export telemetry/traces.json", recordedAt())
	require.NoError(t, err)
	missing := strings.Join(p.Missing, "\n")
	require.Contains(t, missing, "1 span was not a server span and was not counted",
		"a client span is an outbound call this service made, and replaying it as inbound "+
			"traffic would send the environment's own dependency calls at itself")
	require.Contains(t, missing, "no HTTP method or path on the span")
}

func TestFromOTLP_AnExportWithNoServerSpansIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()
	_, err := traffic.FromOTLP([]byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[`+
		`{"name":"POST /v1/charges","kind":"SPAN_KIND_CLIENT","startTimeUnixNano":"1",`+
		`"endTimeUnixNano":"2"}]}]}]}`), "otel export traces.json", recordedAt())
	require.Error(t, err)
	require.Contains(t, err.Error(), "no server spans")
	require.Contains(t, err.Error(), "not a server span",
		"an export full of client spans produces nothing, and the count is the only thing "+
			"that says why")
}

func TestFromAccessLog_CountsTheMixAndRefusesToInventTheRest(t *testing.T) {
	t.Parallel()
	lines := []string{
		`1.2.3.4 - - [01/Sep/2026:02:00:00 +0000] "GET /events HTTP/1.1" 200 12`,
		`1.2.3.4 - - [01/Sep/2026:02:00:10 +0000] "GET /events HTTP/1.1" 200 12`,
		`1.2.3.4 - - [01/Sep/2026:02:00:20 +0000] "POST /capture HTTP/1.1" 204 0`,
		`this line is not a request`,
	}
	p, err := traffic.FromAccessLog(lines, "access log var/log/nginx/access.log", recordedAt())
	require.NoError(t, err)

	require.EqualValues(t, 3, p.Requests)
	require.Len(t, p.Routes, 2)
	require.Equal(t, "GET /events", p.Routes[0].String())
	require.EqualValues(t, 2, p.Routes[0].Requests)
	require.Equal(t, 20*time.Second, p.Window())

	missing := strings.Join(p.Missing, "\n")
	require.Contains(t, missing, "carries no request duration, so this profile has no p95",
		"an access log with no durations reported a p95, which a threshold would compare against")
	require.Contains(t, missing, "carries no concurrency")
	require.Contains(t, missing, "1 line could not be read as a request line")
	require.Zero(t, p.PeakConcurrency)
}

func TestFromAccessLog_ALogWithNoTimestampsHasNoRateAndSaysSo(t *testing.T) {
	t.Parallel()
	p, err := traffic.FromAccessLog(
		[]string{`"GET /events HTTP/1.1" 200 12`}, "access log", recordedAt())
	require.NoError(t, err)
	_, ok := p.Rate()
	require.False(t, ok)
	require.Contains(t, strings.Join(p.Missing, "\n"),
		"no line carried a timestamp this reader could parse")
}

func TestFromAccessLog_ALogWithNoRequestsIsRefusedRatherThanWritten(t *testing.T) {
	t.Parallel()
	_, err := traffic.FromAccessLog([]string{"nothing here"}, "access log", recordedAt())
	require.Error(t, err,
		"an empty profile is a denominator of zero, which is how a report claims a run covers everything")
	require.Contains(t, err.Error(), "no request could be read")
}
