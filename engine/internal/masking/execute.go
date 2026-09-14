package masking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/antifailure/antifailure/engine/internal/clock"
)

// The executor reads a chunk, computes the masked values in Go, and writes
// them back, one row at a time within a chunked transaction.
//
// Computing in Go rather than in SQL is not a performance choice. The
// transforms are deterministic functions of a key that never goes near the
// database, which is what makes the same customer map to the same fake
// customer across every table and every refresh. A database side function
// would need the key in the database, and a key in the database is a key in
// every backup of it.
//
// It can checkpoint per chunk, through a Checkpointer, so that an interrupted
// run resumes rather than starting over. That matters more than it sounds on a
// large table: a masking run that cannot resume is one that has to be restarted
// from the beginning every time somebody's laptop sleeps.
//
// No command passes a Checkpointer yet. `af golden refresh` and `af mask apply`
// both build executors without one, so an interrupted run of either starts from
// the beginning, and InterruptedError says so rather than promising a resume.
// The path is kept, and tested, for the checkpointer that is still to be wired.

// Checkpointer records progress so a run can resume.
//
// An interface rather than the state database directly, so the executor can be
// tested without one and so a hosted runner can put checkpoints somewhere else.
type Checkpointer interface {
	// Save records that a table is masked up to a row's address, encoded by
	// the executor as one value per primary key column.
	Save(ctx context.Context, table, key string) error
	// Load returns where a table got to, and whether there is a record.
	Load(ctx context.Context, table string) (string, bool, error)
	// Clear forgets a run's progress.
	Clear(ctx context.Context) error
}

// Progress reports how far a run has got.
type Progress struct {
	Table    string
	Rows     int64
	Total    int64
	Chunk    int
	Resumed  bool
	Finished bool
}

// Executor applies a plan to a database.
type Executor struct {
	key   *Key
	clock clock.Clock
	// checkpoints records progress, and may be nil for a run that does not
	// need to resume.
	checkpoints Checkpointer
	// report receives progress, and may be nil.
	report func(Progress)
}

// ExecutorOptions configure an executor.
type ExecutorOptions struct {
	Key         *Key
	Clock       clock.Clock
	Checkpoints Checkpointer
	Progress    func(Progress)
}

// NewExecutor returns an executor for a key.
func NewExecutor(opts ExecutorOptions) (*Executor, error) {
	if opts.Key == nil {
		return nil, errors.New("masking: an executor needs a key")
	}
	if opts.Clock == nil {
		opts.Clock = clock.New()
	}
	return &Executor{
		key: opts.Key, clock: opts.Clock,
		checkpoints: opts.Checkpoints, report: opts.Progress,
	}, nil
}

// Result is what a run did.
type Result struct {
	// Tables is how many tables were rewritten.
	Tables int
	// Rows is how many rows were rewritten.
	Rows int64
	// Duration is how long it took.
	Duration time.Duration
	// Resumed reports whether the run picked up from a checkpoint.
	Resumed bool
	// CopiedUnchanged names the columns the plan left exactly as they were
	// because no rule covered them and the default could not empty them.
	// Carried on the result so that the command that ran the plan can say
	// so beside its own success line, rather than leaving the fact in a plan
	// nobody re-reads.
	CopiedUnchanged []string
}

