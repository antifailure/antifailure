// Stripe, behind an interface, so that taking money can be tested.
//
// The interface is narrow on purpose, the same rule src/auth/github.ts states:
// six calls, each returning exactly what the application stores. A wider one
// would tempt callers into passing Stripe's response shape around, and then the
// test double has to reproduce Stripe's response shape rather than its
// behaviour, which is how a double stops being worth anything.
//
// WHAT IS DIFFERENT FROM GitHubClient, AND WHY. GitHub has a hand-written fake
// because there is no shipped GitHub simulator to point at. Stripe has one:
// `engine/internal/mockpack` answers checkout, subscribe, read and cancel
// offline, with state, and it is part of the product. So there is no
// FakeStripeClient here. The tests construct THIS class, the one that ships,
// and give it a fetch that reaches the mock pack. That is stronger than a fake,
// because a fake agrees with whatever the author believed about the response
// shape, and it is what found five defects in the pack: a checkout session
// whose url named a different session, numeric fields returned as strings, a
// cancel that discarded the customer, subscriptions whose items were always
// empty so the plan could not be read off one, and no route for a plan change
// at all.
//
// Every method returns a decoded, typed value or throws. Nothing here returns
// `any`, and nothing lets one surprising field discard a whole response: the
// decoders below read each field they need and coerce, so an unfamiliar status
// or a null where a number was expected produces a row rather than an outage.

import type { StripeConfig } from './plans.ts'

export class StripeError extends Error {
  /** Stripe's own error code when it sent one, for a log line that says which
   *  failure this was rather than which line number it reached. */
  readonly code: string | null
  /**
   * Whether Stripe ANSWERED, as opposed to never having been reached.
   *
   * This is not diagnostics. It decides whether a retry is allowed to use a
   * fresh idempotency key, and getting it wrong is a double charge in one
   * direction and a payment nobody can ever retry in the other.
   *
   * True means a response came back with a status and a body: Stripe made a
   * decision, and a refusal it made is a thing that definitively did NOT
   * happen, so a later deliberate retry is a new attempt and may have its own
   * key. False means no answer was received at all, which is the ambiguous
   * case: the request may have been executed and the response lost, so the
   * only safe retry is one carrying the SAME key, letting Stripe tell us what
   * it already did.
   *
   * Defaults to false, so a new throw site added later is treated as
   * ambiguous until somebody has thought about it. The safe default is the one
   * that cannot double charge.
   */
  readonly answered: boolean
  constructor(message: string, code: string | null = null, answered = false) {
    super(message)
    this.code = code
    this.answered = answered
  }
}

export interface StripeCustomer {
  id: string
  email: string | null
}

export interface StripeSubscription {
  id: string
  customerId: string
  /** Provider creation time, never the time a webhook happened to arrive. */
  createdAt?: Date | null
  /** The subscription item the price hangs off.
   *
   *  Carried because changing a plan means REPLACING that item's price, and an
   *  update that omits the item id adds a second item instead: the customer is
   *  then billed for both plans at once, which is the most expensive way this
   *  integration could be wrong. Null when Stripe sent no items, which is the
   *  same condition that leaves priceId null. */
  itemId: string | null
  /** Stripe's own status vocabulary, stored as sent. */
  status: string
  priceId: string | null
  /**
   * What Stripe says the subscription item's quantity is. A RECORD, never an
   * input to a decision.
   *
   * Nothing this control plane sells has a quantity: checkout sends none and
   * the plan alone decides what an organization may hold. Stripe still reports
   * one on every subscription object, so it is read and stored as sent, which
   * is what lets an operator chasing an invoice see what was actually billed.
   * The moment it is allowed to entitle anything, an organization's limits
   * become whatever a Stripe dashboard was last edited to say.
   */
  quantity: number
  currentPeriodStart: Date | null
  currentPeriodEnd: Date | null
  cancelAtPeriodEnd: boolean
  canceledAt: Date | null
}

