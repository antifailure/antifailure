package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// The two ways an agent drives the application through a browser.
//
// They share a runner, a browser and an evidence directory, and they answer
// opposite questions: a workflow asserts what should happen, and an
// exploration goes looking for what nobody declared. One tool each, because a
// caller choosing between them is choosing between two questions rather than
// between two flags.

const (
	maxNamedWorkflows = 50
	maxNamedGoals     = 20
	// maxWorkflowsReported and maxExplorationsReported bound the two lists
	// that grow with the manifest rather than with the run.
	maxWorkflowsReported    = 40
	maxExplorationsReported = 20
	maxInvariantsReported   = 40
	// maxAttempts is the ceiling on retries. Two is the command line's
	// default and is what tells flaky from failed; five is already far past
	// the point where more attempts are evidence rather than persistence.
	maxAttempts = 5
)

// driveWorkflows runs the manifest's workflows through the runner.
//
// A function value so the tool can be built against a fake in tests. The real
// one is the orchestrator, which is the same code path af test takes.
type driveWorkflows func(ctx context.Context, only []string, attempts int) (*env.TestReport, error)

// driveExploration sends agents at the manifest's goals.
type driveExploration func(ctx context.Context, only []string, seed string) (*explore.Report, []string, error)

// newRunWorkflowsTool builds run_browser_workflows.
func newRunWorkflowsTool(p *Project, eng *Engine, drive driveWorkflows) *Tool {
	return &Tool{
		Name:  "run_browser_workflows",
		Title: "Drive the application the way a person does",
		Description: "Answer whether the application still does what it is supposed to. " +
			"Agents drive the running environment through a real browser, using the " +
			"accessibility tree the way a person uses the screen, and each declared " +
			"workflow returns a verdict with a video, a trace and steps to reproduce it. " +
			"After the workflows, the manifest's invariants are asked of the data the " +
			"run left behind, so an order that reached a success page and then has no " +
			"user is a failure rather than a pass. " +
			"Five verdicts, not two: blocked means a browser crashed or a page never " +
			"loaded, which is a fact about the environment and not evidence about the " +
			"application, and a run in which nothing reached a verdict is INCONCLUSIVE " +
			"rather than clean. " +
			"Bring an environment up first with start_environment; without one this " +
			"reports INCONCLUSIVE. Nothing touches production. " +
			"This takes minutes, so it returns a run_id immediately: poll it with " +
			"get_rehearsal_run.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id":      projectIDSchema(),
				"idempotency_key": idempotencyKeySchema(),
				"workflows": {
					Type: "array", MaxItems: maxNamedWorkflows,
					Description: "Optional. Run only these workflows, by the name the " +
						"manifest gives each one. Leave it out to run every declared " +
						"workflow, which is what a pull request check does. Naming one " +
						"is how a change to checkout is rechecked without paying for " +
						"the whole suite.",
					Items: nameSchema(
						"A workflow's declared name, such as \"returning-customer\"."),
				},
				"attempts": {
					Type: "integer", HasMin: true, Minimum: 1, HasMax: true, Maximum: maxAttempts,
					Description: "Optional. How many times a workflow is tried before " +
						"being called flaky or failed. Defaults to 2, which is what the " +
						"command line uses and what makes flaky a verdict at all: one " +
						"attempt cannot tell an intermittent failure from a real one. " +
						"Every extra attempt costs another browser run.",
				},
				"hypothesis": {
					Type: "string", MaxLength: 2000,
					Description: "Optional. What you expect these workflows to do, in your " +
						"own words. Recorded with the run so the verdicts can be read " +
						"against the expectation. It is never executed and never changes " +
						"what is driven or what is asserted.",
				},
			},
		},
		Handler: func(_ context.Context, call *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			only, fault := readNames(args, "workflows", maxNamedWorkflows)
			if fault != nil {
				return nil, fault
			}
			attempts := 2
			if raw, present := args["attempts"]; present {
				n, err := toInt(raw)
				if err != nil {
					return nil, fieldFault(FaultInvalidArgument, "attempts",
						"This field must be a whole number.")
				}
				attempts = n
			}
			hypothesis, _ := args["hypothesis"].(string)

			return eng.Submit(call, "run_browser_workflows", args,
				func(ctx context.Context, runID string) (string, *ResultBody, *Fault) {
					return runWorkflows(ctx, p, eng, drive, runID, only, attempts, hypothesis)
				})
		},
	}
}

