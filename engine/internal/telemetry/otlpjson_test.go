package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otel/trace"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

// collector is an OTLP endpoint that keeps what it was sent, as raw bytes and
// as the decoded shape, so a test can assert either.
type collector struct {
	mu     sync.Mutex
	bodies [][]byte
	status int
	path   string
	auth   string
}

func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
	}
	c.mu.Lock()
	c.bodies = append(c.bodies, body)
	c.path = r.URL.Path
	c.auth = r.Header.Get("authorization")
	status := c.status
	c.mu.Unlock()

	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{}`))
}

func (c *collector) received() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.bodies...)
}

func tracingTo(t *testing.T, env map[string]string) (*Tracing, *collector) {
	t.Helper()
	c := &collector{}
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)

	full := map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": srv.URL}
	for k, v := range env {
		full[k] = v
	}
	if v, ok := env["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"]; ok && v == "@server" {
		full["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"] = srv.URL + "/custom/traces"
	}

	tr, err := NewTracing(context.Background(), TracingOptions{
		ServiceName: "test-engine", Version: "v0.1.1",
		Getenv: func(k string) string { return full[k] },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Shutdown(context.Background()) })
	return tr, c
}

// The whole point of writing the exporter here: the bytes on the wire are
// produced by a file in this repository, so the strongest form of the
// redaction guarantee is available. Not "no secret reached a span attribute"
// but "no secret is in what left the machine".
func TestNoSecretIsInTheBytesSentToACollector(t *testing.T) {
	r := redact.New()
	const registered = "collector-secret-b41f0c"
	r.Register(registered)

	tr, c := tracingTo(t, nil)
	require.True(t, tr.Enabled())

	ctx, span := tr.Tracer().Start(context.Background(), "af up")
	sink := newSpanSink(span, r)
	require.NoError(t, sink.Deliver(ctx, events.Event{
		ID: "e1", Env: "env-1", Seq: 1, Type: events.DBBranched,
		Level: events.LevelInfo, Msg: "branched with " + registered,
		TS: time.Unix(1700000000, 0).UTC(),
		Data: map[string]any{
			"database_url": "postgres://app:hunter2-on-the-wire@db.internal:5432/app",
			"password":     registered,
		},
	}))
	span.End()
	require.NoError(t, tr.Flush(context.Background()))

	bodies := c.received()
	require.NotEmpty(t, bodies, "nothing reached the collector")
	for _, b := range bodies {
		require.NotContains(t, string(b), registered)
		require.NotContains(t, string(b), "hunter2-on-the-wire")
	}
	require.Contains(t, string(bodies[0]), "db.internal",
		"the host was redacted too, so the trace explains nothing")
}

func TestTheExportedJSONIsTheShapeOTLPDefines(t *testing.T) {
	tr, c := tracingTo(t, nil)
	ctx, span := tr.Tracer().Start(context.Background(), "af up")
	sink := newSpanSink(span, redact.New())
	require.NoError(t, sink.Deliver(ctx, events.Event{
		ID: "e1", Env: "env-1", Seq: 7, Type: events.EnvReady,
		Level: events.LevelInfo, Msg: "ready", TS: time.Unix(1700000000, 0).UTC(),
		Data: map[string]any{"seconds": 1.5, "services": 3, "cached": true},
	}))
	span.End()
	require.NoError(t, tr.Flush(context.Background()))

	bodies := c.received()
	require.Len(t, bodies, 1)

	var req otlpExportRequest
	require.NoError(t, json.Unmarshal(bodies[0], &req), "the collector received invalid JSON")
	require.Len(t, req.ResourceSpans, 1)

	rs := req.ResourceSpans[0]
	var serviceName string
	for _, a := range rs.Resource.Attributes {
		if a.Key == "service.name" && a.Value.StringValue != nil {
			serviceName = *a.Value.StringValue
		}
	}
	require.Equal(t, "test-engine", serviceName)

	require.Len(t, rs.ScopeSpans, 1)
	require.Len(t, rs.ScopeSpans[0].Spans, 1)
	s := rs.ScopeSpans[0].Spans[0]

	require.Len(t, s.TraceID, 32, "a trace identifier is sixteen bytes of lowercase hex")
	require.Len(t, s.SpanID, 16, "a span identifier is eight bytes of lowercase hex")
	require.Equal(t, strings.ToLower(s.TraceID), s.TraceID)
	require.Empty(t, s.ParentSpanID,
		"a root span must omit the parent rather than send sixteen zeroes, which some "+
			"collectors read as a parent that does not exist")
	require.Equal(t, "af up", s.Name)
	require.Equal(t, int(trace.SpanKindInternal), s.Kind)

	// Times are unix nanoseconds encoded as strings, because a JSON number
	// cannot hold one exactly.
	require.Regexp(t, `^\d{16,}$`, s.StartTimeUnixNano)
	require.Regexp(t, `^\d{16,}$`, s.EndTimeUnixNano)

	require.Len(t, s.Events, 1)
	e := s.Events[0]
	require.Equal(t, string(events.EnvReady), e.Name)

	byKey := map[string]otlpValue{}
	for _, a := range e.Attributes {
		byKey[a.Key] = a.Value
	}

	// The one that catches people. Protobuf's JSON mapping encodes 64 bit
	// integers as STRINGS. A collector reading a number where it expects a
	// string rejects the whole batch.
	require.NotNil(t, byKey["af.event.seq"].IntValue)
	require.Equal(t, "7", *byKey["af.event.seq"].IntValue)
	require.Nil(t, byKey["af.event.seq"].StringValue, "exactly one field of the oneof is set")

	require.NotNil(t, byKey["af.data.seconds"].DoubleValue)
	require.InDelta(t, 1.5, *byKey["af.data.seconds"].DoubleValue, 0.0001)

	require.NotNil(t, byKey["af.data.cached"].BoolValue)
	require.True(t, *byKey["af.data.cached"].BoolValue)

	require.NotNil(t, byKey["af.env"].StringValue)
	require.Equal(t, "env-1", *byKey["af.env"].StringValue)
}

// A false boolean and a zero integer must survive, and would not if the oneof
// used omitempty on the values rather than pointers.
func TestAFalseBooleanAndAZeroAreNotDroppedAsEmpty(t *testing.T) {
	sink := &spanSink{redactor: redact.New()}
	require.NotNil(t, otlpValueOf(sink.attr("k", false).Value).BoolValue)
	require.False(t, *otlpValueOf(sink.attr("k", false).Value).BoolValue)

	zero := otlpValueOf(sink.attr("k", 0).Value)
	require.NotNil(t, zero.IntValue)
	require.Equal(t, "0", *zero.IntValue)
}

func TestAnErrorStatusIsCarriedWithItsMessage(t *testing.T) {
	tr, c := tracingTo(t, nil)
	ctx, span := tr.Tracer().Start(context.Background(), "af up")
	sink := newSpanSink(span, redact.New())
	require.NoError(t, sink.Deliver(ctx, events.Event{
		Type: events.EnvFailed, Level: events.LevelError,
		Msg: "the environment could not be created", TS: time.Unix(1700000000, 0).UTC(),
	}))
	span.End()
	require.NoError(t, tr.Flush(context.Background()))

	var req otlpExportRequest
	require.NoError(t, json.Unmarshal(c.received()[0], &req))
	s := req.ResourceSpans[0].ScopeSpans[0].Spans[0]
	require.NotNil(t, s.Status)
	require.Equal(t, 2, s.Status.Code, "2 is STATUS_CODE_ERROR")
	require.Equal(t, "the environment could not be created", s.Status.Message)
}

// The signal-specific variable is used exactly as given; the general one has
// the signal's path appended. Backwards posts every trace to the collector's
// root and produces a 404 that reads as a broken collector.
func TestTheEndpointFollowsTheStandardConvention(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }

	require.Empty(t, tracesEndpoint(get))

	env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://collector:4318"
	require.Equal(t, "http://collector:4318/v1/traces", tracesEndpoint(get))

	env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://collector:4318/"
	require.Equal(t, "http://collector:4318/v1/traces", tracesEndpoint(get),
		"a trailing slash must not produce a double slash")

	env["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"] = "http://collector:4318/somewhere/else"
	require.Equal(t, "http://collector:4318/somewhere/else", tracesEndpoint(get),
		"the signal specific variable is used exactly as given")
}

func TestTheEndpointItPostsToIsTheOneItResolved(t *testing.T) {
	tr, c := tracingTo(t, nil)
	_, span := tr.Tracer().Start(context.Background(), "af up")
	span.End()
	require.NoError(t, tr.Flush(context.Background()))
	require.Equal(t, "/v1/traces", c.path)
}

func TestHeadersCarryACollectorToken(t *testing.T) {
	get := func(k string) string {
		if k == "OTEL_EXPORTER_OTLP_HEADERS" {
			return "authorization=Bearer abc123, x-tenant = acme "
		}
		return ""
	}
	h := otlpHeaders(get)
	require.Equal(t, "Bearer abc123", h["authorization"])
	require.Equal(t, "acme", h["x-tenant"], "spaces around the pair are trimmed")

	require.Nil(t, otlpHeaders(func(string) string { return "" }))
	require.Nil(t, otlpHeaders(func(string) string { return "junk-with-no-equals" }))
}

// A collector that refuses must be reported and must not stop anything. The
// observability of a thing is not the thing.
func TestACollectorThatRefusesIsReportedRatherThanFatal(t *testing.T) {
	c := &collector{status: http.StatusInternalServerError}
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)

	exp, err := newOTLPJSONExporter(srv.URL+"/v1/traces", nil, nil)
	require.NoError(t, err)

	tr, err := NewTracing(context.Background(), TracingOptions{
		Exporter: exp, ServiceName: "test-engine",
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)

	_, span := tr.Tracer().Start(context.Background(), "af up")
	span.End()
	err = tr.Flush(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
	require.NoError(t, tr.Shutdown(context.Background()))
}

func TestExportingAfterShutdownIsANoOpRatherThanAnError(t *testing.T) {
	c := &collector{}
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)

	exp, err := newOTLPJSONExporter(srv.URL, nil, nil)
	require.NoError(t, err)
	require.NoError(t, exp.Shutdown(context.Background()))
	require.NoError(t, exp.ExportSpans(context.Background(), nil))
	require.Empty(t, c.received())
}
