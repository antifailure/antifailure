-- Running an organization without emailing anybody.
--
-- Four things live here, and they are together because they share one property:
-- each is reached by somebody who is not yet, or is no longer, an ordinary
-- member of the organization the row belongs to.
--
--   invitations                  the invited person is not a member yet, so the
--                                acceptance transaction has no tenant.
--   billing_contacts             the address invoices go to, which is not any
--                                member's sign-in address.
--   organization_deletions       the state machine, which has to outlive the
--                                organization row it is deleting.
--   organization_deletion_exports the export, which has to be downloadable
--                                after the membership that would have
--                                authorised the download is gone.
--
-- ---------------------------------------------------------------------------
-- The invitation, and the transaction that has no tenant
-- ---------------------------------------------------------------------------
--
-- An invited person is signed in and belongs to no organization, so
-- `withTenant` cannot be used: current_org() is what the acceptance is trying
-- to establish. The same shape 0013 and 0020 use for a webhook delivery is used
-- here: the caller declares, in `antifailure.invitation_token_hash`, the hash of
-- the token it was given, and the policies key on that. Declaring a value the
-- caller did not receive returns nothing, because the value is the sha256 of a
-- 32 byte random token that exists in the invitation link and nowhere else.
--
-- Unlike a webhook, the declared value here IS a secret rather than a name, so
-- this is the primary control rather than defence in depth.
--
-- ---------------------------------------------------------------------------
-- Why the deletion record has no foreign key to the organization
-- ---------------------------------------------------------------------------
--
-- Every other table here cascades from `organizations`, which is what makes the
-- purge a single statement. The deletion record must not, because the whole
-- point of it is to survive that statement: it carries the evidence of what was
-- done, in what order, and the export the customer is owed. A record that
-- cascaded would delete the proof of the deletion at the moment of the
-- deletion, which is the one moment somebody is most likely to ask for it.
--
-- So org_id is a plain uuid with no reference, and a partial unique index gives
-- the constraint that actually matters: one live deletion per organization at a
-- time. A finished one does not block a later organization reusing the id,
-- which cannot happen anyway, and does not need to be reasoned about.

BEGIN;

-- The invitation a caller declares it holds. Hashed, so the token itself is
-- never compared and never stored: the same reason sessions store a hash.
CREATE OR REPLACE FUNCTION current_invitation_token() RETURNS bytea
  LANGUAGE sql STABLE
  AS $$ SELECT decode(nullif(current_setting('antifailure.invitation_token_hash', true), ''), 'hex') $$;

-- The deletion record a caller declares it holds, for downloading an export
-- after the organization it belonged to is gone.
CREATE OR REPLACE FUNCTION current_deletion_token() RETURNS bytea
  LANGUAGE sql STABLE
  AS $$ SELECT decode(nullif(current_setting('antifailure.deletion_token_hash', true), ''), 'hex') $$;

-- ---------------------------------------------------------------------------
-- Invitations
-- ---------------------------------------------------------------------------

CREATE TABLE invitations (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  -- Lower-cased by the application before it gets here. Compared as text
  -- rather than through a functional index so that the partial unique index
  -- below is readable, and so a mixed-case address cannot become a second
  -- invitation for the same person.
  email         text NOT NULL,
  role          member_role NOT NULL,
  token_hash    bytea NOT NULL UNIQUE,
  -- Who sent it, twice, and the second one is the point.
  --
  -- invited_by goes null when that user's account is deleted. invited_by_label
  -- is a copy of how they were named at the time, kept so that an invitation
  -- accepted after the inviter has left still says who sent it. An invitation
  -- that reads "invited by (nobody)" is one a new joiner cannot evaluate.
  invited_by       uuid REFERENCES users(id) ON DELETE SET NULL,
  invited_by_label text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  -- Three terminal states, and each is distinct on purpose. Accepted names the
  -- account that took it up, so a second click is recognisable as a repeat
  -- rather than as a new join. Revoked records that somebody withdrew it.
  -- Expiry needs no column: it is a comparison against expires_at, made by the
  -- application against its injected clock rather than by a policy against the
  -- server's wall clock, because a test with a fake clock has to be able to
  -- reach it.
  accepted_at      timestamptz,
  accepted_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  revoked_at       timestamptz,
  revoked_by_label text,

  CONSTRAINT invitations_accepted_together
    CHECK ((accepted_at IS NULL) = (accepted_user_id IS NULL)),
  -- An invitation cannot be both. Whichever landed first stands, and the
  -- application refuses the second rather than letting the row say two things.
  CONSTRAINT invitations_one_outcome
    CHECK (accepted_at IS NULL OR revoked_at IS NULL)
);

