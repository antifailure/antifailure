package pgcrash

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Workload is the concurrent writers a crash happens underneath.
//
// It writes to a schema of its own, which is created before the writers start
// and dropped when the run is over, so the only thing a fault can be shown to
// have lost is rows this package put there. Reading a customer's own table
// after a crash would be a better demonstration and a worse idea: the check
// would then be asserting that a table somebody else writes did not change,
// while somebody else was writing it.
//
// The writers are deliberately plain. Each one holds a single connection, runs
// one insert per transaction, and never retries: a retry would put the same id
// in twice and make "was this commit acknowledged" a question with two answers.
// When its connection dies under it, a writer stops rather than reconnecting,
// because the point of the run is what the ledger says at the moment of the
// crash, not how quickly a client recovers from one.
type Workload struct {
	opts   WorkloadOptions
	ledger *Ledger

	stop   chan struct{}
	cancel context.CancelFunc
	closed sync.Once
	wg     sync.WaitGroup

	mu      sync.Mutex
	errs    int
	lastErr string
}

// WorkloadOptions configures the writers.
type WorkloadOptions struct {
	// URL is the database to write to.
	URL string
	// Writers is how many connections write at once. More than one, always:
	// a single writer serialises the workload and a crash under a serial
	// workload exercises none of the concurrency the recovery has to get
	// right.
	Writers int
	// Schema and Table are where the rows go.
	Schema, Table string
	// SynchronousCommit is what each writer sets the setting to, or empty to
	// leave the server's own value alone.
	//
	// This is the knob that makes the durability check falsifiable. With it
	// off, Postgres acknowledges a commit before the write ahead log record
	// reaches the operating system, so a crash that discards shared memory
	// genuinely loses commits the client was told were durable. A check that
	// has never reported a lost commit has not been shown able to.
	SynchronousCommit string
	// SampleLSNEvery is how many acknowledged commits a writer makes between
	// readings of the flush position. Zero means never.
	SampleLSNEvery int
}

const (
	defaultWriters         = 8
	defaultSchema          = "antifailure_chaos"
	defaultTable           = "commits"
	defaultSampleLSN       = 32
	idStride         int64 = 1 << 32
)

// SynchronousCommitValues are the settings a workload may ask for.
//
// A closed list because the value is concatenated into a SET statement, and
// the one place a manifest's string reaches SQL is the one place to refuse
// anything that is not on a list somebody wrote.
func SynchronousCommitValues() []string {
	return []string{"on", "off", "local", "remote_write", "remote_apply"}
}

// ValidSynchronousCommit reports whether v is a setting a workload may ask
// for. Empty means "leave the server's own value alone" and is valid.
func ValidSynchronousCommit(v string) bool {
	if v == "" {
		return true
	}
	for _, known := range SynchronousCommitValues() {
		if v == known {
			return true
		}
	}
	return false
}

// withDefaults fills in what the caller left out.
func (o WorkloadOptions) withDefaults() WorkloadOptions {
	if o.Writers <= 0 {
		o.Writers = defaultWriters
	}
	if o.Schema == "" {
		o.Schema = defaultSchema
	}
	if o.Table == "" {
		o.Table = defaultTable
	}
	if o.SampleLSNEvery == 0 {
		o.SampleLSNEvery = defaultSampleLSN
	}
	return o
}

// Qualified is the schema qualified table name, quoted.
func (o WorkloadOptions) Qualified() string {
	return pgx.Identifier{o.Schema, o.Table}.Sanitize()
}

// Prepare creates the schema and table and makes them durable.
//
// The checkpoint at the end is not tidiness. Without it the CREATE TABLE is
// itself un-replayed write ahead log at the moment of the crash, so a run that
// lost the table would look like a run that lost every row, and the report
// would be about the wrong thing.
func Prepare(ctx context.Context, url string, opts WorkloadOptions) error {
	opts = opts.withDefaults()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return fmt.Errorf("pgcrash: connecting to prepare the workload: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	schema := pgx.Identifier{opts.Schema}.Sanitize()
	for _, stmt := range []string{
		"DROP SCHEMA IF EXISTS " + schema + " CASCADE",
		"CREATE SCHEMA " + schema,
		"CREATE TABLE " + opts.Qualified() + " (id bigint PRIMARY KEY, writer int NOT NULL, at timestamptz NOT NULL DEFAULT now())",
		"CHECKPOINT",
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("pgcrash: preparing the workload with %q: %w", firstWord(stmt), err)
		}
	}
	return nil
}

