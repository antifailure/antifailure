package insights_test

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
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// productionShaped is a schema big enough for the planner to have a choice to
// make. That is the whole point of comparing plans on a branch: on an empty
// database Postgres picks a sequential scan for everything because a
// sequential scan of nothing is free, so both sides of the diff are plans that
// will never run in production and the comparison says nothing.
//
// 50,000 orders is enough for an index scan to beat a sequential scan by a
// wide margin, and small enough that copying the template is quick.
const productionShaped = `
CREATE TABLE users (
  id        bigserial PRIMARY KEY,
  email     varchar(64) NOT NULL UNIQUE,
  full_name text NOT NULL
);
CREATE TABLE orders (
  id          bigserial PRIMARY KEY,
  user_id     bigint NOT NULL REFERENCES users(id),
  status      text NOT NULL,
  total_cents int NOT NULL,
  note        text,
  placed_at   timestamp NOT NULL DEFAULT now()
);
INSERT INTO users (email, full_name)
  SELECT 'user' || g || '@example.test', 'User ' || g FROM generate_series(1, 5000) g;
INSERT INTO orders (user_id, status, total_cents, note)
  SELECT (g % 5000) + 1,
         CASE WHEN g % 7 = 0 THEN 'refunded' ELSE 'paid' END,
         (g % 5000) + 100,
         'note ' || g
  FROM generate_series(1, 50000) g;
CREATE INDEX orders_user_id_idx ON orders (user_id);
CREATE VIEW order_notes AS SELECT id, note FROM orders;
ANALYZE;
`

type testDB struct {
	conn  *pgx.Conn
	watch *pgx.Conn
	url   secrets.Value
}

// testDatabaseURL is the Postgres the whole project's tests share, the one
// `just db` starts. AF_TEST_DATABASE_URL overrides it, and the name and the
// default are the ones web/apps/api/test/harness.ts already uses, because two
// conventions for the same server is one too many.
//
// These tests need a real server rather than a real provider. What they prove
// is what Postgres does with a migration, an EXPLAIN and a lock, and none of
// that is different for a database the Docker provider made. Standing up a
// provider and committing a golden image per test would spend minutes on
// machinery none of the assertions are about.
const testDatabaseURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

// templateDB holds the schema once. Every test copies it with CREATE DATABASE
// ... TEMPLATE, which Postgres does by copying the files, so the rows, the
// indexes and the planner statistics all arrive together and the copy takes
// about as long as a connection does.
const templateDB = "af_insights_template"

var shared struct {
	url  string
	skip string
}

