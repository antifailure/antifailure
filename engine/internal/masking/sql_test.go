package masking_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
`

// uniqueRulesHash returns eight hex characters, because that is how many of it
// survive into a golden version id.
func uniqueRulesHash() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Cannot happen on any platform this runs on, and a fallback that
		// returned a constant would reintroduce the collision it exists to
		// avoid, so it is loud instead.
		panic("masking test: no randomness available: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// The bug this file had, encoded so it cannot come back.
//
// Needs no Docker and no database, which is the point: the failure it describes
// only ever appeared on a machine fast enough to publish two goldens inside one
// second, so a test that needs the slow path to reproduce it is a test that
// passes everywhere except where the bug is.
func TestGoldenIDsFromThisHelperDoNotCollideWithinASecond(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 29, 1, 22, 58, 0, time.UTC)

	// What it used to do. A constant rules hash truncates to its first eight
	// characters, so every golden published in the same second got one id.
	first := provider.NewGoldenVersionID(at, "masking-test")
	second := provider.NewGoldenVersionID(at, "masking-test")
	require.Equal(t, first, second,
		"the collision this test exists for is supposed to be reproducible")
	require.Equal(t, "gv_20260829012258_masking-", first,
		"the id in the CI failure, reproduced exactly")

	// What it does now.
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		id := provider.NewGoldenVersionID(at, uniqueRulesHash())
		require.False(t, seen[id], "two goldens in the same second shared an id: %s", id)
		seen[id] = true
	}
}

func requireDatabase(t *testing.T) (*pgx.Conn, func()) {
	t.Helper()
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	p, err := dockerdb.New(dockerdb.Options{Version: 17, Clock: clock.New(), PortFrom: 46500})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

	// A UNIQUE rules hash per call, and it has to be unique in its first eight
	// characters.
	//
	// NewGoldenVersionID builds gv_<timestamp to the second>_<hash[:8]>, so a
	// constant like "masking-test" truncates to "masking-" and every golden this
	// helper makes inside the same second gets the SAME id. Two tests in this
	// package that land in one second then share a golden: the first one's
	// cleanup drops it, and the second fails with "the golden version
	// gv_..._masking- no longer exists".
	//
	// It passed locally and failed in CI for exactly that reason. The local run
	// takes minutes and the tests fall in different seconds; a CI runner puts
	// them in the same one.
	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: uniqueRulesHash(),
		Mask:   func(context.Context, secrets.Value) error { return nil },
		Verify: func(context.Context, secrets.Value) (string, error) { return `{"rows":0}`, nil },
	})
	if err != nil {
		cancel()
		_ = p.Close()
		t.Skipf("skipped: no golden could be made: %v", err)
	}
	branch, err := p.Branch(ctx, gv.ID, "maskingtest")
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
