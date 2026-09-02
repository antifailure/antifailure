-- Three tables an operator needs and one an operator must never be trusted
-- without: overrides, flags, and the ledger that makes a refund happen once.
--
-- ---------------------------------------------------------------------------
-- Why an override table exists at all
-- ---------------------------------------------------------------------------
--
-- `organizations.plan` names a row in PLAN_QUOTAS and PLAN_COST_CAPS, and those
-- two tables are the whole entitlement model today. That is correct and it is
-- not sufficient: every commercial relationship eventually needs a customer who
-- is on `team` and has been sold forty environments, a design partner using a
-- feature nobody else has yet, and a trial that was extended by a week because
-- somebody asked nicely on a Friday. Doing any of those by moving the plan
-- charges the wrong amount. Doing them by editing PLAN_QUOTAS gives them to
-- everybody.
--
-- So an override is a row. It says which entitlement, at which scope, to what
-- value, WHY, who granted it, and when it stops. The expiry is not decoration:
-- a grant with no end date is how a one-week trial extension becomes permanent
-- revenue leakage that nobody can find, and the only reliable fix is to make
-- forever something a person has to type rather than something they get by
-- leaving a field blank.
--
-- ---------------------------------------------------------------------------
-- Why the resolution order is in the schema comment and not only in the code
-- ---------------------------------------------------------------------------
--
-- Four scopes, resolved most specific first: user, then project, then
-- organization, then global, then the plan's own value. A reader who assumed
-- the other direction would grant an organization-wide cap that silently
-- overrode the one grant somebody made for a single user, which is the shape
-- of every entitlement bug worth having.
--
-- `project` keys on `repositories.id`, because this schema has no separate
-- projects table and a repository is what a run belongs to. If a projects table
-- arrives, this column is what moves.
--
-- ---------------------------------------------------------------------------
-- The ledger, which is the only part of this file about money
-- ---------------------------------------------------------------------------
--
-- A refund button that does not refund is a support ticket. A refund button
-- that refunds twice is somebody's money, gone, and an apology that does not
-- fix it. Between a double click, a client retry, a load balancer replaying a
-- request and an operator pressing the same button in two tabs, "twice" is not
-- an edge case; it is Tuesday.
--
-- `admin_operations` is what makes it once. The primary key is a caller-chosen
-- idempotency key, so the SECOND attempt to claim it is a constraint violation
-- rather than a second call to Stripe, and the row that already exists carries
-- the answer the first attempt got. The same key is sent to Stripe in the
-- `Idempotency-Key` header, which closes the window this table cannot: a
-- process that claimed the key, called Stripe and died before recording the
-- answer retries with the same key and gets Stripe's own cached response back
-- rather than creating a second refund.
--
-- `request_fingerprint` is the half people leave out. A key that is reused with
-- DIFFERENT parameters must be refused, not answered: silently returning the
-- first refund's result for a second, larger refund is worse than either
-- refunding twice or failing, because it reports success for something that
-- never happened. Stripe refuses this case and so does this table.

BEGIN;

-- ---------------------------------------------------------------------------
-- Who may write any of this
-- ---------------------------------------------------------------------------
--
-- 0029 already answered this and the answer is not a database role, so this
-- file deliberately creates none.
--
-- An operator is `antifailure_app`, the same role every request runs as,
-- holding a connection that has declared the hash of a live operator session.
-- `current_admin_user()` resolves to a row only for such a connection, so a
-- policy keyed on it is keyed on a credential rather than on a claim. A second
-- role here would be a second boundary: two things to grant, two things to
-- revoke, and two answers to "who can move money", which is the question that
-- must have one.
--
-- What that means for the grants below, and it is the opposite of what it looks
-- like: `antifailure_app` is granted INSERT and UPDATE on all four tables, and
-- a tenant still cannot write a single row. The grant is what makes the write
-- POSSIBLE; the policy is what makes it permitted, and every write policy here
-- requires current_admin_user() to be non-null, which it is not on a tenant
-- connection. Withholding the grant instead would also withhold it from the
-- operator, because they are the same role.

-- ---------------------------------------------------------------------------
-- Overrides
-- ---------------------------------------------------------------------------

