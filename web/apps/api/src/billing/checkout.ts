// One payable checkout per organization, across replicas, retries and owners.
//
// ---------------------------------------------------------------------------
// The defect this exists for
// ---------------------------------------------------------------------------
//
// A checkout session creates no subscription. So while one is merely open there
// is nothing for the subscriptions table to refuse on, and nothing for the
// Stripe subscription read to refuse on either: both guards in
// routers/subscriptions.ts answer honestly that this organization has no
// subscription, because it does not have one yet. The only thing that ever tied
// two requests together was a thirty second idempotency bucket, so two billing
// owners who pressed Subscribe a minute apart each got their own hosted page,
// and BOTH pages stayed payable for the next twenty four hours. Two paid pages
// are two subscriptions on one Stripe customer and two charges on one card.
//
// WHY NOT A DATABASE CONSTRAINT ON subscriptions. Because the charge does not
// happen here. migrations/0020_billing.sql argues this at length and the
// argument is still right: a unique index on the subscriptions table would
// refuse to RECORD a subscription Stripe has already created and charged for,
// so the webhook carrying it would raise, answer 500, and be retried into the
// same refusal until somebody deployed. That loses the delivery rather than the
// charge, and it would also fire on a legitimate ordering, an organization
// resubscribing before the cancellation of the old subscription arrives.
//
// So the invariant is enforced one step earlier, on the only thing this database
// legitimately owns: the ATTEMPT. One row per organization in
// billing_checkout_attempts, claimed under the organization's own lock, and one
// hosted page per attempt. The charge stays at Stripe and nothing here ever
// refuses to write down something Stripe has done.
//
// ---------------------------------------------------------------------------
// How a page stops being payable
// ---------------------------------------------------------------------------
//
// Reusing the open session is most of it: a second press, a second owner or a
// second replica is handed the page that already exists rather than a new one,
// and a Stripe checkout session can be completed only once. The rest is the
// cases where reuse is wrong, and each one ends with the old page EXPIRED at
// Stripe before a new one exists, never with two open at the same time:
//
//   the buyer changed plan         the open page is expired, then replaced
//   the return addresses differ    the same
//   the page expired on its own    replaced
//   the page was paid              refused, unless the subscription it created
//                                  is affirmatively over
//   Stripe has never heard of it   refused until Refresh from Stripe clears it: a
//                                  404 is also what a rotated account key
//                                  answers, and the page may still be payable
//                                  in the account it was opened in
//
// THE SESSIONS NOTHING HERE RECORDED. Every organization that had a checkout
// open when this shipped has a payable page that no attempt row names, and so
// does any request whose creation response was lost on the way back. Both are
// pages a customer can still put a card into. So before any new page is created,
// Stripe is asked for this customer's open sessions, the one belonging to this
// attempt is adopted if it is there, and every other one is expired. That is the
// step that makes the invariant true of the state this deploy inherits rather
// than only of the state it creates.
//
// ---------------------------------------------------------------------------
// What this deliberately does not do
// ---------------------------------------------------------------------------
//
// No Stripe call happens while the transaction holds a lock. The claim is one
// short transaction, the provider work happens after it commits, and the session
// identifier is written back in a second short transaction that names the
// attempt it belongs to, so a request whose attempt was retired underneath it
// writes nothing and looks again.
//
// Nothing here waits for a webhook. The attempt's state is read from Stripe at
// the moment it is needed, because an event that has already been delivered is
// not coming again, and a row created after it would wait forever.

import { sql } from 'drizzle-orm'
import { TRPCError } from '@trpc/server'
import type { OrgContext } from '../trpc.ts'
import type { Billing } from './index.ts'
import { TERMINAL_STATUSES } from './plans.ts'
import { StripeError, type StripeCheckoutState } from './stripe.ts'

