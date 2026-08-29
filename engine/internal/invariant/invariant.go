// Package invariant runs the read only statements a manifest declares against
// the environment's database, so that a flow which appears to succeed while
// corrupting data is caught by the data rather than by the screen.
//
// Until this package existed the engine parsed an invariant, refused a
// malformed one, and showed it in `af explain`, and stopped there. Nothing ran
// the SQL and no report carried a result, which is the shape a half finished
// feature takes: every piece is visible and the thing itself does not happen.
// A green run was evidence that the workflows passed and evidence of nothing
// at all about the data.
//
// An invariant holds when its statement returns no rows. The rows are the
// point: a boolean says something is wrong, a set of rows says which ones, and
// the statement that found them is one somebody can run again by hand.
//
// # Read only is enforced, not trusted
//
// The manifest validator already refuses a statement that names a writing
// keyword, and that check cannot be complete. `SELECT do_the_thing()` names no
// keyword and writes, because the writing is inside the function. So every
// invariant runs inside a transaction opened READ ONLY, which is Postgres
// refusing the write rather than this package believing it will not happen,
// and the transaction is rolled back whatever the result. The static check
// catches the common mistake early with a good message; the transaction is
// what makes the promise true.
package invariant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Postgres SQLSTATE codes this package turns into the two error codes the
// catalog reserves for invariants.
const (
	// sqlstateReadOnly is read_only_sql_transaction, raised when a statement
	// attempts to write inside a READ ONLY transaction.
	sqlstateReadOnly = "25006"
	// sqlstateCanceled is query_canceled, which is what statement_timeout
	// raises when it fires.
	sqlstateCanceled = "57014"
)

// DefaultTimeout is how long one invariant may take.
//
// Invariants run after every workflow, so the cost is paid once per workflow
// and not once per run. Thirty seconds is generous for a read that is supposed
// to be quick and short enough that a runaway one is a failure rather than a
// hang.
const DefaultTimeout = 30 * time.Second

// DefaultMaxRows is how many violating rows are kept as evidence.
//
// Enough to see the shape of the problem, few enough that a check which finds
// a million broken rows does not pull a million rows over the wire or paste
// them into a pull request comment.
const DefaultMaxRows = 5

// Options tunes a run.
type Options struct {
	// Timeout is the per invariant limit. Zero means DefaultTimeout.
	Timeout time.Duration
	// MaxRows is how many violating rows to keep. Zero means DefaultMaxRows.
	MaxRows int
}

func (o Options) timeout() time.Duration {
	if o.Timeout <= 0 {
		return DefaultTimeout
	}
	return o.Timeout
}

func (o Options) maxRows() int {
	if o.MaxRows <= 0 {
		return DefaultMaxRows
	}
	return o.MaxRows
}

// Result is what one invariant did.
//
// Held and Err are separate on purpose. An invariant that could not be run has
// not been shown to be violated, and reporting it as a violation would blame
// the application for something that is ours. Callers that treat any non-Held
// result as a failure would say the data is broken when the truth is that the
// check timed out.
type Result struct {
	Name        string
	Description string
	// Held is true when the statement returned no rows. It is false only when
	// rows came back; a result that could not be produced leaves it false and
	// sets Err, so read Err first.
	Held bool
	// Columns names the evidence columns, in the statement's own order.
	Columns []string
	// Rows is the evidence, at most MaxRows of them.
	Rows [][]string
	// More is true when the statement returned more rows than were kept.
	More bool
	// Duration is how long the statement took.
	Duration time.Duration
	// Err is why this invariant produced no verdict. AF-AGT-010 for a timeout,
	// AF-AGT-011 for a write attempt, and the underlying error otherwise.
	Err error
}

// Ran reports whether this invariant produced a verdict either way.
func (r Result) Ran() bool { return r.Err == nil }

// Violated reports whether this invariant was shown to be broken.
//
// An invariant that could not be run is not violated. That distinction is the
// same one the report makes between a failed workflow and a blocked one.
func (r Result) Violated() bool { return r.Err == nil && !r.Held }

