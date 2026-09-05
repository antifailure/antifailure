package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// The load profiles, which is the enum a caller chooses between.
//
// Three names for what the CLI spells as three subcommands, because they
// differ by which numbers are resolved and not by what they do: all three send
// the manifest's safe routes at the running environment and measure what came
// back. Two subcommands that differ by one default are one tool with an enum,
// and a model choosing between three near identical tools chooses wrong.
const (
	// profileSmoke is the cheap one and the default. Ten seconds at a tenth
	// of production's rate, capped so a manifest asking for five minutes
	// cannot turn a smoke into a full run.
	profileSmoke = "smoke"
	// profileMix is the full weighted mix at production's rate.
	profileMix = "mix"
	// profileScenarios walks the declared journeys in order.
	profileScenarios = "scenarios"
)

// The bounds the schema publishes and enforces.
//
// They are here rather than inline so that the description and the validator
// read from one number. Every one of them costs money or wall clock time when
// it is crossed, which is why the schema refuses an expensive mistake instead
// of leaving the runtime to discover it eight minutes in.
const (
	maxLoadSeconds     = 600
	maxLoadScale       = 10.0
	maxLoadConcurrency = 200
	maxNamedScenarios  = 20
	// maxRoutesReported and maxScenariosReported bound the two lists in a
	// load result that grow with the project rather than with the run.
	maxRoutesReported     = 30
	maxScenariosReported  = 20
	maxAssertionsReported = 20
)

// The smoke and mix defaults, which are the CLI's own.
//
// Copied deliberately rather than imported, because engine/internal/cli cannot
// be imported from here: it is the package that starts this server. They are
// asserted against the reference page in the tests, so the two cannot drift
// silently.
const (
	smokeDuration = 10 * time.Second
	smokeScale    = 0.1
	mixDuration   = 60 * time.Second
	mixScale      = 1.0
)

// loadRequest is one load run, already validated and bounded.
//
// Note what is not in it. There is no base URL, no branch, no safe route list
// and no threshold: the environment is the one this server's checkout has
// running, the safe routes come from the manifest, and the thresholds come
// from the manifest's policy block. A caller can say how hard to push and for
// how long, within bounds, and cannot say what may be pushed at.
type loadRequest struct {
	Profile     string
	Duration    time.Duration
	Scale       float64
	Scenarios   []string
	Concurrency int
	Seed        int64
}

// loadOutcome is what the generator measured.
//
// The mix and the scenarios are separate fields rather than one interface,
// because they answer different questions and a caller reading the result has
// to be able to tell which was run.
type loadOutcome struct {
	Mix     *load.Result
	Refused []load.Route
	Runs    []load.ScenarioResult
	// P95Increase and ErrorRate are the manifest's thresholds, carried out of
	// the engine so the result can report each measurement beside the limit it
	// was judged against rather than leaving a caller to guess.
	P95Increase float64
	ErrorRate   float64
}

// sendLoad runs one profile against the environment.
//
// A function value so the tool can be built against a fake in tests. The real
// one is the orchestrator, which is the same code path af load takes.
type sendLoad func(ctx context.Context, req loadRequest) (loadOutcome, error)

