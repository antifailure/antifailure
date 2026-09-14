// The routes that let an organization have a role model at all.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Four routes, and the shape is the one policyfile.ts was written for: the
// model is a file, exported, reviewed as a pull request, dry run, then applied
// whole. There is no route that adds one role or one grant, on purpose. A
// permission model edited one click at a time is a model nobody reviews, and
// the dry run is only worth anything if it describes the entire change.
//
//   GET  /roles/policy                          the current model, as YAML
//   POST /roles/policy/dry-run                  what applying a file would do
//   PUT  /roles/policy                          apply a file, whole
//   GET  /roles/members/:userId/permissions     what one person can do, and why
//
// WHO. The console's own session, the cookie and the CSRF header every tRPC
// mutation already requires. Reading or writing the model needs members.manage
// in the BUILT-IN role, asked of the built-in table and never of the resolver:
// a custom role must not be the key to the model that defines custom roles, or
// one grant of members.manage would let its holder rewrite every other grant.
// A person may always read their own effective permissions.
//
// WHICH ORGANIZATION. The session's, and nothing in a request can name another.
// Every statement runs inside that organization's tenant transaction.
//
// ENTITLED. The licence key is asked by the gate this extension is wrapped in,
// which answers 402. The organization's own entitlement is asked here, per
// request, and answers 403 with the sentence every entitlement refusal uses.

import type { Clock, Context, Extension, ExtensionRoute, Permission } from '@antifailure/api'
import {
  CSRF_HEADER,
  ROLE_PERMISSIONS,
  SESSION_COOKIE,
  csrfMatches,
  readCookie,
  resolveSession,
  roleHas,
  type ResolvedSession,
  type Role,
} from '@antifailure/api'
import { appendAudit, sql, type Pool } from '@antifailure/db'
import { refusal } from '@antifailure-ee/features'
import { approvalsRefusal, escalations } from './authoring.ts'
import { entitled } from './enforce.ts'
import { PolicyFileError, dryRun, fromYAML, render, toYAML, type PolicyFile } from './policyfile.ts'
import { ModelError, effectivePermissions, type Model } from './roles.ts'
import { lockModel, readModel, writeModel } from './store.ts'

export interface RbacOptions {
  pool: Pool
  clock: Clock
  log?: (line: string) => void
}

// Keyed on the address because the limiter resolves no organization for a plain
// HTTP route. A person reviewing a model reads it a few times and applies it
// once; these numbers are far above that and far below a loop.
const READ_LIMIT = {
  rate: 2,
  burst: 20,
  key: 'ip' as const,
  reason:
    'Reading the role model or one member\'s effective permissions is a person at a screen ' +
    'during an access review. Two a second is far above that, and each read is a handful of ' +
    'indexed rows in one tenant.',
}
const WRITE_LIMIT = {
  rate: 0.5,
  burst: 10,
  key: 'ip' as const,
  reason:
    'A dry run and an apply follow a reviewed pull request, a few times a day at most. The ' +
    'apply replaces the whole model inside one transaction under a lock, so a loop here would ' +
    'serialize every other writer in the organization behind it.',
}

/** The largest file accepted, in bytes. A model for an organization of
 *  thousands is tens of kilobytes; a megabyte is a mistake or an attack. */
const MAX_POLICY_BYTES = 1_000_000

export function rbacExtension(options: RbacOptions): Extension {
  const routes: ExtensionRoute[] = [
    { method: 'GET', path: '/roles/policy', limit: READ_LIMIT, handler: (c) => handle(c, options, exportPolicy) },
    { method: 'POST', path: '/roles/policy/dry-run', limit: WRITE_LIMIT, handler: (c) => handle(c, options, previewPolicy) },
    { method: 'PUT', path: '/roles/policy', limit: WRITE_LIMIT, handler: (c) => handle(c, options, applyPolicy) },
    {
      method: 'GET',
      path: '/roles/members/:userId/permissions',
      limit: READ_LIMIT,
      handler: (c) => handle(c, options, memberPermissions),
    },
  ]
  return { name: 'rbac', routes }
}

interface Caller {
  session: ResolvedSession & { orgId: string; role: Role }
  token: string
}

type Handler = (c: Context, options: RbacOptions, caller: Caller) => Promise<Response>

/**
 * Authenticates, confines to the session's organization, and asks the
 * entitlement, for every route, so a route added later cannot be the one that
 * forgot any of the three.
 */
async function handle(c: Context, options: RbacOptions, handler: Handler): Promise<Response> {
  const token = readCookie(c.req.header('cookie'), SESSION_COOKIE)
  const session = token ? await resolveSession(options.pool, options.clock, token) : null
  if (!token || !session) {
    return c.json({ error: 'unauthenticated', detail: 'Sign in to do this.' }, 401)
  }
  if (!session.orgId || !session.role) {
    return c.json(
      { error: 'no_organization', detail: 'Your session is not in an organization, so it has no role model.' },
      403,
    )
  }
  if (c.req.method !== 'GET' && !csrfMatches(token, c.req.header(CSRF_HEADER))) {
    return c.json(
      { error: 'csrf', detail: `This request needs the ${CSRF_HEADER} header your session was issued with.` },
      403,
    )
  }
  const caller: Caller = { session: session as Caller['session'], token }

  const isEntitled = await options.pool.withTenant({ orgId: session.orgId }, (db) =>
    entitled(db, session.orgId!, options.clock.now()),
  )
  if (!isEntitled) {
    return c.json({ error: 'not_entitled', feature: 'rbac', detail: refusal('rbac') }, 403)
  }

  try {
    return await handler(c, options, caller)
  } catch (err) {
    if (err instanceof PolicyFileError || err instanceof ModelError) {
      return c.json({ error: 'invalid_policy', detail: err.message }, 400)
    }
    options.log?.(
      `rbac: ${c.req.method} ${new URL(c.req.url).pathname} failed: ` +
        `${err instanceof Error ? err.message : String(err)}`,
    )
    return c.json({ error: 'internal', detail: 'The control plane could not complete that request.' }, 500)
  }
}

