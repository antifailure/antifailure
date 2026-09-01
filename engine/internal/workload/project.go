package workload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// Projection turns what the engine measured into the flat rows a control plane
// stores. Every value here is derived from the engine's own result, which the
// document also carries verbatim under native, so a projection that loses
// something can be caught by reading one document rather than by running the
// workload a second time.

// runObservedLoad sends the mix and judges it against the manifest's
// thresholds.
func runObservedLoad(ctx context.Context, opts Options, res *Result) {
	p := opts.Plan
	out, refused, err := opts.Runner.Load(ctx, env.LoadOptions{
		Duration: p.Duration, Scale: p.Scale, Seed: p.SeedNumber,
	})
	res.Measured.RefusedRoutes = refusedNames(refused)
	if out == nil {
		settle(res, opts, orFailure(err), "", "")
		return
	}
	nativeOf(res, out)
	projectMix(res, out)

	p95Increase, errorRate := opts.Runner.Thresholds()
	res.Thresholds = mixThresholds(out, p95Increase, errorRate)

	settle(res, opts, err, mixVerdict(out, res.Thresholds), mixDetail(out, res.Thresholds))
}

func projectMix(res *Result, out *load.Result) {
	failures := 0
	for _, n := range out.Errors {
		failures += n
	}
	m := &res.Measured
	m.Requests = intp(out.Sent)
	m.Failures = intp(failures)
	m.ErrorRate = floatp(out.ErrorRate)
	m.TargetRate = floatp(out.TargetRate)
	m.AchievedRate = floatp(out.Rate)
	m.DurationMs = floatp(float64(out.Duration.Microseconds()) / 1000)
	m.Source = out.Source
	if len(out.Errors) > 0 {
		m.Errors = out.Errors
	}
	setLatency(m, out.Overall)
	res.Routes = routeMetrics("", out.Routes)
}

// mixThresholds says what each manifest threshold measured, including the ones
// it could not measure.
//
// A route with no baseline gets an unverified row rather than no row at all.
// The engine's Breaches deliberately never breaches such a route, which is
// correct, but a console that saw only breaches would show a clean p95 check
// over routes nothing was ever compared against. An access log shape carries
// no per route timing at all, so that is every route for anybody whose source
// is an access log, and reporting it as passing would be the largest silent
// green in this product.
func mixThresholds(out *load.Result, p95Increase, errorRate float64) []ThresholdVerdict {
	verdicts := []ThresholdVerdict{{
		Name:      "error_rate",
		Measure:   "error_rate",
		Threshold: floatp(errorRate),
		Observed:  floatp(out.ErrorRate),
		Value:     passFail(out.Sent > 0 && out.ErrorRate <= errorRate),
		Position:  0,
	}}
	if out.Sent == 0 {
		verdicts[0].Value = VerdictUnverified
		verdicts[0].Observed = nil
		verdicts[0].Detail = "no requests were sent, so there is nothing to measure"
	}
	for i, r := range out.Routes {
		v := ThresholdVerdict{
			Name:      "p95_increase",
			Scope:     r.Route,
			Measure:   "p95_increase",
			Threshold: floatp(p95Increase),
			Position:  i + 1,
		}
		if !r.HasBaseline {
			v.Value = VerdictUnverified
			v.Detail = "the traffic shape carried no baseline for this route, so there is " +
				"nothing to compare against"
		} else {
			v.Observed = floatp(r.P95Increase)
			v.Value = passFail(r.P95Increase <= p95Increase)
		}
		verdicts = append(verdicts, v)
	}
	return verdicts
}

// mixVerdict is the run's one word answer.
//
// A run that sent nothing is unverified rather than passing, and that is the
// single most important line in this file. af test exits zero on unverified
// and blocked, and an entire nightly corpus in this repository was green
// having never once reached an agent. A load run whose every route was refused
// as unsafe sends nothing, finds nothing, and must not read as a clean run.
func mixVerdict(out *load.Result, verdicts []ThresholdVerdict) string {
	if out.Sent == 0 {
		return VerdictUnverified
	}
	for _, v := range verdicts {
		if v.Value == VerdictFail {
			return VerdictFail
		}
	}
	return VerdictPass
}

