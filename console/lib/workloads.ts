/**
 * The Workload Studio's data boundary.
 *
 * Every `workloads.*` path the console calls is named in this file and nowhere
 * else, so the contract with the control plane is one file to reconcile rather
 * than a grep across a dozen components.
 *
 * Two rules run through all of it, and both were paid for elsewhere in this
 * repository.
 *
 * The first is that a workload's three sources are not one thing. Observed,
 * deterministic and exploratory traffic differ in what produced them, in what
 * they prove, and in whether they can be reproduced at all. A common
 * intermediate representation would let the console show one form for all
 * three, and the form would be lying about two of them. So `kind` is a
 * discriminant on a union here, the per-kind payloads share no fields by
 * accident, and each is read by its own decoder.
 *
 * The second is that a number the control plane did not record must never
 * arrive here as a zero. `af test` exits 0 on `unverified` and an entire
 * nightly corpus once went green having never reached an agent; the console
 * version of that mistake is a stat tile reading "0 ms" for a percentile that
 * was never measured. So every reader below returns `null` for absent and the
 * components render absence as absence.
 */

import { query, mutate } from "@/lib/api";

/* -------------------------------------------------------------------------
 * Reading foreign data
 *
 * Tolerant on the way in, strict on the way out. These take `unknown` because
 * that is honestly what a JSON body is, and they answer `null` rather than
 * throwing, because one surprising field must never discard the object that
 * contains it. A whole run's results disappearing because one route carried a
 * string where a number was expected is the failure this shape prevents.
 * ---------------------------------------------------------------------- */

export function str(v: unknown): string | null {
  return typeof v === "string" && v.length > 0 ? v : null;
}

/** A number, or null. Accepts the string form, because a driver hands back
 *  bigint and numeric columns as strings and `Number("")` is 0, which is the
 *  exact coercion this file exists to prevent. */
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

/** An array, with each element decoded on its own and the failures dropped
 *  rather than the collection. */
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

/* -------------------------------------------------------------------------
 * What produced the traffic
 * ---------------------------------------------------------------------- */

export type Kind = "observed" | "deterministic" | "exploratory";

export const KINDS: readonly Kind[] = ["observed", "deterministic", "exploratory"];

export function kindOf(v: unknown): Kind | null {
  return v === "observed" || v === "deterministic" || v === "exploratory" ? v : null;
}

/**
 * How each kind reads to a person, and what it is honestly worth.
 *
 * `reproducible` is the field that earns this table. It is the difference
 * between a result you can hand to somebody else and one you can only ask them
 * to believe, and it is not the same answer for the three kinds. Putting it
 * beside the name means the console cannot describe a seeded exploration as
 * though it were a pinned scenario.
 */
export const KIND_FACTS: Record<
  Kind,
  { noun: string; provenance: string; what: string; reproducible: string }
> = {
  observed: {
    noun: "Observed",
    provenance: "Measured",
    what: "Weighted from a window of production's own traffic: an OTLP export or a combined access log, compiled to a route mix and a rate.",
    reproducible:
      "As a shape. The mix and the rate replay exactly; the individual requests they were derived from do not.",
  },
  deterministic: {
    noun: "Deterministic",
    provenance: "Authored",
    what: "A scenario somebody wrote and committed, pinned to a version. The routes, the order and the weights are all stated rather than inferred.",
    reproducible: "Exactly. The same version sends the same requests in the same order.",
  },
  exploratory: {
    noun: "Exploratory",
    provenance: "Discovered",
    what: "An agent driving a real browser from a seed, choosing its own way through the product and recording what it reached.",
    reproducible:
      "Only against the same seed and the same build. What it finds is a candidate to promote, not a fact to rely on.",
  },
};

/* -------------------------------------------------------------------------
 * Definitions
 * ---------------------------------------------------------------------- */

/** The fields every definition has regardless of where its traffic came from.
 *  Deliberately small: everything that differs lives on `source`. */