/** The attempt row, as the database holds it. */
interface Attempt extends Record<string, unknown> {
  attempt_id: string
  stripe_customer_id: string
  price_id: string
  success_url: string
  cancel_url: string
  stripe_session_id: string | null
}

export interface CheckoutInput {
  customerId: string
  priceId: string
  successUrl: string
  cancelUrl: string
}

/**
 * How many times one request may claim an attempt.
 *
 * Every path through the loop either answers or retires the attempt it was
 * looking at, and a retired attempt is replaced by a fresh one that answers on
 * the next pass, so two is the most any single case needs. Three leaves room for
 * one concurrent retirement by somebody else, and the refusal after it tells the
 * buyer to press again rather than pretending the loop can go on forever.
 */
const ROUNDS = 3

/** How long to wait for the request that holds this attempt's idempotency key
 *  to finish, before looking again for the page it opened. */
const CONFLICT_WAIT_MS = 250

function refuse(message: string): never {
  throw new TRPCError({ code: 'PRECONDITION_FAILED', message })
}

/** Every refusal that means "somebody has already paid", in one place, because
 *  the person reading it has just been told no immediately after paying. */
const ALREADY_PAID =
  'This organization has already completed a checkout. Nothing was charged again and no second ' +
  'purchase was started. Use Refresh from Stripe to see the subscription, or manage it in the ' +
  'billing portal.'

/**
 * Opens the one checkout this organization is allowed to have, or refuses.
 *
 * The caller has already refused a live subscription, locally and at Stripe.
 * This is about the window those two guards cannot see: the minutes between a
 * page being opened and anybody paying for it.
 */
export async function checkoutOnce(
  c: OrgContext,
  billing: Billing,
  input: CheckoutInput,
): Promise<{ url: string; sessionId: string }> {
  let retire: string | null = null
  for (let round = 0; round < ROUNDS; round += 1) {
    const claimed = await claim(c, input, retire)
    const attempt = claimed.attempt
    retire = null

    // What Stripe holds for this attempt. Either the session the row names, or,
    // when the row names none, whatever this customer has open: the listing is
    // both how a lost response is recovered and how every page that is not this
    // attempt's stops being payable.
    const session = attempt.stripe_session_id
      ? await billing.client.getCheckoutSession(attempt.stripe_session_id)
      : await adoptAndSweep(billing, input.customerId, attempt.attempt_id)

    if (session === null) {
      // Nothing payable belongs to this attempt.
      //
      // A row that names a session Stripe does not hold is REFUSED, not
      // replaced. Stripe answers 404 for a session in a different account just
      // as it does for one that never existed, so after a key rotation the page
      // this row names can still be paid in the account it was opened in, and
      // opening a new one here would be a second payable page. An operator
      // reconciling the row costs a retry; guessing costs a second charge.
      if (attempt.stripe_session_id !== null) {
        refuse(
          'Stripe has no record of the checkout this organization already opened, so no new ' +
            'purchase was started and nothing was charged. Use Refresh from Stripe to check it ' +
            'and clear it, then press Subscribe again.',
        )
      }
      // Terms that no longer match are retired. The stored parameters are what
      // a recovery would have to send, so opening a page from them would sell
      // the buyer the plan they asked for last time.
      if (!sameTerms(attempt, input)) {
        retire = attempt.attempt_id
        continue
      }
      const opened = await open(c, billing, input, attempt)
      if (opened === null) continue
      return opened
    }

    // A session belonging to a different customer means the attempt row and
    // Stripe disagree about who is buying, and nothing here will pick one.
    if (session.customerId !== input.customerId) {
      refuse(
        'The checkout Stripe holds for this organization names a different customer, so it was ' +
          'not reused and no new purchase was started. Ask an operator to reconcile this ' +
          'organization against Stripe.',
      )
    }

    if (session.status === 'complete') {
      // Somebody paid. The only thing that permits another purchase is the
      // subscription it created being over for good.
      await refuseUnlessOver(billing, session)
      retire = attempt.attempt_id
      continue
    }

    if (session.status === 'open') {
      if (sameTerms(attempt, input)) {
        if (!session.url) {
          // Stripe calls it open and gives nowhere to send anybody. Refusing is
          // the only honest answer: opening another page would leave this one
          // payable behind it.
          refuse(
            'Stripe holds an open checkout for this organization with no address to send anybody ' +
              'to. Nothing was charged. Ask an operator to reconcile this organization against ' +
              'Stripe before starting another purchase.',
          )
        }
        // Adoption found it rather than the row naming it, so write the
        // identifier down. A request whose attempt was retired underneath it
        // records nothing and looks again.
        if (attempt.stripe_session_id !== session.id) {
          if (!(await record(c, attempt.attempt_id, session.id))) continue
        }
        return { url: session.url, sessionId: session.id }
      }
      // A different plan, or a different place to come back to. The page that is
      // open has to stop being payable BEFORE another one exists.
      await expireOrRefuse(billing, session)
    }

    // Expired on its own, or expired by the line above.
    retire = attempt.attempt_id
  }
  refuse(
    'This organization\'s checkout changed while this request was running, so nothing was opened ' +
      'and nothing was charged. Press Subscribe again to open the current one.',
  )
}

