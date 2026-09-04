// What Stripe tells us, and what we are willing to believe.
//
// This is an unauthenticated endpoint that anybody on the internet can POST to,
// and what it decides is who has paid. Everything below rests on one check: the
// HMAC in `stripe-signature`, computed over the raw body with a secret only
// Stripe and this process hold. A request that fails it is refused before it is
// parsed, because parsing attacker-controlled JSON to decide whether to trust
// it has the order backwards, and because a billing webhook that trusts its
// payload is a way to give the product away.
//
// After that check the payload is trusted about ONE Stripe customer: the one it
// names. That is what the policies in 0020 key on, and it is why this file uses
// withStripeCustomer rather than opening the tables up.
//
// ---------------------------------------------------------------------------
// The orderings, which are the whole difficulty
// ---------------------------------------------------------------------------
//
// Stripe does not promise ordering and it retries. Every one of these has to be
// right, and the ones that are not obvious are the ones that have bitten this
// repository before:
//
// THE SAME EVENT TWICE. billing_events has the provider's event id as its
// primary key. The insert is the claim on the work, so a retry inserts nothing
// and returns early. A billing event applied twice is a plan granted twice or a
// seat count doubled.
//
// OUT OF ORDER. An `updated` created at 12:00 can arrive after a `deleted`
// created at 12:01. Every write compares the event's own created time against
// the row's last_event_at and refuses to go backwards, so the late one is
// recorded as stale and changes nothing. Without this a cancelled subscription
// gets resurrected and somebody who stopped paying keeps the product.
//
// BEFORE THE LOCAL ROW EXISTS. A delivery can name a customer this control
// plane has no organization for: created in the Stripe dashboard, or created by
// a checkout whose local write did not commit. This repository has already
// shipped that bug in another shape, where a payout webhook fired into nothing
// because a second signup path created the row later, and every test passed
// because every test used one ordering. So the event is RECORDED with no
// organization and replayed the moment the customer is attached; see
// resolvePending below, which attachCustomer calls on creation rather than
// waiting for an event that has already happened.
//
// NEVER AT ALL. Nothing here can fix a delivery that is not made. That is what
// src/billing/store.ts is for: it asks Stripe what it believes and writes
// that down, so a missed event costs a delay rather than a wrong plan.

import { createHmac, timingSafeEqual } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Db, Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import type { Analytics } from '../analytics/record.ts'
import { planName, subscriptionStatus } from '../analytics/normalize.ts'
import { DEFAULT_PLAN } from '../limits.ts'
import { ENTITLING_STATUSES, planForPrice, planForStatus, type StripeConfig } from './plans.ts'
import { invoiceOf, subscriptionOf, type StripeInvoice, type StripeSubscription } from './stripe.ts'

/** How far out of step a delivery's timestamp may be before it is refused.
 *
 *  Stripe's own recommendation, and the reason the signature covers the
 *  timestamp at all: without it a signature captured once is valid forever, and
 *  anybody who ever saw one could replay a paid invoice. */
export const SIGNATURE_TOLERANCE_SECONDS = 300

/** Why a delivery was refused. A value rather than a boolean so the endpoint
 *  can log which check failed without telling the caller, who would use it to
 *  iterate towards a valid signature. */
export type SignatureFailure =
  | 'no signature header'
  | 'the signature header is malformed'
  | 'the timestamp is outside the tolerance'
  | 'the signature does not match'

/**
 * Checks the `stripe-signature` header over the exact bytes that were received.
 *
 * The scheme is HMAC-SHA256 over "timestamp.body". A header carries one
 * timestamp and one or more v1 signatures, because Stripe sends two during a
 * secret rotation, and an implementation that read only the first would refuse
 * every delivery for the length of a rotation.
 */
