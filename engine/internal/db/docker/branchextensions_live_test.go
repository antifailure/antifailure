package docker_test

// THE DECLARED EXTENSIONS HAVE TO REACH THE PATH EVERYBODY TAKES.
//
// TestAnExtensionTheImageDoesNotCarryIsRefusedByName, in the file beside this
// one, proves the refusal on the path that builds a golden. That is the path
// nobody is on twice. A project builds its first golden once and branches it
// for the rest of its life, and every branch after the first was reached by
// Branch, which read the golden image and started a container from it and
// asked the manifest's extension list nothing at all.
//
// So the guarantee was inverted: `af up` on a fresh project refused an
// extension the image lacks, and `af up` on a branch of a project that already
// had a golden returned 0 with the extension simply absent. Adding
// `extensions:` to a manifest is overwhelmingly done on a branch, because that
// is where the migration needing the extension is being written, and the
// golden's identity does not depend on the list, so the existing golden is
// still selected and nothing rebuilds. A check that fires on the first golden
// and never on a branch is close to no check.
//
// Two tests because the failure has two faces and they are opposites. When the
// image HAS the extension, the branch was silently missing something the
// manifest declared and every query using it fails later for a reason nobody
// connects to the manifest. When the image does NOT have it, the run was
// green and wrong, and the refusal the product advertises never happened.
//
// Both read pg_extension off the server rather than asking the provider what
// it did, for the reason the whole of extensions_live_test.go is written that
// way: a provider reporting that it created an extension is a provider
// reporting on its own intentions.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// stockImage is what a manifest that names no image gets, and the image both
// tests here build their golden in.
const stockImage = "postgres:17-alpine"

// TestAnExtensionDeclaredAfterTheGoldenExistsReachesTheBranch.
//
// The golden is built by a provider that declares nothing, which is the state
// of every project whose manifest gained `extensions:` after its first `af up`.
// The second provider is the same project one commit later. Its branch has to
// carry pg_trgm, and the only acceptable evidence is the server's own catalog.
//
// pg_trgm rather than something exotic: it is a contrib module, so the stock
// image has it, and this test is about whether the list is consulted at all
// rather than about any particular extension.
func TestAnExtensionDeclaredAfterTheGoldenExistsReachesTheBranch(t *testing.T) {
	requireImage(t, stockImage)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// The manifest as it was when the golden was made: no extensions.
	before := newExtensionProvider(t, dockerdb.Options{Version: 17, PortFrom: 48900})
	gv := before.refresh(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "branchext1", Provenance: "branch-extensions-present",
	})

	// The same golden, read by the manifest as it is now.
	after := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 48910, Extensions: []string{"pg_trgm"},
	})
	b := after.branch(ctx, gv.ID, "env_branchext1")
	conn := after.open(ctx, b)

	require.Equal(t, 1,
		scan[int](t, conn, `SELECT count(*) FROM pg_extension WHERE extname = 'pg_trgm'`),
		"the branch does not carry the extension the manifest declares, so every statement "+
			"that needs it fails against an environment the run called ready")

	// And the extension is usable rather than merely present in the catalog,
	// which is the difference between a row and a working environment.
	require.InDelta(t, 1.0,
		scan[float64](t, conn, `SELECT similarity('antifailure', 'antifailure')::float8`),
		0.0001,
		"the extension is in the catalog and its functions are not, so something other than "+
			"CREATE EXTENSION produced that row")
}

// TestAnExtensionTheImageDoesNotCarryIsRefusedOnABranchToo.
//
// The severe half. The refusal is one of this product's headline claims, and
// on the branch path it did not exist: the environment came up green, exit 0,
// and `SELECT count(*) FROM pg_extension WHERE extname = 'postgis'` answered 0.
//
// It asserts the same three things the golden path's refusal is asserted on,
// because a refusal that arrives on this path carrying less than that one does
// is a worse answer to the same question: the code, so `af explain` reaches the
// remedy about the image, the extension, and the image itself. "could not
// create extension" on its own sends somebody to look at their SQL.
func TestAnExtensionTheImageDoesNotCarryIsRefusedOnABranchToo(t *testing.T) {
	requireImage(t, stockImage)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	before := newExtensionProvider(t, dockerdb.Options{Version: 17, PortFrom: 48920})
	gv := before.refresh(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "branchext2", Provenance: "branch-extensions-absent",
	})

	// postgis is the one people actually reach for and the stock image has
	// never carried it.
	after := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 48930, Extensions: []string{"postgis"},
	})
	// Branch rather than the helper, because the helper fails the test on an
	// error and an error is what this wants. The branch it returns is torn
	// down either way: a refusal must not leak the container it refused on.
	b, err := after.p.Branch(ctx, gv.ID, "env_branchext2")
	t.Cleanup(func() {
		clean, cancelClean := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancelClean()
		_ = after.p.Destroy(clean, b)
	})

	require.Error(t, err,
		"a branch came up missing an extension the manifest declares, and the run reported it ready")
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB040)),
		"the refusal did not carry the code whose remedy is about the image")
	require.Contains(t, err.Error(), "postgis", "the refusal does not name the extension")
	require.Contains(t, err.Error(), stockImage, "the refusal does not name the image")
}
