package sqlload

import (
	"context"
	"fmt"
	"sort"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
)

// Lock contention, measured while the workload is running.
//
// The engine could already say that a concurrent run deadlocked or lost a
// serialization race, because both of those END a transaction and the client
// sees the SQLSTATE. It could not say anything at all about the far commoner
// outcome, which is a transaction that neither failed nor was retried and
// simply WAITED. A build that takes a lock a little earlier, or holds it a
// little longer, moves the percentiles and changes nothing else this result
// reports: no deadlock, no serialization failure, no error, a slower run and
// no reason given for it. Contention was inferred from the two outcomes loud
// enough to raise, and the quiet one was invisible.
//
// So the watching connection asks the server directly. pg_blocking_pids(pid)
// is the instrument Postgres provides for exactly this and nothing in this
// repository had ever called it. It answers "which backends stand between this
// one and the lock it asked for", it covers every lock type the lock manager
// queues on rather than relations alone, and it does the search inside the
// server where the wait queues actually are.
//
// WHAT THIS IS NOT. It is not the migration rehearsal's lock sampler in
// engine/internal/insights. That one asks what one DDL statement HOLDS and for
// how long, against a branch nothing else is using, and it reports a hold
// whether or not anybody ever waited behind it. This one asks who WAITED,
// under a workload with many sessions, and a lock nobody queued on never
// appears here at all. Two questions, two shapes, and a lane that merged them
// would break the gate that reads the first.
//
// WHY IT RIDES THE EXISTING OBSERVER. An observer that competes with the thing
// it observes measures itself. The watching connection is already open, it is
// already outside the client set, and it is already sampling on a detached
// context: a third connection would be one more backend contending for the
// same lock manager while claiming to report on it.

// LockWait is one blocked statement, one statement that blocked it, and how
// much waiting the pair was seen to cost.
//
// A LABEL rather than a pid on both sides. A pid is meaningless between two
// runs, so a comparison keyed on one can never line up, and a person reading
// "23844 waited on 23851" learns nothing they can act on. The run knows what
// every one of its own backends is executing because its own clients told it,
// so the pair is named in the vocabulary of the mix.
type LockWait struct {
	// BlockedTransaction and BlockedStatement are the mix's own names for the
	// statement that was waiting.
	//
	// Empty is possible and is not a defect. The wait queues and the client's
	// own account of what it is running are two readings taken microseconds
	// apart, so a backend that was queueing when pg_locks was read can have
	// finished by the time its label is looked up, and a backend waiting to
	// BEGIN or to COMMIT is running no statement of the mix at all. Empty
	// means the label could not be joined, never that no statement was
	// involved.
	BlockedTransaction string `json:"blocked_transaction,omitempty"`
	BlockedStatement   string `json:"blocked_statement,omitempty"`
	// BlockingTransaction and BlockingStatement are the same for the backend
	// in front of it, and they are EMPTY in the most interesting case rather
	// than wrong. A holder that is idle in transaction is running no statement
	// to name: it is holding every lock it has taken and doing nothing, which
	// is the classic shape of this defect, and BlockingState is what says so.
	BlockingTransaction string `json:"blocking_transaction,omitempty"`
	BlockingStatement   string `json:"blocking_statement,omitempty"`
	// BlockingState is pg_stat_activity's own word for what the holder was
	// doing: active, idle in transaction, and so on.
	BlockingState string `json:"blocking_state,omitempty"`
	// BlockingInRun says whether the holder was one of this run's own clients.
	//
	// A boolean rather than the neighbour's application_name, and the
	// distinction is the attribution rule this file is built on. The WAITER is
	// always this run's, because the query asks only about this run's backends
	// and a neighbour's wait is never counted here. The HOLDER may be anybody
	// on the database, and a run blocked by something outside itself is a real
	// and important finding that must not read as the run blocking itself.
	// Carrying a stranger's chosen application_name out into a report would be
	// carrying a stranger's text; the boolean is the part that is true.
	BlockingInRun bool `json:"blocking_in_run"`
	// Relation is the table the ungranted lock names, and it is empty for the
	// lock types that name none. A transaction id wait, which is what a row
	// level conflict produces, is a wait for another transaction to END rather
	// than for an object, so Postgres records no relation against it and this
	// field invents none. LockType is what says which kind of wait it was.
	Relation string `json:"relation,omitempty"`
	LockType string `json:"lock_type"`
	// Mode is the lock mode that was ASKED FOR and refused, in Postgres's own
	// spelling.
	Mode string `json:"mode"`
	// Waits is how many times a backend was seen to START waiting in this
	// pair, counted on the sample where it first appears rather than again on
	// every sample it is still there. It is a floor on how many times this
	// pair queued: two separate waits either side of one sample interval read
	// as one. The same word means the same thing on the run wide count.
	Waits int `json:"waits"`
	// WaitedMS is one sample interval for every sample a backend was seen
	// waiting in this pair. See LockWaitBound for what that is and is not.
	//
	// The pair figures can sum to more than the run wide total and that is
	// arithmetic rather than a defect: a backend queued behind two holders
	// appears in two pairs and waited once, so the run wide total counts it
	// once and each pair is told what it was part of.
	WaitedMS float64 `json:"waited_ms"`
}

