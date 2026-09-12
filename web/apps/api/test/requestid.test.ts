// The id a person can quote, on every error the console shows.
//
// The request id existed before this file: minted per request, echoed on
// x-request-id, written on the 500 log line and into the 500 body of the raw
// routes. The console does not use the raw routes. It talks to /trpc/* for
// almost everything, and the tRPC error formatter and the onError log line
// both dropped the id, so a browser error card had a fixed sentence pointing
// at the logs and nothing to search the logs by. Separately, the six cross-site
// refusals in server.ts returned 403 with no log line and no id at all, and
// both cross-site defects found on launch night were found by somebody pasting
// screenshots of the sentence, which is what that looks like from outside.
//
// Everything here goes over the real HTTP boundary, because the bug was in the
// seam between two layers that were each doing what they were written to do.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomBytes, randomUUID } from 'node:crypto'
import {
  available, startApi, seedOrg, dropOrg, signInAs, type ApiHarness, type Org, type SignedIn,
} from './harness.ts'

const hasDatabase = await available()

/** Everything console.error and console.warn were handed while `fn` ran. */
async function capturing<T>(fn: () => Promise<T>): Promise<{ result: T; lines: unknown[][] }> {
  const lines: unknown[][] = []
  const realError = console.error
  const realWarn = console.warn
  console.error = (...args: unknown[]) => lines.push(args)
  console.warn = (...args: unknown[]) => lines.push(args)
  try {
    return { result: await fn(), lines }
  } finally {
    console.error = realError
    console.warn = realWarn
  }
}

/** The one structured line whose requestId is `id`, or a failure naming what was logged. */
function lineFor(lines: unknown[][], id: string): { head: string; fields: Record<string, unknown> } {
  const found = lines.find((args) => {
    const fields = args[1]
    return typeof fields === 'object' && fields !== null && (fields as { requestId?: unknown }).requestId === id
  })
  assert.ok(found, `no log line carried requestId ${id}; logged: ${JSON.stringify(lines)}`)
  return { head: String(found[0]), fields: found[1] as Record<string, unknown> }
}

describe('a tRPC error carries the request id', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'reqid')
  })

  after(async () => {
    if (org) await dropOrg(h.admin, org.orgId)
    if (h) await h.close()
  })

  it('in the body, and it is the same id as the header', async () => {
    const response = await h.fetch('/trpc/environments.list?input=%7B%22limit%22%3A5%7D')
    assert.equal(response.status, 401)
    const header = response.headers.get('x-request-id')
    assert.ok(header, 'the response carried no x-request-id header')
    const body = (await response.json()) as { error: { data: { code: string; requestId?: unknown } } }
    assert.equal(body.error.data.code, 'UNAUTHORIZED')
    assert.equal(body.error.data.requestId, header)
  })

  it('under the key the console reads, honouring a caller-supplied id end to end', async () => {
    // A supplied id is what a trace crossing a proxy carries, and it is the
    // cheapest way to prove the id in the body is the request's own rather
    // than one the formatter minted for itself.
    const response = await h.fetch('/trpc/environments.list?input=%7B%22limit%22%3A5%7D', {
      headers: { 'x-request-id': 'quote-me-0123' },
    })
    assert.equal(response.headers.get('x-request-id'), 'quote-me-0123')
    const body = (await response.json()) as { error: { data: { requestId?: unknown } } }
    assert.equal(body.error.data.requestId, 'quote-me-0123')
  })

  it('an internal failure keeps its fixed sentence, adds the id, and the log line carries the same id', async () => {
    const member = await signInAs(h, org, 'owner')
    await h.admin.unsafe('ALTER TABLE environments RENAME TO environments_moved')
    let status: number
    let text: string
    let header: string | null
    let lines: unknown[][]
    try {
      const captured = await capturing(() =>
        h.fetch(`/trpc/environments.list?input=${encodeURIComponent(JSON.stringify({ limit: 5 }))}`, {
          headers: { cookie: member.cookie },
        }),
      )
      lines = captured.lines
      status = captured.result.status
      header = captured.result.headers.get('x-request-id')
      text = await captured.result.text()
    } finally {
      await h.admin.unsafe('ALTER TABLE environments_moved RENAME TO environments')
    }
    assert.equal(status, 500)
    assert.ok(header, 'no x-request-id header on the 500')

    // The redaction rule is unchanged: the fixed sentence and nothing of the
    // query. The id is the only thing added.
    const body = JSON.parse(text) as { error: { message: string; data: { code: string; requestId?: unknown; stack?: unknown } } }
    assert.equal(body.error.message, 'Something went wrong on the control plane. Nothing was changed, and the reason is in its logs.')
    assert.equal(body.error.data.code, 'INTERNAL_SERVER_ERROR')
    assert.equal(body.error.data.requestId, header)
    assert.equal(body.error.data.stack, undefined)
    assert.ok(!text.includes('environments_moved'), `the table name reached the client: ${text}`)

    // The other half: the line the operator searches for, by that id, with
    // the code and the path beside it.
    const line = lineFor(lines, header)
    assert.equal(line.head, 'trpc procedure failed')
    assert.equal(line.fields.code, 'INTERNAL_SERVER_ERROR')
    assert.equal(line.fields.path, 'environments.list')
    assert.equal(line.fields.type, 'query')
  })
})