func runWorkflows(
	ctx context.Context, p *Project, eng *Engine, drive driveWorkflows,
	runID string, only []string, attempts int, hypothesis string,
) (string, *ResultBody, *Fault) {
	if eng.Cancelled(ctx, runID) {
		return "", nil, faultf(FaultRunNotCancellable, "This run was cancelled before it started.")
	}
	eng.Phase(ctx, runID, "driving the workflows through a browser")

	rep, err := drive(ctx, only, attempts)
	if err != nil {
		// The browser could not be driven, so nothing was learned about the
		// application. That is not a pass with no findings, and reporting it
		// as one is the single most damaging answer this server could give.
		return "", nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The workflows could not be driven, so this says nothing about the " +
				"application. The usual causes are that nothing is running for this " +
				"branch, that the browser runner is not installed, or that the manifest " +
				"declares no workflows.",
			Retryable: true,
			wrapped:   err,
		}
	}

	eng.Phase(ctx, runID, "ranking the verdicts and the invariants")

	// Assembled as a report.Run so that the verdict comes from the same
	// function af ci calls. Its ranking is the authority on what a run of
	// workflows means, including its rule that a word this engine cannot read
	// is blocked rather than a pass, and a second opinion here would be a
	// second answer to one question.
	run := report.Run{Declared: len(p.Manifest.Workflows)}
	for _, w := range rep.Results {
		run.Workflows = append(run.Workflows, report.Workflow{
			Name: w.Workflow, Verdict: w.Outcome.Verdict, Detail: w.Outcome.Detail,
		})
	}
	for _, inv := range rep.Invariants {
		run.Invariants = append(run.Invariants, report.Invariant{
			Name: inv.Name, Description: inv.Description,
			Held: inv.Held, Error: inv.Error,
		})
	}
	var findings []report.Finding
	if f := unverifiedFinding(run, p.Gate); f != nil {
		findings = append(findings, *f)
	}
	run.Findings = findings

	body := &ResultBody{
		Findings: safeWorkflowFindings(findings),
		Metrics:  workflowMetrics(rep),
		Evidence: workflowEvidence(rep),
		Detail:   describeWorkflows(rep),
	}
	native := run.Verdict()
	// The summary says that nothing reached a verdict on the strength of the
	// run itself rather than on the verdict word, because the two are not the
	// same fact. policy.workflows_unverified decides whether that failure
	// stops a merge, so the same run reads FAIL under the default policy and
	// INCONCLUSIVE under an ignoring one, and in both cases the caller has to
	// be told that the application was never actually driven.
	body.Summary = workflowSummary(rep, run.NothingVerified(), hypothesis)
	return native, body, nil
}

// ruleWorkflowsUnverified is the manifest policy key this finding is ranked by.
//
// The same literal engine/internal/cli uses, because the rule name is what a
// person greps for and two spellings of one rule is two rules to a reader.
const ruleWorkflowsUnverified = "workflows_unverified"

// unverifiedFinding is the one finding a workflow run produces on its own.
//
// It fires when no workflow reached a verdict about the application, which is
// the case that used to exit zero and read as "tested, fine" over a run that
// tested nothing. Whether it stops a merge is policy.workflows_unverified,
// which comes from the manifest and cannot be set from a call. The condition
// is report.Run.NothingVerified, which is the shared one.
//
// A near copy of workflowsUnverifiedFinding in engine/internal/cli, for the
// same reason loadFindings is: that package imports this one to start the
// server, so the evaluator cannot be shared until it moves into
// engine/internal/gate. The CONDITION and the LEVEL are both shared already.
func unverifiedFinding(run report.Run, p report.Policy) *report.Finding {
	if p.WorkflowsUnverified == report.LevelIgnore || !run.NothingVerified() {
		return nil
	}
	title := "No workflow reached a verdict about the application."
	detail := "Every workflow was blocked or proved nothing either way, so this run " +
		"says nothing about whether the application works."
	fix := "Read the workflow rows for what stopped each one. If the project has no " +
		"workflows yet, set policy.workflows_unverified to warn so the choice is " +
		"recorded rather than assumed."
	if len(run.Workflows) == 0 {
		title = "No workflows ran, so nothing about the application was checked."
		if run.Declared > 0 {
			detail = fmt.Sprintf(
				"The manifest declares %s and none produced a result, so the run "+
					"stopped before the application was reached.",
				plural(run.Declared, "workflow", "workflows"))
			fix = "Read the server log for what stopped the run before the workflows. " +
				"An environment that did not come up and a runner that could not be " +
				"found both land here."
		} else {
			detail = "The manifest declares no workflows, so there was nothing to carry through."
			fix = "Add a workflow to the manifest, or set policy.workflows_unverified " +
				"to warn so the choice is recorded rather than assumed."
		}
	}
	return &report.Finding{
		Rule: ruleWorkflowsUnverified, Level: p.WorkflowsUnverified,
		Count: len(run.Workflows), Where: "the workflows",
		Title: title, Detail: detail, Fix: fix,
	}
}

