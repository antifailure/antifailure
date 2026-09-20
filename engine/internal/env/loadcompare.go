package env

import (
	"context"
	"errors"
	"fmt"
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
// WHAT IT STILL CANNOT CONTROL, said here rather than left for a reader to
// discover. The two runs are sequential, because two environments sending
// traffic at once on one host would contend with each other and measure that
// instead. Sequential runs mean the second one meets a host the first one just
// warmed, and time of day moved between them. The seed makes the request
// sequence identical and makes none of that identical. Every result this
// returns carries those sentences, and af load compare prints them.

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

	// The base side first, then this one. A fixed order rather than an
	// arbitrary one, so that the bias it introduces is the same on every run
	// and can be measured: the identical build comparison in this product's
	// own tests is what says how large that bias is. The order is named in the
	// notes rather than left for a reader to infer.
	// side takes the orchestrator to send at, rather than closing over one, so
	// that both sides provably receive the SAME duration, scale and seed. A
	// version of this with two call sites each building their own LoadOptions
	// is one edit away from comparing two different workloads and calling the
	// answer a regression.
	sent := func(name string, at *Orchestrator) (*load.Result, []load.Route, error) {
		progress("sending the mix at " + name)
		res, refused, err := at.Load(ctx, LoadOptions{
			Duration: opts.Duration, Scale: opts.Scale, Seed: opts.Seed,
		})
		if err != nil {
			return nil, refused, fmt.Errorf("the mix against %s did not complete: %w", name, err)
		}
		return res, refused, nil
	}

	baseRes, baseRefused, err := sent("the base branch", baseline)
	result.RefusedBaseline = baseRefused
	if err != nil {
		return result, err
	}
	candRes, candRefused, err := sent("this build", o)
	result.RefusedCandidate = candRefused
	if err != nil {
		return result, err
	}

	result.Baseline, result.Candidate = baseRes, candRes
	result.Notes = loadCompareNotes(result)
	return result, nil
}

// loadCompareNotes says what the comparison could not control. Always at least
// two, because there always are at least two.
func loadCompareNotes(r *LoadCompareResult) []string {
	notes := []string{
		"the two runs are sequential rather than simultaneous, because two environments " +
			"sending traffic at once on one host would contend with each other: the base " +
			"branch ran first and this build ran second, so the second met a host the " +
			"first had just warmed",
		"both sides branched the same golden " + short(r.Golden) + ", so they answered " +
			"queries over the same rows, and the seed made the request sequence the same; " +
			"neither makes the machine, the neighbours on the host or the time of day the same",
	}
	if len(r.RefusedBaseline) != len(r.RefusedCandidate) {
		notes = append(notes, fmt.Sprintf(
			"the two sides refused different numbers of routes as unsafe, %d against %d, "+
				"so a route present on one side only may be a manifest change rather than "+
				"a change in what the application serves",
			len(r.RefusedBaseline), len(r.RefusedCandidate)))
	}
	return notes
}
