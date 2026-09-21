package sqlload

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/load"
)

// ClientApplicationName is what every client connection calls itself.
//
// Exported because it is the only way anything outside this package can tell
// this run's backends apart from everybody else's in pg_stat_activity, and
// because the test that proves the clients really overlap has to ask the
// server the same question the observer asks.
const ClientApplicationName = "antifailure_sqlload"

// observerApplicationName is what the watching connection calls itself. A
// different name rather than a pid exclusion, so the observer cannot count
// itself even if the exclusion were wrong.
const observerApplicationName = "antifailure_sqlload_watch"

// maxRetries is how many times a transaction that lost a race is tried again.
//
// A deadlock and a serialization failure are not application bugs: they are
// what a database says when two transactions wanted the same rows, and the
// correct response is to run the transaction again. An application that did
// not retry would report every concurrent run as broken, so a generator that
// does not retry measures something no real caller does. Three, and the count
// is reported, because a workload that only completes on the third attempt is
// a finding even though every transaction eventually succeeded.
const maxRetries = 3

// observeInterval is how often the watching connection samples
// pg_stat_activity. Fast enough to catch overlap in a run of a few seconds,
// slow enough that the sampling is not itself the load.
const observeInterval = 200 * time.Millisecond

// Options configure one run.
type Options struct {
	// URL is the connection string for the branch. It is revealed by the
	// caller, which is also the caller that registered it with the redactor.
	URL string
	// Mix is what to run. Required.
	Mix *Mix
	// Clients is how many connections run at once. Each gets its own.
	Clients int
	// Duration bounds the run. Zero means run until Transactions is reached,
	// and both zero is refused rather than defaulted to forever.
	Duration time.Duration
	// Transactions bounds each client, the way pgbench's -t does. Zero means
	// the duration is the only bound. Per client rather than run wide,
	// because a run wide total makes the last transaction a race between
	// clients and two runs of one seed would then not execute the same
	// sequence.
	Transactions int
	// ThinkTime is how long a client waits between transactions. A workload
	// with no waiting in it is a benchmark rather than a rehearsal: it
	// measures the server at saturation and never measures it at the
	// concurrency a real application holds.
	ThinkTime time.Duration
	// Seed makes two runs execute the same sequence.
	Seed int64
	// Clock is the time source.
	Clock clock.Clock
	// Progress receives a line every second and may be nil.
	Progress func(Progress)
	// SkipObserver turns off the watching connection.
	//
	// It exists for one case: a server that will not give the run one more
	// connection. Off by default, because the observation is the only
	// evidence a report carries that the clients really did overlap, and a
	// run that quietly stopped gathering it would look exactly like a run
	// that did.
	SkipObserver bool
}

// Progress is how far along a run is.
type Progress struct {
	Elapsed      time.Duration
	Transactions int
	Failed       int
	TPS          float64
	P95Ms        float64
	Clients      int
}

