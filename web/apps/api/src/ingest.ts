// Event ingestion.
//
// Everything the control plane knows about an environment arrives here, from
// engines running on developer machines and CI runners that the control plane
// cannot reach and does not trust. Four properties matter, and each one exists
// because the network guarantees its opposite.
//
// Duplicates. A retry after a timeout is the normal case, not the exception:
// the engine sent the batch, the response was lost, and it has no way to know
// which. So an event carries an identifier and the second copy is dropped by a
// unique constraint rather than by anything the sender has to get right.
//
// Order. Events arrive out of order routinely, and the last one to land is not
// the newest. So the state an event implies is applied only when its sequence
// is higher than the last one applied, and a status that flips back from
// running to creating is a status nobody believes again.
//
// Bursts. An engine that was offline sends everything at once. The limit
// answers 429 with Retry-After, because an engine refused without being told
// when retries immediately.
//
// Partial success. A batch with one bad event must not discard the good ones,
// and must not silently accept the bad one either. Each event is reported on
// individually.

import { createHash, timingSafeEqual } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Db, Pool } from '@antifailure/db'
import type { Clock } from './clock.ts'
import type { RateLimiter } from './ratelimit.ts'
import type { Analytics } from './analytics/record.ts'
import { durationBucket, runKind, runtimeClass, verdictValue } from './analytics/normalize.ts'
import { isWorkloadEvent, projectWorkloadEvent } from './workloads/projection.ts'

/** The event types the control plane understands. An event of any other type
 *  is stored but changes nothing, so an older control plane can ingest a newer
 *  engine's events without refusing them. */
export const EVENT_TYPES = [
  'environment.queued',
  'environment.creating',
  'environment.ready',
  'environment.sleeping',
  'environment.failed',
  'environment.torn_down',
  'run.started',
  'run.finished',
  'verdict.recorded',
  'artifact.stored',
  'golden.published',
  'network.decision',
  // Workload Studio. A run is created here and its identifier is handed to the
  // engine, so every one of these carries workload_run_id in its payload; see
  // workloads/projection.ts for what each does and which orderings it survives.
  'workload.started',
  'workload.finished',
  'workload.cancelled',
] as const

export interface IncomingEvent {
  /** The sender's identifier, unique within an organization. */
  id: string
  type: string
  /** The engine's environment identifier. */
  envId?: string
  /** Monotonic within an environment. */
  sequence?: number
  occurredAt: string
  payload?: Record<string, unknown>
}

export type EventOutcome =
  /** `note` is present only when the event was stored and could not be applied
   *  to the environments projection, and it says why. Additive on purpose: a
   *  sender that ignores it behaves exactly as it did before. */
  | { id: string; status: 'accepted'; note?: string }
  | { id: string; status: 'duplicate' }
  | { id: string; status: 'rejected'; reason: string }

export interface IngestResult {
  accepted: number
  duplicates: number
  rejected: number
  /**
   * Accepted events that changed no environment row, because the control plane
   * could not name the environment they are about.
   *
   * Counted separately from rejected rather than folded into it, because the
   * ingestion loss objective is that rejected stays at zero and these events
   * were not lost: they are in the events table and they will be readable when
   * whatever is missing is fixed. A number that is not zero here is an engine
   * too old to report which repository it is running against.
   */
  unprojected: number
  outcomes: EventOutcome[]
}

export class IngestRefused extends Error {
  readonly status: number
  readonly retryAfterSeconds: number | undefined

  constructor(message: string, status: number, retryAfterSeconds?: number) {
    super(message)
    this.status = status
    this.retryAfterSeconds = retryAfterSeconds
  }
}

export interface AuthenticatedEngine {
  orgId: string
  tokenId: string
  tokenName: string
  plan: string
}

/**
 * The reason an organization is stopped, or null.
 *
 * A separate call from authentication, and the reason is a rule this codebase
 * has now learned five times: a query that runs before the tenant is known
 * cannot read a tenant-scoped table. Folding this into authenticateEngine looked
 * tidier and returned undefined every time, because the organizations table is
 * only visible to a transaction that has already declared which organization it
 * is. The suspension is read here, scoped, where the ordinary policy covers it.
 */
export async function suspensionReason(pool: Pool, orgId: string): Promise<string | null> {
  return pool.withTenant({ orgId }, async (db) => {
    const rows = await db.execute<{ suspended_reason: string | null }>(sql`
      SELECT suspended_reason FROM organizations
      WHERE id = ${orgId} AND suspended_at IS NOT NULL`)
    if (rows.length === 0) return null
    // A suspension with no recorded reason is still a suspension. Returning
    // null for it would silently un-suspend an organization somebody stopped in
    // a hurry.
    return rows[0]!.suspended_reason ?? 'no reason was recorded'
  })
}

/** The most events one request may carry. A larger batch is refused with a
 *  number rather than truncated, because truncation looks like success. */
export const MAX_BATCH = 500

export function hashEngineToken(token: string): Buffer {
  return createHash('sha256').update(token, 'utf8').digest()
}

/**
 * Resolves a bearer token to an organization.
 *
 * The lookup is by hash, so a database read never returns anything usable as a
 * credential. Comparing in the database with an indexed equality is a timing
 * side channel in principle; it is not one in practice here because the value
 * compared is already a hash of a 256-bit secret, so there is nothing to learn
 * by narrowing it.
 *
 * REVOKED AND EXPIRED ARE BOTH REFUSALS, and only one of them used to be. This
 * read `revoked_at` and nothing else, which was correct for exactly as long as
 * every row in the table was immortal. It has not been since the device grant:
 * `expires_at` is set on the ninety day CLI token that `af login` mints, and
 * src/auth/device.ts honours it, so an expired CLI token was refused by
 * /v1/whoami and accepted here. The engine surface is the one that writes, and
 * a credential whose expiry only some of the server believes in has no expiry.
 * The exchanged workflow identity tokens in src/github/exchange.ts live fifteen
 * minutes and rest entirely on this line.
 */
