-- The second sweeper that could not sweep.
--
-- sweepSessions was written to keep the sessions table from growing without
-- bound, it is called every five minutes from the housekeeping interval in
-- main.ts, and it has removed zero rows on every instance that has ever run
-- it. Every policy on sessions keys on a value the caller declares, either the
-- acting user or the hash of a presented token, or else on the tenant. A
-- sweeper has none of the three, so nothing matched, and DELETE reported
-- success over zero rows, forever.
--
-- This is 0016 a second time, on a table where the shortcut 0016 took is not
-- available. That difference is the whole of this file.
--
-- ---------------------------------------------------------------------------
-- WHY NOT A POLICY ON antifailure_app, WHICH IS WHAT 0016 DID
-- ---------------------------------------------------------------------------
--
-- Permissive policies are OR'd together. A policy on this table naming no
-- tenant does not narrow anything; it WIDENS every other policy on the table
-- for every request the application makes. 0016 could accept that because
-- device_authorizations has no org_id and no user_id: an expired row there
-- names nobody, so a tenant able to reach it learns nothing.
--
-- A session row names somebody. It carries user_id and org_id. Granting
-- antifailure_app a policy over expired sessions would let any signed-in
-- member of any organization enumerate which users of which OTHER
-- organizations had recently been signed in, and delete and re-parent those
-- rows, because a FOR ALL policy with no WITH CHECK reuses its USING clause
-- and an expired row updated to another org_id is still expired. That is a
-- cross-tenant disclosure bought to make housekeeping work.
--
-- ---------------------------------------------------------------------------
-- WHY NOT A PER-TENANT SWEEP
-- ---------------------------------------------------------------------------
--
-- Two reasons, and the second is fatal rather than expensive.
--
-- First, enumerating tenants means reading organizations from a connection
-- with no tenant set, which is the cross-tenant read the whole design exists
-- to refuse. It would be a policy on organizations of exactly the shape ruled
-- out above.
--
-- Second, sessions.org_id IS NULLABLE. A session created at sign-in, before
-- the person has chosen an organization, has no org_id at all. Those are
-- precisely the rows an abandoned sign-in leaves behind, which is to say the
-- rows a sweeper exists for. No per-tenant sweep can ever reach one. The
-- design cannot do the job it is for.
--
-- ---------------------------------------------------------------------------
-- WHAT THIS DOES INSTEAD: A SEPARATE ROLE, ENTERED FOR ONE TRANSACTION
-- ---------------------------------------------------------------------------
--
-- Policies are attached to roles. A policy on a role the application is not
-- acting as does not join the OR, so it cannot widen anything. So housekeeping
-- gets a role of its own, antifailure_sweeper, and the sweep enters it with
-- SET LOCAL ROLE for the length of one transaction.
--
-- Inside that transaction the role can reach expired session rows and nothing
-- else, and it can read two columns of them and nothing else. Outside it,
-- antifailure_app's view of this table is exactly what it was before this
-- file: its own user's sessions, the session whose token it presented, and its
-- own organization's rows for the administrative read and revoke.
--
-- THE ROW RESTRICTION IS THE DATABASE'S CLOCK, NOT THE CALLER'S. The policy
-- says expires_at <= now(). The statement says expires_at <= the cutoff the
-- application passes from its own clock. A row has to be expired by BOTH to be
-- deleted. That is deliberate, and it is what makes this different from a
-- SECURITY DEFINER function taking a cutoff: no argument, however wrong or
-- however hostile, can make this statement reach a live session, because the
-- policy's now() is not a parameter. Passing 'infinity' deletes nothing that
-- has not already expired.
--
-- The two clocks also fail in the safe direction. When they disagree the
-- sweep removes the intersection, which is fewer rows, and expiry is enforced
-- on every read anyway, so a sweeper that is late costs table size and nothing
-- else.
--
-- WHY FOR ALL AND NOT FOR DELETE, same as 0016: a DELETE that names a column
-- in its WHERE clause reads rows to find them, so the SELECT policies apply to
-- that scan too. A delete-only policy deletes nothing and raises nothing.
--
-- ---------------------------------------------------------------------------
-- THE COLUMN GRANT, WHICH IS THE HALF RLS CANNOT DO
-- ---------------------------------------------------------------------------
--
-- Row-level security restricts rows. It has no way to say "this role may not
-- read token_hash", so the policy above, on its own, would leave the sweeper
-- able to read every column of an expired row. A GRANT can say it, so the
-- sweeper is granted SELECT on expires_at only, plus DELETE. Reading
-- token_hash, user_id or org_id, on an expired row or on any other, is refused
-- with SQLSTATE 42501 rather than returning nothing, which is the failure that
-- says so instead of the failure that looks like an empty table.
--
-- ---------------------------------------------------------------------------
-- THE INHERIT TRAP
-- ---------------------------------------------------------------------------
--
-- Postgres decides whether a policy applies by asking whether the current user
-- has the PRIVILEGES OF the policy's role, not whether it is currently acting
-- as it. So granting antifailure_sweeper to antifailure_app in the ordinary
-- inheriting way would make the sweep policy apply to antifailure_app on every
-- request, which is the widening this file exists to avoid, arrived at by
-- accident and completely silently.
--
-- antifailure_app is therefore made NOINHERIT before the grant. It is a member
-- of nothing else, so this changes nothing that exists today; what it does is
-- make the membership a capability that has to be entered with SET ROLE rather
-- than one that is always on. The login role the application actually connects
-- as in production is a member of antifailure_app, and membership is
-- transitive for SET ROLE, so it can enter the sweeper without being able to
-- inherit it either.
--
-- MEASURED, because the mechanism differs by version and the outcome must not.
-- On 16 and later the INHERIT option is fixed into the grant when the grant is
-- made, from the member's setting at that moment, and rolinherit stops
-- governing it afterwards. Measured on 17: after this file,
-- ALTER ROLE antifailure_app INHERIT does NOT hand it the sweeper's
-- privileges, and only re-granting WITH INHERIT TRUE does. On 15 and earlier
-- rolinherit is consulted at run time and the ALTER would be enough. So the
-- ALTER above is written for the older behaviour and the grant option for the
-- newer, and neither is trusted.
--
-- The DO block at the end asserts the OUTCOME rather than either mechanism,
-- which is the only form of this check that is true on both. A migration that
-- silently got it backwards would leave a green suite over an open table.