// safeWorkflowFindings bounds findings this package wrote itself.
//
// Every field in them is a literal above, so there is nothing from the
// repository to withhold. It still goes through safeProse, which is the
// backstop that catches the day one of those literals becomes a template.
func safeWorkflowFindings(in []report.Finding) FindingPage {
	out := make([]report.Finding, 0, len(in))
	for _, f := range in {
		out = append(out, report.Finding{
			Rule: f.Rule, Level: f.Level, Count: f.Count,
			Title: safeProse(f.Title, 300), Detail: safeProse(f.Detail, maxDetailBytes),
			Fix: safeProse(f.Fix, 400), Where: safeProse(f.Where, 200),
		})
	}
	return boundFindings(out)
}

// workflowDoc is one workflow's outcome, with the browser's own prose bounded.
type workflowDoc struct {
	Workflow string `json:"workflow"`
	Verdict  string `json:"verdict"`
	Cause    string `json:"cause,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Steps    int    `json:"steps_taken"`
	// DurationMs is how long the runner spent on it.
	DurationMs int64 `json:"duration_ms"`
	// HasVideo and HasTrace say whether the evidence exists without naming a
	// path on the host. A caller that wants it runs af test and looks.
	HasVideo bool `json:"has_video"`
	HasTrace bool `json:"has_trace"`
	// RequestsNotMade is how many requests the page could not make, usually
	// because the egress policy refused them, and FirstNotMade is the first
	// of them. The CLI has printed this line under every workflow since the
	// network evidence existed; this result omitted it, so an agent reading
	// PASS here saw less than a person reading the same run at a terminal,
	// and a page that half loaded looked whole.
	RequestsNotMade int    `json:"requests_the_page_could_not_make"`
	FirstNotMade    string `json:"first_request_not_made,omitempty"`
}

// invariantDoc is one invariant's answer, WITHOUT the rows behind it.
//
// The rows are the whole reason this type exists. An invariant that does not
// hold returns the offending rows out of the environment's database, which is
// a branch of a masked copy of production. Masked is not public: it is data a
// person is trusted with because they already have the credentials, and it is
// not something to place in a model's context because a query happened to
// select it. The count says how many rows broke it, which is what somebody
// needs in order to go and look, and af invariants shows them.
type invariantDoc struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Held        bool   `json:"held"`
	// Violations is how many offending rows were returned, and RowsWithheld
	// is always true when there were any, stated rather than implied so that
	// a caller looking for them learns why they are absent.
	Violations   int    `json:"violating_rows,omitempty"`
	RowsWithheld bool   `json:"rows_withheld"`
	More         bool   `json:"more_rows_than_shown,omitempty"`
	Blocked      bool   `json:"blocked"`
	BlockedNote  string `json:"blocked_note,omitempty"`
}

type workflowsDoc struct {
	Results    []workflowDoc  `json:"results"`
	Total      int            `json:"results_total"`
	Truncated  bool           `json:"results_truncated"`
	Passed     int            `json:"passed"`
	Failed     int            `json:"failed"`
	Flaky      int            `json:"flaky"`
	Blocked    int            `json:"blocked"`
	Unverified int            `json:"unverified"`
	Invariants []invariantDoc `json:"invariants,omitempty"`
	Notes      []string       `json:"notes,omitempty"`
	Note       string         `json:"note"`
}

// describeWorkflows renders the run, withholding what came from the browser
// and from the database.
//
// A workflow's detail is written by the runner about a page the application
// served, so its words are chosen by the application under test. It survives
// neutralised and clipped, because a failure that will not say what happened
// is a failure nobody can act on, and the bound is what keeps a page from
// spending a caller's whole context.
// firstOf is the first request the page could not make, bounded and
// neutralised: the text is the browser's account of the application's own
// request, and the application is untrusted input.
func firstOf(failed []string) string {
	if len(failed) == 0 {
		return ""
	}
	return neutralize(failed[0], 300)
}

func describeWorkflows(rep *env.TestReport) *workflowsDoc {
	doc := &workflowsDoc{
		Total:  len(rep.Results),
		Passed: rep.Passed, Failed: rep.Failed, Flaky: rep.Flaky,
		Blocked: rep.Blocked, Unverified: rep.Unverified,
		Note: "Video and trace files stay on this machine and are reported as present " +
			"or absent rather than by path. Invariant rows are withheld: they come " +
			"out of a branch of a masked copy of production.",
	}
	results := rep.Results
	if len(results) > maxWorkflowsReported {
		results = results[:maxWorkflowsReported]
		doc.Truncated = true
		doc.Notes = append(doc.Notes, fmt.Sprintf(
			"%d workflows ran and the first %d are shown. Read the rest with af test.",
			len(rep.Results), maxWorkflowsReported))
	}
	for _, w := range results {
		doc.Results = append(doc.Results, workflowDoc{
			Workflow: neutralize(w.Workflow, 128),
			Verdict:  knownVerdict(w.Outcome.Verdict),
			Cause:    neutralize(w.Outcome.Cause, 128),
			Detail:   neutralize(w.Outcome.Detail, 400),
			Steps:    len(w.Steps), DurationMs: w.DurationMs,
			HasVideo: w.Evidence.Video != "", HasTrace: w.Evidence.Trace != "",
			RequestsNotMade: len(w.Evidence.Failed),
			FirstNotMade:    firstOf(w.Evidence.Failed),
		})
	}
	if doc.Results == nil {
		doc.Results = []workflowDoc{}
	}

	for i, inv := range rep.Invariants {
		if i >= maxInvariantsReported {
			doc.Notes = append(doc.Notes, fmt.Sprintf(
				"%d invariants were asked and the first %d are shown.",
				len(rep.Invariants), maxInvariantsReported))
			break
		}
		entry := invariantDoc{
			Name: neutralize(inv.Name, 128), Description: neutralize(inv.Description, 300),
			Held: inv.Held && inv.Error == "", More: inv.More,
			Blocked: inv.Error != "",
		}
		if inv.Error != "" {
			// The error is the database's or the engine's own words about a
			// query the manifest wrote. Not repeated, because it quotes SQL
			// from the repository; the fact that it could not be asked is
			// what a caller needs.
			entry.BlockedNote = "This invariant could not be asked, so it found nothing. " +
				"That is a fact about the environment and not about the data. " +
				"Read it with af invariants."
		}
		if !entry.Held && !entry.Blocked {
			entry.Violations = len(inv.Rows)
			entry.RowsWithheld = true
		}
		doc.Invariants = append(doc.Invariants, entry)
	}
	for i, n := range rep.Notes {
		if i >= 10 {
			break
		}
		doc.Notes = append(doc.Notes, neutralize(n, 300))
	}
	return doc
}

func workflowMetrics(rep *env.TestReport) []Metric {
	zero := 0.0
	return []Metric{
		{
			Name: "workflows_failed", Value: float64(rep.Failed), Unit: "workflows",
			Threshold: &zero, Breached: rep.Failed > 0,
		},
		{
			Name: "invariants_violated", Value: float64(rep.InvariantsViolated()),
			Unit: "invariants", Threshold: &zero, Breached: rep.InvariantsViolated() > 0,
		},
		{Name: "workflows_passed", Value: float64(rep.Passed), Unit: "workflows"},
		{Name: "workflows_flaky", Value: float64(rep.Flaky), Unit: "workflows"},
		// Blocked and unverified are statements about us rather than about the
		// application, and they are reported separately for that reason: a run
		// made only of them has tested nothing whatever its other counts say.
		{Name: "workflows_blocked", Value: float64(rep.Blocked), Unit: "workflows"},
		{Name: "workflows_unverified", Value: float64(rep.Unverified), Unit: "workflows"},
		{Name: "invariants_blocked", Value: float64(rep.InvariantsBlocked()), Unit: "invariants"},
	}
}

func workflowEvidence(rep *env.TestReport) []Evidence {
	out := []Evidence{{
		URI: "af://test", Kind: "command",
		Note: "Run af test for the full report, including the reproduction steps and " +
			"the console output this result does not carry.",
	}}
	videos := 0
	for _, w := range rep.Results {
		if w.Evidence.Video != "" {
			videos++
		}
	}
	if videos > 0 {
		out = append(out, Evidence{
			URI: "af://test#artifacts", Kind: "recording",
			Note: fmt.Sprintf(
				"%d %s recorded, with traces beside them, under the environment's "+
					"artifacts directory on this machine. Paths are not reported here.",
				videos, plural(videos, "workflow was", "workflows were")),
		})
	}
	if len(rep.Invariants) > 0 {
		out = append(out, Evidence{
			URI: "af://invariants", Kind: "command",
			Note: fmt.Sprintf(
				"%d %s asked of the data. Run af invariants to see the rows behind a "+
					"violation, which this result withholds.",
				len(rep.Invariants), plural(len(rep.Invariants), "invariant was", "invariants were")),
		})
	}
	return out
}

func workflowSummary(rep *env.TestReport, nothingVerified bool, hypothesis string) string {
	var b strings.Builder
	fmt.Fprintf(&b,
		"Drove %d %s: %d passed, %d failed, %d flaky, %d blocked, %d unverified. ",
		len(rep.Results), plural(len(rep.Results), "workflow", "workflows"),
		rep.Passed, rep.Failed, rep.Flaky, rep.Blocked, rep.Unverified)

	if rep.Blocked > 0 || rep.Unverified > 0 {
		b.WriteString("Blocked and unverified are facts about the environment rather " +
			"than about the application, so they are not counted against the change. ")
	}
	switch {
	case len(rep.Invariants) == 0:
		b.WriteString("The manifest asks no invariants of the data. ")
	case rep.InvariantsViolated() > 0:
		fmt.Fprintf(&b,
			"%d %s broken by the rows the run left behind, which is a failure the "+
				"screen was never going to show. ",
			rep.InvariantsViolated(),
			plural(rep.InvariantsViolated(), "invariant was", "invariants were"))
	case rep.InvariantsBlocked() > 0:
		fmt.Fprintf(&b, "%d %s asked, so the data is unchecked to that extent. ",
			rep.InvariantsBlocked(),
			plural(rep.InvariantsBlocked(), "invariant could not be", "invariants could not be"))
	default:
		fmt.Fprintf(&b, "All %d %s held. ",
			len(rep.Invariants), plural(len(rep.Invariants), "invariant", "invariants"))
	}

	if nothingVerified {
		b.WriteString("NOTHING REACHED A VERDICT ABOUT THE APPLICATION. Every workflow " +
			"was blocked or proved nothing either way, so this run says nothing about " +
			"whether the application works, whatever its verdict word. Whether that " +
			"stops a merge is policy.workflows_unverified in the manifest.")
	}
	if hypothesis != "" {
		fmt.Fprintf(&b, " Your stated hypothesis, unevaluated: %q.", neutralize(hypothesis, 500))
	}
	return strings.TrimSpace(b.String())
}

// newExploreTool builds explore_for_friction.
func newExploreTool(p *Project, eng *Engine, drive driveExploration) *Tool {
	return &Tool{
		Name:  "explore_for_friction",
		Title: "Send agents at a goal with no script",
		Description: "Answer the question a workflow cannot ask: nothing broke, so why " +
			"would somebody give up here. Agents are given a goal in words and no " +
			"script, read each page through the accessibility tree, choose where to go " +
			"next, and write down every place the application cost them effort: a " +
			"control that did nothing, a dead end, a loop back to a page they had left, " +
			"an unnamed control, a slow answer, and a goal never reached. " +
			"It cannot fail a build and it never produces a merge blocking finding, " +
			"because nobody declared what should happen on the pages it wanders onto: " +
			"every finding is an observation. " +
			"Every choice comes from a seed, so the same seed walks the same path and a " +
			"finding can be replayed. " +
			"It needs a running environment and the goals the manifest declares under " +
			"explore; without either it reports INCONCLUSIVE. Nothing touches production. " +
			"This takes minutes, so it returns a run_id immediately: poll it with " +
			"get_rehearsal_run.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id":      projectIDSchema(),
				"idempotency_key": idempotencyKeySchema(),
				"goals": {
					Type: "array", MaxItems: maxNamedGoals,
					Description: "Optional. Explore only these goals, by the name the " +
						"manifest gives each one. Leave it out to explore every declared " +
						"goal. The goals themselves live in antifailure.yaml and cannot " +
						"be written from here: this selects among them.",
					Items: nameSchema("A goal's declared name, such as \"find-the-refund-page\"."),
				},
				"seed": {
					Type: "string", MinLength: 1, MaxLength: 64,
					Pattern: `[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}`,
					Description: "Optional. Replay with this seed instead of the one each " +
						"goal declares, which is how a finding reported earlier is walked " +
						"again step for step. Every report names the seed that produced " +
						"it. Leave it out to use the manifest's own seeds.",
				},
			},
		},
		Handler: func(_ context.Context, call *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			only, fault := readNames(args, "goals", maxNamedGoals)
			if fault != nil {
				return nil, fault
			}
			seed, _ := args["seed"].(string)

			return eng.Submit(call, "explore_for_friction", args,
				func(ctx context.Context, runID string) (string, *ResultBody, *Fault) {
					return runExploration(ctx, eng, drive, runID, only, seed)
				})
		},
	}
}

func runExploration(
	ctx context.Context, eng *Engine, drive driveExploration,
	runID string, only []string, seed string,
) (string, *ResultBody, *Fault) {
	if eng.Cancelled(ctx, runID) {
		return "", nil, faultf(FaultRunNotCancellable, "This run was cancelled before it started.")
	}
	eng.Phase(ctx, runID, "sending agents at the declared goals")

	rep, declared, err := drive(ctx, only, seed)
	if err != nil {
		return "", nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The exploration could not be run, so nothing was observed. The usual " +
				"causes are that nothing is running for this branch, that the manifest " +
				"declares no goals under explore, or that the browser runner is not " +
				"installed.",
			Retryable: true,
			wrapped:   err,
		}
	}

	eng.Phase(ctx, runID, "collecting the observations")

	// The completeness rule is report.Exploration.Incomplete, which is the
	// same one the hosted check applies. A configured exploration whose goals
	// did not all produce a browser result is blocked, never clean: an
	// exploration that refused half the application must not read as a clean
	// bill of health.
	summary := &report.Exploration{Declared: declared, Results: rep.Explorations}

	body := &ResultBody{
		// No findings. An exploration produces observations and nothing it
		// finds is ranked by the manifest's policy, so putting them in the
		// findings list would make them look like things that stop a merge.
		// They are in the detail, where they read as what they are.
		Findings: boundFindings(nil),
		Metrics:  explorationMetrics(rep),
		Evidence: explorationEvidence(rep),
		Detail:   describeExploration(rep, declared),
	}
	native := report.VerdictPass
	if summary.Incomplete() {
		native = report.VerdictBlocked
	}
	body.Summary = explorationSummary(rep, declared, native)
	return native, body, nil
}

type explorationDoc struct {
	Declared     []string            `json:"declared_goals"`
	Explorations []oneExplorationDoc `json:"explorations"`
	Total        int                 `json:"explorations_total"`
	Truncated    bool                `json:"explorations_truncated"`
	Findings     int                 `json:"observations_total"`
	Notes        []string            `json:"notes,omitempty"`
	Note         string              `json:"note"`
}

type oneExplorationDoc struct {
	Name    string `json:"goal"`
	Verdict string `json:"verdict"`
	// Reached says whether the goal's own words ever appeared on a page.
	Reached      bool             `json:"goal_reached"`
	Seed         string           `json:"seed"`
	PagesVisited int              `json:"pages_visited"`
	Moves        int              `json:"moves"`
	DurationMs   int64            `json:"duration_ms"`
	Observations []observationDoc `json:"observations,omitempty"`
	// NotExplored names what the run refused or could not reach. An
	// exploration that covered half the application must never read as
	// having covered it, so this is carried rather than summarised away.
	NotExplored []string `json:"not_explored,omitempty"`
	HasTrace    bool     `json:"has_trace"`
}

type observationDoc struct {
	// Kind is one of the closed set the runner and this engine share. Anything
	// outside it is reported as unknown rather than repeated.
	Kind string `json:"kind"`
	// URL is a page in the environment on this machine. Bounded and
	// neutralised: the path half of it is chosen by the application.
	URL string `json:"url,omitempty"`
	// Control is the accessible name of an element, which is text the
	// application rendered.
	Control    string `json:"control,omitempty"`
	Step       int    `json:"step"`
	Confidence string `json:"confidence,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Fix        string `json:"fix,omitempty"`
	MeasuredMs int64  `json:"measured_ms,omitempty"`
}

