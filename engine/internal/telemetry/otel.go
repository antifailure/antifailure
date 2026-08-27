package telemetry

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/antifailure/antifailure/engine/internal/events"
)

// Tracing is the engine's OpenTelemetry provider.
//
// It is off unless it is configured, and off means a no-op tracer rather than a
// disabled exporter: a developer running `af up` on a laptop should pay nothing
// for a feature they have not asked for, and the laptop is not a thing anybody
// scrapes. Configuration is the standard OTEL_ environment, so an operator who
// already runs a collector points the engine at it with the variable they
// already know, and nothing in this repository invents a name for it.
//
// The interesting part is not the export, it is the redaction. A span attribute
// is a string that leaves the machine, and the engine's strings include
// connection URLs with passwords in them. Everything on its way to a log
// already goes through the redactor at the writer rather than at the call site,
// because the call site somebody forgot is how a secret reaches a log. A span
// is a log with a different shape, so the same rule holds here: attributes are
// built in exactly one function in this file, from events that are already
// redacted, and a test asserts that no other package in the engine imports the
// tracing API at all.
type Tracing struct {
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
	enabled  bool
}

// TracingOptions configures tracing.
type TracingOptions struct {
	// Getenv reads configuration. Nil uses the process environment.
	Getenv func(string) string
	// ServiceName overrides OTEL_SERVICE_NAME.
	ServiceName string
	// Version is the engine version, reported as an attribute.
	Version string
	// Exporter replaces the OTLP exporter, for tests.
	Exporter sdktrace.SpanExporter
}

// serviceName is what the engine calls itself when nothing else says.
const serviceName = "antifailure-engine"

// NewTracing builds a tracer provider, or a no-op one when nothing is
// configured.
//
// Nothing configured means no OTEL_EXPORTER_OTLP_ENDPOINT and no
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT, which is the state every developer
// machine is in. OTEL_SDK_DISABLED=true turns it off even when an endpoint is
// present, which is the standard escape hatch and the one an operator will
// reach for during an incident.
func NewTracing(ctx context.Context, opts TracingOptions) (*Tracing, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	exporter := opts.Exporter
	if exporter == nil {
		if strings.EqualFold(strings.TrimSpace(getenv("OTEL_SDK_DISABLED")), "true") {
			return disabledTracing(), nil
		}
		endpoint := firstNonEmpty(
			getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"),
			getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		)
		if endpoint == "" {
			return disabledTracing(), nil
		}
		exp, err := otlptracehttp.New(ctx)
		if err != nil {
			// Reported and then ignored. An unreachable collector must not stop
			// an environment coming up, for exactly the reason an unreachable
			// control plane must not: the observability of the thing is not the
			// thing.
			return disabledTracing(), fmt.Errorf("telemetry: tracing is off: %w", err)
		}
		exporter = exp
	}

	name := strings.TrimSpace(opts.ServiceName)
	if name == "" {
		name = strings.TrimSpace(getenv("OTEL_SERVICE_NAME"))
	}
	if name == "" {
		name = serviceName
	}

	attrs := []attribute.KeyValue{
		attribute.String("service.name", name),
		attribute.String("telemetry.sdk.language", "go"),
	}
	if opts.Version != "" {
		attrs = append(attrs, attribute.String("service.version", opts.Version))
	}
	// The resource is built by hand rather than through resource.New with its
	// detectors. The host detector reads the hostname and the process detector
	// reads the command line, and the command line of an `af` invocation can
	// carry a connection string in a flag. Nothing is collected here that was
	// not chosen here.
	res := resource.NewSchemaless(attrs...)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(2*time.Second)),
		sdktrace.WithResource(res),
	)
	return &Tracing{provider: tp, tracer: tp.Tracer(name), enabled: true}, nil
}

func disabledTracing() *Tracing {
	return &Tracing{tracer: noop.NewTracerProvider().Tracer(serviceName)}
}