CREATE TABLE entitlement_overrides (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),

  scope         text NOT NULL,
  -- The organization, repository or user this applies to. Null only for
  -- global, and the CHECK below makes that an invariant rather than a habit.
  scope_id      uuid,

  -- The organization the override BELONGS to, carried on the row rather than
  -- reached through a join.
  --
  -- Two jobs. The tenant read policy keys on it, and a policy that had to join
  -- three tables to decide whether a row is yours is a policy nobody can read
  -- and Postgres cannot index. And a user's entitlement is per organization:
  -- one person can be a member of two organizations and be granted extra
  -- capacity in one of them, so `scope = 'user'` without an organization would
  -- be a grant that follows somebody into a tenant that never agreed to it.
  org_id        uuid REFERENCES organizations(id) ON DELETE CASCADE,

  -- The entitlement key, from ENTITLEMENTS in src/entitlements.ts. Not a
  -- foreign key and not a CHECK constraint: the catalog lives in code because
  -- it is read by code, and a constraint here would turn adding an entitlement
  -- into a migration that has to land before the deploy that uses it.
  feature       text NOT NULL,

  -- The value, as a JSON scalar. A number for a limit, a boolean for a
  -- capability, a string for a tier.
  --
  -- jsonb rather than three nullable typed columns, because the alternative is
  -- a table where two of the three are always null and nothing stops a row
  -- setting both. The resolver coerces and refuses a value of the wrong shape
  -- for the entitlement it names, which is the check that actually matters and
  -- which no column type could make for a catalog defined in code.
  value         jsonb NOT NULL,

  -- Why. Not optional, and not defaulted to an empty string.
  --
  -- An override with no reason is an override nobody can review, and the
  -- review is the entire control: there is no approval step here, no second
  -- pair of eyes, nothing between an operator and a customer's capacity except
  -- a row that says what they did and why. A blank reason makes that row
  -- worthless six months later, which is exactly when somebody asks why this
  -- customer has four times the environments they pay for.
  reason        text NOT NULL,
  -- Where the conversation happened, when there was one.
  ticket        text,

  created_by_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  -- How to name the grantor a year from now, when the user row may be gone.
  -- The same reason audit_entries carries actor_label beside actor_user_id.
  created_by_label   text NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),

  -- When it stops. Null is forever and is a deliberate, typed choice; see the
  -- header. The resolver compares against this on every read rather than
  -- relying on a sweeper, because an expiry that only takes effect when a cron
  -- job runs is an expiry that does not take effect during the outage that
  -- stopped the cron job.
  expires_at    timestamptz,

  -- Withdrawn early. Set rather than deleted: which grant was in force when a
  -- run was admitted is a question that outlives the grant.
  revoked_at        timestamptz,
  revoked_by_label  text,
  revoked_reason    text,

  CONSTRAINT entitlement_overrides_scope
    CHECK (scope IN ('global', 'organization', 'project', 'user')),
  -- Global has no subject and everything else has one. Without this a row with
  -- scope 'organization' and no scope_id would resolve for every organization,
  -- which is a global grant somebody made by leaving a field empty.
  CONSTRAINT entitlement_overrides_scope_id
    CHECK ((scope = 'global') = (scope_id IS NULL)),
  -- The same rule for the tenant column. A global override belongs to nobody;
  -- every other kind belongs to exactly one organization, and a null here
  -- would make the row invisible to the tenant read policy and therefore
  -- unenforceable for the tenant it was granted to.
  CONSTRAINT entitlement_overrides_org
    CHECK ((scope = 'global') = (org_id IS NULL)),
  -- Revocation is three facts or none. A revoked_at with no reason is the same
  -- problem as a grant with no reason, one step later.
  CONSTRAINT entitlement_overrides_revocation
    CHECK (num_nonnulls(revoked_at, revoked_by_label, revoked_reason) IN (0, 3)),
  -- An expiry before the grant is a typo, and one that would silently produce a
  -- grant that never applied while looking like it did.
  CONSTRAINT entitlement_overrides_expiry
    CHECK (expires_at IS NULL OR expires_at > created_at)
);

