-- Workload Studio: definitions somebody wrote down, versions nobody can edit,
-- and runs that either finished or said why they did not.
--
-- THE ONE DESIGN DECISION THIS FILE EXISTS TO DEFEND.
--
-- Four things can be run against a preview environment and they are not four
-- flavours of one thing:
--
--   observed_load     a weighted mix compiled from OTLP or access logs, sent at
--                     a scale of production's rate. It has routes and
--                     percentiles and no order.
--   http_scenario     an ordered journey with waits, sessions and assertions.
--                     It has an order and it has no browser.
--   browser_workflow  a declared workflow driven through a real browser by a
--                     planner. It has steps and a verdict and no request rate.
--   exploration       a seeded wander with a goal, which produces findings
--                     rather than a pass or a fail.
--
-- The marketing site implies a single scenario intermediate representation that
-- all four compile into. There is no such thing, in this repository or anywhere
-- else, and inventing one here would make the claim structural rather than just
-- wrong. So `kind` is an enum on the definition, the version body is validated
-- per kind by the application, and `workload_run_results` carries a CHECK that
-- refuses a result shaped like the wrong kind. A browser result cannot be
-- written with a request rate, and a load result cannot be written with a
-- workflow count, because the database will not take them.
--
-- WHY THE CONTROL PLANE WRITES THE RUN ROW WHEN IT REFUSES TO WRITE AN
-- ENVIRONMENT ROW.
--
-- routers/dispatch.ts deliberately writes no environments row: the engine
-- reports one when the work starts, and a row invented at dispatch time would
-- be a ghost the moment a runner failed to pick the job up. That reasoning does
-- not carry over, and the difference is which end owns the identity. An
-- environment id is minted by the engine. A workload run is a REQUEST that only
-- this control plane knows about: it names a version that lives only here, and
-- the engine learns about it by asking. So the row has to exist before the
-- engine can be told, and "requested but nobody ever picked it up" is a state a
-- person needs to see rather than a ghost to avoid. That state has a name here,
-- `abandoned`, and it is reached by a deadline rather than by hope.
--
-- WHY STATE AND VERDICT ARE TWO COLUMNS.
--
-- `state` says whether the work happened. `verdict` says what it found. A run
-- that finishes cleanly and fails every threshold is `succeeded` with a verdict
-- of `fail`, and a run whose thresholds could not be evaluated is `succeeded`
-- with `unverified`. Collapsing them is how an exit code of zero over work that
-- never happened reads as a pass, which is a failure this repository has
-- already shipped once.

BEGIN;

-- ---------------------------------------------------------------------------
-- Vocabulary
-- ---------------------------------------------------------------------------

CREATE TYPE workload_kind AS ENUM (
  'observed_load', 'http_scenario', 'browser_workflow', 'exploration'
);

-- The lifecycle of one request to run a workload.
--
-- Eight values and every one is reachable, which is the bar `environment_state`
-- failed: `queued` there is reachable only as a column default because nothing
-- schedules. Here `requested` is written by the console, `accepted` by an
-- engine claiming the run, `running` and the four terminal values by the events
-- the engine sends, and `abandoned` by the deadline when none of them arrive.
CREATE TYPE workload_run_state AS ENUM (
  'requested', 'accepted', 'running',
  'succeeded', 'failed', 'cancelled', 'timed_out', 'abandoned'
);

-- Where a version came from. `promoted` means an exploration was compiled into
-- it, and `promoted_from_run_id` names the run that produced the discovery.
CREATE TYPE workload_version_source AS ENUM ('authored', 'promoted');

-- What the control plane is asking a runtime to do. Two kinds, and both exist
-- because marking a row and hoping is not a teardown.
CREATE TYPE runtime_command_kind AS ENUM ('environment.teardown', 'workload.cancel');

CREATE TYPE runtime_command_state AS ENUM (
  'pending', 'claimed', 'acknowledged', 'failed', 'expired', 'superseded'
);

-- Whether the bytes behind a piece of evidence can actually be fetched.
--
-- `runner_local` is the honest value for a trace that exists at
-- /home/runner/work/... on a machine that no longer exists. Reports in this
-- product have carried exactly those paths, and a console that renders one as a
-- link sends somebody to a 404 and blames itself. A row says which it is, and
-- the CHECK below refuses `uploaded` without an artifact row behind it.
CREATE TYPE workload_evidence_availability AS ENUM (
  'uploaded', 'runner_local', 'not_retained'
);

