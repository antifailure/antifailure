-- Seven hundred dead credentials on the page that teaches somebody the CLI.
--
-- WHAT WAS ON THE SCREEN. /cli ends in a table called "Terminals and tokens",
-- which is the only place in the product a person can see what can act as their
-- organization from outside a browser. On the hosted installation it had grown
-- to roughly seven hundred rows, three days of continuous integration, eight
-- per run, every one of them a fifteen minute workflow identity that had been
-- dead since the minute after it was issued. The one row that belonged to a
-- person, their own signed in terminal, was near the bottom of it.
--
-- WHY THE TABLE ONLY EVER GREW. `oidc` rows are minted by
-- apps/api/src/github/exchange.ts, they carry an expires_at fifteen minutes
-- out, and authenticateEngine refuses an expired one on every request. So they
-- stop working on time and nothing has ever removed one. There is no sweeper
-- for this table, and there could not be: every policy on engine_tokens keys on
-- the tenant or on the hash of a presented token, and a sweep is nobody's
-- request. It has neither. This is 0024 a third time, on a third table.
--
-- ---------------------------------------------------------------------------
-- WHY DELETING A TOKEN ROW IS ALLOWED HERE WHEN 0033 REFUSED IT
-- ---------------------------------------------------------------------------
--
-- 0033 withheld DELETE on engine_tokens from antifailure_admin and gave the
-- reason: a revoked credential is a row that records what was allowed to act as
-- this organization and when that stopped, and deleting it destroys the
-- evidence somebody comes looking for. That argument is correct and it is
-- honoured here rather than overturned.
--
-- It is honoured by what the policy below will not touch. A REVOKED row is
-- never swept, on any kind, ever: revocation is a deliberate act somebody took
-- during an incident and the row is the record of it. A `cli` row is never
-- swept even after its ninety days, because it names the person whose terminal
-- it was. An `engine` row is never swept, because it does not expire at all and
-- because somebody pasted it into a build machine on purpose.
--
-- What is swept is one kind of row: an `oidc` credential that expired without
-- ever being revoked. Nothing about it was a decision anybody took. It was
-- issued to a workflow run by a machine and it died fifteen minutes later on a
-- timer, and the evidence 0033 is protecting is already somewhere else and
-- somewhere better: exchange.ts writes an audit_entries row for every one of
-- these, carrying the prefix, the repository, the run id, the run attempt, the
-- workflow file and the expiry. audit_entries is append only, the application
-- holds SELECT and INSERT on it and nothing more, and no role in this schema
-- can delete from it. So the record of every credential this control plane has
-- ever issued to a workflow survives this sweep in full. What goes is the
-- expired secret's own row.
--
-- ---------------------------------------------------------------------------
-- WHY A ROLE AND NOT A POLICY ON antifailure_app
-- ---------------------------------------------------------------------------
--
-- The whole of 0024's argument, unchanged, and it is sharper on this table
-- rather than weaker. Permissive policies are OR'd. A policy on antifailure_app
-- admitting expired tokens with no tenant named would let any signed in member
-- of any organization read every other organization's token rows, which carry
-- org_id, name and prefix, and a FOR ALL policy with no WITH CHECK reuses its
-- USING clause, so an expired row updated to another org_id is still expired.
-- That is a cross tenant disclosure on the credentials table bought to make
-- housekeeping work.
--
-- A per tenant sweep is no better here than it was there. Enumerating tenants
-- means reading organizations from a connection with no tenant set, which is
-- the cross tenant read the whole design exists to refuse.
--
-- antifailure_sweeper already exists, 0024 created it, antifailure_app is
-- already a NOINHERIT member of it, and client.ts already enters it with a
-- transaction local SET ROLE. So this file adds a grant and a policy and no new
-- mechanism at all.
--
-- ---------------------------------------------------------------------------
-- THE ROW RESTRICTION IS THE DATABASE'S CLOCK, NOT THE CALLER'S
-- ---------------------------------------------------------------------------
--
-- The same property 0024 relies on, and it is what makes the grace period real
-- rather than advisory. The policy says expires_at <= now() minus a day. The
-- statement in apps/api/src/tokens.ts says expires_at <= a cutoff the
-- application computed from its own clock. A row has to be past BOTH to be
-- deleted, so the argument passed from the application can only ever narrow
-- what is removed. No cutoff, however wrong and however hostile, can make that
-- statement reach a token that is still live, because the policy's now() is not
-- a parameter. Passing 'infinity' deletes nothing that has not already been
-- dead for a day.
--
-- A DAY, and the number is not arbitrary. These credentials live fifteen
-- minutes, so a day is ninety six lifetimes: whatever a person is investigating
-- when they open that page, this morning's runs and yesterday's are still in
-- front of them, and only the runs nobody can still be asking about go. It also
-- bounds the table at roughly one day of CI rather than at all of history,
-- which is the difference between a page with a dozen grouped rows on it and
-- the page this file is named after.
--
-- WHY FOR ALL AND NOT FOR DELETE, same as 0016 and 0024: a DELETE that names a
-- column in its WHERE clause reads rows to find them, so the SELECT policies
-- apply to that scan too. A delete only policy deletes nothing and raises
-- nothing, which is the failure mode this whole family of files exists to end.
--
-- ---------------------------------------------------------------------------
-- THE COLUMN GRANT, WHICH IS THE HALF RLS CANNOT DO
-- ---------------------------------------------------------------------------
--
-- Row level security restricts rows. It cannot say "this role may not read
-- token_hash", so the policy on its own would leave the sweeper able to read
-- every column of an expired row, including the hash, the organization it
-- belongs to and the repository in its name. A GRANT can say it. The sweeper is
-- given SELECT on the three columns its own WHERE clause names and DELETE, and
-- nothing else. Reading token_hash, org_id, prefix or name is refused with
-- SQLSTATE 42501, which is the failure that says so rather than the failure
-- that looks like an empty table.

