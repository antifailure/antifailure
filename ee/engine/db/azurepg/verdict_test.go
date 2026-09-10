// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg_test

// Why this provider's conformance run does not assert a real service.
//
// This package's relationship to the copy on write verdict is the OPPOSITE of
// aurora's and cloudsql's, and the difference is worth reading before assuming
// this file is a copy of theirs.
//
// Those two declare CopyOnWrite true, and their reason for leaving RealService
// empty is that a fake copying bytes would make the stopwatch refuse a truthful
// claim. THIS provider declares CopyOnWrite FALSE, so the suite would require
// the opposite proof: that branch time GROWS with the size of the database.
// Against this fake it would, comfortably, because CREATE DATABASE ... TEMPLATE
// really does copy every byte. And that pass would be worth nothing, because it
// would be a measurement of Postgres file copying published as a measurement of
// an Azure point in time restore.
//
// So the field stays empty for a reason that is the mirror image of the other
// two: not "the fake would fail a true claim" but "the fake would PASS a claim
// it has not tested". A false capability that a fake confirms is the more
// dangerous of the two, because nobody goes looking for why a passing assertion
// passed.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTheFakeControlPlaneReallyCopies is the evidence that a measurement here
// would be about Postgres rather than about Azure.
//
// The counter is not a declaration: fakeazurepg increments it with what
// pg_database_size reported for the template immediately before the CREATE
// DATABASE that copied it, so a non-zero reading is bytes Postgres actually
// moved.
func TestTheFakeControlPlaneReallyCopies(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	version, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)

	// Reset after the golden so the reading below is one branch's cost rather
	// than the whole run's.
	server.Reset()

	_, err = p.Branch(ctx, version.ID, "env_really_copies")
	require.NoError(t, err)

	require.Positive(t, server.BytesCopied(),
		"this fake control plane branched without moving a byte, so the branch is not "+
			"carrying the golden's data and the isolation behaviours are measuring "+
			"nothing")
}

// TestTheConformanceRunDoesNotAssertARealService is the other half.
//
// The suite's default is unproven and forgetting the field produces it, so this
// asserts the field STAYS empty rather than that somebody remembered to leave
// it so. A later lane pointing this suite at a real subscription has to change
// this test, and changing it is where the argument above gets read again.
func TestTheConformanceRunDoesNotAssertARealService(t *testing.T) {
	require.Empty(t, conformanceOptions().RealService,
		"this suite claims to drive a real service. The control plane it drives is "+
			"fakeazurepg and the Postgres under it is local, so the copy on write "+
			"behaviour would be decided by timing CREATE DATABASE ... TEMPLATE and "+
			"published as a measurement of an Azure point in time restore. Because this "+
			"provider declares CopyOnWrite false, that measurement would PASS, which is "+
			"the more dangerous direction: nobody investigates why a passing assertion "+
			"passed")
}
