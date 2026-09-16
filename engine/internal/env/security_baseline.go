package env

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The side_effect security family compares what a change made against what the
// base branch made: three PaymentIntents where the base made one is the signal.
// That comparison needs a SECOND environment brought up from the base revision
// and driven with the same workflows, so both counts are of the same golden and
// the same drive. This file is that second environment.
//
// It is the oracle's baseline mechanism reused rather than reinvented. The
// oracle already brings a commit up beside the candidate, pinned to the
// candidate's golden, from a build context checked out with git archive; the
// only new work here is running the workflows against it, reading its captured
// effects, and refusing to report a comparison whose base was not faithfully
// measured. A base whose drive failed or whose effect logs could not be read is
// NOT a base of zero: reporting every candidate effect as new against it would be
// the exact "zero means did not measure" defect the family exists to avoid, so
// this fails closed to no baseline instead.

// ErrBaselineSameCommit is returned when the base resolves to the candidate's
// own HEAD, so there is no change between them and no base twin is worth
// building. The collector reads it as "nothing to compare" rather than as a
// failure, because a branch that is level with its base is a legitimate state,
// not a broken run.
var ErrBaselineSameCommit = errors.New(
	"the base and this change are the same commit, so there is no side effect to compare")

// BaselineTwinOptions are the choices af ci's security collector makes when it
// asks for a base twin.
type BaselineTwinOptions struct {
	// Golden pins the base twin's database branch to the candidate's golden, so
	// both sides hold the same rows and a side-effect difference is the code's
	// and not two different databases'. Required: an empty golden would let the
	// scheduled refresh separate the two sides, and then every row-driven effect
	// would differ for a reason that is not the change.
	Golden string
	// RunnerPath is the runner entry point the workflows and exploration drive
	// through, the same one the candidate used.
	RunnerPath string
	// TTL bounds the base environment's lifetime for the reaper, exactly as af
	// ci's MarkEphemeral bounds the candidate's, so a base env orphaned by a
	// crashed run is collected within the hour rather than living the manifest's
	// day. Non-positive leaves the manifest's ttl in force.
	TTL time.Duration
	// BaseRef overrides the ref the base is resolved from. Empty resolves
	// origin/HEAD, origin/main and the rest, in the order resolveBaseline uses.
	BaseRef string
	// Limit is how many decisions and messages to read back. Zero reads the
	// default window, the same one the candidate's egress summary uses.
	Limit int
}

// BaselineTwin is what a base environment emitted, for the side_effect family to
// diff a candidate run against. Present only when the base was faithfully
// measured; BaselineTwin returns an error otherwise, which the collector reads
// as "no baseline" and turns into a note rather than a comparison.
type BaselineTwin struct {
	// Decisions is the base twin's egress decision log, and Messages its
	// captured message log, the two sources side_effect classifies.
	Decisions []local.Decision
	Messages  []local.Message
	// Rev is the base revision the twin was built from, and How is how that ref
	// was resolved, for the note the collector writes.
	Rev string
	How string
	// Branch is the base environment's branch name, which `af down --branch`
	// takes if a teardown was interrupted.
	Branch string
	// TornDown reports the base environment was removed. False with no error is a
	// leak the collector names, with the exact command to finish it by hand.
	TornDown bool
}

// baselineDefaultLimit is the decision and message window read back from the
// base twin when the caller names none. It matches securityLogLimit on the CLI
// side and the egress summary's own limit, so the base sees the same window of
// its run the candidate's report summarises.
const baselineDefaultLimit = 500