/**
 * Makes every page this customer could still pay on unpayable.
 *
 * Called when the route has decided this organization already HAS a
 * subscription, which is the one state where an open checkout page can only
 * ever produce a second charge. The buyer whose tab is still sitting on Stripe's
 * card form is the case: the guards refuse their next press, and refusing the
 * press does nothing about the page already in front of them.
 *
 * Its failures are deliberately not the caller's problem. The refusal is the
 * answer to the request and it stands either way, so a Stripe that will not
 * answer must not turn "you already have a subscription" into a gateway error.
 * The tidy up is attempted again on the next press, and the guards that actually
 * prevent the second purchase do not depend on it having worked.
 */
export async function closeOpenCheckouts(billing: Billing, customerId: string): Promise<void> {
  const open = await billing.client.listOpenCheckoutSessions(customerId)
  for (const session of open) await billing.client.expireCheckoutSession(session.id)
}

/**
 * Claims the organization's attempt row.
 *
 * The organization row is locked first, which is the same order
 * billing/webhook.ts takes: organization, then billing rows. Two orders is a
 * deadlock between a checkout and the delivery that is confirming it.
 *
 * `retire` is a compare and swap rather than a delete. It names the attempt this
 * request decided was finished, and the row is replaced only if it is still that
 * attempt: when somebody else replaced it first, theirs is used and this request
 * does not mint a second one behind it.
 */
async function claim(
  c: OrgContext,
  input: CheckoutInput,
  retire: string | null,
): Promise<{ attempt: Attempt; fresh: boolean }> {
  return c.pool.withTenant(c.tenant, async (db) => {
    const org = await db.execute(
      sql`SELECT id FROM organizations WHERE id = ${c.actor.orgId}::uuid FOR UPDATE`,
    )
    if (org.length === 0) refuse('This organization no longer exists, so nothing was charged.')

    const held = await db.execute<Attempt>(sql`
      SELECT attempt_id, stripe_customer_id, price_id, success_url, cancel_url, stripe_session_id
      FROM billing_checkout_attempts WHERE org_id = ${c.actor.orgId}::uuid`)
    const previous = held[0]
    if (previous && previous.attempt_id !== retire) return { attempt: previous, fresh: false }

    const now = c.clock.now().toISOString()
    const rows = await db.execute<Attempt>(sql`
      INSERT INTO billing_checkout_attempts
        (org_id, stripe_customer_id, price_id, success_url, cancel_url, created_at, updated_at)
      VALUES (${c.actor.orgId}::uuid, ${input.customerId}, ${input.priceId},
              ${input.successUrl}, ${input.cancelUrl}, ${now}, ${now})
      ON CONFLICT (org_id) DO UPDATE SET
        attempt_id = gen_random_uuid(),
        stripe_customer_id = excluded.stripe_customer_id,
        price_id = excluded.price_id,
        success_url = excluded.success_url,
        cancel_url = excluded.cancel_url,
        stripe_session_id = NULL,
        created_at = excluded.created_at,
        updated_at = excluded.updated_at
      RETURNING attempt_id, stripe_customer_id, price_id, success_url, cancel_url,
                stripe_session_id`)
    return { attempt: rows[0]!, fresh: true }
  })
}

