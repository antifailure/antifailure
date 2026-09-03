// The Customers lane of the operator portal.
//
// THE SECTIONS THIS LANE OWNS: Users & Organizations, Support & Impersonation,
// and Billing & Stripe. Two of the three read routes that already existed, so
// what is in this file is the third one, which had none at all.
//
// The tenant, user and session routes live in router.ts and are NOT moved here.
// Moving them would change every path the console already calls, for filing
// rather than for a reason. The billing, entitlement and flag routes live in
// routers.ts for the same reason. New customer routes belong here; the old ones
// stay where the console can still find them.
//
// PERMISSION PREFIXES RESERVED FOR THIS LANE: admin.tenants.*, admin.users.*,
// admin.sessions.*, admin.support.*, admin.impersonation.*, admin.billing.*.
// The catalog and the reservations are in permissions.ts.
//
// ---------------------------------------------------------------------------
// WHY IMPERSONATION IS SPLIT ACROSS TWO TRANSPORTS, and it is not a filing
// choice. Starting an impersonation ends with a Set-Cookie for the CUSTOMER's
// session cookie, and ending one ends with clearing it. router.ts states the
// rule for the operator sign-in and it applies unchanged here: a procedure that
// exists to set a cookie is a procedure pretending to be a route. So `start`
// and `end` are plain JSON routes under /v1/admin/impersonation/, beside the
// operator sign-in they most resemble, and the things that only read are
// procedures under admin.customers.*.
//
// There is a harder reason for `end` in particular. adminProcedure refuses
// EVERY operator procedure while the session is impersonating, deliberately and
// first, before the permission check. A tRPC `end` would therefore be refused
// exactly when it is the only thing an operator needs, which is a door that
// locks from the inside. Getting out has to stay reachable from within, and it
// is why that route checks no permission at all.
//
// WHAT MAKES THE IMPERSONATION ACCOUNTABLE, since a button that claims to
// record something and does not is worse than no button:
//
//   a reason is required at the edge AND by a CHECK constraint, so it is true
//     of every caller rather than of this one;
//   the audit entry is written BEFORE the session exists, structurally, because
//     sessions.impersonation_audit_seq is NOT NULL when impersonating and
//     carries a foreign key into the chain. A session that was never audited
//     cannot be represented;
//   the customer gets the entry in their OWN audit log. A record only the
//     vendor can read is a vendor's private note rather than accountability;
//   it expires. The minted session's expires_at is minutes rather than the
//     product's thirty days, and resolveSession already enforces expiry on
//     every request, so the bound is the mechanism that ends every other
//     session rather than a second one that could be forgotten.
//
// WHY NOTES DO NOT GET A TENANT COPY when everything else about an organization
// does. Migration 0023 states it in the table's own comment and backs it by
// giving the application no grant on the table at all: an operator's note about
// a customer is not that customer's data and must not appear in their export,
// their audit log, or anywhere they can read. Impersonation is the opposite
// case, the vendor inside their account, which they must be told about. That
// distinction is the only reason adminAudit grew a tenantCopy argument.

import { randomBytes } from 'node:crypto'
import { z } from 'zod'
import { sql } from 'drizzle-orm'
import { TRPCError } from '@trpc/server'
import type { Hono, Context as HonoContext } from 'hono'
import type { ApiEnv } from '../env.ts'
import type { AdminPool, Db, Pool } from '@antifailure/db'
import { appendAdminAudit } from '@antifailure/db'
import { router } from '../trpc.ts'
import { adminProcedure, adminAudit, type AdminContext } from './trpc.ts'
import { adminRoleHas } from './permissions.ts'
import {
  ADMIN_CSRF_HEADER,
  adminCsrfMatches,
  looksSameOrigin,
  readAdminSessionCookie,
  resolveAdminSession,
} from './session.ts'
import { clearedCookie, hashToken, sessionCookie } from '../auth/session.ts'
import type { Clock } from '../clock.ts'
import { clientAddress } from '../clientaddress.ts'

/** Long enough that an operator has to say something. The same bound routers.ts
 *  puts on every money action, for the same reason: "asked" is not a reason. */
const reason = z.string().trim().min(8).max(500)

