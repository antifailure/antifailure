-- Taking money, and the isolation that keeps one customer's money private.
--
-- `organizations.plan` already exists and `checkQuota` already enforces it.
-- What was missing was anything able to CHANGE it, which is why the plan was
-- permanently whatever the seed set. These tables are what a payment provider's
-- answers land in, and the policies below are the part worth reading twice:
-- these rows say what a company pays and when it last failed to, and a
-- cross-tenant read here is a different kind of incident from a leaked
-- environment name.
--
-- ---------------------------------------------------------------------------
-- The awkward part, which decides most of this file
-- ---------------------------------------------------------------------------
--
-- A Stripe webhook has no tenant. It arrives at an unauthenticated endpoint
-- from a machine that has never heard of an organization, and it names a Stripe
-- customer. So it cannot use withTenant, and withoutTenant would leave it able
-- to reach every row in the database.
--
-- This is the same problem 0013 solved for GitHub deliveries and it takes the
-- same shape: the application verifies the signature over the raw body FIRST,
-- and then declares, in `antifailure.stripe_customer`, the customer that
-- verified payload named. The policies key on that. A bug in a handler, a
-- mixed-up variable, a loop that reuses the wrong identifier, writes a row for
-- the customer the delivery was about or writes nothing.
--
-- And the same honest caveat as 0013: the primary control is the signature
-- check in src/billing/webhook.ts, because what the caller declares here is a
-- name rather than a secret it could only hold by having been given it. This is
-- defence in depth. Written down because a reader who assumed otherwise would
-- draw the wrong conclusion about what a leaked connection could do.
--
-- The delivery cannot pick its own organization. Every write policy ties org_id
-- to the organization that already owns the declared customer, so a delivery
-- naming customer A cannot write a subscription belonging to organization B
-- even though it is free to say org_id is anything it likes.
--
-- ---------------------------------------------------------------------------
-- Metering, decided before the schema rather than after it
-- ---------------------------------------------------------------------------
--
-- There is no usage table here and that is a decision, not an omission.
--
-- The engine runs in the customer's own CI, so the control plane cannot observe
-- an environment-hour. It sees the events an engine chooses to send, and an
-- engine that was offline sends its backlog whenever it likes. Billing on a
-- number the payer's own CI can withhold or delay is a billing system that
-- undercharges silently and cannot be audited, and we do not pay for that
-- compute in the first place, so there is no cost to pass through. What is
-- hosted is a control plane whose cost is roughly per organization.
--
-- So: per-seat, with the capacity limits PLAN_QUOTAS already enforces. The
-- subscription's `quantity` is the seat count and is the only number a price
-- multiplies.
--
-- What makes that reversible rather than a corner painted into: every quantity
-- a usage price would bill for is already recorded, per organization, with a
-- timestamp. `events` carries environment.ready, environment.sleeping and
-- environment.torn_down with occurred_at, partitioned by month;
-- golden_versions carries one row per refresh with created_at and size_bytes.
-- A usage rollup is a query over tables that already exist, not a capture path
-- that has to be built and then backfilled from nothing. The subscription rows
-- below are period-scoped for the same reason: a usage record has a period to
-- hang off the day one is wanted.

BEGIN;

-- The Stripe customer for a delivery whose signature has been checked.
--
-- Not hashed and not a secret: it is an opaque provider identifier that appears
-- in the payload of every event, and the thing that makes it believable is the
-- HMAC one layer up.
CREATE OR REPLACE FUNCTION current_stripe_customer() RETURNS text
  LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('antifailure.stripe_customer', true), '') $$;

-- ---------------------------------------------------------------------------
-- The customer
-- ---------------------------------------------------------------------------

