// What a subscription entitles, and where the numbers come from.
//
// The plan is the only thing billing changes about an organization.
// `PLAN_QUOTAS` in src/limits.ts already decides what each plan may hold and
// `checkQuota` already enforces it; this file is the join between a price at
// Stripe and one of those names. Nothing else in the billing code is allowed to
// know a price identifier, so a pricing change is an environment variable
// rather than a deploy.

import { PLAN_QUOTAS, DEFAULT_PLAN } from '../limits.ts'

/** Every plan the quota table knows. Read from PLAN_QUOTAS rather than written
 *  again, because two lists would disagree and the one that would be wrong is
 *  this one, which decides what somebody is charged for. */
export const PLANS: readonly string[] = Object.keys(PLAN_QUOTAS)

/** The plans somebody pays for. `free` is what an organization has when no
 *  subscription is live, so it never has a price. */
export type PaidPlan = 'team' | 'enterprise'
export const PAID_PLANS: readonly PaidPlan[] = ['team', 'enterprise']

export interface StripeConfig {
  /** The API key. Server side only: this never reaches a browser and never
   *  appears in a response. */
  secretKey: string
  /** The signing secret for the webhook endpoint. Without it the endpoint
   *  refuses every delivery rather than accepting unsigned ones. */
  webhookSecret: string
  /** The price each paid plan is sold at. */
  prices: Record<PaidPlan, string>
  /** Where Stripe lives. Overridden by tests so nothing reaches the real one,
   *  and by nothing else. */
  apiBase?: string
  /** How the client makes requests. Injected by tests so the client under test
   *  is the client that ships, talking to the engine's own Stripe mock pack,
   *  rather than a second implementation written to agree with itself. */
  fetch?: typeof globalThis.fetch
}

/**
 * The configuration, or null with a line saying what is missing.
 *
 * Null is a supported state. A self-hosted installation takes no money, and the
 * control plane has to run without ever having heard of Stripe: the routes that
 * need it answer PRECONDITION_FAILED naming the variables, rather than the
 * process refusing to start over a feature that installation does not want.
 */
export function stripeConfigFrom(env: Record<string, string | undefined>): {
  config: StripeConfig | null
  summary: string
} {
  const secretKey = env.AF_STRIPE_SECRET_KEY ?? ''
  const webhookSecret = env.AF_STRIPE_WEBHOOK_SECRET ?? ''
  const team = env.AF_STRIPE_PRICE_TEAM ?? ''
  const enterprise = env.AF_STRIPE_PRICE_ENTERPRISE ?? ''

  const missing = [
    ['AF_STRIPE_SECRET_KEY', secretKey],
    ['AF_STRIPE_WEBHOOK_SECRET', webhookSecret],
    ['AF_STRIPE_PRICE_TEAM', team],
    ['AF_STRIPE_PRICE_ENTERPRISE', enterprise],
  ]
    .filter(([, value]) => !value)
    .map(([name]) => name as string)

  if (missing.length === PLAN_ENV_COUNT) {
    return { config: null, summary: 'billing is off: no Stripe configuration (AF_STRIPE_SECRET_KEY is not set)' }
  }
  if (missing.length > 0) {
    // Partially configured is the dangerous state and it is called out rather
    // than quietly treated as off: an operator who set three of four believes
    // billing works, and the one they missed is usually the webhook secret,
    // which fails only when a real customer pays.
    return {
      config: null,
      summary: `billing is OFF and partially configured: ${missing.join(', ')} not set`,
    }
  }

  return {
    config: {
      secretKey,
      webhookSecret,
      prices: { team, enterprise },
      ...(env.AF_STRIPE_API_BASE ? { apiBase: env.AF_STRIPE_API_BASE } : {}),
    },
    summary: `billing is on: prices for ${PAID_PLANS.join(' and ')} are configured`,
  }
}

const PLAN_ENV_COUNT = 4

/**
 * The plan a price sells, or null.
 *
 * Null rather than a fallback to free. A price this control plane does not
 * recognise means somebody bought something through a link nobody here
 * configured, and silently entitling them to the free plan would take away
 * capacity they just paid for. The caller records the subscription and leaves
 * the plan alone, which is the direction that does not punish the customer.
 */
export function planForPrice(config: StripeConfig, priceId: string | null): PaidPlan | null {
  if (!priceId) return null
  for (const plan of PAID_PLANS) {
    if (config.prices[plan] === priceId) return plan
  }
  return null
}

/**
 * What an organization's plan should be, given a subscription's status.
 *
 * This is the dunning decision, and it is deliberately not a downgrade on the
 * first failed payment.
 *
 * Stripe retries a failed charge on its own schedule and emails the customer
 * while it does. Cutting the plan at `past_due` would take somebody's
 * environments away on the day their card expired, hours before the retry that
 * was going to succeed. So a subscription that is late keeps what it has, and
 * the plan drops only when Stripe has given up and the subscription is over.
 * `unpaid` is included in that: it is the status Stripe leaves a subscription
 * in when the retries are exhausted and the account is configured not to
 * cancel, which is the same fact as cancelled for entitlement purposes.
 */
export function planForStatus(status: string, entitled: PaidPlan | null): string | null {
  if (ENTITLING_STATUSES.includes(status)) return entitled ?? DEFAULT_PLAN
  if (ENDED_STATUSES.includes(status)) return DEFAULT_PLAN
  // A status Stripe added since this was written. Null means "change nothing":
  // moving somebody's plan on a word nobody has read would be a guess about
  // their money in a direction that is wrong either way, and the row still
  // records the status so it can be looked at.
  return null
}

/** A subscription in one of these entitles its plan. `past_due` is here on
 *  purpose; see planForStatus for the dunning reasoning. */
export const ENTITLING_STATUSES: readonly string[] = ['trialing', 'active', 'past_due']

/** A subscription in one of these entitles nothing. `incomplete` is here
 *  because its first payment has not gone through at all, so there was never
 *  an upgrade to take away. Not exported: planForStatus is the only thing that
 *  should be asking, and a second caller comparing statuses itself is how the
 *  two lists start disagreeing. */
const ENDED_STATUSES: readonly string[] = [
  'canceled', 'unpaid', 'incomplete', 'incomplete_expired', 'paused',
]

/** A subscription in one of these exists at Stripe and is being charged for,
 *  or is about to be. What subscriptions.checkout refuses to start a second
 *  one alongside. */
export const LIVE_STATUSES: readonly string[] = [
  ...ENTITLING_STATUSES, 'unpaid', 'incomplete',
]
