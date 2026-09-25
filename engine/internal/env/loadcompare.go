package env

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/sqlload"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Running the same load workload on two builds and keeping the two results.
//
// The manifest has promised a base branch comparison for as long as load has
// existed, and nothing measured one. load.thresholds.p95_increase divides a
// measured p95 by PRODUCTION's p95 for that route, which is a useful number
// and is not the promised one: it answers "is this route slower than the
// fleet serves it", never "is this build slower than the last one". This file
// is the missing half.
//
// It is the oracle's baseline mechanism reused rather than reinvented, exactly
// as the side_effect family's base twin reuses it. The oracle already brings a
// commit up beside the candidate from a checked out tree and pins it to the
// candidate's golden; the only new work here is sending the same mix at both
// and keeping both results.
//
// ONE GOLDEN, and it is not a detail. Two goldens would mean the two builds
// answered queries over different rows, and a p95 difference would then be a
// difference in how much data each side held. The candidate comes up first so
// that its golden is the one to pin, which also means a scheduled golden
// refresh landing mid comparison cannot separate the two sides.
//
// ROUNDS, AND WHAT THEY WERE FOR. The first version sent the whole mix at the
// base branch and then the whole mix at this build. On 2026-09-21 that
// reported GET /health, identical code on both sides, as 93 percent faster on
// the branch, beyond the resolution band's 51 percent. The first explanation
// was a cold base measured first, and it did not survive measurement: a
// freshly brought up environment answered GET /health at a 4 millisecond
// median in its first five seconds, and ten comparisons on identical and on
// regressed code moved routes in BOTH directions by up to four fold. The cause
// was the noise between runs, which the single run band cannot see; see
// workload/compareresolution_rounds.go. So now:
//
//   - each side is first sent the same mix for a short warm-up that is
//     discarded, which takes the first request of every route, the one that
//     opens a connection and fills a cache, out of the numbers;
//   - then each side is sent the mix in sixteen short rounds, interleaved so
//     that neither side always goes first, with round k sent under the same
//     seed at both;
//   - each route's change and its interval are measured ROUND AGAINST ROUND
//     from those pairs, so the interval is as wide as the host's own noise
//     between rounds, and the verdict places that interval against the limit
//     by the rule #540 settled;
//   - each side's rounds are also pooled by load.Merge, from the samples and
//     never by averaging percentiles, for the run wide measures.
//
// WHAT IT STILL CANNOT CONTROL, said here rather than left for a reader to
// discover. The rounds are sequential, not simultaneous, because two
// environments sending traffic at once on one host would contend with each
// other and measure that instead. Interleaving cancels a STEADY drift; it does
// not cancel a neighbour on the host that spikes during one round. The seed
// makes the request sequence identical and makes none of that identical. Every
// result this returns carries those sentences, and af load compare prints them.

// loadBaselineSuffix distinguishes this comparison's base environment from the
// oracle's and from the side_effect family's, so the three never share an
// EnvID and `af down --branch` can address any of them by hand after an
// interrupted teardown.
const loadBaselineSuffix = " (load baseline)"

// ErrLoadBaselineSameCommit is returned when the base resolves to the
// candidate's own HEAD. A branch level with its base is a legitimate state
// rather than a failure, so the caller reports "nothing to compare" instead of
// an error, exactly as the side_effect family does.
var ErrLoadBaselineSameCommit = errors.New(
	"the base and this change are the same commit, so there are not two builds to compare")

