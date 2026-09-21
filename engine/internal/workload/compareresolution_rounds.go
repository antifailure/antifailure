package workload

import (
	"fmt"
	"math"
)

// Measuring a route's change round against round, so that the interval around
// it includes the noise the host actually made.
//
// THE FAILURE THIS EXISTS FOR, measured on 2026-09-21. Ten comparisons on one
// host, all through `af load compare` with its rounds interleaved. Three of
// them compared two builds whose only difference is a comment, and every one
// labelled routes as moved beyond resolution, in BOTH directions and by up to
// four fold: GET /accounts/7/entries plus 412 percent, GET /accounts plus 268,
// GET /statements minus 88. Two of the three FAILED the identical build. The
// three that compared a real regression never once flagged the route the diff
// changed, and failed each time on a route it did not touch.
//
// The resolution band in compareresolution.go was not wrong about what it
// measures. It models how far ONE run's p95 could have landed from itself if
// its requests were independent draws from one stationary distribution. On a
// real host latency arrives in correlated spikes, and two environments, or two
// rounds a minute apart, differ by far more than that band. The band described
// the noise inside a run and the comparison was being decided by the noise
// between runs, which nothing measured.
//
// The rounds measure it. Each round sends the same request sequence at both
// sides back to back, so a round's pair of p95s is a small comparison of its
// own, and the scatter of those small comparisons IS the host's noise, in the
// same units as the change. The change is the mean of the per round log ratios
// and its interval is a t interval on them, at the same ninety percent the
// single run band uses. The verdict is then the rule #540 settled and this file
// does not touch: the interval is placed against the limit, and a limit inside
// it is neither a pass nor a fail.
//
// What this costs is honesty about the size of what can be seen. A host whose
// rounds disagree by a factor of two cannot resolve a thirty percent change,
// and now it says so instead of calling one.

// RoundP95 is one round's p95 per route on each side, in round order. Round k
// on the base and round k on this build were sent the same request sequence,
// back to back, which is what makes them a pair.
type RoundP95 struct {
	Base      map[string]float64 `json:"base"`
	Candidate map[string]float64 `json:"candidate"`
}

// minimumRoundPairs is how many rounds must have sent a route on both sides
// before the spread between them means anything. One pair has no spread at
// all, and a t interval on two is honest but very wide, which is the right
// answer rather than a reason to refuse.
const minimumRoundPairs = 2

// tQuantile95 is the 0.95 quantile of Student's t, which makes a two sided
// ninety percent interval, the same level resolutionZ gives a single run.
// Tabulated to thirty degrees of freedom and approximated past that with the
// first term of the Cornish Fisher expansion, which is within 0.002 of the
// table at thirty and converges on 1.645.
func tQuantile95(df int) float64 {
	table := []float64{0, 6.314, 2.920, 2.353, 2.132, 2.015, 1.943, 1.895, 1.860, 1.833,
		1.812, 1.796, 1.782, 1.771, 1.761, 1.753, 1.746, 1.740, 1.734, 1.729,
		1.725, 1.721, 1.717, 1.714, 1.711, 1.708, 1.706, 1.703, 1.701, 1.699, 1.697}
	if df < 1 {
		return math.Inf(1)
	}
	if df < len(table) {
		return table[df]
	}
	z := resolutionZ
	return z + (z*z*z+z)/(4*float64(df))
}

// ResolveByRounds replaces each route's pooled comparison with a round against
// round one, where the rounds allow it.
//
// The p95 columns become each side's per round p95s averaged on a log scale,
// so that the change column is exactly the ratio of the two columns and a
// reader never sees a change that the numbers beside it do not produce. A
// route that too few rounds sent on both sides keeps its pooled numbers and is
// left unresolved, with the reason, rather than judged on the band this file
// exists because it could not see.
func ResolveByRounds(c *Comparison, rounds []RoundP95) {
	if c == nil {
		return
	}
	for i := range c.Routes {
		r := &c.Routes[i]
		if r.Scenario != "" || !r.InBaseline || !r.InCandidate {
			continue
		}
		var logBase, logCand, logRatio []float64
		for _, round := range rounds {
			b, c := round.Base[r.Route], round.Candidate[r.Route]
			if b <= 0 || c <= 0 {
				continue
			}
			logBase = append(logBase, math.Log(b))
			logCand = append(logCand, math.Log(c))
			logRatio = append(logRatio, math.Log(c/b))
		}
		n := len(logRatio)
		res := RouteResolution{Method: ResolutionRounds, Rounds: n}
		if n < minimumRoundPairs {
			res.Detail = fmt.Sprintf("only %d of %d rounds sent this route on both sides, "+
				"so the spread between rounds, which is the noise this comparison is judged "+
				"against, is not known", n, len(rounds))
			r.Resolution = res
			continue
		}
		m, s := meanAndSD(logRatio)
		h := tQuantile95(n-1) * s / math.Sqrt(float64(n))
		base, cand := math.Exp(mean(logBase)), math.Exp(mean(logCand))
		ratio := cand/base - 1
		delta := cand - base
		r.P95Baseline, r.P95Candidate = floatp(base), floatp(cand)
		r.P95Delta, r.P95Ratio = floatp(delta), floatp(ratio)
		r.Direction = directionOf(delta, true)
		res.ChangeLow = floatp(math.Exp(m-h) - 1)
		res.ChangeHigh = floatp(math.Exp(m+h) - 1)
		// The smallest change, either way, that this route could have shown
		// on this host: the half width of the interval in the direction where
		// it is widest, which on a log scale is always upward.
		res.SmallestVisible = floatp(math.Exp(h) - 1)
		r.Resolution = res
	}
}

// intervalVerdict places a round interval against a limit, by the rule
// Verdict states for a single run: entirely above is a fail, entirely at or
// below is a pass, straddling is neither.
func (r RouteResolution) intervalVerdict(limit float64) (string, bool) {
	switch {
	case *r.ChangeLow > limit:
		return VerdictFail, true
	case *r.ChangeHigh <= limit:
		return VerdictPass, true
	default:
		return VerdictUnverified, false
	}
}

// hasInterval reports whether this resolution was measured round against
// round, and so carries an interval rather than a band.
func (r RouteResolution) hasInterval() bool {
	return r.ChangeLow != nil && r.ChangeHigh != nil
}

func mean(v []float64) float64 {
	sum := 0.0
	for _, x := range v {
		sum += x
	}
	return sum / float64(len(v))
}

// meanAndSD is the mean and the sample standard deviation, with n minus one,
// because the mean was estimated from the same numbers.
func meanAndSD(v []float64) (float64, float64) {
	m := mean(v)
	ss := 0.0
	for _, x := range v {
		ss += (x - m) * (x - m)
	}
	return m, math.Sqrt(ss / float64(len(v)-1))
}
