-- A fifth workload kind, and the shape check that would have accepted it
-- silently.
--
-- WHAT IS BEING ADDED. Everything the engine could run against a preview
-- environment went over HTTP: a weighted mix, a declared journey, a browser
-- workflow, a seeded wander. All four reach the database only through the
-- application, so every number they produce is the application's latency with
-- the database somewhere inside it. That is the right measurement for an
-- application change and the wrong one for a database change: somebody
-- altering an index, a lock, a storage parameter or a query wants transactions
-- per second and the cost of one statement, and could only ever reach those
-- through whatever the application happens to do on a route they can call.
-- sql_workload is clients on their own connections running whole transactions
-- against the branch's database, so it measures the database.
--
-- WHY THE CHECK IS DROPPED AND REBUILT RATHER THAN LEFT ALONE, and this is the
-- part that matters more than the new columns.
--
-- 0026's workload_run_results_shape is a CASE with no ELSE. A CASE with no ELSE
-- returns NULL for an unmatched value, and a CHECK constraint that evaluates to
-- NULL PASSES. So adding a value to workload_kind without touching that
-- constraint does not leave the new kind partly constrained: it leaves it
-- entirely unconstrained, while the constraint's own name and 0026's header go
-- on claiming that a result cannot be written in the wrong shape. A browser
-- result carrying a request count is refused; a sql_workload result carrying
-- one, a session count, a workflow count and a finding count all at once would
-- have been accepted. That is a check that cannot say no about the one kind
-- nobody has read the code for yet, which is exactly the class of defect this
-- repository keeps finding in its own instruments.
--
-- The rebuilt constraint therefore ends in ELSE false. A sixth kind added later
-- without a matching arm now fails its first INSERT with this constraint's
-- name on it, rather than quietly gaining an exemption from the rule the table
-- exists to enforce.
--
-- WHY IT COMPARES kind::text RATHER THAN THE ENUM. Postgres refuses to USE a
-- newly added enum label in the same transaction that added it, and a
-- migration file here is one transaction: it opens with BEGIN and closes with
-- COMMIT. `CASE kind WHEN 'sql_workload'` resolves that literal to the enum
-- label and is refused with "unsafe use of new value". Casting the column to
-- text makes both sides text, so no enum label is referenced and the whole
-- change lands in one file. Verified against a real Postgres 18 rather than
-- reasoned about: the enum form raises 55P04 and the text form commits.
--
-- WHAT THE NEW COLUMNS ARE, AND THE TWO THAT ARE NOT COUNTS.
--
-- transactions, transactions_failed and retries are three columns rather than
-- two, for the reason 0026 gives about the five workflow counts. A transaction
-- that deadlocked and committed on its second attempt is a success whose run
-- is contended, and a table that could only say committed or failed would
-- record it as a clean run. deadlocks and serialization_failures are separated
-- from the error map for the same reason: they are the two outcomes a
-- concurrent workload exists to provoke, and a reader should not have to know
-- their SQLSTATE to find them.
--
-- rows_touched is the honesty column. A workload derived from
-- pg_stat_statements cannot recover the parameter VALUES, because the view
-- normalises them away, so it binds values of the types the server reported.
-- A selective predicate filled that way may match no rows, and a run of forty
-- thousand statements that touched nothing measured the cost of finding
-- nothing. That is a real measurement of an index and it is not a measurement
-- of the customer's result sets. Without this column the two are
-- indistinguishable and the one that found nothing looks like the fast one.
--
-- peak_open_transactions and backends_seen are the evidence rather than the
-- claim, and they are nullable on purpose. They are what the SERVER reported
-- about the run while it was going, read from pg_stat_activity by a separate
-- connection: how many of the run's own backends were inside a transaction at
-- one instant, and how many distinct backends it ever held. N goroutines are
-- not N concurrent sessions. A run whose observer could not connect writes
-- NULL here and never zero, because "no overlap" is a finding and "nobody
-- looked" is not, and a console given a zero for both cannot tell them apart.
--
-- WHAT IS DELIBERATELY REUSED. p50_ms through max_ms hold the latency of a
-- committed TRANSACTION, which is this kind's unit of work the way a request is
-- the mix's. A percentile is a percentile, the comparison code differences
-- these columns by name, and a second set of millisecond columns would be a
-- second thing for every consumer to learn and a second place to disagree.
-- error_rate holds the share of transactions that failed, over commits plus
-- failures, with retries in neither: a deadlock retried into a commit is what a
-- correct application does about a deadlock, and counting it as a failure would
-- make every honestly written concurrent workload report a failing error rate.
--
-- WHAT IS DELIBERATELY NOT REUSED. requests stays NULL. A SQL workload sends no
-- requests, and filling a request count with a statement count is exactly the
-- conflation load.thresholds.query_count_increase was deprecated for: a load
-- run counts requests, not statements.