// describeExploration renders the observations, bounded and neutralised.
//
// Everything an exploration reports is text the application rendered: a page
// title, the accessible name of a button, a URL the application redirected to.
// None of it is engine prose and all of it reaches a model, so every field is
// neutralised and clipped, and the kind, which is what a caller branches on,
// is checked against the closed set rather than repeated.
func describeExploration(rep *explore.Report, declared []string) *explorationDoc {
	doc := &explorationDoc{
		Declared: boundedNames(declared, maxNamedGoals),
		Total:    len(rep.Explorations),
		Findings: len(rep.Findings()),
		Note: "These are observations, not assertions. Nothing here stops a merge, " +
			"because nobody declared what should happen on these pages. Page text and " +
			"control names come from the application under test and are bounded here.",
	}
	explorations := rep.Explorations
	if len(explorations) > maxExplorationsReported {
		explorations = explorations[:maxExplorationsReported]
		doc.Truncated = true
		doc.Notes = append(doc.Notes, fmt.Sprintf(
			"%d goals were explored and the first %d are shown. Read the rest with af explore.",
			len(rep.Explorations), maxExplorationsReported))
	}
	for _, x := range explorations {
		entry := oneExplorationDoc{
			Name:    neutralize(x.Name, 128),
			Verdict: knownVerdict(x.Outcome.Verdict),
			Reached: x.Reached, Seed: neutralize(x.Seed, 64),
			PagesVisited: len(x.Visited), Moves: len(x.Journey),
			DurationMs: x.DurationMs, HasTrace: x.Evidence.Trace != "",
		}
		for i, f := range x.Findings {
			if i >= 20 {
				break
			}
			entry.Observations = append(entry.Observations, observationDoc{
				Kind: knownKind(f.Kind), URL: neutralize(f.URL, 300),
				Control: neutralize(f.Control, 128), Step: f.Step,
				Confidence: neutralize(f.Confidence, 16),
				Detail:     neutralize(f.Detail, 400), Fix: neutralize(f.Fix, 300),
				MeasuredMs: f.MeasuredMs,
			})
		}
		for i, m := range x.Missing {
			if i >= 10 {
				break
			}
			entry.NotExplored = append(entry.NotExplored, neutralize(m, 200))
		}
		doc.Explorations = append(doc.Explorations, entry)
	}
	if doc.Explorations == nil {
		doc.Explorations = []oneExplorationDoc{}
	}
	return doc
}

