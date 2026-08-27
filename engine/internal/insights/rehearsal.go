package insights

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// Applier runs a project's pending migrations against one database.
//
// It is an interface because the two ways to do it are genuinely different.
// A project whose migrations are SQL files is applied statement by statement
// from here, which is the only way to get an honest duration for each one. A
// project whose migrations are Ruby or Python has to be applied by its own
// tool, inside its own image, because only that tool knows what SQL they
// become. Running the tool on the workstation instead would use whatever
// gems, packages and interpreter the workstation happens to have, and a
// rehearsal that does not match the deploy is a rehearsal of the wrong thing.
type Applier interface {
	// Name identifies the applier in the report.
	Name() string
	// Apply runs the pending migrations, in order. Statements are returned
	// when the applier knows what it ran, and nil when it does not.
	Apply(ctx context.Context, url secrets.Value, pending []Migration) ([]StatementTiming, error)
}

// StatementTiming is one statement and what it cost.
type StatementTiming struct {
	// Migration and Index locate the statement in the repository.
	Migration string `json:"migration,omitempty"`
	Index     int    `json:"index,omitempty"`
	// SQL is the statement, normalised for display.
	SQL string `json:"sql"`
	// MS is how long it took.
	MS float64 `json:"ms"`
	// Rewrote names the tables Postgres rewrote while running it, reported by
	// the database rather than inferred from the statement. A rewrite is the
	// difference between a migration that returns instantly and one that
	// holds an ACCESS EXCLUSIVE lock for the length of a full table copy.
	Rewrote []string `json:"rewrote,omitempty"`
}

// Rehearsal is what happened when the migrations ran against the branch.
type Rehearsal struct {
	// Tool is the migration tool that was recognised.
	Tool Tool `json:"tool,omitempty"`
	// Applier says how the migrations were run.
	Applier string `json:"applier"`
	// Pending is the migrations that had not been applied to the branch.
	Pending []Migration `json:"pending,omitempty"`
	// Statements is every statement and its duration, in the order they ran.
	Statements []StatementTiming `json:"statements,omitempty"`
	// TotalMS is the whole run, which is more than the sum of the statements
	// because the tool's own work counts against a deploy window too.
	TotalMS float64 `json:"total_ms"`
	// Locks is the strongest lock seen on each table while it ran.
	Locks []LockHold `json:"locks,omitempty"`
	// Lint is what the statements would do to a table this size.
	Lint []LintFinding `json:"lint,omitempty"`
	// Failed and Error record a migration that did not apply. This is the
	// AF-DB-030 case, and it is a finding rather than an error from the
	// rehearsal itself: the rehearsal did its job.
	Failed bool   `json:"failed"`
	Error  string `json:"error,omitempty"`
	// Missing says what could not be measured, and why.
	Missing []string `json:"missing,omitempty"`
}

// LargeTableRows is the default row count above which a table is large enough
// that a rewrite or a blocking lock on it is an outage rather than a pause.
//
// It mirrors engine/internal/manifest's default, and a test asserts they agree.
const LargeTableRows = 100_000

// Rehearse applies the pending migrations to a branch and reports what
// happened.
//
// conn must be a connection to the branch, and watch a second connection to
// the same database, because a lock held by a statement in flight is not
// visible from the session holding it.
//
// The branch must be a fresh one. Migrations are not required to be
// idempotent and most are not, so a rehearsal against a branch something else
// has already migrated measures the wrong thing or fails outright.
func Rehearse(
	ctx context.Context,
	conn, watch *pgx.Conn,
	url secrets.Value,
	set MigrationSet,
	applier Applier,
	largeRows int,
) (Rehearsal, error) {
	if largeRows <= 0 {
		largeRows = LargeTableRows
	}
	r := Rehearsal{Tool: set.Tool, Applier: applier.Name()}
	if set.Reason != "" {
		r.Missing = append(r.Missing, set.Reason)
	}

	applied, err := set.Applied(ctx, conn)
	if err != nil {
		r.Missing = append(r.Missing,
			"the migration tool's history table could not be read, so every migration on "+
				"disk is treated as pending: "+short(err))
	}
	r.Pending = set.Pending(applied)

	// The schema is captured before anything runs, because that is the state
	// the migrations will meet. Capturing it afterwards would lint the new
	// schema against itself and never find a dropped column in a view.
	schema, err := CaptureSchema(ctx, conn)
	if err != nil {
		return r, err
	}

	var stmts []Statement
	for _, m := range r.Pending {
		stmts = append(stmts, Split(m.Name, m.SQL)...)
	}
	if len(stmts) > 0 {
		r.Lint = Lint(stmts, schema, largeRows)
	}

	// What the server did comes from the server. Event triggers record every
	// DDL command and its duration, and every table Postgres rewrote, neither
	// of which is knowable from the statement: whether an ALTER TABLE rewrites
	// depends on the type the column is coming from, and how long a statement
	// took is not observable from outside when somebody else's tool is sending
	// it. It needs a superuser, which is true on a local branch and often not
	// on a hosted one, so a refusal is reported rather than fatal.
	watcher, stopCapture, err := installCapture(ctx, conn)
	if err != nil {
		r.Missing = append(r.Missing,
			"the server could not be asked what the migrations did, because installing an "+
				"event trigger needs a superuser and this role is not one. Statement timings "+
				"come from the applier where it knows them, and a rewrite is only reported "+
				"when the statement makes it obvious: "+short(err))
	} else {
		defer stopCapture()
	}

	// Everything on this database except the two connections the rehearsal
	// itself is holding. The applier's connection is not one we can name: the
	// SQL applier opens its own, and an applier that runs a migrate command in
	// a container opens one we never see.
	var exclude []int32
	if p, err := backendPID(ctx, conn); err == nil {
		exclude = append(exclude, p)
	}
	collect := watchLocks(ctx, watch, exclude, LockSampleInterval)

	start := time.Now()
	timings, applyErr := applier.Apply(ctx, url, r.Pending)
	r.TotalMS = float64(time.Since(start).Microseconds()) / 1000

	r.Locks = collect()
	r.Statements = timings
	if watcher != nil {
		// The applier may have finished before the server did, which is
		// normal when it ran somewhere else.
		waitForQuiet(ctx, conn, 5*time.Second)
		if len(r.Statements) == 0 {
			// The applier does not know what it ran, which is the case for
			// every tool whose migrations are not SQL we can read. The server
			// does, so this is where a Rails or Django migration gets its
			// per-statement timing.
			r.Statements = watcher.timings(ctx)
		}
		attachRewrites(&r, watcher.rewrites(ctx))
	}

	if applyErr != nil {
		r.Failed = true
		r.Error = short(applyErr)
	}
	return r, nil
}

