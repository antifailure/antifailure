// SCIM, driven the way Okta and Entra ID drive it, in every order they do it.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// This file is organised by ORDERING rather than by endpoint, because that is
// where provisioning breaks. A suite that tests create, then read, then update,
// then delete proves the states and never the sequences, and every one of the
// orderings below happens within days of a real directory being connected:
//
//   created then updated       the ordinary one
//   updated then created       a PATCH for somebody who is not here yet
//   deleted then re-added      a rehire, or a sync that overlapped itself
//   deleted then deleted       the retry, which must not be an error twice
//   membership before the user  Okta and Entra ID both do this, always
//
// The last one is the one implementations get wrong, and getting it wrong is
// invisible: the group is simply missing somebody forever, every response was
// 200, and nothing is logged.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { available, dropTenant, patch, seedTenant, start, user, type Harness, type Tenant } from './harness.ts'

const hasDatabase = await available()

describe('SCIM provisioning', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: Harness
  let acme: Tenant
  let other: Tenant

  before(async () => {
    h = await start()
    acme = await seedTenant(h, 'acme')
    other = await seedTenant(h, 'other')
  })
  after(async () => {
    await dropTenant(h, acme.orgId)
    await dropTenant(h, other.orgId)
    await h.close()
  })

  const address = (name: string) => `${name}@${acme.slug}.test`

  /**
   * Asserts the status and returns the body.
   *
   * Not `assert.equal(res.status, 201, await res.text())`: the message argument
   * is evaluated eagerly, so that reads the body on every call including the
   * successful ones, and the `res.json()` after it fails with "Body has already
   * been read". The failure looks like a broken endpoint and is a broken test.
   */
  async function expect(response: Response, status: number): Promise<Record<string, any>> {
    if (response.status !== status) {
      assert.fail(`expected ${status}, got ${response.status}: ${await response.text()}`)
    }
    return (await response.json()) as Record<string, any>
  }

  async function createUser(name: string, extra: Record<string, unknown> = {}) {
    const response = await h.scim(acme.token, '/scim/v2/Users', {
      method: 'POST',
      body: user(address(name), extra),
    })
    return (await expect(response, 201)) as { id: string; userName: string; active: boolean }
  }

  async function members(orgId: string): Promise<string[]> {
    const rows = await h.admin<{ email: string }[]>`
      SELECT u.email FROM members m JOIN users u ON u.id = m.user_id
      WHERE m.org_id = ${orgId} ORDER BY u.email`
    return rows.map((r) => r.email)
  }

  // -------------------------------------------------------------------------

  it('the positive control: a created user is a member who could sign in', async () => {
    const created = await createUser('ada')
    assert.equal(created.userName, address('ada'))
    assert.equal(created.active, true)

    // The observable effect, not the 201. A SCIM resource that is not a
    // membership is a row nothing reads.
    assert.ok((await members(acme.orgId)).includes(address('ada')))

    const read = await h.scim(acme.token, `/scim/v2/Users/${created.id}`)
    assert.equal(read.status, 200)
    assert.equal(read.headers.get('etag'), 'W/"1"')
  })

  it('created then updated', async () => {
    const created = await createUser('grace')
    const response = await h.scim(acme.token, `/scim/v2/Users/${created.id}`, {
      method: 'PATCH',
      body: patch([{ op: 'replace', path: 'displayName', value: 'Grace Hopper' }]),
    })
    const body = await expect(response, 200)
    assert.equal(body.displayName, 'Grace Hopper')
    assert.equal(response.headers.get('etag'), 'W/"2"', 'the version did not move')
  })

  it('updated then created: a PATCH for somebody who is not here is a 404 and creates nothing', async () => {
    const ghost = '00000000-0000-4000-8000-000000000000'
    const response = await h.scim(acme.token, `/scim/v2/Users/${ghost}`, {
      method: 'PATCH',
      body: patch([{ op: 'replace', value: { active: false } }]),
    })
    assert.equal(response.status, 404)

    // The failure that would hide here is a PATCH that upserts. A directory
    // that PATCHes before it creates would then produce a user with no
    // userName and no membership, and the create that follows would collide
    // with it.
    const listed = await h.scim(acme.token, '/scim/v2/Users?count=200')
    const list = (await listed.json()) as Record<string, any>
    assert.ok(
      !list.Resources.some((r: { id: string }) => r.id === ghost),
      'a PATCH for an unknown id created a resource',
    )
  })

  it('deleted then re-added: the address is free again and the person is a member again', async () => {
    const first = await createUser('alan')
    const removed = await h.scim(acme.token, `/scim/v2/Users/${first.id}`, { method: 'DELETE' })
    assert.equal(removed.status, 204)
    assert.ok(!(await members(acme.orgId)).includes(address('alan')), 'access survived the delete')

    // A rehire. The userName is free, and the new resource is a new id.
    const second = await createUser('alan')
    assert.notEqual(second.id, first.id)
    assert.ok((await members(acme.orgId)).includes(address('alan')))
  })

  it('deleted then deleted: the retry is a 404 and not an alarm', async () => {
    const created = await createUser('katherine')
    assert.equal((await h.scim(acme.token, `/scim/v2/Users/${created.id}`, { method: 'DELETE' })).status, 204)
    // Deprovisioning is the operation most likely to arrive twice. The
    // specification says 404 and every provider treats it as done.
    assert.equal((await h.scim(acme.token, `/scim/v2/Users/${created.id}`, { method: 'DELETE' })).status, 404)
  })

  it('membership before the user: the reference is kept and resolved when they arrive', async () => {
    // Exactly what Okta and Entra ID do. An implementation that resolves
    // members at write time drops this person from the group forever, answers
    // 201, and logs nothing.
    const pending = 'external-id-for-margaret'
    const groupResponse = await h.scim(acme.token, '/scim/v2/Groups', {
      method: 'POST',
      body: JSON.stringify({
        schemas: ['urn:ietf:params:scim:schemas:core:2.0:Group'],
        displayName: 'Apollo',
        members: [{ value: pending }],
      }),
    })
    const group = await expect(groupResponse, 201)

    // The member is reported even though nobody here matches it yet. Reporting
    // a smaller group than the provider believes is how a reconciliation job
    // decides to add everybody again.
    assert.equal(group.members.length, 1)
    assert.equal(group.members[0].value, pending)

    const created = await createUser('margaret', { externalId: pending })

    const after = await h.scim(acme.token, `/scim/v2/Groups/${group.id}`)
    const resolved = (await after.json()) as Record<string, any>
    assert.equal(resolved.members.length, 1)
    assert.equal(
      resolved.members[0].value,
      created.id,
      'the pending membership was not resolved when the user was created',
    )
    assert.equal(resolved.members[0].display, address('margaret'))
  })

  // -------------------------------------------------------------------------
  // The provider variants
  // -------------------------------------------------------------------------

  it("deactivates on Okta's shape: replace with no path", async () => {
    const created = await createUser('okta-user')
    const response = await h.scim(acme.token, `/scim/v2/Users/${created.id}`, {
      method: 'PATCH',
      body: patch([{ op: 'replace', value: { active: false } }]),
    })
    assert.equal((await expect(response, 200)).active, false)
    assert.ok(
      !(await members(acme.orgId)).includes(address('okta-user')),
      'deactivation left the membership in place',
    )
  })

  it('deactivates on Entra ID\'s shape: capitalised op and the string "False"', async () => {
    // The one that silently does nothing. JSON.parse gives the string "False",
    // which is truthy, so an implementation writing Boolean(value) deactivates
    // NOBODY while answering 200 to every request.
    const created = await createUser('entra-user')
    const response = await h.scim(acme.token, `/scim/v2/Users/${created.id}`, {
      method: 'PATCH',
      body: patch([{ op: 'Replace', path: 'active', value: 'False' }]),
    })
    assert.equal((await expect(response, 200)).active, false)
    assert.ok(!(await members(acme.orgId)).includes(address('entra-user')))
  })

  it("deactivates on Entra ID's other shape: the value wrapped in an array", async () => {
    const created = await createUser('entra-wrapped')
    const response = await h.scim(acme.token, `/scim/v2/Users/${created.id}`, {
      method: 'PATCH',
      body: patch([{ op: 'Replace', path: 'active', value: [{ value: 'False' }] }]),
    })
    assert.equal((await expect(response, 200)).active, false)
  })

  it('reactivates, restoring the membership', async () => {
    const created = await createUser('returning')
    await h.scim(acme.token, `/scim/v2/Users/${created.id}`, {
      method: 'PATCH',
      body: patch([{ op: 'replace', path: 'active', value: false }]),
    })
    assert.ok(!(await members(acme.orgId)).includes(address('returning')))

    const back = await h.scim(acme.token, `/scim/v2/Users/${created.id}`, {
      method: 'PATCH',
      body: patch([{ op: 'replace', path: 'active', value: true }]),
    })
    assert.equal(back.status, 200)
    assert.ok((await members(acme.orgId)).includes(address('returning')))
  })

  it('removes one group member with a filter in the path, not all of them', async () => {
    // members[value eq "x"] is Entra ID's shape. Without the selector this
    // becomes a pathless remove, which the specification says removes
    // EVERYTHING, and which would silently empty the group.
    const one = await createUser('keeper')
    const two = await createUser('leaver')
    const groupResponse = await h.scim(acme.token, '/scim/v2/Groups', {
      method: 'POST',
      body: JSON.stringify({
        schemas: ['urn:ietf:params:scim:schemas:core:2.0:Group'],
        displayName: 'Gemini',
        members: [{ value: one.id }, { value: two.id }],
      }),
    })
    const group = (await groupResponse.json()) as Record<string, any>
    assert.equal(group.members.length, 2)

    const removed = await h.scim(acme.token, `/scim/v2/Groups/${group.id}`, {
      method: 'PATCH',
      body: patch([{ op: 'remove', path: `members[value eq "${two.id}"]` }]),
    })
    const after = await expect(removed, 200)
    assert.deepEqual(
      after.members.map((m: { value: string }) => m.value),
      [one.id],
      'removing one member removed the wrong number of them',
    )
  })

  it('refuses an operation it does not understand rather than skipping it', async () => {
    // The difference between a 400 and a silent 200 is the difference between
    // a provider that shows an administrator an error and a provider that
    // records a change nobody applied.
    const created = await createUser('unknown-attr')
    const response = await h.scim(acme.token, `/scim/v2/Users/${created.id}`, {
      method: 'PATCH',
      body: patch([{ op: 'replace', path: 'somethingNobodyImplemented', value: 'x' }]),
    })
    assert.equal(response.status, 400)
    const body = (await response.json()) as Record<string, any>
    assert.equal(body.scimType, 'invalidPath')
    assert.equal(body.status, '400')
  })

  it('accepts and ignores the profile attributes a directory sends anyway', async () => {
    const created = await createUser('profile')
    const response = await h.scim(acme.token, `/scim/v2/Users/${created.id}`, {
      method: 'PATCH',
      body: patch([
        { op: 'replace', path: 'title', value: 'Engineer' },
        { op: 'replace', path: 'department', value: 'Platform' },
        { op: 'replace', path: 'displayName', value: 'Profiled' },
      ]),
    })
    assert.equal((await expect(response, 200)).displayName, 'Profiled')
  })

  // -------------------------------------------------------------------------
  // Filters, tokens and tenancy
  // -------------------------------------------------------------------------

  it('finds a user by userName, which is how every provider avoids duplicates', async () => {
    await createUser('findable')
    const response = await h.scim(
      acme.token,
      `/scim/v2/Users?filter=${encodeURIComponent(`userName eq "${address('findable')}"`)}`,
    )
    assert.equal(response.status, 200)
    const body = (await response.json()) as Record<string, any>
    assert.equal(body.totalResults, 1)
    assert.equal(body.Resources[0].userName, address('findable'))
    assert.equal(body.startIndex, 1)
  })

  it('refuses a filter it cannot answer instead of returning everything', async () => {
    // A dropped filter is the worst outcome: a provider asking "who has this
    // userName" and receiving every user will create a duplicate of everybody.
    const response = await h.scim(
      acme.token,
      `/scim/v2/Users?filter=${encodeURIComponent('nickname eq "x"')}`,
    )
    assert.equal(response.status, 400)
    assert.equal(((await response.json()) as Record<string, unknown>).scimType, 'invalidFilter')
  })

  it('treats a filter as data, never as SQL', async () => {
    const nasty = `userName eq "'; DROP TABLE scim_resources; --"`
    const response = await h.scim(acme.token, `/scim/v2/Users?filter=${encodeURIComponent(nasty)}`)
    assert.equal(response.status, 200)
    assert.equal(((await response.json()) as Record<string, unknown>).totalResults, 0)

    // The table is still there, which is the assertion that matters.
    const still = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM scim_resources WHERE org_id = ${acme.orgId}`
    assert.ok(Number(still[0]!.n) > 0)
  })

  it('does not let a wildcard in a filter value match everything', async () => {
    await createUser('wildcard')
    const response = await h.scim(
      acme.token,
      `/scim/v2/Users?filter=${encodeURIComponent('userName co "%"')}`,
    )
    assert.equal(response.status, 200)
    assert.equal(((await response.json()) as Record<string, unknown>).totalResults, 0, 'a LIKE wildcard was not escaped')
  })

  it('refuses a request with no token, and one with a token that was never issued', async () => {
    const anonymous = await h.scim('', '/scim/v2/Users')
    assert.equal(anonymous.status, 401)
    assert.match(anonymous.headers.get('www-authenticate') ?? '', /Bearer/)

    const invented = await h.scim('afs_not-a-real-token', '/scim/v2/Users')
    assert.equal(invented.status, 401)
  })

  it("a token cannot see another organization's users", async () => {
    await createUser('secret')
    const response = await h.scim(other.token, '/scim/v2/Users?count=200')
    assert.equal(response.status, 200)
    const body = (await response.json()) as Record<string, any>
    const names = body.Resources.map((r: { userName: string }) => r.userName)
    assert.ok(
      !names.includes(address('secret')),
      `a SCIM token read another organization's users: ${names.join(', ')}`,
    )
  })

  it('refuses a duplicate userName with the code a provider knows how to handle', async () => {
    await createUser('duplicate')
    const again = await h.scim(acme.token, '/scim/v2/Users', {
      method: 'POST',
      body: user(address('duplicate')),
    })
    assert.equal(again.status, 409)
    assert.equal(((await again.json()) as Record<string, unknown>).scimType, 'uniqueness')
  })

  it('refuses a stale If-Match rather than clobbering a concurrent change', async () => {
    const created = await createUser('concurrent')
    await h.scim(acme.token, `/scim/v2/Users/${created.id}`, {
      method: 'PATCH',
      body: patch([{ op: 'replace', path: 'displayName', value: 'First' }]),
    })
    const stale = await h.scim(acme.token, `/scim/v2/Users/${created.id}`, {
      method: 'PATCH',
      headers: { 'if-match': 'W/"1"' },
      body: patch([{ op: 'replace', path: 'displayName', value: 'Second' }]),
    })
    assert.equal(stale.status, 412)
  })

  it('publishes what it actually supports', async () => {
    const response = await h.scim(acme.token, '/scim/v2/ServiceProviderConfig')
    assert.equal(response.status, 200)
    const config = (await response.json()) as Record<string, any>
    assert.equal(config.patch.supported, true)
    assert.equal(config.filter.supported, true)
    assert.equal(config.etag.supported, true)
    // Claiming a capability this server lacks would make a client use the path
    // that fails instead of the one that works.
    assert.equal(config.bulk.supported, false)
    assert.equal(config.sort.supported, false)
  })

  it('pages, and reports a total that is not the page size', async () => {
    const response = await h.scim(acme.token, '/scim/v2/Users?count=2&startIndex=1')
    assert.equal(response.status, 200)
    const body = (await response.json()) as Record<string, any>
    assert.equal(body.Resources.length, 2)
    assert.equal(body.itemsPerPage, 2)
    assert.ok(body.totalResults > 2, 'totalResults reported the page size')
  })
})

