package insights

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// LockSampleInterval is how often the sampler asks what is locked.
//
// 250 milliseconds is the spec's figure and it is a compromise worth naming: a
// lock held for less than that can be missed entirely, and a lock held for
// less than that is not the one that takes production down. Sampling faster
// costs a round trip per sample against the database being rehearsed, which
// would distort the timings the rehearsal exists to measure.
const LockSampleInterval = 250 * time.Millisecond

// LockHold is the strongest lock one table was seen under, and for how long.
type LockHold struct {
	Table string `json:"table"`
	// Mode is the strongest lock mode observed, using Postgres's own names.
	Mode string `json:"mode"`
	// HeldMS is how long the table was seen locked at any mode. It is a
	// sampled figure, so it is a lower bound rounded to the sample interval,
	// and it is reported as such rather than as a measurement.
	HeldMS float64 `json:"held_ms"`
	// Blocking is whether another session was ever seen waiting on it. A lock
	// nothing waited for cost nothing, whatever its mode.
	Blocking bool `json:"blocking"`
	// Statement is what held it, when pg_stat_activity had one.
	Statement string `json:"statement,omitempty"`
}

// lockStrength orders Postgres's lock modes from weakest to strongest. The
// order is the one in the documentation's conflict table, and it is what makes
// "the strongest mode held" a well defined thing to report.
var lockStrength = map[string]int{
	"AccessShareLock":          1,
	"RowShareLock":             2,
	"RowExclusiveLock":         3,
	"ShareUpdateExclusiveLock": 4,
	"ShareLock":                5,
	"ShareRowExclusiveLock":    6,
	"ExclusiveLock":            7,
	"AccessExclusiveLock":      8,
}

// sampler watches what a migration locks while it runs.
type sampler struct {
	conn *pgx.Conn
	// exclude is the backends that are not the migration: the sampler's own
	// and the rehearsal's bookkeeping connection.
	//
	// Excluding rather than including is the correction to an earlier version
	// that watched one named backend. The applier opens its own connection,
	// and an applier that runs the project's migrate command in a container
	// opens one we never see at all, so naming the backend to watch means
	// watching the wrong one and reporting no locks on a migration that held
	// an ACCESS EXCLUSIVE lock for ninety seconds. A rehearsal branch is a
	// fresh database nothing else uses, so everything left after the
	// exclusions is the migration.
	exclude []int32

	mu    sync.Mutex
	holds map[string]*LockHold
	err   error

	stop chan struct{}
	done chan struct{}
}

// watchLocks samples pg_locks and pg_stat_activity on its own connection until
// the returned function is called.
//
// It has to be a second connection: the one running the migration is busy
// running the migration, and a lock held by a statement in flight is invisible
// to the session holding it until that statement returns, which is exactly
// when the interesting part is over.
func watchLocks(
	ctx context.Context, conn *pgx.Conn, exclude []int32, every time.Duration,
) func() []LockHold {
	s := &sampler{
		conn: conn, exclude: exclude,
		holds: map[string]*LockHold{},
		stop:  make(chan struct{}), done: make(chan struct{}),
	}
	go s.run(ctx, every)
	return func() []LockHold {
		close(s.stop)
		<-s.done
		return s.result()
	}
}

func (s *sampler) run(ctx context.Context, every time.Duration) {
	defer close(s.done)
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	s.sample(ctx, every)
	for {
		select {
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sample(ctx, every)
		}
	}
}

// bookkeepingPrefix is what this package's own objects on the branch are
// called. The DDL capture table, its primary key and its sequence all begin
// with it, and none of them belongs to the change under review.
//
// They are excluded from the sample rather than filtered later, so nothing
// downstream has to know they exist. The event trigger writes a row inside the
// migration's own transaction, so the capture table carries RowExclusiveLock
// for exactly as long as the migration runs: a migration that holds a lock for
// three seconds produced FOUR lock findings, one about the customer's table
// and three about ours, each telling them to split a statement that never
// touched it. A check that says no about the wrong thing teaches people to
// stop reading it, which costs the finding beside it that was true.
const bookkeepingPrefix = "af_insights_"

