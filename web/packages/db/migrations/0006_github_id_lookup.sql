-- Reading back a user row that was just written.
--
-- Writing to the users table is allowed. Reading it is not, except for
-- yourself, people you share an organization with, and the holder of a live
-- session. That is the right restriction, and it collides with two ordinary
-- operations:
--
--   Signing in upserts the user and needs the id back, on a connection that has
--   no actor yet, because working out who the actor is is what it is doing.
--
--   A membership sync upserts every member GitHub reported and needs each id to
--   write the membership row. The first time somebody is synced they are not a
--   member yet, so the shared-organization clause does not cover them.
--
-- Both are INSERT ... RETURNING id, and RETURNING is checked against the read
-- policy, so both were refused.
--
-- The loosening is the same shape used for sessions and engine tokens: the
-- caller declares which rows it is asking about, and the policy returns those
-- and nothing else. Here the declaration is the set of GitHub numeric ids being
-- upserted, which the caller only holds because GitHub just returned them, for
-- an account that signed in or for an organization the caller administers.
--
-- It is set for the length of one transaction and cleared by every other one,
-- so a request that has not declared an id reads nothing new.

BEGIN;

CREATE OR REPLACE FUNCTION current_github_ids() RETURNS bigint[]
  LANGUAGE sql STABLE
  AS $$
    SELECT coalesce(
      string_to_array(nullif(current_setting('antifailure.github_ids', true), ''), ',')::bigint[],
      ARRAY[]::bigint[])
  $$;

CREATE POLICY declared_github_ids ON users
  FOR SELECT TO antifailure_app
  USING (github_id = ANY(current_github_ids()));

COMMIT;
