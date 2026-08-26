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
  | { id: string; status: 'accepted' }
  | { id: string; status: 'duplicate' }
  | { id: string; status: 'rejected'; reason: string }

export interface IngestResult {
  accepted: number
  duplicates: number
  rejected: number
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
  return pool.withoutTenant(async (db) => {
    const rows = await db.execute<{
      id: string
      org_id: string
      name: string
      token_hash: Buffer
      revoked_at: Date | string | null
    }>(sql`
      SELECT id, org_id, name, token_hash, revoked_at FROM engine_tokens
      WHERE token_hash = ${hash}`)
    const row = rows[0]
    if (!row || row.revoked_at) return null
    // Compared again in constant time. The database comparison is what found
    // the row; this is what makes the code correct if the lookup is ever
    // changed to a prefix scan.
    if (row.token_hash.length !== hash.length || !timingSafeEqual(row.token_hash, hash)) return null

    await db.execute(sql`
      UPDATE engine_tokens SET last_used_at = ${clock.now().toISOString()} WHERE id = ${row.id}`)
    return { orgId: row.org_id, tokenId: row.id, tokenName: row.name }
  }, { engineTokenHash: hash })
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
): Promise<IngestResult> {
  if (events.length === 0) {
    return { accepted: 0, duplicates: 0, rejected: 0, outcomes: [] }
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
        ON CONFLICT (org_id, idempotency_key) DO NOTHING
        RETURNING id`)

      if (rows.length === 0) {
        outcomes.push({ id: event.id, status: 'duplicate' })
        continue
      }

      await applyToProjection(db, clock, engine.orgId, event)
      outcomes.push({ id: event.id, status: 'accepted' })
    }
  })

  return {
    accepted: outcomes.filter((o) => o.status === 'accepted').length,
    duplicates: outcomes.filter((o) => o.status === 'duplicate').length,
    rejected: outcomes.filter((o) => o.status === 'rejected').length,
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
 * Advances the environment row an event refers to.
 *
 * The WHERE clause carries `last_sequence < :sequence`, which is the whole
 * out-of-order defence: an event that arrives late updates nothing, rather
 * than dragging the row back to a state it has already left. It is done in the
 * UPDATE rather than by reading first and deciding, so two events landing at
 * once cannot both read the same last_sequence and both apply.
 */
async function applyToProjection(
  db: Db,
  clock: Clock,
  orgId: string,
  event: IncomingEvent,
): Promise<void> {
  const state = STATE_FOR[event.type]
  if (!state || !event.envId) return

  const sequence = event.sequence ?? 0
  const payload = event.payload ?? {}
  const previewUrl = typeof payload.preview_url === 'string' ? payload.preview_url : null
  const runtime = typeof payload.runtime === 'string' ? payload.runtime : null
  const golden = typeof payload.golden_version === 'string' ? payload.golden_version : null

  await db.execute(sql`
    UPDATE environments SET
      state = ${state}::environment_state,
      last_sequence = ${sequence},
      preview_url = COALESCE(${previewUrl}, preview_url),
      runtime = COALESCE(${runtime}, runtime),
      golden_version = COALESCE(${golden}, golden_version),
      torn_down_at = CASE WHEN ${state} = 'torn_down'
                          THEN ${clock.now().toISOString()}::timestamptz ELSE torn_down_at END,
      updated_at = ${clock.now().toISOString()}
    WHERE org_id = ${orgId} AND env_id = ${event.envId} AND last_sequence < ${sequence}`)
}
