// The document against the wire, rather than the document against itself.
//
// openapi.test.ts asks whether the document is well formed. This asks the
// question that actually costs somebody a morning: when the service answers
// 503, or 500, or 403, is the body the shape the document promised for that
// status.
//
// It was not. The readiness 503 was declared as an error envelope and carries
// no `error` member at all, so a generated client read `undefined` and reported
// the service healthy. The generic 500's `code` is a string where the shared
// schema said integer. Ingestion could answer 400 and 403 and the document
// listed neither. None of that is visible from reading the document, and all of
// it is one request away.

import { describe, it, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID, createHash } from 'node:crypto'
import { inspect } from 'node:util'
import type { Pool } from '@antifailure/db'
import { createServer } from '../src/server.ts'
import { FakeClock } from '../src/clock.ts'
import { FakeGitHub } from '../src/auth/fakegithub.ts'
import { openApiDocument } from '../src/openapi.ts'
import { MAX_BATCH } from '../src/ingest.ts'
import { available, startApi, seedOrg, dropOrg, type ApiHarness, type Org } from './harness.ts'

const document = openApiDocument() as {
  paths: Record<
    string,
    Record<
      string,
      {
        responses: Record<string, { content?: Record<string, { schema: Schema }> }>
        requestBody?: { content: Record<string, { schema: Schema }> }
      }
    >
  >
  components: { schemas: Record<string, Schema> }
}

type Schema = Record<string, unknown>

/**
 * A JSON Schema checker over exactly the keywords this document uses.
 *
 * Deliberately not a permissive one. An unknown keyword throws rather than
 * being skipped, because a checker that quietly ignores the constraint it does
 * not understand reports success over the part it never looked at, which is the
 * failure this whole file exists to catch, one level down. Standard 19.
 */
const ANNOTATIONS = new Set(['description', 'examples', 'default', 'title', 'format', 'summary'])

function validate(schema: Schema, value: unknown, where: string): string[] {
  if (typeof schema.$ref === 'string') {
    const name = schema.$ref.replace('#/components/schemas/', '')
    const target = document.components.schemas[name]
    if (!target) return [`${where}: $ref ${schema.$ref} resolves to nothing`]
    return validate(target, value, where)
  }

  const problems: string[] = []
  for (const keyword of Object.keys(schema)) {
    if (ANNOTATIONS.has(keyword)) continue
    if (
      ![
        'type', 'required', 'properties', 'additionalProperties', 'items',
        'enum', 'const', 'minimum', 'maximum', 'maxItems', 'minLength', '$ref',
      ].includes(keyword)
    ) {
      throw new Error(
        `${where}: this checker does not implement the ${keyword} keyword, so it would ` +
          'report success over a constraint it never applied. Implement it or remove it.',
      )
    }
  }

  const type = schema.type as string | undefined
  if (type === 'object') {
    if (value === null || typeof value !== 'object' || Array.isArray(value)) {
      return [`${where}: expected an object, got ${shapeOf(value)}`]
    }
    const record = value as Record<string, unknown>
    const properties = (schema.properties ?? {}) as Record<string, Schema>
    for (const name of (schema.required ?? []) as string[]) {
      if (!(name in record)) problems.push(`${where}: missing required member ${name}`)
    }
    if (schema.additionalProperties === false) {
      for (const name of Object.keys(record)) {
        if (!(name in properties)) {
          problems.push(`${where}: member ${name} is on the wire and not in the schema`)
        }
      }
    }
    for (const [name, child] of Object.entries(properties)) {
      if (name in record) problems.push(...validate(child, record[name], `${where}.${name}`))
    }
    return problems
  }

  if (type === 'array') {
    if (!Array.isArray(value)) return [`${where}: expected an array, got ${shapeOf(value)}`]
    if (typeof schema.maxItems === 'number' && value.length > schema.maxItems) {
      problems.push(`${where}: ${value.length} items exceeds maxItems ${schema.maxItems}`)
    }
    const items = schema.items as Schema | undefined
    if (items) value.forEach((item, i) => problems.push(...validate(items, item, `${where}[${i}]`)))
    return problems
  }

  if (type === 'string' && typeof value !== 'string') {
    problems.push(`${where}: expected a string, got ${shapeOf(value)}`)
  }
  if (type === 'integer' && !Number.isInteger(value)) {
    problems.push(`${where}: expected an integer, got ${shapeOf(value)}`)
  }
  if (type === 'number' && typeof value !== 'number') {
    problems.push(`${where}: expected a number, got ${shapeOf(value)}`)
  }
  if (type === 'boolean' && typeof value !== 'boolean') {
    problems.push(`${where}: expected a boolean, got ${shapeOf(value)}`)
  }
  if ('const' in schema && value !== schema.const) {
    problems.push(`${where}: expected the constant ${JSON.stringify(schema.const)}, got ${shapeOf(value)}`)
  }
  if (Array.isArray(schema.enum) && !schema.enum.includes(value)) {
    problems.push(`${where}: ${shapeOf(value)} is not one of ${JSON.stringify(schema.enum)}`)
  }
  if (typeof schema.minimum === 'number' && typeof value === 'number' && value < schema.minimum) {
    problems.push(`${where}: ${value} is below the minimum ${schema.minimum}`)
  }
  if (typeof schema.minLength === 'number' && typeof value === 'string' && value.length < schema.minLength) {
    problems.push(`${where}: ${JSON.stringify(value)} is shorter than minLength ${schema.minLength}`)
  }
  return problems
}