/** What a note can be attached to, matching the CHECK on admin_notes. Kept in
 *  step with the database rather than widened here: a subject_type the
 *  constraint refuses is a 500 at the end of a form somebody just filled in. */
const SUBJECT_TYPES = ['user', 'organization', 'repository'] as const
type SubjectType = (typeof SUBJECT_TYPES)[number]

/**
 * How long an impersonation may last.
 *
 * Minutes, and capped, because the failure this bound exists for is not an
 * operator abusing the door. It is an operator forgetting they left it open.
 * The default is the length of a support call; the cap is the length of a bad
 * one. Past that the operator starts another and says why again, which is the
 * behaviour worth having rather than a session that quietly lasts a month.
 */
/**
 * The permission the start route enforces.
 *
 * A named export rather than a string literal at the call site, because this is
 * the one operator permission that is NOT enforced by `adminProcedure`, so the
 * check that every catalogued permission guards something cannot find it by
 * walking the router. That test imports this constant instead of keeping a
 * hand-written list, which means renaming the permission moves both ends at
 * once and deleting the check below makes the test fail rather than pass.
 */
export const IMPERSONATION_START_PERMISSION = 'admin.impersonation.start'

const IMPERSONATION_MIN_MINUTES = 5
const IMPERSONATION_MAX_MINUTES = 60
const IMPERSONATION_DEFAULT_MINUTES = 30

/**
 * The Customers namespace, mounted at `admin.customers` by router.ts.
 *
 * One mount, walked by the operator route matrix, exempt from maintenance mode
 * by its `admin.` prefix. A route added here inherits all three without
 * anybody remembering to arrange them.
 */
