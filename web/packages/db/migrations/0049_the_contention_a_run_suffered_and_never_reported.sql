-- The contention a concurrent run suffered and could never report.
--
-- WHAT WAS MISSING. 0048 gave the SQL workload its columns and two of them are
-- about contention: deadlocks and serialization_failures. Both of those END a
-- transaction, which is why they could be counted at all: the client got a
-- SQLSTATE back and someone wrote it down. The commonest outcome of lock
-- contention ends nothing. A transaction that queued behind another one for
-- four hundred milliseconds and then committed normally raised no error, was
-- not retried, and left this table looking exactly like a run that never
-- queued at all. So a build that takes a lock a little earlier, or holds it a
-- little longer, moved p95 and every other column here stayed put. The stored
-- history could say a run got slower and could not say it got slower because
-- it blocked.
--
-- WHAT THESE TWO COLUMNS ARE. The engine's watching connection, the one that
-- already samples pg_stat_activity to prove the clients overlapped, now also
-- asks pg_blocking_pids which of the run's own backends are in a lock queue
-- and who is in front of them. lock_waits is how many times one of them was
-- seen to START waiting; lock_wait_ms is one sample interval for every sample
-- any of them was still waiting in, so it is backend milliseconds rather than
-- wall clock. The pairs themselves, meaning which statement waited on which,
-- stay out of this table on purpose: they are per run detail in the same class
-- as the refused statement list, they are served by af load sql and the MCP
-- result, and a comparison between two builds is made on the two numbers.
--
-- WHY THEY ARE NULLABLE AND WHY THAT MATTERS MORE HERE THAN ANYWHERE ELSE IN
-- THIS TABLE. Zero lock waits is the most reassuring thing a row in here can
-- say. peak_open_transactions already carries the rule: NULL means nobody
-- looked and zero means the run was watched and nothing was found. The same
-- rule applies with more riding on it, because a comparison that read a NULL
-- as a zero would call the first watched run a regression from a clean one and
-- an unwatched run a clean build. An instrument that did not run must not be
-- able to produce the answer everybody wants.
--
-- WHY THE SHAPE CONSTRAINT IS UNTOUCHED AND THE COUNTS CONSTRAINT IS NOT.
-- workload_run_results_shape says which kind may carry which columns, and it
-- is written as a list of what must be NULL for each kind. These two columns
-- are optional for sql_workload and absent for everything else, which is
-- exactly what peak_open_transactions and backends_seen already are: neither
-- appears in that constraint either. workload_run_results_counts is the one
-- that says a count cannot be negative, and a column added without an arm
-- there is a column outside the rule the constraint exists to enforce, so it
-- is dropped and rebuilt with both.

BEGIN;

ALTER TABLE workload_run_results
  ADD COLUMN lock_waits   integer,
  ADD COLUMN lock_wait_ms double precision;

ALTER TABLE workload_run_results DROP CONSTRAINT workload_run_results_counts;

ALTER TABLE workload_run_results
  ADD CONSTRAINT workload_run_results_counts CHECK (
    (requests IS NULL OR requests >= 0)
    AND (failures IS NULL OR failures >= 0)
    AND (workflows IS NULL OR workflows >= 0)
    AND (findings IS NULL OR findings >= 0)
    AND (clients IS NULL OR clients > 0)
    AND (transactions IS NULL OR transactions >= 0)
    AND (transactions_failed IS NULL OR transactions_failed >= 0)
    AND (retries IS NULL OR retries >= 0)
    AND (deadlocks IS NULL OR deadlocks >= 0)
    AND (serialization_failures IS NULL OR serialization_failures >= 0)
    AND (statements_run IS NULL OR statements_run >= 0)
    AND (statements_failed IS NULL OR statements_failed >= 0)
    AND (rows_touched IS NULL OR rows_touched >= 0)
    AND (tps IS NULL OR tps >= 0)
    -- Zero is a legal observation and a finding: a run whose peak was one
    -- backend inside a transaction did not rehearse concurrency, whatever its
    -- client count said. NULL is the separate answer for a run nobody watched.
    AND (peak_open_transactions IS NULL OR peak_open_transactions >= 0)
    AND (backends_seen IS NULL OR backends_seen >= 0)
    -- Zero is the measurement this pair exists to be able to make, so it is
    -- legal here and NULL keeps its own meaning. A negative one is a decoder
    -- that inverted a subtraction somewhere, and this is where that stops.
    AND (lock_waits IS NULL OR lock_waits >= 0)
    AND (lock_wait_ms IS NULL OR lock_wait_ms >= 0)
  );

COMMENT ON COLUMN workload_run_results.lock_waits IS
  'How many times one of the run''s own backends was seen to start waiting for a lock, read from pg_blocking_pids by the watching connection while the run was going. NULL means nothing watched the wait queues, which is a different answer from zero: zero is a run that was watched and never queued.';
COMMENT ON COLUMN workload_run_results.lock_wait_ms IS
  'One sample interval for every sample in which one of the run''s backends was still waiting, so it is backend milliseconds and not wall clock. Sampled, so a wait shorter than the interval can be missed entirely and the figure is a floor rather than a total.';

COMMIT;
