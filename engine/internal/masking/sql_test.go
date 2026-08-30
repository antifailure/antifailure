package masking_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The schema below is deliberately awkward: a unique email, a nullable phone,
// a foreign key that has to keep joining, a generated column that cannot be
// written, and a table with no primary key. Each one is a way a masking run
// goes wrong.
const schema = `
CREATE TABLE customers (
  id           bigserial PRIMARY KEY,
  email        text NOT NULL UNIQUE,
  full_name    text NOT NULL,
  phone        text,
  city         text,
  notes        text,
  display      text GENERATED ALWAYS AS (full_name || ' <' || email || '>') STORED
);
CREATE TABLE orders (
  id            bigserial PRIMARY KEY,
  customer_id   bigint NOT NULL REFERENCES customers(id),
  customer_email text NOT NULL,
  total_cents   bigint NOT NULL
);
CREATE TABLE audit_log (
  happened_at timestamptz NOT NULL DEFAULT now(),
  actor_email text NOT NULL,
  detail      text
);
INSERT INTO customers (email, full_name, phone, city, notes) VALUES
  ('ada@lovelace-analytics.co.uk',    'Ada Lovelace',   '+1 415 555 0101', 'London',   'Prefers email. Card 4111 1111 1111 1111.'),
  ('grace@hopper-systems.io',  'Grace Hopper',   '+1 415 555 0102', 'New York', 'Called about invoice 4012 8888 8888 1881.'),
  ('alan@turing-labs.net',   'Alan Turing',    NULL,              'Cambridge', NULL);
INSERT INTO orders (customer_id, customer_email, total_cents)
  SELECT id, email, 1999 FROM customers;
INSERT INTO audit_log (actor_email, detail) VALUES
  ('ada@lovelace-analytics.co.uk', 'signed in'), ('grace@hopper-systems.io', 'changed plan');
-- A partitioned table, because the product partitions one of its own and a
-- partition is a way for a rule to be silently not applied. See
-- TestCatalog_DoesNotSeePartitionsAsTablesOfTheirOwn.
CREATE TABLE events (
  id        bigserial,
  occurred_at timestamptz NOT NULL,
  kind      text NOT NULL,
  actor_email text,
  PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);
CREATE TABLE events_2026_01 PARTITION OF events
  FOR VALUES FROM ('2026-01-01') TO ('2026-02-01');
CREATE TABLE events_2026_02 PARTITION OF events
  FOR VALUES FROM ('2026-02-01') TO ('2026-03-01');
INSERT INTO events (occurred_at, kind, actor_email) VALUES
  ('2026-01-15', 'signed-in', 'ada@lovelace-analytics.co.uk'),
  ('2026-02-15', 'signed-in', 'grace@hopper-systems.io');
`

func requireDatabase(t *testing.T) (*pgx.Conn, func()) {
	t.Helper()
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	// A name and a port derived from the test, not fixed for the package.
	//
	// Every test here used to share one container name and one starting port,
	// so a run that failed before its cleanup left the container behind and
	// poisoned the next run with "port is already allocated" and then
	// "container name already in use". The second failure hides the first and
	// points at the runtime rather than at the test that actually broke, which
	// happened twice while dogfooding.
	//
	// The port is taken from the kernel rather than chosen: binding :0 and
	// reading back what was assigned is the only way to pick one nothing else
	// holds, because any number this code picks can be taken between the
	// picking and the binding.
	p, err := dockerdb.New(dockerdb.Options{
		Version: 17, Clock: clock.New(), PortFrom: freePort(t),
	})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "masking-test-" + testName(t),
		Mask:   func(context.Context, secrets.Value) error { return nil },
		Verify: func(context.Context, secrets.Value) (string, error) { return `{"rows":0}`, nil },
	})
	if err != nil {
		cancel()
		_ = p.Close()
		t.Skipf("skipped: no golden could be made: %v", err)
	}
	branch, err := p.Branch(ctx, gv.ID, testName(t))
	require.NoError(t, err)

	url, err := p.ConnString(ctx, branch, provider.ConnDirect)
	require.NoError(t, err)
	conn, err := pgx.Connect(ctx, url.Reveal())
	require.NoError(t, err)

	_, err = conn.Exec(ctx, schema)
	require.NoError(t, err)

	return conn, func() {
		_ = conn.Close(context.Background())
		c, cancel2 := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel2()
		_ = p.Destroy(c, branch)
		_ = p.DestroyGolden(c, gv.ID)
		_ = p.Close()
		cancel()
	}
}