-- One Stripe customer per organization, keyed by the organization so that two
-- customers for one organization is a constraint violation rather than a
-- support ticket about a double charge.
CREATE TABLE billing_customers (
  org_id              uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
  stripe_customer_id  text NOT NULL UNIQUE,
  -- The address the receipts go to. Held here rather than read from the
  -- organization's members, because billing email and sign-in email are
  -- different things and finance departments insist on the difference.
  email               text,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Payment method metadata
-- ---------------------------------------------------------------------------

-- Metadata, never a card. The brand, the last four digits and the expiry are
-- what a person needs to recognise which card is on file, and they are the only
-- parts of a payment method this system is allowed to hold: everything else
-- lives at Stripe, and reaching for it would put this database in scope for PCI
-- DSS, which is a compliance regime nobody here wants to be in.
--
-- A table of its own rather than four columns on billing_customers, and the
-- reason is the policy rather than the shape. A verified delivery has to WRITE
-- this, because the card is changed on Stripe's hosted portal and
-- payment_method.attached is the only place this system learns about it. Giving
-- the delivery UPDATE on billing_customers would give it write access to the
-- column that says which organization a customer belongs to, and org_id there
-- is the primary key: a delivery could move a customer to an organization that
-- has no billing row yet. Here org_id is tied to the customer's own
-- organization by the same WITH CHECK the subscriptions carry, so there is
-- nothing to move.
CREATE TABLE payment_methods (
  id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                    uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  stripe_payment_method_id  text NOT NULL UNIQUE,
  stripe_customer_id        text NOT NULL,
  kind                      text NOT NULL DEFAULT 'card',
  brand                     text,
  last4                     text,
  exp_month                 integer,
  exp_year                  integer,
  -- Set rather than deleted. Which card paid for what is a question that
  -- outlives the card, and the row is small.
  detached_at               timestamptz,
  last_event_at             timestamptz NOT NULL,
  created_at                timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT payment_methods_last4 CHECK (last4 IS NULL OR last4 ~ '^[0-9]{4}$'),
  CONSTRAINT payment_methods_exp_month CHECK (exp_month IS NULL OR (exp_month BETWEEN 1 AND 12))
);

CREATE INDEX payment_methods_customer_idx ON payment_methods (stripe_customer_id);

-- ---------------------------------------------------------------------------
-- The subscription
-- ---------------------------------------------------------------------------

CREATE TABLE subscriptions (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  stripe_subscription_id  text NOT NULL UNIQUE,
  -- Carried on the row as well as reachable through billing_customers, because
  -- the webhook policies key on it and a policy that had to join to find its
  -- own key would be a policy nobody could read.
  stripe_customer_id      text NOT NULL,
  -- The plan this subscription entitles, which is what moves
  -- organizations.plan. Constrained to the plans PLAN_QUOTAS knows: a
  -- subscription for a plan the quota table has never heard of would silently
  -- fall back to free limits on a paying customer.
  plan                    text NOT NULL,
  price_id                text,
  -- Seats. The only number a price multiplies; see the metering note above.
  quantity                integer NOT NULL DEFAULT 1,
  -- Stripe's own vocabulary, stored as sent, and deliberately NOT constrained
  -- to a list.
  --
  -- A status this system has not heard of has to be storable. A CHECK here
  -- turns the day Stripe adds one into a failed INSERT on the one path that
  -- must never drop a message, and the webhook then 500s, retries, and 500s
  -- again until somebody deploys. src/billing/plans.ts decides what a status
  -- ENTITLES, and it answers "leave the plan alone" for one it does not
  -- recognise, which is the direction that neither charges nor punishes anybody
  -- over a word nobody has read.
  status                  text NOT NULL,
  current_period_start    timestamptz,
  current_period_end      timestamptz,
  cancel_at_period_end    boolean NOT NULL DEFAULT false,
  canceled_at             timestamptz,
  -- The watermark that makes an out-of-order delivery harmless.
  --
  -- Stripe does not promise ordering, and it retries. An `updated` event
  -- created at 12:00 arriving after a `deleted` created at 12:01 would
  -- otherwise resurrect a cancelled subscription and hand back a paid plan to
  -- somebody who stopped paying. Every write compares the event's own created
  -- time against this and refuses to go backwards.
  last_event_at           timestamptz NOT NULL,
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT subscriptions_plan CHECK (plan IN ('free', 'team', 'enterprise')),
  CONSTRAINT subscriptions_quantity CHECK (quantity >= 0)
);

-- There is deliberately NO unique index saying one live subscription per
-- organization, and this is the decision most likely to be questioned.
--
-- It would be the obvious guard against a double charge, and it would be wrong
-- here, because the charge does not happen in this database. Stripe is where a
-- customer's subscriptions exist, and a constraint that refuses to record a
-- fact Stripe has already acted on does not prevent the charge, it loses the
-- event: the delivery raises, answers 500, and Stripe retries into the same
-- refusal until somebody deploys. That is worse than the state it is refusing.
-- It would also fire on a legitimate ordering, an organization resubscribing
-- before the cancellation of the old subscription has been delivered.
--
-- So a double subscription is prevented where it can actually be prevented, in
-- the route that starts a checkout: subscriptions.checkout refuses while a live
-- one exists. And it is made visible rather than silent: subscriptions.current
-- returns how many are live, so a second one is something a person can see
-- instead of a constraint violation in a log nobody reads.
CREATE INDEX subscriptions_org_created_idx ON subscriptions (org_id, created_at DESC);
CREATE INDEX subscriptions_customer_idx ON subscriptions (stripe_customer_id);

-- ---------------------------------------------------------------------------
-- Invoices
-- ---------------------------------------------------------------------------

-- An index of what was billed, not a copy of the invoice. The rendered document
-- lives at Stripe and hosted_invoice_url points at it; reproducing it here
-- would mean a second thing to keep correct and a second thing to be wrong
-- about somebody's money.
CREATE TABLE invoices (
  id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  stripe_invoice_id       text NOT NULL UNIQUE,
  stripe_customer_id      text NOT NULL,
  stripe_subscription_id  text,
  -- What the customer's accounts payable department quotes back.
  number                  text,
  status                  text NOT NULL,
  -- Minor units, as an integer, because that is what the provider sends and
  -- what money is. A float would round a cent away somewhere nobody looks.
  amount_due              bigint NOT NULL DEFAULT 0,
  amount_paid             bigint NOT NULL DEFAULT 0,
  currency                text NOT NULL DEFAULT 'usd',
  hosted_invoice_url      text,
  period_start            timestamptz,
  period_end              timestamptz,
  paid_at                 timestamptz,
  -- The same watermark, for the same reason: paid and payment_failed for one
  -- invoice can arrive in either order.
  last_event_at           timestamptz NOT NULL,
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),

  -- Unconstrained for the same reason the subscription's is; see above.
  CONSTRAINT invoices_amounts CHECK (amount_due >= 0 AND amount_paid >= 0)
);