// Summary is the whole run in the terms a caller decides on.
type Summary struct {
	Results []Result
}

// Violated returns the invariants shown to be broken.
func (s Summary) Violated() []Result {
	var out []Result
	for _, r := range s.Results {
		if r.Violated() {
			out = append(out, r)
		}
	}
	return out
}

// Errored returns the invariants that produced no verdict.
func (s Summary) Errored() []Result {
	var out []Result
	for _, r := range s.Results {
		if r.Err != nil {
			out = append(out, r)
		}
	}
	return out
}

// Held reports whether every invariant ran and every one held.
//
// False when anything was violated AND when anything could not be run, because
// "every invariant held" is a claim, and a check that did not happen is not
// evidence for it.
func (s Summary) Held() bool {
	for _, r := range s.Results {
		if !r.Held || r.Err != nil {
			return false
		}
	}
	return true
}

// Conn is the part of *pgx.Conn this package uses, so a caller can pass a
// pooled connection or a test double.
type Conn interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// Run executes every invariant and returns one result each, in the order the
// manifest declares them.
//
// One invariant failing, timing out or erroring never stops the others: a run
// that stopped at the first violation would hide the second, and somebody
// fixing the first would then discover the second on the next run rather than
// this one.
func Run(ctx context.Context, conn Conn, invs []schema.Invariant, o Options) Summary {
	out := Summary{Results: make([]Result, 0, len(invs))}
	for _, inv := range invs {
		// A cancelled run stops here rather than attempting the rest. Carrying
		// on would spend a round trip each to produce a list of identical
		// cancellation errors, and somebody reading the report would have to
		// work out that none of them is about their data. The ones not reached
		// are reported as unrun, which is what they are.
		if err := ctx.Err(); err != nil {
			out.Results = append(out.Results, Result{
				Name: inv.Name, Description: inv.Description,
				Err: fmt.Errorf("invariant %s was not run: %w", inv.Name, err),
			})
			continue
		}
		out.Results = append(out.Results, one(ctx, conn, inv, o))
	}
	return out
}

func one(ctx context.Context, conn Conn, inv schema.Invariant, o Options) Result {
	res := Result{Name: inv.Name, Description: inv.Description}
	timeout := o.timeout()

	// Two mechanisms, because one is not reliable on its own.
	//
	// statement_timeout is the one that should fire: Postgres cancels the
	// statement and the error carries 57014, which says precisely what
	// happened. The context deadline is a backstop for a server that never
	// answers at all, and it is set later so that Postgres normally wins.
	//
	// It does not always win. On a loaded machine the round trip after the
	// cancel can take longer than the grace period, and then the context
	// fires first and the error is a bare "context deadline exceeded". That
	// was not a hypothetical: it happened while another suite was using the
	// same server. So the classification below treats a deadline on THIS
	// context as the same answer, because it is: the invariant did not finish
	// in time. Racing for a better error message is fine; depending on
	// winning that race for a correct one is not.
	deadline := timeout + grace(timeout)
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	started := time.Now()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{
		AccessMode: pgx.ReadOnly,
		IsoLevel:   pgx.RepeatableRead,
	})
	if err != nil {
		res.Duration = time.Since(started)
		res.Err = fmt.Errorf("invariant %s: opening a read only transaction: %w", inv.Name, err)
		return res
	}
	// Rolled back whichever way this goes. An invariant observes and never
	// keeps anything, and a read only transaction has nothing to commit.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = "+msLiteral(timeout)); err != nil {
		res.Duration = time.Since(started)
		res.Err = fmt.Errorf("invariant %s: setting the statement timeout: %w", inv.Name, err)
		return res
	}

	cols, rows, more, err := query(ctx, tx, inv.SQL, o.maxRows())
	res.Duration = time.Since(started)
	if err != nil {
		res.Err = classify(inv.Name, timeout, err, ctx.Err())
		return res
	}

	res.Columns = cols
	res.Rows = rows
	res.More = more
	res.Held = len(rows) == 0
	return res
}