// Result is what a run measured.
type Result struct {
	// Source is the mix's source, carried through so a reader can tell
	// production's own statements from a document.
	Source string `json:"source"`
	// Clients is how many connections ran.
	Clients int `json:"clients"`
	// ClientsStopped counts the clients that ended before the run did, and
	// StoppedBecause says why, one entry per distinct reason. A run of eight
	// clients that finished with six is not the run that was asked for, and a
	// TPS computed over it is a TPS at a concurrency nobody chose.
	ClientsStopped int            `json:"clients_stopped"`
	StoppedBecause map[string]int `json:"stopped_because,omitempty"`

	// Transactions is how many completed and committed.
	Transactions int `json:"transactions"`
	// TransactionsFailed is how many were rolled back and not retried into a
	// success.
	TransactionsFailed int `json:"transactions_failed"`
	// Retries is how many attempts were made again after a deadlock or a
	// serialization failure. It is attempts, not transactions: one
	// transaction retried twice counts two.
	Retries int `json:"retries"`
	// Deadlocks and SerializationFailures are counted separately from the
	// error map, because they are the two a concurrent workload exists to
	// provoke and a reader should not have to know their SQLSTATE to find
	// them.
	Deadlocks             int `json:"deadlocks"`
	SerializationFailures int `json:"serialization_failures"`

	// Statements is how many statements executed successfully, and
	// StatementsFailed how many raised.
	Statements       int `json:"statements"`
	StatementsFailed int `json:"statements_failed"`
	// Rows is how many rows those statements returned or changed in total.
	//
	// Reported because it is the one number that says whether a derived mix's
	// generated parameters matched anything. A run of forty thousand
	// statements that touched no rows measured the cost of finding nothing,
	// which is a real measurement of an index and is not a measurement of the
	// customer's result sets. Without this column a reader has no way to tell
	// the two apart, and the fast one looks like the good one.
	Rows int64 `json:"rows"`

	// Duration is the wall time the clients ran for.
	Duration time.Duration `json:"-"`
	// TPS is committed transactions per second over that wall time.
	//
	// Computed from committed transactions alone. A rate that counted failures
	// would report a database refusing every transaction instantly as the
	// fastest database anybody ever measured.
	TPS float64 `json:"tps"`
	// ErrorRate is the share of transactions that ended in an error, over
	// commits plus failures.
	//
	// A retry is NOT in the denominator and not in the numerator. A deadlock
	// that was retried into a commit is what a correct application does about
	// a deadlock, and counting it as a failure would make every honestly
	// written concurrent workload report a failing error rate. The retries are
	// counted on their own line instead, where a reader can see that a run
	// only completed on the second attempt.
	ErrorRate float64 `json:"error_rate"`
	// Overall is the latency of a committed transaction, end to end, which
	// includes every statement in it and the commit and excludes think time.
	Overall load.Latency `json:"overall"`

	// PerTransaction is each transaction kind's own numbers.
	PerTransaction []TransactionResult `json:"transactions_by_kind"`
	// PerStatement is each statement's own numbers, which is the row a person
	// changing an index actually reads.
	PerStatement []StatementResult `json:"statements_by_label"`

	// Errors counts failures by reason.
	Errors map[string]int `json:"errors,omitempty"`
	// Refused is what the mix would not run, carried through from the mix so
	// the result is readable without it.
	Refused []Refused `json:"refused"`

	// PeakActiveBackends is the most connections of this run the server
	// reported as EXECUTING a statement at one instant,
	// PeakOpenTransactions the most that were inside a transaction whether
	// executing or not, and BackendsSeen how many distinct backends the
	// observer ever saw.
	//
	// This is the evidence, and it is a measurement rather than a claim. N
	// goroutines are not N concurrent database sessions: a pool, a lock, a
	// serialised client library or a think time longer than the statement all
	// produce a run that spawned eight clients and never had two statements
	// in flight. Observed is nil when the observer could not run, because "no
	// overlap" and "not measured" are different answers.
	PeakActiveBackends   *int `json:"peak_active_backends"`
	PeakOpenTransactions *int `json:"peak_open_transactions"`
	BackendsSeen         *int `json:"backends_seen"`
	// ObserverNote says why the observation is missing, when it is.
	ObserverNote string `json:"observer_note,omitempty"`
}

// TransactionResult is one transaction kind's numbers.
type TransactionResult struct {
	Name      string       `json:"name"`
	Executed  int          `json:"executed"`
	Failed    int          `json:"failed"`
	Retries   int          `json:"retries"`
	Latency   load.Latency `json:"latency"`
	Weight    float64      `json:"weight"`
	Baselines Baseline     `json:"baseline"`
}

// StatementResult is one statement's numbers.
type StatementResult struct {
	// Transaction is which transaction the statement belongs to. The pair is
	// the identity, not the label: one statement can appear in two
	// transactions and their latencies cannot be merged, for the same reason
	// two scenarios' p95s for one route cannot be averaged.
	Transaction string `json:"transaction"`
	Label       string `json:"label"`
	Executed    int    `json:"executed"`
	Errors      int    `json:"errors"`
	// Rows is how many rows this statement returned or changed in total.
	Rows    int64        `json:"rows"`
	Latency load.Latency `json:"latency"`
}

// Baseline is what the mix's source said this transaction used to cost.
type Baseline struct {
	// MeanMs is the mean the source reported, and Has says whether there was
	// one at all.
	MeanMs float64 `json:"mean_ms,omitempty"`
	Has    bool    `json:"has_baseline"`
	// MeanIncrease is the measured mean over the baseline, less one, and is
	// meaningful only when Has is true.
	MeanIncrease float64 `json:"mean_increase,omitempty"`
}

