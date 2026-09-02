package workload

import (
	"fmt"
	"math"
	"sort"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// Comparing a baseline run against a candidate run.
//
// WHAT THIS IS NOT. It is not the differential oracle. af oracle brings a
// second environment up from a baseline revision, branches ONE golden for both
// so they start from identical rows, sends both the same probes, and diffs the
// responses and the database contents. That is a much stronger claim than
// anything here, and this file does not duplicate it or pretend to replace it.
//
// WHAT THIS IS. Two workload results that already exist, differenced. The
// control plane has both because it stored them, so the comparison needs no
// second environment, no checkout and no golden, and it can be run over
// history rather than only at the moment of a change.
//
// WHAT THAT COSTS, said plainly rather than buried. Two runs of a load
// workload against two environments are not a controlled experiment. The seed
// makes the request sequence the same; it does not make the machine, the
// database contents, the neighbours on the host or the time of day the same.
// So a difference here is a difference, and calling it a regression is a
// judgement a person or a threshold makes, not one this file makes. Every
// comparison says so in its notes, because a number labelled "regression" that
// is really machine noise is how a check stops being read.

// ComparisonSchema names the wire format.
const ComparisonSchema = "antifailure.workload.comparison/v1"

// Direction is which way a measurement moved, in terms of the product rather
// than of arithmetic: worse means slower or more errors.
const (
	DirectionWorse  = "worse"
	DirectionBetter = "better"
	DirectionSame   = "same"
	// DirectionUnmeasurable is for a route or a measure present on one side
	// only, or absent on both. It is a value rather than an omission because a
	// route that disappeared between two runs is a finding.
	DirectionUnmeasurable = "unmeasurable"
)

// Comparison is one baseline against one candidate.
type Comparison struct {
	Schema string `json:"schema"`
	Kind   Kind   `json:"kind"`
	// Baseline and Candidate name the two runs, so a reader can go back to
	// either without holding both documents.
	Baseline  ComparisonSide `json:"baseline"`
	Candidate ComparisonSide `json:"candidate"`
	// Measures are the run wide numbers, differenced.
	Measures []MeasureDifference `json:"measures"`
	// Routes are the per route numbers, differenced, including routes that
	// exist on one side only.
	Routes []RouteDifference `json:"routes"`
	// Thresholds are the verdict transitions, which is the part a person
	// usually wants first: what passed before and does not now.
	Thresholds []ThresholdDifference `json:"thresholds"`
	// Regressed counts the thresholds that went from a pass to something else.
	// A count rather than a verdict, because this file does not decide whether
	// a difference is acceptable.
	Regressed int `json:"regressed"`
	// Notes are what the comparison cannot see, always populated.
	Notes []string `json:"notes"`
}

// ComparisonSide identifies one of the two runs.
type ComparisonSide struct {
	RunID          string `json:"run_id,omitempty"`
	EnvID          string `json:"env_id,omitempty"`
	Branch         string `json:"branch,omitempty"`
	State          State  `json:"state"`
	Verdict        string `json:"verdict"`
	Command        string `json:"command"`
	ManifestDigest string `json:"manifest_digest,omitempty"`
}

// MeasureDifference is one run wide number, both sides and the movement.
type MeasureDifference struct {
	Measure   string   `json:"measure"`
	Baseline  *float64 `json:"baseline"`
	Candidate *float64 `json:"candidate"`
	Delta     *float64 `json:"delta"`
	// Ratio is candidate over baseline less one, which is how the engine
	// already expresses a p95 change. Absent when the baseline is zero,
	// because a ratio against zero is not a number.
	Ratio     *float64 `json:"ratio"`
	Direction string   `json:"direction"`
}

// RouteDifference is one route on both sides.
type RouteDifference struct {
	Scenario string `json:"scenario,omitempty"`
	Route    string `json:"route"`
	// InBaseline and InCandidate are separate booleans rather than one
	// "added" flag, because a route that vanished and a route that appeared
	// are different findings and both matter.
	InBaseline      bool     `json:"in_baseline"`
	InCandidate     bool     `json:"in_candidate"`
	SentBaseline    *int     `json:"sent_baseline"`
	SentCandidate   *int     `json:"sent_candidate"`
	ErrorsBaseline  *int     `json:"errors_baseline"`
	ErrorsCandidate *int     `json:"errors_candidate"`
	P95Baseline     *float64 `json:"p95_baseline"`
	P95Candidate    *float64 `json:"p95_candidate"`
	P95Delta        *float64 `json:"p95_delta"`
	P95Ratio        *float64 `json:"p95_ratio"`
	Direction       string   `json:"direction"`
}

// ThresholdDifference is one assertion or threshold on both sides.
type ThresholdDifference struct {
	Scenario  string `json:"scenario,omitempty"`
	Name      string `json:"name"`
	Scope     string `json:"scope,omitempty"`
	Measure   string `json:"measure,omitempty"`
	Baseline  string `json:"baseline_verdict,omitempty"`
	Candidate string `json:"candidate_verdict,omitempty"`
	// Regressed is true only for a move away from pass. A move from unverified
	// to fail is a change worth showing and is not a regression from a pass,
	// so the two are separate: Changed says something moved, Regressed says a
	// check that used to pass no longer does.
	Changed      bool     `json:"changed"`
	Regressed    bool     `json:"regressed"`
	ObservedBase *float64 `json:"observed_baseline"`
	ObservedCand *float64 `json:"observed_candidate"`
}

// Compare differences two results of the same kind.
func Compare(baseline, candidate *Result) (*Comparison, error) {
	switch {
	case baseline == nil || candidate == nil:
		return nil, aferrors.Coded(aferrors.AFWLD011,
			"detail", "a comparison needs two results and one of them is missing")
	case baseline.Kind != candidate.Kind:
		return nil, aferrors.Coded(aferrors.AFWLD011,
			"detail", fmt.Sprintf("the kinds differ, %s against %s, and they measure "+
				"different things", baseline.Kind, candidate.Kind))
	}

	c := &Comparison{
		Schema:     ComparisonSchema,
		Kind:       baseline.Kind,
		Baseline:   sideOf(baseline),
		Candidate:  sideOf(candidate),
		Measures:   []MeasureDifference{},
		Routes:     []RouteDifference{},
		Thresholds: []ThresholdDifference{},
	}
	c.Measures = measureDifferences(baseline, candidate)
	c.Routes = routeDifferences(baseline, candidate)
	c.Thresholds = thresholdDifferences(baseline, candidate)
	for _, t := range c.Thresholds {
		if t.Regressed {
			c.Regressed++
		}
	}
	c.Notes = comparisonNotes(baseline, candidate)
	return c, nil
}

func sideOf(r *Result) ComparisonSide {
	return ComparisonSide{
		RunID: r.RunID, EnvID: r.Environment.EnvID, Branch: r.Environment.Branch,
		State: r.State, Verdict: r.Verdict,
		Command: r.Reproduce.Command, ManifestDigest: r.Reproduce.ManifestDigest,
	}
}

// comparisonNotes says what the comparison cannot see. Always at least one,
// because there is always something.
func comparisonNotes(baseline, candidate *Result) []string {
	notes := []string{
		"two runs against two environments are not a controlled experiment: the seed " +
			"makes the request sequence the same and does not make the machine, the " +
			"database contents or the load on the host the same",
	}
	if baseline.State != StateSucceeded || candidate.State != StateSucceeded {
		notes = append(notes, fmt.Sprintf(
			"one of the runs did not complete: the baseline is %s and the candidate is %s, "+
				"so a difference between them may be a difference in how far each got",
			baseline.State, candidate.State))
	}
	if baseline.Reproduce.Command != candidate.Reproduce.Command {
		notes = append(notes, fmt.Sprintf(
			"the two runs were not asked for the same thing: %q against %q",
			baseline.Reproduce.Command, candidate.Reproduce.Command))
	}
	if baseline.Reproduce.ManifestDigest != "" &&
		baseline.Reproduce.ManifestDigest != candidate.Reproduce.ManifestDigest {
		notes = append(notes, "the two runs read different manifests, so the safe route list, "+
			"the thresholds and the declared workflows may not be the same on both sides")
	}
	if baseline.Verdict == VerdictUnverified || candidate.Verdict == VerdictUnverified {
		notes = append(notes, "one of the runs measured nothing, so most of this comparison is "+
			"a difference against an absence rather than against a measurement")
	}
	return notes
}

// measureDifferences differences the run wide numbers this kind actually has.
//
// Driven off the pointers rather than off the kind, so a measure that is null
// on both sides produces no row at all instead of a row of nulls. A console
// rendering "requests: null to null" for a browser run would be showing the
// reader a measurement that does not exist.
func measureDifferences(baseline, candidate *Result) []MeasureDifference {
	type pair struct {
		name          string
		a, b          *float64
		higherIsWorse bool
	}
	ints := func(a, b *int) (*float64, *float64) {
		var x, y *float64
		if a != nil {
			x = floatp(float64(*a))
		}
		if b != nil {
			y = floatp(float64(*b))
		}
		return x, y
	}
	reqA, reqB := ints(baseline.Measured.Requests, candidate.Measured.Requests)
	failA, failB := ints(baseline.Measured.Failures, candidate.Measured.Failures)
	wfA, wfB := ints(baseline.Measured.WorkflowsPassed, candidate.Measured.WorkflowsPassed)
	wffA, wffB := ints(baseline.Measured.WorkflowsFailed, candidate.Measured.WorkflowsFailed)
	goalA, goalB := ints(baseline.Measured.GoalsReached, candidate.Measured.GoalsReached)
	findA, findB := ints(baseline.Measured.Findings, candidate.Measured.Findings)

	pairs := []pair{
		{"requests", reqA, reqB, false},
		{"failures", failA, failB, true},
		{"error_rate", baseline.Measured.ErrorRate, candidate.Measured.ErrorRate, true},
		{"achieved_rate", baseline.Measured.AchievedRate, candidate.Measured.AchievedRate, false},
		{"p50_ms", baseline.Measured.P50Ms, candidate.Measured.P50Ms, true},
		{"p95_ms", baseline.Measured.P95Ms, candidate.Measured.P95Ms, true},
		{"p99_ms", baseline.Measured.P99Ms, candidate.Measured.P99Ms, true},
		{"workflows_passed", wfA, wfB, false},
		{"workflows_failed", wffA, wffB, true},
		{"goals_reached", goalA, goalB, false},
		{"findings", findA, findB, true},
	}

	out := make([]MeasureDifference, 0, len(pairs))
	for _, p := range pairs {
		if p.a == nil && p.b == nil {
			continue
		}
		d := MeasureDifference{Measure: p.name, Baseline: p.a, Candidate: p.b,
			Direction: DirectionUnmeasurable}
		if p.a != nil && p.b != nil {
			delta := *p.b - *p.a
			d.Delta = &delta
			if *p.a != 0 {
				ratio := *p.b/(*p.a) - 1
				d.Ratio = &ratio
			}
			d.Direction = directionOf(delta, p.higherIsWorse)
		}
		out = append(out, d)
	}
	return out
}

// directionOf reads a delta in the product's terms rather than arithmetic's.
//
// A tolerance rather than an equality test, because two floating point
// measurements of the same thing are never bit identical and a comparison that
// reported every run as changed would be read once.
func directionOf(delta float64, higherIsWorse bool) string {
	if math.Abs(delta) < 1e-9 {
		return DirectionSame
	}
	if (delta > 0) == higherIsWorse {
		return DirectionWorse
	}
	return DirectionBetter
}

func routeDifferences(baseline, candidate *Result) []RouteDifference {
	type key struct{ scenario, route string }
	index := func(rows []RouteMetric) map[key]RouteMetric {
		m := map[key]RouteMetric{}
		for _, r := range rows {
			m[key{r.Scenario, r.Route}] = r
		}
		return m
	}
	a, b := index(baseline.Routes), index(candidate.Routes)

	keys := map[key]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	ordered := make([]key, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].scenario != ordered[j].scenario {
			return ordered[i].scenario < ordered[j].scenario
		}
		return ordered[i].route < ordered[j].route
	})

	out := make([]RouteDifference, 0, len(ordered))
	for _, k := range ordered {
		base, inBase := a[k]
		cand, inCand := b[k]
		d := RouteDifference{
			Scenario: k.scenario, Route: k.route,
			InBaseline: inBase, InCandidate: inCand,
			Direction: DirectionUnmeasurable,
		}
		if inBase {
			d.SentBaseline = intp(base.Sent)
			d.ErrorsBaseline = intp(base.Errors)
			d.P95Baseline = base.P95Ms
		}
		if inCand {
			d.SentCandidate = intp(cand.Sent)
			d.ErrorsCandidate = intp(cand.Errors)
			d.P95Candidate = cand.P95Ms
		}
		if inBase && inCand && d.P95Baseline != nil && d.P95Candidate != nil {
			delta := *d.P95Candidate - *d.P95Baseline
			d.P95Delta = &delta
			if *d.P95Baseline != 0 {
				ratio := *d.P95Candidate/(*d.P95Baseline) - 1
				d.P95Ratio = &ratio
			}
			d.Direction = directionOf(delta, true)
		}
		out = append(out, d)
	}
	return out
}

