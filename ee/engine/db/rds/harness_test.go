// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// What every suite in this package needs to build a provider, and the one
// place that decides where the Postgres comes from.
//
// The control plane is fakerds and the data plane is a real Postgres.
// AF_RDS_TEST_DATABASE_URL points it at a server of your own, which is what
// the copy on write behaviour wants: it writes half a gibibyte per golden and
// the cluster the whole repository shares carries every branch's migrations. A
// skip is right on a laptop with no Postgres and wrong in CI, where a job that
// skipped every one of these would go green having proved nothing, so
// AF_REQUIRE_DATABASE turns the skip into a failure.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
	"github.com/antifailure/antifailure/ee/engine/db/rds"
	"github.com/antifailure/antifailure/ee/engine/db/rds/fakerds"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// defaultPostgresURL is the scratch server every other database suite in this
// repository looks for.
const defaultPostgresURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

// sourceInstance is what a manifest would put in database.project.
const sourceInstance = "acme-production"

// testRegion is the region every request in these suites is signed for. A
// wrong one is refused by the fake, which is the point of it being a value
// rather than a wildcard.
const testRegion = "eu-west-1"

// The credentials these suites sign with. They are AWS's own published example
// values, they authenticate nothing, and the fake holds the same pair so that
// a signature can be recomputed and compared.
var testCredentials = cloudauth.AWSCredentials{
	AccessKeyID:     "AKIA" + "IOSFODNN7EXAMPLE",
	SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	Source:          "the test",
}

func postgresURL() string {
	if u := os.Getenv("AF_RDS_TEST_DATABASE_URL"); u != "" {
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
// The prefix carries eight random characters because this Postgres may be
// shared: other suites in this repository and other branches' containers use
// the same server, and two runs that agreed on a database name would destroy
// each other's data rather than fail.
func newFake(t *testing.T, seedSQL string, fault fakerds.Fault) *fakerds.Server {
	t.Helper()
	return newFakeWith(t, fakerds.Options{
		AdminURL:    requirePostgres(t),
		Prefix:      "af_rds_" + randomSuffix(t) + "_",
		Region:      testRegion,
		Credentials: testCredentials,
		Fault:       fault,
	}, seedSQL)
}

func newFakeWith(t *testing.T, opts fakerds.Options, seedSQL string) *fakerds.Server {
	t.Helper()
	server, err := fakerds.New(opts)
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, problem := range server.Close() {
			// Reported rather than ignored. A fake that leaked a database per
			// run on a shared server is the shape of defect this repository
			// keeps finding in its own instruments.
			t.Errorf("the fake control plane could not clean up: %v", problem)
		}
	})
	require.NoError(t, server.SeedSource(sourceInstance, seedSQL))
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
// wants, and the ready timeout is seconds rather than three quarters of an
// hour. Both are options rather than constants precisely so that a suite can
// be fast without the provider having a second, faster code path that
// production never runs.
func options(t *testing.T, server *fakerds.Server) rds.Options {
	t.Helper()
	return rds.Options{
		SourceInstance: sourceInstance,
		Region:         testRegion,
		BranchKey:      secret.New("a-key-these-tests-derive-passwords-from"),
		Endpoint:       server.URL(),
		Credentials:    &testCredentials,
		// The local Postgres speaks no TLS, which is the ordinary case for a
		// container and is not the ordinary case for RDS. The default is
		// verify-full, disable is accepted only for a loopback endpoint, and
		// trust_test.go runs the verified path through a real TLS listener.
		TLSMode:      "disable",
		PollInterval: 5 * time.Millisecond,
		ReadyTimeout: 60 * time.Second,
		// Four is enough for the limit behaviour to reach it and small enough
		// that reaching it costs four small databases.
		MaxBranches: 4,
		// The declared ceiling, lowered for a suite whose restores are local
		// file copies. It is still a real assertion: a branch here takes
		// milliseconds and this refuses anything over a minute.
		BranchLatency: time.Minute,
	}
}

func newProvider(t *testing.T, server *fakerds.Server) *rds.Provider {
	t.Helper()
	p, err := scopedNew(context.Background(), options(t, server))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// scopedNew is rds.New with the inherited login catalog confined to this
// fixture, and it is the ONLY way a test in this package builds a provider.
//
// Every instance the fake creates is a database on one shared Postgres, and a
// Postgres has one role catalog for the whole server. The production query
// would therefore list every login on the server, other suites' and other
// branches' included, and try to disable them. On this server's Postgres 17 the
// attempt fails for want of ADMIN on those roles, which would turn every
// refresh red for a reason that is about the fixture; on an older server
// CREATEROLE could have succeeded. So the catalog here sees only roles whose
// names begin with the rotated administrator's own name and "_x", which is the
// naming every test that creates an inherited login uses.
func scopedNew(ctx context.Context, opts rds.Options) (*rds.Provider, error) {
	p, err := rds.New(ctx, opts)
	if err == nil {
		rds.SetLoginCatalogForTest(p, scopedLogins)
	}
	return p, err
}

// scopedLogins is the fixture's login catalog. See scopedNew.
func scopedLogins(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT rolname FROM pg_catalog.pg_roles
 WHERE starts_with(rolname, current_user::text || '_x') AND
 (rolcanlogin OR EXISTS (SELECT 1 FROM pg_catalog.pg_stat_activity WHERE usename=rolname))
 ORDER BY rolname`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