function shapeOf(value: unknown): string {
  if (value === null) return 'null'
  if (Array.isArray(value)) return 'an array'
  return typeof value
}

/** The schema the document promises for one method, path and status. */
function declared(method: string, path: string, status: number): Schema {
  const operation = document.paths[path]?.[method]
  assert.ok(operation, `the document has no ${method.toUpperCase()} ${path}`)
  const response = operation.responses[String(status)]
  assert.ok(response, `${method.toUpperCase()} ${path} does not declare a ${status}`)
  const schema = response.content?.['application/json']?.schema
  assert.ok(schema, `${method.toUpperCase()} ${path} ${status} declares no JSON schema`)
  return schema
}

async function conforms(
  response: Response,
  method: string,
  path: string,
  status: number,
): Promise<unknown> {
  assert.equal(response.status, status, `${method.toUpperCase()} ${path} answered ${response.status}`)
  assert.match(
    response.headers.get('content-type') ?? '',
    /^application\/json/,
    `${method.toUpperCase()} ${path} ${status} is not JSON`,
  )
  const body = await response.json()
  const problems = validate(declared(method, path, status), body, `${status} body`)
  assert.deepEqual(
    problems,
    [],
    `${method.toUpperCase()} ${path} ${status} does not match its declared schema:\n  ` +
      `${problems.join('\n  ')}\nbody was ${JSON.stringify(body)}`,
  )
  return body
}

describe('the checker itself', () => {
  // Standard 24. Every assertion below is "no problems were found", and a
  // checker that finds nothing wrong with anything produces exactly that
  // reading over a broken service.
  it('reports the mismatches it exists to report', () => {
    const schema: Schema = {
      type: 'object',
      required: ['error'],
      properties: { error: { type: 'string' }, retryAfterSeconds: { type: 'integer', minimum: 0 } },
      additionalProperties: false,
    }
    assert.deepEqual(validate(schema, { error: 'no' }, 'b'), [])
    assert.deepEqual(validate(schema, {}, 'b'), ['b: missing required member error'])
    assert.deepEqual(validate(schema, { error: 7 }, 'b'), ['b.error: expected a string, got number'])
    assert.deepEqual(validate(schema, { error: 'x', stack: 'y' }, 'b'), [
      'b: member stack is on the wire and not in the schema',
    ])
    assert.deepEqual(validate(schema, { error: 'x', retryAfterSeconds: 1.5 }, 'b'), [
      'b.retryAfterSeconds: expected an integer, got number',
    ])
    // The readiness 503 against the error envelope it used to be declared as,
    // which is the exact mismatch this file was written for.
    const asRefusal = validate(
      document.components.schemas.Refusal!,
      { ready: false, version: 'dev', commit: 'unknown', reason: 'no' },
      'b',
    )
    assert.ok(asRefusal.length > 0, 'a readiness body validated against the refusal envelope')
  })

  it('refuses to pass over a keyword it cannot apply', () => {
    assert.throws(
      () => validate({ type: 'string', pattern: '^a' }, 'b', 'x'),
      /does not implement the pattern keyword/,
    )
  })
})

