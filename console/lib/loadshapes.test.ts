// The seam between the rows the control plane produces and the console that
// reads them.
//
// WHY THIS FILE EXISTS. Both halves of this seam passed their own suites and
// it proved nothing: the Load area was built complete, verified, and against a
// contract that did not exist. Two green suites, neither crossing between
// them. This is the crossing, and it is a test of the CONSOLE that reads the
// control plane's own schema files rather than a copy of what they say.
//
// `loadshapes.ts` next door holds no import for that reason. The calls in
// `load.ts` reach the network and cannot run outside a browser; the decoders
// are the half worth testing, because every defect this module has had was a
// shape defect rather than a rendering one.
//
// THE FIVE VOCABULARY TESTS ARE THE POINT. Each reads an enum out of the
// migration that declares it and asserts the console knows exactly those
// values. That is what would have caught the defect this suite was written
// after: `verdict_value` has five values, the console's type had four, `flaky`
// fell through the decoder as null, and a run that had found something drew as
// "No verdict". An absence displayed where there is a finding is the worst
// direction to be wrong in, because nobody investigates a blank.
//
// They fail loudly the day somebody adds a sixth verdict or a ninth state, and
// that is the whole design: the console cannot silently not know about a value
// it will be sent.

import { describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import {
  AVAILABILITY_FACTS,
  COMMAND_FACTS,
  KIND_FACTS,
  KINDS,
  STATE_FACTS,
  VERDICT_FACTS,
  bodyToInput,
  duration,
  increase,
  isRunning,
  isTerminal,
  ms,
  nothingWasChecked,
  percent,
  readBody,
  readEvidence,
  readResult,
  readRouteMetric,
  readRun,
  readRunDetail,
  readThreshold,
  readExplorations,
  readWorkloadRow,
  runStateOf,
  verdictContradiction,
  verdictOf,
  whatDecidedIt,
  type RouteMetric,
  type RunResult,
  type ThresholdVerdict,
} from './loadshapes.ts'

const root = fileURLToPath(new URL('../../', import.meta.url))

/**
 * The three files the vocabulary and column checks read, or the reason they
 * are not here.
 *
 * The Studio schema and the store it is read through arrive with
 * `w-studio-persistence`, and this console branch is landing beside it rather
 * than after it. On a tree that has both, every check below runs. On a tree
 * that has only the console, the ones that compare against the schema say
 * WHICH FILE is missing and skip, and the decoder checks underneath them run
 * either way.
 *
 * A skip reads as a pass in a summary, which is why the reason names the file
 * rather than saying "unavailable", and why it can never be reached once the
 * two branches are on one main: the file is committed, so its absence is a
 * fact about an unmerged branch and nothing else.
 */
function readIfPresent(relative: string): string | null {
  try {
    return readFileSync(`${root}${relative}`, 'utf8')
  } catch {
    return null
  }
}

const initSql = readIfPresent('web/packages/db/migrations/0001_init.sql')
const studioSql = readIfPresent('web/packages/db/migrations/0026_load_definitions_and_runs.sql')
const storeTs = readIfPresent('web/apps/api/src/workloads/store.ts')

const missing = [
  studioSql === null ? '0026_load_definitions_and_runs.sql' : null,
  storeTs === null ? 'src/workloads/store.ts' : null,
  initSql === null ? '0001_init.sql' : null,
].filter((f): f is string => f !== null)

const noSchema =
  missing.length === 0
    ? false
    : `${missing.join(' and ')} is not on this branch, so the console cannot be compared against ` +
      `the schema here. This runs the moment w-studio-persistence and w-studio-console are on one tree.`

/**
 * The values a Postgres enum declares, read out of the migration.
 *
 * Anchored on the type name and the closing parenthesis rather than on a line,
 * because a `CREATE TYPE` here spans two lines and a line oriented pattern
 * would find nothing and report an empty set. An empty set is exactly what a
 * broken instrument prints, so this refuses to return one.
 */
function enumValues(sql: string | null, name: string): string[] {
  assert.ok(sql, 'enumValues was called with no migration, which the skip above should have stopped')
  const match = new RegExp(`CREATE TYPE ${name} AS ENUM \\(([^)]*)\\)`, 's').exec(sql)
  assert.ok(match, `no CREATE TYPE ${name} in the migration, so this test is reading the wrong file`)
  const values = [...match[1]!.matchAll(/'([a-z_]+)'/g)].map((m) => m[1]!)
  assert.ok(values.length > 0, `CREATE TYPE ${name} parsed to no values, which is an instrument fault`)
  return values.sort()
}

/**
 * The output names of a SQL column list, as a client would see them.
 *
 * `w.kind::text AS kind` is `kind`, `wr.git_ref` is `git_ref`. Written out
 * rather than assumed, because a decoder reading `kind` off a row whose column
 * is called `workload_kind` is precisely the mistake this file exists to
 * catch, and only the SELECT itself knows which name arrives.
 */
function columnNames(source: string | null, constant: string): string[] {
  assert.ok(source, 'columnNames was called with no source, which the skip above should have stopped')
  const start = source.indexOf(`export const ${constant} = sql\``)
  assert.ok(start >= 0, `${constant} is not in store.ts, so this test is reading the wrong file`)
  const from = start + `export const ${constant} = sql\``.length
  const end = source.indexOf('`', from)
  assert.ok(end > from, `${constant} has no closing backtick`)
  const body = source.slice(from, end)
  const names = body
    .split(',')
    .map((piece) => piece.trim())
    .filter((piece) => piece.length > 0 && !piece.startsWith('--'))
    .map((piece) => {
      const aliased = /\bAS\s+([a-z_]+)$/i.exec(piece)
      if (aliased) return aliased[1]!
      const bare = /([a-z_]+)$/.exec(piece.replace(/::[a-z]+$/, ''))
      assert.ok(bare, `cannot read a column name out of ${JSON.stringify(piece)}`)
      return bare[1]!
    })
  assert.ok(names.length > 0, `${constant} parsed to no columns, which is an instrument fault`)
  return names.sort()
}

// ---------------------------------------------------------------------------
// The vocabularies
// ---------------------------------------------------------------------------

describe('the console knows exactly the values the database can send', { skip: noSchema }, () => {
  /**
   * Values the console knows before the migration declares them.
   *
   * `warn` is a run verdict the engine produces and `verdict_value` is the
   * WORKFLOW vocabulary, so a run judged `warn` currently decodes to null and
   * renders as "No verdict". studio-persist-fixes is adding it to the enum.
   * The console learns it now so nothing shows that finding as an absence in
   * the window between, and this list is how the exception stays visible
   * rather than becoming a permanent hole in an exact assertion.
   *
   * It is designed to expire loudly. The moment the migration carries a value
   * named here, the assertion below FAILS and names the one line to delete.
   * That red is deliberate: an exception that quietly outlives its reason is
   * the gate weakening itself, which is exactly what this file exists to stop.
   */
  const PENDING = ['warn']

  test('the verdicts, and flaky and warn are among them', () => {
    const declared = enumValues(initSql, 'verdict_value')
    const landed = PENDING.filter((v) => declared.includes(v))
    assert.deepEqual(
      landed,
      [],
      `verdict_value now declares ${landed.join(', ')}. Delete it from PENDING in this file ` +
        `and let the exact assertion below stand on its own.`,
    )
    assert.deepEqual(Object.keys(VERDICT_FACTS).sort(), [...declared, ...PENDING].sort())

    // Named on their own as well as counted, because these are the values that
    // were missing. A count with the wrong members would pass the line above.
    assert.equal(verdictOf('flaky'), 'flaky')
    assert.equal(VERDICT_FACTS.flaky.conclusive, true)
    assert.equal(verdictOf('warn'), 'warn')
    assert.equal(VERDICT_FACTS.warn.conclusive, true)
    for (const v of ['flaky', 'warn'] as const) {
      assert.notEqual(VERDICT_FACTS[v].label, VERDICT_FACTS.pass.label)
      assert.notEqual(VERDICT_FACTS[v].label, VERDICT_FACTS.fail.label)
    }
  })

  test('warn says it is a finding, and blocked says the opposite', () => {
    // Both draw amber and they mean opposite things: warn is we looked and
    // found something, blocked is we did not look. Colour cannot carry that,
    // so the sentence has to, and these two must not be interchangeable.
    assert.match(VERDICT_FACTS.warn.meaning, /found/)
    assert.match(VERDICT_FACTS.blocked.meaning, /never reached/)
    assert.notEqual(VERDICT_FACTS.warn.meaning, VERDICT_FACTS.blocked.meaning)
  })

  test('a verdict nothing declares is refused rather than guessed at', () => {
    assert.equal(verdictOf('regressed'), null)
    assert.equal(verdictOf(''), null)
    assert.equal(verdictOf(null), null)
    assert.equal(verdictOf(3), null)
  })

  test('the eight run states', () => {
    assert.deepEqual(Object.keys(STATE_FACTS).sort(), enumValues(studioSql, 'workload_run_state'))
    // The three that are not over, and the five that are. Asserted as a
    // partition rather than as two lists, so a ninth state cannot be silently
    // absent from both.
    const all = Object.keys(STATE_FACTS) as (keyof typeof STATE_FACTS)[]
    for (const state of all) {
      assert.equal(isRunning(state), !isTerminal(state), `${state} is both or neither`)
    }
    assert.equal(all.filter(isRunning).length, 3)
  })

  test('abandoned says the reporting is missing, not that the work failed', () => {
    // The distinction the whole state enum turns on. If this sentence ever
    // starts calling it a failure, a reader is told the change is broken when
    // what is broken is the plumbing.
    assert.match(STATE_FACTS.abandoned.meaning, /not a failure/)
    assert.match(STATE_FACTS.abandoned.meaning, /report/)
  })

  test('an old state name is not quietly accepted', () => {
    // These four were in the console's previous enum and are in no database.
    for (const gone of ['queued', 'starting', 'finished', 'errored']) {
      assert.equal(runStateOf(gone), null, `${gone} is not a state the control plane has`)
    }
  })

  test('the four kinds', () => {
    assert.deepEqual([...KINDS].sort(), enumValues(studioSql, 'workload_kind'))
    assert.deepEqual(Object.keys(KIND_FACTS).sort(), enumValues(studioSql, 'workload_kind'))
  })

  test('the three evidence availabilities', () => {
    assert.deepEqual(
      Object.keys(AVAILABILITY_FACTS).sort(),
      enumValues(studioSql, 'workload_evidence_availability'),
    )
    // Only one of the three is somewhere bytes can be fetched from. A console
    // that made a link out of a runner path sends somebody to a 404.
    assert.equal(AVAILABILITY_FACTS.uploaded.fetchable, true)
    assert.equal(AVAILABILITY_FACTS.runner_local.fetchable, false)
    assert.equal(AVAILABILITY_FACTS.not_retained.fetchable, false)
  })

  test('the six command states', () => {
    assert.deepEqual(
      Object.keys(COMMAND_FACTS).sort(),
      enumValues(studioSql, 'runtime_command_state'),
    )
  })
})

// ---------------------------------------------------------------------------
// The rows
// ---------------------------------------------------------------------------

/**
 * A run row carrying every column `runColumns` selects.
 *
 * The keys are asserted against the SELECT below, so this cannot drift from
 * what the route actually returns without a test going red. That assertion is
 * the reason this fixture is written out by hand rather than generated: a
 * generated one would agree with the SELECT by construction and prove nothing.
 */
const RUN_ROW: Record<string, unknown> = {
  id: '2f1c0a1e-0000-4000-8000-000000000001',
  workload_id: '2f1c0a1e-0000-4000-8000-0000000000aa',
  workload_slug: 'checkout-friday',
  kind: 'http_scenario',
  version: 3,
  state: 'succeeded',
  env_id: 'pr-412',
  repository: 'acme/store',
  git_ref: 'feature/checkout',
  attempt: 2,
  retry_of: '2f1c0a1e-0000-4000-8000-000000000002',
  superseded_by: '2f1c0a1e-0000-4000-8000-000000000003',
  verdict: 'flaky',
  failure_code: 'AF-WLD-002',
  detail: 'two of the three scenarios were refused',
  reproduce_command: 'af load scenario --only checkout --seed 7',
  manifest_digest: 'sha256:0123456789abcdef',
  requested_at: '2026-09-01T10:00:00.000Z',
  accepted_at: '2026-09-01T10:00:20.000Z',
  started_at: '2026-09-01T10:00:40.000Z',
  finished_at: '2026-09-01T10:04:00.000Z',
  deadline_at: '2026-09-01T12:00:00.000Z',
  cancel_requested_at: '2026-09-01T10:03:00.000Z',
  cancelled_at: '2026-09-01T10:03:30.000Z',
  dispatched_at: '2026-09-01T10:00:05.000Z',
  // The lease columns. runColumns selects these and this fixture predated
  // them, which is exactly the drift this gate exists to catch: the console
  // branch and the lease work were written against different versions of the
  // same route and only meet here.
  lease_takeovers: 1,
  lease_lost_at: '2026-09-01T10:02:00.000Z',
  unheld_reports: 1,
  unheld_report_at: '2026-09-01T10:02:30.000Z',
}

describe('a run row', () => {
  test('the fixture names exactly the columns the route selects', { skip: noSchema }, () => {
    // The drift gate. Rename a column in runColumns and this fails here rather
    // than in a browser three weeks later, showing a dash where a number was.
    assert.deepEqual(Object.keys(RUN_ROW).sort(), columnNames(storeTs, 'runColumns'))
  })

  test('every column reaches a field', () => {
    const run = readRun(RUN_ROW)
    assert.ok(run)
    // Every field, because a decoder that reads twenty three of twenty five
    // columns looks exactly like one that reads all of them until somebody
    // needs the other two.
    for (const [key, value] of Object.entries(run)) {
      assert.notEqual(value, null, `${key} decoded to null from a row that carried it`)
    }
    assert.equal(run.workloadSlug, 'checkout-friday')
    assert.equal(run.verdict, 'flaky')
    assert.equal(run.state, 'succeeded')
    assert.equal(run.reproduceCommand, 'af load scenario --only checkout --seed 7')
  })

  test('a row with no state is dropped rather than drawn in a tone somebody picked', () => {
    assert.equal(readRun({ ...RUN_ROW, state: 'finished' }), null)
    assert.equal(readRun({ ...RUN_ROW, state: null }), null)
    assert.equal(readRun({ ...RUN_ROW, id: null }), null)
  })

  test('a run still going has no verdict, and that is not a failure to decode', () => {
    const run = readRun({ ...RUN_ROW, state: 'running', verdict: null, finished_at: null })
    assert.ok(run)
    assert.equal(run.verdict, null)
    assert.equal(isRunning(run.state), true)
  })

  test('a bigint arriving as a string is a number', () => {
    // postgres hands a count back as a string. `Number("")` is 0, so the empty
    // string has to stay null: a tile reading zero for something nothing
    // measured is the defect this whole module is shaped around.
    const run = readRun({ ...RUN_ROW, attempt: '4' })
    assert.equal(run?.attempt, 4)
    assert.equal(readRun({ ...RUN_ROW, attempt: '' })?.attempt, null)
  })
})

const WORKLOAD_ROW: Record<string, unknown> = {
  id: '2f1c0a1e-0000-4000-8000-0000000000aa',
  slug: 'checkout-friday',
  name: 'Checkout under Friday traffic',
  kind: 'http_scenario',
  description: 'the mix the last incident happened under',
  repository_id: '2f1c0a1e-0000-4000-8000-0000000000bb',
  repository: 'acme/store',
  archived_at: null,
  created_at: '2026-08-30T09:00:00.000Z',
  latest_version: 3,
}

describe('a workload row', () => {
  test('the fixture names exactly the columns the route selects', { skip: noSchema }, () => {
    assert.deepEqual(Object.keys(WORKLOAD_ROW).sort(), columnNames(storeTs, 'workloadColumns'))
  })

  test('it decodes, and the list route adds three of its own', () => {
    // `runs`, `last_state`, `last_verdict` and `last_run_at` come from the
    // LATERAL in workloads.list rather than from workloadColumns, which is why
    // they are not in the fixture above and are asserted here instead.
    const row = readWorkloadRow({
      ...WORKLOAD_ROW,
      runs: '12',
      last_state: 'abandoned',
      last_verdict: null,
      last_run_at: '2026-08-31T22:00:00.000Z',
    })
    assert.ok(row)
    assert.equal(row.slug, 'checkout-friday')
    assert.equal(row.runs, 12)
    assert.equal(row.lastState, 'abandoned')
    assert.equal(row.lastVerdict, null)
    assert.equal(row.latestVersion, 3)
  })

  test('a row with no kind is dropped, because a kind chooses the result table', () => {
    assert.equal(readWorkloadRow({ ...WORKLOAD_ROW, kind: 'load_profile' }), null)
    assert.equal(readWorkloadRow({ ...WORKLOAD_ROW, slug: null }), null)
  })
})

// ---------------------------------------------------------------------------
// Version bodies
// ---------------------------------------------------------------------------

describe('a version body', () => {
  test('each kind reads its own knobs and nothing else', () => {
    const observed = readBody('observed_load', { durationSeconds: 60, scale: 4 })
    assert.deepEqual(observed, { kind: 'observed_load', durationSeconds: 60, scale: 4 })

    const scenario = readBody('http_scenario', { select: ['checkout'], seed: 7, concurrency: 20 })
    assert.deepEqual(scenario, {
      kind: 'http_scenario',
      select: ['checkout'],
      seed: 7,
      concurrency: 20,
    })

    const wander = readBody('exploration', { select: ['upgrade'], seed: 'a-quiet-tuesday' })
    assert.deepEqual(wander, {
      kind: 'exploration',
      select: ['upgrade'],
      seed: 'a-quiet-tuesday',
    })
  })

  test('an absent knob stays absent on the way back out', () => {
    // The schema is strict and z.number().optional() refuses null, so a body
    // sent with `scale: null` is refused outright. An omitted key is the only
    // way to say "do not pass this flag", and it is a different thing from a
    // value of zero.
    const body = readBody('observed_load', { scale: 2 })
    assert.ok(body)
    assert.deepEqual(bodyToInput(body), { scale: 2 })
    assert.equal('durationSeconds' in bodyToInput(body), false)
  })

  test('a promoted workflow carries its manifest block back out', () => {
    // The defect this guards: a version form that dropped the block would
    // silently delete the thing a person has to paste into their repository,
    // in a save that looked like it only changed a selection.
    const body = readBody('browser_workflow', {
      select: ['upgrade-a-plan'],
      manifestBlock: 'workflows:\n  - name: upgrade-a-plan\n',
      dropped: ['The expectation is the goal.'],
    })
    assert.ok(body)
    const out = bodyToInput(body)
    assert.equal(out.manifestBlock, 'workflows:\n  - name: upgrade-a-plan\n')
    assert.deepEqual(out.dropped, ['The expectation is the goal.'])
  })

  test('an empty selection is preserved rather than dropped', () => {
    // `af test` with no --only runs every workflow the manifest declares, so
    // an empty array is a meaning and not a missing value.
    const body = readBody('browser_workflow', { select: [] })
    assert.ok(body)
    assert.deepEqual(bodyToInput(body).select, [])
  })
})

// ---------------------------------------------------------------------------
// Results
// ---------------------------------------------------------------------------

function result(over: Partial<Record<string, unknown>> = {}): RunResult {
  const decoded = readResult({
    kind: 'browser_workflow',
    workflows: 3,
    workflows_passed: 0,
    workflows_failed: 0,
    workflows_flaky: 0,
    workflows_blocked: 0,
    workflows_unverified: 3,
    steps: 0,
    duration_ms: 4200,
    error_reasons: {},
    refused_routes: [],
    recorded_at: '2026-09-01T10:04:00.000Z',
    ...over,
  })
  assert.ok(decoded)
  return decoded
}

describe('a result', () => {
  test('a browser run that checked nothing says so', () => {
    // The exit code zero over nothing defect, moved into a table and read back
    // out. A real `af test` run returned 0 passed, 0 failed and 1 unverified,
    // and with two counts a console draws that as a run with no failures.
    assert.equal(nothingWasChecked(result()), true)
    assert.equal(nothingWasChecked(result({ workflows_passed: 3, workflows_unverified: 0 })), false)
    assert.equal(nothingWasChecked(result({ workflows_flaky: 1 })), false)
    assert.equal(nothingWasChecked(result({ workflows: 0, workflows_unverified: 0 })), false)
  })

  test('it is never asked of a kind that has no workflows', () => {
    const traffic = readResult({ kind: 'observed_load', requests: 0, failures: 0 })
    assert.ok(traffic)
    assert.equal(nothingWasChecked(traffic), false)
  })

  test('error reasons come back commonest first, and a zero is not a reason', () => {
    const r = result({ error_reasons: { timeout: 4, 'connection refused': 19, '503': 0 } })
    assert.deepEqual(r.errorReasons, [
      { reason: 'connection refused', count: 19 },
      { reason: 'timeout', count: 4 },
    ])
  })

  test('a result with no kind is refused, because every renderer switches on it', () => {
    assert.equal(readResult({ requests: 10 }), null)
    assert.equal(readResult(null), null)
  })
})

describe('a route measurement', () => {
  function route(over: Record<string, unknown> = {}): RouteMetric {
    const r = readRouteMetric({ route: 'GET /checkout', sent: 100, errors: 0, p95_ms: 240, ...over })
    assert.ok(r)
    return r
  }

  test('no baseline is null and never zero', () => {
    // 0 is a legitimate reading meaning no change AND the zero value of the
    // column. Only the presence of a baseline tells them apart, which is why
    // the schema constrains the pair to exist together.
    assert.equal(increase(route()), null)
    assert.equal(increase(route({ baseline_p95_ms: 200, p95_increase: 0 })), 0)
    assert.equal(increase(route({ baseline_p95_ms: 200, p95_increase: 0.31 })), 0.31)
  })

  test('half a pair is not half an answer', () => {
    // The CHECK makes this impossible in the database. If it ever arrives
    // anyway, reporting "no change" would be a comparison nobody made.
    assert.equal(increase(route({ baseline_p95_ms: 200 })), null)
    assert.equal(increase(route({ p95_increase: 0.31 })), null)
  })

  test('the scenario is carried, because two of them can send one route', () => {
    assert.equal(route({ scenario: 'checkout' }).scenario, 'checkout')
    assert.equal(route().scenario, null)
  })
})

describe('a threshold', () => {
  function threshold(over: Record<string, unknown> = {}): ThresholdVerdict | null {
    return readThreshold({ name: 'p95_holds', measure: 'p95_below_ms', value: 'pass', ...over })
  }

  test('the verdict is read off the column the route actually selects', () => {
    // `value::text AS value`. Reading `verdict` here instead would decode every
    // row to null and render an empty table over a perfectly good payload.
    assert.equal(threshold()?.verdict, 'pass')
    assert.equal(threshold({ value: 'flaky' })?.verdict, 'flaky')
  })

  test('a row with no verdict is dropped rather than given a tone', () => {
    assert.equal(threshold({ value: null }), null)
    assert.equal(threshold({ value: 'held' }), null)
    assert.equal(threshold({ name: null }), null)
  })
})

describe('evidence', () => {
  const item = {
    kind: 'trace',
    label: 'the browser trace',
    availability: 'runner_local',
    locator: '/home/runner/work/store/trace.zip',
    sha256: null,
    size_bytes: '4096',
  }

  test('a runner path decodes and says it cannot be fetched', () => {
    const e = readEvidence(item)
    assert.ok(e)
    assert.equal(e.availability, 'runner_local')
    assert.equal(AVAILABILITY_FACTS[e.availability].fetchable, false)
    assert.equal(e.sizeBytes, 4096)
  })

  test('a row with no availability is dropped, because the guess is a broken link', () => {
    assert.equal(readEvidence({ ...item, availability: null }), null)
    assert.equal(readEvidence({ ...item, availability: 'maybe' }), null)
    assert.equal(readEvidence({ ...item, locator: null }), null)
  })
})

// ---------------------------------------------------------------------------
// The whole run
// ---------------------------------------------------------------------------

describe('inspecting a run', () => {
  test('a running run has no result, and that is not a gap', () => {
    // Nothing writes a result row before a terminal transition. The console
    // used to carry a partial results state for a running run with some of its
    // numbers; it could never fire and was deleted rather than kept.
    const detail = readRunDetail({
      run: { ...RUN_ROW, state: 'running', verdict: null, finished_at: null },
      result: null,
      routes: [],
      thresholds: [],
      evidence: [],
      cancel: null,
    })
    assert.ok(detail)
    assert.equal(detail.result, null)
    assert.deepEqual(detail.routes, [])
  })

  test('one unreadable row does not discard the collection holding it', () => {
    // The failure this rule was written after was on the other side of a
    // similar seam: a single element that would not decode threw away the
    // whole array and blanked a feature.
    const detail = readRunDetail({
      run: RUN_ROW,
      result: null,
      routes: [
        { route: 'GET /checkout', sent: 10 },
        { sent: 4 },
        { route: 'GET /cart', sent: 6 },
      ],
      thresholds: [{ name: 'holds', value: 'pass' }, { name: 'lost', value: 'held' }],
      evidence: [],
      cancel: null,
    })
    assert.ok(detail)
    assert.equal(detail.routes.length, 2)
    assert.equal(detail.thresholds.length, 1)
  })

  test('a to-one embed that is an object decodes, and an array does not', () => {
    const asObject = readRunDetail({
      run: RUN_ROW,
      result: null,
      routes: [],
      thresholds: [],
      evidence: [],
      cancel: {
        id: '2f1c0a1e-0000-4000-8000-0000000000cc',
        state: 'expired',
        outcome: null,
        detail: null,
        requested_at: '2026-09-01T10:03:00.000Z',
        acknowledged_at: null,
      },
    })
    assert.equal(asObject?.cancel?.state, 'expired')

    // Cardinality is not a guess. A to-one relation is an object or null, and
    // a decoder that took an array here would find nothing on every run.
    const asArray = readRunDetail({
      run: RUN_ROW,
      result: null,
      routes: [],
      thresholds: [],
      evidence: [],
      cancel: [{ id: 'x', state: 'expired' }],
    })
    assert.equal(asArray?.cancel, null)
  })

  test('a detail with no run at all is refused', () => {
    assert.equal(readRunDetail({ result: null }), null)
    assert.equal(readRunDetail(null), null)
  })
})

// ---------------------------------------------------------------------------
// The contradiction
// ---------------------------------------------------------------------------

describe('a verdict that disagrees with its own thresholds', () => {
  const t = (verdict: string, name = 'a'): unknown => ({ name, value: verdict })
  const decode = (rows: unknown[]): ThresholdVerdict[] =>
    rows.map((r) => readThreshold(r)).filter((r): r is ThresholdVerdict => r !== null)

  test('a pass over something that broke is called out', () => {
    const said = verdictContradiction('pass', decode([t('pass'), t('fail', 'b')]))
    assert.ok(said)
    assert.match(said, /recorded as a pass/)
    assert.match(said, /broke/)
  })

  test('a pass over something never evaluated is called out too', () => {
    // The quieter half of the same mistake. A threshold nothing measured has
    // not passed either, and naming only the broken ones leaves a reader
    // believing the rest held.
    const said = verdictContradiction('pass', decode([t('unverified'), t('blocked', 'b')]))
    assert.ok(said)
    assert.match(said, /never evaluated/)
  })

  test('a pass over a flaky threshold is called out', () => {
    const said = verdictContradiction('pass', decode([t('flaky')]))
    assert.ok(said)
    assert.match(said, /flaky/)
  })

  test('all three at once are said in one sentence, not three', () => {
    const said = verdictContradiction(
      'pass',
      decode([t('fail', 'a'), t('flaky', 'b'), t('unverified', 'c')]),
    )
    assert.ok(said)
    assert.match(said, /broke/)
    assert.match(said, /flaky/)
    assert.match(said, /never evaluated/)
    assert.equal(said.split('. ').length, 2, 'one clause and one tail')
  })

  test('a failure with nothing broken under it is called out from the other side', () => {
    const said = verdictContradiction('fail', decode([t('pass')]))
    assert.ok(said)
    assert.match(said, /not in this table/)
  })

  test('a run that agrees with itself says nothing', () => {
    assert.equal(verdictContradiction('pass', decode([t('pass'), t('pass', 'b')])), null)
    assert.equal(verdictContradiction('fail', decode([t('fail')])), null)
    assert.equal(verdictContradiction(null, decode([t('fail')])), null)
  })

  test('an inconclusive verdict is not lectured about being inconclusive', () => {
    // Flagging a blocked run for having unevaluated thresholds would restate
    // its own definition at a reader.
    assert.equal(verdictContradiction('blocked', decode([t('blocked')])), null)
    assert.equal(verdictContradiction('unverified', decode([t('unverified')])), null)
  })
})

// ---------------------------------------------------------------------------
// What a person actually pastes
// ---------------------------------------------------------------------------

describe('reading a pasted exploration document', () => {
  // `af explore --json` prints an ENVELOPE, read off ExploreJSON in
  // engine/internal/cli/explore.go: {headline, explorations, findings,
  // blocked}. The promotion route compiles ONE exploration and reads name and
  // goal off the top level of what it is sent. So the console's own
  // instruction, paste what the command printed, produced a refusal saying the
  // document carried neither, when it carried both one level down.
  const exploration = (over: Record<string, unknown> = {}) => ({
    name: 'correct a customer name',
    goal: 'change a customer name and see the change saved',
    seed: 'a-quiet-tuesday',
    reached: false,
    outcome: { verdict: 'unverified', cause: 'goal_unreached' },
    journey: [{ kind: 'goto', url: '/orders' }],
    findings: [],
    missing: [],
    ...over,
  })
  const envelope = (explorations: unknown[], over: Record<string, unknown> = {}) => ({
    headline: '2 goals explored, 1 reached',
    explorations,
    findings: [],
    blocked: 0,
    ...over,
  })

  test('the envelope the command prints yields one entry per goal, in order', () => {
    const read = readExplorations(
      envelope([exploration({ name: 'what each customer spent', reached: true }), exploration()]),
    )
    assert.equal(read.refusal, null)
    assert.deepEqual(
      read.explorations.map((e) => e.name),
      ['what each customer spent', 'correct a customer name'],
    )
    assert.equal(read.explorations[0]!.reached, true)
    assert.equal(read.explorations[1]!.reached, false)
    assert.equal(read.explorations[1]!.verdict, 'unverified')
  })

  test('what is sent on is the element itself, not a rebuild of it', () => {
    // The compiler reads the journey, the findings and the missing list, none
    // of which the picker summary carries. Rebuilding an exploration out of
    // the four fields shown would compile a workflow from a walk with no
    // steps in it.
    const read = readExplorations(envelope([exploration()]))
    const raw = read.explorations[0]!.raw as Record<string, unknown>
    assert.ok(Array.isArray(raw.journey))
    assert.equal((raw.journey as unknown[]).length, 1)
    assert.ok('missing' in raw)
    assert.ok('findings' in raw)
  })

  test('a single exploration out of the array works too', () => {
    const read = readExplorations(exploration())
    assert.equal(read.refusal, null)
    assert.equal(read.explorations.length, 1)
    assert.equal(read.explorations[0]!.name, 'correct a customer name')
  })

  test('one unusable entry does not take the others with it', () => {
    const read = readExplorations(envelope([{ goal: 'no name here' }, exploration()]))
    assert.equal(read.explorations.length, 1)
    assert.equal(read.explorations[0]!.name, 'correct a customer name')
  })

  test('a run where everything was blocked says that, not "no name"', () => {
    // Blocked is the runner or the environment failing to start. Telling
    // somebody their document has no name would send them to look at the
    // document rather than at the run.
    const read = readExplorations(envelope([], { blocked: 3 }))
    assert.equal(read.explorations.length, 0)
    assert.ok(read.refusal)
    assert.match(read.refusal, /3 of its goals were blocked/)
    assert.match(read.refusal, /rather than a finding about the application/)
  })

  test('an empty list with nothing blocked says something different', () => {
    const read = readExplorations(envelope([]))
    assert.ok(read.refusal)
    assert.match(read.refusal, /needs a name/)
    assert.equal(read.refusal.includes('blocked'), false)
  })

  test('a document that is neither names what it looked for', () => {
    const read = readExplorations({ workflows: [] })
    assert.ok(read.refusal)
    assert.match(read.refusal, /explorations array/)
    for (const junk of [null, 42, 'a string', [1, 2]]) {
      const r = readExplorations(junk)
      assert.equal(r.explorations.length, 0)
      assert.ok(r.refusal, `${JSON.stringify(junk)} was accepted`)
    }
  })
})

// ---------------------------------------------------------------------------
// A red badge with nothing beside it
// ---------------------------------------------------------------------------

describe('what decided a verdict', () => {
  // The gap this closes was found by reading the engine's own bytes rather
  // than the contract. `mixDetail` in engine/internal/workload/project.go
  // returns a sentence ONLY when nothing was sent, so a load run that sent
  // traffic and failed a threshold arrives with an empty detail. Rendered
  // naively that is a red FAIL badge with nothing next to it: the reader knows
  // something broke and is told nothing about what.
  const t = (verdict: string, name: string, scope: string | null = null): ThresholdVerdict => {
    const row = readThreshold({ name, scope, value: verdict, measure: 'p95_below_ms' })
    assert.ok(row)
    return row
  }

  test('a failure names the thresholds that failed it', () => {
    const said = whatDecidedIt('fail', [t('pass', 'a'), t('fail', 'checkout_p95', 'GET /checkout')])
    assert.ok(said)
    assert.match(said, /One threshold broke/)
    assert.match(said, /checkout_p95 on GET \/checkout/)
  })

  test('a scope only appears when there is one', () => {
    const said = whatDecidedIt('fail', [t('fail', 'error_rate_holds')])
    assert.ok(said)
    assert.match(said, /error_rate_holds\./)
    assert.equal(said.includes(' on '), false)
  })

  test('a long list stops at three and counts the rest', () => {
    // Forty names is a wall somebody skips, and the table underneath has all
    // of them anyway.
    const rows = ['a', 'b', 'c', 'd', 'e'].map((n) => t('fail', n))
    const said = whatDecidedIt('fail', rows)
    assert.ok(said)
    assert.match(said, /5 thresholds broke/)
    assert.match(said, /a, b, c and 2 more/)
  })

  test('a failure with rows that all held says which absence it is', () => {
    const said = whatDecidedIt('fail', [t('pass', 'a'), t('pass', 'b')])
    assert.ok(said)
    assert.match(said, /Nothing in the thresholds below broke/)
  })

  test('a failure with no rows at all says something different', () => {
    // Pointing at "the thresholds below" when there are none sends somebody
    // to an empty card.
    const said = whatDecidedIt('fail', [])
    assert.ok(said)
    assert.match(said, /No thresholds were recorded/)
    assert.equal(said.includes('below'), false)
  })

  test('A FAILURE IS NEVER SILENT, whatever the rows say', () => {
    // The property, not the cases. This is the whole point of the function:
    // there is no combination of rows for which a fail verdict renders with
    // nothing beside it.
    const shapes: ThresholdVerdict[][] = [
      [],
      [t('pass', 'a')],
      [t('fail', 'a')],
      [t('flaky', 'a')],
      [t('blocked', 'a'), t('unverified', 'b')],
      [t('fail', 'a', 'GET /x'), t('fail', 'b'), t('fail', 'c'), t('fail', 'd')],
    ]
    for (const rows of shapes) {
      const said = whatDecidedIt('fail', rows)
      assert.ok(said, `a fail over ${rows.length} rows said nothing`)
      assert.ok(said.length > 40, `a fail over ${rows.length} rows said only ${JSON.stringify(said)}`)
    }
  })

  test('flaky names its own rows and stays quiet when it has none', () => {
    const said = whatDecidedIt('flaky', [t('flaky', 'retry_is_fast')])
    assert.ok(said)
    assert.match(said, /came back flaky/)
    // Unlike a failure, a flaky run with no flaky row is not a contradiction
    // worth a sentence: the run verdict can be flaky for a workflow that
    // passed only sometimes, which is not a threshold at all.
    assert.equal(whatDecidedIt('flaky', [t('pass', 'a')]), null)
  })

  test('a verdict that is not a finding says nothing', () => {
    for (const v of ['pass', 'blocked', 'unverified'] as const) {
      assert.equal(whatDecidedIt(v, [t('fail', 'a')]), null, `${v} volunteered a sentence`)
    }
    assert.equal(whatDecidedIt(null, [t('fail', 'a')]), null)
  })
})

// ---------------------------------------------------------------------------
// Numbers a person reads
// ---------------------------------------------------------------------------

describe('formatting', () => {
  test('a missing measurement is a dash and never a zero', () => {
    assert.equal(ms(null), '--')
    assert.equal(percent(null), '--')
    assert.equal(duration(null), '--')
  })

  test('a small error rate does not round away to none', () => {
    // 0.4% rounding to "0%" reads as no errors at all.
    assert.equal(percent(0.004), '0.40%')
    assert.equal(percent(0), '0.0%')
    assert.equal(percent(0.5), '50%')
  })

  test('a short run keeps the precision it was measured at', () => {
    // 840ms rounding to "1s" loses the only interesting part.
    assert.equal(duration(840), '840 ms')
    assert.equal(duration(4200), '4.20 s')
    assert.equal(duration(125_000), '2m 5s')
  })

})
