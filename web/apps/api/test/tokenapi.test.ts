// `af token`, from the server's side.
//
// This is the endpoint the whole self-hosted path rests on: without an engine
// token nothing an engine does reaches a control plane, and until these routes
// existed there was no way to make one. So what is tested here is not that the
// happy path returns a string. It is the four things that decide whether
// putting a credential factory on a terminal was safe:
//
//   - a token from a plain `af login` cannot mint one, so a laptop that signed
//     in months ago and was then lost cannot be used to make a fresh, long
//     lived, non-expiring credential;
//   - the SCOPE says what the terminal may do and the ROLE says what the person
//     may do, and both are checked per request, so a member cannot mint what
//     they could not mint in the console, and a demotion takes effect now
//     rather than when a ninety day token expires;
//   - an engine token cannot mint another engine token, which is the property
//     that stops one leaked CI secret from becoming an unbounded supply;
//   - and the token it produces actually authenticates an engine, because a
//     mint that returns a string the ingest path rejects is the same dead end
//     with a friendlier error message.
//
// Every token below is obtained through the real device grant, for the reason
// providerapi.test.ts gives: a test that inserted a row into engine_tokens
// would prove these endpoints work for tokens they will never see.

import { test, describe, before, after, beforeEach } from 'node:test'
import assert from 'node:assert/strict'
import { available, startApi, seedOrg, dropOrg, signInAs, type ApiHarness, type Org } from './harness.ts'
import type { Role } from '../src/permissions.ts'
import { CSRF_HEADER } from '../src/auth/session.ts'
import { DEVICE_POLL_INTERVAL_SECONDS } from '../src/auth/device.ts'