export async function authenticateEngine(
  pool: Pool,
  clock: Clock,
  token: string,
): Promise<AuthenticatedEngine | null> {
  if (!token || token.length < 16) return null
  const hash = hashEngineToken(token)

  // The hash is declared to the transaction so that the policy on the token
  // table returns exactly the row being presented. Without it the lookup runs
  // with no tenant, the table is invisible, and every token is refused: a
  // failure that looks identical to correct rejection of a bad token.
  const engine = await pool.withoutTenant(async (db) => {
    const rows = await db.execute<{
      id: string
      org_id: string
      name: string
      token_hash: Buffer
      revoked_at: Date | string | null
      expires_at: Date | string | null
    }>(sql`
      SELECT id, org_id, name, token_hash, revoked_at, expires_at FROM engine_tokens
      WHERE token_hash = ${hash}`)
    const row = rows[0]
    if (!row || row.revoked_at) return null
    // An expiry that nothing enforced would make "short lived" a comment rather
    // than a property. The column has existed since migration 0012 and was
    // never read, which cost nothing while every token was permanent and would
    // have cost everything the moment one was not: a workflow identity is
    // traded for a token that is supposed to die within the hour, and without
    // this line it would authenticate forever.
    //
    // Compared in this process against the clock this server runs on rather
    // than as `expires_at > now()` in the statement, so that a test can move
    // time and watch a token stop working. A database side comparison would
    // read the wall clock and quietly ignore the FakeClock, which is how an
    // expiry ends up shipped and untested.
    if (row.expires_at !== null && new Date(row.expires_at).getTime() <= clock.now().getTime()) {
      return null
    }
    // Compared again in constant time. The database comparison is what found
    // the row; this is what makes the code correct if the lookup is ever
    // changed to a prefix scan.
    if (row.token_hash.length !== hash.length || !timingSafeEqual(row.token_hash, hash)) return null

    await db.execute(sql`
      UPDATE engine_tokens SET last_used_at = ${clock.now().toISOString()} WHERE id = ${row.id}`)
    return { orgId: row.org_id, tokenId: row.id, tokenName: row.name }
  }, { engineTokenHash: hash })
  if (!engine) return null

  const plan = await pool.withTenant({ orgId: engine.orgId }, async (db) => {
    const rows = await db.execute<{ plan: string }>(sql`
      SELECT plan FROM organizations WHERE id = ${engine.orgId}::uuid`)
    return rows[0]?.plan ?? 'free'
  })
  return { ...engine, plan }
}

/**
 * Ingests a batch.
 *
 * The whole batch is one transaction, so a failure leaves nothing half applied,
 * and duplicates within the same batch collapse the same way duplicates across
 * batches do.
 */
export async function ingest(
  pool: Pool,
  clock: Clock,
  limiter: RateLimiter,
  engine: AuthenticatedEngine,
  events: IncomingEvent[],
  /**
   * Where the engine's events become analytics.
   *
   * Passed in rather than reached for, so a test can prove what a batch records
   * without a second server, and so this module has one place that decides
   * whether analytics happens at all: the recorder does nothing when no
   * surrogate secret is configured, and every call site is unchanged.
   */
  analytics: Analytics,
): Promise<IngestResult> {
  if (events.length === 0) {
    return { accepted: 0, duplicates: 0, rejected: 0, unprojected: 0, outcomes: [] }
  }
  if (events.length > MAX_BATCH) {
    throw new IngestRefused(
      `A batch may carry ${MAX_BATCH} events and this one carries ${events.length}. Split it.`,
      413,
    )
  }

  // One token per event, so a caller sending large batches is limited by the
  // events it sends rather than by how it packages them.
  const verdict = limiter.take(engine.tokenId, events.length)
  if (!verdict.allowed) {
    throw new IngestRefused(
      'Too many events. The control plane is behind; wait and send the same batch again.',
      429,
      verdict.retryAfterSeconds,
    )
  }

  const outcomes: EventOutcome[] = []

  await pool.withTenant({ orgId: engine.orgId }, async (db) => {
    for (const event of events) {
      const problem = validate(event, clock)
      if (problem) {
        outcomes.push({ id: event.id ?? '(no id)', status: 'rejected', reason: problem })
        continue
      }

      const rows = await db.execute<{ id: string }>(sql`
        INSERT INTO events (org_id, idempotency_key, env_id, sequence, type, payload, occurred_at, received_at)
        VALUES (${engine.orgId}, ${event.id}, ${event.envId ?? null}, ${event.sequence ?? 0},
                ${event.type}, ${JSON.stringify(event.payload ?? {})}::jsonb,
                ${event.occurredAt}, ${clock.now().toISOString()})
        -- occurred_at is in the conflict target because events is partitioned on
        -- it and Postgres requires the partition key in the unique constraint.
        -- It costs nothing here: the sender stamps it once and resends it
        -- unchanged, so a retry still collides with its first attempt.
        ON CONFLICT (org_id, idempotency_key, occurred_at) DO NOTHING
        RETURNING id`)

      if (rows.length === 0) {
        outcomes.push({ id: event.id, status: 'duplicate' })
        continue
      }

      const note = await applyToProjection(db, clock, engine, event, analytics)
      outcomes.push(note === null
        ? { id: event.id, status: 'accepted' }
        : { id: event.id, status: 'accepted', note })
    }
  })

  return {
    accepted: outcomes.filter((o) => o.status === 'accepted').length,
    duplicates: outcomes.filter((o) => o.status === 'duplicate').length,
    rejected: outcomes.filter((o) => o.status === 'rejected').length,
    unprojected: outcomes.filter((o) => o.status === 'accepted' && o.note !== undefined).length,
    outcomes,
  }
}

const MAX_CLOCK_SKEW_MS = 24 * 60 * 60 * 1000

function validate(event: IncomingEvent, clock: Clock): string | null {
  if (!event || typeof event !== 'object') return 'the event is not an object'
  if (typeof event.id !== 'string' || event.id.length === 0) return 'the event has no id'
  if (event.id.length > 200) return 'the id is longer than 200 characters'
  if (typeof event.type !== 'string' || event.type.length === 0) return 'the event has no type'
  if (event.type.length > 100) return 'the type is longer than 100 characters'
  if (typeof event.occurredAt !== 'string') return 'the event has no occurredAt'

  const at = new Date(event.occurredAt)
  if (Number.isNaN(at.getTime())) return 'occurredAt is not a timestamp'

  // A developer machine with a wrong clock is common. An event dated next year
  // would sort to the top of every timeline forever, so the boundary is
  // enforced rather than trusted, and the message says what to fix.
  const skew = at.getTime() - clock.now().getTime()
  if (skew > MAX_CLOCK_SKEW_MS) {
    return 'occurredAt is more than a day in the future; check the sending machine’s clock'
  }
  if (-skew > 365 * 24 * 60 * 60 * 1000) {
    return 'occurredAt is more than a year in the past'
  }

  if (event.sequence !== undefined) {
    if (!Number.isInteger(event.sequence) || event.sequence < 0) {
      return 'sequence must be a whole number that is not negative'
    }
    if (event.sequence > Number.MAX_SAFE_INTEGER) return 'sequence is too large'
  }
  if (event.envId !== undefined && typeof event.envId !== 'string') {
    return 'envId must be a string'
  }
  return null
}

