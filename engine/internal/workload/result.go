package workload

import (
	"encoding/json"
	"time"
)

// ResultSchema names the wire format, so a control plane reading a document
// from an engine older than itself can tell rather than guess.
const ResultSchema = "antifailure.workload.result/v1"

// State says whether the work happened. Verdict says what it found. They are
// two fields because they are two facts: a run that finishes cleanly and
// breaches every threshold is StateSucceeded with a verdict of fail, and a run
// whose thresholds could not be evaluated is StateSucceeded with unverified.
//
// Collapsing them is how an exit code of zero over work that never happened
// reads as a pass, which this repository has already shipped once: an entire
// nightly corpus was green having never reached an agent.
type State string

const (
	// StateSucceeded means the work ran to completion. It says nothing about
	// what the work found.
	StateSucceeded State = "succeeded"
	// StateFailed means the work could not be carried out. A refused knob, an
	// environment that was not up, a runner that could not start.
	StateFailed State = "failed"
	// StateCancelled means a signal or a cancelled context stopped it.
	StateCancelled State = "cancelled"
	// StateTimedOut means the deadline passed with the work still running.
	StateTimedOut State = "timed_out"
)

// Verdicts, spelled as the control plane's verdict_value enum spells them.
//
// Five values and no warn. The engine's run level report vocabulary has a
// sixth, warn, which comes from a policy finding; nothing in this package
// produces one, and if that ever changes the control plane's enum has to grow
// before the engine may send it.
const (
	VerdictPass       = "pass"
	VerdictFail       = "fail"
	VerdictFlaky      = "flaky"
	VerdictBlocked    = "blocked"
	VerdictUnverified = "unverified"
)

