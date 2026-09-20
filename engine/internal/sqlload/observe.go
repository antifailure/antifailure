package sqlload

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// The observer is the evidence, and this file exists because the obvious proof
// is not one.
//
// A run says it used eight clients. Counting the goroutines it started proves
// only that the engine started eight goroutines. Every one of them could be
// waiting behind a lock held by the first, or behind a client library that
// serialises, or behind a think time longer than the statement, and the run
// would report eight clients having never had two statements in the server at
// once. The claim is about the SERVER, so the measurement has to come from the
// server.
//
// So a separate connection samples pg_stat_activity while the run is going and
// reports three numbers. How many distinct backends of this run it ever saw,
// which proves the clients are separate sessions rather than one connection
// shared. The most it ever saw EXECUTING a statement at one instant. And the
// most it ever saw holding a transaction open, executing or not.
//
// The last two are two numbers because they answer two questions and a report
// that gave only one would mislead either way. A backend between the second
// and third statement of a transaction is idle in transaction: it is holding
// every lock it has taken and it is not running anything. Counting only the
// executing ones understates the contention a run produced, and counting them
// together would let a run whose clients all sat idle in transaction report
// itself as saturating the server. A run whose peak is one on BOTH did not
// rehearse concurrency whatever its client count said, and a reader can see
// that without taking anybody's word for it.
//
// It samples rather than integrates, so it is a lower bound: two statements
// that overlapped entirely between two samples are not counted. A lower bound
// is the right error to make here, because it can only understate the
// concurrency and so can never manufacture the evidence it exists to provide.

// observer watches the run's own backends from a connection of its own.
type observer struct {
	mu     sync.Mutex
	conn   *pgx.Conn
	peak   int
	peakTx int
	pids   map[int32]bool
	note   string
	ran    bool
}

// newObserver opens the watching connection.
//
// BEFORE the clients start, and that ordering was earned rather than chosen. It
// opened its connection inside its own goroutine first, which meant it was
// asking a server that eight clients had already saturated for a ninth backend:
// the connect took longer than the whole three second run, the first sample
// then found the context already cancelled, and the result reported that
// nothing had been observed. The run was perfectly healthy and its only
// evidence was missing. A watching connection is part of a run's setup, so it
// is opened when the other connections are.
func newObserver(ctx context.Context, opts Options) *observer {
	o := &observer{pids: map[int32]bool{}}
	if opts.SkipObserver {
		return o
	}
	conn, err := connect(ctx, opts.URL, observerApplicationName)
	if err != nil {
		o.failed("the watching connection could not be opened: " + short(err))
		return o
	}
	o.conn = conn
	// One sample here, before the clients start, and it is not a formality.
	// Every client connection is already open at this point, so this is what
	// establishes how many distinct backends the run holds. Without it a run
	// whose every later sample was cut short by the run ending reported that
	// nobody had looked, when in fact the sessions were there to be counted:
	// measured on a machine under a load average of 170, where the first
	// sample took longer than the whole three second run.
	//
	// It contributes nothing to the peaks, which is correct: no client has
	// begun a transaction yet, so zero executing and zero open is the truth at
	// this instant.
	o.sample(ctx, conn)
	return o
}

// run samples until the context ends.
func (o *observer) run(ctx context.Context, opts Options) {
	conn := o.conn
	if conn == nil {
		return
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	ticker := opts.Clock.NewTicker(observeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			o.sample(ctx, conn)
		}
	}
}

// activeQuery counts this run's own backends and says which are executing.
//
// application_name rather than a list of pids the engine collected, because
// the engine's list would have to be gathered from the connections themselves
// and a connection that died would drop out of it silently. The name is set on
// every client connection at connect time and the server reports it, so the
// question is asked of the server end to end.
const activeQuery = `
SELECT pid,
       state = 'active' AS busy,
       state IN ('active', 'idle in transaction', 'idle in transaction (aborted)') AS in_tx
FROM pg_stat_activity
WHERE application_name = $1`

// sampleTimeout bounds one reading of pg_stat_activity.
//
// The sample runs on a context DETACHED from the run's, which is the correction
// this file needed most. It used the run's context, so a sample still in flight
// when the run ended was cancelled and its data thrown away, and a run whose
// first sample was slower than the run itself reported that nobody had looked.
// An observation that has been taken is worth keeping whether or not the thing
// it was observing has since stopped. The timeout is what stops the detachment
// from becoming a wait with no end: Run joins this goroutine before it returns,
// so a cancelled run can be held up by at most one sample.
const sampleTimeout = 3 * time.Second

func (o *observer) sample(ctx context.Context, conn *pgx.Conn) {
	read, cancel := context.WithTimeout(context.WithoutCancel(ctx), sampleTimeout)
	defer cancel()

	rows, err := conn.Query(read, activeQuery, ClientApplicationName)
	if err != nil {
		o.failed("pg_stat_activity could not be read: " + short(err))
		return
	}
	defer rows.Close()

	active, inTx := 0, 0
	var seen []int32
	for rows.Next() {
		var pid int32
		var busy, open bool
		if err := rows.Scan(&pid, &busy, &open); err != nil {
			o.failed("pg_stat_activity could not be read: " + short(err))
			return
		}
		seen = append(seen, pid)
		if busy {
			active++
		}
		if open {
			inTx++
		}
	}
	if err := rows.Err(); err != nil {
		o.failed("pg_stat_activity could not be read: " + short(err))
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	o.ran = true
	if active > o.peak {
		o.peak = active
	}
	if inTx > o.peakTx {
		o.peakTx = inTx
	}
	for _, pid := range seen {
		o.pids[pid] = true
	}
}

func (o *observer) failed(note string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	// The first reason rather than the last. A database that went away mid run
	// produces one honest failure and then a hundred identical ones, and the
	// first is the one that says what happened.
	if o.note == "" {
		o.note = note
	}
}

// into writes what was observed onto the result.
//
// The two counts are written only when a sample actually landed. A run whose
// observer never connected reports null and a note, never zero: zero backends
// is a finding and "nobody looked" is not, and a console that cannot tell them
// apart will draw the second as the first.
func (o *observer) into(res *Result) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.ran {
		peak, peakTx, seen := o.peak, o.peakTx, len(o.pids)
		res.PeakActiveBackends = &peak
		res.PeakOpenTransactions = &peakTx
		res.BackendsSeen = &seen
	}
	if o.note != "" {
		res.ObserverNote = o.note
		return
	}
	if !o.ran {
		res.ObserverNote = "the run's own backends were never sampled, so this result says " +
			"nothing about whether its clients overlapped"
	}
}

// short trims a driver error to one line for a note.
func short(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := indexNewline(s); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

func indexNewline(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' {
			return i
		}
	}
	return -1
}
