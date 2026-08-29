-- One person, one row, however they arrive.
--
-- THE BUG THIS CLOSES WAS DEMONSTRATED AGAINST A REAL MICROSOFT ENTRA TENANT,
-- and it made offboarding silently fail:
--
--   1. SCIM provisions a person. A users row is written with identity_source
--      `scim` and a membership row beside it.
--   2. They leave. SCIM deprovisions, which DELETES the membership. That
--      deletion is deliberate and correct: it is what makes deprovisioning
--      structural rather than a flag nobody reads.
--   3. They come back, or the application assignment merely flaps, and they
--      sign in through SAML before SCIM has re-provisioned them.
--   4. Single sign-on finds no membership for that address, because it looks
--      the person up by joining THROUGH members, so it creates a SECOND users
--      row with the same email.
--
-- From that moment the directory manages one row and sign-in manages the
-- other. A later SCIM deprovision revokes sessions for the row IT points at,
-- which is not the row the person actually signs in as, so Entra reports
-- action=Disable status=Success while the membership and a live session
-- survive. Measured, not theorised: memberships 1, sessions 1, after a
-- successful offboard.
--
-- WHY THE LOOKUP JOINED THROUGH members IN THE FIRST PLACE, because the fix is
-- shaped entirely by this: `users` is not tenant scoped, and its row level
-- security only exposes a user who shares an organisation with the caller. A
-- user with no memberships at all is INVISIBLE to antifailure_app by
-- construction. The join was not laziness, it was the only lookup the policies
-- permit, and the duplicate row was the price of it.
--
-- So the lookup that can see an orphan has to be SECURITY DEFINER, exactly like
-- user_belongs_only_to in 0013, and it has to be narrow enough that being
-- SECURITY DEFINER buys an attacker nothing.

-- Returns an existing directory account for this address that belongs to NO
-- organisation at all, or null.
--
-- The safety argument is the orphan condition itself. A row with no membership
-- anywhere is a row no tenant can currently see and no tenant currently owns,
-- so adopting it into the caller's organisation cannot expose another tenant's
-- data: there is no other tenant. The moment a user belongs to somebody, this
-- returns null and the caller must go through the ordinary membership path.
--
-- github accounts are excluded deliberately. A GitHub row carries a github_id
-- that a directory assertion has no authority over, and adopting one on the
-- strength of an emailed claim would let a verified domain take over an account
-- that never came from that directory. Linking GitHub to single sign-on stays
-- where it already is: an explicit, audited step that requires the person to be
-- a member first.
CREATE OR REPLACE FUNCTION adoptable_directory_user(target_email text) RETURNS uuid
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = public, pg_temp
  AS $$
    SELECT u.id
    FROM users u
    WHERE lower(u.email) = lower(target_email)
      AND u.identity_source IN ('sso', 'scim')
      AND NOT EXISTS (SELECT 1 FROM members m WHERE m.user_id = u.id)
    -- Oldest first, so repeated cycles converge on one row rather than
    -- wandering between several if any already exist.
    ORDER BY u.created_at
    LIMIT 1
  $$;

REVOKE ALL ON FUNCTION adoptable_directory_user(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION adoptable_directory_user(text) TO antifailure_app;

COMMENT ON FUNCTION adoptable_directory_user(text) IS
  'The id of a directory account for this address that belongs to no organisation, or null. '
  'Lets single sign-on and SCIM re-adopt somebody they previously deprovisioned instead of '
  'creating a second row, which is what allowed an Entra offboard to report success while the '
  'person kept a live session.';