-- ---------------------------------------------------------------------------
-- Definitions
-- ---------------------------------------------------------------------------

CREATE TABLE workloads (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  -- A workload runs against environments of one repository, because the routes
  -- it names, the workflows it selects and the safe list it is checked against
  -- all belong to that repository's manifest.
  repository_id   uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
  -- The same shape an organization slug has, and for the same reason: it
  -- appears in a URL and in a workflow input.
  slug            text NOT NULL,
  name            text NOT NULL,
  kind            workload_kind NOT NULL,
  description     text,
  created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  -- Archived rather than deleted, the same decision `runtimes` made. A run
  -- points at a version which points at a workload, and deleting the workload
  -- would leave every historical run unable to say what it ran.
  archived_at     timestamptz,
  CONSTRAINT workloads_slug_shape CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$')
);

-- One live workload per slug per repository, and an archived one does not hold
-- its name hostage. A partial index rather than a plain unique constraint for
-- exactly that, copied from `runtimes` because the argument is the same.
CREATE UNIQUE INDEX workloads_slug_key ON workloads (org_id, repository_id, slug)
  WHERE archived_at IS NULL;
CREATE INDEX workloads_org_idx ON workloads (org_id, created_at DESC);

CREATE TABLE workload_versions (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  workload_id     uuid NOT NULL REFERENCES workloads(id) ON DELETE CASCADE,
  -- 1, 2, 3. Assigned by the application under the workload's advisory lock so
  -- that two people saving at once cannot both write version 4; the unique
  -- constraint below is what makes the loser retry rather than overwrite.
  version         integer NOT NULL,
  -- The definition itself, in the shape the version's kind requires. Validated
  -- by the application against a per-kind schema before it is written, because
  -- Postgres cannot check a discriminated union and a jsonb column that takes
  -- anything is how the four kinds would quietly become one.
  body            jsonb NOT NULL,
  -- sha256 of the canonical body. It answers "is this the same definition" for
  -- a retry, for a promotion that would produce a duplicate, and for a console
  -- that wants to say a save changed nothing.
  body_digest     text NOT NULL,
  notes           text,
  source          workload_version_source NOT NULL DEFAULT 'authored',
  promoted_from_run_id uuid,
  created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workload_id, version),
  CONSTRAINT workload_versions_positive CHECK (version >= 1),
  CONSTRAINT workload_versions_digest_shape CHECK (body_digest ~ '^[0-9a-f]{64}$'),
  -- An authored version may not claim provenance it does not have. The other
  -- direction is deliberately NOT constrained: a promotion can arrive from an
  -- exploration document a person ran locally and pasted in, which has no run
  -- in this database to point at. Requiring one would have made the only
  -- promotion path that works today impossible, and the version would still be
  -- honestly labelled `promoted`.
  CONSTRAINT workload_versions_provenance CHECK (
    source = 'promoted' OR promoted_from_run_id IS NULL
  )
);
CREATE INDEX workload_versions_workload_idx ON workload_versions (workload_id, version DESC);

-- ---------------------------------------------------------------------------
-- Runs
-- ---------------------------------------------------------------------------

