package load

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
)

// Options configure a run.
type Options struct {
	// BaseURL is where the environment is.
	BaseURL string
	// Shape is the traffic mix.
	Shape Shape
	// Scale multiplies production's rate. One reproduces it; a tenth is what
	// a pull request check usually wants.
	Scale float64
	// Duration is how long to send for.
	Duration time.Duration
	// Concurrency bounds requests in flight. It is a ceiling rather than a
	// target: the rate decides how many are sent, and this stops a slow
	// application from queueing an unbounded number of them.
	Concurrency int
	// Seed makes two runs send the same sequence.
	Seed int64
	// Clock is the time source.
	Clock clock.Clock
	// Progress receives a line every second, and may be nil.
	Progress func(Progress)
}

// Progress is how far along a run is.
type Progress struct {
	Elapsed  time.Duration
	Sent     int
	Errors   int
	P95Ms    float64
	Inflight int
}

// Result is what a run measured.
type Result struct {
	// Source says where the traffic mix came from, so a reader can tell
	// production's shape from the default. Shape.Source has claimed since it
	// was written that it exists "so a report can say whether it is real
	// traffic or a guess", and until this field nothing carried it out of the
	// run: the comment described an intention rather than a behaviour.
	Source string `json:"source,omitempty"`
	// TargetRate is the arrival rate the run aimed for, which is the shape's
	// rate times the scale. Rate below is what was achieved, and the gap
	// between the two is the first thing worth looking at.
	TargetRate float64 `json:"target_rate,omitempty"`
	// Sent is how many requests completed, successfully or not.
	Sent int `json:"sent"`
	// Duration is how long the run took.
	Duration time.Duration `json:"-"`
	// Rate is the achieved requests per second, which is not the target when
	// the application could not keep up. Reporting the target instead is how
	// a load test says everything was fine while the queue grew.
	Rate float64 `json:"rate"`
	// Routes are the per route measurements.
	Routes []RouteResult `json:"routes"`
	// Errors counts responses that were not a success, by reason.
	Errors map[string]int `json:"errors,omitempty"`
	// ErrorRate is the share of requests that failed.
	ErrorRate float64 `json:"error_rate"`
	// Overall is every request together.
	Overall Latency `json:"overall"`
}

// RouteResult is one route's measurement.
type RouteResult struct {
	Route   string  `json:"route"`
	Sent    int     `json:"sent"`
	Errors  int     `json:"errors"`
	Latency Latency `json:"latency"`
	// BaselineP95Ms is what production serves it in, when known.
	BaselineP95Ms float64 `json:"baseline_p95_ms,omitempty"`
	// P95Increase is how much slower this environment is, as a ratio. Zero
	// when there is no baseline to compare against, which is said rather than
	// reported as no change.
	P95Increase float64 `json:"p95_increase,omitempty"`
	// HasBaseline distinguishes no change from nothing to compare with.
	HasBaseline bool `json:"has_baseline"`
}