const STATE_FOR: Record<string, string> = {
  'environment.queued': 'queued',
  'environment.creating': 'creating',
  'environment.ready': 'running',
  'environment.sleeping': 'sleeping',
  'environment.failed': 'failed',
  'environment.torn_down': 'torn_down',
}

/**
 * Brings the environment row an event refers to into existence, and advances it.
 *
 * An upsert rather than an UPDATE, and that is this function's whole history.
 * It was an UPDATE, and nothing anywhere in the control plane ever inserted an
 * environments row: the three INSERTs in the repository were the test harness,
 * the staging seeder and the backup drill. So this statement matched zero rows
 * on every event of every run, forever. The console's environment list was
 * empty for every real customer, the expiry column it renders was permanently
 * null, and the spend cap and the cost attribution were sums over an empty
 * table, which is a cap that can never trip.
 *
 * The row is created on FIRST SIGHT of the environment rather than by a
 * listener waiting for a creating event. Waiting is the bug: environment.
 * creating is the oldest event of a run, and the engine's sink drops the
 * oldest events first when a failed batch overflows its spool, so the one
 * event a waiting listener needs is the first one lost. Every environment
 * lifecycle event carries the repository, branch, pull request and declared
 * lifetime, so whichever one lands first is enough.
 *
 * Three rules, and the orderings fall out of them rather than out of a case
 * analysis of arrival sequences:
 *
 * Existence and identity are set once and never regressed. repository_id and
 * branch come from whichever event created the row, created_at is the LEAST of
 * every occurred_at seen, and pull_request is filled only while it is null.
 *
 * Lifecycle is applied only when the sequence advances, exactly as it was
 * before. `environments.last_sequence < EXCLUDED.last_sequence` is the same
 * out-of-order defence the WHERE clause used to carry, moved into the conflict
 * arm, and it stays inside the statement rather than in a read and then a
 * decision, so that two events landing at once cannot both apply.
 *
 * expires_at is a pure function of created_at and the declared lifetime, so it
 * converges: every ordering of the same events computes the same expiry,
 * because created_at only ever moves earlier and the lifetime is the same
 * number on every event of the run.
 *
 * Returns a sentence when the event could not be projected, and null when it
 * was. An event about an environment this control plane cannot name is stored,
 * counted and said out loud in the response rather than dropped, because a
 * projection that silently swallows what it cannot handle is how this defect
 * survived in the first place.
 */
async function applyToProjection(
  db: Db,
  clock: Clock,
  engine: AuthenticatedEngine,
  event: IncomingEvent,
  analytics: Analytics,
): Promise<string | null> {
  const orgId = engine.orgId
  // A workload event is about a run rather than about an environment, so it is
  // keyed on the run identifier this control plane minted rather than on the
  // engine's env_id, and it goes to its own projection.
  //
  // The TOKEN travels with it, and not for authorization: this batch is already
  // authenticated and scoped to the organization. It travels because it is the
  // same string `claimRun` writes into `lease_holder`, so it is what says
  // whether the sender still holds the run it is reporting on. A terminal event
  // from an engine that lost the run used to end it, and destroy the report of
  // the engine that had taken it over.
  if (isWorkloadEvent(event.type)) {
    return projectWorkloadEvent(db, clock, orgId, engine.tokenId, event)
  }

  switch (event.type) {
    case 'golden.published':
      return applyGolden(db, orgId, event)
    case 'run.started':
    case 'run.finished':
      return applyRun(db, clock, orgId, event)
    case 'verdict.recorded':
      // Counted BEFORE the projection and deliberately not instead of it. This
      // hook used to sit above the dispatch and return null, because nothing
      // projected a verdict at the time; main now projects one in applyVerdict,
      // so returning here would have made that work unreachable while still
      // type checking and still passing every analytics test.
      //
      // Unconditional, and not gated on applyVerdict succeeding: the event says
      // a verdict arrived, which is true even when the projection cannot hang it
      // on an environment this control plane has never heard of.
      //
      // Reached only for an event the INSERT above actually inserted, so a
      // resent batch does not count a run twice and this needs no idempotency
      // of its own. NOTHING IN THE ENGINE EMITS verdict.recorded TODAY:
      // agent.verdict is one of the five types
      // engine/internal/controlplane/vocabulary_test.go names as mapped and
      // emitted by nothing, so the validation funnel is honestly empty rather
      // than quietly missing.
      await analytics.record(db, {
        name: 'validation.run_finished',
        occurredAt: new Date(event.occurredAt),
        orgId,
        payload: {
          kind: runKind(event.payload?.kind ?? event.payload?.workflow_kind),
          verdict: verdictValue(event.payload?.value),
        },
      })
      return applyVerdict(db, orgId, event)
    default:
      return applyEnvironment(db, clock, orgId, event, analytics)
  }
}