// newRunLoadTestTool builds run_load_test.
func newRunLoadTestTool(p *Project, eng *Engine, send sendLoad) *Tool {
	return &Tool{
		Name:  "run_load_test",
		Title: "Send production shaped traffic at the environment",
		Description: "Answer whether this branch still serves its traffic. It sends the " +
			"weighted mix of requests production actually receives, or the declared " +
			"journeys in order, at the environment already running for this branch, and " +
			"reports latency percentiles, error rate and which routes crossed the " +
			"thresholds in the manifest. Hammering one endpoint proves that endpoint is " +
			"fast, which nobody doubted; what breaks under real traffic is the mix. " +
			"It sends requests to the environment and nothing else: no route is sent " +
			"unless the manifest names it safe, so a POST that charges a card is refused " +
			"rather than exercised, and the refused list is reported. " +
			"Nothing reaches production and no production credential is used. " +
			"Bring an environment up first with start_environment; without one this " +
			"reports INCONCLUSIVE rather than a clean result. " +
			"This takes as long as its duration, so it returns a run_id immediately: " +
			"poll it with get_rehearsal_run.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id":      projectIDSchema(),
				"idempotency_key": idempotencyKeySchema(),
				"profile": {
					// Bounded as well as enumerated. The enum is what decides,
					// and the length cap is what stops a caller spending the
					// argument budget on a value that was never going to match.
					Type: "string", MinLength: 1, MaxLength: 16,
					Enum: []string{profileSmoke, profileMix, profileScenarios},
					Description: "Which shape of traffic to send. Defaults to " +
						"\"smoke\", a ten second burst at a tenth of production's rate, " +
						"which is the cheap check that the environment answers at all. " +
						"\"mix\" is the full weighted profile at production's rate, for " +
						"a real answer about latency, and costs a minute by default. " +
						"\"scenarios\" walks the ordered journeys the manifest declares, " +
						"such as open the billing page then submit then submit again, " +
						"and is the only profile that asserts anything.",
				},
				"duration_seconds": {
					Type: "integer", HasMin: true, Minimum: 1, HasMax: true, Maximum: maxLoadSeconds,
					Description: "Optional. How long to send for, in seconds, for the " +
						"smoke and mix profiles. Leave it out and the manifest's own " +
						"load.duration decides, falling back to 10 seconds for a smoke " +
						"and 60 for a mix. Sixty is enough to see a p95; a longer run " +
						"costs wall clock time and finds little a minute did not.",
				},
				"scale": {
					Type: "number", HasMin: true, Minimum: 0.01, HasMax: true, Maximum: maxLoadScale,
					Description: "Optional. Multiplier on production's arrival rate, for " +
						"the smoke and mix profiles. Leave it out and the manifest's own " +
						"load.scale decides, falling back to 0.1 for a smoke and 1.0 for " +
						"a mix. 1.0 means production's measured rate. Above 1.0 is a " +
						"deliberate overload and costs proportionally more.",
				},
				"scenarios": {
					Type: "array", MaxItems: maxNamedScenarios,
					Description: "Optional. Run only these declared scenarios, by the " +
						"name each one gives itself. For the scenarios profile only. " +
						"Leave it out to run every scenario the manifest declares.",
					Items: nameSchema("The scenario's declared name, such as \"checkout\"."),
				},
				"concurrency": {
					Type: "integer", HasMin: true, Minimum: 1, HasMax: true, Maximum: maxLoadConcurrency,
					Description: "Optional. Ceiling on requests in flight for the " +
						"scenarios profile. Defaults to 20, which is what af load " +
						"scenario uses. Raising it pushes harder on the environment " +
						"rather than sending more requests.",
				},
				"seed": {
					Type: "integer", HasMin: true, Minimum: 0, HasMax: true, Maximum: 2147483647,
					Description: "Optional. Makes two runs send the same sequence, so a " +
						"result can be reproduced. Defaults to 1, which is what the " +
						"command line uses, so a run here and a run there match.",
				},
				"hypothesis": {
					Type: "string", MaxLength: 2000,
					Description: "Optional. What you expect this traffic to show, in your " +
						"own words. Recorded with the run so the numbers can be read " +
						"against the expectation. It is never executed and never changes " +
						"what is measured or what is sent.",
				},
			},
		},
		Handler: func(_ context.Context, call *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			req, fault := readLoadRequest(args)
			if fault != nil {
				return nil, fault
			}
			hypothesis, _ := args["hypothesis"].(string)

			return eng.Submit(call, "run_load_test", args,
				func(ctx context.Context, runID string) (string, *ResultBody, *Fault) {
					return runLoadTest(ctx, p, eng, send, runID, req, hypothesis)
				})
		},
	}
}

// nameSchema is the declaration for a name that selects from the manifest.
//
// Bounded alphabet as well as bounded length, because these names are echoed
// back in a summary a model reads. A name is a label somebody wrote in
// antifailure.yaml or in a scenario file, so it is repository content, and the
// alphabet is what stops a "name" being a paragraph of instructions. A name
// outside it selects nothing and is refused here rather than being repeated.
func nameSchema(what string) *Schema {
	return &Schema{
		Type: "string", MinLength: 1, MaxLength: 128,
		Pattern:     `[A-Za-z0-9][A-Za-z0-9 _.:/-]{0,127}`,
		Description: what,
	}
}

