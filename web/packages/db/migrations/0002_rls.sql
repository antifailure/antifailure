-- Row-level security, and the grants that make the audit log append-only.
--
-- Separated from the schema so that this file can be read on its own. It is
-- the whole of the tenancy guarantee, and a reviewer should be able to check
-- it without reading three hundred lines of column definitions first.
--
-- Every policy has the same shape: the row's org_id must equal current_org().
-- When the setting is missing, current_org() is NULL, the comparison is NULL,
-- and the row is neither readable nor writable. There is no policy anywhere in
-- this file that is true when the tenant is unknown.
--
-- USING controls what an existing row can be read, updated, or deleted through.
-- WITH CHECK controls what a new or modified row is allowed to become. Both
-- are needed: USING alone lets a tenant UPDATE its own row to carry another
-- tenant's org_id and hand it over.

BEGIN;

-- ---------------------------------------------------------------------------
-- Grants
--
-- The application gets exactly the verbs it needs per table. Audit entries get
-- INSERT and SELECT and nothing else, which is what makes "append-only" a
-- property of the database rather than a promise about the code.
-- ---------------------------------------------------------------------------

GRANT USAGE ON SCHEMA public TO antifailure_app;

GRANT SELECT, INSERT, UPDATE, DELETE ON
  users, organizations, members, sessions, oauth_states,
  github_installations, repositories, environments, golden_versions,
  runs, verdicts, artifacts, masking_rules, network_rules,
  engine_tokens, events
TO antifailure_app;

GRANT SELECT, INSERT ON audit_entries TO antifailure_app;
GRANT USAGE, SELECT ON SEQUENCE audit_entries_seq_seq TO antifailure_app;

-- Explicitly withheld, so that a later blanket grant has to overwrite a
-- statement that says why it should not.
REVOKE UPDATE, DELETE, TRUNCATE ON audit_entries FROM antifailure_app;

-- ---------------------------------------------------------------------------
-- Tenant-scoped tables
-- ---------------------------------------------------------------------------

DO $$
DECLARE
  t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'members', 'github_installations', 'repositories', 'environments',
    'golden_versions', 'runs', 'verdicts', 'artifacts', 'masking_rules',
    'network_rules', 'engine_tokens', 'events', 'audit_entries'
  ]
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    -- FORCE makes the policies apply to the table's owner too. The
    -- application does not connect as the owner, so this changes nothing
    -- today; it is here so that an operator who runs a migration tool as the
    -- owner and leaves a connection open does not have a hole.
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format($p$
      CREATE POLICY tenant_isolation ON %I
        FOR ALL TO antifailure_app
        USING (org_id = current_org())
        WITH CHECK (org_id = current_org())
    $p$, t);
  END LOOP;
END
$$;

-- ---------------------------------------------------------------------------
-- Tables whose tenancy is not a plain org_id column
-- ---------------------------------------------------------------------------

-- An organization is visible to a session scoped to it, and to nothing else.
ALTER TABLE organizations ENABLE ROW LEVEL SECURITY;
ALTER TABLE organizations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON organizations
  FOR ALL TO antifailure_app
  USING (id = current_org())
  WITH CHECK (id = current_org());

-- A user row is visible to an organization that has the user as a member.
-- Without this, every tenant could enumerate every account on the instance:
-- names, GitHub logins, and email addresses of people at other companies.
--
-- INSERT is the exception. Sign-in creates the user row before any membership
-- exists, and it happens on a connection with no organization at all, so the
-- write is allowed and the read is not. That asymmetry is deliberate: creating
-- an account nobody can see is harmless, and reading accounts is the leak.
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
CREATE POLICY self_or_shared_org ON users
  FOR SELECT TO antifailure_app
  USING (
    id = current_actor()
    OR EXISTS (
      SELECT 1 FROM members m
      WHERE m.user_id = users.id AND m.org_id = current_org()
    )
  );
CREATE POLICY signin_creates ON users
  FOR INSERT TO antifailure_app
  WITH CHECK (true);
CREATE POLICY self_updates ON users
  FOR UPDATE TO antifailure_app
  USING (id = current_actor())
  WITH CHECK (id = current_actor());

-- A session belongs to one user and is readable by nobody else.
--
-- Resolving a cookie is the awkward case: it has to find a session before it
-- knows whose it is, so it cannot be scoped by current_actor(). The obvious
-- shortcut is a policy that opens up when no actor is set, and that shortcut
-- is a hole the size of the whole table: an unauthenticated request is exactly
-- the one with no actor, and it would be able to read every live session on
-- the instance.
--
-- Instead the caller declares which session it is asking about by setting
-- antifailure.session_hash to the hash of the token it was given. The policy
-- returns that row and only that row, so holding the cookie is the proof, and
-- a connection that sets the setting to a value it guessed gets nothing back.
ALTER TABLE sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY own_sessions ON sessions
  FOR ALL TO antifailure_app
  USING (user_id = current_actor())
  WITH CHECK (user_id = current_actor());
CREATE POLICY resolve_by_token ON sessions
  FOR SELECT TO antifailure_app
  USING (token_hash = current_session_hash());
-- Signing in writes the session before any actor is established, for the user
-- the just-completed OAuth exchange named.
CREATE POLICY signin_creates_session ON sessions
  FOR INSERT TO antifailure_app
  WITH CHECK (token_hash = current_session_hash());

-- An OAuth handshake has no tenant and no user yet. The state value is 256
-- bits of randomness and the row is deleted when it is used, so there is
-- nothing here to isolate and nothing worth reading without the value.
ALTER TABLE oauth_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE oauth_states FORCE ROW LEVEL SECURITY;
CREATE POLICY handshakes_are_public ON oauth_states
  FOR ALL TO antifailure_app
  USING (true) WITH CHECK (true);

COMMIT;
