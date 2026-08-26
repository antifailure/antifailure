// Package journal records every external resource before it is created, so
// that a crash at any instant leaves the system recoverable.
//
// The rule the whole product rests on: everything that is created has a
// recorded, compensating deletion. Before the engine calls a provider to make
// a database branch, a container, a namespace, a DNS record, or a webhook
// registration, it writes an intent here. After the call succeeds it commits
// the record with the provider's identifier. Teardown, and crash recovery,
// replay the records in reverse order and delete anything that exists.
//
// Two design choices make replay survive an upgrade and a partial failure.
//
// The compensating action is stored as data, never as code: a provider, a
// resource kind, an identifier, and a small parameter map. A binary built six
// months later can replay a journal written today, because it looks the action
// up in its own registry rather than deserialising a closure.
//
// Every create uses a deterministic idempotency key derived from the
// environment identifier and the resource's role. A retry after a timeout,
// where the provider may or may not have created the resource, finds the
// existing intent instead of creating a second one. Providers without native
// idempotency get a lookup before create using a name derived from the same
// key.
package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/state"
)

// State is where a record is in its lifecycle.
type State string

const (
	// StateIntent means the create was recorded but has not been confirmed.
	// A resource in this state may or may not exist at the provider, so replay
	// must attempt deletion and treat "already gone" as success.
	StateIntent State = "intent"
	// StateCommitted means the resource exists and its identifier is recorded.
	StateCommitted State = "committed"
	// StateCompensated means the resource has been deleted.
	StateCompensated State = "compensated"
	// StateFailed means compensation was attempted and did not succeed. The
	// record stays so that a later teardown retries it.
	StateFailed State = "failed"
)

// Kind names a resource type. Kinds are stable strings because a journal
// written by an older binary is replayed by a newer one.
type Kind string

const (
	KindDatabaseBranch Kind = "database.branch"
	KindGoldenVersion  Kind = "golden.version"
	KindContainer      Kind = "container"
	KindVolume         Kind = "volume"
	KindNetwork        Kind = "network"
	KindImage          Kind = "image"
	KindZFSDataset     Kind = "zfs.dataset"
	KindNamespace      Kind = "k8s.namespace"
	KindDNSRecord      Kind = "dns.record"
	KindStorageObject  Kind = "storage.object"
	KindWebhook        Kind = "webhook.registration"
	KindSandboxObject  Kind = "sandbox.object"
	KindRunnerProcess  Kind = "runner.process"
)