async function applyEnvironment(
  db: Db,
  clock: Clock,
  orgId: string,
  event: IncomingEvent,
  // Carried in because the environment counters live in this half of the
  // dispatch: advanceExisting and countEnvironment below both record. Main
  // extracted this function out of applyToProjection, where analytics was
  // already in scope, so the parameter has to follow the code that uses it.
  analytics: Analytics,
): Promise<string | null> {
  const state = STATE_FOR[event.type]
  if (!state || !event.envId) return null

  const sequence = event.sequence ?? 0
  const payload = event.payload ?? {}
  const previewUrl = text(payload.preview_url, 2048)
  const runtime = text(payload.runtime, 200)
  const golden = text(payload.golden_version, 200)
  const branch = text(payload.branch, 255)
  const repository = repositoryName(payload.repository)
  const pullRequest = wholeNumber(payload.pull_request)
  const ttl = lifetimeSeconds(payload.ttl_seconds)
  // When the environment began existing, which is not the same instant as
  // when this event fired, and the difference is money. See cameUpAt.
  const cameUp = cameUpAt(payload.started_at, event.occurredAt)
  const now = clock.now().toISOString()
  // The engine's own timestamp, not this clock's. Both ends of the interval a
  // bill is computed from have to come from the same clock or the number is
  // partly a measurement of the network: an environment torn down while the
  // control plane was unreachable, whose event arrives a day later, would
  // otherwise be charged for the day it spent in a spool file. created_at
  // already comes from occurred_at, so this makes the pair consistent.
  const tornDownAt = state === 'torn_down' ? event.occurredAt : null

  if (repository === null || branch === null) {
    const note = await advanceExisting(db, orgId, event.envId, {
      state, sequence, previewUrl, runtime, golden, ttl, tornDownAt, cameUp, now,
    }, analytics)
    return note
  }

  const repositoryId = await repositoryIdFor(db, orgId, repository)

  const applied = await db.execute<ProjectionRow>(sql`
    INSERT INTO environments (
      org_id, repository_id, env_id, branch, pull_request, state, last_sequence,
      preview_url, runtime, golden_version, created_at, updated_at, expires_at, torn_down_at)
    VALUES (
      ${orgId}, ${repositoryId}, ${event.envId}, ${branch}, ${pullRequest},
      ${state}::environment_state, ${sequence},
      ${previewUrl}, ${runtime}, ${golden},
      ${cameUp}::timestamptz, ${now}::timestamptz,
      CASE WHEN ${ttl}::double precision IS NULL THEN NULL
           ELSE ${cameUp}::timestamptz
                + make_interval(secs => ${ttl}::double precision) END,
      ${tornDownAt}::timestamptz)
    -- The engine's own identifier for the environment, scoped to the
    -- organization, which is the key the whole event stream is written
    -- against. A retry of the creating event collides here and updates the one
    -- row rather than making a second.
    ON CONFLICT (org_id, env_id) DO UPDATE SET
      -- Identity. repository_id and branch are deliberately absent from this
      -- list, so a later event cannot move them: an environment id is a
      -- project and a branch run through a hash, and a second answer for
      -- either is a collision to investigate rather than a correction to
      -- apply. pull_request is filled only while it is null, because an
      -- environment created on a branch and later attached to a pull request
      -- is ordinary and losing the number is not.
      pull_request = COALESCE(environments.pull_request, EXCLUDED.pull_request),
      created_at = LEAST(environments.created_at, EXCLUDED.created_at),
      expires_at = CASE WHEN ${ttl}::double precision IS NULL THEN environments.expires_at
                        ELSE LEAST(environments.created_at, EXCLUDED.created_at)
                             + make_interval(secs => ${ttl}::double precision) END,
      -- Lifecycle, only forwards.
      state = CASE WHEN environments.last_sequence < EXCLUDED.last_sequence
                   THEN EXCLUDED.state ELSE environments.state END,
      last_sequence = GREATEST(environments.last_sequence, EXCLUDED.last_sequence),
      preview_url = CASE WHEN environments.last_sequence < EXCLUDED.last_sequence
                         THEN COALESCE(EXCLUDED.preview_url, environments.preview_url)
                         ELSE environments.preview_url END,
      runtime = CASE WHEN environments.last_sequence < EXCLUDED.last_sequence
                     THEN COALESCE(EXCLUDED.runtime, environments.runtime)
                     ELSE environments.runtime END,
      golden_version = CASE WHEN environments.last_sequence < EXCLUDED.last_sequence
                            THEN COALESCE(EXCLUDED.golden_version, environments.golden_version)
                            ELSE environments.golden_version END,
      -- Clamped to created_at, because occurred_at comes from a sender this
      -- service does not trust and an environment torn down before it was
      -- created is a negative duration in every sum computed from these two
      -- columns.
      torn_down_at = CASE WHEN environments.last_sequence < EXCLUDED.last_sequence
                               AND EXCLUDED.torn_down_at IS NOT NULL
                          THEN GREATEST(EXCLUDED.torn_down_at,
                                        LEAST(environments.created_at, EXCLUDED.created_at))
                          ELSE environments.torn_down_at END,
      updated_at = ${now}::timestamptz
    -- xmax is zero on a row this statement INSERTED and non-zero on one it
    -- updated. That is what makes environment.created countable exactly once:
    -- every lifecycle event of a run reaches this statement, so counting on the
    -- event type would count creating and ready and sleeping as three
    -- environments, and counting on a deterministic identifier would need a
    -- timestamp that does not move, which created_at does because it converges
    -- to the LEAST of everything seen.
    RETURNING (xmax = 0) AS created, created_at, torn_down_at, runtime`)

  await countEnvironment(db, analytics, {
    orgId, envId: event.envId, row: applied[0], runtime, declaredLifetime: ttl !== null,
    isTeardown: state === 'torn_down',
  })

  return null
}

interface ProjectionRow extends Record<string, unknown> {
  created: boolean
  created_at: Date | string
  torn_down_at: Date | string | null
  runtime: string | null
}

/**
 * The two environment events, from what the projection statement actually did.
 *
 * Deliberately driven by the row rather than by the incoming event. The row is
 * where the ordering has already been resolved: created_at is the LEAST of
 * every occurred_at seen and torn_down_at is clamped to it, so a duration
 * computed here cannot be negative and cannot depend on which lifecycle event
 * happened to arrive first.
 *
 * The teardown carries a deterministic identifier, because unlike creation
 * there is no xmax to ask. A resent teardown recomputes the same identifier and
 * the same torn_down_at, so it collides and is dropped. Two DIFFERENT teardown
 * times for one environment would be counted twice, and that is an engine
 * reporting a teardown twice with different clocks, which the projection
 * already treats as an anomaly.
 */
interface Counted {
  orgId: string
  envId: string
  row: ProjectionRow | undefined
  runtime: string | null
  declaredLifetime: boolean
  /** Whether the event being applied is the teardown itself. A later event
   *  about an environment that is already down would otherwise re-record the
   *  teardown on every batch: harmless, because the identifier below makes it a
   *  duplicate, and a statement per event for nothing. */
  isTeardown: boolean
}

