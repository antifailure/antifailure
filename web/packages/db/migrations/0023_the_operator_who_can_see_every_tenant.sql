-- The operator who can see every tenant, and the record that makes that
-- accountable rather than invisible.
--
-- Everything else in this schema is built so that one tenant cannot reach
-- another's rows. This file deliberately opens a hole in that, because a
-- product with paying customers needs somebody who can answer "why did this
-- account's run fail" without asking the customer to paste their data into a
-- support ticket. Pretending otherwise does not remove the hole; it moves it
-- into a shared password and a psql prompt, where nothing is recorded.
--
-- So the hole is made explicit, given its own role, and made to leave a trace.
-- Three decisions carry that:
--
-- The role is separate. `antifailure_admin` is not a privilege that
-- `antifailure_app` can be granted; it is a different credential with its own
-- connection. A request that arrives from a browser cannot escalate into it,
-- because escalating would mean opening a second connection with a password
-- the application process is not given. That is a stronger boundary than any
-- check in TypeScript, and it is the reason this is a role rather than a flag.
--
-- The tables here are NOT tenant tables. An operator's note about a customer
-- is not that customer's data and must not appear in their export, so
-- `antifailure_app` gets no grant on it at all. Row-level security is enabled
-- on top of that anyway: if somebody later writes a blanket GRANT, the policy
-- is still there to refuse, and they have to delete a statement that says why
-- it should not be done rather than merely forget to add one.
--
-- Impersonation records live on the session row rather than in a side table,
-- and that is load bearing. `resolveSession` reads the session on every single
-- request, and the one thing it must never do is fail open: a session that IS
-- an impersonation but whose marker lives in a table the application cannot
-- read is a session that looks ordinary to every check in the product. The
-- marker travels with the thing it describes.

BEGIN;

-- ---------------------------------------------------------------------------
-- The role
--
-- Created NOLOGIN and without a password, the same way antifailure_app is, so
-- that a self-hosted installation supplies its own credential rather than
-- inheriting one written down in a migration that is in the public repository.
-- An installation that never gives it a password has no admin portal, which is
-- the correct default for somebody running this for one team.
--
-- BYPASSRLS is the whole point. Every table in this schema carries FORCE ROW
-- LEVEL SECURITY, which means even the table owner is subject to its policies,
-- so ownership is not a way around them and neither is `row_security = off`
-- for a role the policies apply to. BYPASSRLS is the only mechanism that
-- actually lets a query see two tenants at once, and it is a role attribute
-- rather than a grant, which is what keeps it out of the application's reach.
--
-- IF NOT EXISTS so that this is idempotent against an installation where the
-- role was created by hand or by another migration.
-- ---------------------------------------------------------------------------

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'antifailure_admin') THEN
    CREATE ROLE antifailure_admin NOLOGIN BYPASSRLS;
  ELSE
    -- The attribute, not the role, is what this migration is really asserting.
    -- A role that exists without BYPASSRLS would read exactly zero rows and
    -- every admin page would render an empty state that looks like a product
    -- with no customers rather than like a misconfiguration.
    ALTER ROLE antifailure_admin BYPASSRLS;
  END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO antifailure_admin;

-- Read everything, because that is what the portal is for. Written as a
-- blanket grant over the schema deliberately: a per-table list here would go
-- stale the first time somebody adds a table, and an operator investigating an
-- incident against a table this grant forgot would get a permission error in
-- the middle of the incident.
GRANT SELECT ON ALL TABLES IN SCHEMA public TO antifailure_admin;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO antifailure_admin;

-- Writing is narrow and enumerated, and the difference from the line above is
-- the point. Reading every tenant is what support work needs; changing every
-- tenant's rows is not, and an operator credential that can rewrite anything
-- is a credential whose compromise is indistinguishable from a database
-- compromise. What is granted here is exactly the set of actions the portal
-- offers as buttons.
GRANT INSERT, UPDATE ON users TO antifailure_admin;
GRANT UPDATE ON organizations TO antifailure_admin;
GRANT INSERT, UPDATE, DELETE ON members TO antifailure_admin;
GRANT INSERT, UPDATE ON sessions TO antifailure_admin;

-- The audit log takes INSERT and SELECT and never UPDATE or DELETE, the same
-- shape the application has. An operator who could rewrite the record of what
-- operators did would make this entire file decorative.
GRANT SELECT, INSERT ON audit_entries TO antifailure_admin;
GRANT USAGE, SELECT ON SEQUENCE audit_entries_seq_seq TO antifailure_admin;

-- ---------------------------------------------------------------------------
-- Suspending a person
--
-- organizations has carried suspended_at, suspended_reason and suspended_by
-- since 0001. users has not, so the only way to stop one person signing in was
-- to remove them from every organization, which destroys the membership record
-- that an investigation later needs. These three columns are the same shape as
-- the organization's, on purpose: two different vocabularies for the same idea
-- is how a check ends up reading the wrong one.
--
-- suspended_by is text rather than a foreign key to users, for the same reason
-- actor_label is text in audit_entries: it has to still say who did it after
-- that operator's own account is gone.
-- ---------------------------------------------------------------------------

