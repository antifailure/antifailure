package workload_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/workload"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The orderings, and why every one of them is here.
//
// Cancellation and a deadline are not one case with two names. A cancel can
// arrive before the work starts, while it runs, or after it finished, and the
// third is the one that quietly corrupts a report: checking the context after
// the work returns turns a completed run into a cancelled one, and the number
// that was measured is thrown away in favour of a fact about the network.
//
// The three that look like control plane problems are here because the engine
// half of each is a property of the document rather than of a database. A
// result that arrives before its row exists has to be complete on its own. A
// result delivered twice has to be the same bytes both times, or the second
// delivery is a different answer. And a result from a superseded run has to
// name the run it belongs to, or a late arrival overwrites a newer one.

var epoch = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// fakeRunner is an orchestrator that answers when the test says to.
type fakeRunner struct {
	envID  string
	status *env.Result

	// block, when set, is closed by the test to release whichever path is
	// running. Until then the path waits on it or on the context.
	block chan struct{}
	// entered is closed the first time a path is reached, so a test that
	// means "cancel while the work is running" can wait for the work to
	// actually be running. Without it, cancelling straight after starting the
	// goroutine races the pre-flight check and silently tests the previous
	// case instead.
	entered chan struct{}
	// ignoreCancel makes the path finish its work and return a complete
	// result even though the context is already cancelled. That is not a
	// contrivance: the real paths check the context at loop boundaries, so a
	// cancel that lands after the last iteration produces exactly this, a
	// finished run whose context is done.
	ignoreCancel bool

	loadResult    *load.Result
	loadRefused   []load.Route
	loadErr       error
	scenarios     []load.ScenarioResult
	scenariosErr  error
	testReport    *env.TestReport
	testErr       error
	exploreReport *explore.Report
	exploreErr    error

	p95Increase, errorRate float64

	teardown     *env.Teardown
	teardownErr  error
	downCalls    int
	downSawAlive bool
}

func (f *fakeRunner) EnvID() string { return f.envID }

func (f *fakeRunner) Status(ctx context.Context) (*env.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.status != nil {
		return f.status, nil
	}
	return &env.Result{EnvID: f.envID, URL: "http://127.0.0.1:46001"}, nil
}

// wait blocks until the test releases the path or the context ends, which is
// how a cancellation during the work is expressed without a sleep.
func (f *fakeRunner) wait(ctx context.Context) error {
	if f.entered != nil {
		close(f.entered)
		f.entered = nil
	}
	if f.block == nil {
		return ctx.Err()
	}
	if f.ignoreCancel {
		<-f.block
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.block:
		return nil
	}
}

func (f *fakeRunner) Load(ctx context.Context, _ env.LoadOptions) (*load.Result, []load.Route, error) {
	if err := f.wait(ctx); err != nil {
		// The real load runner returns what it measured before it stopped,
		// alongside the context error, and this fake has to do the same or the
		// partial result path is never exercised.
		return f.loadResult, f.loadRefused, err
	}
	return f.loadResult, f.loadRefused, f.loadErr
}

func (f *fakeRunner) Scenarios(ctx context.Context, _ env.ScenarioOptions) ([]load.ScenarioResult, error) {
	if err := f.wait(ctx); err != nil {
		return f.scenarios, err
	}
	return f.scenarios, f.scenariosErr
}

func (f *fakeRunner) Test(ctx context.Context, _ env.TestOptions) (*env.TestReport, error) {
	if err := f.wait(ctx); err != nil {
		return f.testReport, err
	}
	return f.testReport, f.testErr
}

func (f *fakeRunner) Explore(ctx context.Context, _ env.ExploreOptions) (*explore.Report, error) {
	if err := f.wait(ctx); err != nil {
		return f.exploreReport, err
	}
	return f.exploreReport, f.exploreErr
}

func (f *fakeRunner) Thresholds() (float64, float64) { return f.p95Increase, f.errorRate }

func (f *fakeRunner) Down(ctx context.Context) (*env.Teardown, error) {
	f.downCalls++
	// Recorded rather than asserted here: the point of the detached context is
	// that teardown runs even when the caller's context is already cancelled.
	f.downSawAlive = ctx.Err() == nil
	return f.teardown, f.teardownErr
}

func mixRunner() *fakeRunner {
	return &fakeRunner{
		envID:       "demo-main-abc123",
		p95Increase: 0.25,
		errorRate:   0.01,
		loadResult: &load.Result{
			Source: "otel", TargetRate: 10, Sent: 100, Rate: 9.8,
			Duration: 10 * time.Second, ErrorRate: 0.02,
			Errors:  map[string]int{"500": 1, "timeout": 1},
			Overall: load.Latency{P50Ms: 10, P90Ms: 20, P95Ms: 25, P99Ms: 40, MaxMs: 90},
			Routes: []load.RouteResult{
				{Route: "GET /orders", Sent: 60, Errors: 2,
					Latency:       load.Latency{P50Ms: 12, P95Ms: 30},
					BaselineP95Ms: 20, P95Increase: 0.5, HasBaseline: true},
				{Route: "GET /health", Sent: 40,
					Latency: load.Latency{P50Ms: 2, P95Ms: 4}},
			},
		},
		loadRefused: []load.Route{{Method: "POST", Path: "/orders"}},
	}
}

