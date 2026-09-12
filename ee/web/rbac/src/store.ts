// Where an organization's role model lives, and the two ways it is read.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// STRICT ON THE WAY IN, TOLERANT ON THE WAY OUT, and the two halves are not
// symmetrical on purpose.
//
// A model is written in one piece, from a policy file somebody reviewed, inside
// one transaction, after validate() has refused anything ambiguous. Nothing
// here repairs what an administrator wrote, for the reason roles.ts gives: a
// model that quietly corrects itself is a model nobody can predict.
//
// A model is read on every request a built-in role would refuse, and a read
// that throws on one odd row would take every custom grant in the organization
// down with it. So the read drops what it cannot act on, a permission that is
// not in the catalogue, a grant whose role or group has gone, and keeps the
// rest. Dropping is always the direction that grants less, which is the only
// direction a permission read is allowed to fail in.
//
// Every statement names the organization explicitly AND runs inside a tenant
// transaction whose policy names it too. Either alone would confine the read.
// Both is the rule features.ts states for the entitlement read, for the same
// reason: a WHERE clause is a claim and a policy is a mechanism, and the day
// one of them is wrong the other is still there.

import { sql, type Db } from '@antifailure/db'
import { PERMISSIONS, type Permission } from '@antifailure/api'
import {
  ModelError,
  SCOPE_KINDS,
  validate,
  type CustomRole,
  type Grant,
  type Model,
  type RepositoryGroup,
  type ScopeKind,
} from './roles.ts'

const KNOWN = new Set<string>(PERMISSIONS)
const KINDS = new Set<string>(SCOPE_KINDS)
const ROLE_KEY = /^[a-z0-9][a-z0-9_-]{0,62}$/
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

type RoleRow = {
  role_key: string
  name: string
  description: string
  permission: string | null
}
type GrantRow = {
  role_key: string
  user_id: string
  scope_kind: string
  scope_name: string | null
}
type GroupRow = {
  name: string
  repository: string
}

/**
 * Serializes every write to one organization's model, and every read that a
 * write is about to be judged against.
 *
 * A transaction lock rather than a row lock, because the application role
 * cannot lock the organizations row and a lock on one of these tables cannot
 * cover a model that does not exist yet. Two administrators applying two files
 * at once must end with one file applied, never with the roles of one and the
 * grants of the other, and a dry run taken outside the lock would describe a
 * model that is no longer the one being replaced.
 */
export async function lockModel(db: Db, orgId: string): Promise<void> {
  await db.execute(sql`SELECT pg_advisory_xact_lock(hashtextextended(${`custom_roles:${orgId}`}, 0))`)
}

/** The whole model, for export and for the diff a new file is judged against. */
export async function readModel(db: Db, orgId: string): Promise<Model> {
  const roles = await db.execute<RoleRow>(sql`
    SELECT r.role_key, r.name, r.description, p.permission
    FROM custom_roles r
    LEFT JOIN custom_role_permissions p ON p.org_id = r.org_id AND p.role_id = r.id
    WHERE r.org_id = ${orgId}
    ORDER BY r.role_key, p.permission`)
  const grants = await db.execute<GrantRow>(sql`
    SELECT r.role_key, g.user_id, g.scope_kind, g.scope_name
    FROM custom_role_grants g
    JOIN custom_roles r ON r.org_id = g.org_id AND r.id = g.role_id
    WHERE g.org_id = ${orgId}
    ORDER BY g.user_id, r.role_key, g.scope_kind, g.scope_name`)
  const groups = await db.execute<GroupRow>(sql`
    SELECT g.name, m.repository
    FROM repository_groups g
    JOIN repository_group_members m ON m.org_id = g.org_id AND m.group_id = g.id
    WHERE g.org_id = ${orgId}
    ORDER BY g.name, m.repository`)
  return assemble([...roles], [...grants], [...groups])
}

