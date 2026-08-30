// Sign in, sessions, and the ways they are attacked.
//
// Every case here is a specific attack rather than a feature: replaying a
// callback, planting a cookie before the victim signs in, sending a request
// from another site, following a redirect somebody chose, and holding a session
// after being removed from the organization.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID, createHash } from 'node:crypto'
import {
  ABSOLUTE_LIFETIME_MS, IDLE_TIMEOUT_MS, hashToken, issueSession, resolveSession,
  sessionCookie, readCookie, csrfTokenFor, csrfMatches,
} from '../src/auth/session.ts'
import { safeRedirect, completeSignIn, syncMembership, SignInError } from '../src/auth/signin.ts'
import { FakeClock } from '../src/clock.ts'
import { FakeGitHub } from '../src/auth/fakegithub.ts'
import {
  available, startApi, seedOrg, dropOrg, signInAs, callProcedure, errorCode,
  type ApiHarness, type Org,
} from './harness.ts'

const hasDatabase = await available()

describe('open redirects', () => {
  it('accepts only a path on this application', () => {
    for (const good of ['/', '/environments', '/repos/acme/app?tab=runs']) {
      assert.equal(safeRedirect(good), good)
    }
    for (const bad of [
      'https://evil.test',
      // Protocol-relative: an absolute URL that begins with a slash, which is
      // the form that gets past a check for "starts with /".
      '//evil.test',
      '///evil.test',
      'http://evil.test',
      // Backslashes are treated as slashes by some browsers.
      '/\\evil.test',
      'javascript:alert(1)',
    ]) {
      assert.equal(safeRedirect(bad), null, `${bad} was accepted as a return target`)
    }
  })
})

describe('cross-site request forgery tokens', () => {
  it('a token derived from one session does not verify against another', () => {
    const a = randomUUID()
    const b = randomUUID()
    assert.equal(csrfMatches(a, csrfTokenFor(a)), true)
    assert.equal(csrfMatches(a, csrfTokenFor(b)), false)
    assert.equal(csrfMatches(a, undefined), false)
    assert.equal(csrfMatches(a, ''), false)
    // A truncated token must not verify against a prefix of the real one.
    assert.equal(csrfMatches(a, csrfTokenFor(a).slice(0, 10)), false)
  })
})

describe('cookies', () => {
  it('are HttpOnly, SameSite, and Secure outside local development', () => {
    const value = sessionCookie('token', new Date('2026-06-01T00:00:00Z'), true)
    assert.match(value, /HttpOnly/)
    assert.match(value, /SameSite=Lax/)
    assert.match(value, /Secure/)
    // Strict would mean arriving from a link in a pull request comment lands
    // you signed out, which is how most people open this application.
    assert.doesNotMatch(value, /SameSite=Strict/)
  })

  it('parse one value out of a header holding several', () => {
    const header = 'other=1; af_session=abc.def; another=2'
    assert.equal(readCookie(header, 'af_session'), 'abc.def')
    assert.equal(readCookie(header, 'missing'), null)
    assert.equal(readCookie(null, 'af_session'), null)
  })
})