func execute(t *testing.T, ctx context.Context, r workload.Runner, req workload.Request, tune func(*workload.Options)) *workload.Result {
	t.Helper()
	plan, err := workload.Parse(req)
	require.NoError(t, err)
	opts := workload.Options{
		Plan: plan, Runner: r, Clock: clock.NewFake(epoch),
		Engine: workload.Engine{Version: "1.0.0", Commit: "abc1234", Edition: "community"},
		Branch: "main",
	}
	if tune != nil {
		tune(&opts)
	}
	res, err := workload.Execute(ctx, opts)
	require.NoError(t, err)
	require.NotNil(t, res)
	return res
}

// ---------------------------------------------------------------------------
// The ordering table
// ---------------------------------------------------------------------------

func TestCancelBeforeTheWorkStarts(t *testing.T) {
	r := mixRunner()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := execute(t, ctx, r, workload.Request{Kind: "observed_load"}, func(o *workload.Options) {
		o.Teardown = true
	})

	require.Equal(t, workload.StateCancelled, res.State)
	require.Equal(t, workload.VerdictBlocked, res.Verdict)
	require.Contains(t, res.Detail, "cancelled")
	require.Nil(t, res.Measured.Requests, "nothing ran, so nothing may be reported as measured")
	require.Equal(t, 1, r.downCalls, "a cancelled run still has to clean up")
	require.True(t, r.downSawAlive,
		"teardown must run on a context the cancellation cannot reach, or the containers "+
			"outlive the run that started them")
}

func TestCancelWhileTheWorkIsRunning(t *testing.T) {
	r := mixRunner()
	r.block = make(chan struct{})
	r.entered = make(chan struct{})
	entered := r.entered
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan *workload.Result, 1)
	go func() {
		done <- execute(t, ctx, r, workload.Request{Kind: "observed_load"}, func(o *workload.Options) {
			o.Teardown = true
		})
	}()
	<-entered
	cancel()
	res := <-done

	require.Equal(t, workload.StateCancelled, res.State)
	require.Equal(t, workload.VerdictBlocked, res.Verdict)
	// A stopped run still carries what it measured before it stopped. Throwing
	// that away is throwing away the only evidence of why somebody stopped it.
	require.NotNil(t, res.Measured.Requests)
	require.Equal(t, 100, *res.Measured.Requests)
	require.Equal(t, 1, r.downCalls)
	require.True(t, r.downSawAlive)
}

func TestCancelAfterTheWorkFinishedDoesNotRewriteTheResult(t *testing.T) {
	// The ordering that quietly corrupts a report, and it is a real one rather
	// than a contrivance. The engine paths check the context at loop
	// boundaries, so a cancel arriving after the last iteration leaves a
	// finished run holding a cancelled context. Deciding the outcome from
	// ctx.Err() rather than from what the path returned throws away the
	// measurement in favour of a fact about the network.
	r := mixRunner()
	r.block = make(chan struct{})
	r.entered = make(chan struct{})
	r.ignoreCancel = true
	entered := r.entered
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan *workload.Result, 1)
	go func() {
		done <- execute(t, ctx, r, workload.Request{Kind: "observed_load"}, func(o *workload.Options) {
			o.Teardown = true
		})
	}()
	<-entered
	cancel()
	close(r.block)
	res := <-done

	require.Equal(t, workload.StateSucceeded, res.State,
		"a run that produced a complete result is finished, whatever the context says afterwards")
	require.Equal(t, workload.VerdictFail, res.Verdict, "the error rate breached its threshold")
	require.Equal(t, 100, *res.Measured.Requests)
}

func TestATimeoutWithNoTerminalEventIsReportedAsATimeout(t *testing.T) {
	r := mixRunner()
	// Never released. The only thing that can end this run is the deadline.
	r.block = make(chan struct{})

	res := execute(t, context.Background(), r, workload.Request{Kind: "browser_workflow"},
		func(o *workload.Options) {
			o.Timeout = 40 * time.Millisecond
			o.Teardown = true
		})

	require.Equal(t, workload.StateTimedOut, res.State)
	require.Equal(t, workload.VerdictBlocked, res.Verdict)
	require.Contains(t, res.Detail, "deadline")
	require.Equal(t, 1, r.downCalls, "a run that ran out of time still has to clean up")
	require.True(t, r.downSawAlive)
}

func TestATimeoutIsNotReportedAsACancellation(t *testing.T) {
	// Two different facts. A person pressed stop, or the work took too long.
	// Collapsing them means a console cannot tell a deadline that is too short
	// from a user who changed their mind.
	slow := mixRunner()
	slow.block = make(chan struct{})
	timedOut := execute(t, context.Background(), slow, workload.Request{Kind: "observed_load"},
		func(o *workload.Options) { o.Timeout = 40 * time.Millisecond })

	stopped := mixRunner()
	stopped.block = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *workload.Result, 1)
	go func() {
		done <- execute(t, ctx, stopped, workload.Request{Kind: "observed_load"},
			func(o *workload.Options) { o.Timeout = time.Minute })
	}()
	cancel()
	cancelled := <-done

	require.Equal(t, workload.StateTimedOut, timedOut.State)
	require.Equal(t, workload.StateCancelled, cancelled.State)
}

