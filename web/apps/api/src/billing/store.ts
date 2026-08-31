// What the control plane holds about an organization's billing, and the two
// operations that are not a webhook.
//
// ATTACHING a customer is where an organization gets a Stripe identity, and it
// is the moment that resolves every delivery that arrived before it existed.
// That is not an optimisation: a row created after the event that was waiting
// for it has to resolve itself, because the event is not coming again.
//
// RECONCILING is the answer to the delivery that never arrives. Nothing in a
// webhook handler can fix a webhook that was not made, so this asks Stripe what
// it believes and writes that down. It is the difference between a missed
// delivery costing a delay and a missed delivery costing a customer the plan
// they are paying for.

import { sql } from 'drizzle-orm'
import type { Db, Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import type { StripeClient } from './stripe.ts'
import { LIVE_STATUSES, type StripeConfig } from './plans.ts'
import { recomputePlan, resolvePending, writeInvoice, writeSubscription } from './webhook.ts'

export interface BillingCustomer {
  orgId: string
  stripeCustomerId: string
  email: string | null
}

/** Everything a billing screen needs, in one read. */
export interface BillingState {
  plan: string
  customer: BillingCustomer | null
  subscription: {
    id: string
    plan: string
    status: string
    priceId: string | null
    quantity: number
    currentPeriodStart: string | null
    currentPeriodEnd: string | null
    cancelAtPeriodEnd: boolean
    canceledAt: string | null
  } | null
  /** How many subscriptions are live at once. More than one is a duplicate
   *  purchase, which no constraint here refuses, so it is reported instead of
   *  being invisible. See migrations/0020_billing.sql. */
  liveSubscriptions: number
  paymentMethod: {
    brand: string | null
    last4: string | null
    expMonth: number | null
    expYear: number | null
  } | null
}

/**
 * Records the Stripe customer for an organization, and applies what was waiting
 * for it.
 *
 * Idempotent on the organization: a second call with a different Stripe
 * customer keeps the first, because two customers for one organization is a
 * double charge and the earlier one is the one Stripe has already been told
 * about. The caller is told which it ended up with rather than assuming.
 */
export async function attachCustomer(
  db: Db,
  clock: Clock,
  config: StripeConfig,
  orgId: string,
  customer: { id: string; email: string | null },
): Promise<{ customerId: string; created: boolean; resolved: number }> {
  const now = clock.now().toISOString()
  const inserted = await db.execute<{ stripe_customer_id: string }>(sql`
    INSERT INTO billing_customers (org_id, stripe_customer_id, email, created_at, updated_at)
    VALUES (${orgId}::uuid, ${customer.id}, ${customer.email}, ${now}, ${now})
    ON CONFLICT (org_id) DO NOTHING
    RETURNING stripe_customer_id`)

  if (inserted.length === 0) {
    const existing = await db.execute<{ stripe_customer_id: string }>(sql`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${orgId}::uuid`)
    return { customerId: existing[0]!.stripe_customer_id, created: false, resolved: 0 }
  }

  // In the same transaction as the insert. Half of this happening would leave a
  // customer attached with its events still unresolved, which looks exactly
  // like the bug this exists to prevent.
  const { resolved } = await resolvePending(db, clock, config, orgId, customer.id)
  return { customerId: customer.id, created: true, resolved }
}

/** Everything a billing screen needs, in one read. */
export async function readBillingState(db: Db, orgId: string): Promise<BillingState> {
  const org = await db.execute<{ plan: string }>(sql`
    SELECT plan FROM organizations WHERE id = ${orgId}::uuid`)

  const customer = await db.execute<{ stripe_customer_id: string; email: string | null }>(sql`
    SELECT stripe_customer_id, email FROM billing_customers WHERE org_id = ${orgId}::uuid`)

  const subscription = await db.execute<SubscriptionRow>(sql`
    SELECT stripe_subscription_id, plan, status, price_id, quantity,
           current_period_start, current_period_end, cancel_at_period_end, canceled_at
    FROM subscriptions WHERE org_id = ${orgId}::uuid
    ORDER BY created_at DESC LIMIT 1`)

  // Each status as its own bind parameter. Interpolating a JavaScript array
  // into `= ANY(...)` produces a row expression rather than an array, which
  // Postgres refuses, and the refusal reaches the caller as a 500 on the
  // billing screen.
  const liveStatuses = sql.join(LIVE_STATUSES.map((status) => sql`${status}`), sql`, `)
  const live = await db.execute<{ n: string }>(sql`
    SELECT count(*) AS n FROM subscriptions
    WHERE org_id = ${orgId}::uuid AND status IN (${liveStatuses})`)

  // The newest card that has not been detached. A person recognises their card
  // by its last four digits and nothing else here is any use to them.
  const method = await db.execute<{
    brand: string | null
    last4: string | null
    exp_month: number | null
    exp_year: number | null
  }>(sql`
    SELECT brand, last4, exp_month, exp_year FROM payment_methods
    WHERE org_id = ${orgId}::uuid AND detached_at IS NULL
    ORDER BY created_at DESC LIMIT 1`)

  const row = subscription[0]
  return {
    plan: org[0]?.plan ?? 'free',
    customer: customer[0]
      ? { orgId, stripeCustomerId: customer[0].stripe_customer_id, email: customer[0].email }
      : null,
    subscription: row
      ? {
          id: row.stripe_subscription_id,
          plan: row.plan,
          status: row.status,
          priceId: row.price_id,
          quantity: row.quantity,
          currentPeriodStart: asIso(row.current_period_start),
          currentPeriodEnd: asIso(row.current_period_end),
          cancelAtPeriodEnd: row.cancel_at_period_end,
          canceledAt: asIso(row.canceled_at),
        }
      : null,
    liveSubscriptions: Number(live[0]?.n ?? 0),
    paymentMethod: method[0]
      ? {
          brand: method[0].brand,
          last4: method[0].last4,
          expMonth: method[0].exp_month,
          expYear: method[0].exp_year,
        }
      : null,
  }
}

/** What reconciliation found, per subscription it asked about. */
export interface Reconciliation {
  checked: number
  changed: number
  notes: string[]
}

/**
 * Asks Stripe what it believes and writes that down.
 *
 * The fix for the delivery that never arrives, and the only one there is. It
 * runs against every subscription this organization has a row for, not only the
 * live ones, because the state that goes wrong is precisely a row this control
 * plane believes is live and Stripe cancelled a week ago.
 *
 * A subscription Stripe has never heard of changes nothing and is reported.
 * Marking it cancelled would be the obvious thing and it is wrong: an
 * identifier from a different Stripe account reads exactly the same way, and
 * downgrading somebody because a key was rotated is a worse failure than an
 * out-of-date row that a person can see.
 *
 * The Stripe calls happen BEFORE the transaction opens. A transaction held
 * across a network call to a third party is a transaction whose length is
 * decided by that third party.
 */
export async function reconcile(
  pool: Pool,
  clock: Clock,
  config: StripeConfig,
  client: StripeClient,
  orgId: string,
): Promise<Reconciliation> {
  const read = await pool.withTenant({ orgId }, async (db) => ({
    customer: await db.execute<{ stripe_customer_id: string }>(sql`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${orgId}::uuid`),
    subscriptions: await db.execute<{ stripe_subscription_id: string }>(sql`
      SELECT stripe_subscription_id FROM subscriptions
      WHERE org_id = ${orgId}::uuid ORDER BY created_at DESC LIMIT 50`),
  }))
  const customerId = read.customer[0]?.stripe_customer_id
  if (!customerId) {
    return { checked: 0, changed: 0, notes: ['this organization has no Stripe customer'] }
  }
  const known = read.subscriptions

  const invoices = await client
    .listInvoices(customerId, 50)
    .then((list) => ({ list, error: null as string | null }))
    .catch((err: unknown) => ({
      list: [],
      error: err instanceof Error ? err.message : String(err),
    }))

  const fetched = await Promise.all(
    known.map(async (row) => ({
      id: row.stripe_subscription_id,
      // Errors are carried rather than thrown, so one unreachable subscription
      // does not abandon the others. A sweep that stops at the first failure
      // reconciles the rows before the broken one and never the rest.
      result: await client
        .getSubscription(row.stripe_subscription_id)
        .then((s) => ({ subscription: s, error: null as string | null }))
        .catch((err: unknown) => ({
          subscription: null,
          error: err instanceof Error ? err.message : String(err),
        })),
    })),
  )

  return pool.withTenant({ orgId }, async (db) => {
    const notes: string[] = []
    let changed = 0
    for (const { id, result } of fetched) {
      if (result.error) {
        notes.push(`${id}: Stripe could not be asked (${result.error})`)
        continue
      }
      if (!result.subscription) {
        notes.push(`${id}: Stripe has no such subscription; the row was left alone`)
        continue
      }
      // The event time is now, because this is what Stripe believes now. That
      // advances the watermark past any delivery still in flight, which is
      // correct: a webhook describing an older state must not undo this.
      const written = await writeSubscription(
        db, clock, config, orgId, result.subscription, clock.now(),
      )
      if (written.outcome === 'applied') changed += 1
      notes.push(`${id}: ${result.subscription.status}, ${written.detail}`)
    }

    // Invoices too, and for the same reason. A missed invoice.paid delivery is
    // a gap in somebody's billing history, and the only way to notice one is to
    // ask Stripe what it issued.
    if (invoices.error) {
      notes.push(`invoices could not be read from Stripe (${invoices.error})`)
    } else {
      for (const invoice of invoices.list) {
        const written = await writeInvoice(db, clock, orgId, invoice, clock.now())
        if (written.outcome === 'applied') changed += 1
      }
      notes.push(`${invoices.list.length} invoices read from Stripe`)
    }

    if (known.length > 0) {
      notes.push(await recomputePlan(db, clock, orgId))
    }
    return { checked: known.length, changed, notes }
  })
}

interface SubscriptionRow extends Record<string, unknown> {
  stripe_subscription_id: string
  plan: string
  status: string
  price_id: string | null
  quantity: number
  current_period_start: Date | string | null
  current_period_end: Date | string | null
  cancel_at_period_end: boolean
  canceled_at: Date | string | null
}

function asIso(v: Date | string | null): string | null {
  if (v === null) return null
  return (v instanceof Date ? v : new Date(v)).toISOString()
}
