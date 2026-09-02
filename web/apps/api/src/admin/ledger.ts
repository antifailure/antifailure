// Doing a thing to somebody's money exactly once.
//
// The failure this exists for is not exotic. An operator presses Refund, the
// page is slow, they press it again. A browser retries a POST that timed out. A
// load balancer replays a request it thinks was lost. A tab was left open and
// somebody hit refresh. Every one of those is a second call to Stripe, and a
// second call to Stripe is a second refund: money out of the company, twice,
// for a mistake that no test written against a happy path will ever catch.
//
// There are two windows and they need two different mechanisms, which is why
// this file is longer than "use an idempotency key".
//
// THE FIRST WINDOW is between the two presses. It is closed by the ledger: the
// idempotency key is the PRIMARY KEY of admin_operations, so the second attempt
// to claim it violates a constraint rather than reaching the network. The claim
// is committed BEFORE Stripe is called, in its own transaction, because a claim
// that is still uncommitted when the process dies is a claim that was never
// made.
//
// THE SECOND WINDOW is between the claim and the answer. A process can claim
// the key, call Stripe, and die before it records what happened. The ledger
// cannot close that on its own: the row says in_flight and nothing here knows
// whether the refund exists. What closes it is sending the SAME key to Stripe
// in the `Idempotency-Key` header, so the retry gets Stripe's own cached
// response for the original request instead of creating a second object. The
// ledger and the header are the same string on purpose; two strings would mean
// a retry that was idempotent locally and not remotely, which is the worst of
// the three possibilities because it looks safe.
//
// WHAT IS DELIBERATELY NOT DONE: a key reused with DIFFERENT parameters is
// refused rather than answered. Returning the first refund's result for a
// second, larger refund would report success for something that never happened,
// which is worse than either refunding twice or failing outright, because
// nobody would go looking. Stripe refuses this case and so does this, before
// the network call rather than after it.

import { createHash } from 'node:crypto'
import { sql } from 'drizzle-orm'
import { appendAdminAudit, canonicalJson, type Db } from '@antifailure/db'

/** How long an in-flight operation is believed to be somebody else's before it
 *  is treated as a crash to recover from.
 *
 *  Two minutes rather than five seconds or an hour. Shorter than the slowest
 *  honest Stripe call and a person would start a second one on top of a first
 *  that is merely slow; longer and a genuine crash leaves a customer's refund
 *  stuck behind a row nobody can clear without a database session. Recovering
 *  is safe at any length because the retry carries the same key, so this
 *  number chooses between "confusing" and "stuck" rather than between "safe"
 *  and "double charge". */
export const IN_FLIGHT_GRACE_MS = 2 * 60 * 1000

export class OperationConflict extends Error {
  readonly kind: 'in-progress' | 'key-reused'
  constructor(kind: 'in-progress' | 'key-reused', message: string) {
    super(message)
    this.kind = kind
  }
}

/** What the caller asks for. */
export interface OperationRequest {
  /** `billing.refund`, `billing.plan_changed`. Verb and object, as the audit
   *  log spells them, because these become audit actions. */
  action: string
  orgId: string
  targetType: string
  /** Stripe's identifier for the thing being acted on. */
  targetId: string | null
  /**
   * The OPERATOR, from admin_users.
   *
   * A different id space from `users(id)`, and naming it wrongly is not a
   * cosmetic mistake: `admin_audit_entries.admin_user_id` has a foreign key to
   * admin_users, so a customer's user id here raises rather than mislabelling
   * somebody, which is the direction this should fail in.
   */
  adminUserId: string | null
  actorLabel: string
  /** Why. Refused when empty; see the migration for why this is not optional. */
  reason: string
  /**
   * The parameters, exactly. Fingerprinted, so a key reused with different
   * ones is refused.
   *
   * Everything the operation's outcome depends on has to be in here. A field
   * left out is a field that can change between a request and its retry
   * without the fingerprint noticing, which is the one way this design fails
   * quietly.
   */
  params: Record<string, unknown>
  /**
   * The key, when the client has one.
   *
   * A browser that mints a key when the FORM OPENS and sends the same one on
   * every press gets double-click safety for free. When it is absent the key is
   * derived from the parameters, which is weaker but not nothing: it makes two
   * identical refunds of the same charge for the same amount collapse into one,
   * which is exactly the double click, and it deliberately does NOT collapse
   * two DIFFERENT refunds that happen to be for the same charge.
   */
  idempotencyKey?: string
  /** The tenant's slug, kept as text on the audit entry for the same reason as
   *  actorLabel: it has to still name the organization once the row is gone. */
  orgLabel?: string | null
  /** Where the request came from, recorded on the operator's entry. */
  ip?: string | null
}

