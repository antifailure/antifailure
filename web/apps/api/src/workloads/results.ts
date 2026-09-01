// What an engine reports about a run, and how little of it is trusted.
//
// This is a decoder for a document written by a program the control plane does
// not run, on a machine it cannot reach, at a version it did not choose. Three
// rules follow from that and every one of them was earned somewhere else in
// this repository:
//
// ONE BAD ELEMENT MUST NOT DISCARD THE COLLECTION. A route measurement with a
// string where a number belongs is skipped and counted, and the other four
// hundred are written. The alternative, an all or nothing decode of an array of
// external items, is a latent outage: one surprising element and the run has no
// measurements at all, which reads as "the run measured nothing" rather than as
// "the decoder refused".
//
// A TO-ONE IS AN OBJECT AND A TO-MANY IS AN ARRAY. `result` is one aggregate
// and `routes`, `thresholds` and `evidence` are lists. A sender that puts an
// object where a list belongs gets that list treated as empty and said so in
// the note, rather than the whole event being rejected.
//
// THE DOCUMENT IS THE CONTRACT, AND THE ENGINE'S NATIVE RESULT IS NOT.
//
// An engine sends the RESULT DOCUMENT (engine/internal/workload/result.go,
// `antifailure.workload.result/v1`). It also HAS a native result per kind, its
// own `load.Result` and friends, which the document carries under `native` and
// which the transport deletes before the event goes out. The two spell the same
// measurements differently: the native load result says `sent` and `rate` and
// nests its percentiles under `overall`, and it calls its refusals
// `refused_as_unsafe`. The document says `requests` and `achieved_rate`, is
// flat, and says `refused_routes`.
//
// This file read the NATIVE names for the run-wide aggregate of the two traffic
// kinds. It failed silently and it failed badly: `requests` fell back to zero
// because the CHECK needs a count, so a run that sent twelve hundred requests
// was recorded as having sent NONE with every percentile null, while every
// route, threshold and piece of evidence beside it decoded perfectly. A console
// draws that as a strange run rather than as a broken decoder. Neither suite
// could see it, because the engine tested a document it wrote itself and this
// tested a document IT wrote itself, and nobody had put a real message on the
// wire between them.
//
// So: ONE spelling here, the document's, and never both. A decoder that accepts
// the native name too would have made the defect above invisible forever rather
// than fixing it, because the wrong name would have kept working. Two things
// hold it: report-seam.test.ts decodes REAL bytes an engine produced, and
// engine/internal/controlplane/report_shape_test.go reads aggregateFor and
// asserts every name it reaches for is one the engine's own struct tags emit.
// That second one matches the literal `r.<name>` form, so reaching a field
// through a variable or a lookup table would leave it green over nothing.
//
// TOLERANT ON THE READ BOUNDARY, STRICT ON THE WRITE BOUNDARY. Numbers arriving
// as strings are coerced, missing optional fields are null, and out of range
// values are dropped. The kind is the one thing that is not coerced: a payload
// claiming to be a browser result for a run this database says is a load
// workload is refused outright, because writing the database's kind with the
// payload's numbers would produce a row that satisfies every constraint and
// means something different from what happened.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'
import type { WorkloadKind } from './bodies.ts'

/** The five words the rest of the product uses. A sixth here would be a sixth
 *  vocabulary for a reader to learn. */
const VERDICTS = ['pass', 'fail', 'flaky', 'blocked', 'unverified'] as const
type Verdict = (typeof VERDICTS)[number]

const AVAILABILITY = ['uploaded', 'runner_local', 'not_retained'] as const

/** Ceilings, so one report cannot make the ingestion transaction unbounded. A
 *  batch is already capped at 500 events and each of those may carry a report,
 *  so these numbers multiply. */
const MAX_ROUTES = 500
const MAX_THRESHOLDS = 200
const MAX_EVIDENCE = 100

interface Skipped {
  routes: number
  thresholds: number
  evidence: number
}