/**
 * The part of the model that can bear on one person asking for one permission.
 *
 * Only the roles this person holds, only the rows of those roles that carry the
 * permission being asked about, and only the groups their grants name. The
 * resolver's answer about that permission is identical to the answer the whole
 * model would give, because every row left out is a row that could not have
 * granted it, and the read is one indexed lookup rather than the organization's
 * entire model on every refused request.
 */
export async function modelFor(
  db: Db,
  orgId: string,
  userId: string,
  permission: Permission,
): Promise<Model> {
  const rows = await db.execute<RoleRow & GrantRow>(sql`
    SELECT r.role_key, r.name, r.description, p.permission,
           g.user_id, g.scope_kind, g.scope_name
    FROM custom_role_grants g
    JOIN custom_roles r ON r.org_id = g.org_id AND r.id = g.role_id
    JOIN custom_role_permissions p ON p.org_id = r.org_id AND p.role_id = r.id
    WHERE g.org_id = ${orgId} AND g.user_id = ${userId} AND p.permission = ${permission}`)
  if (rows.length === 0) return { roles: [], grants: [], groups: [] }

  const groupNames = [
    ...new Set(rows.filter((r) => r.scope_kind === 'group' && r.scope_name).map((r) => r.scope_name!)),
  ]
  const groups =
    groupNames.length === 0
      ? []
      : await db.execute<GroupRow>(sql`
          SELECT g.name, m.repository
          FROM repository_groups g
          JOIN repository_group_members m ON m.org_id = g.org_id AND m.group_id = g.id
          WHERE g.org_id = ${orgId}
            AND g.name IN (${sql.join(groupNames.map((n) => sql`${n}`), sql`, `)})`)
  return assemble([...rows], [...rows], [...groups])
}

/**
 * Rows into a Model the validator accepts, dropping what cannot be acted on.
 *
 * Exported so the tolerance can be tested without a database, because the case
 * it exists for, a row that should not be there, is precisely the case a
 * correct write path never produces.
 */
export function assemble(roleRows: RoleRow[], grantRows: GrantRow[], groupRows: GroupRow[]): Model {
  const roles = new Map<string, CustomRole>()
  for (const row of roleRows) {
    let role = roles.get(row.role_key)
    if (!role) {
      role = { id: row.role_key, name: row.name, description: row.description, permissions: [] }
      roles.set(row.role_key, role)
    }
    if (row.permission && KNOWN.has(row.permission) && !role.permissions.includes(row.permission as Permission)) {
      role.permissions.push(row.permission as Permission)
    }
  }

  const groups = new Map<string, RepositoryGroup>()
  for (const row of groupRows) {
    const group = groups.get(row.name) ?? { name: row.name, repositories: [] }
    if (!group.repositories.includes(row.repository)) group.repositories.push(row.repository)
    groups.set(row.name, group)
  }

  const grants: Grant[] = []
  const seen = new Set<string>()
  for (const row of grantRows) {
    if (!roles.has(row.role_key) || !KINDS.has(row.scope_kind)) continue
    const kind = row.scope_kind as ScopeKind
    if (kind !== 'organization' && !row.scope_name) continue
    if (kind === 'group' && !groups.has(row.scope_name!)) continue
    const key = `${row.user_id}|${row.role_key}|${kind}|${row.scope_name ?? ''}`
    if (seen.has(key)) continue
    seen.add(key)
    grants.push({
      userId: row.user_id,
      roleId: row.role_key,
      scope: kind === 'organization' ? { kind } : { kind, name: row.scope_name! },
    })
  }

  return { roles: [...roles.values()], grants, groups: [...groups.values()] }
}

/**
 * Replaces an organization's model with another, whole.
 *
 * Replaced rather than merged, because the file is the model: a role absent
 * from the file is a role the author removed, and a merge would keep it. The
 * caller holds the transaction and has taken lockModel first, so the file and
 * the diff it was judged by describe the same starting point.
 *
 * Refused, never repaired, with one exception the library already documents:
 * an organization scope carries no name, and one written with a name has it
 * ignored, because roles.ts says the organization scope ignores it. A repeated
 * permission, repository or grant is written once, because a line written
 * twice has one meaning and the database would otherwise refuse the second
 * copy with a message about a constraint rather than about the file.
 */