// attachRewrites puts each rewritten table against the statement that caused
// it, matching on the order the two were recorded in.
func attachRewrites(r *Rehearsal, rewrites []recordedRewrite) {
	if len(rewrites) == 0 {
		return
	}
	if len(r.Statements) == 0 {
		// Nothing to attach them to, so report them as statements of their
		// own rather than losing them.
		for _, rw := range rewrites {
			r.Statements = append(r.Statements, StatementTiming{
				SQL: normalise(rw.Statement), Rewrote: []string{rw.Table},
			})
		}
		return
	}
	for _, rw := range rewrites {
		matched := false
		for i := range r.Statements {
			if strings.EqualFold(
				strings.Join(strings.Fields(r.Statements[i].SQL), " "),
				normalise(rw.Statement)) {
				r.Statements[i].Rewrote = append(r.Statements[i].Rewrote, rw.Table)
				matched = true
				break
			}
		}
		if !matched {
			for i := range r.Statements {
				if strings.Contains(fold(r.Statements[i].SQL), strings.ToUpper(rw.Table)) &&
					strings.HasPrefix(fold(r.Statements[i].SQL), "ALTER TABLE") {
					r.Statements[i].Rewrote = append(r.Statements[i].Rewrote, rw.Table)
					matched = true
					break
				}
			}
		}
		if !matched && len(r.Statements) > 0 {
			last := len(r.Statements) - 1
			r.Statements[last].Rewrote = append(r.Statements[last].Rewrote, rw.Table)
		}
	}
}

// Slowest returns the statements that took longest, worst first.
func (r Rehearsal) Slowest(n int) []StatementTiming {
	out := append([]StatementTiming(nil), r.Statements...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].MS > out[j-1].MS; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// Rewrote lists every table the run rewrote.
func (r Rehearsal) Rewrote() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range r.Statements {
		for _, t := range s.Rewrote {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// SQLApplier applies a set of SQL migrations statement by statement.
//
// Each statement is timed on its own, which is the point: a migration file is
// reported as one number by every tool that runs it, and the number somebody
// needs is which statement inside it took the ninety seconds.
type SQLApplier struct {
	// Progress receives a line per migration, already safe to print.
	Progress func(string)
}

// Name identifies the applier.
func (*SQLApplier) Name() string { return "sql" }

// Apply runs every statement, stopping at the first failure.
//
// Each migration runs in its own transaction, which is what every tool in the
// list does, so a failure leaves the branch in the state a real failed deploy
// would leave production in rather than halfway through a file.
func (a *SQLApplier) Apply(
	ctx context.Context, url secrets.Value, pending []Migration,
) ([]StatementTiming, error) {
	conn, err := pgx.Connect(ctx, url.Reveal())
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	var out []StatementTiming
	for _, m := range pending {
		if a.Progress != nil {
			a.Progress("rehearsing " + m.Name)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return out, err
		}
		for _, st := range Split(m.Name, m.SQL) {
			start := time.Now()
			_, err := tx.Exec(ctx, st.SQL)
			ms := float64(time.Since(start).Microseconds()) / 1000
			out = append(out, StatementTiming{
				Migration: st.Migration, Index: st.Index,
				SQL: normalise(st.SQL), MS: ms,
			})
			if err != nil {
				_ = tx.Rollback(ctx)
				return out, fmt.Errorf("%s statement %d: %w", m.Name, st.Index, err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return out, fmt.Errorf("%s: %w", m.Name, err)
		}
	}
	return out, nil
}
