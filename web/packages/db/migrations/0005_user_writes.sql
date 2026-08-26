-- What the policy on the users table is actually protecting.
--
-- The first version restricted reads and writes alike, and the writes turned
-- out to be wrong in a way that broke sign-in outright: creating an account
-- happens before the account exists, and refreshing somebody's name during a
-- membership sync happens for a user who may not be a member yet. Both are
-- upserts keyed on the GitHub numeric id, and both were refused.
--
-- The fix is not to loosen the read policy, which is the one that matters. It is
-- to say plainly what this table needs isolating from.
--
-- The users table holds no tenant data. It holds a GitHub id, a login, a name,
-- and an email address, for every person who has ever signed in to this
-- instance. The harm in exposing it is enumeration: one customer learning the
-- names and addresses of people at another company. That is a read.
--
-- A write is keyed on a GitHub id, and a caller only has a GitHub id because
-- GitHub just handed it to them: either the person signed in, or they appeared
-- in the member list of an organization the caller is syncing. Neither lets one
-- tenant discover anything about another, because a write returns nothing about
-- the row it touched beyond the id the caller already supplied.
--
-- So reads stay restricted to yourself, to people you share an organization
-- with, and to the holder of a live session for that user. Writes are allowed,
-- and the fields a write may set are the profile fields GitHub returns.
--
-- What this does not protect against: an actor who already has the application
-- role's credentials could overwrite a display name. That is true, it is not
-- worth adding a special case for, and it is recorded here so that nobody later
-- reads this policy as claiming more than it does.

BEGIN;

DROP POLICY IF EXISTS signin_creates ON users;
DROP POLICY IF EXISTS self_updates ON users;

CREATE POLICY profile_writes ON users
  FOR INSERT TO antifailure_app
  WITH CHECK (true);

CREATE POLICY profile_refresh ON users
  FOR UPDATE TO antifailure_app
  USING (true)
  WITH CHECK (true);

COMMIT;
