/**
 * The Load area's data boundary.
 *
 * Every `load.*` path the console calls is named in this file and nowhere else,
 * so the contract with the control plane is one file to reconcile rather than a
 * grep across a dozen components.
 *
 * The shapes here are the ENGINE'S, read out of `engine/internal/load` rather
 * than designed to suit the screen. That is deliberate and it cost a rewrite:
 * the first version of this file invented a percentile set, a signed
 * millisecond delta and a generic threshold row, all of which read plausibly
 * and none of which the engine produces. A console whose vocabulary differs
 * from the tool's makes every report a translation, and a translation is where
 * a number quietly changes meaning.
 *
 * Three rules run through it.
 *
 * A number the control plane did not record arrives as `null`, never as zero.
 * `P95Increase` is the sharpest case: 0 is a legitimate value meaning no
 * change AND the zero value of the field, which is exactly why the engine
 * carries `HasBaseline` beside it.
 *
 * A verdict that is not a verdict is never drawn as one. `blocked` and
 * `unverified` are two of the product's four words and neither means the
 * software is fine.
 *
 * And observed traffic, an authored scenario and an exploration are three
 * different things. The third is not even a load source: `af explore` compiles
 * a discovery into a manifest workflow for `af test`, not into a scenario, so
 * it is modelled apart from the other two rather than beside them.
 */

import { query, mutate } from "@/lib/api";

/* -------------------------------------------------------------------------
 * Reading foreign data
 *
 * Tolerant on the way in, strict on the way out. These take `unknown` because
 * that is honestly what a JSON body is, and they answer `null` rather than
 * throwing, so one surprising field cannot discard the object containing it.
 * ---------------------------------------------------------------------- */

export function str(v: unknown): string | null {
  return typeof v === "string" && v.length > 0 ? v : null;
}

/** A number, or null. Accepts the string form a driver hands back for numeric
 *  columns, but rejects the empty string, because `Number("")` is 0 and a stat
 *  tile reading zero for something nothing measured is the defect this whole
 *  file is shaped around. */
export function num(v: unknown): number | null {
  if (typeof v === "number") return Number.isFinite(v) ? v : null;
  if (typeof v === "string" && v.trim() !== "") {
    const n = Number(v);
    return Number.isFinite(n) ? n : null;
  }
  return null;
}

export function bool(v: unknown): boolean | null {
  return typeof v === "boolean" ? v : null;
}

/** An array, each element decoded on its own, failures dropped rather than the
 *  collection. */
export function list<T>(v: unknown, each: (item: unknown) => T | null): T[] {
  if (!Array.isArray(v)) return [];
  const out: T[] = [];
  for (const item of v) {
    const decoded = each(item);
    if (decoded !== null) out.push(decoded);
  }
  return out;
}

function obj(v: unknown): Record<string, unknown> | null {
  return typeof v === "object" && v !== null && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : null;
}

/** Read snake_case first, camelCase second, so either serialiser works. */
function pick(o: Record<string, unknown>, snake: string, camel: string): unknown {
  return o[snake] !== undefined ? o[snake] : o[camel];
}

/* -------------------------------------------------------------------------
 * Where the traffic comes from
 * ---------------------------------------------------------------------- */

/**
 * The two things that can be sent at a twin.
 *
 * Two, not three. Exploration is not here, and leaving it out is the point:
 * `explore.Compile` returns a `schema.Workflow`, so an exploration produces
 * something `af test` runs, not something `af load` sends. Listing it as a
 * third source would put a lie in the type system, and every screen built on
 * that type would inherit it.
 */
export type SourceKind = "observed" | "deterministic";

export const SOURCE_KINDS: readonly SourceKind[] = ["observed", "deterministic"];

export function sourceKindOf(v: unknown): SourceKind | null {
  return v === "observed" || v === "deterministic" ? v : null;
}

/**
 * What each kind is, and what a result from it is worth.
 *
 * `reproducible` is the field that earns this table. It is the difference
 * between a number you can hand somebody and one you can only ask them to
 * believe, and the answer is not the same for the two kinds.
 */
export const SOURCE_FACTS: Record<
  SourceKind,
  { noun: string; provenance: string; what: string; reproducible: string }