describe('deprovisioning latency', { skip: hasDatabase ? false : 'no database' }, () => {
  let h: Harness
  let tenant: Tenant

  before(async () => {
    h = await start()
    tenant = await seedTenant(h, 'latency')
  })
  after(async () => {
    await dropTenant(h, tenant.orgId)
    await h.close()
  })

  it('revokes live sessions in the same transaction that deactivates', async () => {
    // Spec 13.3's exit criterion is deprovisioning under five seconds end to
    // end. The way to guarantee that is not to be fast, it is to do it in the
    // same transaction: a session revoked as part of the write cannot be late.
    const created = await h.scim(tenant.token, '/scim/v2/Users', {
      method: 'POST',
      body: user(`departing@${tenant.slug}.test`),
    })
    // Status checked before the body is read: the message argument to
    // assert.equal is eager, so reading it here would consume the body the next
    // line needs.
    if (created.status !== 201) assert.fail(`create failed: ${await created.text()}`)
    const resource = (await created.json()) as Record<string, any>

    const resourceRows = await h.admin<{ user_id: string }[]>`
      SELECT user_id FROM scim_resources WHERE id = ${resource.id}`
    const userId = resourceRows[0]!.user_id

    await h.admin`
      INSERT INTO sessions (token_hash, user_id, org_id, expires_at)
      VALUES (${Buffer.from('b'.repeat(64), 'hex')}, ${userId}, ${tenant.orgId},
              now() + interval '1 day')`

    const live = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM sessions WHERE user_id = ${userId} AND revoked_at IS NULL`
    assert.equal(Number(live[0]!.n), 1)

    await h.scim(tenant.token, `/scim/v2/Users/${resource.id}`, {
      method: 'PATCH',
      body: patch([{ op: 'replace', path: 'active', value: false }]),
    })

    const after = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM sessions WHERE user_id = ${userId} AND revoked_at IS NULL`
    assert.equal(Number(after[0]!.n), 0, 'a deprovisioned user kept a live session')
  })
})
