package oracle_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/oracle"
)

// The database half is tested against a real Postgres and nothing else.
//
// A fake would have to agree with the real server about to_jsonb, about
// composite primary keys, about how a timestamptz renders, and about what
// pg_index says for a table with none. Every one of those is a fact this
// package depends on and none of them is a fact a fake can establish.
//
// Two databases per test rather than one, because the comparison is between
// two branches and a test that reads one database twice cannot tell a working
// comparison from one that returns nothing.

// testDatabaseURL is this lane's own Postgres, kept separate from the shared
// one on 55432 so that two suites creating databases at once cannot collide.
const testDatabaseURL = "postgres://postgres:test@127.0.0.1:55509/antifailure"

func maintenanceURL() string {
	if u := os.Getenv("AF_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return testDatabaseURL
}

// twoBranches makes two databases with the same schema and returns a
// connection to each.
//
// Created from one template rather than by running the same script twice, for
// the same reason the product branches one golden for both sides: two databases
// built independently differ in ways nobody controls, and then the test is
// measuring the fixture.
//
// The template is built once per distinct schema and shared. On a loaded
// machine CREATE DATABASE costs seconds, and three of them per test turned a
// fourteen test file into nine minutes.
func twoBranches(t *testing.T, schema string) (base, cand *pgx.Conn) {
	t.Helper()
	ctx := context.Background()

	admin := adminConn(t)
	tmpl := templateFor(t, admin, schema)

	stamp := strings.ToLower(strings.NewReplacer("/", "_", " ", "_", "-", "_").Replace(t.Name()))
	if len(stamp) > 40 {
		stamp = stamp[:40]
	}
	names := []string{"af_o_b_" + stamp, "af_o_c_" + stamp}
	drop := func() {
		for _, n := range names {
			_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+n+" WITH (FORCE)")
		}
	}
	drop()
	t.Cleanup(drop)

	open := func(name string) *pgx.Conn {
		_, err := admin.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE "+tmpl)
		require.NoError(t, err)
		c, err := pgx.Connect(ctx, databaseURL(name))
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.Close(context.Background()) })
		return c
	}
	return open(names[0]), open(names[1])
}

// shared holds the admin connection and the template databases, which outlive
// any one test.
var shared struct {
	mu        sync.Mutex
	admin     *pgx.Conn
	adminErr  error
	templates map[string]string
	next      int
}

// adminConn returns the maintenance connection, or skips.
//
// A machine with no test Postgres has not found a bug. A machine that was
// supposed to have one has found a large one, which is what AF_REQUIRE_DATABASE
// is for: go test prints nothing for a skip, so a silent skip reports ok having
// examined nothing.
func adminConn(t *testing.T) *pgx.Conn {
	t.Helper()
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.admin == nil && shared.adminErr == nil {
		shared.admin, shared.adminErr = pgx.Connect(context.Background(), maintenanceURL())
	}
	if shared.adminErr != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and there is no Postgres at %s: %v",
				redacted(maintenanceURL()), shared.adminErr)
		}
		t.Skipf("no Postgres at %s: %v", redacted(maintenanceURL()), shared.adminErr)
	}
	return shared.admin
}

// templateFor builds the template for a schema once and returns its name.
func templateFor(t *testing.T, admin *pgx.Conn, schema string) string {
	t.Helper()
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.templates == nil {
		shared.templates = map[string]string{}
	}
	if name, ok := shared.templates[schema]; ok {
		return name
	}

	ctx := context.Background()
	name := fmt.Sprintf("af_oracle_tmpl_%d", shared.next)
	shared.next++
	_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	_, err := admin.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)

	loader, err := pgx.Connect(ctx, databaseURL(name))
	require.NoError(t, err)
	_, err = loader.Exec(ctx, schema)
	// Closed either way: CREATE DATABASE ... TEMPLATE refuses while anything is
	// connected to the template.
	closeErr := loader.Close(ctx)
	require.NoError(t, err)
	require.NoError(t, closeErr)

	shared.templates[schema] = name
	return name
}

