import { test } from 'node:test'
import assert from 'node:assert/strict'
import { decodeReport } from '../src/github/lifecycle.ts'
import { stateFromReport } from '../src/github/states.ts'

const observed = { name: 'goal', outcome: { verdict: 'pass' }, visited: ['/runs'], evidence: { trace: 'trace.zip' } }
const complete = { Declared: ['goal'], Results: [observed] }
const cases: Record<string, unknown> = {
  complete,
  legacy: undefined,
  unavailable: { ...complete, Unavailable: 'browser refused' },
  absent: { Declared: ['goal'], Results: [] },
  'null-results': { Declared: ['goal'], Results: null },
  blocked: { ...complete, Results: [{ ...observed, outcome: { verdict: 'blocked' } }] },
  'unknown-verdict': { ...complete, Results: [{ ...observed, outcome: { verdict: 'future' } }] },
  'malformed-element': { Declared: ['goal', 'other'], Results: [observed, null] },
  'no-page': { ...complete, Results: [{ ...observed, visited: [] }] },
  'no-trace': { ...complete, Results: [{ ...observed, evidence: {} }] },
  duplicate: { Declared: ['goal', 'other'], Results: [observed, observed] },
  unknown: { ...complete, Results: [{ ...observed, name: 'other' }] },
  malformed: 'unreadable',
}
for (const [name, exploration] of Object.entries(cases)) {
  test(`hosted report exploration ${name}`, () => {
    const counts = decodeReport({ Workflows: [{ Verdict: 'pass' }], Exploration: exploration }).counts
    assert.equal(stateFromReport(counts), name === 'complete' || name === 'legacy' ? 'passed' : 'blocked')
  })
}

test('exploration observations do not hide a real workflow failure', () => {
  assert.equal(stateFromReport(decodeReport({ Workflows: [{ Verdict: 'fail' }], Exploration: { ...complete, Unavailable: 'browser refused' } }).counts), 'failed')
})

// The dogfood workflow sends this report to the control plane's callback, and
// the exploration evidence is megabytes of captured responses and DOM: the
// untrimmed body is refused with a 413, so the workflow drops every evidence
// field except the trace before it posts. That is only safe while the control
// plane reads nothing but the trace off an evidence object, and this proves it:
// the same report decodes to the same counts whether or not the heavy evidence
// is present. If decodeReport starts reading another evidence field, this goes
// red, which is the signal that the workflow's trim would then drop a field the
// verdict depends on. The trim itself lives in .github/workflows/dogfood.yml.
test('trimming exploration evidence to its trace does not move the verdict', () => {
  const heavy = {
    trace: 'trace.zip',
    responses: [{ url: '/runs', body: 'x'.repeat(4096) }],
    dom: '<html>'.repeat(1024),
    screenshot: 'data:image/png;base64,AAAA',
    console: ['log line'],
  }
  const results = [
    { name: 'a', outcome: { verdict: 'pass' }, visited: ['/runs'], evidence: heavy },
    { name: 'b', outcome: { verdict: 'pass' }, visited: ['/envs'], evidence: heavy },
  ]
  const full = {
    Workflows: [{ Verdict: 'pass' }, { Verdict: 'fail' }, { Verdict: 'flaky' }],
    Invariants: [{ Held: true }, { Held: false }],
    Findings: [{ Level: 'fail' }, { Level: 'warn' }],
    Load: { Sent: 1200 },
    Environment: 'env-1',
    URL: 'http://127.0.0.1:46000',
    Duration: '3m',
    Exploration: { Declared: ['a', 'b'], Results: results },
  }
  // The exact reshape .github/workflows/dogfood.yml applies: each result keeps
  // its trace and loses the rest of its evidence.
  const trimmed = {
    ...full,
    Exploration: {
      ...full.Exploration,
      Results: results.map((r) => ({ ...r, evidence: { trace: r.evidence.trace } })),
    },
  }
  // The trim actually removed the bulk, so a green here is not a green over two
  // identical objects.
  assert.ok(JSON.stringify(full).length - JSON.stringify(trimmed).length > 10_000)
  assert.deepEqual(decodeReport(trimmed).counts, decodeReport(full).counts)
})