export interface StripeCheckoutSession {
  id: string
  /** Where the browser is sent. Stripe puts the session's own id in it. */
  url: string
  status: string
  customerId: string | null
}

export interface StripeInvoice {
  id: string
  customerId: string
  subscriptionId: string | null
  number: string | null
  status: string
  amountDue: number
  amountPaid: number
  currency: string
  hostedInvoiceUrl: string | null
  periodStart: Date | null
  periodEnd: Date | null
}


/**
 * A refund. Money that has left, which is why every field here is read back
 * from the provider rather than assumed from what was asked for: a partial
 * refund can be smaller than requested, and recording the requested amount as
 * the refunded one puts a number in the ledger that the bank statement
 * disagrees with.
 */
export interface StripeRefund {
  id: string
  chargeId: string | null
  paymentIntentId: string | null
  amount: number
  currency: string
  status: string
  reason: string | null
}

/** One movement of a customer's credit balance.
 *
 *  Stripe's sign convention is the opposite of the intuitive one and getting it
 *  backwards bills somebody instead of crediting them: a NEGATIVE amount is
 *  credit the customer may spend, and a positive amount is money they owe.
 *  `creditCustomer` below takes a positive number and negates it, so no caller
 *  has to hold that in their head. */
export interface StripeBalanceTransaction {
  id: string
  customerId: string | null
  amount: number
  currency: string
  /** The customer's balance after this movement, in Stripe's own sign. */
  endingBalance: number
  description: string | null
}

/** What a customer looks like when the admin surface is reading rather than
 *  creating one. `balance` is Stripe's sign again: negative is credit. */
export interface StripeCustomerDetail extends StripeCustomer {
  balance: number
  currency: string | null
  delinquent: boolean
  /** The coupon currently applied, when one is. */
  discountCoupon: string | null
}

export interface StripeCharge {
  id: string
  amount: number
  amountRefunded: number
  currency: string
  status: string
  paid: boolean
  refunded: boolean
  disputed: boolean
  created: Date | null
  invoiceId: string | null
  failureMessage: string | null
}

export interface StripeClient {
  /** Creates the customer an organization is billed as. */
  createCustomer(input: {
    email: string | null
    name: string
    /** The organization, carried on the customer so that a person looking at
     *  Stripe can tell whose it is without a second lookup. */
    orgId: string
  }): Promise<StripeCustomer>

  /**
   * The hosted page somebody buys on.
   *
   * No quantity, and that is the interface saying so. What this product sells
   * is one hosted control plane for one organization, at a flat fee: the plan
   * decides how many members the organization may hold, and buying a bigger
   * number bought nothing. Sending a per unit quantity was how an organization
   * could be charged two hundred times over for a limit that never moved.
   */
  createCheckoutSession(input: {
    customerId: string
    priceId: string
    orgId: string
    successUrl: string
    cancelUrl: string
  }): Promise<StripeCheckoutSession>

  /** The hosted page somebody changes a plan, a card, or a cancellation on. */
  createPortalSession(input: { customerId: string; returnUrl: string }): Promise<{ url: string }>
  /**
   * Changes where the receipts go.
   *
   * Stripe puts this address on every invoice and every receipt it sends, so a
   * billing contact recorded only in this database would be a setting that
   * looks changed and changes nothing a customer's finance department ever
   * sees. This is the write that makes it real.
   */
  updateCustomerEmail(customerId: string, email: string): Promise<StripeCustomer>

  /**
   * One subscription by id, or null when Stripe says it has never heard of it.
   *
   * This is NOT redundant with listSubscriptions, and the difference decides
   * whether a deletion proceeds or stops. An empty list cannot tell "Stripe has
   * already forgotten this subscription" from "Stripe would not answer": both
   * arrive as nothing. A resumed deletion asks about ONE subscription it
   * already holds the id of, and only a positive "no such object" lets it treat
   * the cancellation as already done. See enterprise/deletion.ts.
   */
  getSubscription(id: string): Promise<StripeSubscription | null>