export interface OperationOutcome<T> {
  result: T
  /** What Stripe made, so the ledger and the console can link to it. */
  providerObjectId?: string | null
  /** The money that moved, in minor units, beside its currency. Both or
   *  neither: an amount with no currency is a number that will eventually be
   *  added to a different one. */
  amountMinor?: number | null
  currency?: string | null
  /** What was true after, for the audit entry and the review a month later. */
  after?: Record<string, unknown>
}

export interface OperationRun<T> {
  result: T
  /** True when this call did not do the work, because a previous one had.
   *  The console says so rather than showing a second success toast for a
   *  refund that happened ten seconds ago. */
  replayed: boolean
  idempotencyKey: string
  providerObjectId: string | null
}

/** The fingerprint a key is bound to. Exported for the test that proves a
 *  reused key with changed parameters is refused. */
export function fingerprint(req: Pick<OperationRequest, 'action' | 'orgId' | 'targetId' | 'params'>): string {
  return createHash('sha256')
    .update(
      canonicalJson({
        action: req.action,
        orgId: req.orgId,
        targetId: req.targetId ?? null,
        params: req.params,
      }),
    )
    .digest('hex')
}

/**
 * The key this request will use.
 *
 * Prefixed with the action so that a key seen in a Stripe dashboard says what
 * it was for, and truncated because Stripe's own limit is 255 characters and a
 * key that is silently truncated at the provider is a key that collides.
 */
export function keyFor(req: OperationRequest, attempt = 1): string {
  const base = req.idempotencyKey ?? `af.${req.action}.${fingerprint(req).slice(0, 40)}`
  // The first attempt is the bare key, so an existing ledger row and a client
  // that minted a key both mean what they have always meant. A later attempt
  // is a suffix, and it exists ONLY for the case below where the provider
  // refused: a refusal is a decision that the thing did not happen, so the
  // retry is genuinely a new request and needs a key the provider has not
  // already answered.
  return attempt <= 1 ? base : `${base}.a${attempt}`
}

/** How many deliberate retries one intent may have before this stops minting
 *  keys. A person retrying a declined payment twenty times has a problem the
 *  twenty first attempt will not solve, and an unbounded loop here would keep
 *  claiming rows for it. */
export const MAX_ATTEMPTS = 20

interface LedgerRow extends Record<string, unknown> {
  idempotency_key: string
  state: string
  request_fingerprint: string
  after_state: unknown
  provider_object_id: string | null
  started_at: string | Date
  error_message: string | null
  error_answered: boolean | null
}

/** Either this call owns the key and must do the work, or a previous one
 *  already did and this is its answer. Null is the third case and it is not a
 *  claim: it means "that key is spent, try the next attempt". */
type Claim =
  | {
      mine: true
      /**
       * Whether this call CREATED the row, as opposed to picking up one that a
       * previous attempt left behind.
       *
       * Handed to `work` because a pre-flight check that is right for a first
       * attempt can be wrong for a recovery. The case that forced this: the
       * refund path refuses an amount larger than what is left on the charge,
       * which is correct for a new refund and catastrophic for a retry after a
       * crash, because the provider ALREADY HOLDS that refund, so the charge
       * shows it as refunded and the recovery would be refused forever with the
       * ledger row stuck in flight. A recovery must send the same key and let
       * the provider replay its own answer.
       */
      fresh: boolean
    }
  | { mine: false; replay: { after: unknown; providerObjectId: string | null } }

/**
 * Runs `work` at most once for this key, ever.
 *
 * `withAdmin` is the caller's way of getting a transaction that can reach
 * across tenants. Three separate transactions rather than one, and the split is
 * the design rather than an implementation detail:
 *
 *   1. CLAIM, committed before anything is sent. A claim inside the same
 *      transaction as the call would be rolled back by the failure that made
 *      the call ambiguous, releasing the key precisely when it is most needed.
 *   2. The call itself, outside any transaction. Holding a database
 *      transaction open across a network call to a payment provider is how a
 *      slow provider becomes a connection pool outage.
 *   3. SETTLE, together with the audit entry, so the record of what happened
 *      and the record that it happened commit or roll back as one.
 *
 * `work` receives the key and MUST pass it to the provider as its idempotency
 * key. That is the whole of the second window; a `work` that ignores it makes
 * this function a lock rather than a guarantee.
 */
