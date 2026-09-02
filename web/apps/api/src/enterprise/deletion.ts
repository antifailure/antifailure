// Deleting an organization, which is not a DELETE.
//
// A cascade would do it in one statement, and this repository has already
// proved that it works: deleting an `organizations` row removes every
// referencing row on the cluster, including the ones whose DELETE privilege was
// deliberately revoked, because referential integrity actions run with the
// table owner's privileges and bypass row-level security. So the reason this
// file is not one statement has nothing to do with the database being unable to
// do it.
//
// The reason is that four of the six things a deletion has to do are outside
// this database and cannot be rolled back:
//
//   work that is running       in the customer's own CI
//   a subscription             at Stripe, which keeps billing a card
//   an App installation        at GitHub, which keeps being able to dispatch
//   the export the customer is owed, which cannot be produced afterwards
//
// A row deleted while Stripe still bills is not a data-model detail. It is a
// customer who is gone from our side and still paying, discovering it on a card
// statement, with nothing left here to explain it.
//
// So the deletion is a state machine whose state lives in one row, every step
// writes its own timestamp in the same transaction as the change it describes,
// and the next step is derived rather than stored. That derivation is what
// makes an interruption safe: a process that dies between two steps leaves a
// record that says exactly which steps happened, and the resumer picks up at
// the first one that did not. There is no "in progress" state, because a
// process that has died cannot clear one.
//
// THE ORDER, AND WHY IT IS THIS ORDER
//
//   1 stop work            first, so nothing new is created behind the deletion
//                          and nothing is still running when credentials go.
//   2 cancel subscription  before the wait, so the wait is bounded by a period
//                          that is ending rather than one that renews.
//   3 wait                 the customer paid for this period and keeps it.
//                          Everything still reads during it, and the deletion
//                          can still be called off.
//   4 revoke credentials   after the wait, because revoking during it would
//                          break an organization that is still paying.
//   5 export               after revocation, so the export describes the final
//                          state rather than one that then changed.
//   6 purge                last, and only reachable when 1 to 5 all happened.
//
// WHAT DRIVES IT. Three callers, and it has to be all three. The request itself
// advances as far as it can, so a free organization with nothing running is
// deleted while the person is still looking at the screen. `deletion.advance`
// lets an owner push it along and see the failure if there is one. And the
// resumer runs on the same interval as the session sweep, because step 3 can be
// a month long and nobody is going to come back and press a button.

import { createHash, randomBytes } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Db, Pool } from '@antifailure/db'
import { appendAudit } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import type { GitHubClient } from '../auth/github.ts'
import type { Billing } from '../billing/index.ts'
import { LIVE_STATUSES } from '../billing/plans.ts'
import { StripeError } from '../billing/stripe.ts'
import { buildExport } from './export.ts'

/** How long the export stays downloadable after the organization is gone. */
export const EXPORT_RETENTION_MS = 7 * 24 * 60 * 60 * 1000

/**
 * The exact sentence written into `organizations.suspended_reason` when a
 * deletion suspends an organization.
 *
 * Compared rather than remembered. Calling a deletion off must not lift a
 * suspension somebody else applied during an incident, and the only way to know
 * whose suspension it is, without a column that would have to be kept in step,
 * is to check that it still says what this wrote.
 */
export const SUSPENDED_BY_DELETION = 'deletion requested'

export type DeletionStep =
  | 'stop_work'
  | 'cancel_subscription'
  | 'await_entitlement_end'
  | 'revoke_credentials'
  | 'export'
  | 'purge'
  | 'done'
  | 'cancelled'

export interface DeletionView {
  id: string
  organization: string
  slug: string
  requestedBy: string
  requestedAt: string
  reason: string | null
  /** The step that has not happened yet, or `done`. */
  step: DeletionStep
  /** What each step did, once it has. */
  stoppedWork: { at: string; environments: number; runs: number } | null
  cancelledSubscription: { at: string; subscription: string | null; entitlementEndsAt: string | null } | null
  revokedCredentials: {
    at: string
    engineTokens: number
    providerKeys: number
    sessions: number
    installations: number
  } | null
  exportedAt: string | null
  purgedAt: string | null
  cancelledAt: string | null
  /** When the wait ends, while the deletion is waiting. Null otherwise. */
  waitingUntil: string | null
  export: { available: boolean; expiresAt: string | null; sizeBytes: number | null; downloads: number } | null
  lastError: { at: string; step: string; message: string } | null
  attempts: number
}

export class DeletionError extends Error {}

export interface DeletionDeps {
  pool: Pool
  clock: Clock
  github: GitHubClient
  stripe: Billing | null
  /** Where a step's failure goes when nobody is watching. The resumer has no
   *  request to answer, and a step that fails silently forever is the failure
   *  this whole file exists to make impossible. */
  log?: (line: string, err?: unknown) => void
}

interface DeletionRow extends Record<string, unknown> {
  id: string
  org_id: string
  org_slug: string
  org_name: string
  requested_by_label: string
  requested_at: Date | string
  reason: string | null
  work_stopped_at: Date | string | null
  environments_stopped: number | null
  runs_cancelled: number | null
  subscription_cancelled_at: Date | string | null
  subscription_id: string | null
  entitlement_ends_at: Date | string | null
  credentials_revoked_at: Date | string | null
  engine_tokens_revoked: number | null
  provider_keys_revoked: number | null
  sessions_revoked: number | null
  installations_revoked: number | null
  exported_at: Date | string | null
  purged_at: Date | string | null
  cancelled_at: Date | string | null
  last_error_at: Date | string | null
  last_error_step: string | null
  last_error_message: string | null
  attempts: number
}

const COLUMNS = sql`
  id, org_id, org_slug, org_name, requested_by_label, requested_at, reason,
  work_stopped_at, environments_stopped, runs_cancelled,
  subscription_cancelled_at, subscription_id, entitlement_ends_at,
  credentials_revoked_at, engine_tokens_revoked, provider_keys_revoked,
  sessions_revoked, installations_revoked,
  exported_at, purged_at, cancelled_at,
  last_error_at, last_error_step, last_error_message, attempts`

