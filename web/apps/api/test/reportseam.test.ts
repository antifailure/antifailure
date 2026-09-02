// The wire between the engine and this decoder, tested with real bytes.
//
// THE HOLE THIS FILLS. The engine's suite asserts what it emits, against a
// document it built itself. This suite asserted what it decodes, against a
// document IT built itself. Both were green, and between them was a wire
// neither had ever put a message on. `aggregateFor` read the engine's NATIVE
// load result (`sent`, `rate`, percentiles nested under `overall`) rather than
// the RESULT DOCUMENT (`requests`, `achieved_rate`, flat), and it failed
// silently in the worst direction available: the request count falls back to
// zero rather than refusing, because the column needs one, so a run that sent
// twelve hundred requests recorded as having sent NONE with every percentile
// null, while every route, threshold and piece of evidence beside it decoded
// perfectly. Two of the four kinds carried it.
//
// So this decodes bytes the engine actually produced. Every fixture in
// test/fixtures/engine-reports is `json.Marshal` of a `workload.Result` that
// came out of the engine's own `workload.Execute`, put through the engine's own
// `hostedPayload`. Nothing in them was written by hand, which is the only
// property that matters: a fixture somebody typed is the same self-agreement
// that hid this for a week.
//
// WHY IT FAILS IN BOTH DIRECTIONS. A name the decoder reads and the engine does
// not send is the defect above. A number the engine sends into the aggregate
// that no arm of the decoder picks up is a measurement dropped on the floor,
// which is the same defect wearing the other hat and is just as quiet. The last
// test here reads the fixture's own keys rather than a list kept in this file,
// so a field the engine adds shows up here as a failure rather than as nothing.
//
// No database. Decoding is a pure function of the payload, and a seam test that
// needed Postgres would be the test that gets skipped on a busy machine, and a
// skip reads as a pass. What the numbers do once decoded is workloadruns.test.ts.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { decodeReport } from '../src/workloads/results.ts'
import type { WorkloadKind } from '../src/workloads/bodies.ts'

const here = path.dirname(fileURLToPath(import.meta.url))

function wire(name: string): Record<string, unknown> {
  const file = path.join(here, 'fixtures', 'engine-reports', `${name}.json`)
  return JSON.parse(readFileSync(file, 'utf8')) as Record<string, unknown>
}

/** The aggregate an engine sent, as the payload carries it. */
function sent(payload: Record<string, unknown>): Record<string, unknown> {
  const result = payload.result
  assert.ok(
    result !== null && typeof result === 'object' && !Array.isArray(result),
    'the aggregate is a to-one and has to arrive as an object',
  )
  return result as Record<string, unknown>
}

/** The engine's failures-by-reason map, with the entries the decoder is
 *  entitled to drop taken out: a key it cannot read is skipped rather than
 *  taking the map with it, so the comparison is against what is readable. */
function numbersOnly(value: Record<string, unknown>): Record<string, number> {
  const out: Record<string, number> = {}
  for (const [k, v] of Object.entries(value)) {
    if (typeof v === 'number' && Number.isInteger(v)) out[k] = v
  }
  return out
}

const KINDS: { kind: WorkloadKind; fixture: string }[] = [
  { kind: 'observed_load', fixture: 'observed-load' },
  { kind: 'http_scenario', fixture: 'http-scenario' },
  { kind: 'browser_workflow', fixture: 'browser-workflow' },
  { kind: 'exploration', fixture: 'exploration' },
]

