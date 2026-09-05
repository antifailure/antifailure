package env

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/lease"
	"github.com/antifailure/antifailure/engine/internal/lock"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/reaper"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// ReapLock is the lock a sweep holds. Named rather than derived from an
// environment, because a sweep is about the machine and not about any one
// environment, and because two sweeps racing would both try to tear the same
// environments down.
const ReapLock = "reaper"

// ErrInUse reports an environment a sweep did not destroy because something
// was running against it.
//
// The lifetime is not extended by this: the environment is expired and stays
// expired, and the next sweep after the command finishes will take it. It is
// a deferral of one sweep and not a reprieve, which is the difference between
// "do not destroy the environment underneath a running af test" and "an
// environment stays alive as long as somebody keeps touching it".
var ErrInUse = errors.New("something is running against this environment")

// Deferred is one environment a sweep passed over.
type Deferred struct {
	// EnvID is the environment.
	EnvID string
	// ExpiresAt is when its lifetime ended.
	ExpiresAt time.Time
	// Holder is the command that is holding it, as the lock records it.
	Holder string
	// PID is the process holding it.
	PID int
}

// ReapResult is one sweep of this machine.
type ReapResult struct {
	reaper.Result
	// Deferred is every environment that was expired and in use.
	Deferred []Deferred
	// Teardowns is what each destroyed environment left behind, keyed by
	// environment, so a caller can report a pending resource against the
	// environment it belonged to rather than as one machine-wide number.
	Teardowns map[string]*Teardown
}

// Reap destroys every environment on this machine whose lifetime has ended.
//
// The inventory is the source of truth, not a registry. `af env` has read the
// daemon rather than a registry since it existed, for the reason stated there:
// a registry can be wrong, and a list that disagrees with reality is worse than
// no list. The same applies with more force here, because this one destroys
// what it finds.
//
// What it will not do, and each of these is a decision rather than an
// omission:
//
//   - it does not destroy an environment whose resources state no lifetime,
//     which is everything created by a release before this one;
//   - it does not destroy an environment something is running against. That is
//     the answer to "silently destroying somebody's debugging session is a bad
//     product": a sweep that arrives while af test is running defers, and the
//     environment is taken by the next one;
//   - it does not read the lifetime out of the manifest it was run with. A
//     repository whose ttl is two hours cannot destroy another project's week
//     long environment on the same machine.
//
// A lease from af env extend overrides what the resources say, in both
// directions, bounded by the ceiling the lease store enforces.
func (o *Orchestrator) Reap(ctx context.Context, dryRun bool) (*ReapResult, error) {
	s, err := o.openLocking(ctx, "af env reap", ReapLock)
	if err != nil {
		return nil, err
	}
	defer s.close()
	ctx = s.tel.StartCommand(ctx, "af env reap")

	inventory, err := s.runtime.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	leases, err := lease.NewStore(s.db, o.opts.Clock).Expiries(ctx)
	if err != nil {
		return nil, err
	}

	now := o.opts.Clock.Now().UTC()
	if dryRun {
		// Planned and reported, and nothing is destroyed. The same predicate
		// as the real sweep, deliberately: a dry run that computed its list a
		// different way would be reassurance about something other than what
		// is about to happen.
		plan := reaper.Plan(inventory, leases, now)
		out := &ReapResult{Teardowns: map[string]*Teardown{}}
		out.Scanned = countEnvironments(inventory)
		for _, e := range plan {
			out.Outcomes = append(out.Outcomes, reaper.Outcome{Expired: e})
		}
		return out, nil
	}

	w := &sweeper{
		o: o, s: s, result: &ReapResult{Teardowns: map[string]*Teardown{}},
		stateDir: filepath.Join(o.opts.Root, StateDir),
		leases:   lease.NewStore(s.db, o.opts.Clock),
	}
	w.result.Result = reaper.Sweep(ctx, inventory, leases, now, w)
	return w.result, nil
}

// countEnvironments is how many environments the inventory holds, expired or
// not. It matches what Sweep reports for the same inventory, so a dry run and
// a real sweep say the same thing about the machine.
func countEnvironments(resources []provider.Resource) int {
	seen := map[string]bool{}
	for _, res := range resources {
		if res.EnvID != "" {
			seen[res.EnvID] = true
		}
	}
	return len(seen)
}

// sweeper is the reaper's Destroyer, backed by a real teardown.
type sweeper struct {
	o        *Orchestrator
	s        *session
	stateDir string
	leases   *lease.Store
	result   *ReapResult
}