func TestTheSameExecutionProducesTheSameDocumentTwice(t *testing.T) {
	// What makes a duplicate delivery safe. A control plane that receives the
	// same result twice has to be able to see that it is the same result, and
	// that is a property of the bytes rather than of a deduplication table.
	first := execute(t, context.Background(), mixRunner(), workload.Request{
		RunID: "run_1", Kind: "observed_load",
	}, nil)
	second := execute(t, context.Background(), mixRunner(), workload.Request{
		RunID: "run_1", Kind: "observed_load",
	}, nil)

	a, err := json.Marshal(first)
	require.NoError(t, err)
	b, err := json.Marshal(second)
	require.NoError(t, err)
	require.JSONEq(t, string(a), string(b))
}

func TestAResultNamesTheRunItBelongsTo(t *testing.T) {
	// What stops a superseded run overwriting the one that replaced it, and
	// what lets a result that arrives before its row exists be matched later.
	// The engine never learns what a run row is; it echoes the identifier it
	// was given and the document is complete without any prior handshake.
	res := execute(t, context.Background(), mixRunner(), workload.Request{
		RunID: "run_superseded", Kind: "observed_load",
	}, nil)
	require.Equal(t, "run_superseded", res.RunID)
	require.Equal(t, workload.ResultSchema, res.Schema)
	require.NotEmpty(t, res.Reproduce.Command)
	require.NotEmpty(t, res.Engine.Version)
	require.Equal(t, "demo-main-abc123", res.Environment.EnvID)
	require.Equal(t, "main", res.Environment.Branch)
}

// ---------------------------------------------------------------------------
// Malformed input from the control plane
// ---------------------------------------------------------------------------

func TestMalformedInputRefusesRatherThanRunningOrCrashing(t *testing.T) {
	cases := []workload.Request{
		{Kind: ""},
		{Kind: "  "},
		{Kind: "observed_load\x00"},
		{Kind: "observed_load", Duration: "\x00"},
		{Kind: "observed_load", Scale: "NaN"},
		{Kind: "observed_load", Scale: "Inf"},
		{Kind: "http_scenario", Select: ",,,"},
		{Kind: "http_scenario", Select: "a", Seed: "-1"},
		{Kind: "http_scenario", Select: "a", Concurrency: "0"},
		{Kind: "exploration", Select: "   "},
	}
	for _, req := range cases {
		req := req
		t.Run(req.Kind+"/"+req.Select+req.Duration+req.Scale+req.Seed+req.Concurrency, func(t *testing.T) {
			plan, err := workload.Parse(req)
			require.Error(t, err, "%#v should be refused", req)
			// A refused request either produces no plan at all or a plan
			// carrying its refusals. Neither ever reaches a runner.
			if plan != nil {
				require.NotEmpty(t, plan.Refusals)
			}
		})
	}
}

func TestARefusedRequestNeverReachesTheRunnerAndStillCleansUp(t *testing.T) {
	r := mixRunner()
	plan, err := workload.Parse(workload.Request{Kind: "observed_load", Concurrency: "40"})
	require.Error(t, err)
	require.NotNil(t, plan)

	res, err := workload.Execute(context.Background(), workload.Options{
		Plan: plan, Runner: r, Clock: clock.NewFake(epoch), Teardown: true,
	})
	require.NoError(t, err)
	require.Equal(t, workload.StateFailed, res.State)
	require.Equal(t, workload.VerdictBlocked, res.Verdict)
	require.Equal(t, "AF-WLD-002", res.FailureCode)
	require.Len(t, res.Refusals, 1)
	require.Nil(t, res.Measured.Requests, "nothing ran")
	require.Equal(t, 1, r.downCalls,
		"an environment brought up for a run that was refused still has to come down")
}

func TestAnEngineErrorBecomesAFailedResultRatherThanNoResult(t *testing.T) {
	r := mixRunner()
	r.loadResult = nil
	r.loadErr = aferrors.Coded(aferrors.AFLOD010, "detail", "nothing is running for this branch")

	res := execute(t, context.Background(), r, workload.Request{Kind: "observed_load"}, nil)
	require.Equal(t, workload.StateFailed, res.State)
	require.Equal(t, workload.VerdictBlocked, res.Verdict)
	require.Equal(t, "AF-LOD-010", res.FailureCode)
	require.Contains(t, res.Detail, "nothing is running")
}

func TestNoResultAndNoErrorIsReportedAsTheBugItIs(t *testing.T) {
	r := mixRunner()
	r.loadResult = nil
	r.loadErr = nil

	res := execute(t, context.Background(), r, workload.Request{Kind: "observed_load"}, nil)
	require.Equal(t, workload.StateFailed, res.State)
	require.Contains(t, res.Detail, "bug rather than an outcome")
}

func TestTeardownThatLeavesResourcesBehindSaysSo(t *testing.T) {
	r := mixRunner()
	r.teardown = &env.Teardown{EnvID: "demo-main-abc123", Removed: 5}
	r.teardown.Pending = append(r.teardown.Pending, pendingResource())

	res := execute(t, context.Background(), r, workload.Request{Kind: "observed_load"},
		func(o *workload.Options) { o.Teardown = true })
	require.Contains(t, res.Detail, "resources still pending")
}

func TestTeardownFailureIsReportedRatherThanSwallowed(t *testing.T) {
	r := mixRunner()
	r.teardownErr = errors.New("the docker daemon is not running")

	res := execute(t, context.Background(), r, workload.Request{Kind: "observed_load"},
		func(o *workload.Options) { o.Teardown = true })
	require.Contains(t, res.Detail, "could not be torn down")
	require.Contains(t, res.Detail, "docker daemon")
}