CREATE TABLE workload_runs (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  workload_id     uuid NOT NULL REFERENCES workloads(id) ON DELETE CASCADE,
  -- RESTRICT rather than CASCADE. A version is what a run means; removing one
  -- out from under a finished run would leave a result nobody can interpret.
  -- Nothing deletes a version anyway: the application role has no DELETE on
  -- that table at all, which is the grant below.
  workload_version_id uuid NOT NULL REFERENCES workload_versions(id) ON DELETE RESTRICT,
  environment_id  uuid NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  state           workload_run_state NOT NULL DEFAULT 'requested',

  -- The request
  requested_by    uuid REFERENCES users(id) ON DELETE SET NULL,
  requested_at    timestamptz NOT NULL DEFAULT now(),
  -- What makes a double click one run. Supplied by the caller or derived from
  -- the request; unique per organization, so the second attempt collides and
  -- returns the first run rather than starting a second.
  request_key     text NOT NULL,

  -- What was dispatched, denormalized on purpose. The repository could be
  -- renamed and the branch could move, and this has to keep saying what was
  -- actually sent to GitHub on the day.
  repository      text NOT NULL,
  git_ref         text NOT NULL,
  workflow_file   text,
  dispatched_at   timestamptz,

  -- Lifecycle
  accepted_at     timestamptz,
  started_at      timestamptz,
  finished_at     timestamptz,
  -- When this run stops being believed. An engine extends it by heartbeat; a
  -- run past it with no terminal event becomes `abandoned`.
  deadline_at     timestamptz NOT NULL,
  -- The highest event sequence applied to this row, exactly as `environments`
  -- carries one and for the same reason: arrival order is a property of the
  -- network, so a late `workload.started` must not move a finished run back.
  last_sequence   bigint NOT NULL DEFAULT 0,

  -- Who is holding it. Set when an engine claims the run, so a second engine
  -- polling the same environment does not take work already in flight.
  lease_holder    text,
  lease_expires_at timestamptz,

  -- Cancellation. Requested and effected are separate columns because they are
  -- separate facts: a cancel that was asked for and never confirmed is exactly
  -- the state the hosted teardown used to be in permanently.
  cancel_requested_at timestamptz,
  cancel_requested_by uuid REFERENCES users(id) ON DELETE SET NULL,
  cancel_reason   text,
  cancelled_at    timestamptz,

  -- Retry
  attempt         integer NOT NULL DEFAULT 1,
  retry_of        uuid REFERENCES workload_runs(id) ON DELETE SET NULL,
  superseded_by   uuid REFERENCES workload_runs(id) ON DELETE SET NULL,

  -- Outcome. See the header: state says whether it happened, verdict says what
  -- it found, and neither implies the other.
  verdict         verdict_value,
  failure_code    text,
  detail          text,

  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),

  UNIQUE (org_id, request_key),
  CONSTRAINT workload_runs_attempt CHECK (attempt >= 1),
  -- A run cannot be its own retry or its own successor. Cheap, and it is the
  -- shape a bad UPDATE takes.
  CONSTRAINT workload_runs_not_self CHECK (retry_of IS DISTINCT FROM id AND superseded_by IS DISTINCT FROM id),
  -- A terminal state has an end. Without this a run could read as finished on
  -- the page and contribute nothing to any duration computed from these two
  -- columns, which is how a zero appears in a report and nobody can explain it.
  CONSTRAINT workload_runs_terminal_has_an_end CHECK (
    (state IN ('succeeded', 'failed', 'cancelled', 'timed_out', 'abandoned'))
    = (finished_at IS NOT NULL)
  )
);

-- One live run per workload per environment.
--
-- This is the whole of the concurrency answer, and it is here rather than in a
-- read followed by a decision because two starts landing together would both
-- read zero and both write. Postgres decides it: the loser gets 23505 and the
-- route turns that into a sentence naming the run that is already going.
CREATE UNIQUE INDEX workload_runs_one_live ON workload_runs (workload_id, environment_id)
  WHERE state IN ('requested', 'accepted', 'running');

CREATE INDEX workload_runs_org_idx ON workload_runs (org_id, requested_at DESC);
CREATE INDEX workload_runs_workload_idx ON workload_runs (workload_id, requested_at DESC);
CREATE INDEX workload_runs_environment_idx ON workload_runs (environment_id, requested_at DESC);
-- The index the deadline resolution uses. Partial, so it holds only runs that
-- can still time out, which is a handful rather than the whole history.
CREATE INDEX workload_runs_open_idx ON workload_runs (org_id, deadline_at)
  WHERE state IN ('requested', 'accepted', 'running');
-- What an engine polling an environment scans.
CREATE INDEX workload_runs_claimable_idx ON workload_runs (environment_id, requested_at)
  WHERE state = 'requested';

-- Deferred to here because workload_versions is created before workload_runs
-- and the reference runs the other way. A promotion names the exploration run
-- whose findings it compiled.
ALTER TABLE workload_versions
  ADD CONSTRAINT workload_versions_promoted_from_fkey
  FOREIGN KEY (promoted_from_run_id) REFERENCES workload_runs(id) ON DELETE SET NULL;