CREATE INDEX invoices_org_created_idx ON invoices (org_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- The delivery ledger
-- ---------------------------------------------------------------------------

-- Every verified delivery, once.
--
-- Three jobs, and each of them is a bug this repository has already shipped
-- once in another shape:
--
-- IDEMPOTENCY. The primary key is the provider's event id, so a retry inserts
-- nothing and the handler does nothing twice. A billing webhook applied twice
-- is a plan granted twice or a seat count doubled.
--
-- THE EVENT THAT ARRIVED FIRST. A delivery can be about a customer this system
-- has no organization for yet, because a second path created the row later.
-- That is exactly how a referral payout webhook once fired into nothing here.
-- So org_id is nullable, the row is recorded anyway with outcome `unresolved`,
-- and attaching the customer replays it. An event has to be recoverable from
-- what was stored, so the verified payload is stored with it: re-asking Stripe
-- is not always possible, and it is never possible for an event about an object
-- that has since changed.
--
-- THE EVENT THAT NEVER ARRIVES. Nothing here fixes that, and nothing can. The
-- fix is reconciliation, which reads the subscription back from Stripe and
-- writes what Stripe says; see src/billing/reconcile.ts. This table is what
-- tells reconciliation whether it is filling a gap or repeating work.
CREATE TABLE billing_events (
  stripe_event_id     text PRIMARY KEY,
  -- Null until the customer is attached to an organization. Deliberately not
  -- defaulted to anything: a wrong organization on a billing event is worse
  -- than no organization.
  org_id              uuid REFERENCES organizations(id) ON DELETE CASCADE,
  stripe_customer_id  text NOT NULL,
  type                text NOT NULL,
  -- The provider's own created time, which is what the watermarks compare
  -- against. Not received_at: two deliveries can be received in the order they
  -- were not created in, and that is the whole problem.
  event_created_at    timestamptz NOT NULL,
  received_at         timestamptz NOT NULL DEFAULT now(),
  processed_at        timestamptz,
  outcome             text NOT NULL DEFAULT 'unresolved',
  payload             jsonb NOT NULL DEFAULT '{}'::jsonb,

  CONSTRAINT billing_events_outcome CHECK (outcome IN ('applied', 'ignored', 'stale', 'unresolved'))
);

CREATE INDEX billing_events_unresolved_idx ON billing_events (stripe_customer_id, event_created_at)
  WHERE outcome = 'unresolved';

-- ---------------------------------------------------------------------------
-- Grants
--
-- Exactly the verbs the application uses, per table, the same rule 0002 states.
-- Nothing deletes a billing row: a cancelled subscription is a row whose status
-- is canceled, and an invoice that was issued stays issued. DELETE is withheld
-- rather than granted and unused, so that removing somebody's billing history
-- takes a schema change and a conversation.
-- ---------------------------------------------------------------------------

GRANT SELECT, INSERT, UPDATE ON
  billing_customers, payment_methods, subscriptions, invoices, billing_events
TO antifailure_app;

REVOKE DELETE, TRUNCATE ON
  billing_customers, payment_methods, subscriptions, invoices, billing_events
  FROM antifailure_app;

-- ---------------------------------------------------------------------------
-- Isolation
-- ---------------------------------------------------------------------------

DO $$
DECLARE
  t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'billing_customers', 'payment_methods', 'subscriptions', 'invoices', 'billing_events']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    -- FORCE so the policies apply to the table's owner too, for the operator
    -- who runs a migration as the owner and leaves a connection open.
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
  END LOOP;
