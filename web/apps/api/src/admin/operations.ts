// The Operations lane: what an operator can actually learn about failures and
// about email, from the tables this control plane already writes.
//
// THE SECTIONS THIS LANE OWNS: Infrastructure & Compute, Logs & Error
// Explorer, Email & Notifications, and Incidents & Kill Switches. Two of those
// four are served entirely by routes that already exist. Infrastructure &
// Compute reads `admin.infra.*` and Incidents & Kill Switches reads
// `admin.emergency.*`, both from infra.ts, and neither is duplicated here: a
// second copy of a query is a second place for it to drift, and the emergency
// switches in particular stay where they are because maintenance mode exempts
// them by path and a move would take them out of the exemption exactly when an
// operator needs them to end an outage.
//
// So what is new is the two sections that had no routes at all, and they are
// mounted below under `operationsRouter`, which admin-namespaces.test.ts
// asserts is mounted once and under the key that names it.
//
// THE RULE THIS FILE IS BUILT AROUND: NOTHING HERE IS INVENTED. There is no
// exception group table, no fingerprint, no occurrence counter, no stack trace
// store, no log line store, no send log, no bounce record and no template
// table anywhere in this schema. So this file does not pretend there is one.
// Every number below is an aggregate over a column that exists, and the two
// pages that read it say in their own words which questions this installation
// cannot answer at all. A dashboard over invented data is a worse outcome than
// a page that says a capability is not wired, because the reader acts on the
// first one.
//
// What that leaves is genuinely useful, and it is the shape an error explorer
// has when it is built out of run outcomes rather than out of exceptions:
//
//   workload_runs.failure_code    the closest thing to a fingerprint this
//                                 product has. A code plus the workload kind
//                                 is a group, and the group carries a count,
//                                 how many organizations it reached, when it
//                                 was first and last seen, and the most recent
//                                 run to open.
//   verdicts.value / .workflow    which workflows are failing, across tenants.
//   events.type                   what is flowing in, and whether it stopped.
//
// EVENT PAYLOADS ARE NOT RETURNED, and that is a boundary rather than an
// oversight. `events.payload` is the customer's data: request bodies, database
// values, whatever the engine observed. An operator debugging ingestion needs
// to know that events of a type are arriving, at what rate, with what shape,
// and how far behind they are. None of that needs the values. So the stream
// returns the key NAMES and the byte size, and the page says so out loud. RLS
// is row level and cannot restrict a column, and the operator pool bypasses it
// entirely, so the only place this boundary can exist is here, in the columns
// the query names. That is the same argument as SAFE_COLUMNS in router.ts.
//
// `email_signin_tokens.token_hash` is likewise never selected. A hash is not a
// secret in the way a token is, and it is also of no use to a person and of
// considerable use to anybody who should not have it.

import { z } from 'zod'
import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'
import { router } from '../trpc.ts'
import { adminProcedure, type AdminContext } from './trpc.ts'
import { RecordingMailer, ResendMailer } from '../auth/mail.ts'

/**
 * How far back a page looks.
 *
 * A fixed set rather than a free number, because the two expensive queries
 * here scan by time and an operator typing 8760 during an incident would get a
 * statement timeout instead of an answer. A week is the longest window offered
 * and it is already the one that reads the most partitions.
 */
export const WINDOWS = [1, 6, 24, 72, 168] as const
export type WindowHours = (typeof WINDOWS)[number]

const windowHours = z
  .union([z.literal(1), z.literal(6), z.literal(24), z.literal(72), z.literal(168)])
  .optional()

/** How many groups a page will show before it stops. Past this the list is not
 *  being read, it is being scrolled. */
const GROUP_LIMIT = 60

function iso(value: Date | string): string {
  return value instanceof Date ? value.toISOString() : new Date(value).toISOString()
}

/**
 * Turns one extra row into a cursor.
 *
 * The same shape as `pageOf` in router.ts, and a deliberate second copy rather
 * than an import: router.ts imports this file to mount it, so importing back
 * would be a cycle. Ten lines of arithmetic is the cheaper of the two.
 */