async function countEnvironment(db: Db, analytics: Analytics, c: Counted): Promise<void> {
  const { row } = c
  if (!row) return

  if (row.created) {
    await analytics.record(db, {
      name: 'environment.created',
      occurredAt: asDate(row.created_at),
      orgId: c.orgId,
      payload: {
        runtime_class: runtimeClass(c.runtime ?? row.runtime),
        declared_lifetime: c.declaredLifetime,
      },
    })
  }

  if (c.isTeardown && row.torn_down_at !== null) {
    const down = asDate(row.torn_down_at)
    await analytics.record(db, {
      name: 'environment.torn_down',
      // The environment and the instant, so two environments torn down in the
      // same millisecond are two events and a resent teardown is one.
      eventId: `env-down:${c.envId}:${down.getTime()}`,
      occurredAt: down,
      orgId: c.orgId,
      payload: {
        duration: durationBucket(down.getTime() - asDate(row.created_at).getTime()),
        runtime_class: runtimeClass(c.runtime ?? row.runtime),
      },
    })
  }
}

function asDate(v: Date | string): Date {
  return v instanceof Date ? v : new Date(v)
}

interface Advance {
  state: string
  sequence: number
  previewUrl: string | null
  runtime: string | null
  golden: string | null
  ttl: number | null
  /** The engine's timestamp for the teardown, or null when this is not one. */
  tornDownAt: string | null
  /** When the environment began existing, per the sender. */
  cameUp: string
  now: string
}

/**
 * The old UPDATE, for an event that does not say which repository it is about.
 *
 * Kept rather than deleted, because an engine older than the release that
 * added the identity fields is a real deployment and not a hypothetical one:
 * customers upgrade the engine on their own schedule and the control plane is
 * upgraded for them. Such an engine can still advance an environment that a
 * newer one created, which is most of the value.
 *
 * What it cannot do is create the row, so a miss is distinguished from a late
 * event and reported. The extra read runs only when nothing was updated, which
 * is the out of order case and the unknown environment case and nothing else.
 */
async function advanceExisting(
  db: Db,
  orgId: string,
  envId: string,
  a: Advance,
  analytics: Analytics,
): Promise<string | null> {
  const updated = await db.execute<ProjectionRow & { id: string }>(sql`
    UPDATE environments SET
      state = ${a.state}::environment_state,
      last_sequence = ${a.sequence},
      preview_url = COALESCE(${a.previewUrl}, preview_url),
      runtime = COALESCE(${a.runtime}, runtime),
      golden_version = COALESCE(${a.golden}, golden_version),
      created_at = LEAST(created_at, ${a.cameUp}::timestamptz),
      expires_at = CASE WHEN ${a.ttl}::double precision IS NULL THEN expires_at
                        ELSE LEAST(created_at, ${a.cameUp}::timestamptz)
                             + make_interval(secs => ${a.ttl}::double precision) END,
      torn_down_at = CASE WHEN ${a.tornDownAt}::timestamptz IS NOT NULL
                          THEN GREATEST(${a.tornDownAt}::timestamptz, created_at)
                          ELSE torn_down_at END,
      updated_at = ${a.now}
    WHERE org_id = ${orgId} AND env_id = ${envId} AND last_sequence < ${a.sequence}
    -- created is false by construction: this statement is an UPDATE and cannot
    -- create the row. It is selected anyway so the shape matches the upsert's
    -- and countEnvironment has one contract rather than two.
    RETURNING id, false AS created, created_at, torn_down_at, runtime`)
  if (updated.length > 0) {
    await countEnvironment(db, analytics, {
      orgId, envId, row: updated[0], runtime: a.runtime,
      declaredLifetime: a.ttl !== null, isTeardown: a.state === 'torn_down',
    })
    return null
  }

  const existing = await db.execute<{ id: string }>(sql`
    SELECT id FROM environments WHERE org_id = ${orgId} AND env_id = ${envId}`)
  if (existing.length > 0) return null

  return (
    `Stored, but ${envId} is not an environment this control plane has heard of, and the ` +
    `event carries no repository and branch to create it from. Upgrade the engine to a ` +
    `version that reports them, or this environment will not appear in the console.`
  )
}


// ---------------------------------------------------------------------------
// The projections that were not there
// ---------------------------------------------------------------------------
//
// golden_versions, runs and verdicts had the same defect the environments
// table had, and it survived the fix to that one because the fix was written
// as a case for environment.* rather than as a rule about projections. The
// engine emitted the events, the sink mapped them, this file accepted the
// types, and STATE_FOR held six keys, so an event of any other type was stored
// and projected nothing. The only INSERTs into all three tables in the whole
// repository were the test harness and the staging seeder.
//
// That is why nobody saw it. The seeder fills every one of them, so the
// console's runs list, its verdicts, the goldens quota on the plan page, the
// masking page's attestation table and the compliance pack's masking control
// all look right in development and are empty for every real customer. The
// pack reported "the check ran and found nothing to show" for an organization
// that produced a signed attestation every night.
//
// Each of these follows the rules applyEnvironment already established, and
// they are stated once here rather than three times below:
//
// Existence on first sight, never on a particular event. A row is created by
// whichever event about it arrives first, because the sink drops the OLDEST
// events of a failed batch and the oldest event is exactly the one a listener
// waiting for a "created" event would need.
//
// Identity from the payload, not from a join that cannot be made. Every one of
// these events carries the repository, for the same reason every environment
// event does: the control plane cannot name a row whose owning repository it
// has not been told, and golden_versions and runs are both keyed through one.
//
// A miss is reported rather than swallowed. An event that cannot be projected
// returns the sentence saying why, it is counted as unprojected, and the
// sender is told. A projection that silently drops what it cannot handle is
// how the first one of these lasted as long as it did.

/**
 * Records a golden version, and the attestation that says what was scanned.
 *
 * Keyed on (org_id, repository_id, version), which is the table's own unique
 * index, so the announcement `af up` makes on every run collides with the
 * first one rather than making a row per branch of the same golden.
 *
 * verified is last-writer-wins, and that is a deliberate choice rather than an
 * oversight. There is no sequence column on this table to order two
 * announcements by, and the engine never announces a version whose
 * verification state it has not already established: every path that emits one
 * has either just run the scanner or has read the flag off the provider's
 * listing. Making it monotonically true instead would mean a golden could
 * never be recorded as failing verification, which is the single fact the
 * compliance control exists to surface.
 *
 * Everything else is COALESCEd, so a sparse announcement cannot erase
 * evidence. The pinned and already-verified paths can emit a version they did
 * not build and therefore have no attestation for, and that event must not
 * blank the attestation the refresh that built it sent.
 */
