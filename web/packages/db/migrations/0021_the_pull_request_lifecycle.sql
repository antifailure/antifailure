-- What a pull request is, to this control plane, and what may write it.
--
-- Everything here exists because of one sentence: a result belongs to a
-- COMMIT, not to a pull request. A pull request is a moving target. Somebody
-- pushes while a check is running, the old run finishes second, and the answer
-- on the screen is about a commit that is no longer the head. That is not a
-- rare race. It is what a busy pull request looks like all afternoon.
--
-- So the unit of work is a GENERATION: one attempt against one exact head SHA.
-- The check, the comment and the teardown are all fenced on that SHA, and a
-- writer holding an older one loses on a comparison rather than on timing.
--
-- FOUR TABLES AND WHY EACH IS SEPARATE.
--
-- github_deliveries is the replay fence. Everything below is driven by
-- webhook deliveries, and GitHub sends the same delivery twice often enough
-- that "handled once" has to be a property of the database rather than of the
-- handler being careful. The HMAC says a delivery is genuine; it says nothing
-- about it being new. A captured delivery replayed a thousand times verifies a
-- thousand times.
--
-- pull_requests is the identity: which commit is the head right now, whether
-- the head lives in a fork, which exact commit a maintainer approved, and
-- which comment this control plane maintains.
--
-- pr_generations is the work: one row per head, holding the state, GitHub's
-- check run and workflow run numbers, and the hash of the credential the job
-- reports back with.
--
-- teardown_requests is the durable half of cleanup. `environments.teardown`
-- marked a row `torn_down` and nothing anywhere read it, so the console's
-- teardown button changed a word on a page and left the customer's containers
-- running. A request that has to reach another system across a network cannot
-- be a column update; it is a queue with a lease, an attempt count and an
-- acknowledgement, or it is a lie.

BEGIN;

-- ---------------------------------------------------------------------------
-- The delivery a connection declares it is handling.
--
-- The same shape as current_github_account in 0013 and current_stripe_customer
-- in 0020: the value is not a secret, and what makes it believable is the
-- signature check one layer up. What it buys is that a delivery reaches the
-- one ledger row it is about and no other, so a bug in the handler cannot read
-- the ledger of an account it has nothing to do with.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION current_github_delivery() RETURNS text
  LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('antifailure.github_delivery', true), '') $$;

-- ---------------------------------------------------------------------------
-- The ledger, and the fence
-- ---------------------------------------------------------------------------

-- One row per delivery GitHub has sent, claimed before it is handled.
--
-- THE CLAIM IS THE LEASE. The route inserts this row, handles the delivery,
-- and then stamps handled_at. A second copy of the same delivery collides on
-- the primary key and is answered without running anything. A handler that
-- throws DELETES its claim on the way out, so the retry that follows is free
-- to take it: a claim that survived a failure would turn one transient
-- database error into a delivery that is refused forever and looks handled.
--
-- NO PAYLOAD IS STORED, and this is the one place it differs from
-- billing_events, deliberately. A Stripe event is about money and is not always
-- re-readable, so keeping it is worth the risk. A GitHub delivery carries
-- repository names, branch names, commit messages and the login of everybody
-- involved, all of it re-readable from GitHub by anybody entitled to it. The
-- event name, the action and the account are enough to answer "did we handle
-- it", which is the only question this table exists for.
CREATE TABLE github_deliveries (
  -- The x-github-delivery header. GitHub's own identifier for one delivery,
  -- which is stable across its retries of that delivery and different for a
  -- redelivery somebody asked for by hand.
  delivery_id   text PRIMARY KEY,
  -- Null until the handler resolves one, and null forever for a delivery about
  -- an account this installation has never seen. Deliberately not defaulted:
  -- a wrong organization on a delivery record is worse than no organization.
  org_id        uuid REFERENCES organizations(id) ON DELETE CASCADE,
  -- Lower-cased, because the policies compare lower-cased.
  account_login text,
  event         text NOT NULL,
  action        text,
  received_at   timestamptz NOT NULL DEFAULT now(),
  handled_at    timestamptz,
  -- One short sentence, the same one the endpoint answers with. Never a body.
  outcome       text,
  CONSTRAINT github_deliveries_outcome_length CHECK (outcome IS NULL OR length(outcome) <= 500)
);