describe(
  'engine tokens from a terminal',
  { skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let api: ApiHarness
    let org: Org

    before(async () => {
      api = await startApi({})
      org = await seedOrg(api.admin, 'token-cli')
    })
    after(async () => {
      await dropOrg(api.admin, org.orgId)
      await api.close()
    })

    beforeEach(async () => {
      await api.admin`DELETE FROM engine_tokens WHERE org_id = ${org.orgId} AND kind = 'engine'`
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
    ): Promise<{ status: number; json: Record<string, unknown> }> {
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
        // Left empty. A route answering with something other than JSON is a
        // finding, and the status is asserted on instead.
      }
      return { status: res.status, json: parsed }
    }

    // -----------------------------------------------------------------------
    // The gates
    // -----------------------------------------------------------------------

    test('no token mints nothing', async () => {
      assert.equal((await call(null, 'POST', '/v1/tokens', { name: 'ci' })).status, 401)
      assert.equal(
        (await call(`afu_${'x'.repeat(43)}`, 'POST', '/v1/tokens', { name: 'ci' })).status,
        401,
      )
    })

    test('a plain af login token cannot mint one', async () => {
      const token = await tokenFor('admin', [], 'plain')
      const res = await call(token, 'POST', '/v1/tokens', { name: 'ci' })
      assert.equal(res.status, 403)
      // The message names the command that fixes it, so nobody goes looking for
      // a role problem that does not exist.
      assert.match(String(res.json.error), /af login --scope tokens\.manage/)
    })

    test('a member with the scope is still refused, because role is checked too', async () => {
      // A member can approve their own device login, so if the scope were the
      // only check any member could mint a permanent organization credential.
      const token = await tokenFor('member', ['tokens.manage'], 'member-scope')
      const res = await call(token, 'POST', '/v1/tokens', { name: 'ci' })
      assert.equal(res.status, 403)
      assert.match(String(res.json.error), /owner or admin/)
      assert.equal((await call(token, 'GET', '/v1/tokens')).status, 403)
    })

    test('an engine token cannot mint another engine token', async () => {
      // The property that bounds the blast radius of a leaked CI secret. An
      // engine token authenticates the ingest path, so it is a real credential;
      // it has no identity, so it is not a person, and only a person mints.
      const admin = await tokenFor('owner', ['tokens.manage'], 'minter')
      const made = await call(admin, 'POST', '/v1/tokens', { name: 'ci' })
      assert.equal(made.status, 201)
      const engineToken = String(made.json.token)

      const second = await call(engineToken, 'POST', '/v1/tokens', { name: 'more' })
      assert.equal(second.status, 401)
      assert.equal((await call(engineToken, 'GET', '/v1/tokens')).status, 401)
    })

    // -----------------------------------------------------------------------
    // What it produces
    // -----------------------------------------------------------------------

    test('the token it mints authenticates an engine', async () => {
      // The assertion the whole feature exists for. A mint that returned a
      // string /v1/events rejects would be the same dead end it replaced.
      const admin = await tokenFor('owner', ['tokens.manage'], 'end-to-end')
      const made = await call(admin, 'POST', '/v1/tokens', { name: 'ci' })
      assert.equal(made.status, 201)
      const engineToken = String(made.json.token)
      assert.match(engineToken, /^aft_/)

      const sent = await api.fetch('/v1/events', {
        method: 'POST',
        headers: { 'content-type': 'application/json', authorization: `Bearer ${engineToken}` },
        body: JSON.stringify({ events: [] }),
      })
      // Anything but 401 proves the credential was accepted; an empty batch is
      // refused on its contents, which is a different and correct answer.
      assert.notEqual(sent.status, 401)
    })

    test('nothing can read a minted token back', async () => {
      const admin = await tokenFor('owner', ['tokens.manage'], 'once')
      const made = await call(admin, 'POST', '/v1/tokens', { name: 'ci' })
      const secret = String(made.json.token)

      const listed = await call(admin, 'GET', '/v1/tokens')
      assert.equal(listed.status, 200)
      const rows = listed.json.tokens as Record<string, unknown>[]
      assert.equal(rows.length, 1)
      assert.equal(rows[0]!.name, 'ci')
      assert.equal(rows[0]!.prefix, secret.slice(0, 12))
      // Not "the token field is empty" but "no field anywhere carries it". A
      // route that leaked it under another name would pass the weaker check.
      assert.ok(!JSON.stringify(listed.json).includes(secret.slice(12)))
    })

    test('the database holds a hash and never the token', async () => {
      const admin = await tokenFor('owner', ['tokens.manage'], 'hashed')
      const made = await call(admin, 'POST', '/v1/tokens', { name: 'ci' })
      const secret = String(made.json.token)
      const rows = await api.admin<{ name: string; kind: string; user_id: string | null }[]>`
        SELECT name, kind, user_id FROM engine_tokens
        WHERE org_id = ${org.orgId} AND kind = 'engine'`
      assert.equal(rows.length, 1)
      assert.equal(rows[0]!.kind, 'engine')
      // A machine is not a person. An engine token with a user id would put a
      // build server's actions in somebody's audit trail.
      assert.equal(rows[0]!.user_id, null)

      const leaked = await api.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM engine_tokens WHERE encode(token_hash, 'escape') = ${secret}`
      assert.equal(Number(leaked[0]!.n), 0)
    })

    test('a name is required and a long one is refused', async () => {
      const admin = await tokenFor('owner', ['tokens.manage'], 'named')
      assert.equal((await call(admin, 'POST', '/v1/tokens', {})).status, 400)
      assert.equal((await call(admin, 'POST', '/v1/tokens', { name: '   ' })).status, 400)
      assert.equal(
        (await call(admin, 'POST', '/v1/tokens', { name: 'x'.repeat(61) })).status,
        400,
      )
    })

    test('two tokens can share a name, because that is what a rotation is', async () => {
      // Collapsing onto the name would mean a rotation silently handed back the
      // credential it was replacing, which is the worst possible answer.
      const admin = await tokenFor('owner', ['tokens.manage'], 'rotating')
      const first = await call(admin, 'POST', '/v1/tokens', { name: 'ci' })
      const second = await call(admin, 'POST', '/v1/tokens', { name: 'ci' })
      assert.equal(second.status, 201)
      assert.notEqual(first.json.token, second.json.token)
      assert.notEqual(first.json.id, second.json.id)
    })

    // -----------------------------------------------------------------------
    // Revocation
    // -----------------------------------------------------------------------

    test('revoking by prefix stops the token working', async () => {
      const admin = await tokenFor('owner', ['tokens.manage'], 'revoker')
      const made = await call(admin, 'POST', '/v1/tokens', { name: 'ci' })
      const engineToken = String(made.json.token)
      const prefix = String(made.json.prefix)

      const before = await api.fetch('/v1/events', {
        method: 'POST',
        headers: { 'content-type': 'application/json', authorization: `Bearer ${engineToken}` },
        body: JSON.stringify({ events: [] }),
      })
      assert.notEqual(before.status, 401)

      // The prefix, not the uuid. It is what a person can see, so it is what
      // somebody reaches for when a credential has leaked and minutes matter.
      const gone = await call(admin, 'DELETE', `/v1/tokens/${prefix}`)
      assert.equal(gone.status, 200)
      assert.equal(gone.json.alreadyRevoked, false)

      const after = await api.fetch('/v1/events', {
        method: 'POST',
        headers: { 'content-type': 'application/json', authorization: `Bearer ${engineToken}` },
        body: JSON.stringify({ events: [] }),
      })
      assert.equal(after.status, 401)
    })

    test('revoking twice is not an error', async () => {
      const admin = await tokenFor('owner', ['tokens.manage'], 'twice')
      const made = await call(admin, 'POST', '/v1/tokens', { name: 'ci' })
      const id = String(made.json.id)
      assert.equal((await call(admin, 'DELETE', `/v1/tokens/${id}`)).json.alreadyRevoked, false)
      const again = await call(admin, 'DELETE', `/v1/tokens/${id}`)
      assert.equal(again.status, 200)
      assert.equal(again.json.alreadyRevoked, true)
    })

    test('an unknown token answers the same way as one belonging to somebody else', async () => {
      const admin = await tokenFor('owner', ['tokens.manage'], 'stranger')
      const other = await seedOrg(api.admin, 'token-other')
      try {
        const mine = await call(admin, 'POST', '/v1/tokens', { name: 'ci' })
        const theirs = await api.admin<{ id: string }[]>`
          INSERT INTO engine_tokens (org_id, name, token_hash, prefix, kind)
          VALUES (${other.orgId}, 'theirs', decode('aabbccdd', 'hex'), 'aft_theirs', 'engine')
          RETURNING id`

        const missing = await call(admin, 'DELETE', '/v1/tokens/aft_nothere1')
        const notmine = await call(admin, 'DELETE', `/v1/tokens/${theirs[0]!.id}`)
        assert.equal(missing.status, 404)
        assert.equal(notmine.status, 404)
        assert.deepEqual(missing.json, notmine.json)

        // And it is still there, so 404 meant "not yours", not "deleted".
        const survivors = await api.admin<{ n: string }[]>`
          SELECT count(*) AS n FROM engine_tokens
          WHERE org_id = ${other.orgId} AND revoked_at IS NULL`
        assert.equal(Number(survivors[0]!.n), 1)
        assert.equal(mine.status, 201)
      } finally {
        await dropOrg(api.admin, other.orgId)
      }
    })

    test('a listing shows only this organization', async () => {
      const admin = await tokenFor('owner', ['tokens.manage'], 'scoped')
      const other = await seedOrg(api.admin, 'token-elsewhere')
      try {
        await api.admin`
          INSERT INTO engine_tokens (org_id, name, token_hash, prefix, kind)
          VALUES (${other.orgId}, 'theirs', decode('11223344', 'hex'), 'aft_elsew', 'engine')`
        await call(admin, 'POST', '/v1/tokens', { name: 'mine' })
        const listed = await call(admin, 'GET', '/v1/tokens')
        const rows = listed.json.tokens as Record<string, unknown>[]
        assert.equal(rows.length, 1)
        assert.equal(rows[0]!.name, 'mine')
      } finally {
        await dropOrg(api.admin, other.orgId)
      }
    })

    // -----------------------------------------------------------------------
    // The audit trail
    // -----------------------------------------------------------------------

    test('minting and revoking are audited, and the entry never carries the token', async () => {
      const admin = await tokenFor('owner', ['tokens.manage'], 'audited')
      const made = await call(admin, 'POST', '/v1/tokens', { name: 'ci' })
      const secret = String(made.json.token)
      await call(admin, 'DELETE', `/v1/tokens/${String(made.json.prefix)}`)

      // Scoped to this token's own entries. audit_entries is append only by
      // design, so every earlier test in this file has left its mints behind,
      // and an unfiltered read here would assert on the file's history.
      const rows = await api.admin<{ action: string; origin: string; detail: unknown }[]>`
        SELECT action, origin, detail FROM audit_entries
        WHERE org_id = ${org.orgId}
          AND action IN ('token.created', 'token.revoked')
          AND (target_id = ${String(made.json.prefix)} OR target_id = ${String(made.json.id)})
        ORDER BY seq`
      assert.deepEqual(
        rows.map((r) => r.action),
        ['token.created', 'token.revoked'],
      )
      // From a terminal, not from the console. "Somebody signed in rotated a
      // credential" and "a token on a build machine did" are the same row
      // without this and different incidents with it.
      assert.ok(rows.every((r) => r.origin === 'cli'))
      assert.ok(!JSON.stringify(rows).includes(secret.slice(12)))
    })
  },
)