// Latency is a distribution.
//
// Percentiles rather than an average, because an average hides the tail and
// the tail is what a user notices. A p50 that halves while the p99 doubles is
// a regression that an average reports as an improvement.
type Latency struct {
	P50Ms float64 `json:"p50_ms"`
	P90Ms float64 `json:"p90_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
	MaxMs float64 `json:"max_ms"`
}

// Run sends the shape at the environment and measures what came back.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Clock == nil {
		opts.Clock = clock.New()
	}
	if opts.Scale <= 0 {
		opts.Scale = 1
	}
	if opts.Duration <= 0 {
		opts.Duration = 30 * time.Second
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 20
	}

	picker, err := NewPicker(opts.Shape.Routes, opts.Seed)
	if err != nil {
		return nil, err
	}
	rate := opts.Shape.RequestsPerSecond * opts.Scale

	client := &http.Client{
		Timeout: 30 * time.Second,
		// A response cannot authorize traffic outside the selected safe route.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			MaxIdleConnsPerHost: opts.Concurrency,
			// Compression off, so the numbers measure the application rather
			// than the transport's ability to compress its output.
			DisableCompression: true,
		},
	}

	// A finished run does not keep its sockets. Both transports here are
	// private to the run and set no IdleConnTimeout, so every keep alive
	// connection the run opened stays open after the last request, with its
	// readLoop and writeLoop goroutines still parked on it. In production that
	// is a run holding file descriptors it no longer uses. In the tests it is
	// why this package goes red at random: `goleak.VerifyTestMain` in
	// goleak_test.go sees those two goroutines per connection and cannot know
	// they are idle.
	//
	// WHAT IS PROVEN AND WHAT IS NOT. Proven: the CI failure is
	// nondeterministic rather than caused by the commit it appeared on. The
	// engine tree is byte identical between d03b6c20, which was green, and
	// 2f046c9d, which failed here, and the only difference between those two
	// commits is two .tsx files under www. Also proven: a run that has
	// returned was holding idle sockets open, because these transports set no
	// IdleConnTimeout, and that is worth fixing whatever it does to the test.
	// NOT proven: that this is the cause of that failure. I could not
	// reproduce it locally in 36 runs, 15 with the fix, 15 with the fix
	// removed and 6 more under -race with it removed, and all 36 passed. So
	// this is a correct change with an unproven relationship to the red run.
	// If load goes red on goleak again, this comment is the place to start,
	// and the next step is a reproduction rather than another guess.
	defer client.CloseIdleConnections()

	m := &meter{samples: map[string][]float64{}, errors: map[string]int{}, counts: map[string]int{}, errsBy: map[string]int{}}
	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup

	started := opts.Clock.Now()
	deadline := started.Add(opts.Duration)
	reportAt := started.Add(time.Second)

	for opts.Clock.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			wg.Wait()
			return finish(m, opts, started), ctx.Err()
		default:
		}

		route := picker.Next()
		sem <- struct{}{}
		wg.Add(1)
		go func(r Route) {
			defer wg.Done()
			defer func() { <-sem }()
			send(ctx, client, opts.BaseURL, r, m, opts.Clock, opts.Shape.Source == "safe_routes" || opts.Shape.Source == "default")
		}(route)

		if opts.Progress != nil && !opts.Clock.Now().Before(reportAt) {
			reportAt = opts.Clock.Now().Add(time.Second)
			snapshot := m.snapshot()
			opts.Progress(Progress{
				Elapsed:  opts.Clock.Since(started).Round(time.Second),
				Sent:     snapshot.Sent,
				Errors:   int(snapshot.ErrorRate * float64(snapshot.Sent)),
				P95Ms:    snapshot.Overall.P95Ms,
				Inflight: len(sem),
			})
		}

		if wait := picker.Interval(rate); wait > 0 {
			select {
			case <-ctx.Done():
			case <-opts.Clock.After(wait):
			}
		}
	}
	wg.Wait()
	return finish(m, opts, started), nil
}

func send(
	ctx context.Context, client *http.Client, baseURL string,
	route Route, m *meter, c clock.Clock, smoke bool,
) {
	status, elapsed, err := sendOnce(ctx, client, baseURL, route, c)
	reason := ""
	switch {
	case err != nil:
		reason = classify(err)
	case status >= 500 || (smoke && status >= 400):
		// A mix read from production contains the routes production serves,
		// and some of those answer 404 for most of their traffic. Counting a
		// 404 here would report the shape's own contents as a failure. A
		// scenario or synthetic smoke is judged differently: a declared path
		// that gets a 404 is broken rather than observed production traffic.
		reason = strconv.Itoa(status)
	}
	m.record(route.String(), elapsed, reason)
}

// sendOnce performs one request and reports what came back.
//
// Shared with the scenario executor, which needs the status code rather than
// the mix's judgement of it.
func sendOnce(
	ctx context.Context, client *http.Client, baseURL string,
	route Route, c clock.Clock,
) (status int, elapsedMs float64, err error) {
	url := strings.TrimSuffix(baseURL, "/") + route.Path
	req, err := http.NewRequestWithContext(ctx, route.Method, url, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %w", errMalformedRequest, err)
	}
	// Marked, so an application that wants to behave differently under
	// generated load can, and so an access log can tell the two apart.
	req.Header.Set("X-Antifailure-Load", "1")

	at := c.Now()
	resp, err := client.Do(req)
	elapsedMs = float64(c.Since(at).Microseconds()) / 1000
	if err != nil {
		return 0, elapsedMs, err
	}
	// Drained, or the connection cannot be reused and the run measures
	// connection setup rather than the application.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	return resp.StatusCode, elapsedMs, nil
}

// errMalformedRequest marks a request that could not be built at all, which is
// a fault in the shape rather than in the environment answering it.
var errMalformedRequest = errors.New("malformed request")

// classify turns a transport error into a short reason.
func classify(err error) string {
	if errors.Is(err, errMalformedRequest) {
		return "malformed request"
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "context deadline exceeded"), strings.Contains(text, "Timeout"):
		return "timeout"
	case strings.Contains(text, "connection refused"):
		return "connection refused"
	case strings.Contains(text, "connection reset"):
		return "connection reset"
	case strings.Contains(text, "no such host"):
		return "name not resolved"
	default:
		return "request failed"
	}
}

// meter collects samples.
type meter struct {
	mu      sync.Mutex
	samples map[string][]float64
	counts  map[string]int
	errsBy  map[string]int
	errors  map[string]int
	all     []float64
	sent    int
	failed  int
}

func (m *meter) record(route string, ms float64, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent++
	m.counts[route]++
	if ms > 0 {
		m.samples[route] = append(m.samples[route], ms)
		m.all = append(m.all, ms)
	}
	if reason != "" {
		m.failed++
		m.errors[reason]++
		m.errsBy[route]++
	}
}

func (m *meter) snapshot() Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := Result{Sent: m.sent, Overall: percentiles(m.all)}
	if m.sent > 0 {
		res.ErrorRate = float64(m.failed) / float64(m.sent)
	}
	return res
}

func finish(m *meter, opts Options, started time.Time) *Result {
	m.mu.Lock()
	defer m.mu.Unlock()

	elapsed := opts.Clock.Since(started)
	res := &Result{
		Source:     opts.Shape.Source,
		TargetRate: opts.Shape.RequestsPerSecond * opts.Scale,
		Sent:       m.sent, Duration: elapsed,
		Overall: percentiles(m.all),
		Errors:  map[string]int{},
	}
	for k, v := range m.errors {
		res.Errors[k] = v
	}
	if elapsed > 0 {
		// The achieved rate, not the target. Reporting the target is how a
		// load test says everything was fine while the queue grew.
		res.Rate = float64(m.sent) / elapsed.Seconds()
	}
	if m.sent > 0 {
		res.ErrorRate = float64(m.failed) / float64(m.sent)
	}

	baselines := map[string]float64{}
	for _, r := range opts.Shape.Routes {
		baselines[r.String()] = r.P95Ms
	}
	for route, count := range m.counts {
		rr := RouteResult{
			Route: route, Sent: count, Errors: m.errsBy[route],
			Latency: percentiles(m.samples[route]),
		}
		if base := baselines[route]; base > 0 {
			rr.BaselineP95Ms = base
			rr.HasBaseline = true
			// A ratio rather than a difference, because a route that takes
			// two milliseconds and now takes four is a doubling worth seeing
			// and four milliseconds of absolute change is not.
			rr.P95Increase = rr.Latency.P95Ms/base - 1
		}
		res.Routes = append(res.Routes, rr)
	}
	sort.Slice(res.Routes, func(i, j int) bool {
		// Worst regression first, because that is the line somebody is
		// looking for and scrolling to find it is the same as not showing it.
		if res.Routes[i].P95Increase != res.Routes[j].P95Increase {
			return res.Routes[i].P95Increase > res.Routes[j].P95Increase
		}
		return res.Routes[i].Route < res.Routes[j].Route
	})
	return res
}

// percentiles computes a distribution from samples.
func percentiles(samples []float64) Latency {
	if len(samples) == 0 {
		return Latency{}
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	at := func(p float64) float64 {
		// The nearest rank method, which is what every other tool reports and
		// which needs no interpolation to explain.
		i := int(math.Ceil(p*float64(len(sorted)))) - 1
		if i < 0 {
			i = 0
		}
		if i >= len(sorted) {
			i = len(sorted) - 1
		}
		return sorted[i]
	}
	return Latency{
		P50Ms: at(0.50), P90Ms: at(0.90), P95Ms: at(0.95), P99Ms: at(0.99),
		MaxMs: sorted[len(sorted)-1],
	}
}

// Breach is a threshold that was exceeded.
type Breach struct {
	What     string  `json:"what"`
	Limit    float64 `json:"limit"`
	Measured float64 `json:"measured"`
	Detail   string  `json:"detail"`
}

// InertP95 reports a p95_increase threshold that was evaluated against
// nothing.
//
// Breaches skips every route with no baseline, which is right: comparing
// against nothing and calling the answer a regression is how a check becomes
// noise. But when NO route has one, the whole threshold was inert, and a run
// that reports green having compared nothing is a check everybody believes is
// running. The manifest refuses the combination that guarantees this, so what
// reaches here is the case the manifest cannot see: the default p95_increase
// under a source that carries no durations, or a trace export whose routes
// were all too thin to earn a baseline.
//
// A run that sent nothing is a different problem and says so elsewhere, so it
// is not reported as an inert threshold as well.
func (r *Result) InertP95(p95Increase float64) bool {
	if p95Increase <= 0 || len(r.Routes) == 0 {
		return false
	}
	for _, route := range r.Routes {
		if route.HasBaseline {
			return false
		}
	}
	return true
}

// Breaches reports which thresholds a result exceeded.
//
// A route with no baseline is never a breach. Comparing against nothing and
// calling the answer a regression is how a check becomes noise.
func (r *Result) Breaches(p95Increase, errorRate float64) []Breach {
	var out []Breach
	if errorRate > 0 && r.ErrorRate > errorRate {
		out = append(out, Breach{
			What: "error rate", Limit: errorRate, Measured: r.ErrorRate,
			Detail: fmt.Sprintf("%.1f percent of requests failed", r.ErrorRate*100),
		})
	}
	if p95Increase > 0 {
		for _, route := range r.Routes {
			if !route.HasBaseline || route.P95Increase <= p95Increase {
				continue
			}
			out = append(out, Breach{
				What: route.Route, Limit: p95Increase, Measured: route.P95Increase,
				Detail: fmt.Sprintf("%.0fms against a baseline of %.0fms, %.0f percent slower",
					route.Latency.P95Ms, route.BaselineP95Ms, route.P95Increase*100),
			})
		}
	}
	return out
}