export function verifyStripeSignature(
  secret: string,
  rawBody: string,
  header: string | undefined,
  now: Date,
  toleranceSeconds: number = SIGNATURE_TOLERANCE_SECONDS,
): SignatureFailure | null {
  if (!header) return 'no signature header'

  let timestamp = ''
  const signatures: string[] = []
  for (const part of header.split(',')) {
    const at = part.indexOf('=')
    if (at < 0) continue
    const key = part.slice(0, at).trim()
    const value = part.slice(at + 1).trim()
    if (key === 't') timestamp = value
    else if (key === 'v1') signatures.push(value)
  }
  if (!timestamp || signatures.length === 0) return 'the signature header is malformed'

  const seconds = Number(timestamp)
  if (!Number.isFinite(seconds)) return 'the signature header is malformed'
  // Both directions. A timestamp far in the FUTURE is as much a sign of a
  // forged header as one far in the past, and a check that only looked
  // backwards would accept a replay dated next year.
  if (Math.abs(Math.floor(now.getTime() / 1000) - seconds) > toleranceSeconds) {
    return 'the timestamp is outside the tolerance'
  }

  const expected = createHmac('sha256', secret).update(`${timestamp}.${rawBody}`, 'utf8').digest('hex')
  const want = Buffer.from(expected, 'utf8')
  for (const candidate of signatures) {
    const have = Buffer.from(candidate, 'utf8')
    // Length first, because timingSafeEqual throws on a mismatch and a throw
    // here would be a 500 for what is simply a wrong signature.
    if (have.length === want.length && timingSafeEqual(have, want)) return null
  }
  return 'the signature does not match'
}

/** One delivery, after its signature has been checked. */
export interface StripeEvent {
  id: string
  type: string
  /** Stripe's own created time. What every watermark compares against, because
   *  two deliveries can be received in the order they were not created in. */
  created: Date
  /** data.object, the thing the event is about. */
  object: Record<string, unknown>
}

/** Reads the envelope, or null when it is not one. Null rather than a throw:
 *  the endpoint answers 400 and Stripe stops, and a 500 would make it retry a
 *  body that will never parse. */
export function parseStripeEvent(raw: string): StripeEvent | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return null
  }
  if (parsed === null || typeof parsed !== 'object') return null
  const body = parsed as Record<string, unknown>

  const id = typeof body.id === 'string' ? body.id : ''
  const type = typeof body.type === 'string' ? body.type : ''
  const created = typeof body.created === 'number' ? body.created : Number(body.created)
  const data = body.data as { object?: unknown } | undefined
  const object = data?.object
  if (!id || !type || !Number.isFinite(created)) return null
  if (object === null || typeof object !== 'object' || Array.isArray(object)) return null

  return { id, type, created: new Date(created * 1000), object: object as Record<string, unknown> }
}

/** What one delivery did, for the response and for a log line. */
export interface StripeOutcome {
  event: string
  type: string
  handled: boolean
  /** Short and non-sensitive, for the delivery log at Stripe's end. */
  detail: string
}

/** Every event type this control plane acts on. Anything else is acknowledged
 *  and does nothing: answering 500 to an event that will never be handled
 *  produces a retry storm against an endpoint that refuses it identically every
 *  time. */
export const HANDLED_EVENTS: readonly string[] = [
  'customer.subscription.created',
  'customer.subscription.updated',
  'customer.subscription.deleted',
  'invoice.paid',
  'invoice.payment_failed',
  'invoice.finalized',
  'payment_method.attached',
  'payment_method.detached',
  // Recorded and deliberately not acted on. The entitlement comes from the
  // customer.subscription.* events, which Stripe sends for the same purchase,
  // and taking it from here as well would mean two writers for one fact racing
  // in an order Stripe does not promise. It is in the ledger because a
  // complete record of what was delivered is what makes a support question
  // answerable.
  'checkout.session.completed',
]

/**
 * Handles one verified delivery.
 *
 * Returns rather than throws for anything that is not our problem, so that
 * Stripe is told the delivery was received. It throws only when OUR side
 * failed, because that is the case where a retry helps.
 */
