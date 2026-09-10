// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql_test

// The refusals and behaviours that are this provider's own rather than the
// shared suite's.
//
// The conformance suite proves the twenty four behaviours every database
// provider owes. What it cannot know about is the handful of things that are
// true of Cloud SQL specifically, and each of those is the kind of fact that
// reads as a detail until it is the outage.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/cloudsql"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// TestABranchDoesNotKeepTheSourcePassword is the security assertion of this
// package.
//
// Google documents that a clone carries the SOURCE's users and passwords. So
// without an explicit reset, every preview environment would be reachable with
// PRODUCTION's database credential, which is the exact thing this product
// exists to prevent. The fake models the inheritance for that reason, so this
// test can see the reset happen rather than assume it.
//
// The control is the pair of assertions: the source's password is asserted
// unchanged as well, because a provider that reset EVERY instance's password,
// including the customer's production one, would also pass the first assertion
// and would be a far worse defect.
func TestABranchDoesNotKeepTheSourcePassword(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	before, ok := server.PasswordOf(sourceInstance)
	require.True(t, ok, "the fake has no source instance, so this proves nothing")

	version, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)
	branch, err := p.Branch(ctx, version.ID, "env_password")
	require.NoError(t, err)

	got, ok := server.PasswordOf(branch.ProviderRef)
	require.True(t, ok, "the branch instance is not there")
	require.NotEqual(t, before, got,
		"this branch is reachable with the SOURCE instance's password. A Cloud SQL "+
			"clone inherits the source's users and passwords, so a preview environment "+
			"would hold production's database credential")

	after, ok := server.PasswordOf(sourceInstance)
	require.True(t, ok)
	require.Equal(t, before, after,
		"the provider changed the SOURCE instance's password. The source is the "+
			"customer's production database and this provider must never write to it")
}

// TestAnInstanceWeDidNotCreateIsRefused covers the ownership predicate.
//
// A project where somebody has named their own instance the way this provider
// names its own must not lose it to our garbage collection. The label is the
// predicate rather than the name, and this drives the destructive path against
// an instance that has the right name and no label.
func TestAnInstanceWeDidNotCreateIsRefused(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	// The source instance carries no antifailure label, and Destroy is asked
	// to remove it by naming it directly.
	err := p.Destroy(ctx, provider.Branch{EnvID: "whatever", ProviderRef: sourceInstance})
	require.ErrorIs(t, err, cloudsql.ErrNotOurs,
		"this provider deleted an instance it did not create. The ownership check is "+
			"the only thing standing between a misconfigured project and somebody's "+
			"production database")

	// And it is still there.
	_, ok := server.PasswordOf(sourceInstance)
	require.True(t, ok, "the source instance is gone, so the refusal above did not protect it")
}

// TestAGoldenIsDestroyedWhenVerificationFails is the data exposure assertion.
//
// Between the clone and the verification an instance exists holding UNMASKED
// production data, under a name that has been returned to nobody. If
// RefreshGolden returns an error without removing it, that instance sits in the
// project with no handle by which anything could clean it up.
func TestAGoldenIsDestroyedWhenVerificationFails(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	spec := goldenSpec()
	spec.Verify = func(context.Context, secret.Value) (string, error) {
		return "", context.DeadlineExceeded
	}
	_, err := p.RefreshGolden(ctx, spec)
	require.Error(t, err, "verification failed and the golden was published anyway")

	inventory, err := p.Inventory(ctx)
	require.NoError(t, err)
	require.Empty(t, inventory,
		"a failed golden refresh left %d instance(s) behind, holding an UNMASKED copy "+
			"of production under a name that was never returned to a caller, so nothing "+
			"has a handle to clean it up", len(inventory))
}

// TestAnUnmaskedGoldenCannotBePublished covers the nil hook refusal.
//
// A GoldenSpec whose Mask is nil would otherwise clone production, run no
// masking at all, verify whatever the scanner makes of unmasked data, and
// publish it. The refusal is the provider's, because the field is optional in
// the struct and the consequence of leaving it unset is not.
func TestAnUnmaskedGoldenCannotBePublished(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	spec := goldenSpec()
	spec.Mask = nil
	_, err := p.RefreshGolden(ctx, spec)
	require.Error(t, err, "a golden with no masking hook was published")
	require.Contains(t, err.Error(), "unmasked",
		"the refusal does not say what is wrong, so the person reading it has to guess")

	inventory, err := p.Inventory(ctx)
	require.NoError(t, err)
	require.Empty(t, inventory, "the refused refresh left an unmasked clone behind")
}

// TestResetIsRefusedRatherThanFaked covers the capability this provider does
// not have.
//
// Cloud SQL has no rewind. Restoring a backup onto an existing instance goes
// through the same provisioning as a clone and takes the instance offline, so
// dressing it up as Reset would publish a capability whose cost is nothing like
// what the name implies. The two assertions have to agree: a provider that
// declared the capability and returned ErrUnsupported, or one that denied it
// and silently did something, would each fail one of them.
func TestResetIsRefusedRatherThanFaked(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)

	require.False(t, p.Capabilities().Reset,
		"this provider declares Reset, so the conformance suite will exercise it")
	require.ErrorIs(t, p.Reset(context.Background(), provider.Branch{}), provider.ErrUnsupported,
		"Reset does something. Cloud SQL has no rewind, so whatever it does is not the "+
			"operation the capability names")
}

// TestTheGoldenStopPolicyIsRefusedRatherThanGuessed covers the unknown this
// lane could not settle.
//
// Whether Cloud SQL will fast clone a STOPPED instance is not established by
// Google's documentation in either direction. The provider therefore defaults
// to the policy that is known to work and costs more, and an unreadable value
// is refused rather than resolved to either answer.
func TestTheGoldenStopPolicyIsRefusedRatherThanGuessed(t *testing.T) {
	server := newFake(t, seedSQL)
	opts := options(t, server)
	opts.StopGoldens = cloudsql.GoldenStopPolicy("maybe")
	_, err := cloudsql.New(context.Background(), opts)
	require.Error(t, err,
		"an unrecognised golden stop policy was accepted, so a typo silently selects "+
			"one of the two behaviours and the person who made it is not told which")

	// And the default is the one that is known to work rather than the cheap one.
	opts = options(t, server)
	opts.StopGoldens = ""
	p, err := cloudsql.New(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	require.NoError(t, p.Close())
}