-- ---------------------------------------------------------------------------
-- Results
--
-- Three tables rather than one blob, because three different questions are
-- asked of them: what did the run do overall, which route was slow, and which
-- threshold failed. A jsonb column answering all three cannot be indexed,
-- cannot be constrained, and cannot be joined.
-- ---------------------------------------------------------------------------

CREATE TABLE workload_run_results (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  workload_run_id uuid NOT NULL UNIQUE REFERENCES workload_runs(id) ON DELETE CASCADE,
  -- Copied from the workload rather than joined for it. A reader has to know
  -- which columns to trust before it can read the row, and the CHECK below
  -- needs it in the same row to be a CHECK at all.
  kind            workload_kind NOT NULL,

  -- Sent traffic. observed_load and http_scenario only.
  requests        integer,
  failures        integer,
  error_rate      double precision,
  target_rate     double precision,
  achieved_rate   double precision,
  p50_ms          double precision,
  p90_ms          double precision,
  p95_ms          double precision,
  p99_ms          double precision,
  max_ms          double precision,

  -- A journey. http_scenario only.
  sessions        integer,
  iterations      integer,
  scheduled_ms    double precision,

  -- A browser. browser_workflow only.
  workflows       integer,
  workflows_passed integer,
  workflows_failed integer,
  steps           integer,

  -- A wander. exploration only.
  findings        integer,
  goal_reached    boolean,

  duration_ms     double precision,
  -- Where the traffic mix came from, so a reader can tell production's shape
  -- from a default. The engine's load.Result carries exactly this field and
  -- nothing carried it out of a run until it had somewhere to go.
  source          text,
  -- Routes the safe list refused, which is why a blocked scenario is blocked.
  refused_routes  text[] NOT NULL DEFAULT '{}',
  recorded_at     timestamptz NOT NULL DEFAULT now(),

  -- The four kinds measure different things and this refuses a row that
  -- pretends otherwise. It is the header's argument made enforceable: a browser
  -- result written with a request count would let a console draw a latency
  -- chart over a number that is not a latency.
  CONSTRAINT workload_run_results_shape CHECK (
    CASE kind
      WHEN 'observed_load' THEN
        requests IS NOT NULL AND sessions IS NULL AND workflows IS NULL AND findings IS NULL
      WHEN 'http_scenario' THEN
        requests IS NOT NULL AND sessions IS NOT NULL AND workflows IS NULL AND findings IS NULL
      WHEN 'browser_workflow' THEN
        workflows IS NOT NULL AND requests IS NULL AND sessions IS NULL AND findings IS NULL
      WHEN 'exploration' THEN
        findings IS NOT NULL AND requests IS NULL AND sessions IS NULL AND workflows IS NULL
    END
  ),
  CONSTRAINT workload_run_results_counts CHECK (
    (requests IS NULL OR requests >= 0)
    AND (failures IS NULL OR failures >= 0)
    AND (workflows IS NULL OR workflows >= 0)
    AND (findings IS NULL OR findings >= 0)
  )
);
CREATE INDEX workload_run_results_org_idx ON workload_run_results (org_id, recorded_at DESC);

CREATE TABLE workload_route_metrics (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  workload_run_id uuid NOT NULL REFERENCES workload_runs(id) ON DELETE CASCADE,
  -- "GET /checkout", spelled the way the manifest's safe list spells it, so
  -- what you allow and what you measure look the same on the page.
  route           text NOT NULL,
  sent            integer NOT NULL,
  errors          integer NOT NULL DEFAULT 0,
  p50_ms          double precision,
  p90_ms          double precision,
  p95_ms          double precision,
  p99_ms          double precision,
  max_ms          double precision,
  -- What production serves it in, when that is known.
  baseline_p95_ms double precision,
  -- How much slower this environment is, as a ratio.
  p95_increase    double precision,
  position        integer NOT NULL DEFAULT 0,
  UNIQUE (workload_run_id, route),
  CONSTRAINT workload_route_metrics_counts CHECK (sent >= 0 AND errors >= 0),
  -- No baseline and no change are different answers, and the engine's
  -- RouteResult carries a separate HasBaseline field precisely because a zero
  -- ratio would otherwise read as "no regression" when it means "nothing to
  -- compare with". Here the pair is constrained instead: an increase exists
  -- exactly when a baseline does, so the distinction cannot be lost by a writer
  -- that forgets to carry the flag.
  CONSTRAINT workload_route_metrics_baseline CHECK (
    (baseline_p95_ms IS NULL) = (p95_increase IS NULL)
  )
);
CREATE INDEX workload_route_metrics_run_idx ON workload_route_metrics (workload_run_id, position);