  /** Every subscription Stripe holds for one customer. This is the recovery
   *  path when the creation webhook never arrived and no local row exists yet. */
  listSubscriptions(customerId: string, limit: number): Promise<StripeSubscription[]>

  /** Cancels at the end of the paid period. */
  cancelSubscription(id: string): Promise<StripeSubscription>

  /** The invoices for one customer, newest first. */
  listInvoices(customerId: string, limit: number): Promise<StripeInvoice[]>

  // -------------------------------------------------------------------------
  // The administrative writes.
  //
  // Every one of them takes an idempotency key as its LAST argument, required
  // rather than optional, and that is the point of the signature. An optional
  // key is a key somebody forgets, and the thing they forget it on is a refund.
  // Making it required means a caller cannot reach Stripe with money in the
  // request without having thought about which key this is.
  // -------------------------------------------------------------------------

  /** One customer with the fields the admin screens read. */
  getCustomer(id: string): Promise<StripeCustomerDetail | null>

  /** The charges for one customer, newest first, for the screen somebody picks
   *  a refund off. */
  listCharges(customerId: string, limit: number): Promise<StripeCharge[]>

  /** One charge by id, or null when Stripe has never heard of it.
   *
   *  Not the same question as listCharges, and the difference decides whether a
   *  refund proceeds. A refund is authorised against ONE charge whose id the
   *  operator holds; finding it by listing a customer's charges would need the
   *  customer, would page, and would silently offer to refund nothing when the
   *  charge is older than the page. */
  getCharge(id: string): Promise<StripeCharge | null>

  /** One invoice by id, for reading back what a retry did. */
  getInvoice(id: string): Promise<StripeInvoice | null>

  /**
   * Refunds a charge, in whole or in part.
   *
   * `amountMinor` absent means the whole charge, which is Stripe's own default
   * and the one a caller means when they have not said. A partial refund is
   * capped by Stripe at what is left, so a double refund of half a charge is
   * refused at the provider even if it somehow reached it twice.
   */
  refund(
    input: {
      chargeId?: string | null
      paymentIntentId?: string | null
      amountMinor?: number | null
      /** Stripe's vocabulary: duplicate, fraudulent, requested_by_customer. */
      reason?: string | null
    },
    idempotencyKey: string,
  ): Promise<StripeRefund>

  /**
   * Puts credit on a customer's account.
   *
   * `amountMinor` is POSITIVE and is the credit the customer gets. The sign is
   * flipped inside, once, rather than at every call site; see
   * StripeBalanceTransaction for why that matters.
   */
  creditCustomer(
    customerId: string,
    input: { amountMinor: number; currency: string; description: string },
    idempotencyKey: string,
  ): Promise<StripeBalanceTransaction>

  /**
   * Changes a subscription.
   *
   * A parameter map rather than a method per action, because the actions the
   * admin surface needs, changing the price, extending a trial, cancelling,
   * reactivating and applying a coupon, are all the same Stripe call with
   * different fields, and five methods over one endpoint would be five places
   * for the idempotency key to be handled differently. The callers in
   * admin/money.ts each build one map and name what they are doing.
   */
  updateSubscription(
    id: string,
    params: Record<string, string>,
    idempotencyKey: string,
  ): Promise<StripeSubscription>

  /** Attempts payment on an open invoice. The retry an operator presses after
   *  a customer has fixed their card. */
  payInvoice(id: string, idempotencyKey: string): Promise<StripeInvoice>

  /** Sends the invoice to the customer's billing address again. */
  sendInvoice(id: string, idempotencyKey: string): Promise<StripeInvoice>
}

/** The real client. In tests it is also the client under test, with `fetch`
 *  pointed at the engine's Stripe mock pack. */
export class RealStripeClient implements StripeClient {
  private readonly config: StripeConfig

  constructor(config: StripeConfig) {
    this.config = config
  }

