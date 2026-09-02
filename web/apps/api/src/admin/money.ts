// The nine things an operator can do to a customer's money, and the one rule
// they all obey.
//
// The rule: Stripe is the source of truth and this file never writes a local
// mirror of what it did. Every operation reads its BEFORE state from Stripe,
// sends its change to Stripe, reads the AFTER state out of Stripe's own
// response, and stops. The local `subscriptions` and `invoices` rows are moved
// by the webhook, which is the one path that already handles ordering,
// retries and the watermark that stops a stale delivery resurrecting a
// cancelled subscription.
//
// Writing the mirror here as well would be faster on screen and wrong within a
// week: two writers with different ideas about ordering produce a row that
// disagrees with the provider, an operator trusts the row because it is in our
// own database and renders instantly, and the disagreement is discovered by a
// customer. A mirror that drifts is worse than no mirror.
//
// The other rule, which is `runOnce`'s rather than this file's, is that no
// function here calls Stripe directly. They all go through the ledger, so every
// one of them is idempotent, audited, and recorded with a reason, a before and
// an after, whether or not the person writing the next one remembers.

import { TRPCError } from '@trpc/server'
import type { Db } from '@antifailure/db'
import type { StripeClient, StripeInvoice, StripeSubscription } from '../billing/stripe.ts'
import { StripeError } from '../billing/stripe.ts'
import type { StripeConfig, PaidPlan } from '../billing/plans.ts'
import { PAID_PLANS } from '../billing/plans.ts'
import { OperationConflict, runOnce, type OperationRun } from './ledger.ts'
import { killSwitch, killedMessage } from '../flags.ts'

/** What every operation here needs to reach the provider and the ledger. */
export interface MoneyContext {
  stripe: { client: StripeClient; config: StripeConfig }
  /** A transaction that can reach across tenants. Supplied by the admin
   *  boundary rather than built here, so there is one such thing rather than
   *  two with different ideas about what it may touch. */
  withAdmin: <R>(fn: (db: Db) => Promise<R>) => Promise<R>
  now: Date
  actorUserId: string | null
  actorLabel: string
}

/** What the caller says about WHY, on every one of these. */
export interface MoneyIntent {
  orgId: string
  reason: string
  /** The key the browser minted when the form opened, so that two presses of
   *  one button are one operation even though their parameters are identical
   *  and would otherwise merely collapse by content. */
  idempotencyKey?: string
}

// ---------------------------------------------------------------------------

/** Money, for a confirmation dialog and for a log line. Never a bare number:
 *  "500" is five dollars or five hundred yen and the difference is a hundredfold. */
export function money(minor: number, currency: string): string {
  const upper = currency.toUpperCase()
  // Stripe's zero-decimal currencies. The list is short and getting it wrong
  // is a factor of a hundred on a refund, which is the kind of defect that
  // ends up in a newspaper rather than in a bug tracker.
  const zeroDecimal = new Set([
    'BIF', 'CLP', 'DJF', 'GNF', 'JPY', 'KMF', 'KRW', 'MGA', 'PYG',
    'RWF', 'UGX', 'VND', 'VUV', 'XAF', 'XOF', 'XPF',
  ])
  const value = zeroDecimal.has(upper) ? minor : minor / 100
  try {
    return value.toLocaleString('en-US', { style: 'currency', currency: upper })
  } catch {
    // A currency Intl has never heard of. The number and the code beside each
    // other is worse looking and still unambiguous, which is the property that
    // matters on a screen about a refund.
    return `${value} ${upper}`
  }
}

/**
 * Refuses every administrative money write while the switch is off.
 *
 * One switch over all nine operations rather than nine, because the incident
 * this is for is "something is moving money and we do not yet know which part
 * of it", and an operator who has to find and flip nine switches during that
 * has not been given a control.
 *
 * Read through the admin transaction, so it works for an operator acting on a
 * tenant they are not a member of.
 */