// Run executes the mix and measures what happened.
//
// It returns a Result for every outcome that produced one, including a
// cancellation, and an error only when nothing could be measured at all. A
// caller that reads a non nil error as "nothing to report" throws away the
// more useful half of a failure, which is the shape the rest of this engine
// already settled on in workload.Execute.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Clock == nil {
		opts.Clock = clock.New()
	}
	if opts.Clients <= 0 {
		opts.Clients = 1
	}
	if opts.Duration <= 0 && opts.Transactions <= 0 {
		return nil, fmt.Errorf("sqlload: a run needs a duration or a transaction count, and has neither")
	}
	if opts.Mix == nil {
		return nil, fmt.Errorf("sqlload: a run needs a mix")
	}
	if err := opts.Mix.validate(); err != nil {
		return nil, err
	}

	m := newMeter(opts.Mix)

	// Connected before anything is measured, and all of them, because a run
	// that quietly proceeded with four of the eight clients it was asked for
	// would report a throughput at a concurrency nobody chose. The first
	// refusal is the whole run's refusal.
	conns, err := connectClients(ctx, opts)
	if err != nil {
		closeAll(ctx, conns)
		return nil, err
	}
	defer closeAll(ctx, conns)

	// Parameter pools are filled once, on one connection, before the clients
	// start. Once rather than per client so every client draws from the same
	// pool and the seed alone decides which value each one picks.
	if err := fillPools(ctx, conns[0], opts.Mix); err != nil {
		return nil, err
	}

	// Opened here, beside the clients' own connections, rather than inside the
	// watching goroutine. See newObserver for the run this ordering cost.
	obs := newObserver(ctx, opts)

	started := opts.Clock.Now()
	work, stop := context.WithCancel(ctx)
	defer stop()

	// The duration is a cancellation rather than a loop condition, so a client
	// waiting on a statement that will never answer is interrupted instead of
	// holding the run open past its deadline. It is driven off the engine's
	// clock rather than context.WithTimeout so that a test can end a run by
	// advancing a fake clock instead of by waiting for wall time.
	var timers sync.WaitGroup
	if opts.Duration > 0 {
		timers.Add(1)
		go func() {
			defer timers.Done()
			timer := opts.Clock.NewTimer(opts.Duration)
			defer timer.Stop()
			select {
			case <-timer.C():
				stop()
			case <-work.Done():
			}
		}()
	}

	var watcher sync.WaitGroup
	if !opts.SkipObserver {
		watcher.Add(1)
		go func() {
			defer watcher.Done()
			obs.run(work, opts)
		}()
	}

	var reporter sync.WaitGroup
	if opts.Progress != nil {
		reporter.Add(1)
		go func() {
			defer reporter.Done()
			reportProgress(work, opts, m, started)
		}()
	}

	var clients sync.WaitGroup
	for i, conn := range conns {
		clients.Add(1)
		go func(index int, conn *pgx.Conn) {
			defer clients.Done()
			runClient(work, opts, index, conn, m)
		}(i, conn)
	}
	clients.Wait()
	elapsed := opts.Clock.Since(started)
	// The observer and the progress reporter are told to stop only once every
	// client has finished, so the last sample is of a run that was still
	// running. Stopping them first would lose the peak of a run whose busiest
	// instant was its last.
	stop()
	watcher.Wait()
	reporter.Wait()
	timers.Wait()

	res := m.finish(opts, elapsed)
	obs.into(res)
	return res, ctx.Err()
}

// ErrClientsUnavailable is the server refusing to give the run the connections
// it asked for.
//
// A sentinel rather than a message, because the caller has a distinct error
// code for it: this one is retryable and says to lower the client count or
// raise max_connections, while every other setup failure is a configuration
// problem. Without it the orchestrator would have to match on the text of a
// driver error, which is the kind of matching that stops working on a Postgres
// upgrade.
var ErrClientsUnavailable = errors.New("the server would not give this run the connections it asked for")

// connectClients opens one connection per client.
func connectClients(ctx context.Context, opts Options) ([]*pgx.Conn, error) {
	out := make([]*pgx.Conn, 0, opts.Clients)
	for i := 0; i < opts.Clients; i++ {
		conn, err := connect(ctx, opts.URL, ClientApplicationName)
		if err != nil {
			return out, fmt.Errorf("client %d of %d could not connect: %w: %w",
				i+1, opts.Clients, ErrClientsUnavailable, err)
		}
		out = append(out, conn)
	}
	return out, nil
}