/**
 * The step a record is waiting to do. Derived, never stored.
 *
 * Takes the clock because the wait is a comparison rather than a column: when
 * the entitlement ends is recorded, that it has ended is not. An earlier
 * version cleared `entitlement_ends_at` to mark the wait over, which made the
 * step derivable without a clock and destroyed the evidence of when the paid
 * period actually finished, which is the one fact somebody asks about after the
 * fact.
 */
export function stepOf(row: DeletionRow, now: Date): DeletionStep {
  if (row.cancelled_at) return 'cancelled'
  if (row.purged_at) return 'done'
  if (!row.work_stopped_at) return 'stop_work'
  if (!row.subscription_cancelled_at) return 'cancel_subscription'
  if (!row.credentials_revoked_at) {
    // The wait is not a step of its own: it is the gate in front of revocation.
    // Reported separately so a person reading the console sees "waiting until
    // the 14th" rather than "revoking credentials" for a month.
    return row.entitlement_ends_at && asDate(row.entitlement_ends_at).getTime() > now.getTime()
      ? 'await_entitlement_end'
      : 'revoke_credentials'
  }
  if (!row.exported_at) return 'export'
  return 'purge'
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

/** The live deletion for an organization, or null. */
export async function readDeletion(
  db: Db,
  clock: Clock,
  orgId: string,
): Promise<DeletionView | null> {
  const rows = await db.execute<DeletionRow>(sql`
    SELECT ${COLUMNS} FROM organization_deletions
    WHERE org_id = ${orgId}::uuid AND purged_at IS NULL AND cancelled_at IS NULL
    ORDER BY requested_at DESC LIMIT 1`)
  const row = rows[0]
  if (!row) return null
  return viewOf(row, await readExportRow(db, row.id), clock.now())
}

async function readExportRow(
  db: Db,
  deletionId: string,
): Promise<{ expires_at: Date | string; size_bytes: string; destroyed_at: Date | string | null; download_count: number } | null> {
  const rows = await db.execute<{
    expires_at: Date | string
    size_bytes: string
    destroyed_at: Date | string | null
    download_count: number
  }>(sql`
    SELECT expires_at, size_bytes, destroyed_at, download_count
    FROM organization_deletion_exports WHERE deletion_id = ${deletionId}::uuid`)
  return rows[0] ?? null
}

function viewOf(
  row: DeletionRow,
  exportRow: Awaited<ReturnType<typeof readExportRow>>,
  now: Date,
): DeletionView {
  const step = stepOf(row, now)
  return {
    id: row.id,
    organization: row.org_name,
    slug: row.org_slug,
    requestedBy: row.requested_by_label,
    requestedAt: iso(row.requested_at)!,
    reason: row.reason,
    step,
    stoppedWork: row.work_stopped_at
      ? {
          at: iso(row.work_stopped_at)!,
          environments: row.environments_stopped ?? 0,
          runs: row.runs_cancelled ?? 0,
        }
      : null,
    cancelledSubscription: row.subscription_cancelled_at
      ? {
          at: iso(row.subscription_cancelled_at)!,
          subscription: row.subscription_id,
          entitlementEndsAt: iso(row.entitlement_ends_at),
        }
      : null,
    revokedCredentials: row.credentials_revoked_at
      ? {
          at: iso(row.credentials_revoked_at)!,
          engineTokens: row.engine_tokens_revoked ?? 0,
          providerKeys: row.provider_keys_revoked ?? 0,
          sessions: row.sessions_revoked ?? 0,
          installations: row.installations_revoked ?? 0,
        }
      : null,
    exportedAt: iso(row.exported_at),
    purgedAt: iso(row.purged_at),
    cancelledAt: iso(row.cancelled_at),
    waitingUntil: step === 'await_entitlement_end' ? iso(row.entitlement_ends_at) : null,
    export: exportRow
      ? {
          // There is a document AND it has not been destroyed. The row is
          // created when the deletion is requested, so it exists long before
          // the export step fills it, and "not destroyed" alone would report an
          // empty row as a download somebody could take.
          available: exportRow.destroyed_at === null && Number(exportRow.size_bytes) > 0,
          expiresAt: iso(exportRow.expires_at),
          sizeBytes: Number(exportRow.size_bytes),
          downloads: exportRow.download_count,
        }
      : null,
    lastError: row.last_error_at
      ? {
          at: iso(row.last_error_at)!,
          step: row.last_error_step ?? 'unknown',
          message: row.last_error_message ?? 'no message was recorded',
        }
      : null,
    attempts: row.attempts,
  }
}

// ---------------------------------------------------------------------------
// Requesting
// ---------------------------------------------------------------------------

export interface RequestedDeletion {
  view: DeletionView
  /** The link the export will be downloadable at, given once, at request time.
   *  Held nowhere else: what is stored is its hash. */
  exportToken: string
}

/**
 * Records the request.
 *
 * The confirmation is checked here rather than in the router, because the check
 * and the write have to be one thing: a confirmation validated by a caller that
 * then writes is a confirmation somebody can skip by calling something else.
 *
 * The export token is minted now rather than at export time, and that is the
 * decision that makes the export reachable at all. After the purge there is no
 * membership left to authorise a download, so the link has to be in the
 * requester's hands before the organization stops existing.
 */
export async function requestDeletion(
  deps: DeletionDeps,
  actor: { orgId: string; userId: string; label: string },
  input: { confirm: string; reason?: string | null },
): Promise<RequestedDeletion> {
  const exportToken = randomBytes(32).toString('base64url')

  await deps.pool.withTenant({ orgId: actor.orgId, userId: actor.userId }, async (db) => {
    const orgs = await db.execute<{ slug: string; name: string }>(sql`
      SELECT slug, name FROM organizations WHERE id = ${actor.orgId}::uuid`)
    const org = orgs[0]
    if (!org) throw new DeletionError('This organization no longer exists.')

    if (input.confirm.trim() !== org.slug) {
      throw new DeletionError(
        `Type ${org.slug} to confirm. Deleting an organization cannot be undone once it finishes.`,
      )
    }

    const existing = await db.execute<{ id: string }>(sql`
      SELECT id FROM organization_deletions
      WHERE org_id = ${actor.orgId}::uuid AND purged_at IS NULL AND cancelled_at IS NULL`)
    if (existing.length > 0) {
      throw new DeletionError('This organization is already being deleted.')
    }

    const rows = await db.execute<{ id: string }>(sql`
      INSERT INTO organization_deletions
        (org_id, org_slug, org_name, requested_by, requested_by_label, reason, requested_at,
         created_at, updated_at)
      VALUES (${actor.orgId}::uuid, ${org.slug}, ${org.name}, ${actor.userId}::uuid,
              ${actor.label}, ${input.reason ?? null},
              ${deps.clock.now().toISOString()}, ${deps.clock.now().toISOString()},
              ${deps.clock.now().toISOString()})
      RETURNING id`)

    // Written now, with the hash, so the row that will hold the document
    // already knows which link opens it. INSERTing the export row later would
    // mean minting the token later, which is after the point at which the
    // requester can be handed anything.
    await db.execute(sql`
      INSERT INTO organization_deletion_exports
        (deletion_id, org_id, token_hash, document, entry_count, size_bytes, created_at, expires_at)
      VALUES (${rows[0]!.id}::uuid, ${actor.orgId}::uuid, ${hashExportToken(exportToken)},
              '{}'::jsonb, 0, 0,
              ${deps.clock.now().toISOString()},
              ${new Date(deps.clock.now().getTime() + EXPORT_RETENTION_MS).toISOString()})`)

    await appendAudit(db, {
      orgId: actor.orgId,
      actorUserId: actor.userId,
      actorLabel: actor.label,
      action: 'organization.deletion_requested',
      targetType: 'organization',
      targetId: actor.orgId,
      origin: 'web',
      detail: { reason: input.reason ?? null },
      occurredAt: deps.clock.now(),
    })
  })

  // As far as it will go, now, so that an organization with nothing running and
  // nothing to cancel is finished before the person looks away.
  const { view } = await runToCompletion(deps, actor.orgId)
  return { view: view!, exportToken }
}

/**
 * Calls a deletion off.
 *
 * Allowed at any point before the purge, including after the credentials have
 * been revoked: what was revoked stays revoked and can be reissued, and telling
 * somebody they cannot stop a deletion because a token was revoked would be a
 * refusal with no reason behind it. After the purge there is nothing to come
 * back to, and the refusal says so.
 */
export async function cancelDeletion(
  deps: DeletionDeps,
  actor: { orgId: string; userId: string; label: string },
): Promise<{ cancelled: true; resumedOrganization: boolean }> {
  return deps.pool.withTenant({ orgId: actor.orgId, userId: actor.userId }, async (db) => {
    const rows = await db.execute<{ id: string }>(sql`
      UPDATE organization_deletions
      SET cancelled_at = ${deps.clock.now().toISOString()},
          cancelled_by_label = ${actor.label},
          updated_at = ${deps.clock.now().toISOString()}
      WHERE org_id = ${actor.orgId}::uuid AND purged_at IS NULL AND cancelled_at IS NULL
      RETURNING id`)
    if (rows.length === 0) {
      throw new DeletionError('There is no deletion in progress to call off.')
    }

    // Only when the suspension is still the one this wrote. An organization
    // suspended during an incident, by somebody else, after the deletion was
    // requested, must stay suspended.
    const resumed = await db.execute<{ id: string }>(sql`
      UPDATE organizations
      SET suspended_at = NULL, suspended_reason = NULL, suspended_by = NULL,
          updated_at = ${deps.clock.now().toISOString()}
      WHERE id = ${actor.orgId}::uuid AND suspended_reason = ${SUSPENDED_BY_DELETION}
      RETURNING id`)

    await appendAudit(db, {
      orgId: actor.orgId,
      actorUserId: actor.userId,
      actorLabel: actor.label,
      action: 'organization.deletion_cancelled',
      targetType: 'organization',
      targetId: actor.orgId,
      origin: 'web',
      detail: { resumedOrganization: resumed.length > 0 },
      occurredAt: deps.clock.now(),
    })
    return { cancelled: true as const, resumedOrganization: resumed.length > 0 }
  })
}

// ---------------------------------------------------------------------------
// Advancing
// ---------------------------------------------------------------------------

/**
 * Does one step, if one is due.
 *
 * Every step's write carries `WHERE <its timestamp> IS NULL`, so two callers
 * arriving at once do not both do it: the first commits and the second's UPDATE
 * matches nothing and reports no progress. That is the whole of the concurrency
 * story, and it is in the WHERE clause rather than in a lock because a lock
 * held by a process that then dies is the state this design exists to avoid.
 *
 * Returns whether anything moved, so a caller can loop without guessing.
 */
export async function advanceDeletion(
  deps: DeletionDeps,
  orgId: string,
): Promise<{ moved: boolean; view: DeletionView | null }> {
  const view = await deps.pool.withTenant({ orgId }, async (db) =>
    readDeletion(db, deps.clock, orgId),
  )
  if (!view) return { moved: false, view: null }

  const step = view.step
  if (step === 'done' || step === 'cancelled') return { moved: false, view }

  try {
    let moved = false
    if (step === 'stop_work') moved = await stopWork(deps, orgId)
    else if (step === 'cancel_subscription') moved = await cancelSubscription(deps, orgId)
    else if (step === 'await_entitlement_end') {
      // Waiting is never progress. Reporting otherwise would make the
      // request-time loop spin through its whole budget on a record that is
      // going to sit for a month.
      await refreshEntitlement(deps, orgId)
      moved = false
    }
    else if (step === 'revoke_credentials') moved = await revokeCredentials(deps, orgId)
    else if (step === 'export') moved = await produceExport(deps, orgId)
    else if (step === 'purge') moved = await purge(deps, orgId)

    if (moved) await clearError(deps, orgId)
    const after = await deps.pool.withTenant({ orgId }, async (db) =>
      readDeletion(db, deps.clock, orgId),
    )
    // After a purge the record is no longer "live", so readDeletion returns
    // null. Reading the finished record back is what the caller wants, and it
    // is a different query rather than a widening of the live one.
    return { moved, view: after ?? (await readFinished(deps, orgId)) }
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err)
    await recordError(deps, orgId, step, message)
    deps.log?.(`organization deletion ${orgId} failed at ${step}: ${message}`, err)
    throw err
  }
}

