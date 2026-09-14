// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// RDS deletes an instance asynchronously, and the listing keeps returning it,
// with its tags, while it goes.
//
// A live run on 2026-09-14 destroyed a branch and was refused the golden 0.5
// seconds later: the branch instance was still listed as deleting and still
// carried antifailure:kind=branch, so the golden read as branched and the
// refusal told the operator to tear down the environment they had just torn
// down. Every test before this one used a fake that deleted inside the call, so
// nothing could see it. The fake now lingers, the way RDS does.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/rds/fakerds"
	"github.com/antifailure/antifailure/engine/conformance"
)

func TestAGoldenIsDestroyableWhileItsDestroyedBranchIsStillListed(t *testing.T) {
	server := newFakeWith(t, fakerds.Options{
		AdminURL:      requirePostgres(t),
		Prefix:        "af_rds_" + randomSuffix(t) + "_",
		Region:        testRegion,
		Credentials:   testCredentials,
		DeleteLingers: 3,
	}, conformance.DefaultSeedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	var masked, verified int
	gv, err := p.RefreshGolden(ctx, spec(&masked, &verified, `{"scanner":"conformance","findings":0}`))
	require.NoError(t, err)
	b, err := p.Branch(ctx, gv.ID, "env_lingering_delete")
	require.NoError(t, err)
	require.NoError(t, p.Destroy(ctx, b))

	// Asserted, not assumed: without this the fake could stop lingering and
	// this test would pass while reaching nothing.
	name, _, _ := strings.Cut(b.ProviderRef, "#")
	tags, listed := server.TagsOf(name)
	require.True(t, listed, "the destroyed branch must still be listed, or this test does not reach the live case")
	require.Equal(t, "branch", tags["antifailure:kind"], "the lingering instance must still carry the branch tag")
	require.NoError(t, p.DestroyGolden(ctx, gv.ID),
		"the golden was refused while the branch just destroyed was still listed as deleting")
	goldens, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Empty(t, goldens, "the golden survived its own destroy")
}

// The refusal itself is intact: a branch that is actually live still stops the
// golden from being destroyed, which is what protects a running environment.
func TestAGoldenWithALiveBranchIsStillRefused(t *testing.T) {
	server := newFakeWith(t, fakerds.Options{
		AdminURL:      requirePostgres(t),
		Prefix:        "af_rds_" + randomSuffix(t) + "_",
		Region:        testRegion,
		Credentials:   testCredentials,
		DeleteLingers: 3,
	}, conformance.DefaultSeedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	var masked, verified int
	gv, err := p.RefreshGolden(ctx, spec(&masked, &verified, `{"scanner":"conformance","findings":0}`))
	require.NoError(t, err)
	_, err = p.Branch(ctx, gv.ID, "env_live_branch")
	require.NoError(t, err)

	err = p.DestroyGolden(ctx, gv.ID)
	require.Error(t, err, "a golden with a live branch was destroyed")
	require.Contains(t, err.Error(), "is still branched by the environment env_live_branch")
}

// A branch limit must not count a branch that RDS is still deleting, or an
// environment cannot be replaced until the old one finishes going away.
func TestABranchLimitDoesNotCountABranchOnItsWayOut(t *testing.T) {
	server := newFakeWith(t, fakerds.Options{
		AdminURL:      requirePostgres(t),
		Prefix:        "af_rds_" + randomSuffix(t) + "_",
		Region:        testRegion,
		Credentials:   testCredentials,
		DeleteLingers: 3,
	}, conformance.DefaultSeedSQL)
	ctx := context.Background()
	opts := options(t, server)
	opts.MaxBranches = 1
	p, err := scopedNew(ctx, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	var masked, verified int
	gv, err := p.RefreshGolden(ctx, spec(&masked, &verified, `{"scanner":"conformance","findings":0}`))
	require.NoError(t, err)
	first, err := p.Branch(ctx, gv.ID, "env_first")
	require.NoError(t, err)
	require.NoError(t, p.Destroy(ctx, first))

	_, err = p.Branch(ctx, gv.ID, "env_second")
	require.NoError(t, err, "the limit counted a branch that was already being deleted")
}
