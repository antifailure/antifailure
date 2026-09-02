package workload

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/load"
)

// Runner is the part of the orchestrator this package drives.
//
// An interface rather than the concrete type for one reason that matters: the
// orderings this package has to get right are cancellation, timeout and a late
// terminal event, and every one of them is a question about WHEN a call
// returns rather than about what it computes. Against a real orchestrator each
// of those tests needs Docker, a database branch and a browser, so they would
// be the tests that get skipped, and a skip reads as a pass.
//
// *env.Orchestrator satisfies this. Nothing else in the engine does, and the
// end to end test that runs a real workload against a real environment is what
// proves the interface is the real thing rather than a shape a fake happens to
// fit.
type Runner interface {
	EnvID() string
	Status(ctx context.Context) (*env.Result, error)
	Load(ctx context.Context, opts env.LoadOptions) (*load.Result, []load.Route, error)
	Scenarios(ctx context.Context, opts env.ScenarioOptions) ([]load.ScenarioResult, error)
	Test(ctx context.Context, opts env.TestOptions) (*env.TestReport, error)
	Explore(ctx context.Context, opts env.ExploreOptions) (*explore.Report, error)
	Thresholds() (p95Increase, errorRate float64)
	Down(ctx context.Context) (*env.Teardown, error)
}

// Options configure one execution.
type Options struct {
	// Plan is what to run. Required.
	Plan *Plan
	// Runner is what runs it. Required.
	Runner Runner
	// Clock is the time source. Required, because every timestamp in the
	// result comes from it and a real clock in a test makes the document
	// unstable for no benefit.
	Clock clock.Clock
	// Engine describes this build, for the result envelope.
	Engine Engine
	// Branch is the source control branch the environment belongs to.
	Branch string
	// Root is the repository root, so evidence paths are recorded relative to
	// it rather than as somebody's home directory.
	Root string
	// ManifestDigest is sha256 of the manifest the run read.
	ManifestDigest string
	// Timeout bounds the work. Zero means the caller's context is the only
	// bound. A timeout that fires produces a result rather than an error,
	// because "it did not finish in time" is a finding and losing it would be
	// the same silence the hosted teardown used to produce.
	Timeout time.Duration
	// Teardown removes the environment when the work ends, however it ends.
	//
	// Off by default. A hosted run against a long lived environment must not
	// take it down, and a run that brought one up for itself must. The caller
	// knows which it is and this package does not.
	Teardown bool
	// TeardownGrace bounds the teardown. It runs on a context detached from
	// the caller's, so a cancelled run still cleans up, and this is what stops
	// that detached context running forever.
	TeardownGrace time.Duration
}

// defaultTeardownGrace matches the five minutes af ci allows its own teardown.
const defaultTeardownGrace = 5 * time.Minute

// Execute runs one workload and reports what it did.
//
// It returns a Result for every outcome including cancellation, a timeout and
// a refused knob, and returns an error only when it could not produce a
// document at all. A caller that turns a non nil error into "nothing to
// report" would throw away the more useful half of every failure.
func Execute(ctx context.Context, opts Options) (*Result, error) {
	if opts.Plan == nil || opts.Runner == nil || opts.Clock == nil {
		return nil, errors.New("workload: Execute needs a plan, a runner and a clock")
	}
	p := opts.Plan
	started := opts.Clock.Now()

	res := &Result{
		Schema:      ResultSchema,
		RunID:       p.RunID,
		Kind:        p.Kind,
		StartedAt:   started,
		Environment: Environment{EnvID: opts.Runner.EnvID(), Branch: opts.Branch},
		Engine:      opts.Engine,
		Reproduce: Reproduce{
			Argv:           p.Argv(),
			Command:        p.Command(),
			ManifestDigest: opts.ManifestDigest,
			Note: "run this from the repository root, against an environment that is " +
				"already up for the same branch",
		},
		Refusals:   p.Refusals,
		Measured:   Measured{RefusedRoutes: []string{}},
		Routes:     []RouteMetric{},
		Thresholds: []ThresholdVerdict{},
		Evidence:   []Evidence{},
	}

	// A plan carrying refusals never ran, and the teardown below still does.
	// Reporting a refusal without cleaning up would leave an environment
	// standing for a run that never started, which is the leak this product
	// exists to prevent.
	if len(p.Refusals) > 0 {
		finish(res, opts, StateFailed, VerdictBlocked, string(aferrors.AFWLD002),
			"the request set knobs this workload's command has no flag for")
		tearDown(ctx, opts, res)
		return res, nil
	}

	work := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		work, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Asked before the work rather than after, so a cancellation that arrived
	// before anything started is reported as a cancellation rather than as an
	// environment that was not up.
	if err := work.Err(); err != nil {
		finish(res, opts, stateFor(err), VerdictBlocked, "", stopped(err))
		tearDown(ctx, opts, res)
		return res, nil
	}

	status, err := opts.Runner.Status(work)
	if err == nil && status != nil {
		res.Environment.EnvID = status.EnvID
		res.Environment.URL = status.URL
	}
	if err != nil {
		finish(res, opts, StateFailed, VerdictBlocked, codeOf(err), err.Error())
		tearDown(ctx, opts, res)
		return res, nil
	}

	switch p.Kind {
	case ObservedLoad:
		runObservedLoad(work, opts, res)
	case HTTPScenario:
		runScenarios(work, opts, res)
	case BrowserWorkflow:
		runWorkflows(work, opts, res)
	case Exploration:
		runExploration(work, opts, res)
	}

	tearDown(ctx, opts, res)
	return res, nil
}

