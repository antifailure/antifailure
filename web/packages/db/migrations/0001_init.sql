-- The control plane's schema, and the isolation that makes it multi-tenant.
--
-- Two things in this file are load bearing and everything else is ordinary.
--
-- The first is that tenancy is enforced by the database, not by the
-- application. Every tenant-scoped table has row-level security keyed on a
-- session setting, and the role the application connects as is neither the
-- owner of these tables nor a superuser, so it cannot turn the policies off
-- and Postgres will not skip them on its behalf. Application-level scoping is
-- still written on every query, but it is the second line: the kind of bug
-- that leaks one customer's environments into another customer's list is a
-- forgotten WHERE clause, and a forgotten WHERE clause here returns nothing
-- rather than everything.
--
-- The second is that the audit log cannot be rewritten by the thing being
-- audited. The application role is granted INSERT and SELECT on it and nothing
-- else, so an UPDATE is refused by the database rather than by a code path
-- somebody can forget to call. Each entry also carries the hash of the one
-- before it, so removing an entry with a privileged connection still leaves a
-- gap that verification finds.
--
-- Migrations are transactional. If any statement here fails, none of it
-- applies and the deploy halts with the database exactly as it was.

BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------------------
-- Roles
-- ---------------------------------------------------------------------------

-- The role the application connects as. It owns nothing, so row-level security
-- applies to it without needing FORCE, and it cannot ALTER TABLE to disable a
-- policy. Created here rather than by hand so that a self-hosted installation
-- gets the same isolation the hosted one has.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'antifailure_app') THEN
    CREATE ROLE antifailure_app NOLOGIN NOBYPASSRLS;
  END IF;
END
$$;

-- ---------------------------------------------------------------------------
-- Tenancy helpers
--
-- current_org() reads the session setting the application sets at the start of
-- every transaction. The second argument to current_setting makes a missing
-- setting return NULL instead of raising, and NULL is what makes the policies
-- deny by default: org_id = NULL is NULL, which is not true, so no row is
-- visible and no row can be written. A connection that forgets to identify its
-- tenant sees an empty database, which is the failure everyone notices
-- immediately rather than the one nobody notices at all.
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION current_org() RETURNS uuid
  LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('antifailure.org_id', true), '')::uuid $$;

CREATE OR REPLACE FUNCTION current_actor() RETURNS uuid
  LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('antifailure.user_id', true), '')::uuid $$;

-- The hash of the session token the caller is presenting. Set only on the two
-- statements that resolve or create a session, and used by the policy on that
-- table so that holding the cookie is what makes the row visible.
CREATE OR REPLACE FUNCTION current_session_hash() RETURNS bytea
  LANGUAGE sql STABLE
  AS $$ SELECT decode(nullif(current_setting('antifailure.session_hash', true), ''), 'hex') $$;

-- ---------------------------------------------------------------------------
-- Identity
-- ---------------------------------------------------------------------------

CREATE TABLE users (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  github_id       bigint NOT NULL UNIQUE,
  github_login    text NOT NULL,
  -- Stored lowercased. Identity providers disagree about case and a provider
  -- that sends Ada@Example.com must not create a second account.
  email           text NOT NULL,
  name            text,
  avatar_url      text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT users_email_lowercase CHECK (email = lower(email))
);
CREATE INDEX users_email_idx ON users (email);

CREATE TABLE organizations (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  -- The slug is what a license is issued against and what appears in URLs.
  slug            text NOT NULL UNIQUE,
  name            text NOT NULL,
  github_login    text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT organizations_slug_shape CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$')
);

CREATE TYPE member_role AS ENUM ('owner', 'admin', 'member', 'viewer');

CREATE TABLE members (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role            member_role NOT NULL DEFAULT 'member',
  -- Where the membership came from, so that a member synced from GitHub is
  -- not silently replaced by a manual one and back again on the next sync.
  source          text NOT NULL DEFAULT 'github',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, user_id)
);
CREATE INDEX members_user_idx ON members (user_id);