/**
 * Advances until nothing more can be done now.
 *
 * `maxSteps` is a guard rather than a budget: there are six steps, so eight
 * iterations can only be reached by a step that reports progress without making
 * any, which would otherwise be an infinite loop inside a request.
 */
export async function runToCompletion(
  deps: DeletionDeps,
  orgId: string,
  maxSteps = 8,
): Promise<{ view: DeletionView | null; moved: boolean }> {
  let view: DeletionView | null = null
  let moved = false
  for (let i = 0; i < maxSteps; i++) {
    const result = await advanceDeletion(deps, orgId)
    // The last view that exists, not the last view returned. A deletion that
    // has finished has no live record, so the call after the purge answers
    // null, and taking it would report "there is no deletion" to the person who
    // just watched one complete.
    if (result.view) view = result.view
    if (!result.moved) break
    moved = true
  }
  return { view, moved }
}

/** The record after it has finished, which the live query deliberately misses. */
async function readFinished(deps: DeletionDeps, orgId: string): Promise<DeletionView | null> {
  return deps.pool.withTenant({ orgId }, async (db) => {
    const rows = await db.execute<DeletionRow>(sql`
      SELECT ${COLUMNS} FROM organization_deletions
      WHERE org_id = ${orgId}::uuid ORDER BY requested_at DESC LIMIT 1`)
    const row = rows[0]
    if (!row) return null
    return viewOf(row, await readExportRow(db, row.id), deps.clock.now())
  })
}

