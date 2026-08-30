-- Where an organization's environments are allowed to run.
--
-- `environments.runtime` has always been a free text column the engine fills
-- in with whatever it happened to be running on, which answers "where did this
-- one run" and cannot answer "where may the next one run". `runtimes.manage`
-- was declared for the second question and there was no table for it to act
-- on, so the permission guarded nothing.
--
-- What this is NOT: a connection. The control plane holds no kubeconfig, no
-- cluster credential and no address, and it never reaches a runtime. It holds
-- the NAME, so that a person creating an environment picks from a list the
-- organization agreed on rather than typing a string, and so that the name
-- travels to the customer's own CI as a workflow input. The machinery stays
-- entirely on the customer's side, which is the same boundary that keeps their
-- data out of here.
--
-- Removal is a timestamp rather than a DELETE. An environment that ran on a
-- runtime records its name, and deleting the row would leave those
-- environments pointing at something the console cannot explain. A removed
-- runtime stops being offered and stays readable.

BEGIN;

CREATE TABLE runtimes (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name          text NOT NULL,
  -- The manifest's own vocabulary, `local` or `kubernetes`, checked in the
  -- application rather than as an enum: adding a provider should be a release
  -- of the engine and the control plane together, not a migration on every
  -- customer's database.
  provider      text NOT NULL,
  -- What this runtime is for, in the organization's words: `eu`, `gpu`,
  -- `staging`. Read by a person choosing one, never by policy.
  labels        text[] NOT NULL DEFAULT '{}',
  note          text,
  registered_by uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  removed_at    timestamptz
);

-- One live runtime per name, and a removed one does not hold its name hostage.
-- A partial index rather than a plain unique constraint for exactly that: an
-- organization that removes `staging` and stands up a new one has to be able
-- to call it `staging` again.
CREATE UNIQUE INDEX runtimes_org_name_key ON runtimes (org_id, name)
  WHERE removed_at IS NULL;

CREATE INDEX runtimes_org_idx ON runtimes (org_id, created_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON runtimes TO antifailure_app;

-- The same policy shape as every other tenant-scoped table, and it has to be
-- here rather than inherited from anywhere: a table added without one is
-- readable by every tenant and nothing in the application would say so. The
-- cross-tenant suite reads the list of tables out of the database, so this
-- table is attacked from a second tenant the moment it exists.
ALTER TABLE runtimes ENABLE ROW LEVEL SECURITY;
ALTER TABLE runtimes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON runtimes
  FOR ALL TO antifailure_app
  USING (org_id = current_org())
  WITH CHECK (org_id = current_org());

COMMIT;