// mixDetail is the sentence somebody reads before they read anything else.
//
// It returned EMPTY whenever anything was sent, so every failing load run
// arrived carrying a `fail` verdict and nothing beside it. The reason lived
// only in the threshold rows, and a console that leads with the detail had
// nothing to lead with; the row that says the run failed and the rows that say
// why were in different places, and only one of them is on the first screen.
//
// scenarioDetail directly below has always named its failing scenario. The two
// sat beside each other doing different things for as long as both existed,
// which is how this survived: each reads correctly on its own.
func mixDetail(out *load.Result, verdicts []ThresholdVerdict) string {
	if out.Sent == 0 {
		return "no requests were sent, so nothing was measured"
	}
	var failed []ThresholdVerdict
	for _, v := range verdicts {
		if v.Value == VerdictFail {
			failed = append(failed, v)
		}
	}
	if len(failed) == 0 {
		return ""
	}
	first := breachSentence(failed[0])
	if len(failed) == 1 {
		return first
	}
	return fmt.Sprintf("%s, and %d more thresholds were breached", first, len(failed)-1)
}

// breachSentence says what one breached threshold measured, in its own units.
//
// The measure decides the wording rather than the name, because the name is the
// manifest's word and the measure is what the number means. An unrecognised
// measure gets a sentence that names it rather than a number nobody can read:
// the engine adds a measure by releasing, and a detail line that silently
// stopped explaining the newest one would be the worst kind of stale.
func breachSentence(v ThresholdVerdict) string {
	where := ""
	if v.Scope != "" {
		where = " on " + v.Scope
	}
	switch v.Measure {
	case "error_rate":
		return fmt.Sprintf("the error rate%s was %s against a threshold of %s",
			where, asPercent(v.Observed), asPercent(v.Threshold))
	case "p95_increase":
		return fmt.Sprintf("p95%s rose %s against a threshold of %s",
			where, asPercent(v.Observed), asPercent(v.Threshold))
	}
	return "the threshold " + v.Name + where + " was breached"
}

// asPercent renders a ratio the way the manifest declares it.
//
// Never a bare number. A threshold of 0.2 read out as "0.2" beside an observed
// "0.45" is two numbers a reader has to know the units of, and this is the line
// written for the reader who does not.
func asPercent(v *float64) string {
	if v == nil {
		return "an unmeasured amount"
	}
	return fmt.Sprintf("%.1f percent", *v*100)
}

// runScenarios runs the declared journeys.
func runScenarios(ctx context.Context, opts Options, res *Result) {
	p := opts.Plan
	out, err := opts.Runner.Scenarios(ctx, env.ScenarioOptions{
		Only: p.Select, Seed: p.SeedNumber, Concurrency: p.Concurrency,
	})
	if out == nil {
		settle(res, opts, orFailure(err), "", "")
		return
	}
	nativeOf(res, out)
	projectScenarios(res, out)
	settle(res, opts, err, scenarioVerdict(out), scenarioDetail(out))
}

