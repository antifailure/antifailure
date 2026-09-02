-- The operator's portal, and the one hole it is not allowed to open.
--
-- WHY THIS FILE IS DELICATE. Every other table in this schema is tenant
-- scoped, and 0002 is 149 lines whose entire content is "the row's org_id must
-- equal current_org()". An operator console is inherently cross tenant: it
-- exists to answer "which organization is doing that" and no policy in this
-- schema will answer that question. So something here has to widen, and a
-- careless widening is not a bug in one screen, it is every tenant able to
-- read every other tenant's rows.
--
-- WHAT THIS DELIBERATELY DOES NOT DO. It does not grant BYPASSRLS, it does not
-- create a superuser, it does not add a second login role, and it does not
-- turn row security off. antifailure_app stays NOLOGIN NOBYPASSRLS and owns
-- nothing, exactly as 0001 left it.
--
-- A second role was the obvious alternative and it is a trap. Postgres applies
-- a policy when the current user HAS THE PRIVILEGES OF the named role, not
-- when it is acting as that role, so `FOR SELECT TO antifailure_admin` would
-- also apply to antifailure_app the moment anybody granted membership, and it
-- would be a hole that no test asserting "the admin role can read" would ever
-- notice.
--
-- WHAT IT DOES INSTEAD. It is the sixth instance of a pattern this schema
-- already uses five times. withGitHubAccount, withStripeCustomer,
-- withGitHubDelivery, withPullRequestCallback and withSweeper are all non
-- tenant scopes, and each one has the same shape: the connection DECLARES a
-- value it must already hold, and a policy is keyed on that value. From
-- client.ts: "Declaring a value it did not receive from a client returns
-- nothing."
--
-- So the admin scope declares the hash of the admin session cookie the request
-- arrived with. current_admin_user() returns a row only for a live, unrevoked,
-- unexpired session whose stored hash matches. The cross tenant policies below
-- are keyed on that, which means the widening predicate is FALSE on every
-- connection in the system except one that is physically holding an operator's
-- session token. It is the same construction as the resolve_by_token policy on
-- sessions in 0002, which solves the identical problem of finding a row before
-- you know whose it is.
--
-- Permissive policies are OR'd together, so each policy added here widens the
-- SELECT on its table for anyone the predicate is true for. That is the whole
-- risk and it is why the predicate must not be an assertion. `current_admin()
-- IS NOT NULL` where current_admin() merely reads a setting would be true for
-- anything that can set the setting, which is every caller. Requiring the hash
-- to MATCH A STORED ROW is what makes it a credential rather than a claim.
--
-- The other half of that guarantee lives in client.ts: every one of the seven
-- scopes sets antifailure.admin_session_hash to the empty string, so a
-- transaction that is not the admin scope cannot be running under one. There
-- is a test that reads the source and proves every scope names it, so one
-- added later that forgets fails rather than looking correct.
--
-- WHY THE AUDIT LOG IS A SECOND TABLE. audit_entries.org_id is NOT NULL and
-- references organizations, its index is (org_id, seq DESC), and appendAudit
-- takes an advisory lock keyed on the organization and reads the chain head
-- with WHERE org_id = ... . A platform level action has no organization: an
-- operator signing in, an operator being granted a role, an operator listing
-- every tenant. There is nothing to key those to. Making org_id nullable would
-- not fix it, it would fork the chain, because NULL never equals current_org()
-- and the per organization head lookup would find nothing to chain onto.
--
-- So admin actions get their own table and their own chain, and the column
-- naming the tenant an action concerned is called subject_org_id, not org_id:
-- the row belongs to the platform, not to that tenant, and a column called
-- org_id would claim a tenancy this row does not have. An action
-- that concerns a tenant is written to both chains: this one, and that
-- tenant's own audit_entries, because a customer being able to see what an
-- operator did to their organization is most of the point of having an audit
-- log at all.

BEGIN;

-- ---------------------------------------------------------------------------
-- Who an operator is
-- ---------------------------------------------------------------------------