// ---------------------------------------------------------------------------
// Step 1: stop work
// ---------------------------------------------------------------------------

/**
 * Tears down what is running and stops anything new being started.
 *
 * The suspension is what closes the door behind the deletion. Without it an
 * environment created a minute after the request would be running when the
 * purge arrives, and the purge would remove its rows while the containers stay
 * up in somebody's CI. Suspension is an existing mechanism with an existing
 * meaning: nothing new is created, what is running keeps running and can be
 * read. This adds the teardown that the incident case deliberately does not do.
 *
 * The environments are MARKED torn down. The engine that holds the containers
 * reads that and does the removing; the control plane has no route into a
 * developer's machine and does not pretend otherwise. That is a real limit of
 * this step and it is the same limit `environments.teardown` has.
 */
export async function stopWork(deps: DeletionDeps, orgId: string): Promise<boolean> {
  return deps.pool.withTenant({ orgId }, async (db) => {
    const now = deps.clock.now().toISOString()

    // The claim comes first, before any work, and that ordering is the whole
    // point of it.
    //
    // It used to be the last of four statements: tear down the environments,
    // cancel the runs, suspend the organization, and only then try to mark the
    // record. The comment above advanceDeletion says every step's write
    // carries `WHERE <its timestamp> IS NULL` so two callers arriving at once
    // do not both do it, and that was true of this bookkeeping row and false
    // of the three statements that do the actual work. A second caller ran all
    // three and was then told, at the very end, that it had lost: stopped at
    // the point where stopping it no longer meant anything.
    //
    // It was not reachable as data loss, because each work statement locks the
    // rows it touches and re-evaluates its WHERE after the winner commits, so
    // the loser found nothing left to change. That is a real protection and it
    // is not the one the comment describes, it is not written down anywhere,
    // and it evaporates the moment a step touches a row nobody else locks.
    // Claiming first makes the documented mechanism the actual one: the loser
    // blocks here, re-reads after the winner commits, matches nothing, and
    // returns before it has touched an environment.
    const marked = await db.execute<{ id: string }>(sql`
      UPDATE organization_deletions
      SET work_stopped_at = ${now}, updated_at = ${now}
      WHERE org_id = ${orgId}::uuid AND work_stopped_at IS NULL
        AND purged_at IS NULL AND cancelled_at IS NULL
      RETURNING id`)
    if (marked.length === 0) return false

    const environments = await db.execute<{ id: string }>(sql`
      UPDATE environments SET state = 'torn_down', torn_down_at = ${now}, updated_at = ${now}
      WHERE state <> 'torn_down' RETURNING id`)

    // No updated_at on this table. It has created_at, started_at and
    // finished_at and nothing else about time, which is right for a row that
    // describes one run rather than a record somebody edits.
    const runs = await db.execute<{ id: string }>(sql`
      UPDATE runs SET state = 'cancelled', finished_at = ${now}
      WHERE state IN ('queued', 'running') RETURNING id`)

    await db.execute(sql`
      UPDATE organizations
      SET suspended_at = ${now}, suspended_reason = ${SUSPENDED_BY_DELETION},
          suspended_by = 'the deletion this organization asked for', updated_at = ${now}
      WHERE id = ${orgId}::uuid AND suspended_at IS NULL`)

    // Recorded by the caller that did the work, which is now necessarily the
    // caller that holds the claim. Before, the counts came from whoever won
    // the final statement, and the loser's counts were zero because the winner
    // had already changed every row it would have counted. A ledger saying
    // zero environments were stopped on a deletion that stopped one is worse
    // than no ledger, because it is the record somebody reads afterwards to
    // find out what happened.
    await db.execute(sql`
      UPDATE organization_deletions
      SET environments_stopped = ${environments.length},
          runs_cancelled = ${runs.length}, updated_at = ${now}
      WHERE org_id = ${orgId}::uuid`)

    await appendAudit(db, {
      orgId,
      actorLabel: 'the deletion this organization asked for',
      action: 'organization.deletion_stopped_work',
      targetType: 'organization',
      targetId: orgId,
      origin: 'system',
      detail: { environments: environments.length, runs: runs.length },
      occurredAt: deps.clock.now(),
    })
    return true
  })
}

// ---------------------------------------------------------------------------
// Step 2: cancel the subscription
// ---------------------------------------------------------------------------

