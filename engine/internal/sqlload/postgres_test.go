package sqlload_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/sqlload"
)

// These are the tests that make the claim, and none of them can be written
// against a fake.
//
// The claim is about a SERVER: that N clients hold N sessions and that their
// statements overlap inside it, that a deadlock between two of them is a
// deadlock Postgres detected rather than an error string this package invented,
// that a statement's latency is the server's, and that a mix derived from
// pg_stat_statements is the traffic that really ran. A fake driver would let
// every one of those pass while proving nothing at all, which is the exact
// shape of failure this repository keeps finding in its own instruments.
//
// So this file needs Postgres, with pg_stat_statements preloaded, and it says
// so loudly when it does not have one. AF_REQUIRE_DATABASE turns a skip into a
// failure, because a skip prints nothing and a package that reports ok having
// examined almost nothing is worse than a red.

// testDatabaseURL is the Postgres the project's suites share, the one `just db`
// starts. AF_TEST_DATABASE_URL overrides it. Both spellings are the ones
// engine/internal/insights already uses, because two conventions for one server
// is one too many.
const testDatabaseURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

var shared struct {
	url  string
	skip string
}

func TestMain(m *testing.M) {
	code := func() int {
		shared.url = maintenanceURL()
		defer setupTemplate()()
		if shared.skip != "" && os.Getenv("AF_REQUIRE_DATABASE") != "" {
			fmt.Fprintf(os.Stderr,
				"AF_REQUIRE_DATABASE is set and there is no usable Postgres, so these tests "+
					"would have skipped silently: %s\n", shared.skip)
			return 1
		}
		return m.Run()
	}()
	// goleak after the suite rather than through VerifyTestMain, because this
	// package owns TestMain. Every client connection, the observer, the
	// progress reporter and the deadline timer are goroutines this package
	// starts, and a run that returned while one of them was still going would
	// be a run still holding a backend open on somebody's database.
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

// templateDB holds the fixture once. Every test copies it with CREATE DATABASE
// ... TEMPLATE, which Postgres does by copying the files, so the rows, the
// indexes and the planner statistics arrive together and the copy costs about
// what a connection does. Building the fixture per test cost twenty two
// seconds of every three second run, measured.
const templateDB = "af_sqlload_template"

// setupTemplate checks the server, builds the template, and returns the
// teardown.
//
// The preload is checked rather than assumed. Without it the extension can be
// created and records nothing, so every derived mix would be empty and the
// tests that prove derivation works would fail for a reason that is not about
// the code.
func setupTemplate() func() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	fail := func(format string, args ...any) func() {
		cancel()
		shared.skip = fmt.Sprintf(format, args...)
		return func() {}
	}

	admin, err := pgx.Connect(ctx, shared.url)
	if err != nil {
		return fail("no Postgres at the test URL: %v", err)
	}

	var libs string
	if err := admin.QueryRow(ctx, "SHOW shared_preload_libraries").Scan(&libs); err != nil {
		_ = admin.Close(ctx)
		return fail("the server would not say what it preloaded: %v", err)
	}
	if !strings.Contains(libs, "pg_stat_statements") {
		_ = admin.Close(ctx)
		return fail("the server did not preload pg_stat_statements, so a derived mix " +
			"would be empty for a reason that is not this package's")
	}

	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+templateDB+" WITH (FORCE)"); err != nil {
		_ = admin.Close(ctx)
		return fail("the template database could not be cleared: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+templateDB); err != nil {
		_ = admin.Close(ctx)
		return fail("the template database could not be created: %v", err)
	}

	loader, err := pgx.Connect(ctx, databaseURL(templateDB))
	if err != nil {
		_ = admin.Close(ctx)
		return fail("the template database could not be reached: %v", err)
	}
	_, err = loader.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_stat_statements")
	if err == nil {
		_, err = loader.Exec(ctx, fixture)
	}
	// Closed either way: CREATE DATABASE ... TEMPLATE refuses while anything
	// is connected to the template.
	_ = loader.Close(ctx)
	if err != nil {
		_ = admin.Close(ctx)
		return fail("the fixture could not be loaded: %v", err)
	}

	return func() {
		c, cancel2 := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel2()
		_, _ = admin.Exec(c, "DROP DATABASE IF EXISTS "+templateDB+" WITH (FORCE)")
		_ = admin.Close(c)
		cancel()
	}
}

// fixture is the schema every test runs against: large enough that an index
// scan beats a sequential scan, so the latencies are a real database's.
const fixture = `
CREATE TABLE merchants (
  id bigserial PRIMARY KEY,
  name text NOT NULL
);
CREATE TABLE orders (
  id bigserial PRIMARY KEY,
  merchant_id bigint NOT NULL REFERENCES merchants(id),
  status text NOT NULL,
  total numeric(12,2) NOT NULL,
  note text,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX orders_merchant_idx ON orders (merchant_id);
CREATE TABLE counters (
  id int PRIMARY KEY,
  n bigint NOT NULL
);
INSERT INTO counters (id, n) VALUES (1, 0), (2, 0);
INSERT INTO merchants (name) SELECT 'merchant ' || g FROM generate_series(1, 200) g;
INSERT INTO orders (merchant_id, status, total, note)
  SELECT (g % 200) + 1,
         (ARRAY['paid','pending','refunded'])[(g % 3) + 1],
         (g % 5000)::numeric / 7,
         'note ' || g
  FROM generate_series(1, 20000) g;
ANALYZE;
`

// database makes one database per test, so pg_stat_statements entries are the
// test's own.
//
// A database rather than a schema, and that is not a stylistic choice. The
// statistics view carries a dbid per entry and this package filters on it, so
// two tests sharing one database would see each other's statements and the
// derivation tests would pass or fail depending on what ran before them.
func database(t *testing.T) (string, *pgx.Conn) {
	t.Helper()
	if shared.skip != "" {
		t.Skipf("%s", shared.skip)
	}
	ctx := context.Background()

	name := fmt.Sprintf("af_sqlload_%d", time.Now().UnixNano())
	admin, err := pgx.Connect(ctx, shared.url)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE "+templateDB)
	require.NoError(t, err)
	// Shortened on the database rather than on a connection, because the two
	// clients that will deadlock connect for themselves and a session setting
	// would not reach them. One second is the default and it is the whole of a
	// short test's budget.
	_, err = admin.Exec(ctx, "ALTER DATABASE "+name+" SET deadlock_timeout = '50ms'")
	require.NoError(t, err)
	require.NoError(t, admin.Close(ctx))

	url := databaseURL(name)
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	// The statistics view is cluster wide and the fixture's own inserts are
	// the loudest thing in it. Reset so that a derivation test measures the
	// traffic the test sent rather than the statements that built the table.
	_, err = conn.Exec(ctx, "SELECT pg_stat_statements_reset()")
	require.NoError(t, err)

	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = conn.Close(c)
		a, err := pgx.Connect(c, shared.url)
		if err != nil {
			return
		}
		_, _ = a.Exec(c, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = a.Close(c)
	})
	return url, conn
}