export async function handleStripeDelivery(
  pool: Pool,
  clock: Clock,
  config: StripeConfig,
  event: StripeEvent,
  analytics: Analytics,
): Promise<StripeOutcome> {
  if (!HANDLED_EVENTS.includes(event.type)) {
    // Acknowledged and not recorded. A Stripe account emits dozens of event
    // types and an endpoint may be subscribed to all of them; recording every
    // one would fill the ledger with rows nothing will ever read, and the
    // ledger is what makes a support question answerable.
    return {
      event: event.id, type: event.type, handled: false,
      detail: 'not an event this control plane acts on',
    }
  }

  const customerId = customerOf(event)
  if (!customerId) {
    // Nothing to scope the write by, so there is nowhere safe to put it. This
    // is not an error: account level events carry no customer and this control
    // plane acts on none of them.
    return {
      event: event.id,
      type: event.type,
      handled: false,
      detail: 'no customer in the payload, so there is no organization it could be about',
    }
  }

  return pool.withStripeCustomer(customerId, async (db) => {
    // The insert is the claim on the work. A retry inserts nothing, so the
    // handler below runs once for one event id however many times Stripe sends
    // it. Doing this first, before any decision, is what makes that true even
    // when two deliveries of one event arrive at two replicas at once: the
    // second one's insert waits on the first one's transaction and then finds
    // the row.
    const claimed = await db.execute<{ stripe_event_id: string }>(sql`
      INSERT INTO billing_events (
        stripe_event_id, stripe_customer_id, type, event_created_at, received_at, payload)
      VALUES (${event.id}, ${customerId}, ${event.type},
              ${event.created.toISOString()}, ${clock.now().toISOString()},
              ${JSON.stringify(event.object)}::jsonb)
      ON CONFLICT (stripe_event_id) DO NOTHING
      RETURNING stripe_event_id`)
    if (claimed.length === 0) {
      return {
        event: event.id, type: event.type, handled: true,
        detail: 'already handled; a repeat delivery of one event changes nothing',
      }
    }

    const owner = await db.execute<{ org_id: string }>(sql`
      SELECT org_id FROM billing_customers WHERE stripe_customer_id = ${customerId}`)
    const orgId = owner[0]?.org_id
    if (!orgId) {
      // Left unresolved rather than dropped. This is the ordering that has cost
      // this repository real money before.
      return {
        event: event.id, type: event.type, handled: true,
        detail: 'recorded; no organization holds this customer yet, so it will be applied when one does',
      }
    }

    const applied = await apply(db, clock, config, orgId, event, analytics)
    await db.execute(sql`
      UPDATE billing_events
      SET org_id = ${orgId}::uuid, outcome = ${applied.outcome},
          processed_at = ${clock.now().toISOString()}
      WHERE stripe_event_id = ${event.id}`)
    return { event: event.id, type: event.type, handled: true, detail: applied.detail }
  })
}

/**
 * Applies the events recorded before an organization held this customer.
 *
 * Called by attachCustomer, on creation, rather than left for a sweep. A row
 * that appears after the event that was waiting for it has to resolve itself:
 * waiting for a trigger that already fired is how a thing sits pending until
 * somebody notices.
 *
 * Runs inside the caller's tenant transaction, so it commits with the customer
 * row. Half of this happening would leave a customer attached and its events
 * still unresolved, which looks identical to the bug it exists to prevent.
 */