-- One open invitation per address per organization.
--
-- Partial, so that a revoked or accepted invitation does not block sending a
-- new one, which is the ordinary case: somebody is invited, leaves, and is
-- invited again. Without the partial clause the second invitation would be
-- refused by a constraint violation that reads as a bug.
CREATE UNIQUE INDEX invitations_open_key ON invitations (org_id, email)
  WHERE accepted_at IS NULL AND revoked_at IS NULL;

CREATE INDEX invitations_org_created_idx ON invitations (org_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- The billing contact
-- ---------------------------------------------------------------------------

-- Where invoices and billing notices go, which is deliberately not derived from
-- the members table.
--
-- A separate table rather than a column on billing_customers, and the reason is
-- ordering rather than tidiness. billing_customers only exists once somebody
-- has started a checkout, and the address has to be settable before that: an
-- organization evaluating the product wants finance to receive the first
-- invoice, and being told "buy something first" is not an answer. The
-- application writes both in one transaction whenever the customer row exists,
-- so the two cannot disagree, and this one is the source of truth.
CREATE TABLE billing_contacts (
  org_id      uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
  email       text NOT NULL,
  name        text,
  -- Who set it and when, because "the invoices started going somewhere else"
  -- is a question with a person at the end of it.
  updated_by_label text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- The organization deletion state machine
-- ---------------------------------------------------------------------------

-- Deleting an organization is not a DELETE.
--
-- The row is the last thing that goes, and every step before it is one that
-- cannot be undone by rolling back a transaction, because each acts on
-- something outside this database: work that is running, a subscription at
-- Stripe, an App installation at GitHub, credentials somebody's CI is holding.
-- A bare cascade would remove the row while Stripe kept billing the card, which
-- is a real-world failure rather than a data-model detail: the customer is gone
-- from our side and still paying.
--
-- So each step has its own timestamp column, and the state is derived from
-- which of them are set. That is the property that makes an interrupted
-- deletion safe to re-enter: the resumer reads the record, finds the first step
-- with no timestamp, and does that one. A step that half-happened and was
-- recorded as done is the only unrecoverable state, so every step writes its
-- timestamp in the same transaction as the change it describes, or does not
-- write it at all.
--
-- The order is fixed and it is not arbitrary:
--
--   1 stop work            first, so nothing new is created behind the deletion
--                          and nothing is still running when credentials go.
--   2 cancel subscription  before the wait, so the wait is bounded by a period
--                          that is already ending rather than renewing.
--   3 wait                 the customer paid for the period; they keep it.
--                          Everything still works during it, deliberately.
--   4 revoke credentials   after the wait, because revoking during it would
--                          break an organization that is still paying.
--   5 export               after revocation, so the export describes the final
--                          state rather than one that then changed.
--   6 purge                last, and only reachable when 1 to 5 are all set.
CREATE TABLE organization_deletions (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  -- No REFERENCES. See the note at the top of this file: this record has to
  -- outlive the organization it is about.
  org_id      uuid NOT NULL,
  -- Copied at request time so the record can still say which organization this
  -- was after the row that carried the name is gone.
  org_slug    text NOT NULL,
  org_name    text NOT NULL,

  requested_by       uuid,
  requested_by_label text NOT NULL,
  reason             text,
  requested_at       timestamptz NOT NULL DEFAULT now(),

  -- The six steps, in order. Each is null until its step has completed.
  work_stopped_at         timestamptz,
  -- How much was stopped, so the record says what was destroyed rather than
  -- only that something was.
  environments_stopped    integer,
  runs_cancelled          integer,

  subscription_cancelled_at timestamptz,
  -- Null when there was nothing to cancel, which is the common case for a
  -- free organization and is not an error. The distinction between "cancelled
  -- subscription X" and "there was no subscription" is worth keeping: only one
  -- of them means somebody should check Stripe.
  subscription_id           text,
  -- When the paid entitlement actually ends, read from the subscription rather
  -- than computed here. Null means there is nothing to wait for.
  entitlement_ends_at       timestamptz,

  credentials_revoked_at  timestamptz,
  engine_tokens_revoked   integer,
  provider_keys_revoked   integer,
  sessions_revoked        integer,
  installations_revoked   integer,

  exported_at             timestamptz,

  purged_at               timestamptz,

  -- Abandoning a deletion. Allowed until the purge and not after it, because
  -- after it there is nothing to come back to.
  cancelled_at            timestamptz,
  cancelled_by_label      text,

  -- The last failure, kept so an operator and the owner can both see why a
  -- deletion has stopped moving. Cleared on the next successful step.
  last_error_at           timestamptz,
  last_error_step         text,
  last_error_message      text,
  attempts                integer NOT NULL DEFAULT 0,

  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),

  -- The invariants that make "resumable" mean something. Each step may only be
  -- marked done when its predecessor is, so a record can never claim to have
  -- exported before it revoked, and the purge can never be recorded against a
  -- record that skipped a step.
  CONSTRAINT deletions_cancel_after_stop
    CHECK (subscription_cancelled_at IS NULL OR work_stopped_at IS NOT NULL),
  CONSTRAINT deletions_revoke_after_cancel
    CHECK (credentials_revoked_at IS NULL OR subscription_cancelled_at IS NOT NULL),
  CONSTRAINT deletions_export_after_revoke
    CHECK (exported_at IS NULL OR credentials_revoked_at IS NOT NULL),
  CONSTRAINT deletions_purge_after_export
    CHECK (purged_at IS NULL OR exported_at IS NOT NULL),
  -- A purged deletion cannot also be cancelled. Cancelling is what stops the
  -- machine before the purge; there is nothing to stop afterwards.
  CONSTRAINT deletions_not_both_outcomes
    CHECK (purged_at IS NULL OR cancelled_at IS NULL)
);

-- One live deletion per organization.
--
-- Partial rather than a primary key on org_id, so that a deletion which was
-- cancelled does not stop the owner asking again. A purged one cannot conflict
-- with anything, because there is no organization left to ask.
CREATE UNIQUE INDEX organization_deletions_live_key ON organization_deletions (org_id)
  WHERE purged_at IS NULL AND cancelled_at IS NULL;

CREATE INDEX organization_deletions_org_idx ON organization_deletions (org_id, requested_at DESC);

-- ---------------------------------------------------------------------------
-- The export the customer is owed
-- ---------------------------------------------------------------------------

-- Held apart from the state machine because it is the only part that carries
-- customer data, and because its lifetime is different: the record is kept, the
-- document is destroyed when the retention window ends or when the owner asks
-- for it to be destroyed early.
--
-- The download token is what makes it reachable after the purge. There is no
-- membership to authorise against once the organization is gone, so the owner
-- is given a link at request time, exactly as they would be for a password
-- reset, and the hash is what is stored.
CREATE TABLE organization_deletion_exports (
  deletion_id  uuid PRIMARY KEY REFERENCES organization_deletions(id) ON DELETE CASCADE,
  -- Carried here as well as reachable through the deletion, and it is not
  -- denormalisation for speed.
  --
  -- The first version scoped this table by joining to organization_deletions,
  -- and the deletion record's own download policy joins the other way, so
  -- Postgres refused every statement with "infinite recursion detected in
  -- policy". A policy that has to read another table whose policy reads this
  -- one is not a policy anybody can reason about either. So the tenant is a
  -- column, the policy is the same comparison every other table here makes, and
  -- the only cross-table policy left points one way.
  --
  -- No REFERENCES, for the same reason the deletion record has none: this row
  -- outlives the organization.
  org_id       uuid NOT NULL,
  token_hash   bytea NOT NULL UNIQUE,
  -- The whole export, as the document the download route serves. Held rather
  -- than regenerated because after the purge there is nothing left to generate
  -- it from, which is the entire reason the export happens before the delete
  -- rather than after it.
  document     jsonb NOT NULL,
  entry_count  integer NOT NULL,
  size_bytes   bigint NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  downloaded_at timestamptz,
  download_count integer NOT NULL DEFAULT 0,
  -- Set when the document is destroyed, either by the sweep at expiry or by the
  -- owner asking early. The row stays so that "there was an export and it is
  -- gone" is distinguishable from "there was never an export".
  destroyed_at timestamptz
);

CREATE INDEX organization_deletion_exports_expiry_idx
  ON organization_deletion_exports (expires_at) WHERE destroyed_at IS NULL;

-- ---------------------------------------------------------------------------
-- Grants
--
-- Exactly the verbs the application uses, per table, the rule 0002 states.
--
-- DELETE is granted on invitations and withheld everywhere else here. An
-- invitation that was sent to the wrong address should be removable outright
-- rather than left as a revoked row naming somebody's email forever, and it
-- carries no history anybody needs. A deletion record is the opposite: it is
-- the evidence that a deletion happened correctly, and removing it takes a
-- schema change and a conversation.
-- ---------------------------------------------------------------------------

GRANT SELECT, INSERT, UPDATE, DELETE ON invitations TO antifailure_app;
GRANT SELECT, INSERT, UPDATE ON
  billing_contacts, organization_deletions, organization_deletion_exports
TO antifailure_app;
REVOKE DELETE, TRUNCATE ON
  billing_contacts, organization_deletions, organization_deletion_exports
  FROM antifailure_app;

-- ---------------------------------------------------------------------------
-- Isolation
-- ---------------------------------------------------------------------------

DO $$
DECLARE
  t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'invitations', 'billing_contacts', 'organization_deletions',
    'organization_deletion_exports']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    -- FORCE so the policies apply to the table's owner too, for the operator
    -- who runs a migration as the owner and leaves a connection open.
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
  END LOOP;