// connect opens one connection under a name pg_stat_activity will show.
func connect(ctx context.Context, url, appName string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	// Set on the connection rather than appended to the string, so a URL that
	// already carries an application_name is replaced rather than joined into
	// something the server reads as one long name.
	cfg.RuntimeParams["application_name"] = appName
	return pgx.ConnectConfig(ctx, cfg)
}

func closeAll(ctx context.Context, conns []*pgx.Conn) {
	for _, c := range conns {
		if c == nil {
			continue
		}
		// Closed on a context the caller cannot cancel, for the same reason
		// the workload teardown is: a cancelled run still has to give its
		// connections back, and closing them on the cancelled context leaves
		// the server holding backends for a run that has ended.
		_ = c.Close(context.WithoutCancel(ctx))
	}
}

// fillPools runs each query parameter's query once and keeps what it returned.
func fillPools(ctx context.Context, conn *pgx.Conn, mix *Mix) error {
	for ti := range mix.Transactions {
		tx := &mix.Transactions[ti]
		for si := range tx.Statements {
			st := &tx.Statements[si]
			for pi := range st.Params {
				p := &st.Params[pi]
				if p.Kind != ParamQuery {
					continue
				}
				pool, err := readPool(ctx, conn, p.Query)
				if err != nil {
					return fmt.Errorf("the values for parameter %d of %q could not be read: %w",
						pi+1, tx.Name, err)
				}
				if len(pool) == 0 {
					// An empty pool is refused rather than filled with null.
					// A statement bound to null reads nothing and reports a
					// fast, clean, meaningless run, which is exactly the
					// green over nothing this product exists to stop.
					return fmt.Errorf("the query for parameter %d of %q returned no rows, "+
						"so every execution would be bound to nothing", pi+1, tx.Name)
				}
				p.pool = pool
			}
		}
	}
	return nil
}

func readPool(ctx context.Context, conn *pgx.Conn, query string) ([]any, error) {
	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []any
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			return nil, fmt.Errorf("the query returned a row with no columns")
		}
		out = append(out, values[0])
		if len(out) >= maxPoolValues {
			break
		}
	}
	return out, rows.Err()
}

// maxPoolValues bounds what one parameter query may hold. A pool is drawn from
// uniformly, so a million values buys nothing a thousand does not, and it
// would hold a million rows of the customer's data in the engine's memory.
const maxPoolValues = 1000

// runClient is one connection's whole life.
func runClient(ctx context.Context, opts Options, index int, conn *pgx.Conn, m *meter) {
	// One generator per client, seeded from the run's seed and the client's
	// index, rather than one generator shared between the clients behind a
	// mutex. The same decision load.PlanScenario makes per session and for the
	// same reason: a shared generator makes the sequence each client sees
	// depend on how the scheduler interleaved them, so two runs of one seed
	// would not execute the same work.
	//
	// One per CLIENT and not one per concern. It drives both the choice of
	// transaction and the parameter values, because two generators seeded from
	// the same number draw the same first uniform, so the first transaction a
	// client picks would predict the first row it touches. Drawing both from
	// one stream is what makes them independent of each other, and it is why
	// runTransaction binds a transaction's values once rather than again on
	// every retry: a retry taking a draw would move everything after it.
	rng := rand.New(rand.NewSource(opts.Seed + int64(index)))
	p, err := newPicker(opts.Mix.Transactions)
	if err != nil {
		m.clientStopped("the client could not build its mix: " + err.Error())
		return
	}

	done := 0
	for {
		if ctx.Err() != nil {
			return
		}
		if opts.Transactions > 0 && done >= opts.Transactions {
			return
		}

		tx := p.next(rng)
		outcome := runTransaction(ctx, opts.Clock, conn, tx, rng, m)
		if outcome.fatal != "" {
			m.clientStopped(outcome.fatal)
			return
		}
		done++

		if opts.ThinkTime > 0 {
			if err := opts.Clock.Sleep(ctx, opts.ThinkTime); err != nil {
				return
			}
		}
	}
}

// outcome is what one transaction attempt sequence produced.
type outcome struct {
	// fatal is set when the client cannot continue at all, which today means
	// the connection is gone.
	fatal string
}