export interface DefinitionRow {
  id: string;
  name: string;
  kind: Kind;
  repository: string | null;
  version: number | null;
  updated_at: string | null;
  created_at: string | null;
  /** Runs recorded against this definition. Null when the control plane did
   *  not count them, which is not the same as none. */
  run_count: number | null;
  last_run_at: string | null;
}

export function readDefinition(v: unknown): DefinitionRow | null {
  const o = obj(v);
  if (!o) return null;
  const id = str(o.id);
  const kind = kindOf(o.kind);
  // An identity and a kind are the two things a row cannot be rendered
  // without: with no kind the console would have to pick a form, and picking
  // one is exactly the flattening this file refuses to do.
  if (!id || !kind) return null;
  return {
    id,
    name: str(o.name) ?? id,
    kind,
    repository: str(o.repository),
    version: num(o.version),
    updated_at: str(o.updated_at),
    created_at: str(o.created_at),
    run_count: num(o.run_count),
    last_run_at: str(o.last_run_at),
  };
}

/** Where an observed workload's shape came from, and what it compiled to. */
export interface ObservedSource {
  kind: "observed";
  /** `otlp` or `combined`, as the engine names them. */
  format: string | null;
  sampleName: string | null;
  windowStart: string | null;
  windowEnd: string | null;
  requestsObserved: number | null;
  /** The compiled mix. Each route carries the share of traffic it took. */
  routes: { route: string; method: string | null; share: number | null; rps: number | null }[];
}

export interface DeterministicSource {
  kind: "deterministic";
  scenarioPath: string | null;
  scenarioVersion: string | null;
  steps: { name: string; route: string; method: string | null; weight: number | null }[];
}

export interface ExploratorySource {
  kind: "exploratory";
  seed: string | null;
  entryUrl: string | null;
  budgetSeconds: number | null;
  maxSteps: number | null;
  /** What the agent reached. These are the rows a person promotes from. */
  discoveries: {
    id: string;
    route: string;
    method: string | null;
    reachedAt: string | null;
    /** Already turned into a deterministic definition, and which one. */
    promotedTo: string | null;
  }[];
}

export type Source = ObservedSource | DeterministicSource | ExploratorySource;

function readObserved(o: Record<string, unknown>): ObservedSource {
  return {
    kind: "observed",
    format: str(o.format),
    sampleName: str(o.sample_name) ?? str(o.sampleName),
    windowStart: str(o.window_start) ?? str(o.windowStart),
    windowEnd: str(o.window_end) ?? str(o.windowEnd),
    requestsObserved: num(o.requests_observed) ?? num(o.requestsObserved),
    routes: list(o.routes, (r) => {
      const row = obj(r);
      const route = row && str(row.route);
      if (!row || !route) return null;
      return {
        route,
        method: str(row.method),
        share: num(row.share),
        rps: num(row.rps),
      };
    }),
  };
}

function readDeterministic(o: Record<string, unknown>): DeterministicSource {
  return {
    kind: "deterministic",
    scenarioPath: str(o.scenario_path) ?? str(o.scenarioPath),
    scenarioVersion: str(o.scenario_version) ?? str(o.scenarioVersion),
    steps: list(o.steps, (s) => {
      const row = obj(s);
      const route = row && str(row.route);
      if (!row || !route) return null;
      return {
        name: str(row.name) ?? route,
        route,
        method: str(row.method),
        weight: num(row.weight),
      };
    }),
  };
}

function readExploratory(o: Record<string, unknown>): ExploratorySource {
  return {
    kind: "exploratory",
    seed: str(o.seed),
    entryUrl: str(o.entry_url) ?? str(o.entryUrl),
    budgetSeconds: num(o.budget_seconds) ?? num(o.budgetSeconds),
    maxSteps: num(o.max_steps) ?? num(o.maxSteps),
    discoveries: list(o.discoveries, (d) => {
      const row = obj(d);
      const id = row && str(row.id);
      const route = row && str(row.route);
      if (!row || !id || !route) return null;
      return {
        id,
        route,
        method: str(row.method),
        reachedAt: str(row.reached_at) ?? str(row.reachedAt),
        promotedTo: str(row.promoted_to) ?? str(row.promotedTo),
      };
    }),
  };
}