CREATE INDEX github_deliveries_org_idx ON github_deliveries (org_id, received_at DESC);
-- Finding the claims a crashed process left behind, without scanning the whole
-- ledger. A partial index, because the unhandled rows are the small set.
CREATE INDEX github_deliveries_unhandled_idx ON github_deliveries (received_at)
  WHERE handled_at IS NULL;

-- ---------------------------------------------------------------------------
-- Pull requests
-- ---------------------------------------------------------------------------

CREATE TABLE pull_requests (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  repository_id   uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
  number          integer NOT NULL,
  title           text,
  -- The commit at the tip of the head branch, as of the last delivery. Every
  -- decision below compares against this rather than against the pull request.
  head_sha        text NOT NULL,
  head_ref        text NOT NULL,
  base_ref        text NOT NULL,
  -- owner/name of the repository the head branch lives in. Equal to the base
  -- repository for an ordinary branch, and different for a fork. That
  -- difference is the whole of what makes a pull request untrusted, so it is
  -- stored rather than derived at each decision.
  head_repository text NOT NULL,
  from_fork       boolean NOT NULL DEFAULT false,
  draft           boolean NOT NULL DEFAULT false,
  state           text NOT NULL DEFAULT 'open',
  -- The EXACT commit a maintainer approved for a fork, never the pull request.
  -- A fork approval that outlived a push would be an approval of code nobody
  -- looked at, which is the entire attack this column exists to prevent.
  approved_sha    text,
  approved_by     text,
  approved_at     timestamptz,
  -- The one comment this control plane maintains, and the head it currently
  -- reports. The pair is a compare-and-set: a writer carrying an older head
  -- sees comment_sha and declines rather than overwriting a newer answer with
  -- a stale one. That ordering is not hypothetical, it is what a cancelled run
  -- finishing after its replacement looks like.
  comment_id      bigint,
  comment_sha     text,
  comment_updated_at timestamptz,
  opened_at       timestamptz,
  closed_at       timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (repository_id, number),
  CONSTRAINT pull_requests_state CHECK (state IN ('open', 'closed', 'merged'))
);

CREATE INDEX pull_requests_org_idx ON pull_requests (org_id, updated_at DESC);
CREATE INDEX pull_requests_head_idx ON pull_requests (repository_id, head_sha);

-- ---------------------------------------------------------------------------
-- Generations
-- ---------------------------------------------------------------------------

-- The seven states, written down where a query can see them.
--
-- Five of them are the run verdicts the engine already has, plus the two a
-- pull request adds: queued, for work asked for and not started, and
-- cancelled, for work stopped because the head moved or the pull request
-- closed.
--
-- blocked and unverified are NOT passes and never render as one. That is the
-- defect this whole file exists downstream of: `af test` exits 0 on
-- unverified, blocked does not count against a run, and an entire nightly
-- corpus was green having never once reached an agent. A check run whose
-- conclusion is `success` for a generation that verified nothing is the same
-- lie with a wider audience.
CREATE TYPE pr_generation_state AS ENUM (
  'queued', 'running', 'passed', 'failed', 'blocked', 'unverified', 'cancelled'
);