// runTransaction runs one transaction, retrying a lost race.
//
// The values are bound ONCE, before the first attempt, and every retry uses
// them again. An application that retries a deadlocked transaction retries the
// same work, and drawing new values would also make the value stream depend on
// how many times a transaction lost a race, which is a timing question: two
// runs of one seed would bind different values from the first retry onward.
func runTransaction(ctx context.Context, c clock.Clock, conn *pgx.Conn, tx Transaction, rng *rand.Rand, m *meter) outcome {
	bound, err := bindAll(tx, rng)
	if err != nil {
		// A parameter that cannot produce a value is a mix that should never
		// have been accepted, and Validate is where that is caught. Counting
		// the transaction as failed here rather than stopping the client keeps
		// the run reporting rather than silently ending.
		m.failed(tx.Name, reasonQueryFailed)
		return outcome{}
	}
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return outcome{}
		}
		started := c.Now()
		err := executeOnce(ctx, c, conn, tx, bound, m)
		if err == nil {
			m.committed(tx.Name, msSince(c, started))
			return outcome{}
		}

		// A cancelled context is the caller stopping, not the database
		// failing. Counting it as a failed transaction would make every
		// cancelled run report a burst of errors it did not have.
		if ctx.Err() != nil {
			return outcome{}
		}

		reason := classify(err)
		if conn.IsClosed() || isConnectionGone(err) {
			m.failed(tx.Name, reason)
			return outcome{fatal: reason}
		}
		if (reason == reasonDeadlock || reason == reasonSerialization) && attempt < maxRetries {
			m.retried(tx.Name, reason)
			continue
		}
		m.failed(tx.Name, reason)
		return outcome{}
	}
}

// executeOnce runs the statements of one transaction inside one BEGIN.
func executeOnce(ctx context.Context, c clock.Clock, conn *pgx.Conn, tx Transaction, bound [][]any, m *meter) error {
	dbTx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	for i, st := range tx.Statements {
		args := bound[i]
		started := c.Now()
		tag, err := dbTx.Exec(ctx, st.SQL, args...)
		if err != nil {
			// The run ending is not the statement failing, and the check is
			// here because runTransaction already makes exactly this decision
			// one level up: a transaction interrupted by the caller or by the
			// duration running out is not counted as a failed transaction.
			// Only the statement layer counted it anyway, and the two
			// disagreeing is worse than either answer on its own.
			//
			// Measured against a real database rather than reasoned about. A
			// ten second declared run of a healthy read mix committed 5416
			// transactions, reported 0 failed and 0 retried, and printed 5
			// errors against a read that had just run 3640 times and returned
			// a row every time: the five clients that were mid statement when
			// the duration expired. A reader given that table goes looking for
			// a query that never failed, and the summary line above it says
			// nothing is wrong, so the table is the only thing they have to go
			// on and it is the thing that is lying.
			if ctx.Err() == nil {
				m.statementFailed(tx.Name, st.Label)
			}
			// Rolled back on a context the caller cannot cancel, so a
			// transaction that failed at the moment the run was stopped still
			// releases its locks rather than leaving them for the server to
			// reap when the connection closes.
			_ = dbTx.Rollback(context.WithoutCancel(ctx))
			return err
		}
		m.statementDone(tx.Name, st.Label, msSince(c, started), tag.RowsAffected())
	}
	return dbTx.Commit(ctx)
}

// bindAll binds every statement's parameters for one transaction, in order.
func bindAll(tx Transaction, rng *rand.Rand) ([][]any, error) {
	out := make([][]any, len(tx.Statements))
	for i, st := range tx.Statements {
		args, err := bind(st.Params, rng)
		if err != nil {
			return nil, err
		}
		out[i] = args
	}
	return out, nil
}

