// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// Why this provider's conformance run does not assert a real service.
//
// engine/conformance/verdict.go gives one behaviour a third verdict.
// CopyOnWrite_BranchTimeMatchesTheDeclaration decides by stopwatch whether
// branch time grows with the data, and a stopwatch is only as good as the
// storage underneath the run. Over one local Postgres the only way a fake
// control plane can hand back a branch carrying the golden's data is
// CREATE DATABASE ... TEMPLATE, which copies files. This provider declares
// CopyOnWrite FALSE, so that harness would PASS it comfortably, on any machine,
// for a reason that has nothing to do with Amazon. A green bought that way is
// the unexamined pass this repository keeps finding. So the suite reports the
// behaviour as unproven for any run that does not assert Options.RealService,
// and this package does not assert it.
//
// No AWS account was available to the people who wrote this provider, so no
// run anywhere has timed a real RDS snapshot restore. The declaration false is
// AWS's documented mechanism, a restore that hydrates a new volume from the
// snapshot, and the ledger records it as unproven rather than as measured.
//
// The two assertions are complementary and neither covers the other. The first
// is a fact about the harness: the fake really does move the bytes, so an
// assertion that this run drives RDS's storage would be false. The second is a
// fact about the wiring: the suite really does leave the field empty, so the
// verdict really is unproven rather than measured.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/conformance"
)

// TestTheFakeControlPlaneReallyCopies is the evidence that asserting a real
// service here would be false.
//
// It reads the fake's own byte counter across one branch. The counter is not a
// declaration: fakerds adds what pg_database_size reported for the snapshot's
// database immediately before the CREATE DATABASE that copied it, so a
// non-zero reading is bytes Postgres actually moved.
//
// A branch rather than a golden refresh, because the branch is the operation
// the copy on write claim is about, and it is the operation the conformance
// behaviour times.
func TestTheFakeControlPlaneReallyCopies(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	version := refresh(t, p)

	// Reset after the golden so that the reading below is one branch's cost
	// rather than the whole run's. Building a golden restores and snapshots
	// twice, so without this the assertion would pass on bytes the branch
	// never moved.
	server.Reset()

	_, err := p.Branch(context.Background(), version.ID, "env_really_copies")
	require.NoError(t, err)

	require.Positive(t, server.BytesCopied(),
		"this fake control plane branched without moving a byte, so the reason this "+
			"suite leaves conformance.Options.RealService empty no longer holds. Either "+
			"the fake stopped copying, or the branch stopped carrying the golden's data, "+
			"which is a much worse defect")
}

// TestTheConformanceRunDoesNotAssertARealService is the other half.
//
// The suite's default is unproven and forgetting the field produces it, so this
// asserts the field STAYS empty rather than that somebody remembered to leave
// it so. A later run pointed at a real account has to change this test, and
// changing it is where the paragraph above gets read again.
func TestTheConformanceRunDoesNotAssertARealService(t *testing.T) {
	require.Empty(t, conformanceOptions().RealService,
		"this suite claims to drive a real service. The control plane it drives is "+
			"fakerds and the Postgres under it is local, so the copy on write behaviour "+
			"would be decided by a measurement of that Postgres and published as a "+
			"measurement of RDS. See TestTheFakeControlPlaneReallyCopies for what the "+
			"stopwatch would actually be timing")
}