/** Records which session an attempt opened. False when the attempt is no longer
 *  the organization's current one, which is not an error: it means somebody
 *  retired it, and the caller looks again rather than writing over them. */
async function record(c: OrgContext, attemptId: string, sessionId: string): Promise<boolean> {
  const saved = await c.pool.withTenant(c.tenant, (db) =>
    db.execute(sql`
      UPDATE billing_checkout_attempts
      SET stripe_session_id = ${sessionId}, updated_at = ${c.clock.now().toISOString()}
      WHERE org_id = ${c.actor.orgId}::uuid AND attempt_id = ${attemptId}::uuid
      RETURNING attempt_id`),
  )
  return saved.length > 0
}

/**
 * Adopts this attempt's open session if Stripe has one, and expires every other
 * payable page this customer holds.
 *
 * The adoption is what recovers a creation whose response was lost: the page
 * exists at Stripe, carrying the attempt in its metadata, and nothing here
 * recorded it. Finding it by name costs one call and needs no assumption about
 * how long Stripe keeps an idempotency key.
 *
 * The sweep is what makes this true of pages created before any of this existed.
 * A session with no attempt marker was opened by an earlier version of this
 * control plane; a session marked with a DIFFERENT attempt belongs to a purchase
 * that has been retired. Neither may stay payable.
 */
async function adoptAndSweep(
  billing: Billing,
  customerId: string,
  attemptId: string,
): Promise<StripeCheckoutState | null> {
  const open = await billing.client.listOpenCheckoutSessions(customerId)
  let mine: StripeCheckoutState | null = null
  for (const session of open) {
    // The first is adopted and any further one is expired. Two pages marked with
    // one attempt should not happen, and if a provider ever produces them the
    // answer is still exactly one payable page.
    if (session.attemptId === attemptId && mine === null) {
      mine = session
      continue
    }
    await expireOrRefuse(billing, session)
  }
  return mine
}

/**
 * Makes one open session unpayable, or refuses the whole operation.
 *
 * Stripe expires only an open session, so its refusal is information rather than
 * a failure: the page we were about to retire was paid while we were looking at
 * it. That is the one race this cannot design away, and the answer to it is to
 * stop, because the purchase that just completed is the one the customer made.
 */
async function expireOrRefuse(billing: Billing, session: StripeCheckoutState): Promise<void> {
  try {
    const after = await billing.client.expireCheckoutSession(session.id)
    if (after.status === 'expired') return
    // Stripe answered and the page is not expired. Never observed, and not
    // something to carry on from: carrying on opens a second payable page.
    refuse(
      'Stripe would not close the checkout page this organization already has open, so no second ' +
        'one was started and nothing was charged. Press Subscribe again in a moment.',
    )
  } catch (err) {
    if (err instanceof TRPCError) throw err
    if (!(err instanceof StripeError)) throw err
    // Why it refused decides everything, so ask Stripe what the session is now.
    const now = await billing.client.getCheckoutSession(session.id)
    if (now === null || now.status === 'expired') return
    if (now.status === 'complete') refuse(ALREADY_PAID)
    refuse(
      'Stripe would not close the checkout page this organization already has open, so no second ' +
        'one was started and nothing was charged. Press Subscribe again in a moment.',
    )
  }
}

