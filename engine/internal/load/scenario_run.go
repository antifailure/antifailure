package load

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
)

// ScenarioRun is one scenario and how much of it to run.
type ScenarioRun struct {
	Scenario *Scenario
	// Sessions is how many run the journey at once.
	Sessions int
	// Iterations is how many times each session walks it.
	//
	// A count rather than a duration, so the work a scenario does is declared
	// rather than decided by how long the clock happened to run for. Two runs
	// of one scenario send the same number of requests, which is what makes
	// the two results comparable at all.
	Iterations int
	// StartAfter delays this scenario, so one journey can burst while another
	// is already running. That is the load that actually breaks things, and
	// with every scenario starting at zero it cannot be expressed.
	StartAfter time.Duration
}

// ScenarioOptions configure a scenario run.
type ScenarioOptions struct {
	BaseURL string
	Runs    []ScenarioRun
	// SafeRoutes and UnsafeRoutes are the manifest's, applied to a scenario
	// exactly as they are applied to the mix.
	SafeRoutes   []string
	UnsafeRoutes []string
	Seed         int64
	Concurrency  int
	Clock        clock.Clock
	Progress     func(Progress)
}

// ScenarioResult is what one scenario did and what it proved.
type ScenarioResult struct {
	Scenario    string `json:"scenario"`
	Description string `json:"description,omitempty"`
	// Verdict is the one word answer, from the same vocabulary the rest of
	// the product uses.
	Verdict string `json:"verdict"`
	// Detail says why, in one sentence, for a reader who stops here.
	Detail     string `json:"detail"`
	Sessions   int    `json:"sessions"`
	Iterations int    `json:"iterations"`
	Sent       int    `json:"sent"`
	// ScheduledMs is how long the plan asked for, from the run starting to
	// its last request going out. DurationMs is how long it took for the last
	// one to come back. A run that takes much longer than its schedule is a
	// run the application could not keep up with, which is the finding a load
	// test exists to produce and which an average latency hides.
	ScheduledMs float64 `json:"scheduled_ms"`
	DurationMs  float64 `json:"duration_ms"`
	// Steps are the per route measurements, worst first.
	Steps []RouteResult `json:"steps,omitempty"`
	// Assertions are what was asked and what the answer was.
	Assertions []AssertionResult `json:"assertions,omitempty"`
	// Refused are the routes the safe list would not allow, which is why a
	// blocked scenario is blocked.
	Refused []string `json:"refused_as_unsafe,omitempty"`
	Overall Latency  `json:"overall"`
}

// AssertionResult is one assertion's answer.
//
// Detail is the sentence a person reads. The four fields after it are the same
// answer in numbers, for a reader that has to chart it or store it in columns:
// a hosted console cannot draw "served a p95 of 240ms, over 200ms" and cannot
// tell that sentence from a different measure's without parsing English.
type AssertionResult struct {
	Name    string `json:"name"`
	Verdict string `json:"verdict"`
	Detail  string `json:"detail"`
	// Measure is which of the four this assertion asked for:
	// every_request_succeeded, p95_below_ms, error_rate_below or status_in.
	Measure string `json:"measure,omitempty"`
	// Scope is the route it was narrowed to, empty for a scenario wide one.
	Scope string `json:"scope,omitempty"`
	// Threshold is the number the assertion declared, and Observed is what was
	// measured against it. Both are absent for the two measures that are not
	// numeric comparisons, and Observed is absent when nothing was sent, which
	// is a different answer from zero.
	Threshold *float64 `json:"threshold,omitempty"`
	Observed  *float64 `json:"observed,omitempty"`
}

