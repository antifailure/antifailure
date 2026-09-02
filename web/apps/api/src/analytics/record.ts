// Recording an analytics event, and the two rules that shape every line here.
//
// RULE ONE: RECORDING A NUMBER MUST NEVER BREAK THE PRODUCT.
//
// A sign-in that fails because an analytics insert failed is a worse product
// than a missing row on a chart, and it is not a close call. So every producer
// goes through record(), which catches, counts the failure, and never throws.
//
// That is a swallow, and a swallow is normally how a producer stops working
// without anybody finding out. What makes it not silent is the counter:
// af_analytics_events_total{outcome="failed"} is on /metrics, so a producer
// that has started failing is a line on a graph rather than an absence nobody
// can see. An absence is precisely what cannot be alerted on, which is why the
// counter and not the exception is the visibility here.
//
// RULE TWO: THE ROW GOES IN THE SAME TRANSACTION AS THE THING IT DESCRIBES.
//
// The audit log makes this argument and it applies unchanged: a row written in
// its own transaction survives a rolled-back change, and a store that records
// things that did not happen is as useless as one that misses things that did.
// So record() takes the transaction rather than the pool.
//
// WHY VALIDATION RUNS BEFORE THE INSERT AND NOT AFTER IT.
//
// The database has check constraints, and they are the backstop rather than the
// gate. A value the catalog refuses never reaches a statement, so the constraint
// can only fire on a bug in this file, and a rejection is a counted outcome
// with a reason rather than a caught exception with a SQLSTATE.

import { createHmac, randomUUID } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Db, Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import {
  CATALOG,
  isEventName,
  validatePayload,
  type EventName,
  type Rejection,
} from './catalog.ts'

/** What a producer hands in. Never an identifier, never a raw referrer. */
export interface AnalyticsEvent<N extends EventName = EventName> {
  name: N
  /**
   * When it happened, per whoever it happened to.
   *
   * Stamped once and resent unchanged on a retry. That is not a nicety: the
   * table is partitioned on this column, so the unique key carries it, and a
   * retry that restamps the time inserts a SECOND row rather than colliding
   * with the first. migrations/0011 has the long version of the same trap.
   */
  occurredAt: Date
  /**
   * The sender's identifier for this event. Generated here when the producer
   * has none, which is every control-plane producer: they run once, inside a
   * transaction, and a retry of the transaction is a retry of the whole
   * business operation.
   */
  eventId?: string
  /** The organization this is about. Hashed here; never stored. */
  orgId?: string | null
  /** A short-lived anonymous session identifier from a browser. Hashed here;
   *  never stored, never logged, never returned. */
  session?: string | null
  payload?: Record<string, unknown>
}

export type RecordOutcome =
  | { status: 'recorded' }
  /** The same event, already stored. A retry, which is the normal case. */
  | { status: 'duplicate' }
  | { status: 'rejected'; problem: Rejection }
  /** No surrogate secret is configured, so nothing is recorded at all. */
  | { status: 'disabled' }
  /** The insert failed. Counted, never thrown. */
  | { status: 'failed' }

export interface BatchOutcome {
  recorded: number
  duplicates: number
  rejected: number
  failed: number
  /** One outcome per event, in the order they were given. A batch with one bad
   *  event reports on that one and keeps the rest, which is the property the
   *  ingestion path already has and the reason it is worth having here. */
  outcomes: RecordOutcome[]
}

/** The counters this module keeps. Supplied by the server so two servers in one
 *  process do not share them. */
export interface AnalyticsCounters {
  /** Labelled name and outcome. */
  events: { inc(labels: Record<string, string>, by?: number): void }
  /** Labelled reason, so a producer sending a field nobody declared is visible
   *  separately from one sending an event nobody declared. */
  rejections: { inc(labels: Record<string, string>, by?: number): void }
}

export interface Analytics {
  /** False when no surrogate secret is configured. Every method still works and
   *  records nothing, so a caller never has to check. */
  readonly enabled: boolean
  record(db: Db, event: AnalyticsEvent): Promise<RecordOutcome>
  recordAll(db: Db, events: AnalyticsEvent[]): Promise<BatchOutcome>
  /** Records outside any transaction the caller owns, for the few producers
   *  that have none. Opens its own, so a failure cannot roll anything back. */
  recordDetached(pool: Pool, event: AnalyticsEvent): Promise<RecordOutcome>
  /** The stored form of an organization identifier, for a test or a rollup that
   *  needs to look one up. Null when analytics is off. */
  surrogate(orgId: string): string | null
}

export interface AnalyticsOptions {
  /**
   * The key the surrogates are computed under.
   *
   * Null turns analytics off entirely rather than falling back to a constant,
   * because a constant key is a surrogate anybody can recompute, which is an
   * org_id with extra steps.
   */
  secret: Buffer | null
  clock: Clock
  counters: AnalyticsCounters
}