CREATE TABLE sessions (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  -- Only the hash is stored. A database backup that leaks must not be a
  -- pile of working session cookies.
  token_hash      bytea NOT NULL UNIQUE,
  user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  -- The organization this session is scoped to. A user in several
  -- organizations switches by rotating the session, so a stolen cookie can
  -- never be replayed against an organization it was not issued for.
  org_id          uuid REFERENCES organizations(id) ON DELETE CASCADE,
  csrf_secret     bytea NOT NULL,
  user_agent      text,
  ip              inet,
  created_at      timestamptz NOT NULL DEFAULT now(),
  last_seen_at    timestamptz NOT NULL DEFAULT now(),
  expires_at      timestamptz NOT NULL,
  revoked_at      timestamptz
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expiry_idx ON sessions (expires_at);

-- OAuth handshakes in flight. Rows are short lived and deleted on use, so a
-- replayed callback finds nothing and is rejected.
CREATE TABLE oauth_states (
  state           text PRIMARY KEY,
  redirect_to     text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  expires_at      timestamptz NOT NULL
);

CREATE TABLE github_installations (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  installation_id bigint NOT NULL UNIQUE,
  account_login   text NOT NULL,
  account_type    text NOT NULL,
  suspended_at    timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX github_installations_org_idx ON github_installations (org_id);

-- ---------------------------------------------------------------------------
-- Repositories and environments
-- ---------------------------------------------------------------------------

CREATE TABLE repositories (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  -- owner/name as GitHub spells it.
  full_name       text NOT NULL,
  default_branch  text NOT NULL DEFAULT 'main',
  github_id       bigint,
  private         boolean NOT NULL DEFAULT true,
  archived_at     timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, full_name)
);

CREATE TYPE environment_state AS ENUM (
  'queued', 'creating', 'running', 'sleeping', 'failed', 'torn_down'
);

CREATE TABLE environments (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  repository_id   uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
  -- The engine's own identifier for the environment, which is what appears on
  -- every container label and in every event.
  env_id          text NOT NULL,
  branch          text NOT NULL,
  pull_request    integer,
  state           environment_state NOT NULL DEFAULT 'queued',
  preview_url     text,
  runtime         text,
  golden_version  text,
  created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
  -- The highest event sequence applied to this row. Events arrive out of
  -- order often enough that the last one to land is routinely not the newest,
  -- and a status that flips back to "creating" after "running" is a status
  -- nobody believes again.
  last_sequence   bigint NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  expires_at      timestamptz,
  torn_down_at    timestamptz,
  UNIQUE (org_id, env_id)
);
CREATE INDEX environments_repo_idx ON environments (repository_id, created_at DESC);
CREATE INDEX environments_state_idx ON environments (org_id, state);

CREATE TABLE golden_versions (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  repository_id   uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
  version         text NOT NULL,
  source_digest   text,
  rules_digest    text,
  verified        boolean NOT NULL DEFAULT false,
  -- The signed attestation the engine produced. Metadata about the scan, not
  -- any of the data it scanned.
  attestation     jsonb,
  size_bytes      bigint,
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, repository_id, version)
);

-- ---------------------------------------------------------------------------
-- Runs, verdicts, artifacts
-- ---------------------------------------------------------------------------

CREATE TYPE run_state AS ENUM ('queued', 'running', 'complete', 'failed', 'cancelled');

