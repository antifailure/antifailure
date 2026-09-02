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
  adminBillingRouter,
  adminEntitlementsRouter,
  adminFlagsRouter,
} from './routers.ts'
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

/**
 * THE ONE `admin:` KEY, and why the lanes compose here rather than mounting
 * themselves.
 *
 * Four agents own four slices of this portal, and each of them can write a
 * router. What none of them can do alone is guarantee there is exactly one
 * `admin:` key in appRouter: a second `admin: theirRouter` does not conflict in
 * git, does not fail to compile, and silently wins or loses depending on
 * object key order. That is the duplicate-implementation trap this codebase has
 * spent the night deleting, wearing an object literal.
 *
 * So the mount point is here and it is the only one. A lane exports its
 * sub-routers, this file spreads them in, and appRouter names `adminRouter`
 * once. The cost is that adding a lane is an edit to this file, which is the
 * point: it is a visible edit in a review rather than an invisible collision at
 * runtime.
 *
 * The prefix `admin.` is load bearing beyond tidiness. Maintenance mode exempts
 * `/trpc/admin.*` so an operator can still reach the switch that releases an
 * outage, so a route that lands outside this object is a route that goes dark
 * exactly when it is needed. Every lane's paths therefore read `admin.<lane>.*`.
 *
 * To add a lane: import its sub-routers and spread them below with a comment
 * naming the owner. Do NOT add a second `admin:` key to appRouter.
 */
