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
  constructor(message: string, code: string | null = null) {
    super(message)
    this.code = code
  }
}

export interface StripeCustomer {
  id: string
  email: string | null
}

export interface StripeSubscription {
  id: string
  customerId: string
  /** Stripe's own status vocabulary, stored as sent. */
  status: string
  priceId: string | null
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

export interface StripeClient {
  /** Creates the customer an organization is billed as. */
  createCustomer(input: {
    email: string | null
    name: string
    /** The organization, carried on the customer so that a person looking at
     *  Stripe can tell whose it is without a second lookup. */
    orgId: string
  }): Promise<StripeCustomer>

  /** The hosted page somebody buys on. */
  createCheckoutSession(input: {
    customerId: string
    priceId: string
    quantity: number
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

  /** What Stripe currently believes about a subscription. Null when Stripe has
   *  never heard of it, which is a real answer during reconciliation and not an
   *  error. */
  getSubscription(id: string): Promise<StripeSubscription | null>

  /** Cancels at the end of the paid period. */
  cancelSubscription(id: string): Promise<StripeSubscription>

  /** The invoices for one customer, newest first. */
  listInvoices(customerId: string, limit: number): Promise<StripeInvoice[]>
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
    quantity: number
    orgId: string
    successUrl: string
    cancelUrl: string
  }): Promise<StripeCheckoutSession> {
    const body = new URLSearchParams({
      mode: 'subscription',
      customer: input.customerId,
      'line_items[0][price]': input.priceId,
      'line_items[0][quantity]': String(input.quantity),
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