-- Separate from users, and never joined to it.
--
-- The product's users table is populated by GitHub sign-in: anybody who
-- installs the App gets a row. If administrative power were a flag on that
-- table, then the question "is this person an operator" would be answered by a
-- row that a customer's own OAuth flow created, and compromising an operator's
-- GitHub account would be compromising the platform. Two tables means an
-- operator's product account and their operator account are different
-- credentials that fail independently.
--
-- There is deliberately no foreign key between them, not even a nullable one.
-- A column linking the two is a column somebody eventually authenticates on.
CREATE TABLE admin_users (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  -- Stored lowercased, the same rule users follows, so that an operator who
  -- types their address with different capitalisation is the same operator.
  email           text NOT NULL UNIQUE,
  name            text NOT NULL,
  role            text NOT NULL,
  -- Scrypt, salt stored beside it. NULL means this operator has been created
  -- but not provisioned and CANNOT sign in: there is no password that hashes
  -- to NULL, so the comparison has nothing to succeed against. That is the
  -- state a newly created operator sits in until somebody sets a password, and
  -- it is why no default credential exists anywhere in this schema or in the
  -- source that reads it.
  password_hash   bytea,
  password_salt   bytea,
  password_set_at timestamptz,
  -- The permanent root operator. Cannot be deleted, demoted, suspended, or
  -- stripped of its role by anybody, including itself. Enforced by triggers
  -- below rather than only in the application, because an invariant that lives
  -- in one code path is an invariant until somebody writes a second code path.
  is_root         boolean NOT NULL DEFAULT false,
  suspended_at    timestamptz,
  suspended_reason text,
  last_signed_in_at timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT admin_users_email_lowercase CHECK (email = lower(email)),
  -- A CHECK rather than an enum. Adding a value to an enum and using it in the
  -- same transaction is a restriction nobody remembers until a migration fails
  -- on it, and this list will grow.
  CONSTRAINT admin_users_role_known CHECK (role IN (
    'owner', 'super_admin', 'infrastructure', 'security',
    'billing', 'support', 'analytics', 'read_only'
  )),
  -- A password is a hash AND a salt or it is neither. A half written
  -- credential would otherwise be a row that verification has to guess about.
  CONSTRAINT admin_users_password_whole CHECK (
    (password_hash IS NULL) = (password_salt IS NULL)
  )
);

-- Exactly one root operator, ever. A partial unique index rather than a
-- trigger, because "there is at most one" is a uniqueness property and the
-- index states it in the language the database already enforces.
CREATE UNIQUE INDEX admin_users_single_root ON admin_users ((true)) WHERE is_root;

-- ---------------------------------------------------------------------------
-- The root operator invariant
--
-- Four refusals, each with a test that was watched failing before the trigger
-- existed. They are triggers rather than application guards because the
-- application has several paths that write this table and a guard on one of
-- them is a guard on one of them.
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION admin_root_is_permanent() RETURNS trigger
  LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.is_root THEN
      RAISE EXCEPTION 'the root operator cannot be deleted'
        USING ERRCODE = 'raise_exception';
    END IF;
    RETURN OLD;
  END IF;

  IF OLD.is_root THEN
    IF NOT NEW.is_root THEN
      RAISE EXCEPTION 'the root operator cannot stop being the root operator'
        USING ERRCODE = 'raise_exception';
    END IF;
    IF NEW.role <> 'owner' THEN
      RAISE EXCEPTION 'the root operator cannot be demoted from owner'
        USING ERRCODE = 'raise_exception';
    END IF;
    IF NEW.suspended_at IS NOT NULL THEN
      RAISE EXCEPTION 'the root operator cannot be suspended'
        USING ERRCODE = 'raise_exception';
    END IF;
  END IF;

  -- Promoting somebody else to root is the same hole as demoting the root:
  -- it would make a second permanent operator, which the unique index would
  -- refuse anyway, but refusing it here names the reason.
  IF TG_OP = 'UPDATE' AND NOT OLD.is_root AND NEW.is_root THEN
    RAISE EXCEPTION 'the root operator is set once, at provisioning, and cannot be granted later'
      USING ERRCODE = 'raise_exception';
  END IF;

  RETURN NEW;
END
$$;

CREATE TRIGGER admin_root_is_permanent_upd
  BEFORE UPDATE ON admin_users
  FOR EACH ROW EXECUTE FUNCTION admin_root_is_permanent();

CREATE TRIGGER admin_root_is_permanent_del
  BEFORE DELETE ON admin_users
  FOR EACH ROW EXECUTE FUNCTION admin_root_is_permanent();

-- ---------------------------------------------------------------------------
-- Operator sessions
-- ---------------------------------------------------------------------------

-- The same shape as sessions, and separate for the same reason admin_users is
-- separate: a product session must never be usable against this portal, and
-- the way to guarantee that is for the portal to look in a different table.
CREATE TABLE admin_sessions (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  -- Only the hash. A leaked backup is a list of hashes, not a pile of working
  -- operator cookies.
  token_hash      bytea NOT NULL UNIQUE,
  admin_user_id   uuid NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
  ip              inet,
  user_agent      text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  last_seen_at    timestamptz NOT NULL DEFAULT now(),
  expires_at      timestamptz NOT NULL,
  revoked_at      timestamptz
);
CREATE INDEX admin_sessions_user_idx ON admin_sessions (admin_user_id);
CREATE INDEX admin_sessions_expiry_idx ON admin_sessions (expires_at);