func databaseURL(name string) string {
	base := shared.url
	if i := strings.LastIndex(base, "/"); i >= 0 {
		if q := strings.Index(base[i:], "?"); q >= 0 {
			return base[:i+1] + name + base[i+q:]
		}
		return base[:i+1] + name
	}
	return base
}

// readMix is the declared workload most of these tests run.
func readMix(t *testing.T) *sqlload.Mix {
	t.Helper()
	mix, _, err := sqlload.ParseScript([]byte(`
sql_workload: storefront
transactions:
  - transaction: read one order
    weight: 8
    statements:
      - label: order by id
        sql: SELECT id, status, total FROM orders WHERE id = $1
        params:
          - query: SELECT id FROM orders
  - transaction: a merchant page
    weight: 2
    statements:
      - label: orders for a merchant
        sql: SELECT id, total FROM orders WHERE merchant_id = $1 ORDER BY created_at DESC LIMIT 20
        params:
          - int: {min: 1, max: 200}
      - label: the merchant
        sql: SELECT name FROM merchants WHERE id = $1
        params:
          - int: {min: 1, max: 200}
`))
	require.NoError(t, err)
	return mix
}

// TestTheClientsReallyHoldSeparateSessionsAndOverlapInsideTheServer is the
// central proof and the reason the observer exists.
//
// Eight goroutines are not eight database sessions, and eight sessions are not
// eight overlapping ones. Both halves are asked of the SERVER: how many
// distinct backends carried this run's application name, and how many of them
// were executing a statement at one instant.
func TestTheClientsReallyHoldSeparateSessionsAndOverlapInsideTheServer(t *testing.T) {
	url, _ := database(t)
	res, err := sqlload.Run(context.Background(), sqlload.Options{
		URL: url, Mix: readMix(t), Clients: 8, Duration: 3 * time.Second,
		Seed: 7, Clock: clock.New(),
	})
	require.NoError(t, err)

	require.NotNilf(t, res.BackendsSeen,
		"the observer never sampled, so this run proves nothing: %s", res.ObserverNote)
	require.Equal(t, 8, *res.BackendsSeen,
		"eight clients have to be eight distinct backends in pg_stat_activity")
	// The overlap is asserted on how many backends were INSIDE A TRANSACTION,
	// never on how many were executing, and the difference is the whole point
	// of this block.
	//
	// Executing means state='active' at the instant of a sample, which for a
	// millisecond read against a local server is a sliver of each client's
	// time. Measured here rather than assumed: eight runs of exactly these
	// options reported a peak of 1, 2, 2, 2, 3, 3, 3 and 3 backends executing,
	// while the peak inside a transaction was 7 or 8 in every one of the
	// eight. A live `af load sql` at 540 transactions a second reported 8 of 8
	// inside a transaction and 1 executing. So an assertion that two were
	// executing at once fails about one run in eight for a reason that has
	// nothing to do with whether the clients overlapped, and a flaky red is
	// how people learn to re-run a job until it goes green.
	//
	// Being inside a transaction spans the whole BEGIN to COMMIT, which is
	// what overlap actually means here, and it has lost none of the ability to
	// say no: clients sharing one connection would report 1, and that is the
	// failure this test exists to catch.
	require.NotNil(t, res.PeakActiveBackends,
		"the executing count is still measured and reported, it is just not the overlap claim")
	require.NotNil(t, res.PeakOpenTransactions)
	require.GreaterOrEqual(t, *res.PeakOpenTransactions, 6,
		"with eight clients and no think time, most of them have to be inside a transaction "+
			"at any instant: a lower number means they were queueing behind something, "+
			"and 1 would mean they shared a session rather than holding eight")
	require.GreaterOrEqual(t, *res.PeakOpenTransactions, *res.PeakActiveBackends,
		"a backend executing a statement is by definition inside a transaction")
	require.Empty(t, res.ObserverNote)

	require.Positive(t, res.Transactions)
	require.Positive(t, res.TPS)
	require.Positive(t, res.Overall.P95Ms)
	require.Positive(t, res.Rows, "a query parameter draws real ids, so the reads have to find rows")
	require.Zero(t, res.ClientsStopped)

	t.Logf("clients %d, backends seen %d, peak executing %d, peak in a transaction %d, %d transactions in %s",
		res.Clients, *res.BackendsSeen, *res.PeakActiveBackends, *res.PeakOpenTransactions,
		res.Transactions, res.Duration)
	t.Logf("tps %.1f, p50 %.3fms p95 %.3fms p99 %.3fms max %.3fms, rows %d",
		res.TPS, res.Overall.P50Ms, res.Overall.P95Ms, res.Overall.P99Ms, res.Overall.MaxMs, res.Rows)
	for _, st := range res.PerStatement {
		t.Logf("  %-24s %-34s executed %5d  p95 %7.3fms  rows %d",
			st.Transaction, st.Label, st.Executed, st.Latency.P95Ms, st.Rows)
	}
}

// TestEveryClientRunsItsOwnSequenceAndTwoRunsOfOneSeedAgree is what makes a
// comparison between two runs a comparison of the database.
func TestEveryClientRunsItsOwnSequenceAndTwoRunsOfOneSeedAgree(t *testing.T) {
	url, _ := database(t)
	run := func() map[string]int {
		res, err := sqlload.Run(context.Background(), sqlload.Options{
			URL: url, Mix: readMix(t), Clients: 4, Transactions: 25,
			Seed: 42, Clock: clock.New(),
		})
		require.NoError(t, err)
		out := map[string]int{}
		for _, tx := range res.PerTransaction {
			out[tx.Name] = tx.Executed
		}
		return out
	}
	first, second := run(), run()
	require.Equal(t, first, second,
		"two runs of one seed picked different transactions, so a comparison between "+
			"two builds would be a comparison of two different workloads")
	total := 0
	for _, n := range first {
		total += n
	}
	require.Equal(t, 100, total, "four clients times twenty five transactions each")
}