function pageOf<Row, Out>(
  rows: Row[],
  limit: number,
  cursorOf: (row: Row) => string,
  map: (row: Row) => Out,
): { rows: Out[]; nextCursor: string | null } {
  const more = rows.length > limit
  const visible = more ? rows.slice(0, limit) : rows
  return {
    rows: visible.map(map),
    nextCursor: more && visible.length > 0 ? cursorOf(visible[visible.length - 1]!) : null,
  }
}

function since(now: Date, hours: number): Date {
  return new Date(now.getTime() - hours * 60 * 60 * 1000)
}

/* -------------------------------------------------------------------------
 * Failures
 * ---------------------------------------------------------------------- */

export interface FailureGroup {
  /** The failure code, or null when the run failed without recording one.
   *  Null is a real answer and the page shows it as one: a run that ends
   *  failed with no code is a gap in the engine, and hiding those rows would
   *  hide the gap. */
  failureCode: string | null
  kind: string
  state: string
  runs: number
  organizations: number
  firstSeen: string
  lastSeen: string
  /**
   * The most recent run's own `detail`, truncated.
   *
   * Included on purpose, and it is the one field here that carries text a
   * tenant's code produced. Without it the group is a code and a count, which
   * tells an operator that something is failing and nothing about what. Every
   * read of this route writes an audit entry naming the operator, which is the
   * accountability that makes showing it acceptable.
   */
  latestDetail: string | null
  latestRunId: string
}

/**
 * Which run states count as a failure here.
 *
 * `timed_out` and `abandoned` are in, and leaving them out is the mistake this
 * constant exists to prevent: a run that was killed by its deadline and a run
 * whose engine died are both failures to the person whose test did not run,
 * and both of them carry a failure code. A list of only `failed` reports a
 * smaller, calmer, wrong number.
 */
export const FAILED_STATES = ['failed', 'timed_out', 'abandoned'] as const

/**
 * Why the window has an upper bound here and not on the event queries.
 *
 * `workload_runs.updated_at` and `verdicts.created_at` are written by THIS
 * control plane, so a value after now is clock skew between the process and the
 * database and nothing else. Unbounded, such a row sits inside every window
 * that has ever been offered, forever, and never ages out of the failure list.
 *
 * `events.occurred_at` is the ENGINE's stamp and may legitimately run ahead of
 * ours. Dropping those would hide exactly the skew the ingestion lag check
 * exists to surface, so the event queries take a lower bound only.
 */
export async function failureGroups(
  db: Db,
  from: Date,
  to: Date,
  orgId: string | null,
): Promise<FailureGroup[]> {
  const rows = await db.execute<{
    failure_code: string | null
    kind: string
    state: string
    runs: string | number
    organizations: string | number
    first_seen: Date | string
    last_seen: Date | string
    latest_detail: string | null
    latest_run_id: string
  }>(sql`
    SELECT r.failure_code, w.kind::text AS kind, r.state::text AS state,
           count(*) AS runs,
           count(DISTINCT r.org_id) AS organizations,
           min(r.updated_at) AS first_seen,
           max(r.updated_at) AS last_seen,
           (array_agg(r.detail ORDER BY r.updated_at DESC))[1] AS latest_detail,
           (array_agg(r.id ORDER BY r.updated_at DESC))[1] AS latest_run_id
    FROM workload_runs r
    JOIN workloads w ON w.id = r.workload_id
    WHERE r.updated_at >= ${from.toISOString()}::timestamptz
      AND r.updated_at <= ${to.toISOString()}::timestamptz
      AND r.state::text IN ('failed', 'timed_out', 'abandoned')
      AND (${orgId}::uuid IS NULL OR r.org_id = ${orgId}::uuid)
    GROUP BY r.failure_code, w.kind, r.state
    ORDER BY runs DESC, last_seen DESC
    LIMIT ${GROUP_LIMIT}`)

  return rows.map((r) => ({
    failureCode: r.failure_code,
    kind: r.kind,
    state: r.state,
    runs: Number(r.runs),
    organizations: Number(r.organizations),
    firstSeen: iso(r.first_seen),
    lastSeen: iso(r.last_seen),
    latestDetail: r.latest_detail === null ? null : r.latest_detail.slice(0, 400),
    latestRunId: r.latest_run_id,
  }))
}

