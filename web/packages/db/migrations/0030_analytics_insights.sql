-- The three insights a daily count cannot produce, and the one table they are
-- all computed from.
--
-- WHAT 0029 CAN ANSWER, AND WHERE IT STOPS.
--
-- analytics_daily holds a count per day, per event, per two declared
-- dimensions. That answers "how many page views yesterday, by channel" exactly,
-- and it answers nothing that needs to follow one subject from one day to
-- another or from one event to another. Three questions a product actually runs
-- on fall the wrong side of that line:
--
--   How many distinct organizations were active this WEEK. Summing the daily
--   distinct counts double counts anybody active on two days, which is why
--   read.ts returns the daily peak instead and says so in a comment. A peak is
--   a true number and it is not the number anybody wants.
--
--   How many sessions that landed went on to press the button and then to
--   submit. Every one of those three events exists, is emitted and is counted,
--   and nothing joins them, because analytics_daily counts each name on its own
--   and throws away which session did which.
--
--   Of the organizations that arrived in a given week, how many were still
--   doing anything four weeks later. read.ts answers a nearby question, three
--   scalars over last_active_on, and says in its own comment that it is
--   deliberately not a cohort grid.
--
-- All three have ONE cause: the rollup groups by a surrogate and then discards
-- it. So this migration adds the table that keeps it.
--
-- WHY THE APPLICATION STILL CANNOT READ A SURROGATE.
--
-- This is the part that decides the whole design, and the obvious shape gets it
-- wrong. The obvious shape is to keep a subject level table and let the
-- dashboard query it, because then any insight can be written later without a
-- migration. That would hand the application the ability to follow one
-- organization through a funnel by hand, which is precisely the ability 0029
-- exists to withhold, and it would withhold it only by the dashboard choosing
-- not to write that query.
--
-- So analytics_subject_days below is granted to NOBODY. It is the rollup's own
-- working set, read and written by the owner, and the three tables after it
-- hold nothing but counts. A dashboard cannot follow a subject because the rows
-- it can read do not contain one, which is a property somebody can check by
-- reading the GRANT statements at the bottom of this file.
--
-- The cost is that a new insight needs a new materialization here rather than a
-- new query in TypeScript. That is the same trade the event catalog makes, for
-- the same reason, and this repository has already decided it once.

BEGIN;

-- ---------------------------------------------------------------------------
-- The working set
--
-- One row per subject per event name per day. No time of day, no dimensions,
-- no payload: the question is only WHETHER this subject did this thing on this
-- day, because that is what a distinct count over a window and a retention
-- cohort both need and it is all they need.
--
-- WHY NO TIME OF DAY. A funnel needs ordering, so the obvious move is to keep
-- the first timestamp per row. It is not kept, for two reasons. It is more
-- identifying than a date, which is the argument analytics_org_facts already
-- makes for holding dates. And it would not be enough anyway: a subject that
-- did a step twice in a day has two candidate times and one stored, so an
-- ordered funnel over first timestamps is wrong in exactly the case that
-- matters. The funnel below is computed over the raw stream at full precision
-- instead, where both problems disappear.
-- ---------------------------------------------------------------------------

CREATE TABLE analytics_subject_days (
  -- Which population this subject belongs to. Sessions and organizations are
  -- never mixed in one count: a session is a browsing visit that by
  -- construction never returns, and an organization is a customer that might.
  subject_kind  text NOT NULL,
  -- The surrogate from analytics_events, unchanged. Never granted out.
  subject       text NOT NULL,
  name          text NOT NULL,
  day           date NOT NULL,
  events        bigint NOT NULL DEFAULT 0,
  computed_at   timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (subject_kind, subject, name, day),

  CONSTRAINT analytics_subject_days_kind_is_known CHECK (
    subject_kind IN ('organization', 'session')),
  CONSTRAINT analytics_subject_days_subject_is_a_hash CHECK (length(subject) = 32),
  CONSTRAINT analytics_subject_days_name_is_bounded CHECK (length(name) BETWEEN 1 AND 64),
  CONSTRAINT analytics_subject_days_events_do_not_go_negative CHECK (events >= 0)
);