export const customersRouter = router({
  // -------------------------------------------------------------------------
  // What an operator wrote down
  // -------------------------------------------------------------------------

  notes: router({
    /**
     * What operators have written about one subject.
     *
     * Retracted notes are returned rather than filtered out, flagged. The
     * `deleted_at` column on this table is a retraction and not a delete, for
     * the reason 0023 gives: a note somebody took back is still a thing an
     * operator wrote about a customer, and the taking back is itself worth
     * being able to see. A list that dropped them would make the soft delete
     * pointless and would hide exactly the row an investigation wants.
     */
    list: adminProcedure('admin.support.read')
      .input(
        z.object({
          subjectType: z.enum(SUBJECT_TYPES),
          subjectId: z.string().uuid(),
          limit: z.number().int().min(1).max(200).default(50),
          cursor: z.string().nullish(),
        }),
      )
      .query(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        const after = splitCursor(input.cursor ?? null)
        const rows = await c.adminDb(async (db) =>
          db.execute<{
            id: string
            body: string
            author_label: string
            created_at: Date | string
            deleted_at: Date | string | null
          }>(sql`
            SELECT id, body, author_label, created_at, deleted_at
            FROM admin_notes
            WHERE subject_type = ${input.subjectType}
              AND subject_id = ${input.subjectId}::uuid
              AND (${after?.at ?? null}::timestamptz IS NULL
                   OR (created_at, id) < (${after?.at ?? null}::timestamptz,
                                          ${after?.id ?? null}::uuid))
            ORDER BY created_at DESC, id DESC
            LIMIT ${input.limit + 1}`),
        )
        // Keyset on the PAIR rather than on created_at alone. Two notes written
        // in the same millisecond are ordinary when somebody pastes a thread,
        // and a cursor on the timestamp alone either repeats one or skips one.
        // On a page that is evidence, skipping is the worse of the two.
        return pageOf(
          rows,
          input.limit,
          (r) => `${iso(r.created_at)}|${r.id}`,
          (r) => ({
            id: r.id,
            body: r.body,
            author: r.author_label,
            createdAt: iso(r.created_at),
            retractedAt: r.deleted_at ? iso(r.deleted_at) : null,
          }),
        )
      }),

    /**
     * Writes one down.
     *
     * The subject is checked to exist first, and that is not defensive coding.
     * admin_notes.subject_id cannot carry a foreign key, by the migration's own
     * argument that a note about a deleted account is a note an investigation
     * still wants, so an unresolvable subject is caught here or nowhere. What
     * produces one is a pasted identifier off by a character, and the note it
     * creates is never found by the person who comes looking for it.
     *
     * author_user_id is left NULL and author_label carries the operator's
     * address. That column references users(id) and an operator is a row in
     * admin_users, a different id space: the operator's id there is a foreign
     * key violation, and the customer's would name the person a note is about
     * as its author.
     */
    add: adminProcedure('admin.support.write')
      .input(
        z.object({
          subjectType: z.enum(SUBJECT_TYPES),
          subjectId: z.string().uuid(),
          body: z.string().trim().min(1).max(10_000),
        }),
      )
      .mutation(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        return c.adminDb(async (db) => {
          const subject = await mustFindSubject(db, input.subjectType, input.subjectId)
          await adminAudit(db, c, {
            action: 'note.added',
            targetType: input.subjectType,
            targetId: input.subjectId,
            subjectOrgId: subject.orgId,
            subjectOrgLabel: subject.orgLabel,
            severity: 'notice',
            // The note's own text is NOT in the detail. The entry says a note
            // was written and about whom; the body here would copy it into a
            // chain that takes no DELETE, so retracting the note would leave it
            // readable one table over and the retraction would be theatre.
            detail: { subject: subject.label, length: input.body.length },
            tenantCopy: false,
          })
          const rows = await db.execute<{ id: string; created_at: Date | string }>(sql`
            INSERT INTO admin_notes (subject_type, subject_id, body, author_label)
            VALUES (${input.subjectType}, ${input.subjectId}::uuid, ${input.body},
                    ${c.admin.email})
            RETURNING id, created_at`)
          return { id: rows[0]!.id, createdAt: iso(rows[0]!.created_at) }
        })
      }),

    /**
     * Takes one back without destroying it.
     *
     * UPDATE rather than DELETE, and the operator role holds no DELETE grant on
     * this table at all, so a future statement that tried would be refused by
     * the database rather than merely reviewed against.
     */
    retract: adminProcedure('admin.support.write')
      .input(z.object({ id: z.string().uuid(), reason }))
      .mutation(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        return c.adminDb(async (db) => {
          const found = await db.execute<{
            subject_type: string
            subject_id: string
            author_label: string
            deleted_at: Date | string | null
          }>(sql`
            SELECT subject_type, subject_id, author_label, deleted_at
            FROM admin_notes WHERE id = ${input.id}::uuid`)
          const note = found[0]
          if (!note) throw new TRPCError({ code: 'NOT_FOUND', message: 'No note with that id.' })
          if (note.deleted_at) {
            throw new TRPCError({
              code: 'PRECONDITION_FAILED',
              message: 'That note was already retracted.',
            })
          }
          await adminAudit(db, c, {
            action: 'note.retracted',
            targetType: note.subject_type,
            targetId: note.subject_id,
            severity: 'notice',
            detail: { noteId: input.id, author: note.author_label, reason: input.reason },
            tenantCopy: false,
          })
          await db.execute(sql`
            UPDATE admin_notes
            SET deleted_at = ${c.clock.now().toISOString()},
                updated_at = ${c.clock.now().toISOString()}
            WHERE id = ${input.id}::uuid`)
          return { retracted: true }
        })
      }),
  }),

  // -------------------------------------------------------------------------
  // Impersonation, the half that only reads
  // -------------------------------------------------------------------------

  impersonation: router({
    /**
     * Who is inside a customer account right now, and who has been lately.
     *
     * Both halves, from two tables, because the page has to answer two
     * questions and only one of them can be answered from live rows. The live
     * list comes from `sessions`, which is the truth about what is open. The
     * recent list comes from the audit chain, which is the only place a
     * FINISHED impersonation still exists, because finishing one deliberately
     * leaves no live row behind.
     *
     * A page showing only the live list would answer "nobody is impersonating
     * anybody", perfectly true and perfectly useless to the person asking
     * whether anybody had.
     */
    list: adminProcedure('admin.impersonation.read').query(async ({ ctx }) => {
      const c = ctx as AdminContext
      const now = c.clock.now()
      return c.adminDb(async (db) => {
        const live = await db.execute<{
          id: string
          user_id: string
          github_login: string
          email: string
          org_id: string | null
          slug: string | null
          operator: string
          reason: string
          seq: string
          created_at: Date | string
          expires_at: Date | string
        }>(sql`
          SELECT s.id, s.user_id, u.github_login, u.email, s.org_id, o.slug,
                 s.impersonator_label AS operator, s.impersonation_reason AS reason,
                 s.impersonation_audit_seq AS seq, s.created_at, s.expires_at
          FROM sessions s
          JOIN users u ON u.id = s.user_id
          LEFT JOIN organizations o ON o.id = s.org_id
          -- Keyed on the sequence number rather than on impersonated_by, which
          -- is the predicate 0034 moved it to and the one the partial index is
          -- built on. impersonated_by goes null when an operator's account is
          -- deleted; the record does not.
          WHERE s.impersonation_audit_seq IS NOT NULL
            AND s.revoked_at IS NULL
            AND s.expires_at > ${now.toISOString()}
          ORDER BY s.created_at DESC
          LIMIT 100`)

        const recent = await db.execute<{
          seq: string
          actor_label: string
          action: string
          target_id: string | null
          subject_org_label: string | null
          detail: unknown
          occurred_at: Date | string
        }>(sql`
          SELECT seq, actor_label, action, target_id, subject_org_label, detail, occurred_at
          FROM admin_audit_entries
          WHERE action IN ('impersonation.started', 'impersonation.ended')
          ORDER BY seq DESC
          LIMIT 50`)

        return {
          live: live.map((r) => ({
            sessionId: r.id,
            userId: r.user_id,
            githubLogin: r.github_login,
            email: r.email,
            orgId: r.org_id,
            orgSlug: r.slug,
            operator: r.operator,
            reason: r.reason,
            auditSeq: Number(r.seq),
            startedAt: iso(r.created_at),
            endsAt: iso(r.expires_at),
          })),
          recent: recent.map((r) => ({
            seq: Number(r.seq),
            operator: r.actor_label,
            action: r.action,
            targetId: r.target_id,
            organization: r.subject_org_label,
            detail: r.detail as Record<string, unknown> | null,
            occurredAt: iso(r.occurred_at),
          })),
        }
      })
    }),
  }),
})

