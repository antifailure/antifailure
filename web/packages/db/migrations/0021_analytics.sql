-- Analytics, as a closed stream that cannot become a second copy of the product.
--
-- WHY A SEPARATE TABLE RATHER THAN THE ONE THAT ALREADY EXISTS.
--
-- `events` is the engine's stream. Its payload is jsonb with no schema, on
-- purpose: an older control plane has to accept a newer engine's events without
-- refusing them, so anything a sender puts in the payload is stored. That is
-- correct for ingestion and it is exactly wrong for analytics, because it means
-- the shape of what is kept is decided by whoever sends it. A repository name,
-- a branch, a preview URL and a runtime string are all in there today.
--
-- So analytics gets its own table, and the property that makes it safe is that
-- the schema is CLOSED. An event whose name is not in the catalog is refused
-- and counted. A payload field the catalog does not declare is refused and
-- counted. Neither is stored and then filtered later, because a store that
-- accepts anything is a store somebody eventually queries for the thing nobody
-- meant to keep.
--
-- WHAT IS DELIBERATELY NOT HERE.
--
-- No raw URL, no query string, no free-form referrer, no IP address, no user
-- agent, no email, no name, no repository, no branch, no SQL, no log line, no
-- request body, no DOM text, no prompt, no model response, no screenshot, no
-- trace, no token, no secret, no card. A referrer becomes a bounded source enum
-- and a bounded campaign id at the edge, before anything reaches here, and the
-- raw value is never persisted anywhere.
--
-- WHY THE ORGANIZATION IS A SURROGATE AND NOT AN org_id.
--
-- Two reasons, and the second is the one that decided it.
--
-- First, this table answers questions across organizations: how many reached a
-- first proven run, what the plan mix is, which acquisition source produced
-- customers who stayed. Row-level security keys off a per-transaction setting,
-- so a cross-organization aggregate needs a role that can read every
-- organization's rows. Creating one to draw a graph would put the strongest
-- read in the system on the least important path, which is the argument
-- /metrics already makes for touching no table at all.
--
-- Second, and this is the point: a keyed hash is a one-way door. The analytics
-- store can count organizations and follow one through a funnel, and it cannot
-- name one without the key that lives in the application's environment. That is
-- a property somebody can check by reading this file, which "we only query it
-- carefully" is not.
--
-- WHY THE APPLICATION CANNOT READ THIS TABLE.
--
-- GRANT below is INSERT and nothing else. The application writes the stream and
-- can never read it back; only the rollup, which runs as the owner, reads it,
-- and only aggregates come out. A SELECT by the application role therefore
-- raises 42501 rather than returning zero rows, which is the difference between
-- a mistake somebody sees and a mistake somebody ships. Row-level security is
-- enabled on top of that with an insert-only policy, so a future GRANT that
-- somebody adds without thinking still does not open reads.

BEGIN;

-- ---------------------------------------------------------------------------
-- The stream
-- ---------------------------------------------------------------------------

CREATE TABLE analytics_events (
  -- The sender's identifier. A retry after a lost response is the normal case
  -- for the site beacon and for an engine, so the second copy is dropped by a
  -- unique constraint rather than by anything the sender has to get right.
  event_id        text NOT NULL,
  -- The catalog name. Enforced in the application against a closed list, and
  -- bounded here so a name that got past it cannot be unbounded text.
  name            text NOT NULL,
  -- The payload shape this row was written against. An event whose payload
  -- changes incompatibly gets a new version rather than a new meaning for the
  -- same fields, so a chart over a year of rows can say which shape it is
  -- reading.
  version         smallint NOT NULL DEFAULT 1,
  -- Assigned by the sender when the thing happened, and the partition key. See
  -- 0011 for why a partition key must be the value that survives a resend:
  -- received_at is assigned here, so a retry would get a different one and the
  -- conflict would never fire.
  occurred_at     timestamptz NOT NULL,
  received_at     timestamptz NOT NULL DEFAULT now(),
  source          text NOT NULL,
  -- HMAC of the organization id under a key held by the application. Null for
  -- anything that happened before an organization existed, which is most of
  -- acquisition.
  org_surrogate   text,
  -- HMAC of a short-lived anonymous session identifier that lives for one
  -- browsing session and is never persisted across visits. Null everywhere
  -- except the site beacon.
  session_surrogate text,
  actor_kind      text NOT NULL,
  privacy_basis   text NOT NULL,
  -- The consent record this row was collected under, when the basis is
  -- consent. Nothing emits consent today; the constraint below is what makes
  -- the day something does a correct pairing rather than a convention.
  consent_id      text,
  payload         jsonb NOT NULL DEFAULT '{}'::jsonb,

  -- occurred_at is in both keys because Postgres requires the partition key in
  -- every unique constraint on a partitioned table. It costs nothing: the
  -- sender stamps it once and resends it unchanged.
  PRIMARY KEY (event_id, occurred_at),

  CONSTRAINT analytics_events_source_is_known CHECK (
    source IN ('site', 'console', 'engine', 'control_plane')),
  CONSTRAINT analytics_events_actor_is_known CHECK (
    actor_kind IN ('visitor', 'user', 'engine', 'system')),
  CONSTRAINT analytics_events_basis_is_known CHECK (
    privacy_basis IN ('legitimate_interest', 'contract', 'consent')),
  -- A consent identifier without a consent basis is a row nobody can explain,
  -- and a consent basis without an identifier is a claim with no record behind
  -- it. Both directions are refused.
  CONSTRAINT analytics_events_consent_is_paired CHECK (
    (privacy_basis = 'consent') = (consent_id IS NOT NULL)),
  -- Bounds, so that a bug upstream cannot turn a closed vocabulary into free
  -- text. Every one of these is far above the longest real value.
  CONSTRAINT analytics_events_ids_are_bounded CHECK (
    length(event_id) BETWEEN 1 AND 100
    AND length(name) BETWEEN 1 AND 64
    AND (org_surrogate IS NULL OR length(org_surrogate) = 32)
    AND (session_surrogate IS NULL OR length(session_surrogate) = 32)
    AND (consent_id IS NULL OR length(consent_id) BETWEEN 1 AND 100)),
  CONSTRAINT analytics_events_version_is_positive CHECK (version > 0)
) PARTITION BY RANGE (occurred_at);