func projectScenarios(res *Result, out []load.ScenarioResult) {
	m := &res.Measured
	sent, failures, sessions, iterations := 0, 0, 0, 0
	refused := map[string]bool{}
	position := 0
	for _, s := range out {
		sent += s.Sent
		sessions += s.Sessions
		iterations += s.Iterations
		for _, step := range s.Steps {
			failures += step.Errors
		}
		for _, r := range s.Refused {
			refused[r] = true
		}
		for _, r := range routeMetrics(s.Scenario, s.Steps) {
			r.Position = position
			position++
			res.Routes = append(res.Routes, r)
		}
		for i, a := range s.Assertions {
			res.Thresholds = append(res.Thresholds, ThresholdVerdict{
				Scenario:  s.Scenario,
				Name:      a.Name,
				Scope:     a.Scope,
				Measure:   a.Measure,
				Threshold: a.Threshold,
				Observed:  a.Observed,
				Value:     a.Verdict,
				Detail:    a.Detail,
				Position:  i,
			})
		}
	}
	m.Requests = intp(sent)
	m.Failures = intp(failures)
	m.Sessions = intp(sessions)
	m.Iterations = intp(iterations)
	if sent > 0 {
		m.ErrorRate = floatp(float64(failures) / float64(sent))
	} else {
		m.ErrorRate = floatp(0)
	}
	m.RefusedRoutes = sortedKeys(refused)

	// Run wide percentiles exist only when exactly one scenario ran, and the
	// reason is arithmetic rather than laziness: you cannot combine two p95s.
	// Two journeys sending different routes at different rates have two
	// distributions, and any single number over both is a number nobody can
	// interpret. Per scenario percentiles are in native and per route ones are
	// in routes, so nothing is lost by leaving these null.
	if len(out) == 1 {
		setLatency(m, out[0].Overall)
		m.ScheduledMs = floatp(out[0].ScheduledMs)
		m.DurationMs = floatp(out[0].DurationMs)
		return
	}
	longest := 0.0
	for _, s := range out {
		if s.DurationMs > longest {
			longest = s.DurationMs
		}
	}
	m.DurationMs = floatp(longest)
}

// scenarioVerdict is the worst answer any scenario gave.
//
// Worst rather than most common, and the order is the product's own: a run
// with one failing journey and nine passing ones is a failing run.
func scenarioVerdict(out []load.ScenarioResult) string {
	if len(out) == 0 {
		return VerdictUnverified
	}
	worst := VerdictPass
	for _, s := range out {
		worst = worseOf(worst, s.Verdict)
	}
	return worst
}

func scenarioDetail(out []load.ScenarioResult) string {
	if len(out) == 0 {
		return "no scenario ran, so nothing was measured"
	}
	for _, s := range out {
		if s.Verdict == VerdictFail || s.Verdict == VerdictBlocked {
			return named(s.Scenario, s.Detail)
		}
	}
	return ""
}

// named joins the thing that failed to the reason, and never produces the join
// on its own.
//
// `name + ": " + detail` with an empty detail renders as "checkout: ", which is
// WORSE than an empty string: a name, a colon and nothing reads as a sentence
// that was cut off, so a person goes looking for the missing half. An absence
// should look like an absence.
//
// Written as a guard rather than as an audit of every place an inner detail is
// set. Today every failing assertion sets one, so this is unreachable; the
// point is that it stays unreachable when somebody adds the next failure path
// and forgets, which is the version of this that actually ships.
func named(name, detail string) string {
	if strings.TrimSpace(detail) == "" {
		return name + " failed and reported no reason"
	}
	return name + ": " + detail
}

// runWorkflows drives the declared workflows through a browser.
func runWorkflows(ctx context.Context, opts Options, res *Result) {
	p := opts.Plan
	out, err := opts.Runner.Test(ctx, env.TestOptions{Only: p.Select, Attempts: p.Attempts})
	if out == nil {
		settle(res, opts, orFailure(err), "", "")
		return
	}
	nativeOf(res, out)
	projectWorkflows(res, opts, out)
	settle(res, opts, err, workflowVerdict(out), workflowDetail(out))
}

func projectWorkflows(res *Result, opts Options, out *env.TestReport) {
	m := &res.Measured
	total := out.Passed + out.Failed + out.Flaky + out.Blocked + out.Unverified
	steps := 0
	for _, r := range out.Results {
		steps += len(r.Steps)
	}
	m.Workflows = intp(total)
	m.WorkflowsPassed = intp(out.Passed)
	m.WorkflowsFailed = intp(out.Failed)
	m.WorkflowsFlaky = intp(out.Flaky)
	m.WorkflowsBlocked = intp(out.Blocked)
	m.WorkflowsUnverified = intp(out.Unverified)
	m.Steps = intp(steps)
	m.DurationMs = floatp(float64(out.Duration.Microseconds()) / 1000)

	// An invariant is a threshold in every sense a console cares about: it was
	// declared, it was measured, and it either held or it did not. Reporting
	// it anywhere else would mean a run that broke a data invariant and passed
	// every workflow rendered as a clean run.
	for i, inv := range out.Invariants {
		v := ThresholdVerdict{
			Name:     inv.Name,
			Measure:  "invariant",
			Value:    VerdictPass,
			Detail:   inv.Description,
			Position: i,
		}
		switch {
		case inv.Error != "":
			v.Value = VerdictUnverified
			v.Detail = "the invariant could not be evaluated: " + inv.Error
		case inv.Violated():
			v.Value = VerdictFail
			v.Detail = "the invariant does not hold"
		}
		res.Thresholds = append(res.Thresholds, v)
	}
	res.Evidence = workflowEvidence(opts.Root, out)
}

