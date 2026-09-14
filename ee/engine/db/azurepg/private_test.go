// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package azurepg_test

import (
	"context"
	"testing"
	"time"

	"github.com/antifailure/antifailure/ee/engine/db/azurepg"
	"github.com/stretchr/testify/require"
)

func TestPrivateRestoresRetainTheSubnetAndDNSWithoutPublicFirewallRules(t *testing.T) {
	server := newFake(t, seedSQL)
	const subnet = "/subscriptions/test/resourceGroups/proof/providers/Microsoft.Network/virtualNetworks/v/subnets/db"
	const zone = "/subscriptions/test/resourceGroups/proof/providers/Microsoft.Network/privateDnsZones/proof.postgres.database.azure.com"
	require.True(t, server.MakePrivate(sourceServer, subnet, zone))
	opts := options(t, server)
	opts.AllowCIDR = ""
	p, err := azurepg.NewWithFixtureRoles(opts)
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	golden, err := p.RefreshGolden(context.Background(), goldenSpec())
	require.NoError(t, err)
	branch, err := p.Branch(context.Background(), golden.ID, "private-proof")
	require.NoError(t, err)
	actualSubnet, actualZone := server.NetworkOf(branch.ProviderRef)
	require.Equal(t, subnet, actualSubnet)
	require.Equal(t, zone, actualZone)
	rules, exists := server.FirewallRulesOf(branch.ProviderRef)
	require.True(t, exists)
	require.Zero(t, rules, "a private restore was opened through a public firewall rule")
}

func TestListedGoldensKeepTheirCreationTimeAndNewestFirstOrder(t *testing.T) {
	server := newFake(t, seedSQL)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	opts := options(t, server)
	opts.Now = func() time.Time { return now }
	p, err := azurepg.NewWithFixtureRoles(opts)
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	_, err = p.RefreshGolden(context.Background(), goldenSpec())
	require.NoError(t, err)
	now = now.Add(time.Hour)
	newest, err := p.RefreshGolden(context.Background(), goldenSpec())
	require.NoError(t, err)
	listed, err := p.ListGoldens(context.Background())
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.Equal(t, newest.ID, listed[0].ID)
	require.Equal(t, newest.CreatedAt, listed[0].CreatedAt)
}
