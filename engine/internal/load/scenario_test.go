package load_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/load"
)

// theJourney is the document the product pages describe, written in the format
// this engine can actually execute: HTTP requests rather than clicks, because
// clicking a button is the browser runner's job and a scenario does not
// pretend otherwise.
const theJourney = `
scenario: impatient_upgrade
description: A returning customer opens billing and resubmits when it feels slow.
ramp_ms: 0
steps:
  - request: GET /settings/billing
    think_ms: 5
  - request: GET /api/subscriptions
  - parallel:
      - request: GET /api/subscriptions
        after_ms: 3
      - request: GET /settings/billing
        after_ms: 5
assertions:
  - name: every_request_answered
    every_request_succeeded: true
  - name: billing_stayed_fast
    step: GET /settings/billing
    p95_below_ms: 2000
`

func mustParse(t *testing.T, doc string) *load.Scenario {
	t.Helper()
	s, err := load.ParseScenario([]byte(doc))
	require.NoError(t, err)
	return s
}

func TestParseScenario_ReadsTheJourneyAndItsAssertions(t *testing.T) {
	t.Parallel()
	s := mustParse(t, theJourney)
	require.Equal(t, "impatient_upgrade", s.Name)
	require.Len(t, s.Steps, 3)
	require.Len(t, s.Steps[2].Parallel, 2)
	require.Equal(t, 5, s.Steps[2].Parallel[1].AfterMs)
	require.Len(t, s.Assertions, 2)
	require.NotNil(t, s.RampMs)
	require.Zero(t, *s.RampMs, "asking for no ramp must not silently get the default")

	require.Equal(t, []load.Route{
		{Method: "GET", Path: "/api/subscriptions"},
		{Method: "GET", Path: "/settings/billing"},
	}, s.Routes(), "every route it would send, once each, for the safe list to judge")
}

func TestParseScenario_RefusesAMisspelledKey(t *testing.T) {
	t.Parallel()
	// A key that is silently ignored is a scenario that runs and does not do
	// what it says, and the person reading the result has no way to tell.
	_, err := load.ParseScenario([]byte("scenario: a\nsteps:\n  - request: GET /\n    thinkms: 30\n"))
	require.ErrorContains(t, err, "thinkms")
}

