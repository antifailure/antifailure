// Custom roles, and the scopes they apply at.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The community edition has four roles and they are the right four for a team.
// A large organization is shaped differently: somebody administers two
// repositories and reads the rest, a compliance team approves masking changes
// and cannot create environments, a contractor sees one repository and nothing
// about the others.
//
// Three rules, and each one is a way this goes wrong when it is not stated.
//
// Permissions come from the fixed catalog and nowhere else. A custom role is a
// subset of what already exists, so the enforcement middleware and the
// documentation cannot disagree about what a permission means, and a typo in a
// role definition is refused rather than silently granting nothing.
//
// A narrower scope grants, it never revokes. A grant at the repository level
// adds to what the organization level already gave; it cannot take something
// away. The alternative reads well and is unusable: an administrator adds a
// role to give somebody access to one repository and silently removes their
// access to every other, and nobody can predict what anyone can do without
// evaluating every rule in order.
//
// Deny by default. A request that matches no grant is refused, and a resolver
// that has nothing to say about a request returns no opinion so the built-in
// role decides. That is what stops a resolver covering two repositories from
// having to reproduce the whole permission table correctly.

import {
  PERMISSIONS,
  type Permission,
  type PermissionRequest,
  type Role,
} from '@antifailure/api'

/** Where a grant applies. Ordered from widest to narrowest, for display only. */
export const SCOPE_KINDS = ['organization', 'group', 'repository', 'environment'] as const
export type ScopeKind = (typeof SCOPE_KINDS)[number]

export interface Scope {
  kind: ScopeKind
  /** The organization slug, repository full name, group name, or environment
   *  identifier. Ignored for the organization scope, which covers everything. */
  name?: string
}

export interface CustomRole {
  /** Stable across renames, because a grant refers to it. */
  id: string
  name: string
  description: string
  permissions: Permission[]
}

/** One person holding one role at one scope. */
export interface Grant {
  userId: string
  roleId: string
  scope: Scope
}

/** A named set of repositories, so a grant can cover a team's repositories
 *  without listing them and without being re-edited when one is added. */
export interface RepositoryGroup {
  name: string
  repositories: string[]
}

export interface Model {
  roles: CustomRole[]
  grants: Grant[]
  groups: RepositoryGroup[]
}

export class ModelError extends Error {}

/**
 * Validates a model, refusing anything ambiguous.
 *
 * Refused rather than repaired. A permission model that quietly corrects what
 * an administrator wrote is a model nobody can predict, and the whole value of
 * writing it down is that it can be read.
 */
export function validate(model: Model): void {
  const known = new Set<string>(PERMISSIONS)
  const roleIds = new Set<string>()

  for (const role of model.roles) {
    if (!role.id) throw new ModelError('a role has no id, and a grant refers to a role by id')
    if (roleIds.has(role.id)) {
      throw new ModelError(`two roles share the id ${role.id}, so a grant naming it is ambiguous`)
    }
    roleIds.add(role.id)
    if (!role.name) throw new ModelError(`role ${role.id} has no name`)
    if (!role.description) {
      // Required, because a role called "ops" with no description is a role
      // nobody can review, and reviewing it is the point of writing it down.
      throw new ModelError(
        `role ${role.id} has no description; a reviewer cannot judge a role by its name alone`,
      )
    }
    for (const permission of role.permissions) {
      if (!known.has(permission)) {
        throw new ModelError(
          `role ${role.id} names ${permission}, which is not a permission. ` +
            `A typo here would grant nothing and look like it granted something.`,
        )
      }
    }
  }

  const groupNames = new Set(model.groups.map((g) => g.name))
  for (const group of model.groups) {
    if (!group.name) throw new ModelError('a repository group has no name')
    if (group.repositories.length === 0) {
      throw new ModelError(
        `group ${group.name} is empty, so every grant on it does nothing; ` +
          `remove it or name a repository`,
      )
    }
  }

  for (const grant of model.grants) {
    if (!roleIds.has(grant.roleId)) {
      throw new ModelError(`a grant names role ${grant.roleId}, which does not exist`)
    }
    if (grant.scope.kind !== 'organization' && !grant.scope.name) {
      throw new ModelError(`a ${grant.scope.kind} grant names no ${grant.scope.kind}`)
    }
    if (grant.scope.kind === 'group' && !groupNames.has(grant.scope.name!)) {
      throw new ModelError(`a grant names group ${grant.scope.name}, which does not exist`)
    }
  }
}

/** What the request concerns, resolved against the groups. */
function inScope(scope: Scope, req: PermissionRequest, groups: RepositoryGroup[]): boolean {
  switch (scope.kind) {
    case 'organization':
      return true
    case 'repository':
      return req.repository != null && scope.name === req.repository
    case 'environment':
      return req.envId != null && scope.name === req.envId
    case 'group': {
      if (req.repository == null) return false
      const group = groups.find((g) => g.name === scope.name)
      return group != null && group.repositories.includes(req.repository)
    }
  }
}

/**
 * Builds a resolver from a model.
 *
 * Returns true when some grant covers the request, and undefined otherwise.
 * Undefined rather than false is the whole design: a model that says nothing
 * about a request leaves the built-in role to decide, so installing a model
 * that covers one repository does not remove everybody's access to the rest.
 */
export function resolverFor(model: Model): (req: PermissionRequest) => boolean | undefined {
  validate(model)
  const byId = new Map(model.roles.map((r) => [r.id, r]))

  return (req) => {
    for (const grant of model.grants) {
      if (grant.userId !== req.userId) continue
      if (!inScope(grant.scope, req, model.groups)) continue
      const role = byId.get(grant.roleId)
      if (role?.permissions.includes(req.permission)) return true
    }
    return undefined
  }
}

/**
 * Every permission a user holds, by scope, for the page that answers "what can
 * this person do".
 *
 * Built rather than derived at read time because the question is asked about
 * somebody else, usually during an access review, and an answer assembled from
 * five places is an answer nobody trusts.
 */
export function effectivePermissions(
  model: Model,
  userId: string,
  builtin: Role,
  builtinPermissions: readonly Permission[],
): { scope: Scope; permissions: Permission[]; source: string }[] {
  validate(model)
  const byId = new Map(model.roles.map((r) => [r.id, r]))

  const out: { scope: Scope; permissions: Permission[]; source: string }[] = [
    {
      scope: { kind: 'organization' },
      permissions: [...builtinPermissions],
      source: `the built-in ${builtin} role`,
    },
  ]
  for (const grant of model.grants) {
    if (grant.userId !== userId) continue
    const role = byId.get(grant.roleId)
    if (!role) continue
    out.push({ scope: grant.scope, permissions: [...role.permissions], source: role.name })
  }
  return out
}
