// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
package azurepg

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/stretchr/testify/require"
)

// This opt-in proof only accepts the disposable group's naming convention.
// Its runner owns and deletes that entire group even if the process dies.
func TestLivePrivateAzureRestoreMaskBranchAndDelete(t *testing.T) {
	if os.Getenv("AF_AZUREPG_LIVE") != "1" {
		t.Skip("requires the disposable private Azure proof runner")
	}
	group := os.Getenv(ResourceGroupVariable)
	require.True(t, strings.HasPrefix(group, "af-codex-private-"), "live proof requires an owned disposable group")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Minute)
	defer cancel()
	host := os.Getenv("AF_AZUREPG_LIVE_HOST")
	require.NotEmpty(t, host)
	sourceURL := &url.URL{Scheme: "postgres", Host: host + ":5432", Path: "/postgres", User: url.UserPassword("afproof", os.Getenv("AF_AZUREPG_LIVE_PASSWORD")), RawQuery: "sslmode=require"}
	source, err := sql.Open("pgx", sourceURL.String())
	require.NoError(t, err)
	defer source.Close()
	_, err = source.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS af_live_proof (id integer PRIMARY KEY, value text NOT NULL); INSERT INTO af_live_proof VALUES (1, 'synthetic-original') ON CONFLICT (id) DO UPDATE SET value=EXCLUDED.value")
	require.NoError(t, err)
	_, err = source.ExecContext(ctx, "DO $proof$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='af_proof_reader') THEN CREATE ROLE af_proof_reader; END IF; END $proof$; ALTER ROLE af_proof_reader LOGIN PASSWORD 'AF_FAKE_INHERITED_PASSWORD'")
	require.NoError(t, err)
	p, err := New(Options{Subscription: os.Getenv(SubscriptionVariable), ResourceGroup: group, Location: "centralus", SourceServer: os.Getenv("AF_AZUREPG_LIVE_SOURCE"), BranchKey: secret.New(os.Getenv(DefaultVariable)), Database: "postgres", Getenv: os.Getenv})
	require.NoError(t, err)
	defer p.Close()
	t.Log("source seeded; starting private golden restore")
	query := func(ctx context.Context, connection secret.Value, statement string) (string, error) {
		db, err := sql.Open("pgx", connection.Reveal())
		if err != nil {
			return "", err
		}
		defer db.Close()
		var value string
		err = db.QueryRowContext(ctx, statement).Scan(&value)
		return value, err
	}
	golden, err := p.RefreshGolden(ctx, provider.GoldenSpec{RulesHash: "live-private-proof", Provenance: "disposable synthetic rows", Mask: func(ctx context.Context, connection secret.Value) error {
		value, err := query(ctx, connection, "UPDATE af_live_proof SET value='masked' RETURNING value")
		if err == nil && value != "masked" {
			return fmt.Errorf("mask did not update the restored row")
		}
		return err
	}, Verify: func(ctx context.Context, connection secret.Value) (string, error) {
		value, err := query(ctx, connection, "SELECT value FROM af_live_proof WHERE id=1")
		if err != nil {
			return "", err
		}
		if value != "masked" {
			return "", fmt.Errorf("restored row was not masked")
		}
		return "live-private-row-verified", nil
	}})
	require.NoError(t, err)
	require.True(t, golden.Verified)
	t.Log("golden masked and verified; starting branch restore")
	branch, err := p.Branch(ctx, golden.ID, "live-private-proof")
	require.NoError(t, err)
	connection, err := p.ConnString(ctx, branch, provider.ConnDirect)
	require.NoError(t, err)
	value, err := query(ctx, connection, "SELECT value FROM af_live_proof WHERE id=1")
	require.NoError(t, err)
	require.Equal(t, "masked", value)
	var original string
	require.NoError(t, source.QueryRowContext(ctx, "SELECT value FROM af_live_proof WHERE id=1").Scan(&original))
	require.Equal(t, "synthetic-original", original)
	readerURL, err := url.Parse(connection.Reveal())
	require.NoError(t, err)
	readerURL.User = url.UserPassword("af_proof_reader", "AF_FAKE_INHERITED_PASSWORD")
	reader, err := sql.Open("pgx", readerURL.String())
	require.NoError(t, err)
	require.Error(t, reader.PingContext(ctx), "a copied source login still authenticates to the branch")
	_ = reader.Close()
	readerURL.Host = sourceURL.Host
	sourceReader, err := sql.Open("pgx", readerURL.String())
	require.NoError(t, err)
	require.NoError(t, sourceReader.PingContext(ctx), "the source login must stay unchanged")
	_ = sourceReader.Close()
	resource, err := p.api.getServer(ctx, branch.ProviderRef)
	require.NoError(t, err)
	require.Equal(t, "Disabled", resource.Properties.Network.PublicNetworkAccess)
	require.NotEmpty(t, resource.Properties.Network.DelegatedSubnetResourceID)
	require.NotEmpty(t, resource.Properties.Network.PrivateDNSZoneResourceID)
	listed, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, golden.ID, listed[0].ID)
	require.NoError(t, p.Destroy(ctx, branch))
	_, err = p.api.getServer(ctx, branch.ProviderRef)
	require.True(t, notFound(err), "branch must no longer exist")
	require.NoError(t, p.DestroyGolden(ctx, golden.ID))
	inventory, err := p.Inventory(ctx)
	require.NoError(t, err)
	require.Empty(t, inventory)
	t.Log("private branch served masked rows; source unchanged; branch and golden deleted")
}