describe('the wire, with no database at all', () => {
  const clock = new FakeClock()
  // Every transaction rejects, which is what an unreachable database, a refused
  // password and a database with no schema all look like from in here.
  function poolThatThrows(message: string): Pool {
    const fail = async (): Promise<never> => {
      throw new Error(message)
    }
    return { withTenant: fail, withoutTenant: fail, sql: null as never, close: async () => {} } as unknown as Pool
  }

  // A connection failure, which is what readiness is built to report.
  const { app } = createServer({
    pool: poolThatThrows('password authentication failed for user "af_app"'),
    github: new FakeGitHub(clock),
    clock,
    secureCookies: false,
  })
  const call = (path: string, init?: RequestInit) =>
    app.fetch(new Request(`http://api.test${path}`, init))

  // A QUERY failure, which is a different message and the dangerous one. This
  // is the exact text Drizzle produces, statement and parameters included, and
  // it is why the handler logs the error's class and not the error. Testing the
  // 500 against the connection message above would have proved nothing: it
  // carries no statement, so a handler that logged the whole message would have
  // passed.
  const { app: queryFails } = createServer({
    pool: poolThatThrows(
      'Failed query: select id, org_id from engine_tokens where token_hash = $1 -- params: [secret]',
    ),
    github: new FakeGitHub(clock),
    clock,
    secureCookies: false,
  })

  it('answers /health with the shape the document declares', async () => {
    await conforms(await call('/health'), 'get', '/health', 200)
  })

  it('answers the readiness 503 with a readiness body, not an error envelope', async () => {
    const body = (await conforms(await call('/readyz'), 'get', '/readyz', 503)) as {
      ready: boolean
      reason: string
    }
    assert.equal(body.ready, false)
    assert.match(body.reason, /password authentication failed/)
  })

  it('answers an unexpected failure with the ServerFailure body and a correlation id', async () => {
    // The log is asserted, not just the response. The whole reason this handler
    // does not log the error object is that Drizzle writes the statement and
    // its parameters into `message`, and a correlation id is what replaces it:
    // if the id were added and the message logged anyway, the response would
    // look identical and the leak would be complete.
    const logged: unknown[][] = []
    const realError = console.error
    console.error = (...args: unknown[]) => {
      logged.push(args)
    }

    const response = await queryFails.fetch(new Request('http://api.test/v1/events', {
      method: 'POST',
      // Long enough to reach the database. authenticateEngine refuses anything
      // under sixteen characters before it queries, so a short token answers
      // 401 and never exercises the unexpected path at all.
      headers: {
        authorization: `Bearer aft_${'0'.repeat(32)}`,
        'content-type': 'application/json',
      },
      body: JSON.stringify({ events: [] }),
    }))
    const body = (await conforms(response, 'post', '/v1/events', 500)) as {
      error: { code: string; message: string; resolution: string }
      requestId: string
    }
    assert.equal(body.error.code, 'AF-CP-003')
    // The header and the body agree, so a caller reading either finds the same
    // string in the log.
    assert.equal(response.headers.get('x-request-id'), body.requestId)
    assert.match(body.requestId, /^[0-9a-f-]{36}$/)
    // Nothing from the failed query reaches the caller. The message this pool
    // throws is the Drizzle shape, statement and parameters included.
    const raw = JSON.stringify(body)
    assert.ok(!raw.includes('engine_tokens'), 'the failed statement reached the caller')
    assert.ok(!raw.includes('secret'), 'a query parameter reached the caller')

    console.error = realError
    // inspect, not JSON.stringify. An Error stringifies to {}, so a handler
    // that logged the whole error object would have passed a stringified
    // assertion while writing the statement and its parameters to the log.
    // Found by mutating the handler to do exactly that and watching this test
    // stay green.
    const log = logged.map((args) => args.map((arg) => inspect(arg, { depth: 4 })).join(' ')).join('\n')
    assert.ok(log.includes(body.requestId), 'the log line does not carry the id the caller was given')
    assert.ok(!log.includes('Failed query'), 'the Drizzle message, statement included, was logged')
    assert.ok(!log.includes('engine_tokens'), 'the failed statement was logged')
    assert.ok(!log.includes('secret'), 'a query parameter was logged')
  })

  it('carries a correlation id on an ordinary answer too, so a trace is one id', async () => {
    const response = await call('/health')
    assert.match(response.headers.get('x-request-id') ?? '', /^[0-9a-f-]{36}$/)
  })

  it('honours a caller-supplied id, and refuses one it would not want in a log', async () => {
    const good = await call('/health', { headers: { 'x-request-id': 'trace-abc.123_XYZ' } })
    assert.equal(good.headers.get('x-request-id'), 'trace-abc.123_XYZ')

    // A newline is not in this list because undici refuses to build the
    // request at all, so the header can never reach the server: that one is
    // handled below the code under test and asserting it here would be
    // asserting undici.
    for (const forged of ['x'.repeat(65), 'a b', '', 'a;b', '../../etc']) {
      const answer = await call('/health', { headers: { 'x-request-id': forged } })
      const echoed = answer.headers.get('x-request-id') ?? ''
      assert.notEqual(echoed, forged, `${JSON.stringify(forged)} was echoed straight back`)
      assert.match(echoed, /^[0-9a-f-]{36}$/)
    }
  })
})

