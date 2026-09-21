package workload

import (
	"math"
	"sort"
	"strconv"
)

// Whether a comparison could see the difference it is about to report.
//
// THE FAILURE THIS EXISTS FOR, measured on 2026-09-21. An identical build was
// compared against itself, two commits whose only difference is a comment in
// one Go file, so every number either run produced is noise. Two samples,
// minutes apart, on a loaded host:
//
//	sample 1  every route "better", run wide p95 minus 57.4 percent
//	          worst route minus 86.4 percent, verdict PASS, exit 0
//	sample 2  every route "worse",  run wide p95 plus 192.3 percent
//	          worst route plus 585.9 percent, verdict FAIL on six routes
//
// A real regression measured on the same machine headlined at plus 253.7
// percent, which is SMALLER than the 585.9 percent a comment produced. So the
// tool returned a confident verdict, in both directions, on a comparison it
// could not resolve. A reader of sample 2 opens a pull request about a
// regression that does not exist; a reader of sample 1 ships a real one
// believing it an improvement.
//
// THE CAUSE IS COUNTABLE AND WAS ALREADY IN THE DOCUMENT. Those runs sent 145
// requests across seven routes, about twenty per route. A p95 taken from
// twenty samples by nearest rank IS the nineteenth of twenty, so it is one
// slow request away from the maximum and it moves by the width of the whole
// tail when anything on the host twitches. Nothing asked whether twenty
// samples could support the question being put to them.
//
// WHAT THIS COMPUTES. A percentile estimated from n samples is an order
// statistic, and which order statistic it lands on is itself random: the count
// of samples below the true quantile is Binomial(n, p), so the estimate's rank
// has a standard deviation of sqrt(n*p*(1-p)). Dividing by n turns that into a
// band of QUANTILE LEVELS around p, and reading the run's own distribution at
// the edges of that band turns it into milliseconds. That is the distance this
// route's p95 could have landed from itself, on this run, with nothing
// changing.
//
// It needs no second run and no extra traffic. Every quantity comes from the
// document the comparison already carries, which is the same property that
// makes the copy on write suite's jitter figure honest: a resolution
// reconstructed by hand after the fact is one nobody reconstructs.
//
// WHAT IT DELIBERATELY DOES NOT DO. It does not widen anybody's threshold. The
// threshold is the user's declared tolerance for a real change and it is not
// this file's business. It answers a different question, the one that was
// never asked: whether a difference of that size is visible to this run at
// all. A threshold smaller than the band is not too tight, it is being
// evaluated by an instrument that cannot see it, and the honest answer to it
// is neither pass nor fail.

// resolutionZ is the two sided 90 percent normal quantile.
//
// Ninety rather than ninety five, and the choice is a judgement worth stating.
// A wider interval refuses more comparisons, and every refusal this band
// produces is a run somebody has to repeat or lengthen. Ninety is the point at
// which the band still catches the twenty sample case that produced the
// failure above by an order of magnitude, while not refusing a long run whose
// tail happens to be heavy. It is applied to BOTH sides, so the interval a
// difference is judged against is wider than this figure alone suggests.
const resolutionZ = 1.645

