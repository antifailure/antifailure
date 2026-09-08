// The licence gate, observed refusing.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Directory provisioning was built, tested against a real database over real
// HTTP, and gated by nothing at all: a bearer token named an organization and
// that was the whole of the authorisation. On 2026-09-08 it was one of three
// licensed features in that state, and the difference between that and the six
// that do not exist is commercially the whole point. Absent says we did not
// build it. Ungated says we built it and do not charge for it.
//
// Nothing here asserts that a check exists. Every test makes the same request
// twice with one row changed between and asserts the answer changed, because
// the reason this wave was dispatched is that two people counted enforcement
// sites by reading code and neither of them watched anything refuse.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { available, dropTenant, seedTenant, start, user, type Harness, type Tenant } from './harness.ts'

const hasDatabase = await available()

describe('the SCIM licence gate', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: Harness
  /** On a plan that does not carry directory provisioning, which is where every
   *  organization starts. */
  let free: Tenant
  /** On a plan that does. */
  let entitled: Tenant

  before(async () => {
    h = await start()
    free = await seedTenant(h, 'unentitled', 'free')
    entitled = await seedTenant(h, 'entitled')
  })
  after(async () => {
    await dropTenant(h, free.orgId)
    await dropTenant(h, entitled.orgId)
    await h.close()
  })

  async function grant(orgId: string): Promise<() => Promise<void>> {
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO entitlement_overrides
        (scope, scope_id, org_id, feature, value, reason, created_by_label)
      VALUES ('organization', ${orgId}, ${orgId}, 'scim', ${'true'}::jsonb,
              'A design partner using a capability their plan does not carry.',
              'operator@example.test')
      RETURNING id`
    return async () => {
      await h.admin`
        UPDATE entitlement_overrides
           SET revoked_at = now(), revoked_by_label = 'test@example.test',
               revoked_reason = 'The test that granted it is finished.'
         WHERE id = ${row!.id}`
    }
  }

  const create = (t: Tenant, name: string) =>
    h.scim(t.token, '/scim/v2/Users', { method: 'POST', body: user(`${name}@${t.slug}.test`) })

  it('refuses a write from a directory whose organization is not entitled', async () => {
    const allowed = await create(entitled, 'ada')
    assert.equal(allowed.status, 201, await allowed.text())

    const refused = await create(free, 'ada')
    const body = await refused.text()
    // 403 AND NOT 401, and never a 500. The caller is a robot on its own retry
    // schedule: a 401 makes it prompt somebody for a new token, which is not
    // the problem, and a 500 makes Okta and Entra retry the same request
    // forever. Both hide the actual answer behind an integration that looks
    // broken instead of unentitled.
    assert.equal(refused.status, 403, body)
    assert.match(body, /directory provisioning/)
    assert.match(body, /change the plan/)

    // NOTHING WAS WRITTEN. The status alone would be satisfied by a route that
    // provisions the member and then reports a refusal, which is the failure
    // shape this repository keeps finding: the observable effect is what the
    // assertion has to be about.
    const [row] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM members WHERE org_id = ${free.orgId}`
    assert.equal(Number(row!.n), 0, 'a refused provisioning request created a member anyway')
  })

  it('refuses the reads too, so a directory cannot enumerate what it may not write', async () => {
    // Every tenant route goes through the same authentication function, so this
    // is the assertion that the gate is in THAT function rather than in the
    // write handlers. A read left open would let a disconnected directory keep
    // listing an organization's members indefinitely.
    for (const p of ['/scim/v2/Users', '/scim/v2/Groups']) {
      const res = await h.scim(free.token, p)
      assert.equal(res.status, 403, `${p} answered ${res.status}`)
    }
  })

  it('leaves the two static documents open, and they are the only two', async () => {
    // ServiceProviderConfig and ResourceTypes describe what SCIM this server
    // speaks. They take no token, name no organization and carry no tenant
    // data, so gating them would refuse a client at the moment it is trying to
    // discover how to talk to us, and would refuse it for a reason it could
    // not read. They answer 200 to anybody and that is correct.
    for (const p of ['/scim/v2/ServiceProviderConfig', '/scim/v2/ResourceTypes']) {
      const res = await h.scim(free.token, p)
      assert.equal(res.status, 200, `${p} answered ${res.status}`)
    }

    // AND THEY ARE THE ONLY TWO. The sentence above is a judgement about two
    // paths and it would quietly become a hole the moment a third unguarded
    // route was added, so it is checked against the source rather than left as
    // a claim. Fourteen routes and one authentication function is only a
    // chokepoint while every route actually goes through it.
    const source = await readFile(
      path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'routes.ts'), 'utf8')
    const block = /const routes: ExtensionRoute\[\] = \[([\s\S]*?)\n  \]/.exec(source)
    assert.ok(block?.[1], 'the route table was not found, so nothing was checked')
    const paths = [...block[1].matchAll(/path: '([^']+)'/g)].map((m) => m[1]!)
    assert.ok(paths.length >= 14, `only ${paths.length} routes were read out of the table`)
    const unguarded = [...block[1].matchAll(/path: '([^']+)',[\s\S]{0,200}?handler: ([^\n]+)/g)]
      .filter((m) => !m[2]!.includes('guard('))
      .map((m) => m[1]!)
    assert.deepEqual(
      unguarded.sort(), ['/scim/v2/ResourceTypes', '/scim/v2/ServiceProviderConfig'],
      'a SCIM route was added that does not go through guard, so it is authenticated by ' +
        'nothing and entitled by nothing',
    )
  })

  it('still refuses an invalid token before it refuses the entitlement', async () => {
    // The ordering matters and it is the security-relevant half. Checking the
    // entitlement first would answer a stranger's guessed token with a sentence
    // about this organization's plan, which is a fact about a customer handed
    // to somebody who proved nothing.
    const res = await h.scim('afs_not-a-real-token', '/scim/v2/Users')
    assert.equal(res.status, 401, await res.text())
    assert.match(res.headers.get('www-authenticate') ?? '', /Bearer/)
  })

  it('an override turns it on for an organization whose plan does not carry it', async () => {
    const before = await create(free, 'grace')
    assert.equal(before.status, 403, await before.text())

    const revoke = await grant(free.orgId)
    try {
      const after = await create(free, 'grace')
      assert.equal(after.status, 201, await after.text())
      const [row] = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM members WHERE org_id = ${free.orgId}`
      assert.equal(Number(row!.n), 1, 'the granted request did not actually provision anybody')
    } finally {
      await revoke()
    }

    const again = await create(free, 'katherine')
    assert.equal(again.status, 403, 'revoking the override did not close the door again')
  })
})
