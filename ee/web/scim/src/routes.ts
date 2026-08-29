// The SCIM endpoints.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The caller is a robot with a bearer token that will retry on its own
// schedule until it succeeds or an administrator intervenes, and that shapes
// every response here:
//
//   A refusal is a 4xx with a scimType, never a 500. A 500 makes Okta retry the
//   same broken request forever; a 400 makes it stop and show the person who
//   configured it what is wrong.
//
//   A refusal a provider caused is never silent. An operation this does not
//   understand is an error, because a skipped operation returns 200 and the
//   provider records the change as applied. The first time anybody notices is
//   when a departed employee still has access.
//
//   Nothing here leaks across tenants, and it cannot: the bearer token names
//   one organization and every statement runs inside it.

import type { Pool } from '@antifailure/db'
import type { Clock, Context, Extension, ExtensionRoute } from '@antifailure/api'
import { createHash, timingSafeEqual } from 'node:crypto'
import { FilterRefused, parseFilter, type Filter } from './filter.ts'
import { PatchRefused, asBoolean, asString, normalisePatch, type Change } from './patch.ts'
import {
  CONTENT_TYPE,
  ScimError,
  errorBody,
  etag,
  groupResource,
  listResponse,
  pageFrom,
  resourceTypes,
  serviceProviderConfig,
  userResource,
} from './scim.ts'
import {
  addMember,
  authenticate,
  createGroup,
  createUser,
  deleteGroup,
  deleteUser,
  getGroup,
  getUser,
  inTenant,
  isUuid,
  listGroups,
  listUsers,
  membersOf,
  removeMember,
  replaceGroup,
  sql,
  updateUser,
  type Caller,
} from './store.ts'

export interface ScimOptions {
  pool: Pool
  clock: Clock
  /** Where this control plane is reachable, for the location URLs SCIM
   *  resources carry. */
  baseUrl: string
  /** The role a provisioned member gets when no group maps them to one. */
  defaultRole?: 'admin' | 'member' | 'viewer'
  /** Where an unexpected failure is reported. Defaults to stderr, because a
   *  500 nobody can see is a provisioning integration that stays broken. */
  log?: (line: string) => void
}

// Provisioning clients are bursty by nature: a first sync of a directory sends
// hundreds of creates as fast as it can, and then almost nothing for a day. The
// burst absorbs the sync; the sustained rate is what one directory needs.
const WRITE_LIMIT = {
  rate: 10,
  burst: 200,
  key: 'token' as const,
  reason:
    'A first directory sync sends hundreds of writes at once and then goes quiet for a day. ' +
    'The burst absorbs the sync and the rate is what steady-state provisioning needs. Keyed by ' +
    'token so one customer cannot consume the instance.',
}

const READ_LIMIT = {
  rate: 20,
  burst: 200,
  key: 'token' as const,
  reason:
    'Reconciliation pages through every user and group. Higher than writes because a page is ' +
    'cheap and a provider that cannot finish reconciling never converges.',
}

const CONFIG_LIMIT = {
  rate: 2,
  burst: 20,
  key: 'ip' as const,
  reason: 'Static documents a client fetches once when it connects.',
}