// RouteResolution is what one route's p95 comparison could see.
type RouteResolution struct {
	// BandBaselineMs and BandCandidateMs are each side's own half width: how
	// far that side's p95 could have landed from itself with nothing
	// changing. Absent when the side carried no distribution to read.
	BandBaselineMs  *float64 `json:"band_baseline_ms"`
	BandCandidateMs *float64 `json:"band_candidate_ms"`
	// SmallestVisible is the smallest p95 ratio this route can distinguish
	// from noise, as a fraction of the base branch's p95. A threshold under
	// it cannot be evaluated by this run.
	SmallestVisible *float64 `json:"smallest_visible"`
	// TooFewSamples is set when a side sent fewer requests than a p95 needs
	// to mean anything, which is the crispest form of the same problem: at
	// fewer than twenty samples the nearest rank p95 IS the slowest single
	// request.
	TooFewSamples bool `json:"too_few_samples"`
	// Detail says in one sentence why, and is empty when the route resolved.
	Detail string `json:"detail,omitempty"`
	// Method is "rounds" when the change was measured round against round,
	// and empty for a single run judged by the band above. Rounds is how many
	// rounds sent this route on both sides, and ChangeLow and ChangeHigh are
	// the ninety percent interval for the change, as fractions, which the
	// verdict is decided on in place of the band. See
	// compareresolution_rounds.go for why a single run's band is not enough.
	Method string `json:"method,omitempty"`
	Rounds int    `json:"rounds,omitempty"`
	// Family is how many routes were judged together, which sets how wide
	// each route's interval had to be for the table as a whole to hold at
	// ninety percent.
	Family     int      `json:"family,omitempty"`
	ChangeLow  *float64 `json:"change_low,omitempty"`
	ChangeHigh *float64 `json:"change_high,omitempty"`
}

// ResolutionRounds is RouteResolution.Method for a change measured round
// against round.
const ResolutionRounds = "rounds"

// Verdict decides a threshold against an observed difference, or refuses.
//
// The question is NOT "can this run see a difference the size of the limit",
// which was the first thing this file asked and it was wrong. A run whose
// resolution is 200 percent, shown a difference of 600 percent against a limit
// of 100, can say with complete confidence that the limit was crossed: the
// difference exceeds the limit by three times the distance the number could
// have moved. Refusing there would hide a real regression to silence a false
// one, which is the trade this change exists to avoid making.
//
// The question is which SIDE of the limit the true difference is on. Put an
// interval of plus or minus the resolution around what was observed and ask
// where the limit falls:
//
//	interval entirely above the limit   the limit was crossed, fail
//	interval entirely at or below it    the limit held, pass
//	interval straddles the limit        this run cannot tell, and says so
//
// The third case is the identical build comparison that started this: plus
// 585.9 percent observed, plus or minus 1024, against a limit of 60. The
// interval runs from minus 438 to plus 1610 percent, so the limit sits inside
// it and neither a pass nor a breach would have meant anything.
func (r RouteResolution) Verdict(observed, limit float64) (value string, ok bool) {
	if r.hasInterval() {
		// The same rule, on the interval the rounds measured rather than on
		// a band placed around the observed number.
		return r.intervalVerdict(limit)
	}
	if r.SmallestVisible == nil {
		return VerdictUnverified, false
	}
	band := *r.SmallestVisible
	switch {
	case observed-band > limit:
		return VerdictFail, true
	case observed+band <= limit:
		return VerdictPass, true
	default:
		return VerdictUnverified, false
	}
}

// DirectionResolved reports whether the run can tell better from worse at all,
// which is a different question from whether it can place a limit.
//
// A difference smaller than the band has no sign this run is entitled to
// claim. The identical build comparison reported every route "better" by up to
// 86 percent on one sample and "worse" by up to 586 percent on the next, and
// the arrow was as wrong as the number. A table that prints a direction it
// cannot support teaches a reader to trust the next one.
func (r RouteResolution) DirectionResolved(observed float64) bool {
	if r.hasInterval() {
		return *r.ChangeLow > 0 || *r.ChangeHigh < 0
	}
	if r.SmallestVisible == nil {
		return false
	}
	return math.Abs(observed) > *r.SmallestVisible
}

// minimumSamplesForP95 is how many requests a route needs before its p95 is
// something other than its slowest request. The nearest rank method puts the
// p95 at ceil(0.95n) of n, which equals n for every n below twenty.
const minimumSamplesForP95 = 20

// quantilePoint is one known place on a run's own distribution.
type quantilePoint struct {
	level float64
	value float64
}

// knownQuantiles is what the projection recorded for one side, in order, with
// the levels that were never measured left out rather than invented.
func knownQuantiles(p50, p90, p95, p99, max *float64) []quantilePoint {
	out := []quantilePoint{}
	add := func(level float64, v *float64) {
		if v != nil {
			out = append(out, quantilePoint{level, *v})
		}
	}
	add(0.50, p50)
	add(0.90, p90)
	add(0.95, p95)
	add(0.99, p99)
	add(1.00, max)
	sort.Slice(out, func(i, j int) bool { return out[i].level < out[j].level })
	return out
}

