// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

// Custom roles, and the three rules that make them predictable.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  ROLE_PERMISSIONS,
  permits,
  setPermissionResolver,
  hasPermissionResolver,
  type Permission,
  type PermissionRequest,
} from '@antifailure/api'
import {
  validate, resolverFor, effectivePermissions, ModelError, type Model,
} from '../src/index.ts'

function model(over: Partial<Model> = {}): Model {
  return {
    roles: [
      {
        id: 'repo-admin',
        name: 'Repository administrator',
        description: 'Runs and tears down environments for one repository.',
        permissions: ['environments.view', 'environments.create', 'environments.teardown'],
      },
      {
        id: 'approver',
        name: 'Masking approver',
        description: 'Approves masking rule changes and can do nothing else.',
        permissions: ['masking.approve'],
      },
    ],
    grants: [],
    groups: [{ name: 'payments', repositories: ['acme/billing', 'acme/invoices'] }],
    ...over,
  }
}

function request(over: Partial<PermissionRequest> = {}): PermissionRequest {
  return {
    orgId: 'org-1', userId: 'user-1', role: 'viewer',
    permission: 'environments.create', repository: 'acme/app', envId: null,
    ...over,
  }
}

// ---------------------------------------------------------------------------
// Validation refuses rather than repairs
// ---------------------------------------------------------------------------

describe('validation', () => {
  it('refuses a permission that is not in the catalog', () => {
    // A typo grants nothing and looks like it granted something, which is the
    // worst outcome available: an administrator believes access was given.
    assert.throws(
      () => validate(model({
        roles: [{
          id: 'r', name: 'R', description: 'd',
          permissions: ['environments.veiw' as Permission],
        }],
      })),
      ModelError,
    )
  })

  it('refuses a role with no description', () => {
    // A role called "ops" with no description is a role nobody can review, and
    // reviewing it is the entire point of writing the model down.
    assert.throws(
      () => validate(model({
        roles: [{ id: 'r', name: 'Ops', description: '', permissions: [] }],
      })),
      /description/,
    )
  })

  it('refuses two roles with the same id', () => {
    assert.throws(
      () => validate(model({
        roles: [
          { id: 'r', name: 'A', description: 'a', permissions: [] },
          { id: 'r', name: 'B', description: 'b', permissions: [] },
        ],
      })),
      /share the id/,
    )
  })

  it('refuses a grant naming a role that does not exist', () => {
    assert.throws(
      () => validate(model({
        grants: [{ userId: 'u', roleId: 'ghost', scope: { kind: 'organization' } }],
      })),
      /does not exist/,
    )
  })

  it('refuses a grant naming a group that does not exist', () => {
    assert.throws(
      () => validate(model({
        grants: [{ userId: 'u', roleId: 'approver', scope: { kind: 'group', name: 'nope' } }],
      })),
      /group nope/,
    )
  })

  it('refuses a scoped grant that names no scope', () => {
    assert.throws(
      () => validate(model({
        grants: [{ userId: 'u', roleId: 'approver', scope: { kind: 'repository' } }],
      })),
      /names no repository/,
    )
  })

  it('refuses an empty repository group', () => {
    // Every grant on it does nothing, which reads as access somebody has.
    assert.throws(
      () => validate(model({ groups: [{ name: 'empty', repositories: [] }] })),
      /is empty/,
    )
  })
})

// ---------------------------------------------------------------------------
// Resolution
// ---------------------------------------------------------------------------