// workflowVerdict is the run's one word answer, worst first.
//
// A report with no workflows at all is blocked rather than passing. Zero of
// zero passing is the shape of a selection that matched nothing, and calling
// it a pass is how a typo in a workflow name becomes a green check forever.
func workflowVerdict(out *env.TestReport) string {
	total := out.Passed + out.Failed + out.Flaky + out.Blocked + out.Unverified
	if total == 0 {
		return VerdictBlocked
	}
	switch {
	case out.Failed > 0 || out.InvariantsViolated() > 0:
		return VerdictFail
	case out.Flaky > 0:
		return VerdictFlaky
	case out.Blocked > 0:
		return VerdictBlocked
	case out.Unverified > 0:
		return VerdictUnverified
	}
	return VerdictPass
}

func workflowDetail(out *env.TestReport) string {
	if out.Passed+out.Failed+out.Flaky+out.Blocked+out.Unverified == 0 {
		return "no workflow ran, so nothing was checked"
	}
	for _, r := range out.Results {
		if r.Outcome.Verdict == VerdictFail {
			return named(r.Workflow, r.Outcome.Detail)
		}
	}
	return ""
}

// runExploration walks the declared goals.
func runExploration(ctx context.Context, opts Options, res *Result) {
	p := opts.Plan
	out, err := opts.Runner.Explore(ctx, env.ExploreOptions{Only: p.Select, Seed: p.SeedText})
	if out == nil {
		settle(res, opts, orFailure(err), "", "")
		return
	}
	nativeOf(res, out)
	projectExploration(res, opts, out)
	settle(res, opts, err, explorationVerdict(out), explorationDetail(out))
}

func projectExploration(res *Result, opts Options, out *explore.Report) {
	m := &res.Measured
	reached, findings, steps := 0, 0, 0
	longest := 0.0
	for _, e := range out.Explorations {
		if e.Reached {
			reached++
		}
		findings += len(e.Findings)
		steps += len(e.Steps)
		if float64(e.DurationMs) > longest {
			longest = float64(e.DurationMs)
		}
	}
	m.Goals = intp(len(out.Explorations))
	m.GoalsReached = intp(reached)
	m.Findings = intp(findings)
	m.DurationMs = floatp(longest)

	for i, e := range out.Explorations {
		// Reachability is the only thing an exploration asserts, so it is the
		// only thing that belongs in a threshold row. A friction finding is a
		// defect to look at rather than a check that failed, and putting one
		// here would make a console show a red for a report that is working
		// exactly as intended.
		v := ThresholdVerdict{
			Name:     e.Name,
			Measure:  "goal_reached",
			Value:    VerdictPass,
			Detail:   e.Outcome.Detail,
			Position: i,
		}
		switch {
		case e.Outcome.Verdict == VerdictBlocked:
			v.Value = VerdictBlocked
		case !e.Reached:
			v.Value = VerdictUnverified
			if e.Outcome.Detail == "" {
				v.Detail = "the goal was not reached"
			}
		}
		res.Thresholds = append(res.Thresholds, v)
	}
	res.Evidence = explorationEvidence(opts.Root, out)
}

