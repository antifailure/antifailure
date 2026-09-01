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
  sessionCookie, readCookie, csrfTokenFor, csrfMatches, sweepSessions,
} from '../src/auth/session.ts'
import { safeRedirect, beginSignIn, completeSignIn, syncMembership, SignInError } from '../src/auth/signin.ts'
import { FakeClock } from '../src/clock.ts'
import { FakeGitHub } from '../src/auth/fakegithub.ts'
import { GitHubError } from '../src/auth/github.ts'
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

  /**
   * Signs the SAME person in twice, which the helper above cannot do: it mints
   * a fresh GitHub id per call, and a fresh id is a different person as far as
   * every join in this schema is concerned.
   */
  async function signInAgain(login: string, githubId: number) {
    h.github.addUser({ id: githubId, login, email: `${login}@example.test`, name: login })
    h.github.addOrganization(login, { id: 1, login: org.slug })
    // Past the rate limit window. These tests start more exchanges than a real
    // browser would in a minute, and a 429 here reads as a sign-in bug.
    h.clock.advance(60_000)
    const res = await h.fetch('/auth/github')
    const state = new URL(res.headers.get('location')!).searchParams.get('state')!
    const code = h.github.approve(login)
    const done = await h.fetch(`/auth/github/callback?code=${code}&state=${state}`)
    assert.equal(done.status, 302)
    return done
  }

  async function roleOf(login: string): Promise<string | null> {
    const rows = await h.admin<{ role: string; source: string }[]>`
      SELECT m.role, m.source FROM members m JOIN users u ON u.id = m.user_id
      WHERE u.github_login = ${login}`
    return rows[0]?.role ?? null
  }

  it('an organization owner on GitHub arrives as an admin, not as a member', async () => {
    // The defect this closes, seen on the real deployment: the person who
    // installed the App on their own organization signed in and could not
    // store a provider key, mint an engine token, manage members, export the
    // audit log, or approve a masking change. Their own control plane refused
    // them, because sign-in wrote 'member' for everybody and the function that
    // maps a GitHub owner to an admin had no caller anywhere in the codebase.
    const login = `owner-${randomUUID().slice(0, 6)}`
    const id = Math.floor(Math.random() * 1e9)
    h.github.setMembers(org.slug, [
      { user: { id, login, email: `${login}@example.test`, name: login, avatarUrl: null }, role: 'admin' },
    ])
    await signInAgain(login, id)
    assert.equal(await roleOf(login), 'admin')
  })

  it('a plain member on GitHub arrives as a member', async () => {
    const login = `plain-${randomUUID().slice(0, 6)}`
    const id = Math.floor(Math.random() * 1e9)
    h.github.setMembers(org.slug, [
      { user: { id, login, email: `${login}@example.test`, name: login, avatarUrl: null }, role: 'member' },
    ])
    await signInAgain(login, id)
    assert.equal(await roleOf(login), 'member')
  })

  it('a role set by hand here is not overwritten by GitHub on the next sign-in', async () => {
    // members.setRole marks a row source = 'manual'. Somebody promoted to
    // owner in this application stays an owner, or the promotion silently
    // expires the next time they sign in and nobody can explain why.
    const login = `manual-${randomUUID().slice(0, 6)}`
    const id = Math.floor(Math.random() * 1e9)
    h.github.setMembers(org.slug, [
      { user: { id, login, email: `${login}@example.test`, name: login, avatarUrl: null }, role: 'member' },
    ])
    await signInAgain(login, id)
    await h.admin`
      UPDATE members SET role = 'owner', source = 'manual'
      WHERE user_id = (SELECT id FROM users WHERE github_login = ${login})`
    await signInAgain(login, id)
    assert.equal(await roleOf(login), 'owner')
  })

  it('GitHub failing to answer leaves the role alone rather than demoting', async () => {
    // The ordering that makes this matter: an administrator signs in during a
    // GitHub rate limit. Reading "could not establish the role" as "member"
    // would strip them of members.manage on their own organization, and the
    // only way back is another sign-in that happens to succeed -- which they
    // can no longer grant themselves if they were the only administrator.
    const login = `flaky-${randomUUID().slice(0, 6)}`
    const id = Math.floor(Math.random() * 1e9)
    h.github.setMembers(org.slug, [
      { user: { id, login, email: `${login}@example.test`, name: login, avatarUrl: null }, role: 'admin' },
    ])
    await signInAgain(login, id)
    assert.equal(await roleOf(login), 'admin')

    h.github.breakRoleLookups()
    try {
      await signInAgain(login, id)
    } finally {
      h.github.breakRoleLookups(false)
    }
    assert.equal(await roleOf(login), 'admin', 'a failed role lookup demoted an administrator')
  })

  it('a first sign-in during an outage is a member, not an administrator', async () => {
    // The other direction of the same null. With no row to preserve, the
    // fallback has to guess, and guessing upward hands somebody administrative
    // rights because GitHub was slow.
    const login = `newflaky-${randomUUID().slice(0, 6)}`
    const id = Math.floor(Math.random() * 1e9)
    h.github.setMembers(org.slug, [
      { user: { id, login, email: `${login}@example.test`, name: login, avatarUrl: null }, role: 'admin' },
    ])
    h.github.breakRoleLookups()
    try {
      await signInAgain(login, id)
    } finally {
      h.github.breakRoleLookups(false)
    }
    assert.equal(await roleOf(login), 'member')
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

/**
 * The first member of an organization, and the orderings that reach that state.
 *
 * An organization here is created by an installation webhook, before anybody
 * has signed in, so "no members at all" is the state every tenant starts in and
 * passes through exactly once. What happens on that one sign-in decides whether
 * the organization ever has an owner, and owner is the only role holding
 * `billing.manage`.
 *
 * Every test below builds its own organization, because the question being
 * asked is about an EMPTY one and a suite that shares a fixture would answer it
 * once and then never again.
 */
describe('the first member of an organization', { skip: hasDatabase ? false : 'no Postgres' }, () => {
  let h: ApiHarness
  const created: string[] = []

  before(async () => {
    h = await startApi()
  })
  after(async () => {
    for (const orgId of created) await dropOrg(h.admin, orgId)
    await h.close()
  })

  /** An organization with an installation and nobody in it. */
  async function unclaimed(label: string): Promise<Org & { installationId: number }> {
    const org = await seedOrg(h.admin, label)
    created.push(org.orgId)
    const installationId = Math.floor(Math.random() * 1e12)
    await h.admin`
      INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
      VALUES (${org.orgId}, ${installationId}, ${org.slug}, 'Organization')`
    return { ...org, installationId }
  }

  function person(label: string) {
    const login = `${label}-${randomUUID().slice(0, 6)}`
    return {
      id: Math.floor(Math.random() * 1e9),
      login,
      email: `${login}@example.test`,
      name: login,
      avatarUrl: null,
    }
  }

  async function signIn(orgSlug: string, p: ReturnType<typeof person>) {
    h.github.addUser(p)
    h.github.addOrganization(p.login, { id: 1, login: orgSlug })
    // Past the rate limit window. These tests start more exchanges in a minute
    // than a browser would, and a 429 here reads as a sign-in bug.
    h.clock.advance(60_000)
    const start = await h.fetch('/auth/github')
    const state = new URL(start.headers.get('location')!).searchParams.get('state')!
    const code = h.github.approve(p.login)
    const done = await h.fetch(`/auth/github/callback?code=${code}&state=${state}`)
    assert.equal(done.status, 302)
    return done
  }

  async function memberRow(orgId: string, login: string) {
    const rows = await h.admin<{ role: string; source: string }[]>`
      SELECT m.role::text AS role, m.source FROM members m JOIN users u ON u.id = m.user_id
      WHERE m.org_id = ${orgId} AND u.github_login = ${login}`
    return rows[0] ?? null
  }

  async function owners(orgId: string): Promise<number> {
    const rows = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM members WHERE org_id = ${orgId} AND role = 'owner'`
    return Number(rows[0]!.n)
  }

  it('becomes the owner when GitHub says they administer it', async () => {
    // THE DEFECT THIS CLOSES. Every organization on this control plane was
    // created by a webhook and every member arrived through sign-in, which maps
    // a GitHub administrator to `admin`. So no organization anywhere had an
    // owner, and `billing.manage` is owner-only: nobody could see the plan, the
    // payment method or the spending caps, on any tenant, ever.
    const org = await unclaimed('bootstrap')
    const boss = person('boss')
    h.github.setMembers(org.slug, [{ user: boss, role: 'admin' }])

    await signIn(org.slug, boss)

    const row = await memberRow(org.orgId, boss.login)
    assert.equal(row?.role, 'owner')
    // manual, because this application decided it and GitHub has no opinion to
    // overwrite it with. A later sync must not demote them.
    assert.equal(row?.source, 'manual')

    const audit = await h.admin<{ actor_label: string; detail: Record<string, unknown> }[]>`
      SELECT actor_label, detail FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'member.bootstrapped'`
    assert.equal(audit.length, 1, 'an organization acquired an owner and nothing recorded it')
    assert.equal(audit[0]!.detail.githubRole, 'admin')
  })

  it('does not make the second one an owner as well', async () => {
    const org = await unclaimed('second')
    const first = person('first')
    const second = person('second')
    h.github.setMembers(org.slug, [
      { user: first, role: 'admin' },
      { user: second, role: 'admin' },
    ])

    await signIn(org.slug, first)
    await signIn(org.slug, second)

    assert.equal((await memberRow(org.orgId, first.login))?.role, 'owner')
    assert.equal((await memberRow(org.orgId, second.login))?.role, 'admin')
    assert.equal(await owners(org.orgId), 1)
  })

  it('a plain GitHub member arriving first is a member, not an owner', async () => {
    // The rule is "the first member becomes the owner", and it is still GitHub
    // that says who may be one. Somebody who administers nothing must not own
    // the tenant because they happened to click first.
    const org = await unclaimed('plainfirst')
    const plain = person('plain')
    h.github.setMembers(org.slug, [{ user: plain, role: 'member' }])

    await signIn(org.slug, plain)

    assert.equal((await memberRow(org.orgId, plain.login))?.role, 'member')
    assert.equal(await owners(org.orgId), 0)
  })

  it('an outage during the first sign-in produces a member, not an owner', async () => {
    // The other direction of the null `roleIn` returns. Guessing upward here
    // would mean a rate limit hands ownership of a company's tenant, including
    // its billing, to whoever was signing in at the time. The way out of an
    // organization whose GitHub link is permanently broken is the break-glass
    // command, which is a deliberate act by an operator holding the database
    // credential, not a guess made by a web request.
    const org = await unclaimed('outage')
    const boss = person('unlucky')
    h.github.setMembers(org.slug, [{ user: boss, role: 'admin' }])

    h.github.breakRoleLookups()
    try {
      await signIn(org.slug, boss)
    } finally {
      h.github.breakRoleLookups(false)
    }

    assert.equal((await memberRow(org.orgId, boss.login))?.role, 'member')
    assert.equal(await owners(org.orgId), 0)

    // And it heals on the next sign-in as far as GitHub can take it: `admin`,
    // which holds members.manage, so they can promote themselves the rest of
    // the way. `source` is still github, so this is the ordinary update path.
    await signIn(org.slug, boss)
    assert.equal((await memberRow(org.orgId, boss.login))?.role, 'admin')
    assert.equal(await owners(org.orgId), 0)
  })

  it('two people signing in at once produce exactly one owner', async () => {
    // B-then-A, forced rather than hoped for.
    //
    // Both are administrators on GitHub and both would qualify. Reading the
    // member count without a lock, two transactions in flight together each see
    // an empty organization, each conclude they are the first, and it ends with
    // two owners. Racing two sign-ins and hoping is not a test: they interleave
    // at whatever points Node happens to schedule, and the run that matters is
    // the one that never happens locally.
    //
    // So the window is held open. A gate transaction takes the same advisory
    // lock the decision takes, B's sign-in queues behind it, A's membership is
    // committed while B waits, and the gate opens. If the decision took no
    // lock, B never queues, it finishes before A's row exists, and both are
    // owners.
    const org = await unclaimed('race')
    const a = person('racer-a')
    const b = person('racer-b')
    h.github.setMembers(org.slug, [
      { user: a, role: 'admin' },
      { user: b, role: 'admin' },
    ])
    h.github.addUser(b)
    h.github.addOrganization(b.login, { id: 1, login: org.slug })

    const [firstUser] = await h.admin<{ id: string }[]>`
      INSERT INTO users (github_id, github_login, email, name)
      VALUES (${a.id}, ${a.login}, ${a.email}, ${a.name}) RETURNING id`

    const key = `members:${org.orgId}`
    let acquired!: () => void
    let open!: () => void
    const held = new Promise<void>((resolve) => { acquired = resolve })
    const gate = new Promise<void>((resolve) => { open = resolve })
    // One transaction on one connection, so the lock is taken and released by
    // the same session. A session-scoped pg_advisory_lock on a pooled client
    // can unlock on a different connection and then never release at all.
    const holding = h.admin.begin(async (tx) => {
      await tx`SELECT pg_advisory_xact_lock(hashtext(${key}))`
      acquired()
      await gate
    })
    await held

    const state = await beginSignIn(h.pool, h.clock, h.github)
    const code = h.github.approve(b.login)
    let settled = false
    const second = completeSignIn(h.pool, h.clock, h.github, { code, state: state.state })
      .finally(() => { settled = true })

    // Queued behind the gate, observed rather than waited out. A decision that
    // takes no lock never queues and finishes instead, which is what `settled`
    // is here to notice.
    for (let i = 0; i < 500 && !settled; i++) {
      const waiting = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`
      if (Number(waiting[0]!.n) > 0) break
    }

    await h.admin`
      INSERT INTO members (org_id, user_id, role, source)
      VALUES (${org.orgId}, ${firstUser!.id}, 'owner', 'manual')`
    open()
    await holding
    await second

    assert.equal(await owners(org.orgId), 1, 'two sign-ins in flight both became owner')
    assert.equal((await memberRow(org.orgId, b.login))?.role, 'admin')
  })

  it('a member removed and re-added does not become an owner while others remain', async () => {
    const org = await unclaimed('readded')
    const boss = person('stays')
    const leaver = person('leaves')
    h.github.setMembers(org.slug, [
      { user: boss, role: 'admin' },
      { user: leaver, role: 'admin' },
    ])

    await signIn(org.slug, boss)
    await signIn(org.slug, leaver)
    assert.equal((await memberRow(org.orgId, leaver.login))?.role, 'admin')

    // Removed the way syncMembership removes somebody, then back on GitHub and
    // signing in again. The organization is not unclaimed, so nothing here
    // qualifies as a first member.
    await h.admin`
      DELETE FROM members WHERE org_id = ${org.orgId}
        AND user_id = (SELECT id FROM users WHERE github_login = ${leaver.login})`
    await signIn(org.slug, leaver)

    assert.equal((await memberRow(org.orgId, leaver.login))?.role, 'admin')
    assert.equal(await owners(org.orgId), 1)
  })

  it('an organization that empties can be claimed again', async () => {
    // The same removal, applied to everybody. An organization with no members
    // has nobody who can act, which is the state the rule exists for, and it is
    // reachable long after the organization was created.
    const org = await unclaimed('emptied')
    const boss = person('returning')
    h.github.setMembers(org.slug, [{ user: boss, role: 'admin' }])

    await signIn(org.slug, boss)
    await h.admin`DELETE FROM members WHERE org_id = ${org.orgId}`
    await signIn(org.slug, boss)

    assert.equal((await memberRow(org.orgId, boss.login))?.role, 'owner')
    assert.equal(await owners(org.orgId), 1)
  })

  it('an uninstall and reinstall does not hand ownership to whoever signs in next', async () => {
    // Suspended, so sign-in grants nothing at all: the installation is the
    // reason membership exists and there is not one. What must not happen is
    // that the reinstall makes a later arrival look like the first member of an
    // organization that already has some.
    const org = await unclaimed('reinstall')
    const boss = person('installer')
    const later = person('later')
    h.github.setMembers(org.slug, [
      { user: boss, role: 'admin' },
      { user: later, role: 'admin' },
    ])

    await signIn(org.slug, boss)
    assert.equal((await memberRow(org.orgId, boss.login))?.role, 'owner')

    await h.admin`
      UPDATE github_installations SET suspended_at = now() WHERE org_id = ${org.orgId}`
    await signIn(org.slug, later)
    assert.equal(await memberRow(org.orgId, later.login), null, 'a suspended installation granted membership')

    await h.admin`
      UPDATE github_installations SET suspended_at = NULL WHERE org_id = ${org.orgId}`
    await signIn(org.slug, later)
    assert.equal((await memberRow(org.orgId, later.login))?.role, 'admin')
    assert.equal(await owners(org.orgId), 1)
  })

  it('an uninstall before anybody signs in leaves the organization claimable', async () => {
    // The other ordering of the same two events: the App comes and goes before
    // the first person ever arrives. The first sign-in after it comes back is
    // still the first, and still becomes the owner.
    const org = await unclaimed('suspended-first')
    const boss = person('patient')
    h.github.setMembers(org.slug, [{ user: boss, role: 'admin' }])

    await h.admin`
      UPDATE github_installations SET suspended_at = now() WHERE org_id = ${org.orgId}`
    await signIn(org.slug, boss)
    assert.equal(await memberRow(org.orgId, boss.login), null)

    await h.admin`
      UPDATE github_installations SET suspended_at = NULL WHERE org_id = ${org.orgId}`
    await signIn(org.slug, boss)
    assert.equal((await memberRow(org.orgId, boss.login))?.role, 'owner')
  })

  it('a sync from GitHub does not demote the owner it created', async () => {
    // GitHub has two roles and neither is owner, so a sync that treated this
    // row as GitHub's would map it back to admin every time anybody pressed the
    // button. That is what marking it manual prevents.
    const org = await unclaimed('resync')
    const boss = person('kept')
    h.github.setMembers(org.slug, [{ user: boss, role: 'admin' }])

    await signIn(org.slug, boss)
    assert.equal((await memberRow(org.orgId, boss.login))?.role, 'owner')

    await syncMembership(h.pool, h.clock, h.github, {
      orgId: org.orgId,
      installationId: org.installationId,
      orgLogin: org.slug,
      actorLabel: 'test',
    })
    assert.equal(
      (await memberRow(org.orgId, boss.login))?.role,
      'owner',
      'a membership sync demoted the owner sign-in created',
    )
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

  // -------------------------------------------------------------------------
  // The sweeper, and the two clocks it has to satisfy at once.
  // -------------------------------------------------------------------------
  //
  // sweepSessions was called every five minutes from main.ts and deleted
  // nothing, on every instance, for as long as it existed: it ran through
  // withoutTenant, and every policy on this table keys on the acting user, on
  // a presented token hash, or on the tenant. It matched no row and reported
  // success, because a statement that matches nothing does not raise.
  //
  // It now enters antifailure_sweeper for one transaction (0025). The policy
  // admitting that role restricts it to rows expired by the DATABASE's clock;
  // the statement restricts it to rows expired by the APPLICATION's. A row has
  // to be past both, so the four cases below are a table rather than one case,
  // and two of them isolate one restriction by defeating the other.
  //
  // The harness clock starts in the past, so a session that is live by the
  // application's clock is already expired by the database's. That is not an
  // accident of the fixture, it is the only way to write the third row of the
  // table, and it is why these are four tests rather than one.

  it('sweeps a session both clocks call expired', async () => {
    const member = await signInAs(h, org, 'member', 'sweepable')
    const stale = createHash('sha256').update(`stale-${randomUUID()}`).digest()
    await h.admin`
      INSERT INTO sessions (token_hash, user_id, org_id, expires_at)
      VALUES (${stale}, ${member.userId}, ${org.orgId},
              ${new Date(h.clock.now().getTime() - 48 * 60 * 60 * 1000).toISOString()})`

    const removed = await sweepSessions(h.pool, h.clock)
    assert.ok(removed >= 1, `the sweep removed ${removed} rows`)

    const [gone] = await h.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM sessions WHERE token_hash = ${stale}`
    assert.equal(gone!.n, 0, 'an expired session survived the sweep')
  })

  it('leaves a session the application still considers live', async () => {
    // Expired by the database's clock, because the harness clock is in the
    // past, so the policy admits this row. What keeps it is the statement's
    // own cutoff. Asserted through resolveSession rather than a row count:
    // the property is that the person is still signed in.
    const member = await signInAs(h, org, 'member', 'stillworking')
    await sweepSessions(h.pool, h.clock)
    assert.ok(
      await resolveSession(h.pool, h.clock, member.token),
      'the sweep ended a session that had not expired',
    )
  })

  it('leaves a session the database still considers live', async () => {
    const live = createHash('sha256').update(`live-${randomUUID()}`).digest()
    const member = await signInAs(h, org, 'member', 'dbclocklive')
    await h.admin`
      INSERT INTO sessions (token_hash, user_id, org_id, expires_at)
      VALUES (${live}, ${member.userId}, ${org.orgId}, now() + interval '1 day')`

    await sweepSessions(h.pool, h.clock)

    const [kept] = await h.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM sessions WHERE token_hash = ${live}`
    assert.equal(kept!.n, 1, 'the sweep removed a session that had not expired')
  })

  it('cannot reach a live session however wrong the clock it is given', async () => {
    // The cutoff is the one value a caller controls, so it is the one worth
    // attacking. A clock seventy years fast makes the statement's WHERE match
    // every row in the table. The policy's now() is not a parameter, so the
    // row survives anyway. This is the property a SECURITY DEFINER function
    // taking a cutoff would not have had.
    const live = createHash('sha256').update(`hostile-${randomUUID()}`).digest()
    const member = await signInAs(h, org, 'member', 'hostilecutoff')
    await h.admin`
      INSERT INTO sessions (token_hash, user_id, org_id, expires_at)
      VALUES (${live}, ${member.userId}, ${org.orgId}, now() + interval '1 day')`

    await sweepSessions(h.pool, new FakeClock('2099-01-01T00:00:00.000Z'))

    const [kept] = await h.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM sessions WHERE token_hash = ${live}`
    assert.equal(
      kept!.n,
      1,
      'a cutoff from a broken clock deleted a session the database still considers live',
    )
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


// ---------------------------------------------------------------------------

/**
 * The route, not the function.
 *
 * syncMembership had five tests above and zero callers: it was written,
 * correct, covered, and unreachable. Every one of those tests passed against a
 * feature that did not exist from a user's point of view, which is exactly what
 * dead code looks like from the inside. These are the cases that only hold once
 * something calls it over HTTP with a session and a permission.
 */
describe('members.sync over the route', { skip: hasDatabase ? false : 'no Postgres' }, () => {
  let h: ApiHarness
  let org: Org

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'syncroute')
  })
  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  const run = randomUUID().slice(0, 6)
  function ghUser(name: string) {
    const login = `${name}-${run}`
    return { id: Math.floor(Math.random() * 1e9), login, email: `${login}@example.test`, name: login, avatarUrl: null }
  }

  async function installFor(orgId: string, slug: string): Promise<number> {
    const id = Math.floor(Math.random() * 1e12)
    await h.admin`
      INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
      VALUES (${orgId}, ${id}, ${slug}, 'Organization')`
    return id
  }

  it('an organization with no installation is told so, rather than failing', async () => {
    const admin = await signInAs(h, org, 'admin')
    const { status, body } = await callProcedure(h, admin, 'members.sync', 'mutation', {})
    assert.equal(errorCode(body), 'PRECONDITION_FAILED', `${status} ${JSON.stringify(body).slice(0, 200)}`)
  })

  it('adds and removes members, and the effect is visible on the next request', async () => {
    // defined -> called -> effective. The assertion that matters is the last
    // one: members.list, the thing the console actually renders, changes.
    await installFor(org.orgId, org.slug)
    const admin = await signInAs(h, org, 'admin')
    const stays = ghUser('stays')
    const arrives = ghUser('arrives')
    h.github.setMembers(org.slug, [{ user: stays, role: 'admin' }, { user: arrives, role: 'member' }])

    const { status, body } = await callProcedure(h, admin, 'members.sync', 'mutation', {})
    assert.equal(status, 200, JSON.stringify(body).slice(0, 300))
    const report = (body as { result: { data: { added: string[]; removed: string[] } } }).result.data
    assert.ok(report.added.includes(stays.login), JSON.stringify(report))
    assert.ok(report.added.includes(arrives.login), JSON.stringify(report))

    const listed = await callProcedure(h, admin, 'members.list', 'query', {})
    const logins = ((listed.body as { result: { data: { github_login: string }[] } }).result.data).map(
      (m) => m.github_login,
    )
    assert.ok(logins.includes(stays.login), 'members.list does not show the synced member')

    // And the removal, which is the half sign-in can never do: somebody taken
    // off the GitHub organization has no reason to come back and sign in.
    h.github.setMembers(org.slug, [{ user: stays, role: 'admin' }])
    const second = await callProcedure(h, admin, 'members.sync', 'mutation', {})
    assert.equal(second.status, 200, JSON.stringify(second.body).slice(0, 300))
    const after2 = await callProcedure(h, admin, 'members.list', 'query', {})
    const logins2 = ((after2.body as { result: { data: { github_login: string }[] } }).result.data).map(
      (m) => m.github_login,
    )
    assert.ok(!logins2.includes(arrives.login), 'a member GitHub no longer reports is still listed')
  })

  it('an empty answer from GitHub is a refusal with a reason, not a 500', async () => {
    // syncMembership throws SignInError here on purpose: applying an empty
    // list would remove every owner, and an outage looks exactly like this.
    // Over HTTP that has to arrive as a 4xx with the explanation, or an
    // operator reads it as a bug in the control plane.
    await installFor(org.orgId, `${org.slug}-empty`)
    const admin = await signInAs(h, org, 'admin')
    h.github.setMembers(org.slug, [])
    const { status, body } = await callProcedure(h, admin, 'members.sync', 'mutation', {})
    assert.equal(errorCode(body), 'PRECONDITION_FAILED', `${status} ${JSON.stringify(body).slice(0, 200)}`)
    assert.match(JSON.stringify(body), /remove everyone/)
  })

  it('a control plane with no GitHub App says so, rather than answering 500', async () => {
    // The community edition runs without an App, and this is the route that
    // needs one. What an operator has to see is the sentence that names the
    // three variables to set, not "internal server error".
    await installFor(org.orgId, `${org.slug}-noapp`)
    const admin = await signInAs(h, org, 'admin')
    const previous = h.github.membersOf
    h.github.membersOf = async () => {
      throw new GitHubError(
        'Membership sync needs a GitHub App. Set AF_GITHUB_APP_ID, ' +
          'AF_GITHUB_APP_PRIVATE_KEY and AF_GITHUB_APP_WEBHOOK_SECRET.',
      )
    }
    try {
      const { status, body } = await callProcedure(h, admin, 'members.sync', 'mutation', {})
      assert.equal(errorCode(body), 'PRECONDITION_FAILED', `${status} ${JSON.stringify(body).slice(0, 200)}`)
      assert.match(JSON.stringify(body), /AF_GITHUB_APP_ID/)
    } finally {
      h.github.membersOf = previous
    }
  })

  it('a member cannot run it, because it takes access away', async () => {
    const member = await signInAs(h, org, 'member')
    const { body } = await callProcedure(h, member, 'members.sync', 'mutation', {})
    assert.equal(errorCode(body), 'FORBIDDEN')
  })
})
