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
// and its interval is a t interval on them, at ninety percent for the whole
// table of routes together; see familyLevel. The verdict is then the rule #540
// settled, which this file does not touch: the interval is placed against the
// limit, and a limit inside it is neither a pass nor a fail.
//
// What this costs is honesty about the size of what can be seen. A host whose
// rounds disagree by a factor of two cannot resolve a thirty percent change,
// and now it says so instead of calling one.

// RoundP95 is one round's p95 per unit on each side, in round order. Round k
// on the base and round k on this build were sent the same request sequence,
// back to back, which is what makes them a pair.
//
// THE KEY IS THE UNIT'S SCOPE, not its route. For the HTTP mix the two are the
// same string, because a mix's rows carry no scenario, which is why this file
// read the route name directly for as long as HTTP was the only thing
// compared. A SQL workload's rows are a transaction and the statements inside
// it, so two transactions can hold a statement with the same label and keying
// on the label alone would pool two different statements' latencies into one
// ratio. UnitKey is what both sides must build the key with.
type RoundP95 struct {
	Base      map[string]float64 `json:"base"`
	Candidate map[string]float64 `json:"candidate"`
}

// UnitKey is the key one row of the per unit table is known by, and the same
// string the verdict rows carry as their Scope.
//
// Exported so that a caller filling RoundP95 cannot spell the key a second
// way. Getting it wrong raises no error: the row simply never finds its rounds
// and reports that too few rounds reached it on both sides, forever, which
// reads as a quiet workload rather than as a bug in the harness.
func UnitKey(scenario, unit string) string {
	if scenario == "" {
		return unit
	}
	return scenario + " " + unit
}

// minimumRoundPairs is how many rounds must have sent a route on both sides
// before the spread between them means anything. One pair has no spread at
// all, and a t interval on two is honest but very wide, which is the right
// answer rather than a reason to refuse.
const minimumRoundPairs = 2

// familyLevel is the confidence the comparison holds its WHOLE TABLE to.
//
// Ninety percent, the level the single run band uses, but for every route at
// once rather than for each route alone. Each route's interval is widened by
// Bonferroni to 1 minus 0.10 over the number of routes judged. Measured on
// 2026-09-21: at ninety percent PER ROUTE, three comparisons of identical
// code put a direction on 2 of their 21 route intervals, which is exactly the
// ten percent a per route interval promises and one wrong arrow in every
// other table. A gate that fails when ANY route breaches is making one claim
// about all of them, so it is held to one level for all of them, and the
// same nine comparisons recomputed that way put a direction on none.
const familyLevel = 0.90

// tQuantile is the p quantile of Student's t with df degrees of freedom,
// found by bisection on the distribution function. A table stops at the
// levels somebody tabulated, and a Bonferroni level depends on how many
// routes a manifest has.
func tQuantile(p float64, df int) float64 {
	if df < 1 {
		return math.Inf(1)
	}
	lo, hi := 0.0, 1e4
	for i := 0; i < 200; i++ {
		mid := (lo + hi) / 2
		if tCDF(mid, float64(df)) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// tCDF is Student's t distribution function, through the regularized
// incomplete beta function.
func tCDF(t, df float64) float64 {
	x := df / (df + t*t)
	tail := 0.5 * incompleteBeta(df/2, 0.5, x)
	if t > 0 {
		return 1 - tail
	}
	return tail
}

// incompleteBeta is the regularized incomplete beta function I_x(a, b), by
// its continued fraction, taken from whichever side converges.
func incompleteBeta(a, b, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	front := math.Exp(lab - la - lb + a*math.Log(x) + b*math.Log(1-x))
	if x < (a+1)/(a+b+2) {
		return front * betaFraction(a, b, x) / a
	}
	return 1 - front*betaFraction(b, a, 1-x)/b
}

// betaFraction evaluates the continued fraction for the incomplete beta by
// the modified Lentz method.
func betaFraction(a, b, x float64) float64 {
	const tiny = 1e-300
	guard := func(v float64) float64 {
		if math.Abs(v) < tiny {
			return tiny
		}
		return v
	}
	c, d := 1.0, 1/guard(1-(a+b)*x/(a+1))
	h := d
	for m := 1; m <= 300; m++ {
		fm := float64(m)
		num := fm * (b - fm) * x / ((a + 2*fm - 1) * (a + 2*fm))
		d = 1 / guard(1+num*d)
		c = guard(1 + num/c)
		h *= d * c
		num = -(a + fm) * (a + b + fm) * x / ((a + 2*fm) * (a + 2*fm + 1))
		d = 1 / guard(1+num*d)
		c = guard(1 + num/c)
		step := d * c
		h *= step
		if math.Abs(step-1) < 3e-14 {
			break
		}
	}
	return h
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
	// First every route's log ratios, so that the number of routes judged is
	// known before any interval is drawn: the family is every route this
	// comparison will put a verdict on, and each interval's width depends on
	// its size.
	nouns := nounsFor(c.Kind)
	type pairs struct{ logBase, logCand, logRatio []float64 }
	measured := map[int]pairs{}
	family := 0
	for i := range c.Routes {
		r := &c.Routes[i]
		// A row present on one side only has no pair to be a ratio of, and is
		// left with its pooled numbers and no interval. There is deliberately
		// no test on the scenario here: this file once skipped every row that
		// carried one, which was correct while the HTTP mix was the only thing
		// compared and silently blinded every SQL unit, because a statement's
		// scenario is the transaction it belongs to and is never empty.
		if !r.InBaseline || !r.InCandidate {
			continue
		}
		key := routeScope(*r)
		var p pairs
		for _, round := range rounds {
			b, cand := round.Base[key], round.Candidate[key]
			if b <= 0 || cand <= 0 {
				continue
			}
			p.logBase = append(p.logBase, math.Log(b))
			p.logCand = append(p.logCand, math.Log(cand))
			p.logRatio = append(p.logRatio, math.Log(cand/b))
		}
		measured[i] = p
		if len(p.logRatio) >= minimumRoundPairs {
			family++
		}
	}
	for i, p := range measured {
		r := &c.Routes[i]
		n := len(p.logRatio)
		res := RouteResolution{Method: ResolutionRounds, Rounds: n, Family: family}
		if n < minimumRoundPairs {
			res.Detail = fmt.Sprintf("only %d of %d rounds %s this %s on both sides, "+
				"so the spread between rounds, which is the noise this comparison is judged "+
				"against, is not known", n, len(rounds), nouns.ran, nouns.unit)
			r.Resolution = res
			continue
		}
		m, s := meanAndSD(p.logRatio)
		// Two sided, and shared across the family by Bonferroni.
		q := 1 - (1-familyLevel)/(2*float64(family))
		h := tQuantile(q, n-1) * s / math.Sqrt(float64(n))
		base, cand := math.Exp(mean(p.logBase)), math.Exp(mean(p.logCand))
		r.P95Baseline, r.P95Candidate = floatp(base), floatp(cand)
		r.P95Delta, r.P95Ratio = floatp(cand-base), floatp(cand/base-1)
		r.Direction = directionOf(cand-base, true)
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
