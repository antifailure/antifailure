// The operator's procedure builder, and the refusal at the top of it.
//
// This is `orgProcedure` for the platform, and it copies that file's one good
// idea deliberately: the only exported way to build an operator route takes a
// permission as an argument, so declaring the permission and creating the route
// are the same act. There is no `adminAuthedProcedure` to reach for and forget
// to guard. The way access control actually breaks is not a wrong grant, it is
// a new endpoint nobody remembered to gate.
//
// WHY THE IMPERSONATION REFUSAL COMES FIRST, before the permission check and
// before anything reads the database. Starting an impersonation is ITSELF an
// admin route. So an operator who is already impersonating a customer, calling
// any admin procedure, is a request whose identity has two answers, and every
// one of the interesting failures lives in that gap:
//
//   the audit entry names the operator while the session acts as the customer,
//   an impersonated session reaches an operator route the customer cannot,
//   and impersonation chains, because the route that starts one is gated the
//   same way as every other.
//
// Refusing impersonating callers at the top of EVERY admin procedure closes all
// three at once, with one check that cannot be skipped per route. It is why the
// order in this file is load bearing rather than tidy: a permission check that
// ran first would answer FORBIDDEN for the wrong reason on a route the operator
// does hold, and would answer nothing at all on a route they do not, which is
// the case that leaks.
//
// The rule this expresses: impersonation is a one way door. Step through it and
// you are the customer until you step back, and the operator portal is closed
// to you for as long as that is true.
//
// WHICH POOL THIS USES, and why it is not ctx.pool. Every read an operator
// route makes goes through ctx.adminDb, which is the pool built by
// createAdminPool: a separate connection with a separate credential whose role
// holds BYPASSRLS. A cross tenant read has to be a credential the application
// cannot acquire rather than a claim it makes about itself, and a flag on the
// product's own pool is a claim. See admin-pool.ts for the measurements.

import { TRPCError } from '@trpc/server'
import type { Db, AdminPool } from '@antifailure/db'
import { appendAdminAudit } from '@antifailure/db'
import { publicProcedure, middleware, type Context } from '../trpc.ts'
import { adminRoleHas, type AdminPermission, type AdminRole } from './permissions.ts'
import type { ResolvedAdminSession } from './session.ts'

/** Who is making the request, once the operator session cookie is resolved. */
export interface AdminActor {
  /** The admin_users row. A different id space from users(id): see the foreign
   *  key note on the double write in appendAdminAudit. */
  adminUserId: string
  /** The operator's email, which is what an audit entry should name a year
   *  from now when the row may be gone. */
  label: string
  role: AdminRole
  sessionId: string
  /**
   * True when this session is currently acting as a customer.
   *
   * Read from the session row rather than from a side table, and that is not an
   * implementation detail: a marker the application cannot read is a session
   * that looks ordinary to every check in the product, which is precisely the
   * fail open this flag exists to prevent.
   */
  impersonating: boolean
}

/** Narrows a resolved cookie to the actor the gate carries. Written here rather
 *  than in session.ts so the resolver stays usable by sign-out and by the
 *  shell's "who am I", neither of which holds a permission. */
export function actorOf(session: ResolvedAdminSession): AdminActor {
  return {
    adminUserId: session.adminUserId,
    label: session.email,
    role: session.role,
    sessionId: session.sessionId,
    impersonating: session.impersonating,
  }
}

export interface AdminContext extends Context {
  admin: AdminActor
  /**
   * Runs a callback on the OPERATOR pool, which is a different connection with
   * a different credential from `ctx.pool`.
   *
   * Named `adminDb` rather than `adminPool` at the call site because a route
   * should not be choosing a scope: there is exactly one operator scope and
   * this is it, already bound to the operator making the request.
   */
  adminDb<T>(fn: (db: Db) => Promise<T>): Promise<T>
}

/** The message an impersonating operator gets, which says how to get out. */
const IMPERSONATION_MESSAGE =
  'This session is impersonating a customer, so the operator portal is closed to it. End the impersonation and try again.'

const requireAdminActor = middleware(({ ctx, next }) => {
  const c = ctx as Context
  if (!c.admin) {
    throw new TRPCError({ code: 'UNAUTHORIZED', message: 'Sign in to the operator portal.' })
  }
  if (!c.adminPool) {
    // A deployment with no operator credential configured. Said out loud and
    // named, rather than answering an empty list that reads like a platform
    // with no customers on it, which is exactly what a missing BYPASSRLS role
    // produces and is the failure admin-pool.ts exists to make loud.
    throw new TRPCError({
      code: 'PRECONDITION_FAILED',
      message:
        'This installation has no operator database credential configured, so the operator portal cannot read anything. Set AF_ADMIN_DATABASE_URL.',
    })
  }
  const actor = c.admin
  const pool: AdminPool = c.adminPool
  return next({
    ctx: {
      ...c,
      admin: actor,
      adminDb: <T,>(fn: (db: Db) => Promise<T>) =>
        pool.withOperator({ adminUserId: actor.adminUserId, label: actor.label }, fn),
    },
  })
})

/**
 * Builds an operator procedure that requires a session and one permission.
 *
 * The role is re-read from the database on every request rather than trusted
 * from the session, the same as orgProcedure and for the same reason: removing
 * somebody's operator access has to take effect for a request already in
 * flight, and a role carried in a cookie takes effect whenever they next sign
 * in, which may be never.
 */
