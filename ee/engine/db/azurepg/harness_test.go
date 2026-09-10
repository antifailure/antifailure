// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg_test

// What every suite in this package needs, and the one place that decides where
// the Postgres comes from.
//
// The control plane is fakeazurepg and the data plane is the Postgres the whole
// project's tests share. A skip is right on a laptop with no Postgres and wrong
// in CI, where a job that skipped every one of these would go green having
// proved nothing, so AF_REQUIRE_DATABASE turns the skip into a failure and the
// enterprise workflow sets it.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver

	"github.com/antifailure/antifailure/ee/engine/db/azurepg"
	"github.com/antifailure/antifailure/ee/engine/db/azurepg/fakeazurepg"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

const defaultPostgresURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

const (
	sourceServer      = "acme-production"
	testSubscription  = "11111111-2222-3333-4444-555555555555"
	testResourceGroup = "af-conformance"
	testLocation      = "centralus"
)

// seedSQL is a table with rows in it, so that a restore has bytes to move.
const seedSQL = `
CREATE TABLE people (id serial PRIMARY KEY, name text NOT NULL);
INSERT INTO people (name) SELECT 'person ' || g FROM generate_series(1, 500) g;
`

func postgresURL() string {
	if u := os.Getenv("AF_AZUREPG_TEST_DATABASE_URL"); u != "" {
		return u
	}
	if u := os.Getenv("AF_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return defaultPostgresURL
}

func requirePostgres(t *testing.T) string {
	t.Helper()
	url := postgresURL()
	if err := reachable(url); err != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and the test Postgres did not answer: %v", err)
		}
		t.Skipf("skipped: no Postgres answered: %v", err)
	}
	return url
}

func reachable(url string) error {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var one int
	return db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// newFake starts a control plane with the source server already seeded.
//
// The prefix carries eight random characters because this Postgres is shared:
// two runs that agreed on a database name would destroy each other's data
// rather than fail.
func newFake(t *testing.T, seed string) *fakeazurepg.Server {
	t.Helper()
	server, err := fakeazurepg.New(fakeazurepg.Options{
		AdminURL:      requirePostgres(t),
		Prefix:        "af_az_" + randomSuffix(t) + "_",
		Subscription:  testSubscription,
		ResourceGroup: testResourceGroup,
		SourceServer:  sourceServer,
		Location:      testLocation,
		SeedSQL:       seed,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, problem := range server.Close() {
			t.Errorf("the fake control plane could not clean up: %v", problem)
		}
	})
	return server
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	var b [4]byte
	_, err := rand.Read(b[:])
	require.NoError(t, err)
	return hex.EncodeToString(b[:])
}

func options(t *testing.T, server *fakeazurepg.Server) azurepg.Options {
	t.Helper()
	address, err := url.Parse(postgresURL())
	require.NoError(t, err)
	port := 5432
	if address.Port() != "" {
		port, err = strconv.Atoi(address.Port())
		require.NoError(t, err)
	}
	return azurepg.Options{
		Token:         func(context.Context) (string, error) { return "AF_FAKE_AZUREPG_TOKEN", nil },
		Port:          port,
		Subscription:  testSubscription,
		ResourceGroup: testResourceGroup,
		Location:      testLocation,
		SourceServer:  sourceServer,
		BranchKey:     secret.New("a-key-these-tests-derive-passwords-from"),
		Endpoint:      server.URL(),
		AllowCIDR:     "203.0.113.0/24",
		// The local Postgres speaks no TLS, which is the ordinary case for a
		// container and is not the ordinary case for Azure. The default stays
		// require and only a test lowers it.
		TLSMode:      "disable",
		PollInterval: 5 * time.Millisecond,
		MaxBranches:  4,
	}
}

func newProvider(t *testing.T, server *fakeazurepg.Server) *azurepg.Provider {
	t.Helper()
	p, err := azurepg.NewWithFixtureRoles(options(t, server))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// goldenSpec is a masking and verification pair that succeeds.
func goldenSpec() provider.GoldenSpec {
	return provider.GoldenSpec{
		SourceURL:  secret.New("postgres://unused"),
		Version:    16,
		RulesHash:  "rules-under-test",
		Provenance: "the azurepg suite",
		Mask:       func(context.Context, secret.Value) error { return nil },
		Verify: func(context.Context, secret.Value) (string, error) {
			return "attested-by-the-test", nil
		},
	}
}
