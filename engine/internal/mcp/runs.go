package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/state"
)

// Status is where a run has got to.
//
// Two terminal states rather than one, because "the experiment ran and
// reported a verdict" and "the experiment could not be run" are different
// facts and a caller has to tell them apart. A finished run carries a verdict;
// a failed one carries a fault and its verdict is always INCONCLUSIVE.
type Status string

const (
	// StatusQueued is accepted and not yet started.
	StatusQueued Status = "queued"
	// StatusRunning is in progress.
	StatusRunning Status = "running"
	// StatusFinished is an experiment that completed and produced a verdict.
	StatusFinished Status = "finished"
	// StatusFailed is an experiment that could not be completed. Its verdict
	// is INCONCLUSIVE, never PASS.
	StatusFailed Status = "failed"
	// StatusCancelled is an experiment stopped on request. Also INCONCLUSIVE:
	// an experiment that did not finish proves nothing about the change.
	StatusCancelled Status = "cancelled"
)

// terminal reports whether no further transition is possible.
func (s Status) terminal() bool {
	return s == StatusFinished || s == StatusFailed || s == StatusCancelled
}

// Verdict is the vocabulary a model reads.
//
// Three words, mapped from the engine's five. The engine's own vocabulary is
// richer and is reported alongside this one under native_verdict, so nothing
// is lost; this exists because a caller deciding whether to merge needs the
// question answered in the terms it can act on, and because collapsing the
// five must happen in exactly one place rather than in each tool.
type Verdict string

const (
	// VerdictPass is an experiment that ran completely and found nothing that
	// the project's policy says should stop a merge.
	VerdictPass Verdict = "PASS"
	// VerdictFail is an experiment that ran and found something the project's
	// policy says should stop a merge.
	VerdictFail Verdict = "FAIL"
	// VerdictInconclusive is everything else: an experiment that could not be
	// completed, could not be evaluated, or was cut short.
	//
	// It is not a softer PASS. An incomplete experiment says nothing about
	// the change, and reporting it as a pass would be the single most
	// damaging thing this server could do, because the whole reason a model
	// calls it is to be told when it is wrong.
	VerdictInconclusive Verdict = "INCONCLUSIVE"
)

// verdictFor maps an engine verdict onto the caller facing one.
//
// Total by construction: the default arm catches anything the report package
// adds later, and it maps to INCONCLUSIVE rather than to PASS. A verdict this
// server does not recognise is a verdict it cannot vouch for, and the safe
// direction for an unknown is always the one that does not clear a merge.
func verdictFor(native string) Verdict {
	switch native {
	case report.VerdictPass:
		return VerdictPass
	case report.VerdictFail:
		return VerdictFail
	case report.VerdictWarn:
		// Warn means the run completed and every finding was below the
		// project's failure threshold, which is a pass by the project's own
		// policy. The findings are still reported in full.
		return VerdictPass
	case report.VerdictFlaky, report.VerdictBlocked, report.VerdictUnverified:
		return VerdictInconclusive
	default:
		return VerdictInconclusive
	}
}

// Run is one rehearsal, as stored.
type Run struct {
	ID            string
	Caller        string
	Project       string
	Tool          string
	IdemKey       string
	InputsSHA     string
	Status        Status
	Phase         string
	Verdict       Verdict
	NativeVerdict string
	// Result is the tool's result document as JSON, present once the run has
	// finished. Stored encoded rather than as a typed field because each tool
	// reports a different shape and the store has no business knowing them.
	Result []byte
	// ErrorCode and ErrorDetail describe why a failed run failed.
	ErrorCode   string
	ErrorDetail string
	// CancelRequested is set by cancel_rehearsal_run and read by the running
	// experiment. Durable rather than a channel, because the process that
	// serves the cancel may not be the process that started the run.
	CancelRequested bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Store is the durable home of every run this server has been asked for.
type Store struct {
	db    *state.DB
	clock clock.Clock
}

// NewStore wraps an open state database.
func NewStore(db *state.DB, c clock.Clock) *Store {
	if c == nil {
		c = clock.New()
	}
	return &Store{db: db, clock: c}
}

// canonicalSHA hashes the arguments of a call in a form two equal calls share.
//
// Equality here decides whether a reused idempotency key is a retry or a
// mistake, so the hash has to see through everything that does not change
// meaning. Object member order does not, which is why the arguments are
// re-encoded from the decoded value rather than hashed as they arrived: two
// callers that send the same fields in a different order, or with different
// whitespace, mean the same call and must get the same run.
//
// The idempotency key itself is not part of the hash. The key names the
// request; the hash says what the request was.
func canonicalSHA(tool string, args map[string]any) (string, error) {
	body, err := canonicalJSON(args)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(tool+"\x00"), body...))
	return hex.EncodeToString(sum[:]), nil
}