export interface WorkflowFailure {
  workflow: string
  value: string
  runs: number
  organizations: number
  lastSeen: string
  /** The newest verdict's summary. The engine writes this; it is the sentence
   *  that says what the workflow was doing when it stopped. */
  latestSummary: string | null
}

export async function failingWorkflows(
  db: Db,
  from: Date,
  to: Date,
  orgId: string | null,
): Promise<WorkflowFailure[]> {
  const rows = await db.execute<{
    workflow: string
    value: string
    runs: string | number
    organizations: string | number
    last_seen: Date | string
    latest_summary: string | null
  }>(sql`
    SELECT v.workflow, v.value::text AS value,
           count(*) AS runs,
           count(DISTINCT v.org_id) AS organizations,
           max(v.created_at) AS last_seen,
           (array_agg(v.summary ORDER BY v.created_at DESC))[1] AS latest_summary
    FROM verdicts v
    WHERE v.created_at >= ${from.toISOString()}::timestamptz
      AND v.created_at <= ${to.toISOString()}::timestamptz
      AND v.value::text IN ('fail', 'blocked')
      AND (${orgId}::uuid IS NULL OR v.org_id = ${orgId}::uuid)
    GROUP BY v.workflow, v.value
    ORDER BY runs DESC, last_seen DESC
    LIMIT ${GROUP_LIMIT}`)

  return rows.map((r) => ({
    workflow: r.workflow,
    value: r.value,
    runs: Number(r.runs),
    organizations: Number(r.organizations),
    lastSeen: iso(r.last_seen),
    latestSummary: r.latest_summary === null ? null : r.latest_summary.slice(0, 400),
  }))
}

export interface EventType {
  type: string
  events: number
  organizations: number
  lastReceivedAt: string
  /**
   * Seconds between the engine stamping the newest event and this control
   * plane receiving it.
   *
   * The gap rather than either timestamp, because either one alone looks fine
   * while the pair is wrong: events arriving now for work that finished an
   * hour ago make every run view stale without making any of them look stale.
   */
  lagSeconds: number
}

export async function eventTypes(
  db: Db,
  from: Date,
  orgId: string | null,
): Promise<EventType[]> {
  // occurred_at, not received_at, and that is what makes this query affordable.
  // `events` is partitioned by month on occurred_at, so a predicate on it
  // prunes to the partitions the window touches; the same predicate on
  // received_at reads every partition that has ever existed.
  const rows = await db.execute<{
    type: string
    events: string | number
    organizations: string | number
    last_received_at: Date | string
    lag_seconds: string | number | null
  }>(sql`
    SELECT e.type,
           count(*) AS events,
           count(DISTINCT e.org_id) AS organizations,
           max(e.received_at) AS last_received_at,
           EXTRACT(EPOCH FROM (max(e.received_at) - max(e.occurred_at))) AS lag_seconds
    FROM events e
    WHERE e.occurred_at >= ${from.toISOString()}::timestamptz
      AND (${orgId}::uuid IS NULL OR e.org_id = ${orgId}::uuid)
    GROUP BY e.type
    ORDER BY events DESC
    LIMIT ${GROUP_LIMIT}`)

  return rows.map((r) => ({
    type: r.type,
    events: Number(r.events),
    organizations: Number(r.organizations),
    lastReceivedAt: iso(r.last_received_at),
    lagSeconds: Math.round(Number(r.lag_seconds ?? 0)),
  }))
}

/* -------------------------------------------------------------------------
 * Email
 * ---------------------------------------------------------------------- */

/** How a sign-in link ended up, which the columns do not say on their own. */
export type LinkStanding =
  /** Somebody clicked it and is signed in. */
  | 'used'
  /** Issued, unused, still valid. */
  | 'live'
  /** Issued, never used, and now too old to use. The interesting one: at any
   *  volume this is what a delivery failure looks like from inside a product
   *  that keeps no delivery record. */
  | 'unused'

