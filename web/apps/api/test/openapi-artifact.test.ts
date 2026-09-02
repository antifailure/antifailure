// The check that stands between the router and the file the apex publishes.
//
// The apex used to proxy the production control plane and validate what came
// back by looking at three root keys: openapi, info, paths. A document whose
// nested path item was malformed satisfied all three, was served, and was
// cached for five minutes. So the interesting question about this validator is
// not whether it passes the real document, it is whether it can see a defect
// that is not at the root.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { problemsWith, render } from '../scripts/openapi.ts'
import { openApiDocument } from '../src/openapi.ts'

/** The real document, deep copied, so a test can damage one part of it. */
function copy(): Record<string, unknown> {
  return JSON.parse(JSON.stringify(openApiDocument())) as Record<string, unknown>
}

function reports(damage: (document: Record<string, unknown>) => void, expected: RegExp): void {
  const document = copy()
  damage(document)
  const problems = problemsWith(document)
  assert.ok(
    problems.some((problem) => expected.test(problem)),
    `nothing matching ${expected} was reported; problems were ${JSON.stringify(problems, null, 2)}`,
  )
}

describe('the publishable-document check', () => {
  it('passes the document the router produces', () => {
    assert.deepEqual(problemsWith(copy()), [])
  })

  it('sees a reference that resolves to nothing, however deep it is', () => {
    reports((document) => {
      const paths = document.paths as Record<string, Record<string, Record<string, unknown>>>
      const responses = paths['/v1/events']!.post!.responses as Record<
        string,
        { content: Record<string, { schema: Record<string, unknown> }> }
      >
      responses['401']!.content['application/json']!.schema = {
        $ref: '#/components/schemas/NoSuchThing',
      }
    }, /names a schema that does not exist: NoSuchThing/)
  })

  it('sees a malformed path item, which is the defect the root-shape check could not', () => {
    reports((document) => {
      ;(document.paths as Record<string, unknown>)['/v1/events'] = 'not an object'
    }, /is not a path item object/)
  })

  it('sees a path item with no operation in it', () => {
    reports((document) => {
      ;(document.paths as Record<string, unknown>)['/v1/orphan'] = { summary: 'nothing here' }
    }, /declares no operation/)
  })

  it('sees a response with no description and a response with no schema', () => {
    reports((document) => {
      const paths = document.paths as Record<string, Record<string, Record<string, unknown>>>
      const responses = paths['/health']!.get!.responses as Record<string, Record<string, unknown>>
      delete responses['200']!.description
    }, /response 200 has no description/)

    reports((document) => {
      const paths = document.paths as Record<string, Record<string, Record<string, unknown>>>
      const responses = paths['/health']!.get!.responses as Record<
        string,
        { content: Record<string, Record<string, unknown>> }
      >
      delete responses['200']!.content['application/json']!.schema
    }, /carries no schema/)
  })

  it('sees a duplicated operation id, which makes one generated function shadow another', () => {
    reports((document) => {
      const paths = document.paths as Record<string, Record<string, Record<string, unknown>>>
      paths['/readyz']!.get!.operationId = 'getLiveness'
    }, /repeats the operationId getLiveness/)
  })

  it('sees an operation id no function can be named after', () => {
    reports((document) => {
      const paths = document.paths as Record<string, Record<string, Record<string, unknown>>>
      paths['/readyz']!.get!.operationId = 'get readiness!'
    }, /no operationId a generated function can be named after/)
  })

  it('sees a security requirement naming a scheme that is not declared', () => {
    reports((document) => {
      const paths = document.paths as Record<string, Record<string, Record<string, unknown>>>
      paths['/health']!.get!.security = [{ inventedScheme: [] }]
    }, /security scheme inventedScheme, which is not declared/)
  })

  it('sees a component nothing points at, because it documents nothing', () => {
    reports((document) => {
      const components = document.components as { schemas: Record<string, unknown> }
      components.schemas.Abandoned = { type: 'object' }
    }, /Abandoned is referenced by nothing/)
  })

  it('sees a missing server, which leaves a generated client with no host', () => {
    reports((document) => {
      document.servers = []
    }, /servers is empty/)
  })

  it('sees a version that is not 3.1', () => {
    reports((document) => {
      document.openapi = '3.0.3'
    }, /which is not a 3\.1 version/)
  })

  it('renders bytes that round trip, so the committed file is the document', () => {
    const text = render()
    assert.ok(text.endsWith('\n'))
    assert.deepEqual(JSON.parse(text), JSON.parse(JSON.stringify(openApiDocument())))
  })
})
