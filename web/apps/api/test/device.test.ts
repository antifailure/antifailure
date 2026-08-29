// Signing a terminal in.
//
// The suite is organised around the refusals rather than the happy path,
// because the happy path here is three requests and the interesting behaviour
// is entirely in what is not allowed: a code redeemed twice, a code approved by
// somebody with no tenant, a terminal asking for a scope it may not have, a
// token that outlives the membership it was granted under.
//
// The happy path is still asserted, first, as the negative control. Without it
// a server that refused everything would pass every other test in this file --
// which is the trap migration 0004's comment describes, and it is worth
// repeating here because a login flow is exactly where it hides.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomBytes } from 'node:crypto'
import {
  DEVICE_POLL_INTERVAL_SECONDS,
  newUserCode,
  normaliseUserCode,
  sweepDeviceAuthorizations,
} from '../src/auth/device.ts'
// The header name comes from the server rather than being spelled again here.
// Written out by hand it was wrong, every approve returned 403, and the test
// that asserts a MISSING header is refused still passed -- for the wrong
// reason. A constant cannot drift from the thing it names.
import { CSRF_HEADER } from '../src/auth/session.ts'
import { available, startApi, seedOrg, dropOrg, signInAs, type ApiHarness, type Org } from './harness.ts'

describe('the short code a person types', () => {
  test('never contains a character people confuse', () => {
    // O/0, I/L/1. Somebody reads this off one screen and types it into another,
    // and an ambiguous code is retyped until it is guessed by accident.
    for (let i = 0; i < 500; i++) {
      assert.doesNotMatch(newUserCode(), /[O0IL1]/, 'the alphabet leaked a confusable character')
    }
  })

  test('is XXXX-XXXX, because eight characters in a row get miscounted', () => {
    assert.match(newUserCode(), /^[A-Z0-9]{4}-[A-Z0-9]{4}$/)
  })

  test('is read back the way a person types it, in any case and with or without the dash', () => {
    assert.equal(normaliseUserCode('bcdf-ghjk'), 'BCDF-GHJK')
    assert.equal(normaliseUserCode('  BCDFGHJK '), 'BCDF-GHJK')
    assert.equal(normaliseUserCode('BCDF GHJK'), 'BCDF-GHJK')
    // Anything that is not eight characters is not a code, rather than being
    // padded or truncated into one.
    assert.equal(normaliseUserCode('BCDF'), '')
    assert.equal(normaliseUserCode('BCDFGHJKL'), '')
  })
})

