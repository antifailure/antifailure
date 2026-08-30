package load

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// OpenTelemetry rather than Datadog or New Relic, and traces rather than an
// agent.
//
// The manifest offered four sources and three of them were refused at run
// time, which is worse than offering one: a key somebody can set that cannot
// work reads as a bug in the product rather than as a gap in it. Of the three,
// OpenTelemetry is the one that can be implemented completely and verified
// offline, because OTLP/JSON is a published wire format and a collector's file
// exporter writes it to disk. Datadog and New Relic would each need an
// account, a region, an API key and a live query to prove anything, and the
// test would be a mock of their API rather than a test of ours.
//
// It also buys something an access log cannot give. A combined format line has
// no duration in it, so every route read from a log has a zero baseline, which
// means HasBaseline is false, which means p95_increase can never fire. The
// threshold was real and unreachable. A span carries a start and an end, so a
// shape read from traces arrives with production's p95 per route already in
// it, and the comparison the threshold describes finally has something to
// compare against.

// minBaselineSamples is how many requests a route needs before its p95 is
// treated as a baseline.
//
// A route seen three times in an export has a p95 that is one of those three
// numbers, and a threshold measured against it fires on noise. A check that
// fires on noise is a check people turn off, so a thin route arrives with no
// baseline and is never a breach, which is the same rule Breaches already
// applies to a route it has never seen.
const minBaselineSamples = 20

// TraceRead is what reading a trace export found, including what it did not
// use.
//
// The skipped counts are reported rather than swallowed because the commonest
// failure here is an export full of client spans, or one whose exporter wrote
// the old attribute names. Both produce an empty shape, and "no requests were
// found" is a much harder message to act on than "4,812 spans were not server
// spans".
type TraceRead struct {
	Shape Shape
	// Spans is how many server spans became traffic.
	Spans int
	// Skipped counts what was read and not used, by reason.
	Skipped map[string]int
}

// FromOTLP reads a shape out of an OpenTelemetry trace export.
//
// The input is OTLP/JSON: either one ExportTraceServiceRequest document, or
// one per line, which is what the collector's file exporter writes. Both are
// accepted because both are what people actually have on disk, and refusing
// the newline delimited form would refuse the output of the standard exporter.
func FromOTLP(data []byte) (TraceRead, error) {
	read := TraceRead{Skipped: map[string]int{}}

	docs, err := otlpDocuments(data, read.Skipped)
	if err != nil {
		return read, err
	}

	type bucket struct {
		method    string
		path      string
		durations []float64
	}
	buckets := map[string]*bucket{}
	var first, last uint64

	for _, doc := range docs {
		for _, rs := range doc.ResourceSpans {
			scopes := rs.ScopeSpans
			if len(scopes) == 0 {
				scopes = rs.InstrumentationLibrarySpans
			}
			for _, scope := range scopes {
				for _, span := range scope.Spans {
					method, path, reason := otlpRoute(span)
					if reason != "" {
						read.Skipped[reason]++
						continue
					}
					key := method + " " + path
					b := buckets[key]
					if b == nil {
						b = &bucket{method: method, path: path}
						buckets[key] = b
					}
					b.durations = append(b.durations, otlpDurationMs(span))
					read.Spans++

					start, end := uint64(span.Start), uint64(span.End)
					if start > 0 && (first == 0 || start < first) {
						first = start
					}
					if end > last {
						last = end
					}
				}
			}
		}
	}

	if read.Spans == 0 {
		return read, fmt.Errorf(
			"no server spans with an HTTP method and path were found in the export")
	}

	read.Shape = Shape{
		Source:            "otel",
		RequestsPerSecond: float64(read.Spans) / otlpWindow(first, last).Seconds(),
	}
	for _, b := range buckets {
		r := Route{Method: b.method, Path: b.path, Weight: float64(len(b.durations))}
		if len(b.durations) >= minBaselineSamples {
			r.P95Ms = percentiles(b.durations).P95Ms
		}
		read.Shape.Routes = append(read.Shape.Routes, r)
	}
	sort.Slice(read.Shape.Routes, func(i, j int) bool {
		if read.Shape.Routes[i].Weight != read.Shape.Routes[j].Weight {
			return read.Shape.Routes[i].Weight > read.Shape.Routes[j].Weight
		}
		return read.Shape.Routes[i].String() < read.Shape.Routes[j].String()
	})
	return read, nil
}

// otlpWindow turns the first and last span timestamps into a period to divide
// by.
//
// Clamped to a second at the bottom. An export of forty spans that all
// happened inside the same millisecond would otherwise report forty thousand
// requests a second, and the scale factor would then multiply a number that
// was never real.
func otlpWindow(first, last uint64) time.Duration {
	if last <= first {
		return time.Second
	}
	d := time.Duration(last-first) * time.Nanosecond
	if d < time.Second {
		return time.Second
	}
	return d
}

// otlpDocuments splits an export into the documents it holds.
//
// One JSON value covers a hand written sample and an exporter that wrote a
// single batch. Line by line covers the collector's file exporter, which
// writes one ExportTraceServiceRequest per line and appends forever. A line
// that will not parse is counted and skipped rather than failing the read,
// because a truncated final line is the normal state of a file something is
// still writing to, and discarding an hour of traffic over it would be
// absurd.
func otlpDocuments(data []byte, skipped map[string]int) ([]otlpExport, error) {
	var single otlpExport
	if err := json.Unmarshal(data, &single); err == nil {
		return []otlpExport{single}, nil
	}

	var docs []otlpExport
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var doc otlpExport
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			skipped["unreadable line"]++
			continue
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("this is not an OTLP/JSON trace export: no readable document was found")
	}
	return docs, nil
}

