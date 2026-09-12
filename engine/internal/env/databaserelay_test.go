package env

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
	"github.com/antifailure/antifailure/engine/pkg/secret"
	"github.com/stretchr/testify/require"
)

func TestDatabaseRelayPreservesTLSNamesAndSeparatesPoolAndMigration(t *testing.T) {
	app := secret.New("postgres://user:AF_FAKE_PASSWORD@pool.example.test/app?sslmode=verify-full&hostaddr=10.40.1.2")
	migration := secret.New("postgres://user:AF_FAKE_PASSWORD@direct.example.test/app?sslmode=verify-full")
	policy := &schema.Egress{Rules: []schema.EgressRule{{Host: "other.example.test:45000"}}}
	inside, insideMigration, routes, err := relayDatabaseURLs(app, migration, policy)
	require.NoError(t, err)
	parsed, err := url.Parse(inside.Reveal())
	require.NoError(t, err)
	require.Equal(t, "pool.example.test", parsed.Hostname())
	require.Equal(t, "45001", parsed.Port())
	require.Empty(t, parsed.Query().Get("hostaddr"))
	require.Equal(t, "verify-full", parsed.Query().Get("sslmode"))
	parsed, err = url.Parse(insideMigration.Reveal())
	require.NoError(t, err)
	require.Equal(t, "direct.example.test", parsed.Hostname())
	require.Equal(t, "45002", parsed.Port())
	require.Equal(t, []provider.DatabaseRoute{{Port: 45001, Upstream: "10.40.1.2:5432"}, {Port: 45002, Upstream: "direct.example.test:5432"}}, routes)
}

func TestDatabaseRelayMakesLiteralIPsUseContainedDNS(t *testing.T) {
	address := secret.New("postgres://user:AF_FAKE_PASSWORD@10.40.1.2:5432/app?sslmode=verify-ca")
	inside, migration, routes, err := relayDatabaseURLs(address, address, nil)
	require.NoError(t, err)
	parsed, err := url.Parse(inside.Reveal())
	require.NoError(t, err)
	require.Equal(t, "database-45000.af-database.invalid", parsed.Hostname())
	require.Equal(t, inside.Reveal(), migration.Reveal())
	require.Len(t, routes, 1)
	require.Equal(t, "10.40.1.2:5432", routes[0].Upstream)
}

func TestDatabaseRelayRefusesAnIPWhoseTLSNameWouldChange(t *testing.T) {
	_, _, _, err := relayDatabaseURLs(secret.New("postgres://10.40.1.2/app?sslmode=verify-full"), secret.Value{}, nil)
	require.ErrorContains(t, err, "DNS hostname")
}

type partialDB struct {
	*fakeDB
	removed []provider.Branch
}

func (d *partialDB) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	b, err := d.fakeDB.Branch(ctx, version, envID)
	if err != nil {
		return b, err
	}
	b.ProviderRef = "opaque-partial-resource"
	return b, errors.New("response lost after acceptance")
}
func (d *partialDB) Destroy(ctx context.Context, b provider.Branch) error {
	d.removed = append(d.removed, b)
	if b.ProviderRef != "opaque-partial-resource" {
		return errors.New("the opaque partial reference was lost")
	}
	return d.fakeDB.Destroy(ctx, b)
}

func TestAFailedBranchKeepsItsOpaqueReferenceForTeardown(t *testing.T) {
	database := &partialDB{fakeDB: newFakeDB("acmedb")}
	runtime := &fakeRT{name: "acmert"}
	registry := extension.NewRegistry()
	registry.AddDatabaseProvider(trustRegistration{database})
	registry.AddRuntimeProvider(&fakeRTProvider{name: "acmert", rt: runtime})
	orchestrator := registeredOrchestrator(t, registry)
	_, err := orchestrator.Up(context.Background())
	require.Error(t, err)
	require.Empty(t, runtime.ups)
	_, err = orchestrator.Down(context.Background())
	require.NoError(t, err)
	var references []string
	for _, branch := range database.removed {
		if branch.ProviderRef != "" {
			references = append(references, branch.ProviderRef)
		}
	}
	require.Equal(t, []string{"opaque-partial-resource"}, references)
	require.Empty(t, database.branches, "the partial branch survived teardown")
}