func TestMain(m *testing.M) {
	code := func() int {
		defer setupShared()()
		// A machine with no test Postgres has not found a bug, so the default
		// is to skip. A machine that was SUPPOSED to have one has found a very
		// large bug, and skipping there is the worst outcome available: `go
		// test` prints nothing for a skip, so the package reports ok having
		// examined almost nothing.
		//
		// This is not hypothetical. The engine job in CI had no Postgres at
		// all, so every test in this file skipped on every run while the job
		// went green, and the row in STATUS.md said proven on the strength of
		// it. AF_REQUIRE_DATABASE is what makes that state loud instead.
		if shared.skip != "" && os.Getenv("AF_REQUIRE_DATABASE") != "" {
			fmt.Fprintf(os.Stderr,
				"AF_REQUIRE_DATABASE is set and there is no usable Postgres, so these tests "+
					"would have skipped silently: %s\n", shared.skip)
			return 1
		}
		return m.Run()
	}()
	// G3, after the teardown above rather than instead of it. goleak.
	// VerifyTestMain cannot be used here because this package already owns
	// TestMain, and the check has to run once the deferred provider teardown
	// has closed its containers and connections, or every run would report the
	// suite's own fixtures as leaks.
	//
	// This was taken out once, when it passed on a workstation and failed in
	// CI with net/http persistConn readLoop and writeLoop pairs whose stacks
	// carried no frame from this repository. They were this package's, by a
	// longer route than reading it suggests: waiting on the rehearsal
	// container abandoned the wait when the deadline landed at the same moment
	// as the result, which parks the goroutine that closes the response body
	// and strands its connection where Client.Close cannot reach it. That is
	// fixed at the source, in dockerutil.AwaitExit, which no caller can
	// abandon. The check belongs back here now, because a check that is off is
	// a check that finds nothing.
	if code == 0 {
		if err := goleak.Find(); err != nil {
			fmt.Fprintf(os.Stderr, "goroutines outlived the suite: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}

func maintenanceURL() string {
	if u := os.Getenv("AF_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return testDatabaseURL
}

// setupShared builds the template database and returns the teardown.
func setupShared() func() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	shared.url = maintenanceURL()

	admin, err := pgx.Connect(ctx, shared.url)
	if err != nil {
		cancel()
		shared.skip = fmt.Sprintf("no Postgres at %s: %v", redactURL(shared.url), err)
		return func() {}
	}

	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+templateDB+" WITH (FORCE)"); err != nil {
		_ = admin.Close(ctx)
		cancel()
		shared.skip = fmt.Sprintf("the template database could not be cleared: %v", err)
		return func() {}
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+templateDB); err != nil {
		_ = admin.Close(ctx)
		cancel()
		shared.skip = fmt.Sprintf("the template database could not be created: %v", err)
		return func() {}
	}

	loader, err := pgx.Connect(ctx, databaseURL(templateDB))
	if err != nil {
		_ = admin.Close(ctx)
		cancel()
		shared.skip = fmt.Sprintf("the template database could not be reached: %v", err)
		return func() {}
	}
	_, err = loader.Exec(ctx, productionShaped)
	// The loader is closed either way: CREATE DATABASE ... TEMPLATE refuses
	// while anything is connected to the template.
	_ = loader.Close(ctx)
	if err != nil {
		_ = admin.Close(ctx)
		cancel()
		shared.skip = fmt.Sprintf("the fixture schema could not be loaded: %v", err)
		return func() {}
	}

	return func() {
		c, cancel2 := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel2()
		_, _ = admin.Exec(c, "DROP DATABASE IF EXISTS "+templateDB+" WITH (FORCE)")
		_ = admin.Close(c)
		cancel()
	}
}

// databaseURL points the shared server's connection string at one database.
func databaseURL(name string) string {
	if i := strings.LastIndex(shared.url, "/"); i > 0 {
		return shared.url[:i+1] + name
	}
	return shared.url
}

// redactURL keeps a password out of a skip message. A test that names the
// reason it skipped must not name the credential as well.
func redactURL(u string) string {
	at := strings.LastIndex(u, "@")
	scheme := strings.Index(u, "://")
	if at < 0 || scheme < 0 {
		return u
	}
	return u[:scheme+3] + "..." + u[at:]
}

// requireDatabase copies the template into a database of its own.
//
// A database per test rather than a shared one, because a rehearsal applies
// migrations and a plan diff drops an index, and a test that leaves either
// behind changes what the next one measures. It skips rather than fails when
// there is no server, because a machine with no test Postgres has not found a
// bug.
func requireDatabase(t *testing.T, name string) (testDB, func()) {
	t.Helper()
	if shared.skip != "" {
		t.Skip("skipped: " + shared.skip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	db := "af_insights_" + name

	admin, err := pgx.Connect(ctx, shared.url)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
	require.NoError(t, err)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+db+" TEMPLATE "+templateDB)
	require.NoError(t, err)

	url := secrets.New(databaseURL(db))
	conn, err := pgx.Connect(ctx, url.Reveal())
	require.NoError(t, err)
	watch, err := pgx.Connect(ctx, url.Reveal())
	require.NoError(t, err)

	return testDB{conn: conn, watch: watch, url: url}, func() {
		_ = watch.Close(context.Background())
		_ = conn.Close(context.Background())
		c, cancel2 := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel2()
		_, _ = admin.Exec(c, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
		_ = admin.Close(c)
		cancel()
	}
}

func migrationsFS(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, body := range files {
		out["migrations/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	return out
}

func rehearse(t *testing.T, db testDB, files map[string]string) insights.Rehearsal {
	t.Helper()
	set := insights.Discover(migrationsFS(files))
	require.Equal(t, insights.ToolSQLDir, set.Tool)
	r, err := insights.Rehearse(context.Background(), db.conn, db.watch, db.url,
		set, &insights.SQLApplier{}, insights.LargeTableRows)
	require.NoError(t, err)
	return r
}

func TestRehearse_TimesEveryStatementSeparately(t *testing.T) {
	db, done := requireDatabase(t, "insightstiming")
	defer done()

	r := rehearse(t, db, map[string]string{
		"001_add.sql": "ALTER TABLE orders ADD COLUMN currency text;\n" +
			"UPDATE orders SET currency = 'usd';\n",
	})
	require.False(t, r.Failed, r.Error)
	require.Len(t, r.Statements, 2)

	// The point of the whole rehearsal. Every tool reports one number for the
	// file, and the number somebody needs is which statement inside it took
	// the time: adding a nullable column is instant and updating 50,000 rows
	// is not.
	add, update := r.Statements[0], r.Statements[1]
	require.Contains(t, add.SQL, "ADD COLUMN")
	require.Contains(t, update.SQL, "UPDATE")
	require.Greater(t, update.MS, add.MS,
		"backfilling 50,000 rows must not be reported as faster than adding a column")
	require.Greater(t, r.TotalMS, 0.0)
	require.Equal(t, 1, r.Statements[0].Index)
	require.Equal(t, 2, r.Statements[1].Index)
}

func TestRehearse_ReportsAMigrationThatDoesNotApply(t *testing.T) {
	db, done := requireDatabase(t, "insightsfail")
	defer done()

	// A duplicate unique index, which is the AF-DB-030 case: a migration that
	// fails here is a migration that would have failed in production, found
	// before merge rather than during a deploy window.
	r := rehearse(t, db, map[string]string{
		"001_dup.sql": "CREATE UNIQUE INDEX users_email_key ON users (email);",
	})
	require.True(t, r.Failed)
	require.Contains(t, r.Error, "users_email_key")

	// And the branch is left as a failed deploy leaves production: the
	// migration ran in a transaction, so nothing from it survived. The
	// fixture's own UNIQUE constraint is what the name collides with, so the
	// assertion is that the index count did not change rather than that the
	// name is absent.
	var indexes int
	require.NoError(t, db.conn.QueryRow(context.Background(),
		"SELECT count(*) FROM pg_indexes WHERE tablename = 'users'").Scan(&indexes))
	require.Equal(t, 2, indexes,
		"a failed migration must not leave half of itself behind: the fixture's "+
			"primary key and unique constraint are the only two indexes on users")
}

func TestRehearse_StopsAtTheFirstFailure(t *testing.T) {
	db, done := requireDatabase(t, "insightsstop")
	defer done()

	r := rehearse(t, db, map[string]string{
		"001_ok.sql":    "ALTER TABLE orders ADD COLUMN a text;",
		"002_bad.sql":   "ALTER TABLE nothing_here ADD COLUMN b text;",
		"003_never.sql": "ALTER TABLE orders ADD COLUMN c text;",
	})
	require.True(t, r.Failed)
	for _, s := range r.Statements {
		require.NotContains(t, s.SQL, "COLUMN c",
			"a migration after a failure must not run; that is not what a deploy does")
	}
}

func TestRehearse_DetectsARewriteFromTheDatabaseRatherThanTheStatement(t *testing.T) {
	db, done := requireDatabase(t, "insightsrewrite")
	defer done()

	// int to bigint is the classic one. It looks like a widening and Postgres
	// copies all 50,000 rows under a lock nothing can read through. The
	// event trigger is Postgres saying so, which is a stronger answer than
	// any amount of reading the statement.
	r := rehearse(t, db, map[string]string{
		"001_widen.sql": "ALTER TABLE orders ALTER COLUMN total_cents TYPE bigint;",
	})
	require.False(t, r.Failed, r.Error)
	require.Contains(t, r.Rewrote(), "orders")
	require.Contains(t, r.Explain(), "rewrote")
}

func TestRehearse_DoesNotClaimARewriteWhenPostgresSkipsIt(t *testing.T) {
	db, done := requireDatabase(t, "insightsnorewrite")
	defer done()

	// varchar to text shares an on disk representation, so Postgres changes
	// the catalogue and touches no rows. Reporting a rewrite here would be a
	// false alarm on the exact change somebody made to avoid one.
	r := rehearse(t, db, map[string]string{
		"001_totext.sql": "ALTER TABLE users ALTER COLUMN email TYPE text;",
	})
	require.False(t, r.Failed, r.Error)
	require.Empty(t, r.Rewrote())
}

func TestRehearse_SamplesTheLockAMigrationHolds(t *testing.T) {
	db, done := requireDatabase(t, "insightslocks")
	defer done()

	// A deliberately long held lock. pg_sleep inside the same transaction as
	// the ALTER keeps the ACCESS EXCLUSIVE lock held for long enough that the
	// sampler, which asks every 250 milliseconds, cannot miss it.
	r := rehearse(t, db, map[string]string{
		"001_slow.sql": "ALTER TABLE orders ADD COLUMN slow_col text;\nSELECT pg_sleep(1);\n",
	})
	require.False(t, r.Failed, r.Error)

	var found *insights.LockHold
	for i := range r.Locks {
		if r.Locks[i].Table == "orders" {
			found = &r.Locks[i]
		}
	}
	require.NotNil(t, found, "the lock on the table being altered was not seen at all")
	require.Equal(t, "AccessExclusiveLock", found.Mode,
		"ALTER TABLE ADD COLUMN takes the strongest lock there is, and that is the point")
	require.GreaterOrEqual(t, found.HeldMS, 500.0,
		"a lock held for a second must be reported as held for most of a second")
}

func TestRehearse_LintsAgainstTheBranchesRealRowCounts(t *testing.T) {
	db, done := requireDatabase(t, "insightslint")
	defer done()

	// large_table_rows is set below this branch's size so the rule has
	// something real to fire on. This is the difference between linting SQL
	// and linting a migration against the database it will meet.
	set := insights.Discover(migrationsFS(map[string]string{
		"001_unsafe.sql": "ALTER TABLE orders DROP COLUMN note;",
	}))
	r, err := insights.Rehearse(context.Background(), db.conn, db.watch, db.url,
		set, &insights.SQLApplier{}, 1000)
	require.NoError(t, err)

	// A stock Postgres sets no lock_timeout, so that rule fires here too. It
	// is looked up by rule rather than by position for that reason: an
	// assertion on r.Lint[0] is an assertion about which rules happen to
	// exist.
	dropped := findingFor(t, r.Lint, insights.RuleDropColumnInView)
	require.Contains(t, dropped.Detail, "order_notes",
		"the view that reads the column has to be named, or the finding is not actionable")
	require.Greater(t, dropped.Rows, int64(10000),
		"the row count comes from the branch, not from a guess")
}

// findingFor picks one rule's finding out of a report.
func findingFor(
	t *testing.T, findings []insights.LintFinding, rule insights.Rule,
) insights.LintFinding {
	t.Helper()
	for _, f := range findings {
		if f.Rule == rule {
			return f
		}
	}
	require.FailNowf(t, "rule did not fire", "%s is not among %v", rule, rulesIn(findings))
	return insights.LintFinding{}
}

func TestRehearse_ReadsLockTimeoutFromTheServerRatherThanGuessing(t *testing.T) {
	db, done := requireDatabase(t, "insightslocktimeout")
	defer done()

	// The lock is held long enough for the sampler to see it, because the
	// point of this rule on a rehearsal is that the finding carries the real
	// length of the window rather than an argument about one.
	r := rehearse(t, db, map[string]string{
		"001_slow.sql": "ALTER TABLE orders ADD COLUMN region text;\nSELECT pg_sleep(1);\n",
	})
	require.False(t, r.Failed, r.Error)

	f := findingFor(t, r.Lint, insights.RuleNoLockTimeout)
	require.Equal(t, "orders", f.Table)
	require.Greater(t, f.Rows, int64(10000),
		"the row count comes from the branch, and it is what makes a queued lock an outage")
	require.Contains(t, f.Detail, "ACCESS EXCLUSIVE")
	require.Contains(t, f.Detail, "AccessExclusiveLock",
		"the sampler's own observation has to reach the finding, or the rule is only an "+
			"argument about what a migration might do")

	// And the rule goes quiet when the server already sets it, which is how
	// most projects that set it do it. ALTER DATABASE reaches a session at the
	// moment it connects, so this is proved on fresh connections rather than
	// on the ones the first rehearsal used.
	ctx := context.Background()
	var name string
	require.NoError(t, db.conn.QueryRow(ctx, "SELECT current_database()").Scan(&name))
	_, err := db.conn.Exec(ctx, "ALTER DATABASE "+name+" SET lock_timeout = '3s'")
	require.NoError(t, err)

	conn, err := pgx.Connect(ctx, db.url.Reveal())
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	watch, err := pgx.Connect(ctx, db.url.Reveal())
	require.NoError(t, err)
	defer func() { _ = watch.Close(ctx) }()

	set := insights.Discover(migrationsFS(map[string]string{
		"002_more.sql": "ALTER TABLE orders ADD COLUMN currency text;",
	}))
	quiet, err := insights.Rehearse(ctx, conn, watch, db.url, set,
		&insights.SQLApplier{}, insights.LargeTableRows)
	require.NoError(t, err)
	require.False(t, quiet.Failed, quiet.Error)
	require.NotContains(t, rulesIn(quiet.Lint), insights.RuleNoLockTimeout,
		"the setting is on the database, so warning that nothing sets it would be false")
}

func TestRehearse_OnlyAppliesWhatThePendingSetSays(t *testing.T) {
	db, done := requireDatabase(t, "insightspending")
	defer done()
	ctx := context.Background()

	// A plain SQL directory has no history table we know how to read, so
	// everything is pending. Prove the applier honours the set it is given
	// rather than reaching for the whole directory.
	set := insights.Discover(migrationsFS(map[string]string{
		"001_a.sql": "ALTER TABLE orders ADD COLUMN col_a text;",
		"002_b.sql": "ALTER TABLE orders ADD COLUMN col_b text;",
	}))
	only := insights.MigrationSet{Tool: set.Tool, Dir: set.Dir, Migrations: set.Migrations[1:]}
	r, err := insights.Rehearse(ctx, db.conn, db.watch, db.url, only,
		&insights.SQLApplier{}, insights.LargeTableRows)
	require.NoError(t, err)
	require.False(t, r.Failed, r.Error)

	var cols []string
	rows, err := db.conn.Query(ctx,
		"SELECT column_name FROM information_schema.columns WHERE table_name = 'orders'")
	require.NoError(t, err)
	for rows.Next() {
		var c string
		require.NoError(t, rows.Scan(&c))
		cols = append(cols, c)
	}
	rows.Close()
	require.Contains(t, cols, "col_b")
	require.NotContains(t, cols, "col_a")
}

func TestApplied_AMissingHistoryTableIsNotAnError(t *testing.T) {
	db, done := requireDatabase(t, "insightshistory")
	defer done()

	// A fresh branch of a project that keeps its schema elsewhere. Treating
	// this as an error would refuse to rehearse the one case where every
	// migration is pending.
	set := insights.MigrationSet{Tool: insights.ToolPrisma}
	applied, err := set.Applied(context.Background(), db.conn)
	require.NoError(t, err)
	require.Empty(t, applied)
}

func TestApplied_ReadsARealHistoryTable(t *testing.T) {
	db, done := requireDatabase(t, "insightshistory2")
	defer done()
	ctx := context.Background()

	_, err := db.conn.Exec(ctx, `
CREATE TABLE _prisma_migrations (
  id text PRIMARY KEY, migration_name text NOT NULL, finished_at timestamptz);
INSERT INTO _prisma_migrations VALUES
  ('1', '20240101120000_init', now()),
  ('2', '20240202090000_half', NULL);`)
	require.NoError(t, err)

	set := insights.MigrationSet{Tool: insights.ToolPrisma, Migrations: []insights.Migration{
		{Version: "20240101120000_init", Name: "a"},
		{Version: "20240202090000_half", Name: "b"},
		{Version: "20240303080000_new", Name: "c"},
	}}
	applied, err := set.Applied(ctx, db.conn)
	require.NoError(t, err)

	pending := set.Pending(applied)
	require.Len(t, pending, 2)
	// The half applied one is pending. finished_at is null, which is what
	// Prisma writes for a migration that started and did not land, and
	// treating it as applied would skip the migration that needs rerunning
	// most.
	require.Equal(t, "b", pending[0].Name)
	require.Equal(t, "c", pending[1].Name)
}

func TestPlanDiff_FindsTheDroppedIndex(t *testing.T) {
	db, done := requireDatabase(t, "insightsplan")
	defer done()
	ctx := context.Background()

	// The classic finding, planted. A query that reaches orders by its
	// user_id index before the migration reaches it end to end afterwards,
	// and nothing about the query changed.
	statements := []string{"SELECT id, status FROM orders WHERE user_id = $1"}

	before, err := insights.CapturePlans(ctx, db.conn, statements)
	require.NoError(t, err)
	require.Empty(t, before[0].Error, before[0].Error)
	require.NotEmpty(t, before[0].IndexScans["orders"],
		"the fixture is wrong: this query has to use the index before the migration")

	_, err = db.conn.Exec(ctx, "DROP INDEX orders_user_id_idx")
	require.NoError(t, err)

	after, err := insights.CapturePlans(ctx, db.conn, statements)
	require.NoError(t, err)
	require.Contains(t, after[0].SeqScans, "orders")

	schema, err := insights.CaptureSchema(ctx, db.conn)
	require.NoError(t, err)
	findings := insights.DiffPlans(before, after, insights.PlanOptions{
		LargeTableRows: 1000, CostFactor: 2, Rows: schema.Rows,
	})
	require.NotEmpty(t, findings)
	require.Equal(t, insights.PlanNewSeqScan, findings[0].Kind)
	require.Equal(t, "orders", findings[0].Table)
	require.Contains(t, findings[0].Detail, "orders_user_id_idx",
		"a finding that does not name the index that was lost is not actionable")
	require.Greater(t, findings[0].After, findings[0].Before)
}

func TestPlanDiff_SaysNothingWhenNothingChanged(t *testing.T) {
	db, done := requireDatabase(t, "insightsplanquiet")
	defer done()
	ctx := context.Background()

	// The half that decides whether anybody keeps the check on. Two captures
	// of the same database must produce no findings at all, including no cost
	// drift from statistics being gathered between them.
	statements := []string{
		"SELECT id, status FROM orders WHERE user_id = $1",
		"SELECT count(*) FROM orders WHERE status = $1",
	}
	before, err := insights.CapturePlans(ctx, db.conn, statements)
	require.NoError(t, err)
	after, err := insights.CapturePlans(ctx, db.conn, statements)
	require.NoError(t, err)

	require.Empty(t, insights.DiffPlans(before, after, insights.PlanOptions{
		LargeTableRows: 1000, CostFactor: 2,
	}))
}

func TestPlanDiff_IgnoresASequentialScanOnASmallTable(t *testing.T) {
	db, done := requireDatabase(t, "insightsplansmall")
	defer done()
	ctx := context.Background()

	_, err := db.conn.Exec(ctx,
		"CREATE TABLE flags (id int primary key, name text); "+
			"INSERT INTO flags SELECT g, 'f' || g FROM generate_series(1, 40) g; ANALYZE flags;")
	require.NoError(t, err)

	statements := []string{"SELECT name FROM flags WHERE name = $1"}
	before, err := insights.CapturePlans(ctx, db.conn, statements)
	require.NoError(t, err)
	after, err := insights.CapturePlans(ctx, db.conn, statements)
	require.NoError(t, err)

	schema, err := insights.CaptureSchema(ctx, db.conn)
	require.NoError(t, err)
	// On a table of forty rows a sequential scan is the right plan, and
	// flagging it is the noise that gets a check turned off.
	require.Empty(t, insights.DiffPlans(before, after, insights.PlanOptions{
		LargeTableRows: 1000, CostFactor: 2, Rows: schema.Rows,
	}))
}

func TestCapturePlans_ExplainsAParameterisedStatement(t *testing.T) {
	db, done := requireDatabase(t, "insightsgeneric")
	defer done()

	// pg_stat_statements normalises every literal to $1, so if a
	// parameterised statement cannot be explained then nothing can be. This
	// is what GENERIC_PLAN is for and it is the load bearing assumption of
	// the whole plan diff.
	plans, err := insights.CapturePlans(context.Background(), db.conn,
		[]string{"SELECT id FROM orders WHERE user_id = $1 AND status = $2"})
	require.NoError(t, err)
	require.Empty(t, plans[0].Error,
		"a statement with parameters must be explainable, or the plan diff has nothing to read")
	require.Greater(t, plans[0].Cost, 0.0)
}

func TestCapturePlans_ReportsAStatementItCouldNotExplain(t *testing.T) {
	db, done := requireDatabase(t, "insightsbadplan")
	defer done()

	plans, err := insights.CapturePlans(context.Background(), db.conn,
		[]string{"SELECT * FROM a_table_that_does_not_exist"})
	require.NoError(t, err, "one unexplainable statement must not discard the rest")
	require.NotEmpty(t, plans[0].Error)
}

func TestCaptureSchema_ReadsWhatTheLintNeeds(t *testing.T) {
	db, done := requireDatabase(t, "insightsschema")
	defer done()

	s, err := insights.CaptureSchema(context.Background(), db.conn)
	require.NoError(t, err)
	require.Greater(t, s.Rows["orders"], int64(10000))
	require.Equal(t, "varchar(64)", s.Columns["users.email"])
	require.Equal(t, "int4", s.Columns["orders.total_cents"])
	require.Contains(t, s.ViewsUsing["orders.note"], "order_notes")
	require.Contains(t, s.IndexesUsing["orders.user_id"], "orders_user_id_idx")
}

func TestCollect_FindsThePlantedNPlusOne(t *testing.T) {
	db, done := requireDatabase(t, "insightsnplus1")
	defer done()
	ctx := context.Background()

	if _, err := db.conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_stat_statements"); err != nil {
		t.Skipf("skipped: pg_stat_statements is not available here: %v", err)
	}
	// The extension has to be preloaded to record anything. On an image where
	// it is not, saying so is the honest outcome, and the report says so too.
	if _, err := db.conn.Exec(ctx, "SELECT pg_stat_statements_reset()"); err != nil {
		t.Skipf("skipped: pg_stat_statements is present but not tracking: %v", err)
	}

	baselineRun := func(times int) insights.Report {
		_, err := db.conn.Exec(ctx, "SELECT pg_stat_statements_reset()")
		require.NoError(t, err)
		for i := 0; i < times; i++ {
			var id int64
			_ = db.conn.QueryRow(ctx,
				"SELECT id FROM orders WHERE user_id = $1 LIMIT 1", i+1).Scan(&id)
		}
		r, err := insights.Collect(ctx, db.conn, 20)
		require.NoError(t, err)
		return r
	}

	base := baselineRun(4)
	if len(base.Queries) == 0 {
		t.Skip("skipped: this server records no statements, so there is nothing to compare")
	}
	branch := baselineRun(412)

	diff := branch.CompareTo(base, insights.Thresholds{CallGrowth: 2, TimeGrowth: 2, MinMS: 10})
	require.NotEmpty(t, diff.Busier, "412 calls where there were 4 is the bug this exists for")
	require.Contains(t, diff.Busier[0].Text, "orders")
	require.Greater(t, diff.Busier[0].Factor, 50.0)
}

func TestCollect_SaysSoWhenTheExtensionIsMissing(t *testing.T) {
	db, done := requireDatabase(t, "insightsnostats")
	defer done()
	ctx := context.Background()

	_, _ = db.conn.Exec(ctx, "DROP EXTENSION IF EXISTS pg_stat_statements")
	r, err := insights.Collect(ctx, db.conn, 20)
	require.NoError(t, err)

	var probe int
	if err := db.conn.QueryRow(ctx,
		"SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements'").Scan(&probe); err == nil {
		t.Skip("skipped: the extension could not be removed, so there is nothing to prove")
	}
	// An insight that silently reports nothing because an extension is
	// missing looks exactly like a clean bill of health.
	require.NotEmpty(t, r.Missing)
	require.Contains(t, strings.Join(r.Missing, " "), "pg_stat_statements")
}

func TestRun_HonoursEveryCheckTheManifestTurnedOff(t *testing.T) {
	db, done := requireDatabase(t, "insightsrunoff")
	defer done()
	ctx := context.Background()

	set := insights.Discover(migrationsFS(map[string]string{
		"001_add.sql": "ALTER TABLE orders ADD COLUMN off_col text;",
	}))
	target := &insights.Target{
		Conn: db.conn, Watch: db.watch, URL: db.url, Set: set,
		Applier: &insights.SQLApplier{},
	}

	full, err := insights.Run(ctx, insights.Options{
		Config: insights.Config{
			Enabled: true, MigrationRehearsal: false, QueryRegression: false, PlanDiff: false,
			RegressionFactor: 2, RegressionMinMS: 10, LargeTableRows: 1000,
		},
		Branch: db.conn, Limit: 5, Rehearsal: target,
	})
	require.NoError(t, err)
	require.Nil(t, full.Rehearsal, "migration_rehearsal: false must not run the migrations")
	require.Empty(t, full.PlanFindings)
	require.Len(t, full.Off, 3)

	// And it really did not run them: a check turned off has to leave the
	// database alone, not run and discard the answer.
	var count int
	require.NoError(t, db.conn.QueryRow(ctx,
		"SELECT count(*) FROM information_schema.columns "+
			"WHERE table_name = 'orders' AND column_name = 'off_col'").Scan(&count))
	require.Zero(t, count)
}

func TestRun_RehearsalOnPlanDiffOffStillRunsTheMigrations(t *testing.T) {
	db, done := requireDatabase(t, "insightsrunplanoff")
	defer done()
	ctx := context.Background()

	set := insights.Discover(migrationsFS(map[string]string{
		"001_drop.sql": "DROP INDEX orders_user_id_idx;",
	}))
	full, err := insights.Run(ctx, insights.Options{
		Config: insights.Config{
			Enabled: true, MigrationRehearsal: true, QueryRegression: true, PlanDiff: false,
			RegressionFactor: 2, RegressionMinMS: 10, LargeTableRows: 1000,
		},
		Branch: db.conn, Limit: 5,
		Rehearsal: &insights.Target{
			Conn: db.conn, Watch: db.watch, URL: db.url, Set: set,
			Applier: &insights.SQLApplier{},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, full.Rehearsal)
	require.False(t, full.Rehearsal.Failed, full.Rehearsal.Error)
	require.Empty(t, full.PlanFindings, "plan_diff: false turns off exactly that check")
	require.Equal(t, []string{"the plan diff, because insights.plan_diff is false"}, full.Off)
}

func TestRun_EndToEndFindsTheMigrationThatCostsThePlan(t *testing.T) {
	db, done := requireDatabase(t, "insightsrunall")
	defer done()
	ctx := context.Background()

	if _, err := db.conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_stat_statements"); err != nil {
		t.Skipf("skipped: pg_stat_statements is not available here: %v", err)
	}
	if _, err := db.conn.Exec(ctx, "SELECT pg_stat_statements_reset()"); err != nil {
		t.Skipf("skipped: pg_stat_statements is present but not tracking: %v", err)
	}
	// The workload. The statements the plan diff explains are the ones the
	// application actually ran, which is why they come from
	// pg_stat_statements rather than from a list somebody maintains.
	for i := 0; i < 20; i++ {
		var id int64
		_ = db.conn.QueryRow(ctx,
			"SELECT id FROM orders WHERE user_id = $1 LIMIT 1", i+1).Scan(&id)
	}
	stats, err := insights.Collect(ctx, db.conn, 20)
	require.NoError(t, err)
	if len(stats.Queries) == 0 {
		t.Skip("skipped: this server records no statements, so there is nothing to explain")
	}

	set := insights.Discover(migrationsFS(map[string]string{
		"001_tidy.sql": "DROP INDEX orders_user_id_idx;",
	}))
	// large_table_rows is the manifest's, and the fixture is smaller than the
	// default million, so the manifest sets it. That is the honest way to run
	// this: with the default, a sequential scan on a 50,000 row table is
	// correctly not a finding, and a test that quietly changed the threshold
	// in code would prove the plan diff works and not that the manifest
	// reaches it.
	full, err := insights.Run(ctx, insights.Options{
		Config: insights.Configure(&schema.Insights{LargeTableRows: 1000}),
		Branch: db.conn, Limit: 20,
		Rehearsal: &insights.Target{
			Conn: db.conn, Watch: db.watch, URL: db.url, Set: set,
			Applier: &insights.SQLApplier{},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, full.Rehearsal)
	require.False(t, full.Rehearsal.Failed, full.Rehearsal.Error)
	require.False(t, full.Clean())

	// A migration that reads as tidying up an unused index, and the query the
	// application runs twenty times a page now reads the whole table.
	require.NotEmpty(t, full.PlanFindings)
	require.Equal(t, insights.PlanNewSeqScan, full.PlanFindings[0].Kind)
	out := full.Explain()
	require.Contains(t, out, "read end to end")
	require.Contains(t, out, "orders_user_id_idx")
	fmt.Println(out)
}

func TestRun_SaysTheMigrationsWereNotRehearsedWhenTheyCouldNotBe(t *testing.T) {
	db, done := requireDatabase(t, "insightsnotarget")
	defer done()

	// No rehearsal branch. The failure this guards against is a report that
	// looks exactly like a rehearsal that found nothing wrong.
	full, err := insights.Run(context.Background(), insights.Options{
		Config: insights.Configure(nil), Branch: db.conn, Limit: 5,
	})
	require.NoError(t, err)
	require.Nil(t, full.Rehearsal)
	require.Contains(t, strings.Join(full.Missing, " "), "not rehearsed")
}

func TestRun_SaysTheCallersReasonForNotRehearsing(t *testing.T) {
	db, done := requireDatabase(t, "insightsreason")
	defer done()

	// "The migrations were not rehearsed" is only useful with the because.
	// The orchestrator knows which of several reasons it was, so the reason
	// travels down rather than being guessed at here.
	const reason = "the golden this environment came from is no longer verified"
	full, err := insights.Run(context.Background(), insights.Options{
		Config: insights.Configure(nil), Branch: db.conn, Limit: 5,
		NoRehearsalReason: reason,
	})
	require.NoError(t, err)
	require.Nil(t, full.Rehearsal)
	require.Contains(t, full.Missing, reason)
	require.Contains(t, full.Explain(), reason)
}

func TestRehearse_TimesEachStatementFromTheServerWhenTheApplierCannot(t *testing.T) {
	db, done := requireDatabase(t, "insightsddlcapture")
	defer done()

	// The case every tool whose migrations are not SQL falls into: something
	// else ran the DDL and we did not see it go past. The server did, and the
	// event trigger pair is how a Rails or Django migration still gets a
	// duration per statement.
	//
	// The middle statement is told to sleep for a second, and that second is
	// the only number here the machine does not get a vote on. What breaks
	// silently in this path is not the size of a duration but where it lands:
	// one statement's cost charged to its neighbour, one total copied onto
	// every row, or a running total that grows down the list. Each of those
	// moves the planted second somewhere it does not belong, and each of them
	// survives a test that only compares two real statements to each other.
	// That comparison is what used to be here, and it failed in CI on a pull
	// request that changed no engine code at all: a contended runner spent
	// 84ms adding a nullable column and 33ms building an index over 50,000
	// rows, inverting an ordering that was never the claim.
	const plantedSleepMS = 1000.0
	r, err := insights.Rehearse(context.Background(), db.conn, db.watch, db.url,
		insights.MigrationSet{Tool: insights.ToolRails},
		&opaqueApplier{url: db.url, sql: []string{
			"ALTER TABLE orders ADD COLUMN currency text",
			// A statement whose duration is a fact rather than a measurement.
			// The server cannot finish it sooner than the sleep however busy
			// the machine is, so a floor under it is crossed only by the
			// timing being wrong.
			"CREATE MATERIALIZED VIEW slow_orders AS " +
				"SELECT n FROM (SELECT 1 AS n, pg_sleep(1)) s",
			"CREATE INDEX orders_status_idx ON orders (status)",
		}},
		insights.LargeTableRows)
	require.NoError(t, err)
	require.False(t, r.Failed, r.Error)

	require.Len(t, r.Statements, 3,
		"the applier reported nothing, so every statement here came from the server")
	require.Contains(t, r.Statements[0].SQL, "ADD COLUMN")
	require.Contains(t, r.Statements[1].SQL, "MATERIALIZED VIEW")
	require.Contains(t, r.Statements[2].SQL, "CREATE INDEX")

	for i, s := range r.Statements {
		require.Greaterf(t, s.MS, 0.0,
			"statement %d took no time at all, which is what a duration read from the "+
				"transaction's clock rather than the wall clock looks like", i+1)
	}

	// Timed separately means separate numbers. Two clock deltas measured to
	// the microsecond do not land on the same value by chance, so a repeat is
	// one number copied across rows rather than two statements that agreed.
	require.NotEqual(t, r.Statements[0].MS, r.Statements[1].MS)
	require.NotEqual(t, r.Statements[1].MS, r.Statements[2].MS)
	require.NotEqual(t, r.Statements[0].MS, r.Statements[2].MS)

	// The planted second landed on the statement that slept and on neither
	// neighbour. Contention only ever makes a duration longer, so a slow
	// machine cannot push the sleeping statement under its floor. The ceiling
	// over the other two is half the sleep, six times the worst a catalogue
	// only ALTER has been seen to take on the runner this test failed on.
	require.Greater(t, r.Statements[1].MS, plantedSleepMS*0.9,
		"the statement that slept for a second is not reported as taking about a second")
	require.Less(t, r.Statements[0].MS, plantedSleepMS/2,
		"adding a nullable column is catalogue work, so a duration near the sleeping "+
			"statement's is that statement's time charged to the wrong row")
	require.Less(t, r.Statements[2].MS, plantedSleepMS/2,
		"building this index is not a second of work, so a duration near the sleeping "+
			"statement's is that statement's time charged to the wrong row")

	// And every duration is a slice of the one window the applier ran in
	// rather than a copy of the whole of it. A running total, or the total
	// repeated on every row, adds up to more than the window it happened in.
	// The slack is for the two clocks involved, since the window is measured
	// here and each statement is measured by the server.
	var sum float64
	for _, s := range r.Statements {
		sum += s.MS
	}
	require.Less(t, sum, r.TotalMS+50,
		"the statements add up to more than the whole run took, so at least one of them "+
			"is carrying another statement's time")
}

func TestRehearse_ReportsARewriteEvenWhenTheApplierSawNothing(t *testing.T) {
	db, done := requireDatabase(t, "insightsddlrewrite")
	defer done()

	r, err := insights.Rehearse(context.Background(), db.conn, db.watch, db.url,
		insights.MigrationSet{Tool: insights.ToolDjango},
		&opaqueApplier{url: db.url, sql: []string{
			"ALTER TABLE orders ALTER COLUMN total_cents TYPE bigint",
		}},
		insights.LargeTableRows)
	require.NoError(t, err)
	require.False(t, r.Failed, r.Error)
	require.Contains(t, r.Rewrote(), "orders",
		"a Django migration rewriting a table is exactly what this has to catch")
}

// opaqueApplier runs SQL and reports nothing about it, which is what an
// applier that shells out to somebody else's migration tool can honestly say.
type opaqueApplier struct {
	url secrets.Value
	sql []string
}

func (*opaqueApplier) Name() string { return "opaque" }

func (a *opaqueApplier) Apply(
	ctx context.Context, _ secrets.Value, _ []insights.Migration,
) ([]insights.StatementTiming, error) {
	conn, err := pgx.Connect(ctx, a.url.Reveal())
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	for _, s := range a.sql {
		if _, err := conn.Exec(ctx, s); err != nil {
			return nil, err
		}
	}
	return nil, nil
}