// ---------------------------------------------------------------------------
// Impersonation, the half that sets a cookie
// ---------------------------------------------------------------------------

export interface ImpersonationRouteOptions {
  pool: Pool
  adminPool: AdminPool | null
  clock: Clock
  /** Whether cookies get the Secure attribute, which is the same flag the
   *  sign-in routes take and must be the same value. */
  secure: boolean
  appBaseUrl: string
}

/**
 * Registers POST /v1/admin/impersonation/start and .../end.
 *
 * A registrar rather than two blocks inside server.ts, because six lanes are
 * editing that file and forty lines added in the middle of somebody else's
 * forty is a merge conflict nobody can review. What server.ts gains is one
 * import and one call.
 */
export function registerImpersonationRoutes(
  app: Hono<ApiEnv>,
  options: ImpersonationRouteOptions,
): void {
  const { pool, clock } = options

  /**
   * The two checks the /trpc/* middleware makes, made again here.
   *
   * NOT a duplicate that could be deleted. That middleware is mounted on
   * /trpc/* and these routes are not under it, so without this they would be
   * the only operator mutations in the system with no forgery check at all, and
   * they are the two that hand a browser a customer's session. The order is the
   * middleware's on purpose, origin first and failing open for a request that
   * declares nothing, so the two cannot drift into disagreeing about which
   * refusal a given request gets.
   */
  function refuseForgery(c: HonoContext<ApiEnv>, token: string): Response | null {
    if (
      !looksSameOrigin(
        {
          origin: c.req.header('origin') ?? null,
          secFetchSite: c.req.header('sec-fetch-site') ?? null,
        },
        options.appBaseUrl,
      )
    ) {
      return c.json({ error: 'This operator request came from another site.' }, 403)
    }
    if (!adminCsrfMatches(token, c.req.header(ADMIN_CSRF_HEADER))) {
      return c.json(
        {
          error:
            `This operator request needs the ${ADMIN_CSRF_HEADER} header from the ` +
            'operator session endpoint.',
        },
        403,
      )
    }
    return null
  }

  app.post('/v1/admin/impersonation/start', async (c) => {
    const token = readAdminSessionCookie(c.req.header('cookie'))
    if (!token) return c.json({ error: 'Sign in to the operator portal.' }, 401)
    const operator = await resolveAdminSession(pool, token, clock.now())
    if (!operator) return c.json({ error: 'Sign in to the operator portal.' }, 401)

    const forged = refuseForgery(c, token)
    if (forged) return forged

    const adminPool = options.adminPool
    if (!adminPool) {
      // Said out loud and named, the same refusal requireAdminActor gives,
      // rather than a 500 from the first statement.
      return c.json(
        {
          error:
            'This installation has no operator database credential configured, so it cannot ' +
            'start an impersonation. Set AF_ADMIN_DATABASE_URL.',
        },
        412,
      )
    }

    // Already inside one. Refused rather than replaced, because replacing would
    // be two impersonations by one operator with one reason between them, and
    // the second customer's audit log would carry the first one's sentence.
    if (operator.impersonating) {
      return c.json(
        {
          error:
            'This session is already impersonating a customer. End that one first, so each ' +
            'impersonation has its own reason and its own record.',
        },
        409,
      )
    }

    if (!adminRoleHas(operator.role, IMPERSONATION_START_PERMISSION)) {
      return c.json(
        {
          error:
            'This needs the admin.impersonation.start permission, which your operator role ' +
            'does not have.',
        },
        403,
      )
    }

    const parsed = z
      .object({
        userId: z.string().uuid(),
        orgId: z.string().uuid().nullish(),
        reason,
        minutes: z
          .number()
          .int()
          .min(IMPERSONATION_MIN_MINUTES)
          .max(IMPERSONATION_MAX_MINUTES)
          .default(IMPERSONATION_DEFAULT_MINUTES),
      })
      .safeParse(await c.req.json().catch(() => null))
    if (!parsed.success) {
      return c.json(
        {
          error:
            'Give the account to act as, a reason of at least eight characters, and a length ' +
            `between ${IMPERSONATION_MIN_MINUTES} and ${IMPERSONATION_MAX_MINUTES} minutes.`,
        },
        400,
      )
    }
    const input = parsed.data

    const now = clock.now()
    const endsAt = new Date(now.getTime() + input.minutes * 60_000)
    const sessionToken = randomBytes(32).toString('base64url')
    // Parsed, not taken raw. `x-forwarded-for` is a comma separated list and its
    // entries may carry ports, and this value goes into an `inet` column: two
    // proxies in front of the control plane would otherwise turn the most
    // important action in the portal into a 500 whose message says nothing
    // about a header. Undefined becomes null, because a column that takes an
    // address should hold one or nothing.
    const ip = clientAddress(c.req.header('x-forwarded-for')) ?? null

    try {
      const result = await adminPool.withOperator(
        { adminUserId: operator.adminUserId, label: operator.email },
        async (db) => {
          const found = await db.execute<{
            github_login: string
            email: string
            name: string | null
            suspended_at: Date | string | null
          }>(sql`
            SELECT github_login, email, name, suspended_at
            FROM users WHERE id = ${input.userId}::uuid`)
          const user = found[0]
          if (!user) throw new Refused(404, 'No account with that id.')
          // resolveSession reads users.suspended_at on every request, so a
          // session minted for a suspended account fails on its first use.
          // Refusing here means the audit entry never claims an impersonation
          // that could not have happened.
          if (user.suspended_at) {
            throw new Refused(
              412,
              'That account is suspended, so a session for it would not resolve on its first ' +
                'request. Restore the account first if you need to act as it.',
            )
          }

          let orgId: string | null = null
          let orgLabel: string | null = null
          if (input.orgId) {
            const org = await db.execute<{ slug: string; member: boolean }>(sql`
              SELECT o.slug,
                     EXISTS (SELECT 1 FROM members m
                              WHERE m.org_id = o.id AND m.user_id = ${input.userId}::uuid)
                       AS member
              FROM organizations o WHERE o.id = ${input.orgId}::uuid`)
            const row = org[0]
            if (!row) throw new Refused(404, 'No organization with that id.')
            // The product would show an empty console rather than refuse, so
            // the refusal belongs here. An operator acting as somebody in an
            // organization that person is not in is not a support session; it
            // is a cross tenant read wearing one.
            if (!row.member) {
              throw new Refused(
                412,
                'That account is not a member of that organization, so acting as it there ' +
                  'would show nothing the account can actually see.',
              )
            }
            orgId = input.orgId
            orgLabel = row.slug
          }

          // The record first, and not merely first: the sequence number it
          // returns is a NOT NULL column on the row inserted below, with a
          // foreign key into this chain. A session that was never audited
          // cannot be represented, which is what 0023 built the column for and
          // what 0034 made true.
          const entry = await appendAdminAudit(db, {
            adminUserId: operator.adminUserId,
            actorLabel: operator.email,
            action: 'impersonation.started',
            targetType: 'user',
            targetId: input.userId,
            subjectOrgId: orgId,
            subjectOrgLabel: orgLabel,
            origin: 'admin',
            ip,
            // The same severity as creating an operator, and for the same
            // reason: both are somebody holding an identity they did not hold a
            // moment ago.
            severity: 'critical',
            detail: {
              reason: input.reason,
              githubLogin: user.github_login,
              minutes: input.minutes,
              endsAt: endsAt.toISOString(),
            },
            // The customer's own log gets this one. They are entitled to know
            // that somebody from the vendor was inside their account, when, and
            // why. This is the case tenantCopy exists for, and it only reaches
            // them when the session names an organization, because an entry
            // with no subject has no log to be copied into.
            tenantCopy: orgId !== null,
            occurredAt: now,
          })

          await db.execute(sql`
            INSERT INTO sessions (token_hash, user_id, org_id, ip, user_agent,
                                  created_at, last_seen_at, expires_at,
                                  impersonated_by, impersonator_label,
                                  impersonation_reason, impersonation_audit_seq)
            VALUES (${hashToken(sessionToken)}, ${input.userId}::uuid,
                    ${orgId}::uuid, ${ip}, ${c.req.header('user-agent') ?? null},
                    ${now.toISOString()}, ${now.toISOString()}, ${endsAt.toISOString()},
                    ${operator.adminUserId}::uuid, ${operator.email},
                    ${input.reason}, ${entry.seq})`)

          // The marker on the OPERATOR's own session, which is what the gate
          // reads to close the portal. Both rows or neither, in one
          // transaction: a customer session with no operator marker is an
          // impersonation the portal cannot see, and an operator marker with no
          // session is a portal locked for no reason.
          await db.execute(sql`
            UPDATE admin_sessions
            SET impersonated_user_id = ${input.userId}::uuid,
                impersonation_reason = ${input.reason},
                impersonation_audit_seq = ${entry.seq}
            WHERE id = ${operator.sessionId}::uuid`)

          return {
            userId: input.userId,
            githubLogin: user.github_login,
            label: user.name || user.github_login,
            orgId,
            orgSlug: orgLabel,
            auditSeq: entry.seq,
          }
        },
      )

      c.header('set-cookie', sessionCookie(sessionToken, endsAt, options.secure))
      return c.json({
        impersonating: true,
        ...result,
        endsAt: endsAt.toISOString(),
        // Said in the response as well as on the screen, because this is the
        // sentence an operator repeats to whoever asked them to do it.
        effect:
          'This browser is now signed in as that account. The operator portal is closed to ' +
          'this session until the impersonation ends, the customer sees it in their own audit ' +
          'log, and it stops by itself when it expires.',
      })
    } catch (err) {
      if (err instanceof Refused) return c.json({ error: err.message }, err.status as 400)
      throw err
    }
  })

  /**
   * Ends it.
   *
   * CHECKS NO PERMISSION, deliberately. The session ending an impersonation is
   * the session that is inside one, and asking adminRoleHas would consult a
   * role that may have been changed or suspended while the operator was in
   * there. A door that can be entered and not left is worse than one that never
   * opened, so the only question this asks is "are you the session that is
   * inside this", which the cookie answers.
   *
   * Idempotent. A second press, a stale tab and a page that reloaded after the
   * first one succeeded all get a 200 and a cleared cookie rather than an
   * error, because every one of those is somebody trying to get out.
   */
  app.post('/v1/admin/impersonation/end', async (c) => {
    const token = readAdminSessionCookie(c.req.header('cookie'))
    if (!token) return c.json({ error: 'Sign in to the operator portal.' }, 401)
    const operator = await resolveAdminSession(pool, token, clock.now())
    if (!operator) return c.json({ error: 'Sign in to the operator portal.' }, 401)

    const forged = refuseForgery(c, token)
    if (forged) return forged

    const adminPool = options.adminPool
    if (!adminPool) {
      return c.json(
        {
          error:
            'This installation has no operator database credential configured. Set ' +
            'AF_ADMIN_DATABASE_URL.',
        },
        412,
      )
    }

    const revoked = await endImpersonation(adminPool, operator, clock.now(), {
      ip: clientAddress(c.req.header('x-forwarded-for')) ?? null,
      how: 'ended',
    })

    // Cleared whether or not there was anything to end. A browser holding a
    // customer cookie whose operator marker is already gone is exactly the
    // state this button exists to leave.
    c.header('set-cookie', clearedCookie(options.secure))
    // `impersonating` is the state AFTER this call, which is false either way,
    // and `ended` is whether this call is what made it false. A second press
    // reports ended: false rather than an error, because the caller's goal has
    // been achieved and telling them otherwise invites them to press again.
    return c.json({ impersonating: false, ended: revoked !== null, revoked: revoked ?? 0 })
  })
}

