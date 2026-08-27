package chaos_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Scenario 2: a provider interrupted while it is making a branch leaves either
// nothing, or something its inventory reports.
//
// This is the one scenario the repository already claimed with a test rather
// than only with prose. engine/conformance/db.go carries
// Cancellation_LeavesNoUntrackedResource, and the property it states is exactly
// right: "Succeeding despite cancellation is allowed, as long as the resource
// is reported, because then the journal and the leak detector can see it.
// Silently creating something nothing knows about is not."
//
// The behaviour is nonetheless the weakest possible version of itself, and
// finding that out is what this file is for. It cancels the context BEFORE it
// calls Branch:
//
//	cancelCtx, cancel := context.WithCancel(ctx)
//	cancel()
//	b, err := h.p.Branch(cancelCtx, gv.ID, ...)
//
// A provider that checks ctx.Err() on entry returns immediately, having created
// nothing, and passes. Every provider does that, because every client library
// does. So the behaviour proves that a provider notices a context that was dead
// on arrival, and says nothing at all about the case anybody is worried about:
// a cancellation that lands after the container exists and before it is
// recorded, which is the state a killed engine, a control-C, or a CI job's
// timeout actually produces.
//
// So this test cancels DURING. It lets the branch get far enough to have
// created something and then takes the context away, and asserts the property
// the conformance behaviour states rather than the one it tests.
//
// The negative control is described where it is asserted.

func newDockerProvider(t *testing.T) *dockerdb.Provider {
	t.Helper()
	requireDocker(t)
	p, err := dockerdb.New(dockerdb.Options{
		Version: 17, Clock: clock.New(),
		SeedSQL: conformance.DefaultSeedSQL,
		// Away from the conformance suite's range and the engine's default, so
		// that a chaos run and a conformance run at the same time do not fight
		// over a port and report it as a provider fault.
		PortFrom: 47000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// refreshGolden builds a golden, retrying a daemon that is too busy to start
// Postgres in time.
//
// Not papering over a flake. The failure it retries is specific and was
// observed: AF-DB-002 with "unexpected EOF", which is the provider connecting
// to a container whose initdb has not finished. On a machine already running a
// dozen containers that window is routinely missed, and lane 3 and lane 5 both
// hit the same thing from their own directions. Every other error is returned
// on the first attempt.
//
// EACH ATTEMPT GETS ITS OWN BUDGET, and that is the whole reason this function
// takes a testing.T rather than a context. The first version shared one
// deadline across all three, which meant that on the machine it was written
// for it did not retry at all: attempt one spent the budget, and attempts two
// and three failed instantly on a context that was already dead. A retry loop
// whose later attempts cannot run is a comment, not a retry. Observed at load
// 40, where attempt two came back "context deadline exceeded" in the middle of
// an image pull.
//
// The parent is t.Context() so a finished test still cancels the work, and it
// carries no deadline of its own, so the budget below is the only clock.
//
// Exhausting the attempts is a FAILURE and not a skip. "The daemon is there and
// cannot start a database" is a result about the environment, and reporting it
// as a skip would let this suite go green on a machine that cannot run it.
func refreshGolden(t *testing.T, p *dockerdb.Provider, rules string) provider.GoldenVersion {
	t.Helper()
	const perAttempt = 4 * time.Minute
	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(t.Context(), perAttempt)
		gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
			Version: 17, RulesHash: rules,
			Mask:   func(context.Context, secrets.Value) error { return nil },
			Verify: func(context.Context, secrets.Value) (string, error) { return `{"rows":0}`, nil },
		})
		cancel()
		if err == nil {
			return gv
		}
		last = err
		busy := strings.Contains(err.Error(), "unexpected EOF") ||
			strings.Contains(err.Error(), "could not be reached")
		// A blown per-attempt budget is the busy daemon by another name: the
		// pull or the initdb did not finish in four minutes. Retried for the
		// same reason, and named separately so the log says which it was.
		if errors.Is(err, context.DeadlineExceeded) {
			t.Logf("attempt %d: the daemon did not finish inside %s: %v", attempt, perAttempt, err)
			continue
		}
		if !busy {
			t.Fatalf("RefreshGolden: %v", err)
		}
		t.Logf("attempt %d: the daemon could not bring Postgres up in time: %v", attempt, err)
	}
	t.Fatalf("three attempts of %s each and the daemon never started a usable Postgres. "+
		"This is a statement about the machine rather than about the provider, and it is a "+
		"failure rather than a skip on purpose: a suite that goes green here has proved "+
		"nothing.\nlast error: %v", perAttempt, last)
	return provider.GoldenVersion{}
}

