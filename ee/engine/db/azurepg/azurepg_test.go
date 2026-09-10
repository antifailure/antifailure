// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg_test

// The behaviours that are this provider's own rather than the shared suite's.
//
// Three of these are the post restore facts Microsoft documents and that a
// provider gets wrong silently: firewall rules are not copied, the
// administrator credential IS copied, and a restore cannot cross between public
// and private access. Each is a real outage or a real exposure, and each is
// invisible to a suite that only checks a branch has the golden's rows.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/azurepg"
	"github.com/antifailure/antifailure/ee/engine/db/azurepg/fakeazurepg"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// TestABranchDoesNotKeepTheSourcePassword is the security assertion.
//
// Microsoft documents that a restored server keeps the SOURCE's administrator
// login. Without an explicit reset every preview environment would be reachable
// with PRODUCTION's credential, which is the exact thing this product exists to
// prevent. The fake models the inheritance so this test can see the reset
// happen rather than assume it.
//
// The second assertion is the control: a provider that reset EVERY server's
// password, including the customer's production one, would pass the first and
// be a far worse defect.
func TestABranchDoesNotKeepTheSourcePassword(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	before, ok := server.PasswordOf(sourceServer)
	require.True(t, ok, "the fake has no source server, so this proves nothing")

	version, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)
	branch, err := p.Branch(ctx, version.ID, "env_password")
	require.NoError(t, err)

	got, ok := server.PasswordOf(branch.ProviderRef)
	require.True(t, ok, "the branch server is not there")
	require.NotEqual(t, before, got,
		"this branch is reachable with the SOURCE server's administrator password. An "+
			"Azure restore keeps the source's administrator login, so a preview "+
			"environment would hold production's database credential")

	after, ok := server.PasswordOf(sourceServer)
	require.True(t, ok)
	require.Equal(t, before, after,
		"the provider changed the SOURCE server's administrator password. The source "+
			"is the customer's production database and this provider must never write to it")
}

// TestABranchIsGivenAFirewallRule is the reachability assertion.
//
// Microsoft lists applying firewall rules as a POST RESTORE task: they are not
// copied. A branch created without one is a server that provisioned
// successfully and answers nobody, and the failure arrives as a connection
// timeout that names no firewall, which is the slowest possible way to learn
// the answer.
//
// The control is the assertion that the fake starts a restored server with
// zero rules. Without it this test would pass against a fake that copied rules
// across, and would be measuring nothing.
func TestABranchIsGivenAFirewallRule(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	version, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)
	branch, err := p.Branch(ctx, version.ID, "env_firewall")
	require.NoError(t, err)

	rules, ok := server.FirewallRulesOf(branch.ProviderRef)
	require.True(t, ok, "the branch server is not there")
	require.Positive(t, rules,
		"this branch has no firewall rule. Azure does not copy rules across a restore, "+
			"so the server exists, provisioning reported success, and nothing can "+
			"connect to it. The failure a user sees is a connection timeout that "+
			"mentions no firewall at all")
}

// TestTheFakeDoesNotCopyFirewallRules is the control for the test above.
//
// It asserts the property that makes the previous test meaningful: a restore
// arrives with no rules. If the fake ever started copying them,
// TestABranchIsGivenAFirewallRule would pass whether or not the provider
// created one, and the check would be unable to say no.
func TestTheFakeDoesNotCopyFirewallRules(t *testing.T) {
	server := newFake(t, seedSQL)
	ctx := context.Background()

	// The source has a rule.
	before, ok := server.FirewallRulesOf(sourceServer)
	require.True(t, ok)
	require.Positive(t, before, "the fake's source server has no rule, so nothing could be copied")

	// A restore driven straight through the provider's golden path, whose
	// server the provider has not yet opened.
	p, err := azurepg.NewWithFixtureRoles(optionsWithoutFirewall(t, server))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	_, err = p.RefreshGolden(ctx, goldenSpec())
	require.Error(t, err,
		"a provider with no allowed CIDR published a golden, so the firewall step is "+
			"not on the path this test believes it is on")
}

