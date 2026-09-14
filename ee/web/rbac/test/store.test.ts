// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

// The read that runs on every refused request, and the rule that it may only
// ever fail towards granting less.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import type { PermissionRequest } from '@antifailure/api'
import { assemble, resolverFor } from '../src/index.ts'

function request(over: Partial<PermissionRequest> = {}): PermissionRequest {
  return {
    orgId: 'org-1', userId: 'user-1', role: 'viewer',
    permission: 'environments.create', repository: 'acme/app', envId: null,
    ...over,
  }
}

const role = (key: string, permission: string | null) => ({
  role_key: key, name: key, description: `${key} role`, permission,
})
const grant = (key: string, kind: string, name: string | null) => ({
  role_key: key, user_id: 'user-1', scope_kind: kind, scope_name: name,
})

describe('reading a stored model', () => {
  it('drops a permission the catalogue does not have, and keeps the rest of the role', () => {
    // A row the write path could never have produced: a permission renamed out
    // of the catalogue after it was stored. validate() refuses a model carrying
    // it, so a read that kept it would throw on every request and take every
    // other grant in the organization down with it.
    const model = assemble(
      [role('deployer', 'environments.create'), role('deployer', 'environments.launch')],
      [grant('deployer', 'repository', 'acme/app')],
      [],
    )
    assert.equal(resolverFor(model)(request()), true)
  })

  it('drops a grant whose group has gone, and keeps the grant beside it', () => {
    const model = assemble(
      [role('deployer', 'environments.create')],
      [grant('deployer', 'group', 'payments'), grant('deployer', 'repository', 'acme/app')],
      [],
    )
    assert.equal(model.grants.length, 1)
    assert.equal(resolverFor(model)(request()), true)
  })

  it('drops a grant whose role has gone rather than refusing the model', () => {
    const model = assemble(
      [role('deployer', 'environments.create')],
      [grant('vanished', 'organization', null), grant('deployer', 'repository', 'acme/app')],
      [],
    )
    assert.deepEqual(model.grants.map((g) => g.roleId), ['deployer'])
    assert.equal(resolverFor(model)(request()), true)
  })

  it('drops a grant with a scope kind it does not know, and never reads it as organization wide', () => {
    const model = assemble(
      [role('deployer', 'environments.create')],
      [grant('deployer', 'team', 'platform')],
      [],
    )
    assert.equal(model.grants.length, 0)
    assert.equal(resolverFor(model)(request({ repository: 'acme/anything' })), undefined)
  })
})