-- One live override per subject per entitlement.
--
-- Partial on revoked_at rather than on "not expired", because an index
-- predicate has to be immutable and `now()` is not. That is not a compromise:
-- replacing an override is REVOKE then GRANT, both recorded, rather than an
-- insert that quietly shadows a row still sitting there. An operator who wants
-- to change a grant has to say what happened to the old one.
--
-- coalesce rather than a nullable column in the key, because in Postgres two
-- rows with a NULL scope_id do not collide, so two conflicting global grants
-- for one entitlement would both be live and the resolver would have to pick.
CREATE UNIQUE INDEX entitlement_overrides_live_idx
  ON entitlement_overrides (
    scope,
    coalesce(scope_id, '00000000-0000-0000-0000-000000000000'::uuid),
    feature)
  WHERE revoked_at IS NULL;

-- What the resolver actually reads: everything that could apply to one
-- organization, in one index scan. Global rows have no org_id, so they are
-- fetched by the second index.
CREATE INDEX entitlement_overrides_org_idx
  ON entitlement_overrides (org_id, feature) WHERE revoked_at IS NULL;
CREATE INDEX entitlement_overrides_global_idx
  ON entitlement_overrides (feature) WHERE revoked_at IS NULL AND scope = 'global';

-- ---------------------------------------------------------------------------
-- Flags
-- ---------------------------------------------------------------------------

CREATE TABLE feature_flags (
  key           text PRIMARY KEY,
  description   text NOT NULL,

  -- off, on, or targeted.
  --
  -- Three states rather than a boolean plus a target list, because the state
  -- an incident needs is "off for everybody, right now, regardless of what the
  -- targets say", and expressing that as "delete all the targets" loses the
  -- configuration you will want back in twenty minutes. `off` is the kill
  -- switch: it is checked before targeting, before the rollout, before
  -- anything, and it cannot be overridden by a target row.
  state         text NOT NULL DEFAULT 'off',

  -- The share of subjects a `targeted` flag is on for, beyond its explicit
  -- allow targets. Hashed per subject and per flag key, so 10% is the same 10%
  -- on every replica and every request, and two flags at 10% do not select the
  -- same tenth of the customers.
  rollout_percent integer NOT NULL DEFAULT 0,

  -- Whether members of the operator's own organizations get it regardless of
  -- the rollout. This is a column rather than a target row because "internal
  -- users" is not a value somebody types, it is a property of the subject, and
  -- a target row whose value nobody can enumerate is a row nobody can audit.
  internal_only boolean NOT NULL DEFAULT false,

  -- The kill, recorded apart from an ordinary edit.
  --
  -- Turning a flag off during an incident and turning it off because the
  -- experiment ended are the same UPDATE and completely different events, and
  -- the one worth finding later is the first. These three columns are what
  -- make the incident timeline reconstructable from the database rather than
  -- from somebody's memory of which afternoon it was.
  killed_at        timestamptz,
  killed_by_label  text,
  killed_reason    text,

  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  updated_by_label text NOT NULL,

  CONSTRAINT feature_flags_state CHECK (state IN ('off', 'on', 'targeted')),
  CONSTRAINT feature_flags_rollout CHECK (rollout_percent BETWEEN 0 AND 100),
  CONSTRAINT feature_flags_kill CHECK (num_nonnulls(killed_at, killed_by_label, killed_reason) IN (0, 3)),
  -- A key that reads as a key. Lowercase, dotted or dashed, because these
  -- appear in code as string literals and a flag whose key has a space in it
  -- is a flag somebody will mistype exactly once, in production, silently.
  CONSTRAINT feature_flags_key CHECK (key ~ '^[a-z][a-z0-9]*([._-][a-z0-9]+)*$')
);

