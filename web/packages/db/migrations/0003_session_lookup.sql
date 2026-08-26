-- Letting a session resolve itself, without letting it resolve anything else.
--
-- Resolving a cookie needs three things: the session row, the user it belongs
-- to, and that user's role in the organization the session is scoped to. The
-- session row was already reachable by presenting the token hash. The other two
-- were not, and the reason is worth recording, because the shape of the fix is
-- the whole argument for enforcing tenancy in the database at all.
--
-- The users table is only visible to a caller that shares an organization with
-- the row, and the members table only to a caller whose tenant is set. On the
-- sign-in path neither is true yet: that is the request that is trying to work
-- out who the caller is. So the join returned nothing, every request answered
-- "sign in to do this", and the application looked completely broken.
--
-- The tempting fix is a policy that opens up when nothing is set, and that is
-- the same mistake as before: the unauthenticated request is precisely the one
-- with nothing set. What is added instead is narrow. A user row is visible to
-- somebody presenting a live session belonging to that user, which is not a
-- new privilege at all: holding the cookie already meant being that person.
--
-- The role is not solved here. It is read in a second query that runs scoped to
-- the organization the session named, so the existing policy covers it, and the
-- application never needs a connection that can see members across tenants.

BEGIN;

CREATE POLICY session_owner ON users
  FOR SELECT TO antifailure_app
  USING (
    EXISTS (
      SELECT 1 FROM sessions s
      WHERE s.user_id = users.id
        AND s.token_hash = current_session_hash()
        AND s.revoked_at IS NULL
    )
  );

COMMIT;
