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
	//
	// EMPTY IS NORMAL AND HAS THREE CAUSES, none of them "the holder was doing
	// nothing". A test that required a state here failed on another lane's CI
	// and was right to, because all three are measured facts about Postgres
	// rather than races worth ignoring:
	//
	//   - AN UNPRIVILEGED ROLE, which is the ordinary case for a customer
	//     pointing this product at their own server. Without superuser and
	//     without pg_read_all_stats or pg_monitor, a foreign backend's ROW is
	//     visible while state, backend_type and query are WITHHELD. Read twice
	//     at one instant against one server: as superuser, pid 1641 is
	//     "client backend / postgres / active"; as a role with LOGIN and
	//     nothing else, the same pid is present with usename readable, state
	//     withheld and query "<insufficient privilege>". ROWS VISIBLE, COLUMNS
	//     MASKED: over the same unfiltered reading the row count was identical
	//     at six either way, while backend_type IS NULL went from zero rows as
	//     superuser to five as the plain role. That sentence is here because
	//     the obvious wrong conclusion is that an unprivileged role sees
	//     nothing at all, and it sees everything except the columns that
	//     matter. So on such a deployment this is empty for EVERY holder
	//     outside the run, always, not sometimes.
	//   - A BACKEND THAT IS NOT A CLIENT, which an idle server already has
	//     several of: the autovacuum launcher, the background writer, the
	//     checkpointer, the logical replication launcher and the walwriter all
	//     report a NULL state, measured as superuser so it is the server's own
	//     answer rather than a privilege effect.
	//   - THE HOLDER'S ROW BEING GONE by the time the outer join is evaluated,
	//     because the wait queues and pg_stat_activity are two reads.
	//
	// When looking at this yourself, do not filter on usename to find a
	// foreign backend: usename is itself withholdable, so the filter can
	// remove the very rows it is looking for and answer zero, which reads as
	// "an unprivileged role sees nothing at all" and is wrong. Count the
	// unfiltered view.
	//
	// BlockingNamed is what separates all three from the different question of
	// whether a holder was identified at all.
	BlockingState string `json:"blocking_state,omitempty"`
	// BlockingNamed says whether the server named a holder at all, and it is
	// the difference between "somebody else held it" and "nobody knows who
	// held it".
	//
	// Three outcomes, not two, and the third is not hypothetical: the query's
	// own comment says pg_blocking_pids returns an empty array for a backend
	// that is genuinely waiting when the holder disconnected between the two
	// reads, and the outer join onto pg_stat_activity yields no state for a pid
	// whose row has gone. Both arrived with BlockingInRun false and an empty
	// state, which is indistinguishable from a stranger, and af load sql
	// therefore printed "another session on this database" about a holder
	// nothing had identified. That is the same defect as reading a null wait
	// count as a zero, one level in: an absence rendered as a finding.
	//
	// The WAIT is still reported when this is false, because the wait is the
	// part that was measured. Dropping it would trade a false claim for a
	// missing one.
	BlockingNamed bool `json:"blocking_named"`
	// BlockingPrepared says the named holder is a PREPARED TRANSACTION, which
	// is a real holder with no session at all.
	//
	// Measured rather than reasoned about, because it is the case that proved
	// three outcomes were still one too few. pg_locks carries a prepared
	// transaction with a NULL pid, and pg_blocking_pids reports it as pid ZERO
	// rather than by omitting it: a backend blocked on one answered
	// "blockers {0}, cardinality 1". So the pid is present, BlockingNamed is
	// true, and the outer join on b.pid = 0 finds nothing, which left the
	// renderer saying "another session on this database" about something that
	// is not a session. That is the same false attribution this field's
	// neighbour was added to remove, one costume along.
	//
	// It earns its own answer rather than being folded into "not named",
	// because the remedy is specific and a reader can act on it: a two phase
	// commit holder is released with COMMIT PREPARED or ROLLBACK PREPARED and
	// there is no session to cancel.
	BlockingPrepared bool `json:"blocking_prepared"`
	// BlockingInRun says whether the holder was one of this run's own clients.
	// Meaningless unless BlockingNamed is true, and false in both directions
	// when it is not, which is why the two are separate fields.
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
// The waiter is restricted to this run's application_name AND to this run's
// database, and the holder is restricted to neither. A neighbour's wait is
// never this run's finding, and a neighbour HOLDING a lock this run waited on
// is very much this run's finding.
//
// THE DATABASE PREDICATE IS WHAT MAKES THE pg_class JOIN BELOW CORRECT, and it
// was missing. pg_stat_activity is cluster wide, exactly as pg_locks is, and
// ClientApplicationName is a constant every run of this package shares, so a
// concurrent run against a SIBLING database on the same server answered to the
// application_name filter as though it were this one. Two things then went
// wrong at once, and the second is the quieter of the two.
//
// The relation was resolved in the wrong catalogue. pg_locks names a relation
// by OID alone and the documentation says what that costs: joining it to
// pg_class "will only work correctly for relations in the current database".
// A branch here is a CREATE DATABASE ... TEMPLATE copy, which carries pg_class
// verbatim, so two branches of one golden agree on the OID of every table in
// it. Measured on two such copies, with the table renamed in the second so the
// misresolution could not hide: a session in branch_b queued behind another on
// branch_b's shipments, OID 16386, and this query run in branch_a returned that
// wait with relname ORDERS, which is branch_a's own table at the same OID and
// was never waited on by anybody. A real local table name, for a wait that
// never touched it.
//
// And the wait itself was counted. readLockWaits records every returned waiter
// in the run wide pids set, so a stranger's queue raised this run's wait count
// and its waited milliseconds, which is the number a result persists as "how
// many times one of the run's own backends was seen to start waiting". The
// labels come back empty, because the pid is not one of this run's, and empty
// labels are DOCUMENTED as ordinary above, so nothing downstream could tell the
// row apart from a wait this run really suffered.
//
// Neither is privilege gated, which is worth stating because it is the obvious
// place to hope for a guarantee. Measured as an unprivileged role with no
// pg_read_all_stats: pid, datname and application_name of a foreign backend are
// all visible and pg_blocking_pids answers for it. Only query and state are
// withheld, so the only effect of running unprivileged is that the invented row
// arrives with an empty BlockingState as well.
//
// On a.datname rather than on l.database, and the difference is a whole class of
// wait. pg_locks.database is the database of the locked OBJECT and it is NULL
// for a transaction id lock, so a predicate written there would silently drop
// the transaction id waits a row level conflict produces, which this instrument
// exists to cover and says it covers. The backend's own database is the right
// question anyway: a backend of this run is connected to this database, so
// every relation OID it can lock is resolvable in this database's pg_class,
// including the shared catalogues, which appear in every database's pg_class
// and are the one case where pg_locks.database is zero.
//
// IF YOU EVER ADD AN OID SAFETY PREDICATE HERE, KEY IT ON l.relation AND NOT ON
// l.database ALONE. pg_locks leaves database NULL for every lock type that
// names no relation, which includes the transactionid waits a ROW conflict
// produces, so "l.database = 0 OR l.database = <this one>" is NULL for them and
// a NULL in a WHERE clause is a row discarded. Written that way it silently
// drops row level contention, the commonest and most interesting kind, while
// tuple and relation waits keep arriving so the instrument still looks as
// though it works. Measured, not reasoned about: it cost
// TestLockContentionInsideTheRunIsSeenAndBothStatementsAreNamed, which is why
// that test now requires a row conflict to produce a transactionid wait. Where
// there is no relation there is no OID to resolve and nothing to protect.
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
  WHERE a.application_name = $1 AND a.datname = current_database()
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

		w := lockWaitFrom(blocked, blocker, state, relation, lockType, mode, reg)
		key := lockKey(w)
		seen := out.waiting[key]
		seen.wait = w
		seen.count++
		out.waiting[key] = seen
	}
	return out, rows.Err()
}