// BaselineTwin brings a second environment up from the base revision, pinned to
// the candidate's golden, drives it with the same exploration and workflows the
// candidate ran, and returns its captured effects for the side_effect family to
// diff. The candidate environment must already be up: its golden is passed in,
// and this only builds and tears down the base beside it.
//
// Teardown is reliable on every path. The base environment is removed by a
// deferred Down registered BEFORE the first error is checked, because a failed
// Up leaves resources behind and those are exactly the ones to remove, and it is
// stamped ephemeral before Up so that even a process killed before the defer
// runs leaves a husk the reaper collects within the run's budget rather than the
// manifest's day.
//
// It fails closed. A base that is the same commit, a base ref that does not
// resolve, a base that would not come up, a base whose workflows did not run, or
// a base whose effect logs could not be read all return an error and no bundle,
// so the collector leaves Input.Baseline ok=false and the increase comparison is
// simply not made. The one thing this must never do is return a bundle that
// undercounts the base, because that reads as effects the change added when it
// did not.
func (o *Orchestrator) BaselineTwin(ctx context.Context, opts BaselineTwinOptions) (result *BaselineTwin, rerr error) {
	if opts.Golden == "" {
		// Without the candidate's golden the two sides would branch different
		// databases, so refuse rather than build a comparison of two unrelated
		// row sets. A programming error in the caller, not a state a run reaches.
		return nil, errors.New(
			"a base twin needs the candidate's golden to pin its database branch, and none was given")
	}

	rev, how, err := resolveBaseline(o.opts.Root, schema.BaselineMergeBase, opts.BaseRef)
	if err != nil {
		return nil, err
	}
	head := gitOutput(o.opts.Root, "rev-parse", "HEAD")
	if head != "" && head == rev {
		return nil, ErrBaselineSameCommit
	}

	tree, cleanTree, err := o.baselineTree(ctx, rev)
	if err != nil {
		return nil, err
	}
	defer cleanTree()

	baseline, err := o.baselineOrchestrator(tree, opts.Golden, securityBaselineSuffix)
	if err != nil {
		return nil, err
	}
	// Before Up, because Up is what stamps the lifetime. This is the same
	// backstop af ci puts on the candidate: the normal path tears the base down
	// at the end of this call, and this only changes when the process dies first.
	baseline.MarkEphemeral(opts.TTL)

	result = &BaselineTwin{Rev: rev, How: how, Branch: baseline.opts.Branch}
	baseEnv, upErr := baseline.Up(ctx)
	// Deferred rather than a later statement, for the reason af ci and the oracle
	// both give: a statement after a failing one does not run, and a base
	// environment that outlives its comparison is the leak this product exists to
	// prevent. Registered before upErr is checked, because a failed Up leaves
	// resources behind and those are the ones to remove.
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if td, downErr := baseline.Down(c); downErr == nil {
			result.TornDown = true
			o.progress("the side-effect base twin is torn down, " +
				plural(td.Removed, "resource", "resources") + " removed")
		}
	}()
	if upErr != nil {
		return result, fmt.Errorf("the base twin did not come up: %w", upErr)
	}
	if baseEnv.URL == "" {
		return result, errors.New("the base twin came up with no URL, so nothing could be driven against it")
	}

	// The same drive the candidate ran, in the same order: exploration can submit
	// forms, so it runs before the workflows, and both provoke the outbound
	// effects the base is measured by. Their structured results are discarded;
	// only the effects they cause, read back below, are what the family counts.
	// Exploration failing is tolerated exactly as the candidate tolerates it, but
	// the workflows failing is not: a base whose workflows did not run made fewer
	// effects for a reason that is not the change, which would read as effects the
	// change added.
	if o.opts.Manifest.Explore != nil && o.opts.Manifest.Explore.Enabled {
		_, _ = baseline.Explore(ctx, ExploreOptions{RunnerPath: opts.RunnerPath})
	}
	_, testErr := baseline.Test(ctx, TestOptions{Attempts: 2, RunnerPath: opts.RunnerPath})

	limit := opts.Limit
	if limit <= 0 {
		limit = baselineDefaultLimit
	}
	decisions, dReadErr := baseline.Decisions(ctx, limit)
	messages, mReadErr := baseline.Messages(ctx, limit)
	if measureErr := baselineMeasured(testErr, dReadErr, mReadErr); measureErr != nil {
		return result, measureErr
	}
	result.Decisions, result.Messages = decisions, messages
	return result, nil
}

// baselineMeasured reports whether a base run's raw reads are a faithful
// measurement, returning the reason to skip the comparison when they are not.
//
// It is a pure function, split out of the lifecycle above, so the fail-closed
// rule that keeps a spuriously low base from reading as effects the change added
// can be tested without bringing two environments up. All three inputs must be
// nil for the base to count: the workflows must have run (testErr), and BOTH the
// decision and the message logs must have been read (dReadErr, mReadErr),
// because side_effect counts one class of effect from each log and a missing log
// undercounts exactly the classes it carries. A successful read that is empty is
// a measured base of nothing and is not a failure here; only an error is.
func baselineMeasured(testErr, dReadErr, mReadErr error) error {
	if testErr != nil {
		return fmt.Errorf("the base twin's workflows did not run, so its effect count is not comparable: %w", testErr)
	}
	if dReadErr != nil {
		return fmt.Errorf("the base twin's egress log could not be read, so its effect count is incomplete: %w", dReadErr)
	}
	if mReadErr != nil {
		return fmt.Errorf("the base twin's captured messages could not be read, so its effect count is incomplete: %w", mReadErr)
	}
	return nil
}
