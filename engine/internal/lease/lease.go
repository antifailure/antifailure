// Package lease records extensions to an environment's stated lifetime.
//
// It exists because of the one question a TTL has to answer before it is a
// product rather than a timer: what happens to an environment somebody is
// still using when its lifetime ends.
//
// Two answers are wrong. Destroying it silently takes away a debugging session
// somebody is in the middle of, and they lose work to a policy they did not
// know applied. Letting anybody push the expiry back indefinitely means there
// is no lifetime at all, only a chore nobody does, which is the state this
// whole area was in while nothing read runtime.ttl.
//
// The answer here is a lease with a ceiling. Anybody may extend an environment
// they are using, as often as they like, and each extension records a reason.
// No extension may take the environment past ceiling_at, which is fixed at the
// first extension from the environment's own creation time plus
// runtime.max_ttl and never moves afterwards. So the lifetime is genuinely
// extendable and genuinely bounded, and the bound is a property of the
// environment rather than of whoever is asking or of when they ask.
package lease

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	statedb "github.com/antifailure/antifailure/engine/internal/state"
)

// Lease is one environment's extended lifetime.
type Lease struct {
	// EnvID is the environment the extension applies to.
	EnvID string
	// ExpiresAt is the lifetime as extended.
	ExpiresAt time.Time
	// CeilingAt is the furthest this environment may ever be extended to.
	CeilingAt time.Time
	// Reason is what the person typed. Empty when they did not say.
	Reason string
	// CreatedAt is when the first extension was taken.
	CreatedAt time.Time
	// UpdatedAt is when the most recent one was.
	UpdatedAt time.Time
}

// AtCeiling reports that this lease cannot be extended any further.
func (l Lease) AtCeiling() bool { return !l.ExpiresAt.Before(l.CeilingAt) }

// Store reads and writes leases in the engine's own state database.
//
// The state database and not a file beside it, because a lease is read by the
// reaper at the same time as af up may be writing one, and the database is
// already the thing in this engine that handles two writers.
type Store struct {
	db    *statedb.DB
	clock clock.Clock
}

// NewStore builds a store over an open state database.
func NewStore(db *statedb.DB, c clock.Clock) *Store { return &Store{db: db, clock: c} }

// ErrPastCeiling is returned when an extension would take an environment past
// the furthest point it may ever reach.
//
// Returned alongside the lease that was granted rather than instead of it: the
// extension still happens, up to the ceiling. Silently granting less time than
// somebody asked for is how they come back to find the environment gone at a
// time they believed they had moved, so they are told the ceiling instead.
var ErrPastCeiling = errors.New("the extension would pass the environment's maximum lifetime")

