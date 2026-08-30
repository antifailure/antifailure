package load_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/load"
)

// otlpSpanJSON writes one span the way the Go OTLP exporter writes it: 64 bit
// timestamps as strings, the span kind as its enum name, stable semantic
// convention attribute names.
func otlpSpanJSON(name, kind string, startNanos, endNanos uint64, attrs map[string]string) string {
	var parts []string
	for _, k := range sortedKeys(attrs) {
		parts = append(parts, fmt.Sprintf(
			`{"key":%q,"value":{"stringValue":%q}}`, k, attrs[k]))
	}
	return fmt.Sprintf(
		`{"name":%q,"kind":%q,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[%s]}`,
		name, kind, startNanos, endNanos, strings.Join(parts, ","))
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func otlpDocJSON(spans ...string) string {
	return `{"resourceSpans":[{"resource":{"attributes":[` +
		`{"key":"service.name","value":{"stringValue":"checkout"}}]},` +
		`"scopeSpans":[{"scope":{"name":"net/http"},"spans":[` +
		strings.Join(spans, ",") + `]}]}]}`
}

func TestFromOTLP_ReadsAMixAndABaselineFromRealTraces(t *testing.T) {
	t.Parallel()
	// The reason OpenTelemetry was worth implementing rather than another log
	// parser: a span carries a duration, so the shape arrives with
	// production's p95 per route in it, and p95_increase finally has
	// something to compare against.
	const second = uint64(1_000_000_000)
	var spans []string
	for i := 0; i < 40; i++ {
		start := second * uint64(i)
		spans = append(spans, otlpSpanJSON("GET /settings/billing", "SPAN_KIND_SERVER",
			start, start+50*uint64(1_000_000), map[string]string{
				"http.request.method":       "GET",
				"http.route":                "/settings/billing",
				"http.response.status_code": "200",
			}))
	}
	for i := 0; i < 20; i++ {
		start := second * uint64(i)
		spans = append(spans, otlpSpanJSON("GET /api/subscriptions", "SPAN_KIND_SERVER",
			start, start+10*uint64(1_000_000), map[string]string{
				"http.request.method": "GET",
				"http.route":          "/api/subscriptions",
			}))
	}
	// One route seen twice. Under the sample floor, so it is traffic without
	// a baseline rather than a baseline made of two numbers.
	for i := 0; i < 2; i++ {
		spans = append(spans, otlpSpanJSON("GET /health", "SPAN_KIND_SERVER",
			second*uint64(i), second*uint64(i)+uint64(900_000_000), map[string]string{
				"http.request.method": "GET",
				"http.route":          "/health",
			}))
	}

	read, err := load.FromOTLP([]byte(otlpDocJSON(spans...)))
	require.NoError(t, err)
	require.Equal(t, 62, read.Spans)
	require.Equal(t, "otel", read.Shape.Source)

	byRoute := map[string]load.Route{}
	for _, r := range read.Shape.Routes {
		byRoute[r.String()] = r
	}
	require.Equal(t, 40.0, byRoute["GET /settings/billing"].Weight)
	require.Equal(t, 20.0, byRoute["GET /api/subscriptions"].Weight)
	require.Equal(t, "GET /settings/billing", read.Shape.Routes[0].String(),
		"the busiest route sorts first")

	require.InDelta(t, 50.0, byRoute["GET /settings/billing"].P95Ms, 0.5,
		"the baseline is production's own p95 for that route")
	require.InDelta(t, 10.0, byRoute["GET /api/subscriptions"].P95Ms, 0.5)
	require.Zero(t, byRoute["GET /health"].P95Ms,
		"two samples is not a baseline; a threshold measured against it would fire on noise")

	// Sixty two spans across a window that starts at zero and ends at the last
	// span, which is the thirty ninth second plus fifty milliseconds.
	require.InDelta(t, 62.0/39.05, read.Shape.RequestsPerSecond, 0.05)
}

func TestFromOTLP_ABaselineOnlyCountsWhenThereAreEnoughSamples(t *testing.T) {
	t.Parallel()
	// The whole point of the floor is that this route can never be a breach.
	var spans []string
	for i := 0; i < 5; i++ {
		spans = append(spans, otlpSpanJSON("GET /rare", "SPAN_KIND_SERVER",
			uint64(i)*1_000_000_000, uint64(i)*1_000_000_000+700_000_000,
			map[string]string{"http.request.method": "GET", "http.route": "/rare"}))
	}
	read, err := load.FromOTLP([]byte(otlpDocJSON(spans...)))
	require.NoError(t, err)
	require.Len(t, read.Shape.Routes, 1)
	require.Zero(t, read.Shape.Routes[0].P95Ms)

	res := &load.Result{Routes: []load.RouteResult{{
		Route: "GET /rare", Latency: load.Latency{P95Ms: 5000},
		BaselineP95Ms: read.Shape.Routes[0].P95Ms,
		HasBaseline:   read.Shape.Routes[0].P95Ms > 0,
	}}}
	require.Empty(t, res.Breaches(0.1, 0), "a route with no baseline is never a breach")
}

func TestFromOTLP_ReadsTheOldAttributeNamesAndTheNumericEncodings(t *testing.T) {
	t.Parallel()
	// Semantic conventions renamed every one of these in 1.21 and exporters in
	// the field still write the old names. Reading only the new ones would
	// return an empty shape from a real export and blame the user for it.
	// The numbers, meanwhile, arrive as numbers from some producers and as
	// strings from others, and a decoder that took only one would reject half
	// the real files for a reason unrelated to the traffic in them.
	doc := `{"resourceSpans":[{"instrumentationLibrarySpans":[{"spans":[
	  {"name":"HTTP GET","kind":2,"startTimeUnixNano":0,"endTimeUnixNano":25000000,
	   "attributes":[
	     {"key":"http.method","value":{"stringValue":"GET"}},
	     {"key":"http.target","value":{"stringValue":"/orders/4821?ref=email"}},
	     {"key":"http.status_code","value":{"intValue":200}}]}
	]}]}]}`
	read, err := load.FromOTLP([]byte(doc))
	require.NoError(t, err)
	require.Equal(t, 1, read.Spans)
	require.Equal(t, "GET /orders/{id}", read.Shape.Routes[0].String(),
		"a raw path is normalised and its query dropped, as an access log path is")
}

func TestFromOTLP_PrefersTheTemplatedRouteOverTheRawPath(t *testing.T) {
	t.Parallel()
	// Normalising a raw path guesses at what was an identifier. The
	// instrumentation did not have to guess, so http.route wins.
	doc := otlpDocJSON(otlpSpanJSON("GET /users/:slug", "SPAN_KIND_SERVER", 0, 1_000_000,
		map[string]string{
			"http.request.method": "GET",
			"http.route":          "/users/:slug/orders",
			"url.path":            "/users/marketing-team/orders",
		}))
	read, err := load.FromOTLP([]byte(doc))
	require.NoError(t, err)
	require.Equal(t, "GET /users/:slug/orders", read.Shape.Routes[0].String())
}

func TestFromOTLP_FallsBackToTheSpanName(t *testing.T) {
	t.Parallel()
	// The convention for a server span's name is "{method} {route}", and an
	// exporter that sets the name and no HTTP attributes is common enough to
	// be worth the fallback.
	doc := otlpDocJSON(
		otlpSpanJSON("POST /api/orders", "SPAN_KIND_SERVER", 0, 5_000_000, nil),
		otlpSpanJSON("checkout worker", "SPAN_KIND_SERVER", 0, 5_000_000, nil),
	)
	read, err := load.FromOTLP([]byte(doc))
	require.NoError(t, err)
	require.Equal(t, 1, read.Spans)
	require.Equal(t, "POST /api/orders", read.Shape.Routes[0].String())
	require.Equal(t, 1, read.Skipped["no HTTP method or path on the span"],
		"a span that is not a request says so rather than vanishing")
}

func TestFromOTLP_LeavesClientSpansOutAndSaysHowMany(t *testing.T) {
	t.Parallel()
	// A client span is an outbound call the service made. Replaying those as
	// inbound traffic would send the environment's own dependency calls at
	// itself. An export that is all client spans produces nothing, and the
	// count is the only thing that explains why.
	doc := otlpDocJSON(
		otlpSpanJSON("GET /api/items", "SPAN_KIND_SERVER", 0, 4_000_000,
			map[string]string{"http.request.method": "GET", "http.route": "/api/items"}),
		otlpSpanJSON("GET /v1/charges", "SPAN_KIND_CLIENT", 0, 90_000_000,
			map[string]string{"http.request.method": "GET", "url.path": "/v1/charges"}),
		otlpSpanJSON("SELECT orders", "SPAN_KIND_INTERNAL", 0, 1_000_000, nil),
	)
	read, err := load.FromOTLP([]byte(doc))
	require.NoError(t, err)
	require.Equal(t, 1, read.Spans)
	require.Len(t, read.Shape.Routes, 1)
	require.Equal(t, 2, read.Skipped["not a server span"])
}

func TestFromOTLP_OneUnreadableLineDoesNotDiscardTheRest(t *testing.T) {
	t.Parallel()
	// The collector's file exporter writes one document per line and appends
	// forever, so a truncated final line is the normal state of a file
	// something is still writing to. Discarding an hour of traffic over it
	// would be absurd.
	good := otlpDocJSON(otlpSpanJSON("GET /a", "SPAN_KIND_SERVER", 0, 2_000_000,
		map[string]string{"http.request.method": "GET", "http.route": "/a"}))
	other := otlpDocJSON(otlpSpanJSON("GET /b", "SPAN_KIND_SERVER", 0, 2_000_000,
		map[string]string{"http.request.method": "GET", "http.route": "/b"}))
	lines := good + "\n" + `{"resourceSpans":[{"scopeSpans":[{"spa` + "\n" + other + "\n"

	read, err := load.FromOTLP([]byte(lines))
	require.NoError(t, err)
	require.Equal(t, 2, read.Spans)
	require.Len(t, read.Shape.Routes, 2)
	require.Equal(t, 1, read.Skipped["unreadable line"])
}

func TestFromOTLP_SaysWhatItLookedForWhenThereIsNothingToRead(t *testing.T) {
	t.Parallel()
	_, err := load.FromOTLP([]byte(`{"resourceSpans":[]}`))
	require.ErrorContains(t, err, "no server spans")

	_, err = load.FromOTLP([]byte("this is a log file, not a trace export"))
	require.ErrorContains(t, err, "not an OTLP/JSON trace export")
}

func TestFromOTLP_TheWindowIsClampedSoASingleBurstIsNotAnInfiniteRate(t *testing.T) {
	t.Parallel()
	// Forty spans inside the same millisecond would otherwise report forty
	// thousand requests a second, and the scale factor would then multiply a
	// number that was never real.
	var spans []string
	for i := 0; i < 40; i++ {
		spans = append(spans, otlpSpanJSON("GET /", "SPAN_KIND_SERVER", 1000, 2000,
			map[string]string{"http.request.method": "GET", "http.route": "/"}))
	}
	read, err := load.FromOTLP([]byte(otlpDocJSON(spans...)))
	require.NoError(t, err)
	require.Equal(t, 40.0, read.Shape.RequestsPerSecond,
		"forty spans across a clamped window of one second, not forty thousand a second")
}

func TestFromOTLP_ReadsWhatACollectorActuallyWrote(t *testing.T) {
	t.Parallel()
	// Verbatim from an OpenTelemetry collector file exporter, trimmed to one
	// span. The point of pasting it rather than building it is that it proves
	// the decoder matches the real shape rather than the assumed one: string
	// timestamps, the enum name for the kind, traceId and spanId and status
	// present and ignored.
	const raw = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"shop"}}]},"scopeSpans":[{"scope":{"name":"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp","version":"0.49.0"},"spans":[{"traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174","parentSpanId":"","name":"GET /cart","kind":"SPAN_KIND_SERVER","startTimeUnixNano":"1755000000000000000","endTimeUnixNano":"1755000000123000000","attributes":[{"key":"http.request.method","value":{"stringValue":"GET"}},{"key":"url.path","value":{"stringValue":"/cart"}},{"key":"http.response.status_code","value":{"intValue":"200"}},{"key":"server.address","value":{"stringValue":"shop.example.com"}}],"status":{"code":"STATUS_CODE_UNSET"}}]}]}]}`

	var probe map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &probe), "the fixture is valid JSON")

	read, err := load.FromOTLP([]byte(raw))
	require.NoError(t, err)
	require.Equal(t, 1, read.Spans)
	require.Equal(t, "GET /cart", read.Shape.Routes[0].String())
	require.Equal(t, "otel", read.Shape.Source)
}
