package invariant_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/invariant"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// testDatabaseURL is the Postgres the whole project's tests share, the one
// `just db` starts. The name and the default match internal/insights and
// web/apps/api/test/harness.ts, because two conventions for the same server is
// one too many.
//
// These tests need a real server rather than a real provider. What they prove
// is what Postgres does with a READ ONLY transaction and a statement_timeout,
// and none of that differs for a database the Docker provider made.
const testDatabaseURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

var shared struct {
	url  string
	skip string
}

func TestMain(m *testing.M) {
	shared.url = testDatabaseURL
	if u := os.Getenv("AF_TEST_DATABASE_URL"); u != "" {
		shared.url = u
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	conn, err := pgx.Connect(ctx, shared.url)
	if err != nil {
		shared.skip = fmt.Sprintf("no Postgres at %s: %v", redact(shared.url), err)
	} else {
		_ = conn.Close(ctx)
	}
	cancel()

	// A machine with no test Postgres has not found a bug, so the default is
	// to skip. A machine that was SUPPOSED to have one has found a large one,
	// and skipping there is the worst outcome available: `go test` prints
	// nothing for a skip, so the package reports ok having examined nothing.
	if shared.skip != "" && os.Getenv("AF_REQUIRE_DATABASE") != "" {
		fmt.Fprintf(os.Stderr,
			"AF_REQUIRE_DATABASE is set and there is no usable Postgres, so these tests "+
				"would have skipped silently: %s\n", shared.skip)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func redact(u string) string {
	if i := strings.Index(u, "@"); i > 0 {
		if j := strings.Index(u, "://"); j >= 0 && j+3 < i {
			return u[:j+3] + "..." + u[i:]
		}
	}
	return u
}

// connect gives the test its own database, so one test's tables and functions
// cannot be seen by another and a leaked transaction cannot block one.
func connect(t *testing.T) *pgx.Conn {
	t.Helper()
	if shared.skip != "" {
		t.Skip(shared.skip)
	}
	ctx := t.Context()

	admin, err := pgx.Connect(ctx, shared.url)
	require.NoError(t, err)
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()

	name := "af_inv_" + strings.ToLower(strings.NewReplacer("/", "_", "-", "_").Replace(t.Name()))
	if len(name) > 60 {
		name = name[:60]
	}
	_, err = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	require.NoError(t, err)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)

	url := shared.url
	if i := strings.LastIndex(url, "/"); i > 0 {
		url = url[:i+1] + name
	}
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)

	t.Cleanup(func() {
		bg := context.Background()
		_ = conn.Close(bg)
		a, err := pgx.Connect(bg, shared.url)
		if err != nil {
			return
		}
		defer func() { _ = a.Close(bg) }()
		_, _ = a.Exec(bg, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return conn
}

// orders is the schema the guide's own example is written against, so the
// tests exercise the statement somebody will actually copy.
const orders = `
CREATE TABLE users  (id bigserial PRIMARY KEY, email text NOT NULL);
CREATE TABLE orders (id bigserial PRIMARY KEY, user_id bigint, total_cents int NOT NULL DEFAULT 0);
INSERT INTO users (email) SELECT 'user' || g || '@example.test' FROM generate_series(1, 5) g;
INSERT INTO orders (user_id) SELECT (g % 5) + 1 FROM generate_series(1, 20) g;
`

const noOrphanOrders = `
SELECT o.id AS order_id, o.user_id
FROM orders o
LEFT JOIN users u ON u.id = o.user_id
WHERE u.id IS NULL
ORDER BY o.id
`

func TestAnInvariantThatHoldsReturnsNoRowsAndSaysSo(t *testing.T) {
	conn := connect(t)
	_, err := conn.Exec(t.Context(), orders)
	require.NoError(t, err)

	s := invariant.Run(t.Context(), conn, []schema.Invariant{
		{Name: "no-orphan-orders", SQL: noOrphanOrders},
	}, invariant.Options{})

	require.Len(t, s.Results, 1)
	r := s.Results[0]
	require.NoError(t, r.Err)
	require.True(t, r.Held, "every order belongs to a user that exists")
	require.Empty(t, r.Rows)
	require.True(t, s.Held())
	require.Empty(t, s.Violated())
}

func TestAViolatedInvariantCarriesTheRowsAsEvidence(t *testing.T) {
	conn := connect(t)
	_, err := conn.Exec(t.Context(), orders+
		`INSERT INTO orders (id, user_id) VALUES (900, 4242), (901, 4243);`)
	require.NoError(t, err)

	s := invariant.Run(t.Context(), conn, []schema.Invariant{
		{Name: "no-orphan-orders", Description: "Every order belongs to a user that exists.", SQL: noOrphanOrders},
	}, invariant.Options{})

	r := s.Results[0]
	require.NoError(t, r.Err)
	require.False(t, r.Held)
	require.True(t, r.Violated())
	require.False(t, s.Held())

	// The rows are the point. A boolean says something is wrong; these say
	// which orders, and the statement that found them can be run by hand.
	require.Equal(t, []string{"order_id", "user_id"}, r.Columns)
	require.Equal(t, [][]string{{"900", "4242"}, {"901", "4243"}}, r.Rows)
	require.False(t, r.More)
	require.Len(t, s.Violated(), 1)
}

func TestEvidenceIsBoundedSoAMillionBrokenRowsCostTheSameAsSix(t *testing.T) {
	conn := connect(t)
	_, err := conn.Exec(t.Context(), orders+
		`INSERT INTO orders (user_id) SELECT 9000 + g FROM generate_series(1, 500) g;`)
	require.NoError(t, err)

	s := invariant.Run(t.Context(), conn, []schema.Invariant{
		{Name: "no-orphan-orders", SQL: noOrphanOrders},
	}, invariant.Options{MaxRows: 3})

	r := s.Results[0]
	require.NoError(t, r.Err)
	require.False(t, r.Held)
	require.Len(t, r.Rows, 3, "kept at most MaxRows")
	require.True(t, r.More, "and said there were more")
}

// The bound has to be in the SQL rather than in the row loop, and this is the
// test that says so. Reading a few rows and abandoning the rest leaves pgx to
// drain the portal before the connection is usable again, so an unbounded join
// never returns: the first rows arrive immediately, so statement_timeout is
// satisfied, and the draining is what hangs. Written against the implementation
// that did exactly that, where it failed on the context deadline instead.
func TestAnUnboundedStatementIsAnsweredFromItsFirstRowsAndNotDrained(t *testing.T) {
	conn := connect(t)

	started := time.Now()
	s := invariant.Run(t.Context(), conn, []schema.Invariant{
		// Four trillion rows. Nothing may read them.
		{Name: "huge", SQL: `SELECT a.id FROM generate_series(1, 2000000) a(id)
		                     CROSS JOIN generate_series(1, 2000000) b(id)`},
	}, invariant.Options{MaxRows: 3, Timeout: 30 * time.Second})
	elapsed := time.Since(started)

	r := s.Results[0]
	require.NoError(t, r.Err, "it should answer, not time out and not hang")
	require.False(t, r.Held)
	require.Len(t, r.Rows, 3)
	require.True(t, r.More)
	require.Less(t, elapsed, 20*time.Second,
		"answered from the first rows rather than by reading four trillion")
}

// This is the test the static keyword check in the manifest validator cannot
// be made to pass. The statement names no writing keyword; the writing is
// inside the function. Only the READ ONLY transaction catches it.
func TestAWriteHiddenInsideAFunctionIsRefusedByTheTransaction(t *testing.T) {
	conn := connect(t)
	_, err := conn.Exec(t.Context(), orders+`
CREATE FUNCTION sneak() RETURNS bigint LANGUAGE sql AS $$
  INSERT INTO orders (user_id) VALUES (1) RETURNING id;
$$;`)
	require.NoError(t, err)

	sql := "SELECT sneak() AS id"
	// The manifest validator would let this through, which is the point.
	require.NotContains(t, strings.ToLower(sql), "insert ")

	s := invariant.Run(t.Context(), conn, []schema.Invariant{
		{Name: "sneaky", SQL: sql},
	}, invariant.Options{})

	r := s.Results[0]
	require.Error(t, r.Err)
	require.ErrorIs(t, r.Err, aferrors.Coded(aferrors.AFAGT011),
		"a write attempt is AF-AGT-011, whatever it was disguised as")
	require.False(t, r.Violated(), "an invariant that could not run is not a violation")
	require.False(t, s.Held())

	// And nothing was written. The transaction was rolled back.
	var n int
	require.NoError(t, conn.QueryRow(t.Context(), "SELECT count(*) FROM orders").Scan(&n))
	require.Equal(t, 20, n, "the invariant left the data exactly as it found it")
}

// backstopGrace mirrors the package's own margin past statement_timeout for a
// short invariant. Kept here rather than exported, so a change to the package's
// margin shows up as a failing bound rather than as a test that follows it.
const backstopGrace = 5 * time.Second

func TestAnInvariantThatRunsTooLongIsATimeoutAndNotAViolation(t *testing.T) {
	conn := connect(t)
	_, err := conn.Exec(t.Context(), orders)
	require.NoError(t, err)

	s := invariant.Run(t.Context(), conn, []schema.Invariant{
		// An aggregate, so that the bounding LIMIT cannot short circuit it:
		// count(*) has to see every row before it can produce its single one.
		// A statement that streams rows would be answered from the first few
		// and would never reach the timeout at all.
		{Name: "slow", SQL: `SELECT count(*) FROM generate_series(1, 200000000) a
		                     CROSS JOIN generate_series(1, 200000) b`},
	}, invariant.Options{Timeout: 300 * time.Millisecond})

	r := s.Results[0]
	require.Error(t, r.Err)
	require.ErrorIs(t, r.Err, aferrors.Coded(aferrors.AFAGT010))
	require.False(t, r.Violated(),
		"a check that did not finish has not shown the data to be broken")
	require.False(t, r.Held)
	// Bounded by the backstop rather than by statement_timeout, because which
	// of the two fires depends on how loaded the machine is and the contract
	// does not. What is promised is AF-AGT-010 within the limit plus its
	// grace, and both paths keep that promise.
	//
	// This assertion used to be `< 2s`, which is the fast path only. It went
	// red the moment another suite was using the same server, which is a test
	// measuring the machine rather than the code. The fix was in the package:
	// a context deadline here now classifies as AF-AGT-010 too, because it
	// means the same thing, rather than the answer depending on winning a
	// race with Postgres.
	require.Less(t, r.Duration, 300*time.Millisecond+backstopGrace+2*time.Second,
		"gave up inside the limit plus its grace")
	t.Logf("timed out after %s", r.Duration)
}

func TestOneBadInvariantNeverStopsTheOthers(t *testing.T) {
	conn := connect(t)
	_, err := conn.Exec(t.Context(), orders+
		`INSERT INTO orders (id, user_id) VALUES (900, 4242);`)
	require.NoError(t, err)

	s := invariant.Run(t.Context(), conn, []schema.Invariant{
		{Name: "broken-sql", SQL: "SELECT * FROM a_table_that_is_not_there"},
		{Name: "no-orphan-orders", SQL: noOrphanOrders},
		{Name: "no-negative-totals", SQL: "SELECT id FROM orders WHERE total_cents < 0"},
	}, invariant.Options{})

	require.Len(t, s.Results, 3, "every invariant produced a result")
	require.Equal(t, []string{"broken-sql", "no-orphan-orders", "no-negative-totals"},
		[]string{s.Results[0].Name, s.Results[1].Name, s.Results[2].Name},
		"in the order the manifest declares them")

	require.Error(t, s.Results[0].Err)
	require.True(t, s.Results[1].Violated(), "the second still ran and still found the orphan")
	require.True(t, s.Results[2].Held, "and the third still held")
	require.Len(t, s.Errored(), 1)
	require.Len(t, s.Violated(), 1)
}

// Held is a claim about every invariant, so anything that did not run makes it
// false. Reporting "every invariant held" on the strength of a check that
// never happened is the failure this whole package exists to end.
func TestAnInvariantThatCouldNotRunIsNotEvidenceThatItHeld(t *testing.T) {
	conn := connect(t)
	_, err := conn.Exec(t.Context(), orders)
	require.NoError(t, err)

	s := invariant.Run(t.Context(), conn, []schema.Invariant{
		{Name: "no-orphan-orders", SQL: noOrphanOrders},
		{Name: "broken-sql", SQL: "SELECT * FROM a_table_that_is_not_there"},
	}, invariant.Options{})

	require.True(t, s.Results[0].Held)
	require.False(t, s.Held(), "one unanswered question is enough to withhold the claim")
	require.Empty(t, s.Violated(), "and it is still not a violation")
}

func TestNoInvariantsIsAHeldRunAndNotAFailure(t *testing.T) {
	conn := connect(t)
	s := invariant.Run(t.Context(), conn, nil, invariant.Options{})
	require.Empty(t, s.Results)
	require.True(t, s.Held())
	require.Empty(t, s.Violated())
}

func TestNullsAndTimestampsRenderReadablyInTheEvidence(t *testing.T) {
	conn := connect(t)
	_, err := conn.Exec(t.Context(),
		`CREATE TABLE t (id int, note text, at timestamptz);
		 INSERT INTO t VALUES (1, NULL, '2026-08-29T03:00:00Z');`)
	require.NoError(t, err)

	s := invariant.Run(t.Context(), conn, []schema.Invariant{
		{Name: "shape", SQL: "SELECT id, note, at FROM t"},
	}, invariant.Options{})

	r := s.Results[0]
	require.NoError(t, r.Err)
	require.Equal(t, [][]string{{"1", "NULL", "2026-08-29T03:00:00Z"}}, r.Rows)
}
