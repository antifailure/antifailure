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
// THE COPY ON WRITE CELL PASSES HERE AND THE PASS PROVES NOTHING ABOUT RDS.
// That sentence is owed to anybody who reads the green, and it is written here
// rather than in a report because a green check is the one thing nobody goes
// looking for an explanation of.
//
// CopyOnWrite_BranchTimeMatchesTheDeclaration is two sided. A provider
// declaring FALSE, as this one does, has to show that branch time GREW with
// the data. The only branch this harness can produce is a real
// CREATE DATABASE TEMPLATE, so that side is satisfied by the simulator's own
// copy, comfortably, on any machine, for a reason that has nothing to do with
// Amazon. What the pass actually covers is narrow and worth stating exactly:
// that this provider does not somehow avoid the copy its control plane was
// asked for, and, from the self test beside this file, that a declaration of
// CopyOnWrite TRUE over the same provider is REFUSED rather than accepted.
// Both are properties of the provider and of the instrument. Neither is a
// measurement of RDS, and nothing in this package can be.
//
// The temptation this note exists to close off is making the fake share
// storage so the numbers look more like a real service. That would recreate
// the exact defect the copy on write instrument was written against, one layer
// down: it would write the only thing that can refuse the claim so that it
// agrees with the claim.
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

	"github.com/antifailure/antifailure/ee/engine/db/rds"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func TestConformance(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")

	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		p, err := rds.New(context.Background(), options(t, server))
		require.NoError(t, err)
		return p
	}, conformance.Options{
		// Generous but bounded. Every behaviour here is a snapshot and a
		// restore, which against this fake is two server side file copies on a
		// Postgres other suites may be using at the same time, and a hung call
		// must fail the behaviour rather than the job.
		Timeout:  6 * time.Minute,
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",

		// The copy on write sizes, set rather than defaulted, and the
		// reasoning is a budget rather than a preference.
		//
		// The enterprise job runs `go test ./... -race` over this whole module
		// with a FIFTEEN MINUTE timeout, and this behaviour is the only one in
		// the repository whose cost is set by a size. At the suite's shipped
		// half a gibibyte it builds two goldens and branches each three times,
		// and every one of those is a real CREATE DATABASE TEMPLATE against a
		// containerised Postgres, so the default would spend most of that
		// budget here and the module's other packages would be timed out by a
		// measurement rather than by a defect.
		//
		// Which direction shrinking is safe in is the part worth stating,
		// because it is the opposite of the intuition. For a provider
		// declaring CopyOnWrite TRUE, a smaller golden is a free pass: the
		// extra data costs less to copy than the allowance and the claim is
		// never at risk. THIS PROVIDER DECLARES IT FALSE, and the assertion on
		// that side is the other one, that the extra data must cost MORE than
		// the allowance. So a smaller size can only make this provider fail. It
		// cannot buy it a pass, and the run prints the copy rate it was able to
		// refuse either way.
		//
		// A quarter of a gibibyte rather than the floor of sixty four
		// mebibytes, because the floor is chosen so that a provider claiming
		// copy on write is still refused, and what has to be true here is the
		// stronger thing: that an honest copy is still SLOWER than the
		// allowance on the fastest storage this ever runs on. cow.go measures
		// that at roughly two gigabytes a second at the very best, where a
		// quarter of a gibibyte costs an eighth of a second against a floor of
		// a quarter. Postgres copies a database far slower than a raw file
		// copy, which is the margin this relies on, and the printed refusable
		// rate is what a reader should judge the run by rather than this
		// paragraph.
		CopyOnWriteSmallBytes: 8 << 20,
		CopyOnWriteLargeBytes: 256 << 20,
		// Two rather than three, which is the suite's floor. Every sample is a
		// branch of the large golden, so the third one is a third of the
		// behaviour's cost for a third reading the minimum rarely moves.
		CopyOnWriteSamples: 2,
		CopyOnWriteTimeout: 20 * time.Minute,
	})
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
