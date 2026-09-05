-- The pull request that adds the workflow file, and the queue behind it.
--
-- Installing the App connected a repository and then nothing happened. The
-- installation row was written, the repository row was written, the console
-- listed the repository, and no check ever ran on any pull request, because a
-- check needs a workflow file in the repository and nothing had put one there.
-- The documentation sent people to copy a file of five hundred lines by hand.
-- Most of them did not, and a repository that was "connected" for a month with
-- zero runs was the ordinary outcome rather than the exception.
--
-- So the App opens a pull request that adds the file. The webhook handler that
-- records the installation cannot do that itself: a delivery is leased for two
-- minutes and GitHub's own timeout on it is ten seconds, and opening the pull
-- request is four calls to GitHub, any of which can be refused or slow. The
-- handler enqueues one row per repository here and a sweeper does the calls,
-- the same shape as teardown_requests in 0021 and for the same reason: work
-- that has to reach another system across a network is a queue with a lease,
-- an attempt count and a recorded outcome, or it is a hope.
--
-- ONE ROW PER REPOSITORY, EVER. The unique constraint on repository_id is the
-- idempotence: an installation delivery redelivered, an installation_repositories
-- delivery that names a repository the installation delivery already carried,
-- and a repository added, removed and added again all land on the same row.
-- The state says what happened to it, and `present` and `opened` are terminal
-- because a second pull request adding the same file is exactly the thing a
-- person would uninstall the App over.

BEGIN;

CREATE TABLE repository_setups (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id              uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  repository_id       uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
  -- queued: waiting for a sweeper. leased: a sweeper holds it. opened: the
  -- pull request exists and its number is below. present: the repository
  -- already had the file on its default branch, so nothing was opened.
  -- needs_permission: GitHub refused the write because the installation does
  -- not hold Contents write; last_error says what to grant, and the next
  -- new_permissions_accepted delivery puts the row back to queued. failed:
  -- five attempts, each recorded in last_error, and a person has to look.
  -- skipped: reserved for a repository the sweeper decided not to touch, such
  -- as an archived one; nothing writes it yet and the constraint admits it so
  -- that adding the writer is not a migration.
  state               text NOT NULL DEFAULT 'queued',
  attempts            integer NOT NULL DEFAULT 0,
  lease_holder        text,
  leased_until        timestamptz,
  -- The branch the file was committed to. Always antifailure/setup today, but
  -- recorded so that a later change to the name does not orphan open rows.
  branch              text,
  pull_request_number integer,
  pull_request_url    text,
  last_error          text,
  requested_at        timestamptz NOT NULL DEFAULT now(),
  finished_at         timestamptz,
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT repository_setups_state
    CHECK (state IN ('queued', 'leased', 'opened', 'present', 'needs_permission', 'failed', 'skipped')),
  CONSTRAINT repository_setups_repository_key UNIQUE (repository_id)
);

CREATE INDEX repository_setups_org_idx ON repository_setups (org_id, requested_at);
-- The sweeper's read: what is claimable now, the same shape as the teardown
-- queue's claimable index.
CREATE INDEX repository_setups_claimable_idx ON repository_setups (leased_until)
  WHERE state IN ('queued', 'leased');

-- ---------------------------------------------------------------------------
-- Grants. Nothing deletes a row: a repository whose setup was attempted keeps
-- the record of what happened to it, and the cascade from repositories is the
-- only way one goes away.
-- ---------------------------------------------------------------------------

GRANT SELECT, INSERT, UPDATE ON repository_setups TO antifailure_app;
REVOKE DELETE, TRUNCATE ON repository_setups FROM antifailure_app;

-- ---------------------------------------------------------------------------
-- Isolation
-- ---------------------------------------------------------------------------

ALTER TABLE repository_setups ENABLE ROW LEVEL SECURITY;
ALTER TABLE repository_setups FORCE ROW LEVEL SECURITY;

-- A tenant READS its own rows and writes none of them. Nothing a person can
-- reach changes the state of a setup: the console shows what the sweeper
-- found, and a tenant able to write `opened` with a made up number would be
-- a console lying about a pull request that does not exist. This is narrower
-- than teardown_requests, where the tenant policy is FOR ALL because a person
-- is the one who asks for a teardown.
CREATE POLICY tenant_isolation ON repository_setups
  FOR SELECT TO antifailure_app
  USING (org_id = current_org());

-- A verified delivery enqueues, reached through the installation, the same
-- shape as github_delivery_writes_repository in 0013. The sweeper writes under
-- the same declaration, on a connection scoped to the account whose row it
-- claimed, which is what sweepGenerations already does for pr_generations.
CREATE POLICY github_delivery_writes_setup ON repository_setups
  FOR ALL TO antifailure_app
  USING (org_id IN (
    SELECT org_id FROM github_installations
    WHERE lower(account_login) = current_github_account()))
  WITH CHECK (org_id IN (
    SELECT org_id FROM github_installations
    WHERE lower(account_login) = current_github_account()));

-- What the sweeper may see on its own connection: due rows, SELECT, nothing
-- more. The reasoning is in 0021 above teardown_claim_sweep and it is not
-- repeated here; the short form is that a policy whose USING names no tenant
-- widens every other policy on the table, so this one is gated on the
-- sweeper's own declaration and on the row already being due.
CREATE POLICY setup_claim_sweep ON repository_setups
  FOR SELECT TO antifailure_app
  USING (current_sweeper()
         AND state IN ('queued', 'leased')
         AND (leased_until IS NULL OR leased_until < now()));

COMMIT;