// readLoadRequest turns validated arguments into a request.
//
// Zero for duration and scale means the caller did not ask, which is the
// distinction env.ResolveLoadRate is built around: a value the caller did not
// type must not shadow the manifest's own. This is the same defect the command
// line had, where a cobra flag holding its default made load.scale unreachable.
func readLoadRequest(args map[string]any) (loadRequest, *Fault) {
	req := loadRequest{Profile: profileSmoke, Seed: 1, Concurrency: 20}

	if raw, ok := args["profile"].(string); ok && raw != "" {
		req.Profile = raw
	}
	if raw, present := args["duration_seconds"]; present {
		n, err := toInt(raw)
		if err != nil {
			return req, fieldFault(FaultInvalidArgument, "duration_seconds",
				"This field must be a whole number of seconds.")
		}
		req.Duration = time.Duration(n) * time.Second
	}
	if raw, present := args["scale"]; present {
		f, err := toFloat(raw)
		if err != nil {
			return req, fieldFault(FaultInvalidArgument, "scale",
				"This field must be a number.")
		}
		req.Scale = f
	}
	if raw, present := args["concurrency"]; present {
		n, err := toInt(raw)
		if err != nil {
			return req, fieldFault(FaultInvalidArgument, "concurrency",
				"This field must be a whole number.")
		}
		req.Concurrency = n
	}
	if raw, present := args["seed"]; present {
		n, err := toInt(raw)
		if err != nil {
			return req, fieldFault(FaultInvalidArgument, "seed",
				"This field must be a whole number.")
		}
		req.Seed = int64(n)
	}
	names, fault := readNames(args, "scenarios", maxNamedScenarios)
	if fault != nil {
		return req, fault
	}
	req.Scenarios = names
	return req, nil
}

// readNames reads an array of manifest names.
//
// Schema validation has already bounded the count, the length and the alphabet
// of each element, so this only has to convert. It still checks the element
// type, because a validator and a reader that disagree about a shape is how a
// panic reaches a caller.
func readNames(args map[string]any, field string, max int) ([]string, *Fault) {
	raw, present := args[field]
	if !present {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fieldFault(FaultInvalidArgument, field, "This field must be an array.")
	}
	if len(list) > max {
		return nil, fieldFault(FaultArgumentTooLarge, field,
			"This field carries %d names, and this server accepts at most %d.", len(list), max)
	}
	out := make([]string, 0, len(list))
	for i, item := range list {
		name, ok := item.(string)
		if !ok {
			return nil, fieldFault(FaultInvalidArgument, fmt.Sprintf("%s[%d]", field, i),
				"This element must be a string.")
		}
		out = append(out, name)
	}
	return out, nil
}

// toFloat reads a JSON number that validation already bounded.
func toFloat(raw any) (float64, error) {
	type floatish interface{ Float64() (float64, error) }
	n, ok := raw.(floatish)
	if !ok {
		return 0, fmt.Errorf("not a number")
	}
	return n.Float64()
}

func runLoadTest(
	ctx context.Context, p *Project, eng *Engine, send sendLoad,
	runID string, req loadRequest, hypothesis string,
) (string, *ResultBody, *Fault) {
	if eng.Cancelled(ctx, runID) {
		return "", nil, faultf(FaultRunNotCancellable, "This run was cancelled before it started.")
	}
	eng.Phase(ctx, runID, "sending "+req.Profile+" traffic at the environment")

	out, err := send(ctx, req)
	if err != nil {
		// No traffic was measured, so this says nothing about how the branch
		// serves it. Reporting a pass because no threshold was crossed would
		// be reporting an experiment that did not happen as one that found
		// nothing, which is the failure this whole server refuses.
		return "", nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The traffic could not be sent, so this says nothing about how the " +
				"branch performs. Load is sent at an environment rather than creating " +
				"one, so the usual cause is that nothing is running for this branch: " +
				"bring one up with start_environment. The server log says which it was.",
			Retryable: true,
			wrapped:   err,
		}
	}

	eng.Phase(ctx, runID, "ranking what the traffic showed")

	findings := loadFindings(out, p.Gate)
	body := &ResultBody{
		Findings: boundFindings(findings),
		Metrics:  loadMetrics(out),
		Evidence: loadEvidence(req, out),
		Detail:   describeLoad(req, out),
	}
	native := loadVerdict(req, out, findings)
	body.Summary = loadSummary(req, out, findings, native, hypothesis)
	return native, body, nil
}