// Result is one execution, in the shape the control plane stores it.
//
// The flat projections beneath (Measured, Routes, Thresholds, Evidence) are
// derived from Native and never assembled beside it. Native is the engine's
// own type for this kind, untouched, so that a projection which loses
// something can be caught by reading the same document rather than by running
// the workload again.
type Result struct {
	Schema string `json:"schema"`
	// RunID is the control plane's identifier, echoed back. Empty when a
	// person ran this by hand.
	RunID string `json:"run_id,omitempty"`
	Kind  Kind   `json:"kind"`
	// RequestedKind is what the request actually said, when it said it with a
	// legacy verb. Present so a reader can see the alias rather than infer it.
	RequestedKind string `json:"requested_kind,omitempty"`
	State         State  `json:"state"`
	Verdict       string `json:"verdict"`
	// FailureCode is the AF code behind a failed state, or the code behind a
	// fail verdict when one applies.
	FailureCode string `json:"failure_code,omitempty"`
	Detail      string `json:"detail,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	DurationMs float64   `json:"duration_ms"`

	Environment Environment `json:"environment"`
	Engine      Engine      `json:"engine"`
	Reproduce   Reproduce   `json:"reproduce"`
	// Refusals is every knob the request set that this kind cannot honour. A
	// result carrying refusals is always StateFailed: nothing ran.
	Refusals []Refusal `json:"refusals,omitempty"`

	Measured   Measured           `json:"result"`
	Routes     []RouteMetric      `json:"routes"`
	Thresholds []ThresholdVerdict `json:"thresholds"`
	Evidence   []Evidence         `json:"evidence"`

	// Teardown is what happened to the environment when the work ended, or
	// nil when the caller did not ask for one.
	//
	// Present in the result rather than only in a log because the control
	// plane's teardown used to be an UPDATE and a comment saying the engine
	// reads the row. Nothing read the row. This is the acknowledgement that a
	// runtime command needs: what was actually removed, and what is still
	// standing.
	Teardown *TeardownResult `json:"teardown,omitempty"`

	// Native is the engine's own result for this kind, verbatim. A load.Result,
	// a slice of load.ScenarioResult, an env.TestReport, or an explore.Report.
	Native json.RawMessage `json:"native,omitempty"`
}

// TeardownResult is what reaching the runtime actually accomplished.
type TeardownResult struct {
	EnvID   string `json:"env_id,omitempty"`
	Removed int    `json:"removed"`
	// Pending is what could not be removed, one entry each. Never null, so a
	// reader can tell "nothing pending" from "the question was not asked".
	Pending []PendingResource `json:"pending"`
	// Error is why the teardown could not run at all, as distinct from
	// resources it tried and failed to remove.
	Error string `json:"error,omitempty"`
}

// PendingResource is one thing still standing after a teardown.
type PendingResource struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

// Environment is which environment the work ran against.
type Environment struct {
	EnvID string `json:"env_id"`
	URL   string `json:"url,omitempty"`
	// Branch is the source control branch the environment belongs to, which is
	// how a hosted reader tells two environments of one repository apart.
	Branch string `json:"branch,omitempty"`
}

// Engine is which build produced this document.
type Engine struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Edition string `json:"edition,omitempty"`
}

// Reproduce is how to get this run again on a laptop.
type Reproduce struct {
	// Argv is the plain command, already split. A control plane rendering a
	// copy button uses Command; a caller running it uses Argv, so neither has
	// to re-split the other's.
	Argv []string `json:"argv"`
	// Command is the same thing as one pasteable line.
	Command string `json:"command"`
	// ManifestDigest is sha256 of the manifest the run read. Two runs of the
	// same argv against different manifests are two different runs, and this
	// is what says so.
	ManifestDigest string `json:"manifest_digest,omitempty"`
	// Note names what the command needs that the command line cannot carry.
	Note string `json:"note,omitempty"`
}

// Measured is the one row of numbers a run produced.
//
// Every numeric field is a pointer so that a column which does not apply to
// this kind marshals as null rather than as zero. The difference is the whole
// point: a browser run reporting zero requests would let a console draw a
// latency chart over a number that is not a latency, and the control plane's
// own CHECK constraint refuses a row shaped like the wrong kind.
type Measured struct {
	// Sent traffic. observed_load and http_scenario only.
	Requests     *int     `json:"requests"`
	Failures     *int     `json:"failures"`
	ErrorRate    *float64 `json:"error_rate"`
	TargetRate   *float64 `json:"target_rate"`
	AchievedRate *float64 `json:"achieved_rate"`
	P50Ms        *float64 `json:"p50_ms"`
	P90Ms        *float64 `json:"p90_ms"`
	P95Ms        *float64 `json:"p95_ms"`
	P99Ms        *float64 `json:"p99_ms"`
	MaxMs        *float64 `json:"max_ms"`

	// A journey. http_scenario only.
	Sessions    *int     `json:"sessions"`
	Iterations  *int     `json:"iterations"`
	ScheduledMs *float64 `json:"scheduled_ms"`

	// A browser. browser_workflow only.
	//
	// Five counts rather than two. A run of six workflows that were all
	// unverified for want of a model key has zero passed and zero failed, and
	// a reader given only those two sees a run with no failures. That is the
	// green over nothing this product has shipped before, moved into a table.
	Workflows           *int `json:"workflows"`
	WorkflowsPassed     *int `json:"workflows_passed"`
	WorkflowsFailed     *int `json:"workflows_failed"`
	WorkflowsFlaky      *int `json:"workflows_flaky"`
	WorkflowsBlocked    *int `json:"workflows_blocked"`
	WorkflowsUnverified *int `json:"workflows_unverified"`
	Steps               *int `json:"steps"`

	// A wander. exploration only.
	//
	// Two counts rather than one boolean, because a version selects up to
	// fifty goals and one boolean cannot say which of them became unreachable.
	Goals        *int `json:"goals"`
	GoalsReached *int `json:"goals_reached"`
	Findings     *int `json:"findings"`

	DurationMs *float64 `json:"duration_ms"`
	// Source is where the traffic mix came from, so a reader can tell
	// production's shape from a default. observed_load only.
	Source string `json:"source,omitempty"`
	// RefusedRoutes is what the manifest's safe list would not send. Never
	// null, because an empty list and an unanswered question are different
	// answers and a console renders them differently.
	RefusedRoutes []string `json:"refused_routes"`
	// Errors counts failures by reason. The keys are a closed set the load
	// runner produces: an HTTP status of 500 or above spelled as its number,
	// or one of malformed request, timeout, connection refused, connection
	// reset, name not resolved, request failed.
	Errors map[string]int `json:"errors,omitempty"`
}

// RouteMetric is one route's numbers.
type RouteMetric struct {
	// Scenario names which declared journey this route was measured inside,
	// and is empty for a mix. Two scenarios in one run routinely send the same
	// route, and their percentiles cannot be merged: you cannot average two
	// p95s. So the pair (scenario, route) is the identity, not the route.
	Scenario string   `json:"scenario,omitempty"`
	Route    string   `json:"route"`
	Sent     int      `json:"sent"`
	Errors   int      `json:"errors"`
	P50Ms    *float64 `json:"p50_ms"`
	P90Ms    *float64 `json:"p90_ms"`
	P95Ms    *float64 `json:"p95_ms"`
	P99Ms    *float64 `json:"p99_ms"`
	MaxMs    *float64 `json:"max_ms"`
	// BaselineP95Ms is what production serves this route in, when the shape
	// carried it. P95Increase is a ratio against it.
	//
	// Both nil together or both set together. No baseline and no change are
	// different answers, and a zero ratio standing in for the first would read
	// as no regression when it means nothing to compare with.
	BaselineP95Ms *float64 `json:"baseline_p95_ms"`
	P95Increase   *float64 `json:"p95_increase"`
	Position      int      `json:"position"`
}

// ThresholdVerdict is one assertion or one manifest threshold, and what it
// said.
type ThresholdVerdict struct {
	// Scenario is which declared journey declared this assertion, empty for a
	// manifest wide threshold. Two scenarios can each name an assertion "fast
	// enough", so the name alone is not an identity.
	Scenario string `json:"scenario,omitempty"`
	Name     string `json:"name"`
	// Scope is the route the assertion was narrowed to, empty for a run wide
	// one.
	Scope string `json:"scope,omitempty"`
	// Measure is which of the measures this is: every_request_succeeded,
	// p95_below_ms, error_rate_below, status_in, p95_increase, error_rate.
	Measure   string   `json:"measure"`
	Threshold *float64 `json:"threshold"`
	Observed  *float64 `json:"observed"`
	// Value is a verdict_value.
	Value    string `json:"value"`
	Detail   string `json:"detail,omitempty"`
	Position int    `json:"position"`
}

// Evidence is an artifact the run left behind.
type Evidence struct {
	// Kind is trace, screenshot or video.
	Kind string `json:"kind"`
	// Label is the workflow or goal it belongs to.
	Label string `json:"label,omitempty"`
	// Availability is always runner_local today, and the honesty is the point.
	//
	// The engine uploads nothing. A trace lives at .antifailure/artifacts/...
	// on the machine that ran it, and on a hosted runner that machine is gone
	// minutes later. A console that renders such a path as a link sends
	// somebody to a 404 and blames itself. Reports in this product have
	// carried exactly those paths.
	Availability string `json:"availability"`
	// Locator is the path, relative to the repository root, so a person who
	// ran this locally can open it and a hosted reader can see it was never
	// theirs to open.
	Locator string `json:"locator"`
	// SHA256 and SizeBytes are recorded when the file could be read. They are
	// what an uploader would later use to prove it moved the right bytes.
	SHA256    string `json:"sha256,omitempty"`
	SizeBytes *int64 `json:"size_bytes,omitempty"`
}

// Availability values.
const (
	// AvailabilityRunnerLocal means the bytes are on the machine that ran the
	// workload and nowhere else.
	AvailabilityRunnerLocal = "runner_local"
	// AvailabilityNotRetained means the run named an artifact that is no
	// longer on disk.
	AvailabilityNotRetained = "not_retained"
)

func intp(v int) *int           { return &v }
func floatp(v float64) *float64 { return &v }