// Apply runs a plan against a database.
//
// A plan with unresolved problems is refused. Running one partly is worse than
// not starting: the data is neither real nor safe and nothing says which rows
// are which.
func (e *Executor) Apply(ctx context.Context, conn *pgx.Conn, plan Plan) (Result, error) {
	if !plan.Runnable() {
		return Result{}, fmt.Errorf("masking: the plan has %d problems and will not be run; %s",
			len(plan.Problems), describeProblems(plan.Problems))
	}
	started := e.clock.Now()
	res := Result{CopiedUnchanged: plan.CopiedUnchangedNames()}

	for _, tp := range plan.Tables {
		rows, resumed, err := e.applyTable(ctx, conn, tp)
		res.Rows += rows
		res.Resumed = res.Resumed || resumed
		if err != nil {
			res.Duration = e.clock.Since(started)
			if ctxErr := ctx.Err(); ctxErr != nil {
				// Asked of the context and not of the error, because the error
				// is whatever the store said about the statement in flight. A
				// cancellation that takes the connection down with it arrives
				// here as unexpected EOF, with no cancellation in its chain.
				return res, &InterruptedError{
					Table: tp.Table.String(), Rows: res.Rows,
					Resumable: e.checkpoints != nil && len(tp.OrderBy) > 0,
					cause:     ctxErr, store: err,
				}
			}
			return res, err
		}
		res.Tables++
	}
	res.Duration = e.clock.Since(started)
	return res, nil
}

// InterruptedError is what Apply returns when its context ends during a run.
//
// It replaces what the store said. A control C during `af golden refresh` used
// to print "masking: writing public.customers: unexpected EOF", which reads as a
// broken database and says nothing anybody can act on. What matters is the rows:
// how many were written, that the chunk in flight was not, and whether running
// again carries on or starts over. The store's error stays in the chain for
// anyone who wants it.
type InterruptedError struct {
	// Table is the table being rewritten when the run stopped.
	Table string
	// Rows is how many rows the run wrote and committed before it stopped.
	Rows int64
	// Resumable reports whether progress was being recorded for that table, so
	// that the next run with the same checkpoints carries on after the last
	// chunk that committed.
	Resumable bool

	cause error
	store error
}

func (e *InterruptedError) Error() string {
	stopped := "was interrupted"
	if errors.Is(e.cause, context.DeadlineExceeded) {
		stopped = "was interrupted when its deadline passed"
	}
	// Neutral about what to do next, because the right answer depends on the
	// caller: a refresh can simply run again from the source, and a branch that
	// is now partly masked cannot be masked again, since masking a masked value
	// changes it. The caller adds that sentence.
	next := "nothing records where it got to, so a run cannot carry on from here"
	if e.Resumable {
		next = "the next run with the same checkpoints resumes after the last chunk that committed"
	}
	return fmt.Sprintf("masking %s while rewriting %s, after %d rows were written and committed; "+
		"the chunk in flight was rolled back, and %s", stopped, e.Table, e.Rows, next)
}

// Unwrap returns the context's reason and the store's error, so errors.Is finds
// context.Canceled whatever the store reported.
func (e *InterruptedError) Unwrap() []error { return []error{e.cause, e.store} }

func describeProblems(problems []Assignment) string {
	parts := make([]string, 0, len(problems))
	for _, p := range problems {
		parts = append(parts, fmt.Sprintf("%s.%s: %s", p.Table, p.Column.Name, p.Problem))
	}
	return strings.Join(parts, "; ")
}

// applyTable rewrites one table, chunk by chunk.
func (e *Executor) applyTable(ctx context.Context, conn *pgx.Conn, tp TablePlan) (int64, bool, error) {
	if len(tp.Columns) == 0 {
		return 0, false, nil
	}

	var after []string
	resumed := false
	if e.checkpoints != nil {
		saved, ok, err := e.checkpoints.Load(ctx, tp.Table.String())
		if err != nil {
			return 0, false, err
		}
		if ok {
			if after, err = decodeCheckpoint(tp, saved); err != nil {
				return 0, false, err
			}
			resumed = true
		}
	}

	var total int64
	chunk := 0
	for {
		n, last, err := e.applyChunk(ctx, conn, tp, after)
		if err != nil {
			return total, resumed, err
		}
		total += n
		chunk++
		if n == 0 {
			break
		}
		after = last
		if e.checkpoints != nil && tp.ChunkSize > 0 {
			// Saved after the chunk is committed, so a crash between the two
			// re-runs a chunk rather than skipping one. Masking a row twice is
			// harmless because the transforms are deterministic; skipping one
			// ships real data.
			if err := e.checkpoints.Save(ctx, tp.Table.String(), encodeCheckpoint(after)); err != nil {
				return total, resumed, err
			}
		}
		if e.report != nil {
			e.report(Progress{
				Table: tp.Table.String(), Rows: total, Total: tp.Rows(),
				Chunk: chunk, Resumed: resumed,
			})
		}
		if tp.ChunkSize == 0 {
			break
		}
	}
	if e.report != nil {
		e.report(Progress{Table: tp.Table.String(), Rows: total, Total: tp.Rows(), Finished: true})
	}
	return total, resumed, nil
}