CREATE TABLE workload_threshold_verdicts (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  workload_run_id uuid NOT NULL REFERENCES workload_runs(id) ON DELETE CASCADE,
  -- The assertion's own name, which is its identity in a report.
  name            text NOT NULL,
  -- The route it was scoped to, or NULL for the whole run. The engine's
  -- Assertion.Step is exactly this.
  scope           text,
  -- Which of the four measures this is. Text rather than an enum because the
  -- engine adds one by releasing, and a customer's database should not need a
  -- migration to record a measure it was sent.
  measure         text NOT NULL,
  threshold       double precision,
  observed        double precision,
  -- The same five words the rest of the product uses. A sixth vocabulary for
  -- thresholds would be one more thing a reader has to learn.
  value           verdict_value NOT NULL,
  detail          text,
  position        integer NOT NULL DEFAULT 0
);
-- Unique on the expression rather than on the columns, because scope is NULL
-- for a run wide assertion and two NULLs never collide in a plain unique index.
-- Without the coalesce the same assertion could be written twice.
CREATE UNIQUE INDEX workload_threshold_verdicts_key
  ON workload_threshold_verdicts (workload_run_id, name, coalesce(scope, ''));
CREATE INDEX workload_threshold_verdicts_run_idx
  ON workload_threshold_verdicts (workload_run_id, position);

CREATE TABLE workload_evidence (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  workload_run_id uuid NOT NULL REFERENCES workload_runs(id) ON DELETE CASCADE,
  kind            text NOT NULL,
  label           text,
  availability    workload_evidence_availability NOT NULL,
  -- Where it is, said in the terms of whatever `availability` is: a storage key
  -- for an uploaded artifact, and the path on the runner for one that was never
  -- uploaded. Recorded rather than dropped, because "the trace was at this path
  -- on a machine that is gone" is a useful sentence and a broken link is not.
  --
  -- Deliberately not a reference to the `artifacts` table. Rows there hang off
  -- `runs`, which is the agent run table nothing in the control plane has ever
  -- written, and `artifact.stored` is an accepted event type that nothing
  -- emits. A nullable foreign key to a table with no writer is a socket to
  -- nowhere, and this file is not the place to add one on the chance that an
  -- uploader appears. When one does, it writes both rows and this gains a
  -- column in a migration of its own.
  locator         text NOT NULL,
  sha256          text,
  size_bytes      bigint,
  recorded_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workload_run_id, kind, locator),
  -- Bytes cannot be claimed to be retained without something to verify them
  -- against. Without this a row could say `uploaded` and carry a runner path,
  -- which is the exact defect this column exists to end.
  CONSTRAINT workload_evidence_uploaded_is_verifiable CHECK (
    availability <> 'uploaded' OR sha256 IS NOT NULL
  )
);
CREATE INDEX workload_evidence_run_idx ON workload_evidence (workload_run_id, recorded_at);

-- ---------------------------------------------------------------------------
-- Commands
--
-- The control plane cannot reach a runtime. It has exactly two ways to make
-- something happen out there: dispatch a workflow run in the customer's own
-- repository through the GitHub App, and answer an engine that asks. Both are
-- lossy in the same way, so a request that matters has to survive the loss.
--
-- `environments.teardown` used to be an UPDATE and a comment saying the engine
-- reads the row. Nothing reads the row. A row marked torn_down while the
-- containers keep running is worse than a failure, because the console then
-- says the environment is gone and the bill keeps growing.
--
-- So a teardown is a command: durable, leased, acknowledged, and with a
-- deadline after which it says it was never confirmed rather than pretending.
-- ---------------------------------------------------------------------------