/**
 * The source, read by the decoder its kind names.
 *
 * Three decoders rather than one lenient reader. A single reader would have to
 * accept every field as optional, and the first consequence would be a
 * component asking an exploratory definition for its scenario version and
 * rendering the blank without anybody noticing.
 */
export function readSource(kind: Kind, v: unknown): Source | null {
  const o = obj(v);
  if (!o) return null;
  if (kind === "observed") return readObserved(o);
  if (kind === "deterministic") return readDeterministic(o);
  return readExploratory(o);
}

export interface Definition extends DefinitionRow {
  source: Source | null;
  description: string | null;
}

export function readDefinitionDetail(v: unknown): Definition | null {
  const o = obj(v);
  if (!o) return null;
  const base = readDefinition(o);
  if (!base) return null;
  return {
    ...base,
    description: str(o.description),
    source: readSource(base.kind, o.source),
  };
}

export interface Version {
  id: string;
  version: number | null;
  created_at: string | null;
  author: string | null;
  note: string | null;
  /** The version a run used, so history can say which one produced a result. */
  run_count: number | null;
}

export function readVersion(v: unknown): Version | null {
  const o = obj(v);
  if (!o) return null;
  const id = str(o.id);
  if (!id) return null;
  return {
    id,
    version: num(o.version),
    created_at: str(o.created_at),
    author: str(o.author),
    note: str(o.note),
    run_count: num(o.run_count),
  };
}

/* -------------------------------------------------------------------------
 * Runs
 * ---------------------------------------------------------------------- */

/**
 * Every state a run can be in, kept as its own value.
 *
 * `blocked` and `unverified` are the two that matter and the two a weaker
 * model would fold away. A run that never reached the product is not a run
 * that passed, and a run whose expectation could not be evaluated is not a run
 * that failed. Both have their own value here, both get their own tone, and
 * neither is ever rendered as a pass. This is the console's half of standard
 * 25: an exit code is a claim, and a green run over nothing is a lie.
 */
export type RunStatus =
  | "queued"
  | "starting"
  | "running"
  | "cancelling"
  | "cancelled"
  | "passed"
  | "failed"
  | "blocked"
  | "unverified"
  | "errored";

const RUN_STATUSES = new Set<string>([
  "queued",
  "starting",
  "running",
  "cancelling",
  "cancelled",
  "passed",
  "failed",
  "blocked",
  "unverified",
  "errored",
]);

export function readStatus(v: unknown): RunStatus | null {
  return typeof v === "string" && RUN_STATUSES.has(v) ? (v as RunStatus) : null;
}

export function isTerminal(s: RunStatus): boolean {
  return (
    s === "cancelled" ||
    s === "passed" ||
    s === "failed" ||
    s === "blocked" ||
    s === "unverified" ||
    s === "errored"
  );
}

export function isRunning(s: RunStatus): boolean {
  return s === "queued" || s === "starting" || s === "running" || s === "cancelling";
}

/**
 * What a status means, in words, and how firm the answer is.
 *
 * `conclusive` is false for exactly the outcomes that are not a verdict about
 * the software. It is what the run detail keys off to refuse to draw a
 * threshold table that would read as a judgement.
 */
export const STATUS_FACTS: Record<
  RunStatus,
  { label: string; conclusive: boolean; meaning: string }