// LoadCompareOptions are the choices a caller makes.
type LoadCompareOptions struct {
	// Baseline and BaseRef choose the revision to compare against, with the
	// same vocabulary the oracle uses.
	Baseline schema.BaselineSource
	BaseRef  string
	// Duration, Scale and Seed are sent to BOTH sides unchanged. One field per
	// side would let a caller compare two different workloads and call the
	// answer a regression, so there is deliberately no way to express that.
	Duration time.Duration
	Scale    float64
	Seed     int64
	// SQL compares the concurrent SQL workload instead of the HTTP mix.
	//
	// A flag rather than a second method, because everything up to the moment
	// traffic is sent is the same and is the part worth getting right once:
	// one golden for both sides, the candidate up first so its golden is the
	// one to pin, the base environment stamped ephemeral before it comes up,
	// and a teardown registered before the first error is checked. A second
	// entry point would be a second copy of all of that, and the copy would be
	// the one that leaked an environment.
	SQL bool
	// Clients, Transactions and ThinkTime are the SQL workload's own knobs and
	// are sent to BOTH sides unchanged, for the same reason Duration and Scale
	// are. There is deliberately no per side field here either: a comparison
	// of eight clients against sixteen is a measurement of the concurrency,
	// not of the build.
	Clients      int
	Transactions int
	ThinkTime    time.Duration
	// Rounds is how many interleaved rounds each side is sent, zero meaning
	// DefaultCompareRounds. One reproduces the old single pass, base then this
	// build, which is kept reachable on purpose: it is the arm that shows what
	// the interleaving is for.
	Rounds int
	// Warmup is how long each side is sent the mix before anything is
	// recorded, zero meaning DefaultCompareWarmup. NoWarmup turns it off,
	// which is separate from zero so that "the caller did not say" and "the
	// caller said none" cannot be the same value.
	Warmup   time.Duration
	NoWarmup bool
	// Keep leaves the base environment running, for looking at a difference by
	// hand.
	Keep bool
	// TTL bounds the base environment's lifetime for the reaper, so a base env
	// orphaned by a crashed run is collected within the hour rather than
	// living the manifest's day.
	TTL time.Duration
	// Progress receives a line per stage.
	Progress func(string)
}

// LoadCompareResult is both runs and the provenance of the base side.
type LoadCompareResult struct {
	// Baseline and Candidate are the two load results. Both are present only
	// when both runs completed; a nil Baseline with no error is not a state
	// this returns, because a comparison against an absence is not a
	// comparison and reporting it as one is the defect this file exists to
	// avoid.
	Baseline  *load.Result
	Candidate *load.Result
	// RefusedBaseline and RefusedCandidate are the routes each side declined
	// to send as unsafe. Kept per side because a manifest change in the branch
	// can make the two lists differ, and that difference explains a route
	// present on one side only.
	RefusedBaseline  []load.Route
	RefusedCandidate []load.Route
	// Rev is the base revision compared against and How is how that ref was
	// resolved, for the note the report writes.
	Rev string
	How string
	// CandidateRev is this build's own HEAD, so a reader can name both sides.
	CandidateRev string
	// Golden is the database version BOTH sides branched from. The single most
	// important field for deciding whether a difference is worth anything.
	Golden string
	// BaselineBranch is the base environment's branch name, which
	// `af down --branch` takes if a teardown was interrupted.
	BaselineBranch string
	// BaselineTornDown reports the base environment was removed. False with no
	// error is a leak the caller names, with the exact command to finish it.
	BaselineTornDown bool
	// BaselineRounds and CandidateRounds are each side's rounds before they
	// were pooled, in round order, so that round k of each is a pair sent the
	// same request sequence back to back. The per route change and its
	// interval are measured on these pairs, not on the pooled results.
	BaselineRounds  []*load.Result
	CandidateRounds []*load.Result
	// Rounds, RoundDuration and Warmup are how each side was sent, for the
	// notes and for a reader deciding whether to believe a difference.
	Rounds        int
	RoundDuration time.Duration
	Warmup        time.Duration

	// SQL says the concurrent SQL workload was compared rather than the HTTP
	// mix, which decides which pair of result fields carries the measurement.
	// A boolean rather than "whichever pair is not nil", because a comparison
	// that failed before either side ran has both pairs nil and still has to
	// report which question it was asking.
	SQL bool
	// BaselineSQL and CandidateSQL are the two pooled SQL results, and
	// BaselineSQLRounds and CandidateSQLRounds each side's rounds before they
	// were pooled, in round order. The same shape and the same rule as the
	// four fields above: round k of each is a pair sent the same transaction
	// sequence back to back.
	BaselineSQL        *sqlload.Result
	CandidateSQL       *sqlload.Result
	BaselineSQLRounds  []*sqlload.Result
	CandidateSQLRounds []*sqlload.Result
	// Clients, ThinkTime and RoundTransactions are how the SQL workload was
	// sent, resolved once and used on both sides. RoundTransactions is the per
	// client transaction bound ONE ROUND carried, zero when the rounds were
	// bounded by time alone.
	Clients           int
	ThinkTime         time.Duration
	RoundTransactions int
	// SQLSource is where the mix came from, declared or statement_statistics,
	// and SQLDescription what a declared document calls itself. Carried
	// because "both sides ran the same mix" is the claim the whole comparison
	// rests on, and naming the mix is the cheapest part of supporting it.
	SQLSource      string
	SQLDescription string
	// Notes are what this comparison could not control, always populated.
	Notes []string
}