CREATE TABLE runtime_commands (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  kind            runtime_command_kind NOT NULL,
  environment_id  uuid REFERENCES environments(id) ON DELETE CASCADE,
  workload_run_id uuid REFERENCES workload_runs(id) ON DELETE CASCADE,
  -- What the command carries, in the shape its kind needs. Never a credential
  -- and never anything read out of a customer's environment: this table is a
  -- to-do list, not a channel.
  payload         jsonb NOT NULL DEFAULT '{}'::jsonb,
  state           runtime_command_state NOT NULL DEFAULT 'pending',
  attempts        integer NOT NULL DEFAULT 0,
  -- The engine token id that holds the lease. An identifier of a credential,
  -- never the credential.
  lease_holder    text,
  lease_expires_at timestamptz,
  claimed_at      timestamptz,
  acknowledged_at timestamptz,
  -- What the runtime said it did. Separate from `state` because "acknowledged"
  -- is about this table and "the containers are gone" is about the world.
  outcome         text,
  detail          text,
  requested_by    uuid REFERENCES users(id) ON DELETE SET NULL,
  requested_at    timestamptz NOT NULL DEFAULT now(),
  -- After this the command is `expired` and the console says the request was
  -- never confirmed. A command with no deadline is the same silent nothing the
  -- old teardown was.
  expires_at      timestamptz NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),

  -- Each kind names its own target and only its own. Without this a teardown
  -- could be written with no environment, which is a command nothing can carry
  -- out and nothing would report.
  CONSTRAINT runtime_commands_target CHECK (
    (kind = 'environment.teardown' AND environment_id IS NOT NULL AND workload_run_id IS NULL)
    OR (kind = 'workload.cancel' AND workload_run_id IS NOT NULL AND environment_id IS NULL)
  ),
  CONSTRAINT runtime_commands_attempts CHECK (attempts >= 0),
  CONSTRAINT runtime_commands_settled CHECK (
    (state IN ('acknowledged', 'failed')) = (acknowledged_at IS NOT NULL)
  )
);

-- One live teardown per environment, and one live cancel per run.
--
-- Pressing the button twice is the ordinary case, not the exceptional one, and
-- the second press must join the first rather than queue a second teardown. Two
-- partial indexes rather than a read and a decision, for the same reason
-- workload_runs_one_live is an index: two requests landing together both read
-- nothing and both write.
CREATE UNIQUE INDEX runtime_commands_one_live_teardown
  ON runtime_commands (environment_id)
  WHERE kind = 'environment.teardown' AND state IN ('pending', 'claimed');
CREATE UNIQUE INDEX runtime_commands_one_live_cancel
  ON runtime_commands (workload_run_id)
  WHERE kind = 'workload.cancel' AND state IN ('pending', 'claimed');

CREATE INDEX runtime_commands_org_idx ON runtime_commands (org_id, requested_at DESC);
CREATE INDEX runtime_commands_open_idx ON runtime_commands (org_id, expires_at)
  WHERE state IN ('pending', 'claimed');
CREATE INDEX runtime_commands_environment_idx ON runtime_commands (environment_id, requested_at DESC);
CREATE INDEX runtime_commands_workload_run_idx ON runtime_commands (workload_run_id, requested_at DESC);

-- ---------------------------------------------------------------------------
-- What a policy cannot say
--
-- Row-level security admits a ROW. It cannot say "this column may not change",
-- because a WITH CHECK sees only the new row and has nothing to compare it
-- with. Where that restriction is needed here, it is a grant or a trigger, and
-- which one it is depends on the shape of the requirement:
--
--   "no version may ever be updated"       a privilege, so it is a GRANT above.
--   "a workload's kind may never change"   one column of an otherwise editable
--                                          row, so it is the trigger below.
--
-- No trigger is written for org_id, and the reason is worth stating so nobody
-- adds one as a reflex. tenant_isolation already stops a move in both
-- directions: USING refuses a row that is not the caller's, and WITH CHECK
-- refuses a new row that is not the caller's, so the only org_id an UPDATE can
-- write is the one the row already has. A trigger there would survive every
-- mutation, which makes it decoration. It becomes necessary the day a SECOND
-- permissive policy is added to one of these tables, because permissive
-- policies OR together and a row reachable through the second one is updatable
-- into the caller's own organization. There is no such policy here.
-- ---------------------------------------------------------------------------