export async function resolvePending(
  db: Db,
  clock: Clock,
  config: StripeConfig,
  orgId: string,
  customerId: string,
  analytics: Analytics,
): Promise<{ resolved: number; details: string[] }> {
  const pending = await db.execute<{
    stripe_event_id: string
    type: string
    event_created_at: Date | string
    payload: Record<string, unknown>
  }>(sql`
    SELECT stripe_event_id, type, event_created_at, payload
    FROM billing_events
    WHERE stripe_customer_id = ${customerId} AND outcome = 'unresolved'
    -- In the order Stripe created them, not the order they arrived, so the
    -- watermarks land the same way they would have if nothing had been late.
    ORDER BY event_created_at ASC`)

  const details: string[] = []
  for (const row of pending) {
    const event: StripeEvent = {
      id: row.stripe_event_id,
      type: row.type,
      created: row.event_created_at instanceof Date ? row.event_created_at : new Date(row.event_created_at),
      object: row.payload,
    }
    const applied = await apply(db, clock, config, orgId, event, analytics)
    await db.execute(sql`
      UPDATE billing_events
      SET org_id = ${orgId}::uuid, outcome = ${applied.outcome},
          processed_at = ${clock.now().toISOString()}
      WHERE stripe_event_id = ${event.id}`)
    details.push(`${event.type}: ${applied.detail}`)
  }
  return { resolved: pending.length, details }
}

// ---------------------------------------------------------------------------

export type Outcome = { outcome: 'applied' | 'ignored' | 'stale'; detail: string }

/**
 * The effect of one event, under whichever scoping the caller established.
 *
 * Shared by the live delivery, which runs with the customer declared, and by
 * the replay, which runs with a tenant. Both reach the same rows through
 * different policies, and having one body is what stops the two paths drifting
 * into applying an event differently depending on when it arrived.
 */
async function apply(
  db: Db,
  clock: Clock,
  config: StripeConfig,
  orgId: string,
  event: StripeEvent,
  analytics: Analytics,
): Promise<Outcome> {
  switch (event.type) {
    case 'customer.subscription.created':
    case 'customer.subscription.updated':
    case 'customer.subscription.deleted': {
      const subscription = subscriptionOf(event.object)
      const written = await writeSubscription(db, clock, config, orgId, subscription, event.created, analytics)
      if (written.outcome === 'stale') return written
      const moved = await recomputePlan(db, clock, orgId, analytics)
      return { outcome: 'applied', detail: `${subscription.status}; ${moved}` }
    }

    case 'invoice.paid':
    case 'invoice.payment_failed':
    case 'invoice.finalized': {
      const invoice = invoiceOf(event.object)
      if (!invoice) return { outcome: 'ignored', detail: 'the invoice has no id or no customer' }
      return writeInvoice(db, clock, orgId, invoice, event.created)
    }

    case 'payment_method.attached':
    case 'payment_method.detached':
      return writePaymentMethod(db, clock, orgId, event)

    case 'checkout.session.completed':
      return {
        outcome: 'ignored',
        detail: 'recorded; the entitlement comes from the subscription events for this purchase',
      }

    default:
      // Reached only on a replay, when a row was recorded by a version that
      // handled its type and is being applied by one that does not. The live
      // path never gets here, because handleStripeDelivery gates on
      // HANDLED_EVENTS before anything is written.
      return { outcome: 'ignored', detail: 'not an event this control plane acts on' }
  }
}

/**
 * One subscription, written if the event is newer than what the row already
 * holds.
 *
 * Exported for src/billing/store.ts, which writes what Stripe says through
 * this same function rather than its own SQL. Two writers for one row means two
 * places that have to agree about the watermark, and the one that would be
 * wrong is reconciliation, which runs rarely and is tested least.
 */