ALTER TABLE users ADD COLUMN IF NOT EXISTS suspended_at timestamptz;
ALTER TABLE users ADD COLUMN IF NOT EXISTS suspended_reason text;
ALTER TABLE users ADD COLUMN IF NOT EXISTS suspended_by text;

-- A verified address, so that "verify this person's email" is a real action
-- rather than a button that writes nothing. Nullable because every account
-- that already exists predates the column and claiming they are all verified
-- would be a lie told by a DEFAULT.
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at timestamptz;

-- ---------------------------------------------------------------------------
-- Impersonation
--
-- Four columns and one constraint, and the constraint is what makes the rules
-- true rather than merely intended.
--
-- impersonation_audit_seq is the sequence number of the audit entry that
-- authorised this session. It is NOT decoration and it is not a convenience
-- for a report. The requirement is that the audit record exists BEFORE the
-- session does, and the ordinary way to attempt that is to write the entry,
-- then write the session, and rely on nobody ever reordering those two
-- statements. This makes it structural instead: the session row cannot be
-- INSERTed without already holding the sequence number of an entry that has
-- been written, so a session that was never audited cannot be represented.
--
-- impersonator_label is stored rather than joined for the same reason
-- suspended_by is text: the banner has to name the operator a year later, and
-- an operator's own account may be closed by then.
--
-- The CHECK is all-or-nothing across all four. Without it, a partially
-- populated row is possible, and the shape that matters is the one where
-- impersonated_by is set and impersonation_reason is NULL, which is precisely
-- "an impersonation with no reason captured" -- one of the rules this is
-- supposed to enforce.
-- ---------------------------------------------------------------------------

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS impersonated_by uuid REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS impersonator_label text;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS impersonation_reason text;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS impersonation_audit_seq bigint;

ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_impersonation_is_complete;
ALTER TABLE sessions ADD CONSTRAINT sessions_impersonation_is_complete CHECK (
  (impersonated_by IS NULL
     AND impersonator_label IS NULL
     AND impersonation_reason IS NULL
     AND impersonation_audit_seq IS NULL)
  OR
  (impersonated_by IS NOT NULL
     AND impersonator_label IS NOT NULL
     AND impersonation_reason IS NOT NULL
     AND impersonation_audit_seq IS NOT NULL
     -- An empty reason is not a reason. Enforced here rather than in the
     -- handler's validation because the handler is one caller and this is
     -- every caller.
     AND length(btrim(impersonation_reason)) > 0)
);

-- Every live impersonation, for the page that lists them. Partial, because the
-- overwhelming majority of sessions are ordinary and indexing them all to find
-- the handful that are not is the wrong shape.
CREATE INDEX IF NOT EXISTS sessions_impersonated_idx
  ON sessions (impersonated_by, created_at DESC)
  WHERE impersonated_by IS NOT NULL;

-- ---------------------------------------------------------------------------
-- What an operator wrote down
--
-- Support notes. Global rather than tenant scoped, and with no grant to
-- antifailure_app, because these are the operator's words about a customer
-- rather than the customer's own data: they must not appear in that
-- organization's export, in its audit log, or anywhere the tenant can read.
--
-- subject_type and subject_id rather than three nullable foreign keys, because
-- a note attaches to a user, an organization or a repository and the set will
-- grow. The trade is a column that cannot be a foreign key, which is accepted
-- here: a note about an account that has since been deleted is a note an
-- investigation still wants, so a cascade would delete exactly the evidence
-- somebody came looking for.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS admin_notes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  subject_type text NOT NULL CHECK (subject_type IN ('user', 'organization', 'repository')),
  subject_id uuid NOT NULL,
  body text NOT NULL CHECK (length(btrim(body)) > 0),
  author_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  author_label text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  -- Soft deleted. A note somebody retracted is still a thing an operator wrote
  -- about a customer, and the retraction is itself worth being able to see.
  deleted_at timestamptz
);

CREATE INDEX IF NOT EXISTS admin_notes_subject_idx
  ON admin_notes (subject_type, subject_id, created_at DESC);

GRANT SELECT, INSERT, UPDATE ON admin_notes TO antifailure_admin;

-- Enabled with no permissive policy, and FORCEd so that ownership is not a way
-- around it. The application has no grant on this table, so today this changes
-- nothing. It is here for the day somebody writes a blanket
-- `GRANT ... ON ALL TABLES ... TO antifailure_app`: after that grant the table
-- is still shut, and opening it means deleting these two lines and the comment
-- above them rather than merely forgetting to add something.
ALTER TABLE admin_notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE admin_notes FORCE ROW LEVEL SECURITY;

COMMIT;