// knownKind repeats an observation kind only when it is one this engine
// declares, for the same reason knownVerdict does: it is what a caller
// branches on, so it must carry nothing but the closed set.
func knownKind(k explore.Kind) string {
	for _, known := range explore.AllKinds() {
		if k == known {
			return string(k)
		}
	}
	return "unknown"
}

// boundedNames neutralises and bounds a list of manifest names.
func boundedNames(in []string, max int) []string {
	out := make([]string, 0, len(in))
	for i, name := range in {
		if i >= max {
			break
		}
		out = append(out, neutralize(name, 128))
	}
	return out
}

func explorationMetrics(rep *explore.Report) []Metric {
	kinds := map[explore.Kind]int{}
	for _, f := range rep.Findings() {
		kinds[f.Kind]++
	}
	reached := 0
	for _, x := range rep.Explorations {
		if x.Reached {
			reached++
		}
	}
	return []Metric{
		{Name: "observations", Value: float64(len(rep.Findings())), Unit: "observations"},
		{Name: "goals_explored", Value: float64(len(rep.Explorations)), Unit: "goals"},
		{Name: "goals_reached", Value: float64(reached), Unit: "goals"},
		{Name: "controls_that_did_nothing", Value: float64(kinds[explore.KindNoEffect]), Unit: "observations"},
		{Name: "dead_ends", Value: float64(kinds[explore.KindDeadEnd]), Unit: "observations"},
		{Name: "unnamed_controls", Value: float64(kinds[explore.KindUnnamedControl]), Unit: "observations"},
		{Name: "slow_responses", Value: float64(kinds[explore.KindSlowResponse]), Unit: "observations"},
	}
}