END
$$;

-- The three tables a tenant reads and writes through the API.
DO $$
DECLARE
  t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['billing_customers', 'payment_methods', 'subscriptions', 'invoices']
  LOOP
    EXECUTE format($p$
      CREATE POLICY tenant_isolation ON %I
        FOR ALL TO antifailure_app
        USING (org_id = current_org())
        WITH CHECK (org_id = current_org())
    $p$, t);
  END LOOP;
END
$$;

-- The ledger is readable by its tenant and written only by a verified delivery.
--
-- SELECT rather than ALL, deliberately. Nothing a person can reach writes this
-- table, and a tenant able to insert a row could pre-empt the primary key of an
-- event that has not arrived yet, which would make the real delivery look like
-- a retry and be dropped. Narrowing the verb is what stops that being possible
-- rather than merely unlikely.
CREATE POLICY tenant_isolation ON billing_events
  FOR SELECT TO antifailure_app
  USING (org_id = current_org());

-- What a verified delivery may reach.
--
-- SELECT only on the customer. A delivery finds the organization a customer
-- belongs to; it does not get to decide one. Attaching a customer to an
-- organization happens on a request with a session and a permission behind it.
CREATE POLICY stripe_delivery_reads_customer ON billing_customers
  FOR SELECT TO antifailure_app
  USING (stripe_customer_id = current_stripe_customer());

-- The subscription and the invoice are what a delivery is FOR, so it writes
-- them. The WITH CHECK is the part that matters: org_id is tied to the
-- organization that already owns the declared customer, so a delivery about
-- customer A cannot write a row belonging to organization B however wrong the
-- handler is. USING keys on the customer alone, because a row being corrected
-- may carry an org_id from a state this delivery is about to fix.
CREATE POLICY stripe_delivery_writes_subscription ON subscriptions
  FOR ALL TO antifailure_app
  USING (stripe_customer_id = current_stripe_customer())
  WITH CHECK (
    stripe_customer_id = current_stripe_customer()
    AND org_id = (SELECT c.org_id FROM billing_customers c
                  WHERE c.stripe_customer_id = current_stripe_customer()));