// LockWaitBound says what these numbers are, in the result rather than in a
// document nobody opens.
//
// Assembled from observeInterval rather than written out, because a constant
// that repeats a number another constant owns is a sentence that goes wrong
// silently the first time somebody tunes the interval.
var LockWaitBound = fmt.Sprintf(
	"Sampled every %s with pg_blocking_pids, so a wait that began and ended between two "+
		"samples is missing from this entirely and the counts are floors rather than "+
		"totals. Every lock type the server queues on is in scope, including the "+
		"transaction id waits a row level conflict produces, tuple locks and advisory "+
		"locks, and every pair says which kind it was. Contention that never becomes a "+
		"wait is out of scope by definition: a lock granted with nobody ahead of it cost "+
		"nothing, and a deadlock or a serialization failure is counted on its own line.",
	observeInterval)

// lockWaitQuery asks which of this run's backends are waiting and who is in
// front of them.
//
// The CTE is MATERIALIZED and that is load bearing rather than stylistic.
// pg_blocking_pids takes the lock manager's partition locks to walk the wait
// queues, so it is the one part of this sample that can interfere with the
// workload being sampled. Materialising the waiters first means the function
// is called once per backend of THIS RUN that is actually waiting, which is
// zero calls on an uncontended run. Written as a lateral over an unfiltered
// pg_stat_activity, a planner that chose to evaluate the function before the
// application_name filter would call it once per backend on the whole server,
// every 200 milliseconds, for a number that would then be thrown away.
//
// LEFT JOIN LATERAL rather than CROSS JOIN LATERAL, and the difference is a
// finding rather than a nicety. pg_blocking_pids can return an empty array for
// a backend that is genuinely waiting: the holder disconnected between the two
// reads, or the wait is on something the function does not attribute. A CROSS
// JOIN would drop that row, so the run would report no wait at all for a
// backend it had just watched waiting. The wait is the fact; the name of the
// holder is what may be missing.
//
// The waiter is restricted to this run's application_name and the holder is
// not. A neighbour's wait is never this run's finding, and a neighbour HOLDING
// a lock this run waited on is very much this run's finding.
//
// AS MATERIALIZED needs Postgres 12, which is where the keyword was added and
// also where a plain CTE stopped being a fence on its own. On anything older
// this query does not parse, the sample reports that the wait queues could not
// be read, and the run says it does not know whether it blocked. That is the
// honest failure rather than a silent zero, and Postgres 11 left support in
// November 2023.
const lockWaitQuery = `
WITH waiting AS MATERIALIZED (
  SELECT a.pid, l.locktype, l.mode, l.relation
  FROM pg_stat_activity a
  JOIN pg_locks l ON l.pid = a.pid AND NOT l.granted
  WHERE a.application_name = $1
)
SELECT w.pid,
       bp.pid,
       COALESCE(b.state, ''),
       COALESCE(c.relname, ''),
       w.locktype,
       w.mode
FROM waiting w
LEFT JOIN LATERAL unnest(pg_blocking_pids(w.pid)) AS bp(pid) ON true
LEFT JOIN pg_stat_activity b ON b.pid = bp.pid
LEFT JOIN pg_class c ON c.oid = w.relation`