/**
 * Ends the billing relationship, and records when the entitlement it bought
 * actually stops.
 *
 * Three cases, and the difference between them is what a person needs when they
 * ask what happened to their money:
 *
 *   no subscription        recorded with a null subscription id and no wait.
 *                          The common case, and it is not an error.
 *   cancelled here         the id and the period end are recorded.
 *   cannot be cancelled    Stripe is not configured on a control plane that has
 *                          a live subscription row. The deletion STOPS. Purging
 *                          would remove the only record of a relationship that
 *                          is still charging somebody.
 */
async function cancelSubscription(deps: DeletionDeps, orgId: string): Promise<boolean> {
  const live = await deps.pool.withTenant({ orgId }, async (db) => {
    const statuses = sql.join(LIVE_STATUSES.map((s) => sql`${s}`), sql`, `)
    const rows = await db.execute<{
      stripe_subscription_id: string
      status: string
      current_period_end: Date | string | null
    }>(sql`
      SELECT stripe_subscription_id, status, current_period_end
      FROM subscriptions WHERE org_id = ${orgId}::uuid AND status IN (${statuses})
      ORDER BY created_at DESC LIMIT 1`)
    return rows[0] ?? null
  })

  let subscriptionId: string | null = null
  let entitlementEndsAt: Date | null = null

  if (live) {
    if (!deps.stripe) {
      throw new DeletionError(
        'This organization has a live subscription and this control plane is not configured to ' +
          'talk to Stripe, so the subscription cannot be cancelled from here. Set ' +
          'AF_STRIPE_SECRET_KEY, AF_STRIPE_WEBHOOK_SECRET, AF_STRIPE_PRICE_TEAM and ' +
          'AF_STRIPE_PRICE_ENTERPRISE, or cancel it in Stripe, and then continue the deletion. ' +
          'Nothing has been deleted.',
      )
    }
    subscriptionId = live.stripe_subscription_id
    let cancelled
    try {
      cancelled = await deps.stripe.client.cancelSubscription(live.stripe_subscription_id)
    } catch (err) {
      // A subscription Stripe has already forgotten is a cancellation that has
      // already happened, and a resumed deletion reaches this a second time. It
      // is only treated that way when Stripe positively says there is no such
      // subscription, never when Stripe simply would not answer.
      if (err instanceof StripeError) {
        const known = await deps.stripe.client.getSubscription(live.stripe_subscription_id)
        if (known !== null && LIVE_STATUSES.includes(known.status)) {
          throw new DeletionError(
            `Stripe would not cancel ${live.stripe_subscription_id}. Nothing has been deleted, ` +
              'and the deletion will try again.',
          )
        }
        cancelled = known
      } else {
        throw err
      }
    }
    // The LATER of what Stripe just said and what this database already
    // believed, not simply Stripe's answer.
    //
    // The asymmetry with reconciliation is deliberate and agreed with the
    // billing side rather than an accident of two authors. `reconcile` takes
    // Stripe's answer as authoritative, which is right for REPAIR: it is fixing
    // local state that is presumed wrong. Revocation is the other direction.
    // Being late to revoke costs a deletion that finishes a day after it could
    // have; being early revokes an organization that is still entitled.
    //
    // Stripe is authoritative about a subscription, so preferring its number
    // looks obviously right, and it is wrong in one direction that matters: a
    // response that omits the period, or carries a stale one, would shorten the
    // wait and revoke credentials on an organization that is still entitled.
    // Taking the later of the two costs a day when this database is stale and
    // costs a paying customer their access when Stripe's answer is. That is the
    // same rule the wait applies on every pass, for the same reason.
    entitlementEndsAt = later(
      cancelled?.currentPeriodEnd ?? null,
      live.current_period_end ? asDate(live.current_period_end) : null,
    )

    if (cancelled) {
      await deps.pool.withTenant({ orgId }, async (db) => {
        // The same write subscriptions.cancel makes, and for the same reason it
        // leaves last_event_at alone: advancing the watermark here would make
        // the customer.subscription.updated Stripe is about to send look stale,
        // and that delivery carries the period this response does not.
        await db.execute(sql`
          UPDATE subscriptions
          SET cancel_at_period_end = ${cancelled.cancelAtPeriodEnd}, status = ${cancelled.status},
              canceled_at = ${cancelled.canceledAt ? cancelled.canceledAt.toISOString() : null},
              updated_at = ${deps.clock.now().toISOString()}
          WHERE org_id = ${orgId}::uuid
            AND stripe_subscription_id = ${cancelled.id}`)
      })
    }
    // A period that has already ended is not a wait.
    if (entitlementEndsAt && entitlementEndsAt.getTime() <= deps.clock.now().getTime()) {
      entitlementEndsAt = null
    }
  }

  return deps.pool.withTenant({ orgId }, async (db) => {
    const now = deps.clock.now().toISOString()
    const marked = await db.execute<{ id: string }>(sql`
      UPDATE organization_deletions
      SET subscription_cancelled_at = ${now}, subscription_id = ${subscriptionId},
          entitlement_ends_at = ${entitlementEndsAt ? entitlementEndsAt.toISOString() : null},
          updated_at = ${now}
      WHERE org_id = ${orgId}::uuid AND subscription_cancelled_at IS NULL
        AND work_stopped_at IS NOT NULL AND purged_at IS NULL AND cancelled_at IS NULL
      RETURNING id`)
    if (marked.length === 0) return false

    await appendAudit(db, {
      orgId,
      actorLabel: 'the deletion this organization asked for',
      action: 'organization.deletion_cancelled_subscription',
      targetType: 'organization',
      targetId: orgId,
      origin: 'system',
      detail: {
        subscription: subscriptionId,
        entitlementEndsAt: entitlementEndsAt ? entitlementEndsAt.toISOString() : null,
      },
      occurredAt: deps.clock.now(),
    })
    return true
  })
}

// ---------------------------------------------------------------------------
// Step 3: the wait
// ---------------------------------------------------------------------------