func TestParseScenario_RefusesTheThingsThatCannotMeanAnything(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ doc, want string }{
		"no name": {
			"steps:\n  - request: GET /\n", "has no name"},
		"no steps": {
			"scenario: a\nsteps: []\n", "has no steps"},
		"a step that is not a request": {
			"scenario: a\nsteps:\n  - request: /just/a/path\n", "is not a request"},
		"a path without a slash": {
			"scenario: a\nsteps:\n  - request: GET settings\n", "does not start with a slash"},
		"after_ms outside a parallel block": {
			"scenario: a\nsteps:\n  - request: GET /\n    after_ms: 50\n", "nothing for it to be after"},
		"a parallel block inside a parallel block": {
			"scenario: a\nsteps:\n  - parallel:\n      - parallel:\n          - request: GET /\n", "nests a parallel block"},
		"a step that is both": {
			"scenario: a\nsteps:\n  - request: GET /\n    parallel:\n      - request: GET /b\n", "must be one or the other"},
		"an unnamed assertion": {
			"scenario: a\nsteps:\n  - request: GET /\nassertions:\n  - p95_below_ms: 10\n", "unnamed assertion"},
		"an assertion that asks nothing": {
			"scenario: a\nsteps:\n  - request: GET /\nassertions:\n  - name: n\n", "sets 0 measures"},
		"an assertion that asks two things": {
			"scenario: a\nsteps:\n  - request: GET /\nassertions:\n  - name: n\n    p95_below_ms: 10\n    error_rate_below: 0.1\n", "sets 2 measures"},
		"status codes with no step": {
			"scenario: a\nsteps:\n  - request: GET /\nassertions:\n  - name: n\n    status_in: [200]\n", "does not say which step"},
		"the same assertion twice": {
			"scenario: a\nsteps:\n  - request: GET /\nassertions:\n  - name: n\n    p95_below_ms: 10\n  - name: n\n    p95_below_ms: 20\n", "twice"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := load.ParseScenario([]byte(tc.doc))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestPlanScenario_IsTheSameForASeedAndDifferentForAnother(t *testing.T) {
	t.Parallel()
	// Determinism is a property of the schedule, not of the run. A test that
	// asserted two runs sent the same requests by running them twice would be
	// asserting something about the machine's scheduler.
	s := mustParse(t, `
scenario: jittered
steps:
  - request: GET /a
    think_ms: 10
    jitter_ms: 40
  - request: GET /b
    think_ms: 5
    jitter_ms: 20
`)
	first := load.PlanScenario(s, 8, 3, 99, 0)
	second := load.PlanScenario(s, 8, 3, 99, 0)
	require.Equal(t, first, second, "the same seed schedules the same requests at the same moments")
	require.Len(t, first.Requests, 8*3*2)

	other := load.PlanScenario(s, 8, 3, 100, 0)
	require.NotEqual(t, first.Requests, other.Requests, "a different seed schedules differently")

	// Concurrency must not reach the draws. A single shared generator would
	// hand out draws in whatever order the goroutines got to it, so the same
	// seed would schedule differently on a busier machine.
	var wg sync.WaitGroup
	plans := make([]load.Plan, 16)
	for i := range plans {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			plans[i] = load.PlanScenario(s, 8, 3, 99, 0)
		}(i)
	}
	wg.Wait()
	for i := range plans {
		require.Equal(t, first, plans[i])
	}
}

func TestPlanScenario_ParallelBranchesOverlapAndStartAfterShiftsTheWhole(t *testing.T) {
	t.Parallel()
	// This is what an impatient user is: the same request again three hundred
	// milliseconds later, while the first is still in flight. Sequential
	// steps cannot express it.
	zero := 0
	s := &load.Scenario{
		Name:   "burst",
		RampMs: &zero,
		Steps: []load.Step{
			{Request: "GET /open"},
			{Parallel: []load.Step{
				{Request: "POST /submit", AfterMs: 0},
				{Request: "POST /submit", AfterMs: 300},
				{Request: "GET /open", AfterMs: 450},
			}},
		},
	}
	require.NoError(t, s.Validate())

	plan := load.PlanScenario(s, 1, 1, 1, 2*time.Second)
	require.Len(t, plan.Requests, 4)
	require.Equal(t, 2*time.Second, plan.Requests[0].Offset,
		"start_after shifts the whole journey, so one scenario can burst while another runs")
	require.Equal(t, 2*time.Second, plan.Requests[1].Offset)
	require.Equal(t, 2*time.Second+300*time.Millisecond, plan.Requests[2].Offset)
	require.Equal(t, 2*time.Second+450*time.Millisecond, plan.Requests[3].Offset)
	require.Equal(t, 2*time.Second+450*time.Millisecond, plan.Span)
}

func TestPlanScenario_SessionsAndIterationsMultiply(t *testing.T) {
	t.Parallel()
	zero := 0
	s := &load.Scenario{Name: "n", RampMs: &zero, Steps: []load.Step{{Request: "GET /"}}}
	plan := load.PlanScenario(s, 5, 4, 1, 0)
	require.Len(t, plan.Requests, 20,
		"the work a scenario does is declared rather than decided by how long the clock ran for")
}

// journeyServer records what arrived, in order.
type journeyServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
	// status maps a path onto what to answer with.
	status map[string]int
	delay  map[string]time.Duration
}

func newJourneyServer(t *testing.T) *journeyServer {
	t.Helper()
	js := &journeyServer{status: map[string]int{}, delay: map[string]time.Duration{}}
	js.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		js.mu.Lock()
		js.seen = append(js.seen, r.Method+" "+r.URL.Path)
		code, wait := js.status[r.URL.Path], js.delay[r.URL.Path]
		js.mu.Unlock()
		if wait > 0 {
			time.Sleep(wait)
		}
		if code == 0 {
			code = http.StatusOK
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(js.Close)
	return js
}

func (j *journeyServer) requests() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.seen...)
}

func TestRunScenarios_SendsTheJourneyAndHoldsItsAssertions(t *testing.T) {
	t.Parallel()
	server := newJourneyServer(t)

	results, err := load.RunScenarios(context.Background(), load.ScenarioOptions{
		BaseURL: server.URL,
		Runs: []load.ScenarioRun{{
			Scenario: mustParse(t, theJourney), Sessions: 3, Iterations: 2,
		}},
		SafeRoutes: []string{"GET /**"},
		Seed:       7, Clock: clock.New(),
	})
	require.NoError(t, err)
	require.Len(t, results, 1)

	r := results[0]
	require.Equal(t, "pass", r.Verdict, r.Detail)
	require.Equal(t, 3*2*4, r.Sent, "three sessions, two passes, four requests each")
	require.Len(t, server.requests(), r.Sent)

	byStep := map[string]load.RouteResult{}
	for _, s := range r.Steps {
		byStep[s.Route] = s
	}
	require.Equal(t, 12, byStep["GET /settings/billing"].Sent)
	require.Equal(t, 12, byStep["GET /api/subscriptions"].Sent)

	require.Len(t, r.Assertions, 2)
	for _, a := range r.Assertions {
		require.Equal(t, "pass", a.Verdict, a.Name+": "+a.Detail)
	}
}

