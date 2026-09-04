// Self serve signup, in every order the events can arrive in.
//
// The behaviour under test is one sentence: somebody signs in, belongs to
// nothing, and ends up owning an organization on the free plan whose limits are
// really applied to them. Everything difficult about it is ordering, because
// the organization a signup creates and the organization an installation
// creates are the same row, reached from two directions, and there is no
// transaction spanning them.
//
// So the table is the point of this file, one test per cell:
//
//   sign up, nothing else        the new path, end to end
//   sign in twice                a second visit is not a second tenant
//   sign up then install         the installation ADOPTS the row, does not fork it
//   install then sign in         the installation path still owns the outcome
//   already a member             no personal tenant beside a real one
//   two signups at once          one organization, not two, under a forced race
//   the slug is taken            somebody else's tenant is never adopted
//   the setting is off           the behaviour that existed before this file
//
// And then the thing the whole exercise is for, which is not an ordering: the
// free plan's numbers are enforced against the organization a signup created,
// through the same procedure a customer reaches.

import { describe, it, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { createHmac, randomUUID } from 'node:crypto'
import { provisionPersonalOrganization } from '../src/auth/provision.ts'
import { RealGitHubClient } from '../src/auth/github.ts'
import { slugFor } from '../src/slug.ts'
import {
  available,
  startApi,
  dropOrg,
  callProcedure,
  errorCode,
  type ApiHarness,
} from './harness.ts'

const SECRET = 'signup-webhook-secret'

/**
 * A GitHub numeric id nothing in this database is using.
 *
 * Random rather than a counter from a fixed base, which is what this was and
 * which fails on the SECOND run against the same cluster: users rows outlive a
 * suite by design, the counter restarts at the same number every process, and
 * the first raw insert of a user collides on `users_github_id_key`. That
 * arrives as a duplicate key error inside a test about racing signups, which is
 * the least informative place it could possibly appear.
 */
function freshId(): number {
  return 800_000_000 + Math.floor(Math.random() * 1_000_000_000)
}

describe('self serve signup', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  const orgIds = new Set<string>()

  before(async () => {
    h = await startApi({ githubWebhookSecret: SECRET, selfServeSignup: true })
  })
  after(async () => {
    for (const id of orgIds) await dropOrg(h.admin, id)
    await h.close()
  })

  /** A GitHub login nothing else in this database is using. */
  function freshLogin(prefix: string): string {
    return `${prefix}-${randomUUID().slice(0, 8)}`
  }

  /** Completes a whole OAuth exchange and returns the session cookie. */
  async function signIn(login: string, githubId: number): Promise<string> {
    h.github.addUser({ id: githubId, login, email: `${login}@example.test`, name: login })
    // Past the rate limit window. These tests start more exchanges in a second
    // than a browser would in a minute, and a 429 here reads as a signup bug.
    h.clock.advance(60_000)
    const start = await h.fetch('/auth/github')
    const state = new URL(start.headers.get('location')!).searchParams.get('state')!
    const code = h.github.approve(login)
    const done = await h.fetch(`/auth/github/callback?code=${code}&state=${state}`)
    assert.equal(done.status, 302, 'the callback did not sign the user in')
    return done.headers.get('set-cookie')!.split(';')[0]!
  }

  interface SessionView {
    signedIn: boolean
    orgId: string | null
    orgSlug: string | null
    role: string | null
    plan: string | null
    csrfToken?: string
  }

  async function sessionOf(cookie: string): Promise<SessionView> {
    const res = await h.fetch('/auth/session', { headers: { cookie } })
    return (await res.json()) as SessionView
  }

  /** Signs somebody up and remembers the organization so it is cleaned up. */
  async function signUp(prefix: string): Promise<{
    login: string
    githubId: number
    cookie: string
    me: SessionView
  }> {
    const login = freshLogin(prefix)
    const githubId = freshId()
    const cookie = await signIn(login, githubId)
    const me = await sessionOf(cookie)
    if (me.orgId) orgIds.add(me.orgId)
    return { login, githubId, cookie, me }
  }

  /** How many organizations carry this GitHub login. The question every
   *  ordering below is really asking is whether the answer is ever two. */
  async function organizationsFor(login: string): Promise<{ id: string; slug: string; plan: string }[]> {
    return h.admin<{ id: string; slug: string; plan: string }[]>`
      SELECT id, slug, plan FROM organizations WHERE github_login = ${login} ORDER BY created_at ASC`
  }

  async function membershipsOf(githubId: number): Promise<{ role: string; source: string; org_id: string }[]> {
    return h.admin<{ role: string; source: string; org_id: string }[]>`
      SELECT m.role, m.source, m.org_id FROM members m JOIN users u ON u.id = m.user_id
      WHERE u.github_id = ${githubId}`
  }

  let deliveries = 0
  const deliveryRun = randomUUID().slice(0, 8)

  /** One signed installation delivery through the real endpoint. */
  async function install(
    account: { login: string; type: string },
    sender: { id: number; login: string },
  ): Promise<Response> {
    const payload = {
      action: 'created',
      installation: { id: freshId(), account },
      sender,
      repositories: [],
    }
    const body = JSON.stringify(payload)
    return h.fetch('/webhooks/github', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'x-github-event': 'installation',
        'x-github-delivery': `signup-${deliveryRun}-${(deliveries += 1)}`,
        'x-hub-signature-256':
          'sha256=' + createHmac('sha256', SECRET).update(body, 'utf8').digest('hex'),
      },
      body,
    })
  }

  // -------------------------------------------------------------------------

  it('somebody with no invitation and no installation ends up owning a free organization', async () => {
    // The whole point of the change, asserted through the session the browser
    // actually holds rather than through the database. What a person sees is
    // /auth/session, and an organization that exists but is not on the session
    // is an organization they cannot reach.
    const { login, githubId, me } = await signUp('hobbyist')

    assert.equal(me.signedIn, true)
    assert.ok(me.orgId, 'signing up with nothing landed in no organization')
    assert.equal(me.orgSlug, slugFor(login), 'the slug is not the one an installation would derive')
    assert.equal(me.role, 'owner', 'somebody who cannot reach billing does not own their tenant')
    assert.equal(me.plan, 'free')

    const orgs = await organizationsFor(login)
    assert.equal(orgs.length, 1)
    assert.equal(orgs[0]!.plan, 'free', 'a signup organization is not on the free plan')

    const members = await membershipsOf(githubId)
    assert.equal(members.length, 1)
    assert.equal(members[0]!.role, 'owner')
    // `manual`, not `github`. A later members.sync must not be able to demote
    // the owner of a tenant they created, which is what `github` would allow.
    assert.equal(members[0]!.source, 'manual')
  })

  it('records how the organization came to exist, and that its owner was not GitHub deciding', async () => {
    // An operator reading an audit chain later has to be able to tell a tenant
    // that began with an installation from one that began with a signup, and a
    // person holding owner because GitHub said so from one holding it because
    // they created the organization. Two entries, because they are two facts.
    const { login, me } = await signUp('audited')
    const entries = await h.admin<{ action: string; origin: string; detail: unknown }[]>`
      SELECT action, origin, detail FROM audit_entries
      WHERE org_id = ${me.orgId!} ORDER BY seq ASC`

    const actions = entries.map((e) => e.action)
    assert.deepEqual(actions, ['organization.created', 'member.bootstrapped'])
    assert.ok(entries.every((e) => e.origin === 'signup'), 'the entries do not say a signup did this')
    assert.equal((entries[0]!.detail as { githubLogin: string }).githubLogin, login)
    assert.equal((entries[1]!.detail as { githubRole: unknown }).githubRole, null)
  })

  it('signing in a second time does not create a second organization', async () => {
    // The commonest ordering of all, and the one a naive implementation gets
    // wrong: provisioning keyed on "this sign-in" rather than on "this person
    // belongs nowhere" hands somebody a new tenant every time they come back.
    const { login, githubId, me } = await signUp('returning')
    assert.ok(me.orgId)

    const again = await sessionOf(await signIn(login, githubId))
    assert.equal(again.orgId, me.orgId, 'a second sign-in moved them to a different organization')
    assert.equal((await organizationsFor(login)).length, 1)
    assert.equal((await membershipsOf(githubId)).length, 1)
  })

  it('signing up and THEN installing the App adopts the organization rather than forking it', async () => {
    // The ordering this whole design is arranged around. `rememberInstallation`
    // upserts ON CONFLICT (slug), and the signup writes the slug `slugFor`
    // derives from the same login, so the installation lands on the row that
    // already exists. If it did not, the person would keep their environments
    // in one organization and their repositories in another, both named after
    // them, with no way to tell which was which.
    const { login, githubId, me } = await signUp('adopted')
    const before = me.orgId!

    const res = await install({ login, type: 'User' }, { id: githubId, login })
    assert.equal(res.status, 200)

    const orgs = await organizationsFor(login)
    assert.equal(orgs.length, 1, 'the installation created a second organization beside the signup')
    assert.equal(orgs[0]!.id, before, 'the installation did not land on the organization that existed')

    const members = await membershipsOf(githubId)
    assert.equal(members.length, 1)
    assert.equal(members[0]!.role, 'owner', 'adoption demoted the owner of their own organization')

    // The installation is attached to the same row, which is what makes
    // repositories, dispatch and membership sync work in the tenant they
    // already had.
    const installs = await h.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM github_installations WHERE org_id = ${before}`
    assert.equal(installs[0]!.n, 1)
  })

  it('installing the App first still produces exactly one organization when they sign in', async () => {
    // The other order. The installation creates the tenant and grants
    // membership when the person signs in, so by the time provisioning is
    // consulted they belong somewhere and it must do nothing. A provisioning
    // step that ran before the installation loop would create a second tenant
    // here every time.
    const login = freshLogin('installfirst')
    const githubId = freshId()

    const res = await install({ login, type: 'User' }, { id: freshId(), login: 'somebody-else' })
    assert.equal(res.status, 200)
    const created = await organizationsFor(login)
    assert.equal(created.length, 1)
    orgIds.add(created[0]!.id)

    const me = await sessionOf(await signIn(login, githubId))
    assert.equal(me.orgId, created[0]!.id, 'they did not land in the organization the App created')
    assert.equal((await organizationsFor(login)).length, 1, 'signing in forked the tenant')
  })

  it('somebody already in an organization is not given a second one', async () => {
    // Membership anywhere is the signal that this person has a place. A
    // colleague installing the App on the company account is the ordinary way
    // to acquire one, and a personal tenant appearing beside it would put an
    // empty organization in their switcher forever.
    const orgLogin = freshLogin('company')
    const login = freshLogin('employee')
    const githubId = freshId()

    h.github.addUser({ id: githubId, login, email: `${login}@example.test`, name: login })
    h.github.setMembers(orgLogin, [
      {
        user: { id: githubId, login, email: `${login}@example.test`, name: login, avatarUrl: null },
        role: 'member',
      },
    ])
    const res = await install({ login: orgLogin, type: 'Organization' }, { id: freshId(), login: 'installer' })
    assert.equal(res.status, 200)
    const company = await organizationsFor(orgLogin)
    orgIds.add(company[0]!.id)

    h.github.addOrganization(login, { id: freshId(), login: orgLogin })
    const me = await sessionOf(await signIn(login, githubId))

    assert.equal(me.orgId, company[0]!.id)
    assert.equal(
      (await organizationsFor(login)).length,
      0,
      'somebody who already belonged somewhere was given a personal organization as well',
    )
  })

  it('two signups racing on one login produce one organization, not two', async () => {
    // Forced rather than hoped for. Two calls in flight at once is what two
    // browser tabs finishing an OAuth callback at the same instant looks like,
    // and the thing standing between that and two tenants is the unique index
    // on the slug plus ON CONFLICT DO NOTHING, not any check this code makes.
    const login = freshLogin('raced')
    const githubId = freshId()
    const [user] = await h.admin<{ id: string }[]>`
      INSERT INTO users (github_id, github_login, email, name)
      VALUES (${githubId}, ${login}, ${`${login}@example.test`}, ${login})
      RETURNING id`

    const both = await Promise.all([
      provisionPersonalOrganization(h.pool, h.clock, { userId: user!.id, login, label: login }),
      provisionPersonalOrganization(h.pool, h.clock, { userId: user!.id, login, label: login }),
    ])
    for (const org of both) if (org) orgIds.add(org.orgId)

    const orgs = await organizationsFor(login)
    assert.equal(orgs.length, 1, 'a race created two organizations for one person')
    assert.equal(both.filter((o) => o !== null).length, 1, 'both attempts reported creating one')
    assert.equal((await membershipsOf(githubId)).length, 1)
  })

  it('a slug another account already holds is left alone, and the sign-in still succeeds', async () => {
    // Two GitHub logins can slugify to one slug: `some.org` and `some-org` both
    // become `some-org`. Adopting a row on the strength of that coincidence
    // would hand somebody a tenant belonging to a stranger, so the signup does
    // nothing and the person lands in the empty state, which is exactly what
    // they would have seen before this file existed.
    const stranger = freshLogin('stranger')
    const [taken] = await h.admin<{ id: string }[]>`
      INSERT INTO organizations (slug, name, github_login)
      VALUES (${slugFor(stranger)}, ${stranger}, ${`${stranger}-somebody-else`})
      RETURNING id`
    orgIds.add(taken!.id)

    const githubId = freshId()
    const me = await sessionOf(await signIn(stranger, githubId))

    assert.equal(me.signedIn, true, 'a slug collision cost somebody their sign-in')
    assert.equal(me.orgId, null, 'the signup adopted an organization it did not create')
    const rows = await h.admin<{ github_login: string }[]>`
      SELECT github_login FROM organizations WHERE id = ${taken!.id}`
    assert.equal(
      rows[0]!.github_login,
      `${stranger}-somebody-else`,
      "the signup rewrote another account's organization",
    )
    assert.equal((await membershipsOf(githubId)).length, 0)
  })

  it('the free plan is enforced against the organization a signup created', async () => {
    // The requirement that makes the rest of this worth having. A tenant handed
    // out for free with nothing applied to it is a bill, and the numbers in
    // PLAN_QUOTAS were enforced against organizations that only an installation
    // could create. This reaches the same procedure the console calls.
    const { login, me, cookie } = await signUp('quota')
    const orgId = me.orgId!

    // A repository and three running environments, which is what
    // PLAN_QUOTAS.free.environments allows at once.
    const repository = `${login}/app`
    const [repo] = await h.admin<{ id: string }[]>`
      INSERT INTO repositories (org_id, full_name) VALUES (${orgId}, ${repository}) RETURNING id`
    for (const n of [1, 2, 3]) {
      await h.admin`
        INSERT INTO environments (org_id, repository_id, env_id, branch, state)
        VALUES (${orgId}, ${repo!.id}, ${`env-${login}-${n}`}, 'main', 'running')`
    }

    const session = await sessionOf(cookie)
    const called = await callProcedure(
      h,
      { userId: '', token: '', csrfToken: session.csrfToken!, cookie },
      'environments.create',
      'mutation',
      { repository, branch: 'main' },
    )

    assert.equal(errorCode(called.body), 'PRECONDITION_FAILED')
    const message = (called.body as { error?: { message?: string } }).error?.message ?? ''
    assert.match(
      message,
      /holding 3 of 3 environments on the free plan/,
      `the fourth environment was not refused by the free plan. The API said: ${message}`,
    )
  })
})

describe('self serve signup, turned off', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  const orgIds = new Set<string>()

  before(async () => {
    // No `selfServeSignup`, which is the server's default and every other
    // suite's. If this ever stops being the default, this test says so.
    h = await startApi()
  })
  after(async () => {
    for (const id of orgIds) await dropOrg(h.admin, id)
    await h.close()
  })

  it('leaves somebody with no organization, exactly as before', async () => {
    const login = `off-${randomUUID().slice(0, 8)}`
    const githubId = freshId()
    h.github.addUser({ id: githubId, login, email: `${login}@example.test`, name: login })
    h.clock.advance(60_000)
    const start = await h.fetch('/auth/github')
    const state = new URL(start.headers.get('location')!).searchParams.get('state')!
    const done = await h.fetch(
      `/auth/github/callback?code=${h.github.approve(login)}&state=${state}`,
    )
    assert.equal(done.status, 302)
    const cookie = done.headers.get('set-cookie')!.split(';')[0]!

    const res = await h.fetch('/auth/session', { headers: { cookie } })
    const me = (await res.json()) as { signedIn: boolean; orgId: string | null; selfServeSignup: boolean }
    assert.equal(me.signedIn, true, 'turning the setting off must not stop anybody signing in')
    assert.equal(me.orgId, null)
    // Published on the session so the console can tell the two empty states
    // apart: waiting for an installation, or a provisioning step that failed.
    assert.equal(me.selfServeSignup, false)

    const rows = await h.admin<{ id: string }[]>`
      SELECT id FROM organizations WHERE github_login = ${login}`
    for (const row of rows) orgIds.add(row.id)
    assert.equal(rows.length, 0, 'an organization was created with the setting off')
  })
})

// ---------------------------------------------------------------------------
// The verification the signup path actually rests on
//
// There is no link this control plane can send on the hosted deployment:
// antifailure.dev publishes no mail exchanger and an SPF policy authorizing no
// sender, so the address on a signup is verified by GitHub or it is not
// verified at all. That makes `/user/emails` load bearing rather than a
// fallback, and these exercise the real client through a patched fetch, because
// FakeGitHub returns a user object and so cannot see this code at all.
// ---------------------------------------------------------------------------

describe('the address a signup is identified by is one GitHub says is verified', () => {
  /** The real client, against a GitHub that answers what the test says. */
  async function withGitHubAnswering(
    answer: (url: URL) => { status: number; body: string },
    run: (client: RealGitHubClient, urls: string[]) => Promise<void>,
  ): Promise<void> {
    const urls: string[] = []
    const original = globalThis.fetch
    globalThis.fetch = (async (input: string | URL | Request) => {
      const url = new URL(String(input))
      urls.push(url.pathname)
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
          webBase: 'https://github.test',
        }),
        urls,
      )
    } finally {
      globalThis.fetch = original
    }
  }

  function answering(profileEmail: string | null, list: unknown): (url: URL) => { status: number; body: string } {
    return (url) => {
      if (url.pathname === '/login/oauth/access_token') {
        return { status: 200, body: JSON.stringify({ access_token: 'token' }) }
      }
      if (url.pathname === '/user') {
        return {
          status: 200,
          body: JSON.stringify({
            id: 4242, login: 'reader', email: profileEmail, name: 'Reader', avatar_url: null,
          }),
        }
      }
      if (url.pathname === '/user/emails') return { status: 200, body: JSON.stringify(list) }
      return { status: 404, body: '{}' }
    }
  }

  it('reads the verified list even when the profile carries a public address', async () => {
    // The branch that used to be skipped. `/user` reports whatever is on the
    // public profile and says nothing about verification, so trusting it was
    // trusting a value with no verification attached.
    await withGitHubAnswering(
      answering('public@example.test', [
        { email: 'public@example.test', primary: true, verified: true },
      ]),
      async (client, urls) => {
        const { user } = await client.exchangeCode('code')
        assert.equal(user.email, 'public@example.test')
        assert.ok(urls.includes('/user/emails'), 'the verified list was never asked for')
      },
    )
  })

  it('refuses a public profile address the verified list does not vouch for', async () => {
    // Anybody can put an address on their public profile. If nothing in the
    // verified list matches it, it is not evidence about anything, and using it
    // would let somebody be identified by an address that is not theirs.
    await withGitHubAnswering(
      answering('claimed@example.test', [
        { email: 'real@example.test', primary: true, verified: true },
      ]),
      async (client) => {
        const { user } = await client.exchangeCode('code')
        assert.equal(
          user.email,
          'real@example.test',
          'an address the verified list does not contain was used to identify the account',
        )
      },
    )
  })

  it('refuses an account with no verified address at all', async () => {
    await withGitHubAnswering(
      answering('unverified@example.test', [
        { email: 'unverified@example.test', primary: true, verified: false },
      ]),
      async (client) => {
        await assert.rejects(
          () => client.exchangeCode('code'),
          /no verified email address/,
          'an account with nothing verified was admitted',
        )
      },
    )
  })

  it('takes a verified address that is not the primary rather than turning somebody away', async () => {
    // An account whose primary is unverified still has a verified address in
    // practice. Refusing it would turn somebody away over a GitHub setting they
    // have no reason to connect to this product.
    await withGitHubAnswering(
      answering(null, [
        { email: 'unverified@example.test', primary: true, verified: false },
        { email: 'second@example.test', primary: false, verified: true },
      ]),
      async (client) => {
        const { user } = await client.exchangeCode('code')
        assert.equal(user.email, 'second@example.test')
      },
    )
  })
})