-- Every distinct subject in a window, for any event. The column order is the
-- query's: equality on the kind, a range on the day, and the subject last so
-- the distinct count is an index-only scan rather than a heap visit per row.
CREATE INDEX analytics_subject_days_window_idx
  ON analytics_subject_days (subject_kind, day, subject);

-- The same, narrowed to one event name.
CREATE INDEX analytics_subject_days_event_window_idx
  ON analytics_subject_days (subject_kind, name, day, subject);

-- ---------------------------------------------------------------------------
-- Distinct subjects over a window
--
-- The counts the working set exists to produce, materialized because the
-- application cannot compute them itself. One row per day per window per
-- population per event, where the day is the LAST day of the window: "as of
-- this day, this many distinct organizations were active in the preceding
-- twenty eight".
--
-- The empty string for `name` means every event, which is the number usually
-- meant by monthly actives. The empty string rather than NULL for the same
-- reason analytics_daily uses it: NULL is not equal to itself and the primary
-- key has to tell rows apart.
-- ---------------------------------------------------------------------------

CREATE TABLE analytics_actives (
  day           date NOT NULL,
  -- How many days the window covers, ending on and including `day`.
  window_days   smallint NOT NULL,
  subject_kind  text NOT NULL,
  name          text NOT NULL DEFAULT '',
  subjects      bigint NOT NULL DEFAULT 0,
  computed_at   timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (day, window_days, subject_kind, name),

  CONSTRAINT analytics_actives_window_is_positive CHECK (window_days > 0),
  CONSTRAINT analytics_actives_kind_is_known CHECK (
    subject_kind IN ('organization', 'session')),
  CONSTRAINT analytics_actives_name_is_bounded CHECK (length(name) <= 64),
  CONSTRAINT analytics_actives_subjects_do_not_go_negative CHECK (subjects >= 0)
);

CREATE INDEX analytics_actives_day_idx ON analytics_actives (day DESC, subject_kind, window_days);

-- ---------------------------------------------------------------------------
-- Retention, as a cohort grid
--
-- One row per cohort week per return week. `cohort_week` is the Monday of the
-- week a subject was first seen, `weeks_later` is how many weeks after that
-- week the subject did anything, and `subjects` is how many did.
--
-- weeks_later = 0 is the cohort's own size, so every grid row divides by the
-- row's own zero column and no separate denominator has to be kept in step.
--
-- WHY WEEKS AND NOT DAYS. A daily grid over a quarter is ninety by ninety, most
-- of its cells hold nothing, and the ones that hold something hold one or two
-- subjects, which is close enough to naming them. read.ts already refused a
-- grid for that reason. A weekly grid over a quarter is twelve by twelve and
-- its cells are populations rather than individuals, which is the shape that
-- makes a grid safe to publish as well as the shape that makes it readable.
--
-- SMALL CELLS ARE STILL SUPPRESSED. A cell below the floor in the rollup is
-- written as its true count and the read layer reports it as suppressed, so the
-- suppression is a display rule over a true number rather than a lie in the
-- table. Which of those it is matters the day somebody changes the floor.
-- ---------------------------------------------------------------------------

CREATE TABLE analytics_retention_cohorts (
  subject_kind  text NOT NULL,
  cohort_week   date NOT NULL,
  weeks_later   smallint NOT NULL,
  subjects      bigint NOT NULL DEFAULT 0,
  computed_at   timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (subject_kind, cohort_week, weeks_later),

  CONSTRAINT analytics_retention_kind_is_known CHECK (
    subject_kind IN ('organization', 'session')),
  CONSTRAINT analytics_retention_weeks_are_not_negative CHECK (weeks_later >= 0),
  CONSTRAINT analytics_retention_subjects_do_not_go_negative CHECK (subjects >= 0),
  -- A Monday, so two runs that disagreed about where a week starts would fail
  -- here rather than produce two grids that cannot be compared. Postgres counts
  -- Monday as 1 in ISO day of week.
  CONSTRAINT analytics_retention_cohort_starts_a_week CHECK (
    EXTRACT(ISODOW FROM cohort_week) = 1)
);