// bind turns the declared parameters into values for this execution.
func bind(params []Param, rng *rand.Rand) ([]any, error) {
	if len(params) == 0 {
		return nil, nil
	}
	out := make([]any, 0, len(params))
	for _, p := range params {
		v, err := p.value(rng)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (p Param) value(rng *rand.Rand) (any, error) {
	switch p.Kind {
	case ParamInt:
		span := p.Max - p.Min + 1
		if span <= 0 {
			return p.Min, nil
		}
		return p.Min + rng.Int63n(span), nil
	case ParamText:
		return p.Values[rng.Intn(len(p.Values))], nil
	case ParamQuery:
		if len(p.pool) == 0 {
			return nil, fmt.Errorf("the pool for a query parameter is empty")
		}
		return p.pool[rng.Intn(len(p.pool))], nil
	case ParamGenerated:
		return generate(p.Type, rng)
	}
	return nil, fmt.Errorf("a parameter has no kind")
}

func msSince(c clock.Clock, t time.Time) float64 {
	return float64(c.Since(t).Microseconds()) / 1000
}

// The reasons a transaction failed. A closed set, because a console groups on
// them and an unbounded set of server messages would produce one group per
// row.
const (
	reasonDeadlock       = "deadlock"
	reasonSerialization  = "serialization failure"
	reasonUniqueConflict = "unique violation"
	reasonCancelled      = "cancelled by the server"
	reasonTooManyConns   = "too many connections"
	reasonConnectionGone = "connection lost"
	reasonQueryFailed    = "query failed"
)

// classify names why a transaction failed, in the vocabulary a reader knows.
//
// SQLSTATE where the server gave one, because the class is the fact and the
// message is prose that changes between releases and between locales. The
// engine already learned that lesson about pg_stat_statements text.
func classify(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "40P01":
			return reasonDeadlock
		case "40001":
			return reasonSerialization
		case "23505":
			return reasonUniqueConflict
		case "57014":
			return reasonCancelled
		case "53300":
			return reasonTooManyConns
		case "57P01", "57P02", "57P03":
			// The server shutting down, crashing, or refusing to start a
			// session. The connection is gone whatever the transaction was.
			return reasonConnectionGone
		default:
			return "SQLSTATE " + pg.Code
		}
	}
	if isConnectionGone(err) {
		return reasonConnectionGone
	}
	return reasonQueryFailed
}

// isConnectionGone reports whether the client can carry on at all.
func isConnectionGone(err error) bool {
	if errors.Is(err, pgx.ErrTxClosed) || errors.Is(err, pgx.ErrTxCommitRollback) {
		return false
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "57P01", "57P02", "57P03":
			return true
		}
		// Anything else the server answered with is a live connection: it
		// answered.
		return false
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "conn closed"),
		strings.Contains(text, "connection closed"),
		strings.Contains(text, "connection reset"),
		strings.Contains(text, "broken pipe"),
		strings.Contains(text, "unexpected EOF"),
		strings.Contains(text, "EOF"),
		strings.Contains(text, "connection refused"),
		strings.Contains(text, "no such host"),
		strings.Contains(text, "i/o timeout"):
		return true
	}
	return false
}

// reportProgress emits a line a second while the run is going.
func reportProgress(ctx context.Context, opts Options, m *meter, started time.Time) {
	ticker := opts.Clock.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			opts.Progress(m.progress(opts.Clock.Since(started), opts.Clients))
		}
	}
}

// meter collects what every client measured.
type meter struct {
	mu sync.Mutex

	transactions map[string]*txStat
	statements   map[stmtKey]*stmtStat
	errors       map[string]int
	stopped      map[string]int

	all []float64

	commits      int
	failures     int
	retries      int
	deadlocks    int
	serialFails  int
	statementsOK int
	statementsNo int
	rows         int64

	weights   map[string]float64
	baselines map[string]Baseline
	order     []string
	refused   []Refused
	source    string
}

type stmtKey struct{ transaction, label string }

type txStat struct {
	executed int
	failed   int
	retries  int
	samples  []float64
}

type stmtStat struct {
	executed int
	errors   int
	rows     int64
	samples  []float64
}

func newMeter(mix *Mix) *meter {
	m := &meter{
		transactions: map[string]*txStat{},
		statements:   map[stmtKey]*stmtStat{},
		errors:       map[string]int{},
		stopped:      map[string]int{},
		weights:      map[string]float64{},
		baselines:    map[string]Baseline{},
		source:       mix.Source,
		refused:      mix.Refused,
	}
	// Every declared transaction and statement gets a row before the run
	// starts, so a transaction that was never picked reports zero executions
	// rather than vanishing from the report. A mix whose rarest transaction
	// never ran is a finding about the run's length, and a missing row hides
	// it.
	for _, tx := range mix.Transactions {
		m.transactions[tx.Name] = &txStat{}
		m.weights[tx.Name] = tx.Weight
		m.baselines[tx.Name] = Baseline{MeanMs: tx.BaselineMeanMs, Has: tx.HasBaseline}
		m.order = append(m.order, tx.Name)
		for _, st := range tx.Statements {
			m.statements[stmtKey{tx.Name, st.Label}] = &stmtStat{}
		}
	}
	return m
}