// Destroy removes one expired environment.
//
// The environment's own lock is taken first and held for the whole teardown.
// That is what stops a sweep from removing an environment's network out from
// under an af up that is halfway through creating containers on it: the lock
// is the same one every other command on that environment takes, so either the
// command finishes and the sweep proceeds, or the sweep defers.
func (w *sweeper) Destroy(ctx context.Context, envID string) (int, error) {
	path := filepath.Join(w.stateDir, envID+".lock")
	l, err := lock.Acquire(path, w.o.opts.Clock, "af env reap")
	if err != nil {
		holder, _, readErr := lock.Holder(path)
		d := Deferred{EnvID: envID}
		if readErr == nil {
			d.Holder, d.PID = holder.Command, holder.PID
		}
		w.result.Deferred = append(w.result.Deferred, d)
		return 0, fmt.Errorf("%w: %w", ErrInUse, err)
	}
	defer func() { _ = l.Release() }()

	w.o.eventFor(w.s, envID, events.EnvDestroying, "removing "+envID+", which has expired")
	td := w.o.teardown(ctx, w.s, envID)
	w.result.Teardowns[envID] = td
	w.o.eventFor(w.s, envID, events.EnvDestroyed, "removed "+envID,
		events.F("removed", td.Removed), events.F("pending", len(td.Pending)),
		events.F("reason", "expired"))

	// After the teardown and not before, so that an environment whose removal
	// failed keeps the extension somebody paid attention to take out. Dropped
	// at all so that a later branch reusing this environment id does not
	// inherit it.
	if err := w.leases.Drop(ctx, envID); err != nil {
		td.Pending = append(td.Pending, provider.PendingResource{
			Kind: "lease", ID: envID, Reason: err.Error(),
		})
	}
	return td.Removed, nil
}

// Extend moves one environment's expiry, up to the ceiling runtime.max_ttl
// fixes.
//
// The environment has to exist on this machine. Extending one that does not is
// refused rather than recorded, because a lease for an environment nothing has
// heard of is a row that will never be read and never cleaned up, and because
// the most likely reason for asking is a typo in the name.
//
// The ceiling is measured from the environment's own creation time, which is
// read off its oldest resource here rather than taken from the clock. See
// lease.Store.Extend for why that is the difference between a bound and a
// treadmill.
func (o *Orchestrator) Extend(
	ctx context.Context, envID string, until time.Time, reason string,
) (lease.Lease, error) {
	s, err := o.openLocking(ctx, "af env extend", ReapLock)
	if err != nil {
		return lease.Lease{}, err
	}
	defer s.close()

	inventory, err := s.runtime.Inventory(ctx)
	if err != nil {
		return lease.Lease{}, err
	}
	created, found := createdAt(inventory, envID)
	if !found {
		return lease.Lease{}, fmt.Errorf(
			"%s is not on this machine. 'af env list' shows what is", envID)
	}
	return lease.NewStore(s.db, o.opts.Clock).
		Extend(ctx, envID, until, created, o.maxTTL(), reason)
}

// createdAt is when an environment's oldest resource was created.
//
// The oldest and not the newest, because the ceiling is on the environment's
// whole life. Taking the newest would push the ceiling forward every time a
// container was replaced, which is the unbounded behaviour again by another
// route.
func createdAt(resources []provider.Resource, envID string) (time.Time, bool) {
	var oldest time.Time
	found := false
	for _, res := range resources {
		if res.EnvID != envID || res.CreatedAt.IsZero() {
			continue
		}
		if !found || res.CreatedAt.Before(oldest) {
			oldest, found = res.CreatedAt, true
		}
	}
	return oldest.UTC(), found
}

// maxTTL is runtime.max_ttl: the furthest an environment may ever be extended
// to, measured from when it was created.
//
// Falls back to the normalized default rather than to zero, because zero would
// mean every environment was born already past its ceiling and af env extend
// could only ever refuse. A manifest that reached here unnormalized is a bug,
// and the honest failure for it is the documented default rather than a
// command that never works.
func (o *Orchestrator) maxTTL() time.Duration {
	fallback, err := manifest.ParseDuration(manifest.DefaultMaxTTL)
	if err != nil {
		// DefaultMaxTTL is a constant in this repository and a test parses it.
		fallback = 168 * time.Hour
	}
	m := o.opts.Manifest
	if m == nil || m.Runtime == nil || m.Runtime.MaxTTL == "" {
		return fallback
	}
	d, err := manifest.ParseDuration(m.Runtime.MaxTTL)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}