func TestCatalog_ReadsWhatThePlannerNeeds(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)

	byName := map[string]masking.Table{}
	for _, tb := range tables {
		byName[tb.Name] = tb
	}
	require.Contains(t, byName, "customers")
	require.Contains(t, byName, "orders")
	require.Contains(t, byName, "audit_log")

	customers := byName["customers"]
	require.Equal(t, []string{"id"}, customers.PrimaryKey)

	cols := map[string]masking.ColumnInfo{}
	for _, c := range customers.Columns {
		cols[c.Name] = c
	}
	require.True(t, cols["email"].Unique, "a unique constraint decides which transforms are usable")
	require.True(t, cols["phone"].Nullable)
	require.False(t, cols["email"].Nullable)
	require.True(t, cols["display"].Generated, "a generated column cannot be written to at all")

	require.Empty(t, byName["audit_log"].PrimaryKey,
		"a table with no key cannot be chunked, and the plan has to know")
	require.Equal(t, "public.customers.id", byName["orders"].ColumnNamed("customer_id").ForeignKey)
}

func TestPlan_ClassifiesAndRefusesWhatItCannotDo(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)

	assignments := rules.Assign(tables)
	plan := masking.BuildPlan(tables, assignments, "test")

	require.True(t, plan.Runnable(), "the default rules must not produce an impossible plan: %v",
		masking.Problems(assignments))
	require.Positive(t, plan.Columns())

	byColumn := map[string]masking.Assignment{}
	for _, a := range assignments {
		byColumn[a.Table.Name+"."+a.Column.Name] = a
	}
	require.Equal(t, "email", byColumn["customers.email"].Transform)
	require.Equal(t, "name", byColumn["customers.full_name"].Transform)
	require.Equal(t, "phone", byColumn["customers.phone"].Transform)
	require.Equal(t, "free_text", byColumn["customers.notes"].Transform)

	// The generated column cannot be written and must not be planned.
	require.False(t, byColumn["customers.display"].Masked())

	// Columns holding the same kind of thing share a link, so one address
	// masks to one fake address wherever it appears. Without it,
	// customers.email and orders.customer_email would derive separate subkeys
	// and the join between them would stop working.
	require.Equal(t, "email", byColumn["customers.email"].Link)
	require.Equal(t, "email", byColumn["orders.customer_email"].Link)
	require.Equal(t, "email", byColumn["audit_log.actor_email"].Link)
}

func TestPlan_RefusesATransformThatWouldBreakAUniqueConstraint(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)
	// name does not preserve uniqueness, and email is unique. Running this
	// would fail partway through and leave the table half masked, which is
	// worse than not starting.
	rules, err := masking.NewRuleSet([]masking.Rule{
		{Table: "customers", Column: "email", Transform: "name"},
	})
	require.NoError(t, err)

	assignments := rules.Assign(tables)
	plan := masking.BuildPlan(tables, assignments, "test")
	require.False(t, plan.Runnable())
	require.Contains(t, plan.Problems[0].Problem, "half masked")
}

func TestApply_MasksTheDataAndKeepsTheJoins(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	before := queryOne(t, conn, `SELECT count(*) FROM orders o JOIN customers c ON c.id = o.customer_id`)

	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, rules.Assign(tables), "test")

	key, err := masking.NewKeyFromBytes([]byte("a-test-master-key-for-masking-000"))
	require.NoError(t, err)
	exec, err := masking.NewExecutor(masking.ExecutorOptions{Key: key, Clock: clock.New()})
	require.NoError(t, err)

	res, err := exec.Apply(ctx, conn, plan)
	require.NoError(t, err)
	require.Positive(t, res.Rows)

	// Nothing real survives.
	require.Zero(t, queryOne(t, conn,
		`SELECT count(*) FROM customers WHERE email LIKE '%@lovelace-analytics.co.uk'`),
		"the original addresses are gone")
	require.Zero(t, queryOne(t, conn, `SELECT count(*) FROM customers WHERE full_name = 'Ada Lovelace'`))

	// The joins still join, which is what linking is for.
	require.Equal(t, before,
		queryOne(t, conn, `SELECT count(*) FROM orders o JOIN customers c ON c.id = o.customer_id`),
		"a foreign key masked differently from what it points at breaks the join")

	// A null stays null. A transform that turned it into a value would invent
	// a phone number for somebody who never gave one.
	require.Equal(t, int64(1), queryOne(t, conn, `SELECT count(*) FROM customers WHERE phone IS NULL`))

	// The same input maps to the same output everywhere, which is what makes
	// a masked database usable rather than merely safe.
	require.Equal(t, int64(3), queryOne(t, conn,
		`SELECT count(*) FROM orders o JOIN customers c ON c.id = o.customer_id
		 WHERE o.customer_email = c.email`),
		"the same address masked two ways in two tables is a database nobody can query")
}