func TestRunScenarios_ASeedSendsTheSameRequestsInTheSameOrder(t *testing.T) {
	t.Parallel()
	// The claim the rest of this package makes, made for journeys too: two
	// runs of one scenario compare the application rather than two different
	// schedules.
	doc := `
scenario: repeatable
ramp_ms: 40
steps:
  - request: GET /one
    think_ms: 1
    jitter_ms: 12
  - request: GET /two
    think_ms: 1
    jitter_ms: 12
`
	run := func(seed int64) []string {
		server := newJourneyServer(t)
		_, err := load.RunScenarios(context.Background(), load.ScenarioOptions{
			BaseURL: server.URL,
			Runs: []load.ScenarioRun{{
				Scenario: mustParse(t, doc), Sessions: 4, Iterations: 2,
			}},
			SafeRoutes: []string{"GET /**"},
			Seed:       seed, Concurrency: 1, Clock: clock.New(),
		})
		require.NoError(t, err)
		return server.requests()
	}

	first, second := run(31), run(31)
	require.Equal(t, first, second, "the same seed sends the same sequence")
	require.Len(t, first, 16)
}

func TestRunScenarios_AStepNobodyNamedSafeIsNeverSent(t *testing.T) {
	t.Parallel()
	// The rule the whole package rests on. A scenario is not an exemption
	// from it: a journey that says POST /billing/upgrade is a journey that
	// charges a card, and it does not run until somebody says it may.
	server := newJourneyServer(t)
	s := mustParse(t, `
scenario: upgrade
steps:
  - request: GET /settings/billing
  - request: POST /billing/upgrade
assertions:
  - name: it_worked
    every_request_succeeded: true
`)

	results, err := load.RunScenarios(context.Background(), load.ScenarioOptions{
		BaseURL: server.URL,
		Runs:    []load.ScenarioRun{{Scenario: s, Sessions: 4, Iterations: 4}},
		// GET is allowed, so half the journey would have run if the check
		// were per request rather than per scenario.
		SafeRoutes: []string{"GET /**"},
		Seed:       1, Clock: clock.New(),
	})
	require.NoError(t, err)

	r := results[0]
	require.Equal(t, "blocked", r.Verdict)
	require.Equal(t, []string{"POST /billing/upgrade"}, r.Refused)
	require.Empty(t, server.requests(),
		"not one request goes out, including the safe half of the journey")
	require.Zero(t, r.Sent)

	require.Len(t, r.Assertions, 1)
	require.Equal(t, "blocked", r.Assertions[0].Verdict,
		"blocked rather than failed: it found nothing wrong with the application")
}

func TestRunScenarios_AnUnsafePatternBlocksAScenarioTheSafeListWouldOtherwiseAllow(t *testing.T) {
	t.Parallel()
	server := newJourneyServer(t)
	s := mustParse(t, "scenario: exporting\nsteps:\n  - request: GET /api/export\n")

	results, err := load.RunScenarios(context.Background(), load.ScenarioOptions{
		BaseURL:    server.URL,
		Runs:       []load.ScenarioRun{{Scenario: s, Sessions: 1, Iterations: 1}},
		SafeRoutes: []string{"GET /api/**"}, UnsafeRoutes: []string{"GET /api/export"},
		Seed: 1, Clock: clock.New(),
	})
	require.NoError(t, err)
	require.Equal(t, "blocked", results[0].Verdict)
	require.Empty(t, server.requests())
}