export interface DecodedReport {
  verdict: Verdict | null
  detail: string | null
  failureCode: string | null
  aggregate: Aggregate
  routes: RouteMetric[]
  thresholds: ThresholdVerdict[]
  evidence: Evidence[]
  skipped: Skipped
}

/** The columns workload_run_results holds, already narrowed to the ones this
 *  kind is allowed to fill. The CHECK in the migration refuses the rest. */
interface Aggregate {
  requests: number | null
  failures: number | null
  errorRate: number | null
  targetRate: number | null
  achievedRate: number | null
  p50Ms: number | null
  p90Ms: number | null
  p95Ms: number | null
  p99Ms: number | null
  maxMs: number | null
  sessions: number | null
  iterations: number | null
  scheduledMs: number | null
  workflows: number | null
  workflowsPassed: number | null
  workflowsFailed: number | null
  workflowsFlaky: number | null
  workflowsBlocked: number | null
  workflowsUnverified: number | null
  steps: number | null
  findings: number | null
  goals: number | null
  goalsReached: number | null
  durationMs: number | null
  source: string | null
  errorReasons: Record<string, number>
  refusedRoutes: string[]
}

interface RouteMetric {
  scenario: string | null
  route: string
  sent: number
  errors: number
  p50Ms: number | null
  p90Ms: number | null
  p95Ms: number | null
  p99Ms: number | null
  maxMs: number | null
  baselineP95Ms: number | null
  p95Increase: number | null
}

interface ThresholdVerdict {
  scenario: string | null
  name: string
  scope: string | null
  measure: string
  threshold: number | null
  observed: number | null
  value: Verdict
  detail: string | null
}

interface Evidence {
  kind: string
  label: string | null
  availability: (typeof AVAILABILITY)[number]
  locator: string
  sha256: string | null
  sizeBytes: number | null
}

export class ReportRefused extends Error {}

/**
 * Reads a `workload.finished` payload into rows.
 *
 * `kind` comes from the database rather than from the payload, and a payload
 * that names a different one is refused. See the header.
 */
export function decodeReport(kind: WorkloadKind, payload: Record<string, unknown>): DecodedReport {
  const claimed = str(payload.kind, 40)
  if (claimed !== null && claimed !== kind) {
    throw new ReportRefused(
      `the report says it is a ${claimed} result and this run is a ${kind} workload`,
    )
  }

  const skipped: Skipped = { routes: 0, thresholds: 0, evidence: 0 }
  const result = obj(payload.result)

  return {
    verdict: verdict(payload.verdict),
    detail: str(payload.detail, 2000),
    failureCode: str(payload.failure_code, 60),
    aggregate: aggregateFor(kind, result),
    routes: list(payload.routes, MAX_ROUTES, route, skipped, 'routes'),
    thresholds: list(payload.thresholds, MAX_THRESHOLDS, threshold, skipped, 'thresholds'),
    evidence: list(payload.evidence, MAX_EVIDENCE, evidence, skipped, 'evidence'),
    skipped,
  }
}

/**
 * The aggregate, narrowed to what the kind is allowed to say.
 *
 * Narrowed HERE rather than left to the constraint, and that is deliberate. The
 * constraint refuses a bad row, which is the right last line, and a refusal
 * inside the ingestion transaction rejects an entire batch of events for one
 * confused sender. So a load report that carries a workflow count has the count
 * dropped and is written as a load result, because the numbers that belong to
 * the kind are still worth keeping. What is NOT dropped is a report claiming to
 * be a different kind, which is refused above: that is not a stray field, it is
 * a sender that thinks this run is something else.
 */
