// What a broken query tells the browser.
//
// The formatter withholds the stack, and its comment says why: a stack from
// the control plane names internal paths and table names to anyone who can
// provoke an error. The message beside it was not withheld, and drizzle writes
// a query failure as "Failed query: <the whole statement>" with the bound
// parameters after it. So the control that exists to keep the schema off a
// viewer's screen was sending the schema, the joins, the WHERE clause and the
// source comments inside the SQL, one field over.
//
// This asks the real server over its real HTTP boundary, with a query that
// really fails, rather than asserting anything about the formatter's shape. A
// unit test on the formatter would have passed against the version that leaked,
// because the formatter was doing exactly what it was written to do.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  available, startApi, seedOrg, dropOrg, signInAs, type ApiHarness, type Org,
} from './harness.ts'

const hasDatabase = await available()

describe('an internal failure', { skip: hasDatabase ? false : 'no database' }, () => {
  let h: ApiHarness
  let org: Org

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'errorshape')
  })

  after(async () => {
    if (org) await dropOrg(h.admin, org.orgId)
    if (h) await h.close()
  })

  it('tells the browser nothing about the query that broke', async () => {
    const member = await signInAs(h, org, 'owner')

    // A real failure rather than a thrown fixture: the table the route reads
    // is moved out from under it, which is what a bad migration does and what
    // produced the leak on a live preview.
    await h.admin.unsafe('ALTER TABLE environments RENAME TO environments_moved')
    let body: string
    let status: number
    try {
      const response = await h.fetch(
        `/trpc/environments.list?input=${encodeURIComponent(JSON.stringify({ limit: 5 }))}`,
        { headers: { cookie: member.cookie } },
      )
      status = response.status
      body = await response.text()
    } finally {
      await h.admin.unsafe('ALTER TABLE environments_moved RENAME TO environments')
    }

    assert.equal(status, 500)

    // The whole point, and each of these was in the response before the fix.
    // Named one at a time rather than as one regular expression so that a
    // failure says which kind of thing got out.
    assert.ok(!body.includes('Failed query'), `the driver's wrapper reached the client: ${body}`)
    assert.ok(!body.includes('SELECT'), `the statement reached the client: ${body}`)
    assert.ok(!body.includes('environments_moved'), `the table name reached the client: ${body}`)
    assert.ok(!/\bstack\b.*\bat \//.test(body), `a stack reached the client: ${body}`)

    // And it still says something, because a blank is its own defect: the
    // console renders this message under "That did not load", and an empty one
    // reads as a page that half rendered rather than as an answer.
    assert.match(body, /Something went wrong on the control plane/)
  })

  it('still passes through the messages somebody wrote for the reader', async () => {
    // The redaction is keyed on INTERNAL_SERVER_ERROR alone. Every other code
    // carries a message written for the person reading it, and blanking those
    // would turn "your role cannot see this" into a shrug. A viewer asking for
    // something only an owner may do is the cheapest one of those to provoke.
    const viewer = await signInAs(h, org, 'viewer')
    const response = await h.fetch('/trpc/environments.teardown', {
      method: 'POST',
      headers: {
        cookie: viewer.cookie,
        'content-type': 'application/json',
        'x-csrf-token': viewer.csrfToken,
      },
      body: JSON.stringify({ envId: org.envId }),
    })
    assert.equal(response.status, 403)
    const body = await response.text()
    assert.ok(
      !body.includes('Something went wrong on the control plane'),
      `a refusal was replaced by the internal message: ${body}`,
    )
  })
})
