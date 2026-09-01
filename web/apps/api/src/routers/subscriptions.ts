// Taking money, as routes.
//
// Every procedure here is orgProcedure('billing.manage'), which is the
// permission the catalog has always carried and nothing has ever guarded. The
// enforcement that already works, PLAN_QUOTAS and checkQuota, is now pointed at
// something that can change.
//
// WHAT IS DELIBERATELY NOT HERE. There is no route that changes a plan
// directly, and no route that takes a card. Both live on Stripe's hosted pages:
// checkout for the first purchase, the customer portal for everything after it.
// That is not laziness, it is the decision about proration and about PCI scope
// in one:
//
// PRORATION. Stripe's portal computes the credit for the unused part of the old
// plan and the charge for the new one, applies it in the customer's currency
// and tax jurisdiction, and shows the customer the number before they agree to
// it. Reimplementing that here would mean choosing proration behaviour per
// change, getting the arithmetic right, and being wrong about somebody's money
// in a way they only find out about on their card statement.
//
// PCI SCOPE. A card never touches this process, so this process is never in
// scope for PCI DSS. A single form that posted a card number here would change
// the compliance obligations of the whole control plane.
//
// TAX is the same answer for the same reason: Stripe Tax, enabled on the
// account, computes and remits. There is no code here for it because there
// should not be.

import { z } from 'zod'
import { TRPCError } from '@trpc/server'
import { sql } from 'drizzle-orm'
import { router, orgProcedure, audit, type OrgContext } from '../trpc.ts'
import { attachCustomer, readBillingState, reconcile } from '../billing/store.ts'
import { LIVE_STATUSES, PAID_PLANS, type PaidPlan } from '../billing/plans.ts'
import { StripeError } from '../billing/stripe.ts'

/** The billing context, or a refusal that names the variables an operator has
 *  to set. A self-hosted installation takes no money and has to be able to run
 *  without ever having heard of Stripe, so this is a precondition rather than a
 *  crash at start-up. */
function billingOf(c: OrgContext) {
  if (!c.stripe) {
    throw new TRPCError({
      code: 'PRECONDITION_FAILED',
      message:
        'This control plane is not configured to take payments. Set AF_STRIPE_SECRET_KEY, ' +
        'AF_STRIPE_WEBHOOK_SECRET, AF_STRIPE_PRICE_TEAM and AF_STRIPE_PRICE_ENTERPRISE.',
    })
  }
  return c.stripe
}

/** A Stripe failure, turned into something a person reads, without leaking what
 *  Stripe said about an account that is not theirs. */
function refused(err: unknown, what: string): TRPCError {
  if (err instanceof StripeError) {
    return new TRPCError({
      code: 'BAD_GATEWAY',
      message: `Stripe would not ${what}. Nothing was charged and nothing was changed.`,
      cause: err,
    })
  }
  return err instanceof TRPCError
    ? err
    : new TRPCError({ code: 'INTERNAL_SERVER_ERROR', message: `Could not ${what}.`, cause: err })
}

