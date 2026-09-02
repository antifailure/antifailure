-- Which organization a GitHub repository is allowed to send events as.
--
-- WHY THIS TABLE EXISTS, AND IT IS THE WHOLE POINT OF THE FILE.
--
-- A GitHub Actions workflow identity token is signed by GitHub and carries a
-- `repository` claim. That claim is true. It is also worthless as an
-- authorization: anybody with a GitHub account can create a repository, add a
-- workflow with `id-token: write`, and mint a correctly signed token whose
-- `repository` claim says `them/their-repo`. A verifier that reads the claim
-- and looks up "the organization for that repository owner" has authenticated
-- a stranger perfectly and then authorized them anyway.
--
-- So the claim answers "who is this", and this table answers "may they". The
-- exchange endpoint succeeds only when a row here already says that this exact
-- repository belongs to this organization, and a repository nobody has claimed
-- is refused rather than guessed at.
--
-- WHY NOT `repositories`. That table looks like the answer and is a trap. It
-- is populated by ingestion: src/ingest.ts inserts a row for whatever
-- `full_name` an engine reports, so any organization can cause a
-- `repositories` row for `someone-else/their-app` to exist simply by sending
-- one event naming it. Using it here would invert into the exact attack above:
-- an attacker sends one event claiming a victim's repository, and the victim's
-- genuine CI tokens then resolve to the attacker's tenant. It also carries only
-- UNIQUE (org_id, full_name), so the same repository may legitimately appear
-- under two organizations and there would be no basis for choosing one.
--
-- THREE THINGS MAKE A ROW HERE MEAN SOMETHING.
--
--   1. At most one live binding per repository, across the whole installation.
--      That is the partial unique index below, and it is what makes the lookup
--      unambiguous rather than a choice between candidates.
--
--   2. It is created by a member of the organization who may already mint an
--      engine token, because that is exactly the privilege this grants
--      automatically to a workflow. Enforced in the application, on the same
--      role check as POST /v1/tokens.
--
--   3. The organization has to hold a live GitHub App installation on the
--      repository's owner. Those rows are written only by webhook deliveries
--      whose HMAC has been checked, so they are GitHub saying the organization
--      controls that account, rather than a person typing a name into a form.
--      Enforced twice: in the application, where it can produce a sentence
--      somebody can act on, and in the policy below, where a bug in the
--      application cannot get past it.
--
-- Revocation is a timestamp rather than a DELETE. Who was allowed to report as
-- a repository, and when that stopped, is the question asked after an incident,
-- and a deleted row cannot answer it. The partial index ignores revoked rows,
-- so a repository can be claimed again afterwards.

BEGIN;

CREATE TABLE oidc_repository_bindings (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  -- owner/name, lower-cased. GitHub treats both halves case insensitively, so
  -- storing what somebody typed would let `Acme/App` and `acme/app` be two
  -- different bindings and the unique index below would permit both.
  repository    text NOT NULL,
  created_by    uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  -- When a token was last minted against it. The number somebody looks at
  -- before revoking a binding they no longer recognise.
  last_used_at  timestamptz,
  revoked_at    timestamptz,
  CONSTRAINT oidc_repository_bindings_shape
    CHECK (repository ~ '^[a-z0-9._-]+/[a-z0-9._-]+$')
);

-- The constraint the security of the exchange rests on. Two organizations
-- cannot both hold a live claim on one repository, so resolving a verified
-- `repository` claim either finds exactly one organization or none.
CREATE UNIQUE INDEX oidc_repository_bindings_live
  ON oidc_repository_bindings (repository) WHERE revoked_at IS NULL;

CREATE INDEX oidc_repository_bindings_org_idx ON oidc_repository_bindings (org_id);

-- No DELETE. A binding is revoked, never removed: the row is the record of who
-- was allowed to report as this repository and when that ended. Withheld
-- explicitly so that a later blanket grant has to overwrite a line saying why
-- it should not.
GRANT SELECT, INSERT, UPDATE ON oidc_repository_bindings TO antifailure_app;
REVOKE DELETE, TRUNCATE ON oidc_repository_bindings FROM antifailure_app;