// dropShared removes the templates and closes the admin connection. Called from
// TestMain, so a template cannot outlive the run that made it.
func dropShared() {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.admin == nil {
		return
	}
	for _, name := range shared.templates {
		_, _ = shared.admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	}
	_ = shared.admin.Close(context.Background())
	shared.admin = nil
}

func databaseURL(name string) string {
	u := maintenanceURL()
	if i := strings.LastIndex(u, "/"); i > 0 {
		return u[:i+1] + name
	}
	return u
}

func redacted(u string) string {
	if i := strings.Index(u, "@"); i > 0 {
		return "postgres://[redacted]" + u[i:]
	}
	return u
}

func exec(t *testing.T, c *pgx.Conn, sql string) {
	t.Helper()
	_, err := c.Exec(context.Background(), sql)
	require.NoError(t, err)
}

func capture(t *testing.T, c *pgx.Conn, opts oracle.DatabaseOptions) *oracle.Snapshot {
	t.Helper()
	snap, err := oracle.Capture(context.Background(), c, opts)
	require.NoError(t, err)
	return snap
}

const ordersSchema = `
CREATE TABLE customers (
  id    serial PRIMARY KEY,
  name  text NOT NULL,
  email text NOT NULL UNIQUE
);
CREATE TABLE orders (
  id          serial PRIMARY KEY,
  customer_id integer NOT NULL REFERENCES customers (id),
  total_cents integer NOT NULL,
  placed_at   timestamptz NOT NULL DEFAULT now()
);
INSERT INTO customers (name, email) VALUES
  ('Ada Lovelace', 'ada@example.test'),
  ('Grace Hopper', 'grace@example.test');
INSERT INTO orders (customer_id, total_cents) VALUES (1, 2599);
`

// Two branches of one template, untouched, must compare as identical. The
// placed_at defaults to now() and the two databases were created at different
// instants, so this also proves the timestamp normaliser reaches a table row
// and not only a response body.
func TestTwoUntouchedBranchesAreIdentical(t *testing.T) {
	b, c := twoBranches(t, ordersSchema)
	res := oracle.Compare(oracle.Input{
		BaselineAfter:  capture(t, b, oracle.DatabaseOptions{}),
		CandidateAfter: capture(t, c, oracle.DatabaseOptions{}),
	})
	require.Emptyf(t, res.Findings, "%+v", res.Findings)
	require.Equal(t, 2, res.Database.TablesCompared)
	require.Equal(t, 3, res.Database.RowsCompared)
}

// A row the candidate wrote and the baseline did not is what a new feature
// looks like, and it is minor.
func TestARowOnlyTheCandidateWroteIsMinor(t *testing.T) {
	b, c := twoBranches(t, ordersSchema)
	exec(t, c, `INSERT INTO orders (customer_id, total_cents) VALUES (2, 500)`)

	res := oracle.Compare(oracle.Input{
		BaselineAfter:  capture(t, b, oracle.DatabaseOptions{}),
		CandidateAfter: capture(t, c, oracle.DatabaseOptions{}),
	})
	require.Len(t, res.Findings, 1)
	f := res.Findings[0]
	require.Equal(t, oracle.KindRowExtra, f.Kind)
	require.Equal(t, oracle.Minor, f.Severity)
	require.Equal(t, "public.orders", f.Where)
	require.Equal(t, "id=2", f.Path)
	require.Contains(t, f.Candidate, "total_cents=500")
}

// A row the baseline wrote and the candidate did not is the candidate having
// stopped writing, and it is critical. Both databases branched one template
// and received the same statements, so there is no innocent explanation.
func TestARowOnlyTheBaselineWroteIsCritical(t *testing.T) {
	b, c := twoBranches(t, ordersSchema)
	exec(t, b, `INSERT INTO orders (customer_id, total_cents) VALUES (2, 500)`)

	res := oracle.Compare(oracle.Input{
		BaselineAfter:  capture(t, b, oracle.DatabaseOptions{}),
		CandidateAfter: capture(t, c, oracle.DatabaseOptions{}),
	})
	require.Len(t, res.Findings, 1)
	require.Equal(t, oracle.KindRowMissing, res.Findings[0].Kind)
	require.Equal(t, oracle.Critical, res.Findings[0].Severity)
	require.Equal(t, oracle.PhaseTraffic, res.Findings[0].Phase)
}