// RunScenarios executes scenarios against the environment.
//
// Scenarios run together rather than one after another, because StartAfter
// only means anything if they share a clock: a burst of one journey while
// another is running is the whole point, and running them in sequence would
// make every scenario a solo.
func RunScenarios(ctx context.Context, opts ScenarioOptions) ([]ScenarioResult, error) {
	if opts.Clock == nil {
		opts.Clock = clock.New()
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 20
	}
	if len(opts.Runs) == 0 {
		return nil, fmt.Errorf("load: there are no scenarios to run")
	}

	results := make([]ScenarioResult, len(opts.Runs))
	meters := make([]*scenarioMeter, len(opts.Runs))
	plans := make([]Plan, len(opts.Runs))

	// Safety first, and before anything is sent. A scenario that names a
	// route nobody declared safe does not run, and neither do the others in
	// the same file until it is fixed: sending half of a journey would
	// produce a measurement of half a journey and label it with the whole
	// journey's name.
	for i, run := range opts.Runs {
		results[i] = ScenarioResult{
			Scenario: run.Scenario.Name, Description: run.Scenario.Description,
			Sessions: max(run.Sessions, 1), Iterations: max(run.Iterations, 1),
		}
		_, refused := Shape{Routes: run.Scenario.Routes()}.Safe(opts.SafeRoutes, opts.UnsafeRoutes)
		if len(refused) > 0 {
			for _, r := range refused {
				results[i].Refused = append(results[i].Refused, r.String())
			}
			results[i].Verdict = VerdictBlocked
			results[i].Detail = fmt.Sprintf(
				"it did not run: %s not named in safe_routes", plural(len(refused), "request is", "requests are"))
			results[i].Assertions = blockedAssertions(run.Scenario, results[i].Detail)
			continue
		}
		meters[i] = newScenarioMeter()
		plans[i] = PlanScenario(run.Scenario, run.Sessions, run.Iterations, opts.Seed, run.StartAfter)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: opts.Concurrency,
			// Compression off, so the numbers measure the application rather
			// than the transport's ability to compress its output.
			DisableCompression: true,
		},
	}

	// One list across every scenario, in offset order, so the scenarios
	// genuinely overlap on one timeline.
	type job struct {
		run     int
		request PlannedRequest
	}
	var jobs []job
	for i := range plans {
		for _, req := range plans[i].Requests {
			jobs = append(jobs, job{run: i, request: req})
		}
	}
	sort.SliceStable(jobs, func(a, b int) bool {
		if jobs[a].request.Offset != jobs[b].request.Offset {
			return jobs[a].request.Offset < jobs[b].request.Offset
		}
		return jobs[a].run < jobs[b].run
	})

	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup
	started := opts.Clock.Now()
	reportAt := started.Add(time.Second)

	for _, j := range jobs {
		if err := waitUntil(ctx, opts.Clock, started, j.request.Offset); err != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(runIndex int, route Route) {
			defer wg.Done()
			defer func() { <-sem }()
			status, elapsed, err := sendOnce(ctx, client, opts.BaseURL, route, opts.Clock)
			meters[runIndex].record(route.String(), status, elapsed, err, opts.Clock.Since(started))
		}(j.run, j.request.Route)

		if opts.Progress != nil && !opts.Clock.Now().Before(reportAt) {
			reportAt = opts.Clock.Now().Add(time.Second)
			sent, failed, p95 := totals(meters)
			opts.Progress(Progress{
				Elapsed: opts.Clock.Since(started).Round(time.Second),
				Sent:    sent, Errors: failed, P95Ms: p95, Inflight: len(sem),
			})
		}
	}
	wg.Wait()

	for i, run := range opts.Runs {
		if meters[i] == nil {
			continue
		}
		// This scenario's own last completion rather than the whole run's
		// elapsed time. Scenarios share a timeline, so reporting the run's
		// total against each of them would say a scenario that finished in
		// two seconds took twenty.
		results[i].ScheduledMs = float64(plans[i].Span.Microseconds()) / 1000
		meters[i].fill(&results[i])
		results[i].Assertions = judge(run.Scenario, meters[i])
		results[i].Verdict, results[i].Detail = summarise(results[i])
	}
	return results, ctx.Err()
}

// waitUntil sleeps until the offset, or returns when the context ends.
//
// A request whose moment has already passed goes out at once rather than
// being dropped. The alternative is a scenario that silently sends fewer
// requests than it planned on a slow machine and reports the same numbers.
func waitUntil(ctx context.Context, c clock.Clock, started time.Time, offset time.Duration) error {
	remaining := offset - c.Since(started)
	if remaining <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.After(remaining):
		return nil
	}
}

// plural renders a count with the right verb, so a report does not say that
// one request are not named.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func joinComma(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

