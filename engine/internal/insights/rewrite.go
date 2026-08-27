package insights

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// recordedRewrite is one table Postgres said it was about to rewrite.
type recordedRewrite struct {
	Table     string
	Reason    string
	Statement string
}

// rewriteCaptureSQL installs an event trigger that records every table
// rewrite.
//
// Postgres fires table_rewrite immediately before it copies a table, and
// pg_event_trigger_table_rewrite_oid names the table it is about to copy.
// That is the database itself saying "this ALTER TABLE is a full copy", which
// is a stronger answer than any amount of reading the statement: whether
// ALTER COLUMN TYPE rewrites depends on the type it is coming from, whether
// ADD COLUMN rewrites depends on the server version and on whether the
// default is volatile, and both of those are things the server knows and the
// statement does not say.
//
// The capture table is unlogged and lives in pg_temp's stead as a real table,
// because an event trigger function runs in whatever session fired it and a
// temporary table belongs to the session that made it. It is dropped with the
// trigger.
const rewriteCaptureSQL = `
CREATE TABLE IF NOT EXISTS af_insights_rewrites (
  seq        bigserial PRIMARY KEY,
  table_name text NOT NULL,
  reason     int  NOT NULL,
  statement  text NOT NULL
);
CREATE OR REPLACE FUNCTION af_insights_note_rewrite() RETURNS event_trigger
LANGUAGE plpgsql AS $af$
BEGIN
  INSERT INTO af_insights_rewrites (table_name, reason, statement)
  VALUES (
    pg_event_trigger_table_rewrite_oid()::regclass::text,
    pg_event_trigger_table_rewrite_reason(),
    current_query()
  );
END;
$af$;
CREATE EVENT TRIGGER af_insights_rewrite ON table_rewrite
  EXECUTE FUNCTION af_insights_note_rewrite();`

const rewriteDropSQL = `
DROP EVENT TRIGGER IF EXISTS af_insights_rewrite;
DROP FUNCTION IF EXISTS af_insights_note_rewrite();
DROP TABLE IF EXISTS af_insights_rewrites;`

// rewriteReason turns the bitmask Postgres reports into words. The bits are
// the ones in the documentation for pg_event_trigger_table_rewrite_reason.
func rewriteReason(mask int32) string {
	switch {
	case mask&1 != 0:
		return "the persistence of the table changed"
	case mask&2 != 0:
		return "a column's type changed"
	case mask&4 != 0:
		return "the table's access method changed"
	default:
		return "the table was rewritten"
	}
}

// installRewriteCapture puts the event trigger in place and returns a reader
// for what it recorded and a function that removes it again.
//
// An error means the role cannot create an event trigger, which needs a
// superuser. That is normal on a hosted provider and the caller reports it as
// something not measured rather than as a failure, because the rest of the
// rehearsal is still worth having.
func installRewriteCapture(
	ctx context.Context, conn *pgx.Conn,
) (read func(context.Context) []recordedRewrite, stop func(), err error) {
	// Remove any leftovers first. A rehearsal that was interrupted leaves the
	// trigger behind, and a second CREATE EVENT TRIGGER of the same name
	// fails, which would report "not a superuser" for a database where we are
	// one.
	_, _ = conn.Exec(ctx, rewriteDropSQL)

	if _, err := conn.Exec(ctx, rewriteCaptureSQL); err != nil {
		_, _ = conn.Exec(ctx, rewriteDropSQL)
		return nil, nil, err
	}

	read = func(ctx context.Context) []recordedRewrite {
		rows, err := conn.Query(ctx,
			`SELECT table_name, reason, statement FROM af_insights_rewrites ORDER BY seq`)
		if err != nil {
			return nil
		}
		defer rows.Close()
		var out []recordedRewrite
		for rows.Next() {
			var r recordedRewrite
			var reason int32
			if err := rows.Scan(&r.Table, &reason, &r.Statement); err != nil {
				return out
			}
			r.Reason = rewriteReason(reason)
			r.Table = bareTable(r.Table)
			out = append(out, r)
		}
		return out
	}
	stop = func() {
		// The branch is destroyed after a rehearsal, so this is belt and
		// braces. It matters for the case where somebody points a rehearsal at
		// a database they intend to keep.
		_, _ = conn.Exec(context.WithoutCancel(ctx), rewriteDropSQL)
	}
	return read, stop, nil
}