/**
 * The secret, from the environment.
 *
 * Thirty-two bytes of hex, the same shape as AF_PROVIDER_KEY_SECRET, so an
 * operator generating one does not have to learn a second format. A value of
 * the wrong length stops the process at start-up rather than on the first
 * event, which is the difference between a deploy that fails and a deploy that
 * succeeds and records nothing.
 */
export function surrogateSecretFrom(raw: string | undefined): Buffer | null {
  if (!raw) return null
  const trimmed = raw.trim()
  if (!/^[0-9a-fA-F]{64}$/.test(trimmed)) {
    throw new Error(
      'AF_ANALYTICS_SURROGATE_SECRET has to be 64 hex characters, which is 32 bytes. ' +
        'Generate one with: openssl rand -hex 32',
    )
  }
  return Buffer.from(trimmed, 'hex')
}

/**
 * Which facts an event moves, and how.
 *
 * Separate from the catalog because it is the control plane's knowledge rather
 * than the event's own: an event says what happened, and this says what that
 * means for the organization's milestones. Keeping it here means the catalog
 * stays a description of a wire format that another producer could implement.
 *
 * Every entry is commutative or monotone. A date moves EARLIER only, a counter
 * only adds, and the plan is last-writer-wins on a value that is not a
 * milestone. Nothing here can produce a different answer for a different
 * arrival order, which is what the ordering tests check.
 */
interface FactMove {
  /** A milestone column set to the least of what is there and this event's day. */
  milestone?: 'first_event_on' | 'first_environment_on' | 'first_proven_run_on' | 'first_paid_on'
  /** A counter incremented by one. */
  increment?: 'environments_created' | 'runs_finished'
  /** Reads the plan out of the payload, when the event carries one. */
  plan?: (payload: Record<string, unknown>) => string | null
}

const FACTS: Partial<Record<EventName, FactMove>> = {
  'environment.created': { milestone: 'first_environment_on', increment: 'environments_created' },
  // No milestone here, and the reason is the point: activation is a verdict
  // that PROVED something. blocked and unverified are the two that did not, so
  // whether this run activates the organization depends on the payload rather
  // than the name, and provenRun below decides it.
  'validation.run_finished': { increment: 'runs_finished' },
  'revenue.plan_changed': { plan: (p) => (typeof p.to === 'string' ? p.to : null) },
  'revenue.subscription_changed': { plan: (p) => (typeof p.plan === 'string' ? p.plan : null) },
}

/**
 * The milestone a run finishing sets, which depends on its verdict rather than
 * on its name, so it cannot be a constant in the table above.
 */
function provenRun(name: EventName, payload: Record<string, unknown>): boolean {
  if (name !== 'validation.run_finished') return false
  const verdict = payload.verdict
  return verdict === 'pass' || verdict === 'fail' || verdict === 'flaky'
}

/** The day an event belongs to, in UTC. Dates rather than timestamps in the
 *  facts table, because the question is which week an organization activated
 *  and a timestamp answers a question nobody asked more identifiably. */
function dayOf(at: Date): string {
  return at.toISOString().slice(0, 10)
}