export async function writeSubscription(
  db: Db,
  clock: Clock,
  config: StripeConfig,
  orgId: string,
  subscription: StripeSubscription,
  eventCreated: Date,
  analytics: Analytics,
): Promise<Outcome> {
  // Serialize before taking a subscription lock. Different subscription rows
  // still decide one organization plan, so locking only the final update lets
  // concurrent writers each compute from a snapshot missing the other's row.
  await db.execute(sql`SELECT id FROM organizations WHERE id = ${orgId}::uuid FOR UPDATE`)
  const entitled = planForPrice(config, subscription.priceId)
  const now = clock.now().toISOString()

  // The plan the row records is what the price sells. A price this control
  // plane does not recognise leaves the row's plan as it was rather than
  // dropping it to free: somebody who bought through a link nobody configured
  // has paid, and taking capacity away from them is the wrong direction to be
  // wrong in. `excluded` is the row that would have been inserted, so the
  // coalesce reads "the recognised plan, or whatever we already believed".
  const rows = await db.execute<{ stripe_subscription_id: string }>(sql`
    INSERT INTO subscriptions (
      org_id, stripe_subscription_id, stripe_customer_id, plan, price_id, quantity,
      status, current_period_start, current_period_end, cancel_at_period_end,
      canceled_at, last_event_at, created_at, updated_at)
    VALUES (
      ${orgId}::uuid, ${subscription.id}, ${subscription.customerId},
      ${entitled ?? DEFAULT_PLAN}, ${subscription.priceId}, ${subscription.quantity},
      ${subscription.status}, ${iso(subscription.currentPeriodStart)},
      ${iso(subscription.currentPeriodEnd)}, ${subscription.cancelAtPeriodEnd},
      ${iso(subscription.canceledAt)}, ${eventCreated.toISOString()},
      ${(subscription.createdAt ?? eventCreated).toISOString()}, ${now})
    ON CONFLICT (stripe_subscription_id) DO UPDATE SET
      plan = coalesce(${entitled}, subscriptions.plan),
      price_id = excluded.price_id,
      quantity = excluded.quantity,
      status = excluded.status,
      current_period_start = excluded.current_period_start,
      current_period_end = excluded.current_period_end,
      cancel_at_period_end = excluded.cancel_at_period_end,
      canceled_at = excluded.canceled_at,
      last_event_at = excluded.last_event_at,
      created_at = coalesce(${iso(subscription.createdAt ?? null)}::timestamptz, subscriptions.created_at),
      updated_at = ${now}
    -- The watermark. An event created before the one this row was last written
    -- from updates nothing, so a late delivery cannot resurrect a cancelled
    -- subscription or undo an upgrade that has already landed.
    WHERE subscriptions.last_event_at < excluded.last_event_at
    RETURNING stripe_subscription_id`)

  if (rows.length === 0) {
    return {
      outcome: 'stale',
      detail: 'an older event than the one this subscription was last written from; nothing changed',
    }
  }
  await analytics.record(db, {
    name: 'revenue.subscription_changed',
    occurredAt: eventCreated,
    orgId,
    // The plan and the status. Never the amount, never the price identifier,
    // never the customer: an analytics store kept for years to draw graphs from
    // is not where a payment record belongs, and the subscriptions table
    // already holds all of it.
    payload: {
      plan: planName(entitled ?? DEFAULT_PLAN),
      status: subscriptionStatus(subscription.status),
    },
  })

  return { outcome: 'applied', detail: 'subscription written' }
}

/**
 * The organization's plan, recomputed from its subscriptions.
 *
 * From the rows rather than from the event that just arrived, because an
 * organization can hold more than one subscription row and the plan is a
 * property of all of them. Deterministic: the newest subscription in an
 * entitling status decides, and no subscription in one means free.
 */