// loadFindings ranks what the run measured, using the project's own policy.
//
// Nothing here invents a threshold. Which routes breached comes from
// load.Result.Breaches, which the command line calls with the same two numbers
// out of the same manifest, and the level comes from policy.load_regression.
// This function only writes the sentence.
//
// It is a near copy of loadFinding in engine/internal/cli, and that is a debt
// rather than a design: engine/internal/cli imports this package to start the
// server, so the evaluator cannot be shared until it is lifted into
// engine/internal/gate the way the migration one already was. The DECISION is
// shared, because both sides call Breaches and both read the same policy level.
func loadFindings(out loadOutcome, p report.Policy) []report.Finding {
	if out.Mix == nil || p.LoadRegression == report.LevelIgnore {
		return nil
	}
	breaches := out.Mix.Breaches(out.P95Increase, out.ErrorRate)
	if len(breaches) == 0 {
		return nil
	}
	where := make([]string, 0, len(breaches))
	for _, b := range breaches {
		where = append(where, b.What)
	}
	return []report.Finding{{
		Rule: ruleLoadRegression, Level: p.LoadRegression, Count: len(breaches),
		Where: strings.Join(where, ", "),
		Title: fmt.Sprintf("Load crossed %s the manifest sets.",
			plural(len(breaches), "a threshold", "thresholds")),
		Detail: fmt.Sprintf("%d requests at %.0f a second, p95 %.0fms, %.1f%% failed.",
			out.Mix.Sent, out.Mix.Rate, out.Mix.Overall.P95Ms, out.Mix.ErrorRate*100),
		Fix: "Raise the threshold in load.thresholds, or fix the regression.",
	}}
}

// ruleLoadRegression is the manifest policy key this finding is ranked by.
//
// The same literal engine/internal/cli uses, because the rule name is what
// somebody greps for six months later and two spellings of one rule is two
// rules to anybody reading a report.
const ruleLoadRegression = "load_regression"

// loadVerdict maps the run onto the engine's own vocabulary.
//
// Two branches, and the split is deliberate. A scenario carries a verdict of
// its own from the shared vocabulary, so report.Run.Verdict is the right
// authority and applies exactly the ranking af ci applies, including its rule
// that a word this engine cannot read is blocked rather than a pass. A mix has
// no verdicts to rank, so it takes the same route rehearse_migration_safety
// takes: report.Run.Counts, which is the rule Verdict itself applies to
// findings. Calling Verdict for a mix would answer blocked, because a mix has
// no workflows by design.
func loadVerdict(req loadRequest, out loadOutcome, findings []report.Finding) string {
	if req.Profile == profileScenarios {
		run := report.Run{Findings: findings}
		for _, s := range out.Runs {
			run.Workflows = append(run.Workflows, report.Workflow{
				Name: s.Scenario, Verdict: s.Verdict,
			})
		}
		return run.Verdict()
	}
	if out.Mix == nil || out.Mix.Sent == 0 {
		// Nothing was sent. Every threshold is unbreached and none of them
		// was evaluated, so this is unverified rather than a clean run.
		return report.VerdictUnverified
	}
	if out.Mix.InertP95(out.P95Increase) {
		// A p95 threshold was in force and no route carried a baseline for it
		// to be measured against. The command line exits non zero on exactly
		// this, for the reason that matters here too: a check that ran nothing
		// and reported green is a check everybody believes is running.
		return report.VerdictUnverified
	}
	fail, warn := report.Run{Findings: findings}.Counts()
	switch {
	case fail > 0:
		return report.VerdictFail
	case warn > 0:
		return report.VerdictWarn
	default:
		return report.VerdictPass
	}
}

