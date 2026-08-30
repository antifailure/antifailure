// Billing, as one thing to pass around.
//
// A client and its configuration travel together because neither is useful
// alone: the client cannot say which price sells which plan, and the
// configuration cannot talk to anybody. Null everywhere means this installation
// takes no money, which is the self-hosted default and a state every route has
// to serve rather than crash in.

import type { StripeConfig } from './plans.ts'
import type { StripeClient } from './stripe.ts'

export interface Billing {
  client: StripeClient
  config: StripeConfig
}

export { stripeConfigFrom, planForPrice, planForStatus, PAID_PLANS, LIVE_STATUSES } from './plans.ts'
export type { PaidPlan, StripeConfig } from './plans.ts'
export { RealStripeClient, StripeError } from './stripe.ts'
export type { StripeClient, StripeSubscription, StripeInvoice } from './stripe.ts'
export {
  handleStripeDelivery,
  parseStripeEvent,
  verifyStripeSignature,
  SIGNATURE_TOLERANCE_SECONDS,
  type StripeEvent,
  type StripeOutcome,
} from './webhook.ts'
export { attachCustomer, readBillingState, reconcile } from './store.ts'