// blockedAssertions marks every assertion blocked when the scenario did not
// run.
//
// Blocked rather than failed, and the difference is the whole reason this
// product has two words. A scenario that could not be sent has found nothing
// wrong with the application, and reporting it as a failure would blame the
// change for a gap in the manifest.
func blockedAssertions(s *Scenario, detail string) []AssertionResult {
	out := make([]AssertionResult, 0, len(s.Assertions))
	for _, a := range s.Assertions {
		out = append(out, AssertionResult{Name: a.Name, Verdict: VerdictBlocked, Detail: detail})
	}
	return out
}

// summarise turns the assertions into the scenario's verdict.
//
// The same precedence report.Run.Verdict uses: a failure outranks everything,
// then blocked, then unverified. A scenario that asserted nothing is
// unverified rather than passing, because it ran and proved nothing, and
// calling that a pass is how a check that measures nothing reports green
// forever.
func summarise(r ScenarioResult) (verdict, detail string) {
	counts := map[string]int{}
	for _, a := range r.Assertions {
		counts[a.Verdict]++
	}
	switch {
	case counts[VerdictFail] > 0:
		for _, a := range r.Assertions {
			if a.Verdict == VerdictFail {
				return VerdictFail, a.Detail
			}
		}
	case counts[VerdictBlocked] > 0:
		return VerdictBlocked, "some assertions could not be evaluated"
	case len(r.Assertions) == 0:
		return VerdictUnverified, fmt.Sprintf(
			"%d requests were sent and the scenario asserts nothing, so nothing was proved", r.Sent)
	case counts[VerdictUnverified] > 0:
		for _, a := range r.Assertions {
			if a.Verdict == VerdictUnverified {
				return VerdictUnverified, a.Detail
			}
		}
	}
	return VerdictPass, fmt.Sprintf("%d requests, %d assertions held", r.Sent, len(r.Assertions))
}

// judge evaluates the assertions against what was measured.
func judge(s *Scenario, m *scenarioMeter) []AssertionResult {
	out := make([]AssertionResult, 0, len(s.Assertions))
	for _, a := range s.Assertions {
		out = append(out, evaluate(a, m))
	}
	return out
}

// evaluate answers an assertion, in words and then in numbers.
//
// The verdict half is deliberately left exactly as it was and is not read by
// the numeric half. Deriving the verdict from a threshold comparison computed
// here would put the decision in two places, and the sentences below are the
// ones customers have been reading; a refactor that changed one of them by a
// word would be an invisible break in a report.
func evaluate(a Assertion, m *scenarioMeter) AssertionResult {
	out := evaluateVerdict(a, m)
	out.Measure = a.Measure()
	out.Scope = a.scopeRoute()
	out.Threshold, out.Observed = a.reading(m)
	return out
}

// reading is the assertion's threshold and what was measured against it.
//
// Nil for a measure that is not a numeric comparison, and nil for Observed
// when the requests it is about were never sent, because zero and nothing
// measured are different answers and a console charting the first when it
// means the second says the application was perfect.
func (a Assertion) reading(m *scenarioMeter) (threshold, observed *float64) {
	stat, ok := a.statFor(m)
	switch {
	case a.P95BelowMs > 0:
		threshold = &a.P95BelowMs
		if ok && stat.sent > 0 {
			p95 := percentiles(stat.samples).P95Ms
			observed = &p95
		}
	case a.ErrorRateBelow > 0:
		threshold = &a.ErrorRateBelow
		if ok && stat.sent > 0 {
			rate := float64(stat.failed) / float64(stat.sent)
			observed = &rate
		}
	}
	return threshold, observed
}

// statFor finds the measurements an assertion is about.
func (a Assertion) statFor(m *scenarioMeter) (routeStat, bool) {
	if a.Step == "" {
		return m.overall(), true
	}
	route, err := parseRequest(a.Step)
	if err != nil {
		return routeStat{}, false
	}
	return m.forRoute(route.String())
}

// scopeRoute is the assertion's step, spelled the way a route is spelled
// everywhere else, or empty when it is about the whole scenario.
func (a Assertion) scopeRoute() string {
	if a.Step == "" {
		return ""
	}
	route, err := parseRequest(a.Step)
	if err != nil {
		return a.Step
	}
	return route.String()
}