// Extend moves an environment's expiry, up to its ceiling.
//
// createdAt is the environment's own creation time, read off its resources
// rather than off the clock, and maxTTL is runtime.max_ttl. Together they fix
// the ceiling, and they are consulted only when the ceiling is first set: an
// environment extended once has a ceiling for the rest of its life, so a later
// extension run from a checkout whose manifest says something more generous
// cannot raise it.
//
// Measuring the ceiling from creation and not from now is the part that makes
// it a bound. From now, an environment extended late in its life would be
// entitled to a longer total lifetime than one extended early, and repeated
// extensions would walk the ceiling forward with them, which is the unbounded
// behaviour this is here to prevent.
func (s *Store) Extend(
	ctx context.Context,
	envID string,
	until time.Time,
	createdAt time.Time,
	maxTTL time.Duration,
	reason string,
) (Lease, error) {
	if envID == "" {
		return Lease{}, fmt.Errorf("lease: an extension needs an environment")
	}
	now := s.clock.Now().UTC()
	until = until.UTC()

	var out Lease
	var clamped bool
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		existing, found, err := readTx(ctx, tx, envID)
		if err != nil {
			return err
		}

		ceiling := existing.CeilingAt
		created := now
		if found {
			created = existing.CreatedAt
		} else {
			ceiling = createdAt.UTC().Add(maxTTL)
		}

		granted := until
		clamped = false
		if granted.After(ceiling) {
			granted, clamped = ceiling, true
		}

		out = Lease{
			EnvID: envID, ExpiresAt: granted, CeilingAt: ceiling,
			Reason: reason, CreatedAt: created, UpdatedAt: now,
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO env_leases (env, expires_at, ceiling_at, reason, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(env) DO UPDATE SET
    expires_at = excluded.expires_at,
    reason     = excluded.reason,
    updated_at = excluded.updated_at`,
			envID, granted.Unix(), ceiling.Unix(), reason,
			created.UnixMilli(), now.UnixMilli())
		return err
	})
	if err != nil {
		return Lease{}, fmt.Errorf("lease: extend %s: %w", envID, err)
	}
	if clamped {
		return out, ErrPastCeiling
	}
	return out, nil
}

// Get reads one environment's lease.
func (s *Store) Get(ctx context.Context, envID string) (Lease, bool, error) {
	var out Lease
	var found bool
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		out, found, err = readTx(ctx, tx, envID)
		return err
	})
	if err != nil {
		return Lease{}, false, fmt.Errorf("lease: read %s: %w", envID, err)
	}
	return out, found, nil
}

// Drop removes an environment's lease.
//
// Called on teardown, so that an environment id reused by a later branch of
// the same name does not inherit an extension taken out for the one before it.
// Dropping a lease that is not there is not an error: teardown runs on
// environments that were never extended, which is most of them.
func (s *Store) Drop(ctx context.Context, envID string) error {
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM env_leases WHERE env = ?`, envID)
		return err
	})
	if err != nil {
		return fmt.Errorf("lease: drop %s: %w", envID, err)
	}
	return nil
}

// Expiries is every lease in the shape the reaper wants: an environment id to
// the expiry that overrides whatever its resources carry.
//
// A lease past its own ceiling is read as the ceiling. The ceiling is applied
// here as well as when the lease is taken because this is the read the
// destruction decision is made from, and a bound enforced only on the way in
// is a bound that a hand-edited row walks straight past.
func (s *Store) Expiries(ctx context.Context) (map[string]time.Time, error) {
	all, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(all))
	for _, l := range all {
		at := l.ExpiresAt
		if at.After(l.CeilingAt) {
			at = l.CeilingAt
		}
		out[l.EnvID] = at
	}
	return out, nil
}

// All is every lease, ordered by environment id so that two reads of one
// database produce the same list in the same order.
func (s *Store) All(ctx context.Context) ([]Lease, error) {
	var out []Lease
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
SELECT env, expires_at, ceiling_at, reason, created_at, updated_at FROM env_leases`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		out = nil
		for rows.Next() {
			l, err := scan(rows)
			if err != nil {
				return err
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("lease: list: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EnvID < out[j].EnvID })
	return out, nil
}

// scanner is what both a *sql.Row and a *sql.Rows satisfy.
type scanner interface{ Scan(dest ...any) error }

func scan(sc scanner) (Lease, error) {
	var l Lease
	var expires, ceiling, created, updated int64
	if err := sc.Scan(&l.EnvID, &expires, &ceiling, &l.Reason, &created, &updated); err != nil {
		return Lease{}, err
	}
	l.ExpiresAt = time.Unix(expires, 0).UTC()
	l.CeilingAt = time.Unix(ceiling, 0).UTC()
	l.CreatedAt = time.UnixMilli(created).UTC()
	l.UpdatedAt = time.UnixMilli(updated).UTC()
	return l, nil
}

func readTx(ctx context.Context, tx *sql.Tx, envID string) (Lease, bool, error) {
	row := tx.QueryRowContext(ctx, `
SELECT env, expires_at, ceiling_at, reason, created_at, updated_at
FROM env_leases WHERE env = ?`, envID)
	l, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Lease{}, false, nil
	}
	if err != nil {
		return Lease{}, false, err
	}
	return l, true, nil
}