export async function refuseWhenKilled(ctx: MoneyContext, orgId: string, what: string): Promise<void> {
  const killed = await ctx.withAdmin(async (db) =>
    killSwitch(db, 'billing.admin_writes', { orgId }),
  )
  if (killed) {
    throw new TRPCError({ code: 'PRECONDITION_FAILED', message: killedMessage(killed, what) })
  }
}

/** Turns the two failures this surface has into answers a person can act on. */
function asTrpc(err: unknown): never {
  if (err instanceof OperationConflict) {
    throw new TRPCError({ code: 'CONFLICT', message: err.message })
  }
  if (err instanceof StripeError) {
    // Stripe's own message, deliberately. It is written for a person, it names
    // the actual refusal ("charge has already been refunded"), and replacing it
    // with something of ours would turn a specific answer into a shrug.
    throw new TRPCError({
      code: 'PRECONDITION_FAILED',
      message: `The payment provider refused this: ${err.message}`,
    })
  }
  throw err
}

// ---------------------------------------------------------------------------
// Refunds
// ---------------------------------------------------------------------------

export interface RefundInput extends MoneyIntent {
  chargeId: string
  /** Absent means the whole charge. */
  amountMinor?: number | null
  /** Stripe's vocabulary. */
  category?: 'duplicate' | 'fraudulent' | 'requested_by_customer' | null
}

/**
 * Refunds a charge, once.
 *
 * The before state is read from Stripe rather than from the local invoice
 * rows, and it is not decoration: `amount_refunded` is what says whether this
 * charge has already been refunded, and a screen that offered a full refund on
 * a charge that is already half refunded would be offering an amount Stripe is
 * about to refuse. Reading it also puts the pre-refund figures in the audit
 * entry, which is what makes the entry reviewable a month later.
 */
export async function refundCharge(
  ctx: MoneyContext,
  input: RefundInput,
): Promise<OperationRun<{ refundId: string; amountMinor: number; currency: string }>> {
  await refuseWhenKilled(ctx, input.orgId, 'Refunding a charge')
  try {
    const before = await beforeCharge(ctx, input.chargeId)
    return await runOnce(
      ctx.withAdmin,
      ctx.now,
      {
        action: 'billing.refunded',
        orgId: input.orgId,
        targetType: 'charge',
        targetId: input.chargeId,
        actorUserId: ctx.actorUserId,
        actorLabel: ctx.actorLabel,
        reason: input.reason,
        // Everything the outcome depends on. The amount is in here, so a
        // second refund for a DIFFERENT amount gets a different key and is a
        // different operation rather than a silent replay of the first.
        params: {
          chargeId: input.chargeId,
          amountMinor: input.amountMinor ?? null,
          category: input.category ?? null,
        },
        ...(input.idempotencyKey ? { idempotencyKey: input.idempotencyKey } : {}),
      },
      async (key) => {
        const refund = await ctx.stripe.client.refund(
          {
            chargeId: input.chargeId,
            amountMinor: input.amountMinor ?? null,
            reason: input.category ?? null,
          },
          key,
        )
        return {
          result: {
            refundId: refund.id,
            amountMinor: refund.amount,
            currency: refund.currency,
          },
          providerObjectId: refund.id,
          amountMinor: refund.amount,
          currency: refund.currency,
          after: {
            refundId: refund.id,
            status: refund.status,
            amountMinor: refund.amount,
            currency: refund.currency,
          },
        }
      },
      before,
    )
  } catch (err) {
    return asTrpc(err)
  }
}

/** The charge as Stripe has it now, for the before state. A charge Stripe has
 *  never heard of is a refusal rather than a reason to guess. */
async function beforeCharge(ctx: MoneyContext, chargeId: string): Promise<Record<string, unknown>> {
  const charge = await ctx.stripe.client.getCharge(chargeId)
  if (!charge) {
    throw new TRPCError({
      code: 'NOT_FOUND',
      message:
        `The payment provider has no charge ${chargeId}. Nothing was refunded. Check the ` +
        'identifier against the invoice it came from.',
    })
  }
  return {
    amountMinor: charge.amount,
    amountRefundedMinor: charge.amountRefunded,
    currency: charge.currency,
    status: charge.status,
    disputed: charge.disputed,
  }
}