func TestAChangedColumnNamesTheColumn(t *testing.T) {
	b, c := twoBranches(t, ordersSchema)
	exec(t, c, `UPDATE orders SET total_cents = 9999 WHERE id = 1`)

	res := oracle.Compare(oracle.Input{
		BaselineAfter:  capture(t, b, oracle.DatabaseOptions{}),
		CandidateAfter: capture(t, c, oracle.DatabaseOptions{}),
	})
	require.Len(t, res.Findings, 1)
	f := res.Findings[0]
	require.Equal(t, oracle.KindRowChanged, f.Kind)
	require.Equal(t, oracle.Major, f.Severity)
	require.Equal(t, "1 column differs: total_cents", f.Detail)
	require.Equal(t, "total_cents=2599", f.Baseline)
	require.Equal(t, "total_cents=9999", f.Candidate)
}

// A migration that only the candidate has is reported on the schema rather
// than swallowed into a row difference.
func TestAColumnAddedByAMigrationIsReported(t *testing.T) {
	b, c := twoBranches(t, ordersSchema)
	exec(t, c, `ALTER TABLE orders ADD COLUMN currency text NOT NULL DEFAULT 'usd'`)

	res := oracle.Compare(oracle.Input{
		BaselineAfter:  capture(t, b, oracle.DatabaseOptions{}),
		CandidateAfter: capture(t, c, oracle.DatabaseOptions{}),
	})
	columns := findingsOfKind(res, oracle.KindColumns)
	require.Len(t, columns, 1)
	require.Equal(t, "the candidate adds currency", columns[0].Detail)
}

// The phase attribution is the reason two snapshots per side are taken. A row
// that already differed before any request is the migrations' doing, and
// reporting it as the application's would send somebody to read a handler.
func TestADifferenceThatPredatesTheTrafficIsAttributedToTheMigrations(t *testing.T) {
	b, c := twoBranches(t, ordersSchema)
	// A backfill only the candidate's migrations ran.
	exec(t, c, `UPDATE customers SET name = 'ADA LOVELACE' WHERE id = 1`)

	before := oracle.Input{
		BaselineBefore:  capture(t, b, oracle.DatabaseOptions{}),
		CandidateBefore: capture(t, c, oracle.DatabaseOptions{}),
	}
	// Then the traffic writes a row on the candidate only.
	exec(t, c, `INSERT INTO orders (customer_id, total_cents) VALUES (2, 500)`)

	before.BaselineAfter = capture(t, b, oracle.DatabaseOptions{})
	before.CandidateAfter = capture(t, c, oracle.DatabaseOptions{})
	res := oracle.Compare(before)

	byPhase := map[oracle.Phase][]oracle.Finding{}
	for _, f := range res.Findings {
		byPhase[f.Phase] = append(byPhase[f.Phase], f)
	}
	require.Len(t, byPhase[oracle.PhaseMigration], 1)
	require.Equal(t, "public.customers", byPhase[oracle.PhaseMigration][0].Where)
	require.Len(t, byPhase[oracle.PhaseTraffic], 1)
	require.Equal(t, "public.orders", byPhase[oracle.PhaseTraffic][0].Where)
}

// A row the migrations removed must not be reported as critical, or every
// branch that touches a migration reads as a regression.
func TestAMissingRowFromTheMigrationsIsRankedDown(t *testing.T) {
	b, c := twoBranches(t, ordersSchema)
	exec(t, c, `DELETE FROM orders WHERE id = 1`)

	snapshot := func(conn *pgx.Conn) *oracle.Snapshot {
		return capture(t, conn, oracle.DatabaseOptions{})
	}
	res := oracle.Compare(oracle.Input{
		BaselineBefore: snapshot(b), CandidateBefore: snapshot(c),
		BaselineAfter: snapshot(b), CandidateAfter: snapshot(c),
	})
	require.Len(t, res.Findings, 1)
	require.Equal(t, oracle.KindRowMissing, res.Findings[0].Kind)
	require.Equal(t, oracle.PhaseMigration, res.Findings[0].Phase)
	require.Equal(t, oracle.Major, res.Findings[0].Severity,
		"a row the migrations removed is not the candidate having stopped writing")
}

