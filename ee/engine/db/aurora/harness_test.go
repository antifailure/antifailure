// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora_test

// What every suite in this package needs to build a provider, and the one
// place that decides where the Postgres comes from.
//
// The control plane is fakerds and the data plane is the Postgres the whole
// project's tests share, the one `just db` starts. A skip is right on a laptop
// with no Postgres and wrong in CI, where a job that skipped every one of
// these would go green having proved nothing, so AF_REQUIRE_DATABASE turns the
// skip into a failure and the enterprise workflow sets it.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/awsauth"
	"github.com/antifailure/antifailure/ee/engine/db/aurora"
	"github.com/antifailure/antifailure/ee/engine/db/aurora/fakerds"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// defaultPostgresURL is the scratch server every other database suite in this
// repository looks for.
const defaultPostgresURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

// sourceCluster is what a manifest would put in database.project.
const sourceCluster = "acme-production"

// testRegion is the region every request in these suites is signed for. A
// wrong one is refused by the fake, which is the point of it being a value
// rather than a wildcard.
const testRegion = "eu-west-1"

// The credentials these suites sign with. They are AWS's own published example
// values, they authenticate nothing, and the fake holds the same pair so that
// a signature can be recomputed and compared.
var testCredentials = awsauth.Credentials{
	AccessKeyID:     "AKIA" + "IOSFODNN7EXAMPLE",
	SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	Source:          "the test",
}

func postgresURL() string {
	if u := os.Getenv("AF_AURORA_TEST_DATABASE_URL"); u != "" {
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

// newFake starts a control plane with the source cluster already seeded.
//
// The prefix carries eight random characters because this Postgres is shared:
// other suites in this repository and other branches' containers use the same
// server, and two runs that agreed on a database name would destroy each
// other's data rather than fail.
func newFake(t *testing.T, seedSQL string, fault fakerds.Fault) *fakerds.Server {
	t.Helper()
	server, err := fakerds.New(fakerds.Options{
		AdminURL:    requirePostgres(t),
		Prefix:      "af_aur_" + randomSuffix(t) + "_",
		Region:      testRegion,
		Credentials: testCredentials,
		Fault:       fault,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, problem := range server.Close() {
			// Reported rather than ignored. A fake that leaked a database per
			// run on a shared server is the shape of defect this repository
			// keeps finding in its own instruments.
			t.Errorf("the fake control plane could not clean up: %v", problem)
		}
	})
	require.NoError(t, server.SeedSource(sourceCluster, seedSQL))
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
//
// The poll interval is milliseconds rather than the ten seconds a real account
// wants, and the ready timeout is seconds rather than half an hour. Both are
// options rather than constants precisely so that a suite can be fast without
// the provider having a second, faster code path that production never runs.
func options(t *testing.T, server *fakerds.Server) aurora.Options {
	t.Helper()
	return aurora.Options{
		SourceCluster: sourceCluster,
		Region:        testRegion,
		BranchKey:     secret.New("a-key-these-tests-derive-passwords-from"),
		Endpoint:      server.URL(),
		Credentials:   &testCredentials,
		// The local Postgres speaks no TLS, which is the ordinary case for a
		// container and is not the ordinary case for Aurora. The default stays
		// require and only a test lowers it.
		TLSMode:      "disable",
		PollInterval: 5 * time.Millisecond,
		ReadyTimeout: 30 * time.Second,
		// Four is enough for the limit behaviour to reach it and small enough
		// that reaching it costs four small databases.
		MaxBranches:   4,
		BranchLatency: time.Minute,
	}
}

func newProvider(t *testing.T, server *fakerds.Server) *aurora.Provider {
	t.Helper()
	p, err := aurora.New(context.Background(), options(t, server))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}
