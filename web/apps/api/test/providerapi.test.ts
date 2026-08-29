// `af provider`, from the server's side.
//
// The store itself is tested in providerkeys.test.ts. What is tested here is
// the part that is new when the same capability is reachable from a terminal
// instead of a browser, and it is the part that decides whether exposing it was
// safe:
//
//   - a token minted by a plain `af login` cannot touch a provider key, so the
//     capability is not quietly attached to every terminal that ever signed in;
//   - the SCOPE says what the token may do and the ROLE says what the person
//     may do, and both are checked, because a member who cannot rotate a key in
//     the console must not be able to rotate one from a shell;
//   - no route here ever returns a key, and no scope exists that would grant
//     one -- storing a secret and reading a secret are different capabilities;
//   - and the audit entry says the change came from a terminal, because "the
//     key was rotated in the console by somebody signed in" and "a token on a
//     build machine rotated it" are the same row without that and different
//     incidents with it.
//
// Every request below goes through the real device grant to get its token. A
// test that inserted a row into engine_tokens would prove the endpoints work
// for tokens the endpoints will never see.

import { test, describe, before, after, beforeEach } from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes } from 'node:crypto'
import {
  available,
  startApi,
  seedOrg,
  dropOrg,
  signInAs,
  type ApiHarness,
  type Org,
} from './harness.ts'
import type { Role } from '../src/permissions.ts'
import { CSRF_HEADER } from '../src/auth/session.ts'
import { DEVICE_POLL_INTERVAL_SECONDS } from '../src/auth/device.ts'

// Assembled rather than written out, by the same rule as providerkeys.test.ts:
// tools/scanrepo refuses a repository carrying anything its detector reads as a
// live credential, and a literal fixture is a repository that fails its own gate.
const ANTHROPIC = ['sk', 'ant', 'api03'].join('-')
const OPENAI = ['sk', 'proj'].join('-')
const KEY_ONE = `${ANTHROPIC}-aaaaaaaaaaaaaaaaaaaaaaaaaaaa1111`
const KEY_TWO = `${ANTHROPIC}-bbbbbbbbbbbbbbbbbbbbbbbbbbbb2222`
const OPENAI_KEY = `${OPENAI}-cccccccccccccccccccccccccccc3333`

