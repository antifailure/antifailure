// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// The shared database conformance suite, run against the RDS provider.
//
// Every line of the provider runs. What is replaced is the thing on the other
// end of the socket: the RDS control plane is fakerds and the Postgres behind
// it is real, so the behaviours that are claims about bytes are checked
// against bytes rather than against a fake's opinion of them.
//
// WHAT THIS IS EVIDENCE OF, stated here rather than left to be inferred.
//
// It is evidence that the provider's logic is right: that a golden is masked
// before it is verified and only snapshotted if verification passed, that a
// branch holds the golden's rows and is isolated from the golden and from
// other branches, that branching an unverified or missing golden is refused
// with the code the engine knows, that the declared limit is enforced rather
// than hung on, that destroy removes and destroying twice succeeds, that
// health reports a removed branch as gone rather than erroring, that the
// master password is rotated away from the one the restore inherited, and that
// the provider leaks neither an instance nor a snapshot across a whole run.
//
// THE COPY ON WRITE CELL IS UNPROVEN HERE, AND THAT IS THE HONEST ANSWER.
// CopyOnWrite_BranchTimeMatchesTheDeclaration decides by stopwatch, and the only
// branch this harness can produce is a real CREATE DATABASE TEMPLATE, so a
// declaration of FALSE, which is this provider's, would pass on the simulator's
// own copy for a reason that has nothing to do with Amazon. The suite therefore
// withholds that verdict for any run that does not assert Options.RealService,
// and conformanceOptions below does not assert it. verdict_test.go carries the
// evidence that asserting it would be false.
//
// The temptation this note exists to close off is making the fake share
// storage so the numbers look more like a real service. That would write the
// only thing that can refuse a copy on write claim so that it agrees with the
// claim.
//
// It is NOT evidence that AWS accepts these requests. Nothing here has an
// account and nothing here should: section 10 of the plan says no test may
// need one. What stands between this suite and a real RDS is that the request
// shapes are what the RDS query API documents and the signature is recomputed
// and compared by the fake, and neither of those is the same as AWS having
// answered. It also CANNOT produce a wall clock number for a real RDS snapshot
// and restore: what the copy on write behaviour times here is a local
// CREATE DATABASE TEMPLATE, and RDS moves the same bytes through S3 while
// provisioning an instance. The provider's own package comment carries that
// sentence too, because a reader who finds it only in a test file has already
// been misled.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func TestConformance(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")

	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		p, err := scopedNew(context.Background(), options(t, server))
		require.NoError(t, err)
		return p
	}, conformanceOptions())
}

// conformanceOptions is how this package runs the shared suite.
func conformanceOptions() conformance.Options {
	return conformance.Options{
		// Generous but bounded. Every behaviour here is a snapshot and a
		// restore, which against this fake is two server side file copies on a
		// Postgres other suites may be using at the same time, and a hung call
		// must fail the behaviour rather than the job.
		Timeout:  6 * time.Minute,
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
		// RealService is deliberately NOT set, and its absence is the whole of
		// what makes CopyOnWrite_BranchTimeMatchesTheDeclaration report
		// unproven rather than a measured verdict. No AWS account was
		// available, so no run has timed a real RDS restore. See
		// verdict_test.go and engine/conformance/verdict.go.
	}
}

// TestSweepLeftovers removes anything a killed run left behind.
//
// Separate from the suite on purpose: a failing behaviour legitimately leaves
// things behind for inspection, and a sweep that ran automatically would
// destroy the evidence. It is pointed at a fake rather than at an account,
// which makes it a check that the sweep code is reachable rather than a
// cleanup anybody needs; the fake's own Close is what removes the databases.
func TestSweepLeftovers(t *testing.T) {
	if os.Getenv("AF_RDS_SWEEP") == "" {
		t.Skip("skipped: set AF_RDS_SWEEP=1 to remove what a killed run left")
	}
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	items, err := p.Inventory(ctx)
	require.NoError(t, err)
	for _, r := range items {
		t.Logf("holding %s (%s)", r.ID, r.Kind)
	}
}
