package workload

import (
	"fmt"
	"sort"
)

// Turning a difference into a verdict, which Compare deliberately does not do.
//
// compare.go differences two results and stops there, and its header says why:
// two runs against two environments are not a controlled experiment, so a
// difference is a difference and calling it a regression is a judgement. This
// file is where that judgement is made, by a threshold somebody declared in
// their manifest, and it is a separate file because the two are separate acts.
// Nothing here changes a measurement; it only reads the differences Compare
// already computed and says which declared limit each one crossed.
//
// The rule every verdict in this file obeys: a threshold that could not be
// evaluated is UNVERIFIED, never pass. A route present on one side only, a
// baseline rate of zero, a measurement absent on either side, or a declared
// limit that found nothing at all to measure, all report unverified and say
// why. The defect this repository keeps finding in its own instruments is a
// configured check that evaluated nothing and reported green, and a base
// branch comparison is an easy place to ship another one: the candidate can
// simply have stopped serving a route, which is the loudest possible result
// and the one a pass would hide.

// ComparisonThresholds are the declared base branch limits, in the shape this
// package evaluates them. It mirrors schema.LoadComparisonThresholds rather
// than importing it, so that judging a comparison needs no manifest and can be
// tested with numbers rather than with YAML.
//
// Zero means the limit was not declared and is not evaluated, as it does
// everywhere else in this product's thresholds.
type ComparisonThresholds struct {
	P95Increase       float64
	ThroughputDrop    float64
	ErrorRateIncrease float64
}

// Declared reports whether any limit was asked for at all. A comparison with
// no declared threshold is a report rather than a check, and saying so is
// different from saying everything passed.
func (t ComparisonThresholds) Declared() bool {
	return t.P95Increase > 0 || t.ThroughputDrop > 0 || t.ErrorRateIncrease > 0
}

// ComparisonVerdict is one declared threshold, evaluated against the two runs.
//
// Baseline and Candidate are carried beside Observed rather than left to be
// recomputed, because the number that failed is the first thing somebody asks
// about and a ratio on its own cannot answer "slower than what".
type ComparisonVerdict struct {
	Name string `json:"name"`
	// Scope is the route this applies to, and is empty for a run wide limit.
	Scope     string   `json:"scope,omitempty"`
	Measure   string   `json:"measure"`
	Threshold float64  `json:"threshold"`
	Observed  *float64 `json:"observed"`
	Baseline  *float64 `json:"baseline"`
	Candidate *float64 `json:"candidate"`
	Value     string   `json:"value"`
	Detail    string   `json:"detail,omitempty"`
	// Resolution is what the run could see, carried beside the verdict so
	// that a pass and a "could not look" are never the same row.
	Resolution RouteResolution `json:"resolution"`
	// Unresolvable separates the two reasons a row can be unverified, and
	// they are not the same fact. A route present on one side only has no
	// counterpart to be compared with, which is a finding about the
	// application. A threshold wider than the run's own resolution means the
	// instrument was blind, which is a finding about the run, and it is the
	// one that must never be flattened into a pass.
	Unresolvable bool `json:"unresolvable,omitempty"`
}

// Judge evaluates the declared thresholds against a comparison.
//
// It returns one row per thing that was asked for, including the ones it could
// not measure, because a console shown only the breaches would render a clean
// base branch comparison over routes nothing was ever compared against.
func Judge(c *Comparison, t ComparisonThresholds) []ComparisonVerdict {
	out := []ComparisonVerdict{}
	if c == nil {
		return out
	}
	if t.ThroughputDrop > 0 {
		out = append(out, judgeThroughput(c, t.ThroughputDrop))
	}
	if t.ErrorRateIncrease > 0 {
		out = append(out, judgeErrorRate(c, t.ErrorRateIncrease))
	}
	if t.P95Increase > 0 {
		out = append(out, judgeRouteP95(c, t.P95Increase)...)
	}
	return out
}

// measure finds one run wide measure by name.
func measure(c *Comparison, name string) *MeasureDifference {
	for i := range c.Measures {
		if c.Measures[i].Measure == name {
			return &c.Measures[i]
		}
	}
	return nil
}

// judgeThroughput reads the achieved rate, never the target rate.
//
// A run that fell behind its target reports the target as fine while the queue
// grows, which is the reason load.Result records both and this reads the one
// that happened.
func judgeThroughput(c *Comparison, limit float64) ComparisonVerdict {
	v := ComparisonVerdict{
		Name: "throughput_drop", Measure: "achieved_rate", Threshold: limit,
		Value: VerdictUnverified,
	}
	m := measure(c, "achieved_rate")
	if m == nil || m.Baseline == nil || m.Candidate == nil {
		v.Detail = "one of the two runs did not record an achieved request rate, so " +
			"there is nothing to compare against"
		return v
	}
	v.Baseline, v.Candidate = m.Baseline, m.Candidate
	if *m.Baseline <= 0 {
		v.Detail = "the base branch achieved no requests per second, so a drop against " +
			"it is not a number"
		return v
	}
	// A drop rather than a ratio of rates, so the number reads the same way
	// round as the threshold: 0.1 means a tenth fewer requests served.
	drop := (*m.Baseline - *m.Candidate) / *m.Baseline
	v.Observed = floatp(drop)
	v.Value = passFail(drop <= limit)
	if v.Value == VerdictFail {
		v.Detail = fmt.Sprintf(
			"this branch served %.3g requests per second against the base branch's %.3g, "+
				"a drop of %.1f percent against a limit of %.1f percent",
			*m.Candidate, *m.Baseline, drop*100, limit*100)
	}
	return v
}