func evaluateVerdict(a Assertion, m *scenarioMeter) AssertionResult {
	scope := "the scenario"
	stat := m.overall()
	if a.Step != "" {
		route, err := parseRequest(a.Step)
		if err != nil {
			// Validation already refused this, so reaching it means a caller
			// built a Scenario by hand and skipped Validate.
			return AssertionResult{Name: a.Name, Verdict: VerdictBlocked,
				Detail: fmt.Sprintf("the step %q is not a request", a.Step)}
		}
		scope = route.String()
		found, ok := m.forRoute(route.String())
		if !ok {
			// Unverified rather than passed. An assertion about a request
			// nothing sent has measured nothing, and reporting it as held is
			// how a typo in a step name becomes a green check forever.
			return AssertionResult{Name: a.Name, Verdict: VerdictUnverified,
				Detail: fmt.Sprintf("no request to %s was sent, so there is nothing to measure", scope)}
		}
		stat = found
	}
	if stat.sent == 0 {
		return AssertionResult{Name: a.Name, Verdict: VerdictUnverified,
			Detail: "no requests were sent, so there is nothing to measure"}
	}

	switch {
	case a.EveryRequestSucceeded != nil:
		if !*a.EveryRequestSucceeded {
			return AssertionResult{Name: a.Name, Verdict: VerdictPass,
				Detail: "every_request_succeeded is false, so nothing is required"}
		}
		if stat.failed == 0 {
			return AssertionResult{Name: a.Name, Verdict: VerdictPass,
				Detail: fmt.Sprintf("all %d requests to %s answered below 400", stat.sent, scope)}
		}
		return AssertionResult{Name: a.Name, Verdict: VerdictFail,
			Detail: fmt.Sprintf("%d of %d requests to %s failed: %s",
				stat.failed, stat.sent, scope, stat.reasons())}

	case a.P95BelowMs > 0:
		measured := percentiles(stat.samples).P95Ms
		if measured < a.P95BelowMs {
			return AssertionResult{Name: a.Name, Verdict: VerdictPass,
				Detail: fmt.Sprintf("%s served a p95 of %.0fms, under %.0fms", scope, measured, a.P95BelowMs)}
		}
		return AssertionResult{Name: a.Name, Verdict: VerdictFail,
			Detail: fmt.Sprintf("%s served a p95 of %.0fms, over %.0fms", scope, measured, a.P95BelowMs)}

	case a.ErrorRateBelow > 0:
		rate := float64(stat.failed) / float64(stat.sent)
		if rate < a.ErrorRateBelow {
			return AssertionResult{Name: a.Name, Verdict: VerdictPass,
				Detail: fmt.Sprintf("%.1f percent of requests to %s failed, under %.1f percent",
					rate*100, scope, a.ErrorRateBelow*100)}
		}
		return AssertionResult{Name: a.Name, Verdict: VerdictFail,
			Detail: fmt.Sprintf("%.1f percent of requests to %s failed, over %.1f percent: %s",
				rate*100, scope, a.ErrorRateBelow*100, stat.reasons())}

	case len(a.StatusIn) > 0:
		allowed := map[int]bool{}
		for _, code := range a.StatusIn {
			allowed[code] = true
		}
		var unexpected []string
		for code, n := range stat.statuses {
			if !allowed[code] {
				unexpected = append(unexpected, fmt.Sprintf("%d (%d times)", code, n))
			}
		}
		sort.Strings(unexpected)
		if len(unexpected) == 0 && stat.transportErrors == 0 {
			return AssertionResult{Name: a.Name, Verdict: VerdictPass,
				Detail: fmt.Sprintf("every one of %d responses from %s was in the allowed set", stat.sent, scope)}
		}
		if stat.transportErrors > 0 {
			unexpected = append(unexpected, fmt.Sprintf("%d requests got no response at all", stat.transportErrors))
		}
		return AssertionResult{Name: a.Name, Verdict: VerdictFail,
			Detail: fmt.Sprintf("%s answered outside the allowed set: %s", scope, joinComma(unexpected))}
	}

	// Validate refuses an assertion with no measure, so this is only reachable
	// from a hand built Scenario.
	return AssertionResult{Name: a.Name, Verdict: VerdictBlocked, Detail: "the assertion asks for nothing"}
}