// ---------------------------------------------------------------------------
// Credit
// ---------------------------------------------------------------------------

export interface CreditInput extends MoneyIntent {
  customerId: string
  /** Positive, in minor units. The sign is Stripe's problem, not the caller's. */
  amountMinor: number
  currency: string
}

export async function creditCustomer(
  ctx: MoneyContext,
  input: CreditInput,
): Promise<OperationRun<{ transactionId: string; endingBalance: number; currency: string }>> {
  await refuseWhenKilled(ctx, input.orgId, 'Crediting a customer')
  try {
    const customer = await ctx.stripe.client.getCustomer(input.customerId)
    if (!customer) {
      throw new TRPCError({
        code: 'NOT_FOUND',
        message: `The payment provider has no customer ${input.customerId}. No credit was applied.`,
      })
    }
    return await runOnce(
      ctx.withAdmin,
      ctx.now,
      {
        action: 'billing.credited',
        orgId: input.orgId,
        targetType: 'customer',
        targetId: input.customerId,
        actorUserId: ctx.actorUserId,
        actorLabel: ctx.actorLabel,
        reason: input.reason,
        params: {
          customerId: input.customerId,
          amountMinor: input.amountMinor,
          currency: input.currency,
        },
        ...(input.idempotencyKey ? { idempotencyKey: input.idempotencyKey } : {}),
      },
      async (key) => {
        const moved = await ctx.stripe.client.creditCustomer(
          input.customerId,
          {
            amountMinor: input.amountMinor,
            currency: input.currency,
            // The reason travels to Stripe as well as to the audit log, so
            // whoever opens the dashboard sees the same sentence.
            description: input.reason.slice(0, 350),
          },
          key,
        )
        return {
          result: {
            transactionId: moved.id,
            endingBalance: moved.endingBalance,
            currency: moved.currency,
          },
          providerObjectId: moved.id,
          amountMinor: input.amountMinor,
          currency: moved.currency,
          after: { balanceMinor: moved.endingBalance, currency: moved.currency },
        }
      },
      { balanceMinor: customer.balance, currency: customer.currency },
    )
  } catch (err) {
    return asTrpc(err)
  }
}

// ---------------------------------------------------------------------------
// The subscription changes
//
// One helper, five callers. Each caller names what it is doing and builds the
// parameter map; the helper reads the before state, runs the ledger and shapes
// the after state, so the five cannot drift about how a subscription change is
// recorded.
// ---------------------------------------------------------------------------

function subscriptionState(s: StripeSubscription): Record<string, unknown> {
  return {
    status: s.status,
    priceId: s.priceId,
    quantity: s.quantity,
    cancelAtPeriodEnd: s.cancelAtPeriodEnd,
    currentPeriodEnd: s.currentPeriodEnd?.toISOString() ?? null,
  }
}

async function changeSubscription(
  ctx: MoneyContext,
  intent: MoneyIntent & { subscriptionId: string },
  action: string,
  params: Record<string, string>,
  /** Named so the fingerprint of a plan change and a trial extension on the
   *  same subscription differ even when the parameter maps happen to collide. */
  describe: Record<string, unknown>,
  /** The subscription, when the caller has already had to read it.
   *
   *  changePlan does: it needs the current price to refuse a no-op and the item
   *  id to replace rather than add. Passing it back in is not a micro
   *  optimisation, it is one fact instead of two: reading twice means the
   *  before state recorded in the ledger can differ from the one the caller
   *  made its decision on, and a plan change that raced a webhook would be
   *  audited against a subscription nobody ever saw. */
  already?: StripeSubscription,
): Promise<OperationRun<{ subscription: Record<string, unknown> }>> {
  await refuseWhenKilled(ctx, intent.orgId, 'Changing a subscription')
  const before = already ?? (await ctx.stripe.client.getSubscription(intent.subscriptionId))
  if (!before) {
    throw new TRPCError({
      code: 'NOT_FOUND',
      message:
        `The payment provider has no subscription ${intent.subscriptionId}. Nothing was changed.`,
    })
  }
  return runOnce(
    ctx.withAdmin,
    ctx.now,
    {
      action,
      orgId: intent.orgId,
      targetType: 'subscription',
      targetId: intent.subscriptionId,
      actorUserId: ctx.actorUserId,
      actorLabel: ctx.actorLabel,
      reason: intent.reason,
      params: { subscriptionId: intent.subscriptionId, ...describe },
      ...(intent.idempotencyKey ? { idempotencyKey: intent.idempotencyKey } : {}),
    },
    async (key) => {
      const after = await ctx.stripe.client.updateSubscription(intent.subscriptionId, params, key)
      return {
        result: { subscription: subscriptionState(after) },
        providerObjectId: after.id,
        after: subscriptionState(after),
      }
    },
    subscriptionState(before),
  )
}