BEGIN;

-- 0024 created the role. Recreated here only for an installation that somehow
-- reaches this file without it, which would otherwise fail on the GRANT with a
-- message about a missing role rather than about a missing migration.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'antifailure_sweeper') THEN
    CREATE ROLE antifailure_sweeper NOLOGIN NOBYPASSRLS;
    ALTER ROLE antifailure_app NOINHERIT;
    GRANT antifailure_sweeper TO antifailure_app;
  END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO antifailure_sweeper;
GRANT SELECT (kind, expires_at, revoked_at), DELETE ON engine_tokens TO antifailure_sweeper;

CREATE POLICY sweep_expired_workflow_tokens ON engine_tokens
  FOR ALL TO antifailure_sweeper
  USING (
    kind = 'oidc'
    AND revoked_at IS NULL
    AND expires_at IS NOT NULL
    AND expires_at <= now() - interval '1 day'
  );

-- The sweep reads expires_at to find its rows, so it scans them. Without this
-- it is a sequential scan of the credentials table every five minutes, on every
-- replica, forever. Partial on the predicate the policy already fixes, so the
-- index is the size of the rows being swept rather than the size of the table.
CREATE INDEX engine_tokens_expiring_workflow_idx
  ON engine_tokens (expires_at)
  WHERE kind = 'oidc' AND revoked_at IS NULL;

DO $$
BEGIN
  -- The same trap 0024 names. Postgres decides whether a policy applies by
  -- asking whether the current user has the PRIVILEGES OF the policy's role,
  -- not whether it is acting as it, so an inheriting grant would apply the
  -- policy above to every ordinary request and every tenant would be able to
  -- read and delete every other tenant's expired workflow credentials.
  -- Asserted here as well as in 0024 because the consequence is now worse and
  -- because a later migration re-granting the membership WITH INHERIT TRUE
  -- would break this file rather than that one.
  IF pg_has_role('antifailure_app', 'antifailure_sweeper', 'USAGE') THEN
    RAISE EXCEPTION
      'antifailure_app inherits antifailure_sweeper, so sweep_expired_workflow_tokens applies to every ordinary request and every tenant can reach every other tenant''s token rows';
  END IF;
  IF NOT pg_has_role('antifailure_app', 'antifailure_sweeper', 'MEMBER') THEN
    RAISE EXCEPTION
      'antifailure_app cannot SET ROLE antifailure_sweeper, so the workflow token sweep cannot run';
  END IF;

  -- The column grant is the only thing keeping the credential hash and the
  -- organization out of reach, and a table wide SELECT arriving from anywhere
  -- would undo it without a word.
  IF has_column_privilege('antifailure_sweeper', 'engine_tokens', 'token_hash', 'SELECT') THEN
    RAISE EXCEPTION 'antifailure_sweeper can read engine_tokens.token_hash';
  END IF;
  IF has_column_privilege('antifailure_sweeper', 'engine_tokens', 'org_id', 'SELECT') THEN
    RAISE EXCEPTION 'antifailure_sweeper can read engine_tokens.org_id';
  END IF;
  IF has_column_privilege('antifailure_sweeper', 'engine_tokens', 'prefix', 'SELECT') THEN
    RAISE EXCEPTION 'antifailure_sweeper can read engine_tokens.prefix';
  END IF;
  IF has_column_privilege('antifailure_sweeper', 'engine_tokens', 'name', 'SELECT') THEN
    RAISE EXCEPTION 'antifailure_sweeper can read engine_tokens.name';
  END IF;

  -- 0033 withheld these from the operator and this file must not hand them to
  -- housekeeping by accident. The sweeper deletes; it never revokes, and it
  -- never mints.
  IF has_table_privilege('antifailure_sweeper', 'engine_tokens', 'UPDATE') THEN
    RAISE EXCEPTION 'antifailure_sweeper can update engine_tokens';
  END IF;
  IF has_table_privilege('antifailure_sweeper', 'engine_tokens', 'INSERT') THEN
    RAISE EXCEPTION 'antifailure_sweeper can insert into engine_tokens';
  END IF;
END
$$;

COMMIT;