// systemSchemaPrefix is the prefix Postgres reserves for its own schemas.
//
// This is the difference between a filter that states what a reportable
// relation IS and a filter that lists the kinds of noise somebody thought of.
// The earlier version named two namespaces, pg_catalog and information_schema,
// and a whole family of relations lives outside both: every TOAST table is in
// pg_toast, a temporary relation is in pg_temp_N and its TOAST relation in
// pg_toast_temp_N. So they were all reported as relations the change locked.
//
// The family is closed, and it is closed in the source rather than by
// inspection. toasting.c chooses between exactly two namespaces for every TOAST
// relation it creates, and there is no third branch: "Toast tables for regular
// relations go in pg_toast; those for temp relations go into the per-backend
// temp-toast-table namespace." pg_namespace.dat bootstraps pg_toast at OID 99
// and calls it "reserved schema for TOAST tables". Both names begin pg_.
//
// pg_toast_2618 reached a pull request that way, which is the TOAST table of
// pg_rewrite, written by any migration whose new view has a rule past the toast
// threshold. The worse face is that a USER table's TOAST table is in pg_toast
// too, named pg_toast_<oid>, and a table rewrite takes ACCESS EXCLUSIVE on it.
// That is the strongest mode there is and the key this report sorts by, so a
// rewrite of orders put a relation called pg_toast_16388 above orders itself,
// under a name the author has no way to recognise. A temporary table reached it
// as well, once it outlived the migration file that created it, because
// Postgres records no relation lock for a relation made inside the transaction
// still running.
//
// Matching the prefix rather than the members is sound rather than convenient,
// and the documentation is what makes it sound: "Schema names beginning with pg_
// are reserved for system purposes and cannot be created by users." So no
// relation a project owns can ever sit in a namespace this skips, and no
// namespace Postgres adds later can escape it. information_schema is named
// separately because it is the one reserved schema that does not carry the
// prefix, and it is matched by name rather than by OID because initdb builds it
// from a script rather than bootstrapping it, so its OID differs per cluster.
// TestPostgres_TheSystemSchemaPrefixIsReserved measures the reservation from the
// server, because the whole shape rests on it.
//
// A relkind allowlist was the other candidate and was rejected, because the
// enumeration risk points the wrong way there. TOAST is the only relkind nobody
// can name in a migration; every other kind is addressable, including a
// materialized view whose REFRESH takes ACCESS EXCLUSIVE and blocks readers for
// real. An allowlist that forgot one of those would drop a TRUE finding, and a
// lock finding that never appears is the failure this repository keeps finding in
// its own instruments. A namespace list can only over-report, which is loud. A
// relkind list can under-report, which is silent.
//
// A TOAST relation is not resolved to the table it belongs to, which was the
// other option. It would read better and it would be a worse measurement: the
// parent is already in the same sample, because a statement that locks a TOAST
// relation holds a lock on the table it hangs off, and merging the TOAST row
// into it would report a mode on the author's table that no statement ever
// took there. The number this report exists for is how long production's
// queries would have queued, and queries queue on the table they name.
const systemSchemaPrefix = "pg_"