> = {
  observed: {
    noun: "Observed",
    provenance: "Measured",
    what: "A weighted mix read from what production actually served, compiled from an OTLP export or an access log. The mix is the point: what breaks under real traffic is the page nobody thinks about that is nine percent of requests.",
    reproducible:
      "As a shape. The mix and the rate replay exactly, and the picker is seeded so two runs send the same sequence. The individual production requests it was derived from do not replay.",
  },
  deterministic: {
    noun: "Deterministic",
    provenance: "Authored",
    what: "A scenario somebody wrote and committed: named steps in an order, with think time between them, and assertions that say what holding up means.",
    reproducible:
      "Exactly. The same scenario at the same seed plans the same requests in the same order.",
  },
};

/**
 * One route in a mix, as the engine names it.
 *
 * `Route.String()` in the engine renders "METHOD /path" and this keeps the two
 * halves apart so a table can align the method column. Weight is the share of
 * production's traffic the route carried.
 */
export interface Route {
  method: string | null;
  path: string;
  weight: number | null;
}

export function readRoute(v: unknown): Route | null {
  const o = obj(v);
  if (!o) return null;
  // The engine also serialises a route as one string. Accept both, because a
  // reader that only understood the split form would render an empty table
  // against a perfectly good payload.
  const joined = str(o.route);
  if (joined && !str(o.path)) {
    const parts = joined.split(" ");
    return parts.length > 1
      ? { method: parts[0]!, path: parts.slice(1).join(" "), weight: num(o.weight) }
      : { method: null, path: joined, weight: num(o.weight) };
  }
  const path = str(o.path);
  if (!path) return null;
  return { method: str(o.method), path, weight: num(o.weight) };
}

export interface ObservedSource {
  kind: "observed";
  /** Where the mix came from, real traffic or a guess. The engine carries this
   *  out of the run for exactly this reason. */
  origin: string | null;
  sample: string | null;
  windowStart: string | null;
  windowEnd: string | null;
  requestsObserved: number | null;
  /** The compiled mix, weighted. */
  routes: Route[];
  /**
   * Routes the safety patterns excluded.
   *
   * `Shape.Safe` returns these alongside the shape it kept, and showing them
   * is worth more than hiding them: every route is unsafe until a pattern says
   * otherwise, so a mix that looks thin is usually a safe list that is too
   * narrow rather than traffic that was not there.
   */
  excluded: Route[];
}

export interface Step {
  name: string | null;
  request: string;
  thinkMs: number | null;
  jitterMs: number | null;
  afterMs: number | null;
  /** Steps sent at the same time as this one. Flattening them into siblings
   *  would present a different scenario from the one that runs. */
  parallel: Step[];
}

/** An assertion, in the scenario's own field names. A console that renamed
 *  `p95_below_ms` to "latency threshold" makes somebody translate back to the
 *  YAML every time they want to change it. */
export interface Assertion {
  name: string;
  step: string | null;
  everyRequestSucceeded: boolean | null;
  p95BelowMs: number | null;
  errorRateBelow: number | null;
  statusIn: number[];
}

export interface DeterministicSource {
  kind: "deterministic";
  path: string | null;
  scenarioName: string | null;
  description: string | null;
  steps: Step[];
  assertions: Assertion[];
}

export type LoadSource = ObservedSource | DeterministicSource;

function readStep(v: unknown, depth = 0): Step | null {
  const o = obj(v);
  if (!o) return null;
  const request = str(o.request);
  if (!request) return null;
  return {
    name: str(o.name),
    request,
    thinkMs: num(pick(o, "think_ms", "thinkMs")),
    jitterMs: num(pick(o, "jitter_ms", "jitterMs")),
    afterMs: num(pick(o, "after_ms", "afterMs")),
    // Bounded, because a cycle in the payload would otherwise recurse until
    // the tab dies. The scenario schema nests one level in practice.
    parallel: depth > 3 ? [] : list(o.parallel, (s) => readStep(s, depth + 1)),
  };
}