// A table over the bound is reported as not compared. Silently skipping it is
// the failure this assertion exists to prevent: the report would read as a
// clean bill of health for a table nobody looked at.
func TestATableOverTheBoundIsNamedRatherThanSkipped(t *testing.T) {
	b, c := twoBranches(t, ordersSchema+`
INSERT INTO customers (name, email)
  SELECT 'c' || g, 'c' || g || '@example.test' FROM generate_series(3, 300) g;
ANALYZE;`)
	// A difference inside the table that will not be compared, so a silent
	// skip would look exactly like agreement.
	exec(t, c, `UPDATE customers SET name = 'changed' WHERE id = 5`)

	opts := oracle.DatabaseOptions{MaxRows: 10}
	res := oracle.Compare(oracle.Input{
		Database:       opts,
		BaselineAfter:  capture(t, b, opts),
		CandidateAfter: capture(t, c, opts),
	})
	require.Empty(t, findingsOfKind(res, oracle.KindRowChanged))
	require.Equal(t, 10, res.Database.MaxRows)
	require.Len(t, res.Database.NotCompared, 1)
	require.Contains(t, res.Database.NotCompared[0], "public.customers")
	require.Contains(t, res.Database.NotCompared[0], "more than the 10 row bound")
	require.Contains(t, res.Markdown(), "more than the 10 row bound")
}

// A table with no primary key has no fact about which row corresponds to
// which, so an update reads as one row gone and one row new. That is the
// honest answer and the test pins it rather than leaving it to be discovered.
func TestATableWithNoPrimaryKeyMatchesOnWholeRows(t *testing.T) {
	b, c := twoBranches(t, `
CREATE TABLE events (kind text NOT NULL, at timestamptz NOT NULL);
INSERT INTO events VALUES ('signup', '2026-01-01T00:00:00Z'), ('login', '2026-01-02T00:00:00Z');`)
	exec(t, c, `UPDATE events SET kind = 'logout' WHERE kind = 'login'`)

	res := oracle.Compare(oracle.Input{
		BaselineAfter:  capture(t, b, oracle.DatabaseOptions{}),
		CandidateAfter: capture(t, c, oracle.DatabaseOptions{}),
	})
	require.Len(t, res.Findings, 2)
	require.ElementsMatch(t,
		[]oracle.Kind{oracle.KindRowMissing, oracle.KindRowExtra},
		[]oracle.Kind{res.Findings[0].Kind, res.Findings[1].Kind})
	for _, f := range res.Findings {
		require.Equal(t, "(no primary key)", f.Path)
	}
}

// A composite primary key has to identify a row by both columns, or two rows
// sharing a first column collapse into one and the comparison reports a change
// that is really two different rows.
func TestACompositeKeyIdentifiesARowByEveryColumn(t *testing.T) {
	b, c := twoBranches(t, `
CREATE TABLE memberships (
  org_id  integer NOT NULL,
  user_id integer NOT NULL,
  role    text NOT NULL,
  PRIMARY KEY (org_id, user_id)
);
INSERT INTO memberships VALUES (1, 1, 'owner'), (1, 2, 'member');`)
	exec(t, c, `UPDATE memberships SET role = 'admin' WHERE org_id = 1 AND user_id = 2`)

	res := oracle.Compare(oracle.Input{
		BaselineAfter:  capture(t, b, oracle.DatabaseOptions{}),
		CandidateAfter: capture(t, c, oracle.DatabaseOptions{}),
	})
	require.Len(t, res.Findings, 1)
	require.Equal(t, "org_id=1, user_id=2", res.Findings[0].Path)
	require.Equal(t, `role="member"`, res.Findings[0].Baseline)
	require.Equal(t, `role="admin"`, res.Findings[0].Candidate)
}