-- The rollup reads one day at a time, grouped by name. Nothing else reads this
-- table at all, so there is one index and it is the one the rollup uses.
CREATE INDEX analytics_events_day_idx ON analytics_events (occurred_at, name);

CREATE TABLE analytics_events_default PARTITION OF analytics_events DEFAULT;

DO $$
DECLARE
  m date;
BEGIN
  FOR m IN
    SELECT (date_trunc('month', now() AT TIME ZONE 'UTC') + (n || ' months')::interval)::date
    FROM generate_series(0, 3) AS n
    ORDER BY 1
  LOOP
    EXECUTE format(
      'CREATE TABLE %I PARTITION OF analytics_events FOR VALUES FROM (%L) TO (%L)',
      'analytics_events_' || to_char(m, 'YYYY_MM'), m, (m + interval '1 month')::date);
  END LOOP;
END
$$;

-- ---------------------------------------------------------------------------
-- Organization facts
--
-- One row per organization surrogate, holding the milestones a funnel is drawn
-- from and nothing else. Dates rather than timestamps, because the question is
-- "did this organization reach a first proven run, and in which week", and a
-- timestamp answers a question nobody asked at the cost of being more
-- identifying than a date.
--
-- WHY MILESTONES ARE COLUMNS HERE AND NOT EVENTS IN THE STREAM.
--
-- "The first time this organization proved something" is the number the whole
-- funnel is aimed at, and it was an event first. It cannot be one. Emitting it
-- means deciding, at write time, whether this is the first, which is a read and
-- then a write, and two batches arriving at once both read null and both emit.
-- Worse, a batch that arrives late carrying an OLDER verdict has to move the
-- milestone earlier, and an event already written cannot be unwritten.
--
-- As a column it is `LEAST(existing, incoming)`, which is one statement, has no
-- race, and converges to the same date whatever order the events arrive in.
-- Postgres's LEAST ignores NULLs, so the first value to arrive sets it and only
-- an earlier one moves it.
--
-- So the rule this table is built on: EVENTS ARE COUNTS, FACTS ARE MILESTONES.
-- A count is commutative and a milestone is not, and mixing them is how a
-- dashboard ends up with two different answers to "how many activated".
-- ---------------------------------------------------------------------------

CREATE TABLE analytics_org_facts (
  org_surrogate         text PRIMARY KEY,
  first_seen_on         date NOT NULL,
  last_active_on        date NOT NULL,
  first_event_on        date,
  first_environment_on  date,
  first_proven_run_on   date,
  first_paid_on         date,
  -- The plan as of the last event seen. A closed vocabulary the application
  -- validates; bounded here so it cannot become free text.
  plan                  text,
  environments_created  bigint NOT NULL DEFAULT 0,
  runs_finished         bigint NOT NULL DEFAULT 0,
  updated_at            timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT analytics_org_facts_surrogate_is_a_hash CHECK (length(org_surrogate) = 32),
  CONSTRAINT analytics_org_facts_plan_is_bounded CHECK (plan IS NULL OR length(plan) <= 32),
  CONSTRAINT analytics_org_facts_counts_do_not_go_negative CHECK (
    environments_created >= 0 AND runs_finished >= 0)
);

CREATE INDEX analytics_org_facts_first_seen_idx ON analytics_org_facts (first_seen_on);
CREATE INDEX analytics_org_facts_last_active_idx ON analytics_org_facts (last_active_on);

-- ---------------------------------------------------------------------------
-- Daily aggregates
--
-- What the dashboard reads, and the only analytics table the application may
-- SELECT. There are no surrogates in it: a row is a count, and a count of
-- distinct organizations and sessions computed by the rollup and then thrown
-- away.
--
-- Two dimensions, not an open key-value bag. Each event in the catalog declares
-- which two of its payload fields roll up, so "which acquisition source landed
-- on which page" is one row rather than a join, and an event cannot quietly
-- start carrying a third dimension nobody looked at.
--
-- The empty string rather than NULL for an absent dimension, because the
-- primary key has to distinguish rows and NULL is not equal to itself.
-- ---------------------------------------------------------------------------