describe("a report an engine actually sent", () => {
  it('a load run that sent 1200 requests records 1200, not zero', () => {
    const payload = wire('observed-load')
    // The fixture is the subject, so the fixture is checked first. A test that
    // asserts zero equals zero over an empty file is the shape that certified
    // the lie it guarded, elsewhere in this repository.
    assert.equal(sent(payload).requests, 1200, 'the fixture is not the one this test is about')

    const report = decodeReport('observed_load', payload)
    assert.equal(report.aggregate.requests, 1200)
    assert.equal(report.aggregate.failures, 15)
    assert.equal(report.aggregate.achievedRate, 19.7)
    assert.equal(report.aggregate.targetRate, 20)
    assert.equal(report.aggregate.errorRate, 0.0125)
    // Flat, and not through an `overall` object. All five, because the defect
    // took out all five together and one of them passing would have hidden it.
    assert.equal(report.aggregate.p50Ms, 41.2)
    assert.equal(report.aggregate.p90Ms, 88.5)
    assert.equal(report.aggregate.p95Ms, 121.9)
    assert.equal(report.aggregate.p99Ms, 240.4)
    assert.equal(report.aggregate.maxMs, 812)
    assert.equal(report.aggregate.source, 'otlp')
    assert.equal(report.aggregate.durationMs, 60000)
    assert.deepEqual(report.aggregate.errorReasons, { '500': 9, timeout: 6 })
    // Nothing was skipped. The defect was invisible precisely because this
    // counter stayed at zero while the aggregate was empty, so a zero here is
    // only meaningful next to the numbers above.
    assert.deepEqual(report.skipped, { routes: 0, thresholds: 0, evidence: 0 })
  })

  it('a scenario run carries its sessions, its schedule and its request count', () => {
    const payload = wire('http-scenario')
    assert.equal(sent(payload).requests, 480, 'the fixture is not the one this test is about')

    const report = decodeReport('http_scenario', payload)
    assert.equal(report.aggregate.requests, 480)
    assert.equal(report.aggregate.sessions, 40)
    assert.equal(report.aggregate.iterations, 120)
    assert.equal(report.aggregate.scheduledMs, 60000)
    assert.equal(report.aggregate.failures, 2)
    assert.equal(report.aggregate.p95Ms, 138)
    assert.equal(report.aggregate.maxMs, 690)
    // A scenario run has no arrival rate to aim for, and the engine sends null
    // rather than zero. Zero would let a console draw a rate nobody targeted.
    assert.equal(report.aggregate.targetRate, null)
    assert.equal(report.aggregate.achievedRate, null)
    // The document spells refusals `refused_routes`. The engine's NATIVE
    // scenario result spells the same list `refused_as_unsafe`, and this
    // decoder used to reach for that name first.
    assert.deepEqual(report.aggregate.refusedRoutes, ['DELETE /admin/purge'])
    assert.deepEqual(report.skipped, { routes: 0, thresholds: 0, evidence: 0 })
  })

  it('a browser run carries five counts, so unverified is not read as passing', () => {
    const payload = wire('browser-workflow')
    const report = decodeReport('browser_workflow', payload)
    assert.equal(report.aggregate.workflows, 8)
    assert.equal(report.aggregate.workflowsPassed, 4)
    assert.equal(report.aggregate.workflowsFailed, 1)
    assert.equal(report.aggregate.workflowsFlaky, 1)
    assert.equal(report.aggregate.workflowsBlocked, 0)
    assert.equal(report.aggregate.workflowsUnverified, 2)
    assert.equal(report.aggregate.steps, 9)
    // Four passed and one failed sums to five of eight. The other three are the
    // reason five counts travel rather than two.
    assert.notEqual(
      (report.aggregate.workflowsPassed ?? 0) + (report.aggregate.workflowsFailed ?? 0),
      report.aggregate.workflows,
    )
    assert.equal(report.aggregate.requests, null)
    assert.deepEqual(report.skipped, { routes: 0, thresholds: 0, evidence: 0 })
  })

  it('an exploration carries goals and findings, and its evidence is not claimed as uploaded', () => {
    const payload = wire('exploration')
    const report = decodeReport('exploration', payload)
    assert.equal(report.aggregate.goals, 2)
    assert.equal(report.aggregate.goalsReached, 1)
    assert.equal(report.aggregate.findings, 2)
    assert.equal(report.evidence.length, 1)
    assert.equal(report.evidence[0]!.availability, 'runner_local')
    assert.equal(report.evidence[0]!.kind, 'screenshot')
    assert.match(report.evidence[0]!.locator, /^\.antifailure\/artifacts\//)
    assert.equal(report.evidence[0]!.sha256?.length, 64)
    assert.deepEqual(report.skipped, { routes: 0, thresholds: 0, evidence: 0 })
  })

  it('routes, thresholds and evidence survive the same wire', () => {
    const load = decodeReport('observed_load', wire('observed-load'))
    assert.equal(load.routes.length, 2)
    assert.equal(load.routes[0]!.route, 'GET /checkout')
    assert.equal(load.routes[0]!.sent, 640)
    assert.equal(load.routes[0]!.errors, 9)
    // The document's route metric is flat too. It happens to decode because
    // this decoder falls through to the flat name, which is worth an assertion
    // rather than an assumption.
    assert.equal(load.routes[0]!.p95Ms, 110)
    assert.equal(load.routes[0]!.baselineP95Ms, 96)
    assert.equal(load.routes[0]!.p95Increase, 0.1458)
    // No baseline and no change are different answers, and the second route had
    // nothing to compare with.
    assert.equal(load.routes[1]!.baselineP95Ms, null)
    assert.equal(load.routes[1]!.p95Increase, null)

    const scenario = decodeReport('http_scenario', wire('http-scenario'))
    assert.equal(scenario.thresholds.length, 1)
    // The document spells the answer `value`; the engine's native assertion
    // result spells it `verdict`. Both are read, and this is which one arrived.
    assert.equal(scenario.thresholds[0]!.value, 'pass')
    assert.equal(scenario.thresholds[0]!.measure, 'p95_below_ms')
    assert.equal(scenario.thresholds[0]!.scope, 'POST /api/orders')
    assert.equal(scenario.thresholds[0]!.scenario, 'checkout')
    assert.equal(scenario.thresholds[0]!.threshold, 200)
    assert.equal(scenario.thresholds[0]!.observed, 138)

    const browser = decodeReport('browser_workflow', wire('browser-workflow'))
    assert.equal(browser.evidence.length, 2)
    assert.deepEqual(browser.evidence.map((e) => e.kind), ['trace', 'screenshot'])
    assert.equal(browser.evidence[0]!.label, 'signup')
    assert.equal(browser.evidence[0]!.sizeBytes, 42)
  })

  it('a load run that failed a threshold says so in the thresholds and NOT in the detail', () => {
    const payload = wire('observed-load')
    const report = decodeReport('observed_load', payload)

    // State says the work happened and verdict says what it found. This run
    // succeeded and failed a threshold, which is the pair that must not be
    // collapsed: an exit code over work that never happened would otherwise
    // read as a pass.
    assert.equal(payload.state, 'succeeded')
    assert.equal(report.verdict, 'fail')

    // AND THE DETAIL IS NULL, which is not an accident of this fixture. The
    // engine's mixDetail returns a sentence only when nothing was sent, so
    // EVERY failing load run that actually sent traffic arrives with an empty
    // detail. Anything rendering "why did this fail" has to read the threshold
    // rows; a console that shows `detail` beside a red verdict shows a blank.
    assert.equal(report.detail, null)
    assert.equal(report.failureCode, null)

    const failed = report.thresholds.filter((t) => t.value === 'fail')
    assert.equal(failed.length, 1)
    assert.equal(failed[0]!.name, 'error_rate')
    assert.equal(failed[0]!.observed, 0.0125)
    assert.equal(failed[0]!.threshold, 0.01)

    // A route with no baseline is unverified rather than absent. A console
    // showing only breaches would draw a clean p95 check over routes nothing
    // was ever compared against.
    const unverified = report.thresholds.filter((t) => t.value === 'unverified')
    assert.equal(unverified.length, 1)
    assert.equal(unverified[0]!.scope, 'POST /api/orders')
    assert.match(String(unverified[0]!.detail), /no baseline/)
  })

  // -------------------------------------------------------------------------
  // The other direction: a number the engine sends and nothing here reads.
  // -------------------------------------------------------------------------

  it('every measurement the engine sends is one this decoder picks up', () => {
    // Read off the fixtures rather than listed here, so a field the engine adds
    // arrives as a failure rather than as nothing. The map is from the result
    // document's JSON name to the property decodeReport lands it in.
    const lands: Record<string, keyof ReturnType<typeof decodeReport>['aggregate']> = {
      requests: 'requests',
      failures: 'failures',
      error_rate: 'errorRate',
      target_rate: 'targetRate',
      achieved_rate: 'achievedRate',
      p50_ms: 'p50Ms',
      p90_ms: 'p90Ms',
      p95_ms: 'p95Ms',
      p99_ms: 'p99Ms',
      max_ms: 'maxMs',
      sessions: 'sessions',
      iterations: 'iterations',
      scheduled_ms: 'scheduledMs',
      workflows: 'workflows',
      workflows_passed: 'workflowsPassed',
      workflows_failed: 'workflowsFailed',
      workflows_flaky: 'workflowsFlaky',
      workflows_blocked: 'workflowsBlocked',
      workflows_unverified: 'workflowsUnverified',
      steps: 'steps',
      goals: 'goals',
      goals_reached: 'goalsReached',
      findings: 'findings',
      duration_ms: 'durationMs',
      source: 'source',
      refused_routes: 'refusedRoutes',
      errors: 'errorReasons',
    }

    const dropped: string[] = []
    const unnamed: string[] = []
    for (const { kind, fixture } of KINDS) {
      const payload = wire(fixture)
      const aggregate = decodeReport(kind, payload).aggregate
      for (const [name, value] of Object.entries(sent(payload))) {
        const property = lands[name]
        // The NAME is checked whatever the value, including null. A field the
        // engine adds is null for the three kinds it does not apply to, and
        // checking only non-null values would let a new measurement arrive
        // unnoticed on any regeneration of these fixtures where it happened to
        // be null. The name is the contract; the value is the check on it.
        if (property === undefined) {
          unnamed.push(`${kind}.${name}`)
          continue
        }
        // A null IS the engine saying this measurement does not apply to this
        // kind, so there is nothing for the decoder to have carried.
        if (value === null || value === undefined) continue
        // The value has to ARRIVE UNCHANGED, not merely arrive. `landed !==
        // null` is not enough and the reason is the whole defect: a request
        // count the decoder could not find falls back to ZERO rather than to
        // null, because the column needs a number. A check for absence would
        // have watched a run that sent twelve hundred requests record as
        // having sent none and called it present.
        const landed = aggregate[property]
        const same =
          Array.isArray(value) && Array.isArray(landed)
            ? JSON.stringify(value) === JSON.stringify(landed)
            : typeof value === 'object' && value !== null
              ? JSON.stringify(numbersOnly(value as Record<string, unknown>)) ===
                JSON.stringify(landed)
              : landed === value
        if (!same) dropped.push(`${kind}.${name} was sent as ${JSON.stringify(value)} and decoded to ${JSON.stringify(landed)}`)
      }
    }

    assert.deepEqual(
      unnamed,
      [],
      `the engine sends ${unnamed.join(', ')} inside the result and this test has no column for ` +
        `them. Either decodeReport should be reading them, or this map should say why not. A ` +
        `measurement nothing reads is written nowhere and nothing says so.`,
    )
    assert.deepEqual(
      dropped,
      [],
      `these measurements did not survive the decoder:\n  ${dropped.join('\n  ')}\n` +
        `A number that arrives as something else is the same defect as reading a name the engine ` +
        `does not send, wearing the other hat, and a request count that decodes to ZERO is the ` +
        `exact shape this file was written for: it is present, it is wrong, and nothing says so.`,
    )
  })

  it('the fixtures are the real wire and not a document this suite invented', () => {
    for (const { fixture } of KINDS) {
      const payload = wire(fixture)
      // The schema an engine stamps on the document it sends.
      assert.equal(payload.schema, 'antifailure.workload.result/v1', fixture)
      // The two keys the transport adds, which is what makes these payloads
      // rather than result files.
      assert.equal(typeof payload.workload_run_id, 'string', fixture)
      assert.equal(typeof payload.outcome, 'string', fixture)
      // And the one it removes. `native` is the engine's own result for the
      // kind: the control plane declined to store it, and a fixture carrying it
      // would be a result file somebody copied rather than a message.
      assert.equal('native' in payload, false, fixture)
      // Every fixture carries the reproduce block, because a hosted result
      // whose command nobody can run proves nothing.
      const reproduce = payload.reproduce as Record<string, unknown>
      assert.match(String(reproduce.command), /^af /, fixture)
    }
  })
})
