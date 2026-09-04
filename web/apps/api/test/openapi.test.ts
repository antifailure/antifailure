// The document agents and generated clients read, not just the route that serves it.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { openApiDocument, isPublishedProcedure, listProcedures } from '../src/openapi.ts'
import { appRouter } from '../src/routers/index.ts'

type Operation = {
  operationId?: string
  description?: string
  parameters?: Array<{ name?: string; required?: boolean } & Record<string, unknown>>
  requestBody?: {
    required?: boolean
    content?: Record<string, { schema?: Record<string, unknown> }>
  }
  responses?: Record<string, { description?: string; content?: unknown }>
}

const METHODS = new Set(['get', 'post', 'put', 'patch', 'delete', 'head', 'options'])

function operations() {
  const document = openApiDocument() as {
    paths: Record<string, Record<string, Operation>>
  }
  return Object.entries(document.paths).flatMap(([path, item]) =>
    Object.entries(item)
      .filter(([method]) => METHODS.has(method))
      .map(([method, operation]) => ({ path, method, operation })),
  )
}

describe('the OpenAPI document', () => {
  it('is rooted at the live product API', () => {
    const document = openApiDocument() as {
      openapi: string
      servers: Array<{ url: string }>
    }
    assert.equal(document.openapi, '3.1.0')
    assert.deepEqual(document.servers.map((server) => server.url), [
      'https://app.antifailure.dev',
    ])
  })

  it('gives every operation a unique function-calling name and a description', () => {
    const seen = new Set<string>()
    for (const { path, method, operation } of operations()) {
      assert.match(operation.operationId ?? '', /^[a-zA-Z][a-zA-Z0-9_]*$/, `${method} ${path}`)
      assert.ok(!seen.has(operation.operationId!), `duplicate operationId ${operation.operationId}`)
      seen.add(operation.operationId!)
      assert.ok((operation.description ?? '').length >= 20, `${method} ${path} has no useful description`)
      assert.ok(operation.responses && Object.keys(operation.responses).length > 0, `${method} ${path}`)
      for (const [status, response] of Object.entries(operation.responses ?? {})) {
        assert.ok(response.description, `${method} ${path} response ${status} has no description`)
      }
    }
  })

  it('derives mutation and query inputs from the validators the router executes', () => {
    const byId = new Map(operations().map(({ operation }) => [operation.operationId, operation]))

    const checkout = byId.get('trpc_subscriptions_checkout')!
    const checkoutSchema = checkout.requestBody!.content!['application/json']!.schema!
    assert.deepEqual(
      (checkoutSchema.properties as Record<string, { enum?: string[] }>).plan!.enum,
      ['team', 'enterprise'],
    )
    assert.ok((checkoutSchema.required as string[]).includes('successUrl'))
    // `seats` is GONE, not merely optional.
    //
    // This asserted only that it was absent from `required`, which it was:
    // it had a default. That is what let a per unit seat count sit in the
    // published contract for a product sold at a flat fee per organization,
    // where a number a caller passed multiplied the price and entitled
    // nothing. A property nobody must send should not be a property.
    assert.equal(
      'seats' in (checkoutSchema.properties as Record<string, unknown>), false,
      'the published checkout contract still offers a seat count. Nothing is sold per unit ' +
        'here: the plan decides how many members an organization may hold.',
    )

    const repositories = byId.get('trpc_repositories_list')!
    const parameter = repositories.parameters!.find((item) => item.name === 'input') as {
      content: { 'application/json': { schema: Record<string, unknown> } }
    }
    const includeArchived = (
      parameter.content['application/json'].schema.properties as Record<string, { type: string }>
    ).includeArchived
    assert.ok(includeArchived)
    assert.equal(includeArchived.type, 'boolean')
  })

  // The outer question, which is separate from the shape of the input and was
  // answered by a constant before this: may the caller leave the input out.
  //
  // Compared against the validators the router runs rather than against a list
  // written here, so a route that gains or loses a default changes both sides
  // at once. A list would go stale silently and keep passing.
  it('says whether the input may be omitted, for every route, from the validator', () => {
    const procedures = appRouter._def.procedures as unknown as Record<
      string,
      { _def: { inputs?: unknown[]; type?: string } }
    >
    const byId = new Map(operations().map(({ operation }) => [operation.operationId, operation]))

    let checkedRequired = 0
    let checkedOptional = 0
    let checkedAbsent = 0

    for (const [path, procedure] of Object.entries(procedures)) {
      const kind = procedure._def.type
      if (kind !== 'query' && kind !== 'mutation') continue
      // Operator routes are deliberately absent from the document. The two
      // tests below pin that, in both directions.
      if (!isPublishedProcedure(path)) continue
      const operation = byId.get(`trpc_${path.replace(/[^a-zA-Z0-9]+/g, '_')}`)
      assert.ok(operation, `${path} is not in the document`)
      const inputs = procedure._def.inputs ?? []
      const parameter = (operation.parameters ?? []).find((item) => item.name === 'input')

      if (inputs.length === 0) {
        checkedAbsent++
        assert.equal(parameter, undefined, `${path} takes no input and publishes an input parameter`)
        if (kind === 'mutation') {
          const schema = operation.requestBody!.content!['application/json']!.schema as Record<string, unknown>
          // Not additionalProperties: false. The route reads no body, so a
          // document that permits only {} refuses bodies the server accepts.
          assert.notEqual(
            schema.additionalProperties,
            false,
            `${path} reads no input and its body schema forbids properties the route ignores`,
          )
          assert.equal(operation.requestBody!.required, true, `${path} still needs a JSON content type`)
        }
        continue
      }

      const rejectsUndefined = inputs.some((input) => {
        const validator = input as { safeParse: (value: unknown) => { success: boolean } }
        return !validator.safeParse(undefined).success
      })
      if (kind === 'query') {
        assert.ok(parameter, `${path} takes an input and publishes no input parameter`)
        assert.equal(
          parameter.required,
          rejectsUndefined,
          `${path}: the document says required=${parameter.required} and the validator says ${rejectsUndefined}`,
        )
      } else {
        assert.equal(operation.requestBody!.required, true, `${path} needs a JSON content type`)
      }
      if (rejectsUndefined) checkedRequired++
      else checkedOptional++
    }

    // Standard 24. Three empty buckets and this test would pass over a document
    // it never looked at, which is the shape it exists to catch.
    assert.ok(checkedRequired > 0, 'no route with a mandatory input was examined')
    assert.ok(checkedOptional > 0, 'no route with an omittable input was examined')
    assert.ok(checkedAbsent > 0, 'no route without an input validator was examined')
  })

  it('publishes response schemas for public, engine, and tRPC operations', () => {
    const byId = new Map(operations().map(({ operation }) => [operation.operationId, operation]))
    for (const id of ['getLiveness', 'getReadiness', 'ingestEngineEvents', 'trpc_runs_list']) {
      const operation = byId.get(id)
      assert.ok(operation, `${id} is missing`)
      assert.ok(operation.responses?.['200']?.content ?? operation.responses?.['202']?.content, `${id} has no success schema`)
    }
  })
})

