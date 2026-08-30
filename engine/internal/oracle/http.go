package oracle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/clock"
)

// Probe is one request sent to both sides.
//
// A declared request rather than an agent. The agents in runner/ plan their
// next click from what they see, which makes two runs of the same workflow
// send different requests in a different order, and a diff of two different
// request sequences is noise wearing the costume of a finding. The oracle needs
// both sides to receive the same bytes in the same order, so the plan is
// written down.
type Probe struct {
	// Name identifies the probe in the report, and is what a reader searches
	// for when they want to reproduce it.
	Name string
	// Method is the HTTP method.
	Method string
	// Path is the path and query, starting with a slash.
	Path string
	// Headers are sent as given, on both sides.
	Headers map[string]string
	// Body is the request body, sent byte for byte to both sides.
	Body string
}

// Response is one side's answer to one probe.
type Response struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"-"`
	Body    []byte      `json:"-"`
	// ContentType is the media type with its parameters stripped, because
	// "application/json" and "application/json; charset=utf-8" are the same
	// decision and two frameworks spell it two ways.
	ContentType string `json:"content_type,omitempty"`
	// Bytes is the body length, reported because a body compared as a digest
	// still has a size worth seeing.
	Bytes int `json:"bytes"`
	// Err is why there is no response at all: a refused connection, a timeout,
	// a name that did not resolve.
	Err string `json:"error,omitempty"`
	// DurationMs is how long the request took. Never compared: it is wall
	// clock on a machine running two environments at once, so a difference in
	// it says something about the machine.
	DurationMs int64 `json:"duration_ms"`
}

// ProbeResult is one probe's outcome on both sides.
type ProbeResult struct {
	Name      string   `json:"name"`
	Method    string   `json:"method"`
	Path      string   `json:"path"`
	Baseline  Response `json:"baseline"`
	Candidate Response `json:"candidate"`
	// Findings is how many differences this probe produced, so the summary
	// table has a column worth reading.
	Findings int `json:"findings"`
}

// Driver sends probes.
type Driver struct {
	Client *http.Client
	Clock  clock.Clock
	// MaxBody bounds what is read from a response. A body larger than this is
	// still compared, as a digest, and the report says so.
	MaxBody int64
}

// DefaultMaxBody is how much of a response body is read.
//
// Eight megabytes. Large enough for any JSON document an API returns and small
// enough that a probe pointed at a file download does not put the file in
// memory twice.
const DefaultMaxBody = 8 << 20

// Send performs one probe against one base URL.
//
// A transport failure is a Response with Err set rather than an error return.
// One side refusing the connection while the other served the request is the
// single most important thing this package can report, and returning an error
// would abandon the run at exactly the moment it found something.
func (d *Driver) Send(ctx context.Context, baseURL string, p Probe) Response {
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	limit := d.MaxBody
	if limit <= 0 {
		limit = DefaultMaxBody
	}
	c := d.Clock
	if c == nil {
		c = clock.New()
	}

	url := strings.TrimSuffix(baseURL, "/") + p.Path
	var body io.Reader
	if p.Body != "" {
		body = strings.NewReader(p.Body)
	}
	method := p.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return Response{Err: err.Error()}
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	// Marked, so an application that logs its traffic can tell an oracle
	// request from a person's, and so an access log taken during a run does
	// not become next week's traffic shape.
	req.Header.Set("X-Antifailure-Oracle", "1")

	at := c.Now()
	resp, err := client.Do(req)
	elapsed := c.Since(at).Milliseconds()
	if err != nil {
		return Response{Err: err.Error(), DurationMs: elapsed}
	}
	defer func() { _ = resp.Body.Close() }()

	read, readErr := io.ReadAll(io.LimitReader(resp.Body, limit))
	out := Response{
		Status:     resp.StatusCode,
		Headers:    resp.Header.Clone(),
		Body:       read,
		Bytes:      len(read),
		DurationMs: elapsed,
	}
	if readErr != nil {
		out.Err = "reading the response body: " + readErr.Error()
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		if media, _, mErr := mime.ParseMediaType(ct); mErr == nil {
			out.ContentType = media
		} else {
			out.ContentType = ct
		}
	}
	return out
}

// Drive sends every probe to both sides and returns what each answered.
//
// Each probe goes to the baseline and then immediately to the candidate,
// rather than the whole plan to one side and then the whole plan to the other.
// Two reasons, and the second is the load bearing one. Any value that comes
// from the clock is much more likely to agree when the two requests are
// milliseconds apart than when they are a minute apart, which means the
// normalisers have less to absorb and the report has fewer paths listed as
// normalised. And a probe that depends on a previous probe's write sees the
// same state on both sides at the same point in the sequence, which is what
// makes a plan of more than one request mean anything.
//
// Sequential, never concurrent. Concurrency would make the order of the two
// databases' writes depend on scheduling, and then the identifier a row got
// would depend on scheduling, and every finding after the first would be a
// consequence of that rather than of the change.
func Drive(
	ctx context.Context, d *Driver, baselineURL, candidateURL string, probes []Probe,
	progress func(name string, index, total int),
) []ProbeResult {
	out := make([]ProbeResult, 0, len(probes))
	for i, p := range probes {
		if err := ctx.Err(); err != nil {
			// The rest are not sent and are not reported as agreeing. A probe
			// that never ran is not evidence of anything.
			break
		}
		if progress != nil {
			progress(p.Name, i+1, len(probes))
		}
		res := ProbeResult{Name: p.Name, Method: p.Method, Path: p.Path}
		res.Baseline = d.Send(ctx, baselineURL, p)
		res.Candidate = d.Send(ctx, candidateURL, p)
		out = append(out, res)
	}
	return out
}

// compareResponses is the whole HTTP comparison for one probe.
func compareResponses(
	cfg Config, collect *collector, order int, name string, base, cand Response,
) ([]Finding, bool) {
	d := &differ{
		cfg: cfg, ignore: newMatcher(cfg.IgnoreFields), collect: collect,
		where: name, order: order,
	}

	// A side that did not answer at all outranks everything else, and nothing
	// below it would mean anything: comparing an empty body against a real one
	// would report every field as missing.
	switch {
	case base.Err != "" && cand.Err != "":
		f := newFinding(KindTransport, Minor, name, "")
		f.Baseline, f.Candidate = base.Err, cand.Err
		f.Detail = "neither side answered, so this probe compared nothing"
		d.add(f)
		return d.findings, d.truncated
	case cand.Err != "":
		f := newFinding(KindTransport, Critical, name, "")
		f.Baseline = fmt.Sprintf("%d", base.Status)
		f.Candidate = cand.Err
		f.Detail = "the baseline answered and the candidate did not"
		d.add(f)
		return d.findings, d.truncated
	case base.Err != "":
		f := newFinding(KindTransport, Minor, name, "")
		f.Baseline = base.Err
		f.Candidate = fmt.Sprintf("%d", cand.Status)
		f.Detail = "the candidate answered and the baseline did not"
		d.add(f)
		return d.findings, d.truncated
	}

	d.compareStatus(base.Status, cand.Status)
	d.compareHeaders(base.Headers, cand.Headers)
	d.compareBodies(base, cand)

	sortFindings(d.findings)
	return d.findings, d.truncated
}

// compareStatus ranks a status change by direction.
//
// Moving into 5xx is a regression under any reading. Moving from 2xx to 4xx is
// a request the baseline served and the candidate refused, which is the same
// thing from the caller's side. Moving OUT of an error class is a fix, and
// reporting a fix as critical is how a gate teaches people to ignore it.
func (d *differ) compareStatus(base, cand int) {
	if base == cand {
		return
	}
	bc, cc := base/100, cand/100
	if bc == cc {
		f := newFinding(KindStatus, Major, d.where, "")
		f.Baseline, f.Candidate = fmt.Sprint(base), fmt.Sprint(cand)
		f.Detail = fmt.Sprintf("both are %dxx, so a caller that branches on the class "+
			"still works and one that branches on the code does not", bc)
		d.add(f)
		return
	}
	sev, detail := Major, fmt.Sprintf("%dxx became %dxx", bc, cc)
	switch {
	case cc == 5:
		sev = Critical
		detail = "the candidate returned a server error where the baseline did not"
	case bc == 2 && cc == 4:
		sev = Critical
		detail = "the candidate refused a request the baseline served"
	case bc >= 4 && cc == 2:
		sev = Minor
		detail = "the candidate served a request the baseline refused"
	}
	f := newFinding(KindStatusClass, sev, d.where, "")
	f.Baseline, f.Candidate = fmt.Sprint(base), fmt.Sprint(cand)
	f.Detail = detail
	d.add(f)
}

func (d *differ) compareHeaders(base, cand http.Header) {
	names := map[string]bool{}
	for k := range base {
		names[strings.ToLower(k)] = true
	}
	for k := range cand {
		names[strings.ToLower(k)] = true
	}
	sorted := make([]string, 0, len(names))
	for k := range names {
		if d.cfg.ignoredHeader(k) {
			continue
		}
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	for _, name := range sorted {
		// Values joined rather than compared element by element. A header sent
		// twice and a header sent once with a comma are the same header by the
		// specification, and a comparison that called them different would fire
		// on a framework upgrade rather than on a change.
		bv := strings.Join(base.Values(name), ", ")
		cv := strings.Join(cand.Values(name), ", ")
		if bv == cv {
			continue
		}
		// content-type has its own finding kind, because it changes how the
		// body is read and is therefore a fact about more than one header.
		if name == "content-type" {
			continue
		}
		f := newFinding(KindHeader, Minor, d.where, name)
		f.Baseline, f.Candidate = bv, cv
		d.add(f)
	}
}

func (d *differ) compareBodies(base, cand Response) {
	if base.ContentType != cand.ContentType {
		f := newFinding(KindContentType, Major, d.where, "")
		f.Baseline, f.Candidate = orNone(base.ContentType), orNone(cand.ContentType)
		f.Detail = "the response changed media type, so a client parsing it will not"
		d.add(f)
		return
	}

	if isJSON(base.ContentType) {
		bv, bErr := decodeJSON(base.Body)
		cv, cErr := decodeJSON(cand.Body)
		switch {
		case bErr != nil && cErr != nil:
			// Both unparseable and declared JSON. Compared as bytes, and said
			// out loud: a body that does not parse is worth knowing about
			// whether or not it changed.
			d.compareBytes(base, cand, "the body is declared JSON and parses on neither side")
			return
		case cErr != nil:
			f := newFinding(KindBodyParse, Critical, d.where, "")
			f.Detail = "the candidate returned a body declared JSON that does not parse: " + cErr.Error()
			d.add(f)
			return
		case bErr != nil:
			f := newFinding(KindBodyParse, Minor, d.where, "")
			f.Detail = "the baseline returned a body declared JSON that does not parse: " + bErr.Error()
			d.add(f)
			return
		}
		d.diffValue(rootPath(), bv, cv)
		return
	}

	d.compareBytes(base, cand, "")
}

// compareBytes is the fallback for a body this package cannot read
// structurally.
//
// It reports that the body differs and where the first difference is, and it
// does not try to be a text diff. A text diff of two HTML pages is a wall of
// output about a session token, and this package has no way to know which parts
// of a page are meant to be stable. The documentation says to point probes at
// endpoints that return JSON, and this is the honest answer for everything
// else.
func (d *differ) compareBytes(base, cand Response, note string) {
	if bytes.Equal(base.Body, cand.Body) {
		return
	}
	f := newFinding(KindBodyBytes, Minor, d.where, "")
	f.Baseline = digest(base.Body)
	f.Candidate = digest(cand.Body)
	at := firstDifference(base.Body, cand.Body)
	f.Detail = fmt.Sprintf("the bodies are not identical, first differing at byte %d", at)
	if note != "" {
		f.Detail = note + ", and " + f.Detail
	}
	d.add(f)
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%d bytes, sha256:%s", len(b), hex.EncodeToString(sum[:])[:16])
}

func firstDifference(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// isJSON reports whether a media type carries a JSON document.
//
// The +json suffix is checked as well as the exact type, because
// application/problem+json and application/vnd.api+json are JSON and a
// comparison that read them as bytes would report a digest where it could have
// reported a field.
func isJSON(mediaType string) bool {
	m := strings.ToLower(mediaType)
	return m == "application/json" || m == "text/json" || strings.HasSuffix(m, "+json")
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
