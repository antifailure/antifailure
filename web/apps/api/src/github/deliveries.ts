// Handling one delivery once, however many times GitHub sends it.
//
// The HMAC over the raw body says a delivery is genuine. It says nothing at all
// about it being NEW. A delivery captured off the wire, or out of GitHub's own
// redelivery log, verifies exactly as well the thousandth time as the first, and
// every handler downstream of here writes something: a check run, a comment, a
// teardown request, a cancelled workflow. Replay protection is the difference
// between "somebody saw one of our payloads" and "somebody can cancel a running
// job on any repository we are installed on, whenever they like".
//
// It has to be a property of the database rather than of the handlers being
// careful, because "careful" has to hold for every handler anybody ever adds.
//
// THE CLAIM IS A LEASE, NOT A FLAG. The row is inserted before the delivery is
// handled and stamped after. Three cases follow and each has its own answer:
//
//   the insert wins        this process handles it
//   the row is stamped     it was handled; answer with what it did, do nothing
//   the row is unstamped   somebody else has it right now, or had it and died
//
// The third is the one that is easy to get wrong. Answering 200 to a delivery
// another process is still working on is fine when that process succeeds and is
// a silently dropped delivery when it does not. So it answers 503 with a
// Retry-After: come back, and by then the row will either be stamped or free.
// And a claim older than the takeover window is taken over rather than waited
// for, because a process killed mid-delivery leaves a claim nobody will ever
// stamp, and without this that delivery is refused forever while looking handled.