// LoadCompare brings a second environment up from the base revision, pinned to
// the candidate's golden, sends the same mix at both under the same seed, and
// returns both results.
//
// Teardown of the base environment is reliable on every path: a deferred Down
// registered BEFORE the first error is checked, because a failed Up leaves
// resources behind and those are exactly the ones to remove, and the
// environment is stamped ephemeral before Up so that a process killed before
// the defer runs still leaves a husk the reaper collects.
//
// The CANDIDATE environment is never torn down, whether or not this call
// brought it up. Somebody who had an environment open and ran this would
// otherwise lose it, and the environment is the expensive thing.
//
// It fails closed. A base that is the same commit, a ref that does not
// resolve, a base that would not come up, or a run that did not complete on
// either side all return an error and no comparison. The one thing this must
// never do is return one side and let a caller difference it against nothing.
func (o *Orchestrator) LoadCompare(
	ctx context.Context, opts LoadCompareOptions,
) (result *LoadCompareResult, rerr error) {
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {}
	}

	baselineSource := opts.Baseline
	if baselineSource == "" {
		baselineSource = schema.BaselineMergeBase
	}
	rev, how, err := resolveBaseline(o.opts.Root, baselineSource, opts.BaseRef)
	if err != nil {
		return nil, err
	}
	head := gitOutput(o.opts.Root, "rev-parse", "HEAD")
	if head != "" && head == rev {
		return nil, ErrLoadBaselineSameCommit
	}

	// The candidate first, so the golden it uses is the one to pin. A base
	// environment built before the candidate would have to guess, and a
	// scheduled refresh between the two would then separate them.
	progress("bringing this build up")
	candidate, err := o.Up(ctx)
	if err != nil {
		return nil, err
	}
	// The URL matters only to the HTTP mix. A SQL comparison talks to the
	// database directly and an environment that serves nothing over HTTP is a
	// perfectly good one to run transactions against, so refusing here on a
	// missing URL would refuse the comparison this flag exists for. What the
	// SQL side needs instead is a reachable database, and the mix resolver
	// establishes that before the first round rather than after the warm-up.
	if candidate.URL == "" && !opts.SQL {
		return nil, aferrors.Coded(aferrors.AFLOD010,
			"detail", "this build came up with no URL, so no traffic could be sent at it")
	}
	if candidate.Golden == "" {
		// Without a golden the two sides would branch different databases and
		// every latency difference would be a difference in how many rows each
		// side held. Refuse rather than measure that and call it a regression.
		return nil, aferrors.Coded(aferrors.AFLOD010,
			"detail", "this build's environment reported no golden, so the base "+
				"environment could not be pinned to the same rows and any "+
				"difference between the two would be a difference in their data")
	}

	tree, cleanTree, err := o.baselineTree(ctx, rev)
	if err != nil {
		return nil, err
	}
	defer cleanTree()

	baseline, err := o.baselineOrchestrator(tree, candidate.Golden, loadBaselineSuffix)
	if err != nil {
		return nil, err
	}
	// Before Up, because Up is what stamps the lifetime.
	baseline.MarkEphemeral(opts.TTL)

	result = &LoadCompareResult{
		Rev: rev, How: how, CandidateRev: head,
		Golden: candidate.Golden, BaselineBranch: baseline.opts.Branch,
		SQL: opts.SQL,
	}
	progress("bringing " + short(rev) + " up beside it as the base branch")
	baseEnv, upErr := baseline.Up(ctx)
	if !opts.Keep {
		defer func() {
			c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer cancel()
			if td, downErr := baseline.Down(c); downErr == nil {
				result.BaselineTornDown = true
				progress("the base environment is torn down, " +
					plural(td.Removed, "resource", "resources") + " removed")
			}
		}()
	}
	if upErr != nil {
		return result, fmt.Errorf("the base environment did not come up: %w", upErr)
	}
	if baseEnv.URL == "" && !opts.SQL {
		return result, errors.New(
			"the base environment came up with no URL, so no traffic could be sent at it")
	}

	if opts.SQL {
		return result, o.compareSQL(ctx, result, baseline, opts, progress)
	}

	// One duration and one scale, resolved ONCE from this build's manifest and
	// handed to both sides as explicit values. Leaving them zero let each side
	// resolve its own, and the base side resolves against the base revision's
	// manifest, so a branch that changed load.duration compared two different
	// workloads and called the answer a regression.
	total, scale := ResolveLoadRate(LoadOptions{Duration: opts.Duration, Scale: opts.Scale},
		o.opts.Manifest.Load)
	if total <= 0 {
		total = defaultLoadDuration
	}
	plan := comparePlanFor(opts, total)
	result.Rounds, result.RoundDuration, result.Warmup = plan.rounds, plan.perRound, plan.warmup

	sides := map[compareSide]*Orchestrator{sideBase: baseline, sideCandidate: o}
	send := func(ctx context.Context, side compareSide, d time.Duration, seed int64) (
		*load.Result, []load.Route, error,
	) {
		// CONCURRENCY IS EQUAL ON THE TWO SIDES BY ACCIDENT, and this is the
		// line where it would stop being one. LoadOptions carries no
		// concurrency field, so load.Run falls back to its own hard coded
		// twenty on both sides, and two sides that send at one rate through
		// one number of connections are comparable. The moment a concurrency
		// field is added to LoadOptions it must be resolved ONCE above, beside
		// the duration and the scale, and pinned here as an explicit value on
		// both sides. A comparison of twenty connections against forty
		// measures the connection count, not the build, and it would do it
		// silently: every number in the report would still be a number.
		//
		// No field is added here for it, because nothing in this comparison
		// needs to choose one, and a field that exists only so a comment can
		// point at it is the shape this repository keeps finding and calling
		// dead. The SQL side DOES choose a client count, and pins it; see
		// compareSQL.
		res, refused, err := sides[side].Load(ctx, LoadOptions{Duration: d, Scale: scale, Seed: seed})
		if err != nil {
			return nil, refused, fmt.Errorf("the mix against %s did not complete: %w", side, err)
		}
		return res, refused, nil
	}
	got, err := interleaved(ctx, plan, opts.Seed, send, load.Merge, progress)
	result.RefusedBaseline, result.RefusedCandidate = got.baseRefused, got.candRefused
	if err != nil {
		return result, err
	}

	result.Baseline, result.Candidate = got.base, got.cand
	result.BaselineRounds, result.CandidateRounds = got.baseRounds, got.candRounds
	result.Notes = loadCompareNotes(result)
	return result, nil
}