/**
 * Closes an impersonation: the record, the customer sessions, and the marker.
 *
 * Exported because there are TWO ways out and both have to do the same work.
 * The obvious one is the End button. The other is signing out of the operator
 * portal entirely, which is what the shell's own refusal screen has always
 * offered, and which used to clear the OPERATOR cookie and nothing else. That
 * left the customer cookie live in the browser for the rest of its lifetime,
 * with the operator marker gone from the row that would have explained it. A
 * way out that leaves the door open is not a way out, and the second caller is
 * the reason this is a function rather than a route body.
 *
 * Returns the number of customer sessions revoked, or null when the operator
 * was not impersonating and there was nothing to do. Null rather than 0 so a
 * caller can tell "ended nothing" from "was never in one", which is the
 * difference between an idempotent second press and a bug.
 */
export async function endImpersonation(
  adminPool: AdminPool,
  operator: {
    adminUserId: string
    email: string
    sessionId: string
    impersonating: boolean
    impersonatedUserId: string | null
  },
  now: Date,
  context: { ip: string | null; how: 'ended' | 'signed out' },
): Promise<number | null> {
  if (!operator.impersonating || !operator.impersonatedUserId) return null
  const impersonatedUserId = operator.impersonatedUserId

  return adminPool.withOperator(
    { adminUserId: operator.adminUserId, label: operator.email },
    async (db) => {
      const target = await db.execute<{ github_login: string }>(sql`
        SELECT github_login FROM users WHERE id = ${impersonatedUserId}::uuid`)
      const live = await db.execute<{ id: string; org_id: string | null; slug: string | null }>(sql`
        SELECT s.id, s.org_id, o.slug
        FROM sessions s LEFT JOIN organizations o ON o.id = s.org_id
        WHERE s.impersonated_by = ${operator.adminUserId}::uuid
          AND s.revoked_at IS NULL`)

      await appendAdminAudit(db, {
        adminUserId: operator.adminUserId,
        actorLabel: operator.email,
        action: 'impersonation.ended',
        targetType: 'user',
        targetId: impersonatedUserId,
        subjectOrgId: live[0]?.org_id ?? null,
        subjectOrgLabel: live[0]?.slug ?? null,
        origin: 'admin',
        ip: context.ip,
        severity: 'high',
        detail: {
          githubLogin: target[0]?.github_login ?? null,
          sessionsRevoked: live.length,
          // Which door they left by. Both end the impersonation and only one of
          // them is deliberate, so an investigation reading the chain can tell
          // "finished the call" from "closed the tab".
          how: context.how,
        },
        tenantCopy: (live[0]?.org_id ?? null) !== null,
        occurredAt: now,
      })

      // Every live one this operator holds, not only the newest. If an earlier
      // end failed halfway there is a session out there with this operator's id
      // on it, and leaving it live because it is not the one being ended right
      // now is how a cookie outlives the reason for it.
      //
      // revoked_at rather than DELETE, the same choice sessions.revoke makes:
      // resolveSession reads revoked_at on every request before the expiry
      // check, so the next request fails, and the row stays as evidence of what
      // was signed in from where.
      await db.execute(sql`
        UPDATE sessions SET revoked_at = ${now.toISOString()}
        WHERE impersonated_by = ${operator.adminUserId}::uuid AND revoked_at IS NULL`)

      await db.execute(sql`
        UPDATE admin_sessions
        SET impersonated_user_id = NULL, impersonation_reason = NULL,
            impersonation_audit_seq = NULL
        WHERE id = ${operator.sessionId}::uuid`)

      return live.length
    },
  )
}

