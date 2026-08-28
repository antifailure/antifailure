// Signups are closed, and this is what "closed" means.
//
// The hosted staging deployment runs with an allowlist while the product is not
// open to the public. Two claims have to hold and they are different:
//
//   1. An account that is not on the list cannot sign in AT ALL. Not "signs in
//      and sees an empty page": the exchange is refused and nothing is written,
//      so there is no half-account left behind to reason about later.
//   2. An account that IS on the list still sees nothing until an installation
//      exists for one of its organizations. Being let through the door is not
//      the same as being given a tenant.
//
// Both are asserted, because the security property is the pair. A change that
// removed either would leave the other passing.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { parseAllowlist, describeAllowlist } from '../src/auth/signin.ts'
import { available, startApi, type ApiHarness } from './harness.ts'

describe('reading the allowlist', () => {
  test('unset is open, because a self-hosted operator already chose who reaches the instance', () => {
    assert.equal(parseAllowlist(undefined), null)
    assert.equal(parseAllowlist(null), null)
    assert.match(describeAllowlist(null), /OPEN/)
  })

  test('set but empty is closed to everyone, not open', () => {
    // The dangerous reading. AF_SIGNIN_ALLOWLIST="" is far more likely to be a
    // deployment script that lost a value than a decision to let the world in,
    // so the ambiguous configuration resolves closed.
    const list = parseAllowlist('')
    assert.notEqual(list, null)
    assert.equal(list?.size, 0)
    assert.match(describeAllowlist(list), /CLOSED TO EVERYONE/)
  })

  test('separators and case do not matter, because a person edits this by hand', () => {
    const list = parseAllowlist(' VirSanghavi,maksymrajszewski\n  someoneElse ')
    assert.deepEqual([...(list ?? [])].sort(), ['maksymrajszewski', 'someoneelse', 'virsanghavi'])
    assert.ok(list?.has('virsanghavi'))
  })
})

describe('signing in against the allowlist', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let api: ApiHarness

  before(async () => {
    api = await startApi({ signInAllowlist: new Set(['on-the-list']) })
  })
  after(async () => {
    await api.admin`DELETE FROM users WHERE github_login IN ('a-stranger', 'on-the-list')`
    await api.close()
  })

  /** Drives the real two-step exchange: begin, then come back with the code. */
  async function signIn(login: string): Promise<Response> {
    const begin = await api.fetch('/auth/github', { redirect: 'manual' })
    const state = new URL(String(begin.headers.get('location'))).searchParams.get('state')
    const code = api.github.approve(login)
    return api.fetch(`/auth/github/callback?code=${code}&state=${state}`, { redirect: 'manual' })
  }

  test('a GitHub account that is not on the list is refused', async () => {
    api.github.addUser({ id: 900001, login: 'a-stranger', email: 'stranger@example.test', name: 'A Stranger' })
    const res = await signIn('a-stranger')

    assert.equal(res.status, 400)
    assert.match(JSON.stringify(await res.json()), /not open for sign-ups/)
    assert.equal(res.headers.get('set-cookie'), null, 'a refused sign-in issued a session cookie')
  })

  test('and leaves no user row behind', async () => {
    // The half that matters. A refusal that still creates the account has only
    // postponed the problem to whenever somebody adds a membership by hand.
    const rows = await api.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM users WHERE github_login = 'a-stranger'`
    assert.equal(rows[0]!.n, 0)
  })

  test('an account on the list signs in', async () => {
    // The negative control. Without it, a server that refused everybody would
    // pass every assertion above.
    api.github.addUser({ id: 900002, login: 'on-the-list', email: 'ok@example.test', name: 'Allowed' })
    const res = await signIn('on-the-list')

    assert.equal(res.status, 302)
    assert.match(String(res.headers.get('set-cookie')), /af_session=/)
  })

  test('but lands in no organization, because being allowed in is not being given a tenant', async () => {
    // There is no installation for this person's organizations, so they have a
    // session and no tenant. Every procedure needs an organization to scope to,
    // so this is the state in which the console shows "you are not a member of
    // anything" rather than somebody else's environments.
    const rows = await api.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM members m
      JOIN users u ON u.id = m.user_id WHERE u.github_login = 'on-the-list'`
    assert.equal(rows[0]!.n, 0)
  })
})