func TestRunScenarios_AFourHundredBreaksAJourneyEvenThoughTheMixIgnoresIt(t *testing.T) {
	t.Parallel()
	// A 404 inside production's mix is production's own traffic and counting
	// it would report the shape's contents as a failure. A 404 inside a
	// declared journey means the journey is broken.
	server := newJourneyServer(t)
	server.status["/api/subscriptions"] = http.StatusNotFound

	results, err := load.RunScenarios(context.Background(), load.ScenarioOptions{
		BaseURL: server.URL,
		Runs: []load.ScenarioRun{{
			Scenario: mustParse(t, `
scenario: broken
ramp_ms: 0
steps:
  - request: GET /settings/billing
  - request: GET /api/subscriptions
assertions:
  - name: every_request_answered
    every_request_succeeded: true
  - name: subscriptions_answered_two_hundred
    step: GET /api/subscriptions
    status_in: [200]
  - name: billing_answered
    step: GET /settings/billing
    every_request_succeeded: true
`),
			Sessions: 2, Iterations: 1,
		}},
		SafeRoutes: []string{"GET /**"}, Seed: 1, Clock: clock.New(),
	})
	require.NoError(t, err)

	r := results[0]
	require.Equal(t, "fail", r.Verdict)
	byName := map[string]load.AssertionResult{}
	for _, a := range r.Assertions {
		byName[a.Name] = a
	}
	require.Equal(t, "fail", byName["every_request_answered"].Verdict)
	require.Contains(t, byName["every_request_answered"].Detail, "404")
	require.Equal(t, "fail", byName["subscriptions_answered_two_hundred"].Verdict)
	require.Contains(t, byName["subscriptions_answered_two_hundred"].Detail, "404")
	require.Equal(t, "pass", byName["billing_answered"].Verdict,
		"the step that was fine is still fine, so the report says which one broke")
}

func TestRunScenarios_ASlowStepFailsItsLatencyAssertion(t *testing.T) {
	t.Parallel()
	server := newJourneyServer(t)
	server.delay["/slow"] = 60 * time.Millisecond

	results, err := load.RunScenarios(context.Background(), load.ScenarioOptions{
		BaseURL: server.URL,
		Runs: []load.ScenarioRun{{
			Scenario: mustParse(t, `
scenario: latency
ramp_ms: 0
steps:
  - request: GET /slow
  - request: GET /fast
assertions:
  - name: slow_step_under_ten_ms
    step: GET /slow
    p95_below_ms: 10
  - name: fast_step_under_a_second
    step: GET /fast
    p95_below_ms: 1000
`),
			Sessions: 2, Iterations: 1,
		}},
		SafeRoutes: []string{"GET /**"}, Seed: 1, Clock: clock.New(),
	})
	require.NoError(t, err)

	byName := map[string]load.AssertionResult{}
	for _, a := range results[0].Assertions {
		byName[a.Name] = a
	}
	require.Equal(t, "fail", byName["slow_step_under_ten_ms"].Verdict)
	require.Contains(t, byName["slow_step_under_ten_ms"].Detail, "over 10ms")
	require.Equal(t, "pass", byName["fast_step_under_a_second"].Verdict)
	require.Equal(t, "fail", results[0].Verdict)
}

func TestRunScenarios_AnAssertionAboutAStepNothingSentIsUnverified(t *testing.T) {
	t.Parallel()
	// Reporting it as held is how a typo in a step name becomes a green check
	// forever.
	server := newJourneyServer(t)
	results, err := load.RunScenarios(context.Background(), load.ScenarioOptions{
		BaseURL: server.URL,
		Runs: []load.ScenarioRun{{
			Scenario: mustParse(t, `
scenario: typo
ramp_ms: 0
steps:
  - request: GET /settings/billing
assertions:
  - name: about_a_step_that_does_not_exist
    step: GET /setting/billing
    p95_below_ms: 1000
`),
			Sessions: 1, Iterations: 1,
		}},
		SafeRoutes: []string{"GET /**"}, Seed: 1, Clock: clock.New(),
	})
	require.NoError(t, err)
	require.Equal(t, "unverified", results[0].Verdict)
	require.Equal(t, "unverified", results[0].Assertions[0].Verdict)
	require.Contains(t, results[0].Assertions[0].Detail, "nothing to measure")
}

func TestRunScenarios_AScenarioThatAssertsNothingIsUnverifiedRatherThanPassing(t *testing.T) {
	t.Parallel()
	// It ran and proved nothing. Calling that a pass is how a check that
	// measures nothing reports green forever.
	server := newJourneyServer(t)
	results, err := load.RunScenarios(context.Background(), load.ScenarioOptions{
		BaseURL:    server.URL,
		Runs:       []load.ScenarioRun{{Scenario: mustParse(t, "scenario: quiet\nramp_ms: 0\nsteps:\n  - request: GET /\n"), Sessions: 1, Iterations: 1}},
		SafeRoutes: []string{"GET /**"}, Seed: 1, Clock: clock.New(),
	})
	require.NoError(t, err)
	require.Equal(t, "unverified", results[0].Verdict)
	require.Contains(t, results[0].Detail, "asserts nothing")
	require.Len(t, server.requests(), 1, "it did send the traffic")
}