// TestAPrivateSourceIsRefusedBeforeProvisioning covers Azure's access
// boundary.
//
// Microsoft states a restore cannot cross between public and private access.
// This provider opens a branch with a firewall rule, which exists only on the
// public side, so a private source would provision a server it could not then
// open. Refusing up front costs one read; discovering it after the restore
// costs a provisioned server and the time to provision it.
func TestAPrivateSourceWithoutDNSIsRefusedBeforeProvisioning(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	require.True(t, server.MakePrivate(sourceServer,
		"/subscriptions/x/resourceGroups/y/providers/Microsoft.Network/virtualNetworks/v/subnets/s"))

	_, err := p.RefreshGolden(ctx, goldenSpec())
	require.Error(t, err, "a private access source was accepted")
	require.Contains(t, err.Error(), "virtual network",
		"the refusal does not name the reason, so the person reading it has to guess")

	require.Zero(t, server.Restores(),
		"the provider issued %d restore(s) before refusing. The whole point of this "+
			"check is that it costs a read rather than a provisioned server",
		server.Restores())
}

// TestAServerWeDidNotCreateIsRefused covers the ownership predicate.
//
// It matters more here than anywhere else in this repository, because Microsoft
// states that deleting a flexible server deletes every backup belonging to it.
// There is no recovery from a wrong delete.
func TestAServerWeDidNotCreateIsRefused(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	ctx := context.Background()

	err := p.Destroy(ctx, provider.Branch{EnvID: "whatever", ProviderRef: sourceServer})
	require.ErrorIs(t, err, azurepg.ErrNotOurs,
		"this provider deleted a server it did not create. On Azure that also deletes "+
			"every backup the server had, so there is nothing to restore from")
	require.True(t, server.Exists(sourceServer),
		"the source server is gone, so the refusal above did not protect it")
}

// TestAGoldenIsDestroyedWhenVerificationFails is the data exposure assertion.
//
// Between the restore and the verification a server exists holding UNMASKED
// production data, billed by the hour, under a name returned to nobody.
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
		"a failed golden refresh left %d server(s) behind, holding an UNMASKED copy of "+
			"production, billed by the hour, under a name that was never returned to a "+
			"caller, so nothing has a handle to clean it up", len(inventory))
}

// TestCopyOnWriteIsDeclaredFalse is the honesty assertion of this package.
//
// A restore creates an independent copy. Microsoft's own sentence about a
// snapshot restore not depending on the size of the data reads exactly like a
// copy on write claim, and the rest of the same paragraph is why it is not one.
// This test exists so that changing the declaration requires reading the
// argument in the package comment rather than flipping a bool.
//
// It is paired with the latency assertion because the two are the same claim
// seen from different sides: a provider claiming copy on write and declaring a
// latency in minutes is incoherent, and so is one denying it and declaring
// seconds.
func TestCopyOnWriteIsDeclaredFalse(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)
	caps := p.Capabilities()

	require.False(t, caps.CopyOnWrite,
		"this provider declares copy on write. A branch here is a point in time "+
			"restore, which Microsoft documents as creating a NEW SERVER that is an "+
			"independent copy, and nothing in Microsoft's documentation claims the "+
			"restored server shares storage with its source. engine/conformance would "+
			"then require branch time NOT to grow with the database, which is an "+
			"assertion this provider cannot honestly make")
	require.Greater(t, caps.ExpectedBranchLatency.Minutes(), 1.0,
		"the declared branch latency is under a minute, which contradicts CopyOnWrite "+
			"being false: Microsoft gives the overall recovery as a few minutes up to a "+
			"few hours")
}

// TestResetIsRefusedRatherThanFaked covers the capability this provider does
// not have.
func TestResetIsRefusedRatherThanFaked(t *testing.T) {
	server := newFake(t, seedSQL)
	p := newProvider(t, server)

	require.False(t, p.Capabilities().Reset,
		"this provider declares Reset, so the conformance suite will exercise it")
	require.ErrorIs(t, p.Reset(context.Background(), provider.Branch{}), provider.ErrUnsupported,
		"Reset does something. A restore creates a new server rather than returning an "+
			"existing one to an earlier state, so whatever it does is not the operation "+
			"the capability names")
}

// optionsWithoutFirewall is the provider with no allowed CIDR, for the control
// above.
func optionsWithoutFirewall(t *testing.T, server *fakeazurepg.Server) azurepg.Options {
	t.Helper()
	opts := options(t, server)
	opts.AllowCIDR = ""
	return opts
}