func TestNothingIsTornDownWhenTheCallerDidNotAskFor(t *testing.T) {
	// A hosted run against a long lived environment must not take it down.
	r := mixRunner()
	_ = execute(t, context.Background(), r, workload.Request{Kind: "observed_load"}, nil)
	require.Equal(t, 0, r.downCalls)
}

// ---------------------------------------------------------------------------
// Projection
// ---------------------------------------------------------------------------

func TestAMixProjectsIntoTheColumnsAMixHas(t *testing.T) {
	res := execute(t, context.Background(), mixRunner(), workload.Request{Kind: "observed_load"}, nil)

	require.Equal(t, workload.ObservedLoad, res.Kind)
	require.Equal(t, 100, *res.Measured.Requests)
	require.Equal(t, 2, *res.Measured.Failures)
	require.InDelta(t, 0.02, *res.Measured.ErrorRate, 1e-9)
	require.InDelta(t, 10, *res.Measured.TargetRate, 1e-9)
	require.InDelta(t, 9.8, *res.Measured.AchievedRate, 1e-9)
	require.InDelta(t, 25, *res.Measured.P95Ms, 1e-9)
	require.Equal(t, "otel", res.Measured.Source)
	require.Equal(t, []string{"POST /orders"}, res.Measured.RefusedRoutes)
	require.Equal(t, map[string]int{"500": 1, "timeout": 1}, res.Measured.Errors)

	// The columns another kind owns are null, not zero. The control plane's
	// own CHECK refuses a row shaped like the wrong kind, and a zero here
	// would let a console draw a latency chart over a number that is not one.
	require.Nil(t, res.Measured.Sessions)
	require.Nil(t, res.Measured.Workflows)
	require.Nil(t, res.Measured.Findings)
	require.Nil(t, res.Measured.Goals)
}

func TestARouteWithNoBaselineSaysSoRatherThanReportingNoRegression(t *testing.T) {
	res := execute(t, context.Background(), mixRunner(), workload.Request{Kind: "observed_load"}, nil)

	byRoute := map[string]workload.RouteMetric{}
	for _, r := range res.Routes {
		byRoute[r.Route] = r
	}
	orders := byRoute["GET /orders"]
	require.NotNil(t, orders.BaselineP95Ms)
	require.NotNil(t, orders.P95Increase)
	require.InDelta(t, 0.5, *orders.P95Increase, 1e-9)

	health := byRoute["GET /health"]
	require.Nil(t, health.BaselineP95Ms)
	require.Nil(t, health.P95Increase,
		"no baseline and no change are different answers, and a zero standing in for "+
			"the first reads as no regression when it means nothing to compare with")

	// And the threshold row for that route says unverified rather than pass.
	var found bool
	for _, v := range res.Thresholds {
		if v.Scope == "GET /health" && v.Measure == "p95_increase" {
			found = true
			require.Equal(t, workload.VerdictUnverified, v.Value)
			require.Contains(t, v.Detail, "no baseline")
		}
	}
	require.True(t, found, "every route gets a p95 row, including the ones nothing could compare")
}

func TestAMixThatSentNothingIsUnverifiedRatherThanPassing(t *testing.T) {
	// The single most important assertion in the projection. af test exits
	// zero on unverified, and an entire nightly corpus in this repository was
	// green having never once reached an agent. A load run whose every route
	// the safe list refused sends nothing and must not read as clean.
	r := mixRunner()
	r.loadResult = &load.Result{Source: "otel", Sent: 0, Errors: map[string]int{}}
	r.loadRefused = []load.Route{{Method: "GET", Path: "/orders"}}

	res := execute(t, context.Background(), r, workload.Request{Kind: "observed_load"}, nil)
	require.Equal(t, workload.StateSucceeded, res.State)
	require.Equal(t, workload.VerdictUnverified, res.Verdict)
	require.Contains(t, res.Detail, "nothing was measured")
	require.Equal(t, []string{"GET /orders"}, res.Measured.RefusedRoutes)
	require.Equal(t, workload.VerdictUnverified, res.Thresholds[0].Value)
	require.Nil(t, res.Thresholds[0].Observed,
		"an error rate of zero over zero requests is not an error rate")
}