// DefaultCompareRounds is how many interleaved rounds each side is sent.
//
// Sixteen, from the width each count produced, measured rather than chosen.
// Each route's change is judged on the scatter of its per round ratios, so
// more rounds means more pairs to estimate that scatter from, even though
// each round is shorter and its own p95 rests on fewer requests. On
// 2026-09-21, identical code against main, 30 seconds a side, the median
// route's resolvable change at ninety percent per route was:
//
//	4 rounds    130 percent
//	8 rounds    101, 134 and 141 percent across three runs
//	16 rounds   50 percent
//
// and sixteen cost no measurable time over eight, 110 seconds a comparison
// either way. Sixteen also kept a steady drift out of a pooled p95 in the
// model: at four rounds either order left about seven percent, at eight about
// three hundredths of one. Every route of that mix still met both sides in at
// least two rounds at sixteen; a mix with more routes or less traffic reaches
// the point where a route misses rounds sooner, and says so per route.
const DefaultCompareRounds = 16

// DefaultCompareWarmup is how long each side is sent the mix, and discarded,
// before anything is recorded.
//
// Five seconds, from a probe rather than from taste. A freshly brought up
// environment was sent GET /health and GET /accounts/7/balance every hundred
// milliseconds for a minute from the moment `af up` returned. Its median in
// the first five seconds was 4.0 and 3.9 milliseconds, no slower than any
// later five, so there was no cold start to wait out. The one reading that
// stood out was the slowest single request of the first five seconds, 96.8
// milliseconds on the balance route against 21 to 80 in every later window:
// the first request of a route, the one that opens a connection and fills a
// cache. Five seconds sends every route of a small mix at least once and
// discards it. A longer warm-up measured nothing further worth discarding.
const DefaultCompareWarmup = 5 * time.Second