ALTER TABLE oidc_repository_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE oidc_repository_bindings FORCE ROW LEVEL SECURITY;

-- Managing a binding from the console or the terminal: the ordinary tenant
-- policy, identical in shape to every other table in 0002.
CREATE POLICY tenant_isolation ON oidc_repository_bindings
  FOR ALL TO antifailure_app
  USING (org_id = current_org())
  WITH CHECK (org_id = current_org());

-- Reading a binding during the exchange, which has no tenant: a CI job holds
-- no session and the organization is precisely what is being worked out.
--
-- Keyed on the account out of the VERIFIED identity token, the same shape as
-- the delivery policies in 0013, and reached through github_installations for
-- the same reason the repository policy there is: a connection scoped to
-- `attacker` can see bindings belonging to organizations that hold an
-- installation on `attacker` and no others. So even if the application handed
-- this the wrong repository name, it could not read a binding out of an
-- organization the account has no installation for.
--
-- Two policies rather than one FOR ALL, and the difference is the point. The
-- exchange reads a claim and stamps it; it must not be able to CREATE one. A
-- FOR ALL would carry an INSERT path that nothing uses and that, if anything
-- ever did, would let a connection holding only a repository owner's name mint
-- the very permission this table exists to require. Claiming happens under a
-- tenant, by a person, above.
CREATE POLICY github_account_reads_binding ON oidc_repository_bindings
  FOR SELECT TO antifailure_app
  USING (org_id IN (
    SELECT org_id FROM github_installations
    WHERE lower(account_login) = current_github_account()));

-- The stamp a successful exchange leaves, with the same scope as the read that
-- authorized it. WITH CHECK as well as USING: without it, an UPDATE could move
-- a row it can see into an organization it cannot.
CREATE POLICY github_account_stamps_binding ON oidc_repository_bindings
  FOR UPDATE TO antifailure_app
  USING (org_id IN (
    SELECT org_id FROM github_installations
    WHERE lower(account_login) = current_github_account()))
  WITH CHECK (org_id IN (
    SELECT org_id FROM github_installations
    WHERE lower(account_login) = current_github_account()));

-- ---------------------------------------------------------------------------
-- The tokens the exchange mints
-- ---------------------------------------------------------------------------
--
-- A third kind in engine_tokens rather than a fourth token table, for the
-- reason 0012 gives for the second: the hashing, the prefix, the revocation and
-- the policy that lets a bearer token find its own organization are already
-- here and already tested, and a parallel implementation of exactly those is
-- where a subtle difference becomes a security bug.
--
-- It is a distinct kind rather than a plain 'engine' token because these are
-- minted per workflow job and would otherwise bury the two or three real engine
-- tokens an organization has in `af token list`. `authenticateEngine` does not
-- filter on kind, so an 'oidc' token authenticates on POST /v1/events exactly
-- as a static one does, which is the whole point of the exchange.
ALTER TABLE engine_tokens DROP CONSTRAINT engine_tokens_kind;
ALTER TABLE engine_tokens
  ADD CONSTRAINT engine_tokens_kind CHECK (kind IN ('engine', 'cli', 'oidc'));

ALTER TABLE engine_tokens
  ADD COLUMN binding_id uuid REFERENCES oidc_repository_bindings(id) ON DELETE CASCADE;

-- Short-lived as a property of the schema rather than as a promise about the
-- code. A future caller that forgets to pass an expiry cannot mint an immortal
-- credential from a workflow identity: the INSERT fails.
ALTER TABLE engine_tokens
  ADD CONSTRAINT engine_tokens_oidc_expires CHECK (kind <> 'oidc' OR expires_at IS NOT NULL);

-- And it has to say which binding earned it, so revoking the binding can reach
-- the credentials it produced. Without this a revoked binding would stop
-- issuing new tokens while the ones already issued kept working, which is the
-- shape of a revocation that does not revoke.
ALTER TABLE engine_tokens
  ADD CONSTRAINT engine_tokens_oidc_has_a_binding CHECK (kind <> 'oidc' OR binding_id IS NOT NULL);

CREATE INDEX engine_tokens_binding_idx ON engine_tokens (binding_id) WHERE binding_id IS NOT NULL;

COMMIT;