func (m *meter) committed(name string, ms float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commits++
	m.all = append(m.all, ms)
	if s := m.transactions[name]; s != nil {
		s.executed++
		s.samples = append(s.samples, ms)
	}
}

func (m *meter) failed(name, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures++
	m.errors[reason]++
	m.countRace(reason)
	if s := m.transactions[name]; s != nil {
		s.failed++
	}
}

func (m *meter) retried(name, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retries++
	m.errors[reason]++
	m.countRace(reason)
	if s := m.transactions[name]; s != nil {
		s.retries++
	}
}

// countRace is called with the lock held.
func (m *meter) countRace(reason string) {
	switch reason {
	case reasonDeadlock:
		m.deadlocks++
	case reasonSerialization:
		m.serialFails++
	}
}

func (m *meter) statementDone(tx, label string, ms float64, rows int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statementsOK++
	m.rows += rows
	if s := m.statements[stmtKey{tx, label}]; s != nil {
		s.executed++
		s.rows += rows
		s.samples = append(s.samples, ms)
	}
}

func (m *meter) statementFailed(tx, label string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statementsNo++
	if s := m.statements[stmtKey{tx, label}]; s != nil {
		s.errors++
	}
}

func (m *meter) clientStopped(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped[reason]++
}

func (m *meter) progress(elapsed time.Duration, clients int) Progress {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := Progress{
		Elapsed: elapsed, Transactions: m.commits, Failed: m.failures,
		P95Ms: load.Percentiles(m.all).P95Ms, Clients: clients - len(m.stopped),
	}
	if elapsed > 0 {
		p.TPS = float64(m.commits) / elapsed.Seconds()
	}
	return p
}

func (m *meter) finish(opts Options, elapsed time.Duration) *Result {
	m.mu.Lock()
	defer m.mu.Unlock()

	res := &Result{
		Source: m.source, Clients: opts.Clients,
		Transactions: m.commits, TransactionsFailed: m.failures,
		Retries: m.retries, Deadlocks: m.deadlocks, SerializationFailures: m.serialFails,
		Statements: m.statementsOK, StatementsFailed: m.statementsNo, Rows: m.rows,
		Duration: elapsed, Overall: load.Percentiles(m.all),
		Errors: map[string]int{}, Refused: m.refused,
	}
	if res.Refused == nil {
		res.Refused = []Refused{}
	}
	for k, v := range m.errors {
		res.Errors[k] = v
	}
	for reason, n := range m.stopped {
		res.ClientsStopped += n
		if res.StoppedBecause == nil {
			res.StoppedBecause = map[string]int{}
		}
		res.StoppedBecause[reason] += n
	}
	if elapsed > 0 {
		res.TPS = float64(m.commits) / elapsed.Seconds()
	}
	if attempts := m.commits + m.failures; attempts > 0 {
		res.ErrorRate = float64(m.failures) / float64(attempts)
	}

	for _, name := range m.order {
		s := m.transactions[name]
		tr := TransactionResult{
			Name: name, Executed: s.executed, Failed: s.failed, Retries: s.retries,
			Latency: load.Percentiles(s.samples), Weight: m.weights[name],
			Baselines: m.baselines[name],
		}
		if tr.Baselines.Has && tr.Baselines.MeanMs > 0 && len(s.samples) > 0 {
			tr.Baselines.MeanIncrease = mean(s.samples)/tr.Baselines.MeanMs - 1
		}
		res.PerTransaction = append(res.PerTransaction, tr)
	}

	for key, s := range m.statements {
		res.PerStatement = append(res.PerStatement, StatementResult{
			Transaction: key.transaction, Label: key.label,
			Executed: s.executed, Errors: s.errors, Rows: s.rows,
			Latency: load.Percentiles(s.samples),
		})
	}
	sort.Slice(res.PerStatement, func(i, j int) bool {
		// Slowest first, because that is the line somebody changing an index
		// is looking for and scrolling to find it is the same as not showing
		// it.
		a, b := res.PerStatement[i], res.PerStatement[j]
		if a.Latency.P95Ms != b.Latency.P95Ms {
			return a.Latency.P95Ms > b.Latency.P95Ms
		}
		if a.Transaction != b.Transaction {
			return a.Transaction < b.Transaction
		}
		return a.Label < b.Label
	})
	return res
}

func mean(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	total := 0.0
	for _, s := range samples {
		total += s
	}
	return total / float64(len(samples))
}
