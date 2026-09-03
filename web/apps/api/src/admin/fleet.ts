// Twins and the runs on them, and the ledger of what has been asked to stop.
//
// The one distinction this file exists to preserve: a teardown that was
// RECORDED is not a teardown that was DISPATCHED, and neither is a teardown
// that was CONFIRMED. Three different situations, and a console that shows one
// "teardown" column collapses them into a word that is true of all three and
// useful for none.
//
//   recorded, nothing to reach   the request has no workflow run and no engine
//                                route, so nothing was ever sent. It will sit
//                                pending until the sweeper abandons it.
//   dispatched, unconfirmed      a cancel went to GitHub, or the engine was
//                                asked. Accepted is not done: GitHub records
//                                the request and the run stops some time later.
//   confirmed                    the runtime said the environment is gone.
//
// The second and third are the ones people conflate, and the queue itself is
// careful about it: sweepTeardowns only marks a row acknowledged when the
// workflow run reaches a terminal status or the engine reports the environment
// torn down. This surface has to be equally careful or it undoes that.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'

export interface TwinRow {
  envId: string
  orgId: string
  orgSlug: string
  repository: string
  branch: string
  pullRequest: number | null
  state: string
  previewUrl: string | null
  runtime: string | null
  goldenVersion: string | null
  createdAt: Date | string
  updatedAt: Date | string
  expiresAt: Date | string | null
  tornDownAt: Date | string | null
  /** Past the lifetime it was created with and not torn down. Costs money. */
  overdue: boolean
  /** A live teardown request exists for this environment. */
  teardownPending: boolean
  runs: number
}

export interface TwinFilter {
  /** One organization, or every organization the connection can see. */
  orgId?: string | null
  /** 'live' excludes torn down, which is what an operator almost always wants:
   *  a fleet view whose default includes every environment ever created is a
   *  page that grows forever and answers nothing. */
  scope?: 'live' | 'overdue' | 'all'
  limit?: number
}

export async function twins(db: Db, now: Date, filter: TwinFilter = {}): Promise<TwinRow[]> {
  const scope = filter.scope ?? 'live'
  const limit = Math.min(Math.max(filter.limit ?? 100, 1), 500)
  const rows = await db.execute<{
    env_id: string
    org_id: string
    org_slug: string
    repository: string
    branch: string
    pull_request: number | null
    state: string
    preview_url: string | null
    runtime: string | null
    golden_version: string | null
    created_at: Date | string
    updated_at: Date | string
    expires_at: Date | string | null
    torn_down_at: Date | string | null
    teardown_pending: boolean
    runs: string
  }>(sql`
    SELECT e.env_id, e.org_id, o.slug AS org_slug, r.full_name AS repository,
           e.branch, e.pull_request, e.state::text AS state, e.preview_url,
           e.runtime, e.golden_version, e.created_at, e.updated_at, e.expires_at,
           e.torn_down_at,
           EXISTS (
             SELECT 1 FROM teardown_requests t
             WHERE t.env_id = e.env_id AND t.state IN ('pending', 'leased')
           ) AS teardown_pending,
           (SELECT count(*) FROM runs rn WHERE rn.environment_id = e.id) AS runs
    FROM environments e
    JOIN organizations o ON o.id = e.org_id
    JOIN repositories r ON r.id = e.repository_id
    WHERE (${filter.orgId ?? null}::uuid IS NULL OR e.org_id = ${filter.orgId ?? null}::uuid)
      AND (
        ${scope} = 'all'
        OR (${scope} = 'live' AND e.state <> 'torn_down')
        OR (${scope} = 'overdue' AND e.state <> 'torn_down'
            AND e.expires_at IS NOT NULL
            AND e.expires_at < ${now.toISOString()}::timestamptz)
      )
    ORDER BY e.created_at DESC
    LIMIT ${limit}`)

  return rows.map((r) => ({
    envId: r.env_id,
    orgId: r.org_id,
    orgSlug: r.org_slug,
    repository: r.repository,
    branch: r.branch,
    pullRequest: r.pull_request,
    state: r.state,
    previewUrl: r.preview_url,
    runtime: r.runtime,
    goldenVersion: r.golden_version,
    createdAt: r.created_at,
    updatedAt: r.updated_at,
    expiresAt: r.expires_at,
    tornDownAt: r.torn_down_at,
    overdue:
      r.state !== 'torn_down' &&
      r.expires_at !== null &&
      new Date(r.expires_at).getTime() < now.getTime(),
    teardownPending: r.teardown_pending,
    runs: Number(r.runs),
  }))
}