func TestTwoScenariosDoNotShareARouteRowAndDoNotShareAPercentile(t *testing.T) {
	r := mixRunner()
	r.scenarios = []load.ScenarioResult{
		{
			Scenario: "checkout", Verdict: "pass", Sessions: 2, Iterations: 3, Sent: 30,
			ScheduledMs: 900, DurationMs: 1000,
			Overall: load.Latency{P95Ms: 20},
			Steps: []load.RouteResult{
				{Route: "GET /", Sent: 10, Latency: load.Latency{P95Ms: 15}},
			},
			Assertions: []load.AssertionResult{
				{Name: "fast enough", Verdict: "pass", Measure: "p95_below_ms",
					Scope: "GET /", Threshold: f(200), Observed: f(15)},
			},
		},
		{
			Scenario: "refund", Verdict: "fail", Sessions: 1, Iterations: 1, Sent: 10,
			ScheduledMs: 100, DurationMs: 2000,
			Overall: load.Latency{P95Ms: 900},
			Steps: []load.RouteResult{
				{Route: "GET /", Sent: 10, Errors: 3, Latency: load.Latency{P95Ms: 800}},
			},
			Assertions: []load.AssertionResult{
				{Name: "fast enough", Verdict: "fail", Measure: "p95_below_ms",
					Scope: "GET /", Threshold: f(200), Observed: f(800)},
			},
		},
	}

	res := execute(t, context.Background(), r,
		workload.Request{Kind: "http_scenario", Select: "checkout,refund"}, nil)

	require.Equal(t, workload.VerdictFail, res.Verdict)
	require.Equal(t, 40, *res.Measured.Requests)
	require.Equal(t, 3, *res.Measured.Sessions)
	require.Equal(t, 4, *res.Measured.Iterations)
	require.Equal(t, 3, *res.Measured.Failures)

	// You cannot combine two p95s, so a run with more than one scenario has
	// none. Both scenarios' own percentiles survive on their route rows and in
	// native, so nothing is lost by refusing to invent one.
	require.Nil(t, res.Measured.P95Ms)
	require.Nil(t, res.Measured.ScheduledMs)
	require.InDelta(t, 2000, *res.Measured.DurationMs, 1e-9)

	require.Len(t, res.Routes, 2, "the same route in two scenarios is two rows")
	require.Equal(t, "checkout", res.Routes[0].Scenario)
	require.Equal(t, "refund", res.Routes[1].Scenario)
	require.Equal(t, res.Routes[0].Route, res.Routes[1].Route)
	require.NotEqual(t, res.Routes[0].Position, res.Routes[1].Position)

	require.Len(t, res.Thresholds, 2, "the same assertion name in two scenarios is two rows")
	require.Equal(t, "checkout", res.Thresholds[0].Scenario)
	require.Equal(t, "refund", res.Thresholds[1].Scenario)
	require.Equal(t, "p95_below_ms", res.Thresholds[1].Measure)
	require.InDelta(t, 800, *res.Thresholds[1].Observed, 1e-9)
}

func TestOneScenarioKeepsItsPercentiles(t *testing.T) {
	r := mixRunner()
	r.scenarios = []load.ScenarioResult{{
		Scenario: "checkout", Verdict: "pass", Sent: 10, ScheduledMs: 500, DurationMs: 600,
		Overall: load.Latency{P50Ms: 5, P95Ms: 20, MaxMs: 40},
	}}
	res := execute(t, context.Background(), r,
		workload.Request{Kind: "http_scenario", Select: "checkout"}, nil)
	require.InDelta(t, 20, *res.Measured.P95Ms, 1e-9)
	require.InDelta(t, 500, *res.Measured.ScheduledMs, 1e-9)
}

func TestNoScenarioRanIsUnverified(t *testing.T) {
	r := mixRunner()
	r.scenarios = []load.ScenarioResult{}
	res := execute(t, context.Background(), r,
		workload.Request{Kind: "http_scenario", Select: "checkout"}, nil)
	require.Equal(t, workload.VerdictUnverified, res.Verdict)
	require.Contains(t, res.Detail, "nothing was measured")
}

func TestAllFiveWorkflowCountsSurviveTheProjection(t *testing.T) {
	// Six workflows, every one unverified for want of a model key. A reader
	// given only passed and failed sees a run with no failures, which is the
	// green over nothing this product has shipped before.
	r := mixRunner()
	r.testReport = &env.TestReport{
		Unverified: 6, Duration: 90 * time.Second,
		Results: make([]env.WorkflowResult, 6),
	}
	res := execute(t, context.Background(), r, workload.Request{Kind: "browser_workflow"}, nil)

	require.Equal(t, 6, *res.Measured.Workflows)
	require.Equal(t, 0, *res.Measured.WorkflowsPassed)
	require.Equal(t, 0, *res.Measured.WorkflowsFailed)
	require.Equal(t, 6, *res.Measured.WorkflowsUnverified)
	require.Equal(t, workload.VerdictUnverified, res.Verdict,
		"a run where nothing could be checked is not a run with no failures")
}

func TestASelectionThatMatchedNothingIsBlockedRatherThanPassing(t *testing.T) {
	r := mixRunner()
	r.testReport = &env.TestReport{}
	res := execute(t, context.Background(), r,
		workload.Request{Kind: "browser_workflow", Select: "a-typo"}, nil)
	require.Equal(t, workload.VerdictBlocked, res.Verdict)
	require.Contains(t, res.Detail, "nothing was checked")
	require.Equal(t, 0, *res.Measured.Workflows)
}

func TestAViolatedInvariantFailsTheRunEvenWhenEveryWorkflowPassed(t *testing.T) {
	r := mixRunner()
	r.testReport = &env.TestReport{
		Passed: 3, Results: make([]env.WorkflowResult, 3),
		Invariants: []env.InvariantResult{
			{Name: "no-orphaned-orders", Held: true},
			{Name: "no-negative-totals", Held: false, Rows: [][]string{{"1", "-5"}}},
		},
	}
	res := execute(t, context.Background(), r, workload.Request{Kind: "browser_workflow"}, nil)
	require.Equal(t, workload.VerdictFail, res.Verdict)

	byName := map[string]workload.ThresholdVerdict{}
	for _, v := range res.Thresholds {
		byName[v.Name] = v
	}
	require.Equal(t, workload.VerdictPass, byName["no-orphaned-orders"].Value)
	require.Equal(t, workload.VerdictFail, byName["no-negative-totals"].Value)
}