describe('the wire, against a real database', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let org: Org
  let token: string

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'wire')
    token = `aft_${randomUUID().replace(/-/g, '')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
      VALUES (${org.orgId}, 'ci', ${createHash('sha256').update(token).digest()}, 'aft_test')`
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  const send = (body: string, bearer = token) =>
    h.fetch('/v1/events', {
      method: 'POST',
      headers: { authorization: `Bearer ${bearer}`, 'content-type': 'application/json' },
      body,
    })

  const event = (over: Record<string, unknown> = {}) => ({
    id: randomUUID(),
    type: 'environment.ready',
    envId: org.envId,
    sequence: 1,
    occurredAt: new Date(1_760_000_000_000).toISOString(),
    payload: { repository: org.repository, branch: 'main' },
    ...over,
  })

  it('answers readiness 200 with a readiness body', async () => {
    await conforms(await h.fetch('/readyz'), 'get', '/readyz', 200)
  })

  it('answers ingestion 202 with the result envelope', async () => {
    await conforms(await send(JSON.stringify({ events: [event()] })), 'post', '/v1/events', 202)
  })

  it('answers ingestion 207 with the result envelope, outcomes included', async () => {
    const body = (await conforms(
      await send(JSON.stringify({ events: [event({ occurredAt: 'not a time' })] })),
      'post',
      '/v1/events',
      207,
    )) as { rejected: number }
    assert.equal(body.rejected, 1)
  })

  it('answers a body that is not JSON with the refusal shape', async () => {
    await conforms(await send('{'), 'post', '/v1/events', 400)
  })

  it('answers a body with no events array with the refusal shape', async () => {
    await conforms(await send(JSON.stringify({ nope: [] })), 'post', '/v1/events', 400)
  })

  it('answers an invalid token with the refusal shape', async () => {
    await conforms(await send(JSON.stringify({ events: [] }), 'aft_nope'), 'post', '/v1/events', 401)
  })

  it('answers an oversized batch with the refusal shape', async () => {
    const events = Array.from({ length: MAX_BATCH + 1 }, () => event())
    await conforms(await send(JSON.stringify({ events })), 'post', '/v1/events', 413)
  })

  it('answers a suspended organization with the refusal shape and a wait', async () => {
    await h.admin`
      UPDATE organizations SET suspended_at = now(), suspended_reason = 'unpaid'
      WHERE id = ${org.orgId}`
    try {
      const body = (await conforms(
        await send(JSON.stringify({ events: [event()] })),
        'post',
        '/v1/events',
        403,
      )) as { retryAfterSeconds: number }
      assert.equal(typeof body.retryAfterSeconds, 'number')
    } finally {
      await h.admin`
        UPDATE organizations SET suspended_at = NULL, suspended_reason = NULL
        WHERE id = ${org.orgId}`
    }
  })

  it('stores an event type this version has never heard of, as the document says', async () => {
    // The document publishes the known types as examples rather than as an
    // enum, and this is why: the server accepts a newer engine's type, stores
    // it, and projects nothing. A closed enum would make every generated client
    // refuse at the boundary exactly what the server was built to take.
    const batch = document.paths['/v1/events']!.post!.requestBody!.content['application/json']!.schema
    const events = (batch.properties as Record<string, Schema>).events!
    const item = events.items as Schema
    const typeSchema = (item.properties as Record<string, Schema>).type!
    assert.equal(typeSchema.enum, undefined, 'the event type is a closed enum and the server is not')
    assert.ok(Array.isArray(typeSchema.examples) && typeSchema.examples.length > 0)

    const body = (await conforms(
      await send(JSON.stringify({ events: [event({ type: 'environment.hibernated' })] })),
      'post',
      '/v1/events',
      202,
    )) as { accepted: number }
    assert.equal(body.accepted, 1)
  })

  it('answers a tRPC route with no session using the tRPC envelope, not the refusal one', async () => {
    const response = await h.fetch('/trpc/runs.recent')
    await conforms(response, 'get', '/trpc/runs.recent', 401)
  })
})