// TestADeadlockIsDetectedByThePostgresServerAndRetried proves the two counts a
// concurrent workload exists to produce are the server's own.
func TestADeadlockIsDetectedByThePostgresServerAndRetried(t *testing.T) {
	url, _ := database(t)
	mix, _, err := sqlload.ParseScript([]byte(`
sql_workload: crossing updates
transactions:
  - transaction: one then two
    statements:
      - {label: lock one, sql: "UPDATE counters SET n = n + 1 WHERE id = 1"}
      - {label: hold, sql: "SELECT pg_sleep(0.1)"}
      - {label: lock two, sql: "UPDATE counters SET n = n + 1 WHERE id = 2"}
  - transaction: two then one
    statements:
      - {label: lock two, sql: "UPDATE counters SET n = n + 1 WHERE id = 2"}
      - {label: hold, sql: "SELECT pg_sleep(0.1)"}
      - {label: lock one, sql: "UPDATE counters SET n = n + 1 WHERE id = 1"}
`))
	require.NoError(t, err)

	res, err := sqlload.Run(context.Background(), sqlload.Options{
		URL: url, Mix: mix, Clients: 2, Duration: 6 * time.Second,
		Seed: 3, Clock: clock.New(),
	})
	require.NoError(t, err)

	require.Positive(t, res.Deadlocks,
		"two clients taking two rows in opposite orders for six seconds never deadlocked, "+
			"so either they did not overlap or the deadlock was not counted")
	require.Positive(t, res.Retries, "a deadlock has to be retried rather than counted as a failure")
	require.Contains(t, res.Errors, "deadlock")
	require.Positive(t, res.Transactions,
		"every retried transaction eventually committed, so the run still has throughput")
}

// TestAClientWhoseConnectionIsTakenAwayStopsAndTheRunSaysSo is the ordering
// where the database goes away underneath a run that is already going.
//
// pg_terminate_backend rather than stopping the container, because it kills
// exactly this run's own backends and nothing else on a shared server, and
// because what the client sees is the same either way: the connection ends
// mid transaction.
func TestAClientWhoseConnectionIsTakenAwayStopsAndTheRunSaysSo(t *testing.T) {
	url, admin := database(t)

	// Gated on the run having measured something, rather than on the backends
	// merely existing. The first connection is the one that fills the query
	// parameter pools before the clients start, so terminating on the backend
	// count alone kills the setup and the test proves nothing about a run that
	// was already going. Found by writing it the other way first.
	started := make(chan struct{}, 1)
	killed := make(chan int, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		select {
		case <-started:
		case <-ctx.Done():
			killed <- 0
			return
		}
		var n int
		_ = admin.QueryRow(ctx,
			`SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE application_name = $1`,
			sqlload.ClientApplicationName).Scan(&n)
		killed <- n
	}()

	res, err := sqlload.Run(context.Background(), sqlload.Options{
		URL: url, Mix: readMix(t), Clients: 3, Duration: 20 * time.Second,
		Seed: 9, Clock: clock.New(),
		Progress: func(sqlload.Progress) {
			select {
			case started <- struct{}{}:
			default:
			}
		},
	})
	wg.Wait()
	require.NoError(t, err)
	require.Positive(t, <-killed, "nothing was terminated, so this test proves nothing")

	require.Positive(t, res.ClientsStopped,
		"the server took the connections away and the run reported every client as healthy")
	require.NotEmpty(t, res.StoppedBecause)
	require.Contains(t, strings.Join(keys(res.StoppedBecause), ","), "connection")
}

// TestARunThatCommittedNothingReportsNoThroughputRatherThanAFastOne is the
// cell the whole result shape exists for.
func TestARunThatCommittedNothingReportsNoThroughputRatherThanAFastOne(t *testing.T) {
	url, _ := database(t)
	mix, _, err := sqlload.ParseScript([]byte(`
sql_workload: every attempt raises
transactions:
  - transaction: divide by zero
    statements:
      - {label: raise, sql: "SELECT 1 / 0"}
`))
	require.NoError(t, err)

	res, err := sqlload.Run(context.Background(), sqlload.Options{
		URL: url, Mix: mix, Clients: 2, Transactions: 20, Seed: 1, Clock: clock.New(),
	})
	require.NoError(t, err)

	require.Zero(t, res.Transactions)
	require.Equal(t, 40, res.TransactionsFailed)
	require.Zero(t, res.TPS, "a run that committed nothing reported a throughput")
	require.Zero(t, res.Overall.P95Ms, "a run that committed nothing reported a latency")
	require.InDelta(t, 1.0, res.ErrorRate, 1e-9)
	require.Equal(t, 40, res.StatementsFailed)
	require.Zero(t, res.Statements)
	require.Contains(t, res.Errors, "SQLSTATE 22012")
}

// TestCancellationEndsTheRunAndKeepsWhatItAlreadyMeasured.
func TestCancellationEndsTheRunAndKeepsWhatItAlreadyMeasured(t *testing.T) {
	url, _ := database(t)
	ctx, cancel := context.WithCancel(context.Background())

	// Cancelled once the run has definitely committed something, so the test
	// proves the measurements SURVIVE a cancellation rather than proving that
	// an empty result survives one. The progress callback is the signal
	// because it is emitted from inside the run.
	progressed := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-progressed:
		case <-time.After(30 * time.Second):
		}
		cancel()
	}()

	res, err := sqlload.Run(ctx, sqlload.Options{
		URL: url, Mix: readMix(t), Clients: 2, Duration: time.Minute,
		Seed: 5, Clock: clock.New(),
		Progress: func(sqlload.Progress) {
			select {
			case progressed <- struct{}{}:
			default:
			}
		},
	})
	wg.Wait()

	require.ErrorIs(t, err, context.Canceled,
		"a cancelled run has to say it was cancelled rather than report a short clean run")
	require.NotNil(t, res, "a cancelled run still has to carry what it measured")
	require.Positive(t, res.Transactions)
	require.Positive(t, res.TPS)
	// The caller stopping is not the database failing. A transaction in flight
	// when the cancellation lands returns an error, and counting it would make
	// every cancelled run of a healthy workload report a burst of failures it
	// did not have.
	//
	// Asserted on the COUNT rather than on a particular reason, which is what
	// the first version of this did and why a mutation removing the check
	// survived it: a context cancellation does not arrive from pgx as a
	// Postgres error at all, so it was classified as an ordinary query failure
	// and the reason this looked for was never going to be there.
	require.Zero(t, res.TransactionsFailed,
		"a cancelled run counted the caller stopping as failed transactions: %v", res.Errors)
	require.Empty(t, res.Errors,
		"a cancelled run of a healthy workload recorded database errors")
	require.Zero(t, res.ClientsStopped)
}