func loadMetrics(out loadOutcome) []Metric {
	var metrics []Metric
	if out.Mix != nil {
		errorLimit := out.ErrorRate
		m := []Metric{
			{Name: "requests_sent", Value: float64(out.Mix.Sent), Unit: "requests"},
			{Name: "achieved_rate", Value: out.Mix.Rate, Unit: "requests_per_second"},
			{Name: "target_rate", Value: out.Mix.TargetRate, Unit: "requests_per_second"},
			{Name: "p95_latency", Value: out.Mix.Overall.P95Ms, Unit: "ms"},
			{Name: "p99_latency", Value: out.Mix.Overall.P99Ms, Unit: "ms"},
		}
		if errorLimit > 0 {
			m = append(m, Metric{
				Name: "error_rate", Value: out.Mix.ErrorRate, Unit: "ratio",
				Threshold: &errorLimit, Breached: out.Mix.ErrorRate > errorLimit,
			})
		} else {
			m = append(m, Metric{Name: "error_rate", Value: out.Mix.ErrorRate, Unit: "ratio"})
		}
		metrics = append(metrics, m...)
	}
	// Reported always, including as zero, because the number of routes the
	// safe list would not send is the difference between a run that exercised
	// the application and one that exercised a fortieth of it, and the request
	// count cannot show it.
	metrics = append(metrics, Metric{
		Name: "routes_refused_as_unsafe", Value: float64(len(out.Refused)), Unit: "routes",
	})
	if len(out.Runs) > 0 {
		failed := 0
		for _, s := range out.Runs {
			if s.Verdict == report.VerdictFail {
				failed++
			}
		}
		zero := 0.0
		metrics = append(metrics,
			Metric{Name: "scenarios_run", Value: float64(len(out.Runs)), Unit: "scenarios"},
			Metric{
				Name: "scenarios_failed", Value: float64(failed), Unit: "scenarios",
				Threshold: &zero, Breached: failed > 0,
			})
	}
	return metrics
}

// loadEvidence points at where the full measurement lives, and never carries it.
func loadEvidence(req loadRequest, out loadOutcome) []Evidence {
	command := "af load smoke -o json"
	switch req.Profile {
	case profileMix:
		command = "af load run -o json"
	case profileScenarios:
		command = "af load scenario -o json"
	}
	evidence := []Evidence{{
		URI: "af://load/" + req.Profile, Kind: "command",
		Note: "Run " + command + " for the full per route measurement, including the " +
			"routes this result truncated.",
	}}
	if len(out.Refused) > 0 {
		evidence = append(evidence, Evidence{
			URI: "af://load/refused", Kind: "safe_routes",
			Note: fmt.Sprintf(
				"%d routes were not sent because nothing in the manifest's "+
					"load.safe_routes names them safe. They are listed in the detail.",
				len(out.Refused)),
		})
	}
	return evidence
}

// loadDoc is the load evidence a result carries.
type loadDoc struct {
	Profile string `json:"profile"`
	// Source says where the mix came from, so a reader can tell production's
	// shape from the default this falls back to. A guess and a measurement
	// must never be mistaken for each other.
	Source       string         `json:"source,omitempty"`
	RequestedFor string         `json:"requested_duration,omitempty"`
	Sent         int            `json:"requests_sent"`
	TargetRate   float64        `json:"target_rate,omitempty"`
	AchievedRate float64        `json:"achieved_rate"`
	ErrorRate    float64        `json:"error_rate"`
	Overall      *latencyDoc    `json:"overall_latency,omitempty"`
	Routes       []loadRouteDoc `json:"routes"`
	RoutesTotal  int            `json:"routes_total"`
	Truncated    bool           `json:"routes_truncated"`
	Refused      []string       `json:"refused_as_unsafe,omitempty"`
	RefusedTotal int            `json:"refused_total"`
	Scenarios    []scenarioDoc  `json:"scenarios,omitempty"`
	Thresholds   loadThresholds `json:"thresholds"`
	Errors       map[string]int `json:"errors_by_reason,omitempty"`
	Notes        []string       `json:"notes,omitempty"`
}