export function scimExtension(input: ScimOptions): Extension {
  const options: ScimOptions = { ...input, log: input.log ?? ((line) => console.error(line)) }
  const routes: ExtensionRoute[] = [
    {
      method: 'GET',
      path: '/scim/v2/ServiceProviderConfig',
      limit: CONFIG_LIMIT,
      handler: (c) => json(c, 200, serviceProviderConfig(options.baseUrl)),
    },
    {
      method: 'GET',
      path: '/scim/v2/ResourceTypes',
      limit: CONFIG_LIMIT,
      handler: (c) => json(c, 200, resourceTypes(options.baseUrl)),
    },

    { method: 'GET', path: '/scim/v2/Users', limit: READ_LIMIT, handler: (c) => guard(c, options, usersList) },
    { method: 'POST', path: '/scim/v2/Users', limit: WRITE_LIMIT, handler: (c) => guard(c, options, usersCreate) },
    { method: 'GET', path: '/scim/v2/Users/:id', limit: READ_LIMIT, handler: (c) => guard(c, options, usersGet) },
    { method: 'PUT', path: '/scim/v2/Users/:id', limit: WRITE_LIMIT, handler: (c) => guard(c, options, usersReplace) },
    { method: 'PATCH', path: '/scim/v2/Users/:id', limit: WRITE_LIMIT, handler: (c) => guard(c, options, usersPatch) },
    { method: 'DELETE', path: '/scim/v2/Users/:id', limit: WRITE_LIMIT, handler: (c) => guard(c, options, usersDelete) },

    { method: 'GET', path: '/scim/v2/Groups', limit: READ_LIMIT, handler: (c) => guard(c, options, groupsList) },
    { method: 'POST', path: '/scim/v2/Groups', limit: WRITE_LIMIT, handler: (c) => guard(c, options, groupsCreate) },
    { method: 'GET', path: '/scim/v2/Groups/:id', limit: READ_LIMIT, handler: (c) => guard(c, options, groupsGet) },
    { method: 'PUT', path: '/scim/v2/Groups/:id', limit: WRITE_LIMIT, handler: (c) => guard(c, options, groupsReplace) },
    { method: 'PATCH', path: '/scim/v2/Groups/:id', limit: WRITE_LIMIT, handler: (c) => guard(c, options, groupsPatch) },
    { method: 'DELETE', path: '/scim/v2/Groups/:id', limit: WRITE_LIMIT, handler: (c) => guard(c, options, groupsDelete) },
  ]
  return { name: 'scim', routes }
}

// ---------------------------------------------------------------------------
// Authentication and error handling, in one place
// ---------------------------------------------------------------------------

type Handler = (c: Context, options: ScimOptions, caller: Caller) => Promise<Response>

/**
 * Authenticates, runs the handler, and turns every refusal into a SCIM error.
 *
 * Every route goes through here, which is what stops one of fourteen handlers
 * being the one that forgot to authenticate. An unexpected error becomes a 500
 * with no detail, because the detail would be a stack trace going to a third
 * party's provisioning log.
 */
async function guard(c: Context, options: ScimOptions, handler: Handler): Promise<Response> {
  // Set on the Response rather than through c.header(). Every response in this
  // file is constructed here rather than by the router, and c.header() only
  // decorates a response the router builds, so setting it that way put the
  // header nowhere. A 401 with no WWW-Authenticate tells a client its request
  // was malformed rather than that it needs to authenticate, which is the
  // difference between a provisioning integration that prompts for a token and
  // one that retries the same anonymous request forever.
  const challenge = { 'www-authenticate': 'Bearer realm="antifailure-scim"' }

  const header = c.req.header('authorization') ?? ''
  if (!header.startsWith('Bearer ')) {
    return json(c, 401, errorBody(401, 'This endpoint needs a bearer token.'), challenge)
  }

  const presented = header.slice(7).trim()
  const caller = await authenticate(
    options.pool,
    createHash('sha256').update(presented, 'utf8').digest(),
    options.clock.now(),
  )
  if (!caller) {
    // One answer for a revoked token, an expired token and a token that never
    // existed. Telling them apart tells somebody probing which of their
    // guesses was once real.
    return json(c, 401, errorBody(401, 'This token is not valid.'), challenge)
  }

  try {
    return await handler(c, options, caller)
  } catch (err) {
    if (err instanceof ScimError) {
      return json(c, err.status, errorBody(err.status, err.message, err.scimType))
    }
    if (err instanceof FilterRefused) {
      return json(c, 400, errorBody(400, err.message, err.scimType))
    }
    if (err instanceof PatchRefused) {
      return json(c, 400, errorBody(400, err.message, err.scimType))
    }
    // Opaque to the client, and never to the operator. An unexpected failure
    // that is invisible on both sides is how a provisioning integration stays
    // broken for weeks: the provider retries on a schedule and nobody here has
    // anything to look at. The message only, not the stack and not the query,
    // because this is a third party's request and the detail is ours.
    options.log?.(
      `scim: ${c.req.method} ${new URL(c.req.url).pathname} failed: ${describe(err)}`,
    )
    return json(c, 500, errorBody(500, 'The control plane could not complete that request.'))
  }
}