describe('provider keys from a terminal', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let api: ApiHarness
  let org: Org
  const sealingKey = randomBytes(32)

  before(async () => {
    api = await startApi({ sealingKey })
    org = await seedOrg(api.admin, 'byok-cli')
  })
  after(async () => {
    await dropOrg(api.admin, org.orgId)
    await api.close()
  })

  beforeEach(async () => {
    await api.admin`DELETE FROM provider_keys WHERE org_id = ${org.orgId}`
    await api.admin`DELETE FROM provider_budgets WHERE org_id = ${org.orgId}`
  })

  /** A real CLI token, obtained the way `af login` obtains one. */
  async function tokenFor(role: Role, scopes: string[], label = 'cli'): Promise<string> {
    const started = await api.fetch('/auth/device/code', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ clientLabel: 'a test terminal', scopes }),
    })
    assert.equal(started.status, 200)
    const codes = (await started.json()) as { device_code: string; user_code: string }

    const person = await signInAs(api, org, role, label)
    const approved = await api.fetch('/auth/device/approve', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: person.cookie,
        [CSRF_HEADER]: person.csrfToken,
      },
      body: JSON.stringify({ user_code: codes.user_code }),
    })
    assert.equal(approved.status, 200)

    api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)
    const granted = await api.fetch('/auth/device/token', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ device_code: codes.device_code }),
    })
    assert.equal(granted.status, 200)
    return ((await granted.json()) as { access_token: string }).access_token
  }

  async function call(
    token: string | null,
    method: string,
    path: string,
    body?: unknown,
  ): Promise<{ status: number; text: string; json: Record<string, unknown> }> {
    const headers: Record<string, string> = {}
    if (token) headers.authorization = `Bearer ${token}`
    if (body !== undefined) headers['content-type'] = 'application/json'
    const res = await api.fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await res.text()
    let parsed: Record<string, unknown> = {}
    try {
      parsed = JSON.parse(text) as Record<string, unknown>
    } catch {
      // Left empty. A route that answered with something other than JSON is a
      // finding, and the raw text is asserted on instead.
    }
    return { status: res.status, text, json: parsed }
  }

  // -------------------------------------------------------------------------
  // The three gates
  // -------------------------------------------------------------------------

  test('no token reads nothing', async () => {
    assert.equal((await call(null, 'GET', '/v1/providers')).status, 401)
    assert.equal((await call('afu_' + 'x'.repeat(43), 'GET', '/v1/providers')).status, 401)
  })

  test('a plain af login token cannot see or change a key', async () => {
    // THE DEFAULT, and the reason the two scope lists differ. Somebody who ran
    // af login months ago on a laptop that has since been lost has a token that
    // cannot reach a provider key.
    const token = await tokenFor('admin', [], 'plain')

    const read = await call(token, 'GET', '/v1/providers')
    assert.equal(read.status, 403)
    // The message names the command that fixes it. A bare "forbidden" sends
    // somebody looking for a permissions problem that does not exist.
    assert.match(String(read.json.error), /af login --scope providers\.view/)

    const write = await call(token, 'PUT', '/v1/providers/anthropic', { key: KEY_ONE })
    assert.equal(write.status, 403)
    assert.match(String(write.json.error), /af login --scope providers\.write/)
  })

  test('the view scope reads and cannot write', async () => {
    // Scope is not a single provider-shaped permission. A token for a script
    // that reports what is configured must not also be able to rotate it.
    const token = await tokenFor('admin', ['providers.view'], 'viewer-scope')
    assert.equal((await call(token, 'GET', '/v1/providers')).status, 200)
    assert.equal(
      (await call(token, 'PUT', '/v1/providers/anthropic', { key: KEY_ONE })).status,
      403,
    )
    assert.equal((await call(token, 'DELETE', '/v1/providers/anthropic')).status, 403)
    assert.equal(
      (await call(token, 'PUT', '/v1/providers/anthropic/budget', { capUsd: 5 })).status,
      403,
    )
  })

  test('a member with the scope is still refused, because role is checked too', async () => {
    // The gate that a scope check alone would miss. A member can approve a
    // device login -- that is how they signed their own terminal in -- so if
    // scope were the only check, any member could mint a token carrying
    // providers.write and change a key they cannot change in the console.
    const token = await tokenFor('member', ['providers.write'], 'member-scope')
    const res = await call(token, 'PUT', '/v1/providers/anthropic', { key: KEY_ONE })
    assert.equal(res.status, 403)
    assert.match(String(res.json.error), /owner or admin/)
    assert.match(String(res.json.error), /You are member/)
  })

  test('a member with the view scope may read, because the console shows them too', async () => {
    const token = await tokenFor('member', ['providers.view'], 'member-view')
    assert.equal((await call(token, 'GET', '/v1/providers')).status, 200)
  })

  // -------------------------------------------------------------------------
  // Doing the thing
  // -------------------------------------------------------------------------

  test('an owner stores, rotates and removes a key from a terminal', async () => {
    const token = await tokenFor('owner', ['providers.view', 'providers.write'], 'owner-cli')

    const stored = await call(token, 'PUT', '/v1/providers/anthropic', { key: KEY_ONE })
    assert.equal(stored.status, 200)
    assert.equal(stored.json.last4, '1111')
    assert.equal(stored.json.replaced, false)
    assert.equal(stored.json.sameAsBefore, false)

    const listed = await call(token, 'GET', '/v1/providers')
    assert.equal(listed.status, 200)
    const keys = listed.json.keys as { provider: string; last4: string }[]
    assert.equal(keys.length, 1)
    assert.equal(keys[0]!.provider, 'anthropic')
    assert.equal(keys[0]!.last4, '1111')
    assert.equal(listed.json.sealing, true)

    // Rotation. The answer says it replaced one, which is what makes a script
    // able to tell "I added a key" from "I overwrote somebody's".
    const rotated = await call(token, 'PUT', '/v1/providers/anthropic', { key: KEY_TWO })
    assert.equal(rotated.status, 200)
    assert.equal(rotated.json.replaced, true)
    assert.equal(rotated.json.last4, '2222')

    // One live key per provider, so rotating replaced rather than added.
    const after = await call(token, 'GET', '/v1/providers')
    assert.equal((after.json.keys as unknown[]).length, 1)

    const removed = await call(token, 'DELETE', '/v1/providers/anthropic')
    assert.equal(removed.status, 200)
    assert.equal(removed.json.revoked, true)
    assert.equal(((await call(token, 'GET', '/v1/providers')).json.keys as unknown[]).length, 0)
  })

  test('pasting the key that is already there is said out loud', async () => {
    // The mistake somebody makes at the exact moment they believe they have
    // just rotated a leaked key. Accepting it silently would report success for
    // an action that changed nothing.
    const token = await tokenFor('owner', ['providers.write'], 'same-key')
    await call(token, 'PUT', '/v1/providers/anthropic', { key: KEY_ONE })
    const again = await call(token, 'PUT', '/v1/providers/anthropic', { key: KEY_ONE })
    assert.equal(again.status, 200)
    assert.equal(again.json.sameAsBefore, true)
  })

  test('removing a key twice is not an error', async () => {
    // Idempotent on purpose: this is the call somebody makes in a hurry when a
    // key has leaked, and a retry after a timeout must not report failure for
    // reaching the state they asked for.
    const token = await tokenFor('owner', ['providers.write'], 'idempotent')
    await call(token, 'PUT', '/v1/providers/anthropic', { key: KEY_ONE })
    assert.equal((await call(token, 'DELETE', '/v1/providers/anthropic')).json.revoked, true)
    const second = await call(token, 'DELETE', '/v1/providers/anthropic')
    assert.equal(second.status, 200)
    assert.equal(second.json.revoked, false)
  })

  test('a cap is set and read back with what has been spent against it', async () => {
    const token = await tokenFor('owner', ['providers.view', 'providers.write'], 'budget-cli')
    const set = await call(token, 'PUT', '/v1/providers/openai/budget', { capUsd: 25 })
    assert.equal(set.status, 200)
    assert.equal(set.json.capUsd, 25)
    assert.equal(set.json.spentUsd, 0)
    assert.equal(set.json.remainingUsd, 25)

    const budgets = (await call(token, 'GET', '/v1/providers')).json.budgets as {
      provider: string
      capUsd: number
    }[]
    assert.equal(budgets.length, 1)
    assert.equal(budgets[0]!.provider, 'openai')
    assert.equal(budgets[0]!.capUsd, 25)
  })

  test('a negative cap is refused', async () => {
    const token = await tokenFor('owner', ['providers.write'], 'bad-cap')
    // null and '' are in this list because Number() turns both into 0, and a
    // silent cap of zero dollars is indistinguishable from a working setup
    // until every run refuses. This caught exactly that.
    for (const capUsd of [-1, 'lots', null, '', true, [], {}]) {
      const res = await call(token, 'PUT', '/v1/providers/anthropic/budget', { capUsd })
      assert.equal(res.status, 400, `capUsd ${String(capUsd)} should be refused`)
    }
  })

  // -------------------------------------------------------------------------
  // Refusing what is not a key
  // -------------------------------------------------------------------------

  test('the wrong provider is caught, and the complaint does not echo the key', async () => {
    // The mistake people actually make. The message has to be specific enough
    // to fix and must not quote the value: an error body is logged by every
    // HTTP client there is.
    const token = await tokenFor('owner', ['providers.write'], 'wrong-provider')
    const res = await call(token, 'PUT', '/v1/providers/openai', { key: KEY_ONE })
    assert.equal(res.status, 400)
    assert.match(String(res.json.error), /Anthropic key/)
    assert.ok(!res.text.includes(KEY_ONE))
    assert.ok(!res.text.includes('aaaaaaaa'))
  })

  test('an unknown provider is refused before anything is stored', async () => {
    const token = await tokenFor('owner', ['providers.write'], 'unknown-provider')
    const res = await call(token, 'PUT', '/v1/providers/gemini', { key: KEY_ONE })
    assert.equal(res.status, 400)
    assert.match(String(res.json.error), /anthropic, openai/)
  })

  test('a body with no key is refused', async () => {
    const token = await tokenFor('owner', ['providers.write'], 'no-key')
    assert.equal((await call(token, 'PUT', '/v1/providers/anthropic', {})).status, 400)
    assert.equal((await call(token, 'PUT', '/v1/providers/anthropic', { key: '  ' })).status, 400)
  })

  // -------------------------------------------------------------------------
  // The key never comes back
  // -------------------------------------------------------------------------

  test('nothing this surface answers with contains the key', async () => {
    // Asserted by grepping what the routes actually wrote, rather than by
    // reading them. Both providers, every route, both the success and the
    // failure path.
    const token = await tokenFor('owner', ['providers.view', 'providers.write'], 'no-leak')
    const bodies: string[] = []
    bodies.push((await call(token, 'PUT', '/v1/providers/anthropic', { key: KEY_ONE })).text)
    bodies.push((await call(token, 'PUT', '/v1/providers/openai', { key: OPENAI_KEY })).text)
    bodies.push((await call(token, 'PUT', '/v1/providers/anthropic/budget', { capUsd: 10 })).text)
    bodies.push((await call(token, 'GET', '/v1/providers')).text)
    bodies.push((await call(token, 'PUT', '/v1/providers/anthropic', { key: 'sk-ant-x' })).text)
    bodies.push((await call(token, 'DELETE', '/v1/providers/anthropic')).text)
    bodies.push((await call(token, 'GET', '/v1/whoami')).text)

    for (const body of bodies) {
      for (const secret of [KEY_ONE, OPENAI_KEY]) {
        assert.ok(!body.includes(secret), `a response carried a key: ${body.slice(0, 200)}`)
      }
      // And not a long fragment of one either, which is what a truncated echo
      // would look like.
      assert.ok(!body.includes('aaaaaaaaaaaa'))
      assert.ok(!body.includes('cccccccccccc'))
    }
  })

  test('there is no route that reads a key back', async () => {
    // The negative control for the test above, which would pass on a surface
    // that had such a route as long as nothing here called it. Walks the
    // server's own route table.
    const paths = api.app.routes
      .filter((r) => r.path.startsWith('/v1/providers'))
      .map((r) => `${r.method} ${r.path}`)
    assert.deepEqual(paths.sort(), [
      'DELETE /v1/providers/:provider',
      'GET /v1/providers',
      'PUT /v1/providers/:provider',
      'PUT /v1/providers/:provider/budget',
    ])
  })

  // -------------------------------------------------------------------------
  // The record
  // -------------------------------------------------------------------------

  test('the audit entry says the change came from a terminal', async () => {
    const token = await tokenFor('owner', ['providers.write'], 'audit-origin')
    // The audit log is append-only by design, so earlier tests in this file
    // have already written to it. Read from here rather than truncating: a test
    // that deleted audit rows would be a test asserting the table is mutable.
    const [head] = await api.admin<{ seq: number | null }[]>`
      SELECT max(seq) AS seq FROM audit_entries WHERE org_id = ${org.orgId}`
    const from = head?.seq ?? 0

    await call(token, 'PUT', '/v1/providers/anthropic', { key: KEY_ONE })
    await call(token, 'PUT', '/v1/providers/anthropic/budget', { capUsd: 5 })
    await call(token, 'DELETE', '/v1/providers/anthropic')

    const rows = await api.admin<{ action: string; origin: string; detail: unknown }[]>`
      SELECT action, origin, detail FROM audit_entries
      WHERE org_id = ${org.orgId} AND seq > ${from} AND action LIKE 'provider_%'
      ORDER BY seq`
    assert.deepEqual(
      rows.map((r) => [r.action, r.origin]),
      [
        ['provider_key.stored', 'cli'],
        ['provider_budget.set', 'cli'],
        ['provider_key.revoked', 'cli'],
      ],
    )
    // And the record itself carries no key, which is the whole point of a
    // record an operator reads over somebody's shoulder.
    assert.ok(!JSON.stringify(rows).includes(KEY_ONE))
  })

  test('the actor is the person, not "a terminal"', async () => {
    // A token acts for somebody. An audit trail that recorded the machine would
    // answer "which laptop" when the question is always "who".
    const token = await tokenFor('owner', ['providers.write'], 'named-actor')
    await call(token, 'PUT', '/v1/providers/anthropic', { key: KEY_ONE })
    const [row] = await api.admin<{ actor_label: string }[]>`
      SELECT actor_label FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'provider_key.stored'
      ORDER BY seq DESC LIMIT 1`
    assert.match(row!.actor_label, /^named-actor-/)
  })

  // -------------------------------------------------------------------------
  // Tenancy
  // -------------------------------------------------------------------------

  test('a token cannot reach another organization', async () => {
    const other = await seedOrg(api.admin, 'byok-cli-other')
    try {
      const mine = await tokenFor('owner', ['providers.view', 'providers.write'], 'tenant-mine')
      await call(mine, 'PUT', '/v1/providers/anthropic', { key: KEY_ONE })

      // There is no organization in the path at all -- the tenant comes from
      // the token -- so the only way to try is with the other tenant's token,
      // and it sees its own empty state rather than this one.
      const started = await api.fetch('/auth/device/code', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ clientLabel: 'other', scopes: ['providers.view'] }),
      })
      const codes = (await started.json()) as { device_code: string; user_code: string }
      const stranger = await signInAs(api, other, 'owner', 'tenant-other')
      await api.fetch('/auth/device/approve', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          cookie: stranger.cookie,
          [CSRF_HEADER]: stranger.csrfToken,
        },
        body: JSON.stringify({ user_code: codes.user_code }),
      })
      api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)
      const granted = await api.fetch('/auth/device/token', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ device_code: codes.device_code }),
      })
      const theirs = ((await granted.json()) as { access_token: string }).access_token

      const seen = await call(theirs, 'GET', '/v1/providers')
      assert.equal(seen.status, 200)
      assert.deepEqual(seen.json.keys, [])
      assert.ok(!seen.text.includes('1111'))
    } finally {
      await dropOrg(api.admin, other.orgId)
    }
  })

  // -------------------------------------------------------------------------
  // No sealing secret
  // -------------------------------------------------------------------------

  test('an installation with no sealing secret says so instead of failing on save', async () => {
    // Reported by the read, so `af provider list` on such an installation
    // explains itself rather than looking merely empty, and refused by the
    // write with a 503 rather than a stack trace.
    const bare = await startApi()
    const bareOrg = await seedOrg(bare.admin, 'byok-cli-bare')
    try {
      const started = await bare.fetch('/auth/device/code', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ scopes: ['providers.view', 'providers.write'] }),
      })
      const codes = (await started.json()) as { device_code: string; user_code: string }
      const owner = await signInAs(bare, bareOrg, 'owner', 'bare-owner')
      await bare.fetch('/auth/device/approve', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          cookie: owner.cookie,
          [CSRF_HEADER]: owner.csrfToken,
        },
        body: JSON.stringify({ user_code: codes.user_code }),
      })
      bare.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)
      const granted = await bare.fetch('/auth/device/token', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ device_code: codes.device_code }),
      })
      const token = ((await granted.json()) as { access_token: string }).access_token

      const read = await bare.fetch('/v1/providers', {
        headers: { authorization: `Bearer ${token}` },
      })
      assert.equal(read.status, 200)
      assert.equal(((await read.json()) as { sealing: boolean }).sealing, false)

      const write = await bare.fetch('/v1/providers/anthropic', {
        method: 'PUT',
        headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
        body: JSON.stringify({ key: KEY_ONE }),
      })
      assert.equal(write.status, 503)
      assert.match(String(((await write.json()) as { error: string }).error), /AF_PROVIDER_KEY_SECRET/)
    } finally {
      await dropOrg(bare.admin, bareOrg.orgId)
      await bare.close()
    }
  })
})
