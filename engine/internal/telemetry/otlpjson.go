package telemetry

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// An OTLP exporter written here rather than imported, and the reason is the
// dependency set rather than the protocol.
//
// go.opentelemetry.io/otel, its SDK and its API were already in this module's
// graph, pulled in by the Docker client, so using them costs nothing. The
// official OTLP exporter is a different matter: otlptracehttp speaks protobuf,
// and adding it pulls google.golang.org/protobuf, google.golang.org/grpc,
// grpc-gateway and two genproto modules into a binary that has none of them.
// Measured rather than assumed: 157 packages from those trees link into `af`,
// and go.work's own comment says the workspace exists so that "the shipped
// binary's dependency set stays small and auditable".
//
// OTLP over HTTP has a JSON encoding as well as a protobuf one, defined by the
// protobuf JSON mapping and accepted by the OpenTelemetry Collector. It is
// about two hundred lines of encoding/json, it needs nothing this module does
// not already have, and it puts the serialisation of every span attribute in a
// file in this repository, which is where the redaction guarantee wants it
// anyway.
//
// The mapping's sharp edges, each of which is a silent wrong answer rather than
// an error if you get it wrong: identifiers are lowercase hex strings and not
// byte arrays; 64 bit integers are STRINGS, because JSON numbers cannot hold
// them exactly; and timestamps are unix nanoseconds, also as strings.

// otlpJSONExporter posts spans to an OTLP endpoint as JSON.
type otlpJSONExporter struct {
	endpoint string
	headers  map[string]string
	client   *http.Client

	mu      sync.Mutex
	stopped bool
}