// explorationVerdict never returns fail.
//
// An exploration produces findings rather than a pass or a fail, which is why
// af explore cannot fail a build. A goal that was not reached is unverified: a
// wander that did not get there has not shown the application is broken, it
// has shown that a seeded wander did not find the way.
// explorationVerdict is the run's one word answer, and it is `pass` unless the
// exploration could not be carried out.
//
// A GOAL THAT WAS NOT REACHED IS NOT A FAILING RUN. It used to roll up to
// `unverified`, and workloadOutcome maps unverified to a NON-ZERO exit, so a
// hosted exploration of a page offering no control to press failed the job
// while behaving exactly as designed. docs/concepts/exploration promises the
// opposite in as many words: an exploration cannot fail your build.
//
// The reasoning that produced the old rollup is sound and is why the empty case
// below is untouched: `af test` exits zero on unverified, and that single fact
// is how a nightly corpus in this repository went green having never reached an
// agent, so a run that MEASURED NOTHING must not look like a run that FOUND
// NOTHING. An exploration that found a wall is not that case. It ran, it
// explored, and it established something: that the goal is unreachable from
// there. The unreached goal travels in the DETAIL, where it is a finding, and
// findings do not fail builds.
//
// The empty case is separable and stays. No explorations at all means nobody
// looked, which is `blocked`, and blocked keeps its non-zero exit. So the two
// halves of the original reasoning are both preserved: nothing ran is still
// loud, and looking and finding a wall is quiet.
//
// Narrowing the documentation to name one command was the alternative and it
// was refused: a promise that holds for `af explore` and breaks for
// `af workload run --kind exploration` is a promise a customer discovers is
// conditional at the worst moment, and the hosted path is the one the console
// drives.
func explorationVerdict(out *explore.Report) string {
	if len(out.Explorations) == 0 {
		return VerdictBlocked
	}
	worst := VerdictPass
	for _, e := range out.Explorations {
		if e.Outcome.Verdict == VerdictBlocked {
			worst = worseOf(worst, VerdictBlocked)
		}
	}
	return worst
}

// explorationDetail is the sentence beside an exploration's verdict.
//
// An exploration never FAILS: explorationVerdict above can only return pass,
// blocked or unverified, because a wander produces findings rather than a
// judgement. So this was empty for every exploration that explored anything,
// on the reasoning that an empty detail never lands under a red verdict.
//
// That reasoning is half right and the half it misses is the one people see.
// `unverified` is not red and it is not a pass either, and an unverified run
// with nothing beside it is the same blank first line a failing load run used
// to have. A goal that was not reached is the single most useful thing to say
// about an exploration, and it was the one thing this did not say.
func explorationDetail(out *explore.Report) string {
	if len(out.Explorations) == 0 {
		return "no goal was explored, so nothing was found"
	}
	for _, e := range out.Explorations {
		if e.Outcome.Verdict == VerdictBlocked {
			return named(e.Name, e.Outcome.Detail)
		}
	}
	var unreached []string
	for _, e := range out.Explorations {
		if !e.Reached {
			unreached = append(unreached, e.Name)
		}
	}
	switch len(unreached) {
	case 0:
		return ""
	case 1:
		return unreached[0] + " did not reach its goal"
	}
	return fmt.Sprintf("%d goals were not reached, starting with %s",
		len(unreached), unreached[0])
}

