package cli

// The seam, against a real server.
//
// gate_test.go proves the decision on fixtures. This proves the thing the
// fixtures stand in for: that a migration holding a real ACCESS EXCLUSIVE lock
// on a real table produces a real finding in the comment a person reads. The
// two halves were connected by nothing before, because af ci never called
// insights, so a fixture on its own would be proving the shape of a pipe that
// was not plugged in.
//
// A real server rather than a real provider. What is being proved is what
// Postgres does with a migration and a lock, and none of that is different for
// a database the Docker provider made.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// gateTestDatabaseURL is the Postgres the project's tests share, the one
// `just db` starts. AF_TEST_DATABASE_URL overrides it.
const gateTestDatabaseURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

// gateShaped is a table big enough that a lock on it means something. The lint
// rules and the lock sampler both ask the database how many rows there are,
// and on an empty table every answer is "this is fine".
const gateShaped = `
CREATE TABLE orders (
  id          bigserial PRIMARY KEY,
  status      text NOT NULL,
  total_cents int NOT NULL
);
INSERT INTO orders (status, total_cents)
  SELECT CASE WHEN g %% 7 = 0 THEN 'refunded' ELSE 'paid' END, (g %% 5000) + 100
  FROM generate_series(1, %d) g;
ANALYZE;
`

func gateMaintenanceURL() string {
	if u := os.Getenv("AF_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return gateTestDatabaseURL
}

// requireGateDatabase makes a database of its own for one test.
//
// It skips when there is no server, because a machine with no test Postgres
// has not found a bug, and fails instead when AF_REQUIRE_DATABASE is set,
// because a machine that was supposed to have one has found a large one. A
// skip prints nothing, so a suite that skipped everything reports ok having
// examined nothing, and that is exactly how a row in STATUS.md said proven
// once on the strength of a job with no Postgres in it.
func requireGateDatabase(t *testing.T, name string, rows int) (*pgx.Conn, *pgx.Conn, secrets.Value, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	base := gateMaintenanceURL()

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		cancel()
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and there is no Postgres at %s: %v",
				redactGateURL(base), err)
		}
		t.Skipf("skipped: no Postgres at %s: %v", redactGateURL(base), err)
	}

	db := "af_gate_" + name
	_, err = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
	require.NoError(t, err)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+db)
	require.NoError(t, err)

	url := secrets.New(gateDatabaseURL(base, db))
	conn, err := pgx.Connect(ctx, url.Reveal())
	require.NoError(t, err)
	_, err = conn.Exec(ctx, fmt.Sprintf(gateShaped, rows))
	require.NoError(t, err)

	// A second connection, because a lock held by a statement in flight is
	// invisible to the session holding it.
	watch, err := pgx.Connect(ctx, url.Reveal())
	require.NoError(t, err)

	return conn, watch, url, func() {
		_ = watch.Close(context.Background())
		_ = conn.Close(context.Background())
		c, cancel2 := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel2()
		_, _ = admin.Exec(c, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
		_ = admin.Close(c)
		cancel()
	}
}

func gateDatabaseURL(base, name string) string {
	if i := strings.LastIndex(base, "/"); i > 0 {
		return base[:i+1] + name
	}
	return base
}

// redactGateURL keeps a password out of a skip message.
func redactGateURL(u string) string {
	at := strings.LastIndex(u, "@")
	scheme := strings.Index(u, "://")
	if at < 0 || scheme < 0 {
		return u
	}
	return u[:scheme+3] + "..." + u[at:]
}

func rehearseForGate(t *testing.T, files map[string]string, rows int, name string) insights.Rehearsal {
	t.Helper()
	conn, watch, url, done := requireGateDatabase(t, name, rows)
	defer done()

	fsys := fstest.MapFS{}
	for file, body := range files {
		fsys["migrations/"+file] = &fstest.MapFile{Data: []byte(body)}
	}
	set := insights.Discover(fsys)
	require.Equal(t, insights.ToolSQLDir, set.Tool)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	r, err := insights.Rehearse(ctx, conn, watch, url, set, &insights.SQLApplier{}, 1000)
	require.NoError(t, err)
	return r
}

func TestGatePostgres_ARealExclusiveLockReachesThePullRequestComment(t *testing.T) {
	// The product's headline promise, end to end: catch exclusive locks before
	// they take checkout down. A real ALTER TABLE holding ACCESS EXCLUSIVE for
	// three seconds, sampled from a second connection, over the two second
	// threshold the manifest defaults to.
	r := rehearseForGate(t, map[string]string{
		"001_slow_alter.sql": "ALTER TABLE orders ADD COLUMN refunded_at timestamp;\nSELECT pg_sleep(3);\n",
	}, 2000, "reallock")
	require.False(t, r.Failed, r.Error)

	findings, migration := migrationFindings(insights.Full{Rehearsal: &r}, defaultGate())

	var lock *report.Finding
	for i := range findings {
		if findings[i].Rule == ruleMigrationLock && findings[i].Where == "orders" {
			lock = &findings[i]
		}
	}
	require.NotNil(t, lock, "a three second ACCESS EXCLUSIVE lock produced no finding: %+v", findings)
	require.Equal(t, report.LevelFail, lock.Level,
		"three seconds is over the two second default and must fail the check")
	require.Contains(t, lock.Title, "AccessExclusiveLock on orders")

	run := report.Run{
		Environment: "shop-x", Branch: "feature/refunds",
		Findings: findings, Migration: migration,
		Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}},
	}
	require.Equal(t, report.VerdictFail, run.Verdict())

	comment := run.Comment()
	require.Contains(t, comment, report.Marker)
	require.Contains(t, comment, "AccessExclusiveLock on orders")
	require.Contains(t, comment, "`migration_lock`")
	// The measured figure, not a fixture's.
	require.Contains(t, comment, "| `orders` | AccessExclusiveLock |")

	err := ciExit(run)
	require.Error(t, err)
	require.Equal(t, aferrors.ExitTestFailure, exitCodeOfSilent(t, err))
}