END
$$;

-- The two tables an ordinary tenant reads and writes through the API.
CREATE POLICY tenant_isolation ON invitations
  FOR ALL TO antifailure_app
  USING (org_id = current_org())
  WITH CHECK (org_id = current_org());

CREATE POLICY tenant_isolation ON billing_contacts
  FOR ALL TO antifailure_app
  USING (org_id = current_org())
  WITH CHECK (org_id = current_org());

-- The deletion record, while the organization still exists.
--
-- No DELETE verb is granted, so FOR ALL here cannot become a way to remove the
-- record; the grant is the control and the policy is the scope.
CREATE POLICY tenant_isolation ON organization_deletions
  FOR ALL TO antifailure_app
  USING (org_id = current_org())
  WITH CHECK (org_id = current_org());

CREATE POLICY tenant_isolation ON organization_deletion_exports
  FOR ALL TO antifailure_app
  USING (org_id = current_org())
  WITH CHECK (org_id = current_org());

-- ---------------------------------------------------------------------------
-- What somebody holding an invitation link may reach
-- ---------------------------------------------------------------------------

-- The invitation itself, so the page can say which organization and which role
-- before anybody signs in, and so acceptance can read what it is accepting.
CREATE POLICY invitation_holder_reads ON invitations
  FOR SELECT TO antifailure_app
  USING (token_hash = current_invitation_token());

