-- Letting an engine token identify its own organization.
--
-- The same shape as the session lookup, and the same trap. An engine presents a
-- bearer token and the control plane has to work out which organization it
-- belongs to, but the table holding the tokens is isolated by organization, so
-- the lookup that determines the tenant cannot run without already knowing it.
--
-- A test caught this in a way worth recording: the case that passed was
-- "an invalid token is refused", which passed because every token was being
-- refused. A suite that only checks refusals cannot tell a working authenticator
-- from a broken one, which is why the suite next to it checks that a valid token
-- is accepted and what it is then allowed to do.
--
-- The caller declares the hash it is presenting, and the policy returns that row
-- and nothing else. Holding the token is what makes the row visible, so this
-- grants nothing that presenting the token did not already grant.

BEGIN;

CREATE OR REPLACE FUNCTION current_engine_token_hash() RETURNS bytea
  LANGUAGE sql STABLE
  AS $$ SELECT decode(nullif(current_setting('antifailure.engine_token_hash', true), ''), 'hex') $$;

CREATE POLICY presented_token ON engine_tokens
  FOR ALL TO antifailure_app
  USING (token_hash = current_engine_token_hash())
  WITH CHECK (token_hash = current_engine_token_hash());

COMMIT;
