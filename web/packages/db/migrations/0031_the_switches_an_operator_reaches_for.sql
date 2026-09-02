-- WHAT PROTECTS THIS TABLE IS A GRANT, NOT A POLICY.
--
-- There is no org_id here to key a policy on: these rows are configuration for
-- the installation, not data belonging to a tenant. So row level security
-- cannot be the boundary, and saying so is better than a policy that looks
-- like one.
--
-- The application role is granted SELECT and nothing else. It cannot INSERT or
-- UPDATE, so the privilege is missing rather than the predicate being false,
-- and a bug in any tenant route raises `permission denied` instead of quietly
-- matching no rows. Writes are granted to antifailure_admin, the BYPASSRLS
-- operator role from the admin portal's own migration, which the application
-- has no password for and therefore cannot acquire.
--
-- An earlier draft of this file gated writes on
-- `current_setting('antifailure.platform_admin', true) = 'on'`, contained only
-- by the fact that one function set it. That is a CLAIM: the predicate is true
-- for anything that can set a setting. A grant to a role the application
-- cannot become is a CREDENTIAL, and it removes the distinction rather than
-- arguing about it. The setting, and the pool scope that set it, are gone.
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

-- The operator role, created here only if it does not exist yet.
--
-- It is created by the admin portal's own migration, which runs before this
-- one. The guard is for a database where this file is applied on its own, and
-- it mirrors how 0001 creates antifailure_app: NOLOGIN, so a password has to be
-- set deliberately by whoever operates the installation, and BYPASSRLS,
-- because reaching every tenant is the whole reason the role exists.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'antifailure_admin') THEN
    CREATE ROLE antifailure_admin NOLOGIN BYPASSRLS;
  END IF;
END
$$;

-- Read on the hot path, by everyone. Every request has to be able to learn
-- that the installation is paused, including one that has no organization yet,
-- and a check that needs a privileged connection is a check somebody will skip.
GRANT SELECT ON platform_controls TO antifailure_app;

-- Write by the operator only. Deliberately no INSERT or UPDATE to
-- antifailure_app: the privilege is absent rather than the predicate false, so
-- a tenant route that reached this table raises rather than silently writing
-- nothing.
GRANT SELECT, INSERT, UPDATE ON platform_controls TO antifailure_admin;

-- Deliberately no DELETE to either role. Releasing a control sets engaged_at
-- back to NULL, which leaves the row saying who last touched it and when.
-- Deleting it would erase that, and the row is small.

ALTER TABLE platform_controls ENABLE ROW LEVEL SECURITY;

-- FORCE is deliberately NOT set. Forcing row security applies policies to the
-- table's owner too, and the owner is the migration role that has to be able
-- to repair this table by hand during the incident it exists for.
--
-- The policy below is the whole policy set, and it only concerns
-- antifailure_app. antifailure_admin holds BYPASSRLS, so policies do not apply
-- to it at all: what confines the application role here is the missing grant,
-- one line above, not this.
CREATE POLICY read_the_switches ON platform_controls
  FOR SELECT TO antifailure_app
  USING (true);

COMMIT;