export async function writeModel(db: Db, orgId: string, model: Model): Promise<void> {
  validate(model)

  for (const role of model.roles) {
    if (!ROLE_KEY.test(role.id)) {
      throw new ModelError(
        `role id ${JSON.stringify(role.id)} must be lower case letters, digits, dashes and ` +
          'underscores, starting with a letter or digit, because a grant refers to it in YAML',
      )
    }
  }
  for (const group of model.groups) {
    if (group.repositories.some((r) => !r)) {
      throw new ModelError(`group ${group.name} names an empty repository`)
    }
  }
  const userIds = [...new Set(model.grants.map((g) => g.userId))]
  for (const grant of model.grants) {
    if (!KINDS.has(grant.scope.kind)) {
      throw new ModelError(
        `a grant is scoped to ${String(grant.scope.kind)}, and a scope is one of ${SCOPE_KINDS.join(', ')}`,
      )
    }
    if (!UUID.test(grant.userId)) {
      throw new ModelError(`a grant names ${JSON.stringify(grant.userId)}, which is not a user id`)
    }
  }

  // Membership, checked here so the refusal names the person rather than a
  // constraint. The composite reference to members would refuse it anyway, and
  // that reference is what holds when this check is someday wrong.
  if (userIds.length > 0) {
    const members = await db.execute<{ user_id: string }>(sql`
      SELECT user_id FROM members
      WHERE org_id = ${orgId}
        AND user_id IN (${sql.join(userIds.map((id) => sql`${id}::uuid`), sql`, `)})`)
    const present = new Set([...members].map((m) => m.user_id))
    const absent = userIds.filter((id) => !present.has(id))
    if (absent.length > 0) {
      throw new ModelError(
        `a grant names ${absent.join(', ')}, who ${absent.length === 1 ? 'is' : 'are'} not a ` +
          'member of this organization. A custom role adds to a membership; it cannot create one.',
      )
    }
  }

  await db.execute(sql`DELETE FROM custom_role_grants WHERE org_id = ${orgId}`)
  await db.execute(sql`DELETE FROM custom_roles WHERE org_id = ${orgId}`)
  await db.execute(sql`DELETE FROM repository_groups WHERE org_id = ${orgId}`)

  const roleIds = new Map<string, string>()
  for (const role of model.roles) {
    const [row] = await db.execute<{ id: string }>(sql`
      INSERT INTO custom_roles (org_id, role_key, name, description)
      VALUES (${orgId}, ${role.id}, ${role.name}, ${role.description})
      RETURNING id`)
    roleIds.set(role.id, row!.id)
    for (const permission of new Set(role.permissions)) {
      await db.execute(sql`
        INSERT INTO custom_role_permissions (org_id, role_id, permission)
        VALUES (${orgId}, ${row!.id}, ${permission})`)
    }
  }

  for (const group of model.groups) {
    const [row] = await db.execute<{ id: string }>(sql`
      INSERT INTO repository_groups (org_id, name) VALUES (${orgId}, ${group.name}) RETURNING id`)
    for (const repository of new Set(group.repositories)) {
      await db.execute(sql`
        INSERT INTO repository_group_members (org_id, group_id, repository)
        VALUES (${orgId}, ${row!.id}, ${repository})`)
    }
  }

  const written = new Set<string>()
  for (const grant of model.grants) {
    const name = grant.scope.kind === 'organization' ? null : grant.scope.name!
    const key = `${grant.userId}|${grant.roleId}|${grant.scope.kind}|${name ?? ''}`
    if (written.has(key)) continue
    written.add(key)
    await db.execute(sql`
      INSERT INTO custom_role_grants (org_id, role_id, user_id, scope_kind, scope_name)
      VALUES (${orgId}, ${roleIds.get(grant.roleId)!}, ${grant.userId}, ${grant.scope.kind}, ${name})`)
  }
}