/**
 * The gate in front of revocation, and the place a late webhook is caught.
 *
 * `entitlement_ends_at` was read when the cancellation was made, and Stripe can
 * move it afterwards: a `customer.subscription.updated` that was in flight at
 * that moment, a proration, an invoice that extended the period. The stored
 * value would then be too early, and the deletion would revoke credentials on
 * an organization that is still entitled.
 *
 * So the subscription row is re-read every time the wait is examined and the
 * later of the two is kept. Moving it forward is the safe direction: the cost
 * of being late is a deletion that finishes a day after it could have, and the
 * cost of being early is breaking something somebody paid for.
 *
 * It is called from two places and the second one is the one that matters. The
 * wait calls it, which keeps the date on screen fresh. REVOCATION calls it too,
 * and without that the protection is not there at all: `deletions_due` and the
 * derived step both read the STORED end, so once the original date passes the
 * record moves to revocation and would never re-read the subscription that had
 * meanwhile extended it. Measured, not reasoned about: the ordering test purged
 * an organization that was still entitled until this call was added here.
 *
 * Returns whether the wait is still on after the refresh, so the caller can
 * stop rather than having to ask again.
 */
async function refreshEntitlement(deps: DeletionDeps, orgId: string): Promise<boolean> {
  return deps.pool.withTenant({ orgId }, async (db) => {
    const rows = await db.execute<{
      entitlement_ends_at: Date | string | null
      subscription_id: string | null
    }>(sql`
      SELECT entitlement_ends_at, subscription_id FROM organization_deletions
      WHERE org_id = ${orgId}::uuid AND purged_at IS NULL AND cancelled_at IS NULL`)
    const row = rows[0]
    if (!row) return false
    if (!row.entitlement_ends_at) return false

    let endsAt = asDate(row.entitlement_ends_at)
    if (row.subscription_id) {
      const fresh = await db.execute<{ current_period_end: Date | string | null }>(sql`
        SELECT current_period_end FROM subscriptions
        WHERE org_id = ${orgId}::uuid AND stripe_subscription_id = ${row.subscription_id}`)
      const end = fresh[0]?.current_period_end
      if (end && asDate(end).getTime() > endsAt.getTime()) {
        endsAt = asDate(end)
        await db.execute(sql`
          UPDATE organization_deletions
          SET entitlement_ends_at = ${endsAt.toISOString()},
              updated_at = ${deps.clock.now().toISOString()}
          WHERE org_id = ${orgId}::uuid AND purged_at IS NULL AND cancelled_at IS NULL`)
      }
    }
    return endsAt.getTime() > deps.clock.now().getTime()
  })
}

// ---------------------------------------------------------------------------
// Step 4: revoke credentials and installations
// ---------------------------------------------------------------------------

/**
 * Takes away everything that can still act as this organization.
 *
 * The GitHub installation is the one that is not in this database. Marking it
 * suspended here and leaving it installed would leave an App able to dispatch
 * workflows in the customer's repositories after we told them the organization
 * was gone, so the removal is a real call and a refusal from GitHub stops the
 * deletion. An installation GitHub has already forgotten is success; a GitHub
 * that will not answer is not.
 *
 * Sessions go too, which signs everybody out. They can sign back in, because
 * their membership is still there until the purge, and doing so is how somebody
 * calls the deletion off after this point. The export link they were given at
 * request time does not depend on a session at all, which is the reason it is a
 * link.
 */
async function revokeCredentials(deps: DeletionDeps, orgId: string): Promise<boolean> {
  // Before anything is taken away, and this is where the wait is actually
  // enforced. Both the due query and the derived step read the stored end, so a
  // subscription that was extended after the cancellation would have moved this
  // record to revocation on the original date and nothing else would have
  // looked again.
  if (await refreshEntitlement(deps, orgId)) return false

  const installations = await deps.pool.withTenant({ orgId }, async (db) =>
    db.execute<{ id: string; installation_id: string }>(sql`
      SELECT id, installation_id FROM github_installations WHERE suspended_at IS NULL`),
  )

  let removed = 0
  for (const installation of installations) {
    const outcome = await deps.github.revokeInstallation(Number(installation.installation_id))
    if (outcome.configured) removed += 1
  }

  return deps.pool.withTenant({ orgId }, async (db) => {
    const now = deps.clock.now().toISOString()

    const tokens = await db.execute<{ id: string }>(sql`
      UPDATE engine_tokens SET revoked_at = ${now} WHERE revoked_at IS NULL RETURNING id`)
    const keys = await db.execute<{ id: string }>(sql`
      UPDATE provider_keys SET revoked_at = ${now} WHERE revoked_at IS NULL RETURNING id`)
    const sessions = await db.execute<{ id: string }>(sql`
      UPDATE sessions SET revoked_at = ${now}
      WHERE org_id = ${orgId}::uuid AND revoked_at IS NULL RETURNING id`)
    await db.execute(sql`
      UPDATE github_installations SET suspended_at = ${now}, updated_at = ${now}
      WHERE suspended_at IS NULL`)

    const marked = await db.execute<{ id: string }>(sql`
      UPDATE organization_deletions
      SET credentials_revoked_at = ${now}, engine_tokens_revoked = ${tokens.length},
          provider_keys_revoked = ${keys.length}, sessions_revoked = ${sessions.length},
          installations_revoked = ${removed}, updated_at = ${now}
      WHERE org_id = ${orgId}::uuid AND credentials_revoked_at IS NULL
        AND subscription_cancelled_at IS NOT NULL
        AND (entitlement_ends_at IS NULL OR entitlement_ends_at <= ${now})
        AND purged_at IS NULL AND cancelled_at IS NULL
      RETURNING id`)
    if (marked.length === 0) return false

    await appendAudit(db, {
      orgId,
      actorLabel: 'the deletion this organization asked for',
      action: 'organization.deletion_revoked_credentials',
      targetType: 'organization',
      targetId: orgId,
      origin: 'system',
      detail: {
        engineTokens: tokens.length,
        providerKeys: keys.length,
        sessions: sessions.length,
        installations: removed,
        installationsSeen: installations.length,
      },
      occurredAt: deps.clock.now(),
    })
    return true
  })
}

// ---------------------------------------------------------------------------
// Step 5: the export
// ---------------------------------------------------------------------------

