// The tRPC layer, and the reason a route cannot exist without a permission.
//
// The enforcement is a middleware, which is ordinary. What is not ordinary is
// that the only exported way to build an authenticated procedure requires a
// permission as an argument. There is no `authedProcedure` to reach for and
// forget to guard: `orgProcedure(permission)` is the whole surface, so
// declaring the permission and creating the route are the same act.
//
// That closes the gap the matrix test would otherwise only be able to notice
// after the fact, and the matrix test still checks it, because a rule enforced
// in two places is a rule that survives one of them being refactored away.

import { initTRPC, TRPCError } from '@trpc/server'
import type { Pool, Tenant } from '@antifailure/db'
import { appendAudit, type AuditInput } from '@antifailure/db'
import type { Permission, Role } from './permissions.ts'
import { permits } from './permissions.ts'
import type { Clock } from './clock.ts'
import type { GitHubClient } from './auth/github.ts'
import type { Billing } from './billing/index.ts'

/** Who is making the request, once the session cookie has been resolved. */
export interface Actor {
  userId: string
  label: string
  orgId: string
  role: Role
}

export interface Context {
  pool: Pool
  clock: Clock
  /**
   * The GitHub client, for the routes that have to ask GitHub something rather
   * than read it back from a table. Required rather than optional so that a new
   * construction site cannot forget it and leave a route that compiles, ships,
   * and then throws the first time somebody presses the button.
   */
  github: GitHubClient
  /**
   * Stripe, when this installation takes money. Null is a supported state and
   * not an oversight: a self-hosted control plane charges nobody, and the
   * routes that need it answer PRECONDITION_FAILED naming the variables rather
   * than the process refusing to start over a feature nobody wants.
   */
  stripe: Billing | null
  /** Null for an unauthenticated request. */
  actor: Actor | null
  /** Where the request came from, recorded on every audit entry. */
  origin: 'web' | 'api' | 'engine' | 'github' | 'system'
  ip?: string
  userAgent?: string
}

/** What a procedure declares about itself. Read by the matrix test and by the
 *  OpenAPI generator without a request having to be made. */
export interface Meta {
  permission?: Permission
}

/** What the client is told when the control plane broke rather than refused.
 *
 * Every other tRPC code carries a message somebody wrote for the person
 * reading it: BAD_REQUEST says which field, FORBIDDEN says which role,
 * PRECONDITION_FAILED names the variable that is not set. Those are answers
 * and they travel. INTERNAL_SERVER_ERROR is the one code whose message nobody
 * wrote, so whatever threw decides what the browser prints.
 */
const BROKE_RATHER_THAN_REFUSED =
  'Something went wrong on the control plane. Nothing was changed, and the reason is in its logs.'

const t = initTRPC.context<Context>().meta<Meta>().create({
  errorFormatter({ shape, error }) {
    // Both halves of the same rule, and shipping one without the other is how
    // this got out. The stack was already withheld because it names internal
    // paths and table names to anyone who can provoke an error. The message
    // beside it was not, and drizzle writes a query failure as "Failed query:
    // <the whole statement>" with the bound parameters after it, so a renamed
    // table put the schema, the join, the WHERE clause and the source
    // comments inside it onto the console's error card for any signed-in
    // viewer to read. Withholding the stack and sending that is no control at
    // all.
    //
    // Only this code is replaced. Every other one carries a message written
    // for the reader, and blanking those would turn "your role cannot see
    // this" into a shrug.
    const broke = shape.data.code === 'INTERNAL_SERVER_ERROR'
    return {
      ...shape,
      message: broke ? BROKE_RATHER_THAN_REFUSED : shape.message,
      data: {
        ...shape.data,
        stack: undefined,
      },
    }
  },
})

export const router = t.router
export const middleware = t.middleware

/** A route that does not require a session: sign-in, health, the OpenAPI doc. */
export const publicProcedure = t.procedure