// TestAStatementStoppedByTheRunEndingIsNotCountedAsAFailedStatement is the
// statement level half of the test above, and it is here because the two
// levels disagreed in a real run.
//
// The transaction above has always refused to count a cancelled attempt. The
// STATEMENT inside it counted one anyway, so a healthy ten second run against
// a real database committed 5416 transactions, reported 0 failed and 0
// retried, and still printed 5 errors against a read that had run 3640 times
// and returned a row every time. Those five were the clients that happened to
// be inside a statement when the duration expired. The summary said nothing
// was wrong and the table said a query had failed, and the table is the one a
// reader acts on.
//
// Deterministic rather than hopeful, which is the whole reason for the sleep.
// Cancelling a fast mix and trusting that something was in flight would pass
// on a quiet machine and fail on a busy one. Every client here is provably
// inside a statement when the run ends, because the statement takes a hundred
// times the run's duration.
func TestAStatementStoppedByTheRunEndingIsNotCountedAsAFailedStatement(t *testing.T) {
	url, _ := database(t)
	mix, _, err := sqlload.ParseScript([]byte(`
sql_workload: a statement that outlives the run
transactions:
  - transaction: wait
    statements:
      - {label: sleep, sql: "SELECT pg_sleep(30)"}
`))
	require.NoError(t, err)

	res, err := sqlload.Run(context.Background(), sqlload.Options{
		URL: url, Mix: mix, Clients: 2, Duration: 300 * time.Millisecond,
		Seed: 1, Clock: clock.New(),
	})
	require.NoError(t, err, "the duration running out is the run finishing, not the run failing")
	require.NotNil(t, res)

	// Nothing committed, because nothing could: the point is what the run says
	// about the statements it interrupted, not what it measured.
	require.Zero(t, res.Transactions)
	require.Zero(t, res.StatementsFailed,
		"the run counted its own deadline as failed statements: %v", res.Errors)
	for _, st := range res.PerStatement {
		require.Zero(t, st.Errors,
			"%q reported %d errors for statements the run itself stopped",
			st.Label, st.Errors)
	}
	// And the two levels agree, which is the property that was missing.
	require.Zero(t, res.TransactionsFailed)
	require.Empty(t, res.Errors)
}

// TestTheTransactionSequenceIsTheSameEvenWhenOneRunLosesMoreRaces.
//
// The reproducibility claim, made where it is hard rather than where it is
// easy. Two runs of an uncontended read mix pick the same transactions because
// nothing perturbs them. Under contention a client retries, and a retry is a
// TIMING event: how many times one happens is not the same between two runs of
// one seed. So the question is whether a retry can shift what runs next, and
// the answer has to be no.
//
// It is no because of one decision. A client draws its transaction choices and
// its parameter values from ONE generator, which is what keeps the two
// uncorrelated, and a transaction's values are therefore bound ONCE, before its
// first attempt, so a retry takes no draw at all. Bind again on the retry and
// the single stream moves under the next transaction, and the two runs below
// diverge the first time one of them loses a race and the other does not.
func TestTheTransactionSequenceIsTheSameEvenWhenOneRunLosesMoreRaces(t *testing.T) {
	url, _ := database(t)
	mix, _, err := sqlload.ParseScript([]byte(`
sql_workload: crossing updates with values
transactions:
  - transaction: one then two
    weight: 3
    statements:
      - {label: lock one, sql: "UPDATE counters SET n = n + $1 WHERE id = 1", params: [{int: {min: 1, max: 9}}]}
      - {label: hold, sql: "SELECT pg_sleep(0.05)"}
      - {label: lock two, sql: "UPDATE counters SET n = n + $1 WHERE id = 2", params: [{int: {min: 1, max: 9}}]}
  - transaction: two then one
    weight: 2
    statements:
      - {label: lock two, sql: "UPDATE counters SET n = n + $1 WHERE id = 2", params: [{int: {min: 1, max: 9}}]}
      - {label: hold, sql: "SELECT pg_sleep(0.05)"}
      - {label: lock one, sql: "UPDATE counters SET n = n + $1 WHERE id = 1", params: [{int: {min: 1, max: 9}}]}
  - transaction: read a counter
    weight: 5
    statements:
      - {label: read, sql: "SELECT n FROM counters WHERE id = $1", params: [{int: {min: 1, max: 2}}]}
`))
	require.NoError(t, err)

	run := func() (map[string]int, int) {
		res, err := sqlload.Run(context.Background(), sqlload.Options{
			URL: url, Mix: mix, Clients: 3, Transactions: 20, Seed: 21, Clock: clock.New(),
		})
		require.NoError(t, err)
		out := map[string]int{}
		for _, tx := range res.PerTransaction {
			out[tx.Name] = tx.Executed + tx.Failed
		}
		return out, res.Retries
	}
	first, retriesFirst := run()
	second, retriesSecond := run()

	require.Equal(t, first, second,
		"two runs of one seed picked different transactions, and the only thing that "+
			"differed between them was how many times a client lost a race")
	t.Logf("the same sequence both times, with %d retries in the first run and %d in the second",
		retriesFirst, retriesSecond)
}