// canonicalJSON encodes a decoded JSON value with object members in sorted
// order at every depth.
//
// encoding/json already sorts the keys of a map[string]any, but it is sorted
// only at the top level of that map's own encoding and the guarantee is worth
// making explicit rather than relying on. Numbers arrive as json.Number and
// are written back as their original text, so 1.0 and 1 stay distinguishable,
// which is right: a caller that changed 1 to 1.0 changed nothing, but a
// caller that changed 1 to 1.5 changed the experiment, and treating the text
// as canonical is the conservative reading of both.
func canonicalJSON(v any) ([]byte, error) {
	var b strings.Builder
	if err := writeCanonical(&b, v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func writeCanonical(b *strings.Builder, v any) error {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			key, err := json.Marshal(k)
			if err != nil {
				return err
			}
			b.Write(key)
			b.WriteByte(':')
			if err := writeCanonical(b, t[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
		return nil
	case []any:
		b.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
		return nil
	case json.Number:
		b.WriteString(t.String())
		return nil
	default:
		enc, err := json.Marshal(t)
		if err != nil {
			return err
		}
		b.Write(enc)
		return nil
	}
}

// newRunID mints an unguessable identifier.
//
// Random rather than sequential, and that is a deliberate defence rather than
// tidiness. A run identifier is not an authorisation: every lookup checks the
// caller and the project as well as the id, so guessing one buys nothing. But
// a sequential id would also tell any caller how many runs every other caller
// has submitted, and there is no reason to publish that.
func newRunID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("minting a run id: %w", err)
	}
	return "run_" + hex.EncodeToString(raw[:]), nil
}

// Submit records a new run, or returns the existing one for a repeated key.
//
// The three outcomes are the whole idempotency contract. A key never seen
// before creates a run. The same key with the same canonical inputs returns
// the run already created, with created false, so a client that retried after
// a timeout gets the original experiment rather than a second one. The same
// key with different inputs is refused, because the caller has reused a key by
// mistake and answering with the first run would report one experiment's
// verdict as though it were another's.
func (s *Store) Submit(
	ctx context.Context, caller, project, tool, idemKey string, args map[string]any,
) (run Run, created bool, fault *Fault) {
	sum, err := canonicalSHA(tool, args)
	if err != nil {
		return Run{}, false, internalFault(err)
	}
	id, err := newRunID()
	if err != nil {
		return Run{}, false, internalFault(err)
	}
	now := s.clock.Now()

	txErr := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if idemKey != "" {
			existing, found, err := scanOne(tx.QueryRowContext(ctx,
				selectRunSQL+` WHERE caller = ? AND project = ? AND tool = ? AND idem_key = ?`,
				caller, project, tool, idemKey))
			if err != nil {
				return err
			}
			if found {
				if existing.InputsSHA != sum {
					return fieldFault(FaultIdempotencyConflict, "idempotency_key",
						"This key was already used for a call to %s with different arguments. "+
							"Use a new key for a different experiment, or repeat the original "+
							"arguments to poll the existing run.", tool)
				}
				run, created = existing, false
				return nil
			}
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO mcp_runs
    (id, caller, project, tool, idem_key, inputs_sha, status, phase,
     verdict, native_verdict, result, error_code, error_detail,
     cancel_requested, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', '', '', '', '', 0, ?, ?)`,
			id, caller, project, tool, idemKey, sum,
			string(StatusQueued), phaseAccepted,
			now.UnixMilli(), now.UnixMilli()); err != nil {
			return err
		}
		run = Run{
			ID: id, Caller: caller, Project: project, Tool: tool,
			IdemKey: idemKey, InputsSHA: sum, Status: StatusQueued,
			Phase: phaseAccepted, CreatedAt: now, UpdatedAt: now,
		}
		created = true
		return nil
	})
	if txErr != nil {
		return Run{}, false, asFault(txErr)
	}
	return run, created, nil
}