-- ---------------------------------------------------------------------------
-- The scope
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION current_admin_session_hash() RETURNS bytea
  LANGUAGE sql STABLE
  AS $$ SELECT decode(nullif(current_setting('antifailure.admin_session_hash', true), ''), 'hex') $$;

-- The operator this connection is holding a live session for, or NULL.
--
-- NULL is the answer on every connection in the system except one that
-- presented a cookie matching a stored hash, which is what makes every policy
-- below deny by default in the same way current_org() does.
--
-- Reads admin_sessions, so the policy on admin_sessions must NOT call this
-- function or the two would recurse forever. That policy is keyed directly on
-- current_admin_session_hash() instead, which is why these are two functions
-- and not one.
--
-- SECURITY DEFINER, and that is not a shortcut. Without it this recurses:
-- every policy below is written as `current_admin_user() IS NOT NULL`, this
-- function reads admin_sessions and admin_users, and the policies on THOSE two
-- tables are among the ones written that way. Postgres refuses the query with
-- "infinite recursion detected in policy for relation admin_sessions", so the
-- boundary denies everybody, which is the failure that looks like a working
-- deny-by-default until somebody notices the portal is empty.
--
-- Definer rights are what let this one function read the two tables it needs
-- without the policies that are built ON TOP of it applying to it. It is the
-- standard answer to policy recursion and the reason admin_users and
-- admin_sessions get ENABLE but not FORCE row level security: FORCE would
-- apply the policies to the owner as well, which is precisely the thing being
-- stepped around here.
--
-- What makes it safe to hand out: it takes NO ARGUMENTS, so there is nothing a
-- caller can influence, and it returns one uuid rather than a row, so it leaks
-- nothing beyond "the session you already presented belongs to this operator".
-- search_path is pinned so a caller cannot put a temp table in front of the
-- ones it names.
CREATE OR REPLACE FUNCTION current_admin_user() RETURNS uuid
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = public, pg_temp
  AS $$
    SELECT s.admin_user_id
    FROM admin_sessions s
    JOIN admin_users u ON u.id = s.admin_user_id
    WHERE s.token_hash = current_admin_session_hash()
      AND s.revoked_at IS NULL
      AND s.expires_at > now()
      -- A suspended operator's live cookie stops working on the next
      -- statement rather than at its next sign-in. Suspending somebody who is
      -- signed in is exactly the case where the delay matters.
      AND u.suspended_at IS NULL
  $$;

-- ---------------------------------------------------------------------------
-- The operator audit chain
-- ---------------------------------------------------------------------------

CREATE TABLE admin_audit_entries (
  -- A sequence for the same reason audit_entries uses one: the chain is an
  -- order, and the order has to be assigned by the database.
  seq             bigserial PRIMARY KEY,
  admin_user_id   uuid REFERENCES admin_users(id) ON DELETE SET NULL,
  -- Kept as text as well, so deleting an operator does not erase who did what.
  actor_label     text NOT NULL,
  action          text NOT NULL,
  target_type     text NOT NULL,
  target_id       text,
  -- The tenant the action concerned, when it concerned one. Nullable, which is
  -- the entire reason this table exists separately: an operator signing in
  -- concerns no organization, and audit_entries.org_id is NOT NULL.
  --
  -- SET NULL rather than CASCADE. An operator deleting an organization is
  -- precisely the record that must survive the organization.
  subject_org_id  uuid REFERENCES organizations(id) ON DELETE SET NULL,
  -- Kept as text as well, for the same reason as actor_label.
  subject_org_label text,
  origin          text NOT NULL,
  ip              inet,
  detail          jsonb NOT NULL DEFAULT '{}'::jsonb,
  occurred_at     timestamptz NOT NULL DEFAULT now(),
  prev_hash       text,
  entry_hash      text NOT NULL
);
CREATE INDEX admin_audit_seq_idx ON admin_audit_entries (seq DESC);
CREATE INDEX admin_audit_org_idx ON admin_audit_entries (subject_org_id, seq DESC)
  WHERE subject_org_id IS NOT NULL;
CREATE INDEX admin_audit_actor_idx ON admin_audit_entries (admin_user_id, seq DESC);