// maxLockPairs bounds how many distinct pairs one run keeps.
//
// The cardinality is (statement, statement, lock type, mode, holder state), so
// a mix of any size cannot produce an unbounded number of them, but "cannot"
// is a belief about a mix somebody else writes. The cap is what stops a
// surprising one from growing a map for the length of a run, and a truncation
// is REPORTED rather than silent: a list that quietly stopped growing is a
// list somebody reads as complete.
const maxLockPairs = 64

// stmtRef is one statement's identity in the mix, shared by pointer.
//
// Built once per statement before the run starts and never allocated again, so
// a client publishing what it is about to execute costs one atomic store. The
// alternative, allocating the pair on every execution, would put an allocation
// inside the loop whose latency this package exists to measure.
type stmtRef struct {
	Transaction string
	Label       string
}

// statements is what each of this run's clients is executing right now,
// readable by the observer without touching the clients.
//
// A backend pid per client and an atomic pointer per client, rather than a map
// behind a mutex. The clients write to it on every statement and the observer
// reads it five times a second, so a shared mutex would put the measurement in
// the path of the thing being measured: the clients would contend on the
// engine's own lock and the run would report the observer's cost as the
// database's.
type statements struct {
	pids    []uint32
	current []atomic.Pointer[stmtRef]
}

// newStatements records which backend each client holds.
//
// Read from the connections rather than from the server, because the driver
// already knows: the backend pid arrives in the startup message. Asking
// pg_stat_activity instead would mean matching connections to rows by
// application_name, which every client shares, so it could not tell them
// apart at all.
func newStatements(conns []*pgx.Conn) *statements {
	s := &statements{
		pids:    make([]uint32, len(conns)),
		current: make([]atomic.Pointer[stmtRef], len(conns)),
	}
	for i, c := range conns {
		if c == nil {
			continue
		}
		s.pids[i] = c.PgConn().PID()
	}
	return s
}

// track is one client's handle on the registry.
type track struct {
	all   *statements
	index int
}

// begin publishes what this client is about to run, and end withdraws it.
//
// end leaves a NIL rather than the last statement's name, and that is the
// distinction the whole join rests on. A backend between two statements of an
// open transaction is holding every lock it has taken and running nothing, so
// naming the statement it last ran would report a holder as busy with work it
// had already finished. Nothing is the truth there, and BlockingState is what
// turns that nothing into an answer.
func (t track) begin(ref *stmtRef) {
	if t.all == nil {
		return
	}
	t.all.current[t.index].Store(ref)
}

func (t track) end() {
	if t.all == nil {
		return
	}
	t.all.current[t.index].Store(nil)
}

// lookup says whether a backend belongs to this run and what it was running.
func (s *statements) lookup(pid int32) (ref *stmtRef, mine bool) {
	if s == nil || pid <= 0 {
		return nil, false
	}
	want := uint32(pid)
	for i, p := range s.pids {
		if p == want {
			return s.current[i].Load(), true
		}
	}
	return nil, false
}

// labelStatements gives every statement in the mix its shared identity.
func labelStatements(mix *Mix) {
	for ti := range mix.Transactions {
		tx := &mix.Transactions[ti]
		for si := range tx.Statements {
			st := &tx.Statements[si]
			st.ref = &stmtRef{Transaction: tx.Name, Label: st.Label}
		}
	}
}

// lockSample is one reading of the wait queues, folded into the observer.
type lockSample struct {
	// waiting is the pairs seen in this sample with how many backends were in
	// each, because two clients queued behind one holder on the same statement
	// are one pair and two waits.
	waiting map[string]lockRow
	// pids is the distinct backends of this run seen waiting, which is what
	// the run wide counts are taken over: a backend queued behind two holders
	// waited once, not twice.
	pids map[int32]bool
}

type lockRow struct {
	wait  LockWait
	count int
}