CREATE TABLE feature_flag_targets (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  flag_key    text NOT NULL REFERENCES feature_flags(key) ON DELETE CASCADE,

  -- What the value names.
  --
  -- `project` and `repository` are BOTH here and they are not duplicates.
  -- `project` is a repositories.id, which exists only once a repository has
  -- been registered. `repository` is a full_name, optionally with a trailing
  -- `/*`, which matches before registration and matches a whole owner at once.
  -- A rollout that could only name registered repositories could not be turned
  -- on for a customer's next repository, which is the one they are about to
  -- create.
  kind        text NOT NULL,
  value       text NOT NULL,

  -- Deny beats allow, and this column is why the kill switch is not the only
  -- lever. Taking ONE customer out of a rollout that is working for everybody
  -- else is the common case during an incident, and doing it by turning the
  -- whole flag off punishes everybody for one tenant's problem.
  allow       boolean NOT NULL DEFAULT true,

  -- The organization this target concerns, when it concerns one, so that one
  -- tenant cannot enumerate which other tenants are in a private beta. Null
  -- for the kinds that name no tenant: plan and environment.
  org_id      uuid REFERENCES organizations(id) ON DELETE CASCADE,

  reason           text NOT NULL,
  created_by_label text NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT feature_flag_targets_kind
    CHECK (kind IN ('user', 'organization', 'project', 'repository', 'plan', 'environment')),
  CONSTRAINT feature_flag_targets_value CHECK (value <> ''),
  -- The kinds that name a tenant must carry one, and the two that do not must
  -- not. Without this, a target for plan 'team' carrying somebody's org_id
  -- would be invisible to every other tenant's evaluation and the flag would
  -- be on for one customer and off for the rest of their plan.
  CONSTRAINT feature_flag_targets_org
    CHECK ((kind IN ('plan', 'environment')) = (org_id IS NULL))
);

CREATE UNIQUE INDEX feature_flag_targets_unique_idx
  ON feature_flag_targets (flag_key, kind, value);
CREATE INDEX feature_flag_targets_flag_idx ON feature_flag_targets (flag_key);

-- ---------------------------------------------------------------------------
-- The ledger
-- ---------------------------------------------------------------------------

CREATE TABLE admin_operations (
  -- The idempotency key IS the primary key. See the header: the second attempt
  -- to claim it has to fail loudly at the database rather than succeed and
  -- issue a second refund.
  idempotency_key   text PRIMARY KEY,

  action            text NOT NULL,
  org_id            uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  target_type       text NOT NULL,
  -- The provider's identifier for what is being acted on: a charge, an
  -- invoice, a subscription. Text rather than uuid: these are Stripe's.
  target_id         text,

  -- The OPERATOR, not a customer's user.
  --
  -- admin_users(id), which is a different id space from users(id). The foreign
  -- key is what makes confusing the two RAISE rather than quietly attributing a
  -- refund to whichever customer happened to share the uuid, and getting the
  -- attribution of a money action wrong is worse than failing to record it.
  --
  -- SET NULL rather than CASCADE: an operator leaving must not delete the
  -- record of what they did, and actor_label below is the half that survives.
  admin_user_id     uuid REFERENCES admin_users(id) ON DELETE SET NULL,
  actor_label       text NOT NULL,
  reason            text NOT NULL,

  -- Exactly what was asked for, and its fingerprint.
  --
  -- The fingerprint is compared on every replay. A key reused with different
  -- parameters is refused rather than answered with the first attempt's
  -- result, because answering would report success for a refund that never
  -- happened. This is Stripe's own rule and it is reproduced here so that the
  -- refusal happens before the network call rather than after it.
  request           jsonb NOT NULL,
  request_fingerprint text NOT NULL,

  state             text NOT NULL DEFAULT 'in_flight',

  -- What was true before, and what was true after. Both, because a money
  -- action reviewed a month later is unreadable without them: "changed the
  -- plan" says nothing, "team -> enterprise, seats 10 -> 40" is a fact.
  before_state      jsonb,
  after_state       jsonb,

  -- What Stripe made, so a person can open it in the dashboard. Held apart
  -- from after_state because it is the one field an operator searches by.
  provider_object_id text,

  -- The money, in minor units, beside its currency.
  --
  -- Denormalised out of after_state on purpose. "How much did we refund last
  -- month" is the first question anybody asks of this table, and a column
  -- answers it with a SUM while a jsonb path answers it with a full scan and
  -- an assumption about which key the amount was under for that action.
  --
  -- The currency is NOT NULL when the amount is, and that pairing is a
  -- constraint below rather than a convention: an amount with no currency is a
  -- number that will eventually be added to a different one.
  amount_minor      bigint,
  currency          text,

  error_code        text,
  error_message     text,
  -- Whether the PROVIDER answered, or whether nothing came back at all.
  --
  -- The single most consequential boolean in this table. A provider that
  -- refused made a decision, so the thing definitively did not happen and a
  -- later deliberate retry is a new attempt that may carry its own key. A
  -- request that got no answer may have been executed with the response lost,
  -- so its only safe retry is one carrying the SAME key, letting the provider
  -- say what it already did.
  --
  -- Reuse the key in the first case and an operator can never retry a declined
  -- payment, because the provider replays its own refusal forever. Mint a new
  -- key in the second and the customer is charged twice. There is no default
  -- that is right for both, which is why this is a column rather than an
  -- inference from error_code.
  error_answered    boolean,

  started_at        timestamptz NOT NULL DEFAULT now(),
  finished_at       timestamptz,

  CONSTRAINT admin_operations_state CHECK (state IN ('in_flight', 'succeeded', 'failed')),
  CONSTRAINT admin_operations_money
    CHECK (num_nonnulls(amount_minor, currency) <> 1),
  -- A settled operation has a settling time; an in-flight one does not.
  CONSTRAINT admin_operations_finished
    CHECK ((state = 'in_flight') = (finished_at IS NULL)),
  CONSTRAINT admin_operations_reason CHECK (reason <> '')
);

