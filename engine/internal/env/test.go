package env

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/personas"
	"github.com/antifailure/antifailure/engine/internal/runnerpath"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The runner is a subprocess with a document boundary rather than a library.
//
// The engine is Go and the browser automation that actually works is
// TypeScript. The alternatives are a worse browser driver or a foreign
// function interface, and a JSON document in and a JSON document out is
// neither. It is also a boundary a person can drive by hand, which is worth
// more than it sounds the first time somebody has to debug it.

// TestOptions configure a run.
type TestOptions struct {
	// Only runs just the named workflows. Empty runs all of them.
	Only []string
	// Attempts is how many times a workflow is tried before being called
	// flaky or failed.
	Attempts int
	// Headed shows the browser, for somebody watching it work.
	Headed bool
	// RunnerPath overrides where the runner lives.
	RunnerPath string
}

// TestReport is what a run produced.
type TestReport struct {
	Results    []WorkflowResult `json:"results"`
	Passed     int              `json:"passed"`
	Failed     int              `json:"failed"`
	Flaky      int              `json:"flaky"`
	Blocked    int              `json:"blocked"`
	Unverified int              `json:"unverified"`
	// Invariants is what the data said after the workflows ran. Filled by the
	// engine rather than by the runner, which never sees the database.
	Invariants []InvariantResult `json:"invariants,omitempty"`
	// Notes say what the run noticed that belongs to no single workflow.
	// Filled by the engine, which is the only half that sees the environment.
	Notes    []string      `json:"notes,omitempty"`
	Duration time.Duration `json:"-"`
}

// InvariantsViolated counts the invariants shown to be broken.
func (r TestReport) InvariantsViolated() int {
	n := 0
	for _, inv := range r.Invariants {
		if inv.Violated() {
			n++
		}
	}
	return n
}

// InvariantsBlocked counts the invariants that produced no verdict.
//
// Named blocked rather than failed for the same reason a workflow is: the
// check not happening is a fact about us, and counting it against the
// application would make an incomplete environment indistinguishable from
// broken data.
func (r TestReport) InvariantsBlocked() int {
	n := 0
	for _, inv := range r.Invariants {
		if inv.Error != "" {
			n++
		}
	}
	return n
}

// WorkflowResult is one workflow's outcome.
type WorkflowResult struct {
	Workflow string `json:"workflow"`
	Outcome  struct {
		Verdict      string   `json:"verdict"`
		Cause        string   `json:"cause"`
		Detail       string   `json:"detail"`
		Reproduction []string `json:"reproduction"`
	} `json:"outcome"`
	Steps    []string `json:"steps"`
	Evidence struct {
		Video      string   `json:"video"`
		Trace      string   `json:"trace"`
		Screenshot string   `json:"screenshot"`
		Console    []string `json:"console"`
		Failed     []string `json:"failed"`
	} `json:"evidence"`
	DurationMs int64 `json:"durationMs"`
	// StartedAt and FinishedAt are when the runner ran this workflow, RFC 3339
	// in UTC. They exist so an outbound request can be attributed to the
	// workflow that caused it; see markSynthesized.
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

// AnyFailed reports whether anything counted against the application.
//
// Blocked does not, and that is deliberate: an incomplete environment must not
// be indistinguishable from a broken application, or people stop reading
// either.
//
// A violated invariant does count, and it is the reason invariants exist. A
// workflow that signed in, placed an order and saw a success page has passed
// on the screen; if that order now has no user, the run is a failure and the
// screen was never going to say so.
func (r TestReport) AnyFailed() bool {
	return r.Failed > 0 || r.InvariantsViolated() > 0
}

// NothingVerified reports that no workflow reached a verdict about the
// application.
//
// The counterpart to AnyFailed and not a weakening of it. AnyFailed answers
// "did we find something wrong", and the comment above is right that a blocked
// workflow must not answer yes. This answers "did we find anything at all",
// which nothing asked until now: `af test` exited zero on a run where every
// workflow was blocked, which tells a pipeline the application was checked and
// found fine when it was never driven.
//
// Passed, failed and flaky are verdicts about the application. Blocked and
// unverified are statements about us. A run made only of the second kind, or of
// no workflows at all, has tested nothing.
func (r TestReport) NothingVerified() bool {
	return r.Passed+r.Failed+r.Flaky == 0
}

// jobDocument is what the runner reads.
type jobDocument struct {
	BaseURL   string        `json:"base_url"`
	Artifacts string        `json:"artifacts"`
	Workflows []workflowDoc `json:"workflows"`
	// Goals are the exploratory runs, empty for 'af test'. One document shape
	// rather than two, because everything around the planner is identical.
	Goals    []goalDoc    `json:"goals,omitempty"`
	Personas []personaDoc `json:"personas"`
	AF       string       `json:"af,omitempty"`
	WorkDir  string       `json:"work_dir,omitempty"`
	Attempts int          `json:"attempts,omitempty"`
	Headless bool         `json:"headless"`
}

type workflowDoc struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Persona     string   `json:"persona,omitempty"`
	Expect      []string `json:"expect"`
	StartPath   string   `json:"startPath,omitempty"`
}