// worseOf picks the worse of two verdicts, in the product's own precedence.
func worseOf(a, b string) string {
	rank := map[string]int{
		VerdictFail: 5, VerdictFlaky: 4, VerdictBlocked: 3,
		VerdictUnverified: 2, VerdictPass: 1,
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func passFail(ok bool) string {
	if ok {
		return VerdictPass
	}
	return VerdictFail
}

func setLatency(m *Measured, l load.Latency) {
	m.P50Ms = floatp(l.P50Ms)
	m.P90Ms = floatp(l.P90Ms)
	m.P95Ms = floatp(l.P95Ms)
	m.P99Ms = floatp(l.P99Ms)
	m.MaxMs = floatp(l.MaxMs)
}

func routeMetrics(scenario string, rows []load.RouteResult) []RouteMetric {
	out := make([]RouteMetric, 0, len(rows))
	for i, r := range rows {
		m := RouteMetric{
			Scenario: scenario,
			Route:    r.Route,
			Sent:     r.Sent,
			Errors:   r.Errors,
			P50Ms:    floatp(r.Latency.P50Ms),
			P90Ms:    floatp(r.Latency.P90Ms),
			P95Ms:    floatp(r.Latency.P95Ms),
			P99Ms:    floatp(r.Latency.P99Ms),
			MaxMs:    floatp(r.Latency.MaxMs),
			Position: i,
		}
		// Both together or neither. No baseline and no change are different
		// answers, and a zero standing in for the first reads as no
		// regression when it means nothing to compare with.
		if r.HasBaseline {
			m.BaselineP95Ms = floatp(r.BaselineP95Ms)
			m.P95Increase = floatp(r.P95Increase)
		}
		out = append(out, m)
	}
	return out
}

func refusedNames(refused []load.Route) []string {
	out := make([]string, 0, len(refused))
	for _, r := range refused {
		out = append(out, r.String())
	}
	sort.Strings(out)
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// orFailure turns "the path returned nothing and no error" into an error,
// because a nil result with a nil error is a bug in a caller and reporting it
// as a successful run of nothing is exactly the silence this package exists to
// remove.
func orFailure(err error) error {
	if err != nil {
		return err
	}
	return errNoResult
}

var errNoResult = &noResultError{}

type noResultError struct{}

func (*noResultError) Error() string {
	return "the engine returned no result and no error, which is a bug rather than an outcome"
}

// evidenceKinds maps a field on the runner's evidence to the kind recorded.
var evidenceKinds = []struct {
	kind string
	pick func(video, trace, shot string) string
}{
	{"video", func(v, _, _ string) string { return v }},
	{"trace", func(_, t, _ string) string { return t }},
	{"screenshot", func(_, _, s string) string { return s }},
}

func workflowEvidence(root string, out *env.TestReport) []Evidence {
	ev := []Evidence{}
	for _, r := range out.Results {
		ev = append(ev, evidenceFor(root, r.Workflow,
			r.Evidence.Video, r.Evidence.Trace, r.Evidence.Screenshot)...)
	}
	return ev
}

func explorationEvidence(root string, out *explore.Report) []Evidence {
	ev := []Evidence{}
	for _, e := range out.Explorations {
		ev = append(ev, evidenceFor(root, e.Name,
			e.Evidence.Video, e.Evidence.Trace, e.Evidence.Screenshot)...)
	}
	return ev
}

// evidenceFor records an artifact, its digest and its size, and says plainly
// that the bytes are on this machine and nowhere else.
//
// The digest is computed rather than skipped, and it is the whole reason this
// is more than a path. Nothing uploads evidence today, so a hosted reader gets
// a path on a runner that no longer exists; recording the digest and the size
// is what lets an uploader, when there is one, prove it moved the right bytes,
// and lets a person who still has the file confirm it is the same one.
func evidenceFor(root, label string, video, trace, shot string) []Evidence {
	var out []Evidence
	for _, k := range evidenceKinds {
		path := k.pick(video, trace, shot)
		if strings.TrimSpace(path) == "" {
			continue
		}
		e := Evidence{
			Kind:         k.kind,
			Label:        label,
			Availability: AvailabilityRunnerLocal,
			Locator:      relativeTo(root, path),
		}
		if sum, size, ok := digest(path); ok {
			e.SHA256 = sum
			e.SizeBytes = &size
		} else {
			// The run named it and it is not there. Said rather than dropped:
			// a missing trace is a fact about the run worth seeing.
			e.Availability = AvailabilityNotRetained
		}
		out = append(out, e)
	}
	return out
}

func relativeTo(root, path string) string {
	if root == "" {
		return path
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return filepath.ToSlash(rel)
}

func digest(path string) (string, int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, false
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, false
	}
	return hex.EncodeToString(h.Sum(nil)), size, true
}

// verdictsAgree is a compile time reminder that this package's five verdict
// strings are the product's, not a sixth vocabulary invented here.
var verdictsAgree = func() bool {
	for _, v := range []string{VerdictFail, VerdictFlaky, VerdictBlocked, VerdictUnverified, VerdictPass} {
		if !report.Known(v) {
			return false
		}
	}
	return true
}()