func TestAnInvariantThatCouldNotRunIsUnverifiedRatherThanHeld(t *testing.T) {
	r := mixRunner()
	r.testReport = &env.TestReport{
		Passed: 1, Results: make([]env.WorkflowResult, 1),
		Invariants: []env.InvariantResult{
			{Name: "no-orphaned-orders", Held: false, Error: "relation \"orders\" does not exist"},
		},
	}
	res := execute(t, context.Background(), r, workload.Request{Kind: "browser_workflow"}, nil)
	require.Equal(t, workload.VerdictUnverified, res.Thresholds[0].Value)
	require.Contains(t, res.Thresholds[0].Detail, "could not be evaluated")
}

func TestAnExplorationCountsGoalsRatherThanAnsweringWithOneBoolean(t *testing.T) {
	r := mixRunner()
	r.exploreReport = &explore.Report{Explorations: []explore.Exploration{
		reached("upgrade", true, 1),
		reached("invite a teammate", false, 2),
	}}
	res := execute(t, context.Background(), r,
		workload.Request{Kind: "exploration", Select: "upgrade,invite a teammate"}, nil)

	require.Equal(t, 2, *res.Measured.Goals)
	require.Equal(t, 1, *res.Measured.GoalsReached)
	require.Equal(t, 3, *res.Measured.Findings)
	require.Equal(t, workload.VerdictUnverified, res.Verdict,
		"an exploration finds things; it never fails a build")
	require.Len(t, res.Thresholds, 2)
	require.Equal(t, workload.VerdictPass, res.Thresholds[0].Value)
	require.Equal(t, workload.VerdictUnverified, res.Thresholds[1].Value)
}

func TestEvidenceIsRecordedWithItsDigestAndSaidToBeRunnerLocal(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "sign-in.trace.zip")
	require.NoError(t, os.WriteFile(trace, []byte("not really a zip"), 0o600))

	r := mixRunner()
	report := &env.TestReport{Passed: 1}
	one := env.WorkflowResult{Workflow: "sign-in"}
	one.Evidence.Trace = trace
	one.Evidence.Screenshot = filepath.Join(dir, "gone.png")
	report.Results = []env.WorkflowResult{one}
	r.testReport = report

	res := execute(t, context.Background(), r, workload.Request{Kind: "browser_workflow"},
		func(o *workload.Options) { o.Root = dir })

	require.Len(t, res.Evidence, 2)
	byKind := map[string]workload.Evidence{}
	for _, e := range res.Evidence {
		byKind[e.Kind] = e
	}
	got := byKind["trace"]
	require.Equal(t, "sign-in", got.Label)
	require.Equal(t, workload.AvailabilityRunnerLocal, got.Availability)
	require.Equal(t, "sign-in.trace.zip", got.Locator, "recorded relative to the repository root")
	require.Len(t, got.SHA256, 64)
	require.Equal(t, int64(16), *got.SizeBytes)

	// A file the run named and that is not there is said to be gone rather
	// than reported as though it could be fetched.
	require.Equal(t, workload.AvailabilityNotRetained, byKind["screenshot"].Availability)
	require.Empty(t, byKind["screenshot"].SHA256)
}

func TestNoEvidenceIsEverClaimedToBeUploaded(t *testing.T) {
	// The engine uploads nothing. A row saying uploaded would be the runner
	// local path defect wearing a different word, and the control plane's own
	// CHECK refuses an uploaded row with no digest behind it.
	dir := t.TempDir()
	trace := filepath.Join(dir, "t.trace.zip")
	require.NoError(t, os.WriteFile(trace, []byte("x"), 0o600))
	r := mixRunner()
	one := env.WorkflowResult{Workflow: "w"}
	one.Evidence.Trace = trace
	r.testReport = &env.TestReport{Passed: 1, Results: []env.WorkflowResult{one}}

	res := execute(t, context.Background(), r, workload.Request{Kind: "browser_workflow"},
		func(o *workload.Options) { o.Root = dir })
	for _, e := range res.Evidence {
		require.NotEqual(t, "uploaded", e.Availability)
	}
}

func TestTheNativeResultIsCarriedVerbatim(t *testing.T) {
	// So that a projection which loses something can be caught by reading one
	// document rather than by running the workload again.
	res := execute(t, context.Background(), mixRunner(), workload.Request{Kind: "observed_load"}, nil)
	require.NotEmpty(t, res.Native)

	var native load.Result
	require.NoError(t, json.Unmarshal(res.Native, &native))
	require.Equal(t, 100, native.Sent)
	require.Len(t, native.Routes, 2)
}