// judgeErrorRate compares in absolute points rather than as a ratio, because a
// base branch error rate of zero has no ratio and a build introducing errors
// where there were none is the case this most needs to catch.
func judgeErrorRate(c *Comparison, limit float64) ComparisonVerdict {
	v := ComparisonVerdict{
		Name: "error_rate_increase", Measure: "error_rate", Threshold: limit,
		Value: VerdictUnverified,
	}
	m := measure(c, "error_rate")
	if m == nil || m.Baseline == nil || m.Candidate == nil {
		v.Detail = "one of the two runs did not record an error rate, so there is " +
			"nothing to compare against"
		return v
	}
	v.Baseline, v.Candidate = m.Baseline, m.Candidate
	rise := *m.Candidate - *m.Baseline
	v.Observed = floatp(rise)
	v.Value = passFail(rise <= limit)
	if v.Value == VerdictFail {
		v.Detail = fmt.Sprintf(
			"this branch failed %.2f percent of requests against the base branch's %.2f percent, "+
				"a rise of %.2f points against a limit of %.2f points",
			*m.Candidate*100, *m.Baseline*100, rise*100, limit*100)
	}
	return v
}

// judgeRouteP95 evaluates the per route latency limit, one row per route.
//
// A route on one side only gets an unverified row rather than no row. It is
// the single most important case here: a candidate that stopped serving a
// route entirely has no p95 to be slower, and silence would report the loudest
// possible regression as a clean comparison.
//
// When the limit was declared and NOT ONE route could be measured, a final row
// says so. Without it a comparison of two runs that share no route at all
// would return only unverified rows that a reader skims past, and the run
// would carry a threshold that evaluated nothing.
func judgeRouteP95(c *Comparison, limit float64) []ComparisonVerdict {
	rows := make([]ComparisonVerdict, 0, len(c.Routes)+1)
	measured := 0
	for _, r := range c.Routes {
		v := ComparisonVerdict{
			Name: "p95_increase", Scope: routeScope(r), Measure: "p95_ms",
			Threshold: limit, Value: VerdictUnverified,
			Baseline: r.P95Baseline, Candidate: r.P95Candidate,
		}
		switch {
		case !r.InBaseline:
			v.Detail = "this route was not sent on the base branch, so there is no " +
				"base measurement to compare against"
		case !r.InCandidate:
			v.Detail = "this route was sent on the base branch and not on this one, so " +
				"there is no measurement on this side to compare"
		case r.P95Baseline == nil || r.P95Candidate == nil:
			v.Detail = "one of the two runs recorded no p95 for this route"
		case *r.P95Baseline == 0:
			v.Detail = "the base branch recorded a p95 of zero for this route, so a " +
				"ratio against it is not a number"
		default:
			ratio := *r.P95Candidate/(*r.P95Baseline) - 1
			v.Observed = floatp(ratio)
			v.Resolution = r.Resolution
			// Can this run see a difference of the size being asked about?
			// Asked BEFORE the comparison against the limit, and about the
			// LIMIT rather than about the observed number, because those are
			// different questions and only one of them is answerable. "Is
			// this route more than 60 percent slower" cannot be answered at
			// all by a run whose p95 moves by 300 percent on its own, and the
			// answer it was giving was a confident pass or a confident fail.
			//
			// A threshold this run can resolve makes the verdict beneath it
			// meaningful in both directions: a pass means no difference of
			// that size was there to see, and a fail means one was.
			decided, ok := r.Resolution.Verdict(ratio, limit)
			if !ok {
				v.Value = VerdictUnverified
				v.Unresolvable = true
				v.Detail = unresolvableDetail(r, ratio, limit)
				break
			}
			measured++
			v.Value = decided
			if v.Value == VerdictFail {
				v.Detail = fmt.Sprintf(
					"p95 went from %.3gms on the base branch to %.3gms on this one, "+
						"%.1f percent slower against a limit of %.1f percent",
					*r.P95Baseline, *r.P95Candidate, ratio*100, limit*100)
				if r.Resolution.hasInterval() {
					v.Detail += fmt.Sprintf(", and across %d rounds the change lies between "+
						"%.1f and %.1f percent, all of it above the limit",
						r.Resolution.Rounds, *r.Resolution.ChangeLow*100,
						*r.Resolution.ChangeHigh*100)
				}
			}
		}
		rows = append(rows, v)
	}
	if measured == 0 {
		rows = append(rows, ComparisonVerdict{
			Name: "p95_increase", Measure: "p95_ms", Threshold: limit,
			Value: VerdictUnverified,
			Detail: "a per route latency limit was in force and not one route carried a " +
				"measurement it could be judged on, so this threshold compared nothing",
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Scope < rows[j].Scope })
	return rows
}

// unresolvableDetail says what the run could see and what was asked of it.
//
// The numbers rather than an adjective, because "could not resolve" on its own
// tells somebody nothing about what to do next, and the two things that fix it
// are a longer run and a quieter machine. The sample count is named because it
// is the lever the reader actually has.
func unresolvableDetail(r RouteDifference, observed, limit float64) string {
	if r.Resolution.Method == ResolutionRounds {
		return roundsUnresolvableDetail(r.Resolution, observed, limit)
	}
	sent := 0
	if r.SentBaseline != nil {
		sent = *r.SentBaseline
	}
	if r.SentCandidate != nil && *r.SentCandidate < sent {
		sent = *r.SentCandidate
	}
	if r.Resolution.SmallestVisible == nil {
		return "this run cannot say how far this route's p95 could have landed from " +
			"itself, so it cannot say whether a difference of " +
			fmt.Sprintf("%.1f percent", limit*100) + " would have been visible: " +
			r.Resolution.Detail
	}
	band := *r.Resolution.SmallestVisible
	detail := fmt.Sprintf(
		"the difference measured is %.1f percent give or take %.1f, so the true value "+
			"lies anywhere between %.1f and %.1f percent and the limit of %.1f sits "+
			"inside that: neither a pass nor a breach would have meant anything. The "+
			"p95 here rests on %d requests and could have landed %.3gms either side of "+
			"itself with nothing changing",
		observed*100, band*100, (observed-band)*100, (observed+band)*100,
		limit*100, sent, bandOf(r.Resolution))
	if r.Resolution.TooFewSamples {
		detail += ". " + r.Resolution.Detail
	}
	return detail + ". Send for longer, or on a quieter machine"
}

// roundsUnresolvableDetail says the same for a change measured round against
// round, with the number a reader can act on: the smallest change this host
// could have resolved on this route at this many rounds.
func roundsUnresolvableDetail(res RouteResolution, observed, limit float64) string {
	if !res.hasInterval() {
		return res.Detail + ". Send for longer, so every route reaches every round"
	}
	return fmt.Sprintf(
		"measured round against round across %d rounds, the change is %.1f percent and "+
			"the true value lies between %.1f and %.1f percent; the limit of %.1f sits "+
			"inside that, so neither a pass nor a breach would have meant anything. On "+
			"this host, at this many rounds, this route can resolve a change of about "+
			"%.0f percent. Send for longer, run more rounds, or use a quieter machine",
		res.Rounds, observed*100, *res.ChangeLow*100, *res.ChangeHigh*100, limit*100,
		*res.SmallestVisible*100)
}

// bandOf is the wider of the two sides' half widths, which is the one a reader
// should picture when asking how far the number could have moved.
func bandOf(res RouteResolution) float64 {
	worst := 0.0
	if res.BandBaselineMs != nil {
		worst = *res.BandBaselineMs
	}
	if res.BandCandidateMs != nil && *res.BandCandidateMs > worst {
		worst = *res.BandCandidateMs
	}
	return worst
}

func routeScope(r RouteDifference) string {
	if r.Scenario == "" {
		return r.Route
	}
	return r.Scenario + " " + r.Route
}

// ComparisonOutcome is the one word answer over a set of judged thresholds.
//
// Unverified when nothing could be evaluated, and that is the line that
// matters. A comparison whose every threshold was unmeasurable is not a clean
// comparison, and returning pass for it is exactly how this product once
// shipped a green nightly corpus that had never reached an agent.
func ComparisonOutcome(rows []ComparisonVerdict) string {
	if len(rows) == 0 {
		return VerdictUnverified
	}
	evaluated, blind := false, false
	for _, r := range rows {
		if r.Value == VerdictFail {
			return VerdictFail
		}
		if r.Value == VerdictPass {
			evaluated = true
		}
		if r.Unresolvable {
			blind = true
		}
	}
	// A threshold the run could not resolve outranks the ones it could. Three
	// routes passing and four being invisible is not a pass with a footnote:
	// the limit was in force over seven routes and the run answered for
	// three, so reporting a pass tells somebody their change was checked
	// against a tolerance that four routes were never held to.
	//
	// A failure still outranks both, above. A breach the run COULD resolve is
	// a real breach whatever else went unseen, and burying it because a
	// different route was too quiet would be the same defect pointed the
	// other way.
	if blind || !evaluated {
		return VerdictUnverified
	}
	return VerdictPass
}

// ComparisonBreaches returns just the failing rows, for a caller that wants to
// print the reason a run failed without walking every route that passed.
func ComparisonBreaches(rows []ComparisonVerdict) []ComparisonVerdict {
	out := []ComparisonVerdict{}
	for _, r := range rows {
		if r.Value == VerdictFail {
			out = append(out, r)
		}
	}
	return out
}