-- ---------------------------------------------------------------------------
-- Grants
--
-- The same shape as 0002: the application gets exactly the verbs it needs, and
-- the audit table gets INSERT and SELECT and nothing else, which is what makes
-- append-only a property of the database rather than a promise about the code.
-- ---------------------------------------------------------------------------

GRANT SELECT, INSERT, UPDATE, DELETE ON admin_users, admin_sessions TO antifailure_app;
GRANT SELECT, INSERT ON admin_audit_entries TO antifailure_app;
GRANT USAGE, SELECT ON SEQUENCE admin_audit_entries_seq_seq TO antifailure_app;

-- Explicitly withheld, so that a later blanket grant has to overwrite a
-- statement that says why it should not.
REVOKE UPDATE, DELETE, TRUNCATE ON admin_audit_entries FROM antifailure_app;

-- ---------------------------------------------------------------------------
-- Policies on the operator's own tables
-- ---------------------------------------------------------------------------

-- ENABLE on all three. FORCE on the audit chain only, and NOT on the two
-- tables current_admin_user() reads.
--
-- 0002 turns FORCE on everywhere with the reasoning that the application does
-- not connect as the owner, so it costs nothing and closes a hole if anybody
-- ever does. That reasoning still holds and it is why the third table has it.
--
-- The first two cannot have it, and the reason is the definer function above:
-- FORCE applies the policies to the owner, the function runs as the owner
-- precisely so the policies do NOT apply to it, and turning FORCE on here puts
-- the recursion straight back. The cost is the one 0002 names, an owner
-- connection left open by a migration tool, and it is bounded to two tables
-- holding operator rows rather than to customer data.
ALTER TABLE admin_users ENABLE ROW LEVEL SECURITY;
ALTER TABLE admin_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE admin_audit_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE admin_audit_entries FORCE ROW LEVEL SECURITY;

-- Resolving the cookie. Keyed on the declared hash and nothing else, because
-- this is the statement that runs BEFORE anybody knows whose session it is,
-- and because current_admin_user() reads this table and a policy that called
-- it would recurse.
--
-- Holding the cookie is the proof. A connection that sets the setting to a
-- value it guessed gets nothing back.
CREATE POLICY resolve_by_token ON admin_sessions
  FOR SELECT TO antifailure_app
  USING (token_hash = current_admin_session_hash());

-- Signing in writes the session for the operator the password check just
-- named, on a connection that has no admin session yet by definition.
CREATE POLICY signin_creates_session ON admin_sessions
  FOR INSERT TO antifailure_app
  WITH CHECK (token_hash = current_admin_session_hash());

-- An operator may see and end their own sessions, and an operator holding a
-- live session may see and end anybody's, which is what "sign this operator
-- out" on the portal is.
CREATE POLICY admin_reads_sessions ON admin_sessions
  FOR SELECT TO antifailure_app
  USING (current_admin_user() IS NOT NULL);
CREATE POLICY admin_ends_sessions ON admin_sessions
  FOR DELETE TO antifailure_app
  USING (current_admin_user() IS NOT NULL);
CREATE POLICY admin_touches_own_session ON admin_sessions
  FOR UPDATE TO antifailure_app
  USING (admin_user_id = current_admin_user())
  WITH CHECK (admin_user_id = current_admin_user());

-- The sign-in path has to find an operator by email before any session exists,
-- and that is the one read here that cannot be gated on holding a session.
--
-- It is gated on declaring the email instead, the same way the SSO and device
-- lookups in 0012 and 0014 are gated on the value the caller is asking about.
-- The row it returns carries a scrypt hash and a salt, which are not
-- credentials: verifying a password requires the password. What this refuses
-- is ENUMERATION, so a connection cannot list the operators without holding a
-- session.
CREATE POLICY signin_finds_operator ON admin_users
  FOR SELECT TO antifailure_app
  USING (email = nullif(current_setting('antifailure.admin_email', true), ''));

CREATE POLICY admin_reads_operators ON admin_users
  FOR SELECT TO antifailure_app
  USING (current_admin_user() IS NOT NULL);
CREATE POLICY admin_writes_operators ON admin_users
  FOR INSERT TO antifailure_app
  WITH CHECK (current_admin_user() IS NOT NULL);
CREATE POLICY admin_updates_operators ON admin_users
  FOR UPDATE TO antifailure_app
  USING (current_admin_user() IS NOT NULL)
  WITH CHECK (current_admin_user() IS NOT NULL);
CREATE POLICY admin_deletes_operators ON admin_users
  FOR DELETE TO antifailure_app
  USING (current_admin_user() IS NOT NULL);

