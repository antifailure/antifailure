// Every statement SCIM runs.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// One unscoped read: the bearer token lookup, which determines the tenant and
// therefore cannot be scoped by it. It declares the hash it is presenting and
// the policy in migration 0012 returns that row alone, exactly as engine tokens
// have done since migration 0004. Everything else here runs inside the
// organization that lookup named.
//
// Two decisions in this file are security decisions rather than modelling ones
// and both are worth arguing for.
//
// DEACTIVATION REMOVES THE MEMBERSHIP. `active: false` from a directory means
// this person no longer works here, and the only honest implementation of that
// is that the row granting them access stops existing. The alternative, a flag
// somewhere that every read path must remember to check, is the shape of bug
// this repository has shipped repeatedly: the flag is set, the button says
// deactivated, and one query that forgot the check still returns their data.
// Their sessions are revoked in the same transaction, so the change takes
// effect on their next request rather than at the end of whatever session they
// are holding. The cost is that a role set by hand here is not remembered
// across a deactivate and reactivate cycle, which is a real annoyance and the
// right trade against a departed employee keeping access.
//
// A GROUP MEMBERSHIP IS KEPT WHEN THE USER DOES NOT EXIST YET. Okta and Entra
// ID both send group membership naming users they have not created, and an
// implementation that resolves the reference at write time either drops the
// member silently or rejects the request. Both leave the group permanently
// missing somebody. The reference is stored as it arrived and resolved when the
// user turns up, which is what makes the "membership before user" ordering work
// rather than being a known limitation.

import { randomUUID } from 'node:crypto'
import { sql, type Db, type Pool } from '@antifailure/db'
import { appendAudit } from '@antifailure/db'
import type { Filter } from './filter.ts'
import { FilterRefused } from './filter.ts'
import { ScimError, type GroupMemberRecord, type GroupRecord, type UserRecord } from './scim.ts'

export interface Caller {
  orgId: string
  tokenId: string
}

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

/**
 * The organization a bearer token belongs to.
 *
 * Unscoped, because deciding which organization is what it does. The caller
 * declares the hash, the policy returns that row, and a hash the caller
 * invented returns nothing.
 */
export async function authenticate(
  pool: Pool,
  tokenHash: Buffer,
  now: Date,
): Promise<Caller | null> {
  const rows = await pool.withoutTenant(
    async (db) =>
      db.execute<{ id: string; org_id: string; revoked_at: Date | null; expires_at: Date | null }>(
        sql`SELECT id, org_id, revoked_at, expires_at FROM scim_tokens`,
      ),
    { scimTokenHash: tokenHash },
  )
  const row = rows[0]
  if (!row) return null
  if (row.revoked_at) return null
  if (row.expires_at && new Date(row.expires_at).getTime() <= now.getTime()) return null

  // Written on the request that used it, which is what makes a token nobody
  // has used visible in the dashboard. Best effort: a failure here must not
  // fail the provisioning request it is decorating.
  await pool
    .withoutTenant(
      async (db) => {
        await db.execute(
          sql`UPDATE scim_tokens SET last_used_at = ${now.toISOString()} WHERE id = ${row.id}`,
        )
      },
      { scimTokenHash: tokenHash },
    )
    .catch(() => {})

  return { orgId: row.org_id, tokenId: row.id }
}

// ---------------------------------------------------------------------------
// Filters
// ---------------------------------------------------------------------------

/**
 * The attributes a filter may name, per resource type.
 *
 * A closed set, and this is the whole of the injection defence: the parser
 * produced an abstract syntax tree, and this maps a known attribute name to a
 * known column. An attribute not in this map is refused. No string from the
 * request reaches the query as anything but a bound parameter.
 */
const USER_COLUMNS: Record<string, string> = {
  id: 'id',
  externalid: 'external_id',
  username: 'user_name',
  active: 'active',
  displayname: 'display_name',
  'emails.value': 'user_name',
  emails: 'user_name',
  'name.givenname': 'given_name',
  'name.familyname': 'family_name',
}

const GROUP_COLUMNS: Record<string, string> = {
  id: 'id',
  externalid: 'external_id',
  displayname: 'display_name',
}

/* eslint-disable @typescript-eslint/no-explicit-any */
type Sql = ReturnType<typeof sql>