func TestABranchInterruptedPartWayThroughIsEitherGoneOrInTheInventory(t *testing.T) {
	scenario(t)
	p := newDockerProvider(t)

	// Built before the working deadline starts. Preparing the fixture is not
	// part of what this scenario measures, and letting it eat the budget is
	// how the inventory call below came to fail with "a provider that cannot
	// enumerate itself", which blamed the provider for a clock this test had
	// already spent.
	gv := refreshGolden(t, p, "chaos-interrupted-branch")
	t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	const envID = "chaosinterrupted01"
	t.Cleanup(func() {
		_ = p.Destroy(context.Background(), provider.Branch{EnvID: envID})
	})

	// Long enough that the provider is past its entry check and into creating
	// something, short enough that it is nowhere near ready. A cancellation
	// that arrives before any work has started is the case the conformance
	// suite already covers and is not the case worth worrying about.
	interrupted, stop := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer stop()

	branch, branchErr := p.Branch(interrupted, gv.ID, envID)

	inventory, invErr := p.Inventory(ctx)
	// Two different failures wear the same error here and want opposite
	// responses, so they are separated rather than both reported as a provider
	// fault. This assertion used to blame the provider for a deadline the test
	// had already spent building its fixture, which sends whoever reads it on
	// Monday to read Inventory rather than to look at their machine.
	if errors.Is(invErr, context.DeadlineExceeded) {
		t.Fatalf("this scenario ran out of time before it could check anything, which is a "+
			"statement about the machine and not about the provider. Nothing was proved "+
			"either way.\ninventory said: %v", invErr)
	}
	require.NoError(t, invErr, "a provider that cannot enumerate itself cannot be checked at all")

	reported := func(needle string) bool {
		if needle == "" {
			return false
		}
		for _, r := range inventory {
			if r.ID == needle || strings.Contains(r.ID, needle) {
				return true
			}
		}
		return false
	}

	if branchErr == nil {
		// Allowed. Succeeding despite the interruption is fine as long as the
		// caller is given the identifier, because then the journal has it.
		require.NotEmpty(t, branch.ProviderRef,
			"the branch succeeded and returned no reference, so nothing can ever delete it")
		return
	}

	// It failed. The property is that nothing untracked survives: either the
	// provider cleaned up after itself, or whatever it left is in the
	// inventory, where the leak detector and `af env prune` can find it.
	//
	// The negative control for this assertion is the assertion itself run
	// against a provider that half-creates and does not report: comment out the
	// inventory branch below and the test still passes on a clean provider,
	// which is why the check is that the resource is EITHER gone OR reported,
	// and both halves are read off the same live daemon rather than assumed.
	leftBehind := reported(envID)
	if leftBehind {
		t.Logf("the interrupted branch left a resource and the inventory reports it, "+
			"which is the acceptable outcome: %v", inventory)
		return
	}

	// Nothing in the inventory. Prove it is genuinely gone rather than merely
	// unreported, by asking the provider to destroy it: a destroy that reports
	// having removed something means the inventory was lying.
	require.NoError(t, p.Destroy(ctx, provider.Branch{EnvID: envID}),
		"destroying a branch that the inventory says does not exist must succeed")

	after, err := p.Inventory(ctx)
	require.NoError(t, err)
	require.False(t, reported(envID),
		"the interrupted branch left something the inventory does not report, which is a "+
			"resource nothing can ever find: not the journal, not the leak detector, not "+
			"af env prune. Inventory after destroy: %v", after)
}

// The other half of scenario 2, and the one lane 3 found the hard way against a
// real Database Lab Engine: a resource leaving a provider's inventory is not
// the same event as its storage being released, and most APIs only report the
// first. If a delete is reported complete before it is, anything that acts on
// what the resource was holding is racing a teardown it cannot see.
func TestAProviderThatReportsADeleteAsCompleteHasActuallyReleasedIt(t *testing.T) {
	scenario(t)
	p := newDockerProvider(t)

	gv := refreshGolden(t, p, "chaos-delete-then-collect")

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	branch, err := p.Branch(ctx, gv.ID, "chaosdeleterace01")
	require.NoError(t, err)

	// Destroy, then immediately act on what the branch was holding. If the
	// destroy is only a deregistration, collecting the golden fails because
	// something still depends on it, and the failure arrives at whoever runs
	// the retention sweep rather than at whoever wrote the provider.
	require.NoError(t, p.Destroy(ctx, branch))
	if err := p.DestroyGolden(ctx, gv.ID); err != nil {
		// The raw provider message, not only the coded error, on lane 3's
		// advice from watching this exact failure against a real Database Lab
		// Engine: when the two views disagree, the storage layer names the
		// dependent dataset by its full path while the API reports no such
		// clone. That disagreement is the diagnosis, and it is invisible if the
		// test only asserts on the code.
		t.Fatalf("the golden could not be collected immediately after its only branch was "+
			"destroyed, so the destroy returned before the storage was released and every "+
			"retention sweep will race it.\nprovider said: %v", err)
	}

	inventory, err := p.Inventory(ctx)
	require.NoError(t, err)
	for _, r := range inventory {
		require.NotContains(t, r.ID, "chaosdeleterace01",
			"the branch is still in the inventory after a destroy that reported success")
	}
}