function forbidden(c: Context, permission: Permission): Response {
  return c.json(
    {
      error: 'forbidden',
      permission,
      detail:
        `This needs the ${permission} permission in your built-in role. A custom role cannot ` +
        'grant it here, because a custom role must not be the key to the model that defines them.',
    },
    403,
  )
}

function asFile(model: Model): PolicyFile {
  return { version: 1, roles: model.roles, groups: model.groups, grants: model.grants, approvals: [] }
}

async function exportPolicy(c: Context, options: RbacOptions, caller: Caller): Promise<Response> {
  if (!roleHas(caller.session.role, 'members.manage')) return forbidden(c, 'members.manage')
  const { orgId } = caller.session
  const model = await options.pool.withTenant({ orgId }, (db) => readModel(db, orgId))
  return c.body(toYAML(asFile(model)), 200, { 'content-type': 'application/yaml; charset=utf-8' })
}

async function readBody(c: Context): Promise<PolicyFile> {
  const text = await c.req.text()
  if (Buffer.byteLength(text, 'utf8') > MAX_POLICY_BYTES) {
    throw new PolicyFileError(`the file is larger than ${MAX_POLICY_BYTES} bytes`)
  }
  return fromYAML(text)
}

/** Everything that would stop a file being applied, in one list. */
function refusalsFor(current: Model, file: PolicyFile, role: Role): string[] {
  const next: Model = { roles: file.roles, grants: file.grants, groups: file.groups }
  return [
    ...approvalsRefusal(file),
    ...escalations(current, next, ROLE_PERMISSIONS[role]),
    ...dryRun(asFile(current), file).refusals,
  ]
}

async function previewPolicy(c: Context, options: RbacOptions, caller: Caller): Promise<Response> {
  if (!roleHas(caller.session.role, 'members.manage')) return forbidden(c, 'members.manage')
  const file = await readBody(c)
  const { orgId } = caller.session
  const current = await options.pool.withTenant({ orgId }, (db) => readModel(db, orgId))
  const diff = dryRun(asFile(current), file)
  const refusals = refusalsFor(current, file, caller.session.role)
  return c.json({
    changes: diff.changes,
    refusals,
    summary: render({ changes: diff.changes, refusals }),
  })
}

async function applyPolicy(c: Context, options: RbacOptions, caller: Caller): Promise<Response> {
  if (!roleHas(caller.session.role, 'members.manage')) return forbidden(c, 'members.manage')
  const file = await readBody(c)
  const { orgId, userId, label } = caller.session

  const outcome = await options.pool.withTenant({ orgId, userId }, async (db) => {
    // The lock first and the read second, so the diff this is judged by and the
    // model it replaces are the same model.
    await lockModel(db, orgId)
    const current = await readModel(db, orgId)
    const refusals = refusalsFor(current, file, caller.session.role)
    const diff = dryRun(asFile(current), file)
    if (refusals.length > 0) return { applied: false as const, refusals, changes: diff.changes }

    await writeModel(db, orgId, { roles: file.roles, grants: file.grants, groups: file.groups })
    // In the same transaction, so a model that was replaced has an entry and an
    // entry always describes a model that was replaced.
    await appendAudit(db, {
      orgId,
      actorUserId: userId,
      actorLabel: label,
      action: 'roles.policy_applied',
      targetType: 'organization',
      targetId: orgId,
      origin: 'web',
      detail: {
        added: diff.changes.filter((ch) => ch.kind === 'added').length,
        changed: diff.changes.filter((ch) => ch.kind === 'changed').length,
        removed: diff.changes.filter((ch) => ch.kind === 'removed').length,
        roles: file.roles.length,
        grants: file.grants.length,
      },
      occurredAt: options.clock.now(),
    })
    return { applied: true as const, refusals, changes: diff.changes }
  })

  const summary = render({ changes: outcome.changes, refusals: outcome.refusals })
  if (!outcome.applied) {
    return c.json({ applied: false, refusals: outcome.refusals, changes: outcome.changes, summary }, 409)
  }
  return c.json({ applied: true, changes: outcome.changes, summary })
}

async function memberPermissions(c: Context, options: RbacOptions, caller: Caller): Promise<Response> {
  const subject = c.req.param('userId') ?? ''
  const self = subject === caller.session.userId
  if (!self && !roleHas(caller.session.role, 'members.manage')) return forbidden(c, 'members.manage')
  const { orgId } = caller.session

  const answer = await options.pool.withTenant({ orgId }, async (db) => {
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(subject)) return null
    const rows = await db.execute<{ role: Role }>(
      sql`SELECT role FROM members WHERE org_id = ${orgId} AND user_id = ${subject}`,
    )
    const builtin = rows[0]?.role
    if (!builtin) return null
    const model = await readModel(db, orgId)
    return {
      userId: subject,
      role: builtin,
      permissions: effectivePermissions(model, subject, builtin, ROLE_PERMISSIONS[builtin]),
    }
  })
  if (!answer) {
    return c.json({ error: 'not_found', detail: 'Nobody with that id is a member of this organization.' }, 404)
  }
  return c.json(answer)
}
