package masking_test

// A RUN THAT IS INTERRUPTED SAYS IT WAS INTERRUPTED, AND WHETHER IT CAN RESUME.
//
// Control C during `af golden refresh` printed
// "AF-MSK-010 Masking could not run: masking: writing public.customers: unexpected EOF".
// The signal cancelled the run's context, the connection went down under the
// statement in flight, and the executor returned the store's error for it. That
// sentence says the database broke, and it says nothing about the rows: which
// were rewritten, which were not, and whether running again carries on or starts
// over.
//
// ORDERINGS. The cancellation lands inside a chunk, while the database is part
// way through its rows, which is where a control C lands on a large table. With
// the connection still up, so the driver reports the cancellation. With the
// connection dropped at the same moment, so the driver reports only a broken
// connection: that is the demo's case, and a check of the error chain alone
// cannot see it. And with a checkpoint, where the run says it resumes and a second
// run does.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/masking"
)

// stallSchema holds twelve contacts and a trigger that sleeps on the seventh.
// With a chunk of three, rows one to six commit in two chunks and the third chunk
// stalls on its first row.
const stallSchema = `
DROP TABLE IF EXISTS stall_contacts;
DROP FUNCTION IF EXISTS stall_on_seven();
CREATE TABLE stall_contacts (
  id    integer PRIMARY KEY,
  email text NOT NULL
);
INSERT INTO stall_contacts (id, email)
  SELECT i, 'person' || i || '@realcorp.com' FROM generate_series(1, 12) i;
CREATE FUNCTION stall_on_seven() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id = 7 THEN
    PERFORM pg_sleep(60);
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER stall BEFORE UPDATE ON stall_contacts
  FOR EACH ROW EXECUTE FUNCTION stall_on_seven();
`

func stallPlan(t *testing.T, conn *pgx.Conn) masking.Plan {
	t.Helper()
	ctx := context.Background()
	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, rules.Assign(tables), "test")
	require.True(t, plan.Runnable(), masking.DescribeProblems(plan.Problems))
	tp := tablePlanNamed(t, plan, "stall_contacts")
	tp.ChunkSize = 3
	plan.Tables = []masking.TablePlan{tp}
	return plan
}

// interruptInsideAChunk runs the stall table's plan and cancels it once the
// database is asleep inside row seven. dropConnection also terminates the
// connection at that moment.
//
// The stalled backend is terminated afterwards either way. A backend asleep in
// pg_sleep does not notice that its client went away, so it would otherwise hold
// its transaction, and the rows it locked, into whatever runs next.
func interruptInsideAChunk(
	t *testing.T, connString string, checkpoints masking.Checkpointer, dropConnection bool,
) (masking.Result, error) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, connString)
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()
	_, err = conn.Exec(ctx, stallSchema)
	require.NoError(t, err)
	plan := stallPlan(t, conn)

	watcher, err := pgx.Connect(ctx, connString)
	require.NoError(t, err)
	defer func() { _ = watcher.Close(context.Background()) }()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	pid := conn.PgConn().PID()
	stalled := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			var asleep bool
			err := watcher.QueryRow(ctx,
				`SELECT count(*) > 0 FROM pg_stat_activity WHERE pid = $1 AND wait_event = 'PgSleep'`,
				pid).Scan(&asleep)
			if err == nil && asleep {
				cancel()
				if dropConnection {
					_, _ = watcher.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid)
				}
				stalled <- true
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		stalled <- false
	}()

	exec, err := masking.NewExecutor(masking.ExecutorOptions{
		Key: testKey(t), Clock: clock.New(), Checkpoints: checkpoints,
	})
	require.NoError(t, err)
	res, applyErr := exec.Apply(runCtx, conn, plan)
	require.True(t, <-stalled,
		"the run never reached the stalled row, so nothing here is about an interruption inside a chunk")

	_, err = watcher.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid)
	require.NoError(t, err)
	gone := fmt.Sprintf("SELECT count(*) FROM pg_stat_activity WHERE pid = %d", pid)
	for i := 0; i < 250 && queryOne(t, watcher, gone) > 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	return res, applyErr
}

func requireInterrupted(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err, "a cancelled run returned no error")
	msg := err.Error()
	require.Contains(t, msg, "interrupted", "a cancelled run was not reported as interrupted: %s", msg)
	require.Contains(t, msg, "stall_contacts", "the report does not say which table was in flight: %s", msg)
	require.NotContains(t, msg, "EOF", "an interrupted run was reported as a store error: %s", msg)
	require.NotContains(t, msg, "timeout", "an interrupted run was reported as a store error: %s", msg)
	require.ErrorIs(t, err, context.Canceled,
		"an interrupted run's error does not carry the cancellation, so a caller cannot tell: %s", msg)
	var interrupted *masking.InterruptedError
	require.ErrorAs(t, err, &interrupted, "a caller cannot tell an interruption from a store error")
}

func TestApply_AnInterruptedRunSaysSoAndWhetherItResumes(t *testing.T) {
	base, done := requireDatabase(t)
	defer done()
	connString := base.Config().ConnString()

	for _, tc := range []struct {
		name string
		drop bool
	}{
		{name: "the driver reports the cancellation", drop: false},
		{name: "the connection drops as the run is cancelled", drop: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := interruptInsideAChunk(t, connString, nil, tc.drop)
			requireInterrupted(t, err)
			require.Contains(t, err.Error(), "cannot carry on from here",
				"a run with nothing recording its progress did not say it cannot carry on")
			require.NotContains(t, err.Error(), "starts from the beginning",
				"the executor told every caller to run again, which masks a partly masked branch twice")
			require.EqualValues(t, 6, res.Rows, "the rows of the two chunks that committed")
			require.Contains(t, err.Error(), "6 rows", "the report does not say how far the run got")

			// The chunk in flight rolled back, so exactly its rows and the ones
			// after it still hold what production held.
			require.EqualValues(t, 6, queryOne(t, base,
				`SELECT count(*) FROM stall_contacts WHERE email LIKE '%@realcorp%'`))
		})
	}

	t.Run("a run with a checkpoint says it resumes, and it does", func(t *testing.T) {
		checkpoints := &memoryCheckpoints{saved: map[string]string{}}
		_, err := interruptInsideAChunk(t, connString, checkpoints, false)
		requireInterrupted(t, err)
		require.Contains(t, err.Error(), "resumes after",
			"a run whose progress was recorded did not say the next run carries on")

		ctx := context.Background()
		_, err = base.Exec(ctx, `DROP TRIGGER stall ON stall_contacts`)
		require.NoError(t, err)
		exec, err := masking.NewExecutor(masking.ExecutorOptions{
			Key: testKey(t), Clock: clock.New(), Checkpoints: checkpoints,
		})
		require.NoError(t, err)
		res, err := exec.Apply(ctx, base, stallPlan(t, base))
		require.NoError(t, err)
		require.True(t, res.Resumed, "the second run did not pick up from the checkpoint")
		require.EqualValues(t, 6, res.Rows, "the resumed run did not start after the rows that committed")
		require.Zero(t, queryOne(t, base, `SELECT count(*) FROM stall_contacts WHERE email LIKE '%@realcorp%'`))
	})
}