CREATE TABLE pr_generations (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  pull_request_id uuid NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
  head_sha        text NOT NULL,
  -- Bumped when somebody presses Re-run. One row per head rather than one per
  -- attempt, because "one stable check per head" is the property a repository
  -- makes required, and a second row would mean a second check.
  attempt         integer NOT NULL DEFAULT 1,
  state           pr_generation_state NOT NULL DEFAULT 'queued',
  -- Why it is in that state, in one sentence, for the check output and the
  -- comment. Never a stack, and never an address this control plane cannot
  -- serve: a report full of http://localhost:46001 links is a report whose
  -- every link is dead for the person reading it.
  detail          text,
  -- GitHub's numbers. check_run_id stays null while the installation does not
  -- hold `checks: write`, which is a state this has to serve rather than crash
  -- in: the comment still lands, and the console says which permission is
  -- missing.
  check_run_id    bigint,
  workflow_run_id bigint,
  -- The hash of the credential this generation's job reports with, never the
  -- credential. Scoped to one generation and short lived, so a leaked one is
  -- good for overwriting one commit's result until it expires.
  callback_hash   bytea,
  callback_expires_at timestamptz,
  -- How the report was authenticated, for the audit trail: the workflow's own
  -- OIDC identity, or an engine token.
  reported_by     text,
  -- The engine's environment identifier, when one was reported. The evidence
  -- and the preview address hang off this.
  env_id          text,
  -- The report as it arrived, already reduced to counts and verdicts by the
  -- engine. Never a body, never a log, never a screenshot.
  verdict         jsonb,
  superseded_by   uuid REFERENCES pr_generations(id) ON DELETE SET NULL,
  queued_at       timestamptz NOT NULL DEFAULT now(),
  started_at      timestamptz,
  finished_at     timestamptz,
  -- When a generation that has said nothing becomes `unverified` rather than
  -- staying `running` forever. A check that spins for a week is worse than one
  -- that says it gave up, because nobody can tell it apart from a slow one.
  deadline_at     timestamptz NOT NULL,
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (pull_request_id, head_sha)
);

CREATE INDEX pr_generations_org_idx ON pr_generations (org_id, queued_at DESC);
-- The sweeper's read: everything still open and past its deadline. Partial, so
-- the scan is over the live set rather than over every generation ever.
CREATE INDEX pr_generations_open_idx ON pr_generations (deadline_at)
  WHERE state IN ('queued', 'running');
-- Matching a workflow_run delivery back to the generation that asked for it.
CREATE INDEX pr_generations_run_idx ON pr_generations (workflow_run_id)
  WHERE workflow_run_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Teardown
-- ---------------------------------------------------------------------------

-- Cleanup that has to reach another system, recorded so it survives this
-- process dying.
--
-- The lease is the part that makes it durable rather than decorative. A pass
-- takes a request by writing its own name and an expiry; a pass that dies
-- holding one leaves a row the next pass takes over when the lease runs out,
-- rather than a row nobody may ever touch again. attempts is what stops a
-- request that can never succeed from being retried forever, and last_error is
-- what makes the giving up readable.
--
-- ACKNOWLEDGED MEANS THE RUNTIME SAID SO. Not "we sent the cancel", not "we
-- updated the row": the workflow run reached a terminal status, or the engine
-- reported the environment torn down. Everything this repository has learned
-- about dead code says the difference matters, and teardown is the surface
-- where the difference is somebody's cloud bill.
CREATE TABLE teardown_requests (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  -- What to tear down. environment_id is set when the control plane has a row
  -- for it, and env_id alone when a generation reported a name before the
  -- projection caught up.
  environment_id  uuid REFERENCES environments(id) ON DELETE CASCADE,
  env_id          text,
  repository_id   uuid REFERENCES repositories(id) ON DELETE CASCADE,
  -- The Actions run holding the environment, when there is one. Cancelling it
  -- is the only route this control plane HAS into the customer's runtime: it
  -- holds no cluster credential, no address and no kubeconfig, by design, and
  -- `af ci` tears the environment down on a cancelled job.
  workflow_run_id bigint,
  generation_id   uuid REFERENCES pr_generations(id) ON DELETE SET NULL,
  reason          text NOT NULL,
  requested_by    uuid REFERENCES users(id) ON DELETE SET NULL,
  state           text NOT NULL DEFAULT 'pending',
  attempts        integer NOT NULL DEFAULT 0,
  lease_holder    text,
  leased_until    timestamptz,
  last_error      text,
  requested_at    timestamptz NOT NULL DEFAULT now(),
  acknowledged_at timestamptz,
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT teardown_requests_state
    CHECK (state IN ('pending', 'leased', 'acknowledged', 'abandoned'))
);