export async function recomputePlan(
  db: Db,
  clock: Clock,
  orgId: string,
  analytics: Analytics,
): Promise<string> {
  await db.execute(sql`SELECT id FROM organizations WHERE id = ${orgId}::uuid FOR UPDATE`)
  const entitling = sql.join(ENTITLING_STATUSES.map((status) => sql`${status}`), sql`, `)
  const live = await db.execute<{ plan: string; status: string }>(sql`
    SELECT plan, status FROM subscriptions
    WHERE org_id = ${orgId}::uuid
    ORDER BY (status IN (${entitling}) AND plan <> ${DEFAULT_PLAN}) DESC,
             created_at DESC, stripe_subscription_id DESC
    LIMIT 1`)
  const newest = live[0]
  if (!newest) return 'no subscription; the plan was left alone'
  if (ENTITLING_STATUSES.includes(newest.status) && newest.plan === DEFAULT_PLAN) {
    return 'the subscription price grants no known plan; the plan was left alone'
  }

  const wanted = planForStatus(newest.status, null)
  if (wanted === null) {
    // A status Stripe added since this was written. Changing somebody's plan on
    // a word nobody has read would be a guess about their money.
    return `status ${newest.status} is not one this control plane knows; the plan was left alone`
  }
  const plan = ENTITLING_STATUSES.includes(newest.status) ? newest.plan : wanted

  // The plan it is moving FROM, read in the same statement rather than before
  // it. A separate SELECT would be a different snapshot from the UPDATE, so two
  // deliveries landing together could each report the same old plan and the
  // chart would show one upgrade twice.
  const moved = await db.execute<{ was: string }>(sql`
    WITH before AS (SELECT id, plan FROM organizations WHERE id = ${orgId}::uuid FOR UPDATE)
    UPDATE organizations
    SET plan = ${plan}, updated_at = ${clock.now().toISOString()}
    FROM before
    WHERE organizations.id = before.id AND organizations.plan <> ${plan}
    RETURNING before.plan AS was`)

  if (moved.length > 0) {
    await analytics.record(db, {
      name: 'revenue.plan_changed',
      occurredAt: clock.now(),
      orgId,
      payload: { from: planName(moved[0]!.was), to: planName(plan) },
    })
  }
  return moved.length > 0 ? `the plan is now ${plan}` : `the plan stays ${plan}`
}

/**
 * One invoice, written if the event is newer than what the row already holds.
 *
 * Exported for src/billing/store.ts, which reconciles invoices through this
 * same function. A missed invoice.paid delivery is a missing row in somebody's
 * billing history, and the only way to notice it is to ask Stripe.
 */
export async function writeInvoice(
  db: Db,
  clock: Clock,
  orgId: string,
  invoice: StripeInvoice,
  eventCreated: Date,
): Promise<Outcome> {
  const now = clock.now().toISOString()
  const paidAt = invoice.status === 'paid' ? eventCreated.toISOString() : null

  const rows = await db.execute<{ stripe_invoice_id: string }>(sql`
    INSERT INTO invoices (
      org_id, stripe_invoice_id, stripe_customer_id, stripe_subscription_id, number,
      status, amount_due, amount_paid, currency, hosted_invoice_url,
      period_start, period_end, paid_at, last_event_at, created_at, updated_at)
    VALUES (
      ${orgId}::uuid, ${invoice.id}, ${invoice.customerId}, ${invoice.subscriptionId},
      ${invoice.number}, ${invoice.status}, ${invoice.amountDue}, ${invoice.amountPaid},
      ${invoice.currency}, ${invoice.hostedInvoiceUrl}, ${iso(invoice.periodStart)},
      ${iso(invoice.periodEnd)}, ${paidAt}, ${eventCreated.toISOString()}, ${now}, ${now})
    ON CONFLICT (stripe_invoice_id) DO UPDATE SET
      stripe_subscription_id = excluded.stripe_subscription_id,
      number = excluded.number,
      status = excluded.status,
      amount_due = excluded.amount_due,
      amount_paid = excluded.amount_paid,
      currency = excluded.currency,
      hosted_invoice_url = excluded.hosted_invoice_url,
      period_start = excluded.period_start,
      period_end = excluded.period_end,
      -- Kept once set. An invoice that was paid and later voided was still paid
      -- on the day it was paid, and losing that date loses the answer to the
      -- only question anybody asks about an old invoice.
      paid_at = coalesce(invoices.paid_at, excluded.paid_at),
      last_event_at = excluded.last_event_at,
      updated_at = ${now}
    WHERE invoices.last_event_at < excluded.last_event_at
    RETURNING stripe_invoice_id`)

  if (rows.length === 0) {
    return { outcome: 'stale', detail: 'an older event than this invoice was last written from' }
  }
  // Deliberately no plan change here, and this is the dunning decision.
  //
  // Stripe retries a failed charge on its own schedule and emails the customer
  // while it does. Cutting somebody off on the first failure would take their
  // environments away on the day their card expired, hours before the retry
  // that was going to succeed. The plan follows the SUBSCRIPTION's status, and
  // Stripe moves that to canceled or unpaid when it has given up.
  return { outcome: 'applied', detail: `invoice ${invoice.status}` }
}