/**
 * Writes the document the customer is owed.
 *
 * Before the purge and not after it, because after it there is nothing left to
 * build one from. The row it lands in already exists, with the hash of the link
 * the requester was handed; this fills it.
 */
async function produceExport(deps: DeletionDeps, orgId: string): Promise<boolean> {
  return deps.pool.withTenant({ orgId }, async (db) => {
    const rows = await db.execute<{ id: string }>(sql`
      SELECT id FROM organization_deletions
      WHERE org_id = ${orgId}::uuid AND purged_at IS NULL AND cancelled_at IS NULL`)
    const deletionId = rows[0]?.id
    if (!deletionId) return false

    const document = await buildExport(db, deps.clock, {
      orgId,
      generatedBy: 'the deletion this organization asked for',
    })
    const text = JSON.stringify(document)
    const entryCount = Object.values(document.counts).reduce((a, b) => a + b, 0)

    const now = deps.clock.now().toISOString()
    await db.execute(sql`
      UPDATE organization_deletion_exports
      SET document = ${text}::jsonb, entry_count = ${entryCount},
          size_bytes = ${Buffer.byteLength(text, 'utf8')}
      WHERE deletion_id = ${deletionId}::uuid`)

    const marked = await db.execute<{ id: string }>(sql`
      UPDATE organization_deletions
      SET exported_at = ${now}, updated_at = ${now}
      WHERE id = ${deletionId}::uuid AND exported_at IS NULL
        AND credentials_revoked_at IS NOT NULL
      RETURNING id`)
    if (marked.length === 0) return false

    await appendAudit(db, {
      orgId,
      actorLabel: 'the deletion this organization asked for',
      action: 'organization.deletion_exported',
      targetType: 'organization',
      targetId: orgId,
      origin: 'system',
      detail: { sizeBytes: Buffer.byteLength(text, 'utf8') },
      occurredAt: deps.clock.now(),
    })
    return true
  })
}

// ---------------------------------------------------------------------------
// Step 6: the purge
// ---------------------------------------------------------------------------

/**
 * The one statement, at the end, after everything that could not be rolled back
 * has already happened.
 *
 * There is no audit entry for this step and there cannot be: `audit_entries`
 * references `organizations`, so an entry written before the DELETE is removed
 * by the same cascade and one written after it has nothing to reference. The
 * deletion record is the evidence, which is why it has no foreign key and why
 * it carries the counts. The export, taken one step earlier, holds the audit
 * log as it stood.
 *
 * The record's own UPDATE is in the same transaction as the DELETE and after
 * it, which works because the record does not cascade: the tenant policy still
 * resolves through `current_org()`, which is a setting rather than a row.
 */
async function purge(deps: DeletionDeps, orgId: string): Promise<boolean> {
  return deps.pool.withTenant({ orgId }, async (db) => {
    const now = deps.clock.now().toISOString()
    const ready = await db.execute<{ id: string }>(sql`
      SELECT id FROM organization_deletions
      WHERE org_id = ${orgId}::uuid AND purged_at IS NULL AND cancelled_at IS NULL
        AND exported_at IS NOT NULL`)
    if (ready.length === 0) return false

    const gone = await db.execute<{ id: string }>(sql`
      DELETE FROM organizations WHERE id = ${orgId}::uuid RETURNING id`)

    const marked = await db.execute<{ id: string }>(sql`
      UPDATE organization_deletions SET purged_at = ${now}, updated_at = ${now}
      WHERE id = ${ready[0]!.id}::uuid AND purged_at IS NULL
      RETURNING id`)
    // The organization row being already gone is not a reason to leave the
    // record open forever. It is recorded as purged either way, because that is
    // what is true.
    void gone
    return marked.length > 0
  })
}

// ---------------------------------------------------------------------------
// The export the record holds
// ---------------------------------------------------------------------------

export function hashExportToken(token: string): Buffer {
  return createHash('sha256').update(token, 'utf8').digest()
}

export interface HeldExport {
  organization: string
  slug: string
  generatedAt: string | null
  expiresAt: string
  sizeBytes: number
  /** Null when this was a describe rather than a download. */
  document: unknown
}

/**
 * Serves the export to whoever holds the link.
 *
 * No session, no membership, no organization: after the purge none of those
 * exist. The token is the authorisation, and the policies in migrations/0022
 * confine a caller declaring it to exactly this one row.
 */
export async function readHeldExport(
  pool: Pool,
  clock: Clock,
  token: string,
  /**
   * Whether this is the download or a look at it.
   *
   * The page a link opens describes the export before offering it, so it calls
   * this too, and counting that as a download would make the count a measure of
   * how many times somebody opened the page. `describe` also leaves the
   * document out of the answer, which is the difference between a metadata
   * request and several megabytes of JSON.
   */
  mode: 'download' | 'describe' = 'download',
): Promise<
  | { found: false; state: 'unknown' | 'destroyed' | 'expired' | 'not_ready'; reason: string }
  | { found: true; value: HeldExport }
> {
  const tokenHash = hashExportToken(token)
  return pool.withoutTenant(
    async (db) => {
      const rows = await db.execute<{
        deletion_id: string
        document: unknown
        size_bytes: string
        expires_at: Date | string
        destroyed_at: Date | string | null
        org_name: string
        org_slug: string
        exported_at: Date | string | null
      }>(sql`
        SELECT e.deletion_id, e.document, e.size_bytes, e.expires_at, e.destroyed_at,
               d.org_name, d.org_slug, d.exported_at
        FROM organization_deletion_exports e
        JOIN organization_deletions d ON d.id = e.deletion_id
        WHERE e.token_hash = ${tokenHash}`)
      const row = rows[0]
      // A state as well as a sentence, because the page has to title itself
      // differently for the two cases that are not the same thing: a link that
      // was never valid, and a valid link for an export the deletion has not
      // produced yet. Titling the second "not valid" tells somebody to go and
      // find another link when the one they have is the right one.
      if (!row) {
        return {
          found: false as const,
          state: 'unknown' as const,
          reason: 'That download link is not valid.',
        }
      }
      if (row.destroyed_at) {
        return {
          found: false as const,
          state: 'destroyed' as const,
          reason: 'That export has been destroyed. Downloads are only available for a limited time.',
        }
      }
      if (asDate(row.expires_at).getTime() <= clock.now().getTime()) {
        return {
          found: false as const,
          state: 'expired' as const,
          reason: 'That export has expired. Downloads are only available for a limited time.',
        }
      }
      if (!row.exported_at) {
        return {
          found: false as const,
          state: 'not_ready' as const,
          reason:
            'The export is not ready yet. It is produced once the deletion has stopped work, ' +
            'ended billing and revoked credentials.',
        }
      }

      if (mode === 'download') {
        await db.execute(sql`
          UPDATE organization_deletion_exports
          SET downloaded_at = ${clock.now().toISOString()}, download_count = download_count + 1
          WHERE token_hash = ${tokenHash}`)
      }

      return {
        found: true as const,
        value: {
          organization: row.org_name,
          slug: row.org_slug,
          generatedAt: iso(row.exported_at),
          expiresAt: iso(row.expires_at)!,
          sizeBytes: Number(row.size_bytes),
          document: mode === 'download' ? row.document : null,
        },
      }
    },
    { deletionTokenHash: tokenHash },
  )
}