-- ---------------------------------------------------------------------------
-- Funnels, as how far each subject got
--
-- One row per funnel per entry week per step depth: how many subjects that
-- entered the funnel in this week completed exactly this many steps. A step's
-- total is the sum of every depth at or above it, which is what makes the
-- numbers monotone by construction rather than by a caption promising they are.
-- read.ts learned that difference the hard way and its comment says so.
--
-- WHY DEPTH RATHER THAN A COUNT PER STEP. A row per step invites the query that
-- counts each step independently, which is the query that produced a funnel
-- where step four was wider than step three. A depth cannot express that: a
-- subject has exactly one depth, so the steps are cumulative sums of a
-- partition and cannot cross.
--
-- The funnel definitions live in the catalog, not here, for the same reason the
-- event definitions do. This table stores results and names the funnel that
-- produced them; what its steps were is source.
-- ---------------------------------------------------------------------------

CREATE TABLE analytics_funnel_weeks (
  funnel          text NOT NULL,
  -- The Monday of the week the subject completed step one.
  entered_week    date NOT NULL,
  -- How many consecutive steps from the first, within the funnel's window.
  -- Always at least one: a subject with no step one never entered.
  steps_completed smallint NOT NULL,
  subjects        bigint NOT NULL DEFAULT 0,
  computed_at     timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (funnel, entered_week, steps_completed),

  CONSTRAINT analytics_funnel_weeks_name_is_bounded CHECK (length(funnel) BETWEEN 1 AND 64),
  CONSTRAINT analytics_funnel_weeks_entered_at_least_one CHECK (steps_completed >= 1),
  CONSTRAINT analytics_funnel_weeks_subjects_do_not_go_negative CHECK (subjects >= 0),
  CONSTRAINT analytics_funnel_weeks_entry_starts_a_week CHECK (
    EXTRACT(ISODOW FROM entered_week) = 1)
);

CREATE INDEX analytics_funnel_weeks_window_idx
  ON analytics_funnel_weeks (funnel, entered_week DESC, steps_completed);

-- ---------------------------------------------------------------------------
-- How far one subject got through an ordered sequence
--
-- The one piece of the funnel that cannot be written as a plain aggregate, so
-- it is a function rather than a subquery per step. It takes one subject's
-- events, already ordered by time, as two parallel arrays of step index and
-- timestamp, and returns how many consecutive steps from the first they
-- completed inside the window.
--
-- WHY NOT A SUBQUERY PER STEP, WHICH IS THE OBVIOUS SHAPE.
--
-- Two reasons and the second is the one that decided it. A correlated subquery
-- per step means one index lookup per subject per step, which turns a
-- sequential scan of a date range into millions of random reads. And it is
-- WRONG in a case that happens: it finds the earliest qualifying event after
-- the previous step, so a subject whose first attempt ran out of window is
-- counted as having failed even when a later attempt succeeded. This walks the
-- events once and RESTARTS the chain when the window expires, keeping the best
-- attempt, which is what the same function does in a columnar store.
--
-- The window runs from the FIRST step, not between consecutive steps. "Signed
-- up and got a proven run within thirty days" is the question; "each step
-- within thirty days of the last" is a different and much weaker one that a
-- subject could satisfy over a year.
-- ---------------------------------------------------------------------------

CREATE FUNCTION analytics_funnel_depth(
  steps smallint[],
  times timestamptz[],
  window_span interval,
  step_count int
) RETURNS smallint
LANGUAGE plpgsql IMMUTABLE
AS $$
DECLARE
  best     smallint := 0;
  reached  smallint := 0;
  deadline timestamptz;
  i        int;
  s        smallint;
  t        timestamptz;