export function createAnalytics(options: AnalyticsOptions): Analytics {
  const { secret, clock, counters } = options
  const enabled = secret !== null

  function hash(domain: string, value: string): string {
    // Domain separated, so an organization identifier and a session identifier
    // can never hash to the same surrogate. Without the prefix a caller that
    // passed the wrong one would produce a collision that looks like a real
    // organization and is a browser tab.
    return createHmac('sha256', secret!).update(`${domain}:${value}`).digest('hex').slice(0, 32)
  }

  async function one(db: Db, event: AnalyticsEvent): Promise<RecordOutcome> {
    if (!enabled) return { status: 'disabled' }

    const problem = check(event)
    if (problem) {
      counters.rejections.inc({ reason: problem.reason })
      counters.events.inc({ name: safeName(event.name), outcome: 'rejected' })
      return { status: 'rejected', problem }
    }

    const name = event.name
    const spec = CATALOG[name]
    const validated = validatePayload(name, event.payload ?? {})
    if (!validated.ok) {
      counters.rejections.inc({ reason: validated.problem.reason })
      counters.events.inc({ name, outcome: 'rejected' })
      return { status: 'rejected', problem: validated.problem }
    }

    const orgSurrogate = event.orgId ? hash('org', event.orgId) : null
    const sessionSurrogate = event.session ? hash('session', event.session) : null
    const occurredAt = event.occurredAt.toISOString()

    try {
      // A SAVEPOINT, and it is what makes "never throws" true rather than
      // merely written down.
      //
      // A caught database error is not a recovered one. Postgres aborts the
      // whole transaction on the first failed statement, so catching here and
      // returning would leave the CALLER's transaction poisoned: every
      // statement after it fails with 25P02, and the sign-in this was not
      // allowed to break breaks anyway, one statement later, with an error
      // about something else. Found by watching exactly that happen.
      //
      // drizzle's nested transaction is a savepoint, so a failure rolls back to
      // here and the caller's transaction carries on. It also makes the event
      // and the facts it moves atomic with each other: a facts update that
      // failed would otherwise leave a counted event with no milestone.
      return await db.transaction(async (tx) => {
        const inserted = await tx.execute(sql`
          INSERT INTO analytics_events (
            event_id, name, version, occurred_at, received_at, source,
            org_surrogate, session_surrogate, actor_kind, privacy_basis, payload)
          VALUES (
            ${event.eventId ?? randomUUID()}, ${name}, ${spec.version},
            ${occurredAt}::timestamptz, ${clock.now().toISOString()}::timestamptz,
            ${spec.source}, ${orgSurrogate}, ${sessionSurrogate},
            ${spec.actorKind}, ${spec.privacyBasis},
            ${JSON.stringify(validated.payload)}::jsonb)
          -- No conflict target, and that is a privilege constraint rather than
          -- a style choice. ON CONFLICT (cols) has to look the columns up in
          -- the arbiter index, which needs SELECT on them, and this role holds
          -- INSERT and no SELECT precisely so the stream cannot be read back.
          -- The targeted form failed with 42501 on every single event.
          --
          -- Bare DO NOTHING covers every unique constraint on the table rather
          -- than the one named. There is exactly one, the primary key, so the
          -- two forms are the same statement today; a test asserts that stays
          -- true, because a second unique constraint added later would start
          -- silently dropping rows here.
          ON CONFLICT DO NOTHING`)

        if (inserted.count === 0) {
          counters.events.inc({ name, outcome: 'duplicate' })
          return { status: 'duplicate' } as const
        }

        // Facts only for a row that was actually written. A duplicate that
        // bumped a counter would make "environments created" grow every time an
        // engine retried a batch, which is the number a bill gets checked
        // against.
        if (orgSurrogate) {
          await moveFacts(tx as unknown as Db, orgSurrogate, name, validated.payload, dayOf(event.occurredAt))
        }

        counters.events.inc({ name, outcome: 'recorded' })
        return { status: 'recorded' } as const
      })
    } catch (err) {
      // Counted, never thrown. See the header: a number is not worth failing a
      // sign-in over, and the counter is what stops that being silent.
      counters.events.inc({ name, outcome: 'failed' })
      reportFailure(err)
      return { status: 'failed' }
    }
  }

  async function moveFacts(
    db: Db,
    orgSurrogate: string,
    name: EventName,
    payload: Record<string, string | number | boolean>,
    day: string,
  ): Promise<void> {
    const move = FACTS[name] ?? {}
    const milestone = provenRun(name, payload) ? 'first_proven_run_on' : move.milestone
    const plan = move.plan?.(payload) ?? null

    await db.execute(sql`
      INSERT INTO analytics_org_facts (
        org_surrogate, first_seen_on, last_active_on, first_event_on,
        first_environment_on, first_proven_run_on, first_paid_on,
        plan, environments_created, runs_finished, updated_at)
      VALUES (
        -- first_event_on is set by EVERY attributable event rather than by a
        -- special one, because "this organization's engine has reported at
        -- all" is true of any of them. That is also what makes it converge: a
        -- late event about an earlier day moves it earlier, nothing later.
        ${orgSurrogate}, ${day}::date, ${day}::date, ${day}::date,
        ${milestone === 'first_environment_on' ? day : null}::date,
        ${milestone === 'first_proven_run_on' ? day : null}::date,
        ${plan !== null && plan !== 'free' ? day : null}::date,
        ${plan}, ${move.increment === 'environments_created' ? 1 : 0},
        ${move.increment === 'runs_finished' ? 1 : 0},
        ${clock.now().toISOString()}::timestamptz)
      ON CONFLICT (org_surrogate) DO UPDATE SET
        -- LEAST and GREATEST ignore NULLs in Postgres, so a column that is not
        -- being set by this event is left exactly as it was and one that has
        -- never been set takes the incoming value. That is what lets one
        -- statement handle every milestone without a CASE per column.
        first_seen_on = LEAST(analytics_org_facts.first_seen_on, EXCLUDED.first_seen_on),
        last_active_on = GREATEST(analytics_org_facts.last_active_on, EXCLUDED.last_active_on),
        first_event_on = LEAST(analytics_org_facts.first_event_on, EXCLUDED.first_event_on),
        first_environment_on =
          LEAST(analytics_org_facts.first_environment_on, EXCLUDED.first_environment_on),
        first_proven_run_on =
          LEAST(analytics_org_facts.first_proven_run_on, EXCLUDED.first_proven_run_on),
        first_paid_on = LEAST(analytics_org_facts.first_paid_on, EXCLUDED.first_paid_on),
        -- The plan is the one value that is not a milestone: it moves both ways
        -- and the latest answer is the right one. COALESCE so an event that
        -- carries no plan leaves the recorded one alone rather than clearing it.
        plan = COALESCE(EXCLUDED.plan, analytics_org_facts.plan),
        environments_created =
          analytics_org_facts.environments_created + EXCLUDED.environments_created,
        runs_finished = analytics_org_facts.runs_finished + EXCLUDED.runs_finished,
        updated_at = EXCLUDED.updated_at`)
  }

  return {
    enabled,

    record: one,

    async recordAll(db, events) {
      const outcomes: RecordOutcome[] = []
      for (const event of events) {
        // Sequentially and each on its own, so one rejection reports on itself
        // and the rest still land. A batch that discarded its good events
        // because of one bad one is the failure this shape exists to prevent,
        // and it is the same shape ingest.ts uses for the same reason.
        outcomes.push(await one(db, event))
      }
      return {
        recorded: outcomes.filter((o) => o.status === 'recorded').length,
        duplicates: outcomes.filter((o) => o.status === 'duplicate').length,
        rejected: outcomes.filter((o) => o.status === 'rejected').length,
        failed: outcomes.filter((o) => o.status === 'failed').length,
        outcomes,
      }
    },

    async recordDetached(pool, event) {
      if (!enabled) return { status: 'disabled' }
      try {
        // withoutTenant, because none of these tables carries an org_id and
        // there is nothing for a tenant setting to key on. See migrations/0031.
        return await pool.withoutTenant((db) => one(db, event))
      } catch (err) {
        counters.events.inc({ name: safeName(event.name), outcome: 'failed' })
        reportFailure(err)
        return { status: 'failed' }
      }
    },

    surrogate(orgId) {
      return enabled ? hash('org', orgId) : null
    },
  }
}