// scenarioMeter collects what a scenario's requests did.
//
// Separate from meter because a scenario judges a status differently: the mix
// counts a 500 and ignores a 404, and a declared journey that gets a 404 is
// broken.
type scenarioMeter struct {
	mu     sync.Mutex
	byName map[string]*routeStat
	all    routeStat
	// last is when this scenario's final request came back, measured from the
	// run starting.
	last time.Duration
}

type routeStat struct {
	sent            int
	failed          int
	transportErrors int
	samples         []float64
	statuses        map[int]int
	byReason        map[string]int
}

func newScenarioMeter() *scenarioMeter {
	return &scenarioMeter{byName: map[string]*routeStat{}, all: newRouteStat()}
}

func newRouteStat() routeStat {
	return routeStat{statuses: map[int]int{}, byReason: map[string]int{}}
}

func (s *routeStat) add(status int, ms float64, err error) {
	s.sent++
	if ms > 0 {
		s.samples = append(s.samples, ms)
	}
	switch {
	case err != nil:
		s.failed++
		s.transportErrors++
		s.byReason[classify(err)]++
	case status >= 400:
		s.failed++
		s.statuses[status]++
		s.byReason[strconv.Itoa(status)]++
	default:
		s.statuses[status]++
	}
}

// reasons renders the failure reasons, worst first, for a person.
func (s routeStat) reasons() string {
	type pair struct {
		reason string
		n      int
	}
	pairs := make([]pair, 0, len(s.byReason))
	for reason, n := range s.byReason {
		pairs = append(pairs, pair{reason, n})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].n != pairs[j].n {
			return pairs[i].n > pairs[j].n
		}
		return pairs[i].reason < pairs[j].reason
	})
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, fmt.Sprintf("%s x%d", p.reason, p.n))
	}
	return joinComma(out)
}

func (m *scenarioMeter) record(route string, status int, ms float64, err error, at time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if at > m.last {
		m.last = at
	}
	stat := m.byName[route]
	if stat == nil {
		fresh := newRouteStat()
		stat = &fresh
		m.byName[route] = stat
	}
	stat.add(status, ms, err)
	m.all.add(status, ms, err)
}

func (m *scenarioMeter) overall() routeStat {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.all.clone()
}

func (m *scenarioMeter) forRoute(route string) (routeStat, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stat, ok := m.byName[route]
	if !ok {
		return routeStat{}, false
	}
	return stat.clone(), true
}

// clone copies a stat out from under the lock, so an assertion reading it
// cannot race a request still in flight.
func (s routeStat) clone() routeStat {
	out := s
	out.samples = append([]float64(nil), s.samples...)
	out.statuses = map[int]int{}
	for k, v := range s.statuses {
		out.statuses[k] = v
	}
	out.byReason = map[string]int{}
	for k, v := range s.byReason {
		out.byReason[k] = v
	}
	return out
}

// fill writes the measurements into the result.
func (m *scenarioMeter) fill(r *ScenarioResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.Sent = m.all.sent
	r.DurationMs = float64(m.last.Microseconds()) / 1000
	r.Overall = percentiles(m.all.samples)
	for name, stat := range m.byName {
		r.Steps = append(r.Steps, RouteResult{
			Route: name, Sent: stat.sent, Errors: stat.failed,
			Latency: percentiles(stat.samples),
		})
	}
	sort.Slice(r.Steps, func(i, j int) bool {
		// Slowest first, because that is the line somebody is looking for.
		if r.Steps[i].Latency.P95Ms != r.Steps[j].Latency.P95Ms {
			return r.Steps[i].Latency.P95Ms > r.Steps[j].Latency.P95Ms
		}
		return r.Steps[i].Route < r.Steps[j].Route
	})
}

// totals is the running count across every scenario, for progress lines.
func totals(meters []*scenarioMeter) (sent, failed int, p95 float64) {
	var all []float64
	for _, m := range meters {
		if m == nil {
			continue
		}
		stat := m.overall()
		sent += stat.sent
		failed += stat.failed
		all = append(all, stat.samples...)
	}
	return sent, failed, percentiles(all).P95Ms
}