/**
 * What actually happened to a teardown request.
 *
 * Derived rather than stored, because the queue's own `state` column answers a
 * different question. `pending` covers both "asked for a second ago" and
 * "asked for yesterday with nothing to reach", and those are not the same
 * situation to anybody looking at this page.
 */
export type TeardownStanding =
  /** Recorded. No workflow run and no environment id, so nothing was sent and
   *  nothing will be: this will sit until it is abandoned. */
  | 'nothing-to-reach'
  /** Recorded, has a route, not yet claimed by a sweeper pass. */
  | 'waiting-to-dispatch'
  /** A sweeper holds it and has asked. Accepted is not confirmed. */
  | 'dispatched-unconfirmed'
  /** The runtime said the environment is gone. */
  | 'confirmed'
  /** Every attempt failed. Nothing will try again. */
  | 'abandoned'

export interface TeardownRow {
  id: string
  orgId: string
  orgSlug: string
  envId: string | null
  repository: string | null
  workflowRunId: string | null
  reason: string
  state: string
  standing: TeardownStanding
  attempts: number
  leaseHolder: string | null
  leasedUntil: Date | string | null
  /** True when the lease has run out and no pass has taken it back yet. */
  leaseExpired: boolean
  lastError: string | null
  requestedAt: Date | string
  acknowledgedAt: Date | string | null
  /** How this request can reach the thing it wants stopped, in words. */
  route: string
}

export function standingOf(
  row: {
    state: string
    env_id: string | null
    workflow_run_id: string | null
    lease_holder: string | null
    attempts: number
  },
): TeardownStanding {
  if (row.state === 'acknowledged') return 'confirmed'
  if (row.state === 'abandoned') return 'abandoned'
  // Nothing to reach is checked BEFORE the lease, because a row with no route
  // can still be leased: the sweeper claims it, finds nothing to ask, and puts
  // it back. Reporting that as "dispatched" would be the exact conflation this
  // module exists to prevent.
  if (!row.env_id && !row.workflow_run_id) return 'nothing-to-reach'
  if (row.state === 'leased' || row.attempts > 0) return 'dispatched-unconfirmed'
  return 'waiting-to-dispatch'
}

export async function teardownLedger(
  db: Db,
  now: Date,
  filter: { orgId?: string | null; open?: boolean; limit?: number } = {},
): Promise<TeardownRow[]> {
  const limit = Math.min(Math.max(filter.limit ?? 100, 1), 500)
  const rows = await db.execute<{
    id: string
    org_id: string
    org_slug: string
    env_id: string | null
    repository: string | null
    workflow_run_id: string | null
    reason: string
    state: string
    attempts: number
    lease_holder: string | null
    leased_until: Date | string | null
    last_error: string | null
    requested_at: Date | string
    acknowledged_at: Date | string | null
  }>(sql`
    SELECT t.id, t.org_id, o.slug AS org_slug, t.env_id, r.full_name AS repository,
           t.workflow_run_id::text AS workflow_run_id, t.reason, t.state, t.attempts,
           t.lease_holder, t.leased_until, t.last_error, t.requested_at, t.acknowledged_at
    FROM teardown_requests t
    JOIN organizations o ON o.id = t.org_id
    LEFT JOIN repositories r ON r.id = t.repository_id
    WHERE (${filter.orgId ?? null}::uuid IS NULL OR t.org_id = ${filter.orgId ?? null}::uuid)
      AND (${filter.open ?? false} = false OR t.state IN ('pending', 'leased'))
    ORDER BY t.requested_at DESC
    LIMIT ${limit}`)

  return rows.map((r) => {
    const standing = standingOf(r)
    return {
      id: r.id,
      orgId: r.org_id,
      orgSlug: r.org_slug,
      envId: r.env_id,
      repository: r.repository,
      workflowRunId: r.workflow_run_id,
      reason: r.reason,
      state: r.state,
      standing,
      attempts: r.attempts,
      leaseHolder: r.lease_holder,
      leasedUntil: r.leased_until,
      leaseExpired:
        r.leased_until !== null && new Date(r.leased_until).getTime() < now.getTime(),
      lastError: r.last_error,
      requestedAt: r.requested_at,
      acknowledgedAt: r.acknowledged_at,
      route: r.workflow_run_id
        ? `the workflow run ${r.workflow_run_id} that built it`
        : r.env_id
          ? `the engine, by environment id ${r.env_id}`
          : 'nothing. No workflow run and no environment id were recorded, so no cancel was ever sent',
    }
  })
}

