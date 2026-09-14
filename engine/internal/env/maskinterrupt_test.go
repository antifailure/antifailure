package env

// AN INTERRUPTED MASKING RUN IS REPORTED AS INTERRUPTED.
//
// Control C during `af golden refresh` printed AF-MSK-010, "Masking could not
// run: masking: writing public.customers: unexpected EOF", and exited 3, the
// status for a configuration error. Nothing was wrong with the configuration or
// with the database. The signal cancelled the context, the connection went down
// with it, and every error out of the executor was wrapped as the one code.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// maskInterruptStall makes the second customer's rewrite sleep, which is inside
// the one chunk a table of two rows is masked in.
const maskInterruptStall = `
CREATE FUNCTION stall_on_second_customer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id = 2 THEN
    PERFORM pg_sleep(60);
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER stall BEFORE UPDATE ON customers
  FOR EACH ROW EXECUTE FUNCTION stall_on_second_customer();
`

func TestMaskDatabase_AnInterruptedRunIsReportedAsInterrupted(t *testing.T) {
	url := maskEventsDatabase(t, maskEventsSchema+maskInterruptStall)
	o := maskEventsOrchestrator(t)
	ctx := context.Background()

	watcher, err := pgx.Connect(ctx, url.Reveal())
	require.NoError(t, err)
	defer func() { _ = watcher.Close(context.Background()) }()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stalled := make(chan int32, 1)
	go func() {
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			var pid int32
			err := watcher.QueryRow(ctx, `SELECT pid FROM pg_stat_activity
				WHERE datname = current_database() AND wait_event = 'PgSleep' LIMIT 1`).Scan(&pid)
			if err == nil {
				cancel()
				// The connection goes down with the cancellation, which is what
				// turned the driver's report into unexpected EOF.
				_, _ = watcher.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid)
				stalled <- pid
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		stalled <- 0
	}()

	_, _, err = o.maskDatabase(
		runCtx, maskPolicySession(t, o), url, maskEventsKey(t), maskEventsRules(t), "h1")
	require.NotZero(t, <-stalled, "masking never reached the stalled row, so this measured no interruption")

	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded), "an interrupted run returned an error with no code: %v", err)
	require.NotEqual(t, "AF-MSK-010", string(coded.Code()),
		"an interrupted run was reported as masking that could not run: %v", err)
	require.Equal(t, "AF-MSK-016", string(coded.Code()))
	require.Equal(t, aferrors.ExitInterruptedClean, aferrors.ExitCodeOf(err),
		"an interrupted run exits with a status that does not say it was interrupted")
	require.Contains(t, coded.Message(), "interrupted")
	require.Contains(t, coded.Message(), "fresh copy of the source",
		"an interrupted refresh does not say that running it again is safe")
	require.NotContains(t, err.Error(), "EOF", "an interrupted run was reported as a store error: %v", err)
	require.ErrorIs(t, err, context.Canceled)
}