CREATE INDEX admin_operations_org_idx ON admin_operations (org_id, started_at DESC);
CREATE INDEX admin_operations_action_idx ON admin_operations (action, started_at DESC);
-- Finding the operation that made a Stripe object, from the Stripe object.
CREATE INDEX admin_operations_object_idx ON admin_operations (provider_object_id)
  WHERE provider_object_id IS NOT NULL;
-- The sweep that finds operations that claimed a key and never settled, which
-- is what a crash between the claim and the answer leaves behind.
CREATE INDEX admin_operations_in_flight_idx ON admin_operations (started_at)
  WHERE state = 'in_flight';

-- ---------------------------------------------------------------------------
-- Grants
--
-- Exactly the verbs used, per table, the rule 0002 states. One role, because
-- 0029 decided that; see the note above for why a grant that looks wide is not.
--
-- Nothing gets DELETE. An override is revoked, a flag is killed, an operation
-- is settled. "Who granted this, and when did it stop" is a question that has
-- to survive somebody wanting the answer to go away, and withholding DELETE is
-- what makes removing an answer take a schema change and a conversation.
-- ---------------------------------------------------------------------------

-- The tenant-facing role reads and never writes.
GRANT SELECT ON
  entitlement_overrides, feature_flags, feature_flag_targets TO antifailure_app;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON
  entitlement_overrides, feature_flags, feature_flag_targets FROM antifailure_app;
-- Nothing at all on the ledger. It records what an operator did across tenants;
-- a customer's view of what was done to them is their own audit log, which is
-- where these actions are also written.
REVOKE ALL ON admin_operations FROM antifailure_app;

-- The operator writes all four and deletes none of them.
--
-- These GRANTs are not redundant with BYPASSRLS and the distinction is the one
-- 0030's own comment makes: BYPASSRLS exempts a role from row level security
-- and gives it no table privileges whatsoever, so a connection as
-- `antifailure_admin` with no grant here gets 42501 on every statement. The
-- policies are what a tenant is stopped by; these are what the operator is
-- allowed by, and both have to be right.
GRANT SELECT, INSERT, UPDATE ON
  entitlement_overrides, feature_flags, feature_flag_targets, admin_operations
TO antifailure_admin;
REVOKE DELETE, TRUNCATE ON
  entitlement_overrides, feature_flags, feature_flag_targets, admin_operations
FROM antifailure_admin;