describe('the device grant', { skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let api: ApiHarness
  let org: Org

  before(async () => {
    api = await startApi()
    org = await seedOrg(api.admin, 'device')
  })
  after(async () => {
    await dropOrg(api.admin, org.orgId)
    await api.close()
  })

  async function begin(body: unknown = { clientLabel: 'a laptop' }) {
    const res = await api.fetch('/auth/device/code', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
    })
    assert.equal(res.status, 200)
    return (await res.json()) as {
      device_code: string
      user_code: string
      verification_uri: string
      verification_uri_complete: string
      interval: number
    }
  }

  async function poll(deviceCode: string) {
    const res = await api.fetch('/auth/device/token', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ device_code: deviceCode }),
    })
    return { status: res.status, body: (await res.json()) as Record<string, string> }
  }

  async function approve(userCode: string, who: { cookie: string; csrfToken: string }) {
    return api.fetch('/auth/device/approve', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: who.cookie,
        [CSRF_HEADER]: who.csrfToken,
      },
      body: JSON.stringify({ user_code: userCode }),
    })
  }

  // -------------------------------------------------------------------------

  test('a terminal logs in and receives a token that identifies the person', async () => {
    const started = await begin()
    const admin = await signInAs(api, org, 'admin', 'device-admin')

    // Pending, before anyone approves.
    const pending = await poll(started.device_code)
    assert.equal(pending.status, 400)
    assert.equal(pending.body.error, 'authorization_pending')

    assert.equal((await approve(started.user_code, admin)).status, 200)

    // The poll interval is enforced, so move the clock past it rather than
    // sleeping. A test that sleeps for the real interval is a test somebody
    // eventually deletes.
    api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)

    const granted = await poll(started.device_code)
    assert.equal(granted.status, 200)
    assert.match(String(granted.body.access_token), /^afu_/)

    // And the token answers for the person, not for a machine.
    const who = await api.fetch('/v1/whoami', {
      headers: { authorization: `Bearer ${granted.body.access_token}` },
    })
    assert.equal(who.status, 200)
    const identity = (await who.json()) as { login: string; organization: string; role: string }
    assert.equal(identity.organization, org.slug)
    assert.equal(identity.role, 'admin')
  })

  test('the same device code cannot be redeemed twice', async () => {
    const started = await begin()
    const admin = await signInAs(api, org, 'admin', 'device-twice')
    await approve(started.user_code, admin)
    api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)

    const first = await poll(started.device_code)
    assert.equal(first.status, 200)

    api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)
    const second = await poll(started.device_code)
    // A device code redeemable twice mints a second token from anywhere it has
    // ever been: a shell history, a CI log, a screen recording.
    assert.equal(second.status, 400)
    assert.equal(second.body.error, 'expired_token')
  })

  test('polling faster than the interval is told to slow down, not refused', async () => {
    const started = await begin()
    await poll(started.device_code)
    // Immediately again, with no clock movement.
    const fast = await poll(started.device_code)
    assert.equal(fast.body.error, 'slow_down')
    // Distinct from authorization_pending on purpose: a client that cannot tell
    // them apart either backs off when it should not or hammers when it should
    // not.
    assert.notEqual(fast.body.error, 'authorization_pending')
  })

  test('a declined login says so, so the terminal stops instead of waiting', async () => {
    const started = await begin()
    const admin = await signInAs(api, org, 'admin', 'device-deny')
    const denied = await api.fetch('/auth/device/deny', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: admin.cookie,
        [CSRF_HEADER]: admin.csrfToken,
      },
      body: JSON.stringify({ user_code: started.user_code }),
    })
    assert.equal(denied.status, 200)

    api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)
    const after = await poll(started.device_code)
    assert.equal(after.body.error, 'access_denied')
  })

  test('approving needs a session', async () => {
    const started = await begin()
    const res = await api.fetch('/auth/device/approve', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ user_code: started.user_code }),
    })
    assert.equal(res.status, 401)
  })

  test('approving needs the CSRF header, because it is a state change from a browser', async () => {
    const started = await begin()
    const admin = await signInAs(api, org, 'admin', 'device-csrf')
    const res = await api.fetch('/auth/device/approve', {
      method: 'POST',
      headers: { 'content-type': 'application/json', cookie: admin.cookie },
      body: JSON.stringify({ user_code: started.user_code }),
    })
    assert.equal(res.status, 403)
  })

  test('a terminal asking only for scopes that do not exist is refused outright', async () => {
    // Refused here rather than at the far end. Intersecting the ask down to
    // nothing and issuing the code anyway produces a login that appears to
    // work, prints a code, waits for a person to approve it, and hands back a
    // token that can do nothing -- so the failure surfaces at the first real
    // command, several minutes and one human interaction away from its cause.
    const res = await api.fetch('/auth/device/code', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ clientLabel: 'greedy', scopes: ['members.manage', 'not-a-scope'] }),
    })
    assert.equal(res.status, 400)
    const body = (await res.json()) as { error: string; error_description: string }
    assert.equal(body.error, 'invalid_scope')
    // The message names what does exist, because the caller has to pick one.
    assert.match(body.error_description, /providers\.write/)
  })

  test('an ask that is part real is narrowed to the real part rather than refused', async () => {
    // THE INTERSECTION ITSELF, which is the property the refusal above must not
    // be mistaken for. A terminal naming a capability that does not exist
    // alongside one that does must not receive the invented one.
    const started = await begin({
      clientLabel: 'half greedy',
      scopes: ['runs.view', 'members.manage'],
    })
    const admin = await signInAs(api, org, 'admin', 'device-scope')
    await approve(started.user_code, admin)
    api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)

    const granted = await poll(started.device_code)
    assert.equal(granted.status, 200)
    assert.equal(granted.body.scope, 'runs.view')
  })

  test('provider management is grantable, and is not in the default set', async () => {
    // The gap between the two lists is the design: a plain af login cannot
    // touch a provider key, and one that can had to say so where a person
    // approving could read it.
    const plain = await begin({ clientLabel: 'a laptop' })
    const admin = await signInAs(api, org, 'admin', 'device-scope-providers')
    await approve(plain.user_code, admin)
    api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)
    const defaults = await poll(plain.device_code)
    assert.equal(defaults.status, 200)
    assert.ok(!String(defaults.body.scope).includes('providers.'))

    const asked = await begin({ clientLabel: 'a laptop', scopes: ['providers.write'] })
    // What the approval screen is handed. If this did not carry the scope, a
    // person would be approving a capability they were never shown.
    const pending = await api.fetch(`/auth/device/pending?code=${asked.user_code}`, {
      headers: { cookie: admin.cookie },
    })
    assert.equal(pending.status, 200)
    assert.deepEqual(((await pending.json()) as { scopes: string[] }).scopes, ['providers.write'])

    await approve(asked.user_code, admin)
    api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)
    const granted = await poll(asked.device_code)
    assert.equal(granted.status, 200)
    assert.equal(granted.body.scope, 'providers.write')
  })

  test('an unknown device code is refused without saying why', async () => {
    const made_up = await poll('afd_' + 'a'.repeat(43))
    assert.equal(made_up.status, 400)
    assert.equal(made_up.body.error, 'expired_token')
  })

  // -------------------------------------------------------------------------
  // What the token is afterwards
  // -------------------------------------------------------------------------

  test('af logout revokes it everywhere, not just on the machine that ran it', async () => {
    const started = await begin()
    const admin = await signInAs(api, org, 'admin', 'device-logout')
    await approve(started.user_code, admin)
    api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)
    const token = String((await poll(started.device_code)).body.access_token)

    const out = await api.fetch('/v1/logout', {
      method: 'POST',
      headers: { authorization: `Bearer ${token}` },
    })
    assert.equal(out.status, 200)

    const after = await api.fetch('/v1/whoami', { headers: { authorization: `Bearer ${token}` } })
    assert.equal(after.status, 401)
  })

  test('logging out twice is not an error', async () => {
    // A sign-out that fails on retry leaves a live credential on a machine
    // somebody is trying to clean, and the client has no way to tell a network
    // timeout from a refusal.
    const started = await begin()
    const admin = await signInAs(api, org, 'admin', 'device-logout-twice')
    await approve(started.user_code, admin)
    api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)
    const token = String((await poll(started.device_code)).body.access_token)

    const headers = { authorization: `Bearer ${token}` }
    assert.equal((await api.fetch('/v1/logout', { method: 'POST', headers })).status, 200)
    assert.equal((await api.fetch('/v1/logout', { method: 'POST', headers })).status, 200)
  })

  test('a token stops identifying anybody once the membership is gone', async () => {
    const started = await begin()
    const member = await signInAs(api, org, 'member', 'device-removed')
    await approve(started.user_code, member)
    api.clock.advance(DEVICE_POLL_INTERVAL_SECONDS * 1000 + 1000)
    const token = String((await poll(started.device_code)).body.access_token)

    assert.equal((await api.fetch('/v1/whoami', { headers: { authorization: `Bearer ${token}` } })).status, 200)

    await api.admin`DELETE FROM members WHERE org_id = ${org.orgId} AND user_id = ${member.userId}`

    // The token row still exists and is not revoked. What has gone is the
    // membership, and a whoami that still answered with the old role is how
    // somebody removed from an organization keeps working from a laptop.
    const after = await api.fetch('/v1/whoami', { headers: { authorization: `Bearer ${token}` } })
    assert.equal(after.status, 401)
  })

  test('an engine token is not an identity', async () => {
    // Engine tokens authenticate a machine. Answering whoami for one would put
    // a machine's actions in a person's name in every audit trail that reads
    // this endpoint, so kind is checked rather than merely the token being
    // valid.
    const engineToken = `afe_${randomBytes(32).toString('base64url')}`
    const hash = createHash('sha256').update(engineToken).digest()
    await api.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix, kind)
      VALUES (${org.orgId}, 'a runner', ${hash}, ${engineToken.slice(0, 12)}, 'engine')`

    const res = await api.fetch('/v1/whoami', {
      headers: { authorization: `Bearer ${engineToken}` },
    })
    assert.equal(res.status, 401)

    // The negative control for THIS test: the same token does authenticate for
    // ingestion, so the 401 above is about kind and not about the token being
    // broken.
    const ingest = await api.fetch('/v1/events', {
      method: 'POST',
      headers: { authorization: `Bearer ${engineToken}`, 'content-type': 'application/json' },
      body: JSON.stringify({ events: [] }),
    })
    assert.notEqual(ingest.status, 401)
  })

  test('the sweeper removes finished authorizations and leaves live ones', async () => {
    // This function was written, commented, and called from nowhere, so
    // device_authorizations grew for the life of the process on any instance
    // where people run af login. It is wired into the housekeeping interval in
    // main.ts now, beside the session sweep it was clearly meant to sit next to.
    const start = await api.fetch('/auth/device/code', { method: 'POST' })
    assert.equal(start.status, 200)
    const live = (await start.json()) as { user_code: string }

    const stale = normaliseUserCode(newUserCode())
    await api.admin`
      INSERT INTO device_authorizations (device_code_hash, user_code, scopes, client_label, expires_at)
      VALUES (${createHash('sha256').update(randomBytes(16)).digest()}, ${stale},
              ARRAY['events:write']::text[], 'a sweeper test',
              ${new Date(api.clock.now().getTime() - 48 * 60 * 60 * 1000).toISOString()})`

    const removed = await sweepDeviceAuthorizations(api.pool, api.clock)
    assert.ok(removed >= 1, `swept ${removed}`)

    const [gone] = await api.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM device_authorizations WHERE user_code = ${stale}`
    assert.equal(gone!.n, 0, 'an expired authorization survived the sweep')

    // The negative control. A sweeper that took the live one too would break
    // every af login that is waiting on a browser.
    const [kept] = await api.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM device_authorizations
      WHERE user_code = ${normaliseUserCode(live.user_code)}`
    assert.equal(kept!.n, 1, 'the sweep removed an authorization that had not expired')
  })
})