// TestAMixDerivedFromStatementStatisticsIsTheTrafficThatActuallyRan.
func TestAMixDerivedFromStatementStatisticsIsTheTrafficThatActuallyRan(t *testing.T) {
	url, conn := database(t)
	ctx := context.Background()

	// The traffic an application would send, at three different rates, so the
	// weights have something to be wrong about.
	for i := 0; i < 60; i++ {
		_, err := conn.Exec(ctx, `SELECT id, status FROM orders WHERE id = $1`, int64(i%20000)+1)
		require.NoError(t, err)
	}
	for i := 0; i < 20; i++ {
		_, err := conn.Exec(ctx,
			`SELECT o.id FROM orders o JOIN merchants m ON m.id = o.merchant_id WHERE o.merchant_id = $1`,
			int64(i%200)+1)
		require.NoError(t, err)
	}
	for i := 0; i < 10; i++ {
		_, err := conn.Exec(ctx, `UPDATE orders SET status = $1 WHERE id = $2`, "pending", int64(i)+1)
		require.NoError(t, err)
	}

	mix, err := sqlload.Derive(ctx, conn, sqlload.DeriveOptions{})
	require.NoError(t, err)
	require.Equal(t, sqlload.SourceStatementStatistics, mix.Source)

	byWeight := map[string]float64{}
	for _, tx := range mix.Transactions {
		byWeight[tx.Name] = tx.Weight
		require.True(t, tx.HasBaseline,
			"a derived transaction carries the mean the statistics reported")
		require.Len(t, tx.Statements, 1)
		require.False(t, tx.Statements[0].Write)
	}
	require.Len(t, mix.Transactions, 2, "the two reads, and not the write")

	var single, joined float64
	for name, w := range byWeight {
		switch {
		case strings.Contains(name, "JOIN"):
			joined = w
		case strings.Contains(name, "WHERE id ="):
			single = w
		}
	}
	require.Equal(t, float64(60), single, "the weight is the call count the server reported")
	require.Equal(t, float64(20), joined)
	require.Greater(t, single, joined, "the busiest statement carries the heaviest weight")

	var refusedWrite bool
	for _, r := range mix.Refused {
		if r.Code == sqlload.RefusedWrite && strings.Contains(r.Statement, "UPDATE orders") {
			refusedWrite = true
		}
	}
	require.True(t, refusedWrite,
		"a write whose values were normalised away has to be refused by name, not dropped")

	// And the derived mix runs.
	res, err := sqlload.Run(ctx, sqlload.Options{
		URL: url, Mix: mix, Clients: 4, Transactions: 20, Seed: 2, Clock: clock.New(),
	})
	require.NoError(t, err)
	require.Equal(t, 80, res.Transactions)
	require.Positive(t, res.TPS)
	for _, tx := range res.PerTransaction {
		require.True(t, tx.Baselines.Has)
		require.Positive(t, tx.Baselines.MeanMs)
	}
}

// TestADerivedWriteIsTakenOnlyWhenTheManifestAsksForIt.
func TestADerivedWriteIsTakenOnlyWhenTheManifestAsksForIt(t *testing.T) {
	url, conn := database(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		_, err := conn.Exec(ctx, `UPDATE orders SET status = $1 WHERE id = $2`, "pending", int64(i)+1)
		require.NoError(t, err)
	}

	_, err := sqlload.Derive(ctx, conn, sqlload.DeriveOptions{})
	require.Error(t, err, "the only traffic was a write, so a mix that refuses writes has nothing")
	require.Contains(t, err.Error(), "AF-LOD-020")

	mix, err := sqlload.Derive(ctx, conn, sqlload.DeriveOptions{Writes: true})
	require.NoError(t, err)
	require.Len(t, mix.Transactions, 1)
	require.True(t, mix.Transactions[0].Statements[0].Write)

	res, err := sqlload.Run(ctx, sqlload.Options{
		URL: url, Mix: mix, Clients: 2, Transactions: 5, Seed: 4, Clock: clock.New(),
	})
	require.NoError(t, err)
	require.Equal(t, 10, res.Transactions)
}

// TestAStatementThatNoLongerParsesAgainstTheBranchIsRefusedByName.
//
// The second thing preparing every candidate buys, and the one worth as much
// as the parameter types: a statement against a column this branch's migration
// dropped is refused with the server's own message, before a transaction runs.
func TestAStatementThatNoLongerParsesAgainstTheBranchIsRefusedByName(t *testing.T) {
	_, conn := database(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		_, err := conn.Exec(ctx, `SELECT note FROM orders WHERE id = $1`, int64(i)+1)
		require.NoError(t, err)
	}
	for i := 0; i < 10; i++ {
		_, err := conn.Exec(ctx, `SELECT status FROM orders WHERE id = $1`, int64(i)+1)
		require.NoError(t, err)
	}
	// The change under rehearsal, arriving between the traffic and the run.
	_, err := conn.Exec(ctx, `ALTER TABLE orders DROP COLUMN note`)
	require.NoError(t, err)

	mix, err := sqlload.Derive(ctx, conn, sqlload.DeriveOptions{})
	require.NoError(t, err)

	var refused *sqlload.Refused
	for i := range mix.Refused {
		if strings.Contains(mix.Refused[i].Statement, "note") {
			refused = &mix.Refused[i]
		}
	}
	require.NotNil(t, refused, "the statement reading a dropped column was not refused")
	require.Equal(t, sqlload.RefusedUnpreparable, refused.Code)
	require.Contains(t, refused.Reason, "note")
	require.Len(t, mix.Transactions, 1, "the statement that still parses is still taken")
}

// TestAnInternalIntegrityCheckIsSkippedRatherThanRefused.
//
// It is the busiest statement in most databases and it is not the
// application's. Skipped rather than refused, because a refusal is a decision
// about the customer's workload and burying the one refusal they can act on
// under forty they cannot is how a report stops being read.
func TestAnInternalIntegrityCheckIsSkippedRatherThanRefused(t *testing.T) {
	_, conn := database(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		_, err := conn.Exec(ctx,
			`INSERT INTO orders (merchant_id, status, total) VALUES ($1, $2, $3)`,
			int64(i%200)+1, "paid", 1.5)
		require.NoError(t, err)
	}
	for i := 0; i < 30; i++ {
		_, err := conn.Exec(ctx, `SELECT id FROM orders WHERE id = $1`, int64(i)+1)
		require.NoError(t, err)
	}

	var integrity int64
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT coalesce(max(calls), 0) FROM pg_stat_statements
         WHERE query LIKE '%FOR KEY SHARE OF%'
           AND dbid = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&integrity))
	require.Positive(t, integrity,
		"the foreign key trigger's own statement is not in the view, so this test proves nothing")

	mix, err := sqlload.Derive(ctx, conn, sqlload.DeriveOptions{})
	require.NoError(t, err)
	for _, r := range mix.Refused {
		require.NotContains(t, r.Statement, "FOR KEY SHARE OF")
	}
	for _, tx := range mix.Transactions {
		require.NotContains(t, tx.Statements[0].SQL, "FOR KEY SHARE OF")
	}
	require.Contains(t, strings.Join(keys(mix.Skipped), " | "), "integrity check")
}