// settle decides state and verdict from what the path returned.
//
// The ordering rule this function exists to hold: a cancellation that arrives
// AFTER the work produced a result does not rewrite that result. The engine
// paths return their own error, and a context that is cancelled a moment later
// says nothing about a run that already finished. Checking ctx.Err() here
// instead of the returned error is exactly how a completed run becomes a
// cancelled one in a report.
func settle(res *Result, opts Options, err error, verdict string, detail string) {
	switch {
	case err == nil:
		finish(res, opts, StateSucceeded, verdict, "", detail)
	case isStop(err):
		// A stopped run still carries whatever it measured before it stopped,
		// which the callers have already projected. The verdict is blocked
		// rather than fail: work that did not finish has not found anything.
		finish(res, opts, stateFor(err), VerdictBlocked, "", stopped(err))
	default:
		finish(res, opts, StateFailed, VerdictBlocked, codeOf(err), err.Error())
	}
}

func finish(res *Result, opts Options, state State, verdict, code, detail string) {
	res.State = state
	res.Verdict = verdict
	res.FailureCode = code
	res.Detail = detail
	res.FinishedAt = opts.Clock.Now()
	res.DurationMs = float64(res.FinishedAt.Sub(res.StartedAt).Microseconds()) / 1000
}

// tearDown removes the environment on a context the caller cannot cancel.
//
// The detachment is load bearing and it is the same decision af ci made. A
// person pressing stop, or a hosted deadline firing, cancels the context the
// work is running on. Cleaning up on that same context means the cleanup is
// cancelled too, and the containers keep running while the row says the run
// ended. That is the defect this whole workstream exists to close: a teardown
// that marks a row and never reaches the runtime.
func tearDown(ctx context.Context, opts Options, res *Result) {
	if !opts.Teardown {
		return
	}
	grace := opts.TeardownGrace
	if grace <= 0 {
		grace = defaultTeardownGrace
	}
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), grace)
	defer cancel()

	td, err := opts.Runner.Down(c)
	res.Teardown = TearDownResultOf(td, err)
	switch {
	case err != nil:
		res.Detail = appendDetail(res.Detail, "the environment could not be torn down: "+err.Error())
	case len(res.Teardown.Pending) > 0:
		res.Detail = appendDetail(res.Detail,
			"the environment was torn down with resources still pending; run 'af down' to finish it")
	}
}

// TearDownResultOf turns what Down reported into the acknowledgement a control
// plane stores.
//
// Exported because af workload teardown produces the same document without
// running a workload, and two shapes for one answer is how a console ends up
// with two renderers that disagree.
func TearDownResultOf(td *env.Teardown, err error) *TeardownResult {
	out := &TeardownResult{Pending: []PendingResource{}}
	if err != nil {
		out.Error = err.Error()
	}
	if td == nil {
		return out
	}
	out.EnvID = td.EnvID
	out.Removed = td.Removed
	for _, p := range td.Pending {
		out.Pending = append(out.Pending, PendingResource{Kind: p.Kind, ID: p.ID, Reason: p.Reason})
	}
	return out
}

func appendDetail(existing, add string) string {
	if existing == "" {
		return add
	}
	return existing + ". " + add
}

// isStop reports whether an error is the caller stopping rather than the work
// failing.
func isStop(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func stateFor(err error) State {
	if errors.Is(err, context.DeadlineExceeded) {
		return StateTimedOut
	}
	return StateCancelled
}

func stopped(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "the run passed its deadline before finishing"
	}
	return "the run was cancelled before finishing"
}

// codeOf pulls the AF code out of an engine error, or reports nothing rather
// than inventing one.
func codeOf(err error) string {
	var coded *aferrors.Error
	if errors.As(err, &coded) {
		return string(coded.Entry.Code)
	}
	return ""
}

// nativeOf marshals the engine's own result for this kind.
//
// A failure to marshal is recorded in the detail rather than raised, because
// the projection is already computed and losing a whole result to a JSON
// encoding problem in the copy of it would be the wrong trade.
func nativeOf(res *Result, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		res.Detail = appendDetail(res.Detail, "the engine result could not be serialised: "+err.Error())
		return
	}
	res.Native = body
}