export function standingOf(row: {
  consumed_at: Date | string | null
  expires_at: Date | string
}, now: Date): LinkStanding {
  if (row.consumed_at !== null) return 'used'
  return new Date(iso(row.expires_at)).getTime() > now.getTime() ? 'live' : 'unused'
}

export interface EmailReach {
  /** Sign-in links issued in the window, and what became of them. */
  linksIssued: number
  linksUsed: number
  linksUnused: number
  linksLive: number
  /** Invitations, which are the other thing this product puts in an inbox. */
  invitationsSent: number
  invitationsAccepted: number
  invitationsOpen: number
  /** Addresses on file that a bill would go to, if billing mail existed. */
  billingContacts: number
}

export async function emailReach(db: Db, from: Date, now: Date): Promise<EmailReach> {
  const [links] = await db.execute<{
    issued: string | number
    used: string | number
    unused: string | number
    live: string | number
  }>(sql`
    SELECT count(*) AS issued,
           count(*) FILTER (WHERE consumed_at IS NOT NULL) AS used,
           count(*) FILTER (WHERE consumed_at IS NULL
                              AND expires_at <= ${now.toISOString()}::timestamptz) AS unused,
           count(*) FILTER (WHERE consumed_at IS NULL
                              AND expires_at > ${now.toISOString()}::timestamptz) AS live
    FROM email_signin_tokens
    WHERE created_at >= ${from.toISOString()}::timestamptz`)

  const [invites] = await db.execute<{
    sent: string | number
    accepted: string | number
    open: string | number
  }>(sql`
    SELECT count(*) AS sent,
           count(*) FILTER (WHERE accepted_at IS NOT NULL) AS accepted,
           count(*) FILTER (WHERE accepted_at IS NULL AND revoked_at IS NULL
                              AND expires_at > ${now.toISOString()}::timestamptz) AS open
    FROM invitations
    WHERE created_at >= ${from.toISOString()}::timestamptz`)

  const [contacts] = await db.execute<{ n: string | number }>(
    sql`SELECT count(*) AS n FROM billing_contacts`,
  )

  return {
    linksIssued: Number(links?.issued ?? 0),
    linksUsed: Number(links?.used ?? 0),
    linksUnused: Number(links?.unused ?? 0),
    linksLive: Number(links?.live ?? 0),
    invitationsSent: Number(invites?.sent ?? 0),
    invitationsAccepted: Number(invites?.accepted ?? 0),
    invitationsOpen: Number(invites?.open ?? 0),
    billingContacts: Number(contacts?.n ?? 0),
  }
}

/* -------------------------------------------------------------------------
 * The routes
 * ---------------------------------------------------------------------- */