// otlpRoute pulls the method and path out of a span, or says why it could not.
//
// Only server spans. A client span is an outbound call the service made, and
// replaying those as inbound traffic would send this environment's own
// dependency calls at itself.
func otlpRoute(s otlpSpan) (method, path, reason string) {
	if s.Kind != otlpKindServer {
		return "", "", "not a server span"
	}

	attrs := map[string]string{}
	for _, a := range s.Attributes {
		if v := a.Value.text(); v != "" {
			attrs[a.Key] = v
		}
	}

	// Both spellings. The stable semantic conventions renamed every one of
	// these in 1.21 and exporters in the field still write the old names, so
	// reading only the new ones would return an empty shape from a real
	// export and blame the user for it.
	method = firstOf(attrs, "http.request.method", "http.method")

	// http.route first, because it is already templated: /users/{id} rather
	// than /users/4821. Normalising a raw path guesses at what was an
	// identifier, and the instrumentation did not have to guess.
	if route := attrs["http.route"]; route != "" {
		path = route
	} else if raw := firstOf(attrs, "url.path", "http.target"); raw != "" {
		path = NormalisePath(stripQuery(raw))
	} else if full := attrs["http.url"]; full != "" {
		if u, err := url.Parse(full); err == nil {
			path = NormalisePath(u.Path)
		}
	}

	if method == "" || path == "" {
		// The convention for a server span's name is "{method} {route}", and
		// an exporter that sets the name and no HTTP attributes is common
		// enough to be worth the fallback.
		if m, p, ok := strings.Cut(s.Name, " "); ok && strings.HasPrefix(p, "/") {
			if method == "" {
				method = m
			}
			if path == "" {
				path = p
			}
		}
	}
	if method == "" || path == "" {
		return "", "", "no HTTP method or path on the span"
	}
	if !strings.HasPrefix(path, "/") {
		return "", "", "the span's path is not a path"
	}
	return strings.ToUpper(method), path, ""
}

func firstOf(attrs map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := attrs[k]; v != "" {
			return v
		}
	}
	return ""
}

func stripQuery(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}

// otlpDurationMs is how long the span took, in milliseconds.
func otlpDurationMs(s otlpSpan) float64 {
	if s.End <= s.Start {
		return 0
	}
	return float64(s.End-s.Start) / 1e6
}

// otlpExport is the OTLP/JSON trace export.
type otlpExport struct {
	ResourceSpans []otlpResource `json:"resourceSpans"`
}

type otlpResource struct {
	ScopeSpans []otlpScope `json:"scopeSpans"`
	// Collectors before 0.60 wrote this key instead. Files written by them
	// are still on disk, and refusing one would refuse the sample somebody
	// actually has.
	InstrumentationLibrarySpans []otlpScope `json:"instrumentationLibrarySpans"`
}

type otlpScope struct {
	Spans []otlpSpan `json:"spans"`
}

type otlpSpan struct {
	Name       string     `json:"name"`
	Kind       otlpKind   `json:"kind"`
	Start      otlpNanos  `json:"startTimeUnixNano"`
	End        otlpNanos  `json:"endTimeUnixNano"`
	Attributes []otlpAttr `json:"attributes"`
}

type otlpAttr struct {
	Key   string        `json:"key"`
	Value otlpAttrValue `json:"value"`
}

// otlpAttrValue is the AnyValue union, of which only the scalar arms matter
// here. An array or a map attribute is not a method or a path.
type otlpAttrValue struct {
	StringValue *string    `json:"stringValue"`
	IntValue    *otlpNanos `json:"intValue"`
	BoolValue   *bool      `json:"boolValue"`
}

func (v otlpAttrValue) text() string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		return strconv.FormatUint(uint64(*v.IntValue), 10)
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue)
	}
	return ""
}

// otlpNanos is a 64 bit integer that arrives as a string or as a number.
//
// The protobuf JSON mapping says 64 bit integers are encoded as strings, and
// the Go OTLP exporter obeys it. Several other producers, including hand
// written fixtures and some collector processors, emit a bare number. A
// decoder that accepted only one of them would reject half the real files for
// a reason that has nothing to do with the traffic in them.
type otlpNanos uint64

func (n *otlpNanos) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return fmt.Errorf("expected a 64 bit integer, found %s", string(b))
	}
	*n = otlpNanos(v)
	return nil
}

// otlpKind is the span kind, which arrives as a number or as its enum name.
type otlpKind int

const otlpKindServer otlpKind = 2

func (k *otlpKind) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	switch s {
	case "", "null":
		return nil
	case "SPAN_KIND_UNSPECIFIED":
		*k = 0
	case "SPAN_KIND_INTERNAL":
		*k = 1
	case "SPAN_KIND_SERVER":
		*k = otlpKindServer
	case "SPAN_KIND_CLIENT":
		*k = 3
	case "SPAN_KIND_PRODUCER":
		*k = 4
	case "SPAN_KIND_CONSUMER":
		*k = 5
	default:
		v, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("expected a span kind, found %s", string(b))
		}
		*k = otlpKind(v)
	}
	return nil
}