export async function changePlan(
  ctx: MoneyContext,
  input: MoneyIntent & { subscriptionId: string; plan: PaidPlan; prorate: boolean },
) {
  if (!PAID_PLANS.includes(input.plan)) {
    throw new TRPCError({ code: 'BAD_REQUEST', message: `${input.plan} is not a plan with a price.` })
  }
  const priceId = ctx.stripe.config.prices[input.plan]
  // The kill switch before the read, not just before the write. During a
  // payments incident the useful behaviour is to stop touching the provider at
  // all, and a refusal that first spends a round trip on a read is a refusal
  // that took longer than it needed to.
  await refuseWhenKilled(ctx, input.orgId, 'Changing a plan')
  const before = await ctx.stripe.client.getSubscription(input.subscriptionId)
  if (!before) {
    throw new TRPCError({
      code: 'NOT_FOUND',
      message: `The payment provider has no subscription ${input.subscriptionId}. Nothing was changed.`,
    })
  }
  if (before.priceId === priceId) {
    // Not an error and not an operation. Recording "changed the plan" for a
    // change that changes nothing makes the ledger worse at the one job it
    // has, the same rule billing.set already follows.
    throw new TRPCError({
      code: 'PRECONDITION_FAILED',
      message: `That subscription is already on the ${input.plan} plan. Nothing was changed.`,
    })
  }
  try {
    return await changeSubscription(
      ctx,
      input,
      'billing.plan_changed',
      {
        // The item id, not just the price. Omitting it makes Stripe ADD a
        // second item rather than replace the first, and the customer is then
        // billed for both plans at once.
        ...(before.itemId ? { 'items[0][id]': before.itemId } : {}),
        'items[0][price]': priceId,
        // Said explicitly rather than left to the account's default, because
        // the account default decides whether somebody is billed today, and
        // an operator changing a plan has to be the one who decides that.
        proration_behavior: input.prorate ? 'create_prorations' : 'none',
      },
      { plan: input.plan, priceId, prorate: input.prorate },
      before,
    )
  } catch (err) {
    return asTrpc(err)
  }
}

export async function extendTrial(
  ctx: MoneyContext,
  input: MoneyIntent & { subscriptionId: string; until: Date },
) {
  const seconds = Math.floor(input.until.getTime() / 1000)
  if (input.until.getTime() <= ctx.now.getTime()) {
    throw new TRPCError({
      code: 'BAD_REQUEST',
      message: 'A trial can only be extended to a date in the future. Nothing was changed.',
    })
  }
  try {
    return await changeSubscription(
      ctx,
      input,
      'billing.trial_extended',
      {
        trial_end: String(seconds),
        // Without this Stripe treats the change as a proration event and can
        // invoice immediately, which is the opposite of extending a trial.
        proration_behavior: 'none',
      },
      { until: input.until.toISOString() },
    )
  } catch (err) {
    return asTrpc(err)
  }
}

export async function cancelSubscription(
  ctx: MoneyContext,
  input: MoneyIntent & { subscriptionId: string },
) {
  try {
    // At the end of the paid period, the same as the customer-facing route.
    // Cancelling immediately takes away a month somebody has already paid for,
    // which is a refund question rather than a cancellation, and it is asked
    // and answered separately.
    return await changeSubscription(
      ctx,
      input,
      'billing.subscription_canceled',
      { cancel_at_period_end: 'true' },
      { atPeriodEnd: true },
    )
  } catch (err) {
    return asTrpc(err)
  }
}