describe('the OAuth exchange', { skip: hasDatabase ? false : 'no Postgres' }, () => {
  let h: ApiHarness
  let org: Org

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'oauth')
    await h.admin`
      INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
      VALUES (${org.orgId}, ${Math.floor(Math.random() * 1e12)}, ${org.slug}, 'Organization')`
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  async function startAndApprove(login: string) {
    const user = h.github.addUser({
      id: Math.floor(Math.random() * 1e9),
      login,
      email: `${login}@example.test`,
      name: login,
    })
    h.github.addOrganization(login, { id: 1, login: org.slug })
    const res = await h.fetch('/auth/github')
    const url = new URL(res.headers.get('location')!)
    const state = url.searchParams.get('state')!
    const code = h.github.approve(login)
    return { user, state, code }
  }

  it('signs a user in and lands them in the organization the App is installed for', async () => {
    const { state, code } = await startAndApprove(`ada-${randomUUID().slice(0, 6)}`)
    const res = await h.fetch(`/auth/github/callback?code=${code}&state=${state}`)
    assert.equal(res.status, 302)
    const cookie = res.headers.get('set-cookie')!
    assert.match(cookie, /af_session=/)

    const session = await h.fetch('/auth/session', {
      headers: { cookie: cookie.split(';')[0]! },
    })
    const body = (await session.json()) as { signedIn: boolean; orgId: string; role: string }
    assert.equal(body.signedIn, true)
    assert.equal(body.orgId, org.orgId)
    assert.equal(body.role, 'member')
  })

  it('signs in behind a chain of proxies, where the forwarded header is a list', async () => {
    // The header a request arrives with in production is not one address. Two
    // proxies in front of this make it "client, proxy", and the sessions table
    // records the address in an `inet` column, which refuses a list outright.
    // The failure was total: the INSERT threw, the callback answered 500, and
    // nobody could sign in at all through that path. Nothing behind a single
    // proxy or a direct request reproduces it, which is why it is written down
    // here rather than left to be found by whoever deploys behind a second one.
    //
    // A direct request with no header at all is the same bug from the other
    // side: the bucket key for a request with no address is the literal string
    // "unknown", and that is a fine bucket key and not an address.
    const cases: Record<string, string | null> = {
      '203.0.113.7, 198.51.100.4': '203.0.113.7',
      '203.0.113.9:44321': '203.0.113.9',
      '[2001:db8::1]:443': '2001:db8::1',
      'unknown': null,
      '': null,
    }

    for (const [header, expected] of Object.entries(cases)) {
      const { state, code } = await startAndApprove(`proxy-${randomUUID().slice(0, 6)}`)
      const res = await h.fetch(`/auth/github/callback?code=${code}&state=${state}`, {
        ...(header ? { headers: { 'x-forwarded-for': header } } : {}),
      })
      assert.equal(res.status, 302, `a request forwarded as "${header}" could not sign in`)

      const cookie = res.headers.get('set-cookie')!.split(';')[0]!
      const token = cookie.slice(cookie.indexOf('=') + 1)
      const [row] = await h.admin<{ ip: string | null }[]>`
        SELECT host(ip) AS ip FROM sessions
        WHERE token_hash = ${createHash('sha256').update(token, 'utf8').digest()}`
      assert.equal(row?.ip ?? null, expected, `"${header}" was recorded as ${row?.ip ?? 'nothing'}`)
    }
  })

  it('refuses a state value it never issued', async () => {
    const { code } = await startAndApprove(`eve-${randomUUID().slice(0, 6)}`)
    const res = await h.fetch(`/auth/github/callback?code=${code}&state=made-up`)
    assert.equal(res.status, 400)
    assert.equal(res.headers.get('set-cookie'), null, 'a refused callback issued a cookie')
  })

  it('refuses a state value a second time', async () => {
    const { state, code } = await startAndApprove(`bob-${randomUUID().slice(0, 6)}`)
    const first = await h.fetch(`/auth/github/callback?code=${code}&state=${state}`)
    assert.equal(first.status, 302)

    // The same callback replayed. It has to fail even though the state was
    // genuine, because a replayable callback is a session fixation primitive.
    const second = await h.fetch(`/auth/github/callback?code=${code}&state=${state}`)
    assert.equal(second.status, 400)
    assert.equal(second.headers.get('set-cookie'), null)
  })

  it('refuses a state value that has expired', async () => {
    const { state, code } = await startAndApprove(`slow-${randomUUID().slice(0, 6)}`)
    h.clock.advance(11 * 60 * 1000)
    const res = await h.fetch(`/auth/github/callback?code=${code}&state=${state}`)
    assert.equal(res.status, 400)
  })

  it('refuses a code that GitHub has already redeemed', async () => {
    const login = `twice-${randomUUID().slice(0, 6)}`
    const { state, code } = await startAndApprove(login)
    await h.fetch(`/auth/github/callback?code=${code}&state=${state}`)

    const second = await h.fetch('/auth/github')
    const state2 = new URL(second.headers.get('location')!).searchParams.get('state')!
    // A genuine state with a spent code. The fake enforces single use the way
    // GitHub does, so this is the real failure and not a contrived one.
    const res = await h.fetch(`/auth/github/callback?code=${code}&state=${state2}`)
    assert.equal(res.status, 400)
  })

  it('replaces any session the browser already held, so a planted cookie cannot ride the login', async () => {
    const victim = await signInAs(h, org, 'admin', 'victim')
    const planted = victim.token

    const { state, code } = await startAndApprove(`fresh-${randomUUID().slice(0, 6)}`)
    const res = await h.fetch(`/auth/github/callback?code=${code}&state=${state}`, {
      headers: { cookie: `af_session=${planted}` },
    })
    assert.equal(res.status, 302)
    const issued = readCookie(res.headers.get('set-cookie')!.replace(/;.*$/, ''), 'af_session')
    assert.notEqual(issued, planted, 'sign in kept the session the browser arrived with')

    // And the old one is gone rather than merely superseded.
    const stale = await resolveSession(h.pool, h.clock, planted)
    assert.equal(stale, null, 'the session held before sign in still works')
  })

  it('does not put a user in an organization that has not installed the App', async () => {
    const login = `outsider-${randomUUID().slice(0, 6)}`
    h.github.addUser({
      id: Math.floor(Math.random() * 1e9),
      login,
      email: `${login}@example.test`,
      name: login,
    })
    // Belongs to a GitHub organization with no installation here. Membership of
    // a GitHub organization is not by itself a reason to see another company's
    // environments.
    h.github.addOrganization(login, { id: 99, login: 'unrelated-company' })

    const start = await h.fetch('/auth/github')
    const state = new URL(start.headers.get('location')!).searchParams.get('state')!
    const code = h.github.approve(login)
    const res = await h.fetch(`/auth/github/callback?code=${code}&state=${state}`)
    assert.equal(res.status, 302)

    const session = await h.fetch('/auth/session', {
      headers: { cookie: res.headers.get('set-cookie')!.split(';')[0]! },
    })
    const body = (await session.json()) as { signedIn: boolean; orgId: string | null }
    assert.equal(body.signedIn, true)
    assert.equal(body.orgId, null, 'a stranger was placed inside an organization')
  })
})