> = {
  queued: { label: "Queued", conclusive: false, meaning: "Accepted, waiting for a runner." },
  starting: {
    label: "Starting",
    conclusive: false,
    meaning: "The runner has it and is bringing the environment up.",
  },
  running: { label: "Running", conclusive: false, meaning: "Sending traffic now." },
  cancelling: {
    label: "Cancelling",
    conclusive: false,
    meaning: "Stop requested. Waiting for the runner to acknowledge it.",
  },
  cancelled: {
    label: "Cancelled",
    conclusive: false,
    meaning: "Stopped before it finished. Any numbers below cover only the part that ran.",
  },
  passed: {
    label: "Passed",
    conclusive: true,
    meaning: "Every threshold was evaluated and every one of them held.",
  },
  failed: {
    label: "Failed",
    conclusive: true,
    meaning: "At least one threshold was evaluated and did not hold.",
  },
  blocked: {
    label: "Blocked",
    conclusive: false,
    meaning:
      "The traffic never reached the product, so nothing here is a judgement about it. Blocked is not a pass.",
  },
  unverified: {
    label: "Unverified",
    conclusive: false,
    meaning:
      "The run finished but its thresholds could not be evaluated, so it proved nothing either way. Unverified is not a pass.",
  },
  errored: {
    label: "Errored",
    conclusive: false,
    meaning: "The runner itself failed. This says nothing about the software under test.",
  },
};

export interface RunRow {
  id: string;
  definition_id: string | null;
  definition_name: string | null;
  kind: Kind | null;
  status: RunStatus;
  execution: "baseline" | "candidate" | null;
  started_at: string | null;
  finished_at: string | null;
  created_at: string | null;
  env_id: string | null;
}

function readExecution(v: unknown): "baseline" | "candidate" | null {
  return v === "baseline" || v === "candidate" ? v : null;
}

export function readRun(v: unknown): RunRow | null {
  const o = obj(v);
  if (!o) return null;
  const id = str(o.id);
  const status = readStatus(o.status);
  // No status, no row. Rendering a run whose status the console does not
  // recognise means picking a tone for it, and every wrong guess in that
  // position paints something inconclusive as a pass.
  if (!id || !status) return null;
  return {
    id,
    definition_id: str(o.definition_id),
    definition_name: str(o.definition_name),
    kind: kindOf(o.kind),
    status,
    execution: readExecution(o.execution),
    started_at: str(o.started_at),
    finished_at: str(o.finished_at),
    created_at: str(o.created_at),
    env_id: str(o.env_id),
  };
}

/* -------------------------------------------------------------------------
 * Results
 * ---------------------------------------------------------------------- */

/** One latency percentile, in milliseconds. `value` is null when the run did
 *  not record that percentile, which the ladder draws as a gap. */
export interface Percentile {
  label: string;
  ms: number | null;
}

export interface Throughput {
  requests: number | null;
  rps: number | null;
  errors: number | null;
  /** A fraction between 0 and 1, not a percentage. The component does the
   *  multiplication so that no caller can disagree about which it received. */
  errorRate: number | null;
  durationSeconds: number | null;
}

export interface Threshold {
  id: string;
  name: string;
  /** `held`, `broke`, or `not_evaluated`. The third is why this is not a
   *  boolean: a threshold nothing measured has not passed. */
  outcome: "held" | "broke" | "not_evaluated";
  target: string | null;
  actual: string | null;
}

function readOutcome(v: unknown): Threshold["outcome"] | null {
  return v === "held" || v === "broke" || v === "not_evaluated" ? v : null;
}

export function readThreshold(v: unknown): Threshold | null {
  const o = obj(v);
  if (!o) return null;
  const id = str(o.id);
  const outcome = readOutcome(o.outcome);
  if (!id || !outcome) return null;
  return {
    id,
    name: str(o.name) ?? id,
    outcome,
    target: str(o.target),
    actual: str(o.actual),
  };
}

/**
 * One route, measured on both sides of the comparison.
 *
 * `baselineMs` and `candidateMs` are kept rather than a precomputed delta, so
 * that a route measured on only one side is visibly one-sided instead of
 * arriving as a delta of zero. A route the candidate added has no baseline and
 * that is information, not a missing value to fill in.
 */