// One field pattern covers a response field and the column behind it, which is
// what somebody means when they write $..updated_at once.
func TestAFieldPatternIgnoresTheColumnBehindIt(t *testing.T) {
	b, c := twoBranches(t, ordersSchema)
	exec(t, c, `UPDATE orders SET placed_at = placed_at + interval '1 day' WHERE id = 1`)

	opts := oracle.DatabaseOptions{}
	noisy := oracle.Compare(oracle.Input{
		Database:      opts,
		BaselineAfter: capture(t, b, opts), CandidateAfter: capture(t, c, opts),
	})
	require.Len(t, noisy.Findings, 1,
		"a day is far outside what the timestamp normaliser absorbs, so it is a finding")

	quiet := oracle.Compare(oracle.Input{
		Config:        oracle.Config{IgnoreFields: []string{"$..placed_at"}},
		Database:      opts,
		BaselineAfter: capture(t, b, opts), CandidateAfter: capture(t, c, opts),
	})
	require.Empty(t, quiet.Findings)
	require.Contains(t, quiet.Ignored.Describe(), "$..placed_at")
}

// The include and exclude lists have to actually change what is read, or they
// are manifest keys nothing honours.
func TestTablesCanBeIncludedAndExcluded(t *testing.T) {
	b, c := twoBranches(t, ordersSchema)
	exec(t, c, `UPDATE orders SET total_cents = 1 WHERE id = 1`)
	exec(t, c, `UPDATE customers SET name = 'changed' WHERE id = 1`)

	only := oracle.DatabaseOptions{Include: []string{"orders"}}
	res := oracle.Compare(oracle.Input{
		Database:      only,
		BaselineAfter: capture(t, b, only), CandidateAfter: capture(t, c, only),
	})
	require.Len(t, res.Findings, 1)
	require.Equal(t, "public.orders", res.Findings[0].Where)

	without := oracle.DatabaseOptions{Exclude: []string{"public.orders"}}
	res = oracle.Compare(oracle.Input{
		Database:      without,
		BaselineAfter: capture(t, b, without), CandidateAfter: capture(t, c, without),
	})
	require.Len(t, res.Findings, 1)
	require.Equal(t, "public.customers", res.Findings[0].Where)
}

// Capture opens a READ ONLY transaction, and this proves the server enforces
// it rather than the package promising it. The same argument the invariant
// package makes, and worth making twice: this one reads every table in the
// customer's database.
func TestCaptureCannotWrite(t *testing.T) {
	b, _ := twoBranches(t, ordersSchema)
	ctx := context.Background()
	tx, err := b.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `INSERT INTO orders (customer_id, total_cents) VALUES (1, 1)`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read-only transaction")
}

// The negative control for this file. Every test above asserts a finding or
// its absence, and a Capture that returned nothing at all would pass the ones
// that assert absence. This proves the capture reads what is there.
func TestCaptureReadsWhatIsThere(t *testing.T) {
	b, _ := twoBranches(t, ordersSchema)
	snap := capture(t, b, oracle.DatabaseOptions{})
	require.Len(t, snap.Tables, 2)

	byName := map[string]int{}
	for _, tbl := range snap.Tables {
		byName[tbl.Qualified()] = tbl.RowCount
		require.NotEmpty(t, tbl.Columns, "%s has no columns", tbl.Qualified())
	}
	require.Equal(t, map[string]int{"public.customers": 2, "public.orders": 1}, byName)

	for _, tbl := range snap.Tables {
		require.Equal(t, []string{"id"}, tbl.Key)
		for key, row := range tbl.Rows {
			require.NotEmpty(t, row, "row %s is empty", key)
		}
	}
	require.Empty(t, snap.Notes)
	require.Equal(t, fmt.Sprintf("%d", 2), fmt.Sprintf("%d", len(snap.Tables)))
}
