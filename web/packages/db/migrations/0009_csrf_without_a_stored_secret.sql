-- Removing the one secret the sessions table stored, and the read it forced.
--
-- The previous migration added an UPDATE-only policy so that an organization
-- could revoke a departing member's sessions without being able to read them.
-- It updated nothing, and the reason is a rule that is easy to miss: an UPDATE
-- whose WHERE clause references the table also has the SELECT policies applied
-- to it. USING on the UPDATE policy is not enough to find a row that no read
-- policy exposes. So the choice was between granting the read and not having
-- the feature.
--
-- Granting the read was unattractive because the row carried csrf_secret, and
-- anybody who can read that can mint a valid CSRF token for that session.
--
-- The better answer was to stop storing it. The token a mutating request has to
-- present is now derived from the session token itself, which the client
-- already holds and the database never sees: HMAC(session token, "csrf"). It is
-- safe to hand to the page for the same reason the old one was, being a one-way
-- function of a secret rather than the secret, and it needs no column, no key
-- to manage, and nothing to keep in step when a session rotates.
--
-- With that column gone, a session row holds a hash, a user, an organization,
-- and when it was last used. Letting an organization read its own sessions is
-- then an ordinary administrative view rather than a leak, and it is what makes
-- revocation work. Reading another organization's sessions is still refused,
-- and the cross-tenant suite still proves it.

BEGIN;

ALTER TABLE sessions DROP COLUMN csrf_secret;

CREATE POLICY org_reads_sessions ON sessions
  FOR SELECT TO antifailure_app
  USING (org_id = current_org());

COMMIT;