/**
 * The failure underneath whatever the query builder wrapped it in.
 *
 * Drizzle reports a database failure as "Failed query: <sql>" and hangs the
 * driver's error off `cause`, so logging only the outer message prints a
 * sentence with no information in it. The chain is walked to the first thing
 * carrying a SQLSTATE, which is the part that says what actually went wrong.
 */
function describe(err: unknown): string {
  const parts: string[] = []
  let cur: unknown = err
  for (let depth = 0; depth < 8 && cur; depth += 1) {
    const e = cur as { code?: string; message?: string; detail?: string; cause?: unknown }
    if (e.message) parts.push(e.code ? `${e.code}: ${e.message}` : e.message)
    if (e.detail) parts.push(e.detail)
    cur = e.cause
  }
  return parts.join(' <- ') || String(err)
}

function json(c: Context, status: number, body: unknown, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': CONTENT_TYPE, ...headers },
  })
}

function query(c: Context): URLSearchParams {
  return new URL(c.req.url).searchParams
}

function filterFrom(c: Context): Filter | null {
  const raw = query(c).get('filter')
  return raw && raw.trim() !== '' ? parseFilter(raw) : null
}

async function bodyOf(c: Context): Promise<Record<string, unknown>> {
  try {
    const parsed = await c.req.json()
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      throw new ScimError(400, 'The body must be a JSON object.', 'invalidSyntax')
    }
    return parsed as Record<string, unknown>
  } catch (err) {
    if (err instanceof ScimError) throw err
    throw new ScimError(400, 'The body is not JSON.', 'invalidSyntax')
  }
}

/**
 * Enforces If-Match, when the client sent one.
 *
 * Compared in constant time out of habit rather than necessity: a version
 * number is not a secret. It costs nothing and it means nobody has to decide,
 * for each comparison in this codebase, whether this particular value is worth
 * protecting.
 */