type loadThresholds struct {
	// P95Increase and ErrorRate come from the manifest's load.thresholds and
	// cannot be set from a call. Zero means the manifest set none, which is
	// stated rather than reported as a threshold of zero.
	P95Increase float64 `json:"p95_increase"`
	ErrorRate   float64 `json:"error_rate"`
	Note        string  `json:"note"`
}

type latencyDoc struct {
	P50Ms float64 `json:"p50_ms"`
	P90Ms float64 `json:"p90_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
	MaxMs float64 `json:"max_ms"`
}

type loadRouteDoc struct {
	Route   string      `json:"route"`
	Sent    int         `json:"sent"`
	Errors  int         `json:"errors"`
	Latency *latencyDoc `json:"latency,omitempty"`
	// BaselineP95Ms and P95Increase are absent when there was no baseline,
	// and HasBaseline says which. Nothing to compare against and no change
	// are different answers, and a zero would read as the second.
	BaselineP95Ms float64 `json:"baseline_p95_ms,omitempty"`
	P95Increase   float64 `json:"p95_increase,omitempty"`
	HasBaseline   bool    `json:"has_baseline"`
}

type scenarioDoc struct {
	Scenario    string         `json:"scenario"`
	Description string         `json:"description,omitempty"`
	Verdict     string         `json:"verdict"`
	Detail      string         `json:"detail,omitempty"`
	Sessions    int            `json:"sessions"`
	Iterations  int            `json:"iterations"`
	Sent        int            `json:"sent"`
	ScheduledMs float64        `json:"scheduled_ms"`
	DurationMs  float64        `json:"duration_ms"`
	Assertions  []assertionDoc `json:"assertions,omitempty"`
	Refused     []string       `json:"refused_as_unsafe,omitempty"`
}

type assertionDoc struct {
	Name      string   `json:"name"`
	Verdict   string   `json:"verdict"`
	Measure   string   `json:"measure,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	Threshold *float64 `json:"threshold,omitempty"`
	Observed  *float64 `json:"observed,omitempty"`
	Detail    string   `json:"detail,omitempty"`
}

// describeLoad renders the measurement, bounded and neutralised.
//
// Route strings, scenario names and scenario descriptions all originate in the
// repository or in a traffic export the repository points at, so none of them
// is engine prose. They are not identifiers either: a route contains a space
// and a slash by construction. So they are neutralised and clipped rather than
// checked against safeIdentifier, which would refuse every one of them, and
// the bound is what keeps a "route" from being a paragraph.
func describeLoad(req loadRequest, out loadOutcome) *loadDoc {
	doc := &loadDoc{
		Profile:      req.Profile,
		RefusedTotal: len(out.Refused),
		Thresholds: loadThresholds{
			P95Increase: out.P95Increase, ErrorRate: out.ErrorRate,
			Note: "These come from the manifest's load.thresholds and cannot be set " +
				"from a tool call. Zero means the manifest sets none, so nothing was " +
				"judged against it.",
		},
	}
	if req.Duration > 0 {
		doc.RequestedFor = req.Duration.String()
	}
	if out.Mix != nil {
		doc.Source = neutralize(out.Mix.Source, 120)
		doc.Sent = out.Mix.Sent
		doc.TargetRate = out.Mix.TargetRate
		doc.AchievedRate = out.Mix.Rate
		doc.ErrorRate = out.Mix.ErrorRate
		doc.Overall = latency(out.Mix.Overall)
		doc.Errors = boundedReasons(out.Mix.Errors)
		doc.RoutesTotal = len(out.Mix.Routes)

		routes := out.Mix.Routes
		if len(routes) > maxRoutesReported {
			routes = routes[:maxRoutesReported]
			doc.Truncated = true
			doc.Notes = append(doc.Notes, fmt.Sprintf(
				"%d routes were exercised and the first %d are shown. Read the rest "+
					"with af load run -o json.", doc.RoutesTotal, maxRoutesReported))
		}
		for _, r := range routes {
			l := latency(r.Latency)
			doc.Routes = append(doc.Routes, loadRouteDoc{
				Route: neutralize(r.Route, 200), Sent: r.Sent, Errors: r.Errors,
				Latency: l, BaselineP95Ms: r.BaselineP95Ms,
				P95Increase: r.P95Increase, HasBaseline: r.HasBaseline,
			})
		}
		if out.Mix.InertP95(out.P95Increase) {
			doc.Notes = append(doc.Notes,
				"load.thresholds sets p95_increase and no route carried a baseline to "+
					"compare against, so that threshold was in force and measured nothing.")
		}
		if out.Mix.Sent == 0 {
			doc.Notes = append(doc.Notes,
				"The run completed without sending a single request, so no measurement "+
					"here is evidence about the branch.")
		}
	}
	if doc.Routes == nil {
		doc.Routes = []loadRouteDoc{}
	}

	for i, r := range out.Refused {
		if i >= maxRoutesReported {
			doc.Notes = append(doc.Notes, fmt.Sprintf(
				"%d routes were refused as unsafe and the first %d are named.",
				len(out.Refused), maxRoutesReported))
			break
		}
		doc.Refused = append(doc.Refused, neutralize(r.String(), 200))
	}

	for i, s := range out.Runs {
		if i >= maxScenariosReported {
			doc.Notes = append(doc.Notes, fmt.Sprintf(
				"%d scenarios ran and the first %d are shown.",
				len(out.Runs), maxScenariosReported))
			break
		}
		doc.Scenarios = append(doc.Scenarios, describeScenario(s))
	}
	return doc
}

