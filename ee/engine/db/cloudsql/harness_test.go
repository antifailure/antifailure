// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql_test

// What every suite in this package needs to build a provider, and the one
// place that decides where the Postgres comes from.
//
// The control plane is fakecloudsql and the data plane is the Postgres the
// whole project's tests share, the one `just db` starts. A skip is right on a
// laptop with no Postgres and wrong in CI, where a job that skipped every one
// of these would go green having proved nothing, so AF_REQUIRE_DATABASE turns
// the skip into a failure and the enterprise workflow sets it.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver

	"github.com/antifailure/antifailure/ee/engine/db/cloudsql"
	"github.com/antifailure/antifailure/ee/engine/db/cloudsql/fakecloudsql"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// defaultPostgresURL is the scratch server every other database suite in this
// repository looks for.
const defaultPostgresURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

// sourceInstance is what a manifest would put in database.project.
const sourceInstance = "acme-production"

// testProject is the Google Cloud project every request here names. A wrong
// one is refused by the fake, which is the point of it being a value rather
// than a wildcard.
const testProject = "af-conformance"

// testRegion is the region the source instance lives in.
const testRegion = "us-central1"

func postgresURL() string {
	if u := os.Getenv("AF_CLOUDSQL_TEST_DATABASE_URL"); u != "" {
		return u
	}
	if u := os.Getenv("AF_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return defaultPostgresURL
}

// requirePostgres returns the server these suites run against, or skips.
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

// newFake starts a control plane with the source instance already seeded.
//
// The prefix carries eight random characters because this Postgres is shared:
// other suites in this repository and other branches' containers use the same
// server, and two runs that agreed on a database name would destroy each
// other's data rather than fail.
func newFake(t *testing.T, seedSQL string) *fakecloudsql.Server {
	t.Helper()
	server, err := fakecloudsql.New(fakecloudsql.Options{
		AdminURL:       requirePostgres(t),
		Prefix:         "af_cs_" + randomSuffix(t) + "_",
		Project:        testProject,
		SourceInstance: sourceInstance,
		SeedSQL:        seedSQL,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, problem := range server.Close() {
			// Reported rather than ignored, for the reason the fake's own
			// Close comment gives.
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

// options are the provider options every suite here starts from.
func options(t *testing.T, server *fakecloudsql.Server) cloudsql.Options {
	t.Helper()
	host, port := hostPort(t, requirePostgres(t))
	return cloudsql.Options{
		Token: func(context.Context) (string, error) { return "AF_FAKE_CLOUDSQL_TOKEN", nil },
		// The Auth Proxy path, which is also how this suite reaches its data:
		// every fake instance is a database on ONE Postgres, so the address is
		// shared and the database name is what distinguishes them. The
		// provider discovers that name through the Admin API's databases
		// collection, which the fake serves, rather than through anything
		// test only.
		ProxyAddress:   host + ":" + port,
		Project:        testProject,
		Region:         testRegion,
		SourceInstance: sourceInstance,
		BranchKey:      secret.New("a-key-these-tests-derive-passwords-from"),
		Endpoint:       server.URL(),
		// The local Postgres speaks no TLS, which is the ordinary case for a
		// container and is not the ordinary case for Cloud SQL. The default
		// stays require and only a test lowers it.
		TLSMode:      "disable",
		PollInterval: 5 * time.Millisecond,
		// Four is enough for the limit behaviour to reach it and small enough
		// that reaching it costs four small databases.
		MaxBranches: 4,
	}
}

// hostPort splits a Postgres URL into the pair ProxyAddress wants.
func hostPort(t *testing.T, raw string) (string, string) {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	host, port, err := net.SplitHostPort(u.Host)
	require.NoError(t, err)
	return host, port
}

func newProvider(t *testing.T, server *fakecloudsql.Server) *cloudsql.Provider {
	t.Helper()
	p, err := cloudsql.New(context.Background(), options(t, server))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}