// Get returns one run, scoped to the caller and project that submitted it.
//
// The scoping is in the WHERE clause rather than in a check afterwards, which
// is what makes a cross project lookup indistinguishable from a lookup of
// something that does not exist. A caller must not be able to learn that
// another project's run id is real by observing a different refusal.
func (s *Store) Get(ctx context.Context, caller, project, id string) (Run, *Fault) {
	var run Run
	var found bool
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		run, found, err = scanOne(tx.QueryRowContext(ctx,
			selectRunSQL+` WHERE id = ? AND caller = ? AND project = ?`, id, caller, project))
		return err
	})
	if err != nil {
		return Run{}, asFault(err)
	}
	if !found {
		return Run{}, fieldFault(FaultRunNotFound, "run_id", "No such run.")
	}
	return run, nil
}

// Start moves a queued run to running.
func (s *Store) Start(ctx context.Context, id, phase string) error {
	return s.setStatus(ctx, id, StatusRunning, phase)
}

// Phase records progress without changing the status.
func (s *Store) Phase(ctx context.Context, id, phase string) error {
	now := s.clock.Now().UnixMilli()
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE mcp_runs SET phase = ?, updated_at = ? WHERE id = ? AND status = ?`,
			phase, now, id, string(StatusRunning))
		return err
	})
}

func (s *Store) setStatus(ctx context.Context, id string, st Status, phase string) error {
	now := s.clock.Now().UnixMilli()
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE mcp_runs SET status = ?, phase = ?, updated_at = ? WHERE id = ?`,
			string(st), phase, now, id)
		return err
	})
}

// Finish records a completed experiment and its verdict.
//
// The verdict is derived from the engine's, here, in one place. A tool hands
// over what the deterministic evaluator said and never chooses a word itself.
func (s *Store) Finish(ctx context.Context, id, nativeVerdict string, result any) error {
	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encoding the result of run %s: %w", id, err)
	}
	now := s.clock.Now().UnixMilli()
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
UPDATE mcp_runs SET status = ?, phase = ?, verdict = ?, native_verdict = ?,
                    result = ?, updated_at = ?
WHERE id = ?`,
			string(StatusFinished), phaseComplete,
			string(verdictFor(nativeVerdict)), nativeVerdict,
			string(body), now, id)
		return err
	})
}

// Fail records an experiment that could not be completed.
//
// The verdict is written as INCONCLUSIVE rather than left empty, so that a
// caller reading only the verdict field of a failed run cannot mistake an
// absent word for a passing one.
func (s *Store) Fail(ctx context.Context, id string, f *Fault) error {
	now := s.clock.Now().UnixMilli()
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
UPDATE mcp_runs SET status = ?, phase = ?, verdict = ?, error_code = ?,
                    error_detail = ?, updated_at = ?
WHERE id = ? AND status NOT IN (?, ?)`,
			string(StatusFailed), phaseComplete, string(VerdictInconclusive),
			string(f.Code), f.Detail, now,
			id, string(StatusFinished), string(StatusCancelled))
		return err
	})
}