async function applyGolden(db: Db, orgId: string, event: IncomingEvent): Promise<string | null> {
  const payload = event.payload ?? {}
  const version = text(payload.version, 200)
  const repository = repositoryName(payload.repository)

  if (version === null) {
    return (
      'Stored, but the event carries no golden version, so there is nothing to record it ' +
      'against. This is an engine defect rather than a configuration problem.'
    )
  }
  if (repository === null) {
    return (
      `Stored, but the golden ${version} arrived without a repository, and golden_versions is ` +
      'keyed through one. Upgrade the engine to a version that reports it, or this golden will ' +
      'not appear in the console and the compliance pack will not count it.'
    )
  }

  const repositoryId = await repositoryIdFor(db, orgId, repository)
  const createdAt = timestamp(payload.created_at) ?? event.occurredAt
  // The engine's own word. Absent is not false: an announcement that does not
  // mention verification must not record an unverified golden.
  const verified = typeof payload.verified === 'boolean' ? payload.verified : null
  const rulesDigest = text(payload.rules_digest, 200)
  const sourceDigest = text(payload.source_digest, 200)
  const sizeBytes = wholeBigNumber(payload.size_bytes)
  // Stored as jsonb, so a document that is not JSON is stored as nothing
  // rather than failing the whole event. The compliance pack already reports
  // an attestation it cannot decode as one that does not verify, so a
  // malformed document that did reach the column would be read correctly; one
  // that made the INSERT throw would take the golden row with it.
  const attestation = jsonDocument(payload.attestation)

  await db.execute(sql`
    INSERT INTO golden_versions (
      org_id, repository_id, version, source_digest, rules_digest,
      verified, attestation, size_bytes, created_at)
    VALUES (
      ${orgId}, ${repositoryId}, ${version}, ${sourceDigest}, ${rulesDigest},
      COALESCE(${verified}::boolean, false), ${attestation}::jsonb,
      ${sizeBytes}::bigint, ${createdAt}::timestamptz)
    ON CONFLICT (org_id, repository_id, version) DO UPDATE SET
      verified = COALESCE(${verified}::boolean, golden_versions.verified),
      source_digest = COALESCE(EXCLUDED.source_digest, golden_versions.source_digest),
      rules_digest = COALESCE(EXCLUDED.rules_digest, golden_versions.rules_digest),
      attestation = COALESCE(EXCLUDED.attestation, golden_versions.attestation),
      size_bytes = COALESCE(EXCLUDED.size_bytes, golden_versions.size_bytes),
      -- A golden is as old as the moment it was made, not as old as the last
      -- run that branched it. The compliance pack selects on created_at
      -- within a reporting period, so a version that drifted forward on every
      -- branch would wander out of the period it belongs to.
      created_at = LEAST(golden_versions.created_at, EXCLUDED.created_at)`)

  return null
}

/**
 * Records an agent run, and advances it.
 *
 * runs carries no column for the engine's own identifier for a run, and there
 * is no migration in this change to add one, so the row's primary key is
 * derived from it instead: a version 5 UUID over the organization, the
 * environment and the engine's run id. Deterministic, so run.finished finds
 * the row run.started created without a lookup table, and so a retry that got
 * past the events unique constraint still cannot make a second run.
 *
 * The alternative, a random id plus a run_id column, is the better schema and
 * it is a migration. This is the same answer with the key computed rather than
 * stored, and it is written down here so the next person adding that column
 * knows the ids already in the table are reproducible from the event stream.
 */
async function applyRun(
  db: Db,
  clock: Clock,
  orgId: string,
  event: IncomingEvent,
): Promise<string | null> {
  const payload = event.payload ?? {}
  const runId = text(payload.run_id, 200)
  if (!event.envId || runId === null) {
    return (
      'Stored, but the event names no environment and run, so there is nothing to record. ' +
      'Upgrade the engine to a version that reports both.'
    )
  }

  const environmentId = await environmentIdFor(db, orgId, event, payload)
  if (environmentId === null) {
    return (
      `Stored, but ${event.envId} is not an environment this control plane has heard of, and ` +
      'the event carries no repository and branch to create it from, so the run has nowhere to ' +
      'hang. Upgrade the engine to a version that reports them.'
    )
  }

  const id = deterministicId(orgId, event.envId, runId)
  const sequence = event.sequence ?? 0
  const kind = text(payload.kind, 200) ?? 'workflows'
  const finished = event.type === 'run.finished'
  const state = finished ? runStateFrom(payload.state) : 'running'
  const startedAt = timestamp(payload.started_at) ?? (finished ? null : event.occurredAt)
  const finishedAt = finished ? event.occurredAt : null
  const now = clock.now().toISOString()

  await db.execute(sql`
    INSERT INTO runs (
      id, org_id, environment_id, kind, state, started_at, finished_at, last_sequence, created_at)
    VALUES (
      ${id}::uuid, ${orgId}, ${environmentId}::uuid, ${kind}, ${state}::run_state,
      ${startedAt}::timestamptz, ${finishedAt}::timestamptz, ${sequence}, ${now}::timestamptz)
    ON CONFLICT (id) DO UPDATE SET
      -- Lifecycle only forwards, exactly as the environments projection does
      -- it, and inside the statement rather than as a read and then a
      -- decision, so two events landing at once cannot both apply.
      state = CASE WHEN runs.last_sequence < EXCLUDED.last_sequence
                   THEN EXCLUDED.state ELSE runs.state END,
      last_sequence = GREATEST(runs.last_sequence, EXCLUDED.last_sequence),
      kind = COALESCE(EXCLUDED.kind, runs.kind),
      started_at = LEAST(
        COALESCE(runs.started_at, EXCLUDED.started_at),
        COALESCE(EXCLUDED.started_at, runs.started_at)),
      -- Never earlier than the start, because both timestamps come from a
      -- sender this service does not trust and a run that finished before it
      -- began is a negative duration in every column computed from the pair.
      finished_at = CASE WHEN EXCLUDED.finished_at IS NULL THEN runs.finished_at
                         ELSE GREATEST(EXCLUDED.finished_at,
                                       COALESCE(runs.started_at, EXCLUDED.started_at,
                                                EXCLUDED.finished_at)) END,
      created_at = LEAST(runs.created_at, EXCLUDED.created_at)`)

  return null
}

/**
 * Records one workflow's verdict.
 *
 * Keyed over the run and the workflow, because a workflow produces one verdict
 * per run and the second copy of an event is a retry rather than a second
 * opinion. verdicts carries no unique index for ON CONFLICT to name, so the
 * primary key is the conflict target and it is derived from the identity the
 * row actually has.
 */