export const subscriptionsRouter = router({
  /** The plan, the subscription, the card on file, in one read. */
  current: orgProcedure('billing.manage').query(async ({ ctx }) => {
    const c = ctx as OrgContext
    const state = await c.pool.withTenant(c.tenant, async (db) =>
      readBillingState(db, c.actor.orgId),
    )
    return {
      ...state,
      /** Whether this control plane can take money at all, so a screen can say
       *  so rather than showing a button that always fails. */
      configured: c.stripe !== null,
      plans: c.hostedRequiredPlan
        ? PAID_PLANS.filter((plan) => plan === c.hostedRequiredPlan)
        : PAID_PLANS,
      hostedRequiredPlan: c.hostedRequiredPlan,
    }
  }),

  /** Somebody's invoices, newest first. Read from this database rather than
   *  from Stripe: a billing page that cannot render while Stripe is slow is a
   *  billing page that is down whenever Stripe is. */
  invoices: orgProcedure('billing.manage')
    .input(z.object({ limit: z.number().int().min(1).max(100).default(20) }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute(sql`
          SELECT stripe_invoice_id, number, status, amount_due, amount_paid, currency,
                 hosted_invoice_url, period_start, period_end, paid_at, created_at
          FROM invoices WHERE org_id = ${c.actor.orgId}::uuid
          ORDER BY created_at DESC LIMIT ${input.limit}`)
        return { invoices: rows }
      })
    }),

  /**
   * Starts a purchase, and returns where to send the browser.
   *
   * Creates the Stripe customer on first use. The local row is written in the
   * same transaction that resolves whatever deliveries were already waiting on
   * that customer, so an event that arrived first is applied here rather than
   * sitting unresolved forever.
   */
  checkout: orgProcedure('billing.manage')
    .input(
      z.object({
        plan: z.enum(['team', 'enterprise']),
        seats: z.number().int().min(1).max(1000).default(1),
        /** Where Stripe sends the browser afterwards. Both are required rather
         *  than defaulted, because a default that pointed at the wrong
         *  deployment would land somebody who has just paid on a stranger's
         *  page. */
        successUrl: z.string().url(),
        cancelUrl: z.string().url(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      const billing = billingOf(c)
      if (c.hostedRequiredPlan && input.plan !== c.hostedRequiredPlan) {
        throw new TRPCError({
          code: 'PRECONDITION_FAILED',
          message: `This hosted control plane sells only the ${c.hostedRequiredPlan} plan.`,
        })
      }

      const state = await c.pool.withTenant(c.tenant, async (db) =>
        readBillingState(db, c.actor.orgId),
      )
      // The only place a double subscription can actually be prevented. No
      // constraint in the database can, because the charge happens at Stripe
      // and a refused write there would lose the delivery rather than the
      // charge; see migrations/0020_billing.sql.
      if (state.subscription && LIVE_STATUSES.includes(state.subscription.status)) {
        throw new TRPCError({
          code: 'PRECONDITION_FAILED',
          message:
            `This organization already has a ${state.subscription.status} subscription. ` +
            'Change the plan in the billing portal rather than buying a second one.',
        })
      }

      let customerId = state.customer?.stripeCustomerId ?? null
      if (!customerId) {
        const customer = await billing.client
          .createCustomer({
            email: state.customer?.email ?? null,
            name: c.actor.orgId,
            orgId: c.actor.orgId,
          })
          .catch((err: unknown) => {
            throw refused(err, 'create a customer for this organization')
          })
        const attached = await c.pool.withTenant(c.tenant, async (db) =>
          attachCustomer(db, c.clock, billing.config, c.actor.orgId, {
            id: customer.id,
            email: customer.email,
          }),
        )
        customerId = attached.customerId
      }

      const priceId = billing.config.prices[input.plan as PaidPlan]
      const session = await billing.client
        .createCheckoutSession({
          customerId,
          priceId,
          quantity: input.seats,
          orgId: c.actor.orgId,
          successUrl: input.successUrl,
          cancelUrl: input.cancelUrl,
        })
        .catch((err: unknown) => {
          throw refused(err, 'open a checkout page')
        })

      await c.pool.withTenant(c.tenant, async (db) => {
        await audit(db, c, {
          action: 'billing.checkout_started',
          targetType: 'organization',
          targetId: c.actor.orgId,
          // No card, no amount, no session secret. What an auditor needs is who
          // started buying what, and when.
          detail: { plan: input.plan, seats: input.seats, session: session.id },
        })
      })
      return { url: session.url, sessionId: session.id }
    }),

  /** The hosted page where a plan, a card, or a cancellation is changed. */
  portal: orgProcedure('billing.manage')
    .input(z.object({ returnUrl: z.string().url() }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      const billing = billingOf(c)

      const state = await c.pool.withTenant(c.tenant, async (db) =>
        readBillingState(db, c.actor.orgId),
      )
      if (!state.customer) {
        throw new TRPCError({
          code: 'PRECONDITION_FAILED',
          message: 'This organization has never bought anything, so it has no billing portal yet.',
        })
      }

      const session = await billing.client
        .createPortalSession({
          customerId: state.customer.stripeCustomerId,
          returnUrl: input.returnUrl,
        })
        .catch((err: unknown) => {
          throw refused(err, 'open the billing portal')
        })

      await c.pool.withTenant(c.tenant, async (db) => {
        await audit(db, c, {
          action: 'billing.portal_opened',
          targetType: 'organization',
          targetId: c.actor.orgId,
        })
      })
      return { url: session.url }
    }),

  /**
   * Cancels at the end of the paid period.
   *
   * Nothing is torn down and nothing is refunded. Somebody who cancels on day
   * two of a month they have paid for keeps the month, and the plan drops when
   * Stripe says the subscription has ended, which arrives here as
   * customer.subscription.deleted.
   */
  cancel: orgProcedure('billing.manage')
    .input(z.object({ reason: z.string().max(500).optional() }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      const billing = billingOf(c)

      const state = await c.pool.withTenant(c.tenant, async (db) =>
        readBillingState(db, c.actor.orgId),
      )
      if (!state.subscription || !LIVE_STATUSES.includes(state.subscription.status)) {
        throw new TRPCError({
          code: 'PRECONDITION_FAILED',
          message: 'There is no live subscription to cancel.',
        })
      }

      const cancelled = await billing.client
        .cancelSubscription(state.subscription.id)
        .catch((err: unknown) => {
          throw refused(err, 'cancel this subscription')
        })

      // Written here as well as when the webhook lands, so the screen reflects
      // what was just asked for rather than waiting on a delivery.
      //
      // It deliberately leaves last_event_at alone. Advancing the watermark
      // here would make the customer.subscription.updated that Stripe is about
      // to send look stale, and that delivery is the one carrying the fields
      // this response does not: the period the cancellation takes effect at,
      // and eventually the deleted event that drops the plan. What it writes is
      // only what Stripe just returned about this subscription, and it never
      // touches the plan, because the plan follows the subscription's status
      // and the status is still active until the period ends.
      await c.pool.withTenant(c.tenant, async (db) => {
        await db.execute(sql`
          UPDATE subscriptions
          SET cancel_at_period_end = ${cancelled.cancelAtPeriodEnd},
              status = ${cancelled.status},
              canceled_at = ${cancelled.canceledAt ? cancelled.canceledAt.toISOString() : null},
              updated_at = ${c.clock.now().toISOString()}
          WHERE org_id = ${c.actor.orgId}::uuid
            AND stripe_subscription_id = ${cancelled.id}`)
        await audit(db, c, {
          action: 'billing.subscription_cancelled',
          targetType: 'organization',
          targetId: c.actor.orgId,
          detail: { subscription: cancelled.id, reason: input.reason ?? null },
        })
      })

      return {
        cancelAtPeriodEnd: cancelled.cancelAtPeriodEnd,
        endsAt: state.subscription.currentPeriodEnd,
        running: 'nothing was torn down; the plan changes when the period ends',
      }
    }),

  /**
   * Asks Stripe what it believes and writes that down.
   *
   * The answer to a delivery that never arrived. A webhook that is not made
   * cannot be fixed by a webhook handler, so this exists and is reachable by a
   * person rather than only by a sweep nobody can trigger during an incident.
   */
  reconcile: orgProcedure('billing.manage').mutation(async ({ ctx }) => {
    const c = ctx as OrgContext
    const billing = billingOf(c)

    const result = await reconcile(
      c.pool, c.clock, billing.config, billing.client, c.actor.orgId,
    ).catch((err: unknown) => {
      throw refused(err, 'reconcile this organization against Stripe')
    })

    await c.pool.withTenant(c.tenant, async (db) => {
      await audit(db, c, {
        action: 'billing.reconciled',
        targetType: 'organization',
        targetId: c.actor.orgId,
        detail: { checked: result.checked, changed: result.changed },
      })
    })
    return result
  }),
})