// readLockWaits runs one sample of the wait queues.
func readLockWaits(ctx context.Context, conn *pgx.Conn, reg *statements) (lockSample, error) {
	out := lockSample{waiting: map[string]lockRow{}, pids: map[int32]bool{}}
	rows, err := conn.Query(ctx, lockWaitQuery, ClientApplicationName)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	for rows.Next() {
		var blocked int32
		var blocker *int32
		var state, relation, lockType, mode string
		if err := rows.Scan(&blocked, &blocker, &state, &relation, &lockType, &mode); err != nil {
			return out, err
		}
		out.pids[blocked] = true

		w := LockWait{Relation: relation, LockType: lockType, Mode: mode}
		if ref, _ := reg.lookup(blocked); ref != nil {
			w.BlockedTransaction, w.BlockedStatement = ref.Transaction, ref.Label
		}
		if blocker != nil {
			ref, mine := reg.lookup(*blocker)
			w.BlockingInRun = mine
			w.BlockingState = state
			if ref != nil {
				w.BlockingTransaction, w.BlockingStatement = ref.Transaction, ref.Label
			}
		}
		key := lockKey(w)
		seen := out.waiting[key]
		seen.wait = w
		seen.count++
		out.waiting[key] = seen
	}
	return out, rows.Err()
}

// lockKey is a pair's identity for aggregation.
//
// The holder's STATE is part of it, which looks like over splitting and is
// not. The same two statements blocking each other while the holder executes
// and while the holder sits idle in transaction are two different defects with
// two different fixes, and a row that averaged them would name neither.
func lockKey(w LockWait) string {
	return w.BlockedTransaction + "\x00" + w.BlockedStatement +
		"\x00" + w.BlockingTransaction + "\x00" + w.BlockingStatement +
		"\x00" + w.BlockingState + "\x00" + w.Relation +
		"\x00" + w.LockType + "\x00" + w.Mode + "\x00" + boolKey(w.BlockingInRun)
}

func boolKey(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// lockTotals accumulates the samples into what the result reports.
type lockTotals struct {
	// ran says a sample completed, so zero waits means zero waits rather than
	// nobody having looked.
	ran bool
	// waits counts a wait on the sample it FIRST appears in, so a wait held
	// across four samples is one wait rather than four.
	waits int
	// waitMS is one interval per sample per waiting backend.
	waitMS float64
	// pairs is every distinct pair seen, and dropped counts the ones the cap
	// refused so the truncation can be reported.
	pairs   map[string]*LockWait
	dropped int
	// previous is what was waiting in the last sample, which is what turns a
	// continuing wait into no new wait.
	previousPIDs  map[int32]bool
	previousPairs map[string]int
	// note is the first failure, kept for the same reason the observer keeps
	// the first: a database that went away produces one honest reason and then
	// a hundred identical ones.
	note string
}

func newLockTotals() *lockTotals {
	return &lockTotals{
		pairs:         map[string]*LockWait{},
		previousPIDs:  map[int32]bool{},
		previousPairs: map[string]int{},
	}
}

// add folds one sample in.
func (t *lockTotals) add(s lockSample, intervalMS float64) {
	t.ran = true
	for pid := range s.pids {
		if !t.previousPIDs[pid] {
			t.waits++
		}
		t.waitMS += intervalMS
	}
	for key, row := range s.waiting {
		pair, ok := t.pairs[key]
		if !ok {
			if len(t.pairs) >= maxLockPairs {
				t.dropped++
				continue
			}
			copied := row.wait
			pair = &copied
			t.pairs[key] = pair
		}
		if newly := row.count - t.previousPairs[key]; newly > 0 {
			pair.Waits += newly
		}
		pair.WaitedMS += intervalMS * float64(row.count)
	}
	t.previousPIDs = s.pids
	t.previousPairs = make(map[string]int, len(s.waiting))
	for key, row := range s.waiting {
		t.previousPairs[key] = row.count
	}
}

// failed keeps the first reason the wait queues could not be read.
func (t *lockTotals) failed(note string) {
	if t.note == "" {
		t.note = note
	}
	// The previous sample is cleared, because a failed sample is not evidence
	// that a wait ended. Leaving it would let the next successful sample see
	// the same wait as continuing when in truth nobody knows what happened in
	// between; clearing it counts that wait again, which is the direction that
	// reports contention rather than hides it.
	t.previousPIDs = map[int32]bool{}
	t.previousPairs = map[string]int{}
}

// list returns the pairs, worst first.
func (t *lockTotals) list() []LockWait {
	out := make([]LockWait, 0, len(t.pairs))
	for _, p := range t.pairs {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WaitedMS != out[j].WaitedMS {
			return out[i].WaitedMS > out[j].WaitedMS
		}
		if out[i].Waits != out[j].Waits {
			return out[i].Waits > out[j].Waits
		}
		return lockKey(out[i]) < lockKey(out[j])
	})
	return out
}
