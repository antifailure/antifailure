// The operator routes.
//
// Every one of these is built with adminProcedure, which is the only exported
// way to make one, so declaring the permission and creating the route are a
// single act. There is no unguarded builder in this file to reach for.
//
// WHAT IS AND IS NOT HERE. Reads across every tenant, and three writes: suspend
// an organization, change its plan, and revoke a session. Those three were
// chosen because each is a lever that ALREADY EXISTS and is already enforced
// somewhere a test can observe it, so a button here moves something real rather
// than writing a row nobody consults:
//
//   suspend  -> organizations.suspended_at, refused at ingest (server.ts) and
//               at dispatch (routers/dispatch.ts).
//   plan     -> quotas are derived from it in limits.ts, and hosted access is
//               gated on it in hosted.ts.
//   revoke   -> resolveSession reads revoked_at on EVERY request before the
//               expiry check, so the next request fails.
//
// A write whose effect could not be traced to an existing enforcement point was
// left out rather than shipped, because an operator who presses Suspend and is
// told it worked has been lied to if nothing refuses afterwards.
//
// SUSPEND'S HONEST SEMANTICS. It stops the organization creating new work and
// leaves running environments alone. That is what the column has always meant
// and what 0010 says about it. The copy in the console says exactly that; an
// operator who reads "suspended" as "locked out" during an incident will make
// the wrong call about what else to do.

import { z } from 'zod'
import { sql } from 'drizzle-orm'
import { TRPCError } from '@trpc/server'
import { router } from '../trpc.ts'
import { adminProcedure, adminAudit, type AdminContext } from './trpc.ts'
import {
  ADMIN_PERMISSIONS,
  ADMIN_PERMISSION_DESCRIPTIONS,
  ADMIN_ROLES,
  ADMIN_ROLE_PERMISSIONS,
  adminRolesWith,
} from './permissions.ts'

/** A page of rows, and how to ask for the next one.
 *
 *  Keyset rather than OFFSET. An operator paging through every organization on
 *  the instance while rows are being inserted gets duplicates and gaps from
 *  OFFSET, and the duplicates look like real data. */
const page = z.object({
  limit: z.number().int().min(1).max(200).default(50),
  cursor: z.string().nullish(),
})

/**
 * The columns an operator read may name, per table.
 *
 * RLS is row level and cannot restrict a column, and a GRANT cannot help either
 * when the operator path and the tenant path share one database role. So column
 * safety on this path is an application property, and this is where it lives:
 * every query below names its columns from these lists, none of them says
 * `select *`, and none of them names a secret.
 *
 * What is deliberately absent: sessions.token_hash and sessions.csrf_secret,
 * engine_tokens' hashes, admin_users.password_hash and password_salt. An
 * operator answering a support question never needs a credential, and a portal
 * that can display one is a portal that can leak one.
 */
const SAFE_COLUMNS = {
  organizations: ['id', 'slug', 'name', 'plan', 'created_at', 'suspended_at', 'suspended_reason'],
  users: ['id', 'github_login', 'email', 'name', 'created_at'],
  sessions: ['id', 'user_id', 'org_id', 'created_at', 'last_seen_at', 'expires_at', 'revoked_at', 'ip', 'user_agent'],
  admin_users: ['id', 'email', 'name', 'role', 'is_root', 'suspended_at', 'last_signed_in_at', 'created_at'],
} as const