async function writePaymentMethod(
  db: Db,
  clock: Clock,
  orgId: string,
  event: StripeEvent,
): Promise<Outcome> {
  const id = typeof event.object.id === 'string' ? event.object.id : ''
  if (!id) return { outcome: 'ignored', detail: 'the payment method has no id' }

  // detached carries the customer it was detached FROM, and attached carries
  // the one it was attached to. The scoping already proved which customer this
  // delivery is about, so the column is written from that rather than from a
  // field whose meaning changes with the event type.
  const customerId = customerOf(event)
  if (!customerId) return { outcome: 'ignored', detail: 'the payment method names no customer' }

  const card = event.object.card as Record<string, unknown> | undefined
  const detached = event.type === 'payment_method.detached'
  const now = clock.now().toISOString()

  const rows = await db.execute<{ id: string }>(sql`
    INSERT INTO payment_methods (
      org_id, stripe_payment_method_id, stripe_customer_id, kind, brand, last4,
      exp_month, exp_year, detached_at, last_event_at, created_at, updated_at)
    VALUES (
      ${orgId}::uuid, ${id}, ${customerId},
      ${typeof event.object.type === 'string' ? event.object.type : 'card'},
      ${text(card?.brand)}, ${digits(card?.last4)},
      ${whole(card?.exp_month)}, ${whole(card?.exp_year)},
      ${detached ? event.created.toISOString() : null},
      ${event.created.toISOString()}, ${now}, ${now})
    ON CONFLICT (stripe_payment_method_id) DO UPDATE SET
      brand = excluded.brand,
      last4 = excluded.last4,
      exp_month = excluded.exp_month,
      exp_year = excluded.exp_year,
      detached_at = excluded.detached_at,
      last_event_at = excluded.last_event_at,
      updated_at = ${now}
    WHERE payment_methods.last_event_at < excluded.last_event_at
    RETURNING id`)

  if (rows.length === 0) {
    return { outcome: 'stale', detail: 'an older event than this payment method was last written from' }
  }
  return { outcome: 'applied', detail: detached ? 'payment method detached' : 'payment method attached' }
}

// ---------------------------------------------------------------------------

/**
 * The Stripe customer an event is about.
 *
 * Every object this control plane acts on carries one, and Stripe sends it
 * either as an identifier or, when somebody expanded it, as an object. A reader
 * that assumed one shape would work until the day anybody turned expansion on.
 */
function customerOf(event: StripeEvent): string | null {
  const raw = event.object.customer
  if (typeof raw === 'string' && raw !== '') return raw
  if (raw !== null && typeof raw === 'object') {
    const id = (raw as { id?: unknown }).id
    return typeof id === 'string' && id !== '' ? id : null
  }
  return null
}

function iso(v: Date | null): string | null {
  return v === null ? null : v.toISOString()
}

function text(v: unknown): string | null {
  return typeof v === 'string' && v !== '' ? v : null
}

/** Four digits or nothing. The column has a CHECK on it, and a payment method
 *  whose last4 arrived as something else must not fail the write on the path
 *  that has to record every delivery. */
function digits(v: unknown): string | null {
  const s = typeof v === 'string' ? v : typeof v === 'number' ? String(v) : ''
  return /^[0-9]{4}$/.test(s) ? s : null
}

function whole(v: unknown): number | null {
  const n = typeof v === 'number' ? v : typeof v === 'string' ? Number(v) : Number.NaN
  return Number.isFinite(n) ? Math.trunc(n) : null
}