export async function reactivateSubscription(
  ctx: MoneyContext,
  input: MoneyIntent & { subscriptionId: string },
) {
  try {
    return await changeSubscription(
      ctx,
      input,
      'billing.subscription_reactivated',
      { cancel_at_period_end: 'false' },
      { atPeriodEnd: false },
    )
  } catch (err) {
    return asTrpc(err)
  }
}

export async function applyDiscount(
  ctx: MoneyContext,
  input: MoneyIntent & { subscriptionId: string; coupon: string },
) {
  try {
    return await changeSubscription(
      ctx,
      input,
      'billing.discount_applied',
      { coupon: input.coupon },
      { coupon: input.coupon },
    )
  } catch (err) {
    return asTrpc(err)
  }
}

// ---------------------------------------------------------------------------
// Invoices
// ---------------------------------------------------------------------------

function invoiceState(i: StripeInvoice): Record<string, unknown> {
  return {
    status: i.status,
    amountDueMinor: i.amountDue,
    amountPaidMinor: i.amountPaid,
    currency: i.currency,
  }
}

async function beforeInvoice(ctx: MoneyContext, invoiceId: string): Promise<StripeInvoice> {
  const invoice = await ctx.stripe.client.getInvoice(invoiceId)
  if (!invoice) {
    throw new TRPCError({
      code: 'NOT_FOUND',
      message: `The payment provider has no invoice ${invoiceId}. Nothing was attempted.`,
    })
  }
  return invoice
}

export async function retryPayment(
  ctx: MoneyContext,
  input: MoneyIntent & { invoiceId: string },
) {
  await refuseWhenKilled(ctx, input.orgId, 'Retrying a payment')
  try {
    const before = await beforeInvoice(ctx, input.invoiceId)
    if (before.status === 'paid') {
      throw new TRPCError({
        code: 'PRECONDITION_FAILED',
        message:
          `Invoice ${before.number ?? before.id} is already paid. Nothing was charged.`,
      })
    }
    return await runOnce(
      ctx.withAdmin,
      ctx.now,
      {
        action: 'billing.payment_retried',
        orgId: input.orgId,
        targetType: 'invoice',
        targetId: input.invoiceId,
        actorUserId: ctx.actorUserId,
        actorLabel: ctx.actorLabel,
        reason: input.reason,
        params: { invoiceId: input.invoiceId },
        ...(input.idempotencyKey ? { idempotencyKey: input.idempotencyKey } : {}),
      },
      async (key) => {
        const after = await ctx.stripe.client.payInvoice(input.invoiceId, key)
        return {
          result: invoiceState(after),
          providerObjectId: after.id,
          amountMinor: after.amountPaid,
          currency: after.currency,
          after: invoiceState(after),
        }
      },
      invoiceState(before),
    )
  } catch (err) {
    return asTrpc(err)
  }
}

export async function resendInvoice(
  ctx: MoneyContext,
  input: MoneyIntent & { invoiceId: string },
) {
  await refuseWhenKilled(ctx, input.orgId, 'Resending an invoice')
  try {
    const before = await beforeInvoice(ctx, input.invoiceId)
    return await runOnce(
      ctx.withAdmin,
      ctx.now,
      {
        action: 'billing.invoice_resent',
        orgId: input.orgId,
        targetType: 'invoice',
        targetId: input.invoiceId,
        actorUserId: ctx.actorUserId,
        actorLabel: ctx.actorLabel,
        reason: input.reason,
        params: { invoiceId: input.invoiceId },
        ...(input.idempotencyKey ? { idempotencyKey: input.idempotencyKey } : {}),
      },
      async (key) => {
        const after = await ctx.stripe.client.sendInvoice(input.invoiceId, key)
        return {
          result: invoiceState(after),
          providerObjectId: after.id,
          after: invoiceState(after),
        }
      },
      invoiceState(before),
    )
  } catch (err) {
    return asTrpc(err)
  }
}