-- Marking it accepted. USING and WITH CHECK both key on the token, so this
-- cannot be used to write any other invitation, including another one in the
-- same organization.
CREATE POLICY invitation_holder_accepts ON invitations
  FOR UPDATE TO antifailure_app
  USING (token_hash = current_invitation_token())
  WITH CHECK (token_hash = current_invitation_token());

-- The membership the acceptance creates.
--
-- INSERT only, and the organization is not the caller's to choose: it is read
-- from the invitation the declared token names. A handler that passed the wrong
-- organization writes a row for the invitation's organization or writes
-- nothing.
--
-- Deliberately NOT conditioned on accepted_at being null. The handler marks the
-- invitation accepted and inserts the membership in one transaction, and a
-- policy that read accepted_at would make the two orderings behave differently
-- for no security benefit: the token is the proof either way, and replaying it
-- is refused by the unique index on (org_id, user_id) and by the handler.
CREATE POLICY invitation_holder_joins ON members
  FOR INSERT TO antifailure_app
  WITH CHECK (
    org_id = (SELECT i.org_id FROM invitations i
              WHERE i.token_hash = current_invitation_token()));

-- ---------------------------------------------------------------------------
-- What somebody holding a deletion export link may reach
-- ---------------------------------------------------------------------------

-- After the purge there is no membership left to authorise a download, so the
-- link is the authorisation. SELECT and UPDATE: the download is a read, and
-- recording that it happened is a write. No INSERT, so a link cannot create a
-- second export row, and no way to reach any other export.
CREATE POLICY export_holder_reads ON organization_deletion_exports
  FOR SELECT TO antifailure_app
  USING (token_hash = current_deletion_token());

CREATE POLICY export_holder_records_download ON organization_deletion_exports
  FOR UPDATE TO antifailure_app
  USING (token_hash = current_deletion_token())
  WITH CHECK (token_hash = current_deletion_token());

-- The deletion record behind an export the caller can already read, so the
-- download page can name the organization and say when the document goes.
-- SELECT only: holding a download link does not move a state machine.
CREATE POLICY export_holder_reads_deletion ON organization_deletions
  FOR SELECT TO antifailure_app
  USING (id = (SELECT e.deletion_id FROM organization_deletion_exports e
               WHERE e.token_hash = current_deletion_token()));

-- ---------------------------------------------------------------------------
-- Finding the deletions that are due, from a process that is not a request
-- ---------------------------------------------------------------------------

