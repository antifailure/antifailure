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
  /**
   * The price each paid plan is sold at, for the plans that HAVE one.
   *
   * Partial, and the partiality is the point. A plan can be real and still not
   * be sold self-serve: Enterprise is arranged with a person, so there is no
   * Stripe price behind it and `AF_STRIPE_PRICE_ENTERPRISE` is deliberately
   * unset. A plan missing from this map is not a misconfiguration, and the
   * routes that would have sold it say so by name rather than reaching Stripe
   * with an empty price id.
   */
  prices: Partial<Record<PaidPlan, string>>
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

  // AF_STRIPE_PRICE_ENTERPRISE IS NOT REQUIRED, and leaving it out of this
  // list is a fix rather than a relaxation.
  //
  // It used to be the fourth required variable, so a deployment that set the
  // secret, the webhook secret and the Team price and nothing else fell into
  // the "partially configured" branch below: `config` came back null, billing
  // was entirely OFF, and the Team price that DOES exist could not be sold
  // either. The startup line called it an operator error. That is the shape
  // this product actually sells in: Team has a recurring price a person can buy
  // on their own, and Enterprise is arranged with a human, so it has no price
  // and never will until somebody decides to sell it self-serve.
  //
  // A plan with no price is a plan that is not sold here. `checkout` refuses it
  // by name and points at the contact route; it is not a reason to stop taking
  // money for the plans that do have one.
  const required = [
    ['AF_STRIPE_SECRET_KEY', secretKey],
    ['AF_STRIPE_WEBHOOK_SECRET', webhookSecret],
    ['AF_STRIPE_PRICE_TEAM', team],
  ] as const
  const missing = required.filter(([, value]) => !value).map(([name]) => name)

  // Nothing set AT ALL, which is the self-hosted default and not a mistake.
  // The optional variable is checked too, so an installation that set only
  // AF_STRIPE_PRICE_ENTERPRISE is reported as half configured rather than as
  // untouched.
  if (missing.length === required.length && !enterprise) {
    return { config: null, summary: 'billing is off: no Stripe configuration (AF_STRIPE_SECRET_KEY is not set)' }
  }
  if (missing.length > 0) {
    // Partially configured is the dangerous state and it is called out rather
    // than quietly treated as off: an operator who set some of these believes
    // billing works, and the one they missed is usually the webhook secret,
    // which fails only when a real customer pays.
    return {
      config: null,
      summary: `billing is OFF and partially configured: ${missing.join(', ')} not set`,
    }
  }

  // Only the plans that have a price. An empty string is left OUT rather than
  // stored, so `prices.enterprise` is undefined instead of '', and nothing can
  // compare a subscription's missing price id against it and match.
  const prices: Partial<Record<PaidPlan, string>> = { team }
  if (enterprise) prices.enterprise = enterprise

  const sold = PAID_PLANS.filter((plan) => prices[plan])
  const arranged = PAID_PLANS.filter((plan) => !prices[plan])
  return {
    config: {
      secretKey,
      webhookSecret,
      prices,
      ...(env.AF_STRIPE_API_BASE ? { apiBase: env.AF_STRIPE_API_BASE } : {}),
    },
    // Names both halves. An operator reading "billing is on" needs to know
    // which plans that sentence covers, or the first Enterprise refusal reads
    // like an outage.
    summary:
      `billing is on: ${sold.join(' and ')} sold self-serve` +
      (arranged.length > 0 ? `, ${arranged.join(' and ')} has no price and is arranged with a person` : ''),
  }
}

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
    // `configured &&` before the comparison, and it is load bearing now that a
    // plan may have no price. Without it, a plan whose price is undefined would
    // match any priceId that is also undefined, and a subscription object Stripe
    // sent with no items would resolve to enterprise and move somebody onto the
    // largest plan for free.
    const configured = config.prices[plan]
    if (configured && configured === priceId) return plan
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
