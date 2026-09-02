-- The switches an operator reaches for during an incident.
--
-- Installation-wide rather than per organization, which is the whole reason
-- this table exists: `organizations.suspended_at` already stops ONE customer
-- creating work, and there was nothing that stops an installation. During an
-- incident the question is usually not "which customer" but "stop everything
-- until we understand this", and the answer to that was a deploy.
--
-- Engaged is a timestamp rather than a boolean, in the same shape as
-- `suspended_at` and for the same reason: "when did this start" is the first
-- question anybody asks about a switch that is on, and a boolean cannot answer
-- it. A row that has never been engaged does not exist; a row that was
-- engaged and released keeps its history in the audit log, not here.
--
-- WHAT PROTECTS THIS TABLE IS NOT ROW LEVEL SECURITY, and pretending otherwise
-- would be worse than saying so. There is no org_id to key a policy on: these
-- rows are configuration for the installation, not data belonging to a tenant.
-- So the write path is gated on a setting no ordinary connection declares.
-- `withTenant`, `withoutTenant`, `withGitHubAccount`, `withStripeCustomer` and
-- `withSweeper` never set `antifailure.platform_admin`, so `current_setting`
-- returns NULL for every one of them and the policy denies. A bug in an
-- ordinary tenant route therefore cannot disable signups, which is a
-- meaningfully different guarantee from "the route checks a permission".
--
-- A NOTE FOR WHOEVER MERGES THIS WITH THE ADMIN PORTAL'S OWN MIGRATION.
-- The gate below is a claim, not a credential: the predicate is true for any
-- connection that sets the setting, and what contains it is that exactly one
-- function in client.ts sets it. That is weaker than the portal's admin scope,
-- which requires the declared hash to MATCH A STORED SESSION ROW and is
-- therefore false even for a caller that can set settings freely. Once
-- current_admin_user() exists on the same branch, these two policies should be
-- rewritten to require it, which turns this from "a connection that says it is
-- an operator" into "a connection physically holding an operator's session".
-- It is written this way here only because the two migrations were built on
-- separate branches and this one cannot reference a function it does not have.
--
-- Reading is deliberately open to the application role. Every request has to
-- be able to ask whether maintenance is engaged, and a check that needs a
-- privileged connection is a check somebody will skip on the hot path.

BEGIN;

CREATE TABLE platform_controls (
  -- The control's stable name, from the catalog in
  -- web/apps/api/src/admin/controls.ts. Text rather than an enum so that
  -- adding a switch is a release rather than a migration on every customer's
  -- database, matching how `runtimes.provider` is handled.
  name        text PRIMARY KEY,
  -- NULL means not engaged. A row only exists once somebody has touched the
  -- control, so an installation nobody has ever paused holds no rows at all.
  engaged_at  timestamptz,
  -- Why, in the operator's own words. Required by the route that sets it: a
  -- switch that stops an installation with no reason recorded is one the next
  -- person on call cannot safely release.
  reason      text,
  -- Who, by label rather than by id, so the row still reads after the user is
  -- deleted. The audit entry carries the id.
  engaged_by  text,
  updated_at  timestamptz NOT NULL DEFAULT now()
);

GRANT SELECT ON platform_controls TO antifailure_app;
GRANT INSERT, UPDATE ON platform_controls TO antifailure_app;

ALTER TABLE platform_controls ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_controls FORCE ROW LEVEL SECURITY;

-- Anyone the application serves may READ the switches. Refusing this would
-- mean a request cannot find out that the installation is paused, which is the
-- one thing every request needs to know.
CREATE POLICY read_the_switches ON platform_controls
  FOR SELECT TO antifailure_app
  USING (true);

-- Writing needs a connection that declared itself. The second argument to
-- current_setting is `missing_ok`, so an ordinary connection that never sets
-- this gets NULL rather than an error, and NULL is not 'on'.
CREATE POLICY only_a_declared_operator_writes ON platform_controls
  FOR INSERT TO antifailure_app
  WITH CHECK (current_setting('antifailure.platform_admin', true) = 'on');

CREATE POLICY only_a_declared_operator_changes ON platform_controls
  FOR UPDATE TO antifailure_app
  USING (current_setting('antifailure.platform_admin', true) = 'on')
  WITH CHECK (current_setting('antifailure.platform_admin', true) = 'on');

-- Deliberately no DELETE grant and no DELETE policy. Releasing a control sets
-- engaged_at back to NULL, which leaves the row saying who last touched it and
-- when. Deleting it would erase that, and the row is small.

COMMIT;
