package insights

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// This file is the database watching itself while a migration runs.
//
// It exists because the useful facts about a migration are ones the statement
// does not contain. Whether an ALTER TABLE rewrites depends on the type the
// column is coming from. How long a statement took is not knowable from
// outside when somebody else's tool is sending it. And when the migrations are
// Ruby or Python, there is no SQL to read at all: only the server ever sees
// what they became.
//
// So two event triggers. One pair around every DDL command, recording what was
// sent and how long it took, and one on table_rewrite, which Postgres fires
// immediately before it copies a table.
//
// Both need a superuser, which is true on a local branch and often not on a
// hosted one. A refusal is reported rather than fatal, because the rest of a
// rehearsal is still worth having.

// captureSQL installs the capture tables and the triggers that fill them.
//
// The tables are ordinary rather than temporary: an event trigger function
// runs in whatever session fired the command, and a temporary table belongs to
// the session that created it, so a trigger writing to one would fail for
// every session but ours. They are dropped with the triggers.
//
// The end trigger updates the newest unfinished row rather than matching on
// the statement text, because one DDL command can fire the pair more than once
// when it cascades, and the innermost is the one that finished.
const captureSQL = `
CREATE TABLE IF NOT EXISTS af_insights_ddl (
  seq        bigserial PRIMARY KEY,
  tag        text        NOT NULL,
  statement  text        NOT NULL,
  started_at timestamptz NOT NULL,
  ended_at   timestamptz
);
CREATE TABLE IF NOT EXISTS af_insights_rewrites (
  seq        bigserial PRIMARY KEY,
  table_name text NOT NULL,
  reason     int  NOT NULL,
  statement  text NOT NULL
);

CREATE OR REPLACE FUNCTION af_insights_ddl_start() RETURNS event_trigger
LANGUAGE plpgsql AS $af$
BEGIN
  INSERT INTO af_insights_ddl (tag, statement, started_at)
  VALUES (tg_tag, current_query(), clock_timestamp());
END;
$af$;

CREATE OR REPLACE FUNCTION af_insights_ddl_end() RETURNS event_trigger
LANGUAGE plpgsql AS $af$
BEGIN
  UPDATE af_insights_ddl SET ended_at = clock_timestamp()
  WHERE seq = (SELECT max(seq) FROM af_insights_ddl WHERE ended_at IS NULL);
END;
$af$;

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

CREATE EVENT TRIGGER af_insights_start ON ddl_command_start
  EXECUTE FUNCTION af_insights_ddl_start();
CREATE EVENT TRIGGER af_insights_end ON ddl_command_end
  EXECUTE FUNCTION af_insights_ddl_end();
CREATE EVENT TRIGGER af_insights_rewrite ON table_rewrite
  EXECUTE FUNCTION af_insights_note_rewrite();`

const captureDropSQL = `
DROP EVENT TRIGGER IF EXISTS af_insights_start;
DROP EVENT TRIGGER IF EXISTS af_insights_end;
DROP EVENT TRIGGER IF EXISTS af_insights_rewrite;
DROP FUNCTION IF EXISTS af_insights_ddl_start();
DROP FUNCTION IF EXISTS af_insights_ddl_end();
DROP FUNCTION IF EXISTS af_insights_note_rewrite();
DROP TABLE IF EXISTS af_insights_ddl;
DROP TABLE IF EXISTS af_insights_rewrites;`

// recordedRewrite is one table Postgres said it was about to rewrite.
type recordedRewrite struct {
	Table     string
	Reason    string
	Statement string
}

// recordedDDL is one DDL command the server ran, and what it cost.
type recordedDDL struct {
	Tag       string
	Statement string
	MS        float64
	Finished  bool
}

// capture reads what the triggers recorded.
type capture struct {
	conn *pgx.Conn
}

// installCapture puts the event triggers in place and returns a reader and a
// function that removes them.
//
// An error means the role cannot create an event trigger, which needs a
// superuser. That is normal on a hosted provider and the caller reports it as
// something not measured rather than as a failure.
func installCapture(ctx context.Context, conn *pgx.Conn) (*capture, func(), error) {
	// Remove any leftovers first. An interrupted rehearsal leaves the triggers
	// behind, and a second CREATE EVENT TRIGGER of the same name fails, which
	// would report "not a superuser" for a database where we are one.
	_, _ = conn.Exec(ctx, captureDropSQL)

	if _, err := conn.Exec(ctx, captureSQL); err != nil {
		_, _ = conn.Exec(ctx, captureDropSQL)
		return nil, nil, err
	}
	stop := func() {
		// The branch is destroyed after a rehearsal, so this is belt and
		// braces. It matters for somebody pointing a rehearsal at a database
		// they intend to keep.
		_, _ = conn.Exec(context.WithoutCancel(ctx), captureDropSQL)
	}
	return &capture{conn: conn}, stop, nil
}

// rewrites is every table the run rewrote, in order.
func (c *capture) rewrites(ctx context.Context) []recordedRewrite {
	rows, err := c.conn.Query(ctx,
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

// ddl is every DDL command the server ran, in order, with its duration.
//
// This is what gives a tool-driven migration per-statement timing. The tool
// reports one number for a file and the server knows what each command inside
// it cost, so the server is asked.
func (c *capture) ddl(ctx context.Context) []recordedDDL {
	rows, err := c.conn.Query(ctx, `
SELECT tag, statement,
       COALESCE(EXTRACT(EPOCH FROM (ended_at - started_at)) * 1000, 0),
       ended_at IS NOT NULL
FROM af_insights_ddl
WHERE statement NOT LIKE '%af_insights_%'
ORDER BY seq`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []recordedDDL
	for rows.Next() {
		var d recordedDDL
		if err := rows.Scan(&d.Tag, &d.Statement, &d.MS, &d.Finished); err != nil {
			return out
		}
		out = append(out, d)
	}
	return out
}

// rewriteReason turns the bitmask Postgres reports into words. The bits are
// the ones documented for pg_event_trigger_table_rewrite_reason.
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

// timings turns the recorded DDL into the report's shape.
//
// A command that never finished is one the migration failed on, and it is
// reported with the time it had used rather than dropped: the statement that
// was still running when everything stopped is the interesting one.
func (c *capture) timings(ctx context.Context) []StatementTiming {
	recorded := c.ddl(ctx)
	out := make([]StatementTiming, 0, len(recorded))
	for i, d := range recorded {
		out = append(out, StatementTiming{
			Index: i + 1,
			SQL:   normalise(d.Statement),
			MS:    d.MS,
		})
	}
	return out
}

// waitForQuiet is unused by the SQL applier and exists for an applier that
// runs somewhere else: a container can exit before the server has finished the
// commit it asked for, and reading the capture too early loses the last
// statement's end time.
func waitForQuiet(ctx context.Context, conn *pgx.Conn, within time.Duration) {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		var open int
		if err := conn.QueryRow(ctx,
			"SELECT count(*) FROM af_insights_ddl WHERE ended_at IS NULL").Scan(&open); err != nil {
			return
		}
		if open == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}
