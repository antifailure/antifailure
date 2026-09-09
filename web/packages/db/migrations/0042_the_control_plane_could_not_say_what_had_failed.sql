-- The control plane counted its own failures and could not say what any of them was.
--
-- THE FAILURE THIS EXISTS FOR. `af_http_requests_total{status_class="5xx"}`
-- goes up. That is the whole of what an operator can see. The line that says
-- WHICH route, WHICH error class and WHICH driver code went to stdout, where it
-- is one line among the day's traffic in a container's log buffer. On the
-- hosted control plane that is shipped somewhere searchable. A self hoster
-- running this container against their own Postgres, with no Log Analytics and
-- no Grafana, has `docker logs`, no aggregation, no count, no first seen, and
-- therefore no answer to the two questions an operator opens a portal with:
-- what is failing right now, and did it start when we deployed.
--
-- The Logs & Error Explorer says in its own header that this product has no
-- error group, no fingerprint and no occurrence counter, and that inventing one
-- would be worse than having none. That statement is correct about the ENGINE's
-- exceptions, which would need the engine to report them. It was also true of
-- the control plane's own failures, and those need nothing from anybody: the
-- process already catches every one of them in two handlers that already
-- decided what is safe to write down.
--
-- WHY THIS IS AN AGGREGATE AND NOT A LOG LINE STORE.
--
-- A row per occurrence is a table whose size is set by how badly the
-- installation is behaving, which is the worst possible coupling: the day the
-- control plane starts failing is the day its disk fills. One row per
-- FINGERPRINT is bounded by the code instead. The fingerprint is built from the
-- declared route key, the HTTP method, the error class name and the driver's
-- own code, and each of those is a bounded vocabulary that ships in the
-- container. A busy day adds occurrences to existing rows and no rows.
--
-- The cap and the retention below are belt and braces on top of that, because
-- "bounded by the code" is an argument and a LIMIT is a fact.
--
-- WHAT NEVER ENTERS THIS TABLE, and where the boundary is enforced.
--
-- Not the error message. Not the stack. Not the request body, the query string,
-- the parameters or any payload. Not an org_id, a user id, an email or an
-- environment. The reason is written on `app.onError` in server.ts already, and
-- it is specific rather than general: Drizzle writes a query failure as
-- "Failed query: <the whole statement>" with the parameters after it, so an
-- error MESSAGE from this stack can carry an event payload. That is why the
-- existing log line records the class and the driver code and not the error.
-- This table inherits exactly that column list.
--
-- The boundary is enforced in `recordFailure` in web/apps/api/src/failures.ts,
-- which takes five bounded strings and has no parameter that could carry one of
-- the forbidden values. It is not enforced by a policy here and cannot be: row
-- level security is row level, and the operator role bypasses it anyway. Same
-- argument as SAFE_COLUMNS in admin/router.ts and as the event stream in
-- admin/operations.ts.
--
-- WHAT IS DELIBERATELY MISSING FROM THE ANSWER, so nobody reads its absence as
-- a zero. There is no tenant count. "How many organizations does this failure
-- touch" is a question this table cannot answer, because answering it means
-- writing an organization identifier next to a failure, and that turns a
-- bounded operational table into tenant data with a second retention argument
-- attached. The page says so in words. `workload_runs.failure_code`, which the
-- same page already groups, is where a per-tenant count comes from.

BEGIN;