CREATE INDEX teardown_requests_org_idx ON teardown_requests (org_id, requested_at DESC);
-- The sweeper's read: what is claimable now. `leased_until IS NULL OR
-- leased_until < now()` is the takeover condition and it is why a dead holder
-- costs one lease period rather than the request.
CREATE INDEX teardown_requests_claimable_idx ON teardown_requests (leased_until)
  WHERE state IN ('pending', 'leased');

-- ---------------------------------------------------------------------------
-- Grants
--
-- Exactly the verbs the application uses. Nothing deletes any of these: a
-- delivery that was handled stays handled, a generation that ran stays in the
-- history of that commit, and a teardown request that was acknowledged is the
-- evidence the cleanup happened. The one exception is the delivery claim,
-- which a failed handler removes so its own retry can take it, and that is why
-- DELETE is granted on github_deliveries alone.
-- ---------------------------------------------------------------------------

GRANT SELECT, INSERT, UPDATE ON
  pull_requests, pr_generations, teardown_requests
TO antifailure_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON github_deliveries TO antifailure_app;

REVOKE DELETE, TRUNCATE ON
  pull_requests, pr_generations, teardown_requests FROM antifailure_app;
REVOKE TRUNCATE ON github_deliveries FROM antifailure_app;

-- ---------------------------------------------------------------------------
-- Isolation
-- ---------------------------------------------------------------------------

DO $$
DECLARE
  t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'github_deliveries', 'pull_requests', 'pr_generations', 'teardown_requests']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    -- FORCE so the policies apply to the table's owner too, for the operator
    -- who runs a migration as the owner and leaves a connection open.
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
  END LOOP;
END
$$;

-- What a tenant reads. All three of these are written by a delivery or by a
-- sweeper and read by the console.
--
-- pull_requests and pr_generations are SELECT only for a tenant, deliberately.
-- Nothing a person can reach writes them: the state of a commit's check is
-- what GitHub and the job reported, and a tenant able to write it could mark
-- their own failing commit passed.
CREATE POLICY tenant_isolation ON pull_requests
  FOR SELECT TO antifailure_app
  USING (org_id = current_org());

CREATE POLICY tenant_isolation ON pr_generations
  FOR SELECT TO antifailure_app
  USING (org_id = current_org());

CREATE POLICY tenant_isolation ON github_deliveries
  FOR SELECT TO antifailure_app
  USING (org_id = current_org());

-- Teardown is the one a person asks for, so a tenant writes it. The WITH CHECK
-- ties the row to the caller's own organization, so a request naming another
-- tenant's environment is refused by Postgres rather than by the handler
-- remembering to filter.
CREATE POLICY tenant_isolation ON teardown_requests
  FOR ALL TO antifailure_app
  USING (org_id = current_org())
  WITH CHECK (org_id = current_org());

-- What a verified delivery may reach.
--
-- The ledger row it declared, and only that one. Reached before the account is
-- known, which is why this keys on the delivery rather than on the account: the
-- claim has to be writable for a delivery about an account this installation
-- has never heard of, and that delivery has no account to key on.
CREATE POLICY github_delivery_records_itself ON github_deliveries
  FOR ALL TO antifailure_app
  USING (delivery_id = current_github_delivery())
  WITH CHECK (delivery_id = current_github_delivery());

-- The pull request and its generations, reached through the installation, the
-- same shape 0013 uses for repositories. Naming another account's repository
-- writes under this account's organization or writes nothing.
CREATE POLICY github_delivery_writes_pull_request ON pull_requests
  FOR ALL TO antifailure_app
  USING (org_id IN (
    SELECT org_id FROM github_installations
    WHERE lower(account_login) = current_github_account()))
  WITH CHECK (org_id IN (
    SELECT org_id FROM github_installations
    WHERE lower(account_login) = current_github_account()));

CREATE POLICY github_delivery_writes_generation ON pr_generations
  FOR ALL TO antifailure_app
  USING (org_id IN (
    SELECT org_id FROM github_installations
    WHERE lower(account_login) = current_github_account()))
  WITH CHECK (org_id IN (
    SELECT org_id FROM github_installations
    WHERE lower(account_login) = current_github_account()));

-- A delivery asks for teardown when a pull request closes, so it writes here
-- too, confined the same way.
CREATE POLICY github_delivery_writes_teardown ON teardown_requests
  FOR ALL TO antifailure_app
  USING (org_id IN (
    SELECT org_id FROM github_installations
    WHERE lower(account_login) = current_github_account()))
  WITH CHECK (org_id IN (
    SELECT org_id FROM github_installations
    WHERE lower(account_login) = current_github_account()));

-- A delivery reads the repository it is about, to find the pull request's
-- organization. 0013 already grants ALL on repositories to the same
-- declaration, so this adds nothing; it is named here so a reader of this file
-- does not conclude the join below has no policy behind it.

-- ---------------------------------------------------------------------------
-- What housekeeping may see
--
-- A sweeper has no tenant and no delivery, so every policy above denies it, and
-- a DELETE or an UPDATE that matches nothing reports success. That is not a
-- hypothetical failure mode here: migration 0016 exists because
-- sweepDeviceAuthorizations ran for the life of the process and removed zero
-- rows, forever, for exactly this reason.
--
-- SELECT and nothing more, and only over rows that are already overdue. The
-- sweeper reads which work is due on this connection and then does the writing
-- on a connection scoped to that work's own tenant, so nothing here needs to be
-- able to write across tenants. A read-only policy over a row set that is
-- already late is the narrowest thing that makes the sweeper able to see its
-- own queue.
-- ---------------------------------------------------------------------------

CREATE POLICY generation_deadline_sweep ON pr_generations
  FOR SELECT TO antifailure_app
  USING (state IN ('queued', 'running') AND deadline_at < now());

CREATE POLICY teardown_claim_sweep ON teardown_requests
  FOR SELECT TO antifailure_app
  USING (state IN ('pending', 'leased')
         AND (leased_until IS NULL OR leased_until < now()));

-- ---------------------------------------------------------------------------
-- What the job reports with
--
-- The callback is a bearer credential, so it is reachable by presenting the
-- hash of the credential itself, exactly as 0004 does for engine tokens and
-- 0012 for a device code. Without this the report path would have to run with
-- no tenant, on a connection that can read every generation in the database.
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION current_pr_callback() RETURNS bytea
  LANGUAGE sql STABLE
  AS $$ SELECT decode(nullif(current_setting('antifailure.pr_callback_hash', true), ''), 'hex') $$;

-- USING is what admits the row: the connection reaches the generation whose
-- stored hash matches the credential it declared, and nothing else.
--
-- WITH CHECK permits the hash to be cleared as well as kept, which reads as a
-- loosening and is not: a row whose hash is null is a row this declaration can
-- no longer reach at all, so the only thing it allows is a caller giving up its
-- own access. Refusing it would mean a report could never mark its own
-- credential spent, which is the one thing that has to happen exactly once.
CREATE POLICY pr_callback_reports ON pr_generations
  FOR ALL TO antifailure_app
  USING (callback_hash IS NOT NULL AND callback_hash = current_pr_callback())
  WITH CHECK (callback_hash IS NULL OR callback_hash = current_pr_callback());

COMMIT;