const logsRouter = router({
  /**
   * What is failing on this installation, grouped.
   *
   * Three aggregates in one round trip rather than three routes, because they
   * are one question asked from three angles and a page that loads them
   * separately shows an operator three windows that do not agree about when
   * "now" was.
   */
  overview: adminProcedure('admin.logs.read')
    .input(
      z
        .object({
          hours: windowHours,
          orgId: z.string().uuid().nullish(),
        })
        .optional(),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const now = c.clock.now()
      const hours: WindowHours = input?.hours ?? 24
      const from = since(now, hours)
      const orgId = input?.orgId ?? null
      return c.adminDb(async (db) => {
        const [failures, workflows, types] = await Promise.all([
          failureGroups(db, from, now, orgId),
          failingWorkflows(db, from, now, orgId),
          eventTypes(db, from, orgId),
        ])
        return {
          hours,
          from: from.toISOString(),
          at: now.toISOString(),
          failures,
          workflows,
          eventTypes: types,
          /** True when a list was cut off, so the page can say the list is a
           *  top slice rather than the whole answer. `More` is the wrong shape
           *  here: these are aggregates and there is no stable cursor into a
           *  GROUP BY whose counts move between calls. */
          truncated: {
            failures: failures.length === GROUP_LIMIT,
            workflows: workflows.length === GROUP_LIMIT,
            eventTypes: types.length === GROUP_LIMIT,
          },
          limit: GROUP_LIMIT,
        }
      })
    }),

  /**
   * The event stream, newest first, paged.
   *
   * Shape and timing, never contents. See the header.
   */
  events: adminProcedure('admin.logs.read')
    .input(
      z.object({
        hours: windowHours,
        type: z.string().max(200).nullish(),
        orgId: z.string().uuid().nullish(),
        limit: z.number().int().min(1).max(200).default(50),
        cursor: z.string().max(200).nullish(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const now = c.clock.now()
      const from = since(now, input.hours ?? 24)

      // The cursor is the sort key, both halves of it. occurred_at alone is
      // not unique: a batch of events sharing a millisecond would repeat a row
      // on one page and drop another, which is the failure mode nobody
      // notices because the page still looks full.
      let cursorAt: string | null = null
      let cursorId: string | null = null
      if (input.cursor) {
        const at = input.cursor.indexOf('|')
        if (at > 0) {
          cursorAt = input.cursor.slice(0, at)
          cursorId = input.cursor.slice(at + 1)
        }
      }

      return c.adminDb(async (db) => {
        const rows = await db.execute<{
          id: string
          occurred_at: Date | string
          received_at: Date | string
          type: string
          org_id: string
          org_slug: string
          env_id: string | null
          run_id: string | null
          payload_keys: string[] | null
          payload_bytes: string | number
        }>(sql`
          SELECT e.id, e.occurred_at, e.received_at, e.type, e.org_id,
                 o.slug AS org_slug, e.env_id, e.run_id,
                 (SELECT array_agg(k ORDER BY k) FROM jsonb_object_keys(e.payload) k)
                   AS payload_keys,
                 octet_length(e.payload::text) AS payload_bytes
          FROM events e
          JOIN organizations o ON o.id = e.org_id
          WHERE e.occurred_at >= ${from.toISOString()}::timestamptz
            AND (${input.type ?? null}::text IS NULL OR e.type = ${input.type ?? null}::text)
            AND (${input.orgId ?? null}::uuid IS NULL
                 OR e.org_id = ${input.orgId ?? null}::uuid)
            AND (${cursorAt}::timestamptz IS NULL
                 OR (e.occurred_at, e.id) < (${cursorAt}::timestamptz, ${cursorId}::uuid))
          ORDER BY e.occurred_at DESC, e.id DESC
          LIMIT ${input.limit + 1}`)

        return pageOf(
          rows,
          input.limit,
          (r) => `${iso(r.occurred_at)}|${r.id}`,
          (r) => ({
            id: r.id,
            occurredAt: iso(r.occurred_at),
            receivedAt: iso(r.received_at),
            type: r.type,
            orgId: r.org_id,
            orgSlug: r.org_slug,
            envId: r.env_id,
            runId: r.run_id,
            payloadKeys: r.payload_keys ?? [],
            payloadBytes: Number(r.payload_bytes),
          }),
        )
      })
    }),
})

const emailRouter = router({
  /**
   * Whether this installation can send email at all, and what it has tried to
   * send.
   *
   * `canSend` is the fact the page leads with, and it is the one an operator
   * most often gets wrong: the mailer is nullable and an installation with no
   * provider configured accepts a sign-in request, writes a token row, and
   * puts nothing in anybody's inbox. The row exists either way, so no query
   * over `email_signin_tokens` can tell the two apart. Only the context can.
   */
  status: adminProcedure('admin.email.read')
    .input(z.object({ hours: windowHours }).optional())
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const now = c.clock.now()
      const hours: WindowHours = input?.hours ?? 168
      const from = since(now, hours)

      // instanceof rather than a name string. A constructor name survives this
      // codebase but not a bundler, and the thing being decided here is
      // whether real messages leave the process.
      const mailer = c.mailer
      const provider =
        mailer === null
          ? null
          : mailer instanceof ResendMailer
            ? 'resend'
            : mailer instanceof RecordingMailer
              ? 'recording'
              : 'other'

      const reach = await c.adminDb((db) => emailReach(db, from, now))
      return {
        hours,
        from: from.toISOString(),
        at: now.toISOString(),
        canSend: mailer !== null,
        provider,
        /** True when messages are being kept in memory instead of sent, which
         *  looks identical to working from every other angle. */
        recordingOnly: provider === 'recording',
        reach,
      }
    }),

  /**
   * Every sign-in link issued recently, and what became of it.
   *
   * This is the closest this product comes to a send log, and the page says so
   * rather than dressing it up: a row here proves a message was COMPOSED, and
   * `used` proves somebody clicked the link, which is the only evidence of
   * delivery that exists anywhere in this schema.
   */
  signInLinks: adminProcedure('admin.email.read')
    .input(
      z.object({
        hours: windowHours,
        standing: z.enum(['used', 'live', 'unused']).nullish(),
        query: z.string().trim().max(200).nullish(),
        limit: z.number().int().min(1).max(200).default(50),
        cursor: z.string().max(200).nullish(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const now = c.clock.now()
      const from = since(now, input.hours ?? 168)

      let cursorAt: string | null = null
      let cursorId: string | null = null
      if (input.cursor) {
        const at = input.cursor.indexOf('|')
        if (at > 0) {
          cursorAt = input.cursor.slice(0, at)
          cursorId = input.cursor.slice(at + 1)
        }
      }
      const nowIso = now.toISOString()
      const standing = input.standing ?? null

      return c.adminDb(async (db) => {
        // Columns named one by one. `token_hash` is deliberately absent: it is
        // useless to a person and useful to anybody who should not have it,
        // and a SELECT * here would have shipped it to a browser.
        const rows = await db.execute<{
          id: string
          email: string
          created_at: Date | string
          expires_at: Date | string
          consumed_at: Date | string | null
          ip: string | null
          user_agent: string | null
          redirect_to: string | null
        }>(sql`
          SELECT t.id, t.email, t.created_at, t.expires_at, t.consumed_at,
                 host(t.ip) AS ip, t.user_agent, t.redirect_to
          FROM email_signin_tokens t
          WHERE t.created_at >= ${from.toISOString()}::timestamptz
            AND (${input.query ?? null}::text IS NULL
                 OR t.email LIKE ${'%' + (input.query ?? '').toLowerCase() + '%'})
            AND (${standing}::text IS NULL
                 OR (${standing}::text = 'used' AND t.consumed_at IS NOT NULL)
                 OR (${standing}::text = 'live' AND t.consumed_at IS NULL
                     AND t.expires_at > ${nowIso}::timestamptz)
                 OR (${standing}::text = 'unused' AND t.consumed_at IS NULL
                     AND t.expires_at <= ${nowIso}::timestamptz))
            AND (${cursorAt}::timestamptz IS NULL
                 OR (t.created_at, t.id) < (${cursorAt}::timestamptz, ${cursorId}::uuid))
          ORDER BY t.created_at DESC, t.id DESC
          LIMIT ${input.limit + 1}`)

        return pageOf(
          rows,
          input.limit,
          (r) => `${iso(r.created_at)}|${r.id}`,
          (r) => ({
            id: r.id,
            email: r.email,
            createdAt: iso(r.created_at),
            expiresAt: iso(r.expires_at),
            consumedAt: r.consumed_at === null ? null : iso(r.consumed_at),
            standing: standingOf(r, now),
            ip: r.ip,
            userAgent: r.user_agent,
            redirectTo: r.redirect_to,
          }),
        )
      })
    }),
})

/**
 * The Operations namespace, mounted at `admin.operations` and nowhere else.
 *
 * The two sub-routers are nested rather than spread, so every path reads
 * `admin.operations.logs.*` or `admin.operations.email.*` and says which lane
 * owns it from the path alone. Spreading them into adminRouter as `logs:` and
 * `email:` would work identically and would put two more keys in the one
 * object literal six lanes edit, which is the collision the group namespaces
 * exist to remove.
 *
 * NOT a second `admin:` key, for the reason written at the bottom of infra.ts:
 * a second one does not conflict in git, does not fail to compile, and wins or
 * loses on object key order.
 */
export const operationsRouter = router({
  logs: logsRouter,
  email: emailRouter,
})