describe('resolution', () => {
  it('grants a permission at the repository it was granted for', () => {
    const resolve = resolverFor(model({
      grants: [{
        userId: 'user-1', roleId: 'repo-admin',
        scope: { kind: 'repository', name: 'acme/app' },
      }],
    }))
    assert.equal(resolve(request()), true)
  })

  it('says nothing about a different repository rather than refusing', () => {
    // Undefined, not false. A model covering one repository must not remove
    // everybody's access to the rest, and a resolver that returned false here
    // would do exactly that.
    const resolve = resolverFor(model({
      grants: [{
        userId: 'user-1', roleId: 'repo-admin',
        scope: { kind: 'repository', name: 'acme/app' },
      }],
    }))
    assert.equal(resolve(request({ repository: 'acme/other' })), undefined)
  })

  it('says nothing about another user', () => {
    const resolve = resolverFor(model({
      grants: [{
        userId: 'user-1', roleId: 'repo-admin', scope: { kind: 'organization' },
      }],
    }))
    assert.equal(resolve(request({ userId: 'somebody-else' })), undefined)
  })

  it('never returns false, so a grant can only ever add', () => {
    // The property that makes this predictable. Whatever the model says, the
    // built-in role's answer is a floor.
    const resolve = resolverFor(model({
      grants: [
        { userId: 'user-1', roleId: 'approver', scope: { kind: 'organization' } },
        { userId: 'user-1', roleId: 'repo-admin', scope: { kind: 'repository', name: 'acme/app' } },
      ],
    }))
    for (const permission of Object.values(ROLE_PERMISSIONS).flat()) {
      for (const repository of ['acme/app', 'acme/other', null]) {
        const answer = resolve(request({ permission, repository }))
        assert.notEqual(answer, false,
          `the resolver refused ${permission} on ${repository}, which would revoke access`)
      }
    }
  })

  it('covers every repository in a group without listing them in the grant', () => {
    const resolve = resolverFor(model({
      grants: [{
        userId: 'user-1', roleId: 'repo-admin', scope: { kind: 'group', name: 'payments' },
      }],
    }))
    assert.equal(resolve(request({ repository: 'acme/billing' })), true)
    assert.equal(resolve(request({ repository: 'acme/invoices' })), true)
    assert.equal(resolve(request({ repository: 'acme/app' })), undefined)
  })

  it('grants at an environment', () => {
    const resolve = resolverFor(model({
      grants: [{
        userId: 'user-1', roleId: 'repo-admin', scope: { kind: 'environment', name: 'af-1' },
      }],
    }))
    assert.equal(resolve(request({ envId: 'af-1', permission: 'environments.teardown' })), true)
    assert.equal(resolve(request({ envId: 'af-2', permission: 'environments.teardown' })), undefined)
  })

  it('an organization grant covers a request that names no repository', () => {
    const resolve = resolverFor(model({
      grants: [{ userId: 'user-1', roleId: 'approver', scope: { kind: 'organization' } }],
    }))
    assert.equal(resolve(request({ permission: 'masking.approve', repository: null })), true)
  })

  it('a repository grant does not cover a request that names none', () => {
    // A route that concerns no repository is not covered by a grant that only
    // applies to one, and the answer is no opinion rather than a refusal.
    const resolve = resolverFor(model({
      grants: [{
        userId: 'user-1', roleId: 'approver', scope: { kind: 'repository', name: 'acme/app' },
      }],
    }))
    assert.equal(resolve(request({ permission: 'masking.approve', repository: null })), undefined)
  })
})

// ---------------------------------------------------------------------------
// The extension point in the community API
// ---------------------------------------------------------------------------

describe('the community permission check', () => {
  it('uses the built-in table when no resolver is installed', () => {
    setPermissionResolver(null)
    assert.equal(hasPermissionResolver(), false)
    assert.equal(permits(request({ role: 'viewer', permission: 'environments.create' })), false)
    assert.equal(permits(request({ role: 'admin', permission: 'environments.create' })), true)
  })

  it('lets a resolver widen a built-in role', () => {
    // The whole point: a viewer who has been granted a custom role at one
    // repository can do more there and nothing more anywhere else.
    setPermissionResolver(resolverFor(model({
      grants: [{
        userId: 'user-1', roleId: 'repo-admin',
        scope: { kind: 'repository', name: 'acme/app' },
      }],
    })))
    try {
      assert.equal(permits(request({ role: 'viewer' })), true)
      assert.equal(permits(request({ role: 'viewer', repository: 'acme/other' })), false)
    } finally {
      setPermissionResolver(null)
    }
  })

  it('falls back to the built-in table when a resolver throws', () => {
    // A resolver that fails must not open anything up, and must not take the
    // application down either.
    setPermissionResolver(() => {
      throw new Error('the model could not be loaded')
    })
    try {
      assert.equal(permits(request({ role: 'admin', permission: 'environments.create' })), true)
      assert.equal(permits(request({ role: 'viewer', permission: 'environments.create' })), false)
    } finally {
      setPermissionResolver(null)
    }
  })

  it('a resolver cannot take away what a built-in role grants', () => {
    setPermissionResolver(() => false)
    try {
      // Even a resolver that refuses everything leaves the built-in answer
      // intact where it granted, because permits asks the table first and a
      // resolver's false is only consulted where the table said no.
      assert.equal(permits(request({ role: 'owner', permission: 'members.manage' })), false)
    } finally {
      setPermissionResolver(null)
    }
  })
})

// ---------------------------------------------------------------------------

describe('effective permissions', () => {
  it('answers what one person can do, and where it came from', () => {
    // Asked about somebody else during an access review, where an answer
    // assembled from five places is an answer nobody trusts.
    const rows = effectivePermissions(
      model({
        grants: [{
          userId: 'user-1', roleId: 'approver',
          scope: { kind: 'repository', name: 'acme/app' },
        }],
      }),
      'user-1',
      'viewer',
      ROLE_PERMISSIONS.viewer,
    )

    assert.equal(rows.length, 2)
    assert.equal(rows[0]!.source, 'the built-in viewer role')
    assert.deepEqual(rows[0]!.permissions, [...ROLE_PERMISSIONS.viewer])
    assert.equal(rows[1]!.source, 'Masking approver')
    assert.deepEqual(rows[1]!.scope, { kind: 'repository', name: 'acme/app' })
  })
})