async function applyVerdict(db: Db, orgId: string, event: IncomingEvent): Promise<string | null> {
  const payload = event.payload ?? {}
  const runId = text(payload.run_id, 200)
  const workflow = text(payload.workflow, 500)
  if (!event.envId || runId === null || workflow === null) {
    return (
      'Stored, but the event names no environment, run and workflow, so there is no verdict to ' +
      'record. Upgrade the engine to a version that reports all three.'
    )
  }

  const environmentId = await environmentIdFor(db, orgId, event, payload)
  if (environmentId === null) {
    return (
      `Stored, but ${event.envId} is not an environment this control plane has heard of, and ` +
      'the event carries no repository and branch to create it from, so the verdict has nowhere ' +
      'to hang. Upgrade the engine to a version that reports them.'
    )
  }

  // The run row is created here when the verdict is the first thing to arrive
  // about it. A verdict whose run event was dropped from an overflowing spool
  // is the ordinary case, not a hypothetical one, and a verdict that waited
  // for a run.started that already came and went would wait forever.
  const runRow = deterministicId(orgId, event.envId, runId)
  await db.execute(sql`
    INSERT INTO runs (id, org_id, environment_id, kind, state, created_at)
    VALUES (${runRow}::uuid, ${orgId}, ${environmentId}::uuid, 'workflows',
            'running'::run_state, ${event.occurredAt}::timestamptz)
    ON CONFLICT (id) DO NOTHING`)

  const value = verdictValueFrom(payload.value)
  await db.execute(sql`
    INSERT INTO verdicts (
      id, org_id, run_id, workflow, persona, value, summary, steps, duration_ms,
      reproduction, created_at)
    VALUES (
      ${deterministicId(runRow, workflow, '')}::uuid, ${orgId}, ${runRow}::uuid,
      ${workflow}, ${text(payload.persona, 200)}, ${value}::verdict_value,
      ${text(payload.summary, 4000)}, ${wholeNumber(payload.steps) ?? 0},
      ${wholeNumber(payload.duration_ms)}, ${jsonDocument(payload.reproduction)}::jsonb,
      ${event.occurredAt}::timestamptz)
    ON CONFLICT (id) DO UPDATE SET
      persona = COALESCE(EXCLUDED.persona, verdicts.persona),
      value = EXCLUDED.value,
      summary = COALESCE(EXCLUDED.summary, verdicts.summary),
      steps = GREATEST(verdicts.steps, EXCLUDED.steps),
      duration_ms = COALESCE(EXCLUDED.duration_ms, verdicts.duration_ms),
      reproduction = COALESCE(EXCLUDED.reproduction, verdicts.reproduction)`)

  return null
}

/**
 * The environment a run or a verdict hangs off, created if this is the first
 * the control plane has heard of it.
 *
 * The same shape as repositoryIdFor and for the same reason: refusing would
 * mean an agent run reported after its environment's own events were dropped
 * could never appear at all, and that failure looks exactly like the empty
 * console this change exists to fix.
 *
 * The row it creates is deliberately inert. state is left at the column
 * default and last_sequence at zero, so the first environment lifecycle event
 * to arrive advances it normally: a run event carries a high sequence number
 * and writing that number here would make every environment event that
 * followed look stale and leave the environment reading "queued" forever.
 */
async function environmentIdFor(
  db: Db,
  orgId: string,
  event: IncomingEvent,
  payload: Record<string, unknown>,
): Promise<string | null> {
  const found = await db.execute<{ id: string }>(sql`
    SELECT id FROM environments WHERE org_id = ${orgId} AND env_id = ${event.envId ?? null}`)
  if (found[0]) return found[0].id

  const repository = repositoryName(payload.repository)
  const branch = text(payload.branch, 255)
  if (repository === null || branch === null) return null

  const repositoryId = await repositoryIdFor(db, orgId, repository)
  const created = await db.execute<{ id: string }>(sql`
    INSERT INTO environments (org_id, repository_id, env_id, branch, created_at, updated_at)
    VALUES (${orgId}, ${repositoryId}, ${event.envId ?? null}, ${branch},
            ${event.occurredAt}::timestamptz, ${event.occurredAt}::timestamptz)
    ON CONFLICT (org_id, env_id) DO NOTHING
    RETURNING id`)
  if (created[0]) return created[0].id

  const raced = await db.execute<{ id: string }>(sql`
    SELECT id FROM environments WHERE org_id = ${orgId} AND env_id = ${event.envId ?? null}`)
  return raced[0]?.id ?? null
}

/**
 * A UUID derived from what a row is about rather than from randomness.
 *
 * Version 5, name-based, over SHA-1, which is what the standard specifies and
 * is not being relied on for anything a collision would matter to: the inputs
 * are already scoped to one organization, and two of them colliding would take
 * a deliberate construction rather than an accident.
 *
 * It exists so that two events about the same thing, arriving in either order,
 * write one row without a column to join them on.
 */
function deterministicId(a: string, b: string, c: string): string {
  const hash = createHash('sha1').update(`${a} ${b} ${c}`).digest()
  // Version 5 and the RFC 4122 variant, stamped into the bytes the standard
  // reserves for them, so the value is a UUID rather than a hash that fits in
  // one.
  hash[6] = (hash[6]! & 0x0f) | 0x50
  hash[8] = (hash[8]! & 0x3f) | 0x80
  const hex = hash.subarray(0, 16).toString('hex')
  return [
    hex.slice(0, 8), hex.slice(8, 12), hex.slice(12, 16), hex.slice(16, 20), hex.slice(20, 32),
  ].join('-')
}

const RUN_STATES = new Set(['queued', 'running', 'complete', 'failed', 'cancelled'])

/** The run state a finished run reports, defaulting to complete.
 *
 *  A word this control plane does not know is complete rather than failed: the
 *  run happened and this service could not read its outcome, and charging a
 *  newer engine's vocabulary to the customer's application is the mistake the
 *  blocked verdict exists to prevent. */
function runStateFrom(value: unknown): string {
  return typeof value === 'string' && RUN_STATES.has(value) ? value : 'complete'
}

const VERDICT_VALUES = new Set(['pass', 'fail', 'flaky', 'blocked', 'unverified'])

/** The verdict a workflow reached, or unverified.
 *
 *  A word this control plane cannot read is unverified rather than pass or
 *  fail, for the same reason the engine renders it as blocked: a verdict
 *  nobody here understood is not evidence about the application in either
 *  direction. */
