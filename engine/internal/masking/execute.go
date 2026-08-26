package masking

import (
	"context"
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
// It checkpoints per chunk, so an interrupted run resumes rather than starting
// over. That matters more than it sounds on a large table: a masking run that
// cannot resume is one that has to be restarted from the beginning every time
// somebody's laptop sleeps.

// Checkpointer records progress so a run can resume.
//
// An interface rather than the state database directly, so the executor can be
// tested without one and so a hosted runner can put checkpoints somewhere else.
type Checkpointer interface {
	// Save records that a table is masked up to a key.
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
	var res Result

	for _, tp := range plan.Tables {
		rows, resumed, err := e.applyTable(ctx, conn, tp)
		res.Rows += rows
		res.Resumed = res.Resumed || resumed
		if err != nil {
			res.Duration = e.clock.Since(started)
			return res, err
		}
		res.Tables++
	}
	res.Duration = e.clock.Since(started)
	return res, nil
}

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

	after := ""
	resumed := false
	if e.checkpoints != nil {
		saved, ok, err := e.checkpoints.Load(ctx, tp.Table.String())
		if err != nil {
			return 0, false, err
		}
		if ok {
			after, resumed = saved, true
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
			if err := e.checkpoints.Save(ctx, tp.Table.String(), after); err != nil {
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
	ctx context.Context, conn *pgx.Conn, tp TablePlan, after string,
) (int64, string, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("masking: starting a transaction for %s: %w", tp.Table, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	selectSQL, args := tp.selectChunk(after)
	rows, err := tx.Query(ctx, selectSQL, args...)
	if err != nil {
		return 0, "", fmt.Errorf("masking: reading %s: %w", tp.Table, err)
	}

	type record struct {
		key    string
		values []*string
	}
	var batch []record
	for rows.Next() {
		vals, scanErr := rows.Values()
		if scanErr != nil {
			rows.Close()
			return 0, "", fmt.Errorf("masking: reading %s: %w", tp.Table, scanErr)
		}
		r := record{key: fmt.Sprint(vals[0])}
		for i := range tp.Columns {
			r.values = append(r.values, toStringPtr(vals[1+i]))
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, "", fmt.Errorf("masking: reading %s: %w", tp.Table, err)
	}
	if len(batch) == 0 {
		return 0, after, nil
	}

	stmt := tp.Compile()
	last := after
	for _, r := range batch {
		params := make([]any, 0, len(tp.Columns)+1)
		params = append(params, r.key)
		for i, c := range tp.Columns {
			col := Column{
				Schema: tp.Table.Schema, Table: tp.Table.Name,
				Name: c.Column.Name, Link: c.Link,
			}
			transform, ok := Lookup(c.Transform)
			if !ok {
				return 0, "", fmt.Errorf("masking: no transform called %s", c.Transform)
			}
			out, applyErr := transform.Apply(e.key, col, r.values[i])
			if applyErr != nil {
				return 0, "", fmt.Errorf("masking: %s.%s: %w", tp.Table, c.Column.Name, applyErr)
			}
			params = append(params, out)
		}
		if _, err := tx.Exec(ctx, stmt.SQL, params...); err != nil {
			return 0, "", fmt.Errorf("masking: writing %s: %w", tp.Table, err)
		}
		last = r.key
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, "", fmt.Errorf("masking: committing %s: %w", tp.Table, err)
	}
	return int64(len(batch)), last, nil
}

// selectChunk builds the read for one chunk.
func (tp TablePlan) selectChunk(after string) (string, []any) {
	key := tp.keyExpr()
	cols := make([]string, 0, len(tp.Columns)+1)
	// Cast on the way out, so every key type arrives as a string. A ctid read
	// natively comes back as a struct that formats as nothing the WHERE clause
	// will match, which produced an update that silently changed no rows.
	cols = append(cols, key+"::text")
	for _, c := range tp.Columns {
		cols = append(cols, quoteIdent(c.Column.Name))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "SELECT %s FROM %s", strings.Join(cols, ", "), tp.Table.Qualified())
	var args []any
	if after != "" && len(tp.OrderBy) > 0 {
		// Compared as text, so one code path covers integer keys, uuids, and
		// anything else somebody used. The order is not the key's natural
		// order for an integer, and it does not need to be: it only has to be
		// total and stable, so every row is visited once and a resume picks up
		// where the last chunk stopped.
		fmt.Fprintf(&b, " WHERE %s::text > $1", key)
		args = append(args, after)
	}
	fmt.Fprintf(&b, " ORDER BY %s::text", key)
	if tp.ChunkSize > 0 {
		fmt.Fprintf(&b, " LIMIT %d", tp.ChunkSize)
	}
	return b.String(), args
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
