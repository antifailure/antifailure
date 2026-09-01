// Signing in and installing the App, in every order they can arrive in.
//
// These are two events with no guaranteed order and no shared transaction, and
// only one of the orders was ever built. Installing first and then signing in
// worked, because sign-in reads the installation table on its way through.
// Signing in first and then installing produced an organization that existed,
// that the person administered, and that nothing would ever attach them to:
// the only writer of membership had already run. What they saw was the empty
// state that means "nobody has installed the App", on an account where they had
// just installed it, with a second sign-in button as the only way out.
//
// So the table below is the point of this file. One test per cell:
//
//   install then sign in      the ordering that already worked
//   sign in then install      the ordering that did not, and self-resolves now
//   install, nobody signed in the installer has no account here yet
//   install twice             GitHub redelivers, and retries are not a promotion
//   install elsewhere         a session already in a tenant is not moved
//
// Plus the two ways an account could never be entered at all: a personal
// account, which /user/orgs does not return, and an organization past the
// first page of a list that was never paged.

import { describe, it, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { createHmac, randomUUID } from 'node:crypto'
import { handleDelivery } from '../src/github/webhook.ts'
import { RealGitHubClient } from '../src/auth/github.ts'
import { available, startApi, dropOrg, type ApiHarness } from './harness.ts'

const SECRET = 'installorder-webhook-secret'

let nextGitHubId = 700_000_000
function freshId(): number {
  nextGitHubId += 1
  return nextGitHubId
}

describe('signing in and installing, in both orders', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  const logins: string[] = []

  before(async () => {
    h = await startApi({ githubWebhookSecret: SECRET })
  })
  after(async () => {
    const rows = await h.admin<{ id: string }[]>`
      SELECT id FROM organizations WHERE github_login = ANY(${logins})`
    for (const row of rows) await dropOrg(h.admin, row.id)
    await h.close()
  })

  /** An account login nothing else in this database is using. */
  function accountLogin(prefix: string): string {
    const login = `${prefix}-${randomUUID().slice(0, 8)}`
    logins.push(login)
    return login
  }

  /**
   * One signed delivery through the real endpoint, not through the handler.
   *
   * At least one test has to go this way. The handler taking a GitHub client is
   * useless if the route never passes one, and a client that is wired nowhere
   * is exactly the shape of dead code this repository keeps shipping: it
   * compiles, it reads as a feature, and the behaviour never happens.
   */
  // Unique per delivery, the way GitHub's own identifier is. The endpoint fences
  // on it: a delivery arriving twice is answered without the handler running,
  // and one with no identifier at all is refused rather than handled unfenced,
  // because a delivery that cannot be recorded cannot be fenced. A fixture that
  // sent no header, or the same one every time, would be a fixture that does
  // not look like GitHub.
  //
  // Reusing one id for every delivery would be worse than omitting it: the
  // second delivery would come back 200 as a replay having run nothing, and the
  // test below that sends the same installation twice on purpose would pass
  // while proving nothing.
  let deliveries = 0
  const deliveryRun = randomUUID().slice(0, 8)

  async function deliver(payload: Record<string, unknown>): Promise<Response> {
    const body = JSON.stringify(payload)
    return h.fetch('/webhooks/github', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'x-github-event': 'installation',
        'x-github-delivery': `installorder-${deliveryRun}-${(deliveries += 1)}`,
        'x-hub-signature-256':
          'sha256=' + createHmac('sha256', SECRET).update(body, 'utf8').digest('hex'),
      },
      body,
    })
  }

  /** Completes a whole OAuth exchange and returns the session cookie. */
  async function signIn(login: string, githubId: number): Promise<string> {
    h.github.addUser({ id: githubId, login, email: `${login}@example.test`, name: login })
    // Past the rate limit window: these tests start more exchanges in a second
    // than a browser would in a minute, and a 429 here reads as a sign-in bug.
    h.clock.advance(60_000)
    const start = await h.fetch('/auth/github')
    const state = new URL(start.headers.get('location')!).searchParams.get('state')!
    const code = h.github.approve(login)
    const done = await h.fetch(`/auth/github/callback?code=${code}&state=${state}`)
    assert.equal(done.status, 302, 'the callback did not sign the user in')
    return done.headers.get('set-cookie')!.split(';')[0]!
  }

  async function sessionOf(cookie: string): Promise<{
    signedIn: boolean
    orgId: string | null
    role: string | null
  }> {
    const res = await h.fetch('/auth/session', { headers: { cookie } })
    return (await res.json()) as { signedIn: boolean; orgId: string | null; role: string | null }
  }

  async function membership(login: string): Promise<{ role: string; source: string } | null> {
    const rows = await h.admin<{ role: string; source: string }[]>`
      SELECT m.role, m.source FROM members m JOIN users u ON u.id = m.user_id
      WHERE u.github_login = ${login}`
    return rows[0] ?? null
  }

  /** The installer is an administrator of the organization, as GitHub sees it. */
  function githubSaysAdmin(orgLogin: string, login: string, id: number): void {
    h.github.setMembers(orgLogin, [
      {
        user: { id, login, email: `${login}@example.test`, name: login, avatarUrl: null },
        role: 'admin',
      },
    ])
  }

  // -------------------------------------------------------------------------

  it('install then sign in: the ordering that already worked, still works', async () => {
    const orgLogin = accountLogin('installfirst')
    const login = `ada-${randomUUID().slice(0, 6)}`
    const id = freshId()
    githubSaysAdmin(orgLogin, login, id)

    const res = await deliver({
      action: 'created',
      installation: { id: freshId(), account: { login: orgLogin, type: 'Organization' } },
      sender: { id: freshId(), login: 'somebody-else' },
      repositories: [],
    })
    assert.equal(res.status, 200)

    // The organization is on the list GitHub reports for this person, which is
    // what sign-in reads to decide which tenants they may enter.
    h.github.addOrganization(login, { id: freshId(), login: orgLogin })
    const cookie = await signIn(login, id)

    const me = await sessionOf(cookie)
    assert.equal(me.signedIn, true)
    assert.ok(me.orgId, 'signing in after the installation landed in no organization')
    assert.equal(me.role, 'owner', 'the first administrator to arrive should own the organization')
  })

  it('sign in then install: the same session becomes the organization, with no second sign-in', async () => {
    // The ordering the product's own two-button flow produces, and the one
    // that used to dead-end. The assertion that matters is that the cookie
    // taken BEFORE the installation is the cookie that works after it: a fix
    // that required another OAuth exchange would pass a test written against a
    // fresh cookie and change nothing about what the person actually sees.
    const orgLogin = accountLogin('signinfirst')
    const login = `grace-${randomUUID().slice(0, 6)}`
    const id = freshId()

    const cookie = await signIn(login, id)
    const before = await sessionOf(cookie)
    assert.equal(before.signedIn, true)
    assert.equal(before.orgId, null, 'there should be no organization to land in yet')

    githubSaysAdmin(orgLogin, login, id)
    const res = await deliver({
      action: 'created',
      installation: { id: freshId(), account: { login: orgLogin, type: 'Organization' } },
      sender: { id, login },
      repositories: [],
    })
    assert.equal(res.status, 200)
    const outcome = (await res.json()) as { detail: string }
    assert.match(outcome.detail, new RegExp(`${login} adopted`))

    const after = await sessionOf(cookie)
    assert.ok(after.orgId, 'the session held through the installation never gained the organization')
    assert.equal(after.role, 'owner', 'the person who installed the App cannot pay for it as a member')
  })

  it('install by somebody who has never signed in writes no membership and no user', async () => {
    // The rule the webhook has always kept, and adopting the installer must not
    // break it: a delivery does not create accounts for people who have never
    // been here. There is nothing to attach, and the delivery still succeeds.
    const orgLogin = accountLogin('nosignin')
    const login = `stranger-${randomUUID().slice(0, 6)}`
    const strangerId = freshId()

    const res = await deliver({
      action: 'created',
      installation: { id: freshId(), account: { login: orgLogin, type: 'Organization' } },
      sender: { id: strangerId, login },
      repositories: [],
    })
    assert.equal(res.status, 200)
    const outcome = (await res.json()) as { handled: boolean; detail: string }
    assert.equal(outcome.handled, true)
    assert.equal(outcome.detail.includes('adopted'), false)

    const users = await h.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM users WHERE github_id = ${strangerId}`
    assert.equal(users[0]!.n, 0, 'a delivery invented a user row')
    assert.equal(await membership(login), null)
  })

  it('the same installation delivered twice does not promote anybody a second time', async () => {
    // GitHub redelivers, for hours, on any delivery that failed. A retry that
    // re-ran the first-member bootstrap would hand ownership out again, and a
    // second audit entry would say ownership was granted twice for one act.
    const orgLogin = accountLogin('twice')
    const login = `repeat-${randomUUID().slice(0, 6)}`
    const id = freshId()
    const installationId = freshId()
    await signIn(login, id)
    githubSaysAdmin(orgLogin, login, id)

    const payload = {
      action: 'created',
      installation: { id: installationId, account: { login: orgLogin, type: 'Organization' } },
      sender: { id, login },
      repositories: [],
    }
    assert.equal((await deliver(payload)).status, 200)
    const first = await membership(login)
    assert.equal(first?.role, 'owner')

    // A DIFFERENT delivery identifier on purpose, which `deliver` gives it.
    // Reusing one would make the endpoint answer this as a replay without
    // running the handler at all, and the test would pass while proving
    // nothing about whether adoptInstaller is idempotent. The endpoint's fence
    // and the handler's idempotence are two separate properties and this test
    // is about the second.
    assert.equal((await deliver(payload)).status, 200)
    const second = await membership(login)
    assert.deepEqual(second, first, 'a redelivery changed the membership it had already written')

    const orgs = await h.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM organizations WHERE github_login = ${orgLogin}`
    assert.equal(orgs[0]!.n, 1)

    const bootstraps = await h.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM audit_entries
      WHERE action = 'member.bootstrapped' AND target_id = ${login}`
    assert.equal(bootstraps[0]!.n, 1, 'a redelivery audited a second bootstrap')
  })

  it('a session already inside an organization is not moved by an installation elsewhere', async () => {
    // Adopting the installer rotates a session that is in NO organization. A
    // session that is in one is somebody working, and moving it would take
    // them out of the tenant on their screen because they installed the App
    // somewhere else.
    const firstOrg = accountLogin('stayput')
    const secondOrg = accountLogin('elsewhere')
    const login = `mover-${randomUUID().slice(0, 6)}`
    const id = freshId()

    await signIn(login, id)
    githubSaysAdmin(firstOrg, login, id)
    await deliver({
      action: 'created',
      installation: { id: freshId(), account: { login: firstOrg, type: 'Organization' } },
      sender: { id, login },
      repositories: [],
    })

    // Sign in again now that the first organization exists, so this cookie is
    // genuinely inside a tenant rather than merely attached to one.
    h.github.addOrganization(login, { id: freshId(), login: firstOrg })
    const cookie = await signIn(login, id)
    const inFirst = await sessionOf(cookie)
    assert.ok(inFirst.orgId)

    githubSaysAdmin(secondOrg, login, id)
    await deliver({
      action: 'created',
      installation: { id: freshId(), account: { login: secondOrg, type: 'Organization' } },
      sender: { id, login },
      repositories: [],
    })

    const after = await sessionOf(cookie)
    assert.equal(after.orgId, inFirst.orgId, 'an installation elsewhere moved a working session')
  })

  it('a personal account can be entered, and its holder owns it', async () => {
    // /user/orgs never returns your own account, so an App installed on a
    // personal account created an organization keyed on a login that sign-in
    // never asked about. The tenant was real, the installation was live, and
    // the one person entitled to it landed in no organization every time.
    //
    // The role matters as much as the membership. GitHub has no membership
    // record to consult for a personal account, so asking would return null
    // and make the account holder a plain member of their own tenant, with
    // nobody at all holding billing.manage.
    const login = accountLogin('solo')
    const id = freshId()

    const res = await deliver({
      action: 'created',
      installation: { id: freshId(), account: { login, type: 'User' } },
      sender: { id: freshId(), login: 'installed-by-somebody-else' },
      repositories: [],
    })
    assert.equal(res.status, 200)

    const cookie = await signIn(login, id)
    const me = await sessionOf(cookie)
    assert.ok(me.orgId, 'the holder of a personal account could not enter their own organization')
    assert.equal(me.role, 'owner')
  })
})

// ---------------------------------------------------------------------------

describe('the organizations GitHub reports', () => {
  /**
   * Runs one body against a fetch that answers per URL.
   *
   * Restored in a finally rather than in an after hook: an after registered
   * from inside a test runs when the whole describe finishes, which would
   * leave a patched fetch installed for every suite sharing this process.
   */
  async function withGitHubAnswering(
    answer: (url: URL) => { status: number; body: string },
    run: (client: RealGitHubClient, urls: string[]) => Promise<void>,
  ): Promise<void> {
    const urls: string[] = []
    const original = globalThis.fetch
    globalThis.fetch = (async (input: string | URL | Request) => {
      const url = new URL(String(input))
      urls.push(url.pathname + url.search)
      const { status, body } = answer(url)
      return new Response(body, { status })
    }) as typeof fetch
    try {
      await run(
        new RealGitHubClient({
          clientId: 'id',
          clientSecret: 'secret',
          redirectUri: 'https://app.test/callback',
          apiBase: 'https://api.github.test',
        }),
        urls,
      )
    } finally {
      globalThis.fetch = original
    }
  }

  it('reads past the first page, because the thirty-first is a tenant nobody could enter', async () => {
    // The default page size is thirty, and this list decides which
    // organizations somebody may enter. Truncating it does not shorten a list
    // somebody reads: it withholds the tenant they came for and renders the
    // empty state that means nobody has installed the App.
    const page1 = Array.from({ length: 100 }, (_, i) => ({ id: i + 1, login: `filler-${i + 1}` }))
    const page2 = [{ id: 999, login: 'the-one-that-matters' }]

    await withGitHubAnswering(
      (url) => {
        const page = url.searchParams.get('page')
        if (page === '1') return { status: 200, body: JSON.stringify(page1) }
        if (page === '2') return { status: 200, body: JSON.stringify(page2) }
        return { status: 200, body: '[]' }
      },
      async (client, urls) => {
        const orgs = await client.organizationsFor('token')
        assert.equal(orgs.length, 101)
        assert.ok(
          orgs.some((o) => o.login === 'the-one-that-matters'),
          'the organization on the second page was never read',
        )
        assert.equal(urls[0], '/user/orgs?per_page=100&page=1')
        // A short page ends the walk. Asking for page three after a page of one
        // is a request per sign-in that can only ever answer with nothing.
        assert.equal(urls.length, 2, `walked ${urls.length} pages: ${urls.join(' ')}`)
      },
    )
  })

  it('one malformed entry does not discard the organizations around it', async () => {
    // The whole list is thrown away by a strict decode, and the whole list is
    // what access is decided from. One surprising row costing somebody every
    // tenant is the expensive direction to fail in.
    await withGitHubAnswering(
      (url) =>
        url.searchParams.get('page') === '1'
          ? {
              status: 200,
              body: JSON.stringify([
                { id: 1, login: 'good-one' },
                null,
                { login: 'no-id' },
                { id: 'not-a-number', login: 'wrong-type' },
                { id: 2, login: 'good-two' },
              ]),
            }
          : { status: 200, body: '[]' },
      async (client) => {
        const orgs = await client.organizationsFor('token')
        assert.deepEqual(orgs, [
          { id: 1, login: 'good-one' },
          { id: 2, login: 'good-two' },
        ])
      },
    )
  })
})