-- A workload's kind is what every one of its versions means.
--
-- Changing it would reinterpret every version body already written, so it is
-- refused by the database rather than by the absence of a route that does it.
-- The absence of a route is not a guarantee: the next route somebody writes is
-- the one that forgets.
CREATE OR REPLACE FUNCTION refuse_workload_kind_change() RETURNS trigger
  LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.kind IS DISTINCT FROM OLD.kind THEN
    RAISE EXCEPTION
      'a workload is a % and cannot become a %; the versions already written are in the old kind''s shape',
      OLD.kind, NEW.kind
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER workloads_kind_is_forever
  BEFORE UPDATE ON workloads
  FOR EACH ROW EXECUTE FUNCTION refuse_workload_kind_change();

-- ---------------------------------------------------------------------------
-- Grants
--
-- Per table and per verb, the same way 0002 does it. Two are narrower than the
-- rest and both are deliberate.
--
-- workload_versions gets INSERT and SELECT and nothing else. A version is what
-- a finished run means, so editing one rewrites history: a run that passed
-- against a threshold of 200ms would read as having passed against 900ms. That
-- is an append-only table for the same reason audit_entries is, and saying so
-- with a grant makes it a property of the database rather than a promise about
-- the routes. RLS could not express it: a policy admits a ROW and a privilege
-- is granted to a ROLE, and "no version may ever be updated" is the second one.
--
-- The result tables get INSERT and SELECT and nothing else, for the same
-- reason. A measurement that can be edited after the fact is not a measurement.
-- The one thing that must be able to change them is a DELETE cascading from the
-- run, and a referential action is carried out by the system rather than by the
-- role, so withholding DELETE here does not orphan anything.
-- ---------------------------------------------------------------------------

GRANT SELECT, INSERT, UPDATE ON workloads TO antifailure_app;
GRANT SELECT, INSERT ON workload_versions TO antifailure_app;
GRANT SELECT, INSERT, UPDATE ON workload_runs TO antifailure_app;
GRANT SELECT, INSERT ON workload_run_results TO antifailure_app;
GRANT SELECT, INSERT ON workload_route_metrics TO antifailure_app;
GRANT SELECT, INSERT ON workload_threshold_verdicts TO antifailure_app;
GRANT SELECT, INSERT ON workload_evidence TO antifailure_app;
GRANT SELECT, INSERT, UPDATE ON runtime_commands TO antifailure_app;

-- Explicitly withheld, so a later blanket grant has to overwrite a statement
-- saying why it should not. The same shape 0002 uses for audit_entries.
REVOKE UPDATE, DELETE, TRUNCATE ON workload_versions FROM antifailure_app;
REVOKE UPDATE, DELETE, TRUNCATE ON workload_run_results FROM antifailure_app;
REVOKE UPDATE, DELETE, TRUNCATE ON workload_route_metrics FROM antifailure_app;
REVOKE UPDATE, DELETE, TRUNCATE ON workload_threshold_verdicts FROM antifailure_app;
REVOKE UPDATE, DELETE, TRUNCATE ON workload_evidence FROM antifailure_app;
REVOKE DELETE, TRUNCATE ON workloads FROM antifailure_app;
REVOKE DELETE, TRUNCATE ON workload_runs FROM antifailure_app;
REVOKE DELETE, TRUNCATE ON runtime_commands FROM antifailure_app;

-- ---------------------------------------------------------------------------
-- Row-level security
--
-- The same policy shape as every other tenant-scoped table, and it has to be
-- written here rather than inherited from anywhere: a table added without one
-- is readable by every tenant and nothing in the application would say so. The
-- cross-tenant suite reads the table list out of the database, so all eight of
-- these are attacked from a second tenant the moment they exist.
-- ---------------------------------------------------------------------------

DO $$
DECLARE
  t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'workloads', 'workload_versions', 'workload_runs', 'workload_run_results',
    'workload_route_metrics', 'workload_threshold_verdicts', 'workload_evidence',
    'runtime_commands'
  ]
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