CREATE TABLE runs (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  environment_id  uuid NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  kind            text NOT NULL,
  state           run_state NOT NULL DEFAULT 'queued',
  started_at      timestamptz,
  finished_at     timestamptz,
  last_sequence   bigint NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX runs_env_idx ON runs (environment_id, created_at DESC);

-- The five verdicts the runner can return. Written out rather than free text
-- so that a sixth cannot appear in the control plane without appearing here.
CREATE TYPE verdict_value AS ENUM ('pass', 'fail', 'flaky', 'blocked', 'unverified');

CREATE TABLE verdicts (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  run_id          uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  workflow        text NOT NULL,
  persona         text,
  value           verdict_value NOT NULL,
  summary         text,
  steps           integer NOT NULL DEFAULT 0,
  duration_ms     integer,
  reproduction    jsonb,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX verdicts_run_idx ON verdicts (run_id);

-- An index of artifacts, not the artifacts. The control plane holds a pointer
-- and a checksum; the bytes live in object storage and are fetched with a
-- signed URL. Screenshots and videos of a preview environment are the most
-- sensitive thing this system touches, and the fewer places they exist the
-- smaller that problem is.
CREATE TABLE artifacts (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  run_id          uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  kind            text NOT NULL,
  step            integer,
  storage_key     text NOT NULL,
  content_type    text,
  size_bytes      bigint,
  sha256          text,
  -- Set when retention removed the bytes. The row stays so that the timeline
  -- can say "not retained" instead of rendering a gap that looks like a bug.
  retained        boolean NOT NULL DEFAULT true,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX artifacts_run_idx ON artifacts (run_id, step);

-- ---------------------------------------------------------------------------
-- Policy
-- ---------------------------------------------------------------------------

-- Masking rules as metadata only: which column, which transform, which rule
-- decided. Never a value, never a sample. The control plane is not a place
-- where production data can leak because production data never arrives.
CREATE TABLE masking_rules (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  repository_id   uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
  table_name      text NOT NULL,
  column_name     text NOT NULL,
  transform       text NOT NULL,
  link            text,
  reason          text,
  -- Suggested by the classifier and awaiting a human, versus in effect.
  confirmed       boolean NOT NULL DEFAULT false,
  source_digest   text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, repository_id, table_name, column_name)
);

CREATE TABLE network_rules (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  -- NULL means the rule applies to every repository in the organization.
  repository_id   uuid REFERENCES repositories(id) ON DELETE CASCADE,
  host            text NOT NULL,
  mode            text NOT NULL,
  paths           text[],
  methods         text[],
  rate_limit      text,
  credential      text,
  fixtures        text,
  webhook_path    text,
  note            text,
  position        integer NOT NULL DEFAULT 0,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX network_rules_scope_idx ON network_rules (org_id, repository_id, position);

-- ---------------------------------------------------------------------------
-- Events
-- ---------------------------------------------------------------------------

CREATE TABLE engine_tokens (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name            text NOT NULL,
  -- SHA-256 of the token. The token is 256 bits of randomness, so a fast hash
  -- is the right one: there is no dictionary to attack and a slow hash would
  -- only make the ingestion path slow.
  token_hash      bytea NOT NULL UNIQUE,
  -- The first characters, shown in the UI so an operator can tell two tokens
  -- apart without holding either.
  prefix          text NOT NULL,
  created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  last_used_at    timestamptz,
  revoked_at      timestamptz
);
CREATE INDEX engine_tokens_org_idx ON engine_tokens (org_id);

CREATE TABLE events (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  -- The sender's identifier for the event. Retries after a timeout are the
  -- normal case, not the exceptional one, so the second copy is dropped by
  -- the unique constraint rather than by anything the sender has to get right.
  idempotency_key text NOT NULL,
  env_id          text,
  environment_id  uuid REFERENCES environments(id) ON DELETE CASCADE,
  run_id          uuid REFERENCES runs(id) ON DELETE CASCADE,
  -- Assigned by the sender, monotonic within an environment. Ordering is by
  -- this and never by arrival, because arrival order is a property of the
  -- network.
  sequence        bigint NOT NULL DEFAULT 0,
  type            text NOT NULL,
  payload         jsonb NOT NULL DEFAULT '{}'::jsonb,
  occurred_at     timestamptz NOT NULL,
  received_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, idempotency_key)
);
CREATE INDEX events_env_sequence_idx ON events (org_id, env_id, sequence);
CREATE INDEX events_received_idx ON events (org_id, received_at DESC);

-- ---------------------------------------------------------------------------
-- Audit
-- ---------------------------------------------------------------------------

CREATE TABLE audit_entries (
  -- A sequence rather than a uuid, because the chain is an order and the
  -- order has to be assigned by the database. Two replicas writing at once
  -- must not both believe they are entry number nine.
  seq             bigserial PRIMARY KEY,
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  actor_user_id   uuid REFERENCES users(id) ON DELETE SET NULL,
  -- Kept as text as well, so that deleting a user does not erase who did what.
  actor_label     text NOT NULL,
  action          text NOT NULL,
  target_type     text NOT NULL,
  target_id       text,
  origin          text NOT NULL,
  detail          jsonb NOT NULL DEFAULT '{}'::jsonb,
  occurred_at     timestamptz NOT NULL DEFAULT now(),
  -- hex sha256 of the previous entry's hash concatenated with this entry's
  -- canonical form. Computed by the application, verified by anyone.
  prev_hash       text,
  entry_hash      text NOT NULL
);
CREATE INDEX audit_org_idx ON audit_entries (org_id, seq DESC);

COMMIT;