func TestGatePostgres_ARealFastMigrationIsNotAFinding(t *testing.T) {
	// The other half, and the one that decides whether anybody keeps the check
	// on. Adding a nullable column to a small table is instant and holds its
	// lock for less than one sample, so it must produce nothing.
	r := rehearseForGate(t, map[string]string{
		"001_fast.sql": "ALTER TABLE orders ADD COLUMN note text;\n",
	}, 200, "fastmigration")
	require.False(t, r.Failed, r.Error)

	findings, migration := migrationFindings(insights.Full{Rehearsal: &r}, defaultGate())
	for _, f := range findings {
		require.NotEqual(t, ruleMigrationLock, f.Rule, "a fast migration produced a lock finding")
	}

	run := report.Run{
		Findings: findings, Migration: migration,
		Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}},
	}
	require.Equal(t, report.VerdictPass, run.Verdict())
	require.NoError(t, ciExit(run))
}

func TestGatePostgres_ARealRewriteIsReportedByTheDatabase(t *testing.T) {
	// Reported by an event trigger rather than inferred from the statement,
	// which is the difference between knowing and guessing: whether a type
	// change rewrites depends on the type it is coming FROM, and the statement
	// only says what it is going to.
	r := rehearseForGate(t, map[string]string{
		"001_retype.sql": "ALTER TABLE orders ALTER COLUMN total_cents TYPE bigint;\n",
	}, 2000, "realrewrite")
	require.False(t, r.Failed, r.Error)
	require.Contains(t, r.Rewrote(), "orders")

	findings, _ := migrationFindings(insights.Full{Rehearsal: &r}, defaultGate())
	var rewrite *report.Finding
	for i := range findings {
		if findings[i].Rule == ruleMigrationRewrite {
			rewrite = &findings[i]
		}
	}
	require.NotNil(t, rewrite, "a real table rewrite produced no finding: %+v", findings)
	require.Equal(t, report.LevelWarn, rewrite.Level, "a rewrite warns by default")
	require.Equal(t, "orders", rewrite.Where)
}

func TestGatePostgres_ARealFailedMigrationFailsTheCheck(t *testing.T) {
	// A migration that fails on a branch with production's shape in it is one
	// that would have failed in production, so the check has to say so rather
	// than reporting that it could not measure anything.
	r := rehearseForGate(t, map[string]string{
		"001_broken.sql": "ALTER TABLE customers ADD COLUMN nothing text;\n",
	}, 100, "failedmigration")
	require.True(t, r.Failed, "the migration should not have applied")

	findings, _ := migrationFindings(insights.Full{Rehearsal: &r}, defaultGate())
	require.NotEmpty(t, findings)
	require.Equal(t, ruleMigrationFailed, findings[0].Rule)
	require.Equal(t, report.LevelFail, findings[0].Level)
	require.Contains(t, findings[0].Detail, "customers")

	run := report.Run{Findings: findings,
		Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}}}
	require.Equal(t, report.VerdictFail, run.Verdict())
	require.Equal(t, aferrors.ExitTestFailure, exitCodeOfSilent(t, ciExit(run)))
}

func TestGatePostgres_ARealLintFindingCarriesTheBranchesRowCount(t *testing.T) {
	// Linting a migration against the database it will meet, not linting SQL.
	// The row count is what turns "this locks writes" into "this locks writes
	// on a table with 2000 rows in it".
	r := rehearseForGate(t, map[string]string{
		"001_index.sql": "CREATE INDEX orders_status_idx ON orders (status);\n",
	}, 2000, "reallint")
	require.False(t, r.Failed, r.Error)

	findings, _ := migrationFindings(insights.Full{Rehearsal: &r}, defaultGate())
	var lint *report.Finding
	for i := range findings {
		if findings[i].Rule == string(insights.RuleIndexNotConcurrent) {
			lint = &findings[i]
		}
	}
	require.NotNil(t, lint, "CREATE INDEX without CONCURRENTLY produced no finding: %+v", findings)
	require.Equal(t, report.LevelWarn, lint.Level)
	require.Contains(t, lint.Detail, "rows in orders")

	// A warning does not fail the build. That is the whole point of the level.
	run := report.Run{Findings: findings,
		Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}}}
	require.Equal(t, report.VerdictWarn, run.Verdict())
	require.NoError(t, ciExit(run))
}

// A guard against this file going quiet. Every test above skips without a
// server, and a suite that skipped everything prints ok.
func TestGatePostgres_TheSuiteSaysWhetherItRan(t *testing.T) {
	if os.Getenv("AF_REQUIRE_DATABASE") == "" {
		t.Skip("skipped: AF_REQUIRE_DATABASE is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, gateMaintenanceURL())
	require.NoError(t, err, "AF_REQUIRE_DATABASE is set and there is no usable Postgres")
	require.NoError(t, conn.Close(ctx))
}