// TestAGeneratedValueFindsNothingAndTheResultSaysSo is the negative arm for
// the derived path's one real limitation.
//
// It is deliberately a test that the run reports ZERO rows. A derived read
// whose parameter is generated does not have production's value, and the
// rows column is what lets a reader see that rather than read an empty scan
// as a fast one. If this ever goes red because the generator started finding
// rows, the documentation about it has to change in the same commit.
func TestAGeneratedValueFindsNothingAndTheResultSaysSo(t *testing.T) {
	url, conn := database(t)
	ctx := context.Background()
	// A predicate on a uuid nothing in this database holds. The generated
	// value is a legal uuid and is never one of the twenty thousand rows.
	_, err := conn.Exec(ctx, `ALTER TABLE orders ADD COLUMN token uuid`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `UPDATE orders SET token = gen_random_uuid()`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `CREATE INDEX orders_token_idx ON orders (token)`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, "SELECT pg_stat_statements_reset()")
	require.NoError(t, err)

	for i := 0; i < 20; i++ {
		var id int64
		_ = conn.QueryRow(ctx, `SELECT id FROM orders WHERE token = $1`, randomUUIDForTest(i)).Scan(&id)
	}

	mix, err := sqlload.Derive(ctx, conn, sqlload.DeriveOptions{})
	require.NoError(t, err)
	require.Len(t, mix.Transactions, 1)
	require.Equal(t, "uuid", mix.Transactions[0].Statements[0].Params[0].Type,
		"the parameter type came from the server rather than from a guess about the column name")

	res, err := sqlload.Run(ctx, sqlload.Options{
		URL: url, Mix: mix, Clients: 2, Transactions: 10, Seed: 6, Clock: clock.New(),
	})
	require.NoError(t, err)
	require.Equal(t, 20, res.Transactions)
	require.Zero(t, res.Rows,
		"a generated uuid matched a row, so the documented limitation is wrong")
	require.Positive(t, res.Overall.P95Ms,
		"the statements still cost something: that cost is the index lookup, which is real")
}

// TestAnEmptyQueryParameterPoolIsRefusedBeforeTheRunStarts.
func TestAnEmptyQueryParameterPoolIsRefusedBeforeTheRunStarts(t *testing.T) {
	url, _ := database(t)
	mix, _, err := sqlload.ParseScript([]byte(`
sql_workload: nothing to draw from
transactions:
  - transaction: read
    statements:
      - label: by id
        sql: SELECT id FROM orders WHERE id = $1
        params:
          - query: SELECT id FROM orders WHERE 1 = 0
`))
	require.NoError(t, err)

	res, err := sqlload.Run(context.Background(), sqlload.Options{
		URL: url, Mix: mix, Clients: 2, Transactions: 5, Seed: 1, Clock: clock.New(),
	})
	require.Error(t, err)
	require.Nil(t, res)
	require.Contains(t, err.Error(), "returned no rows")
}

// TestTheClientsAreRefusedTogetherRatherThanRunningShortHanded.
func TestTheClientsAreRefusedTogetherRatherThanRunningShortHanded(t *testing.T) {
	url, conn := database(t)
	ctx := context.Background()

	// A role with a connection limit rather than a run that asks for more than
	// max_connections. The second version of this test opened a hundred and
	// ninety seven connections before the server said no and took a hundred and
	// nineteen seconds to do it, which is a test nobody runs. A limit of two
	// refuses the third connection immediately and proves the same rule.
	// Not a superuser, because Postgres does not enforce a connection limit
	// against one.
	role := "sqlload_limited_" + strings.TrimPrefix(currentDatabase(t, conn), "af_sqlload_")
	_, err := conn.Exec(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD 'limited' CONNECTION LIMIT 2")
	require.NoError(t, err)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// The grant has to go before the role can. A role holding a privilege
		// cannot be dropped, and a role left behind is cluster wide, so the
		// next run of this test on a shared server would collide with it.
		_, _ = conn.Exec(c, "DROP OWNED BY "+role)
		_, _ = conn.Exec(c, "DROP ROLE IF EXISTS "+role)
	})
	_, err = conn.Exec(ctx, "GRANT SELECT ON ALL TABLES IN SCHEMA public TO "+role)
	require.NoError(t, err)

	res, err := sqlload.Run(ctx, sqlload.Options{
		URL: asRole(t, url, role, "limited"), Mix: readMix(t), Clients: 4,
		Transactions: 1, Seed: 1, Clock: clock.New(),
	})
	require.Error(t, err,
		"more clients than the server will give has to refuse rather than run short handed")
	require.Nil(t, res)
	require.Contains(t, err.Error(), "could not connect")
	require.Contains(t, err.Error(), "client 3 of 4",
		"the refusal names which client could not connect")
}

func currentDatabase(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	var name string
	require.NoError(t, conn.QueryRow(context.Background(), "SELECT current_database()").Scan(&name))
	return name
}

// asRole rewrites a connection string's user and password.
func asRole(t *testing.T, raw, user, password string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	u.User = url.UserPassword(user, password)
	return u.String()
}

// TestTheObserverCanBeTurnedOffAndTheResultSaysItWasNotMeasured.
//
// The third falsification arm: the instrument is removed while the run is
// otherwise healthy, and the result has to report an absence rather than a
// zero. A console that cannot tell "no overlap" from "nobody looked" will draw
// the second as the first.
func TestTheObserverCanBeTurnedOffAndTheResultSaysItWasNotMeasured(t *testing.T) {
	url, _ := database(t)
	res, err := sqlload.Run(context.Background(), sqlload.Options{
		URL: url, Mix: readMix(t), Clients: 2, Transactions: 5, Seed: 1,
		Clock: clock.New(), SkipObserver: true,
	})
	require.NoError(t, err)
	require.Positive(t, res.Transactions)
	require.Nil(t, res.PeakActiveBackends, "an unmeasured overlap reported as a number")
	require.Nil(t, res.PeakOpenTransactions)
	require.Nil(t, res.BackendsSeen)
	require.Contains(t, res.ObserverNote, "never sampled")

	// The contention numbers take the same arm, because they are the ones
	// where the mistake is expensive. A zero here would read as "this build
	// blocked nothing", which is the most reassuring thing this result can
	// say, produced by an instrument that never ran.
	require.Nil(t, res.LockWaits, "an unmeasured run reported as having queued nothing")
	require.Nil(t, res.LockWaitMS)
	require.Nil(t, res.LockWaitPairs)
	require.Contains(t, res.LockWaitNote, "says nothing about whether it blocked")
	require.NotContains(t, res.LockWaitNote, "pg_blocking_pids",
		"an unwatched run carried the instrument's limits as though it had run")
}

