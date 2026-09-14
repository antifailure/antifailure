-- One payable checkout per organization.
--
-- THE DEFECT. A checkout session creates no subscription, so while one is merely
-- open there is nothing in the subscriptions table to refuse a second one on,
-- and nothing at Stripe either: the organization genuinely has no subscription
-- yet. The only thing that tied two checkout requests together was a thirty
-- second idempotency bucket on the key sent to Stripe. Two billing owners who
-- pressed Subscribe a minute apart fell either side of that bucket, each got
-- their own hosted page, and a Stripe checkout session stays payable for twenty
-- four hours. Both pages could be paid. That is two subscriptions on one Stripe
-- customer and two charges on one card, with nothing downstream able to refuse
-- either of them.
--
-- WHY THIS TABLE AND NOT A UNIQUE INDEX ON subscriptions. Because the charge
-- does not happen in this database, and 0020_billing.sql already wrote the
-- argument out in full where the subscriptions table is defined. It is worth
-- repeating here, because this is the file somebody will reach for that index
-- in: a unique index saying one live subscription per organization would refuse
-- to RECORD a subscription Stripe has already created and already charged for.
-- The webhook carrying it would raise, answer 500, and be retried by Stripe into
-- the identical refusal until somebody deployed. That loses the delivery rather
-- than the charge, and the customer is charged twice either way, now with no
-- local record of the second one. It would also fire on a legitimate ordering:
-- an organization resubscribing before the cancellation of its old subscription
-- has been delivered. So subscriptions stays exactly as it is.
--
-- What this database can legitimately be the authority on is the ATTEMPT to buy,
-- because that begins here. One row per organization, keyed on the organization
-- so that a second concurrent claim is a primary key conflict rather than a
-- second hosted page, and claimed under the organization's own row lock in the
-- same order billing/webhook.ts takes its locks. The session identifier is
-- recorded so a retry can ask Stripe what became of that page rather than
-- opening another one.
--
-- WHAT IS NOT HERE. No checkout url, ever. The url is the bearer capability to
-- pay: anybody holding it can put a card into that page, and a row holding it
-- would put "can start paying as this organization" behind every read of this
-- table, including the backup drill's. The session identifier is not a
-- capability, and Stripe has to be asked with the account's own key before it
-- says anything about it.
--
-- No card, no amount, no price beyond the identifier of the price being bought,
-- so this table stays as far outside PCI scope as the rest of this schema.

BEGIN;

CREATE TABLE billing_checkout_attempts (
  -- The organization, as the primary key. This IS the invariant: one
  -- organization cannot hold two purchase attempts, and two requests that race
  -- to create one resolve to a single row rather than to two payable pages.
  org_id              uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
  -- The attempt, which is what Stripe is told. It becomes the idempotency key
  -- the session is created under and the marker written into that session's
  -- metadata, so a page whose creation response was lost can be found again by
  -- name instead of guessed at. It is replaced, never reused, when an attempt is
  -- retired: reusing it would send a returning buyer back to the expired page
  -- they walked away from, which is the defect a permanent per organization key
  -- would have introduced while fixing this one.
  attempt_id          uuid NOT NULL DEFAULT gen_random_uuid(),
  -- Who is being billed, and for what. Held rather than re-derived because a
  -- recovery has to reproduce the SAME request: Stripe refuses a repeated
  -- idempotency key whose parameters have changed, and a page opened from
  -- different parameters would sell the buyer something they did not ask for.
  stripe_customer_id  text NOT NULL,
  price_id            text NOT NULL,
  -- Where Stripe sends the browser afterwards. Part of the request, so part of
  -- what has to be reproduced exactly.
  success_url         text NOT NULL,
  cancel_url          text NOT NULL,
  -- The page this attempt opened, once Stripe has answered. Null between the
  -- claim and that answer, which is the window a lost response leaves behind and
  -- the reason billing/checkout.ts can ask Stripe for the customer's open
  -- sessions rather than trusting this column to be complete.
  stripe_session_id   text,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Isolation, the same shape as the rest of 0020's tables
-- ---------------------------------------------------------------------------

ALTER TABLE billing_checkout_attempts ENABLE ROW LEVEL SECURITY;
-- FORCE so the policy applies to the table's owner too, for the operator who
-- runs a migration as the owner and leaves a connection open.
ALTER TABLE billing_checkout_attempts FORCE ROW LEVEL SECURITY;

-- Read and written only through the API, by the organization it belongs to.
--
-- Deliberately NO policy for a Stripe delivery, unlike subscriptions and
-- invoices. A webhook is about a subscription or an invoice and has no business
-- with an attempt to buy: the attempt is retired by the next request, which asks
-- Stripe directly what became of the page. A delivery that could write here
-- would be a second writer for a row whose whole purpose is to be claimed once.
CREATE POLICY tenant_isolation ON billing_checkout_attempts
  FOR ALL TO antifailure_app
  USING (org_id = current_org())
  WITH CHECK (org_id = current_org());

GRANT SELECT, INSERT, UPDATE ON billing_checkout_attempts TO antifailure_app;
-- No DELETE and no TRUNCATE, the same as every other billing table. An attempt
-- is superseded in place, and the row goes when the organization does, through
-- the foreign key rather than through a statement the application can make.
REVOKE DELETE, TRUNCATE ON billing_checkout_attempts FROM antifailure_app;

-- The operator's read only credential, for the same reason it holds one on the
-- other billing tables: answering "what did this organization try to buy" during
-- an incident must not need the tenant's own connection.
GRANT SELECT ON billing_checkout_attempts TO antifailure_admin;

COMMIT;