export function readAssertion(v: unknown): Assertion | null {
  const o = obj(v);
  if (!o) return null;
  const name = str(o.name);
  if (!name) return null;
  return {
    name,
    step: str(o.step),
    everyRequestSucceeded: bool(pick(o, "every_request_succeeded", "everyRequestSucceeded")),
    p95BelowMs: num(pick(o, "p95_below_ms", "p95BelowMs")),
    errorRateBelow: num(pick(o, "error_rate_below", "errorRateBelow")),
    statusIn: list(pick(o, "status_in", "statusIn"), num),
  };
}

export function readSource(kind: SourceKind, v: unknown): LoadSource | null {
  const o = obj(v);
  if (!o) return null;
  if (kind === "observed") {
    return {
      kind: "observed",
      origin: str(o.origin) ?? str(o.source),
      sample: str(o.sample),
      windowStart: str(pick(o, "window_start", "windowStart")),
      windowEnd: str(pick(o, "window_end", "windowEnd")),
      requestsObserved: num(pick(o, "requests_observed", "requestsObserved")),
      routes: list(o.routes, readRoute),
      excluded: list(o.excluded, readRoute),
    };
  }
  return {
    kind: "deterministic",
    path: str(o.path),
    scenarioName: str(pick(o, "scenario", "scenarioName")),
    description: str(o.description),
    steps: list(o.steps, (s) => readStep(s)),
    assertions: list(o.assertions, readAssertion),
  };
}

export interface SourceRow {
  id: string;
  name: string;
  kind: SourceKind;
  repository: string | null;
  version: number | null;
  updatedAt: string | null;
  createdAt: string | null;
  /** Null when the control plane did not count them, which is not none. */
  runCount: number | null;
  lastRunAt: string | null;
  /** The verdict of the most recent run, so a list says which sources are
   *  currently unhappy without opening each one. */
  lastVerdict: Verdict | null;
}

export function readSourceRow(v: unknown): SourceRow | null {
  const o = obj(v);
  if (!o) return null;
  const id = str(o.id);
  const kind = sourceKindOf(o.kind);
  // No kind, no row. Rendering one would mean picking a form for it, and
  // picking is the flattening this module refuses to do.
  if (!id || !kind) return null;
  return {
    id,
    name: str(o.name) ?? id,
    kind,
    repository: str(o.repository),
    version: num(o.version),
    updatedAt: str(pick(o, "updated_at", "updatedAt")),
    createdAt: str(pick(o, "created_at", "createdAt")),
    runCount: num(pick(o, "run_count", "runCount")),
    lastRunAt: str(pick(o, "last_run_at", "lastRunAt")),
    lastVerdict: verdictOf(pick(o, "last_verdict", "lastVerdict")),
  };
}

export interface SourceDetail extends SourceRow {
  source: LoadSource | null;
}

export function readSourceDetail(v: unknown): SourceDetail | null {
  const o = obj(v);
  if (!o) return null;
  const base = readSourceRow(o);
  if (!base) return null;
  return { ...base, source: readSource(base.kind, o.source) };
}

export interface Version {
  id: string;
  version: number | null;
  createdAt: string | null;
  author: string | null;
  note: string | null;
  runCount: number | null;
}

export function readVersion(v: unknown): Version | null {
  const o = obj(v);
  if (!o) return null;
  const id = str(o.id);
  if (!id) return null;
  return {
    id,
    version: num(o.version),
    createdAt: str(pick(o, "created_at", "createdAt")),
    author: str(o.author),
    note: str(o.note),
    runCount: num(pick(o, "run_count", "runCount")),
  };
}

/* -------------------------------------------------------------------------
 * Verdicts and run state
 * ---------------------------------------------------------------------- */

/**
 * The product's four words, and no fifth.
 *
 * `engine/internal/load/scenario.go` declares exactly these and says why: they
 * are what `report.Run.Verdict` and a workflow result already use, and a
 * scenario that invented "regressed" would give a reader a sixth vocabulary to
 * learn for no gain. The console does not get to invent one either.
 */
export type Verdict = "pass" | "fail" | "blocked" | "unverified";

export function verdictOf(v: unknown): Verdict | null {
  return v === "pass" || v === "fail" || v === "blocked" || v === "unverified" ? v : null;
}

/**
 * What a verdict means, and whether it is a judgement at all.
 *
 * `conclusive` is false for the two that say nothing about the software. It is
 * what the run screen keys off to refuse to present a threshold table as a
 * finding.
 */