// lockWaitFrom turns one row of the wait queue reading into a pair.
//
// A pure function, separated from the query for one reason: it decides which of
// FOUR things a holder is, and that decision is worth testing without a server.
// max_prepared_transactions is zero by default, so the case that proved three
// outcomes were too few cannot be built on the suite's own Postgres at all, and
// a test that skips on the only machine that runs it is worse than no test. The
// query is proved against a real server; this is proved against every
// combination of inputs.
func lockWaitFrom(
	blocked int32, blocker *int32, state, relation, lockType, mode string,
	reg *statements,
) LockWait {
	w := LockWait{Relation: relation, LockType: lockType, Mode: mode}
	if ref, _ := reg.lookup(blocked); ref != nil {
		w.BlockedTransaction, w.BlockedStatement = ref.Transaction, ref.Label
	}
	if blocker == nil {
		// No pid came back, so the wait is the only thing known. Every field
		// about the holder stays zero, which is what BlockingNamed false means.
		return w
	}
	w.BlockingNamed = true
	// Zero is the lock manager naming a holder that has no backend, which
	// today means a prepared transaction. It must not reach the registry as
	// though it were a pid, and it must never be reported as a session.
	w.BlockingPrepared = *blocker == 0
	if w.BlockingPrepared {
		// Belt and braces rather than load bearing, and said so because a
		// mutation of this return SURVIVES: lookup already refuses a pid of
		// zero or less, and the server's own state column is empty for a pid
		// that matches no backend, so removing this changes no field. It stays
		// as a statement of intent, because a reader should not have to derive
		// "a prepared transaction is never one of our clients" from a bounds
		// check two functions away.
		return w
	}
	ref, mine := reg.lookup(*blocker)
	w.BlockingInRun = mine
	w.BlockingState = state
	if ref != nil {
		w.BlockingTransaction, w.BlockingStatement = ref.Transaction, ref.Label
	}
	return w
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
		"\x00" + w.LockType + "\x00" + w.Mode +
		"\x00" + boolKey(w.BlockingInRun) + boolKey(w.BlockingNamed) +
		boolKey(w.BlockingPrepared)
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
	sortLockWaits(out)
	return out
}

// sortLockWaits puts the pairs worst first, and it is a function rather than a
// closure inside list because Merge pools several rounds' pairs and has to
// order the pool the same way. Two orderings of the same rows, one for a run
// and one for a pool of rounds, is a difference a reader would attribute to
// the builds being compared.
func sortLockWaits(out []LockWait) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].WaitedMS != out[j].WaitedMS {
			return out[i].WaitedMS > out[j].WaitedMS
		}
		if out[i].Waits != out[j].Waits {
			return out[i].Waits > out[j].Waits
		}
		return lockKey(out[i]) < lockKey(out[j])
	})
}