import { sql } from 'drizzle-orm'
import type { Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'

/**
 * How long a claim is respected before another attempt may take it.
 *
 * Two minutes, and the number comes from the slowest thing a handler does
 * rather than from taste: a pull request delivery creates a check run, reads
 * the comments and writes one, which is three GitHub calls, and GitHub's own
 * timeout on a webhook delivery is ten seconds. Two minutes is an order of
 * magnitude above the work and an order of magnitude below anybody noticing,
 * and a handler that takes longer than this has a problem this constant should
 * not be hiding.
 */
export const CLAIM_TAKEOVER_MS = 2 * 60 * 1000

export type DeliveryClaim =
  | { status: 'claimed' }
  | { status: 'replay'; outcome: string | null; handledAt: Date }
  | { status: 'in_flight'; retryAfterSeconds: number }

interface ClaimRow extends Record<string, unknown> {
  handled_at: Date | string | null
  outcome: string | null
  received_at: Date | string
}

/**
 * Takes the claim for a delivery, or says who has it.
 *
 * The account is declared alongside so the same transaction can write the
 * ledger row, and it may be null: a delivery about an account this installation
 * has never seen still has to be recorded, and that is precisely the delivery
 * that has no account to key on. That is why the ledger's policy keys on the
 * delivery rather than on the account.
 */
export async function claimDelivery(
  pool: Pool,
  clock: Clock,
  delivery: { deliveryId: string; event: string; action: string | null; login: string | null },
): Promise<DeliveryClaim> {
  const now = clock.now()
  return pool.withGitHubDelivery(
    { deliveryId: delivery.deliveryId, login: delivery.login },
    async (db) => {
      const inserted = await db.execute<{ delivery_id: string }>(sql`
        INSERT INTO github_deliveries (delivery_id, account_login, event, action, received_at)
        VALUES (${delivery.deliveryId}, ${delivery.login?.toLowerCase() ?? null},
                ${delivery.event}, ${delivery.action}, ${now.toISOString()})
        ON CONFLICT (delivery_id) DO NOTHING
        RETURNING delivery_id`)
      if (inserted.length > 0) return { status: 'claimed' } as const

      const rows = await db.execute<ClaimRow>(sql`
        SELECT handled_at, outcome, received_at FROM github_deliveries
        WHERE delivery_id = ${delivery.deliveryId}`)
      const row = rows[0]
      if (!row) {
        // The insert lost the race and the row is invisible, which under these
        // policies means it belongs to a different delivery id. That cannot
        // happen for a primary key conflict, so it is a policy misconfiguration
        // rather than a race, and saying so is better than looping.
        throw new Error(
          `github delivery ${delivery.deliveryId} collided on its own key and is not readable; ` +
            'the delivery policy in migration 0021 is not in force',
        )
      }
      if (row.handled_at !== null) {
        return {
          status: 'replay',
          outcome: row.outcome,
          handledAt: new Date(row.handled_at as string),
        } as const
      }

      const age = now.getTime() - new Date(row.received_at as string).getTime()
      if (age < CLAIM_TAKEOVER_MS) {
        // Rounded up, and never zero: a Retry-After of 0 is a client hammering
        // the endpoint it was asked to back off from.
        const retryAfterSeconds = Math.max(1, Math.ceil((CLAIM_TAKEOVER_MS - age) / 1000))
        return { status: 'in_flight', retryAfterSeconds } as const
      }

      // Taken over. received_at moves to now so that this attempt holds the
      // lease for its own full window rather than inheriting a spent one.
      await db.execute(sql`
        UPDATE github_deliveries SET received_at = ${now.toISOString()}
        WHERE delivery_id = ${delivery.deliveryId} AND handled_at IS NULL`)
      return { status: 'claimed' } as const
    },
  )
}

/** Stamps a delivery handled, with what it did and the tenant it turned out to
 *  be about. */
export async function closeDelivery(
  pool: Pool,
  clock: Clock,
  delivery: { deliveryId: string; login: string | null },
  result: { orgId: string | null; outcome: string },
): Promise<void> {
  await pool.withGitHubDelivery(
    { deliveryId: delivery.deliveryId, login: delivery.login },
    async (db) => {
      await db.execute(sql`
        UPDATE github_deliveries
        SET handled_at = ${clock.now().toISOString()},
            org_id = ${result.orgId}::uuid,
            outcome = ${result.outcome.slice(0, 500)}
        WHERE delivery_id = ${delivery.deliveryId}`)
    },
  )
}

/**
 * Gives the claim back after a handler threw.
 *
 * Deleted rather than marked failed. A row that stayed would be read as
 * "handled" by the next attempt, so one transient database error would turn
 * into a delivery refused forever, and the symptom is silence: GitHub's log
 * says 200, this control plane says nothing, and an installation event nothing
 * else will ever report is simply gone.
 *
 * This is the one place anything deletes from the ledger, and it is why DELETE
 * is granted on that table and nowhere else in migration 0021.
 */
export async function releaseDelivery(
  pool: Pool,
  delivery: { deliveryId: string; login: string | null },
): Promise<void> {
  await pool.withGitHubDelivery(
    { deliveryId: delivery.deliveryId, login: delivery.login },
    async (db) => {
      await db.execute(sql`
        DELETE FROM github_deliveries
        WHERE delivery_id = ${delivery.deliveryId} AND handled_at IS NULL`)
    },
  )
}

/**
 * The account a verified payload is about, or null.
 *
 * Three places, in the order they are trustworthy for this purpose. The
 * installation's account is the right answer and is present on the two
 * installation events. Everything else carries the MINIMAL installation object,
 * `{ id, node_id }` and nothing more, which is how reading the account off it
 * made the repository handler answer "no repository in the payload" for every
 * repository delivery GitHub has ever sent. So the organization and then the
 * repository's owner, both in the same signed body and trusted for the same
 * reason.
 */
export function accountLoginFrom(payload: Record<string, unknown>): string | null {
  const installation = payload.installation as { account?: { login?: unknown } } | undefined
  if (typeof installation?.account?.login === 'string') return installation.account.login

  const organization = payload.organization as { login?: unknown } | undefined
  if (typeof organization?.login === 'string') return organization.login

  const repository = payload.repository as { owner?: { login?: unknown } } | undefined
  if (typeof repository?.owner?.login === 'string') return repository.owner.login

  return null
}