export const VERDICT_FACTS: Record<
  Verdict,
  { label: string; conclusive: boolean; meaning: string }
> = {
  pass: {
    label: "Pass",
    conclusive: true,
    meaning: "Every assertion was evaluated and every one of them held.",
  },
  fail: {
    label: "Fail",
    conclusive: true,
    meaning: "At least one assertion was evaluated and did not hold.",
  },
  blocked: {
    label: "Blocked",
    conclusive: false,
    meaning:
      "The traffic never reached the application, so nothing here is a judgement about it. Blocked is not a pass.",
  },
  unverified: {
    label: "Unverified",
    conclusive: false,
    meaning:
      "The run finished and its assertions could not be evaluated, so it proved nothing either way. Unverified is not a pass.",
  },
};

/** Where a run is, which is a different question from what it decided. A run
 *  that is still going has no verdict yet, and one that was cancelled never
 *  will have. */
export type RunState = "queued" | "starting" | "running" | "cancelling" | "cancelled" | "finished" | "errored";

const RUN_STATES = new Set<string>([
  "queued",
  "starting",
  "running",
  "cancelling",
  "cancelled",
  "finished",
  "errored",
]);

export function runStateOf(v: unknown): RunState | null {
  return typeof v === "string" && RUN_STATES.has(v) ? (v as RunState) : null;
}

export function isRunning(s: RunState): boolean {
  return s === "queued" || s === "starting" || s === "running" || s === "cancelling";
}

export function isTerminal(s: RunState): boolean {
  return s === "cancelled" || s === "finished" || s === "errored";
}

export const STATE_FACTS: Record<RunState, { label: string; meaning: string }> = {
  queued: { label: "Queued", meaning: "Accepted, waiting for a runner." },
  starting: { label: "Starting", meaning: "The runner has it and is bringing the environment up." },
  running: { label: "Running", meaning: "Sending traffic now." },
  cancelling: {
    label: "Stopping",
    meaning: "Stop requested. Waiting for the runner to acknowledge it.",
  },
  cancelled: {
    label: "Cancelled",
    meaning:
      "Stopped before it finished, so it reached no verdict and anything measured covers only the part that ran.",
  },
  finished: { label: "Finished", meaning: "The run completed and reported a verdict." },
  errored: {
    label: "Errored",
    meaning: "The runner itself failed. This says nothing about the application under test.",
  },
};

/* -------------------------------------------------------------------------
 * Results, as the engine measures them
 * ---------------------------------------------------------------------- */

/**
 * A latency distribution.
 *
 * Exactly the engine's five, and no sixth. The first version of this file had
 * a p75 that `load.Latency` does not produce, which would have rendered a rung
 * on every chart that no measurement backed.
 */
export interface Latency {
  p50Ms: number | null;
  p90Ms: number | null;
  p95Ms: number | null;
  p99Ms: number | null;
  maxMs: number | null;
}

export function readLatency(v: unknown): Latency {
  const o = obj(v) ?? {};
  return {
    p50Ms: num(pick(o, "p50_ms", "p50Ms")),
    p90Ms: num(pick(o, "p90_ms", "p90Ms")),
    p95Ms: num(pick(o, "p95_ms", "p95Ms")),
    p99Ms: num(pick(o, "p99_ms", "p99Ms")),
    maxMs: num(pick(o, "max_ms", "maxMs")),
  };
}

/** The distribution as an ordered list, dropping what was not measured. An
 *  unrecorded p99 is absent from a chart rather than drawn at zero. */
export function percentiles(l: Latency): { label: string; ms: number }[] {
  return (
    [
      ["p50", l.p50Ms],
      ["p90", l.p90Ms],
      ["p95", l.p95Ms],
      ["p99", l.p99Ms],
      ["max", l.maxMs],
    ] as const
  )
    .filter(([, ms]) => ms !== null)
    .map(([label, ms]) => ({ label, ms: ms as number }));
}

/**
 * One route's measurement.
 *
 * `hasBaseline` is carried rather than inferred, and that is the whole reason
 * this interface is not simpler. `p95Increase` is a RATIO whose zero value is
 * also a legitimate reading, so "no baseline" and "no change" are the same
 * number and only the flag tells them apart. The engine added the flag for
 * this reason and the console keeps it for the same one.
 */
