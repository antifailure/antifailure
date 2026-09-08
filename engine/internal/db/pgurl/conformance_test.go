package pgurl_test

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/db/pgurl"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// testAdminURL is the Postgres the whole project's tests share, the one
// `just db` starts. AF_TEST_DATABASE_URL overrides it, and AF_PGURL_ADMIN_URL
// overrides that for somebody who wants this suite pointed somewhere else.
//
// One convention rather than a new one, deliberately: a suite pointed at a
// port nothing in CI starts skips silently on every run while the job goes
// green, and this repository has been bitten by exactly that before.
const testAdminURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

// TestConformance runs the shared suite against a real Postgres.
//
// Against the real service, like every other provider here, and for this one
// the real service is the point: pgurl's whole claim is that any reachable
// Postgres works, so a fake would be testing the claim's opposite. It is also
// the only provider in the set whose real service needs no account, no card
// and no network, which is why this suite runs on every pull request rather
// than skipping the way the Neon and Supabase ones do.
//
// What it costs the server: one database per golden and one per branch, each a
// copy of the conformance dataset, all removed by the suite's own cleanup and
// checked by its leak assertion at the end.
func TestConformance(t *testing.T) {
	admin := adminURL(t)

	// Declared rather than discovered, which is also what makes the limit
	// behaviour run at all. Four is enough to reach it in one behaviour and
	// small enough that reaching it costs four small databases.
	limit := 4
	if raw := os.Getenv("AF_PGURL_MAX_BRANCHES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		require.NoError(t, err, "AF_PGURL_MAX_BRANCHES is not a number")
		limit = parsed
	}

	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		p, err := pgurl.New(context.Background(), pgurl.Options{
			AdminURL:    admin,
			Variable:    "AF_PGURL_ADMIN_URL",
			SeedSQL:     conformance.DefaultSeedSQL,
			MaxBranches: limit,
			Clock:       clock.New(),
		})
		require.NoError(t, err)
		return p
	}, conformance.Options{
		// A real Postgres server, which for THIS provider is the whole of the
		// service: pgurl ships against any reachable Postgres, so the thing the
		// stopwatch times here is the thing a customer would run. That is what
		// makes this one of the two suites in the repository that can decide a
		// copy on write declaration with no account at all.
		RealService: "a real Postgres server, which is this provider's entire service",
		// Generous but bounded. Every behaviour here is a CREATE DATABASE or a
		// file copy on a server that other suites are hammering at the same
		// time, and a hung call must fail the behaviour rather than the job.
		Timeout:  4 * time.Minute,
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
	})
}

// TestSweepLeftovers removes anything a killed run left behind.
//
// Separate from the suite on purpose, the way the Neon one is: a failing
// behaviour legitimately leaves things behind for inspection, and a sweep that
// ran automatically would destroy the evidence.
func TestSweepLeftovers(t *testing.T) {
	if os.Getenv("AF_PGURL_SWEEP") == "" {
		t.Skip("skipped: set AF_PGURL_SWEEP=1 to remove databases left by a killed run")
	}
	admin := adminURL(t)
	p, err := pgurl.New(context.Background(), pgurl.Options{AdminURL: admin, Clock: clock.New()})
	require.NoError(t, err)
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	items, err := p.Inventory(ctx)
	require.NoError(t, err)
	for _, r := range items {
		t.Logf("removing %s (%s)", r.ID, r.Kind)
		switch r.Kind {
		case "database/golden":
			require.NoError(t, p.DestroyGolden(ctx, r.Labels["version"]))
		default:
			require.NoError(t, p.Destroy(ctx, provider.Branch{ProviderRef: r.ID}))
		}
	}
}

// adminURL returns the server this suite runs against, or skips.
//
// A skip is right on a laptop with no Postgres and wrong in CI, where a job
// that skipped every one of these would go green having proved nothing. That
// is what AF_REQUIRE_DATABASE turns into a failure, and it is set in the
// workflow that starts the server.
func adminURL(t *testing.T) secrets.Value {
	t.Helper()
	raw := os.Getenv("AF_PGURL_ADMIN_URL")
	if raw == "" {
		raw = os.Getenv("AF_TEST_DATABASE_URL")
	}
	if raw == "" {
		raw = testAdminURL
	}
	if err := reachable(raw); err != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and %s did not answer: %v", redactHost(raw), err)
		}
		t.Skipf("skipped: no Postgres answered at %s: %v", redactHost(raw), err)
	}
	return secrets.NewFrom(raw, "AF_PGURL_ADMIN_URL")
}

func reachable(raw string) error {
	db, err := sql.Open("pgx", raw)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var one int
	return db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// redactHost is what a skip message may print: where it looked, never what it
// would have logged in with.
func redactHost(raw string) string {
	return pgurl.HostPortOf(secrets.New(raw))
}