function verdictValueFrom(value: unknown): string {
  return typeof value === 'string' && VERDICT_VALUES.has(value) ? value : 'unverified'
}

/** A timestamp a statement can use, or null. The same untrusted sender writes
 *  it that writes occurredAt. */
function timestamp(value: unknown): string | null {
  if (typeof value !== 'string') return null
  const at = new Date(value)
  if (Number.isNaN(at.getTime())) return null
  return at.toISOString()
}

/** A count that may exceed what an integer column holds, for bigint columns. */
function wholeBigNumber(value: unknown): number | null {
  if (typeof value !== 'number' || !Number.isInteger(value)) return null
  if (value < 0 || value > Number.MAX_SAFE_INTEGER) return null
  return value
}

/**
 * A jsonb column's value, or null.
 *
 * The payload has already been through JSON.parse to get here, so a string is
 * a document that was serialised twice: the engine's event fields are strings
 * and an attestation is a JSON document inside one. Parsed rather than stored
 * as a JSON string, because a column holding a quoted document reads as text
 * to every query written against it and the compliance pack casts it to text
 * and parses it again.
 *
 * A string that is not JSON is stored as null rather than thrown, because one
 * malformed field must not discard the row it is attached to.
 */
function jsonDocument(value: unknown): string | null {
  if (value === null || value === undefined) return null
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (trimmed.length === 0) return null
    try {
      JSON.parse(trimmed)
    } catch {
      return null
    }
    return trimmed
  }
  if (typeof value !== 'object') return null
  try {
    return JSON.stringify(value)
  } catch {
    return null
  }
}

/**
 * The repository row an environment hangs off, created if this is the first
 * time the control plane has heard the name.
 *
 * Created rather than refused, and the distinction matters. The repositories
 * table is otherwise filled only by the GitHub App webhook, so refusing here
 * would mean an engine running against a repository the App is not installed
 * on could never report an environment at all: a self-hosted forge, a
 * repository nobody has connected yet, a checkout under a different name. The
 * failure would look exactly like the empty console this change exists to fix.
 *
 * This is not the ghost row that routers/dispatch.ts refuses to write. That one
 * would be an environment nothing had run; this is a name for something an
 * authenticated engine says it is running right now.
 *
 * SELECT first, because the repository already existing is the overwhelmingly
 * common case and it costs one indexed read. The INSERT is idempotent, and the
 * SELECT after it is for the race: two engines reporting the same new
 * repository at once, where the loser's insert returns no row and the winner's
 * is visible by the time it looks again.
 */
async function repositoryIdFor(db: Db, orgId: string, fullName: string): Promise<string> {
  const found = await db.execute<{ id: string }>(sql`
    SELECT id FROM repositories WHERE org_id = ${orgId} AND full_name = ${fullName}`)
  if (found[0]) return found[0].id

  const created = await db.execute<{ id: string }>(sql`
    INSERT INTO repositories (org_id, full_name) VALUES (${orgId}, ${fullName})
    ON CONFLICT (org_id, full_name) DO NOTHING
    RETURNING id`)
  if (created[0]) return created[0].id

  const raced = await db.execute<{ id: string }>(sql`
    SELECT id FROM repositories WHERE org_id = ${orgId} AND full_name = ${fullName}`)
  if (raced[0]) return raced[0].id

  // Unreachable: the insert either created the row or something else did, and
  // both are visible to this transaction. Thrown rather than returned as a
  // null the caller would have to handle, because a repository that neither
  // exists nor can be created means the tenant scoping is wrong, and that is a
  // bug to see rather than an event to skip.
  throw new Error(`the repository ${fullName} could not be created or read back`)
}

/** A payload string, bounded, or null. Anything else on this boundary is a
 *  sender saying something the column cannot hold. */
function text(value: unknown, max: number): string | null {
  if (typeof value !== 'string') return null
  const trimmed = value.trim()
  if (trimmed.length === 0 || trimmed.length > max) return null
  return trimmed
}

/**
 * owner/name as a repository may be called, or null.
 *
 * Shape checked rather than trusted, because this value creates a row. It has
 * to carry at least one slash and no whitespace, which every real forge path
 * does and none of the ways a confused sender spells "unknown" do.
 */
function repositoryName(value: unknown): string | null {
  const name = text(value, 255)
  if (name === null) return null
  if (!name.includes('/')) return null
  if (/\s/.test(name)) return null
  return name
}

/** A pull request number, or null. Zero is not a pull request, and the column
 *  is an integer. */
function wholeNumber(value: unknown): number | null {
  if (typeof value !== 'number' || !Number.isInteger(value)) return null
  if (value <= 0 || value > 2_147_483_647) return null
  return value
}

/**
 * When the environment began existing, as a timestamp the statement can use.
 *
 * The event's own occurred_at is when the event fired; started_at is when the
 * work began, and for env.ready those are separated by the whole build. The
 * control plane bills created_at to torn_down_at, so taking the event's
 * timestamp would drop the build from every environment whose creating event
 * never arrived, and a cold build is the expensive part of a run.
 *
 * Never later than the event that carries it, because an environment cannot
 * have begun after something it did was reported, and never more than a year
 * earlier, which is the same bound validate() puts on occurred_at. Both are
 * clamps rather than rejections: the field comes from a sender this service
 * does not trust, and the failure mode being defended against is a bill, so a
 * value outside the possible range falls back to the event's own timestamp
 * rather than refusing an event that is otherwise fine.
 */
function cameUpAt(value: unknown, occurredAt: string): string {
  if (typeof value !== 'string') return occurredAt
  const at = new Date(value)
  if (Number.isNaN(at.getTime())) return occurredAt
  const fired = new Date(occurredAt).getTime()
  if (Number.isNaN(fired)) return occurredAt
  if (at.getTime() > fired) return occurredAt
  if (fired - at.getTime() > 365 * 24 * 60 * 60 * 1000) return occurredAt
  return at.toISOString()
}

/**
 * A declared lifetime in seconds, or null.
 *
 * Bounded at ten years because it lands in make_interval, and refused at zero
 * or below because an environment that expired the moment it was created is a
 * reaper destroying live work. Absent is a lifetime nobody declared, which is
 * what leaves expires_at null in the console.
 */
function lifetimeSeconds(value: unknown): number | null {
  if (typeof value !== 'number' || !Number.isFinite(value)) return null
  if (value <= 0 || value > 10 * 365 * 24 * 60 * 60) return null
  return value
}