export interface RouteResult {
  route: string;
  sent: number | null;
  errors: number | null;
  latency: Latency;
  baselineP95Ms: number | null;
  /** A ratio. 0.31 is thirty one percent slower than the baseline. */
  p95Increase: number | null;
  hasBaseline: boolean;
}

export function readRouteResult(v: unknown): RouteResult | null {
  const o = obj(v);
  if (!o) return null;
  const route = str(o.route);
  if (!route) return null;
  return {
    route,
    sent: num(o.sent),
    errors: num(o.errors),
    latency: readLatency(o.latency),
    baselineP95Ms: num(pick(o, "baseline_p95_ms", "baselineP95Ms")),
    p95Increase: num(pick(o, "p95_increase", "p95Increase")),
    hasBaseline: bool(pick(o, "has_baseline", "hasBaseline")) ?? false,
  };
}

/** The comparison for one route, or null when there is nothing to compare
 *  against. Never zero for absent. */
export function increase(r: RouteResult): number | null {
  return r.hasBaseline ? (r.p95Increase ?? 0) : null;
}

/** One threshold the run exceeded, in the engine's own shape. */
export interface Breach {
  what: string;
  limit: number | null;
  measured: number | null;
  detail: string | null;
}

export function readBreach(v: unknown): Breach | null {
  const o = obj(v);
  if (!o) return null;
  const what = str(o.what);
  if (!what) return null;
  return { what, limit: num(o.limit), measured: num(o.measured), detail: str(o.detail) };
}

/**
 * Errors by reason.
 *
 * The engine's `classify()` produces a closed set of six, and the reason is the
 * only part of an error count that tells somebody what to do. A thousand
 * timeouts and a thousand connection refusals are the same number and
 * completely different problems.
 */
export const ERROR_REASONS = [
  "timeout",
  "connection refused",
  "connection reset",
  "name not resolved",
  "malformed request",
  "request failed",
] as const;

/** What each reason usually means, so a reader is not left with a bare string.
 *  Unknown reasons are rendered without a note rather than guessed at. */
export const REASON_NOTES: Record<string, string> = {
  timeout: "The application did not answer inside the request deadline.",
  "connection refused": "Nothing was listening. Usually the service is not up yet.",
  "connection reset": "The connection was closed mid-request, often a crash or a restart.",
  "name not resolved": "DNS did not answer for the host, which under a deny-all egress policy is what a blocked host looks like.",
  "malformed request": "The request could not be built. This is the scenario or the mix, not the application.",
  "request failed": "A transport error the runner could not classify further.",
};

export function readErrors(v: unknown): { reason: string; count: number }[] {
  const o = obj(v);
  if (!o) return [];
  const out: { reason: string; count: number }[] = [];
  for (const [reason, raw] of Object.entries(o)) {
    const count = num(raw);
    if (count !== null && count > 0) out.push({ reason, count });
  }
  // Commonest first. The order is the finding: it says which failure to chase.
  return out.sort((a, b) => b.count - a.count);
}

export interface Results {
  /** True while these numbers cover only what has landed so far. */
  partial: boolean;
  /** Where the mix came from, as the run recorded it. */
  origin: string | null;
  /** What the run aimed for. */
  targetRate: number | null;
  /** What it achieved. The gap between this and the target is the first thing
   *  worth looking at: reporting the target instead is how a load test says
   *  everything was fine while the queue grew. */
  rate: number | null;
  sent: number | null;
  durationSeconds: number | null;
  errorRate: number | null;
  errors: { reason: string; count: number }[];
  overall: Latency;
  routes: RouteResult[];
  breaches: Breach[];
  assertions: AssertionResult[];
  evidence: Evidence[];
  /** The command that reproduces this run, as the control plane recorded it at
   *  dispatch. Never assembled here: a command the console rebuilds drifts
   *  from the one that ran, and being the same one is the point. */
  command: string | null;
}

/** An assertion and what became of it. Three outcomes, because one nothing
 *  evaluated has not held. */
export interface AssertionResult {
  name: string;
  step: string | null;
  outcome: "held" | "broke" | "not_evaluated";
  detail: string | null;
}

