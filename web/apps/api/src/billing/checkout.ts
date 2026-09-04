import { sql } from 'drizzle-orm'
import { TRPCError } from '@trpc/server'
import type { OrgContext } from '../trpc.ts'
import type { Billing } from './index.ts'
import { LIVE_STATUSES } from './plans.ts'

interface Attempt extends Record<string, unknown> {
  attempt_id: string
  stripe_customer_id: string
  price_id: string
  success_url: string
  cancel_url: string
  stripe_session_id: string | null
  created_at: Date | string
}

export interface CheckoutInput {
  customerId: string
  priceId: string
  successUrl: string
  cancelUrl: string
}

function blocked(message: string): never {
  throw new TRPCError({ code: 'PRECONDITION_FAILED', message })
}

/** One organization can hold one payable checkout at a time, across replicas. */
export async function checkoutOnce(
  c: OrgContext,
  billing: Billing,
  input: CheckoutInput,
): Promise<{ url: string; sessionId: string }> {
  let replaceId: string | undefined
  for (let retry = 0; retry < 3; retry += 1) {
    // Check before reserving so a provider refusal leaves no idle attempt that
    // could later age out of the idempotency window without ever being sent.
    if (await billing.client.hasBlockingSubscription(input.customerId)) {
      blocked('Stripe already holds a subscription for this organization. Refresh from Stripe, then manage it in the billing portal.')
    }
    const reserved = await c.pool.withTenant(c.tenant, async (db) => {
      // Lock order matches subscription ingestion: organization before billing
      // rows. No provider call runs while this transaction holds a lock.
      const org = await db.execute(sql`SELECT id FROM organizations WHERE id = ${c.actor.orgId}::uuid FOR UPDATE`)
      if (org.length === 0) blocked('This organization no longer exists.')
      const live = await db.execute(sql`
        SELECT 1 FROM subscriptions WHERE org_id = ${c.actor.orgId}::uuid
        AND status IN (${sql.join(LIVE_STATUSES.map((status) => sql`${status}`), sql`, `)}) LIMIT 1`)
      if (live.length > 0) blocked('This organization already has a live subscription. Manage it in the billing portal.')

      const previous = await db.execute<Attempt>(sql`
        SELECT * FROM billing_checkout_attempts WHERE org_id = ${c.actor.orgId}::uuid`)
      if (previous[0] && previous[0].attempt_id !== replaceId) return { attempt: previous[0], fresh: false }

      const rows = await db.execute<Attempt>(sql`
        INSERT INTO billing_checkout_attempts
          (org_id, stripe_customer_id, price_id, success_url, cancel_url, created_at)
        VALUES (${c.actor.orgId}::uuid, ${input.customerId}, ${input.priceId},
                ${input.successUrl}, ${input.cancelUrl}, ${c.clock.now().toISOString()})
        ON CONFLICT (org_id) DO UPDATE SET
          attempt_id = gen_random_uuid(), stripe_customer_id = excluded.stripe_customer_id,
          price_id = excluded.price_id, success_url = excluded.success_url,
          cancel_url = excluded.cancel_url, stripe_session_id = NULL, created_at = excluded.created_at
        RETURNING *`)
      return { attempt: rows[0]!, fresh: true }
    })
    const attempt = reserved.attempt
    const key = `af-checkout-${attempt.attempt_id}`

    const previous = attempt.stripe_session_id
      ? await billing.client.getCheckoutSession(attempt.stripe_session_id)
      : reserved.fresh ? null : await billing.client.findCheckoutAttempt(input.customerId, key)
    if (previous) {
      if (previous.customerId !== input.customerId || (attempt.stripe_session_id && previous.id !== attempt.stripe_session_id)) blocked('The checkout identity could not be verified. No new purchase was started.')
      if (previous.status === 'expired') {
        replaceId = attempt.attempt_id
        continue
      }
      if (previous.status === 'complete') {
        const subscription = previous.subscriptionId
          ? await billing.client.getSubscription(previous.subscriptionId) : null
        if (subscription && subscription.customerId === input.customerId && subscription.id === previous.subscriptionId && ['canceled', 'incomplete_expired'].includes(subscription.status)) {
          replaceId = attempt.attempt_id
          continue
        }
        blocked('This checkout has already completed. Refresh from Stripe to see its subscription. No second purchase was started.')
      }
    } else if (attempt.stripe_session_id) {
      blocked('Stripe could not find the previous checkout. Ask the operator to reconcile it before starting another purchase.')
    }

    if (attempt.stripe_customer_id !== input.customerId || attempt.price_id !== input.priceId ||
        attempt.success_url !== input.successUrl || attempt.cancel_url !== input.cancelUrl) {
      blocked('An earlier checkout attempt has different settings. Ask the operator to reconcile it before starting another.')
    }

    if (!previous && c.clock.now().getTime() - new Date(attempt.created_at).getTime() >= 23 * 60 * 60 * 1000) {
      blocked('The previous checkout could not be recovered within Stripe\'s retry window. Ask the operator to locate it in Stripe before retrying. No new purchase was started.')
    }

    const session = previous ?? await billing.client.createCheckoutSession({
      customerId: attempt.stripe_customer_id,
      priceId: attempt.price_id,
      orgId: c.actor.orgId,
      successUrl: attempt.success_url,
      cancelUrl: attempt.cancel_url,
    }, key)
    if (session.customerId !== input.customerId || session.status !== 'open' || !session.url) {
      blocked('Stripe did not return an open checkout for this organization. Refresh from Stripe before retrying.')
    }

    const saved = await c.pool.withTenant(c.tenant, (db) => db.execute(sql`
      UPDATE billing_checkout_attempts SET stripe_session_id = ${session.id}
      WHERE org_id = ${c.actor.orgId}::uuid AND attempt_id = ${attempt.attempt_id}::uuid
      RETURNING attempt_id`))
    if (saved.length > 0) return { url: session.url, sessionId: session.id }
  }
  blocked('The checkout changed while this request was running. Retry to open the current checkout.')
}