func TestTheVerdictVocabularyIsTheProductsFiveWords(t *testing.T) {
	// Five values and no warn. The control plane's verdict_value enum has
	// exactly these, and a sixth invented here would be rejected by the
	// database after the run was already paid for.
	allowed := map[string]bool{
		workload.VerdictPass: true, workload.VerdictFail: true, workload.VerdictFlaky: true,
		workload.VerdictBlocked: true, workload.VerdictUnverified: true,
	}
	runners := []func() *fakeRunner{
		mixRunner,
		func() *fakeRunner {
			r := mixRunner()
			r.scenarios = []load.ScenarioResult{{Scenario: "s", Verdict: "blocked"}}
			return r
		},
		func() *fakeRunner {
			r := mixRunner()
			r.testReport = &env.TestReport{Flaky: 1, Results: make([]env.WorkflowResult, 1)}
			return r
		},
		func() *fakeRunner {
			r := mixRunner()
			r.exploreReport = &explore.Report{Explorations: []explore.Exploration{reached("g", true, 0)}}
			return r
		},
	}
	reqs := []workload.Request{
		{Kind: "observed_load"},
		{Kind: "http_scenario", Select: "s"},
		{Kind: "browser_workflow"},
		{Kind: "exploration", Select: "g"},
	}
	for i, make := range runners {
		res := execute(t, context.Background(), make(), reqs[i], nil)
		require.Truef(t, allowed[res.Verdict], "%s is not one of the product's five verdicts", res.Verdict)
		for _, v := range res.Thresholds {
			require.Truef(t, allowed[v.Value], "%s is not one of the product's five verdicts", v.Value)
		}
	}
}

func f(v float64) *float64 { return &v }

func pendingResource() provider.PendingResource {
	return provider.PendingResource{Kind: "container", ID: "abc", Reason: "the daemon refused"}
}

func reached(name string, ok bool, findings int) explore.Exploration {
	e := explore.Exploration{Name: name, Goal: name, Reached: ok}
	e.Outcome.Verdict = "pass"
	if !ok {
		e.Outcome.Verdict = "unverified"
		e.Outcome.Detail = "the wander did not find the way"
	}
	e.Findings = make([]explore.Finding, findings)
	return e
}

// A failing load run says why, on the line a person reads first.
//
// It said nothing. `mixDetail` returned empty whenever anything was sent, so a
// run with a `fail` verdict arrived with an empty detail and the reason lived
// only in the threshold rows. The scenario path beside it has always named its
// failing scenario, and the two reading differently is what kept this
// invisible: each one is correct on its own.
func TestAFailingLoadRunSaysWhichThresholdItBreached(t *testing.T) {
	for _, c := range []struct {
		name     string
		result   *load.Result
		contains []string
	}{
		{
			name: "an error rate breach names both numbers in the units the manifest uses",
			result: &load.Result{
				Sent: 1000, ErrorRate: 0.042,
				Overall: load.Latency{P95Ms: 100},
			},
			contains: []string{"the error rate was 4.2 percent", "threshold of 1.0 percent"},
		},
		{
			name: "a p95 breach names the route it happened on",
			result: &load.Result{
				Sent: 1000, ErrorRate: 0,
				Overall: load.Latency{P95Ms: 100},
				Routes: []load.RouteResult{
					{Route: "GET /checkout", Sent: 1000, HasBaseline: true, P95Increase: 0.45},
				},
			},
			contains: []string{"p95 on GET /checkout rose 45.0 percent", "threshold of 25.0 percent"},
		},
		{
			name: "several breaches name the first and count the rest",
			result: &load.Result{
				Sent: 1000, ErrorRate: 0.5,
				Overall: load.Latency{P95Ms: 100},
				Routes: []load.RouteResult{
					{Route: "GET /a", Sent: 500, HasBaseline: true, P95Increase: 0.9},
					{Route: "GET /b", Sent: 500, HasBaseline: true, P95Increase: 0.8},
				},
			},
			contains: []string{"the error rate was 50.0 percent", "and 2 more thresholds were breached"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := mixRunner()
			r.loadResult = c.result
			res := execute(t, context.Background(), r,
				workload.Request{Kind: "observed_load"}, nil)
			require.Equal(t, workload.VerdictFail, res.Verdict,
				"the fixture is meant to breach something")
			require.NotEmpty(t, res.Detail,
				"a failing run reported no reason at all, so a console leads with an empty line")
			for _, want := range c.contains {
				require.Contains(t, res.Detail, want)
			}
		})
	}
}

// A clean run still says nothing, which is correct rather than an oversight:
// there is no failure to explain and a sentence here would be noise on every
// passing run.
func TestAPassingLoadRunSaysNothing(t *testing.T) {
	r := mixRunner()
	r.loadResult = &load.Result{
		Sent: 1000, ErrorRate: 0, Overall: load.Latency{P95Ms: 50},
		Routes: []load.RouteResult{
			{Route: "GET /ok", Sent: 1000, HasBaseline: true, P95Increase: 0.01},
		},
	}
	res := execute(t, context.Background(), r, workload.Request{Kind: "observed_load"}, nil)
	require.Equal(t, workload.VerdictPass, res.Verdict)
	require.Empty(t, res.Detail)
}

// A run that sent nothing keeps the sentence it always had, because "nothing
// was measured" and "a threshold was breached" are different findings and the
// first one outranks every threshold row beneath it.
func TestALoadRunThatSentNothingStillSaysSo(t *testing.T) {
	r := mixRunner()
	r.loadResult = &load.Result{Sent: 0}
	res := execute(t, context.Background(), r, workload.Request{Kind: "observed_load"}, nil)
	require.Equal(t, workload.VerdictUnverified, res.Verdict)
	require.Contains(t, res.Detail, "no requests were sent")
}