export function readAssertionResult(v: unknown): AssertionResult | null {
  const o = obj(v);
  if (!o) return null;
  const name = str(o.name);
  const outcome = o.outcome;
  if (!name) return null;
  if (outcome !== "held" && outcome !== "broke" && outcome !== "not_evaluated") return null;
  return { name, step: str(o.step), outcome, detail: str(o.detail) };
}

export interface Evidence {
  id: string;
  kind: string;
  label: string | null;
  href: string | null;
  retained: boolean | null;
}

export function readEvidence(v: unknown): Evidence | null {
  const o = obj(v);
  if (!o) return null;
  const id = str(o.id);
  const kind = str(o.kind);
  if (!id || !kind) return null;
  return { id, kind, label: str(o.label), href: str(o.href), retained: bool(o.retained) };
}

export function readResults(v: unknown): Results | null {
  const o = obj(v);
  if (!o) return null;
  return {
    partial: bool(o.partial) ?? false,
    origin: str(o.origin) ?? str(o.source),
    targetRate: num(pick(o, "target_rate", "targetRate")),
    rate: num(o.rate),
    sent: num(o.sent),
    durationSeconds: num(pick(o, "duration_seconds", "durationSeconds")),
    errorRate: num(pick(o, "error_rate", "errorRate")),
    errors: readErrors(o.errors),
    overall: readLatency(o.overall),
    routes: list(o.routes, readRouteResult),
    breaches: list(o.breaches, readBreach),
    assertions: list(o.assertions, readAssertionResult),
    evidence: list(o.evidence, readEvidence),
    command: str(o.command),
  };
}

/** How far short of the target the run fell, as a fraction, or null when
 *  either number is missing. Positive means it did not keep up. */
export function rateShortfall(r: Results): number | null {
  if (r.targetRate === null || r.rate === null || r.targetRate <= 0) return null;
  return (r.targetRate - r.rate) / r.targetRate;
}

/* -------------------------------------------------------------------------
 * Runs
 * ---------------------------------------------------------------------- */

export interface RunRow {
  id: string;
  sourceId: string | null;
  sourceName: string | null;
  kind: SourceKind | null;
  state: RunState;
  /** Null until the run reaches one, and null forever on a cancelled run. A
   *  run that is still going has no verdict, and inventing one for the list
   *  would be the console's own version of a green run over nothing. */
  verdict: Verdict | null;
  envId: string | null;
  startedAt: string | null;
  finishedAt: string | null;
  createdAt: string | null;
}

export function readRun(v: unknown): RunRow | null {
  const o = obj(v);
  if (!o) return null;
  const id = str(o.id);
  const state = runStateOf(o.state);
  // No state, no row. Rendering a run whose state the console does not know
  // means choosing a tone for it, and a wrong choice there paints something
  // inconclusive as a pass.
  if (!id || !state) return null;
  return {
    id,
    sourceId: str(pick(o, "source_id", "sourceId")),
    sourceName: str(pick(o, "source_name", "sourceName")),
    kind: sourceKindOf(o.kind),
    state,
    verdict: verdictOf(o.verdict),
    envId: str(pick(o, "env_id", "envId")),
    startedAt: str(pick(o, "started_at", "startedAt")),
    finishedAt: str(pick(o, "finished_at", "finishedAt")),
    createdAt: str(pick(o, "created_at", "createdAt")),
  };
}

export interface RunDetail extends RunRow {
  results: Results | null;
  /** Why it stopped, when the state does not say it. */
  detail: string | null;
  scale: number | null;
  durationSeconds: number | null;
  concurrency: number | null;
  version: number | null;
  safe: string[];
  unsafe: string[];
}

export function readRunDetail(v: unknown): RunDetail | null {
  const o = obj(v);
  if (!o) return null;
  const base = readRun(o);
  if (!base) return null;
  return {
    ...base,
    results: readResults(o.results),
    detail: str(o.detail),
    scale: num(o.scale),
    durationSeconds: num(pick(o, "duration_seconds", "durationSeconds")),
    concurrency: num(o.concurrency),
    version: num(o.version),
    safe: list(o.safe, str),
    unsafe: list(o.unsafe, str),
  };
}