  async createCustomer(input: {
    email: string | null
    name: string
    orgId: string
  }): Promise<StripeCustomer> {
    const body = new URLSearchParams({ name: input.name })
    if (input.email) body.set('email', input.email)
    // On the customer as well as on the checkout session, because a person in
    // the Stripe dashboard chasing a failed payment needs to know which
    // organization to contact and the session is gone by then.
    body.set('metadata[org_id]', input.orgId)
    // Keyed on the organization, which is the one place a duplicate is
    // permanent rather than untidy.
    //
    // The customer is created at Stripe and the local row is written after it,
    // in a transaction that can fail. Without this, the retry creates a SECOND
    // Stripe customer for the same organization, the first is orphaned with
    // nothing pointing at it, and an organization that ends up billed twice has
    // two customers that both look real in the dashboard. Stripe returns the
    // first customer for a repeated key, so the retry converges instead.
    //
    // Not used on the checkout session: Stripe returns the SAME session for a
    // repeated key, so an organization that cancelled and came back would be
    // sent to a stale expired page forever.
    return customerOf(await this.post('/v1/customers', body, `af-customer-${input.orgId}`))
  }

  async createCheckoutSession(input: {
    customerId: string
    priceId: string
    orgId: string
    successUrl: string
    cancelUrl: string
  }): Promise<StripeCheckoutSession> {
    const body = new URLSearchParams({
      mode: 'subscription',
      customer: input.customerId,
      'line_items[0][price]': input.priceId,
      // `line_items[0][quantity]` is deliberately absent rather than set to 1.
      //
      // Stripe treats the parameter as optional and bills a licensed recurring
      // price once when it is omitted, which is exactly the flat organization
      // fee this sells. Omitting it also keeps the call valid if a price is
      // ever made metered, because Stripe REFUSES a quantity on a metered
      // price. A hardcoded 1 would still be declaring a per unit purchase of
      // one, which is the shape that let a caller ask for two hundred.
      //
      // test/billing.test.ts asserts this parameter is not in the body that
      // reaches Stripe, so putting it back turns a suite red rather than an
      // invoice large.
      success_url: input.successUrl,
      cancel_url: input.cancelUrl,
      // Both, and deliberately. client_reference_id is what the completed
      // session carries back, and metadata is what survives onto the objects
      // the session creates. A webhook that could only find the organization
      // one way would be a webhook that fails whenever that way is absent.
      client_reference_id: input.orgId,
      'metadata[org_id]': input.orgId,
      'subscription_data[metadata][org_id]': input.orgId,
    })
    return checkoutOf(await this.post('/v1/checkout/sessions', body))
  }

  async createPortalSession(input: {
    customerId: string
    returnUrl: string
  }): Promise<{ url: string }> {
    const body = new URLSearchParams({
      customer: input.customerId,
      return_url: input.returnUrl,
    })
    const session = await this.post('/v1/billing_portal/sessions', body)
    const url = text(session.url)
    if (!url) throw new StripeError('Stripe returned a portal session with no address to send anybody to.')
    return { url }
  }

  async updateCustomerEmail(customerId: string, email: string): Promise<StripeCustomer> {
    const body = new URLSearchParams({ email })
    return customerOf(await this.post(`/v1/customers/${encodeURIComponent(customerId)}`, body))
  }

  async getSubscription(id: string): Promise<StripeSubscription | null> {
    const found = await this.get(`/v1/subscriptions/${encodeURIComponent(id)}`)
    // A subscription Stripe has never heard of is a real answer here rather
    // than a failure: reconciliation asks about a row that may have been
    // created against a different Stripe account, and throwing would stop the
    // sweep at the first bad row instead of fixing the rest.
    if (found === null) return null
    return subscriptionOf(found)
  }