function condition(filter: Filter, columns: Record<string, string>): Sql {
  switch (filter.kind) {
    case 'and':
      return sql`(${condition(filter.left, columns)} AND ${condition(filter.right, columns)})`
    case 'or':
      return sql`(${condition(filter.left, columns)} OR ${condition(filter.right, columns)})`
    case 'not':
      // NOT over a NULL is NULL, which is not true, so a row where the
      // attribute is absent does not come back from a negation. That is the
      // SCIM meaning too, and writing it out avoids the classic surprise where
      // "not (externalId eq x)" silently omits everyone without an externalId.
      return sql`(NOT COALESCE(${condition(filter.inner, columns)}, false))`
    case 'present': {
      const column = columnFor(filter.attribute, columns)
      return sql`${sql.raw(column)} IS NOT NULL`
    }
    case 'compare': {
      const column = columnFor(filter.attribute, columns)
      const value = filter.value
      // Compared as text for the string operators, so that a filter on a
      // boolean or a uuid column does not fail on a type mismatch.
      const asText = sql`${sql.raw(column)}::text`
      switch (filter.operator) {
        case 'eq':
          return value === null ? sql`${sql.raw(column)} IS NULL` : sql`${asText} = ${String(value)}`
        case 'ne':
          return value === null
            ? sql`${sql.raw(column)} IS NOT NULL`
            : sql`${asText} IS DISTINCT FROM ${String(value)}`
        // Case-insensitively, because SCIM says these operators are, and
        // because a directory that sends Ada@Example.Test and a store that
        // holds ada@example.test must still match.
        case 'co':
          return sql`${asText} ILIKE ${'%' + like(String(value)) + '%'}`
        case 'sw':
          return sql`${asText} ILIKE ${like(String(value)) + '%'}`
        case 'ew':
          return sql`${asText} ILIKE ${'%' + like(String(value))}`
        case 'gt':
          return sql`${asText} > ${String(value)}`
        case 'ge':
          return sql`${asText} >= ${String(value)}`
        case 'lt':
          return sql`${asText} < ${String(value)}`
        case 'le':
          return sql`${asText} <= ${String(value)}`
      }
    }
  }
}
/* eslint-enable @typescript-eslint/no-explicit-any */

function columnFor(attribute: string, columns: Record<string, string>): string {
  const column = columns[attribute.toLowerCase()]
  if (!column) {
    throw new FilterRefused(
      `This implementation cannot filter on "${attribute}". Filterable attributes are: ` +
        `${Object.keys(columns).sort().join(', ')}.`,
    )
  }
  return column
}

