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
//
// A third claim was added later and it is about what the refused person SEES.
// Being refused correctly and being told so are different things, and this flow
// got the first right and the second badly wrong: the primary call to action on
// the marketing site ended at a raw JSON body in the address bar, after the
// visitor had already authorised an OAuth application against their real GitHub
// account. So the assertions below cover the refusal, the page, the grant that
// refusal takes back, and the case where the installation is closed to
// everybody and nobody should be sent to GitHub at all.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { parseAllowlist, describeAllowlist, SignInError } from '../src/auth/signin.ts'
import { wantsHtml, problemHtml, problemCsp } from '../src/errorpage.ts'
import { available, startApi, type ApiHarness } from './harness.ts'

/** What a browser sends when a person navigates to an address. Chrome, Firefox
 *  and Safari all lead with text/html; nothing else this server serves does. */
const BROWSER = { accept: 'text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8' }

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

describe('deciding what a caller asked for', () => {
  test('a browser navigation asks for a page', () => {
    assert.equal(wantsHtml(BROWSER.accept), true)
    assert.equal(wantsHtml('text/html'), true)
    assert.equal(wantsHtml('TEXT/HTML;q=0.9'), true)
  })

  test('and everything else does not', () => {
    // The rule that keeps this safe to apply to routes with existing clients.
    // A wildcard is what `fetch` with no Accept and `curl` both send, and
    // reading it as "anything, so give it HTML" would change the answer every
    // script already written against this API receives.
    assert.equal(wantsHtml('*/*'), false)
    assert.equal(wantsHtml('application/json'), false)
    assert.equal(wantsHtml(undefined), false)
    assert.equal(wantsHtml(''), false)
    // Not a prefix match. text/htmlish is not text/html.
    assert.equal(wantsHtml('text/htmlish'), false)
  })
})

describe('the page itself', () => {
  test('escapes every value it is given', () => {
    const html = problemHtml(
      {
        status: 403,
        error: 'x',
        title: '<script>alert(1)</script>',
        body: ['it said "no" & meant it'],
        actions: [{ href: 'https://x.test/?a=1&b=2', label: '<b>go</b>' }],
      },
      'nonce-value',
    )
    assert.ok(!html.includes('<script>'), 'a title reached the document as markup')
    assert.ok(html.includes('&lt;script&gt;'))
    assert.ok(html.includes('&amp; meant it'))
    assert.ok(html.includes('href="https://x.test/?a=1&amp;b=2"'))
    assert.ok(html.includes('&lt;b&gt;go&lt;/b&gt;'))
  })

  test('carries its own policy, and that policy allows its own stylesheet', () => {
    // The defect this replaces: the API's global middleware applies
    // `default-src 'none'` to any response that did not set a policy, which has
    // no style-src, so a hand-written page renders as unstyled text. It shipped
    // that way for a week last time, and the test that missed it asserted a
    // substring both policies contained. So this asserts the whole string.
    const csp = problemCsp('abc123')
    assert.equal(
      csp,
      "default-src 'none'; style-src 'nonce-abc123'; form-action 'none'; " +
        "base-uri 'none'; frame-ancestors 'none'",
    )
    assert.ok(!csp.includes('unsafe-inline'), 'the page bought its style with a blanket permission')
  })

  test('has no script in it at all', () => {
    const html = problemHtml(
      { status: 403, error: 'x', title: 'Refused', body: ['because'] },
      'n',
    )
    assert.ok(!/<script/i.test(html))
  })
})