/**
 * Refuses unless the subscription a completed checkout created is over for good.
 *
 * A completed session is a purchase, and the webhook carrying its subscription
 * may not have arrived, so the local tables cannot answer this. Stripe is asked
 * about the subscription the session itself names, and only a status nothing can
 * move out of permits the attempt to be replaced. Anything else, including a
 * subscription Stripe will not talk about, is a refusal: the direction that
 * costs a customer a retry is better than the direction that charges them twice.
 */
async function refuseUnlessOver(billing: Billing, session: StripeCheckoutState): Promise<void> {
  if (!session.subscriptionId) {
    refuse(
      'A checkout for this organization has completed and Stripe has not yet named the ' +
        'subscription it created. Nothing was charged again. Use Refresh from Stripe in a ' +
        'moment, or ask an operator to reconcile this organization.',
    )
  }
  const subscription = await billing.client.getSubscription(session.subscriptionId)
  if (subscription === null) refuse(ALREADY_PAID)
  // Stripe answering about a different subscription than the one asked for is
  // not an answer about this purchase, whatever status it carries.
  if (subscription.id !== session.subscriptionId) refuse(ALREADY_PAID)
  if (subscription.customerId !== session.customerId) refuse(ALREADY_PAID)
  if (!TERMINAL_STATUSES.includes(subscription.status)) refuse(ALREADY_PAID)
}

/** Opens the page, and records it. Null when the attempt was retired while
 *  Stripe was answering, which the caller resolves by looking again. */
async function open(
  c: OrgContext,
  billing: Billing,
  input: CheckoutInput,
  attempt: Attempt,
): Promise<{ url: string; sessionId: string } | null> {
  let session
  try {
    session = await billing.client.createCheckoutSession({
      customerId: attempt.stripe_customer_id,
      priceId: attempt.price_id,
      orgId: c.actor.orgId,
      successUrl: attempt.success_url,
      cancelUrl: attempt.cancel_url,
      attemptId: attempt.attempt_id,
    })
  } catch (err) {
    // TWO REPLICAS INSIDE THE SAME MOMENT, and Stripe is the one that notices.
    //
    // Both requests hold the same attempt, so both send the same idempotency
    // key, and Stripe answers the second with `idempotency_key_in_use` while the
    // first is still executing. It saves no result for that answer and documents
    // it as retryable, so it is a signal rather than a failure: the other request
    // is opening the page this one was about to open. Returning null sends the
    // caller round the loop, where the listing adopts whatever that request
    // created. Treating this as an error would refuse a buyer whose only mistake
    // was pressing at the same instant as their colleague.
    //
    // https://docs.stripe.com/api/idempotent_requests
    if (err instanceof StripeError && err.code === 'idempotency_key_in_use') {
      // Wall clock, deliberately, and not the injected clock: the thing being
      // waited for is another request finishing at Stripe, which no test clock
      // moves. Short, because the request it is waiting for is one HTTP call
      // long, and a buyer is watching a button.
      await new Promise((resolve) => setTimeout(resolve, CONFLICT_WAIT_MS))
      return null
    }
    throw err
  }
  if (session.customerId !== input.customerId || session.status !== 'open' || !session.url) {
    refuse(
      'Stripe did not open a checkout for this organization\'s own customer. Nothing was charged. ' +
        'Use Refresh from Stripe, then try again.',
    )
  }
  if (!(await record(c, attempt.attempt_id, session.id))) return null
  return { url: session.url, sessionId: session.id }
}

/** Whether the page this attempt would reopen is the page the buyer just asked
 *  for. The customer is compared too, because a session opened against another
 *  customer is somebody else's money. */
function sameTerms(attempt: Attempt, input: CheckoutInput): boolean {
  return (
    attempt.stripe_customer_id === input.customerId &&
    attempt.price_id === input.priceId &&
    attempt.success_url === input.successUrl &&
    attempt.cancel_url === input.cancelUrl
  )
}