CREATE TABLE control_plane_failures (
  -- Sixty-four hex characters of SHA-256 over the five bounded fields below,
  -- computed in the application so that every replica and every restart agrees
  -- on the identity of a group without coordinating. A natural key over the
  -- columns themselves would do the same job; the hash is what lets the console
  -- carry one opaque identifier for a row rather than five.
  fingerprint     text PRIMARY KEY,

  -- Which handler caught it. Two exist and both are in server.ts.
  source          text NOT NULL,
  -- The DECLARED route key that matched, never the path that matched it. See
  -- routeLabel in metrics.ts: the first version of that function reported the
  -- path, so every environment identifier anybody fetched became its own
  -- series, and this table would have grown a row for each. For a tRPC failure
  -- this is the procedure path, which is bounded by the router.
  route           text NOT NULL,
  method          text NOT NULL,
  -- The error's class name. Code, not data.
  kind            text NOT NULL,
  -- The driver's own code, when the cause carried one: a Postgres SQLSTATE, an
  -- undici code. Null when the error had no cause with a code.
  provider_code   text,

  occurrences     bigint NOT NULL DEFAULT 0,
  first_seen_at   timestamptz NOT NULL,
  last_seen_at    timestamptz NOT NULL,

  -- The build that was running the first and the most recent time this group
  -- was seen, from the same version string af_control_plane_info carries. This
  -- is the deploy overlay, and it is a column rather than a join because there
  -- is no deployment table in this schema and inventing one to draw a line on a
  -- chart would be exactly the kind of thing the Logs page refuses to do.
  first_seen_version text NOT NULL,
  last_seen_version  text NOT NULL,

  -- The request id of the most recent occurrence. It is the one identifier the
  -- 500 response already hands the caller and the only thing that ties this row
  -- to a line in `af logs web`. It names a request, not a person.
  last_request_id text,

  CONSTRAINT control_plane_failures_source_is_known CHECK (
    source IN ('http', 'trpc')),
  CONSTRAINT control_plane_failures_fingerprint_is_a_hash CHECK (
    fingerprint ~ '^[0-9a-f]{64}$'),
  -- Bounds, so that a bug upstream cannot turn a bounded vocabulary into free
  -- text. Every one of these is far above the longest real value: the longest
  -- declared route key in ENDPOINT_LIMITS is well under 100 characters and a
  -- class name is a JavaScript identifier.
  CONSTRAINT control_plane_failures_fields_are_bounded CHECK (
    length(route) BETWEEN 1 AND 200
    AND length(method) BETWEEN 1 AND 16
    AND length(kind) BETWEEN 1 AND 100
    AND (provider_code IS NULL OR length(provider_code) BETWEEN 1 AND 64)
    AND length(first_seen_version) BETWEEN 1 AND 64
    AND length(last_seen_version) BETWEEN 1 AND 64
    AND (last_request_id IS NULL OR length(last_request_id) BETWEEN 1 AND 100)),
  CONSTRAINT control_plane_failures_occurrences_do_not_go_negative CHECK (
    occurrences >= 0),
  -- A group cannot have been last seen before it was first seen. The flush
  -- takes LEAST and GREATEST on both, so a late flush from a replica whose
  -- clock is behind cannot move either one the wrong way, and this is what
  -- proves that stayed true.
  CONSTRAINT control_plane_failures_seen_in_order CHECK (
    last_seen_at >= first_seen_at)
);

-- The page orders by last_seen_at descending and filters on a window, and the
-- sweep deletes by last_seen_at. One index serves all three.
CREATE INDEX control_plane_failures_last_seen_idx
  ON control_plane_failures (last_seen_at DESC);

-- ---------------------------------------------------------------------------
-- Grants
--
-- The application writes and reads: the flush needs to know whether the group
-- already exists before the cap can decide, and that read is the cap. The
-- operator role reads, which is what the portal is.
--
-- The application is given no DELETE. The retention sweep runs on the operator
-- credential beside the other administrative maintenance, so an application
-- role compromised through a request path cannot erase the record of what it
-- did to get there.
-- ---------------------------------------------------------------------------

GRANT SELECT, INSERT, UPDATE ON control_plane_failures TO antifailure_app;
GRANT SELECT, DELETE ON control_plane_failures TO antifailure_admin;

-- ---------------------------------------------------------------------------
-- Isolation
--
-- This table carries no org_id, on purpose and as stated above, so tenant
-- isolation has nothing to key on. What the policy does is the same second lock
-- analytics_events has: the grants above are the real protection, and a policy
-- means a GRANT added later by somebody who did not read this file still cannot
-- turn the application role into something that can delete its own history.
-- ---------------------------------------------------------------------------

ALTER TABLE control_plane_failures ENABLE ROW LEVEL SECURITY;
ALTER TABLE control_plane_failures FORCE ROW LEVEL SECURITY;
CREATE POLICY control_plane_failures_are_operational ON control_plane_failures
  FOR SELECT TO antifailure_app USING (true);
CREATE POLICY control_plane_failures_are_written_by_the_app ON control_plane_failures
  FOR INSERT TO antifailure_app WITH CHECK (true);
CREATE POLICY control_plane_failures_are_counted_up ON control_plane_failures
  FOR UPDATE TO antifailure_app USING (true) WITH CHECK (true);

COMMIT;