// valueAtQuantile reads the run's own distribution at a level between the ones
// it recorded, by straight line interpolation.
//
// Linear rather than anything cleverer, because the points either side are
// real measurements and the shape between them is not known. Outside the
// recorded range it returns the nearest endpoint rather than extrapolating: a
// tail extended by a model is exactly the invented number this whole file
// exists to refuse.
func valueAtQuantile(points []quantilePoint, level float64) (float64, bool) {
	if len(points) == 0 {
		return 0, false
	}
	if level <= points[0].level {
		return points[0].value, true
	}
	last := points[len(points)-1]
	if level >= last.level {
		return last.value, true
	}
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		if level > b.level {
			continue
		}
		if b.level == a.level {
			return b.value, true
		}
		t := (level - a.level) / (b.level - a.level)
		return a.value + t*(b.value-a.value), true
	}
	return last.value, true
}

// p95HalfBand is how far one side's p95 could have landed from itself.
//
// Half the width of the interval rather than the whole, because a difference
// is judged against the two sides' half widths added, which is the condition
// that the two intervals do not overlap.
func p95HalfBand(sent int, points []quantilePoint) (float64, bool) {
	if sent <= 0 || len(points) == 0 {
		return 0, false
	}
	const p = 0.95
	dq := resolutionZ * math.Sqrt(p*(1-p)/float64(sent))
	lo, hi := p-dq, p+dq
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	vLo, okLo := valueAtQuantile(points, lo)
	vHi, okHi := valueAtQuantile(points, hi)
	if !okLo || !okHi {
		return 0, false
	}
	band := (vHi - vLo) / 2
	if band < 0 {
		band = 0
	}
	return band, true
}

// resolutionOf measures what one route's p95 comparison could see.
//
// Both sides, because a difference inherits the uncertainty of both, and the
// two half widths are added rather than the larger of them taken: added is
// exactly the condition that the two intervals do not overlap, and the larger
// alone would claim to resolve a difference that the other side's own spread
// could have produced. The copy on write suite takes the worse of its two
// arms, and it is comparing two MINIMA of a one sided noise, where the error
// does not accumulate the same way.
func resolutionOf(base, cand RouteMetric) RouteResolution {
	out := RouteResolution{}
	basePoints := knownQuantiles(base.P50Ms, base.P90Ms, base.P95Ms, base.P99Ms, base.MaxMs)
	candPoints := knownQuantiles(cand.P50Ms, cand.P90Ms, cand.P95Ms, cand.P99Ms, cand.MaxMs)

	if base.Sent < minimumSamplesForP95 || cand.Sent < minimumSamplesForP95 {
		out.TooFewSamples = true
	}

	bBand, bOK := p95HalfBand(base.Sent, basePoints)
	cBand, cOK := p95HalfBand(cand.Sent, candPoints)
	if bOK {
		out.BandBaselineMs = floatp(bBand)
	}
	if cOK {
		out.BandCandidateMs = floatp(cBand)
	}
	if !bOK || !cOK {
		out.Detail = "one of the two runs recorded no distribution for this route, so how " +
			"far its p95 could have landed from itself is not known"
		return out
	}
	if base.P95Ms == nil || *base.P95Ms <= 0 {
		out.Detail = "the base branch p95 for this route is zero, so a difference against " +
			"it has no scale to be measured on"
		return out
	}

	// As a fraction of the base branch p95, because that is the unit every
	// threshold in this block is written in.
	smallest := (bBand + cBand) / *base.P95Ms
	out.SmallestVisible = floatp(smallest)
	if out.TooFewSamples {
		out.Detail = "one of the two runs sent fewer than " +
			strconv.Itoa(minimumSamplesForP95) + " requests to this route, so its p95 is " +
			"its slowest single request rather than a percentile of anything"
	}
	return out
}