describe('a cross-site refusal is written down and quotable', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org
  let member: SignedIn
  let adminCookie: string
  let adminCsrf: string

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'reqid-csrf')
    member = await signInAs(h, org, 'owner')

    // An operator session written directly, for the same reason admincsrf.test.ts
    // writes one: the sign-in route is rate limited by address and this is a
    // test of the transport gate, which reads only the cookie and the header.
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO admin_users (email, name, role)
      VALUES (${`reqid-${randomUUID().slice(0, 8)}@example.test`}, 'Request id operator', 'owner')
      RETURNING id`
    const token = randomBytes(32).toString('base64url')
    await h.admin`
      INSERT INTO admin_sessions (token_hash, admin_user_id, expires_at)
      VALUES (${createHash('sha256').update(token).digest()}, ${row!.id},
              ${new Date(Date.now() + 3_600_000).toISOString()})`
    adminCookie = `af_admin_session=${token}`
    const session = await h.fetch('/v1/admin/session', { headers: { cookie: adminCookie } })
    assert.equal(session.status, 200)
    adminCsrf = ((await session.json()) as { csrfToken: string }).csrfToken
  })

  after(async () => {
    await h.admin`DELETE FROM admin_sessions WHERE admin_user_id IN (
      SELECT id FROM admin_users WHERE email LIKE 'reqid-%')`
    await h.admin`DELETE FROM admin_audit_entries WHERE actor_label LIKE 'reqid-%'`
    await h.admin`DELETE FROM admin_users WHERE email LIKE 'reqid-%'`
    if (org) await dropOrg(h.admin, org.orgId)
    if (h) await h.close()
  })

  /**
   * One refusal, checked the same way six times: a 403, the fixed body shape
   * with the id in it, the id on the header, and one structured log line by
   * that id naming the gate and the reason, with no token in it.
   */
  async function refused(
    request: () => Promise<Response>,
    expect: { gate: string; why: string; message: RegExp; secrets: string[] },
  ) {
    const { result: response, lines } = await capturing(request)
    const text = await response.text()
    assert.equal(response.status, 403, text)
    const header = response.headers.get('x-request-id')
    assert.ok(header, 'no x-request-id header on the refusal')
    const body = JSON.parse(text) as {
      error: { code: string; message: string; resolution: string }
      requestId: string
    }
    assert.equal(body.error.code, 'AF-CP-004')
    assert.match(body.error.message, expect.message)
    assert.match(body.error.resolution, /requestId/)
    assert.equal(body.requestId, header)

    const line = lineFor(lines, header)
    assert.equal(line.head, 'cross-site request refused')
    assert.equal(line.fields.gate, expect.gate)
    assert.equal(line.fields.why, expect.why)
    assert.equal(line.fields.method, 'POST')
    const logged = JSON.stringify(lines)
    for (const secret of expect.secrets) {
      assert.ok(!logged.includes(secret), `a credential reached the log: ${logged}`)
    }
    return { body, line }
  }

  const json = { 'content-type': 'application/json' }

  it('POST /auth/device/approve', async () => {
    await refused(
      () => h.fetch('/auth/device/approve', {
        method: 'POST', headers: { ...json, cookie: member.cookie }, body: JSON.stringify({ user_code: 'ABCD-EFGH' }),
      }),
      { gate: 'customer-csrf', why: 'header-missing', message: /x-antifailure-csrf header from GET \/auth\/session/, secrets: [member.token, member.csrfToken] },
    )
  })

  it('POST /auth/device/deny', async () => {
    await refused(
      () => h.fetch('/auth/device/deny', {
        method: 'POST', headers: { ...json, cookie: member.cookie, 'x-antifailure-csrf': 'not-the-token' }, body: JSON.stringify({ user_code: 'ABCD-EFGH' }),
      }),
      { gate: 'customer-csrf', why: 'header-mismatch', message: /x-antifailure-csrf header from GET \/auth\/session/, secrets: [member.token, member.csrfToken, 'not-the-token'] },
    )
  })

  it('POST /auth/invitation/accept', async () => {
    await refused(
      () => h.fetch('/auth/invitation/accept', {
        method: 'POST', headers: { ...json, cookie: member.cookie }, body: JSON.stringify({ token: 'whatever' }),
      }),
      { gate: 'customer-csrf', why: 'header-missing', message: /x-antifailure-csrf header from GET \/auth\/session/, secrets: [member.token, member.csrfToken] },
    )
  })

  it('a customer mutation on /trpc', async () => {
    const { line } = await refused(
      () => h.fetch('/trpc/environments.teardown', {
        method: 'POST', headers: { ...json, cookie: member.cookie }, body: JSON.stringify({ envId: org.envId }),
      }),
      { gate: 'customer-csrf', why: 'header-missing', message: /x-antifailure-csrf header from GET \/auth\/session/, secrets: [member.token, member.csrfToken] },
    )
    assert.equal(line.fields.procedure, '/trpc/environments.teardown')
  })

  it('an operator mutation on /trpc that declares another site as its origin', async () => {
    await refused(
      () => h.fetch('/trpc/admin.flags.kill', {
        method: 'POST',
        headers: {
          ...json, cookie: adminCookie, 'x-antifailure-admin-csrf': adminCsrf,
          'sec-fetch-site': 'cross-site', origin: 'https://not-this-product.example',
        },
        body: JSON.stringify({ key: 'nothing.here', reason: 'a reason long enough' }),
      }),
      { gate: 'operator-origin', why: 'cross-site-origin', message: /came from another site/, secrets: [adminCookie.split('=')[1]!, adminCsrf] },
    )
  })

  it('an operator mutation on /trpc with no operator token', async () => {
    await refused(
      () => h.fetch('/trpc/admin.flags.kill', {
        method: 'POST', headers: { ...json, cookie: adminCookie },
        body: JSON.stringify({ key: 'nothing.here', reason: 'a reason long enough' }),
      }),
      { gate: 'operator-csrf', why: 'header-missing', message: /x-antifailure-admin-csrf/, secrets: [adminCookie.split('=')[1]!, adminCsrf] },
    )
  })

  it('the procedure on the log line is bounded and filtered, so a caller cannot forge a line', async () => {
    const hostile = '/trpc/environments.teardown%0Across-site%20request%20refused%20' + 'a'.repeat(400)
    const { lines } = await capturing(() =>
      h.fetch(hostile, { method: 'POST', headers: { ...json, cookie: member.cookie }, body: '{}' }),
    )
    const line = lines.find((args) => args[0] === 'cross-site request refused')
    assert.ok(line, `nothing was logged: ${JSON.stringify(lines)}`)
    const procedure = (line[1] as { procedure: string }).procedure
    assert.ok(!procedure.includes('\n'), `a newline reached the log: ${JSON.stringify(procedure)}`)
    assert.ok(!procedure.includes(' '), `a space reached the log: ${JSON.stringify(procedure)}`)
    assert.ok(procedure.length <= 200, `an unbounded path reached the log: ${procedure.length}`)
  })
})