// TestThinkTimeDelaysTheClientAndStaysOutOfTheLatency makes two claims and
// measures each one where noise cannot reach it.
//
// ONE client, not eight, and that is what makes this stable. The first version
// ran two clients against a duration and compared throughputs, so the number it
// measured was think time PLUS however long a contended transaction took, and
// on a busy machine the second term moved by more than the first. With one
// client the database is barely loaded, a primary key lookup costs a couple of
// milliseconds, and the two claims separate:
//
// The delay is one sided. Eight transactions with a wait of half a second
// between them cannot finish in less than three and a half seconds, whatever
// the machine is doing, because noise can only make a run longer.
//
// The latency is untouched. Think time is not part of a transaction, so the
// measured percentile has to stay in the milliseconds rather than approach the
// wait. A generator that timed the wait as part of the transaction would report
// a p95 of about five hundred milliseconds here, which is a hundred times what
// this allows.
func TestThinkTimeDelaysTheClientAndStaysOutOfTheLatency(t *testing.T) {
	url, _ := database(t)
	const think = 500 * time.Millisecond
	const each = 12

	run := func(wait time.Duration) *sqlload.Result {
		res, err := sqlload.Run(context.Background(), sqlload.Options{
			URL: url, Mix: readMix(t), Clients: 1, Transactions: each,
			ThinkTime: wait, Seed: 8, Clock: clock.New(),
		})
		require.NoError(t, err)
		require.Equal(t, each, res.Transactions)
		return res
	}
	// Warmed first and the result thrown away. The database arrives from
	// CREATE DATABASE ... TEMPLATE with a cold cache, so the first transaction
	// against it reads its pages off disk and measured 1.1 seconds here, which
	// is a real cost of a cold database and says nothing about think time.
	run(0)
	fast, slow := run(0), run(think)

	// (each - 1) rather than each: the client waits between transactions, so
	// the last one is not followed by a wait. Asserting the larger number
	// would make this test fail for being right about the wrong boundary.
	require.GreaterOrEqual(t, slow.Duration, time.Duration(each-1)*think,
		"twelve transactions with half a second between them finished too quickly, "+
			"so the think time did not reach the client")
	require.Less(t, fast.Duration, time.Duration(each-1)*think,
		"the run with no think time took as long as the one with it, so this test "+
			"could not tell them apart and proves nothing")

	// Against the other run rather than against an absolute number, because
	// this machine's absolute transaction latency is whatever else is running
	// on it and the claim is about the DIFFERENCE. If the wait were timed as
	// part of the transaction, the median here would be about five hundred
	// milliseconds above the other run's; half of that is the line.
	require.Less(t, slow.Overall.P50Ms, fast.Overall.P50Ms+float64(think.Milliseconds())/2,
		"the think time was measured as part of the transaction it precedes, which it is not")
	require.Less(t, slow.TPS, fast.TPS)
	t.Logf("with no think time: %d transactions in %s, p95 %.2fms, %.1f a second",
		fast.Transactions, fast.Duration.Round(time.Millisecond), fast.Overall.P95Ms, fast.TPS)
	t.Logf("with %s of think time: %d transactions in %s, p95 %.2fms, %.1f a second",
		think, slow.Transactions, slow.Duration.Round(time.Millisecond), slow.Overall.P95Ms, slow.TPS)
}

func keys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func randomUUIDForTest(i int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
}

// The lock contention tests, and the reason they are here rather than beside a
// fake.
//
// The claim is that the run can say WHO WAITED FOR WHOM while it was going.
// Every part of that is the server's: which backend is in a lock queue, which
// backends are in front of it, what kind of lock it is and which relation it
// names. A fake would let the aggregation pass while proving nothing about
// pg_blocking_pids, which is the one call in this instrument that this
// repository had never made before.
//
// Three cases, and the middle one is the one that makes the other two mean
// anything. A test that only ever runs the contended case has proved the
// instrument can say yes; a test that only runs the quiet one has proved it
// can stay silent. Both are needed, plus the run where nobody watched at all,
// because "no contention" and "not measured" are the pair this result exists
// to keep apart.

// contendingMix is one row, taken and then held, so every client after the
// first has to queue for it.
//
// pg_sleep INSIDE the transaction rather than think time between them. Think
// time happens after COMMIT, when the row lock is already gone, so a mix that
// used it would produce a run with no contention at all. The hold is 300ms
// against a 200ms sample interval, which is what makes the wait visible: a
// wait shorter than the interval is exactly what this instrument says it
// cannot see.
func contendingMix(t *testing.T) *sqlload.Mix {
	t.Helper()
	mix, _, err := sqlload.ParseScript([]byte(`
sql_workload: everybody wants row one
transactions:
  - transaction: bump the counter
    statements:
      - {label: take the row, sql: "UPDATE counters SET n = n + 1 WHERE id = 1"}
      - {label: hold it, sql: "SELECT pg_sleep(0.3)"}
`))
	require.NoError(t, err)
	return mix
}