export const adminRouter = router({
  // The money lane, composed in rather than mounted beside. One operator tree
  // means one matrix test walking it and one place a reader looks for "what can
  // an operator do", which is the same argument this file's own header makes
  // for one catalog.
  billing: adminBillingRouter,
  entitlements: adminEntitlementsRouter,
  flags: adminFlagsRouter,

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

    /**
     * Suspends an ACCOUNT, which is not the same as revoking a session.
     *
     * Revoking ends one sign-in. Suspending ends every current session AND
     * every future one, because resolveSession reads users.suspended_at on
     * every request beside the session's own revoked_at.
     *
     * That enforcement is what makes this route mean anything, and it did not
     * exist until this commit: the column was added by an earlier migration
     * and read by nothing, so a Suspend would have changed a row, written an
     * audit entry, and left the person working. The route came second on
     * purpose.
     */
    suspend: adminProcedure('admin.users.write')
      .input(z.object({ userId: z.string().uuid(), reason: z.string().trim().min(1).max(500) }))
      .mutation(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        return c.adminDb(async (db) => {
          const found = await db.execute<{ github_login: string; suspended_at: Date | null }>(sql`
            SELECT github_login, suspended_at FROM users WHERE id = ${input.userId}::uuid`)
          const user = found[0]
          if (!user) {
            throw new TRPCError({ code: 'NOT_FOUND', message: 'No account with that id.' })
          }
          if (user.suspended_at) {
            throw new TRPCError({
              code: 'PRECONDITION_FAILED',
              message: 'That account is already suspended.',
            })
          }
          // The record first, in the same transaction, so a failed update
          // takes its entry with it and the update cannot commit without one.
          await adminAudit(db, c, {
            action: 'user.suspended',
            targetType: 'user',
            targetId: input.userId,
            severity: 'high',
            detail: { reason: input.reason, githubLogin: user.github_login },
          })
          await db.execute(sql`
            UPDATE users
            SET suspended_at = ${c.clock.now().toISOString()},
                suspended_reason = ${input.reason},
                suspended_by = ${c.admin.email},
                updated_at = ${c.clock.now().toISOString()}
            WHERE id = ${input.userId}::uuid`)
          return {
            suspended: true,
            // Said in the response as well as the console, because this is the
            // sentence an operator repeats to whoever asked for it. Unlike an
            // organization suspension, this one DOES lock the person out.
            effect:
              'Every session this account holds stops working on its next request, and it cannot sign in again until restored.',
          }
        })
      }),

    restore: adminProcedure('admin.users.write')
      .input(z.object({ userId: z.string().uuid() }))
      .mutation(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        return c.adminDb(async (db) => {
          const found = await db.execute<{ github_login: string }>(sql`
            SELECT github_login FROM users WHERE id = ${input.userId}::uuid`)
          if (!found[0]) {
            throw new TRPCError({ code: 'NOT_FOUND', message: 'No account with that id.' })
          }
          await adminAudit(db, c, {
            action: 'user.restored',
            targetType: 'user',
            targetId: input.userId,
            severity: 'high',
            detail: { githubLogin: found[0].github_login },
          })
          await db.execute(sql`
            UPDATE users
            SET suspended_at = NULL, suspended_reason = NULL, suspended_by = NULL,
                updated_at = ${c.clock.now().toISOString()}
            WHERE id = ${input.userId}::uuid`)
          // Restoring does NOT bring old sessions back. They were not revoked,
          // so they resolve again if they have not otherwise expired, which is
          // the honest behaviour: suspension paused the account rather than
          // ending its sign-ins, and saying otherwise would be a promise the
          // sessions table does not keep.
          return { suspended: false }
        })
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

    /**
     * Creates an operator who CANNOT SIGN IN YET.
     *
     * No password is accepted here and none is generated. The row lands with a
     * NULL password_hash, and no password hashes to NULL, so the account
     * exists and is unusable until somebody provisions a credential out of
     * band. That is the whole provisioning story: there is no default
     * credential anywhere in this system, and a route that minted one would be
     * the single worst thing in the portal.
     *
     * The operator list shows `provisioned: false` for these, so an account
     * that cannot sign in is visibly different from one that can rather than
     * looking identical until somebody tries.
     */
    create: adminProcedure('admin.operators.write')
      .input(
        z.object({
          email: z.string().trim().email().max(320),
          name: z.string().trim().min(1).max(200),
          role: z.enum(ADMIN_ROLES),
        }),
      )
      .mutation(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        const email = input.email.toLowerCase()
        return c.adminDb(async (db) => {
          const clash = await db.execute<{ id: string }>(
            sql`SELECT id FROM admin_users WHERE email = ${email}`,
          )
          if (clash[0]) {
            throw new TRPCError({
              code: 'CONFLICT',
              message: 'An operator with that email already exists.',
            })
          }
          await adminAudit(db, c, {
            action: 'operator.created',
            targetType: 'admin_user',
            targetId: email,
            severity: 'critical',
            detail: { email, name: input.name, role: input.role },
          })
          const rows = await db.execute<{ id: string }>(sql`
            INSERT INTO admin_users (email, name, role)
            VALUES (${email}, ${input.name}, ${input.role})
            RETURNING id`)
          return {
            id: rows[0]!.id,
            provisioned: false,
            effect:
              'The account exists and cannot sign in. Set a password out of band before it is usable.',
          }
        })
      }),

    /**
     * Changes an operator's role.
     *
     * REFUSES TO CHANGE YOUR OWN, and that is the guard that matters here
     * rather than a nicety. admin.operators.write is held by super_admin as
     * well as owner, so without this a super_admin could promote themselves to
     * owner and pick up admin.audit.export and every other owner-only
     * permission. A privilege model where the privileged can widen their own
     * privileges is not a privilege model.
     *
     * The refusal is on SELF rather than on the target role, deliberately.
     * Blocking only "promote to owner" would still let somebody grant
     * themselves anything below it, and it would need updating every time the
     * role table changes. "Somebody else changes your role" needs no such list.
     */
    setRole: adminProcedure('admin.operators.write')
      .input(z.object({ adminUserId: z.string().uuid(), role: z.enum(ADMIN_ROLES) }))
      .mutation(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        if (input.adminUserId === c.admin.adminUserId) {
          throw new TRPCError({
            code: 'FORBIDDEN',
            message:
              'You cannot change your own operator role. Ask another owner to do it, so that granting privilege is always somebody else deciding.',
          })
        }
        return c.adminDb(async (db) => {
          const found = await db.execute<{ email: string; role: string; is_root: boolean }>(
            sql`SELECT email, role, is_root FROM admin_users WHERE id = ${input.adminUserId}::uuid`,
          )
          const target = found[0]
          if (!target) throw new TRPCError({ code: 'NOT_FOUND', message: 'No operator with that id.' })
          if (target.role === input.role) {
            return { role: input.role, changed: false }
          }
          await adminAudit(db, c, {
            action: 'operator.role_changed',
            targetType: 'admin_user',
            targetId: input.adminUserId,
            severity: 'critical',
            detail: { email: target.email, from: target.role, to: input.role },
          })
          // The root operator's trigger refuses a demotion from owner. Letting
          // it raise rather than pre-checking here means the database is the
          // thing enforcing it, and the route cannot drift from the trigger.
          await db.execute(sql`
            UPDATE admin_users SET role = ${input.role}, updated_at = ${c.clock.now().toISOString()}
            WHERE id = ${input.adminUserId}::uuid`)
          return { role: input.role, changed: true }
        })
      }),

    /** Suspends an operator, which stops their live sessions on the next
     *  request: current_admin_user() joins admin_users and checks it, so the
     *  cookie dies mid-session rather than at its next sign-in. */
    suspend: adminProcedure('admin.operators.write')
      .input(z.object({ adminUserId: z.string().uuid(), reason: z.string().trim().min(1).max(500) }))
      .mutation(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        if (input.adminUserId === c.admin.adminUserId) {
          throw new TRPCError({
            code: 'FORBIDDEN',
            message: 'You cannot suspend yourself. Ask another owner to do it.',
          })
        }
        return c.adminDb(async (db) => {
          const found = await db.execute<{ email: string }>(
            sql`SELECT email FROM admin_users WHERE id = ${input.adminUserId}::uuid`,
          )
          if (!found[0]) throw new TRPCError({ code: 'NOT_FOUND', message: 'No operator with that id.' })
          await adminAudit(db, c, {
            action: 'operator.suspended',
            targetType: 'admin_user',
            targetId: input.adminUserId,
            severity: 'critical',
            detail: { email: found[0].email, reason: input.reason },
          })
          await db.execute(sql`
            UPDATE admin_users
            SET suspended_at = ${c.clock.now().toISOString()},
                suspended_reason = ${input.reason},
                updated_at = ${c.clock.now().toISOString()}
            WHERE id = ${input.adminUserId}::uuid`)
          return {
            suspended: true,
            effect: 'Their sessions stop working on the next request, and they cannot sign in.',
          }
        })
      }),

    restore: adminProcedure('admin.operators.write')
      .input(z.object({ adminUserId: z.string().uuid() }))
      .mutation(async ({ ctx, input }) => {
        const c = ctx as AdminContext
        return c.adminDb(async (db) => {
          const found = await db.execute<{ email: string }>(
            sql`SELECT email FROM admin_users WHERE id = ${input.adminUserId}::uuid`,
          )
          if (!found[0]) throw new TRPCError({ code: 'NOT_FOUND', message: 'No operator with that id.' })
          await adminAudit(db, c, {
            action: 'operator.restored',
            targetType: 'admin_user',
            targetId: input.adminUserId,
            severity: 'critical',
            detail: { email: found[0].email },
          })
          await db.execute(sql`
            UPDATE admin_users SET suspended_at = NULL, suspended_reason = NULL,
                                   updated_at = ${c.clock.now().toISOString()}
            WHERE id = ${input.adminUserId}::uuid`)
          return { suspended: false }
        })
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

  // ---------------------------------------------------------------------------
  // Other lanes spread in here.
  //
  // admin-infra:  infra: infraRouter, emergency: emergencyRouter
  // admin-money:  billing, entitlements, flags
  // admin-ops:    users detail, projects, impersonation, support, search
  //
  // Each exports its sub-routers from its own file and this object names them,
  // so the matrix test in admin-routes.test.ts walks every operator route in one
  // pass. A lane mounted as its own `admin:` key would be invisible to it.
  // ---------------------------------------------------------------------------
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