/** Destroys the held document early, at the owner's request. */
export async function destroyHeldExport(
  deps: DeletionDeps,
  actor: { orgId: string; userId: string; label: string },
): Promise<{ destroyed: boolean }> {
  return deps.pool.withTenant({ orgId: actor.orgId, userId: actor.userId }, async (db) => {
    const rows = await db.execute<{ deletion_id: string }>(sql`
      UPDATE organization_deletion_exports
      SET document = '{}'::jsonb, size_bytes = 0, entry_count = 0,
          destroyed_at = ${deps.clock.now().toISOString()}
      WHERE destroyed_at IS NULL AND org_id = ${actor.orgId}::uuid
      RETURNING deletion_id`)
    return { destroyed: rows.length > 0 }
  })
}

// ---------------------------------------------------------------------------
// The resumer
// ---------------------------------------------------------------------------

export interface ResumeReport {
  examined: number
  advanced: number
  failed: number
  exportsDestroyed: number
}

/**
 * Picks up every deletion that is due and pushes it along.
 *
 * Runs on the same interval as the session sweep, and it has to exist rather
 * than being a nicety: step 3 is a paid period, which can be a month. Nobody is
 * going to come back and press a button, and a deletion that stops halfway is
 * exactly the state a customer asked us not to leave them in.
 *
 * One organization failing does not stop the others. A step that throws is
 * recorded on its own record with the message, the attempt count goes up, and
 * `deletions_due` will not offer that record again for a minute per attempt, so
 * a deterministic failure does not fill the log.
 */
export async function resumeDeletions(
  deps: DeletionDeps,
  limit = 20,
): Promise<ResumeReport> {
  const report: ResumeReport = { examined: 0, advanced: 0, failed: 0, exportsDestroyed: 0 }

  const due = await deps.pool.withoutTenant(async (db) =>
    db.execute<{ org_id: string }>(sql`
      SELECT org_id FROM deletions_due(${deps.clock.now().toISOString()}, ${limit})`),
  )

  for (const row of due) {
    report.examined += 1
    try {
      const { moved } = await runToCompletion(deps, row.org_id)
      if (moved) report.advanced += 1
    } catch (err) {
      report.failed += 1
      deps.log?.(`organization deletion ${row.org_id} could not be advanced`, err)
    }
  }

  const expired = await deps.pool.withoutTenant(async (db) =>
    db.execute<{ org_id: string; deletion_id: string }>(sql`
      SELECT org_id, deletion_id
      FROM deletion_exports_expired(${deps.clock.now().toISOString()}, ${limit})`),
  )
  for (const row of expired) {
    try {
      await deps.pool.withTenant({ orgId: row.org_id }, async (db) => {
        await db.execute(sql`
          UPDATE organization_deletion_exports
          SET document = '{}'::jsonb, size_bytes = 0, entry_count = 0,
              destroyed_at = ${deps.clock.now().toISOString()}
          WHERE deletion_id = ${row.deletion_id}::uuid AND destroyed_at IS NULL`)
      })
      report.exportsDestroyed += 1
    } catch (err) {
      deps.log?.(`deletion export ${row.deletion_id} could not be destroyed`, err)
    }
  }

  return report
}

// ---------------------------------------------------------------------------

async function recordError(
  deps: DeletionDeps,
  orgId: string,
  step: string,
  message: string,
): Promise<void> {
  try {
    await deps.pool.withTenant({ orgId }, async (db) => {
      await db.execute(sql`
        UPDATE organization_deletions
        SET last_error_at = ${deps.clock.now().toISOString()}, last_error_step = ${step},
            last_error_message = ${message.slice(0, 1000)}, attempts = attempts + 1,
            updated_at = ${deps.clock.now().toISOString()}
        WHERE org_id = ${orgId}::uuid AND purged_at IS NULL AND cancelled_at IS NULL`)
    })
  } catch (err) {
    // Recording the failure failing must not replace the failure. The original
    // is what the caller is about to be told about.
    deps.log?.(`could not record a deletion failure for ${orgId}`, err)
  }
}

async function clearError(deps: DeletionDeps, orgId: string): Promise<void> {
  await deps.pool.withTenant({ orgId }, async (db) => {
    await db.execute(sql`
      UPDATE organization_deletions
      SET last_error_at = NULL, last_error_step = NULL, last_error_message = NULL, attempts = 0,
          updated_at = ${deps.clock.now().toISOString()}
      WHERE org_id = ${orgId}::uuid AND last_error_at IS NOT NULL
        AND purged_at IS NULL AND cancelled_at IS NULL`)
  })
}

function iso(v: Date | string | null): string | null {
  if (v === null || v === undefined) return null
  return (v instanceof Date ? v : new Date(v)).toISOString()
}

function asDate(v: Date | string): Date {
  return v instanceof Date ? v : new Date(v)
}

/** The later of two instants, either of which may be absent. */
function later(a: Date | null, b: Date | null): Date | null {
  if (!a) return b
  if (!b) return a
  return a.getTime() >= b.getTime() ? a : b
}