  async listSubscriptions(customerId: string, limit: number): Promise<StripeSubscription[]> {
    const query = new URLSearchParams({
      customer: customerId,
      status: 'all',
      limit: String(limit),
    })
    const page = await this.get(`/v1/subscriptions?${query.toString()}`)
    if (page === null || !Array.isArray(page.data)) return []

    const out: StripeSubscription[] = []
    for (const item of page.data) {
      // One malformed subscription must not hide the other subscriptions. The
      // identifier and customer are strict inside subscriptionOf; this boundary
      // contains that failure to the one element that caused it.
      if (item === null || typeof item !== 'object') continue
      try {
        const subscription = subscriptionOf(item as Record<string, unknown>)
        // Stripe applies the customer filter. Checking it again means a broken
        // proxy or simulator cannot attach another customer's subscription.
        if (subscription.customerId === customerId) out.push(subscription)
      } catch {
        continue
      }
    }
    return out
  }

  async cancelSubscription(id: string): Promise<StripeSubscription> {
    // Deletes at the end of the paid period rather than immediately. Somebody
    // who cancels on day two of a month they have paid for keeps the month;
    // taking it away immediately is a refund question nobody asked.
    const body = new URLSearchParams({ cancel_at_period_end: 'true' })
    return subscriptionOf(await this.post(`/v1/subscriptions/${encodeURIComponent(id)}`, body))
  }

  async listInvoices(customerId: string, limit: number): Promise<StripeInvoice[]> {
    const query = new URLSearchParams({ customer: customerId, limit: String(limit) })
    const page = await this.get(`/v1/invoices?${query.toString()}`)
    if (page === null) return []
    const data = page.data
    // A list whose data is not a list is a shape this code will not guess at.
    if (!Array.isArray(data)) return []
    const out: StripeInvoice[] = []
    for (const item of data) {
      // Per element. One malformed invoice must not discard the other
      // eleven: a billing page that renders empty because of one row is a
      // support ticket that looks like data loss.
      if (item === null || typeof item !== 'object') continue
      const invoice = invoiceOf(item as Record<string, unknown>)
      if (invoice) out.push(invoice)
    }
    return out
  }


  // -------------------------------------------------------------------------
  // The administrative writes
  // -------------------------------------------------------------------------

  async getCustomer(id: string): Promise<StripeCustomerDetail | null> {
    const found = await this.get(`/v1/customers/${encodeURIComponent(id)}`)
    if (found === null) return null
    // A customer Stripe has deleted comes back as an object with deleted:true
    // and almost nothing else. Treating it as a customer would render a
    // balance of zero on a screen beside somebody's name, which reads as a
    // paid-up account rather than as one that no longer exists.
    if (found.deleted === true) return null
    return {
      ...customerOf(found),
      balance: count(found.balance, 0),
      currency: text(found.currency),
      delinquent: found.delinquent === true,
      discountCoupon: couponOf(found.discount),
    }
  }

  async listCharges(customerId: string, limit: number): Promise<StripeCharge[]> {
    const query = new URLSearchParams({ customer: customerId, limit: String(limit) })
    const page = await this.get(`/v1/charges?${query.toString()}`)
    if (page === null || !Array.isArray(page.data)) return []
    const out: StripeCharge[] = []
    for (const item of page.data) {
      // Per element, the same rule listInvoices states: one unreadable charge
      // must not empty the screen somebody picks a refund off, because an
      // empty refund screen reads as "there is nothing to refund".
      if (item === null || typeof item !== 'object') continue
      const charge = chargeOf(item as Record<string, unknown>)
      if (charge) out.push(charge)
    }
    return out
  }

  async getCharge(id: string): Promise<StripeCharge | null> {
    const found = await this.get(`/v1/charges/${encodeURIComponent(id)}`)
    return found === null ? null : chargeOf(found)
  }

  async getInvoice(id: string): Promise<StripeInvoice | null> {
    const found = await this.get(`/v1/invoices/${encodeURIComponent(id)}`)
    return found === null ? null : invoiceOf(found)
  }

