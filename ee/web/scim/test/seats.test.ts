// The seat limit, on the provisioning path that never had one.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// AF-EE-004 says the licence covers N seats and they are all in use, and
// ee/web/sso has refused an addition past that number since the limit was
// wired. This package never took a seat limit at all: the word "seat" appeared
// nowhere in it, scimExtension was constructed without one while ssoExtension
// was handed the licence's own number, and a directory sync could add members
// without bound on a licence sold with a count. A limit that one of two
// provisioning paths enforces is a limit with a documented way around it.
//
// Every case here drives real HTTP through the mounted extension against a real
// database, and asserts the MEMBERSHIP rather than the response alone: a refusal
// that still added the member, or an acceptance that added nobody, would both
// pass a test that read only the status code.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { available, dropTenant, patch, seedTenant, start, user, type Harness, type Tenant } from './harness.ts'

const hasDatabase = await available()

describe('the licence seat limit', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: Harness
  let acme: Tenant
  // Read per request by the option the extension is given, so a case can change
  // the licence's number between calls the way a licence change would.
  let seats: number | null = null

  before(async () => {
    h = await start({ seats: async () => seats })
    acme = await seedTenant(h, 'seats')
  })
  after(async () => {
    await dropTenant(h, acme.orgId)
    await h.close()
  })

  const address = (name: string) => `${name}@${acme.slug}.test`

  async function post(name: string, extra: Record<string, unknown> = {}): Promise<Response> {
    return h.scim(acme.token, '/scim/v2/Users', { method: 'POST', body: user(address(name), extra) })
  }

  async function members(): Promise<string[]> {
    const rows = await h.admin<{ email: string }[]>`
      SELECT u.email FROM members m JOIN users u ON u.id = m.user_id
      WHERE m.org_id = ${acme.orgId} ORDER BY u.email`
    return rows.map((r) => r.email)
  }

  /**
   * The status, and the body read ONCE.
   *
   * assert.equal's message argument is evaluated eagerly, so passing
   * `await res.text()` reads the body on every call including the passing ones,
   * and a later res.json() then throws "Body has already been read". The failure
   * reads as a broken endpoint and is a broken test. harness.ts says the same
   * thing about its own expect helper.
   */
  async function body(response: Response, status: number): Promise<string> {
    const text = await response.text()
    if (response.status !== status) {
      assert.fail(`expected ${status}, got ${response.status}: ${text}`)
    }
    return text
  }

  it('refuses the addition that would exceed the seats, and adds nobody', async () => {
    seats = 1
    const first = await post('one')
    await body(first, 201)

    const refused = await body(await post('two'), 403)
    // The code, because a support conversation starts from it, and the sentence
    // that says nobody was evicted to make room.
    assert.match(refused, /AF-EE-004/)
    assert.match(refused, /No existing member was removed/)

    assert.deepEqual(await members(), [address('one')], 'the refused addition still added a member')
  })

  it('does not spend a seat on an inactive resource', async () => {
    seats = 1
    await body(await post('three', { active: false }), 201)
    assert.deepEqual(await members(), [address('one')], 'an inactive resource became a member')
  })

  it('refuses a reactivation past the seats, which is an addition too', async () => {
    seats = 2
    const created = JSON.parse(await body(await post('four'), 201)) as { id: string }
    assert.deepEqual(await members(), [address('four'), address('one')])

    // Deprovision, which frees the seat.
    const off = await h.scim(acme.token, `/scim/v2/Users/${created.id}`, {
      method: 'PATCH',
      body: patch([{ op: 'replace', path: 'active', value: false }]),
    })
    await body(off, 200)
    assert.deepEqual(await members(), [address('one')])

    // Somebody else takes it.
    await body(await post('five'), 201)

    // Now the reactivation has nowhere to go.
    const back = await h.scim(acme.token, `/scim/v2/Users/${created.id}`, {
      method: 'PATCH',
      body: patch([{ op: 'replace', path: 'active', value: true }]),
    })
    const refusal = await body(back, 403)
    assert.match(refusal, /AF-EE-004/)
    assert.deepEqual(await members(), [address('five'), address('one')], 'the refused reactivation added a member')
  })

  it('treats no seat count as unlimited, which is what an unmetered licence is', async () => {
    seats = null
    const before = (await members()).length
    for (const name of ['six', 'seven', 'eight']) {
      await body(await post(name), 201)
    }
    assert.equal((await members()).length, before + 3, 'an unmetered licence did not add every member')
  })
})
