-- Revoking the sessions of somebody who has left.
--
-- When a membership sync removes a member, their sessions have to stop working
-- now rather than whenever the session they are already holding happens to
-- expire. That update runs scoped to the organization and touches rows keyed by
-- a token nobody in the transaction holds, so no existing policy matched and it
-- updated nothing. Silently: an update that matches no row is not an error.
--
-- The policy added here is deliberately UPDATE only, and that is the whole
-- design. Sessions in an organization can be revoked by that organization,
-- which is an administrative action anybody would expect to exist. They cannot
-- be read by it, because a session row carries the secret that CSRF tokens are
-- derived from, and there is no reason for one member to be able to read
-- another's. An UPDATE finds the row without returning it.
--
-- The cross-tenant suite still proves that reading another tenant's sessions
-- returns nothing, and it still passes, because this grants no read.

BEGIN;

CREATE POLICY org_revokes_sessions ON sessions
  FOR UPDATE TO antifailure_app
  USING (org_id = current_org())
  WITH CHECK (org_id = current_org());

COMMIT;
