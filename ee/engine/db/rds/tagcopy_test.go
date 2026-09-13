// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// A restore that copies tags must not turn a branch into a golden.
//
// The Azure provider's live run found a restored branch listed as a golden,
// because the restore carried the golden's tags onto the branch. AWS documents
// that a RestoreDBInstanceFromDBSnapshot naming its own tags applies only those,
// and this provider names its tags on every restore and snapshot, so the
// documented behaviour copies nothing. Documentation is not a run, so the fake
// is made to copy: once with the request's tags winning a clash, and once with
// the inherited tags winning, which nothing documents and is the worst case.

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/rds/fakerds"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func newTagCopyingFake(t *testing.T, mode fakerds.TagCopy) *fakerds.Server {
	t.Helper()
	return newFakeWith(t, fakerds.Options{
		AdminURL:                   requirePostgres(t),
		Prefix:                     "af_rds_" + randomSuffix(t) + "_",
		Region:                     testRegion,
		Credentials:                testCredentials,
		RestoreCopiesSnapshotTags:  mode,
		SnapshotCopiesInstanceTags: mode,
	}, conformance.DefaultSeedSQL)
}

// With the golden's tags copied onto the branch underneath the branch's own,
// the branch is still a branch in every place a tag decides it.
func TestABranchThatInheritsTheGoldensTagsIsStillABranch(t *testing.T) {
	server := newTagCopyingFake(t, fakerds.TagCopyRequestWins)
	p := newProvider(t, server)
	ctx := context.Background()
	version := refresh(t, p)

	b, err := p.Branch(ctx, version.ID, "env_inherits_tags")
	require.NoError(t, err)

	// The fake really copied, or this test would pass against a restore that
	// inherits nothing and prove nothing about one that does.
	tags, ok := server.TagsOf(b.ProviderRef)
	require.True(t, ok)
	require.NotEmpty(t, tags["antifailure:attestation.1"],
		"the branch carries no inherited attestation, so the fake did not copy the golden's tags")

	goldens, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, goldens, 1, "a restore that copied tags listed %d goldens: %v", len(goldens), goldens)
	require.Equal(t, version.ProviderRef, goldens[0].ProviderRef)

	items, err := p.Inventory(ctx)
	require.NoError(t, err)
	kinds := make([]string, 0, len(items))
	for _, it := range items {
		kinds = append(kinds, it.Kind)
	}
	sort.Strings(kinds)
	require.Equal(t, []string{"instance/branch", "snapshot/golden"}, kinds,
		"the inventory reads the copied tags as something other than one branch and one golden")

	require.Error(t, p.DestroyGolden(ctx, version.ID),
		"the golden was destroyable while a branch restored from it exists, so the branch was not counted as a branch")

	connection, err := p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)
	require.NoError(t, reachable(connection.Reveal()))
}

// If a restore's inherited tags overrode the ones the request named, the
// branch would carry the golden's kind and the golden's receipt. It must then
// be refused rather than handed out, and the golden list must not grow.
func TestABranchWhoseRestoreKeptTheGoldensKindIsNeverHandedOut(t *testing.T) {
	server := newTagCopyingFake(t, fakerds.TagCopyInheritedWins)
	p := newProvider(t, server)
	ctx := context.Background()

	// Building the golden in this mode snapshots a candidate whose tags could
	// override the golden's own, and the refresh reads its publication back,
	// so it either publishes a real golden or refuses.
	var masked, verified int
	version, err := p.RefreshGolden(ctx, spec(&masked, &verified, `{"findings":0}`))
	if err != nil {
		goldens, listErr := p.ListGoldens(ctx)
		require.NoError(t, listErr)
		require.Empty(t, goldens, "a refresh that refused still published a golden")
		return
	}

	b, err := p.Branch(ctx, version.ID, "env_inherited_wins")
	if err == nil {
		tags, _ := server.TagsOf(b.ProviderRef)
		require.Equal(t, "branch", tags["antifailure:kind"],
			"a branch was handed out while it carries the kind %q", tags["antifailure:kind"])
	}
	_, connErr := p.ConnString(ctx, b, provider.ConnDirect)
	if err != nil {
		require.Error(t, connErr, "a branch whose preparation was refused still produced a connection string")
	}

	goldens, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, goldens, 1, "a restore whose inherited tags won listed %d goldens", len(goldens))
	require.Equal(t, version.ProviderRef, goldens[0].ProviderRef)
}
