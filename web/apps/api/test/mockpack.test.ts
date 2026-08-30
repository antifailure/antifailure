// The parity test: this runtime reproduces the engine's answers, or it fails.
//
// The corpus is not written here. It is a transcript emitted by
// engine/internal/mockpack/vectors_test.go, checked in, and read by both. Two
// implementations of one mock will drift, and the one that drifts is the one
// nobody runs against real traffic: the control plane's billing tests would go
// green against responses the sidecar never produces, which is exactly the
// plausible but wrong shape the mock pack exists to avoid, one level up.
//
// The steps are ordered on purpose. The pack is stateful and mints identifiers
// in sequence, so replaying them in another order produces different answers on
// both sides and proves nothing.

import { describe, it, before } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { MockPack, loadPack } from './mockpack.ts'

const here = path.dirname(fileURLToPath(import.meta.url))
const vectorPath = path.join(here, '..', '..', '..', '..', 'schemas', 'mockpack-vectors.json')

interface VectorFile {
  note: string
  host: string
  steps: VectorStep[]
}

interface VectorStep {
  why: string
  method: string
  path: string
  body?: string
  matched: boolean
  status?: number
  response?: unknown
}

const vectors: VectorFile = JSON.parse(await readFile(vectorPath, 'utf8')) as VectorFile

describe('the pack runtime reproduces the engine’s answers', () => {
  let engine: MockPack

  before(async () => {
    engine = new MockPack([await loadPack('stripe')])
  })

  it('reads a corpus that is not empty, so a missing file fails loudly', () => {
    assert.ok(vectors.steps.length >= 15, `only ${vectors.steps.length} steps in the corpus`)
    assert.ok(
      vectors.steps.some((s) => !s.matched),
      'the corpus never misses, so the refusal path is not compared',
    )
    assert.ok(
      vectors.steps.some((s) => s.status === 404),
      'the corpus never reads something absent, so the not_found shape is not compared',
    )
  })

  // One test per step rather than a loop with one assertion, so a failure names
  // the property that broke rather than the index that differed.
  for (const [index, step] of vectors.steps.entries()) {
    it(`step ${index + 1}: ${step.why}`, () => {
      const answer = engine.answer(vectors.host, step.method, step.path, step.body ?? '')

      if (!step.matched) {
        assert.equal(answer, null, `${step.method} ${step.path} was answered, and the engine misses it`)
        return
      }
      assert.ok(answer, `${step.method} ${step.path} matched nothing, and the engine answers it`)
      assert.equal(answer.status, step.status, `${step.method} ${step.path} answered a different status`)
      assert.deepEqual(
        JSON.parse(answer.body),
        step.response,
        `${step.method} ${step.path} answered a different body`,
      )
    })
  }
})

describe('the pack runtime’s own behaviour', () => {
  it('answers only for the hosts the pack names', async () => {
    const engine = new MockPack([await loadPack('stripe')])
    assert.equal(engine.handles('api.stripe.com'), true)
    assert.equal(engine.handles('API.Stripe.com.'), true)
    assert.equal(engine.handles('api.notstripe.com'), false)
    assert.equal(engine.answer('api.somewhere-else.test', 'GET', '/v1/customers', ''), null)
  })

  it('mints a fresh identifier per object and reuses it within one response', async () => {
    // Two customers are two customers. One checkout session named twice in one
    // body is one session, which is the property that keeps a session's url
    // pointing at the session.
    const engine = new MockPack([await loadPack('stripe')])
    const first = JSON.parse(engine.answer('api.stripe.com', 'POST', '/v1/customers', '')!.body)
    const second = JSON.parse(engine.answer('api.stripe.com', 'POST', '/v1/customers', '')!.body)
    assert.notEqual(first.id, second.id)

    const session = JSON.parse(
      engine.answer('api.stripe.com', 'POST', '/v1/checkout/sessions', 'mode=subscription')!.body,
    )
    assert.ok(String(session.url).endsWith(String(session.id)))
  })
})