/**
 * A run whose verdict disagrees with its own assertions.
 *
 * This exists because of the specific way this product has been wrong before:
 * a nightly corpus reported six passing workflows having never once reached an
 * agent, and every summary anybody read said green. The console cannot correct
 * a verdict the engine computed, but it can refuse to present a contradiction
 * quietly.
 *
 * Only a conclusive verdict is checked. Flagging a blocked run for having
 * unevaluated assertions would be restating its own definition at a reader.
 */
export function verdictContradiction(
  verdict: Verdict | null,
  assertions: AssertionResult[],
): string | null {
  if (verdict === null || !VERDICT_FACTS[verdict].conclusive) return null;
  const broke = assertions.filter((a) => a.outcome === "broke").length;
  const unevaluated = assertions.filter((a) => a.outcome === "not_evaluated").length;

  if (verdict === "pass" && (broke > 0 || unevaluated > 0)) {
    // Both halves when both apply. Naming only the broken ones would leave a
    // reader believing the rest held, which is the quieter half of the same
    // mistake: an assertion nothing measured has not passed either.
    const parts: string[] = [];
    if (broke > 0) {
      parts.push(broke === 1 ? "one assertion below broke" : `${broke} of the assertions below broke`);
    }
    if (unevaluated > 0) {
      parts.push(unevaluated === 1 ? "one was never evaluated" : `${unevaluated} were never evaluated`);
    }
    const tail =
      broke > 0
        ? "A run cannot pass over an assertion that broke, so one of the two records is wrong."
        : "An assertion nothing measured has not held, so this pass covers less than it appears to.";
    return `This run is recorded as a pass, but ${parts.join(" and ")}. ${tail}`;
  }
  if (verdict === "fail" && broke === 0) {
    return "This run is recorded as a failure, but no assertion below broke. Whatever failed it is not in this table.";
  }
  return null;
}

/* -------------------------------------------------------------------------
 * Exploration
 *
 * Kept apart from the two load sources above, because it is not one. `af
 * explore` drives a real browser from a seed and compiles what it reached into
 * a manifest WORKFLOW, which `af test` runs. It never produces a load
 * scenario. Modelling it beside observed and deterministic would put that
 * confusion in the type system, where every screen would inherit it.
 * ---------------------------------------------------------------------- */

export interface Discovery {
  id: string;
  /** What the agent reached, in the manifest's own words. */
  title: string;
  persona: string | null;
  seed: string | null;
  reachedAt: string | null;
  steps: number | null;
  /** The workflow name it became, once promoted. Not a scenario id. */
  workflowName: string | null;
}

export function readDiscovery(v: unknown): Discovery | null {
  const o = obj(v);
  if (!o) return null;
  const id = str(o.id);
  const title = str(o.title) ?? str(o.name);
  if (!id || !title) return null;
  return {
    id,
    title,
    persona: str(o.persona),
    seed: str(o.seed),
    reachedAt: str(pick(o, "reached_at", "reachedAt")),
    steps: num(o.steps),
    workflowName: str(pick(o, "workflow_name", "workflowName")),
  };
}

export interface Exploration {
  id: string;
  entryUrl: string | null;
  seed: string | null;
  budgetSeconds: number | null;
  startedAt: string | null;
  discoveries: Discovery[];
}

export function readExploration(v: unknown): Exploration | null {
  const o = obj(v);
  if (!o) return null;
  const id = str(o.id);
  if (!id) return null;
  return {
    id,
    entryUrl: str(pick(o, "entry_url", "entryUrl")),
    seed: str(o.seed),
    budgetSeconds: num(pick(o, "budget_seconds", "budgetSeconds")),
    startedAt: str(pick(o, "started_at", "startedAt")),
    discoveries: list(o.discoveries, readDiscovery),
  };
}

/* -------------------------------------------------------------------------
 * The calls
 * ---------------------------------------------------------------------- */

interface Page<T> {
  items: T[];
  nextCursor: string | null;
}

function readPage<T>(v: unknown, key: string, each: (item: unknown) => T | null): Page<T> {
  const o = obj(v);
  return { items: list(o?.[key], each), nextCursor: str(o?.nextCursor) };
}