  async refund(
    input: {
      chargeId?: string | null
      paymentIntentId?: string | null
      amountMinor?: number | null
      reason?: string | null
    },
    idempotencyKey: string,
  ): Promise<StripeRefund> {
    const body = new URLSearchParams()
    if (input.chargeId) body.set('charge', input.chargeId)
    else if (input.paymentIntentId) body.set('payment_intent', input.paymentIntentId)
    else throw new StripeError('A refund needs a charge or a payment intent to refund.')
    // Absent means the whole charge, which is Stripe's default. Sending an
    // explicit 0 would be a refund of nothing that reports success.
    if (typeof input.amountMinor === 'number') {
      if (!Number.isInteger(input.amountMinor) || input.amountMinor <= 0) {
        throw new StripeError('A partial refund has to be a positive whole number of minor units.')
      }
      body.set('amount', String(input.amountMinor))
    }
    if (input.reason) body.set('reason', input.reason)
    return refundOf(await this.post('/v1/refunds', body, idempotencyKey))
  }

  async creditCustomer(
    customerId: string,
    input: { amountMinor: number; currency: string; description: string },
    idempotencyKey: string,
  ): Promise<StripeBalanceTransaction> {
    if (!Number.isInteger(input.amountMinor) || input.amountMinor <= 0) {
      throw new StripeError('Credit has to be a positive whole number of minor units.')
    }
    const body = new URLSearchParams({
      // Negated HERE, once. Stripe reads a negative balance as credit the
      // customer may spend and a positive one as money they owe, so passing
      // the caller's positive number through unchanged would bill somebody for
      // the apology they were being given.
      amount: String(-input.amountMinor),
      currency: input.currency,
      description: input.description,
    })
    return balanceTransactionOf(
      await this.post(
        `/v1/customers/${encodeURIComponent(customerId)}/balance_transactions`,
        body,
        idempotencyKey,
      ),
    )
  }

  async updateSubscription(
    id: string,
    params: Record<string, string>,
    idempotencyKey: string,
  ): Promise<StripeSubscription> {
    const body = new URLSearchParams(params)
    return subscriptionOf(
      await this.post(`/v1/subscriptions/${encodeURIComponent(id)}`, body, idempotencyKey),
    )
  }

  async payInvoice(id: string, idempotencyKey: string): Promise<StripeInvoice> {
    const paid = await this.post(
      `/v1/invoices/${encodeURIComponent(id)}/pay`, new URLSearchParams(), idempotencyKey,
    )
    const invoice = invoiceOf(paid)
    if (!invoice) throw new StripeError(`Stripe answered the payment of ${id} with no invoice.`)
    return invoice
  }

  async sendInvoice(id: string, idempotencyKey: string): Promise<StripeInvoice> {
    const sent = await this.post(
      `/v1/invoices/${encodeURIComponent(id)}/send`, new URLSearchParams(), idempotencyKey,
    )
    const invoice = invoiceOf(sent)
    if (!invoice) throw new StripeError(`Stripe answered the sending of ${id} with no invoice.`)
    return invoice
  }

  // -------------------------------------------------------------------------

  private base(): string {
    return this.config.apiBase ?? 'https://api.stripe.com'
  }

  private call(): typeof globalThis.fetch {
    return this.config.fetch ?? globalThis.fetch
  }

  private async post(
    path: string,
    body: URLSearchParams,
    idempotencyKey?: string,
  ): Promise<Record<string, unknown>> {
    const res = await this.call()(new URL(path, this.base()), {
      method: 'POST',
      headers: {
        authorization: `Bearer ${this.config.secretKey}`,
        'content-type': 'application/x-www-form-urlencoded',
        // Pinned. An account whose default version moves is an account whose
        // responses change shape under a running deployment.
        'stripe-version': STRIPE_API_VERSION,
        ...(idempotencyKey ? { 'idempotency-key': idempotencyKey } : {}),
      },
      body: body.toString(),
    })
    const parsed = await decode(res, path)
    if (!parsed) throw new StripeError(`Stripe answered ${path} with ${res.status} and no body.`)
    return parsed
  }

  /** Null for a 404, which several callers treat as an answer. */
  private async get(path: string): Promise<Record<string, unknown> | null> {
    const res = await this.call()(new URL(path, this.base()), {
      headers: {
        authorization: `Bearer ${this.config.secretKey}`,
        'stripe-version': STRIPE_API_VERSION,
      },
    })
    if (res.status === 404) {
      // Read and discarded, so the connection is not left holding a body.
      await res.text()
      return null
    }
    return decode(res, path)
  }
}