// bound wraps the statement so Postgres stops producing rows once there are
// enough, rather than this package reading a few and abandoning the rest.
//
// Abandoning is what the obvious implementation does, and it does not work.
// Breaking out of the row loop and closing leaves pgx to drain the portal to
// completion before the connection can be used again, so an invariant that
// matches a million rows pulls a million rows over the wire to throw them
// away, and one over an unbounded join never returns at all. A test that read
// five rows out of a four trillion row cross join proved it: the statement
// timeout never fired, because the statement had already delivered its first
// rows and it was the draining that hung.
//
// LIMIT max+1 puts the bound where the rows are. The extra row is how More is
// known without counting: max+1 rows back means there are more than max.
//
// Every form the manifest validator permits survives the wrap, which is
// checked against a real server rather than assumed: SELECT, WITH, VALUES and
// TABLE, an inner ORDER BY, and duplicate output column names, which a
// subquery is allowed to have as long as nothing references them by name.
func bound(sql string, max int) string {
	return fmt.Sprintf("SELECT * FROM (%s) AS af_invariant LIMIT %d",
		strings.TrimSuffix(strings.TrimSpace(sql), ";"), max+1)
}

// query runs the statement and keeps at most max rows.
//
// The exact count past the limit is deliberately not known, which is why
// Result carries More rather than a total: "at least six" is honest, and a
// number that cost a million rows to compute would not be worth it.
func query(ctx context.Context, tx pgx.Tx, sql string, max int) ([]string, [][]string, bool, error) {
	rows, err := tx.Query(ctx, bound(sql, max))
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()

	var cols []string
	for _, fd := range rows.FieldDescriptions() {
		cols = append(cols, string(fd.Name))
	}

	var kept [][]string
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, nil, false, err
		}
		kept = append(kept, render(vals))
	}
	// Checked rather than ignored, because a statement_timeout that fires
	// mid-stream surfaces here rather than from Query.
	if err := rows.Err(); err != nil {
		return nil, nil, false, err
	}

	more := len(kept) > max
	if more {
		kept = kept[:max]
	}
	return cols, kept, more, nil
}

// render turns one row into strings for display.
func render(vals []any) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		switch t := v.(type) {
		case nil:
			out[i] = "NULL"
		case []byte:
			out[i] = string(t)
		case string:
			out[i] = t
		case time.Time:
			out[i] = t.UTC().Format(time.RFC3339)
		default:
			out[i] = fmt.Sprintf("%v", t)
		}
	}
	return out
}

// grace is how long past statement_timeout the context waits.
//
// Proportional rather than fixed, because the round trip that carries the
// cancellation is what has to fit in it, and a machine slow enough to make a
// short invariant miss its timeout is slow enough to need more than a constant.
// Never less than five seconds, so a 100ms invariant on a busy laptop still
// gets a sane cancellation window.
func grace(timeout time.Duration) time.Duration {
	g := timeout / 2
	if g < 5*time.Second {
		g = 5 * time.Second
	}
	return g
}

// classify turns a failed statement into the catalog code that describes it.
//
// ctxErr is this invariant's own context error, consulted because a deadline
// that fires here means the same thing statement_timeout means and should not
// reach somebody as "context deadline exceeded".
func classify(name string, timeout time.Duration, err error, ctxErr error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlstateCanceled:
			return aferrors.Wrap(err, aferrors.AFAGT010,
				"invariant", name, "timeout", timeout.String())
		case sqlstateReadOnly:
			return aferrors.Wrap(err, aferrors.AFAGT011, "invariant", name)
		}
	}
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return aferrors.Wrap(err, aferrors.AFAGT010,
			"invariant", name, "timeout", timeout.String())
	}
	return fmt.Errorf("invariant %s: %w", name, err)
}

// msLiteral renders a duration as an integer millisecond literal.
//
// statement_timeout takes no parameter, so this is interpolated. It is built
// from a time.Duration and can only ever be digits.
func msLiteral(d time.Duration) string {
	ms := d.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return fmt.Sprintf("%d", ms)
}