export interface RouteDelta {
  route: string;
  method: string | null;
  baselineMs: number | null;
  candidateMs: number | null;
  baselineErrors: number | null;
  candidateErrors: number | null;
  requests: number | null;
}

export function readRouteDelta(v: unknown): RouteDelta | null {
  const o = obj(v);
  if (!o) return null;
  const route = str(o.route);
  if (!route) return null;
  return {
    route,
    method: str(o.method),
    baselineMs: num(o.baseline_ms) ?? num(o.baselineMs),
    candidateMs: num(o.candidate_ms) ?? num(o.candidateMs),
    baselineErrors: num(o.baseline_errors) ?? num(o.baselineErrors),
    candidateErrors: num(o.candidate_errors) ?? num(o.candidateErrors),
    requests: num(o.requests),
  };
}

/** Signed change, or null when either side is missing. Never zero for absent:
 *  a route with no baseline is not a route that did not move. */
export function deltaMs(d: RouteDelta): number | null {
  if (d.baselineMs === null || d.candidateMs === null) return null;
  return d.candidateMs - d.baselineMs;
}

/** The change as a fraction of the baseline. Null when there is no baseline to
 *  be a fraction of, and null at a zero baseline rather than Infinity. */
export function deltaFraction(d: RouteDelta): number | null {
  const abs = deltaMs(d);
  if (abs === null || d.baselineMs === null || d.baselineMs === 0) return null;
  return abs / d.baselineMs;
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
  return {
    id,
    kind,
    label: str(o.label),
    href: str(o.href),
    retained: bool(o.retained),
  };
}

export interface Results {
  /** True while the run is still going and these numbers cover only what has
   *  landed so far. The run detail says so rather than presenting a partial
   *  measurement as a final one. */
  partial: boolean;
  throughput: Throughput;
  percentiles: Percentile[];
  thresholds: Threshold[];
  routes: RouteDelta[];
  evidence: Evidence[];
  /** The command that reproduces this run, as the control plane recorded it at
   *  dispatch. Never assembled here: a command the console rebuilds from the
   *  form can drift from the one that actually ran, and the whole value of
   *  printing it is that it is the same one. */
  command: string | null;
}

const PERCENTILE_KEYS: { key: string; label: string }[] = [
  { key: "p50", label: "p50" },
  { key: "p75", label: "p75" },
  { key: "p90", label: "p90" },
  { key: "p95", label: "p95" },
  { key: "p99", label: "p99" },
  { key: "max", label: "max" },
];

export function readResults(v: unknown): Results | null {
  const o = obj(v);
  if (!o) return null;
  const t = obj(o.throughput) ?? {};
  const p = obj(o.percentiles) ?? {};
  return {
    partial: bool(o.partial) ?? false,
    throughput: {
      requests: num(t.requests),
      rps: num(t.rps),
      errors: num(t.errors),
      errorRate: num(t.error_rate) ?? num(t.errorRate),
      durationSeconds: num(t.duration_seconds) ?? num(t.durationSeconds),
    },
    // Only the percentiles the run actually reported. A fixed set with nulls
    // filled in would draw five empty rungs for a run that recorded two, which
    // reads as five measurements that came back empty.
    percentiles: PERCENTILE_KEYS.map(({ key, label }) => ({ label, ms: num(p[key]) })).filter(
      (x) => x.ms !== null,
    ),
    thresholds: list(o.thresholds, readThreshold),
    routes: list(o.routes, readRouteDelta),
    evidence: list(o.evidence, readEvidence),
    command: str(o.command),
  };
}

export interface RunDetail extends RunRow {
  results: Results | null;
  /** Why it stopped, when that is not obvious from the status. A cancellation
   *  reason, a runner error, or what blocked it. */
  detail: string | null;
  scale: number | null;
  durationSeconds: number | null;
  concurrency: number | null;
  version: number | null;
  safeRoutes: string[];
  unsafeRoutes: string[];
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
    durationSeconds: num(o.duration_seconds) ?? num(o.durationSeconds),
    concurrency: num(o.concurrency),
    version: num(o.version),
    safeRoutes: list(o.safe_routes ?? o.safeRoutes, str),
    unsafeRoutes: list(o.unsafe_routes ?? o.unsafeRoutes, str),
  };
}