/** The version every request pins. Moving it is a deliberate change with a
 *  reading of Stripe's changelog behind it, not something an account setting
 *  does to a running deployment. */
export const STRIPE_API_VERSION = '2024-06-20'

async function decode(res: Response, path: string): Promise<Record<string, unknown> | null> {
  const raw = await res.text()
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    throw new StripeError(
      `Stripe answered ${path} with ${res.status} and something that is not JSON.`,
    )
  }
  if (parsed === null || typeof parsed !== 'object') {
    throw new StripeError(`Stripe answered ${path} with ${res.status} and a body that is not an object.`)
  }
  const body = parsed as Record<string, unknown>
  if (!res.ok) {
    const error = body.error as { message?: unknown; code?: unknown } | undefined
    throw new StripeError(
      `Stripe refused ${path} (${res.status}): ${text(error?.message) ?? 'no reason given'}`,
      text(error?.code),
      // Answered: a status and a body came back, so Stripe decided.
      true,
    )
  }
  return body
}

// ---------------------------------------------------------------------------
// Decoders
//
// Written by hand rather than generated, and tolerant on purpose. Stripe adds
// fields, and an application that refuses a response carrying one it has not
// seen is an application that goes down when its provider ships. What is NOT
// tolerated is a missing identifier: an object with no id cannot be stored
// against anything and guessing one would attach a payment to the wrong row.
// ---------------------------------------------------------------------------

function text(v: unknown): string | null {
  return typeof v === 'string' && v !== '' ? v : null
}

/** Seconds since the epoch, as Stripe sends them, or null.
 *
 *  Tolerates the string form as well as the number. Stripe sends a number; the
 *  engine's mock pack sent a string until this integration was written against
 *  it, and a decoder that only accepted one of them would have hidden that. */
function instant(v: unknown): Date | null {
  const seconds = typeof v === 'number' ? v : typeof v === 'string' ? Number(v) : Number.NaN
  if (!Number.isFinite(seconds) || seconds <= 0) return null
  return new Date(seconds * 1000)
}

function count(v: unknown, fallback: number): number {
  const n = typeof v === 'number' ? v : typeof v === 'string' ? Number(v) : Number.NaN
  return Number.isFinite(n) ? Math.trunc(n) : fallback
}

/** A field Stripe sends either expanded or as an identifier. `customer` on an
 *  invoice is a string normally and an object when somebody expanded it, and a
 *  reader that assumed one shape would break the day anybody did. */
function idOf(v: unknown): string | null {
  if (typeof v === 'string') return v === '' ? null : v
  if (v !== null && typeof v === 'object') return text((v as { id?: unknown }).id)
  return null
}

function customerOf(body: Record<string, unknown>): StripeCustomer {
  const id = text(body.id)
  if (!id) throw new StripeError('Stripe returned a customer with no id.')
  return { id, email: text(body.email) }
}

function checkoutOf(body: Record<string, unknown>): StripeCheckoutSession {
  const id = text(body.id)
  const url = text(body.url)
  if (!id) throw new StripeError('Stripe returned a checkout session with no id.')
  if (!url) {
    throw new StripeError('Stripe returned a checkout session with no address to send anybody to.')
  }
  return { id, url, status: text(body.status) ?? 'open', customerId: idOf(body.customer) }
}