BEGIN;

ALTER TYPE workload_kind ADD VALUE IF NOT EXISTS 'sql_workload';

ALTER TABLE workload_run_results
  ADD COLUMN clients                 integer,
  ADD COLUMN transactions            integer,
  ADD COLUMN transactions_failed     integer,
  ADD COLUMN retries                 integer,
  ADD COLUMN deadlocks               integer,
  ADD COLUMN serialization_failures  integer,
  ADD COLUMN statements_run          integer,
  ADD COLUMN statements_failed       integer,
  ADD COLUMN rows_touched            bigint,
  ADD COLUMN tps                     double precision,
  ADD COLUMN peak_open_transactions  integer,
  ADD COLUMN backends_seen           integer;

ALTER TABLE workload_run_results DROP CONSTRAINT workload_run_results_shape;

ALTER TABLE workload_run_results
  ADD CONSTRAINT workload_run_results_shape CHECK (
    CASE kind::text
      WHEN 'observed_load' THEN
        requests IS NOT NULL AND sessions IS NULL AND workflows IS NULL AND findings IS NULL
        AND transactions IS NULL
      WHEN 'http_scenario' THEN
        requests IS NOT NULL AND sessions IS NOT NULL AND workflows IS NULL AND findings IS NULL
        AND transactions IS NULL
      WHEN 'browser_workflow' THEN
        workflows IS NOT NULL AND requests IS NULL AND sessions IS NULL AND findings IS NULL
        AND transactions IS NULL
      WHEN 'exploration' THEN
        findings IS NOT NULL AND goals IS NOT NULL
        AND requests IS NULL AND sessions IS NULL AND workflows IS NULL
        AND transactions IS NULL
      WHEN 'sql_workload' THEN
        transactions IS NOT NULL AND clients IS NOT NULL
        AND requests IS NULL AND sessions IS NULL AND workflows IS NULL AND findings IS NULL
      -- A sixth kind with no arm here fails its first insert with this
      -- constraint's name on it. The CASE this replaces returned NULL for an
      -- unmatched kind, and a CHECK that evaluates to NULL passes, so the new
      -- kind would have been exempt from the rule rather than covered by it.
      ELSE false
    END
  );

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
  );

COMMENT ON COLUMN workload_run_results.rows_touched IS
  'How many rows the run''s statements returned or changed. It is what says whether a derived mix''s generated parameters matched anything: a run that touched no rows measured the cost of finding nothing, which is a real measurement of an index and is not a measurement of the result sets.';
COMMENT ON COLUMN workload_run_results.peak_open_transactions IS
  'The most of the run''s own backends the server reported inside a transaction at one instant, read from pg_stat_activity while it ran. NULL means nobody looked, which is a different answer from zero.';
COMMENT ON COLUMN workload_run_results.backends_seen IS
  'How many distinct backends of this run the server ever reported. It is what proves N clients held N sessions rather than sharing one.';

COMMIT;
