package telemetry

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

func tracingWithMemory(t *testing.T) (*Tracing, *tracetest.InMemoryExporter) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tr, err := NewTracing(context.Background(), TracingOptions{
		Exporter: exp, ServiceName: "test-engine", Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Shutdown(context.Background()) })
	return tr, exp
}

// The test the lane exists to write: a connection string cannot reach a span
// attribute.
//
// A span attribute is a string that leaves the machine, so it is a log with a
// different shape and the same rule applies. Both directions are covered: a
// value the engine registered because it created it, and a value nobody
// registered that the default rules recognise on sight. The second matters
// more, because the first depends on somebody having remembered.
func TestASecretInAnEventNeverReachesASpanAttribute(t *testing.T) {
	r := redact.New()
	const registered = "registered-password-9df13a"
	r.Register(registered)
	const neverRegistered = "postgres://app:hunter2-not-registered@db.internal:5432/app"

	tr, exp := tracingWithMemory(t)
	ctx, span := tr.Tracer().Start(context.Background(), "af up")
	sink := newSpanSink(span, r)

	require.NoError(t, sink.Deliver(ctx, events.Event{
		ID: "e1", Env: "env-1", Seq: 1, Type: events.EnvReady,
		Level: events.LevelInfo,
		Msg:   "branched from " + registered,
		TS:    time.Unix(1700000000, 0).UTC(),
		Data: map[string]any{
			"database_url": neverRegistered,
			"password":     registered,
			"seconds":      1.5,
			"services":     3,
		},
	}))
	span.End()
	// Flush rather than Shutdown: tracetest's in-memory exporter resets itself
	// when it is shut down, so asserting after a Shutdown asserts on an empty
	// list and passes for the wrong reason.
	require.NoError(t, tr.Flush(context.Background()))

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	require.NotEmpty(t, spans[0].Events)

	var seen []string
	for _, e := range spans[0].Events {
		for _, a := range e.Attributes {
			seen = append(seen, a.Value.Emit())
		}
	}
	for _, v := range seen {
		require.NotContainsf(t, v, registered, "a registered secret reached a span attribute: %q", v)
		require.NotContainsf(t, v, "hunter2-not-registered",
			"a connection string password reached a span attribute: %q", v)
	}

	// And the point of redacting rather than dropping: what is left is still
	// worth having.
	joined := strings.Join(seen, " ")
	require.Contains(t, joined, "db.internal", "the host survives, or the trace explains nothing")
	require.Contains(t, joined, "1.5", "a number is not a string and is not mangled into one")
}

func TestAnErrorEventMarksTheSpanAsFailed(t *testing.T) {
	tr, exp := tracingWithMemory(t)
	ctx, span := tr.Tracer().Start(context.Background(), "af up")
	sink := newSpanSink(span, redact.New())

	require.NoError(t, sink.Deliver(ctx, events.Event{
		ID: "e1", Env: "env-1", Seq: 1, Type: events.EnvFailed,
		Level: events.LevelError, Msg: "the environment could not be created",
		TS: time.Unix(1700000000, 0).UTC(),
	}))
	span.End()
	require.NoError(t, tr.Flush(context.Background()))

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, "Error", spans[0].Status.Code.String())
}

// Nothing configured is the state every developer machine is in, and it must
// cost nothing rather than cost an unreachable exporter.
func TestTracingIsOffUnlessAnEndpointIsConfigured(t *testing.T) {
	tr, err := NewTracing(context.Background(), TracingOptions{
		Getenv: func(string) string { return "" },
	})
	require.NoError(t, err)
	require.False(t, tr.Enabled())
	require.NotNil(t, tr.Tracer(), "a disabled tracer is still a tracer, so callers do not branch")

	_, span := tr.Tracer().Start(context.Background(), "af up")
	require.False(t, span.IsRecording())
	span.End()
	require.NoError(t, tr.Shutdown(context.Background()))
}

func TestTheStandardDisableSwitchIsObeyed(t *testing.T) {
	env := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
		"OTEL_SDK_DISABLED":           "TRUE",
	}
	tr, err := NewTracing(context.Background(), TracingOptions{
		Getenv: func(k string) string { return env[k] },
	})
	require.NoError(t, err)
	require.False(t, tr.Enabled(), "OTEL_SDK_DISABLED is the switch an operator reaches for mid-incident")
	require.NoError(t, tr.Shutdown(context.Background()))
}

// Redaction happens at the writer rather than at the call site, because the
// call site somebody forgot is how a secret reaches a log. That is only true
// while there is one writer. This test is what keeps it to one: it fails the
// moment a second package in the engine starts building spans of its own, at
// which point somebody has to either route through here or take on the
// redaction themselves, deliberately, rather than by not noticing.
func TestOnlyTheTelemetryPackageTouchesTheTracingAPI(t *testing.T) {
	const tracingPrefix = "go.opentelemetry.io/otel"
	root := filepath.Join("..", "..")

	offenders := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		pkgDir := filepath.Dir(path)
		if filepath.Base(pkgDir) == "telemetry" {
			return nil
		}
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if perr != nil {
			return nil
		}
		for _, imp := range f.Imports {
			p, uerr := strconv.Unquote(imp.Path.Value)
			if uerr != nil {
				continue
			}
			if strings.HasPrefix(p, tracingPrefix) {
				offenders[path] = append(offenders[path], p)
			}
		}
		return nil
	})
	require.NoError(t, err)

	if len(offenders) > 0 {
		var lines []string
		for file, imports := range offenders {
			lines = append(lines, fmt.Sprintf("  %s imports %v", file, imports))
		}
		t.Fatalf("the OpenTelemetry API is imported outside internal/telemetry:\n%s\n"+
			"Span attributes are strings that leave the machine, and they are redacted in "+
			"exactly one function so that there is no call site to forget. Route the events "+
			"through the bus instead, or move the attribute building into internal/telemetry.",
			strings.Join(lines, "\n"))
	}
}

// A guard on the guard above: if the walk found nothing to look at, it would
// pass while checking nothing.
func TestTheImportScanActuallyReadsTheEngine(t *testing.T) {
	root := filepath.Join("..", "..")
	var goFiles int
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") {
			goFiles++
		}
		return nil
	}))
	require.Greater(t, goFiles, 100, "the scan is not seeing the engine")
}

var _ sdktrace.SpanExporter = (*tracetest.InMemoryExporter)(nil)

// A span sink attached to a span that is not recording must be free, because
// that is the state on every machine with no collector configured.
func TestDeliveringToANonRecordingSpanDoesNothing(t *testing.T) {
	tr, err := NewTracing(context.Background(), TracingOptions{Getenv: func(string) string { return "" }})
	require.NoError(t, err)
	_, span := tr.Tracer().Start(context.Background(), "af up")
	sink := newSpanSink(span, redact.New())
	require.NoError(t, sink.Deliver(context.Background(), events.Event{Type: events.EnvReady}))
	require.NoError(t, sink.Close())
	span.End()
}