export function subscriptionOf(body: Record<string, unknown>): StripeSubscription {
  const id = text(body.id)
  if (!id) throw new StripeError('Stripe returned a subscription with no id.')
  const customerId = idOf(body.customer)
  if (!customerId) throw new StripeError(`Stripe returned subscription ${id} with no customer.`)

  // The plan lives on the first item's price, which is where every Stripe
  // integration reads it from. A subscription with no items cannot say what it
  // sells, so priceId is null and the caller leaves the plan alone rather than
  // guessing one.
  const items = (body.items as { data?: unknown } | undefined)?.data
  const first = Array.isArray(items) && items.length > 0 ? items[0] : null
  const item = first !== null && typeof first === 'object' ? (first as Record<string, unknown>) : null

  return {
    id,
    customerId,
    createdAt: instant(body.created),
    itemId: item ? text(item.id) : null,
    status: text(body.status) ?? 'incomplete',
    priceId: item ? idOf(item.price) : null,
    quantity: item ? count(item.quantity, 1) : 1,
    currentPeriodStart: instant(body.current_period_start),
    currentPeriodEnd: instant(body.current_period_end),
    cancelAtPeriodEnd: body.cancel_at_period_end === true,
    canceledAt: instant(body.canceled_at),
  }
}

export function invoiceOf(body: Record<string, unknown>): StripeInvoice | null {
  const id = text(body.id)
  const customerId = idOf(body.customer)
  // Null rather than a throw, so one unreadable invoice in a list does not
  // discard the rest of somebody's billing history.
  if (!id || !customerId) return null
  return {
    id,
    customerId,
    subscriptionId: idOf(body.subscription),
    number: text(body.number),
    status: text(body.status) ?? 'draft',
    amountDue: count(body.amount_due, 0),
    amountPaid: count(body.amount_paid, 0),
    currency: text(body.currency) ?? 'usd',
    hostedInvoiceUrl: text(body.hosted_invoice_url),
    periodStart: instant(body.period_start),
    periodEnd: instant(body.period_end),
  }
}

/** The coupon on a customer's discount, when Stripe sent one.
 *
 *  Two levels deep and both are optional: `discount` is null when there is no
 *  discount, and a discount can in principle carry a promotion code rather than
 *  a coupon. Reading it with two guards rather than one chained access is what
 *  stops an admin page throwing on a customer who simply has no discount. */
function couponOf(v: unknown): string | null {
  if (v === null || typeof v !== 'object') return null
  return idOf((v as { coupon?: unknown }).coupon)
}

function refundOf(body: Record<string, unknown>): StripeRefund {
  const id = text(body.id)
  // Strict, unlike the list decoders. A refund with no id is money that may
  // have moved and cannot be recorded against anything, and storing it as
  // "unknown" would put a row in the ledger that no reconciliation could ever
  // match to a Stripe object.
  if (!id) throw new StripeError('Stripe returned a refund with no id.')
  return {
    id,
    chargeId: idOf(body.charge),
    paymentIntentId: idOf(body.payment_intent),
    // Read back rather than echoed from the request: a partial refund can come
    // back smaller than it was asked for, and recording the request would put
    // a number in the ledger the bank statement disagrees with.
    amount: count(body.amount, 0),
    currency: text(body.currency) ?? 'usd',
    status: text(body.status) ?? 'pending',
    reason: text(body.reason),
  }
}

function balanceTransactionOf(body: Record<string, unknown>): StripeBalanceTransaction {
  const id = text(body.id)
  if (!id) throw new StripeError('Stripe returned a balance transaction with no id.')
  return {
    id,
    customerId: idOf(body.customer),
    amount: count(body.amount, 0),
    currency: text(body.currency) ?? 'usd',
    endingBalance: count(body.ending_balance, 0),
    description: text(body.description),
  }
}

function chargeOf(body: Record<string, unknown>): StripeCharge | null {
  const id = text(body.id)
  // Null rather than a throw, so one unreadable charge does not empty the
  // screen an operator picks a refund off.
  if (!id) return null
  return {
    id,
    amount: count(body.amount, 0),
    amountRefunded: count(body.amount_refunded, 0),
    currency: text(body.currency) ?? 'usd',
    status: text(body.status) ?? 'pending',
    paid: body.paid === true,
    refunded: body.refunded === true,
    disputed: body.disputed === true,
    created: instant(body.created),
    invoiceId: idOf(body.invoice),
    failureMessage: text(body.failure_message),
  }
}