describe('signing in against the allowlist', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let api: ApiHarness

  before(async () => {
    api = await startApi({
      signInAllowlist: new Set(['on-the-list']),
      // The hosted planes set this. It is what turns "you cannot come in" into
      // "you cannot come in, here is the list you were one click away from".
      signupUrl: 'https://antifailure.test/signup',
    })
  })
  after(async () => {
    await api.admin`DELETE FROM users WHERE github_login IN ('a-stranger', 'on-the-list', 'refused-twice')`
    await api.close()
  })

  /** Drives the real two-step exchange: begin, then come back with the code. */
  async function signIn(login: string, headers: Record<string, string> = {}): Promise<Response> {
    const begin = await api.fetch('/auth/github', { redirect: 'manual' })
    const state = new URL(String(begin.headers.get('location'))).searchParams.get('state')
    const code = api.github.approve(login)
    return api.fetch(`/auth/github/callback?code=${code}&state=${state}`, {
      redirect: 'manual',
      headers,
    })
  }

  test('a GitHub account that is not on the list is refused', async () => {
    api.github.addUser({ id: 900001, login: 'a-stranger', email: 'stranger@example.test', name: 'A Stranger' })
    const res = await signIn('a-stranger', { accept: 'application/json' })

    // 403 rather than 400. The request was perfectly well formed and was
    // understood; the server will not authorize it. 400 said the caller had
    // made a mistake they could correct, and there is nothing about the request
    // they could change that would work.
    assert.equal(res.status, 403)
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

  test('and takes back the authorization the refused person just granted', async () => {
    // The thing the ordering cannot fix. A non-empty allowlist is keyed on the
    // GitHub login, the login only arrives with the code exchange, and no part
    // of the request before the redirect carries it, so this refusal HAS to
    // happen after the visitor has pressed Authorize. What can be done is give
    // the grant straight back, so being turned away does not also cost them a
    // third party application sitting on their GitHub account.
    assert.deepEqual(api.github.authorizationsRevoked(), ['a-stranger'])
  })

  test('a browser gets a page, and it is not the JSON body', async () => {
    api.github.addUser({ id: 900003, login: 'refused-twice', email: 'twice@example.test', name: 'Twice' })
    const res = await signIn('refused-twice', BROWSER)
    const html = await res.text()

    // Content type first, deliberately. This is the defect, and a test that
    // checked the status first would report a number rather than the fact that
    // a person navigating here was handed a JSON body to read.
    assert.ok(!html.trimStart().startsWith('{'), 'the browser was served a JSON body')
    assert.match(String(res.headers.get('content-type')), /^text\/html/)
    assert.match(html, /<!doctype html>/i)
    assert.equal(res.status, 403)
    // It says what happened, in words a person reads rather than a key and a
    // quoted string.
    assert.match(html, /You have not been invited yet/)
    // And it gives them somewhere to go, which is the whole point.
    assert.match(html, /href="https:\/\/antifailure\.test\/signup"/)
    // Not "Join the waitlist", which is what this label was and what it said
    // for as long as there was a list behind the link. The list is gone, and so
    // is the promise beside it: the page it points at now writes a row a person
    // reads, on a domain that could never have mailed anybody back.
    assert.match(html, /Ask for access/)
    assert.doesNotMatch(
      html,
      /we will tell you when it opens/i,
      'the refusal page promises a message on a domain that authorizes no sender',
    )
    assert.equal(res.headers.get('set-cookie'), null, 'the page issued a session cookie')
  })

  test('the page sets a policy that lets its own stylesheet run', async () => {
    // The API's global middleware applies `default-src 'none'` to anything that
    // did not set a policy of its own, and that has no style-src. A page it
    // reached would render as unstyled text and still answer 403 with the right
    // words, which is exactly how the last hand-written page here shipped
    // broken for a week.
    const res = await signIn('refused-twice', BROWSER)
    const csp = String(res.headers.get('content-security-policy'))
    assert.match(csp, /style-src 'nonce-/)
    const nonce = /style-src 'nonce-([^']+)'/.exec(csp)?.[1]
    assert.ok(nonce, 'no nonce in the policy')
    assert.match(await res.text(), new RegExp(`<style nonce="${nonce!.replace(/[+/=]/g, (c) => '\\' + c)}"`))
  })

  test('and says the grant is gone only when it actually is', async () => {
    // A reassurance that might not be true is worse than none. With GitHub
    // refusing the withdrawal the page must not claim it happened, and must
    // tell the person where to undo it themselves.
    api.github.refuseAuthorizationRevocation()
    try {
      const html = await (await signIn('refused-twice', BROWSER)).text()
      assert.doesNotMatch(html, /has already been withdrawn/)
      assert.match(html, /Applications in your GitHub settings/)
    } finally {
      api.github.refuseAuthorizationRevocation(false)
    }
  })

  test('an account on the list signs in', async () => {
    // The negative control. Without it, a server that refused everybody would
    // pass every assertion above.
    api.github.addUser({ id: 900002, login: 'on-the-list', email: 'ok@example.test', name: 'Allowed' })
    const res = await signIn('on-the-list')

    assert.equal(res.status, 302)
    assert.match(String(res.headers.get('set-cookie')), /af_session=/)
  })

  test('and the invited person keeps their authorization', async () => {
    // The other side of the revocation. Taking the grant back from somebody who
    // was let in would sign them out of GitHub's view of this app the moment
    // they arrived.
    assert.ok(!api.github.authorizationsRevoked().includes('on-the-list'))
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

describe('telling one refusal from another', () => {
  test('a refusal with no reason given is not read as an expired link', () => {
    // The default matters more than it looks. Every SignInError thrown later,
    // by code nobody has written yet, lands on it. syncMembership refusing to
    // apply an empty member list is one that exists today, and answering that
    // person "your link expired, start again" sends them to press the same
    // button until they give up.
    assert.equal(new SignInError('something else went wrong').refusal, 'exchange-failed')
    assert.equal(new SignInError('x').authorizationRevoked, false)
  })

  test('and the two that really are expired links say so', () => {
    assert.equal(new SignInError('x', { refusal: 'link-expired' }).refusal, 'link-expired')
  })
})

describe('a sign-in that came back with nothing', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let api: ApiHarness

  before(async () => {
    api = await startApi()
  })
  after(async () => {
    await api.close()
  })

  test('gets a page that says to start again, and a way to', async () => {
    const res = await api.fetch('/auth/github/callback?code=gone&state=gone', {
      redirect: 'manual',
      headers: BROWSER,
    })
    const html = await res.text()

    assert.equal(res.status, 400)
    assert.match(String(res.headers.get('content-type')), /^text\/html/)
    assert.match(html, /This sign-in link is no longer valid/)
    assert.match(html, /href="\/auth\/github"/)
    assert.match(html, /Start again/)
  })

  test('and a script still gets the same JSON body it always did', async () => {
    const res = await api.fetch('/auth/github/callback?code=gone&state=gone', {
      redirect: 'manual',
      headers: { accept: 'application/json' },
    })
    assert.equal(res.status, 400)
    assert.deepEqual(await res.json(), {
      error: 'This sign-in link is no longer valid. Start again.',
    })
  })
})

describe('an installation closed to everybody', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  // The one case the ordering CAN fix, and the one this deployment shape
  // actually hits. An allowlist that names nobody refuses every GitHub account
  // there is, which is a property of the deployment rather than of the visitor,
  // so it is knowable before the browser leaves. Sending somebody to GitHub to
  // authorise an application in order to tell them an answer already known is
  // asking for something in exchange for nothing.
  let api: ApiHarness

  before(async () => {
    api = await startApi({ signInAllowlist: new Set(), signupUrl: 'https://antifailure.test/signup' })
    // The suite above completed real exchanges against this same database, so
    // the table is cleared here rather than assumed empty. The claim below is
    // "this route wrote nothing", and it can only be read off a known start.
    await api.admin`DELETE FROM oauth_states`
  })
  after(async () => {
    await api.close()
  })

  test('never sends the browser to GitHub', async () => {
    const res = await api.fetch('/auth/github', { redirect: 'manual', headers: BROWSER })

    assert.equal(res.status, 403)
    assert.equal(res.headers.get('location'), null, 'the browser was sent to GitHub anyway')
    const html = await res.text()
    assert.match(html, /not open for sign-ups/)
    assert.match(html, /href="https:\/\/antifailure\.test\/signup"/)
  })

  test('and does not even record a state, so nothing was started', async () => {
    // The proof that the refusal is BEFORE the exchange rather than early
    // inside it. A row here would mean the server had begun a sign-in it was
    // always going to refuse.
    const rows = await api.admin<{ n: number }[]>`SELECT count(*)::int AS n FROM oauth_states`
    assert.equal(rows[0]!.n, 0)
  })

  test('and a script still gets the same sentence as JSON', async () => {
    const res = await api.fetch('/auth/github', {
      redirect: 'manual',
      headers: { accept: 'application/json' },
    })
    assert.equal(res.status, 403)
    assert.match(String(res.headers.get('content-type')), /application\/json/)
    assert.match(JSON.stringify(await res.json()), /not open for sign-ups/)
  })
})

describe('a self-hosted installation with an allowlist and nowhere to send anybody', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  // AF_SIGNUP_URL unset is the self-hosted default. The page must not invent a
  // link: an operator running their own allowlist has their own way of being
  // asked, and pointing their users at the vendor's contact form would be wrong.
  let api: ApiHarness

  before(async () => {
    api = await startApi({ signInAllowlist: new Set() })
  })
  after(async () => {
    await api.close()
  })

  test('offers no link, and says who to ask instead', async () => {
    const html = await (
      await api.fetch('/auth/github', { redirect: 'manual', headers: BROWSER })
    ).text()

    assert.doesNotMatch(html, /waitlist/i)
    assert.doesNotMatch(html, /Ask for access/)
    assert.doesNotMatch(html, /antifailure\.dev/)
    assert.doesNotMatch(html, /<a /, 'a page with nowhere to send anybody rendered a link')
    assert.match(html, /Ask an owner of this installation/)
  })
})
