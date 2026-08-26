-- Events, partitioned by month, without giving up exactly-once ingestion.
--
-- The events table is the one table with no natural ceiling: every environment
-- that has ever come up wrote to it and nothing ever deletes from it. Deleting
-- a year of rows from a single heap is a long transaction, a table lock, and a
-- vacuum afterwards. Dropping a partition is a catalogue update. That is the
-- whole reason for this file.
--
-- The part that took two attempts is which column to partition on.
--
-- Postgres will not enforce a unique constraint that omits the partition key,
-- and ingestion depends on one:
--
--   INSERT INTO events (...) VALUES (...)
--   ON CONFLICT (org_id, idempotency_key) DO NOTHING
--
-- That constraint is what makes a retry safe. An engine that sent a batch and
-- lost the response cannot know which half landed, so it sends the batch again
-- and the database drops the copy rather than the sender having to get it
-- right.
--
-- Partitioning on received_at was the obvious choice and it is wrong. received_at
-- is assigned here, by now(), so the retry gets a different value from the first
-- attempt, the constraint becomes (org_id, idempotency_key, received_at), the
-- conflict never fires, and every retry duplicates silently.
--
-- occurred_at is assigned by the sender when the event happened. The engine
-- stamps it once and passes it through unchanged on every resend, so an attempt
-- and its retry carry the same value and the conflict fires exactly as before.
--
-- The usual objection to partitioning on a value a client supplies is that a
-- skewed clock invents partitions forever. That is already handled upstream:
-- ingestion rejects occurred_at more than a day in the future or more than a
-- year in the past before a row ever reaches this table, so the live range is
-- bounded at [now - 1 year, now + 1 day] no matter what a sender claims.
--
-- A DEFAULT partition catches anything outside the months that exist, so a late
-- event is stored rather than erroring at the sender. Because the manager always
-- keeps future months created ahead of time, nothing in the future ever lands in
-- DEFAULT, which means adding a month never has to scan it for conflicting rows.

BEGIN;

-- Renamed out of the way rather than dropped first, because the rows have to be
-- copied across. The constraints are renamed too: Postgres would otherwise
-- resolve the collision by appending a digit, and a schema containing
-- events_pkey1 reads like an accident nobody noticed.
ALTER TABLE events RENAME TO events_unpartitioned;
ALTER INDEX events_env_sequence_idx RENAME TO events_unpartitioned_env_sequence_idx;
ALTER INDEX events_received_idx RENAME TO events_unpartitioned_received_idx;
ALTER TABLE events_unpartitioned RENAME CONSTRAINT events_pkey TO events_unpartitioned_pkey;
ALTER TABLE events_unpartitioned RENAME CONSTRAINT events_org_id_fkey TO events_unpartitioned_org_id_fkey;
ALTER TABLE events_unpartitioned RENAME CONSTRAINT events_environment_id_fkey TO events_unpartitioned_environment_id_fkey;
ALTER TABLE events_unpartitioned RENAME CONSTRAINT events_run_id_fkey TO events_unpartitioned_run_id_fkey;
ALTER TABLE events_unpartitioned RENAME CONSTRAINT events_org_id_idempotency_key_key TO events_unpartitioned_idempotency_key;

CREATE TABLE events (
  id              uuid NOT NULL DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  -- The sender's identifier for the event. Retries after a timeout are the
  -- normal case, not the exceptional one, so the second copy is dropped by
  -- the unique constraint rather than by anything the sender has to get right.
  idempotency_key text NOT NULL,
  env_id          text,
  environment_id  uuid REFERENCES environments(id) ON DELETE CASCADE,
  run_id          uuid REFERENCES runs(id) ON DELETE CASCADE,
  -- Assigned by the sender, monotonic within an environment. Ordering is by
  -- this and never by arrival, because arrival order is a property of the
  -- network.
  sequence        bigint NOT NULL DEFAULT 0,
  type            text NOT NULL,
  payload         jsonb NOT NULL DEFAULT '{}'::jsonb,
  -- The partition key. Assigned by the sender, stable across a resend, and
  -- bounded by ingestion before it arrives here.
  occurred_at     timestamptz NOT NULL,
  received_at     timestamptz NOT NULL DEFAULT now(),
  -- Both keys carry occurred_at because Postgres requires the partition key in
  -- every unique constraint on a partitioned table. Adding it to the
  -- idempotency key costs nothing precisely because it does not vary between an
  -- attempt and its retry.
  PRIMARY KEY (id, occurred_at),
  UNIQUE (org_id, idempotency_key, occurred_at)
) PARTITION BY RANGE (occurred_at);

CREATE INDEX events_env_sequence_idx ON events (org_id, env_id, sequence);
CREATE INDEX events_received_idx ON events (org_id, received_at DESC);

-- ---------------------------------------------------------------------------
-- Partitions
--
-- Created here for every month the existing rows fall in, plus the current
-- month and the two after it, so that an installation is never one insert away
-- from needing DDL. The manager in src/partitions.ts keeps it that way.
-- ---------------------------------------------------------------------------

CREATE TABLE events_default PARTITION OF events DEFAULT;

DO $$
DECLARE
  m date;
BEGIN
  -- date_trunc rather than a series over the raw values, so that two rows in
  -- the same month do not try to create the same partition twice.
  FOR m IN
    SELECT DISTINCT date_trunc('month', occurred_at AT TIME ZONE 'UTC')::date AS month
    FROM events_unpartitioned
    UNION
    SELECT (date_trunc('month', now() AT TIME ZONE 'UTC') + (n || ' months')::interval)::date
    FROM generate_series(0, 2) AS n
    ORDER BY 1
  LOOP
    EXECUTE format(
      'CREATE TABLE %I PARTITION OF events FOR VALUES FROM (%L) TO (%L)',
      'events_' || to_char(m, 'YYYY_MM'), m, (m + interval '1 month')::date);
  END LOOP;
END
$$;

INSERT INTO events (id, org_id, idempotency_key, env_id, environment_id, run_id,
                    sequence, type, payload, occurred_at, received_at)
SELECT id, org_id, idempotency_key, env_id, environment_id, run_id,
       sequence, type, payload, occurred_at, received_at
FROM events_unpartitioned;

DROP TABLE events_unpartitioned;

-- ---------------------------------------------------------------------------
-- Grants and isolation
--
-- Recreating the table dropped what 0002 gave it, so both come back here. The
-- policy is applied to the partitions as well as the parent. Reading through
-- the parent applies the parent's policy and never a partition's, so this
-- changes nothing about the application's path; it is here so that a query
-- naming a partition directly is isolated by exactly the same rule.
-- ---------------------------------------------------------------------------

GRANT SELECT, INSERT, UPDATE, DELETE ON events TO antifailure_app;

DO $$
DECLARE
  t text;
  tables text[];
BEGIN
  SELECT array_agg(c.relname ORDER BY c.relname) INTO tables
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'public'
    AND (c.relname = 'events'
         OR c.oid IN (SELECT inhrelid FROM pg_inherits
                      WHERE inhparent = 'public.events'::regclass));

  FOREACH t IN ARRAY tables
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format($p$
      CREATE POLICY tenant_isolation ON %I
        FOR ALL TO antifailure_app
        USING (org_id = current_org())
        WITH CHECK (org_id = current_org())
    $p$, t);
  END LOOP;
END
$$;

COMMIT;