function checkIfMatch(c: Context, version: number): void {
  const presented = c.req.header('if-match')
  if (!presented) return
  const expected = etag(version)
  const a = Buffer.from(presented.trim(), 'utf8')
  const b = Buffer.from(expected, 'utf8')
  if (a.length !== b.length || !timingSafeEqual(a, b)) {
    throw new ScimError(
      412,
      `This resource is at version ${expected} and the request expected ${presented.trim()}. ` +
        `Fetch it again and retry.`,
    )
  }
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

async function usersList(c: Context, options: ScimOptions, caller: Caller): Promise<Response> {
  const page = pageFrom(query(c))
  const { users, total } = await listUsers(options.pool, caller, {
    filter: filterFrom(c),
    startIndex: page.startIndex,
    count: page.count,
  })
  return json(
    c,
    200,
    listResponse(users.map((u) => userResource(u, options.baseUrl)), total, page.startIndex),
  )
}

async function usersGet(c: Context, options: ScimOptions, caller: Caller): Promise<Response> {
  const user = await getUser(options.pool, caller, c.req.param('id') ?? '')
  if (!user) throw new ScimError(404, 'No such user.')
  return json(c, 200, userResource(user, options.baseUrl), { etag: etag(user.version) })
}

async function usersCreate(c: Context, options: ScimOptions, caller: Caller): Promise<Response> {
  const body = await bodyOf(c)
  const user = await createUser(
    options.pool,
    caller,
    readUserBody(body),
    options.clock.now(),
    options.defaultRole ?? 'member',
  )
  return json(c, 201, userResource(user, options.baseUrl), {
    etag: etag(user.version),
    location: `${options.baseUrl.replace(/\/$/, '')}/scim/v2/Users/${user.id}`,
  })
}

async function usersReplace(c: Context, options: ScimOptions, caller: Caller): Promise<Response> {
  const id = c.req.param('id') ?? ''
  const existing = await getUser(options.pool, caller, id)
  if (!existing) throw new ScimError(404, 'No such user.')
  checkIfMatch(c, existing.version)

  const body = await bodyOf(c)
  // PUT is a replace, so an attribute the client omitted becomes absent rather
  // than keeping its old value. That is the difference between PUT and PATCH
  // and getting it wrong makes a client that clears a field see it come back.
  const input = readUserBody(body)
  const user = await updateUser(
    options.pool,
    caller,
    id,
    {
      userName: input.userName,
      externalId: input.externalId ?? null,
      active: input.active ?? true,
      givenName: input.givenName ?? null,
      familyName: input.familyName ?? null,
      displayName: input.displayName ?? null,
    },
    options.clock.now(),
    options.defaultRole ?? 'member',
  )
  return json(c, 200, userResource(user, options.baseUrl), { etag: etag(user.version) })
}

async function usersPatch(c: Context, options: ScimOptions, caller: Caller): Promise<Response> {
  const id = c.req.param('id') ?? ''
  const existing = await getUser(options.pool, caller, id)
  if (!existing) throw new ScimError(404, 'No such user.')
  checkIfMatch(c, existing.version)

  const changes = normalisePatch(await bodyOf(c))
  const update: Record<string, unknown> = {}

  for (const change of changes) {
    const attribute = change.sub ? `${change.attribute}.${change.sub}` : change.attribute
    switch (attribute) {
      case 'active':
        update.active = change.op === 'remove' ? false : asBoolean(change.value, 'active')
        break
      case 'username':
        update.userName = asString(change.value, 'userName').toLowerCase()
        break
      case 'externalid':
        update.externalId = change.op === 'remove' ? null : asString(change.value, 'externalId')
        break
      case 'displayname':
        update.displayName = change.op === 'remove' ? null : asString(change.value, 'displayName')
        break
      case 'name.givenname':
        update.givenName = change.op === 'remove' ? null : asString(change.value, 'name.givenName')
        break
      case 'name.familyname':
        update.familyName = change.op === 'remove' ? null : asString(change.value, 'name.familyName')
        break
      case 'name':
        // The whole name object at once, which is what a PUT-shaped PATCH sends.
        if (change.value && typeof change.value === 'object') {
          const name = change.value as Record<string, unknown>
          if (typeof name.givenName === 'string') update.givenName = name.givenName
          if (typeof name.familyName === 'string') update.familyName = name.familyName
        }
        break
      case 'emails':
      case 'emails.value':
        update.userName = readEmail(change.value).toLowerCase()
        break
      // Attributes a provider sends that this schema does not keep. Accepted
      // and ignored ON PURPOSE, and listed by name rather than caught by a
      // default branch: a provider that sends title or department should not
      // fail to provision over a field nobody here needs, and the difference
      // between "ignored deliberately" and "not understood" has to be visible
      // in the code.
      case 'title':
      case 'department':
      case 'locale':
      case 'timezone':
      case 'preferredlanguage':
      case 'phonenumbers':
      case 'addresses':
      case 'nickname':
      case 'profileurl':
      case 'usertype':
      case 'roles':
      case 'entitlements':
      case 'password':
        break
      default:
        throw new PatchRefused(
          `This implementation does not understand the attribute "${attribute}". Supported ` +
            `attributes are userName, externalId, active, displayName and name.`,
          'invalidPath',
        )
    }
  }

  const user = await updateUser(
    options.pool,
    caller,
    id,
    update,
    options.clock.now(),
    options.defaultRole ?? 'member',
  )
  return json(c, 200, userResource(user, options.baseUrl), { etag: etag(user.version) })
}

async function usersDelete(c: Context, options: ScimOptions, caller: Caller): Promise<Response> {
  await deleteUser(options.pool, caller, c.req.param('id') ?? '', options.clock.now())
  return new Response(null, { status: 204 })
}

function readUserBody(body: Record<string, unknown>) {
  const userName = body.userName
  if (typeof userName !== 'string' || userName.trim() === '') {
    throw new ScimError(400, 'userName is required.', 'invalidValue')
  }
  const name = (body.name ?? {}) as Record<string, unknown>
  return {
    userName: userName.trim().toLowerCase(),
    externalId: typeof body.externalId === 'string' ? body.externalId : null,
    // Absent means active. A provider that omits the flag on create is
    // creating somebody who works here.
    active: body.active === undefined ? true : asBoolean(body.active, 'active'),
    givenName: typeof name.givenName === 'string' ? name.givenName : null,
    familyName: typeof name.familyName === 'string' ? name.familyName : null,
    displayName: typeof body.displayName === 'string' ? body.displayName : null,
  }
}

/** The primary address out of the emails array, or the only one. */
function readEmail(value: unknown): string {
  if (typeof value === 'string') return value
  if (Array.isArray(value)) {
    const entries = value.filter(
      (v): v is Record<string, unknown> => v !== null && typeof v === 'object',
    )
    const primary = entries.find((e) => e.primary === true) ?? entries[0]
    if (primary && typeof primary.value === 'string') return primary.value
  }
  throw new PatchRefused('emails must be a string or an array of {value}.', 'invalidValue')
}

// ---------------------------------------------------------------------------
// Groups
// ---------------------------------------------------------------------------

async function groupsList(c: Context, options: ScimOptions, caller: Caller): Promise<Response> {
  const page = pageFrom(query(c))
  const { groups, total } = await listGroups(options.pool, caller, {
    filter: filterFrom(c),
    startIndex: page.startIndex,
    count: page.count,
  })
  const resources = await inTenant(options.pool, caller, async (db) =>
    Promise.all(
      groups.map(async (g) => groupResource(g, await membersOf(db, g.id), options.baseUrl)),
    ),
  )
  return json(c, 200, listResponse(resources, total, page.startIndex))
}

async function groupsGet(c: Context, options: ScimOptions, caller: Caller): Promise<Response> {
  const found = await getGroup(options.pool, caller, c.req.param('id') ?? '')
  if (!found) throw new ScimError(404, 'No such group.')
  return json(c, 200, groupResource(found.group, found.members, options.baseUrl), {
    etag: etag(found.group.version),
  })
}

async function groupsCreate(c: Context, options: ScimOptions, caller: Caller): Promise<Response> {
  const body = await bodyOf(c)
  const displayName = body.displayName
  if (typeof displayName !== 'string' || displayName.trim() === '') {
    throw new ScimError(400, 'displayName is required.', 'invalidValue')
  }
  const { group, members } = await createGroup(
    options.pool,
    caller,
    {
      displayName,
      externalId: typeof body.externalId === 'string' ? body.externalId : null,
      members: memberRefs(body.members),
    },
    options.clock.now(),
  )
  return json(c, 201, groupResource(group, members, options.baseUrl), {
    etag: etag(group.version),
    location: `${options.baseUrl.replace(/\/$/, '')}/scim/v2/Groups/${group.id}`,
  })
}

async function groupsReplace(c: Context, options: ScimOptions, caller: Caller): Promise<Response> {
  const id = c.req.param('id') ?? ''
  const existing = await getGroup(options.pool, caller, id)
  if (!existing) throw new ScimError(404, 'No such group.')
  checkIfMatch(c, existing.group.version)

  const body = await bodyOf(c)
  const displayName = body.displayName
  if (typeof displayName !== 'string' || displayName.trim() === '') {
    throw new ScimError(400, 'displayName is required.', 'invalidValue')
  }
  const { group, members } = await replaceGroup(
    options.pool,
    caller,
    id,
    {
      displayName,
      externalId: typeof body.externalId === 'string' ? body.externalId : null,
      members: memberRefs(body.members) ?? [],
    },
    options.clock.now(),
  )
  return json(c, 200, groupResource(group, members, options.baseUrl), { etag: etag(group.version) })
}

async function groupsPatch(c: Context, options: ScimOptions, caller: Caller): Promise<Response> {
  const id = c.req.param('id') ?? ''
  const existing = await getGroup(options.pool, caller, id)
  if (!existing) throw new ScimError(404, 'No such group.')
  checkIfMatch(c, existing.group.version)

  const changes = normalisePatch(await bodyOf(c))
  const now = options.clock.now()

  await inTenant(options.pool, caller, async (db) => {
    for (const change of changes) {
      if (change.attribute === 'members') {
        await applyMembers(db, caller, id, change)
        continue
      }
      if (change.attribute === 'displayname') {
        await db.execute(
          sql`UPDATE scim_groups SET display_name = ${asString(change.value, 'displayName')}
              WHERE id = ${id}`,
        )
        continue
      }
      if (change.attribute === 'externalid') {
        await db.execute(
          sql`UPDATE scim_groups SET external_id = ${
            change.op === 'remove' ? null : asString(change.value, 'externalId')
          } WHERE id = ${id}`,
        )
        continue
      }
      throw new PatchRefused(
        `This implementation does not understand the group attribute "${change.attribute}". ` +
          `Supported attributes are displayName, externalId and members.`,
        'invalidPath',
      )
    }
    await db.execute(
      sql`UPDATE scim_groups SET version = version + 1, updated_at = ${now.toISOString()} WHERE id = ${id}`,
    )
  })

  const updated = await getGroup(options.pool, caller, id)
  if (!updated) throw new ScimError(404, 'No such group.')
  return json(c, 200, groupResource(updated.group, updated.members, options.baseUrl), {
    etag: etag(updated.group.version),
  })
}

/* eslint-disable @typescript-eslint/no-explicit-any */
async function applyMembers(db: any, caller: Caller, groupId: string, change: Change) {
  if (change.op === 'remove') {
    // Entra ID removes one member with a filter inside the path:
    // members[value eq "<id>"]. Without the selector, the operation removes
    // every member, which is what the specification says a pathless remove
    // means and which is a very bad thing to get wrong by accident.
    if (change.selector) {
      await removeMember(db, groupId, change.selector.value)
      return
    }
    if (change.value !== undefined && change.value !== null) {
      for (const ref of memberRefs(change.value) ?? []) await removeMember(db, groupId, ref)
      return
    }
    await db.execute(sql`DELETE FROM scim_group_members WHERE group_id = ${groupId}`)
    return
  }

  const refs = memberRefs(change.value) ?? []
  if (change.op === 'replace') {
    await db.execute(sql`DELETE FROM scim_group_members WHERE group_id = ${groupId}`)
  }
  for (const ref of refs) await addMember(db, caller.orgId, groupId, ref)
}
/* eslint-enable @typescript-eslint/no-explicit-any */

async function groupsDelete(c: Context, options: ScimOptions, caller: Caller): Promise<Response> {
  await deleteGroup(options.pool, caller, c.req.param('id') ?? '', options.clock.now())
  return new Response(null, { status: 204 })
}

/**
 * The member references out of whatever shape arrived.
 *
 * A member may be `{value: "<id>"}`, a bare string, or a single object rather
 * than an array. Each of those is a real provider's output, and an
 * implementation that handles one silently drops the members sent in the
 * others.
 */
function memberRefs(value: unknown): string[] | undefined {
  if (value === undefined || value === null) return undefined
  const list = Array.isArray(value) ? value : [value]
  const refs: string[] = []
  for (const entry of list) {
    if (typeof entry === 'string' && entry !== '') refs.push(entry)
    else if (entry && typeof entry === 'object') {
      const v = (entry as Record<string, unknown>).value
      if (typeof v === 'string' && v !== '') refs.push(v)
    }
  }
  return refs
}

export { isUuid }
