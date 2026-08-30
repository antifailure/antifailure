// Signing in with a link, against a real database and its policies.
//
// The happy path is one test out of these and it is the least interesting one.
// What this suite is actually for is the set of ways a link sign-in goes wrong
// in production, each of which looks fine in a code review:
//
//   the address does not exist        and the caller can tell
//   the link is opened twice          and both browsers get a session
//   the link is opened after expiry   and it still works
//   the link is guessed               and a near miss says so
//   the link is edited to point away  and it is an open redirect
//   somebody already had a session    and the old one still works after
//
// Each has a test below, and each of those tests fails if the enforcement is
// moved out of the statement it lives in and into the application.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID, createHash } from 'node:crypto'
import { sql } from 'drizzle-orm'
import {
  available, startApi, seedOrg, dropOrg, type ApiHarness, type Org,
} from './harness.ts'
import { LINK_TTL_MS, sweepEmailSignInTokens } from '../src/auth/email.ts'
import { SESSION_COOKIE, readCookie } from '../src/auth/session.ts'

const hasDatabase = await available()

describe('signing in with a link', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org
  const created: string[] = []

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'link')
    created.push(org.orgId)
  })

  after(async () => {
    for (const id of created) await dropOrg(h.admin, id)
    await h.close()
  })

  /** A member of the organization, by address. */
  async function member(address: string): Promise<string> {
    const login = `link-${randomUUID().slice(0, 8)}`
    const [user] = await h.admin<{ id: string }[]>`
      INSERT INTO users (github_id, github_login, email, name)
      VALUES (${Math.floor(Math.random() * 1e12)}, ${login}, ${address}, ${'Link Tester'})
      RETURNING id`
    await h.admin`
      INSERT INTO members (org_id, user_id, role, source)
      VALUES (${org.orgId}, ${user!.id}, 'admin', 'manual')`
    return user!.id
  }

  // The sign-in limits are the tightest on the server and the clock is a fake
  // one, so nothing refills unless a test moves it. Two seconds between
  // attempts is what a person does anyway, and it is four orders of magnitude
  // inside the fifteen-minute lifetime of a link.
  function aMomentPasses(): void {
    h.clock.advance(2000)
  }

  async function ask(email: string, redirectTo?: string): Promise<Response> {
    aMomentPasses()
    return h.fetch('/auth/email', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ email, ...(redirectTo ? { redirect_to: redirectTo } : {}) }),
    })
  }

  /** Opens a link the way a browser would. */
  async function open(token: string, headers: Record<string, string> = {}): Promise<Response> {
    aMomentPasses()
    return h.fetch(`/auth/email/callback?token=${encodeURIComponent(token)}`, { headers })
  }

  /** The token out of the most recent message to an address. */
  function tokenSentTo(address: string): string {
    const message = h.mailer.lastTo(address)
    assert.ok(message, `nothing was sent to ${address}`)
    const found = /[?&]token=([^&\s"<]+)/.exec(message.text)
    assert.ok(found, `the message to ${address} carried no token:\n${message.text}`)
    return decodeURIComponent(found[1]!)
  }

  /** Waits for the send that the handler deliberately does not await. */
  async function settled(): Promise<void> {
    await new Promise((resolve) => setImmediate(resolve))
  }

  it('sends a link to a member, and it signs them in', async () => {
    const address = `ada-${randomUUID().slice(0, 6)}@example.test`
    const userId = await member(address)

    const asked = await ask(address)
    assert.equal(asked.status, 200)
    await settled()

    const message = h.mailer.lastTo(address)
    assert.ok(message, 'a member asked for a link and none was sent')
    assert.match(message.subject, /sign in/i)
    // The link has to be findable by something that reads the body rather than
    // the code that wrote it. An agent extracts the first URL, so the body has
    // to carry one that works.
    assert.match(message.text, /http:\/\/api\.test\/auth\/email\/callback\?token=/)

    const opened = await open(tokenSentTo(address))
    assert.equal(opened.status, 302)

    const cookie = readCookie(
      (opened.headers.get('set-cookie') ?? '').split(';')[0] ?? '',
      SESSION_COOKIE,
    )
    assert.ok(cookie, 'signing in issued no session cookie')

    const session = await h.fetch('/auth/session', { headers: { cookie: `${SESSION_COOKIE}=${cookie}` } })
    const body = (await session.json()) as { signedIn: boolean; orgId: string | null; role: string | null }
    assert.equal(body.signedIn, true)
    assert.equal(body.orgId, org.orgId, 'the session did not land in the one organization they belong to')
    assert.equal(body.role, 'admin', 'the role is read from the membership, not from the link')
    assert.ok(userId)
  })

  it('answers an address with no account exactly as it answers one with, and sends nothing', async () => {
    const stranger = `nobody-${randomUUID().slice(0, 6)}@example.test`
    const known = `known-${randomUUID().slice(0, 6)}@example.test`
    await member(known)

    const forStranger = await ask(stranger)
    const forKnown = await ask(known)
    await settled()

    assert.equal(forStranger.status, forKnown.status)
    assert.deepEqual(await forStranger.json(), await forKnown.json())
    assert.equal(
      h.mailer.lastTo(stranger),
      undefined,
      'an address with no account was sent mail, which is both a leak and a way to post somebody mail',
    )

    // And nothing was written for them either, so the table cannot be used to
    // ask the question a second way.
    const [row] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM email_signin_tokens WHERE email = ${stranger}`
    assert.equal(Number(row!.n), 0)
  })

  it('refuses an address that belongs to a user in no organization', async () => {
    // A user row with no membership is somebody who has been removed. They
    // must not be able to sign back in and land in a session with no tenant.
    const orphan = `orphan-${randomUUID().slice(0, 6)}@example.test`
    await h.admin`
      INSERT INTO users (github_id, github_login, email)
      VALUES (${Math.floor(Math.random() * 1e12)}, ${`orphan-${randomUUID().slice(0, 6)}`}, ${orphan})`

    await ask(orphan)
    await settled()
    assert.equal(h.mailer.lastTo(orphan), undefined)
  })

  it('works once: the second browser to open the same link is refused', async () => {
    const address = `twice-${randomUUID().slice(0, 6)}@example.test`
    await member(address)
    await ask(address)
    await settled()
    const token = tokenSentTo(address)

    const first = await open(token)
    assert.equal(first.status, 302)

    const second = await open(token)
    assert.equal(second.status, 400, 'the same link signed two browsers in')
    assert.equal(second.headers.get('set-cookie'), null, 'a refused link still set a cookie')
  })

  it('works once even when both browsers open it at the same instant', async () => {
    // The single-use check lives in the UPDATE's own WHERE clause, so this is
    // the test that fails if somebody moves it into the application and writes
    // the flag afterwards. Two requests, no await between them.
    const address = `race-${randomUUID().slice(0, 6)}@example.test`
    await member(address)
    await ask(address)
    await settled()
    const token = tokenSentTo(address)

    aMomentPasses()
    const both = await Promise.all([
      h.fetch(`/auth/email/callback?token=${encodeURIComponent(token)}`),
      h.fetch(`/auth/email/callback?token=${encodeURIComponent(token)}`),
    ])
    const signedIn = both.filter((r) => r.status === 302)
    assert.equal(
      signedIn.length,
      1,
      `${signedIn.length} of two simultaneous opens got a session; exactly one may`,
    )
  })

  it('expires', async () => {
    const address = `stale-${randomUUID().slice(0, 6)}@example.test`
    await member(address)
    await ask(address)
    await settled()
    const token = tokenSentTo(address)

    h.clock.advance(LINK_TTL_MS + 1000)
    const opened = await open(token)
    assert.equal(opened.status, 400, 'a link older than its lifetime still worked')
  })

  it('says the same thing for a link that never existed, one already used, and one expired', async () => {
    const address = `same-${randomUUID().slice(0, 6)}@example.test`
    await member(address)
    await ask(address)
    await settled()
    const used = tokenSentTo(address)
    await open(used)

    const messages = new Set<string>()
    for (const token of [used, 'never-issued-at-all', '']) {
      const res = await open(token)
      assert.equal(res.status, 400)
      messages.add(((await res.json()) as { error: string }).error)
    }
    assert.equal(
      messages.size,
      1,
      `three refusals gave ${messages.size} different messages, which tells somebody grinding ` +
        `tokens which of their guesses was once real: ${[...messages].join(' / ')}`,
    )
  })

  it('stores a hash, so the table is not a list of working links', async () => {
    const address = `hash-${randomUUID().slice(0, 6)}@example.test`
    await member(address)
    await ask(address)
    await settled()
    const token = tokenSentTo(address)

    const [row] = await h.admin<{ token_hash: Buffer }[]>`
      SELECT token_hash FROM email_signin_tokens WHERE email = ${address} ORDER BY created_at DESC LIMIT 1`
    assert.ok(row, 'no row was written for a link that was sent')
    assert.equal(row.token_hash.toString('hex'), createHash('sha256').update(token, 'utf8').digest('hex'))
    assert.ok(
      !row.token_hash.toString('utf8').includes(token),
      'the token itself is in the row, so a backup of this table is a list of working links',
    )
  })

  it('follows a path on this application and refuses everywhere else', async () => {
    const address = `redir-${randomUUID().slice(0, 6)}@example.test`
    await member(address)

    // A path is kept.
    await ask(address, '/audit')
    await settled()
    const kept = await open(tokenSentTo(address))
    assert.equal(kept.headers.get('location'), 'http://app.test/audit')

    // An absolute address, a protocol-relative one, and a backslash trick are
    // all dropped rather than followed. This is the open redirect that matters,
    // because the link arrives carrying a session.
    for (const hostile of ['https://evil.test/steal', '//evil.test/steal', '/\\evil.test']) {
      await ask(address, hostile)
      await settled()
      const res = await open(tokenSentTo(address))
      assert.equal(
        res.headers.get('location'),
        'http://app.test/',
        `${hostile} was followed, which is an open redirect on the one link people are trained to click`,
      )
    }
  })

  it('destroys the session the browser already held', async () => {
    // Fixation. A cookie planted before sign-in must not survive it.
    const address = `rotate-${randomUUID().slice(0, 6)}@example.test`
    await member(address)

    await ask(address)
    await settled()
    const firstOpen = await open(tokenSentTo(address))
    const planted = readCookie((firstOpen.headers.get('set-cookie') ?? '').split(';')[0] ?? '', SESSION_COOKIE)
    assert.ok(planted)

    await ask(address)
    await settled()
    const secondOpen = await open(tokenSentTo(address), { cookie: `${SESSION_COOKIE}=${planted}` })
    assert.equal(secondOpen.status, 302)

    const old = await h.fetch('/auth/session', { headers: { cookie: `${SESSION_COOKIE}=${planted}` } })
    const body = (await old.json()) as { signedIn: boolean }
    assert.equal(body.signedIn, false, 'the session held before signing in still works afterwards')
  })

  it('accepts a form post, so a page with no JavaScript can still sign in', async () => {
    const address = `form-${randomUUID().slice(0, 6)}@example.test`
    await member(address)

    aMomentPasses()
    const res = await h.fetch('/auth/email', {
      method: 'POST',
      headers: { 'content-type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({ email: address }).toString(),
    })
    assert.equal(res.status, 200)
    await settled()
    assert.ok(h.mailer.lastTo(address), 'a form post sent nothing')
  })

  it('answers a body that is not an address the same way, without writing anything', async () => {
    const before = await h.admin<{ n: string }[]>`SELECT count(*) AS n FROM email_signin_tokens`
    for (const body of ['{"email":"not-an-address"}', '{"email":123}', 'not json at all', '{}']) {
      aMomentPasses()
      const res = await h.fetch('/auth/email', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body,
      })
      assert.equal(res.status, 200, `${body} was answered differently`)
    }
    const afterwards = await h.admin<{ n: string }[]>`SELECT count(*) AS n FROM email_signin_tokens`
    assert.equal(Number(afterwards[0]!.n), Number(before[0]!.n))
  })

  it('cannot be used to read a user row without holding a token', async () => {
    // The policy that makes the user row reachable is keyed on the token hash.
    // A connection that declares one it did not receive gets nothing, and this
    // is the test that fails if that policy is ever loosened to "any address".
    const address = `policy-${randomUUID().slice(0, 6)}@example.test`
    await member(address)

    const rows = await h.pool.withoutTenant(
      async (db) => db.execute<{ id: string }>(sql`SELECT id FROM users WHERE lower(email) = ${address}`),
      { emailTokenHash: createHash('sha256').update('a token nobody issued', 'utf8').digest() },
    )
    assert.equal(rows.length, 0, 'a made-up token hash read a user row')
  })

  it('sweeps what can no longer be used and leaves what can', async () => {
    const live = `live-${randomUUID().slice(0, 6)}@example.test`
    const stale = `stale2-${randomUUID().slice(0, 6)}@example.test`
    await member(live)
    await member(stale)

    await ask(stale)
    await settled()
    h.clock.advance(LINK_TTL_MS + 1000)
    await ask(live)
    await settled()

    const removed = await sweepEmailSignInTokens(h.pool, h.clock)
    assert.ok(removed >= 1, 'the sweep removed nothing, so the table grows forever')

    const [stillThere] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM email_signin_tokens WHERE email = ${live}`
    assert.equal(Number(stillThere!.n), 1, 'the sweep removed a link that still works')
  })
})