// TestLockContentionInsideTheRunIsSeenAndBothStatementsAreNamed is the yes arm.
func TestLockContentionInsideTheRunIsSeenAndBothStatementsAreNamed(t *testing.T) {
	url, _ := database(t)
	res, err := sqlload.Run(context.Background(), sqlload.Options{
		URL: url, Mix: contendingMix(t), Clients: 3, Duration: 3 * time.Second,
		Seed: 11, Clock: clock.New(),
	})
	require.NoError(t, err)

	require.NotNilf(t, res.LockWaits,
		"nothing read the wait queues, so this run proves nothing: %s", res.LockWaitNote)
	require.Positive(t, *res.LockWaits,
		"three clients queueing for one row for three seconds never waited, so either "+
			"they did not overlap or the wait was not seen")
	require.NotNil(t, res.LockWaitMS)
	require.Positive(t, *res.LockWaitMS)
	require.NotEmpty(t, res.LockWaitPairs, "waits were counted and no pair was named")

	// The pair is the part a person acts on, so it is asserted by name rather
	// than by count. A number that says contention happened and cannot say
	// between what is a number nobody can do anything with.
	var found bool
	for _, w := range res.LockWaitPairs {
		if w.BlockedStatement != "take the row" {
			continue
		}
		require.Equal(t, "bump the counter", w.BlockedTransaction)
		require.True(t, w.BlockingInRun,
			"the holder was one of this run's own clients and was reported as a stranger")
		require.Positive(t, w.Waits)
		require.Positive(t, w.WaitedMS)
		// A row conflict queues on the holder's transaction id, and a second
		// waiter queues behind the first on a tuple lock. Either is the truth
		// about this mix and neither is a relation lock, which is the whole
		// reason this sampler does not filter on locktype the way the
		// migration rehearsal's does.
		require.Contains(t, []string{"transactionid", "tuple"}, w.LockType)
		if w.BlockingStatement != "" {
			// Either label, and the second one is a finding rather than a
			// looseness in this assertion. Three clients queue in a chain: the
			// one at the front is holding the row while it sleeps, and the one
			// behind it is itself blocked on "take the row" while a third
			// queues behind both. pg_blocking_pids reports every backend in
			// front, so a blocked backend correctly appears as a blocker. An
			// assertion that allowed only the sleeper would be asserting that
			// the chain does not exist.
			require.Contains(t, []string{"hold it", "take the row"}, w.BlockingStatement,
				"the holder was named as a statement this mix does not contain")
		}
		found = true
	}
	require.True(t, found,
		"no pair named the statement that was waiting, so the join to the mix's own "+
			"labels did not happen")

	require.Contains(t, res.LockWaitNote, "pg_blocking_pids")
	require.Contains(t, res.LockWaitNote, "floors rather than totals")

	t.Logf("%d waits, %.0f backend ms waiting, %d pairs",
		*res.LockWaits, *res.LockWaitMS, len(res.LockWaitPairs))
	for _, w := range res.LockWaitPairs {
		t.Logf("  %q waited on %q (%s, holder %s, in run %v) %s %d times, %.0fms",
			w.BlockedStatement, w.BlockingStatement, w.LockType, w.BlockingState,
			w.BlockingInRun, w.Mode, w.Waits, w.WaitedMS)
	}
}

// TestAWorkloadThatNeverQueuedReportsNoWaitsRatherThanNoAnswer is the no arm.
//
// The same instrument, the same number of clients, a mix of reads that take
// only AccessShareLock and conflict with nothing. It has to come back with a
// zero that is a MEASUREMENT: a nil here would mean the yes arm above proved
// only that the instrument fires, not that it fires on contention.
func TestAWorkloadThatNeverQueuedReportsNoWaitsRatherThanNoAnswer(t *testing.T) {
	url, _ := database(t)
	res, err := sqlload.Run(context.Background(), sqlload.Options{
		URL: url, Mix: readMix(t), Clients: 3, Duration: 2 * time.Second,
		Seed: 12, Clock: clock.New(),
	})
	require.NoError(t, err)

	require.Positive(t, res.Transactions, "a run that did nothing cannot say it did not queue")
	require.NotNilf(t, res.LockWaits,
		"the wait queues were never read, so this is not a zero: %s", res.LockWaitNote)
	require.Zero(t, *res.LockWaits,
		"reads that conflict with nothing were reported as having queued")
	require.NotNil(t, res.LockWaitMS)
	require.Zero(t, *res.LockWaitMS)
	require.Empty(t, res.LockWaitPairs)
	require.Equal(t, sqlload.LockWaitBound, res.LockWaitNote,
		"a clean run has to carry the instrument's own limits, not a failure note")
}

// TestARunBlockedFromOutsideItselfDoesNotReportItAsItsOwn is the attribution.
//
// A session that is not part of the run takes the row and sits on it. Every
// client then waits on a stranger, and the report has to say so: the WAITER is
// this run's, because the query asks about nobody else's backends, and the
// HOLDER is not, because it was not one of the clients. A pair that claimed
// the run blocked itself here would be blaming the mix for somebody else's
// open transaction, which is the wrong fix printed confidently.
func TestARunBlockedFromOutsideItselfDoesNotReportItAsItsOwn(t *testing.T) {
	url, outsider := database(t)
	ctx := context.Background()

	held, err := outsider.Begin(ctx)
	require.NoError(t, err)
	_, err = held.Exec(ctx, "UPDATE counters SET n = n + 1 WHERE id = 1")
	require.NoError(t, err)

	// Released part way through, so the run still commits something and this
	// test is not silently measuring a run that did nothing at all.
	var release sync.WaitGroup
	release.Add(1)
	go func() {
		defer release.Done()
		time.Sleep(1200 * time.Millisecond)
		_ = held.Rollback(ctx)
	}()

	res, err := sqlload.Run(ctx, sqlload.Options{
		URL: url, Mix: contendingMix(t), Clients: 2, Duration: 3 * time.Second,
		Seed: 13, Clock: clock.New(),
	})
	release.Wait()
	require.NoError(t, err)

	require.NotNilf(t, res.LockWaits, "nothing read the wait queues: %s", res.LockWaitNote)
	require.Positive(t, *res.LockWaits)

	var stranger bool
	for _, w := range res.LockWaitPairs {
		if w.BlockingInRun {
			continue
		}
		stranger = true
		require.Empty(t, w.BlockingStatement,
			"a backend outside the run was given one of this mix's statement labels")
		require.NotEmpty(t, w.BlockingState,
			"the holder was outside the run, so its state is the only thing that can "+
				"say what it was doing")
	}
	require.True(t, stranger,
		"an outside session held the row for more than a second and every wait was "+
			"attributed to the run's own clients")

	t.Logf("%d waits while an outside session held the row", *res.LockWaits)
	for _, w := range res.LockWaitPairs {
		t.Logf("  %q waited on in-run=%v state=%q %s %s, %d times",
			w.BlockedStatement, w.BlockingInRun, w.BlockingState, w.LockType, w.Mode, w.Waits)
	}
}