-- The resumer runs on a timer inside the control plane and has to act on every
-- organization, so it cannot use withTenant: current_org() is what it is
-- looking for. Every other unscoped read in this schema is keyed on a value the
-- caller received from a client, and this one has no client to receive
-- anything from.
--
-- So it gets a SECURITY DEFINER function returning the narrowest thing that
-- works: an organization id and a deletion id, for records that are actually
-- due, and nothing else. There is no projection of a deletion record, an
-- export, or any tenant table through it, and no argument that widens it: the
-- caller cannot ask for a record that is not due. Having found an organization,
-- the resumer does every step scoped to that one organization through the
-- ordinary tenant path, so the isolation the rest of this schema provides still
-- applies to the work itself.
--
-- The wait is expressed here rather than in the caller because it is what makes
-- a record NOT due: a deletion whose paid period has not ended must not be
-- picked up, and a caller that had to filter afterwards would have had to read
-- the record to do it.
--
-- The back-off is here for the same reason. A step that fails increments
-- attempts, and a record that has just failed is not offered again for a
-- minute per attempt, capped at thirty. Without it a step that fails
-- deterministically, such as Stripe refusing a cancellation, is retried every
-- sweep forever and writes a line to the log every time.
CREATE OR REPLACE FUNCTION deletions_due(as_of timestamptz, limit_count integer)
  RETURNS TABLE (org_id uuid, deletion_id uuid)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
  AS $fn$
    SELECT d.org_id, d.id
    FROM organization_deletions d
    WHERE d.purged_at IS NULL
      AND d.cancelled_at IS NULL
      -- Blocked only while the paid period is still running AND the step that
      -- waits for it has not happened. Everything before the wait, and
      -- everything after it, is due immediately.
      AND NOT (
        d.credentials_revoked_at IS NULL
        AND d.subscription_cancelled_at IS NOT NULL
        AND d.entitlement_ends_at IS NOT NULL
        AND d.entitlement_ends_at > as_of
      )
      AND (
        d.last_error_at IS NULL
        OR d.last_error_at <= as_of - (interval '1 minute' * least(d.attempts, 30))
      )
    ORDER BY d.requested_at ASC
    LIMIT greatest(limit_count, 0)
  $fn$;

-- Exports whose retention window has ended, for the same resumer and for the
-- same reason. Returns the organization so the destroy runs through the tenant
-- path: the deletion record keeps its org_id after the purge, so the tenant
-- policy still resolves even though the organization row is gone.
CREATE OR REPLACE FUNCTION deletion_exports_expired(as_of timestamptz, limit_count integer)
  RETURNS TABLE (org_id uuid, deletion_id uuid)
  LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public
  AS $fn$
    SELECT d.org_id, e.deletion_id
    FROM organization_deletion_exports e
    JOIN organization_deletions d ON d.id = e.deletion_id
    WHERE e.destroyed_at IS NULL AND e.expires_at <= as_of
    ORDER BY e.expires_at ASC
    LIMIT greatest(limit_count, 0)
  $fn$;

-- A SECURITY DEFINER function is executable by PUBLIC unless it is taken away,
-- and PUBLIC includes every role on the cluster. Revoked first and granted
-- explicitly, in that order, so the window between the two does not exist.
REVOKE ALL ON FUNCTION deletions_due(timestamptz, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION deletion_exports_expired(timestamptz, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION deletions_due(timestamptz, integer) TO antifailure_app;
GRANT EXECUTE ON FUNCTION deletion_exports_expired(timestamptz, integer) TO antifailure_app;

-- ---------------------------------------------------------------------------
-- Closing an account
-- ---------------------------------------------------------------------------

-- A person may always leave, and their row may not be removed.
--
-- `audit_entries.actor_user_id` references `users` with NO ACTION, so deleting
-- somebody who has ever done anything is refused by the database, and that is
-- deliberate rather than an oversight: an audit log whose subject can erase
-- themselves from it is not an audit log. Nulling the column instead is not
-- available either, because UPDATE on `audit_entries` is revoked to make the
-- table append-only, and because `actor_user_id` is one of the fields the hash
-- chain covers.
--
-- So closing an account is an erasure of the personal data on the row rather
-- than a removal of the row: the GitHub identity, the name, the address and the
-- avatar go, the memberships and sessions go, and what stays is a row with no
-- personal data in it that the audit entries can still point at. The audit
-- entries keep the label the person had at the time, because the chain hashes
-- it, and those go when the organization does.
--
-- This column is what makes that state visible. Without it, a closed account is
-- only recognisable by the shape of its nulled columns, which is a convention
-- rather than a fact and is exactly the sort of thing a later query gets wrong.
ALTER TABLE users ADD COLUMN closed_at timestamptz;

COMMIT;