/** A refusal raised inside the transaction and answered outside it, so the
 *  transaction rolls back rather than committing half an impersonation on its
 *  way to returning an error. */
class Refused extends Error {
  // A plain field assigned in the body rather than a parameter property. This
  // project compiles with `erasableSyntaxOnly`, which refuses the shorthand
  // because erasing the type annotation would change what the constructor does.
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
    this.name = 'Refused'
  }
}

// ---------------------------------------------------------------------------
// Shared bits
// ---------------------------------------------------------------------------

/**
 * The thing a note is about, and the organization to file the entry under.
 *
 * One function rather than three call sites, because the interesting part is
 * the same in all three: admin_notes.subject_id cannot carry a foreign key, so
 * an unresolvable subject is caught here or nowhere.
 */
async function mustFindSubject(
  db: Db,
  type: SubjectType,
  id: string,
): Promise<{ label: string; orgId: string | null; orgLabel: string | null }> {
  if (type === 'organization') {
    const rows = await db.execute<{ slug: string }>(
      sql`SELECT slug FROM organizations WHERE id = ${id}::uuid`,
    )
    if (!rows[0]) {
      throw new TRPCError({ code: 'NOT_FOUND', message: 'No organization with that id.' })
    }
    return { label: rows[0].slug, orgId: id, orgLabel: rows[0].slug }
  }
  if (type === 'user') {
    const rows = await db.execute<{ github_login: string }>(
      sql`SELECT github_login FROM users WHERE id = ${id}::uuid`,
    )
    if (!rows[0]) throw new TRPCError({ code: 'NOT_FOUND', message: 'No account with that id.' })
    // No organization on a note about a person. They may be in several, and
    // filing the entry under one of them would attribute the note to an
    // account that has nothing to do with why it was written.
    return { label: rows[0].github_login, orgId: null, orgLabel: null }
  }
  const rows = await db.execute<{ full_name: string; org_id: string; slug: string }>(sql`
    SELECT r.full_name, r.org_id, o.slug
    FROM repositories r JOIN organizations o ON o.id = r.org_id
    WHERE r.id = ${id}::uuid`)
  if (!rows[0]) throw new TRPCError({ code: 'NOT_FOUND', message: 'No repository with that id.' })
  return { label: rows[0].full_name, orgId: rows[0].org_id, orgLabel: rows[0].slug }
}

/** Splits the composite note cursor. Null for a first page and for anything
 *  malformed: a cursor is opaque, and a caller inventing one gets the first
 *  page rather than a 500. */
function splitCursor(cursor: string | null): { at: string; id: string } | null {
  if (!cursor) return null
  const bar = cursor.lastIndexOf('|')
  if (bar <= 0) return null
  return { at: cursor.slice(0, bar), id: cursor.slice(bar + 1) }
}

/** Asking for limit + 1 and returning limit is how "is there more" is answered
 *  without a second count query. The same helper router.ts keeps, copied rather
 *  than exported across the lane boundary because it is six lines and the
 *  import would be the only thing tying the two files together. */
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

function iso(value: Date | string): string {
  return value instanceof Date ? value.toISOString() : new Date(value).toISOString()
}
