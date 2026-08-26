-- The rest of the sign-in path, which writes before a tenant exists.
--
-- Three operations were being refused silently, and silently is the word that
-- matters: a row-level security policy does not raise on an UPDATE or a DELETE
-- that matches nothing, it just changes nothing. So sign-out returned success
-- and left the session working, session rotation left the old session usable,
-- and the refresh of last_seen_at never happened, which made every session die
-- at the idle timeout instead of living out its lifetime. Three real defects,
-- none of which produced an error anywhere.
--
-- What each one needs:
--
-- A session presenting its own token may refresh and delete itself. The read
-- policy already keys on the presented hash; extending it to the other verbs
-- grants nothing new, because holding the token was already equivalent to being
-- that session.
--
-- Sign-in has to work out which organizations the person may enter, which is a
-- question no single tenant scope can answer: it spans tenants by nature. It
-- reads the installations for the GitHub organizations the user belongs to, and
-- reads and writes its own membership rows. Both are keyed on values the caller
-- holds only because GitHub just returned them for this person.
--
-- The alternative in every case was a policy that opens up when nothing is set,
-- and the request with nothing set is the unauthenticated one.

BEGIN;

-- ---------------------------------------------------------------------------
-- A session may act on itself.
-- ---------------------------------------------------------------------------

DROP POLICY IF EXISTS resolve_by_token ON sessions;
DROP POLICY IF EXISTS signin_creates_session ON sessions;

CREATE POLICY presented_session ON sessions
  FOR ALL TO antifailure_app
  USING (token_hash = current_session_hash())
  WITH CHECK (token_hash = current_session_hash());

-- ---------------------------------------------------------------------------
-- Sign-in reads installations and writes its own membership.
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION current_signin_user() RETURNS uuid
  LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('antifailure.signin_user_id', true), '')::uuid $$;

CREATE OR REPLACE FUNCTION current_github_logins() RETURNS text[]
  LANGUAGE sql STABLE
  AS $$
    SELECT coalesce(
      string_to_array(lower(nullif(current_setting('antifailure.github_logins', true), '')), ','),
      ARRAY[]::text[])
  $$;

CREATE POLICY signin_reads_installations ON github_installations
  FOR SELECT TO antifailure_app
  USING (lower(account_login) = ANY(current_github_logins()));

CREATE POLICY signin_own_membership ON members
  FOR ALL TO antifailure_app
  USING (user_id = current_signin_user())
  WITH CHECK (user_id = current_signin_user());

COMMIT;