func describeScenario(s load.ScenarioResult) scenarioDoc {
	doc := scenarioDoc{
		Scenario: neutralize(s.Scenario, 128),
		// The verdict is one of the engine's own closed vocabulary, and a word
		// outside it is read as blocked rather than repeated, which is the
		// same rule report.read applies. A verdict is what a caller branches
		// on, so it must not be able to carry anything else.
		Verdict:     knownVerdict(s.Verdict),
		Description: neutralize(s.Description, 300),
		Detail:      neutralize(s.Detail, 400),
		Sessions:    s.Sessions, Iterations: s.Iterations, Sent: s.Sent,
		ScheduledMs: s.ScheduledMs, DurationMs: s.DurationMs,
	}
	for i, a := range s.Assertions {
		if i >= maxAssertionsReported {
			break
		}
		doc.Assertions = append(doc.Assertions, assertionDoc{
			Name: neutralize(a.Name, 128), Verdict: knownVerdict(a.Verdict),
			Measure: neutralize(a.Measure, 64), Scope: neutralize(a.Scope, 200),
			Threshold: a.Threshold, Observed: a.Observed,
			Detail: neutralize(a.Detail, 300),
		})
	}
	for i, r := range s.Refused {
		if i >= maxRoutesReported {
			break
		}
		doc.Refused = append(doc.Refused, neutralize(r, 200))
	}
	return doc
}

// knownVerdict repeats a verdict only when it is one this engine can read.
//
// Anything else becomes blocked, which is exactly what report.read does and is
// here for the same reason: an outcome we cannot read is a fact about us
// rather than about the change, and a verdict field a caller branches on must
// carry nothing but the closed set.
func knownVerdict(v string) string {
	if report.Known(v) {
		return v
	}
	return report.VerdictBlocked
}

func latency(l load.Latency) *latencyDoc {
	return &latencyDoc{
		P50Ms: l.P50Ms, P90Ms: l.P90Ms, P95Ms: l.P95Ms, P99Ms: l.P99Ms, MaxMs: l.MaxMs,
	}
}

// boundedReasons copies the error tally, bounding both the number of reasons
// and the length of each.
//
// The keys are how a response failed, which for a transport error includes
// text from the network stack and, for a status code, nothing surprising.
// Bounded and neutralised anyway, because a map from an untrusted run is a map
// a caller reads.
func boundedReasons(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	i := 0
	for reason, n := range in {
		if i >= 20 {
			break
		}
		out[neutralize(reason, 120)] = n
		i++
	}
	return out
}