func TestApply_IsDeterministicAcrossRuns(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	// The property that makes a masked golden comparable to the last one: the
	// same customer becomes the same fake customer every time.
	first := maskAndRead(t, conn, "a-test-master-key-for-masking-000")

	_, err := conn.Exec(ctx, `TRUNCATE orders, customers RESTART IDENTITY CASCADE;`+
		`INSERT INTO customers (email, full_name, phone, city, notes) VALUES
		  ('ada@lovelace-analytics.co.uk','Ada Lovelace','+1 415 555 0101','London','x')`)
	require.NoError(t, err)

	second := maskAndRead(t, conn, "a-test-master-key-for-masking-000")
	require.Equal(t, first[0], second[0], "the same key and the same input must give the same output")

	_, err = conn.Exec(ctx, `TRUNCATE orders, customers RESTART IDENTITY CASCADE;`+
		`INSERT INTO customers (email, full_name, phone, city, notes) VALUES
		  ('ada@lovelace-analytics.co.uk','Ada Lovelace','+1 415 555 0101','London','x')`)
	require.NoError(t, err)

	third := maskAndRead(t, conn, "a-different-master-key-0000000000")
	require.NotEqual(t, first[0], third[0], "a different key must give a different mapping")
}

func maskAndRead(t *testing.T, conn *pgx.Conn, master string) []string {
	t.Helper()
	ctx := context.Background()
	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, rules.Assign(tables), "test")

	key, err := masking.NewKeyFromBytes([]byte(master))
	require.NoError(t, err)
	exec, err := masking.NewExecutor(masking.ExecutorOptions{Key: key, Clock: clock.New()})
	require.NoError(t, err)
	_, err = exec.Apply(ctx, conn, plan)
	require.NoError(t, err)

	rows, err := conn.Query(ctx, `SELECT email FROM customers ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}
	return out
}

func TestVerify_CatchesDataThatWasNotMasked(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	// The property the whole product rests on. A scan that could not find
	// unmasked data would let every golden through, and nobody would know
	// until it mattered.
	report, err := verify.Scan(ctx, conn, verify.Options{SampleSize: 100})
	require.NoError(t, err)
	require.False(t, report.Clean(), "the unmasked fixture must not verify clean")

	var detectors []string
	for _, f := range report.Findings {
		detectors = append(detectors, f.Detector)
		require.NotContains(t, f.Example, "ada@lovelace-analytics.co.uk",
			"a report that quoted the data would be a report that leaks it")
	}
	require.Contains(t, detectors, "email")
	require.Contains(t, detectors, "payment-card", "the card in the notes column has to be found")
}

func TestVerify_PassesAfterMasking(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, rules.Assign(tables), "test")

	key, err := masking.NewKeyFromBytes([]byte("a-test-master-key-for-masking-000"))
	require.NoError(t, err)
	exec, err := masking.NewExecutor(masking.ExecutorOptions{Key: key, Clock: clock.New()})
	require.NoError(t, err)
	_, err = exec.Apply(ctx, conn, plan)
	require.NoError(t, err)

	report, err := verify.Scan(ctx, conn, verify.Options{SampleSize: 100})
	require.NoError(t, err)
	if !report.Clean() {
		for _, f := range report.Findings {
			t.Logf("still found: %s", f)
		}
	}
	require.True(t, report.Clean(), "masked data must verify clean")
	require.Positive(t, report.Columns)
	require.Positive(t, report.RowsSampled)
	require.Empty(t, report.Skipped, "a column nobody could read is not a column that passed")
}

func queryOne(t *testing.T, conn *pgx.Conn, sql string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, conn.QueryRow(context.Background(), sql).Scan(&n), sql)
	return n
}

var _ = fmt.Sprint
var _ = strings.TrimSpace

// A partition is storage for its parent, not a table of its own.
//
// The bug this is about was silent and it destroyed data somebody had asked to
// keep. information_schema reports a partitioned parent and every one of its
// partitions as a BASE TABLE, so the catalogue held both. They hold the same
// rows, so those rows were masked twice; and because a rule names a table, and
// `events` is not `events_2026_01`, the parent got the rules somebody wrote
// while each partition got the fail-closed default. The partitions sort after
// the parent and so ran last, which meant a column explicitly marked
// `preserve` came out emptied.
//
// It was found by refreshing a golden of this product's own control plane,
// whose events table is partitioned by month.
func TestCatalog_DoesNotSeePartitionsAsTablesOfTheirOwn(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	// Row estimates come from the statistics collector, and a table that has
	// just been filled has none: reltuples is zero until something analyses it.
	// Asked for before that, this test would be asserting about the fixture's
	// age rather than about the query.
	_, err := conn.Exec(ctx, "ANALYZE")
	require.NoError(t, err)

	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)

	named := map[string]masking.Table{}
	for _, tb := range tables {
		named[tb.Name] = tb
	}

	parent, ok := named["events"]
	require.True(t, ok, "the partitioned parent is missing, so its rows would never be masked")
	require.NotEmpty(t, parent.Columns)

	for _, partition := range []string{"events_2026_01", "events_2026_02"} {
		_, listed := named[partition]
		require.False(t, listed,
			"%s is catalogued as a table in its own right. Its rows are the parent's, so they "+
				"would be masked twice, and by whichever rules happen to match a name nobody "+
				"writes rules against", partition)
	}

	// The estimate has to come from where the rows are. A parent carries none
	// of its own, so left alone it looks empty and the planner masks the
	// largest table in the database in one unchunked statement.
	require.Greater(t, parent.Rows, int64(0),
		"the partitioned parent is estimated at %d rows, so it would be masked in one "+
			"statement holding a lock on every partition", parent.Rows)
}

// The rules somebody wrote reach the rows inside the partitions.
//
// The other half of the same fix, and the half that is about data rather than
// about a catalogue. `kind` is preserved on purpose; before the fix it came
// back empty, because the default emptied it through a partition after the
// rule had preserved it through the parent.
func TestApply_MasksThroughAPartitionedParent(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)

	rules, err := masking.NewRuleSet([]masking.Rule{
		{Table: "events", Column: "kind", Transform: "preserve", Why: "a fixed vocabulary"},
	})
	require.NoError(t, err)

	plan := masking.BuildPlan(tables, rules.Assign(tables), "partition-test")
	require.True(t, plan.Runnable(), "%v", plan.Problems)

	key, err := masking.NewKeyFromBytes([]byte("a-test-master-key-for-masking-000"))
	require.NoError(t, err)
	exec, err := masking.NewExecutor(masking.ExecutorOptions{Key: key, Clock: clock.New()})
	require.NoError(t, err)
	_, err = exec.Apply(ctx, conn, plan)
	require.NoError(t, err)

	var kinds, addresses int
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT count(*) FROM events WHERE kind = 'signed-in'`).Scan(&kinds))
	require.Equal(t, 2, kinds,
		"a column marked preserve on the parent was emptied through its partitions")

	require.NoError(t, conn.QueryRow(ctx,
		`SELECT count(*) FROM events WHERE actor_email LIKE '%@example.test'`).Scan(&addresses))
	require.Equal(t, 2, addresses,
		"the address column inside the partitions was not masked, so masking the parent did "+
			"not reach the rows")
}

// testName reduces a test's name to something a container name accepts.
//
// Hashed rather than truncated, because two subtests can share their first
// twenty characters and the whole point is that two tests never collide.
func testName(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name()))
	return "masking" + hex.EncodeToString(sum[:4])
}

// freePort asks the kernel for a port nothing is using.
//
// The listener is closed before the value is returned, so there is a window in
// which something else could take it. That window is unavoidable without
// handing the socket to Docker, and it is far smaller than the certainty of a
// collision that a constant guarantees.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("skipped: no free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("closing the probe listener: %v", err)
	}
	return port
}