// A failing scenario or workflow that reports no reason says so, rather than
// rendering a name, a colon and nothing.
//
// `name + ": " + detail` with an empty detail is a WORSE empty than an absent
// one: it reads as a sentence that was cut off, so a person goes looking for
// the missing half. Unreachable today, because every failing assertion sets a
// detail; the guard is here so it stays unreachable when somebody adds the next
// failure path and forgets.
func TestAFailureWithNoReasonDoesNotRenderADanglingColon(t *testing.T) {
	require.Equal(t, "checkout: the p95 was over budget",
		workload.NamedForTest("checkout", "the p95 was over budget"))
	for _, empty := range []string{"", "   ", "\t"} {
		got := workload.NamedForTest("checkout", empty)
		require.NotContains(t, got, ": ",
			"an empty reason rendered as a name and a colon, which reads as a truncated sentence")
		require.Contains(t, got, "checkout")
		require.Contains(t, got, "no reason")
	}
}

// An exploration that did not reach its goal says which one.
//
// An exploration never fails, so this detail never lands under a red verdict,
// which was the reasoning for leaving it empty. It lands under `unverified`,
// which is not a pass and is the same blank first line a failing load run used
// to have.
func TestAnExplorationThatMissedItsGoalSaysWhichOne(t *testing.T) {
	r := &fakeRunner{envID: "demo-main-abc123", exploreReport: &explore.Report{
		Explorations: []explore.Exploration{
			{Name: "sign up", Reached: true},
			{Name: "check out", Reached: false},
		},
	}}
	res := execute(t, context.Background(), r,
		workload.Request{Kind: "exploration", Select: "sign up,check out"}, nil)
	require.Equal(t, workload.VerdictUnverified, res.Verdict)
	require.Contains(t, res.Detail, "check out did not reach its goal")

	two := &fakeRunner{envID: "demo-main-abc123", exploreReport: &explore.Report{
		Explorations: []explore.Exploration{
			{Name: "sign up", Reached: false},
			{Name: "check out", Reached: false},
		},
	}}
	res = execute(t, context.Background(), two,
		workload.Request{Kind: "exploration", Select: "sign up,check out"}, nil)
	require.Contains(t, res.Detail, "2 goals were not reached, starting with sign up")

	all := &fakeRunner{envID: "demo-main-abc123", exploreReport: &explore.Report{
		Explorations: []explore.Exploration{{Name: "sign up", Reached: true}},
	}}
	res = execute(t, context.Background(), all,
		workload.Request{Kind: "exploration", Select: "sign up"}, nil)
	require.Equal(t, workload.VerdictPass, res.Verdict)
	require.Empty(t, res.Detail, "a run that reached everything has nothing to explain")
}

// The two exploration shapes examples/next-app declares, and the distinction
// between them that a console has to be able to draw.
//
// `what-each-customer-spent` reaches its goal on the first page. `correct-a-
// customer-name` never reaches its goal, on purpose, because that page offers
// no control to press: somebody looked and the application had no way onward.
//
// THE THING THIS PINS is that the second is NOT `blocked`. Blocked means the
// run could not be carried out, and this run was carried out perfectly: the
// state is that the work happened and the verdict is a finding. The runner's
// own VERDICT_FOR_CAUSE is what makes that true, mapping the `explored` cause
// to pass and reserving blocked for `runner-failure` and
// `environment-incomplete`, so a dead end arrives here as a passing outcome
// that simply did not reach, and only `Reached` tells them apart.
//
// An exploration cannot fail a build, which docs/concepts/exploration states,
// so a wander that found a wall is a finding rather than a defect.
func TestADeadEndIsUnverifiedRatherThanBlocked(t *testing.T) {
	reached := explore.Exploration{Name: "what-each-customer-spent", Reached: true}
	reached.Outcome.Verdict = workload.VerdictPass
	deadEnd := explore.Exploration{Name: "correct-a-customer-name", Reached: false}
	// The cause is `explored`, which the runner maps to pass. A dead end is not
	// a runner failure and not an incomplete environment, which are the only
	// two causes that produce blocked.
	deadEnd.Outcome.Verdict = workload.VerdictPass

	r := &fakeRunner{envID: "next-app-main-abc123", exploreReport: &explore.Report{
		Explorations: []explore.Exploration{reached, deadEnd},
	}}
	res := execute(t, context.Background(), r,
		workload.Request{Kind: "exploration", Select: "what-each-customer-spent,correct-a-customer-name"}, nil)

	require.Equal(t, workload.StateSucceeded, res.State,
		"the work happened; a dead end is a finding and not a run that could not be carried out")
	require.Equal(t, workload.VerdictUnverified, res.Verdict)
	require.NotEqual(t, workload.VerdictBlocked, res.Verdict,
		"a page with no control to press was reported as a run that could not proceed")
	require.Contains(t, res.Detail, "correct-a-customer-name did not reach its goal",
		"the one useful thing to say about this run is which goal was not reached")

	// And blocked is still reachable, so the distinction above is a real one
	// rather than a verdict nothing produces.
	blocked := explore.Exploration{Name: "correct-a-customer-name", Reached: false}
	blocked.Outcome.Verdict = workload.VerdictBlocked
	blocked.Outcome.Detail = "the browser could not be started"
	stopped := &fakeRunner{envID: "next-app-main-abc123", exploreReport: &explore.Report{
		Explorations: []explore.Exploration{blocked},
	}}
	res = execute(t, context.Background(), stopped, workload.Request{
		Kind: "exploration", Select: "correct-a-customer-name"}, nil)
	require.Equal(t, workload.VerdictBlocked, res.Verdict)
	require.Contains(t, res.Detail, "the browser could not be started")
}