// defaultLoadDuration is what load.Run sends for when nothing names a
// duration, restated here because the comparison has to split a known total
// into rounds rather than let each round fall back on its own.
const defaultLoadDuration = 30 * time.Second

// comparePlan is how each side will be sent.
type comparePlan struct {
	rounds   int
	perRound time.Duration
	warmup   time.Duration
}

// comparePlanFor turns the options into a schedule. The total is the time
// each side is MEASURED for, the same as a single pass measured before rounds
// existed, so a comparison does not quietly send less traffic than it used to
// and lose resolution by being fixed.
func comparePlanFor(opts LoadCompareOptions, total time.Duration) comparePlan {
	rounds := opts.Rounds
	if rounds <= 0 {
		rounds = DefaultCompareRounds
	}
	warmup := opts.Warmup
	if warmup <= 0 {
		warmup = DefaultCompareWarmup
	}
	if opts.NoWarmup {
		warmup = 0
	}
	return comparePlan{rounds: rounds, perRound: total / time.Duration(rounds), warmup: warmup}
}

// compareSide is which environment a send is aimed at.
type compareSide int

const (
	sideBase compareSide = iota
	sideCandidate
)

func (s compareSide) String() string {
	if s == sideBase {
		return "the base branch"
	}
	return "this build"
}

// compareOrder is the schedule, the Thue Morse sequence: slot n goes to this
// build when n has an odd number of set bits, and to the base otherwise, so
// four rounds are base, this build, this build, base, this build, base, base,
// this build. Every pair of slots holds one of each side, so round k is one
// seed sent at both.
//
// What the order does and does not buy, measured rather than argued, because
// the first argument for it was wrong. Against base then this build every
// time, which put every drift on this build, any interleaving that alternates
// who goes first removes most of a steady drift: see DefaultCompareRounds.
// Thue Morse also balances the sum and the sum of squares of each side's slot
// positions (base 0, 3, 5, 6 and this build 1, 2, 4, 7: 14 and 14, 70 and 70),
// which keeps a drift out of the centre and the spread of each side. It does
// NOT keep a drift out of a pooled p95 any better than base, this build, this
// build, base does: a p95 lives in the extremes, the latest slot has to go to
// one side, and at four rounds the two orders left the same bias with opposite
// signs. That leftover shrinks with the number of rounds, not with the order,
// which is one reason the rounds are sixteen.
func compareOrder(rounds int) []compareSide {
	order := make([]compareSide, 0, 2*rounds)
	for n := 0; n < 2*rounds; n++ {
		if bits.OnesCount(uint(n))%2 == 1 {
			order = append(order, sideCandidate)
			continue
		}
		order = append(order, sideBase)
	}
	return order
}

// compareSender sends the mix at one side for a duration under a seed.
type compareSender[R any] func(ctx context.Context, side compareSide, d time.Duration, seed int64) (
	R, []load.Route, error)

// interleavedResult is each side's rounds, pooled.
type interleavedResult[R any] struct {
	base, cand               R
	baseRounds, candRounds   []R
	baseRefused, candRefused []load.Route
}

