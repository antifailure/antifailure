// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg_test

// A branch is a restore of a golden, and an Azure restore carries the source
// server's tags onto the new server. The live run on 2026-09-13 branched a
// golden and then listed two goldens with one version id: the golden, and the
// branch wearing the golden's tags. Every branch read as a golden, so listing,
// inventory and the check that keeps a referenced golden from being destroyed
// were all wrong about it.
//
// The fix follows the Aurora provider's precedent in ee/engine/db/aurora/aurora.go:
// an explicit kind tag, set to golden at publish and to branch on every branch,
// so that an inherited value is overwritten and listing selects on kind.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/azurepg/fakeazurepg"
)

func TestABranchIsNeverListedAsAGolden(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	version, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)
	branch, err := p.Branch(ctx, version.ID, "env_not_a_golden")
	require.NoError(t, err)

	tags, ok := server.TagsOf(branch.ProviderRef)
	require.True(t, ok)
	require.Emptyf(t, tags["antifailure-golden"],
		"the branch still carries the golden tag it inherited from the restore: %v", tags)

	listed, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1, "a branch was listed as a golden")
}

func TestAServerWithAnEnvironmentIsNeverAGolden(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	version, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)
	branch, err := p.Branch(ctx, version.ID, "env_wears_golden_tags")
	require.NoError(t, err)

	// The state a crash between the restore and the branch's preparation
	// leaves: the environment tag is there and so are the golden's.
	golden, ok := server.TagsOf(version.ProviderRef)
	require.True(t, ok)
	fakeazurepg.SetTagsForTest(server, branch.ProviderRef, map[string]string{
		"antifailure-golden":  golden["antifailure-golden"],
		"antifailure-version": golden["antifailure-version"],
	})

	listed, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1, "a server carrying an environment tag was listed as a golden")

	inventory, err := p.Inventory(ctx)
	require.NoError(t, err)
	kinds := map[string]string{}
	for _, r := range inventory {
		kinds[r.ID] = r.Kind
	}
	require.Equal(t, "branch", kinds[branch.ProviderRef], "a server carrying an environment tag was inventoried as a golden")

	require.Error(t, p.DestroyGolden(ctx, version.ID),
		"a golden with a live branch was destroyable because the branch looked like a golden")
}