/** What the operator is told before they confirm stopping everything. */
export interface FleetBlastRadius {
  organizations: number
  environments: number
  runs: number
  /** Environments that already have a live teardown request, so confirming
   *  adds nothing for them. Said out loud so the number the operator confirms
   *  is the number that changes. */
  alreadyRequested: number
}

/**
 * What terminating every twin in scope would actually touch.
 *
 * Computed, not estimated. An operator confirming a blast radius written in
 * prose is confirming somebody's recollection of what the query would return.
 */
export async function fleetBlastRadius(
  db: Db,
  filter: { orgId?: string | null } = {},
): Promise<FleetBlastRadius> {
  const rows = await db.execute<{
    organizations: string
    environments: string
    runs: string
    already: string
  }>(sql`
    SELECT count(DISTINCT e.org_id) AS organizations,
           count(*) AS environments,
           coalesce(sum((SELECT count(*) FROM runs rn
                          WHERE rn.environment_id = e.id
                            AND rn.state IN ('queued', 'running'))), 0) AS runs,
           count(*) FILTER (
             WHERE EXISTS (SELECT 1 FROM teardown_requests t
                            WHERE t.env_id = e.env_id
                              AND t.state IN ('pending', 'leased'))) AS already
    FROM environments e
    WHERE e.state <> 'torn_down'
      AND (${filter.orgId ?? null}::uuid IS NULL OR e.org_id = ${filter.orgId ?? null}::uuid)`)
  const r = rows[0]
  return {
    organizations: Number(r?.organizations ?? 0),
    environments: Number(r?.environments ?? 0),
    runs: Number(r?.runs ?? 0),
    alreadyRequested: Number(r?.already ?? 0),
  }
}

export interface RequestedTeardown {
  envId: string
  /** False when a live request already existed. The operator pressed a button
   *  and nothing new happened, which is a different answer from "done". */
  recorded: boolean
  /** Whether anything can be reached. False means the row was written and no
   *  cancel will ever be sent, which the caller must not report as a teardown. */
  reachable: boolean
}

/**
 * Records a teardown request for every live environment in scope.
 *
 * Writes rows. Sends nothing. The sweeper is what reaches a runtime, and the
 * return value says per environment whether there is anything for it to reach,
 * so the caller cannot report "terminated" over a row that will sit pending
 * until it is abandoned.
 *
 * The caller supplies the transaction so that the requests and the audit entry
 * describing them commit together.
 */
export async function requestFleetTeardown(
  db: Db,
  now: Date,
  actor: { userId: string | null },
  reason: string,
  filter: { orgId?: string | null } = {},
): Promise<RequestedTeardown[]> {
  const targets = await db.execute<{
    id: string
    org_id: string
    env_id: string
    repository_id: string
    workflow_run_id: string | null
    generation_id: string | null
    already: boolean
  }>(sql`
    SELECT e.id, e.org_id, e.env_id, e.repository_id,
           g.workflow_run_id::text AS workflow_run_id, g.id AS generation_id,
           EXISTS (SELECT 1 FROM teardown_requests t
                    WHERE t.env_id = e.env_id AND t.state IN ('pending', 'leased')) AS already
    FROM environments e
    LEFT JOIN LATERAL (
      SELECT workflow_run_id, id FROM pr_generations
      WHERE env_id = e.env_id ORDER BY queued_at DESC LIMIT 1
    ) g ON true
    WHERE e.state <> 'torn_down'
      AND (${filter.orgId ?? null}::uuid IS NULL OR e.org_id = ${filter.orgId ?? null}::uuid)`)

  const out: RequestedTeardown[] = []
  for (const t of targets) {
    if (t.already) {
      out.push({ envId: t.env_id, recorded: false, reachable: t.workflow_run_id !== null })
      continue
    }
    await db.execute(sql`
      INSERT INTO teardown_requests (
        org_id, environment_id, env_id, repository_id, workflow_run_id, generation_id,
        reason, requested_by, requested_at, updated_at)
      VALUES (${t.org_id}::uuid, ${t.id}::uuid, ${t.env_id}, ${t.repository_id}::uuid,
              ${t.workflow_run_id}::bigint, ${t.generation_id}::uuid,
              ${reason.slice(0, 500)}, ${actor.userId}::uuid,
              ${now.toISOString()}::timestamptz, ${now.toISOString()}::timestamptz)`)
    out.push({ envId: t.env_id, recorded: true, reachable: t.workflow_run_id !== null })
  }
  return out
}