export async function runOnce<T>(
  withAdmin: <R>(fn: (db: Db) => Promise<R>) => Promise<R>,
  now: Date,
  req: OperationRequest,
  work: (idempotencyKey: string, fresh: boolean) => Promise<OperationOutcome<T>>,
  /** What was true before, read by the caller so this file never has to know
   *  what a subscription looks like. */
  before?: Record<string, unknown>,
): Promise<OperationRun<T>> {
  if (!req.reason.trim()) {
    throw new Error(`${req.action} needs a reason; an unexplained money action cannot be reviewed.`)
  }
  const print = fingerprint(req)

  // ---- 1. Claim, or find out who has it. -----------------------------------
  //
  // The loop walks attempt keys. It advances only in one case, the case the
  // comment on `error_answered` describes: a previous attempt under this key
  // was REFUSED by the provider, so that attempt definitively did nothing and
  // this deliberate retry needs a key the provider has not already answered.
  // Every other state either returns or claims the key it is looking at.
  let key = keyFor(req)
  let claimed: Claim | null = null

  for (let attempt = 1; attempt <= MAX_ATTEMPTS && claimed === null; attempt += 1) {
    key = keyFor(req, attempt)
    claimed = await withAdmin(async (db): Promise<Claim | null> => {
      const inserted = await db.execute<{ idempotency_key: string }>(sql`
        INSERT INTO admin_operations (
          idempotency_key, action, org_id, target_type, target_id,
          admin_user_id, actor_label, reason, request, request_fingerprint,
          state, before_state, started_at)
        VALUES (
          ${key}, ${req.action}, ${req.orgId}, ${req.targetType}, ${req.targetId},
          ${req.adminUserId}, ${req.actorLabel}, ${req.reason},
          ${JSON.stringify(req.params)}::jsonb, ${print},
          'in_flight', ${before === undefined ? null : JSON.stringify(before)}::jsonb,
          ${now.toISOString()})
        ON CONFLICT (idempotency_key) DO NOTHING
        RETURNING idempotency_key`)
      // A row that did not exist. This is a first attempt, or a deliberate
      // retry that minted its own key after a refusal; either way nothing has
      // been sent under this key and a pre-flight check is meaningful.
      if (inserted.length > 0) return { mine: true, fresh: true }

      // Somebody else holds it. The row is locked while it is read so that two
      // arrivals do not both decide the other is stale and both call Stripe;
      // without FOR UPDATE this whole branch is a read-modify-write race with a
      // refund on the other end of it.
      const held = await db.execute<LedgerRow>(sql`
        SELECT idempotency_key, state, request_fingerprint, after_state,
               provider_object_id, started_at, error_message, error_answered
        FROM admin_operations WHERE idempotency_key = ${key} FOR UPDATE`)
      const row = held[0]
      // Deleted between the conflict and the read. Nothing has DELETE on this
      // table, so this is unreachable rather than merely unlikely, and it is
      // handled instead of asserted because an unreachable branch that throws a
      // TypeError is worse than one that says what it saw.
      if (!row) {
        throw new OperationConflict(
          'in-progress', 'That operation is being recorded. Try again in a moment.',
        )
      }

      if (row.request_fingerprint !== print) {
        // A client-supplied key reused for a different request. Refused before
        // the network call rather than after it, so nothing is sent and the
        // operator is told to start again. Only reachable on the first attempt,
        // because the suffixed keys are derived from this request.
        throw new OperationConflict(
          'key-reused',
          'That idempotency key has already been used for a different request. Nothing was sent ' +
            'to the payment provider. Start the action again so it gets a key of its own.',
        )
      }

      if (row.state === 'succeeded') {
        return {
          mine: false,
          replay: { after: row.after_state, providerObjectId: row.provider_object_id },
        }
      }

      if (row.state === 'in_flight') {
        const startedAt = row.started_at instanceof Date ? row.started_at : new Date(row.started_at)
        if (now.getTime() - startedAt.getTime() < IN_FLIGHT_GRACE_MS) {
          // The honest answer to a double click. Not "done" and not a second
          // call: somebody is doing it, right now, and the second press has to
          // wait rather than race.
          throw new OperationConflict(
            'in-progress',
            'That operation is already in progress. Wait for it to finish rather than starting a ' +
              'second one; nothing has been sent twice.',
          )
        }
        // Stale: whatever claimed this is gone, and it may have reached the
        // provider before it died. Retrying under the SAME key is the only safe
        // recovery, because the provider then replays its own answer instead of
        // doing the work again. NOT fresh: the provider may already hold the
        // result, so a pre-flight check would be looking at a world this
        // operation has already changed.
        return { mine: true, fresh: false }
      }

      // Failed, and which kind of failed decides everything.
      if (row.error_answered === true) {
        // The provider refused. That attempt definitively did nothing, so this
        // deliberate retry is a new request and needs a key the provider has
        // not already answered. Reusing the key here is the bug that makes a
        // declined payment un-retryable forever: the provider replays its own
        // 402 and the customer who has since fixed their card can never pay.
        return null
      }
      // No answer came back. The request may have been executed and the
      // response lost, so the same key goes back out and the provider decides.
      // Not fresh, for the same reason as the stale branch above.
      return { mine: true, fresh: false }
    })
  }

  if (claimed === null) {
    throw new OperationConflict(
      'in-progress',
      `That operation has been attempted ${MAX_ATTEMPTS} times and refused every time. Look at ` +
        'what the payment provider is saying before trying again.',
    )
  }

  if (!claimed.mine) {
    return {
      result: claimed.replay.after as T,
      replayed: true,
      idempotencyKey: key,
      providerObjectId: claimed.replay.providerObjectId,
    }
  }

  // ---- 2. Do it. -----------------------------------------------------------
  let outcome: OperationOutcome<T>
  try {
    outcome = await work(key, claimed.fresh)
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err)
    const code = (err as { code?: string | null }).code ?? null
    // Whether the provider answered. See the column comment in 0030_entitlements_flags_and_the_money_ledger: this
    // decides whether the NEXT deliberate retry may have a key of its own or
    // has to reuse this one. Anything that is not a provider error is treated
    // as unanswered, which is the direction that cannot double charge.
    const answered = (err as { answered?: unknown }).answered === true
    // Recorded rather than swallowed. A failed money action that leaves no row
    // is a failed money action nobody can find, and the row is also what makes
    // the retry above reuse the key instead of minting a new one.
    await withAdmin(async (db) => {
      await db.execute(sql`
        UPDATE admin_operations
        SET state = 'failed', error_code = ${code}, error_message = ${message.slice(0, 2000)},
            error_answered = ${answered}, finished_at = ${now.toISOString()}
        WHERE idempotency_key = ${key}`)
    })
    throw err
  }

  // ---- 3. Settle, with the audit entry, in one transaction. ----------------
  await withAdmin(async (db) => {
    await db.execute(sql`
      UPDATE admin_operations
      SET state = 'succeeded',
          after_state = ${outcome.after === undefined ? null : JSON.stringify(outcome.after)}::jsonb,
          provider_object_id = ${outcome.providerObjectId ?? null},
          amount_minor = ${outcome.amountMinor ?? null},
          currency = ${outcome.currency ?? null},
          error_code = NULL, error_message = NULL, error_answered = NULL,
          finished_at = ${now.toISOString()}
      WHERE idempotency_key = ${key}`)

    // ONE call, TWO chains.
    //
    // appendAdminAudit writes the operator's entry and, because subjectOrgId is
    // set, the customer's copy in the same transaction. An earlier version of
    // this called appendAudit directly and wrote only the tenant's, which left
    // a money action absent from the platform chain entirely; writing both by
    // hand would have given the customer two entries for one refund.
    //
    // The tenant copy is not optional here and the default is deliberately not
    // overridden. A refund is something that happened to that customer, and a
    // record only the vendor can read is a vendor's private note rather than
    // accountability.
    //
    // `high` on every one of these. The severity vocabulary is coarse on
    // purpose and money is the case it was made coarse for: there is no
    // administrative write in this file that is routine, so a severity that
    // varied per action would be a judgement call somebody eventually gets
    // wrong in the quiet direction.
    await appendAdminAudit(db, {
      adminUserId: req.adminUserId,
      actorLabel: req.actorLabel,
      action: req.action,
      targetType: req.targetType,
      targetId: req.targetId,
      subjectOrgId: req.orgId,
      subjectOrgLabel: req.orgLabel ?? null,
      origin: 'admin',
      ip: req.ip ?? null,
      severity: 'high',
      occurredAt: now,
      detail: {
        reason: req.reason,
        idempotencyKey: key,
        request: req.params,
        before: before ?? null,
        after: outcome.after ?? null,
        providerObjectId: outcome.providerObjectId ?? null,
        ...(outcome.amountMinor !== undefined && outcome.amountMinor !== null
          ? { amountMinor: outcome.amountMinor, currency: outcome.currency ?? null }
          : {}),
      },
    })
  })

  return {
    result: outcome.result,
    replayed: false,
    idempotencyKey: key,
    providerObjectId: outcome.providerObjectId ?? null,
  }
}