func loadSummary(
	req loadRequest, out loadOutcome, findings []report.Finding,
	native, hypothesis string,
) string {
	var b strings.Builder

	switch {
	case req.Profile == profileScenarios:
		failed, blocked := 0, 0
		for _, s := range out.Runs {
			switch knownVerdict(s.Verdict) {
			case report.VerdictFail:
				failed++
			case report.VerdictBlocked, report.VerdictUnverified:
				blocked++
			}
		}
		fmt.Fprintf(&b, "Walked %d declared %s. %d failed and %d produced no verdict. ",
			len(out.Runs), plural(len(out.Runs), "journey", "journeys"), failed, blocked)
	case out.Mix == nil || out.Mix.Sent == 0:
		b.WriteString("No request was sent, so this says nothing about how the branch " +
			"serves its traffic. ")
	default:
		fmt.Fprintf(&b,
			"Sent %d requests at %.0f a second against a target of %.0f, p95 %.0fms, "+
				"p99 %.0fms, %.1f%% failed. ",
			out.Mix.Sent, out.Mix.Rate, out.Mix.TargetRate,
			out.Mix.Overall.P95Ms, out.Mix.Overall.P99Ms, out.Mix.ErrorRate*100)
		if out.Mix.Source != "" {
			fmt.Fprintf(&b, "The mix came from %s. ", neutralize(out.Mix.Source, 120))
		}
	}

	if len(out.Refused) > 0 {
		fmt.Fprintf(&b,
			"%d %s not sent because the manifest's load.safe_routes does not name "+
				"%s safe, so the application was exercised less than these numbers suggest. ",
			len(out.Refused), plural(len(out.Refused), "route was", "routes were"),
			plural(len(out.Refused), "it", "them"))
	}

	fail, warn := report.Run{Findings: findings}.Counts()
	switch {
	case native == report.VerdictUnverified:
		b.WriteString("Nothing was measured against a threshold, so this is not a pass. ")
	case fail > 0:
		fmt.Fprintf(&b, "%d %s stop a merge under this project's policy and %d %s reported only. ",
			fail, plural(fail, "finding", "findings"), warn, plural(warn, "is", "are"))
	case warn > 0:
		fmt.Fprintf(&b, "Nothing stops a merge. %d %s reported for attention. ",
			warn, plural(warn, "finding is", "findings are"))
	default:
		b.WriteString("Nothing the project's policy treats as a problem. ")
	}

	if hypothesis != "" {
		fmt.Fprintf(&b, "Your stated hypothesis, unevaluated: %q.", neutralize(hypothesis, 500))
	}
	return strings.TrimSpace(b.String())
}

// sendLoadThrough runs one profile through the orchestrator.
//
// The branch, the base URL, the safe route list and the thresholds are all
// decided here from the manifest and the checkout, and no argument in the tool
// schema reaches any of them. A caller says how hard and how long, inside the
// schema's bounds, and cannot say where the traffic goes or what may be sent.
func (f *orchestratorFactory) sendLoad(ctx context.Context, req loadRequest) (loadOutcome, error) {
	o, err := f.build()
	if err != nil {
		return loadOutcome{}, err
	}
	p95, errorRate := o.Thresholds()
	out := loadOutcome{P95Increase: p95, ErrorRate: errorRate}

	if req.Profile == profileScenarios {
		runs, err := o.Scenarios(ctx, env.ScenarioOptions{
			Only: req.Scenarios, Seed: req.Seed, Concurrency: req.Concurrency,
		})
		if err != nil {
			return loadOutcome{}, err
		}
		out.Runs = runs
		return out, nil
	}

	// Ceiling for the smoke, exactly as af load smoke sets it: the promise is
	// a short burst, and a manifest asking for five minutes at production's
	// rate must not silently turn one into a full run. It only ever lowers.
	opts := env.LoadOptions{
		Duration: req.Duration, Scale: req.Scale, Seed: req.Seed,
		DefaultDuration: mixDuration, DefaultScale: mixScale,
	}
	if req.Profile == profileSmoke {
		opts.DefaultDuration, opts.DefaultScale, opts.Ceiling = smokeDuration, smokeScale, true
	}
	res, refused, err := o.Load(ctx, opts)
	if err != nil {
		return loadOutcome{}, err
	}
	out.Mix, out.Refused = res, refused
	return out, nil
}