BEGIN;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'antifailure_sweeper') THEN
    CREATE ROLE antifailure_sweeper NOLOGIN NOBYPASSRLS;
  END IF;
END
$$;

-- No table privileges beyond the two lines below, and none of the schema-wide
-- grants 0002 hands antifailure_app. This role exists to delete expired
-- sessions and to do nothing else.
GRANT USAGE ON SCHEMA public TO antifailure_sweeper;
GRANT SELECT (expires_at), DELETE ON sessions TO antifailure_sweeper;

CREATE POLICY sweep_expired_sessions ON sessions
  FOR ALL TO antifailure_sweeper
  USING (expires_at <= now());

ALTER ROLE antifailure_app NOINHERIT;
GRANT antifailure_sweeper TO antifailure_app;

DO $$
BEGIN
  -- Must NOT be able to act with the sweeper's privileges without asking.
  IF pg_has_role('antifailure_app', 'antifailure_sweeper', 'USAGE') THEN
    RAISE EXCEPTION
      'antifailure_app inherits antifailure_sweeper, so sweep_expired_sessions applies to every ordinary request and every tenant can reach every expired session';
  END IF;
  -- Must be able to enter it deliberately, or the sweep silently deletes
  -- nothing, which is the defect this file is fixing.
  IF NOT pg_has_role('antifailure_app', 'antifailure_sweeper', 'MEMBER') THEN
    RAISE EXCEPTION
      'antifailure_app cannot SET ROLE antifailure_sweeper, so the session sweep cannot run';
  END IF;
  -- The column grant is the only thing keeping token_hash out of reach, and a
  -- table-wide SELECT arriving from anywhere would undo it without a word.
  IF has_column_privilege('antifailure_sweeper', 'sessions', 'token_hash', 'SELECT') THEN
    RAISE EXCEPTION
      'antifailure_sweeper can read sessions.token_hash';
  END IF;
  IF has_column_privilege('antifailure_sweeper', 'sessions', 'user_id', 'SELECT') THEN
    RAISE EXCEPTION
      'antifailure_sweeper can read sessions.user_id';
  END IF;
  IF has_column_privilege('antifailure_sweeper', 'sessions', 'org_id', 'SELECT') THEN
    RAISE EXCEPTION
      'antifailure_sweeper can read sessions.org_id';
  END IF;
END
$$;

COMMIT;
