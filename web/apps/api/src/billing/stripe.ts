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
   * Checkout buys exactly one organization subscription, and
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

/**
 * A checkout session read back, rather than one just created.
 *
 * A separate type from StripeCheckoutSession because the two answer different
 * questions and only one of them can be trusted to have an address. A session
 * that has been paid, or that has expired, carries no url at all, and the
 * status is the field a caller has to branch on before it does anything with
 * the rest. Stripe documents exactly three: open, complete and expired.
 *
 * `attemptId` is this control plane's own marker, carried in the session's
 * metadata, and it is what makes a checkout recoverable. A response lost on the
 * way back leaves a payable page at Stripe that nothing here has recorded, and
 * the only way to tell that page apart from a stranger's is a name we put on it
 * ourselves. See billing/checkout.ts.
 */
export interface StripeCheckoutState {
  id: string
  url: string | null
  status: 'open' | 'complete' | 'expired'
  customerId: string
  /** The subscription a completed session created, when it created one. */
  subscriptionId: string | null
  /** The purchase attempt this session was opened for, or null for a session
   *  opened before this control plane recorded attempts. */
  attemptId: string | null
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
    /**
     * The purchase attempt this session is for, and the only thing this method
     * takes about idempotency.
     *
     * It is deliberately NOT a free text key. The attempt is both the
     * idempotency key Stripe is sent and the marker written into the session's
     * metadata, and a caller able to pass those separately is a caller able to
     * pass two that disagree: a retry would then reach Stripe as a second
     * request, and the page it opened could not be found again by the metadata
     * either. One value, used for both, so they cannot drift.
     *
     * The attempt lives in billing_checkout_attempts, one row per organization,
     * so it survives a browser retry, a second billing owner and a restart.
     * See billing/checkout.ts.
     */
    attemptId: string
  }): Promise<StripeCheckoutSession>

  /**
   * One checkout session read back, or null when Stripe says it has never heard
   * of it.
   *
   * Null is a real answer and it is the safe direction: a session Stripe does
   * not hold cannot be paid, so it cannot be the second charge. Anything else
   * that goes wrong throws, because a read that authorises opening another
   * payable page must not be able to fail quietly.
   */
  getCheckoutSession(id: string): Promise<StripeCheckoutState | null>

  /**
   * Every checkout session for this customer that can still be paid.
   *
   * This is the one call that can see a payable page nothing here recorded: one
   * opened by a version of this control plane that kept no attempt row, or one
   * whose creation response was lost. Both are pages a customer could still put
   * a card into, so they have to be enumerable rather than inferred.
   *
   * Throws rather than returning a short list. An empty answer is read as "there
   * is nothing else payable" and acted on by opening a new page, so a list that
   * could silently omit a session would be the double charge it exists to
   * prevent.
   */
  listOpenCheckoutSessions(customerId: string): Promise<StripeCheckoutState[]>

  /**
   * Makes one open session unpayable.
   *
   * Stripe expires only a session in the `open` status and refuses anything
   * else, which is the behaviour this depends on: the refusal is how we learn
   * that the page we were about to retire was paid while we were looking at it.
   * See https://docs.stripe.com/api/checkout/sessions/expire
   */
  expireCheckoutSession(id: string): Promise<StripeCheckoutState>

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

  /**
   * Every subscription this customer holds, or a throw.
   *
   * The strict sibling of listSubscriptions, for the one caller whose answer
   * authorizes a charge: the checkout guard. listSubscriptions reads one page,
   * answers [] for a 404 and skips a row it cannot read, which is right for a
   * screen and is a double charge here, because each of those reads as "this
   * customer has no subscription". So this follows every page, and a 404, a
   * page that fails, a malformed page or an unreadable row of this customer's
   * throws instead.
   */
  listSubscriptionsForPurchase(customerId: string): Promise<StripeSubscription[]>

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
    // The checkout session's key is different in kind: it names a purchase
    // ATTEMPT rather than the organization, because Stripe returns the same
    // session for a repeated key and an organization that cancelled and came
    // back a week later must not be sent to the page it walked away from. The
    // customer's key carries no such qualifier, because there is exactly one
    // customer per organization forever.
    return customerOf(await this.post('/v1/customers', body, `af-customer-${input.orgId}`))
  }

  async createCheckoutSession(input: {
    customerId: string
    priceId: string
    orgId: string
    successUrl: string
    cancelUrl: string
    attemptId: string
  }): Promise<StripeCheckoutSession> {
    const body = new URLSearchParams({
      mode: 'subscription',
      customer: input.customerId,
      'line_items[0][price]': input.priceId,
      // A licensed recurring price requires a quantity. One buys the flat
      // organization subscription; this is never a caller supplied seat count.
      // Metered prices have a different contract and are not sold here.
      'line_items[0][quantity]': '1',
      success_url: input.successUrl,
      cancel_url: input.cancelUrl,
      // Both, and deliberately. client_reference_id is what the completed
      // session carries back, and metadata is what survives onto the objects
      // the session creates. A webhook that could only find the organization
      // one way would be a webhook that fails whenever that way is absent.
      client_reference_id: input.orgId,
      'metadata[org_id]': input.orgId,
      'subscription_data[metadata][org_id]': input.orgId,
      // The attempt, on the session itself, and it is not decoration. A
      // creation whose response is lost leaves a payable page here and nothing
      // recorded there, and this is the only field that tells that page apart
      // from one belonging to a different purchase. listOpenCheckoutSessions
      // reads it back.
      'metadata[checkout_attempt]': input.attemptId,
    })
    return checkoutOf(
      await this.post('/v1/checkout/sessions', body, checkoutKey(input.attemptId)),
    )
  }

  async getCheckoutSession(id: string): Promise<StripeCheckoutState | null> {
    const found = await this.get(`/v1/checkout/sessions/${encodeURIComponent(id)}`)
    if (found === null) return null
    const state = checkoutStateOf(found)
    // A session read back under another identifier is not the page that was
    // asked about, so nothing may be decided from it.
    if (state.id !== id) {
      throw new StripeError(
        `Stripe answered a request for checkout session ${id} with ${state.id}. No new purchase was started.`,
      )
    }
    return state
  }

  async listOpenCheckoutSessions(customerId: string): Promise<StripeCheckoutState[]> {
    const out: StripeCheckoutState[] = []
    let cursor: string | null = null
    // Bounded, because an unbounded loop against a third party is an outage
    // waiting for a provider bug. A customer holds at most one open session in
    // any state this control plane creates, so five pages of a hundred is four
    // hundred and ninety nine more than the case that exists, and the refusal
    // below is what happens rather than a quiet short list.
    for (let page = 0; page < 5; page += 1) {
      const query = new URLSearchParams({
        customer: customerId,
        // Stripe filters; the decoder checks again. A proxy or a simulator that
        // ignored the filter would otherwise have this expiring a session that
        // was already paid.
        status: 'open',
        limit: '100',
      })
      if (cursor) query.set('starting_after', cursor)
      const body = await this.get(`/v1/checkout/sessions?${query.toString()}`)
      // A collection that answers 404 is not "this customer has nothing". It is
      // an answer this code will not translate into permission to open another
      // payable page.
      if (body === null || !Array.isArray(body.data) || typeof body.has_more !== 'boolean') {
        throw new StripeError(
          'Stripe did not answer with this customer\'s checkout sessions, so it is not known ' +
            'whether one is already open. No new purchase was started.',
        )
      }
      for (const item of body.data) {
        if (item === null || typeof item !== 'object') {
          throw new StripeError(
            'Stripe returned a checkout session that cannot be read. No new purchase was started.',
          )
        }
        // NOT skipped on failure, unlike listSubscriptions and listInvoices.
        // Those render a screen, where one bad row must not empty the page.
        // This decides whether another payable page may exist, so an element
        // that cannot be read has to stop the operation instead.
        //
        // Another customer's session is passed over BEFORE the strict read. It
        // cannot be this customer's second charge, so its shape must not be
        // able to refuse this customer's purchase. Stripe filters on the
        // customer already; a simulator that ignores the filter, as the
        // engine's pack does, answers with every session it holds.
        if (idOf((item as Record<string, unknown>).customer) !== customerId) continue
        const session = checkoutStateOf(item as Record<string, unknown>)
        if (session.customerId === customerId && session.status === 'open') out.push(session)
      }
      if (!body.has_more) return out
      const last = body.data.at(-1) as { id?: unknown } | undefined
      const next = text(last?.id)
      if (!next || next === cursor) {
        throw new StripeError('Stripe\'s checkout session pagination did not advance.')
      }
      cursor = next
    }
    throw new StripeError(
      'This customer holds more open checkout sessions than this control plane will page ' +
        'through. No new purchase was started.',
    )
  }

  async expireCheckoutSession(id: string): Promise<StripeCheckoutState> {
    return checkoutStateOf(
      await this.post(`/v1/checkout/sessions/${encodeURIComponent(id)}/expire`, new URLSearchParams()),
    )
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

  async listSubscriptionsForPurchase(customerId: string): Promise<StripeSubscription[]> {
    const out: StripeSubscription[] = []
    let cursor: string | null = null
    // Bounded for the reason listOpenCheckoutSessions is, and the bound is a
    // refusal rather than a short list.
    for (let page = 0; page < 5; page += 1) {
      const query = new URLSearchParams({ customer: customerId, status: 'all', limit: '100' })
      if (cursor) query.set('starting_after', cursor)
      const body = await this.get(`/v1/subscriptions?${query.toString()}`)
      if (body === null || !Array.isArray(body.data) || typeof body.has_more !== 'boolean') {
        throw new StripeError(
          'Stripe did not answer with this customer\'s subscriptions, so it is not known whether ' +
            'one is already live. No new purchase was started.',
        )
      }
      for (const item of body.data) {
        if (item === null || typeof item !== 'object') {
          throw new StripeError('Stripe returned a subscription that cannot be read. No new purchase was started.')
        }
        // Another customer's row cannot be this customer's live subscription, so
        // its shape must not be able to refuse this purchase.
        if (idOf((item as Record<string, unknown>).customer) !== customerId) continue
        // NOT caught, unlike listSubscriptions: a row of this customer's that
        // cannot be read may be the live subscription.
        out.push(subscriptionOf(item as Record<string, unknown>))
      }
      if (!body.has_more) return out
      const next = text((body.data.at(-1) as { id?: unknown } | undefined)?.id)
      if (!next || next === cursor) {
        throw new StripeError('Stripe\'s subscription pagination did not advance. No new purchase was started.')
      }
      cursor = next
    }
    throw new StripeError(
      'This customer holds more subscriptions than this control plane will page through. No new ' +
        'purchase was started.',
    )
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

  /**
   * The request itself, with a connection that failed turned into a StripeError
   * that says so.
   *
   * Nothing used to produce the ambiguous case this class documents. A refusal
   * Stripe MADE arrived as a StripeError with `answered` true, and a connection
   * that dropped arrived as whatever fetch threw, which is a TypeError with no
   * provider vocabulary in it at all. Every caller here branches on StripeError,
   * so the dropped connection took the path meant for a programming mistake: the
   * checkout route answered a buyer "something went wrong on the control plane"
   * at the exact moment they most needed to be told that nothing was charged and
   * that pressing again resumes the same purchase rather than starting a second.
   *
   * `answered` stays FALSE here, which is the whole point of the flag: no answer
   * came back, so the request may have been executed and its response lost, and
   * only a retry carrying the SAME idempotency key is safe. The underlying
   * message is kept in the text because an operator reading a log needs to know
   * whether this was a timeout, a refused connection or a hung socket.
   */
  private async reach(path: string, url: URL, init: RequestInit): Promise<Response> {
    try {
      return await this.call()(url, init)
    } catch (err) {
      if (err instanceof StripeError) throw err
      throw new StripeError(
        `Stripe did not answer ${path}: ${err instanceof Error ? err.message : String(err)}`,
        null,
        false,
      )
    }
  }

  private async post(
    path: string,
    body: URLSearchParams,
    idempotencyKey?: string,
  ): Promise<Record<string, unknown>> {
    const res = await this.reach(path, new URL(path, this.base()), {
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
    const res = await this.reach(path, new URL(path, this.base()), {
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

/**
 * The version every OUTGOING request pins. Moving it is a deliberate change
 * with a reading of Stripe's changelog behind it, not something an account
 * setting does to a running deployment.
 *
 * THE READING HAS NOW BEEN DONE, AND THE PIN STAYS. Recorded here so the next
 * person does not repeat it.
 *
 * The question came up because this account's default version is Basil and
 * its decoders were reading two field paths Basil removed. It is worth being
 * exact about what moving the pin would and would not have fixed.
 *
 * It would have fixed NOTHING about the delivered events. A webhook is
 * delivered at the version set on its ENDPOINT, or the account default when
 * that is unset, and neither is this header. So the decoders have to read
 * both shapes whatever this constant says, and moving it cannot retire that
 * tolerance. It would only make the two directions agree.
 *
 * It would have BROKEN two things that work today, because Basil is one
 * release with many breaking changes in it and only two of them were the ones
 * being fixed:
 *
 *  - `invoice` was removed from the Charge object, by "Adds support for
 *    multiple (partial) payments on invoices". `chargeOf` reads it, and the
 *    admin console's customer view is handed the result. It would have gone
 *    silently null, which is the same class of defect being fixed here.
 *  - the singular `coupon` PARAMETER was removed from Subscription#update, by
 *    "Removes coupon and promotion code parameters with stackable discounts",
 *    in favour of `discounts`. `applyDiscount` in src/admin/money.ts sends
 *    exactly that parameter, so an operator applying a discount would have
 *    been refused.
 *
 * So moving the pin buys agreement between the two directions and costs two
 * live regressions on a money path, one of them silent, neither of them
 * tested by the change that would cause them. The trade is not worth taking
 * as a side effect of a decoder fix. Moving it is its own piece of work, and
 * the two above are its entry conditions.
 */
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

/** The idempotency key one purchase attempt reaches Stripe under. Exported for
 *  the test that asserts what is actually sent, and derived in one place so the
 *  key and the metadata on the session can never name different attempts. */
export function checkoutKey(attemptId: string): string {
  return `af-checkout-${attemptId}`
}

/**
 * A session read back, strictly.
 *
 * Strict where the rest of this file is tolerant, and the asymmetry is the
 * point: every other decoder here feeds a screen, where refusing a response
 * means an outage over a field nobody reads. This one decides whether a second
 * payable page may be opened for somebody's card, so a status this code does not
 * recognise, a missing customer or an `open` session with no address is a
 * refusal rather than a guess. Guessing in the tolerant direction here means
 * treating a real, payable page as though it were not there.
 */
function checkoutStateOf(body: Record<string, unknown>): StripeCheckoutState {
  const id = text(body.id)
  const customerId = idOf(body.customer)
  const status = text(body.status)
  const url = text(body.url)
  if (
    !id ||
    !customerId ||
    status === null ||
    !['open', 'complete', 'expired'].includes(status) ||
    (status === 'open' && !url)
  ) {
    throw new StripeError(
      `Stripe returned a checkout session whose state cannot be verified (${id ?? 'no id'}, ` +
        `${status ?? 'no status'}). No new purchase was started.`,
    )
  }
  const metadata = body.metadata
  const attemptId =
    metadata !== null && typeof metadata === 'object'
      ? text((metadata as Record<string, unknown>).checkout_attempt)
      : null
  return {
    id,
    url,
    status: status as StripeCheckoutState['status'],
    customerId,
    subscriptionId: idOf(body.subscription),
    attemptId,
  }
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
    currentPeriodStart: periodBound(body, item, 'current_period_start'),
    currentPeriodEnd: periodBound(body, item, 'current_period_end'),
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
    subscriptionId: invoiceSubscriptionOf(body),
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

/**
 * A subscription's billing period, from whichever level of the object carries it.
 *
 * Basil, `2025-03-31.basil`, REMOVED `current_period_start` and
 * `current_period_end` from the subscription and added them to each
 * subscription item, so the period on a Basil payload lives only at
 * `items.data[].current_period_start` and `items.data[].current_period_end`.
 *
 * Both shapes are live here at once, and that is not a transitional state that
 * goes away. A webhook is delivered at the version its ENDPOINT was created
 * with, which Stripe's versioning page is explicit about, and that is a
 * different setting from the `stripe-version` header this client sends. So
 * this decoder reads deliveries of one age and responses to its own requests
 * of another, and neither shape can be dropped while that is true.
 *
 * The top level is tried FIRST, and the reason is about being wrong rather
 * than about being right, because on a real payload only one of the two is
 * ever present. It is the field the version pinned on outgoing requests still
 * sends, so trying it first means this fallback cannot change what the
 * outgoing path already reads: the new branch is reached only by a payload
 * that genuinely carries no subscription level period, which is exactly the
 * Basil delivery that is null without it.
 *
 * The item is the FIRST one, the same item `priceId` and `quantity` are read
 * off above. Basil later allowed items on one subscription to bill on
 * different intervals, so reading the period off the item the price came from
 * reports one item's period rather than half of one and half of another.
 */
function periodBound(
  body: Record<string, unknown>,
  item: Record<string, unknown> | null,
  field: 'current_period_start' | 'current_period_end',
): Date | null {
  return instant(body[field]) ?? (item === null ? null : instant(item[field]))
}

/**
 * The subscription an invoice was generated by, from whichever shape carries it.
 *
 * Basil, `2025-03-31.basil`, REMOVED `subscription` from the invoice, along
 * with `quote`, `subscription_details` and `subscription_proration_date`, and
 * replaced them with one `parent` field that names the upstream object and
 * says what KIND of object it was.
 *
 * `parent.type` is checked rather than assumed, and that check is the point of
 * this function. An invoice generated by a quote carries `parent.quote_details`
 * instead, and an unguarded walk to `parent.subscription_details.subscription`
 * would be reading a field off the wrong branch of a union. Returning null
 * there is right: an invoice this control plane cannot attribute to a
 * subscription is stored with no subscription, which is recoverable, and
 * attributing it to the wrong one is not.
 *
 * Every level is guarded separately, the same rule `couponOf` below states,
 * because every level is legitimately absent: a one off invoice has no parent
 * at all.
 */
function invoiceSubscriptionOf(body: Record<string, unknown>): string | null {
  const top = idOf(body.subscription)
  if (top) return top
  const parent = body.parent
  if (parent === null || typeof parent !== 'object') return null
  const generated = parent as { type?: unknown; subscription_details?: unknown }
  if (generated.type !== 'subscription_details') return null
  const details = generated.subscription_details
  if (details === null || typeof details !== 'object') return null
  return idOf((details as { subscription?: unknown }).subscription)
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