// RequestCancel marks a run for cancellation and reports its state.
//
// Cancelling is a request rather than a kill. The experiment observes the flag
// at the next point it can stop safely and tears its environment down on the
// way out, because an environment abandoned mid run is the leak this product
// exists to prevent.
func (s *Store) RequestCancel(ctx context.Context, caller, project, id string) (Run, *Fault) {
	run, fault := s.Get(ctx, caller, project, id)
	if fault != nil {
		return Run{}, fault
	}
	if run.Status.terminal() {
		return run, faultf(FaultRunNotCancellable,
			"This run already reached %s and cannot be cancelled.", run.Status)
	}
	now := s.clock.Now().UnixMilli()
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE mcp_runs SET cancel_requested = 1, updated_at = ? WHERE id = ?`, now, id)
		return err
	})
	if err != nil {
		return Run{}, asFault(err)
	}
	run.CancelRequested = true
	run.UpdatedAt = time.UnixMilli(now)
	return run, nil
}

// Cancelled reports whether a cancel has been requested for a run.
func (s *Store) Cancelled(ctx context.Context, id string) bool {
	var flag int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT cancel_requested FROM mcp_runs WHERE id = ?`, id).Scan(&flag)
	})
	// A store that cannot be read is not a reason to keep running an
	// experiment nobody can observe, so an error reads as cancelled. This is
	// the fail closed direction: the cost of stopping a run that was not
	// cancelled is a repeat, and the cost of continuing one that was is an
	// environment nobody is watching.
	if err != nil {
		return true
	}
	return flag != 0
}

// MarkCancelled records that a cancelled run has stopped.
func (s *Store) MarkCancelled(ctx context.Context, id string) error {
	now := s.clock.Now().UnixMilli()
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
UPDATE mcp_runs SET status = ?, phase = ?, verdict = ?, updated_at = ?
WHERE id = ? AND status NOT IN (?, ?)`,
			string(StatusCancelled), phaseComplete, string(VerdictInconclusive), now,
			id, string(StatusFinished), string(StatusFailed))
		return err
	})
}

// RecoverInterrupted settles runs left mid flight by a process that died.
//
// Called once at startup. A run recorded as queued or running while no process
// is running it is a run that will never progress, and leaving it that way
// would make a caller poll forever. It is settled as failed with the verdict
// INCONCLUSIVE, which is the honest report: the experiment did not finish, so
// it says nothing about the change.
//
// This is the reason the store is durable at all. A server that forgot its
// runs on restart would answer RUN_NOT_FOUND for work that really happened;
// one that remembered them but never settled them would answer "running" for
// work that stopped hours ago. Both are worse than saying so.
func (s *Store) RecoverInterrupted(ctx context.Context) (int, error) {
	now := s.clock.Now().UnixMilli()
	var settled int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
UPDATE mcp_runs SET status = ?, phase = ?, verdict = ?, error_code = ?,
                    error_detail = ?, updated_at = ?
WHERE status IN (?, ?)`,
			string(StatusFailed), phaseComplete, string(VerdictInconclusive),
			string(FaultSafetyUnavailable),
			"The server stopped while this run was in progress, so the experiment "+
				"did not finish and proves nothing about the change. Submit it again.",
			now, string(StatusQueued), string(StatusRunning))
		if err != nil {
			return err
		}
		settled, _ = res.RowsAffected()
		return nil
	})
	return int(settled), err
}

// phase names are stable strings a caller may branch on.
const (
	phaseAccepted = "accepted"
	phaseComplete = "complete"
)

const selectRunSQL = `
SELECT id, caller, project, tool, idem_key, inputs_sha, status, phase,
       verdict, native_verdict, result, error_code, error_detail,
       cancel_requested, created_at, updated_at
FROM mcp_runs`

// scanOne reads a single run row, reporting absence rather than an error.
func scanOne(row *sql.Row) (Run, bool, error) {
	var (
		r                     Run
		status, verdict       string
		result                string
		cancel                int64
		createdAt, updatedAt  int64
		phase, native, code   string
		detail, idem, project string
	)
	err := row.Scan(&r.ID, &r.Caller, &project, &r.Tool, &idem, &r.InputsSHA,
		&status, &phase, &verdict, &native, &result, &code, &detail,
		&cancel, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	r.Project, r.IdemKey = project, idem
	r.Status, r.Phase = Status(status), phase
	r.Verdict, r.NativeVerdict = Verdict(verdict), native
	r.ErrorCode, r.ErrorDetail = code, detail
	if result != "" {
		r.Result = []byte(result)
	}
	r.CancelRequested = cancel != 0
	r.CreatedAt = time.UnixMilli(createdAt)
	r.UpdatedAt = time.UnixMilli(updatedAt)
	return r, true, nil
}