CREATE POLICY stripe_delivery_writes_invoice ON invoices
  FOR ALL TO antifailure_app
  USING (stripe_customer_id = current_stripe_customer())
  WITH CHECK (
    stripe_customer_id = current_stripe_customer()
    AND org_id = (SELECT c.org_id FROM billing_customers c
                  WHERE c.stripe_customer_id = current_stripe_customer()));

CREATE POLICY stripe_delivery_writes_payment_method ON payment_methods
  FOR ALL TO antifailure_app
  USING (stripe_customer_id = current_stripe_customer())
  WITH CHECK (
    stripe_customer_id = current_stripe_customer()
    AND org_id = (SELECT c.org_id FROM billing_customers c
                  WHERE c.stripe_customer_id = current_stripe_customer()));

-- The ledger accepts a row with no organization, which is the whole point of
-- it: the delivery that arrives before the customer is attached has to be
-- recorded somewhere, and refusing it is how an event fires into nothing.
CREATE POLICY stripe_delivery_records_event ON billing_events
  FOR ALL TO antifailure_app
  USING (stripe_customer_id = current_stripe_customer())
  WITH CHECK (
    stripe_customer_id = current_stripe_customer()
    AND (org_id IS NULL
         OR org_id = (SELECT c.org_id FROM billing_customers c
                      WHERE c.stripe_customer_id = current_stripe_customer())));

-- Moving the plan is what all of this is for.
--
-- SELECT and UPDATE, and no more: a delivery cannot create an organization and
-- cannot remove one, and it reaches exactly the organization that already owns
-- the customer it declared.
--
-- The SELECT half is not optional and it is not obvious. Postgres applies the
-- SELECT policies to an UPDATE whose statement READS the table, which any
-- UPDATE with a WHERE clause or a RETURNING does. With only the UPDATE policy
-- the statement matched no rows, raised nothing, and the plan silently never
-- moved: the entire feature was a no-op that every unit test passed.
--
-- Row-level security cannot restrict a policy to one COLUMN, so the UPDATE is
-- wider than the handler needs, which writes plan and updated_at and nothing
-- else. That is the same shape as github_delivery_writes_org in 0013, and it is
-- why the handler's UPDATE names its columns explicitly rather than taking a
-- row.
CREATE POLICY stripe_delivery_reads_org ON organizations
  FOR SELECT TO antifailure_app
  USING (id = (SELECT c.org_id FROM billing_customers c
               WHERE c.stripe_customer_id = current_stripe_customer()));

CREATE POLICY stripe_delivery_moves_plan ON organizations
  FOR UPDATE TO antifailure_app
  USING (id = (SELECT c.org_id FROM billing_customers c
               WHERE c.stripe_customer_id = current_stripe_customer()))
  WITH CHECK (id = (SELECT c.org_id FROM billing_customers c
                    WHERE c.stripe_customer_id = current_stripe_customer()));

-- Attaching a customer is what resolves the events that arrived first, and it
-- runs with a tenant rather than with a declared customer, so the tenant needs
-- to be able to reach its own unresolved rows. Scoped to the customer the
-- organization owns, so this cannot become a way to read another tenant's
-- unresolved events by guessing a customer identifier.
--
-- Two policies rather than one, and the SELECT half is not optional: an
-- unresolved row has no org_id, so tenant_isolation above cannot see it, and
-- the replay reads the rows before it applies them. Postgres also applies the
-- SELECT policies to the UPDATE itself, because the statement has a WHERE
-- clause. Without this the replay found nothing, raised nothing, and every
-- event that arrived before its organization stayed unresolved forever, which
-- is precisely the failure the ledger exists to prevent.
CREATE POLICY tenant_reads_own_unresolved_events ON billing_events
  FOR SELECT TO antifailure_app
  USING (
    org_id IS NULL
    AND stripe_customer_id IN (
      SELECT c.stripe_customer_id FROM billing_customers c WHERE c.org_id = current_org()));

CREATE POLICY tenant_resolves_own_events ON billing_events
  FOR UPDATE TO antifailure_app
  USING (
    org_id IS NULL
    AND stripe_customer_id IN (
      SELECT c.stripe_customer_id FROM billing_customers c WHERE c.org_id = current_org()))
  WITH CHECK (org_id = current_org());

COMMIT;
