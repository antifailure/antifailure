// The resolver the control plane asks, and the entitlement it asks first.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// THE JOIN THAT WAS MISSING. web/apps/api/src/permissions.ts has asked an
// installed resolver on every request since it was written, and resolverFor in
// roles.ts has always answered in exactly that resolver's shape. The test
// beside roles.ts joined them. Nothing else did: no model was stored anywhere,
// so there was nothing to build a resolver from, and a licence naming rbac
// verified and granted nothing.
//
// PER REQUEST, FROM THE DATABASE, AND NEVER CACHED. orgProcedure re-reads the
// member's built-in role on every request, and says why: a role change has to
// take effect for a request already in flight, and a role carried anywhere else
// takes effect whenever that copy is refreshed. A model cached in this process
// would be exactly that copy, on every replica separately, and a revoked grant
// would go on granting until each one noticed. The read is cheap because of
// where permits asks it: only when the built-in role has already said no, and
// only for the rows that could grant the one permission being asked about.
//
// TWO GATES, ASKED IN THIS ORDER, AND NEITHER IS THE OTHER. The licence key
// this process was started with says whether this INSTALLATION bought custom
// roles, and costs nothing to ask, so it is asked before any read at all. The
// entitlement catalogue says whether THIS ORGANIZATION is entitled, which an
// operator can change with no deploy, so it is asked per request inside the
// same transaction as the model. It is asked last, and only when the model
// would grant: a request the model does not cover gets the same answer either
// way, and asking there would be a read that can change nothing. This is the
// arrangement ee/web/server/src/register.ts already uses for the audit stream.

import type { Clock, PermissionResolver } from '@antifailure/api'
import type { Db, Pool } from '@antifailure/db'
import { declare, licensedIn } from '@antifailure-ee/features'
import { resolverFor } from './roles.ts'
import { modelFor } from './store.ts'

// Declared at module scope, so importing this package is what records the site,
// and naming the function that asks the entitlement rather than the one that
// installs it, for the reason ee/web/scim/src/routes.ts gives: a site that names
// something unable to answer the question is the same lie one level down.
declare('rbac', 'ee/web/rbac/src/enforce.ts:customRoleResolver')

export interface ResolverOptions {
  pool: Pool
  clock: Clock
  /** Whether the licence this installation runs under permits rbac, right now.
   *  Evaluated per call, so a licence that lapses tonight stops custom roles on
   *  the next request rather than at the next restart. */
  installationPermits: () => boolean
  log?: (line: string) => void
}

/**
 * Whether one organization is entitled to custom roles, inside a transaction
 * the caller already holds. One function so the resolver and the routes that
 * define a model cannot be gated by two different questions.
 */
export async function entitled(db: Db, orgId: string, now: Date): Promise<boolean> {
  return licensedIn(db, orgId, 'rbac', now)
}

/**
 * The resolver the enterprise entry point installs.
 *
 * Answers true when a stored grant covers the request and the organization is
 * entitled, and undefined otherwise, never false. Undefined leaves the built-in
 * role to decide, which is what makes withdrawing the entitlement safe: custom
 * grants stop widening anything, and nobody loses what their built-in role
 * gives them.
 *
 * A failure is logged and rethrown, and permits turns a throw into the
 * built-in answer. Logged here because permits deliberately says nothing, and a
 * resolver failing silently on every request would look exactly like a model
 * that grants nothing.
 */
export function customRoleResolver(options: ResolverOptions): PermissionResolver {
  return async (req) => {
    if (!options.installationPermits()) return undefined
    try {
      return await options.pool.withTenant({ orgId: req.orgId, userId: req.userId }, async (db) => {
        const model = await modelFor(db, req.orgId, req.userId, req.permission)
        if (model.grants.length === 0) return undefined
        if (resolverFor(model)(req) !== true) return undefined
        return (await entitled(db, req.orgId, options.clock.now())) ? true : undefined
      })
    } catch (err) {
      options.log?.(
        `custom roles: the model for ${req.orgId} could not be read, so the built-in role ` +
          `decided ${req.permission}: ${err instanceof Error ? err.message : String(err)}`,
      )
      throw err
    }
  }
}