// Enabled reports whether spans go anywhere.
func (t *Tracing) Enabled() bool { return t != nil && t.enabled }

// Tracer returns the tracer. It is never nil, so callers do not branch.
func (t *Tracing) Tracer() trace.Tracer {
	if t == nil || t.tracer == nil {
		return noop.NewTracerProvider().Tracer(serviceName)
	}
	return t.tracer
}

// Flush sends what is buffered without tearing the provider down.
//
// Bounded, for the same reason Shutdown is: a collector that has gone away must
// cost a command a few seconds rather than its exit.
func (t *Tracing) Flush(ctx context.Context) error {
	if t == nil || t.provider == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return t.provider.ForceFlush(ctx)
}

// Shutdown flushes what is buffered, bounded so that a command does not hang on
// a collector that has gone away.
func (t *Tracing) Shutdown(ctx context.Context) error {
	if t == nil || t.provider == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return t.provider.Shutdown(ctx)
}

// Install makes this the process-wide provider, so that a library reaching for
// otel.Tracer finds it. Returns a function that puts back what was there.
func (t *Tracing) Install() func() {
	if t == nil || t.provider == nil {
		return func() {}
	}
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(t.provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	return func() { otel.SetTracerProvider(previous) }
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// spanSink turns bus events into span events on the command's span.
//
// A span per event would produce a thousand one-microsecond spans for a build,
// which is a trace nobody can read. Span events on the phase span keep the
// shape of the command visible and put the detail where somebody drilling in
// will find it.
//
// This file is the only place in the engine that constructs a span attribute.
// TestOnlyTheTelemetryPackageTouchesTheTracingAPI keeps it that way, because a
// second place is a second thing to remember to redact.
type spanSink struct {
	span     trace.Span
	redactor redactor
}

func newSpanSink(span trace.Span, r redactor) *spanSink {
	return &spanSink{span: span, redactor: r}
}

// Name identifies the sink in drop counters.
func (s *spanSink) Name() string { return "trace" }

// Deliver records one event on the span.
func (s *spanSink) Deliver(_ context.Context, e events.Event) error {
	if s.span == nil || !s.span.IsRecording() {
		return nil
	}
	attrs := []attribute.KeyValue{
		attribute.String("af.event.type", string(e.Type)),
		attribute.Int64("af.event.seq", int64(e.Seq)),
	}
	if e.Env != "" {
		attrs = append(attrs, attribute.String("af.env", e.Env))
	}
	if e.Msg != "" {
		attrs = append(attrs, attribute.String("af.message", s.redactor.String(e.Msg)))
	}
	for k, v := range e.Data {
		attrs = append(attrs, s.attr("af.data."+k, v))
	}

	s.span.AddEvent(string(e.Type), trace.WithAttributes(attrs...), trace.WithTimestamp(e.TS))
	if e.Level == events.LevelError {
		s.span.SetStatus(codes.Error, s.redactor.String(e.Msg))
	}
	return nil
}

// attr converts one payload value, redacting every string on the way.
//
// Numbers and booleans pass through as themselves: a redactor that turned an
// integer into a string would make every duration in every trace unqueryable,
// and an integer cannot carry a password. Everything else is rendered as a
// string and redacted, which is the conservative direction.
func (s *spanSink) attr(key string, v any) attribute.KeyValue {
	switch t := v.(type) {
	case string:
		return attribute.String(key, s.redactor.String(t))
	case bool:
		return attribute.Bool(key, t)
	case int:
		return attribute.Int(key, t)
	case int64:
		return attribute.Int64(key, t)
	case uint64:
		return attribute.Int64(key, int64(t))
	case float64:
		return attribute.Float64(key, t)
	case time.Duration:
		return attribute.Float64(key, t.Seconds())
	default:
		return attribute.String(key, s.redactor.String(fmt.Sprint(v)))
	}
}

// Close does nothing: the span belongs to whoever started it.
func (s *spanSink) Close() error { return nil }