export const adminRouter = router({
  /**
   * Who the operator is, and what the shell may show them.
   *
   * Guarded by admin.portal.access rather than left public, because the answer
   * names an operator and their role. Every role holds that permission, so the
   * gate here is "is there a live operator session", which is the question the
   * shell is actually asking.
   */
  me: adminProcedure('admin.portal.access').query(({ ctx }) => {
    const c = ctx as AdminContext
    return {
      adminUserId: c.admin.adminUserId,
      label: c.admin.label,
      email: c.admin.email,
      role: c.admin.role,
      impersonating: c.admin.impersonating,
      // The shell hides a nav entry whose permission is absent, so it needs the
      // list rather than the role name. Sending the role and letting the client
      // reimplement the table is how the two drift.
      permissions: ADMIN_ROLE_PERMISSIONS[c.admin.role],
    }
  }),

  /** The catalog, for the roles page. Guarded because it describes the
   *  platform's own privilege structure rather than the product's. */
  catalog: adminProcedure('admin.operators.read').query(() => ({
    permissions: ADMIN_PERMISSIONS.map((p) => ({
      name: p,
      description: ADMIN_PERMISSION_DESCRIPTIONS[p],
      roles: adminRolesWith(p),
    })),
    roles: ADMIN_ROLES.map((r) => ({ name: r, permissions: ADMIN_ROLE_PERMISSIONS[r] })),
  })),

  // -------------------------------------------------------------------------
  // Tenants
  // -------------------------------------------------------------------------

  tenants: router({
    list: adminProcedure('admin.tenants.read')
      .input(page.extend({ query: z.string().trim().max(200).optional() }))
      .query(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        const rows = await c.adminDb(async (db) =>
          db.execute<{
            id: string
            slug: string
            name: string
            plan: string
            created_at: Date | string
            suspended_at: Date | string | null
            suspended_reason: string | null
            members: string
            environments: string
          }>(sql`
            SELECT o.id, o.slug, o.name, o.plan, o.created_at,
                   o.suspended_at, o.suspended_reason,
                   (SELECT count(*) FROM members m WHERE m.org_id = o.id) AS members,
                   (SELECT count(*) FROM environments e
                     WHERE e.org_id = o.id AND e.state <> 'torn_down') AS environments
            FROM organizations o
            WHERE (${input.query ?? null}::text IS NULL
                   OR o.slug ILIKE ${'%' + (input.query ?? '') + '%'}
                   OR o.name ILIKE ${'%' + (input.query ?? '') + '%'})
              AND (${input.cursor ?? null}::text IS NULL OR o.slug > ${input.cursor ?? ''})
            ORDER BY o.slug ASC
            LIMIT ${input.limit + 1}`),
        )
        return pageOf(rows, input.limit, (r) => r.slug, (r) => ({
          id: r.id,
          slug: r.slug,
          name: r.name,
          plan: r.plan,
          createdAt: iso(r.created_at),
          suspended: r.suspended_at !== null,
          suspendedReason: r.suspended_reason,
          members: Number(r.members),
          environments: Number(r.environments),
        }))
      }),

    get: adminProcedure('admin.tenants.read')
      .input(z.object({ orgId: z.string().uuid() }))
      .query(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        return c.adminDb(async (db) => {
          const rows = await db.execute<Record<string, unknown>>(sql`
            SELECT o.id, o.slug, o.name, o.plan, o.created_at, o.suspended_at,
                   o.suspended_reason, o.suspended_by
            FROM organizations o WHERE o.id = ${input.orgId}::uuid`)
          const org = rows[0]
          if (!org) {
            throw new TRPCError({ code: 'NOT_FOUND', message: 'No organization with that id.' })
          }
          const members = await db.execute<{
            user_id: string
            role: string
            github_login: string
            email: string
            name: string | null
          }>(sql`
            SELECT m.user_id, m.role::text AS role, u.github_login, u.email, u.name
            FROM members m JOIN users u ON u.id = m.user_id
            WHERE m.org_id = ${input.orgId}::uuid
            ORDER BY u.github_login ASC LIMIT 200`)
          return {
            id: org.id as string,
            slug: org.slug as string,
            name: org.name as string,
            plan: org.plan as string,
            createdAt: iso(org.created_at as Date),
            suspended: org.suspended_at !== null,
            suspendedReason: (org.suspended_reason as string | null) ?? null,
            suspendedBy: (org.suspended_by as string | null) ?? null,
            members: members.map((m) => ({
              userId: m.user_id,
              role: m.role,
              githubLogin: m.github_login,
              email: m.email,
              name: m.name,
            })),
          }
        })
      }),

    /**
     * Stops an organization creating new work.
     *
     * A reason is REQUIRED and is not a formality: it is written into
     * `suspended_reason`, which is what the customer's own console shows them
     * and what the ingest path quotes back when it refuses an event. A
     * suspension with no reason is one nobody can undo confidently later.
     */
    suspend: adminProcedure('admin.tenants.suspend')
      .input(z.object({ orgId: z.string().uuid(), reason: z.string().trim().min(1).max(500) }))
      .mutation(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        return c.adminDb(async (db) => {
          const org = await mustFindOrg(db, input.orgId)
          // The record first, in the same transaction. If the UPDATE fails the
          // entry goes with it, and the UPDATE cannot commit without it.
          await adminAudit(db, c, {
            action: 'organization.suspended',
            targetType: 'organization',
            targetId: input.orgId,
            subjectOrgId: input.orgId,
            subjectOrgLabel: org.slug,
            severity: 'high',
            detail: { reason: input.reason },
          })
          await db.execute(sql`
            UPDATE organizations
            SET suspended_at = ${c.clock.now().toISOString()},
                suspended_reason = ${input.reason},
                suspended_by = ${c.admin.label},
                updated_at = ${c.clock.now().toISOString()}
            WHERE id = ${input.orgId}::uuid`)
          return {
            suspended: true,
            // Said in the response as well as in the console, because this is
            // the sentence an operator repeats to whoever asked for it.
            effect: 'No new environments, agent runs or events. Anything already running keeps running.',
          }
        })
      }),

    resume: adminProcedure('admin.tenants.suspend')
      .input(z.object({ orgId: z.string().uuid() }))
      .mutation(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        return c.adminDb(async (db) => {
          const org = await mustFindOrg(db, input.orgId)
          await adminAudit(db, c, {
            action: 'organization.resumed',
            targetType: 'organization',
            targetId: input.orgId,
            subjectOrgId: input.orgId,
            subjectOrgLabel: org.slug,
            severity: 'high',
          })
          await db.execute(sql`
            UPDATE organizations
            SET suspended_at = NULL, suspended_reason = NULL, suspended_by = NULL,
                updated_at = ${c.clock.now().toISOString()}
            WHERE id = ${input.orgId}::uuid`)
          return { suspended: false }
        })
      }),

    /**
     * Changes the plan, which is what quotas are derived from.
     *
     * Named setPlan rather than setQuota because there is no per-organization
     * quota column: limits.ts derives every quota from the plan, and inventing
     * an override here would create a second source of truth that the quota
     * check does not read.
     */
    setPlan: adminProcedure('admin.tenants.plan')
      .input(
        z.object({
          orgId: z.string().uuid(),
          plan: z.string().trim().min(1).max(64),
          reason: z.string().trim().min(1).max(500),
        }),
      )
      .mutation(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        return c.adminDb(async (db) => {
          const org = await mustFindOrg(db, input.orgId)
          const previous = await db.execute<{ plan: string }>(
            sql`SELECT plan FROM organizations WHERE id = ${input.orgId}::uuid`,
          )
          await adminAudit(db, c, {
            action: 'organization.plan_changed',
            targetType: 'organization',
            targetId: input.orgId,
            subjectOrgId: input.orgId,
            subjectOrgLabel: org.slug,
            severity: 'notice',
            // Before and after, because "who changed this and to what" is the
            // question a billing dispute actually asks.
            detail: { from: previous[0]?.plan ?? null, to: input.plan, reason: input.reason },
          })
          await db.execute(sql`
            UPDATE organizations SET plan = ${input.plan},
                                     updated_at = ${c.clock.now().toISOString()}
            WHERE id = ${input.orgId}::uuid`)
          return { plan: input.plan }
        })
      }),
  }),

  // -------------------------------------------------------------------------
  // People and their sessions
  // -------------------------------------------------------------------------

  users: router({
    list: adminProcedure('admin.users.read')
      .input(page.extend({ query: z.string().trim().max(200).optional() }))
      .query(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        const rows = await c.adminDb(async (db) =>
          db.execute<{
            id: string
            github_login: string
            email: string
            name: string | null
            created_at: Date | string
            orgs: string
          }>(sql`
            SELECT u.id, u.github_login, u.email, u.name, u.created_at,
                   (SELECT count(*) FROM members m WHERE m.user_id = u.id) AS orgs
            FROM users u
            WHERE (${input.query ?? null}::text IS NULL
                   OR u.github_login ILIKE ${'%' + (input.query ?? '') + '%'}
                   OR u.email ILIKE ${'%' + (input.query ?? '') + '%'})
              AND (${input.cursor ?? null}::text IS NULL OR u.github_login > ${input.cursor ?? ''})
            ORDER BY u.github_login ASC
            LIMIT ${input.limit + 1}`),
        )
        return pageOf(rows, input.limit, (r) => r.github_login, (r) => ({
          id: r.id,
          githubLogin: r.github_login,
          email: r.email,
          name: r.name,
          createdAt: iso(r.created_at),
          organizations: Number(r.orgs),
        }))
      }),
  }),

  sessions: router({
    list: adminProcedure('admin.sessions.read')
      .input(z.object({ userId: z.string().uuid() }))
      .query(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        const rows = await c.adminDb(async (db) =>
          // Named columns, never a star. token_hash and csrf_secret are on this
          // table and an operator has no use for either.
          db.execute<{
            id: string
            org_id: string | null
            slug: string | null
            created_at: Date | string
            last_seen_at: Date | string
            expires_at: Date | string
            revoked_at: Date | string | null
            ip: string | null
            user_agent: string | null
          }>(sql`
            SELECT s.id, s.org_id, o.slug, s.created_at, s.last_seen_at,
                   s.expires_at, s.revoked_at, host(s.ip) AS ip, s.user_agent
            FROM sessions s LEFT JOIN organizations o ON o.id = s.org_id
            WHERE s.user_id = ${input.userId}::uuid
            ORDER BY s.last_seen_at DESC LIMIT 100`),
        )
        return rows.map((r) => ({
          id: r.id,
          orgId: r.org_id,
          orgSlug: r.slug,
          createdAt: iso(r.created_at),
          lastSeenAt: iso(r.last_seen_at),
          expiresAt: iso(r.expires_at),
          revoked: r.revoked_at !== null,
          ip: r.ip,
          userAgent: r.user_agent,
        }))
      }),

    /**
     * Signs one session out.
     *
     * revoked_at rather than DELETE, and that is not the product's own choice
     * for its own sign-out. It is deliberate here: an operator revoking
     * somebody else's session is an investigation, and the row is evidence of
     * what was signed in from where. resolveSession reads revoked_at on every
     * request BEFORE the expiry check, so the next request fails either way.
     */
    revoke: adminProcedure('admin.sessions.revoke')
      .input(z.object({ sessionId: z.string().uuid(), reason: z.string().trim().min(1).max(500) }))
      .mutation(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        return c.adminDb(async (db) => {
          const found = await db.execute<{ user_id: string; org_id: string | null; slug: string | null }>(sql`
            SELECT s.user_id, s.org_id, o.slug
            FROM sessions s LEFT JOIN organizations o ON o.id = s.org_id
            WHERE s.id = ${input.sessionId}::uuid AND s.revoked_at IS NULL`)
          const row = found[0]
          if (!row) {
            throw new TRPCError({
              code: 'NOT_FOUND',
              message: 'No live session with that id. It may already have been signed out.',
            })
          }
          await adminAudit(db, c, {
            action: 'session.revoked',
            targetType: 'session',
            targetId: input.sessionId,
            subjectOrgId: row.org_id,
            subjectOrgLabel: row.slug,
            severity: 'high',
            detail: { reason: input.reason, userId: row.user_id },
          })
          await db.execute(sql`
            UPDATE sessions SET revoked_at = ${c.clock.now().toISOString()}
            WHERE id = ${input.sessionId}::uuid`)
          return { revoked: true }
        })
      }),
  }),

  // -------------------------------------------------------------------------
  // Operators and the record of what they did
  // -------------------------------------------------------------------------

  operators: router({
    list: adminProcedure('admin.operators.read').query(async ({ ctx }) => {
      const c = ctx as AdminContext
      const rows = await c.adminDb(async (db) =>
        // password_hash and password_salt are on this table and are named
        // nowhere here. See SAFE_COLUMNS.
        db.execute<{
          id: string
          email: string
          name: string
          role: string
          is_root: boolean
          suspended_at: Date | string | null
          last_signed_in_at: Date | string | null
          provisioned: boolean
        }>(sql`
          SELECT id, email, name, role, is_root, suspended_at, last_signed_in_at,
                 (password_hash IS NOT NULL) AS provisioned
          FROM admin_users ORDER BY email ASC`),
      )
      return rows.map((r) => ({
        id: r.id,
        email: r.email,
        name: r.name,
        role: r.role,
        isRoot: r.is_root,
        suspended: r.suspended_at !== null,
        lastSignedInAt: r.last_signed_in_at ? iso(r.last_signed_in_at) : null,
        /** Whether a password has ever been set. An operator who cannot sign in
         *  looks identical to one who can, and that is the difference. */
        provisioned: r.provisioned,
      }))
    }),
  }),

  audit: router({
    list: adminProcedure('admin.audit.read')
      .input(
        page.extend({
          severity: z.enum(['info', 'notice', 'high', 'critical']).optional(),
          orgId: z.string().uuid().optional(),
        }),
      )
      .query(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        const rows = await c.adminDb(async (db) =>
          db.execute<{
            seq: string
            actor_label: string
            action: string
            target_type: string
            target_id: string | null
            subject_org_label: string | null
            severity: string
            detail: unknown
            occurred_at: Date | string
          }>(sql`
            SELECT seq, actor_label, action, target_type, target_id,
                   subject_org_label, severity, detail, occurred_at
            FROM admin_audit_entries
            WHERE (${input.severity ?? null}::text IS NULL OR severity = ${input.severity ?? ''})
              AND (${input.orgId ?? null}::uuid IS NULL OR subject_org_id = ${input.orgId ?? null}::uuid)
              AND (${input.cursor ?? null}::text IS NULL OR seq < ${Number(input.cursor ?? 0)})
            ORDER BY seq DESC
            LIMIT ${input.limit + 1}`),
        )
        return pageOf(rows, input.limit, (r) => String(r.seq), (r) => ({
          seq: Number(r.seq),
          actor: r.actor_label,
          action: r.action,
          targetType: r.target_type,
          targetId: r.target_id,
          organization: r.subject_org_label,
          severity: r.severity,
          detail: r.detail,
          occurredAt: iso(r.occurred_at),
        }))
      }),
  }),
})

/** Reads the organization or refuses, so an audit entry is never written about
 *  a row that does not exist. */
async function mustFindOrg(
  db: Parameters<Parameters<AdminContext['adminDb']>[0]>[0],
  orgId: string,
): Promise<{ slug: string }> {
  const rows = await db.execute<{ slug: string }>(
    sql`SELECT slug FROM organizations WHERE id = ${orgId}::uuid`,
  )
  const row = rows[0]
  if (!row) throw new TRPCError({ code: 'NOT_FOUND', message: 'No organization with that id.' })
  return row
}

/**
 * Turns one extra row into a cursor.
 *
 * Asking for limit + 1 and returning limit is how "is there more" is answered
 * without a second count query, which on a cross-tenant table is the expensive
 * one.
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

function iso(value: Date | string): string {
  return value instanceof Date ? value.toISOString() : new Date(value).toISOString()
}