function aggregateFor(kind: WorkloadKind, r: Record<string, unknown>): Aggregate {
  const empty: Aggregate = {
    requests: null, failures: null, errorRate: null, targetRate: null, achievedRate: null,
    p50Ms: null, p90Ms: null, p95Ms: null, p99Ms: null, maxMs: null,
    sessions: null, iterations: null, scheduledMs: null,
    workflows: null, workflowsPassed: null, workflowsFailed: null,
    workflowsFlaky: null, workflowsBlocked: null, workflowsUnverified: null,
    steps: null,
    findings: null, goals: null, goalsReached: null,
    durationMs: num(r.duration_ms, 0, 1e12),
    source: str(r.source, 200),
    // Failures by reason. Bounded and coerced per entry, because it is a map
    // from an untrusted sender and one absurd key must not take the rest with
    // it, the same rule the lists follow.
    errorReasons: counts(r.errors),
    refusedRoutes: strings(r.refused_routes, 100, 300),
  }
  // Flat, and read straight off the result object.
  //
  // This read the percentiles through an `overall` object, and read the count
  // and the rate as `sent` and `rate`, which are internal/load.Result's names:
  // the engine's NATIVE type, which is precisely the document nobody sends. What arrives is the projected result the
  // engine's `--result` writes, and it spells them `requests`, `achieved_rate`
  // and flat percentiles, with `refused_routes` for the refusals the native
  // type calls `refused_as_unsafe`.
  //
  // It failed silently, which is the whole reason it survived a green suite on
  // both sides. `requests` falls back to zero because the CHECK needs a count,
  // so a load run that sent 1200 requests was written with 0 requests and
  // every percentile null, `skipped` stayed at zero so the note never fired,
  // and every route row decoded perfectly. A console would have drawn per route
  // p95s over a run-wide total of nothing and nobody would have read that as a
  // decoder bug.
  //
  // Found by studio-emitters dumping the real bytes and running this file over
  // them, which is the only thing that could have found it: neither suite
  // crossed the seam.
  //
  // ONE SPELLING, NOT BOTH, and this is a correction to a fix that accepted
  // both. Tolerance is the right instinct for a VALUE arriving in an unexpected
  // form, which is what this file's header argues for and what `num` does with
  // a number written as a string. It is the wrong instinct for a NAME, because
  // a name is the contract: accepting `sent` as well as `requests` means the
  // decoder still works when the two ends disagree, so the disagreement stops
  // being visible and the next one is found the same way this one was, by
  // somebody dumping bytes. It also leaves engine/internal/controlplane/
  // report_shape_test.go RED after a merge, naming those three names, which was
  // checked by running that gate over both versions of this file rather than
  // argued. That gate reads THIS function's source, so a name written here even
  // in a comment, in the `r.` form, is a name it will report.
  const percentiles = {
    p50Ms: num(r.p50_ms, 0, 1e9),
    p90Ms: num(r.p90_ms, 0, 1e9),
    p95Ms: num(r.p95_ms, 0, 1e9),
    p99Ms: num(r.p99_ms, 0, 1e9),
    maxMs: num(r.max_ms, 0, 1e9),
  }

  switch (kind) {
    case 'observed_load':
      return {
        ...empty,
        ...percentiles,
        // Zero rather than null, because the CHECK requires a load result to
        // carry a request count and an engine that reported no number sent no
        // requests as far as anybody can tell. Saying nothing here would refuse
        // the whole row and lose the percentiles with it.
        requests: whole(r.requests, 0, 2_147_483_647) ?? 0,
        failures: whole(r.failures, 0, 2_147_483_647),
        errorRate: num(r.error_rate, 0, 1),
        targetRate: num(r.target_rate, 0, 1e9),
        achievedRate: num(r.achieved_rate, 0, 1e9),
      }
    case 'http_scenario':
      return {
        ...empty,
        ...percentiles,
        requests: whole(r.requests, 0, 2_147_483_647) ?? 0,
        failures: whole(r.failures, 0, 2_147_483_647),
        errorRate: num(r.error_rate, 0, 1),
        sessions: whole(r.sessions, 0, 1_000_000) ?? 0,
        iterations: whole(r.iterations, 0, 1_000_000),
        scheduledMs: num(r.scheduled_ms, 0, 1e12),
      }
    case 'browser_workflow':
      return {
        ...empty,
        workflows: whole(r.workflows, 0, 100_000) ?? 0,
        workflowsPassed: whole(r.workflows_passed, 0, 100_000),
        workflowsFailed: whole(r.workflows_failed, 0, 100_000),
        workflowsFlaky: whole(r.workflows_flaky, 0, 100_000),
        workflowsBlocked: whole(r.workflows_blocked, 0, 100_000),
        workflowsUnverified: whole(r.workflows_unverified, 0, 100_000),
        steps: whole(r.steps, 0, 1_000_000),
      }
    case 'exploration':
      return {
        ...empty,
        findings: whole(r.findings, 0, 100_000) ?? 0,
        // Zero rather than null for the same reason a load result's request
        // count is: the CHECK requires an exploration result to carry a goal
        // count, and refusing the whole row would lose the findings with it.
        goals: whole(r.goals, 0, 100_000) ?? 0,
        goalsReached: whole(r.goals_reached, 0, 100_000),
        steps: whole(r.steps, 0, 1_000_000),
      }
  }
}