type personaDoc struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Phone    string `json:"phone,omitempty"`
	Password string `json:"password,omitempty"`
	Role     string `json:"role,omitempty"`
	Login    string `json:"login"`
	// TOTPSecret is the base32 secret the adapter enrolled, present when the
	// persona has a second factor. The runner holds it so that it can
	// complete a challenge, which is what the manifest's `mfa` field promises.
	TOTPSecret string `json:"totpSecret,omitempty"`
}

// Test runs the manifest's workflows against the running environment.
func (o *Orchestrator) Test(ctx context.Context, opts TestOptions) (*TestReport, error) {
	status, err := o.Status(ctx)
	if err != nil {
		return nil, err
	}
	if status.URL == "" {
		return nil, aferrors.Coded(aferrors.AFAGT001,
			"detail", "nothing is running for this branch; bring it up with 'af up' first")
	}

	workflows := o.workflowDocs(opts.Only)
	if len(workflows) == 0 {
		return nil, aferrors.Coded(aferrors.AFAGT001,
			"detail", "the manifest declares no workflows to run")
	}

	// The personas have to exist before the browser opens. Until this call
	// was here, the runner was handed a name, an address and a derived
	// password for an account nobody had created, the application refused the
	// sign in, and settle() reported it as the application refusing a
	// correct password. Every workflow failed, and it failed with a finding
	// against the application rather than against the environment, which is
	// the most expensive kind of wrong answer this product can give.
	//
	// Idempotent, so a persona already provisioned into the golden or by an
	// earlier run is reconciled rather than duplicated.
	provisioned, err := o.ProvisionPersonas(ctx)
	if err != nil {
		return nil, err
	}

	runner, err := o.findRunner(opts.RunnerPath)
	if err != nil {
		return nil, err
	}
	artifacts := filepath.Join(o.opts.Root, StateDir, "artifacts", o.envID)
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFAGT001, "detail", err.Error())
	}

	// The run is reported from here, and this session is why the reporting
	// exists at all. agent.started, agent.finished and agent.verdict were three
	// of the event types the control plane sink translates and nothing emitted,
	// because af test runs in its own process and opened no session, so there
	// was no bus to emit onto. The consequence was two empty tables: the
	// console's runs list and every verdict under it had no writer in the whole
	// repository except the test harness and the staging seeder.
	//
	// Opened before the runner starts rather than after it finishes, so that a
	// run which is killed part way through has still been announced. A run
	// nobody was told about is indistinguishable from a run that never
	// happened, and those call for opposite responses.
	rs := o.openReporting(ctx)
	defer closeSession(rs)
	runStartedAt := o.opts.Clock.Now()
	id := runID(runStartedAt, o.envID)
	o.reportRunStarted(rs, id, "workflows", runStartedAt, len(workflows))

	report, err := o.driveRunner(ctx, runnerJob{
		Runner: runner, BaseURL: status.URL, Artifacts: artifacts,
		Workflows: workflows, Personas: o.personaDocs(provisioned),
		WorkDir: o.opts.Root, Attempts: opts.Attempts, Headless: !opts.Headed,
	})
	if err != nil {
		// Failed, not complete. The runner could not be driven, so nothing was
		// learned about the application, and a run recorded as complete with no
		// verdicts under it says the opposite.
		o.reportRunFinished(rs, id, runStartedAt, nil, "failed")
		return nil, err
	}

	// Asked after the workflows, of the rows they left behind. This is the
	// only part of a run that looks at the data rather than at the screen, and
	// until it was here the manifest's invariants were parsed, validated,
	// shown in `af explain` and never executed, so a green run said nothing
	// whatsoever about whether they held.
	//
	// A failure to run them does not fail the run. The workflows have already
	// produced a real result and discarding it because a follow up check could
	// not be made would throw away the more expensive answer of the two; the
	// invariants that could not be asked are reported as blocked instead.
	invs, invErr := o.RunInvariants(ctx)
	if invErr != nil {
		for _, inv := range o.opts.Manifest.Invariants {
			invs = append(invs, InvariantResult{
				Name:        inv.Name,
				Description: inv.Description,
				Error:       o.opts.Redactor.String(invErr.Error()),
			})
		}
	}
	report.Invariants = invs

	// Last, because it reads the log of what the workflows made the
	// application do. See markSynthesized: this is the only place the fact
	// that a response was invented can reach a verdict.
	o.markSynthesized(ctx, report)
	// Only publish the answer the caller receives. The runner's pass can
	// become unverified after the proxy log is read, and a completed run must
	// not be announced while its database checks are still running.
	o.reportVerdicts(rs, id, report.Results)
	o.reportRunFinished(rs, id, runStartedAt, report, "complete")
	return report, nil
}

