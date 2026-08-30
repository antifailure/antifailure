// Billing, as one thing to pass around.
//
// A client and its configuration travel together because neither is useful
// alone: the client cannot say which price sells which plan, and the
// configuration cannot talk to anybody. Null everywhere means this installation
// takes no money, which is the self-hosted default and a state every route has
// to serve rather than crash in.
//
// What is re-exported here is exactly what the process and the HTTP surface
// reach for, and nothing else. Everything inside this directory imports from
// the module that defines it, so there is one path to each symbol rather than
// two, and a re-export nobody uses cannot sit here looking like an interface.

import type { StripeConfig } from './plans.ts'
import type { StripeClient } from './stripe.ts'

export interface Billing {
  client: StripeClient
  config: StripeConfig
}

// main.ts, which reads the configuration and builds the client once.
export { stripeConfigFrom } from './plans.ts'
export { RealStripeClient } from './stripe.ts'

// server.ts, which owns the one unauthenticated endpoint this feature has.
export { handleStripeDelivery, parseStripeEvent, verifyStripeSignature } from './webhook.ts'