function route(raw: unknown): RouteMetric | null {
  const r = obj(raw)
  const name = str(r.route, 400)
  if (name === null) return null
  const sent = whole(r.sent, 0, 2_147_483_647)
  if (sent === null) return null
  // A baseline and an increase travel together or not at all, because the
  // constraint says so and because "no change" and "nothing to compare with"
  // are different answers. The engine carries a separate has_baseline flag for
  // exactly this reason, so it is honoured when it is present and inferred from
  // the baseline when it is not.
  const baseline = num(r.baseline_p95_ms, 0, 1e9)
  const declared = bool(r.has_baseline)
  const hasBaseline = declared === null ? baseline !== null : declared && baseline !== null
  return {
    scenario: str(r.scenario, 200),
    route: name,
    sent,
    errors: whole(r.errors, 0, 2_147_483_647) ?? 0,
    p50Ms: num(obj(r.latency).p50_ms ?? r.p50_ms, 0, 1e9),
    p90Ms: num(obj(r.latency).p90_ms ?? r.p90_ms, 0, 1e9),
    p95Ms: num(obj(r.latency).p95_ms ?? r.p95_ms, 0, 1e9),
    p99Ms: num(obj(r.latency).p99_ms ?? r.p99_ms, 0, 1e9),
    maxMs: num(obj(r.latency).max_ms ?? r.max_ms, 0, 1e9),
    baselineP95Ms: hasBaseline ? baseline : null,
    p95Increase: hasBaseline ? (num(r.p95_increase, -1e6, 1e6) ?? 0) : null,
  }
}

function threshold(raw: unknown): ThresholdVerdict | null {
  const t = obj(raw)
  const name = str(t.name, 300)
  if (name === null) return null
  const value = verdict(t.verdict ?? t.value)
  if (value === null) return null
  return {
    scenario: str(t.scenario, 200),
    name,
    // `scope` is what the engine's AssertionResult calls it and `step` is what
    // the manifest calls it. Both are read so either wire spelling lands.
    scope: str(t.scope ?? t.step, 400),
    // The measure is free text on purpose: the engine adds an assertion by
    // releasing, and a customer's database should not need a migration to
    // record a measure it was sent. Unknown falls back to a word that says so
    // rather than to an empty column.
    measure: str(t.measure, 100) ?? 'unspecified',
    threshold: num(t.threshold, -1e12, 1e12),
    observed: num(t.observed, -1e12, 1e12),
    value,
    detail: str(t.detail, 2000),
  }
}

