package env

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/load"
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
// ORDER AND COLD START, and why each side is sent in rounds. The first version
// sent the whole mix at the base branch and then the whole mix at this build.
// On 2026-09-21 that reported GET /health, identical code on both sides, as 93
// percent FASTER on the branch, beyond the resolution gate's own band of 51
// percent, and every other route the same way. The resolution gate models the
// noise inside one run and it modelled it correctly; this was not noise. The
// base environment had just been brought up for the comparison and was
// measured first and cold, while this build's environment had been serving for
// an hour. A bias that lands on one side every time cannot be averaged away by
// sending for longer. So now:
//
//   - each side is first sent the same mix for a warm-up that is discarded,
//     so a freshly branched database and a freshly started service are not
//     measured answering from cold caches and an empty connection pool;
//   - then each side is sent the mix in several short rounds in the Thue
//     Morse order, base, this build, this build, base, this build, base, base,
//     this build, so that a host warming or cooling across the comparison
//     lands on both sides equally instead of on whichever went second. See
//     compareOrder for why that order and not the obvious alternation;
//   - round k uses the same seed on both sides, so the two sides are still
//     sent the same request sequence round for round;
//   - each side's rounds are pooled back into one result by load.Merge, from
//     the samples and never by averaging percentiles, and everything after
//     that, the difference, the resolution and the verdict, is unchanged.
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
	// Rounds, RoundDuration and Warmup are how each side was sent, for the
	// notes and for a reader deciding whether to believe a difference.
	Rounds        int
	RoundDuration time.Duration
	Warmup        time.Duration
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
	if candidate.URL == "" {
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
	if baseEnv.URL == "" {
		return result, errors.New(
			"the base environment came up with no URL, so no traffic could be sent at it")
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
	result.Notes = loadCompareNotes(result)
	return result, nil
}

// DefaultCompareRounds is how many interleaved rounds each side is sent.
//
// Four, because four is the smallest count at which the Thue Morse order
// balances both the sum and the sum of squares of each side's slot positions,
// which is what keeps a drift out of the p95 and not only out of the mean. See
// compareOrder. Two balances the sum only.
const DefaultCompareRounds = 4

// DefaultCompareWarmup is how long each side is sent the mix, and discarded,
// before anything is recorded. See the evidence in comparePlanFor.
const DefaultCompareWarmup = 10 * time.Second

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
// build when n has an odd number of set bits, and to the base otherwise. For
// four rounds that is base, this build, this build, base, this build, base,
// base, this build.
//
// WHY NOT THE OBVIOUS ONE. The comparison judges a p95 of the POOLED samples,
// not a mean, and that changes which order is fair. Base then this build every
// time puts every drift on this build, which is the defect this file was
// changed for. The next obvious fix, base, this build, this build, base,
// repeated, balances the SUM of each side's slot positions, so a steady drift
// cancels out of the mean; but it gives the base both the earliest and the
// latest slot, 0 and 7 of 8, so the base's pooled distribution is wider than
// this build's and its tail, which is where a p95 lives, is inflated by the
// drift. That is the same "this build looks faster" in a smaller costume, and
// it was caught writing the test for it rather than on camera.
//
// The Thue Morse order balances the sum AND the sum of squares of the slot
// positions for four rounds (base 0, 3, 5, 6 and this build 1, 2, 4, 7: 14 and
// 14, 70 and 70), so a drift adds the same centre and the same spread to both
// sides, and the two extreme slots go one to each side. No schedule can make
// two pooled percentiles immune to drift exactly; this is the one that makes
// the leftover smallest. Every pair of slots still holds one of each side, so
// round k is still one seed sent at both.
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
// made with --rounds 1 or --warmup 0s does not borrow the sentences of one
// that was interleaved and warmed.
func loadCompareNotes(r *LoadCompareResult) []string {
	var notes []string
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