func thresholdDifferences(baseline, candidate *Result) []ThresholdDifference {
	type key struct{ scenario, name, scope string }
	index := func(rows []ThresholdVerdict) map[key]ThresholdVerdict {
		m := map[key]ThresholdVerdict{}
		for _, t := range rows {
			m[key{t.Scenario, t.Name, t.Scope}] = t
		}
		return m
	}
	a, b := index(baseline.Thresholds), index(candidate.Thresholds)

	keys := map[key]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	ordered := make([]key, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].scenario != ordered[j].scenario {
			return ordered[i].scenario < ordered[j].scenario
		}
		if ordered[i].name != ordered[j].name {
			return ordered[i].name < ordered[j].name
		}
		return ordered[i].scope < ordered[j].scope
	})

	out := make([]ThresholdDifference, 0, len(ordered))
	for _, k := range ordered {
		base, inBase := a[k]
		cand, inCand := b[k]
		d := ThresholdDifference{
			Scenario: k.scenario, Name: k.name, Scope: k.scope,
			Baseline: base.Value, Candidate: cand.Value,
		}
		switch {
		case inCand:
			d.Measure = cand.Measure
		case inBase:
			d.Measure = base.Measure
		}
		if inBase {
			d.ObservedBase = base.Observed
		}
		if inCand {
			d.ObservedCand = cand.Observed
		}
		d.Changed = base.Value != cand.Value
		// A regression is a move away from a pass, and only that. A check that
		// went from unverified to fail is a change worth showing and was never
		// passing, so counting it as a regression would tell somebody a
		// working thing broke.
		d.Regressed = inBase && base.Value == VerdictPass && cand.Value != VerdictPass
		out = append(out, d)
	}
	return out
}
