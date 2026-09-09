// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora_test

// Why this provider's conformance run does not assert a real service.
//
// engine/conformance/verdict.go gives one behaviour a third verdict.
// CopyOnWrite_BranchTimeMatchesTheDeclaration decides by stopwatch whether a
// branch shares storage with its golden, and a stopwatch is only as good as the
// storage underneath the run. Over one local Postgres the only way a fake
// control plane can hand back a branch carrying the golden's data is
// CREATE DATABASE ... TEMPLATE, which copies files. So the measurement refuses
// a truthful CopyOnWrite true and passes a CopyOnWrite false comfortably, and
// both answers are about the harness rather than about Aurora. The suite
// therefore reports the behaviour as unproven for any run that does not assert
// Options.RealService, and this suite does not assert it.
//
// The rule is inverted on purpose: a fake does not opt out of anything, it
// simply never asserts. That inversion is what makes forgetting safe, and it
// leaves exactly one question for somebody reading this package in six months.
// Not "why did this fake ask to be excused" but "why does this provider not
// assert a real service", and the answer to that has to be a test rather than a
// comment. This file is that test, and verdict.go names it by name.
//
// The two assertions are complementary and neither covers the other. The first
// is a fact about the harness: the fake really does move the bytes, so an
// assertion that this run drives Aurora's storage would be false. The second is
// a fact about the wiring: the suite really does leave the field empty, so the
// verdict really is unproven rather than measured. A green first assertion with
// a broken second one is a fake that copies inside a run reporting a measured
// pass, which is precisely the outcome verdict.go exists to prevent.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTheFakeControlPlaneReallyCopies is the evidence that asserting a real
// service here would be false.
//
// It reads the fake's own byte counter across one branch. The counter is not a
// declaration: fakerds increments it with what pg_database_size reported for
// the template immediately before the CREATE DATABASE that copied it, so a
// non-zero reading is bytes Postgres actually moved and bytes Aurora would not
// have moved.
//
// A branch rather than a golden refresh, because the branch is the operation
// the copy on write claim is about, and it is the operation the conformance
// behaviour times.
func TestTheFakeControlPlaneReallyCopies(t *testing.T) {
	server := newFake(t, seedSQL, "")
	p := newProvider(t, server)
	ctx := context.Background()

	golden, _ := spec("c0w0c0w0")
	version, err := p.RefreshGolden(ctx, golden)
	require.NoError(t, err)

	// Reset after the golden so that the reading below is one branch's cost
	// rather than the whole run's. Refreshing the golden clones the source, so
	// without this the assertion would pass on bytes the branch never moved.
	server.Reset()

	_, err = p.Branch(ctx, version.ID, "env_really_copies")
	require.NoError(t, err)

	require.Positive(t, server.BytesCopied(),
		"this fake control plane branched without moving a byte, so the reason this "+
			"suite leaves conformance.Options.RealService empty no longer holds. Either "+
			"the fake stopped copying, in which case the copy on write behaviour could "+
			"honestly be measured here and RealService should be reconsidered, or the "+
			"branch stopped carrying the golden's data, which is a much worse defect")
}

// TestTheConformanceRunDoesNotAssertARealService is the other half.
//
// The suite's default is unproven and forgetting the field produces it, so this
// asserts the field STAYS empty rather than that somebody remembered to leave
// it so. A later lane pointing this suite at a real endpoint has to change this
// test, and changing it is where the sentence above gets read again.
func TestTheConformanceRunDoesNotAssertARealService(t *testing.T) {
	require.Empty(t, conformanceOptions().RealService,
		"this suite claims to drive a real service. The control plane it drives is "+
			"fakerds and the Postgres under it is local, so every service owned "+
			"behaviour would be decided by a measurement of that Postgres and published "+
			"as a measurement of Aurora. See TestTheFakeControlPlaneReallyCopies for "+
			"what the stopwatch would actually be timing")
}
