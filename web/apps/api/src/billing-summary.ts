// One organization's commercial standing, read from the database alone.
//
// Written for a caller outside this lane: admin-ops needed the plan, the seats,
// the subscription's status and period end, and whatever has been granted by
// hand, in a single call, without reaching Stripe and without the operator
// pool. The operator portal already had all four, spread across a tRPC route
// that hits Stripe four times and an entitlements resolver, and neither is
// importable from a background job.
//
// WHY IT TAKES A `Db` AND NOT THE BYPASS POOL. The bypass pool exists so an
// operator can read across tenants; a job that already knows which organization
// it is looking at does not need that, and taking it would mean every caller of
// this function had a handle that reads every customer's billing. So the
// parameter is an ordinary `Db`. Under the tenant pool row level security
// answers the same question this signature does, and the explicit `org_id`
// filter is not redundant with it: it is what makes the function correct when
// the caller passes an operator handle, where RLS is bypassed and the filter is
// the only thing scoping the read.
//
// WHY NOT STRIPE. Stripe is the system of record for what was charged; these
// tables are the system of record for what this deployment believes it sold,
// which is what an entitlement decision and a support answer both run on. They
// can disagree, and when they do the webhook handler is what reconciles them.
// A summary that called Stripe would be slower, would fail when Stripe is down,
// and would answer a different question than the one the product enforces.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'
import { asDate, resolveEntitlements, seatVerdict } from './entitlements.ts'
import type { Resolved } from './entitlements.ts'

export interface BillingSummarySeats {
  /** Members plus invitations that are still open, which is what a seat check
   *  counts. An invitation nobody has accepted still holds a seat. */
  used: number
  members: number
  openInvitations: number
  /** `null` where the plan and the overrides set no limit at all. Not `0`, and
   *  not `Infinity`: both of those survive JSON badly and one of them reads as
   *  "no seats allowed", which is the opposite of what it means. */
  limit: number | null
  /** True when `used` has reached `limit`, so the next invitation is refused. */
  atLimit: boolean
}

export interface BillingSummarySubscription {
  plan: string
  status: string
  /**
   * What Stripe reported as the subscription item's quantity.
   *
   * Shown so an operator can see what was billed. It is NOT a seat allowance
   * and it is not what `seats` above is computed from: nothing this control
   * plane sells has a quantity, checkout sends none, and `seats.limit` comes
   * from the plan. A subscription bought before that change can carry any
   * number here, and it still entitles exactly what its plan entitles.
   */
  quantity: number
  currentPeriodEnd: Date | null
  cancelAtPeriodEnd: boolean
  canceledAt: Date | null
}

export interface BillingSummaryOverride {
  feature: string
  scope: 'global' | 'organization' | 'project' | 'user'
  value: unknown
  /** What the plan alone would have said, so a caller can show the difference
   *  rather than just the result. */
  planValue: unknown
  reason: string
  ticket: string | null
  grantedBy: string
  grantedAt: Date
  expiresAt: Date | null
}

export interface BillingSummary {
  orgId: string
  slug: string
  /** The plan on the organization row, which is what entitlement checks run
   *  against. `subscription.plan` is what the provider last confirmed, and the
   *  two differ while a change is in flight or after a failed payment. */
  plan: string
  seats: BillingSummarySeats
  /** The newest subscription row, or `null` for an organization that has never
   *  checked out. Null is an answer, not a failure. */
  subscription: BillingSummarySubscription | null
  /** Only what is live at `now`: revoked and expired grants are left out, so a
   *  caller never has to filter them and never accidentally shows one. */
  overrides: BillingSummaryOverride[]
}

/**
 * Reads one organization's plan, seats, subscription and live overrides.
 *
 * Returns `null` when the organization does not exist OR when the handle cannot
 * see it, and those two are deliberately not distinguished: under the tenant
 * pool a caller asking about somebody else's organization gets the same answer
 * as one asking about an organization that was deleted, which is the only way
 * for this function not to be a membership oracle.
 */
export async function getOrgBillingSummary(
  db: Db,
  orgId: string,
  now: Date,
): Promise<BillingSummary | null> {
  const orgs = await db.execute<{ id: string; slug: string; plan: string }>(sql`
    SELECT id, slug, plan FROM organizations WHERE id = ${orgId}`)
  const org = orgs[0]
  if (!org) return null

  // Counted with the same predicate the seat check uses, rather than a simpler
  // one written here. An invitation that has expired has released its seat and
  // a revoked one never held it, so a plain `count(*) FROM invitations` would
  // report an organization over its limit that is not.
  const held = await db.execute<{ members: string; open_invitations: string }>(sql`
    SELECT (SELECT count(*) FROM members WHERE org_id = ${orgId}) AS members,
           (SELECT count(*) FROM invitations
             WHERE org_id = ${orgId} AND accepted_at IS NULL AND revoked_at IS NULL
               AND expires_at > ${now.toISOString()}) AS open_invitations`)
  const members = Number(held[0]?.members ?? 0)
  const openInvitations = Number(held[0]?.open_invitations ?? 0)

  const entitlements = await resolveEntitlements(db, now, { orgId, plan: org.plan })
  const verdict = seatVerdict(entitlements, members, openInvitations)

  // The newest row rather than the one Stripe calls active. A cancelled
  // subscription is still the answer to "what is this customer on" until a new
  // one replaces it, and filtering on status would return null for an
  // organization in exactly the state somebody is calling support about.
  // Every timestamp below is typed `string | Date`, not `Date`, and that is not
  // defensive typing. `db.execute` returns what the driver parsed for a raw
  // statement, and a `timestamptz` comes back as the string
  // `2026-04-14 19:00:00-05`. Declaring these `Date` typechecks, logs
  // correctly, and throws on the caller's first `.getTime()`.
  const subscriptions = await db.execute<{
    plan: string
    status: string
    quantity: number | string
    current_period_end: string | Date | null
    cancel_at_period_end: boolean
    canceled_at: string | Date | null
  }>(sql`
    SELECT plan, status, quantity, current_period_end, cancel_at_period_end, canceled_at
    FROM subscriptions WHERE org_id = ${orgId}
    ORDER BY last_event_at DESC, created_at DESC LIMIT 1`)
  const row = subscriptions[0]

  return {
    orgId: org.id,
    slug: org.slug,
    plan: org.plan,
    seats: {
      used: verdict.current,
      members,
      openInvitations,
      limit: Number.isFinite(verdict.limit) ? verdict.limit : null,
      atLimit: !verdict.allowed,
    },
    subscription: row
      ? {
          plan: row.plan,
          status: row.status,
          quantity: Number(row.quantity),
          currentPeriodEnd: row.current_period_end === null ? null : asDate(row.current_period_end),
          cancelAtPeriodEnd: row.cancel_at_period_end,
          canceledAt: row.canceled_at === null ? null : asDate(row.canceled_at),
        }
      : null,
    overrides: entitlements
      .all()
      .filter((resolved): resolved is Resolved & { override: NonNullable<Resolved['override']> } =>
        resolved.override !== null,
      )
      .map((resolved) => ({
        feature: resolved.key,
        scope: resolved.override.scope,
        value: resolved.value,
        planValue: resolved.planValue,
        reason: resolved.override.reason,
        ticket: resolved.override.ticket,
        grantedBy: resolved.override.grantedBy,
        grantedAt: resolved.override.grantedAt,
        expiresAt: resolved.override.expiresAt,
      })),
  }
}