// applyChunk reads a chunk and writes it back inside one transaction.
//
// The read is inside the transaction rather than before it so that both see
// one snapshot. That matters most for a table with no primary key, which is
// addressed by ctid: a physical row identifier is only meaningful within the
// transaction that read it, and updating by one from an earlier snapshot would
// silently match nothing.
func (e *Executor) applyChunk(
	ctx context.Context, conn *pgx.Conn, tp TablePlan, after []string,
) (int64, []string, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("masking: starting a transaction for %s: %w", tp.Table, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	selectSQL, args := tp.selectChunk(after)
	rows, err := tx.Query(ctx, selectSQL, args...)
	if err != nil {
		return 0, nil, fmt.Errorf("masking: reading %s: %w", tp.Table, err)
	}

	// The address is every key column, read as text, and the values follow it.
	width := tp.AddressWidth()
	type record struct {
		key    []string
		values []*string
	}
	var batch []record
	for rows.Next() {
		vals, scanErr := rows.Values()
		if scanErr != nil {
			rows.Close()
			return 0, nil, fmt.Errorf("masking: reading %s: %w", tp.Table, scanErr)
		}
		if len(vals) != width+len(tp.Columns) {
			rows.Close()
			return 0, nil, fmt.Errorf("masking: reading %s: a chunk row has %d columns and the plan "+
				"expects %d key columns and %d values", tp.Table, len(vals), width, len(tp.Columns))
		}
		r := record{key: make([]string, width)}
		for j := 0; j < width; j++ {
			r.key[j] = fmt.Sprint(vals[j])
		}
		for i := range tp.Columns {
			r.values = append(r.values, toStringPtr(vals[width+i]))
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("masking: reading %s: %w", tp.Table, err)
	}
	if len(batch) == 0 {
		return 0, after, nil
	}

	stmt := tp.Compile()
	last := after
	for _, r := range batch {
		params := make([]any, 0, len(tp.Columns)+width)
		for _, k := range r.key {
			params = append(params, k)
		}
		for i, c := range tp.Columns {
			col := Column{
				Schema: tp.Table.Schema, Table: tp.Table.Name,
				Name: c.Column.Name, Link: c.Link,
			}
			transform, ok := Lookup(c.Transform)
			if !ok {
				return 0, nil, fmt.Errorf("masking: no transform called %s", c.Transform)
			}
			out, applyErr := transform.Apply(e.key, col, r.values[i])
			if applyErr != nil {
				return 0, nil, fmt.Errorf("masking: %s.%s: %w", tp.Table, c.Column.Name, applyErr)
			}
			params = append(params, out)
		}
		if _, err := tx.Exec(ctx, stmt.SQL, params...); err != nil {
			return 0, nil, fmt.Errorf("masking: writing %s: %w", tp.Table, err)
		}
		last = r.key
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, fmt.Errorf("masking: committing %s: %w", tp.Table, err)
	}
	return int64(len(batch)), last, nil
}

// selectChunk builds the read for one chunk, in the table's own dialect.
func (tp TablePlan) selectChunk(after []string) (string, []any) {
	q := tp.Table.dialect().SelectChunk(tp, after)
	return q.SQL, q.Args
}

// checkpointPrefix marks a checkpoint that carries a row's whole address.
const checkpointPrefix = "keys:"

// encodeCheckpoint records a row's address, one value per primary key column.
func encodeCheckpoint(address []string) string {
	b, _ := json.Marshal(address)
	return checkpointPrefix + string(b)
}

// decodeCheckpoint reads back an address, and refuses one it cannot trust.
//
// A checkpoint without the prefix was written when a row was addressed by the
// text of its first key column alone and pages were ordered by that text. It is
// refused rather than read: it names no whole address, and its position in text
// order is not a position in the key's order, so resuming from it could skip
// rows, and a skipped row ships what production had. An address with a different
// number of values than the key has columns means the key changed since it was
// written, and is refused for the same reason.
func decodeCheckpoint(tp TablePlan, saved string) ([]string, error) {
	restart := fmt.Sprintf("clear the masking checkpoints and mask %s from the beginning", tp.Table)
	if !strings.HasPrefix(saved, checkpointPrefix) {
		return nil, fmt.Errorf("masking: the checkpoint for %s was written by a version that addressed "+
			"a row by the text of its first key column alone, and resuming from it could skip rows; %s",
			tp.Table, restart)
	}
	var address []string
	if err := json.Unmarshal([]byte(strings.TrimPrefix(saved, checkpointPrefix)), &address); err != nil {
		return nil, fmt.Errorf("masking: the checkpoint for %s cannot be read (%w); %s", tp.Table, err, restart)
	}
	if len(tp.OrderBy) == 0 || len(address) != len(tp.OrderBy) {
		return nil, fmt.Errorf("masking: the checkpoint for %s names %d key values and the table's "+
			"primary key has %d columns, so the key changed since it was written; %s",
			tp.Table, len(address), len(tp.OrderBy), restart)
	}
	return address, nil
}

// toStringPtr converts a scanned value to the shape a transform takes.
//
// Null is a pointer to nothing rather than an empty string, because the two
// mean different things in every schema and a transform that cannot tell them
// apart turns every missing value into a present one.
func toStringPtr(v any) *string {
	if v == nil {
		return nil
	}
	switch value := v.(type) {
	case string:
		return &value
	case []byte:
		s := string(value)
		return &s
	default:
		s := fmt.Sprint(value)
		return &s
	}
}

// PreviewRow is one column's value before and after.
type PreviewRow struct {
	Column string
	Before string
	After  string
}

// Preview transforms a few rows in memory and writes nothing.
//
// Somebody iterating on rules has to see the output before committing to it,
// and the alternative, applying and looking, is irreversible on a branch they
// may want to keep.
func Preview(
	ctx context.Context, conn *pgx.Conn, tp TablePlan, key *Key, rows int,
) ([][]PreviewRow, error) {
	limited := tp
	limited.ChunkSize = rows
	selectSQL, args := limited.selectChunk(nil)

	result, err := conn.Query(ctx, selectSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("masking: reading %s: %w", tp.Table, err)
	}
	defer result.Close()

	var out [][]PreviewRow
	for result.Next() {
		vals, scanErr := result.Values()
		if scanErr != nil {
			return nil, scanErr
		}
		var row []PreviewRow
		for i, c := range tp.Columns {
			before := toStringPtr(vals[tp.AddressWidth()+i])
			col := Column{
				Schema: tp.Table.Schema, Table: tp.Table.Name,
				Name: c.Column.Name, Link: c.Link,
			}
			transform, ok := Lookup(c.Transform)
			if !ok {
				continue
			}
			after, applyErr := transform.Apply(key, col, before)
			if applyErr != nil {
				return nil, fmt.Errorf("masking: %s.%s: %w", tp.Table, c.Column.Name, applyErr)
			}
			row = append(row, PreviewRow{
				Column: c.Column.Name,
				Before: orNull(before),
				After:  orNull(after),
			})
		}
		out = append(out, row)
	}
	return out, result.Err()
}

// orNull renders a value, distinguishing a missing one from an empty one.
func orNull(v *string) string {
	if v == nil {
		return "(null)"
	}
	return *v
}