function evidence(raw: unknown): Evidence | null {
  const e = obj(raw)
  const kind = str(e.kind, 60)
  const locator = str(e.locator ?? e.path, 2048)
  if (kind === null || locator === null) return null
  const declared = str(e.availability, 40)
  const availability = (AVAILABILITY as readonly string[]).includes(declared ?? '')
    ? (declared as Evidence['availability'])
    // An engine that does not say is telling us about a file on its own disk.
    // `runner_local` is the honest default and `uploaded` would be a claim the
    // sender never made, which is the exact claim a broken console link is
    // built on.
    : 'runner_local'
  const sha256 = str(e.sha256, 64)
  if (availability === 'uploaded' && sha256 === null) return null
  return {
    kind,
    label: str(e.label, 300),
    availability,
    locator,
    sha256,
    sizeBytes: whole(e.size_bytes, 0, Number.MAX_SAFE_INTEGER),
  }
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

/**
 * Writes what a run measured.
 *
 * Every statement is ON CONFLICT DO NOTHING, because the same report can arrive
 * twice under two event identifiers: an engine that resends after a timeout
 * assigns a new identifier only if it regenerates the event, and a spooled
 * batch replayed from disk after a crash is the ordinary case. The result
 * tables have no UPDATE grant at all, so a second copy cannot rewrite the
 * first, and DO NOTHING is what turns that refusal into a no-op rather than
 * into a failed batch.
 */
export async function writeReport(
  db: Db,
  input: { orgId: string; runId: string; kind: WorkloadKind; report: DecodedReport },
): Promise<void> {
  const a = input.report.aggregate
  await db.execute(sql`
    INSERT INTO workload_run_results (
      org_id, workload_run_id, kind, requests, failures, error_rate,
      target_rate, achieved_rate, p50_ms, p90_ms, p95_ms, p99_ms, max_ms,
      sessions, iterations, scheduled_ms,
      workflows, workflows_passed, workflows_failed,
      workflows_flaky, workflows_blocked, workflows_unverified, steps,
      findings, goals, goals_reached, duration_ms, source, error_reasons, refused_routes)
    VALUES (
      ${input.orgId}, ${input.runId}, ${input.kind}::workload_kind,
      ${a.requests}, ${a.failures}, ${a.errorRate},
      ${a.targetRate}, ${a.achievedRate},
      ${a.p50Ms}, ${a.p90Ms}, ${a.p95Ms}, ${a.p99Ms}, ${a.maxMs},
      ${a.sessions}, ${a.iterations}, ${a.scheduledMs},
      ${a.workflows}, ${a.workflowsPassed}, ${a.workflowsFailed},
      ${a.workflowsFlaky}, ${a.workflowsBlocked}, ${a.workflowsUnverified}, ${a.steps},
      ${a.findings}, ${a.goals}, ${a.goalsReached}, ${a.durationMs}, ${a.source},
      ${JSON.stringify(a.errorReasons)}::jsonb,
      -- sql.param rather than the bare array. The template inlines a JavaScript
      -- array as a parenthesised value list, so an empty one renders as an empty
      -- pair of brackets, which is a syntax error, and a full one renders as a
      -- row constructor rather than as an array. Found by running it: every
      -- report with no refused routes, which is every healthy report, failed
      -- the whole batch with a message about the syntax near a bracket.
      ${sql.param(a.refusedRoutes)}::text[])
    ON CONFLICT (workload_run_id) DO NOTHING`)

  let position = 0
  for (const r of input.report.routes) {
    await db.execute(sql`
      INSERT INTO workload_route_metrics (
        org_id, workload_run_id, scenario, route, sent, errors,
        p50_ms, p90_ms, p95_ms, p99_ms, max_ms, baseline_p95_ms, p95_increase, position)
      VALUES (${input.orgId}, ${input.runId}, ${r.scenario}, ${r.route}, ${r.sent}, ${r.errors},
              ${r.p50Ms}, ${r.p90Ms}, ${r.p95Ms}, ${r.p99Ms}, ${r.maxMs},
              ${r.baselineP95Ms}, ${r.p95Increase}, ${position})
      ON CONFLICT (workload_run_id, coalesce(scenario, ''), route) DO NOTHING`)
    position += 1
  }

  position = 0
  for (const t of input.report.thresholds) {
    await db.execute(sql`
      INSERT INTO workload_threshold_verdicts (
        org_id, workload_run_id, scenario, name, scope, measure, threshold, observed, value, detail, position)
      VALUES (${input.orgId}, ${input.runId}, ${t.scenario}, ${t.name}, ${t.scope}, ${t.measure},
              ${t.threshold}, ${t.observed}, ${t.value}::verdict_value, ${t.detail}, ${position})
      ON CONFLICT (workload_run_id, coalesce(scenario, ''), name, coalesce(scope, '')) DO NOTHING`)
    position += 1
  }

  for (const e of input.report.evidence) {
    await db.execute(sql`
      INSERT INTO workload_evidence (
        org_id, workload_run_id, kind, label, availability, locator, sha256, size_bytes)
      VALUES (${input.orgId}, ${input.runId}, ${e.kind}, ${e.label},
              ${e.availability}::workload_evidence_availability, ${e.locator},
              ${e.sha256}, ${e.sizeBytes})
      ON CONFLICT (workload_run_id, kind, locator) DO NOTHING`)
  }
}

// ---------------------------------------------------------------------------
// Coercion
//
// One function per shape, each returning null rather than throwing, because a
// throw here would take the whole batch with it. Every bound is a real column
// bound: an integer column takes 2147483647 and a double takes a finite number,
// and a value outside one of those is a sender saying something the column
// cannot hold rather than a number worth rounding.
// ---------------------------------------------------------------------------

function obj(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

function str(value: unknown, max: number): string | null {
  if (typeof value !== 'string') return null
  const trimmed = value.trim()
  if (trimmed.length === 0 || trimmed.length > max) return null
  return trimmed
}

function strings(value: unknown, maxItems: number, maxLength: number): string[] {
  if (!Array.isArray(value)) return []
  const out: string[] = []
  for (const item of value.slice(0, maxItems)) {
    const s = str(item, maxLength)
    if (s !== null) out.push(s)
  }
  return out
}

/** A number, or a number written as a string, which is what a JSON encoder in
 *  another language does with a large integer often enough to be worth taking. */
function num(value: unknown, min: number, max: number): number | null {
  const n = typeof value === 'number' ? value : typeof value === 'string' ? Number(value) : NaN
  if (!Number.isFinite(n)) return null
  if (n < min || n > max) return null
  return n
}

function whole(value: unknown, min: number, max: number): number | null {
  const n = num(value, min, max)
  if (n === null) return null
  return Number.isInteger(n) ? n : Math.round(n)
}

/**
 * A map of counts from an untrusted sender.
 *
 * Bounded per entry and in total, and one unreadable entry is skipped rather
 * than taking the map with it, which is the same rule the lists follow. The
 * keys are a closed set the load runner produces, but they are not checked
 * against it: the engine adds a reason by releasing, and a control plane that
 * dropped an unknown one would lose exactly the failure nobody has seen before.
 */
function bool(value: unknown): boolean | null {
  return typeof value === 'boolean' ? value : null
}

function counts(value: unknown): Record<string, number> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return {}
  const out: Record<string, number> = {}
  for (const [key, raw] of Object.entries(value as Record<string, unknown>).slice(0, 100)) {
    const name = str(key, 200)
    const n = whole(raw, 0, 2_147_483_647)
    if (name !== null && n !== null) out[name] = n
  }
  return out
}

function verdict(value: unknown): Verdict | null {
  const s = str(value, 40)
  return s !== null && (VERDICTS as readonly string[]).includes(s) ? (s as Verdict) : null
}

/**
 * Decodes a list without letting one element take the rest with it.
 *
 * A value that is not an array at all yields an empty list and is counted as
 * one skip, so a sender that put an object where a list belongs is visible in
 * the note rather than silently reported as having measured nothing.
 */
function list<T>(
  value: unknown,
  max: number,
  decode: (raw: unknown) => T | null,
  skipped: Skipped,
  bucket: keyof Skipped,
): T[] {
  if (!Array.isArray(value)) {
    if (value !== undefined && value !== null) skipped[bucket] += 1
    return []
  }
  const out: T[] = []
  for (const item of value.slice(0, max)) {
    const decoded = decode(item)
    if (decoded === null) skipped[bucket] += 1
    else out.push(decoded)
  }
  if (value.length > max) skipped[bucket] += value.length - max
  return out
}