// Drop removes the schema the workload owns.
func Drop(ctx context.Context, url string, opts WorkloadOptions) error {
	opts = opts.withDefaults()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return fmt.Errorf("pgcrash: connecting to drop the workload schema: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	_, err = conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{opts.Schema}.Sanitize()+" CASCADE")
	return err
}

// StartWorkload opens the writers and returns once they are writing.
func StartWorkload(ctx context.Context, opts WorkloadOptions) (*Workload, error) {
	opts = opts.withDefaults()
	if !ValidSynchronousCommit(opts.SynchronousCommit) {
		return nil, fmt.Errorf("pgcrash: synchronous_commit %q is not one of %s",
			opts.SynchronousCommit, strings.Join(SynchronousCommitValues(), ", "))
	}
	// The writers run under a context this workload owns, so that Stop can
	// cancel a statement that will never come back on its own.
	//
	// It is not a nicety. A frozen container answers nothing and closes
	// nothing: a writer inside Exec against a paused Postgres waits forever,
	// and a Stop that only closed a channel and waited would deadlock the
	// whole run. A partitioned container is the same shape. Cancelling the
	// statement is also the honest ledger entry, because a write that was
	// abandoned mid flight is exactly what unresolved means.
	writerCtx, cancel := context.WithCancel(ctx)
	w := &Workload{opts: opts, ledger: NewLedger(), stop: make(chan struct{}), cancel: cancel}

	// Every writer's first connection is opened before any of them writes, so
	// that a workload which cannot connect fails here rather than reporting a
	// crash that happened while it was still trying to start.
	conns := make([]*pgx.Conn, 0, opts.Writers)
	for i := 0; i < opts.Writers; i++ {
		conn, err := pgx.Connect(ctx, opts.URL)
		if err != nil {
			cancel()
			for _, c := range conns {
				_ = c.Close(context.WithoutCancel(ctx))
			}
			return nil, fmt.Errorf("pgcrash: opening writer %d of %d: %w", i+1, opts.Writers, err)
		}
		if opts.SynchronousCommit != "" {
			if _, err := conn.Exec(ctx, "SET synchronous_commit = "+opts.SynchronousCommit); err != nil {
				cancel()
				_ = conn.Close(context.WithoutCancel(ctx))
				for _, c := range conns {
					_ = c.Close(context.WithoutCancel(ctx))
				}
				return nil, fmt.Errorf("pgcrash: setting synchronous_commit on writer %d: %w", i+1, err)
			}
		}
		conns = append(conns, conn)
	}
	for i, conn := range conns {
		w.wg.Add(1)
		go w.write(writerCtx, i, conn)
	}
	return w, nil
}

// write is one writer's loop.
func (w *Workload) write(ctx context.Context, writer int, conn *pgx.Conn) {
	defer w.wg.Done()
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	insert := "INSERT INTO " + w.opts.Qualified() + " (id, writer) VALUES ($1, $2)"
	var seq int64
	var sinceSample int
	for {
		select {
		case <-w.stop:
			return
		case <-ctx.Done():
			return
		default:
		}
		id := int64(writer)*idStride + seq
		seq++

		// Recorded before the statement is sent, so that a writer killed in
		// the gap leaves the id as "might have landed" rather than as nothing
		// at all.
		w.ledger.Attempt(id)
		_, err := conn.Exec(ctx, insert, id, writer)
		if err != nil {
			w.note(err)
			if fatal(err) {
				return
			}
			continue
		}
		w.ledger.Acknowledged(id)

		sinceSample++
		if sinceSample >= w.opts.SampleLSNEvery {
			sinceSample = 0
			var lsn string
			if err := conn.QueryRow(ctx, "SELECT pg_current_wal_flush_lsn()::text").Scan(&lsn); err == nil {
				if v, err := ParseLSN(lsn); err == nil {
					w.ledger.ObservedFlushLSN(v)
				}
			}
		}
	}
}

// fatal reports whether an error means this connection is finished.
//
// A writer that kept retrying on a dead connection would spin at the speed of
// the dial timeout and fill the ledger with attempts that were never sent,
// which would make the unresolved count a measure of how long the loop ran
// rather than of how many commits were in flight when the fault landed.
func fatal(err error) bool {
	if err == nil {
		return false
	}
	if conn, ok := err.(interface{ SafeToRetry() bool }); ok && !conn.SafeToRetry() {
		return true
	}
	s := err.Error()
	for _, phrase := range []string{
		"conn closed",
		"unexpected EOF",
		"broken pipe",
		"connection reset",
		"server closed the connection",
		"terminating connection",
		"the database system is in recovery mode",
		"the database system is starting up",
		"the database system is shutting down",
		"the database system is not yet accepting connections",
	} {
		if strings.Contains(s, phrase) {
			return true
		}
	}
	return false
}

// note records that a write failed, keeping a count and the most recent one.
func (w *Workload) note(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.errs++
	w.lastErr = err.Error()
}

// Stop ends the writers and waits for them.
//
// Safe to call more than once, and safe to call when every writer has already
// exited because its connection died: the deferred stop in a caller runs after
// a crash by construction, and a stop that panicked on a crashed workload
// would lose the ledger that the whole run exists to read.
func (w *Workload) Stop() {
	w.closed.Do(func() {
		close(w.stop)
		// A grace period first, so that the ordinary case leaves every writer
		// having finished the statement it was on and the ledger says what
		// the database said about it. Only a writer that is still waiting
		// after that is cancelled, which is the writer whose server is frozen
		// or unreachable.
		done := make(chan struct{})
		go func() { w.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(stopGrace):
			w.cancel()
		}
	})
	w.wg.Wait()
	w.cancel()
}

// stopGrace is how long a writer has to finish the statement it is on before
// its context is cancelled.
const stopGrace = 2 * time.Second

// Ledger is what the client observed.
func (w *Workload) Ledger() *Ledger { return w.ledger }

// Errors is how many writes failed, and the most recent failure.
func (w *Workload) Errors() (int, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.errs, w.lastErr
}

// Options is the configuration the workload is running under.
func (w *Workload) Options() WorkloadOptions { return w.opts }

// WaitForCommits blocks until the ledger holds at least n acknowledged
// commits or the deadline passes, and reports how many there were.
//
// A crash is injected once the workload is warm, and "warm" has to mean
// commits acknowledged rather than seconds elapsed. Sleeping for a second on a
// busy machine and then killing a database that has not acknowledged anything
// yet produces a run with nothing to lose, which passes every durability
// assertion by having nothing to assert.
func (w *Workload) WaitForCommits(ctx context.Context, n int, within time.Duration) int {
	deadline := time.Now().Add(within)
	for {
		acked, _, _ := w.ledger.Counts()
		if acked >= n || time.Now().After(deadline) {
			return acked
		}
		select {
		case <-ctx.Done():
			return acked
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// firstWord is how a statement is named in an error, so a failure says
// "CREATE" rather than quoting the whole statement back.
func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}