describe('sessions', { skip: hasDatabase ? false : 'no Postgres' }, () => {
  let h: ApiHarness
  let org: Org

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'sessions')
  })
  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  it('stores a hash and never the token', async () => {
    const issued = await issueSession(h.pool, h.clock, {
      userId: (await signInAs(h, org, 'member')).userId,
      orgId: org.orgId,
    })
    const rows = await h.admin<{ token_hash: Buffer }[]>`
      SELECT token_hash FROM sessions WHERE token_hash = ${hashToken(issued.token)}`
    assert.equal(rows.length, 1)
    // The literal token must appear nowhere in the row.
    const raw = await h.admin<Record<string, unknown>[]>`
      SELECT * FROM sessions WHERE token_hash = ${hashToken(issued.token)}`
    const serialized = JSON.stringify(raw[0], (_k, v) =>
      Buffer.isBuffer(v) ? v.toString('hex') : v,
    )
    assert.ok(!serialized.includes(issued.token), 'the session token is stored in the clear')
  })

  it('expires at its absolute lifetime however active it has been', async () => {
    const member = await signInAs(h, org, 'member', 'longlived')
    // Kept warm the whole way, so only the absolute limit can end it. The loop
    // stops one step short of the boundary: expiring exactly at the lifetime is
    // correct, so asserting the session is alive at that instant would be
    // asserting a bug.
    const step = IDLE_TIMEOUT_MS / 2
    for (let elapsed = step; elapsed < ABSOLUTE_LIFETIME_MS; elapsed += step) {
      h.clock.advance(step)
      const live = await resolveSession(h.pool, h.clock, member.token)
      assert.ok(live, `the session ended after ${elapsed}ms, before its absolute lifetime`)
    }
    h.clock.advance(step)
    assert.equal(
      await resolveSession(h.pool, h.clock, member.token),
      null,
      'the session outlived its absolute lifetime',
    )
  })

  it('expires after the idle timeout even though its lifetime has not run out', async () => {
    const member = await signInAs(h, org, 'member', 'idle')
    h.clock.advance(IDLE_TIMEOUT_MS + 1000)
    assert.equal(await resolveSession(h.pool, h.clock, member.token), null)
  })

  it('signing out makes the token stop working immediately', async () => {
    const member = await signInAs(h, org, 'member', 'leaving')
    const before = await callProcedure(h, member, 'environments.list', 'query', { limit: 5 })
    assert.equal(before.status, 200)

    const out = await h.fetch('/auth/signout', {
      method: 'POST',
      headers: { cookie: member.cookie },
    })
    assert.equal(out.status, 200)

    const after = await callProcedure(h, member, 'environments.list', 'query', { limit: 5 })
    assert.equal(errorCode(after.body), 'UNAUTHORIZED')
  })

  it('a mutation without the CSRF header is refused, and a query is not', async () => {
    const member = await signInAs(h, org, 'admin', 'csrf')

    // The shape of the attack: the browser sends the cookie because the request
    // came from a page the user has open, and the attacker's page cannot read
    // the token to send with it.
    const forged = await h.fetch('/trpc/environments.teardown', {
      method: 'POST',
      headers: { cookie: member.cookie, 'content-type': 'application/json' },
      body: JSON.stringify({ envId: org.envId }),
    })
    assert.equal(forged.status, 403)

    // A read is not guarded, because a read changes nothing and the page has to
    // be able to render before it has a token.
    const read = await h.fetch(
      `/trpc/environments.list?input=${encodeURIComponent(JSON.stringify({ limit: 5 }))}`,
      { headers: { cookie: member.cookie } },
    )
    assert.equal(read.status, 200)

    // And with the header it goes through.
    const allowed = await callProcedure(h, member, 'environments.teardown', 'mutation', {
      envId: org.envId,
    })
    assert.notEqual(allowed.status, 403)
  })

  it('a session for one organization cannot be used against another', async () => {
    const other = await seedOrg(h.admin, 'elsewhere')
    try {
      const member = await signInAs(h, org, 'owner', 'scoped')
      const { body } = await callProcedure(h, member, 'environments.get', 'query', {
        envId: other.envId,
      })
      // Not found rather than forbidden. Distinguishing them would turn this
      // into a way to ask whether another organization has an environment.
      assert.equal(errorCode(body), 'NOT_FOUND')
    } finally {
      await dropOrg(h.admin, other.orgId)
    }
  })
})