// markSynthesized downgrades a workflow that touched a response a model
// invented.
//
// THE PROMISE THIS KEEPS, which was made in five places and kept in none. The
// proxy's synth mode asks a model to invent a response for a host with no
// sandbox and no fixture, and every description of it says the same thing: a
// workflow that touched one reports unverified rather than passed, because
// what it saw came from a model rather than from the thing under test. The
// sidecar has always recorded `synthesized` on the decision and set an
// `X-Antifailure-Synthesized` response header. Nothing on this side of the
// boundary had a field for it, so the decode dropped it, and the runner's
// mapping from `synthesized-response` to unverified sat there with its only
// producer firing on a completely different condition, a page nobody could
// read. A green run over an invented Stripe charge reported PASSED.
//
// IT HAS TO BE HERE RATHER THAN IN THE RUNNER. The runner drives a browser. A
// synthesized call is made by the APPLICATION, server side, and never appears
// in anything the browser can see. Only the proxy knows, and only the engine
// reads the proxy. That is why the runner emits a window instead of a verdict
// for this case.
//
// Attribution is by time window, which is exact enough because the runner runs
// workflows one after another and honest about what it cannot do: a request
// the application makes AFTER the workflow that triggered it has finished,
// from a background job say, lands on whatever workflow was running then or on
// none. That is recorded rather than hidden, and a decision inside no window
// is reported as a note on the run instead of being attributed to a workflow
// that did not cause it.
//
// A log that cannot be read changes nothing. The workflows have already
// produced a real result, and discarding it because a follow up read failed
// would throw away the more expensive answer of the two.
func (o *Orchestrator) markSynthesized(ctx context.Context, report *TestReport) {
	decisions, err := o.Decisions(ctx, 2000)
	if err != nil || len(decisions) == 0 {
		return
	}
	attributeSynthesized(report, decisions)
}