// Record is one journalled resource.
type Record struct {
	ID         int64
	Env        string
	Provider   string
	Kind       Kind
	IdemKey    string
	ExternalID string
	State      State
	// Compensation carries what the deleter needs beyond the identifier: a
	// region, a project, a parent resource. It is data, never code.
	Compensation map[string]string
	Attempts     int
	LastError    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Deleter deletes one kind of resource for one provider.
//
// Deleting something that is already gone must succeed. Replay runs after
// crashes and after partial teardowns, so "not found" is the expected case at
// least as often as "deleted".
type Deleter interface {
	// Delete removes the resource. A resource that does not exist is success.
	Delete(ctx context.Context, rec Record) error
}

// DeleterFunc adapts a function into a Deleter.
type DeleterFunc func(ctx context.Context, rec Record) error

// Delete calls the function.
func (f DeleterFunc) Delete(ctx context.Context, rec Record) error { return f(ctx, rec) }

// Registry maps a provider and kind to the deleter that compensates it.
//
// Replay looks the action up here rather than deserialising it, which is what
// lets a newer binary tear down resources an older one created.
type Registry struct {
	deleters map[string]Deleter
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{deleters: map[string]Deleter{}} }

// Register associates a deleter with a provider and kind.
func (r *Registry) Register(provider string, kind Kind, d Deleter) {
	r.deleters[provider+"/"+string(kind)] = d
}

// Lookup returns the deleter for a record, if one is registered.
func (r *Registry) Lookup(provider string, kind Kind) (Deleter, bool) {
	d, ok := r.deleters[provider+"/"+string(kind)]
	return d, ok
}

// Journal reads and writes resource records.
type Journal struct {
	db    *state.DB
	clock clock.Clock
	bus   *events.Bus
}

// New returns a journal backed by the state database.
func New(db *state.DB, c clock.Clock, bus *events.Bus) *Journal {
	return &Journal{db: db, clock: c, bus: bus}
}

// Intent records that a resource is about to be created.
//
// It returns the record. If an intent for the same idempotency key already
// exists, the existing record is returned instead of a new one, so that a
// retry after a timeout cannot create a duplicate resource.
func (j *Journal) Intent(ctx context.Context, env, provider string, kind Kind, idemKey string, comp map[string]string) (Record, error) {
	if idemKey == "" {
		return Record{}, fmt.Errorf("journal: an intent needs an idempotency key (%s/%s)", provider, kind)
	}
	now := j.clock.Now().UTC()

	if existing, ok, err := j.byIdem(ctx, provider, kind, idemKey); err != nil {
		return Record{}, err
	} else if ok {
		return existing, nil
	}

	compJSON, err := json.Marshal(orEmpty(comp))
	if err != nil {
		return Record{}, fmt.Errorf("journal: encode the compensation for %s/%s: %w", provider, kind, err)
	}

	var id int64
	err = j.db.Tx(ctx, func(tx *sql.Tx) error {
		// The unique index covers live records only, so a compensated record
		// with the same key does not block a fresh intent. Re-checking inside
		// the transaction closes the window between the lookup above and here.
		res, err := tx.ExecContext(ctx, `
INSERT INTO journal (env, provider, kind, idem_key, state, compensation, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			env, provider, string(kind), idemKey, string(StateIntent), string(compJSON),
			now.UnixMilli(), now.UnixMilli())
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		// A concurrent intent for the same key wins the race; return it.
		if isUniqueViolation(err) {
			if existing, ok, lookupErr := j.byIdem(ctx, provider, kind, idemKey); lookupErr == nil && ok {
				return existing, nil
			}
		}
		return Record{}, fmt.Errorf("journal: record the intent for %s/%s: %w", provider, kind, err)
	}

	rec := Record{
		ID: id, Env: env, Provider: provider, Kind: kind, IdemKey: idemKey,
		State: StateIntent, Compensation: orEmpty(comp), CreatedAt: now, UpdatedAt: now,
	}
	return rec, nil
}

// Commit records that the resource now exists, with the provider's identifier.
func (j *Journal) Commit(ctx context.Context, id int64, externalID string) error {
	now := j.clock.Now().UTC()
	res, err := j.db.SQL().ExecContext(ctx,
		"UPDATE journal SET state = ?, external_id = ?, updated_at = ? WHERE id = ?",
		string(StateCommitted), externalID, now.UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("journal: commit record %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("journal: commit record %d: no such record", id)
	}
	if j.bus != nil {
		rec, _, lookupErr := j.byID(ctx, id)
		if lookupErr == nil {
			j.bus.Info(rec.Env, events.ResourceCreated, string(rec.Kind),
				events.F("provider", rec.Provider),
				events.F("kind", string(rec.Kind)),
				events.F("id", externalID))
		}
	}
	return nil
}

// Compensated records that the resource has been deleted.
func (j *Journal) Compensated(ctx context.Context, id int64) error {
	now := j.clock.Now().UTC()
	rec, ok, err := j.byID(ctx, id)
	if err != nil {
		return err
	}
	if _, err := j.db.SQL().ExecContext(ctx,
		"UPDATE journal SET state = ?, updated_at = ? WHERE id = ?",
		string(StateCompensated), now.UnixMilli(), id); err != nil {
		return fmt.Errorf("journal: mark record %d compensated: %w", id, err)
	}
	if ok && j.bus != nil {
		j.bus.Info(rec.Env, events.ResourceDeleted, string(rec.Kind),
			events.F("provider", rec.Provider),
			events.F("kind", string(rec.Kind)),
			events.F("id", rec.ExternalID))
	}
	return nil
}

// Failed records that compensation was attempted and failed. The record stays
// live so that the next teardown retries it.
func (j *Journal) Failed(ctx context.Context, id int64, cause error) error {
	now := j.clock.Now().UTC()
	msg := ""
	if cause != nil {
		msg = truncate(cause.Error(), 1000)
	}
	if _, err := j.db.SQL().ExecContext(ctx,
		"UPDATE journal SET state = ?, attempts = attempts + 1, last_error = ?, updated_at = ? WHERE id = ?",
		string(StateFailed), msg, now.UnixMilli(), id); err != nil {
		return fmt.Errorf("journal: mark record %d failed: %w", id, err)
	}
	return nil
}

// Pending returns the live records for an environment, newest first, which is
// the order compensation runs in: a branch is deleted before the golden it
// came from, a container before the network it joined.
//
// An empty environment returns every live record, which is what af down --all
// and crash recovery use.
func (j *Journal) Pending(ctx context.Context, env string) ([]Record, error) {
	q := `SELECT id, env, provider, kind, idem_key, external_id, state, compensation,
                 attempts, last_error, created_at, updated_at
          FROM journal WHERE state != ?`
	args := []any{string(StateCompensated)}
	if env != "" {
		q += " AND env = ?"
		args = append(args, env)
	}
	q += " ORDER BY id DESC"
	return j.query(ctx, q, args...)
}

// All returns every record, including compensated ones, oldest first.
func (j *Journal) All(ctx context.Context, env string) ([]Record, error) {
	q := `SELECT id, env, provider, kind, idem_key, external_id, state, compensation,
                 attempts, last_error, created_at, updated_at FROM journal`
	var args []any
	if env != "" {
		q += " WHERE env = ?"
		args = append(args, env)
	}
	q += " ORDER BY id ASC"
	return j.query(ctx, q, args...)
}

// ReplayResult reports what a replay did.
type ReplayResult struct {
	// Compensated is how many resources were deleted.
	Compensated int
	// Failed is how many could not be deleted and remain recorded.
	Failed int
	// Skipped is how many had no registered deleter in this binary.
	Skipped []Record
	// Errors holds one error per failed record, in record order.
	Errors []error
}

// Clean reports whether the replay left nothing behind.
func (r ReplayResult) Clean() bool { return r.Failed == 0 && len(r.Skipped) == 0 }

// Replay deletes every live resource for an environment, newest first.
//
// It is idempotent. Running it twice is safe, running it after a partial
// teardown finishes the job, and running it after a crash between intent and
// create deletes a resource that may not exist, which every deleter treats as
// success.
//
// Replay never stops at the first failure. A provider being unreachable must
// not prevent the other twelve resources from being cleaned up, so each record
// is attempted, failures are recorded for a later retry, and the result says
// what is left.
func (j *Journal) Replay(ctx context.Context, env string, reg *Registry) (ReplayResult, error) {
	recs, err := j.Pending(ctx, env)
	if err != nil {
		return ReplayResult{}, err
	}
	var out ReplayResult
	for _, rec := range recs {
		// A cancelled context stops the replay, leaving the remaining records
		// live so that the next run picks them up.
		if ctxErr := ctx.Err(); ctxErr != nil {
			out.Errors = append(out.Errors, ctxErr)
			return out, ctxErr
		}
		d, ok := reg.Lookup(rec.Provider, rec.Kind)
		if !ok {
			// A record this binary has no deleter for is left alone rather
			// than dropped, so that a downgrade does not orphan resources.
			out.Skipped = append(out.Skipped, rec)
			continue
		}
		if err := d.Delete(ctx, rec); err != nil {
			out.Failed++
			out.Errors = append(out.Errors, fmt.Errorf("%s %s (%s): %w",
				rec.Provider, rec.Kind, displayID(rec), err))
			if markErr := j.Failed(ctx, rec.ID, err); markErr != nil {
				out.Errors = append(out.Errors, markErr)
			}
			continue
		}
		if err := j.Compensated(ctx, rec.ID); err != nil {
			out.Errors = append(out.Errors, err)
			out.Failed++
			continue
		}
		out.Compensated++
	}
	return out, nil
}

// PendingError returns the AF-RUN-030 error for a replay that left work, or
// nil when the replay was clean.
func (r ReplayResult) PendingError() error {
	left := r.Failed + len(r.Skipped)
	if left == 0 {
		return nil
	}
	return aferrors.Coded(aferrors.AFRUN030, "count", fmt.Sprint(left))
}

// Summary renders a short human readable description of what is left.
func (r ReplayResult) Summary() string {
	var parts []string
	if r.Compensated > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", r.Compensated))
	}
	if r.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", r.Failed))
	}
	if len(r.Skipped) > 0 {
		kinds := map[string]int{}
		for _, s := range r.Skipped {
			kinds[s.Provider+"/"+string(s.Kind)]++
		}
		keys := make([]string, 0, len(kinds))
		for k := range kinds {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts = append(parts, fmt.Sprintf("%d with no deleter in this build (%s)",
			len(r.Skipped), strings.Join(keys, ", ")))
	}
	if len(parts) == 0 {
		return "nothing to do"
	}
	return strings.Join(parts, ", ")
}

func displayID(rec Record) string {
	if rec.ExternalID != "" {
		return rec.ExternalID
	}
	return "idem " + rec.IdemKey
}

func (j *Journal) byIdem(ctx context.Context, provider string, kind Kind, idemKey string) (Record, bool, error) {
	recs, err := j.query(ctx, `
SELECT id, env, provider, kind, idem_key, external_id, state, compensation,
       attempts, last_error, created_at, updated_at
FROM journal WHERE provider = ? AND kind = ? AND idem_key = ? AND state != ?`,
		provider, string(kind), idemKey, string(StateCompensated))
	if err != nil || len(recs) == 0 {
		return Record{}, false, err
	}
	return recs[0], true, nil
}

func (j *Journal) byID(ctx context.Context, id int64) (Record, bool, error) {
	recs, err := j.query(ctx, `
SELECT id, env, provider, kind, idem_key, external_id, state, compensation,
       attempts, last_error, created_at, updated_at FROM journal WHERE id = ?`, id)
	if err != nil || len(recs) == 0 {
		return Record{}, false, err
	}
	return recs[0], true, nil
}

func (j *Journal) query(ctx context.Context, q string, args ...any) ([]Record, error) {
	rows, err := j.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("journal: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Record
	for rows.Next() {
		var (
			r                    Record
			kind, st, compJSON   string
			createdMs, updatedMs int64
		)
		if err := rows.Scan(&r.ID, &r.Env, &r.Provider, &kind, &r.IdemKey, &r.ExternalID,
			&st, &compJSON, &r.Attempts, &r.LastError, &createdMs, &updatedMs); err != nil {
			return nil, fmt.Errorf("journal: scan: %w", err)
		}
		r.Kind, r.State = Kind(kind), State(st)
		r.CreatedAt = time.UnixMilli(createdMs).UTC()
		r.UpdatedAt = time.UnixMilli(updatedMs).UTC()
		// A record whose compensation JSON is unreadable still has to be
		// compensable: the identifier and kind are what the deleter needs, and
		// losing them because a parameter map was corrupted would strand the
		// resource forever.
		r.Compensation = map[string]string{}
		if compJSON != "" {
			_ = json.Unmarshal([]byte(compJSON), &r.Compensation)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("journal: iterate: %w", err)
	}
	return out, nil
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// The pure Go driver reports constraint failures in the message. Matching
	// on it is unpleasant, but the alternative is importing the driver's error
	// type into every caller.
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// ErrNoDeleter is returned by a registry lookup helper for callers that want
// an error rather than a boolean.
var ErrNoDeleter = errors.New("journal: no deleter is registered for this provider and kind")