export function adminProcedure(permission: AdminPermission) {
  return publicProcedure
    .meta({ adminPermission: permission })
    .use(requireAdminActor)
    .use(
      middleware(async ({ ctx, next, path, type }) => {
        const actx = ctx as unknown as AdminContext

        // FIRST. Before the permission check, before any read. See the header:
        // an impersonating session has two answers to "who is this", and every
        // check below this line would be answering for the wrong one.
        if (actx.admin.impersonating) {
          // Recorded at high severity because it is the shape of both an
          // operator mistake and an attempt to chain impersonations, and the
          // two are indistinguishable from here. The entry is what makes them
          // distinguishable afterwards.
          await recordRefusal(actx, path, 'impersonating', 'high')
          throw new TRPCError({ code: 'FORBIDDEN', message: IMPERSONATION_MESSAGE })
        }

        if (!adminRoleHas(actx.admin.role, permission)) {
          await recordRefusal(actx, path, `missing ${permission}`, 'notice')
          throw new TRPCError({
            code: 'FORBIDDEN',
            // Naming the permission rather than the roles that hold it, the
            // same choice orgProcedure makes: the useful next step is "ask for
            // this", and listing holders describes the company's structure to
            // somebody who just failed a check.
            message: `This needs the ${permission} permission, which your operator role does not have.`,
          })
        }

        const result = await next({ ctx: actx })

        // AUTOMATIC PER REQUEST READ AUDITING.
        //
        // Here rather than in each route, because per row auditing is the rule
        // everybody agrees with and nobody applies: it is a line to add to
        // every list, every search and every detail page, and the one that gets
        // forgotten is the one that mattered. One entry per request is coarser
        // and it is COMPLETE, which is the property an investigation needs.
        //
        // Only queries. A mutation writes its own entry inside its own
        // transaction, naming what it changed, and auditing it again here would
        // record the same act twice with the second copy outside the
        // transaction that decided whether it happened.
        if (type === 'query') {
          await auditRead(actx, path)
        }
        return result
      }),
    )
}

/**
 * One entry saying an operator looked at something.
 *
 * Written on the operator pool in its own transaction, which is the one place
 * this file departs from "the entry and the thing it describes commit
 * together". A read has no transaction of its own to join: it already happened
 * by the time this runs. What is preserved instead is that the operator does
 * not get the ANSWER unless the record is written, because this is awaited
 * before the result is returned and a failure here fails the request.
 *
 * tenantCopy is off. It is the one caller that passes false, and the reason is
 * in that field's own comment: a per request entry that names no specific row
 * would fill a customer's log with "an operator loaded a page" and bury the
 * entries that say something was changed.
 */
async function auditRead(ctx: AdminContext, path: string): Promise<void> {
  await ctx.adminDb((db) =>
    appendAdminAudit(db, {
      adminUserId: ctx.admin.adminUserId,
      actorLabel: ctx.admin.label,
      action: `read.${path}`,
      targetType: 'route',
      targetId: path,
      origin: 'admin',
      ip: ctx.ip ?? null,
      severity: 'info',
      detail: { role: ctx.admin.role },
      tenantCopy: false,
      occurredAt: ctx.clock.now(),
    }),
  )
}

/**
 * One entry saying an operator was refused.
 *
 * A refusal is the line most worth having and the line most often missing,
 * because the code path that refuses is the one that returns early. Both
 * refusals in adminProcedure come through here.
 */
async function recordRefusal(
  ctx: AdminContext,
  path: string,
  reason: string,
  severity: 'notice' | 'high',
): Promise<void> {
  await ctx.adminDb((db) =>
    appendAdminAudit(db, {
      adminUserId: ctx.admin.adminUserId,
      actorLabel: ctx.admin.label,
      action: `refused.${path}`,
      targetType: 'route',
      targetId: path,
      origin: 'admin',
      ip: ctx.ip ?? null,
      severity,
      detail: { reason, role: ctx.admin.role },
      tenantCopy: false,
      occurredAt: ctx.clock.now(),
    }),
  )
}

/**
 * Records an operator action, for the routes that CHANGE something.
 *
 * Takes the transaction rather than the pool so the entry and the change commit
 * together, which is the rule `audit` in ../trpc.ts states and the same reason:
 * an entry written in its own transaction survives a rolled back change, and a
 * log that records things that did not happen is as useless as one that misses
 * things that did.
 *
 * The customer's copy is not this function's decision and not the caller's. It
 * happens inside appendAdminAudit whenever subjectOrgId is set.
 */
export async function adminAudit(
  db: Db,
  ctx: AdminContext,
  entry: {
    action: string
    targetType: string
    targetId?: string | null
    subjectOrgId?: string | null
    subjectOrgLabel?: string | null
    severity: 'info' | 'notice' | 'high' | 'critical'
    detail?: Record<string, unknown>
  },
): Promise<void> {
  await appendAdminAudit(db, {
    ...entry,
    adminUserId: ctx.admin.adminUserId,
    actorLabel: ctx.admin.label,
    origin: 'admin',
    ip: ctx.ip ?? null,
    occurredAt: ctx.clock.now(),
  })
}