describe('membership sync', { skip: hasDatabase ? false : 'no Postgres' }, () => {
  let h: ApiHarness
  let org: Org

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'sync')
  })
  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  // A unique login per run. The database is not reset between runs, and a
  // fixture that reuses a login leaves two rows with the same login and a
  // different GitHub id, so a lookup by login returns whichever one Postgres
  // felt like. That is how this suite first "proved" that a removed member kept
  // their session: it was checking a stale user from an earlier run.
  const run = randomUUID().slice(0, 6)
  function ghUser(name: string) {
    const login = `${name}-${run}`
    return {
      id: Math.floor(Math.random() * 1e9),
      login,
      email: `${login}@example.test`,
      name: login,
      avatarUrl: null,
    }
  }

  it('refuses to apply an empty member list', async () => {
    h.github.setMembers(org.slug, [])
    await assert.rejects(
      syncMembership(h.pool, h.clock, h.github, {
        orgId: org.orgId,
        installationId: 1,
        orgLogin: org.slug,
        actorLabel: 'system',
      }),
      SignInError,
      // An outage that answers with an empty array looks exactly like an
      // organization that emptied. Applying it would remove every owner and
      // leave nobody able to sign in and undo it.
      'an empty list from GitHub was applied, which would remove everyone',
    )
  })

  it('adds, promotes, and removes to match GitHub', async () => {
    const ada = ghUser('ada')
    const grace = ghUser('grace')
    h.github.setMembers(org.slug, [
      { user: ada, role: 'admin' },
      { user: grace, role: 'member' },
    ])
    const first = await syncMembership(h.pool, h.clock, h.github, {
      orgId: org.orgId, installationId: 1, orgLogin: org.slug, actorLabel: 'system',
    })
    assert.deepEqual(first.added.sort(), [ada.login, grace.login].sort())

    // Grace promoted upstream, Ada gone.
    h.github.setMembers(org.slug, [{ user: grace, role: 'admin' }])
    const second = await syncMembership(h.pool, h.clock, h.github, {
      orgId: org.orgId, installationId: 1, orgLogin: org.slug, actorLabel: 'system',
    })
    assert.deepEqual(second.removed, [ada.login])
    assert.deepEqual(second.changed, [{ login: grace.login, from: 'member', to: 'admin' }])
  })

  it('does not overwrite a role that was set here by hand', async () => {
    const carol = ghUser('carol')
    h.github.setMembers(org.slug, [{ user: carol, role: 'member' }])
    await syncMembership(h.pool, h.clock, h.github, {
      orgId: org.orgId, installationId: 1, orgLogin: org.slug, actorLabel: 'system',
    })
    // Promoted in this application, which GitHub knows nothing about.
    await h.admin`
      UPDATE members SET role = 'owner', source = 'manual'
      FROM users u WHERE u.id = members.user_id AND u.github_id = ${carol.id}`

    await syncMembership(h.pool, h.clock, h.github, {
      orgId: org.orgId, installationId: 1, orgLogin: org.slug, actorLabel: 'system',
    })
    const [row] = await h.admin<{ role: string }[]>`
      SELECT m.role::text AS role FROM members m JOIN users u ON u.id = m.user_id
      WHERE u.github_id = ${carol.id} AND m.org_id = ${org.orgId}`
    assert.equal(row?.role, 'owner', 'GitHub demoted somebody who was promoted here')
  })

  it('revokes the sessions of a member it removes', async () => {
    const dan = ghUser('dan')
    h.github.setMembers(org.slug, [{ user: dan, role: 'member' }])
    await syncMembership(h.pool, h.clock, h.github, {
      orgId: org.orgId, installationId: 1, orgLogin: org.slug, actorLabel: 'system',
    })
    // Looked up by GitHub numeric id, which is the only identifier that is
    // stable and unique. A login can be renamed and reused.
    const [user] = await h.admin<{ id: string }[]>`
      SELECT id FROM users WHERE github_id = ${dan.id}`
    const issued = await issueSession(h.pool, h.clock, { userId: user!.id, orgId: org.orgId })
    assert.ok(await resolveSession(h.pool, h.clock, issued.token))

    // Removed upstream. Somebody who left the company must lose access now,
    // not when the session they are already holding happens to expire.
    h.github.setMembers(org.slug, [{ user: ghUser('someone-else'), role: 'admin' }])
    await syncMembership(h.pool, h.clock, h.github, {
      orgId: org.orgId, installationId: 1, orgLogin: org.slug, actorLabel: 'system',
    })
    assert.equal(
      await resolveSession(h.pool, h.clock, issued.token),
      null,
      'a removed member kept a working session',
    )
  })

  it('records what it changed in the audit log', async () => {
    const rows = await h.admin<{ action: string; detail: any }[]>`
      SELECT action, detail FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'members.synced' ORDER BY seq DESC LIMIT 1`
    assert.ok(rows.length > 0, 'a membership change was not audited')
    assert.ok(rows[0]!.detail, 'the entry does not say what changed')
  })
})

describe('the fake GitHub behaves like the real one', () => {
  it('gives out a code once', async () => {
    const clock = new FakeClock()
    const gh = new FakeGitHub(clock)
    gh.addUser({ id: 1, login: 'ada', email: 'ada@example.test', name: 'Ada' })
    const code = gh.approve('ada')
    await gh.exchangeCode(code)
    await assert.rejects(gh.exchangeCode(code), /bad_verification_code/)
  })

  it('expires a code that is not used promptly', async () => {
    const clock = new FakeClock()
    const gh = new FakeGitHub(clock)
    gh.addUser({ id: 1, login: 'ada', email: 'ada@example.test', name: 'Ada' })
    const code = gh.approve('ada')
    clock.advance(11 * 60 * 1000)
    await assert.rejects(gh.exchangeCode(code), /bad_verification_code/)
  })

  it('lowercases the email address, because providers disagree about case', async () => {
    const clock = new FakeClock()
    const gh = new FakeGitHub(clock)
    const user = gh.addUser({ id: 1, login: 'ada', email: 'Ada@Example.Test', name: 'Ada' })
    assert.equal(user.email, 'ada@example.test')
  })
})