/**
 * The permission each route declares, read out of the router itself.
 *
 * An earlier version of this recorded the permission when the middleware ran,
 * which meant the map was empty until every route had been called once and the
 * test that checks for unguarded routes could not tell "no permission" from
 * "not called yet". Declaring it as metadata puts it on the procedure at
 * construction, so this is complete the moment the module is loaded.
 */
export function declaredPermissions(): Map<string, Permission> {
  const out = new Map<string, Permission>()
  const procedures = (routerRef?._def.procedures ?? {}) as Record<
    string,
    { _def: { meta?: Meta } }
  >
  for (const [path, procedure] of Object.entries(procedures)) {
    const permission = procedure._def.meta?.permission
    if (permission) out.set(path, permission)
  }
  return out
}

// Set once, by the router module, to avoid an import cycle: the router imports
// the procedure builders from here, so this cannot import the router.
let routerRef: { _def: { procedures: unknown } } | null = null
export function registerRouter(r: { _def: { procedures: unknown } }): void {
  routerRef = r
}

export interface OrgContext extends Context {
  actor: Actor
  /** Runs a callback inside a transaction scoped to the actor's organization. */
  tenant: Tenant
}

const requireActor = t.middleware(({ ctx, next }) => {
  if (!ctx.actor) {
    throw new TRPCError({ code: 'UNAUTHORIZED', message: 'Sign in to do this.' })
  }
  return next({
    ctx: { ...ctx, actor: ctx.actor, tenant: { orgId: ctx.actor.orgId, userId: ctx.actor.userId } },
  })
})

/**
 * Builds a procedure that requires a session and one permission.
 *
 * The role is re-read from the database on every request rather than trusted
 * from the session. A role change has to take effect for a request that is
 * already in flight when somebody is removed, and a role carried in a cookie
 * takes effect whenever the user next signs in, which may be never.
 */
export function orgProcedure(permission: Permission) {
  return publicProcedure.meta({ permission }).use(requireActor).use(
    t.middleware(async ({ ctx, next, input }) => {
      const octx = ctx as OrgContext
      // permits, not roleHas. The built-in table decides unless a resolver has
      // been installed, and the community edition installs none, so this is the
      // same answer with a socket in it.
      const allowed = permits({
        orgId: octx.actor.orgId,
        userId: octx.actor.userId,
        role: octx.actor.role,
        permission,
        repository: repositoryOf(input),
        envId: envOf(input),
      })
      if (!allowed) {
        throw new TRPCError({
          code: 'FORBIDDEN',
          // Naming the permission rather than the role, because the useful
          // next step is "ask for this permission", and because listing which
          // roles have it tells a caller about the organization's structure.
          message: `This needs the ${permission} permission, which your role does not have.`,
        })
      }
      return next({ ctx: octx })
    }),
  )
}

/**
 * Records an action in the audit log.
 *
 * Takes the transaction rather than the pool so that the entry and the change
 * it describes commit together. An audit entry written in its own transaction
 * is an entry that survives a rolled-back change, and a log that records
 * things that did not happen is as useless as one that misses things that did.
 */
export async function audit(
  db: Parameters<typeof appendAudit>[0],
  ctx: OrgContext,
  entry: Omit<AuditInput, 'orgId' | 'actorUserId' | 'actorLabel' | 'origin'>,
): Promise<void> {
  await appendAudit(db, {
    ...entry,
    orgId: ctx.actor.orgId,
    actorUserId: ctx.actor.userId,
    actorLabel: ctx.actor.label,
    origin: ctx.origin,
    occurredAt: entry.occurredAt ?? ctx.clock.now(),
  })
}


// The scope a request concerns, read off its input.
//
// Read here rather than declared per route, because every route that concerns a
// repository already takes it under the same name, and a second declaration is
// a second thing to keep in step. A route that concerns no repository passes
// null, and a resolver scoped to repositories then has no opinion about it,
// which is the right answer rather than a refusal.
function repositoryOf(input: unknown): string | null {
  const i = input as { repository?: unknown } | null
  return typeof i?.repository === 'string' ? i.repository : null
}

function envOf(input: unknown): string | null {
  const i = input as { envId?: unknown } | null
  return typeof i?.envId === 'string' ? i.envId : null
}