BEGIN
  IF steps IS NULL OR times IS NULL THEN
    RETURN 0;
  END IF;
  FOR i IN 1 .. coalesce(array_length(steps, 1), 0) LOOP
    s := steps[i];
    t := times[i];
    -- The chain in progress has run out of window. Keep how far it got and
    -- start again, so a subject who tried twice is judged on the better try.
    IF reached > 0 AND t > deadline THEN
      best := greatest(best, reached);
      reached := 0;
    END IF;
    IF reached = 0 THEN
      IF s = 0 THEN
        reached := 1;
        deadline := t + window_span;
      END IF;
    ELSIF s = reached THEN
      reached := reached + 1;
      IF reached >= step_count THEN
        RETURN step_count::smallint;
      END IF;
    END IF;
  END LOOP;
  RETURN greatest(best, reached);
END
$$;

-- ---------------------------------------------------------------------------
-- What the rollup has computed, and how far back it is trustworthy
--
-- The state row from 0029 says when the DAILY rollup last ran. These three
-- questions settle differently and a dashboard that showed one freshness for
-- all of them would be wrong about two: a funnel with a thirty day window
-- cannot be final for a week that ended yesterday, and a retention grid's last
-- column is always still filling.
-- ---------------------------------------------------------------------------

ALTER TABLE analytics_rollup_state
  -- The oldest entry week whose funnel results can still change, which is the
  -- lookback plus the widest funnel window. A week older than this is final.
  ADD COLUMN funnels_final_before date,
  -- The most recent cohort week whose row is complete, which is one full week
  -- ago at best. Later cohorts are still accumulating their own zero column.
  ADD COLUMN cohorts_complete_through date,
  -- How many days of the working set are kept, so the dashboard can say how far
  -- back a cohort grid is able to reach rather than drawing an empty corner.
  ADD COLUMN subject_days_kept smallint;

-- ---------------------------------------------------------------------------
-- Grants
--
-- The working set is granted to nobody at all, which is the point of this file.
-- The application reads three tables of counts and cannot reach the rows they
-- were computed from, so "the control plane cannot follow one organization
-- through a funnel" stays a property of the permissions rather than a property
-- of which queries somebody happened to write.
-- ---------------------------------------------------------------------------

GRANT SELECT ON analytics_actives TO antifailure_app;
GRANT SELECT ON analytics_retention_cohorts TO antifailure_app;
GRANT SELECT ON analytics_funnel_weeks TO antifailure_app;

-- ---------------------------------------------------------------------------
-- Isolation
--
-- Row-level security on all four, and on the working set it is the second lock
-- behind the absent grant, for the same reason 0029 puts an insert-only policy
-- on a stream nobody can select from: a GRANT added later by somebody who did
-- not read this file still returns nothing.
--
-- None of these tables carries an org_id, so there is no tenant to confine them
-- to and every policy is either read-only or absent. What a policy prevents
-- here is a path this file did not authorise appearing later.
-- ---------------------------------------------------------------------------

ALTER TABLE analytics_subject_days ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_subject_days FORCE ROW LEVEL SECURITY;

ALTER TABLE analytics_actives ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_actives FORCE ROW LEVEL SECURITY;
CREATE POLICY analytics_actives_are_read_only ON analytics_actives
  FOR SELECT TO antifailure_app USING (true);

ALTER TABLE analytics_retention_cohorts ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_retention_cohorts FORCE ROW LEVEL SECURITY;
CREATE POLICY analytics_retention_is_read_only ON analytics_retention_cohorts
  FOR SELECT TO antifailure_app USING (true);

ALTER TABLE analytics_funnel_weeks ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_funnel_weeks FORCE ROW LEVEL SECURITY;
CREATE POLICY analytics_funnel_weeks_are_read_only ON analytics_funnel_weeks
  FOR SELECT TO antifailure_app USING (true);

COMMIT;
