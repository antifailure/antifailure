package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/antifailure/antifailure/engine/internal/state"
)

// SequenceReserver keeps an environment's event sequence monotonic across the
// processes that make it up.
//
// This exists because of a bug that would have made the whole control plane
// integration useless the moment it was wired. The bus counts sequence numbers
// in memory and starts every process at zero, and one environment is the work
// of three or four separate commands: `af up`, `af test`, `af down`. The
// control plane advances an environment's row with
//
//	WHERE org_id = $1 AND env_id = $2 AND last_sequence < $3
//
// which is the whole out-of-order defence and is right. But it means that once
// `af up` has taken the row to sequence 7, every one of `af test`'s events
// arrives numbered 1, 2, 3 and matches zero rows. The environment would sit in
// the dashboard in the state its first command left it in, forever, and
// `af down` would never mark it torn down. The events would all be stored, so
// nothing would look broken except the one thing anybody is looking at.
//
// The fix is a durable per-environment counter. A block is reserved before any
// event is issued, so a process that is killed cannot cause a later one to
// reuse numbers, and the true high water mark is written back at close, so a
// clean run leaves no gap.
type SequenceReserver struct {
	db *state.DB
}

// NewSequenceReserver returns a reserver backed by the local state database,
// which is where everything else that has to survive a process already lives.
func NewSequenceReserver(db *state.DB) *SequenceReserver {
	return &SequenceReserver{db: db}
}

// BlockSize is how many sequence numbers are reserved at once.
//
// Large enough that no single command exhausts it: the busiest event source is
// one line of build output per event, and a build that emits sixty-five
// thousand lines has other problems. Small enough that the gap a killed process
// leaves is meaningless to a consumer that only compares numbers.
const BlockSize = 65536

func seqKey(envID string) string { return "events.sequence." + envID }

// Reserve claims a block and returns the sequence to continue from.
//
// The read and the write are one transaction. Without that, two commands
// starting at the same moment would both read the same value and both issue the
// same numbers, which is the bug this type exists to prevent, arrived at from a
// different direction.
func (r *SequenceReserver) Reserve(ctx context.Context, envID string) (uint64, error) {
	if r == nil || r.db == nil {
		return 0, nil
	}
	var base uint64
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		var raw string
		err := tx.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", seqKey(envID)).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if raw != "" {
			// A value this process cannot parse is a value it must not trust.
			// Starting from zero would reuse numbers, so the safe reading of an
			// unreadable counter is that it is as high as it could be.
			n, perr := strconv.ParseUint(raw, 10, 64)
			if perr != nil {
				return fmt.Errorf(
					"telemetry: the event sequence for %s reads %q, which is not a number; "+
						"delete that row from the local state to reset it", envID, raw)
			}
			base = n
		}
		_, err = tx.ExecContext(ctx,
			"INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
			seqKey(envID), strconv.FormatUint(base+BlockSize, 10))
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("telemetry: reserve event sequence for %s: %w", envID, err)
	}
	return base, nil
}

// Settle records where the sequence actually reached.
//
// Called at the end of a command. Writing the true high water mark rather than
// leaving the reserved ceiling means a clean run leaves no gap at all, while a
// run that was killed leaves the ceiling standing, which is the conservative
// answer and the one that cannot cause a reuse.
//
// Writing unconditionally is safe for one specific reason and would not be
// otherwise: `af up`, `af test` and `af down` each hold the per-environment
// file lock for their whole run, so two commands are never issuing sequence
// numbers for one environment at the same time. The value written is therefore
// always at least the value read at Reserve.
func (r *SequenceReserver) Settle(ctx context.Context, envID string, reached uint64) error {
	if r == nil || r.db == nil || reached == 0 {
		return nil
	}
	if err := r.db.SetMeta(ctx, seqKey(envID), strconv.FormatUint(reached, 10)); err != nil {
		return fmt.Errorf("telemetry: settle event sequence for %s: %w", envID, err)
	}
	return nil
}