-- The chain is readable and appendable by an operator, and by nothing else. A
-- tenant connection cannot read it: what an operator did is not a tenant's
-- record, and the tenant-visible half of an operator's action is written into
-- that tenant's own audit_entries instead.
CREATE POLICY admin_reads_audit ON admin_audit_entries
  FOR SELECT TO antifailure_app
  USING (current_admin_user() IS NOT NULL);
-- Appending is permitted to a live operator, and ALSO to the sign-in scope,
-- which is the one that declares an email and holds no session.
--
-- That second half is not a loosening for convenience. A failed sign-in is the
-- single most valuable line in an operator audit log and it happens, by
-- definition, on a connection that never got a session. A policy requiring a
-- live operator would record every successful sign-in and silently drop every
-- failed one, which is precisely backwards.
--
-- What it permits is appending to an append-only table from the one route that
-- enters this scope, which is rate limited like the rest of the sign-in path.
-- It grants no read: admin_reads_audit still requires a live operator.
CREATE POLICY admin_appends_audit ON admin_audit_entries
  FOR INSERT TO antifailure_app
  WITH CHECK (
    current_admin_user() IS NOT NULL
    OR nullif(current_setting('antifailure.admin_email', true), '') IS NOT NULL
  );

-- ---------------------------------------------------------------------------
-- The cross tenant reads
--
-- One policy per table, SELECT only, each true only for a connection holding a
-- live operator session. These are the widening, and they are the whole of it:
-- there is no policy in this file that permits a cross tenant read to anybody
-- who is not physically holding an operator's cookie.
--
-- SELECT only, and deliberately not FOR ALL. An operator reads the platform;
-- the two tables an operator may WRITE across tenants are named separately
-- below, so that adding a third is an edit to this file rather than something
-- a FOR ALL already permitted.
-- ---------------------------------------------------------------------------

DO $$
DECLARE
  t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'organizations', 'users', 'members', 'sessions', 'repositories',
    'github_installations', 'environments', 'runs', 'verdicts',
    'engine_tokens', 'audit_entries', 'masking_rules', 'network_rules'
  ]
  LOOP
    EXECUTE format($p$
      CREATE POLICY admin_reads ON %I
        FOR SELECT TO antifailure_app
        USING (current_admin_user() IS NOT NULL)
    $p$, t);
  END LOOP;
END
$$;

-- ---------------------------------------------------------------------------
-- The cross tenant writes
--
-- Two, and each one is a lever that already exists in the product and is
-- already enforced somewhere a test can observe.
-- ---------------------------------------------------------------------------

-- Ending a customer's session. DELETE rather than a revoked_at flag, because
-- that is what the product's own sign-out does and session.ts says why: "a
-- revoked row that some later query forgets to filter is a working session".
CREATE POLICY admin_ends_customer_sessions ON sessions
  FOR DELETE TO antifailure_app
  USING (current_admin_user() IS NOT NULL);

-- Suspending an organization, resuming it, and changing its plan.
--
-- RLS is row level and cannot restrict a column, so this policy on its own
-- would let an operator rewrite an organization's slug, which is what a
-- license is issued against. A GRANT can restrict a column and cannot help
-- here either, because the operator path and the tenant path are the SAME
-- database role and the tenant path legitimately writes those columns.
--
-- So the column restriction is a trigger, which makes it a property of the
-- database rather than a promise about which columns the application's SQL
-- happens to name.
CREATE POLICY admin_updates_organizations ON organizations
  FOR UPDATE TO antifailure_app
  USING (current_admin_user() IS NOT NULL)
  WITH CHECK (current_admin_user() IS NOT NULL);

CREATE OR REPLACE FUNCTION admin_updates_only_operational_columns() RETURNS trigger
  LANGUAGE plpgsql AS $$
BEGIN
  -- Only constrains the operator path. A tenant's own update runs with
  -- current_org() set and no admin session, and is untouched by this.
  IF current_admin_user() IS NULL THEN
    RETURN NEW;
  END IF;

  IF NEW.id IS DISTINCT FROM OLD.id
     OR NEW.slug IS DISTINCT FROM OLD.slug
     OR NEW.name IS DISTINCT FROM OLD.name
     OR NEW.github_login IS DISTINCT FROM OLD.github_login
     OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'an operator may change an organization''s plan and suspension, not its identity'
      USING ERRCODE = 'raise_exception';
  END IF;

  RETURN NEW;
END
$$;

CREATE TRIGGER admin_updates_only_operational_columns_trg
  BEFORE UPDATE ON organizations
  FOR EACH ROW EXECUTE FUNCTION admin_updates_only_operational_columns();

COMMIT;