// attributeSynthesized is markSynthesized without the environment, so the
// decision it makes can be tested against a log rather than against a running
// Docker daemon. The reading of the log is the part that needs one; which
// workflow owns which invented response is arithmetic on timestamps.
func attributeSynthesized(report *TestReport, decisions []local.Decision) {
	var synthesized []local.Decision
	for _, d := range decisions {
		if d.Synthesized {
			synthesized = append(synthesized, d)
		}
	}
	if len(synthesized) == 0 {
		return
	}

	attributed := make([]bool, len(synthesized))
	for i := range report.Results {
		r := &report.Results[i]
		from, to, ok := window(r.StartedAt, r.FinishedAt)
		if !ok {
			continue
		}
		var hosts []string
		seen := map[string]bool{}
		for j, d := range synthesized {
			at := d.At
			if at.IsZero() {
				parsed, pErr := time.Parse(time.RFC3339Nano, d.AtRaw)
				if pErr != nil {
					continue
				}
				at = parsed
			}
			if at.Before(from) || at.After(to) {
				continue
			}
			attributed[j] = true
			if !seen[d.Host] {
				seen[d.Host] = true
				hosts = append(hosts, d.Host)
			}
		}
		if len(hosts) == 0 {
			continue
		}
		sort.Strings(hosts)
		// Only a pass is downgraded. A failure is a real finding about the
		// application and stays one: the application did the wrong thing with
		// an invented answer, and calling that unverified would hide a defect
		// behind our own escape hatch. Blocked and unverified are already not
		// passes and are left alone.
		if r.Outcome.Verdict != "pass" {
			continue
		}
		report.Passed--
		report.Unverified++
		r.Outcome.Verdict = "unverified"
		r.Outcome.Cause = "synthesized-response"
		r.Outcome.Detail = fmt.Sprintf(
			"This workflow passed, and it touched a response a model invented rather than one "+
				"%s returned, so nothing it showed is evidence either way. Give %s a sandbox "+
				"credential or a mock fixture and run it again.",
			strings.Join(hosts, " and "), pick(len(hosts) == 1, "that host", "those hosts"))
		r.Outcome.Reproduction = nil
	}

	// A synthesized call inside no workflow's window is still worth saying.
	// Attributing it to a workflow that did not cause it would be worse than
	// not attributing it, and saying nothing would put the run back where it
	// started.
	stray := map[string]bool{}
	for j, d := range synthesized {
		if !attributed[j] {
			stray[d.Host] = true
		}
	}
	if len(stray) > 0 {
		hosts := make([]string, 0, len(stray))
		for h := range stray {
			hosts = append(hosts, h)
		}
		sort.Strings(hosts)
		report.Notes = append(report.Notes, fmt.Sprintf(
			"a model invented %d responses outside any workflow's run, for %s. They belong to "+
				"no verdict here; see 'af net log'.",
			countOutside(synthesized, attributed), strings.Join(hosts, ", ")))
	}
}

// countOutside counts the synthesized decisions no workflow claimed.
func countOutside(all []local.Decision, attributed []bool) int {
	n := 0
	for i := range all {
		if !attributed[i] {
			n++
		}
	}
	return n
}

// window parses a workflow's start and end.
//
// A workflow with no window is skipped rather than given the whole run. The
// timestamps come from the runner, and a runner from before they existed, or
// one somebody is driving by hand, sends none: attributing every synthesized
// call in the log to every workflow would be a confident wrong answer, which
// is the failure this whole file is written against.
func window(startedAt, finishedAt string) (time.Time, time.Time, bool) {
	from, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	to, err := time.Parse(time.RFC3339Nano, finishedAt)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

// runnerJob is one invocation of the agent runner.
//
// A struct rather than a growing parameter list because there are two callers
// now: af test drives the environment's own services, and the rolling deploy
// check drives a second environment running the PREVIOUS release, with that
// release's own workflows and personas rather than this one's.
type runnerJob struct {
	Runner    string
	BaseURL   string
	Artifacts string
	WorkDir   string
	Workflows []workflowDoc
	Personas  []personaDoc
	Attempts  int
	Headless  bool
}

// driveRunner writes the job document, runs the runner, and reads its verdict.
//
// A non zero exit with a readable report is a test failure, which the caller
// decides about. Only an unreadable one is an error here, and it is reported
// as AF-AGT-003 so that the runner's own failure is never counted against the
// application.
func (o *Orchestrator) driveRunner(ctx context.Context, job runnerJob) (*TestReport, error) {
	self, err := os.Executable()
	if err != nil {
		// Without the engine's own path the runner cannot read the inbox, so
		// a workflow waiting on a message is blocked rather than failed. That
		// is the right answer and it is said out loud rather than guessed at.
		self = ""
	}

	started := o.opts.Clock.Now()
	// Through invokeRunner rather than starting the subprocess here, so there
	// is still one place that decides how the runner is started and what a
	// runner that writes nothing means. This landed as a second copy of that
	// code, which is the drift its comment was written to prevent.
	stdout, err := o.invokeRunner(ctx, job.Runner, jobDocument{
		BaseURL: job.BaseURL, Artifacts: job.Artifacts,
		Workflows: job.Workflows, Personas: job.Personas,
		AF: self, WorkDir: job.WorkDir,
		Attempts: job.Attempts, Headless: job.Headless,
	})
	if err != nil {
		return nil, err
	}

	var report TestReport
	if err := json.Unmarshal(stdout, &report); err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFAGT003,
			"detail", "the runner's output could not be read: "+err.Error())
	}
	report.Duration = o.opts.Clock.Since(started)
	return &report, nil
}