CREATE TABLE analytics_daily (
  day           date NOT NULL,
  name          text NOT NULL,
  dim_a         text NOT NULL DEFAULT '',
  dim_b         text NOT NULL DEFAULT '',
  events        bigint NOT NULL DEFAULT 0,
  organizations bigint NOT NULL DEFAULT 0,
  sessions      bigint NOT NULL DEFAULT 0,
  computed_at   timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (day, name, dim_a, dim_b),
  CONSTRAINT analytics_daily_counts_do_not_go_negative CHECK (
    events >= 0 AND organizations >= 0 AND sessions >= 0),
  CONSTRAINT analytics_daily_dimensions_are_bounded CHECK (
    length(name) <= 64 AND length(dim_a) <= 64 AND length(dim_b) <= 64)
);

CREATE INDEX analytics_daily_day_idx ON analytics_daily (day DESC, name);

-- ---------------------------------------------------------------------------
-- The rollup watermark
--
-- One row, so a rollup that has never run is distinguishable from one that ran
-- and found nothing. A dashboard that cannot tell those apart shows an empty
-- chart for both, and only one of them is a working system.
-- ---------------------------------------------------------------------------

CREATE TABLE analytics_rollup_state (
  id            boolean PRIMARY KEY DEFAULT true,
  last_run_at   timestamptz,
  -- The oldest day the last run recomputed. A day older than this is settled;
  -- a day newer is still absorbing late arrivals.
  settled_after date,
  CONSTRAINT analytics_rollup_state_is_one_row CHECK (id)
);

INSERT INTO analytics_rollup_state (id) VALUES (true);

-- ---------------------------------------------------------------------------
-- Grants
--
-- INSERT on the stream and nothing else, so the application cannot read back
-- what it wrote. SELECT on the aggregates, which is what the dashboard needs.
-- The facts table is read as well as written, because deciding whether an
-- organization has already had its first proven run is a read, and doing that
-- read here is what keeps the milestone correct under any arrival order.
-- ---------------------------------------------------------------------------

GRANT INSERT ON analytics_events TO antifailure_app;
GRANT SELECT, INSERT, UPDATE ON analytics_org_facts TO antifailure_app;
GRANT SELECT ON analytics_daily TO antifailure_app;
GRANT SELECT ON analytics_rollup_state TO antifailure_app;

-- ---------------------------------------------------------------------------
-- Isolation
--
-- None of these tables carries an org_id, and that is deliberate rather than an
-- omission: an org_id would make the table joinable back to a customer, which
-- is the property the surrogate exists to remove. So tenant isolation does not
-- apply and the protection is the grant above plus these policies.
--
-- The stream gets an insert-only policy. With no SELECT grant a read already
-- raises, and this is the second lock: a GRANT SELECT added later by somebody
-- who did not read this file still returns nothing rather than everything.
--
-- The aggregates get a read-only policy for the same shape of reason. They hold
-- no identifier of any kind, so there is nothing to confine them to; what the
-- policy prevents is a write path appearing that this file did not authorise.
-- ---------------------------------------------------------------------------

ALTER TABLE analytics_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_events FORCE ROW LEVEL SECURITY;
CREATE POLICY analytics_stream_is_write_only ON analytics_events
  FOR INSERT TO antifailure_app WITH CHECK (true);

ALTER TABLE analytics_events_default ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_events_default FORCE ROW LEVEL SECURITY;
CREATE POLICY analytics_stream_is_write_only ON analytics_events_default
  FOR INSERT TO antifailure_app WITH CHECK (true);

DO $$
DECLARE
  t text;
BEGIN
  FOR t IN
    SELECT c.relname FROM pg_class c
    WHERE c.oid IN (SELECT inhrelid FROM pg_inherits
                    WHERE inhparent = 'public.analytics_events'::regclass)
      AND c.relname <> 'analytics_events_default'
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format(
      'CREATE POLICY analytics_stream_is_write_only ON %I FOR INSERT TO antifailure_app WITH CHECK (true)',
      t);
  END LOOP;
END
$$;

ALTER TABLE analytics_org_facts ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_org_facts FORCE ROW LEVEL SECURITY;
CREATE POLICY analytics_facts_are_de_identified ON analytics_org_facts
  FOR ALL TO antifailure_app USING (true) WITH CHECK (true);

ALTER TABLE analytics_daily ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_daily FORCE ROW LEVEL SECURITY;
CREATE POLICY analytics_daily_is_read_only ON analytics_daily
  FOR SELECT TO antifailure_app USING (true);

ALTER TABLE analytics_rollup_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_rollup_state FORCE ROW LEVEL SECURITY;
CREATE POLICY analytics_rollup_state_is_read_only ON analytics_rollup_state
  FOR SELECT TO antifailure_app USING (true);

COMMIT;