-- The billing tables an operator has to READ, for the same reason: the policy
-- half is moot under BYPASSRLS and the privilege half is not there yet.
--
-- 0030 grants the operator `admin_users`, `admin_sessions`,
-- `admin_audit_entries` and `audit_entries` and stops. These six are the tables
-- this lane reads, so this file grants them, and the extension is visible in
-- the diff of the feature that needed it rather than buried in the boundary.
--
-- SELECT only, and that is not a formality. Moving a plan goes through Stripe
-- and comes back as a webhook; an operator with UPDATE on `subscriptions` could
-- make this database disagree with the provider about what somebody is paying
-- for, and the provider would be right.
GRANT SELECT ON billing_customers, subscriptions, invoices, payment_methods,
  billing_events, golden_versions,
  -- `organizations` too, and it is not a billing table.
  --
  -- Every operator route in this lane opens by reading the tenant's slug and
  -- plan: the slug because `subject_org_label` is what still names the customer
  -- once the row is gone, and the plan because an entitlement means nothing
  -- without the plan it is an override of. Without this grant the first
  -- statement of every one of them is 42501.
  --
  -- Only this one, and only SELECT. The rest of the tenant tables an operator
  -- portal needs, users and members and repositories and the rest, belong to
  -- the lanes whose screens read them; granting them here would put the
  -- privilege list in the file least likely to be read when somebody asks what
  -- an operator can see.
  organizations
TO antifailure_admin;

-- ---------------------------------------------------------------------------
-- Isolation
-- ---------------------------------------------------------------------------

DO $$
DECLARE
  t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'entitlement_overrides', 'feature_flags', 'feature_flag_targets', 'admin_operations']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    -- FORCE so the policies apply to the owner too, for the operator who runs
    -- a migration and leaves a connection open. Same reason as 0020.
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
  END LOOP;
END
$$;

-- A tenant reads the overrides that apply to it, and the global ones, because
-- both change what it is allowed to do and a customer is entitled to know why
-- their limit is what it is. It reads no other tenant's.
CREATE POLICY tenant_reads_own_overrides ON entitlement_overrides
  FOR SELECT TO antifailure_app
  USING (scope = 'global' OR org_id = current_org());

-- The flag itself is readable, because evaluation happens on the request path
-- inside a tenant transaction and it needs the state and the rollout. A flag
-- key names an unreleased feature and that is a real, small disclosure; it is
-- accepted deliberately rather than by omission, because the alternative is a
-- security definer function around every evaluation and the console has to
-- render "why is this on for me" anyway.
CREATE POLICY tenant_reads_flags ON feature_flags
  FOR SELECT TO antifailure_app
  USING (true);

-- The TARGETS are where the disclosure would actually matter, and they are
-- scoped. Without this, any customer could list which other customers are in a
-- private beta by reading the target rows, which is a customer list.
CREATE POLICY tenant_reads_own_targets ON feature_flag_targets
  FOR SELECT TO antifailure_app
  USING (org_id IS NULL OR org_id = current_org());

-- The ledger: nothing, said out loud.
--
-- The tenant-facing role already holds no privilege on this table, so this
-- policy changes no behaviour today and is not decoration. It is the second
-- lock. A table with row level security enabled and NO policy denies everything
-- for the same reason, and denies it invisibly: the day somebody adds a GRANT
-- here for a reporting screen, the absence of a policy silently becomes an open
-- door, whereas this refuses and makes them come and read the sentence.
--
-- It also answers the tenancy suite, which treats a table carrying org_id with
-- RLS on and no policy as a mistake. It is usually right about that. Here the
-- honest statement is not "no policy" but "no rows, deliberately".
CREATE POLICY tenant_sees_nothing ON admin_operations
  FOR ALL TO antifailure_app
  USING (false) WITH CHECK (false);

-- Nothing here for the operator, and that is the change 0030 made necessary.
--
-- An earlier draft of this file gave `antifailure_app` INSERT and UPDATE on
-- these four tables behind a policy keyed on current_admin_user(). 0030 chose a
-- different and better boundary: the operator connects as `antifailure_admin`,
-- a separate credential holding BYPASSRLS, so policies do not apply to it at
-- all. That leaves the old design as a write path on the tenant-facing role
-- that nothing would ever use, and a grant nobody needs is a grant somebody
-- will eventually reach through. It is gone.
--
-- What remains for `antifailure_app` is SELECT with the tenant policies above:
-- a customer can see the overrides that apply to it and the flags, and can
-- write none of it and reach the ledger not at all.

COMMIT;