// THERE ARE THREE OF THESE PREDICATES AND TWO OF THEM ARE NOT IN THIS FILE.
// This used to be the only place in the repository that read pg_locks, and the
// sentence saying so is the reason somebody will arrive here expecting to find
// the whole of it. They will not. The other two are both in
// engine/internal/sqlload: the waiting CTE in lockwait.go, which reads pg_locks
// for the concurrent workload's wait queues, and activeQuery in observe.go,
// which reads pg_stat_activity for the same run's backend counts. All three had
// the same gap for the same reason and all three were closed in one commit. If
// you are auditing the scoping, grep for current_database() across
// engine/internal rather than reading this file and stopping.
//
// The database predicate in the query below is the other half of the same
// defect, and it is the half that does not look like noise.
//
// The documentation states the defect and the remedy in consecutive sentences,
// under pg_locks. "pg_locks provides a global view of all locks in the database
// cluster, not only those relevant to the current database." And then: "Although
// its relation column can be joined against pg_class.oid to identify locked
// relations, this will only work correctly for relations in the current database
// (those for which the database column is either the current database's OID or
// zero)." This query did that join and did not carry that condition.
//
// What it costs: the join resolves a foreign OID against THIS database's
// catalogue, so a lock held elsewhere is reported under whatever relation
// happens to carry the same OID here. Catalogue OIDs are identical in every
// database by construction, and a rehearsal branch is a CREATE DATABASE ...
// TEMPLATE copy of the golden, which copies pg_class verbatim: two branches of
// the same golden therefore agree on the OID of every table in it.
//
// Measured, on two template copies of one database: a session in the second
// taking ACCESS EXCLUSIVE on its own orders put a row reading orders,
// AccessExclusiveLock into the report of a rehearsal running in the first that
// had touched nothing but users. Not an opaque name this time. The author's own
// table, at the mode and the duration that fail the check, indistinguishable
// from a finding about their change. Two rehearsals on one cluster is the normal
// case here, not a contrived one.
//
// Written as the documentation writes it, the current database's OID or zero,
// rather than as the OID alone. Zero is a shared relation, and the join IS valid
// for one, so admitting it keeps this predicate answering exactly one question:
// is the lock on a relation this database can see. Dropping shared catalogues is
// the namespace predicate's job and it already does it. The two conditions are
// then each correct on their own, which matters because the alternative leaves a
// filter that is only correct while its neighbour is.
//
// Nothing is lost either way. A lock in another database cannot queue a query in
// this one, which is the only thing this report measures.
//
// The Blocking subquery needed it too, and that half is the one with teeth. It
// matched a waiter on w.relation alone, so a session queued behind a foreign
// relation of the same OID set Blocking on this hold, and Blocking is not
// cosmetic: gate.MigrationFindings reports a hold that Blocking is set on even
// when its duration is below the ignore threshold, and adds "Another session was
// seen waiting on it" to the finding. A foreign waiter could therefore conjure a
// finding out of a lock too brief to report, and then explain it with a sentence
// that was not true. Fixed by requiring the waiter to be in the holder's own
// database, which is stronger than repeating the condition above: two sessions
// contending for one relation are necessarily in one database, so this says what
// contention IS rather than where to look for it.
const lockQuery = `
SELECT c.relname, l.mode, COALESCE(a.query, ''), NOT l.granted,
       EXISTS (
         SELECT 1 FROM pg_locks w
         WHERE NOT w.granted AND w.relation = l.relation AND w.pid <> l.pid
           AND w.database = l.database
       )
FROM pg_locks l
JOIN pg_class c ON c.oid = l.relation
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_stat_activity a ON a.pid = l.pid
WHERE l.pid <> pg_backend_pid()
  AND l.pid <> ALL($1::int[])
  AND l.locktype = 'relation'
  AND (l.database = 0 OR l.database = (
        SELECT d.oid FROM pg_database d WHERE d.datname = current_database()))
  AND left(n.nspname, $4::int) <> $5::text
  AND n.nspname <> 'information_schema'
  AND left(c.relname, $2::int) <> $3::text`

func (s *sampler) sample(ctx context.Context, every time.Duration) {
	rows, err := s.conn.Query(ctx, lockQuery, s.exclude,
		len(bookkeepingPrefix), bookkeepingPrefix,
		len(systemSchemaPrefix), systemSchemaPrefix)
	if err != nil {
		s.mu.Lock()
		// Keep the first error. A cancelled context at the end of the run
		// produces one every tick and the first is the informative one.
		if s.err == nil {
			s.err = err
		}
		s.mu.Unlock()
		return
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for rows.Next() {
		var table, mode, query string
		var waiting, contended bool
		if err := rows.Scan(&table, &mode, &query, &waiting, &contended); err != nil {
			return
		}
		hold, ok := s.holds[table]
		if !ok {
			hold = &LockHold{Table: table}
			s.holds[table] = hold
		}
		if lockStrength[mode] > lockStrength[hold.Mode] {
			hold.Mode = mode
		}
		if contended || waiting {
			hold.Blocking = true
		}
		if q := strings.TrimSpace(query); q != "" && hold.Statement == "" {
			hold.Statement = normalise(q)
		}
		// One interval per table per sample, not per row: a table appears once
		// for every mode its session holds on it.
		if !seen[table] {
			seen[table] = true
			hold.HeldMS += float64(every) / float64(time.Millisecond)
		}
	}
}

func (s *sampler) result() []LockHold {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LockHold, 0, len(s.holds))
	for _, h := range s.holds {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		if lockStrength[out[i].Mode] != lockStrength[out[j].Mode] {
			return lockStrength[out[i].Mode] > lockStrength[out[j].Mode]
		}
		return out[i].HeldMS > out[j].HeldMS
	})
	return out
}

// backendPID asks a connection which backend it is, so the sampler can watch
// that one and ignore every other session on the database.
func backendPID(ctx context.Context, conn *pgx.Conn) (int32, error) {
	var pid int32
	err := conn.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid)
	return pid, err
}