/** Escapes the wildcards in a LIKE pattern, so a filter cannot smuggle one. */
function like(value: string): string {
  return value.replace(/([\\%_])/g, '\\$1')
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

/* eslint-disable @typescript-eslint/no-explicit-any */
function toUser(row: Record<string, any>): UserRecord {
  return {
    id: row.id,
    externalId: row.external_id ?? null,
    userName: row.user_name,
    active: row.active,
    givenName: row.given_name ?? null,
    familyName: row.family_name ?? null,
    displayName: row.display_name ?? null,
    version: Number(row.version),
    createdAt: asDate(row.created_at),
    updatedAt: asDate(row.updated_at),
  }
}
/* eslint-enable @typescript-eslint/no-explicit-any */

const USER_SELECT = sql`
  id, external_id, user_name, active, given_name, family_name, display_name,
  version, created_at, updated_at`

export async function listUsers(
  pool: Pool,
  caller: Caller,
  input: { filter: Filter | null; startIndex: number; count: number },
): Promise<{ users: UserRecord[]; total: number }> {
  const where = input.filter ? condition(input.filter, USER_COLUMNS) : sql`true`
  return pool.withTenant({ orgId: caller.orgId }, async (db) => {
    const total = await db.execute<{ n: string }>(
      sql`SELECT count(*) AS n FROM scim_resources WHERE ${where}`,
    )
    const rows = await db.execute<Record<string, unknown>>(sql`
      SELECT ${USER_SELECT} FROM scim_resources WHERE ${where}
      ORDER BY created_at, id LIMIT ${input.count} OFFSET ${input.startIndex - 1}`)
    return { users: rows.map(toUser), total: Number(total[0]?.n ?? 0) }
  })
}

export async function getUser(
  pool: Pool,
  caller: Caller,
  id: string,
): Promise<UserRecord | null> {
  if (!isUuid(id)) return null
  const rows = await pool.withTenant({ orgId: caller.orgId }, async (db) =>
    db.execute<Record<string, unknown>>(
      sql`SELECT ${USER_SELECT} FROM scim_resources WHERE id = ${id}`,
    ),
  )
  return rows[0] ? toUser(rows[0]) : null
}

export interface UserInput {
  userName: string
  externalId?: string | null
  active?: boolean
  givenName?: string | null
  familyName?: string | null
  displayName?: string | null
}

export async function createUser(
  pool: Pool,
  caller: Caller,
  input: UserInput,
  now: Date,
  defaultRole: string,
): Promise<UserRecord> {
  const userName = input.userName.trim().toLowerCase()
  if (!userName.includes('@')) {
    throw new ScimError(
      400,
      'userName must be an email address: it is what links this account to a person signing in.',
      'invalidValue',
    )
  }
  const active = input.active ?? true

  return pool.withTenant({ orgId: caller.orgId }, async (db) => {
    const clash = await db.execute<{ id: string }>(
      sql`SELECT id FROM scim_resources WHERE user_name = ${userName}`,
    )
    if (clash.length > 0) {
      // 409 and the exact scimType, because that is what tells a provider to
      // reconcile with the existing resource rather than retry forever.
      throw new ScimError(409, `A user with userName ${userName} already exists.`, 'uniqueness')
    }

    const userId = await upsertLocalUser(db, caller.orgId, userName, input, now)

    const created = await db.execute<Record<string, unknown>>(sql`
      INSERT INTO scim_resources
        (org_id, user_id, external_id, user_name, active, given_name, family_name, display_name,
         created_at, updated_at)
      VALUES (${caller.orgId}, ${userId}, ${input.externalId ?? null}, ${userName}, ${active},
              ${input.givenName ?? null}, ${input.familyName ?? null}, ${input.displayName ?? null},
              ${now.toISOString()}, ${now.toISOString()})
      RETURNING ${USER_SELECT}`)

    const resource = toUser(created[0]!)

    if (active) await grantMembership(db, caller.orgId, userId, defaultRole, now)
    else await revokeAccess(db, caller.orgId, userId, now)

    // The ordering this exists for. A group that named this person before they
    // existed now has a member to point at.
    await resolvePending(db, caller.orgId, resource.id, userName, input.externalId ?? null)

    await appendAudit(db, {
      orgId: caller.orgId,
      actorLabel: 'scim',
      action: 'scim.user.created',
      targetType: 'user',
      targetId: resource.id,
      origin: 'scim',
      detail: { userName, active },
      occurredAt: now,
    })

    return resource
  })
}

export interface UserUpdate extends Partial<UserInput> {}

export async function updateUser(
  pool: Pool,
  caller: Caller,
  id: string,
  update: UserUpdate,
  now: Date,
  defaultRole: string,
): Promise<UserRecord> {
  if (!isUuid(id)) throw new ScimError(404, 'No such user.')

  return pool.withTenant({ orgId: caller.orgId }, async (db) => {
    const existing = await db.execute<Record<string, unknown>>(
      sql`SELECT ${USER_SELECT}, user_id FROM scim_resources WHERE id = ${id}`,
    )
    const before = existing[0]
    if (!before) throw new ScimError(404, 'No such user.')

    const userName = update.userName?.trim().toLowerCase() ?? (before.user_name as string)
    if (userName !== before.user_name) {
      const clash = await db.execute<{ id: string }>(
        sql`SELECT id FROM scim_resources WHERE user_name = ${userName} AND id <> ${id}`,
      )
      if (clash.length > 0) {
        throw new ScimError(409, `A user with userName ${userName} already exists.`, 'uniqueness')
      }
    }

    const active = update.active ?? (before.active as boolean)
    const updated = await db.execute<Record<string, unknown>>(sql`
      UPDATE scim_resources SET
        user_name = ${userName},
        external_id = ${update.externalId === undefined ? (before.external_id ?? null) : update.externalId},
        active = ${active},
        given_name = ${update.givenName === undefined ? (before.given_name ?? null) : update.givenName},
        family_name = ${update.familyName === undefined ? (before.family_name ?? null) : update.familyName},
        display_name = ${update.displayName === undefined ? (before.display_name ?? null) : update.displayName},
        version = version + 1,
        updated_at = ${now.toISOString()}
      WHERE id = ${id}
      RETURNING ${USER_SELECT}`)

    const localUserId = (before.user_id as string | null) ?? null
    if (localUserId) {
      // Reactivation grants the membership FIRST. The policy that lets a
      // directory write a profile is only true while the membership exists, so
      // updating the profile of somebody who is currently deprovisioned would
      // match no rows and report success. Deactivation is the mirror image and
      // is handled below, after the profile is written.
      if (active && !(before.active as boolean)) {
        await grantMembership(db, caller.orgId, localUserId, defaultRole, now)
      }
      // COALESCE rather than an overwrite: a PATCH that says nothing about the
      // name must not blank one. The directory owns this profile, and owning it
      // does not mean clearing every field it did not mention.
      const nextName = displayNameFor({
        displayName: update.displayName ?? (before.display_name as string | null),
        givenName: update.givenName ?? (before.given_name as string | null),
        familyName: update.familyName ?? (before.family_name as string | null),
      })
      await db.execute(sql`
        UPDATE users SET email = ${userName},
                         name = COALESCE(${nextName}, name),
                         updated_at = ${now.toISOString()}
        WHERE id = ${localUserId}`)

      // The deprovisioning path, and the reason it is a delete and not a flag.
      // Last, so that the profile write above still had a membership to be
      // permitted by.
      //
      // Revoked whenever the directory says inactive, NOT only on the
      // active-to-inactive transition. Guarding on the transition made a repeat
      // deactivation a silent no-op, and access can be regained BETWEEN two
      // deactivations by any path that grants a membership. Microsoft Entra
      // will not rescue us from that: once it has soft-deleted somebody it
      // answers the next deprovision with `RedundantSoftDelete` and skips
      // entirely, so the one call that would have cleaned up never arrives.
      // Revoking unconditionally is idempotent and costs one statement.
      if (!active) {
        await revokeAccess(db, caller.orgId, localUserId, now)
      }
    }

    await appendAudit(db, {
      orgId: caller.orgId,
      actorLabel: 'scim',
      action: active === (before.active as boolean) ? 'scim.user.updated' : active ? 'scim.user.activated' : 'scim.user.deactivated',
      targetType: 'user',
      targetId: id,
      origin: 'scim',
      detail: { userName, active },
      occurredAt: now,
    })

    return toUser(updated[0]!)
  })
}

/**
 * Removes a user.
 *
 * A delete for somebody who is not here is a 404 and that is the specification
 * and the right behaviour: deprovisioning is the operation most likely to
 * arrive twice, and treating the second as an error produces alarms for exactly
 * the state everybody wanted.
 */
export async function deleteUser(
  pool: Pool,
  caller: Caller,
  id: string,
  now: Date,
): Promise<void> {
  if (!isUuid(id)) throw new ScimError(404, 'No such user.')

  await pool.withTenant({ orgId: caller.orgId }, async (db) => {
    const rows = await db.execute<{ user_id: string | null; user_name: string }>(
      sql`SELECT user_id, user_name FROM scim_resources WHERE id = ${id}`,
    )
    const found = rows[0]
    if (!found) throw new ScimError(404, 'No such user.')

    if (found.user_id) await revokeAccess(db, caller.orgId, found.user_id, now)

    // The membership goes and the account row stays. Environments, runs and
    // audit entries reference a user, and deleting the row would either cascade
    // them away or leave dangling references; the organization keeps its
    // history and the person keeps no access, which is what "deprovisioned"
    // has to mean.
    await db.execute(sql`DELETE FROM scim_resources WHERE id = ${id}`)

    await appendAudit(db, {
      orgId: caller.orgId,
      actorLabel: 'scim',
      action: 'scim.user.deleted',
      targetType: 'user',
      targetId: id,
      origin: 'scim',
      detail: { userName: found.user_name },
      occurredAt: now,
    })
  })
}

// ---------------------------------------------------------------------------
// Access
// ---------------------------------------------------------------------------

async function upsertLocalUser(
  db: Db,
  orgId: string,
  userName: string,
  input: UserInput,
  now: Date,
): Promise<string> {
  const name =
    input.displayName ?? [input.givenName, input.familyName].filter(Boolean).join(' ') ?? null

  // Somebody already in this organization under that address is the same
  // person. Matched inside the tenant only: an assertion or a provisioning
  // call is not evidence about an account in another organization, and merging
  // on it would be an account takeover.
  const existing = await db.execute<{ id: string }>(sql`
    SELECT u.id FROM users u JOIN members m ON m.user_id = u.id
    WHERE m.org_id = ${orgId} AND lower(u.email) = ${userName}`)
  if (existing[0]) return existing[0].id

  // Somebody this organization previously deprovisioned is ADOPTED rather than
  // duplicated. The lookup above joins through members, so a person whose
  // membership was deleted matches nothing, and creating a fresh row for them
  // splits one human across two accounts. Measured against a real Entra tenant
  // in the other direction: it is what let an offboard report success and
  // revoke nothing. Migration 0013 carries the reasoning and the safety
  // argument for the SECURITY DEFINER lookup.
  const orphan = await db.execute<{ id: string | null }>(
    sql`SELECT adoptable_directory_user(${userName}) AS id`,
  )
  const adopted = orphan[0]?.id ?? null
  if (adopted) return adopted

  // The key is generated here rather than asked for back, and that is not a
  // style choice. INSERT ... RETURNING has the SELECT policies applied to the
  // returned row, and a directory account is not readable until its membership
  // row exists, which is written after this. RETURNING id therefore fails with
  // "new row violates row-level security policy" on a row that was inserted
  // perfectly well. Migration 0006 records the same trap on the GitHub path.
  //
  // randomUUID is a version 4 UUID, which is exactly what the column's
  // gen_random_uuid() default produces, so nothing downstream can tell.
  const id = randomUUID()
  // github_id is null, which migration 0012 made possible: this account did
  // not come from GitHub and there is no id to invent.
  await db.execute(sql`
    INSERT INTO users (id, email, name, identity_source, created_at, updated_at)
    VALUES (${id}, ${userName}, ${name || null}, 'scim', ${now.toISOString()}, ${now.toISOString()})`)
  return id
}

async function grantMembership(
  db: Db,
  orgId: string,
  userId: string,
  role: string,
  now: Date,
): Promise<void> {
  await db.execute(sql`
    INSERT INTO members (org_id, user_id, role, source, created_at, updated_at)
    -- Bound and cast, never interpolated. The value is server configuration
    -- today and a bound parameter costs nothing, and the day somebody wires
    -- this to a group mapping the role becomes attacker-influenced input.
    VALUES (${orgId}, ${userId}, ${role}::member_role, 'scim',
            ${now.toISOString()}, ${now.toISOString()})
    ON CONFLICT (org_id, user_id) DO UPDATE SET updated_at = ${now.toISOString()}`)
}

/**
 * Takes access away, now.
 *
 * The membership is deleted rather than flagged, and the sessions go with it in
 * the same transaction. Deprovisioning that took effect at the end of somebody's
 * current session would mean a person removed at nine still reading data at
 * five, which is the thing a customer buys SCIM to prevent.
 */
async function revokeAccess(db: Db, orgId: string, userId: string, now: Date): Promise<void> {
  await db.execute(sql`DELETE FROM members WHERE org_id = ${orgId} AND user_id = ${userId}`)
  await db.execute(sql`
    UPDATE sessions SET revoked_at = ${now.toISOString()}
    WHERE user_id = ${userId} AND org_id = ${orgId} AND revoked_at IS NULL`)
}

/** Points every waiting group membership at a user who has now appeared. */
async function resolvePending(
  db: Db,
  orgId: string,
  resourceId: string,
  userName: string,
  externalId: string | null,
): Promise<void> {
  const refs = [resourceId, userName, ...(externalId ? [externalId] : [])]
  await db.execute(sql`
    UPDATE scim_group_members SET resource_id = ${resourceId}
    WHERE org_id = ${orgId} AND resource_id IS NULL
      AND member_ref = ANY(${sql.param(refs)}::text[])`)
}

// ---------------------------------------------------------------------------
// Groups
// ---------------------------------------------------------------------------

/* eslint-disable @typescript-eslint/no-explicit-any */
function toGroup(row: Record<string, any>): GroupRecord {
  return {
    id: row.id,
    externalId: row.external_id ?? null,
    displayName: row.display_name,
    role: row.role ?? null,
    version: Number(row.version),
    createdAt: asDate(row.created_at),
    updatedAt: asDate(row.updated_at),
  }
}
/* eslint-enable @typescript-eslint/no-explicit-any */

const GROUP_SELECT = sql`
  id, external_id, display_name, role::text AS role, version, created_at, updated_at`

export async function listGroups(
  pool: Pool,
  caller: Caller,
  input: { filter: Filter | null; startIndex: number; count: number },
): Promise<{ groups: GroupRecord[]; total: number }> {
  const where = input.filter ? condition(input.filter, GROUP_COLUMNS) : sql`true`
  return pool.withTenant({ orgId: caller.orgId }, async (db) => {
    const total = await db.execute<{ n: string }>(
      sql`SELECT count(*) AS n FROM scim_groups WHERE ${where}`,
    )
    const rows = await db.execute<Record<string, unknown>>(sql`
      SELECT ${GROUP_SELECT} FROM scim_groups WHERE ${where}
      ORDER BY created_at, id LIMIT ${input.count} OFFSET ${input.startIndex - 1}`)
    return { groups: rows.map(toGroup), total: Number(total[0]?.n ?? 0) }
  })
}

export async function getGroup(
  pool: Pool,
  caller: Caller,
  id: string,
): Promise<{ group: GroupRecord; members: GroupMemberRecord[] } | null> {
  if (!isUuid(id)) return null
  return pool.withTenant({ orgId: caller.orgId }, async (db) => {
    const rows = await db.execute<Record<string, unknown>>(
      sql`SELECT ${GROUP_SELECT} FROM scim_groups WHERE id = ${id}`,
    )
    if (!rows[0]) return null
    return { group: toGroup(rows[0]), members: await membersOf(db, id) }
  })
}

export async function membersOf(db: Db, groupId: string): Promise<GroupMemberRecord[]> {
  const rows = await db.execute<{
    member_ref: string
    resource_id: string | null
    user_name: string | null
  }>(sql`
    SELECT gm.member_ref, gm.resource_id, r.user_name
    FROM scim_group_members gm
    LEFT JOIN scim_resources r ON r.id = gm.resource_id
    WHERE gm.group_id = ${groupId}
    ORDER BY gm.created_at, gm.id`)
  return rows.map((r) => ({
    memberRef: r.member_ref,
    resourceId: r.resource_id,
    userName: r.user_name,
  }))
}

export interface GroupInput {
  displayName: string
  externalId?: string | null
  members?: readonly string[]
}

export async function createGroup(
  pool: Pool,
  caller: Caller,
  input: GroupInput,
  now: Date,
): Promise<{ group: GroupRecord; members: GroupMemberRecord[] }> {
  const displayName = input.displayName.trim()
  if (!displayName) throw new ScimError(400, 'displayName is required.', 'invalidValue')

  return pool.withTenant({ orgId: caller.orgId }, async (db) => {
    const clash = await db.execute<{ id: string }>(
      sql`SELECT id FROM scim_groups WHERE display_name = ${displayName}`,
    )
    if (clash.length > 0) {
      throw new ScimError(409, `A group named ${displayName} already exists.`, 'uniqueness')
    }

    const created = await db.execute<Record<string, unknown>>(sql`
      INSERT INTO scim_groups (org_id, external_id, display_name, created_at, updated_at)
      VALUES (${caller.orgId}, ${input.externalId ?? null}, ${displayName},
              ${now.toISOString()}, ${now.toISOString()})
      RETURNING ${GROUP_SELECT}`)
    const group = toGroup(created[0]!)

    for (const ref of input.members ?? []) await addMember(db, caller.orgId, group.id, ref)

    await appendAudit(db, {
      orgId: caller.orgId,
      actorLabel: 'scim',
      action: 'scim.group.created',
      targetType: 'group',
      targetId: group.id,
      origin: 'scim',
      detail: { displayName, members: (input.members ?? []).length },
      occurredAt: now,
    })

    return { group, members: await membersOf(db, group.id) }
  })
}

/**
 * Adds a member, whether or not the user is here yet.
 *
 * A reference that matches nothing is stored with a null resource_id, and
 * createUser resolves it later. Dropping it would leave the group permanently
 * missing whoever the provider sent first.
 */
export async function addMember(
  db: Db,
  orgId: string,
  groupId: string,
  memberRef: string,
): Promise<void> {
  const resolved = await db.execute<{ id: string }>(sql`
    SELECT id FROM scim_resources
    WHERE org_id = ${orgId}
      AND (id::text = ${memberRef} OR user_name = ${memberRef.toLowerCase()} OR external_id = ${memberRef})
    LIMIT 1`)

  await db.execute(sql`
    INSERT INTO scim_group_members (org_id, group_id, member_ref, resource_id)
    VALUES (${orgId}, ${groupId}, ${memberRef}, ${resolved[0]?.id ?? null})
    ON CONFLICT (group_id, member_ref) DO UPDATE SET resource_id = EXCLUDED.resource_id`)
}

export async function removeMember(db: Db, groupId: string, memberRef: string): Promise<void> {
  // By reference OR by resolved id, because a provider may name a member on
  // removal by a different identifier than it used to add them.
  await db.execute(sql`
    DELETE FROM scim_group_members
    WHERE group_id = ${groupId} AND (member_ref = ${memberRef} OR resource_id::text = ${memberRef})`)
}

export async function replaceGroup(
  pool: Pool,
  caller: Caller,
  id: string,
  input: GroupInput,
  now: Date,
): Promise<{ group: GroupRecord; members: GroupMemberRecord[] }> {
  if (!isUuid(id)) throw new ScimError(404, 'No such group.')

  return pool.withTenant({ orgId: caller.orgId }, async (db) => {
    const existing = await db.execute<{ id: string }>(sql`SELECT id FROM scim_groups WHERE id = ${id}`)
    if (!existing[0]) throw new ScimError(404, 'No such group.')

    const updated = await db.execute<Record<string, unknown>>(sql`
      UPDATE scim_groups SET display_name = ${input.displayName.trim()},
        external_id = ${input.externalId === undefined ? sql`external_id` : input.externalId},
        version = version + 1, updated_at = ${now.toISOString()}
      WHERE id = ${id} RETURNING ${GROUP_SELECT}`)

    if (input.members) {
      // PUT is a replace, so the member list becomes exactly what arrived.
      await db.execute(sql`DELETE FROM scim_group_members WHERE group_id = ${id}`)
      for (const ref of input.members) await addMember(db, caller.orgId, id, ref)
    }

    await appendAudit(db, {
      orgId: caller.orgId,
      actorLabel: 'scim',
      action: 'scim.group.replaced',
      targetType: 'group',
      targetId: id,
      origin: 'scim',
      detail: { displayName: input.displayName },
      occurredAt: now,
    })

    return { group: toGroup(updated[0]!), members: await membersOf(db, id) }
  })
}

export async function deleteGroup(
  pool: Pool,
  caller: Caller,
  id: string,
  now: Date,
): Promise<void> {
  if (!isUuid(id)) throw new ScimError(404, 'No such group.')
  await pool.withTenant({ orgId: caller.orgId }, async (db) => {
    const rows = await db.execute<{ display_name: string }>(
      sql`DELETE FROM scim_groups WHERE id = ${id} RETURNING display_name`,
    )
    if (!rows[0]) throw new ScimError(404, 'No such group.')
    await appendAudit(db, {
      orgId: caller.orgId,
      actorLabel: 'scim',
      action: 'scim.group.deleted',
      targetType: 'group',
      targetId: id,
      origin: 'scim',
      detail: { displayName: rows[0].display_name },
      occurredAt: now,
    })
  })
}

/** Runs several statements inside one tenant transaction. */
export function inTenant<T>(pool: Pool, caller: Caller, fn: (db: Db) => Promise<T>): Promise<T> {
  return pool.withTenant({ orgId: caller.orgId }, fn)
}

export function isUuid(value: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value)
}

function displayNameFor(input: {
  displayName?: string | null
  givenName?: string | null
  familyName?: string | null
}): string | null {
  if (input.displayName) return input.displayName
  const parts = [input.givenName, input.familyName].filter(Boolean)
  return parts.length ? parts.join(' ') : null
}

function asDate(value: unknown): Date {
  return value instanceof Date ? value : new Date(String(value))
}

export { sql }