func explorationEvidence(rep *explore.Report) []Evidence {
	out := []Evidence{{
		URI: "af://explore", Kind: "command",
		Note: "Run af explore for the full report, including the journey each agent " +
			"walked, which this result reports only as a count.",
	}}
	if len(rep.Explorations) > 0 {
		out = append(out, Evidence{
			URI: "af://explore#workflow", Kind: "command",
			Note: "Run 'af explore --emit-workflow checkout.yaml' to print the workflow " +
				"block that replays what was explored, which turns an observation into a " +
				"check that runs on every pull request.",
		})
	}
	return out
}

func explorationSummary(rep *explore.Report, declared []string, native string) string {
	var b strings.Builder
	findings := rep.Findings()
	reached := 0
	for _, x := range rep.Explorations {
		if x.Reached {
			reached++
		}
	}
	fmt.Fprintf(&b,
		"Explored %d of %d declared %s and reached the goal in %d of them, recording "+
			"%d %s. ",
		len(rep.Explorations), len(declared), plural(len(declared), "goal", "goals"),
		reached, len(findings), plural(len(findings), "observation", "observations"))

	kinds := map[explore.Kind]int{}
	for _, f := range findings {
		kinds[f.Kind]++
	}
	var parts []string
	for _, k := range explore.AllKinds() {
		if kinds[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", kinds[k], k.Title()))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, "By kind: %s. ", strings.Join(parts, ", "))
	}
	if native == report.VerdictBlocked {
		b.WriteString("Not every declared goal produced a browser result, so this " +
			"exploration is incomplete and is reported INCONCLUSIVE rather than clean. ")
	}
	b.WriteString("Nothing here stops a merge: an exploration observes and does not assert.")
	return strings.TrimSpace(b.String())
}

// driveWorkflows runs the workflows through the orchestrator.
//
// Headed is never set and there is no argument that could set it. A visible
// browser needs a display this server does not have, and a run that waits for
// one is a run that hangs rather than one that reports. RunnerPath is not set
// either, and that one is a safety control rather than a convenience: it names
// an executable to launch, so an argument reaching it would be a way for a
// call to run a program of its choosing on this machine.
func (f *orchestratorFactory) driveWorkflows(
	ctx context.Context, only []string, attempts int,
) (*env.TestReport, error) {
	o, err := f.build()
	if err != nil {
		return nil, err
	}
	return o.Test(ctx, env.TestOptions{Only: only, Attempts: attempts})
}

// driveExploration runs the exploration and reports which goals were declared.
//
// The declared list comes back beside the report because completeness is
// judged against it: results without the list cannot tell a goal that found
// nothing from a goal that never ran.
func (f *orchestratorFactory) driveExploration(
	ctx context.Context, only []string, seed string,
) (*explore.Report, []string, error) {
	o, err := f.build()
	if err != nil {
		return nil, nil, err
	}
	rep, err := o.Explore(ctx, env.ExploreOptions{Only: only, Seed: seed})
	if err != nil {
		return nil, nil, err
	}
	var declared []string
	for _, g := range o.Goals() {
		if len(only) > 0 && !contains(only, g.Name) {
			continue
		}
		declared = append(declared, g.Name)
	}
	return rep, declared, nil
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
