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

const lockQuery = `
SELECT c.relname, l.mode, COALESCE(a.query, ''), NOT l.granted,
       EXISTS (
         SELECT 1 FROM pg_locks w
         WHERE NOT w.granted AND w.relation = l.relation AND w.pid <> l.pid
       )
FROM pg_locks l
JOIN pg_class c ON c.oid = l.relation
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_stat_activity a ON a.pid = l.pid
WHERE l.pid <> pg_backend_pid()
  AND l.pid <> ALL($1::int[])
  AND l.locktype = 'relation'
  AND n.nspname NOT IN ('pg_catalog','information_schema')`

func (s *sampler) sample(ctx context.Context, every time.Duration) {
	rows, err := s.conn.Query(ctx, lockQuery, s.exclude)
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