/* -------------------------------------------------------------------------
 * The calls
 *
 * Thin on purpose. Each one names its path, decodes what comes back, and does
 * nothing else, so that a change to the control plane's shape is a change to
 * one function.
 * ---------------------------------------------------------------------- */

interface Page<T> {
  items: T[];
  nextCursor: string | null;
}

function readPage<T>(v: unknown, key: string, each: (item: unknown) => T | null): Page<T> {
  const o = obj(v);
  return {
    items: list(o?.[key], each),
    nextCursor: str(o?.nextCursor),
  };
}

export async function listDefinitions(input: {
  kind?: Kind;
  repository?: string;
  limit?: number;
  cursor?: string;
}): Promise<Page<DefinitionRow>> {
  const raw = await query<unknown>("workloads.definitions.list", { limit: 50, ...input });
  return readPage(raw, "definitions", readDefinition);
}

export async function getDefinition(id: string): Promise<Definition | null> {
  return readDefinitionDetail(await query<unknown>("workloads.definitions.get", { id }));
}

export async function listVersions(id: string): Promise<Version[]> {
  const raw = await query<unknown>("workloads.definitions.versions", { id, limit: 50 });
  return readPage(raw, "versions", readVersion).items;
}

export async function listRuns(input: {
  definitionId?: string;
  status?: RunStatus;
  limit?: number;
  cursor?: string;
}): Promise<Page<RunRow>> {
  const raw = await query<unknown>("workloads.runs.list", { limit: 50, ...input });
  return readPage(raw, "runs", readRun);
}

export async function getRun(runId: string): Promise<RunDetail | null> {
  return readRunDetail(await query<unknown>("workloads.runs.get", { runId }));
}

export interface StartInput {
  definitionId: string;
  envId: string;
  execution: "baseline" | "candidate";
  scale?: number;
  durationSeconds?: number;
  concurrency?: number;
  safeRoutes?: string[];
  unsafeRoutes?: string[];
}

export async function startRun(input: StartInput, csrf: string): Promise<{ runId: string | null }> {
  const raw = await mutate<unknown>("workloads.runs.start", input, csrf);
  return { runId: str(obj(raw)?.runId) };
}

export async function cancelRun(
  runId: string,
  reason: string | undefined,
  csrf: string,
): Promise<void> {
  await mutate("workloads.runs.cancel", { runId, ...(reason ? { reason } : {}) }, csrf);
}

export async function retryRun(runId: string, csrf: string): Promise<{ runId: string | null }> {
  const raw = await mutate<unknown>("workloads.runs.retry", { runId }, csrf);
  return { runId: str(obj(raw)?.runId) };
}

/** Turn one exploratory discovery into a deterministic definition somebody can
 *  pin, review and re-run. The whole point of the exploratory kind is that its
 *  output is a candidate; this is the step that makes a candidate a commitment. */
export async function promoteDiscovery(
  input: { definitionId: string; discoveryId: string; name: string },
  csrf: string,
): Promise<{ definitionId: string | null }> {
  const raw = await mutate<unknown>("workloads.promote", input, csrf);
  return { definitionId: str(obj(raw)?.definitionId) };
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

export function rate(value: number | null, unit: string): string {
  if (value === null) return "--";
  if (value >= 100) return `${Math.round(value).toLocaleString()} ${unit}`;
  if (value >= 10) return `${value.toFixed(1)} ${unit}`;
  return `${value.toFixed(2)} ${unit}`;
}

export function count(value: number | null): string {
  return value === null ? "--" : value.toLocaleString();
}

/** A fraction rendered as a percentage. Two figures under one percent, because
 *  an error rate of 0.4% rounds to "0%" and reads as none. */
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