// interleaved warms both sides, runs the rounds in compareOrder, and pools
// each side's rounds with merge.
//
// Generic over what one round returns, so that the schedule, the discarded
// warm-up and the pooling can be tested against a host whose drift and cold
// start are known exactly, with sample slices standing in for load results.
// Production passes load.Merge; a test that needed two real environments to
// check an ordering would be a test nobody runs.
func interleaved[R any](
	ctx context.Context, plan comparePlan, seed int64, send compareSender[R],
	merge func(...R) (R, error), progress func(string),
) (interleavedResult[R], error) {
	var out interleavedResult[R]
	if plan.warmup > 0 {
		for _, side := range []compareSide{sideBase, sideCandidate} {
			progress(fmt.Sprintf("warming %s for %s, discarded", side, plan.warmup))
			// The warm-up's result is thrown away and its error is not: a
			// warm-up that could not reach the environment says the same
			// about every round that follows, and saying it now is cheaper
			// than after all of them.
			if _, _, err := send(ctx, side, plan.warmup, seed); err != nil {
				return out, err
			}
		}
	}
	rounds := map[compareSide][]R{}
	for i, side := range compareOrder(plan.rounds) {
		// Slots 2k and 2k+1 are round k, one of each side, and both get the
		// same seed, so the two sides are sent the same request sequence
		// round for round.
		k := i / 2
		progress(fmt.Sprintf("round %d of %d: sending the mix at %s", k+1, plan.rounds, side))
		res, refused, err := send(ctx, side, plan.perRound, seed+int64(k))
		if side == sideBase {
			out.baseRefused = refused
		} else {
			out.candRefused = refused
		}
		if err != nil {
			return out, err
		}
		rounds[side] = append(rounds[side], res)
	}
	out.baseRounds, out.candRounds = rounds[sideBase], rounds[sideCandidate]
	var err error
	if out.base, err = merge(rounds[sideBase]...); err != nil {
		return out, fmt.Errorf("pooling the base branch's rounds: %w", err)
	}
	if out.cand, err = merge(rounds[sideCandidate]...); err != nil {
		return out, fmt.Errorf("pooling this build's rounds: %w", err)
	}
	return out, nil
}

// loadCompareNotes says what the comparison could not control. Always at least
// two, because there always are at least two, and written from how THIS run
// was sent rather than from how runs are usually sent, so that a comparison
// made with `--rounds 1` or `--warmup 0s` does not borrow the sentences of one
// that was interleaved and warmed.
func loadCompareNotes(r *LoadCompareResult) []string {
	var notes []string
	if r.SQL {
		return sqlCompareNotes(r)
	}
	switch {
	case r.Rounds <= 1:
		notes = append(notes, "the two sides were measured once each, the base branch first "+
			"and this build second, so the second met a host the first had just warmed, and "+
			"that lands on this build every time rather than on either side by chance")
	case r.Rounds%2 == 1:
		notes = append(notes, fmt.Sprintf("each side was sent %d rounds of %s, interleaved "+
			"so that neither side always went first; with an odd number of rounds the two "+
			"sides' slots cannot be balanced, so a steady drift across the comparison is "+
			"reduced rather than cancelled", r.Rounds, r.RoundDuration))
	default:
		notes = append(notes, fmt.Sprintf("each side was sent %d rounds of %s, interleaved "+
			"in a balanced order, so a host warming or cooling steadily across the comparison "+
			"lands on both sides equally; it does not cancel a neighbour on the host that "+
			"spikes during one round, and the rounds are sequential rather than simultaneous "+
			"because two environments sending at once would contend with each other",
			r.Rounds, r.RoundDuration))
	}
	if r.Rounds >= 2 {
		notes = append(notes, "each route's change is measured round against round: the "+
			"p95 columns are each side's per round p95s averaged on a log scale, and the "+
			"interval around the change comes from how much the rounds disagreed with each "+
			"other, which includes the host's own noise between rounds; a route that too few "+
			"rounds sent on both sides is left unresolved rather than judged")
	} else {
		notes = append(notes, "with one round each, the only resolution available is the "+
			"single run band, which models how far a p95 could land from itself inside one "+
			"run and cannot see the noise between two runs; on the host this change was "+
			"measured on, that band labelled a build that differed by a comment as regressed")
	}
	if r.Warmup > 0 {
		notes = append(notes, fmt.Sprintf("each side was first sent %s of the same mix and "+
			"that was discarded, so neither side's numbers include a cold start", r.Warmup))
	} else {
		notes = append(notes, "no warm-up was sent, so an environment brought up for this "+
			"comparison carries its cold start, its empty caches and its unopened connections, "+
			"in its numbers")
	}
	notes = append(notes, "both sides branched the same golden "+short(r.Golden)+", so they "+
		"answered queries over the same rows, and round for round the seed made the request "+
		"sequence the same; neither makes the machine, the neighbours on the host or the time "+
		"of day the same")
	if len(r.RefusedBaseline) != len(r.RefusedCandidate) {
		notes = append(notes, fmt.Sprintf(
			"the two sides refused different numbers of routes as unsafe, %d against %d, "+
				"so a route present on one side only may be a manifest change rather than "+
				"a change in what the application serves",
			len(r.RefusedBaseline), len(r.RefusedCandidate)))
	}
	return notes
}