// findRunner locates the runner's entry point.
//
// Looked for inside the checkout first, so a checkout works with no
// installation, then where an install put one, then beside the binary, so an
// installed engine finds an installed runner. A clear refusal beats a
// mysterious exec failure.
//
// The candidate list is shared with `af runner install`, which is the whole
// point of it living in runnerpath. This function used to keep its own copy:
// o.opts.Root and one level above it, and nothing else. o.opts.Root is the
// directory holding the manifest, so a project one directory further down than
// that never reached the runner at the top of its own checkout. Every nightly
// leg that ran an example under examples/ failed here, naming four paths and
// not the one the runner was in, and so did any customer whose manifest is not
// at the top of their repository.
func (o *Orchestrator) findRunner(override string) (string, error) {
	if override != "" {
		if _, err := os.Stat(override); err == nil {
			return override, nil
		}
		return "", aferrors.Coded(aferrors.AFAGT004, "detail", "looked in "+override)
	}
	dirs := runnerpath.ToRun(o.opts.Root)
	candidates := make([]string, 0, len(dirs))
	for _, d := range dirs {
		candidates = append(candidates, filepath.Join(d, "src", "main.ts"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", aferrors.Coded(aferrors.AFAGT004,
		"detail", "looked in "+strings.Join(candidates, ", "))
}

func (o *Orchestrator) workflowDocs(only []string) []workflowDoc {
	wanted := map[string]bool{}
	for _, n := range only {
		wanted[n] = true
	}
	var out []workflowDoc
	for _, w := range o.opts.Manifest.Workflows {
		if len(wanted) > 0 && !wanted[w.Name] {
			continue
		}
		out = append(out, workflowDoc{
			Name: w.Name, Description: w.Description, Persona: w.Persona,
			Expect: w.Expect, StartPath: w.StartPath,
		})
	}
	return out
}

func (o *Orchestrator) personaDocs(provisioned *personas.Result) []personaDoc {
	var out []personaDoc
	for _, p := range o.opts.Manifest.Personas {
		login := string(p.Login)
		if login == "" {
			login = string(schema.LoginPassword)
		}
		doc := personaDoc{Name: p.Name, Email: p.Email, Phone: p.Phone, Role: p.Role, Login: login}

		// Taken from what provisioning actually created, rather than derived
		// again here. Two derivations that agree today are two derivations
		// that can disagree tomorrow, and the symptom would be a sign in
		// refused for a password that is correct everywhere except in the one
		// place it is typed.
		if provisioned != nil {
			if account, ok := provisioned.Account(p.Name); ok {
				doc.Email = account.Email
				doc.Phone = account.Phone
				doc.Password = account.Password.Reveal()
				doc.TOTPSecret = account.TOTPSecret.Reveal()
				out = append(out, doc)
				continue
			}
		}
		out = append(out, doc)
	}
	return out
}

// LoadOptions configure a load run.
//
// Duration and Scale are what the CALLER asked for, and zero means the caller
// did not ask. That distinction is the whole point of the two Default fields
// beside them, and it exists because it was missing: a cobra flag holds its
// default value whether or not anybody typed it, so `af load run` handed 60s
// and scale 1.0 down here on every invocation and the manifest's own
// load.scale and load.duration were unreachable. A repository whose manifest
// said `scale: 0.05` because it was pointing this at something fragile got
// production's full arrival rate, while `af explain` read the 5 percent back
// correctly. Only the command knows whether a value was typed; only these
// fields let it say so.
type LoadOptions struct {
	// Duration and Scale override the manifest. Zero means the caller did not
	// ask, and the manifest decides.
	Duration time.Duration
	Scale    float64
	// DefaultDuration and DefaultScale apply when neither the caller nor the
	// manifest named one. They are the command's own defaults, carried here
	// rather than baked into a flag, so that an untyped flag cannot be
	// mistaken for a choice.
	DefaultDuration time.Duration
	DefaultScale    float64
	// Ceiling caps the resolved values at the two Default fields, for a
	// caller whose defaults are a promise rather than a preference.
	// `af load smoke` promises a short burst; a manifest asking for five
	// minutes at production's rate must not silently turn one into a full
	// run. It only ever lowers, so it cannot cause the defect above.
	Ceiling bool

	Seed     int64
	Progress func(load.Progress)
}

// ResolveLoadRate settles how long to send for and at what multiple of
// production's rate.
//
// Three sources in precedence order, and the order is the fix: what the caller
// explicitly asked for, then what the manifest configured, then the caller's
// own fallback. A caller that passes its fallback in the first slot, which is
// what a cobra flag default does, makes the middle one unreachable.
//
// Exported because the defect was a disagreement between two layers about
// exactly this, and neither could see the other. The command decides what the
// user typed; the engine decides what that means beside the manifest. A test
// that asserts one of those and not the seam between them stayed green through
// the whole thing.
func ResolveLoadRate(opts LoadOptions, cfg *schema.Load) (time.Duration, float64) {
	duration := opts.Duration
	if duration <= 0 && cfg != nil && cfg.Duration != "" {
		if d, parseErr := time.ParseDuration(cfg.Duration); parseErr == nil {
			duration = d
		}
	}
	if duration <= 0 {
		duration = opts.DefaultDuration
	}
	scale := opts.Scale
	if scale <= 0 && cfg != nil && cfg.Scale > 0 {
		scale = cfg.Scale
	}
	if scale <= 0 {
		scale = opts.DefaultScale
	}

	// The cap applies to the manifest and to the default, never to a typed
	// flag: somebody who types --duration 5m on a smoke has said what they
	// want, and refusing it would be a second surprise rather than a fix.
	if opts.Ceiling {
		if opts.Duration <= 0 && opts.DefaultDuration > 0 && duration > opts.DefaultDuration {
			duration = opts.DefaultDuration
		}
		if opts.Scale <= 0 && opts.DefaultScale > 0 && scale > opts.DefaultScale {
			scale = opts.DefaultScale
		}
	}
	return duration, scale
}

// Load sends traffic shaped like production's at the environment.
//
// The shape comes from the manifest's source when one is configured, and from
// a default otherwise. The default says so in the report, because a shape that
// is a guess and a shape that is production's should never be mistaken for
// each other.
func (o *Orchestrator) Load(ctx context.Context, opts LoadOptions) (*load.Result, []load.Route, error) {
	status, err := o.Status(ctx)
	if err != nil {
		return nil, nil, err
	}
	if status.URL == "" {
		return nil, nil, aferrors.Coded(aferrors.AFLOD010,
			"detail", "nothing is running for this branch; bring it up with 'af up' first")
	}

	shape, err := o.trafficShape()
	if err != nil {
		return nil, nil, err
	}
	cfg := o.opts.Manifest.Load
	var safe, unsafe []string
	if cfg != nil {
		safe, unsafe = cfg.SafeRoutes, cfg.UnsafeRoutes
	}
	if len(safe) == 0 {
		// Reads under the root, which is what a smoke test wants and is the
		// only thing that can be assumed safe without being told.
		safe = []string{"GET /**"}
	}
	sendable, refused := shape.Safe(safe, unsafe)
	if len(sendable.Routes) == 0 {
		return nil, refused, aferrors.Coded(aferrors.AFLOD010,
			"detail", "every route in the shape is unsafe to send; add safe_routes to the manifest")
	}

	duration, scale := ResolveLoadRate(opts, cfg)

	res, err := load.Run(ctx, load.Options{
		BaseURL: status.URL, Shape: sendable, Scale: scale, Duration: duration,
		Seed: opts.Seed, Clock: o.opts.Clock, Progress: opts.Progress,
	})
	return res, refused, err
}

// ScenarioOptions configure a scenario run.
type ScenarioOptions struct {
	// Only runs just the named scenarios. Empty runs all of them.
	Only []string
	Seed int64
	// Concurrency bounds requests in flight, as it does for the mix.
	Concurrency int
	Progress    func(load.Progress)
}

// Scenarios runs the declared journeys against the environment.
//
// Beside the mix rather than instead of it: a scenario says what order the
// requests come in and what must hold afterwards, and the mix says what the
// rest of production is doing at the same time. Both are sent by the same
// generator and both obey the same safe list.
func (o *Orchestrator) Scenarios(ctx context.Context, opts ScenarioOptions) ([]load.ScenarioResult, error) {
	status, err := o.Status(ctx)
	if err != nil {
		return nil, err
	}
	if status.URL == "" {
		return nil, aferrors.Coded(aferrors.AFLOD010,
			"detail", "nothing is running for this branch; bring it up with 'af up' first")
	}

	runs, err := o.scenarioRuns(opts.Only)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, aferrors.Coded(aferrors.AFLOD010,
			"detail", "no scenarios are declared; add load.scenarios to the manifest")
	}

	cfg := o.opts.Manifest.Load
	var safe, unsafe []string
	if cfg != nil {
		safe, unsafe = cfg.SafeRoutes, cfg.UnsafeRoutes
	}
	if len(safe) == 0 {
		// The same default the mix takes. Reads under the root are the only
		// thing that can be assumed safe without being told.
		safe = []string{"GET /**"}
	}

	return load.RunScenarios(ctx, load.ScenarioOptions{
		BaseURL: status.URL, Runs: runs,
		SafeRoutes: safe, UnsafeRoutes: unsafe,
		Seed: opts.Seed, Concurrency: opts.Concurrency,
		Clock: o.opts.Clock, Progress: opts.Progress,
	})
}

// scenarioRuns reads and validates every scenario the manifest declares.
//
// All of them are read before any of them runs, so a typo in the third
// document is a message rather than a run that sends two journeys and then
// stops halfway.
func (o *Orchestrator) scenarioRuns(only []string) ([]load.ScenarioRun, error) {
	cfg := o.opts.Manifest.Load
	if cfg == nil {
		return nil, nil
	}
	wanted := map[string]bool{}
	for _, name := range only {
		wanted[name] = true
	}

	var runs []load.ScenarioRun
	seen := map[string]string{}
	for _, entry := range cfg.Scenarios {
		full := filepath.Join(o.opts.Root, filepath.FromSlash(entry.Path))
		body, err := os.ReadFile(full)
		if err != nil {
			return nil, aferrors.Wrap(err, aferrors.AFLOD013, "path", entry.Path, "detail", err.Error())
		}
		sc, err := load.ParseScenario(body)
		if err != nil {
			return nil, aferrors.Wrap(err, aferrors.AFLOD013, "path", entry.Path, "detail", err.Error())
		}
		if where, dup := seen[sc.Name]; dup {
			return nil, aferrors.Coded(aferrors.AFLOD013, "path", entry.Path,
				"detail", fmt.Sprintf("the scenario name %q is already used by %s", sc.Name, where))
		}
		seen[sc.Name] = entry.Path
		if len(wanted) > 0 && !wanted[sc.Name] {
			continue
		}

		run := load.ScenarioRun{Scenario: sc, Sessions: entry.Sessions, Iterations: entry.Iterations}
		if entry.StartAfter != "" {
			d, err := time.ParseDuration(entry.StartAfter)
			if err != nil {
				return nil, aferrors.Wrap(err, aferrors.AFLOD013, "path", entry.Path,
					"detail", fmt.Sprintf("start_after %q is not a duration", entry.StartAfter))
			}
			run.StartAfter = d
		}
		runs = append(runs, run)
	}
	if len(wanted) > 0 && len(runs) == 0 {
		return nil, aferrors.Coded(aferrors.AFLOD010,
			"detail", "no scenario matched the names given")
	}
	return runs, nil
}

// Thresholds returns the limits a load run is judged against.
func (o *Orchestrator) Thresholds() (p95Increase, errorRate float64) {
	if o.opts.Manifest.Load == nil || o.opts.Manifest.Load.Thresholds == nil {
		return 0, 0
	}
	t := o.opts.Manifest.Load.Thresholds
	return t.P95Increase, t.ErrorRate
}

// trafficShape reads the mix from whatever source is configured.
func (o *Orchestrator) trafficShape() (load.Shape, error) {
	cfg := o.opts.Manifest.Load
	if cfg == nil || cfg.Source == "" || cfg.Source == schema.LoadNone {
		return load.DefaultShape(), nil
	}

	switch cfg.Source {
	case schema.LoadAccessLog, schema.LoadOTel:
	default:
		// Datadog and New Relic used to reach here. They were in the schema,
		// so a person could set them, and they were refused at run time, so
		// they could never work. They are gone from the schema now and this
		// arm catches a manifest that reached the engine without being
		// validated, naming what does work rather than what does not.
		return load.Shape{}, aferrors.Coded(aferrors.AFLOD012, "source", string(cfg.Source))
	}

	path := cfg.SourceConfig["path"]
	if path == "" {
		return load.Shape{}, aferrors.Coded(aferrors.AFLOD010,
			"detail", "the load source is "+string(cfg.Source)+" and no source_config.path is configured")
	}
	body, err := os.ReadFile(filepath.Join(o.opts.Root, filepath.FromSlash(path)))
	if err != nil {
		return load.Shape{}, aferrors.Wrap(err, aferrors.AFLOD010, "detail", err.Error())
	}

	if cfg.Source == schema.LoadOTel {
		read, err := load.FromOTLP(body)
		if err != nil {
			// The skip counts go in the message. An export full of client
			// spans and an export whose exporter wrote the old attribute
			// names both produce nothing, and "no requests were found" is a
			// much harder message to act on than the reason.
			return load.Shape{}, aferrors.Coded(aferrors.AFLOD010,
				"detail", err.Error()+" in "+path+describeSkipped(read.Skipped))
		}
		return read.Shape, nil
	}

	shape := load.FromAccessLog(strings.Split(string(body), "\n"))
	if len(shape.Routes) == 0 {
		return load.Shape{}, aferrors.Coded(aferrors.AFLOD010,
			"detail", "no requests could be read from "+path)
	}
	if shape.RequestsPerSecond == 0 {
		// No line in the log carried a readable timestamp, so the arrival
		// rate is a guess. It says so in the source, because presenting a
		// guessed rate as production's is how a load run reports a number
		// nobody measured.
		shape.RequestsPerSecond = 10
		shape.Source = "access_log, arrival rate assumed"
	}
	return shape, nil
}

// describeSkipped renders what a trace read passed over, worst first.
func describeSkipped(skipped map[string]int) string {
	if len(skipped) == 0 {
		return ""
	}
	type pair struct {
		reason string
		n      int
	}
	pairs := make([]pair, 0, len(skipped))
	for reason, n := range skipped {
		pairs = append(pairs, pair{reason, n})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].n != pairs[j].n {
			return pairs[i].n > pairs[j].n
		}
		return pairs[i].reason < pairs[j].reason
	})
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf("%d %s", p.n, p.reason))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// pick chooses between two words. Not plural, which is next door in oracle.go
// and prefixes the count, and a count in front of "that host" reads wrong.
func pick(one bool, a, b string) string {
	if one {
		return a
	}
	return b
}