// The document is published at antifailure.dev/openapi.json and read by agents
// that generate clients from it. What it leaves out is as load bearing as what
// it carries, and both directions fail quietly: an operator route that leaks in
// is published as `security: []` because this generator reads the tenant
// permission catalogue and operator routes declare their permission in a
// different one, and a tenant route that falls out stops being documented at
// all with nothing to say so.
describe('what the published document leaves out', () => {
  const documented = new Set(
    Object.keys((openApiDocument() as { paths: Record<string, unknown> }).paths),
  )

  it('publishes no operator route, because this generator cannot describe one honestly', () => {
    const leaked = [...documented].filter((path) => path.startsWith('/trpc/admin.'))
    assert.deepEqual(
      leaked,
      [],
      'these operator routes reached the public document, where they would be published as ' +
        'requiring no permission and no session: ' + leaked.join(', '),
    )
  })

  it('publishes every tenant route, so the exclusion cannot widen by accident', () => {
    const tenant = listProcedures().filter(({ path }) => isPublishedProcedure(path))
    // A predicate that excluded everything would satisfy the test above.
    assert.ok(tenant.length > 50, `only ${tenant.length} tenant procedures, which is too few to be right`)
    const missing = tenant
      .map(({ path }) => `/trpc/${path}`)
      .filter((path) => !documented.has(path))
    assert.deepEqual(missing, [], `these tenant routes are not in the document: ${missing.join(', ')}`)
  })

  it('excludes the operator routes the router actually mounts, not an empty set', () => {
    const operator = listProcedures().filter(({ path }) => !isPublishedProcedure(path))
    assert.ok(
      operator.length > 0,
      'no procedure was excluded, so this exclusion is no longer testing anything',
    )
  })
})