// newOTLPJSONExporter builds an exporter for a collector endpoint.
//
// The endpoint follows the OpenTelemetry environment convention: a signal
// specific variable is used exactly as given, and the general one has the
// signal's path appended. Getting that backwards posts traces to the
// collector's root and gets a 404 that reads like the collector is broken.
func newOTLPJSONExporter(endpoint string, headers map[string]string, client *http.Client) (*otlpJSONExporter, error) {
	if _, err := url.Parse(endpoint); err != nil {
		return nil, fmt.Errorf("telemetry: %q is not a URL: %w", endpoint, err)
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &otlpJSONExporter{endpoint: endpoint, headers: headers, client: client}, nil
}

// ExportSpans sends one batch.
func (e *otlpJSONExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	e.mu.Lock()
	stopped := e.stopped
	e.mu.Unlock()
	if stopped {
		return nil
	}

	body, err := json.Marshal(otlpRequest(spans))
	if err != nil {
		return fmt.Errorf("telemetry: encode spans: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telemetry: build the export request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.headers {
		req.Header.Set(k, v)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("telemetry: export to %s: %w", e.endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Drained rather than ignored, so the connection can be reused. A collector
	// answers every export, and leaving each body unread costs a connection per
	// batch.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("telemetry: %s answered %s", e.endpoint, resp.Status)
	}
	return nil
}

// Shutdown stops accepting batches.
func (e *otlpJSONExporter) Shutdown(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopped = true
	return nil
}

// The OTLP JSON shapes. Only the fields this engine populates are here; the
// protocol permits omitting the rest, and carrying fields nothing sets would be
// a larger surface to keep correct for no benefit.

type otlpExportRequest struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes,omitempty"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type otlpSpan struct {
	TraceID string `json:"traceId"`
	SpanID  string `json:"spanId"`
	// Omitted rather than sent as zeroes for a root span: a parent identifier
	// of all zeroes is a valid encoding of "no parent" in protobuf and is not
	// in JSON, where some collectors read it as a parent that does not exist.
	ParentSpanID      string          `json:"parentSpanId,omitempty"`
	Name              string          `json:"name"`
	Kind              int             `json:"kind"`
	StartTimeUnixNano string          `json:"startTimeUnixNano"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano"`
	Attributes        []otlpKeyValue  `json:"attributes,omitempty"`
	Events            []otlpSpanEvent `json:"events,omitempty"`
	Status            *otlpStatus     `json:"status,omitempty"`
}

type otlpSpanEvent struct {
	TimeUnixNano string         `json:"timeUnixNano"`
	Name         string         `json:"name"`
	Attributes   []otlpKeyValue `json:"attributes,omitempty"`
}

type otlpStatus struct {
	Message string `json:"message,omitempty"`
	Code    int    `json:"code"`
}

type otlpKeyValue struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

// otlpValue is a protobuf oneof, so exactly one field is set. Encoded with
// pointers so that a false boolean and a zero integer are distinguishable from
// an unset field, which they would not be with omitempty on the values.
type otlpValue struct {
	StringValue *string `json:"stringValue,omitempty"`
	BoolValue   *bool   `json:"boolValue,omitempty"`
	// A string, and this is the one that catches people. Protobuf's JSON
	// mapping encodes 64 bit integers as strings because a JSON number cannot
	// represent them exactly, and a collector reading a number where it expects
	// a string rejects the whole batch.
	IntValue    *string  `json:"intValue,omitempty"`
	DoubleValue *float64 `json:"doubleValue,omitempty"`
}

func otlpRequest(spans []sdktrace.ReadOnlySpan) otlpExportRequest {
	// Grouped by resource and scope, which is what the protocol asks for. Every
	// span from one engine process shares both, so this is one group in
	// practice and the loop is here for the case where it is not.
	type groupKey struct{ resource, scope string }
	order := make([]groupKey, 0, 1)
	groups := map[groupKey][]otlpSpan{}
	resources := map[groupKey]otlpResource{}
	scopes := map[groupKey]otlpScope{}

	for _, s := range spans {
		res := otlpResource{Attributes: otlpAttributes(s.Resource().Attributes())}
		scope := otlpScope{Name: s.InstrumentationScope().Name, Version: s.InstrumentationScope().Version}
		key := groupKey{fingerprintOf(res.Attributes), scope.Name + "\x00" + scope.Version}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
			resources[key] = res
			scopes[key] = scope
		}
		groups[key] = append(groups[key], otlpSpanOf(s))
	}

	out := otlpExportRequest{ResourceSpans: make([]otlpResourceSpans, 0, len(order))}
	for _, key := range order {
		out.ResourceSpans = append(out.ResourceSpans, otlpResourceSpans{
			Resource:   resources[key],
			ScopeSpans: []otlpScopeSpans{{Scope: scopes[key], Spans: groups[key]}},
		})
	}
	return out
}

func fingerprintOf(attrs []otlpKeyValue) string {
	var b strings.Builder
	for _, a := range attrs {
		b.WriteString(a.Key)
		b.WriteByte('=')
		if a.Value.StringValue != nil {
			b.WriteString(*a.Value.StringValue)
		}
		b.WriteByte(';')
	}
	return b.String()
}

func otlpSpanOf(s sdktrace.ReadOnlySpan) otlpSpan {
	ctx := s.SpanContext()
	span := otlpSpan{
		TraceID:           hex.EncodeToString(idBytes(ctx.TraceID())),
		SpanID:            hex.EncodeToString(idBytes16(ctx.SpanID())),
		Name:              s.Name(),
		Kind:              int(s.SpanKind()),
		StartTimeUnixNano: strconv.FormatInt(s.StartTime().UnixNano(), 10),
		EndTimeUnixNano:   strconv.FormatInt(s.EndTime().UnixNano(), 10),
		Attributes:        otlpAttributes(s.Attributes()),
	}
	if parent := s.Parent(); parent.IsValid() {
		span.ParentSpanID = hex.EncodeToString(idBytes16(parent.SpanID()))
	}
	for _, e := range s.Events() {
		span.Events = append(span.Events, otlpSpanEvent{
			TimeUnixNano: strconv.FormatInt(e.Time.UnixNano(), 10),
			Name:         e.Name,
			Attributes:   otlpAttributes(e.Attributes),
		})
	}
	if st := s.Status(); st.Code != codes.Unset {
		span.Status = &otlpStatus{Message: st.Description, Code: statusCodeOf(st.Code)}
	}
	return span
}

func statusCodeOf(c codes.Code) int {
	switch c {
	case codes.Ok:
		return 1
	case codes.Error:
		return 2
	default:
		return 0
	}
}

func idBytes(id trace.TraceID) []byte { b := [16]byte(id); return b[:] }
func idBytes16(id trace.SpanID) []byte {
	b := [8]byte(id)
	return b[:]
}

func otlpAttributes(attrs []attribute.KeyValue) []otlpKeyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]otlpKeyValue, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, otlpKeyValue{Key: string(a.Key), Value: otlpValueOf(a.Value)})
	}
	return out
}

func otlpValueOf(v attribute.Value) otlpValue {
	switch v.Type() {
	case attribute.BOOL:
		b := v.AsBool()
		return otlpValue{BoolValue: &b}
	case attribute.INT64:
		s := strconv.FormatInt(v.AsInt64(), 10)
		return otlpValue{IntValue: &s}
	case attribute.FLOAT64:
		f := v.AsFloat64()
		return otlpValue{DoubleValue: &f}
	default:
		// Everything else is rendered as a string, including the slice types.
		// The protocol has array values; nothing in this engine produces one,
		// and a shape nothing exercises is a shape nothing has proved.
		s := v.Emit()
		return otlpValue{StringValue: &s}
	}
}

// tracesEndpoint resolves the collector URL from the standard environment.
//
// The signal-specific variable is used exactly as given; the general one has
// /v1/traces appended. Getting that the other way round posts to the
// collector's root and produces a 404 that reads as a broken collector.
func tracesEndpoint(getenv func(string) string) string {
	if signal := strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")); signal != "" {
		return signal
	}
	general := strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if general == "" {
		return ""
	}
	return strings.TrimSuffix(general, "/") + "/v1/traces"
}

// otlpHeaders parses OTEL_EXPORTER_OTLP_HEADERS, which is a comma separated
// list of key=value pairs and is where an authentication token for a hosted
// collector lives.
func otlpHeaders(getenv func(string) string) map[string]string {
	raw := strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_HEADERS"))
	if raw == "" {
		return nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