export async function listSources(input: {
  kind?: SourceKind;
  repository?: string;
  limit?: number;
  cursor?: string;
}): Promise<Page<SourceRow>> {
  return readPage(
    await query<unknown>("load.sources.list", { limit: 50, ...input }),
    "sources",
    readSourceRow,
  );
}

export async function getSource(id: string): Promise<SourceDetail | null> {
  return readSourceDetail(await query<unknown>("load.sources.get", { id }));
}

export async function listVersions(id: string): Promise<Version[]> {
  return readPage(
    await query<unknown>("load.sources.versions", { id, limit: 50 }),
    "versions",
    readVersion,
  ).items;
}

export async function listRuns(input: {
  sourceId?: string;
  limit?: number;
  cursor?: string;
}): Promise<Page<RunRow>> {
  return readPage(await query<unknown>("load.runs.list", { limit: 50, ...input }), "runs", readRun);
}

export async function getRun(runId: string): Promise<RunDetail | null> {
  return readRunDetail(await query<unknown>("load.runs.get", { runId }));
}

export interface StartInput {
  sourceId: string;
  envId: string;
  scale?: number;
  durationSeconds?: number;
  concurrency?: number;
  safe?: string[];
  unsafe?: string[];
}

export async function startRun(input: StartInput, csrf: string): Promise<{ runId: string | null }> {
  const raw = await mutate<unknown>("load.runs.start", input, csrf);
  return { runId: str(obj(raw)?.runId) };
}

export async function cancelRun(runId: string, csrf: string): Promise<void> {
  await mutate("load.runs.cancel", { runId }, csrf);
}

export async function retryRun(runId: string, csrf: string): Promise<{ runId: string | null }> {
  const raw = await mutate<unknown>("load.runs.retry", { runId }, csrf);
  return { runId: str(obj(raw)?.runId) };
}

export async function listExplorations(input: { limit?: number } = {}): Promise<Exploration[]> {
  return readPage(
    await query<unknown>("load.explorations.list", { limit: 20, ...input }),
    "explorations",
    readExploration,
  ).items;
}

/** Compile a discovery into a manifest workflow. The result is YAML for
 *  `antifailure.yaml`, which is what `af explore --emit-workflow` prints. It is
 *  not a load scenario and this function does not pretend otherwise. */
export async function promoteDiscovery(
  input: { explorationId: string; discoveryId: string; name: string },
  csrf: string,
): Promise<{ workflowName: string | null; yaml: string | null }> {
  const raw = await mutate<unknown>("load.explorations.promote", input, csrf);
  const o = obj(raw);
  return { workflowName: str(pick(o ?? {}, "workflow_name", "workflowName")), yaml: str(o?.yaml) };
}

/* -------------------------------------------------------------------------
 * Formatting
 * ---------------------------------------------------------------------- */

/** Milliseconds, at a precision that does not imply more than was measured. */
export function ms(value: number | null): string {
  if (value === null) return "--";
  if (value < 1) return `${value.toFixed(2)} ms`;
  if (value < 10) return `${value.toFixed(1)} ms`;
  if (value < 1000) return `${Math.round(value)} ms`;
  return `${(value / 1000).toFixed(2)} s`;
}

/** A rate, at a precision that scales with the number. No unit: every caller
 *  puts this in a tile whose label already carries one. */
export function rate(value: number | null): string {
  if (value === null) return "--";
  if (value >= 100) return Math.round(value).toLocaleString();
  if (value >= 10) return value.toFixed(1);
  return value.toFixed(2);
}

export function count(value: number | null): string {
  return value === null ? "--" : value.toLocaleString();
}

/** A fraction as a percentage. Two figures under one percent, because an error
 *  rate of 0.4% rounds to "0%" and reads as none. */
export function percent(fraction: number | null): string {
  if (fraction === null) return "--";
  const p = fraction * 100;
  if (p > 0 && p < 1) return `${p.toFixed(2)}%`;
  if (p < 10) return `${p.toFixed(1)}%`;
  return `${Math.round(p)}%`;
}

export function seconds(value: number | null): string {
  if (value === null) return "--";
  if (value < 60) return `${Math.round(value)}s`;
  const m = Math.floor(value / 60);
  const s = Math.round(value % 60);
  return s === 0 ? `${m}m` : `${m}m ${s}s`;
}