/** Envelope rules the catalog declares but validatePayload does not see. */
function check(event: AnalyticsEvent): Rejection | null {
  if (!isEventName(event.name)) {
    // Deliberately does not quote the name. A rejection message is written to a
    // log, and a log is a place a value nobody vetted would be persisted, which
    // is the one thing this whole subsystem is built to prevent.
    return {
      reason: 'unknown_event',
      detail: 'That event name is not in the analytics catalog, so nothing was recorded.',
    }
  }
  const spec = CATALOG[event.name]

  if (!(event.occurredAt instanceof Date) || Number.isNaN(event.occurredAt.getTime())) {
    return { reason: 'bad_envelope', detail: `${event.name} was given no usable occurredAt.` }
  }
  if (event.eventId !== undefined && (event.eventId.length === 0 || event.eventId.length > 100)) {
    return { reason: 'bad_envelope', detail: `${event.name} carries an event id of a length the column cannot hold.` }
  }

  const hasOrg = Boolean(event.orgId)
  if (spec.organization === 'required' && !hasOrg) {
    return {
      reason: 'organization_required',
      detail: `${event.name} is attributable to an organization and this one carries none, so it would be a row nobody can group.`,
    }
  }
  if (spec.organization === 'never' && hasOrg) {
    return {
      reason: 'organization_forbidden',
      detail: `${event.name} is anonymous by declaration and this one carries an organization.`,
    }
  }

  const hasSession = Boolean(event.session)
  if (spec.session === 'required' && !hasSession) {
    return { reason: 'session_required', detail: `${event.name} needs an anonymous session and carries none.` }
  }
  if (spec.session === 'never' && hasSession) {
    return {
      reason: 'session_forbidden',
      detail: `${event.name} is not a browser event and must not carry a session identifier.`,
    }
  }

  return null
}

/** A name safe to use as a metric label, for an event whose name was refused.
 *  An unbounded label is how a metrics endpoint becomes the largest thing in
 *  the process, and a rejected name is by definition one nobody vetted. */
function safeName(name: string): string {
  return isEventName(name) ? name : 'unknown'
}

/**
 * What a failed insert does besides being counted.
 *
 * console.error rather than nothing, because a counter says how many and never
 * says why. The error is logged and the event is not, so a failure that is
 * caused by the row cannot put the row in a log.
 */
function reportFailure(err: unknown): void {
  console.error('analytics insert failed', err instanceof Error ? err.message : String(err))
}