func TestRunScenarios_OneScenarioBurstsWhileAnotherIsAlreadyRunning(t *testing.T) {
	t.Parallel()
	// The load that actually breaks things, and the reason start_after
	// exists. With every scenario starting at zero it cannot be expressed,
	// and running them one after another would make every scenario a solo.
	server := newJourneyServer(t)
	background := mustParse(t, `
scenario: background
ramp_ms: 0
steps:
  - request: GET /background
    think_ms: 20
`)
	burst := mustParse(t, "scenario: burst\nramp_ms: 0\nsteps:\n  - request: GET /burst\n")

	results, err := load.RunScenarios(context.Background(), load.ScenarioOptions{
		BaseURL: server.URL,
		Runs: []load.ScenarioRun{
			{Scenario: background, Sessions: 2, Iterations: 6},
			{Scenario: burst, Sessions: 3, Iterations: 1, StartAfter: 40 * time.Millisecond},
		},
		SafeRoutes: []string{"GET /**"}, Seed: 3, Clock: clock.New(),
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, 12, results[0].Sent)
	require.Equal(t, 3, results[1].Sent)

	seen := server.requests()
	require.Equal(t, "GET /background", seen[0], "the delayed scenario does not go first")
	var firstBurst, backgroundAfterBurst int
	for i, r := range seen {
		if r == "GET /burst" && firstBurst == 0 {
			firstBurst = i
		}
		if firstBurst > 0 && r == "GET /background" {
			backgroundAfterBurst++
		}
	}
	require.Positive(t, firstBurst)
	require.Positive(t, backgroundAfterBurst,
		"the background journey is still running when the burst lands, which is the whole point")
}

func TestRunScenarios_StopsWhenTheContextIsCancelled(t *testing.T) {
	t.Parallel()
	// A run that ignored cancellation would keep generating load against an
	// environment somebody is trying to tear down.
	server := newJourneyServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	results, err := load.RunScenarios(ctx, load.ScenarioOptions{
		BaseURL: server.URL,
		Runs: []load.ScenarioRun{{
			Scenario: mustParse(t, "scenario: long\nramp_ms: 0\nsteps:\n  - request: GET /\n    think_ms: 200\n"),
			Sessions: 1, Iterations: 100,
		}},
		SafeRoutes: []string{"GET /**"}, Seed: 1, Clock: clock.New(),
	})
	require.Error(t, err)
	require.NotNil(t, results, "what it measured before stopping still comes back")
	require.Less(t, time.Since(started), 5*time.Second)
	require.Less(t, results[0].Sent, 100)
}

func TestRunScenarios_RefusesARunWithNoScenariosInIt(t *testing.T) {
	t.Parallel()
	_, err := load.RunScenarios(context.Background(), load.ScenarioOptions{BaseURL: "http://x"})
	require.ErrorContains(t, err, "no scenarios")
}

func TestRunScenarios_ABlockedScenarioDoesNotStopTheOnesThatCanRun(t *testing.T) {
	t.Parallel()
	// The block is per scenario, not per run. A journey that names an
	// undeclared route is a mistake in one document, and refusing to run the
	// other three because of it would mean one typo silently stops every
	// measurement in the pull request.
	server := newJourneyServer(t)
	fine := mustParse(t, "scenario: fine\nramp_ms: 0\nsteps:\n  - request: GET /fine\n"+
		"assertions:\n  - name: it_answered\n    every_request_succeeded: true\n")
	unsafe := mustParse(t, "scenario: unsafe\nramp_ms: 0\nsteps:\n  - request: POST /pay\n")

	results, err := load.RunScenarios(context.Background(), load.ScenarioOptions{
		BaseURL: server.URL,
		Runs: []load.ScenarioRun{
			{Scenario: unsafe, Sessions: 2, Iterations: 2},
			{Scenario: fine, Sessions: 2, Iterations: 2},
		},
		SafeRoutes: []string{"GET /**"}, Seed: 1, Clock: clock.New(),
	})
	require.NoError(t, err)
	require.Equal(t, "blocked", results[0].Verdict)
	require.Zero(t, results[0].Sent)
	require.Equal(t, "pass", results[1].Verdict, results[1].Detail)
	require.Equal(t, 4, results[1].Sent)

	for _, r := range server.requests() {
		require.Equal(t, "GET /fine", r, "nothing from the blocked scenario reached the server")
	}
}

func TestRunScenarios_ASlowApplicationDelaysTheScheduleRatherThanLosingRequests(t *testing.T) {
	t.Parallel()
	// A request whose moment has passed goes out late rather than being
	// dropped. Dropping it would make a slow application look like a fast one
	// that simply received fewer requests, which is the reading a load test
	// must never allow.
	server := newJourneyServer(t)
	server.delay["/slow"] = 25 * time.Millisecond

	results, err := load.RunScenarios(context.Background(), load.ScenarioOptions{
		BaseURL: server.URL,
		Runs: []load.ScenarioRun{{
			Scenario: mustParse(t, "scenario: behind\nramp_ms: 0\nsteps:\n  - request: GET /slow\n"),
			Sessions: 6, Iterations: 3,
		}},
		SafeRoutes: []string{"GET /**"}, Seed: 1, Concurrency: 2, Clock: clock.New(),
	})
	require.NoError(t, err)

	r := results[0]
	require.Equal(t, 18, r.Sent, "every planned request was sent, however late")
	require.Len(t, server.requests(), 18)
	require.Greater(t, r.DurationMs, r.ScheduledMs,
		"the run took longer than the schedule asked for, which is the finding")
}

func TestRunScenarios_AnAssertionSaysWhatItMeasuredAndNotOnlyWhatItConcluded(t *testing.T) {
	t.Parallel()
	// The sentence is for a person. A console has to chart the same answer and
	// store it in columns, and it cannot do either from "served a p95 of 240ms,
	// over 200ms" without parsing English, nor tell that measure from another.
	server := newJourneyServer(t)
	server.delay["/slow"] = 60 * time.Millisecond

	results, err := load.RunScenarios(context.Background(), load.ScenarioOptions{
		BaseURL: server.URL,
		Runs: []load.ScenarioRun{{
			Scenario: mustParse(t, `
scenario: measures
ramp_ms: 0
steps:
  - request: GET /slow
  - request: GET /fast
assertions:
  - name: slow_step_under_ten_ms
    step: GET /slow
    p95_below_ms: 10
  - name: nothing_failed
    every_request_succeeded: true
  - name: errors_under_one_percent
    error_rate_below: 0.01
  - name: about_a_step_nothing_sent
    step: GET /missing
    p95_below_ms: 5
`),
			Sessions: 1, Iterations: 1,
		}},
		SafeRoutes: []string{"GET /**"}, Seed: 1, Clock: clock.New(),
	})
	require.NoError(t, err)

	byName := map[string]load.AssertionResult{}
	for _, a := range results[0].Assertions {
		byName[a.Name] = a
	}

	slow := byName["slow_step_under_ten_ms"]
	require.Equal(t, "p95_below_ms", slow.Measure)
	require.Equal(t, "GET /slow", slow.Scope, "spelled the way a route is spelled everywhere else")
	require.NotNil(t, slow.Threshold)
	require.InDelta(t, 10, *slow.Threshold, 1e-9)
	require.NotNil(t, slow.Observed)
	require.Greater(t, *slow.Observed, 10.0, "the observed value is the p95 the verdict was decided on")

	// A measure that is not a numeric comparison carries no numbers. Inventing
	// a threshold of one for "every request succeeded" would put a number on a
	// chart that means nothing.
	every := byName["nothing_failed"]
	require.Equal(t, "every_request_succeeded", every.Measure)
	require.Empty(t, every.Scope, "a scenario wide assertion has no scope")
	require.Nil(t, every.Threshold)
	require.Nil(t, every.Observed)

	rate := byName["errors_under_one_percent"]
	require.Equal(t, "error_rate_below", rate.Measure)
	require.NotNil(t, rate.Threshold)
	require.NotNil(t, rate.Observed)
	require.InDelta(t, 0, *rate.Observed, 1e-9)

	// Nothing was sent to it, so there is no observation. Zero would read as a
	// perfect application rather than as a question nobody asked.
	missing := byName["about_a_step_nothing_sent"]
	require.Equal(t, "unverified", missing.Verdict)
	require.Equal(t, "p95_below_ms", missing.Measure)
	require.NotNil(t, missing.Threshold)
	require.Nil(t, missing.Observed,
		"an unmeasured p95 is absent, not zero")
}
