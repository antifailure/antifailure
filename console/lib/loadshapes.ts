/**
 * What the control plane's Studio rows ARE, and how to read one.
 *
 * Split from `load.ts` and holding no import, which is what makes it testable.
 * The calls next door reach the network and cannot run outside a browser; the
 * decoders here are the half that is worth a test, because every defect this
 * module has had was a shape defect: a verdict the type did not know about, a
 * ratio defaulted to zero where the answer was "nothing to compare with", a
 * cursor decoded off a response that carries none.
 *
 * `web/apps/api/test/loadconsole.test.ts` is that test, and it lives on the
 * API side deliberately: that is where a test runner exists in this repository
 * and where the rows these decoders read are produced, so the two halves of
 * the seam are checked against each other rather than each against itself.
 *
 * Import from `@/lib/load`, not from here. That module re-exports all of this,
 * so components still have one place to reconcile the contract in.
 *
 * Three rules run through it.
 *
 * A number the control plane did not record arrives as `null`, never as zero.
 * `p95_increase` is the sharpest case: 0 is a legitimate value meaning no
 * change AND the zero value of the column, which is why the schema constrains
 * it to exist exactly when a baseline does.
 *
 * A verdict that is not a verdict is never drawn as one. There are FIVE, not
 * four: `flaky` is one of them, and a decoder that dropped it would render a
 * run that found something as "No verdict", which is an absence displayed
 * where there is a finding.
 *
 * And state is not verdict. State says whether the work happened; verdict says
 * what it found. Neither implies the other, which is why they are two columns
 * on the run and two badges on the screen. A run that finishes cleanly and
 * fails every threshold is succeeded and fail.
 */

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

/** A number, or null. Accepts the string form a driver hands back for a bigint
 *  or a numeric column, but rejects the empty string, because `Number("")` is 0
 *  and a stat tile reading zero for something nothing measured is the defect
 *  this whole file is shaped around. */
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

/** An object, or null. Exported because the calls module reads a to-one embed
 *  out of a response with it, and a second copy of one line is a second thing
 *  that can disagree about what an object is. */
export function obj(v: unknown): Record<string, unknown> | null {
  return typeof v === "object" && v !== null && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : null;
}

/** Read snake_case first, camelCase second. The run and workload rows come
 *  straight out of SQL and are snake_case; a version body was written by a zod
 *  schema and is camelCase. One reader handles both rather than two. */
function pick(o: Record<string, unknown>, snake: string, camel: string): unknown {
  return o[snake] !== undefined ? o[snake] : o[camel];
}

/* -------------------------------------------------------------------------
 * The four kinds
 * ---------------------------------------------------------------------- */

/**
 * What a workload is, and the command a run of it becomes.
 *
 * Four kinds, and deliberately no shared shape between them. They measure
 * materially different things: a mix has no order, a journey has no browser, a
 * workflow has no request rate, and an exploration has no pass. The schema
 * carries a CHECK refusing a result of one kind wearing another's columns, and
 * this file keeps the same separation rather than flattening it back out.
 */
export type Kind = "observed_load" | "http_scenario" | "browser_workflow" | "exploration";

export const KINDS: readonly Kind[] = [
  "observed_load",
  "http_scenario",
  "browser_workflow",
  "exploration",
];

export function kindOf(v: unknown): Kind | null {
  return v === "observed_load" ||
    v === "http_scenario" ||
    v === "browser_workflow" ||
    v === "exploration"
    ? v
    : null;
}

/**
 * What each kind is, what a result from it is worth, and what it measures.
 *
 * `reproducible` earns its place here. It is the difference between a number
 * you can hand somebody and one you can only ask them to believe, and the
 * answer is not the same for the four.
 */
export const KIND_FACTS: Record<
  Kind,
  {
    noun: string;
    command: string;
    what: string;
    reproducible: string;
    /** What a run of it produces, so a reader knows which table to look at. */
    measures: string;
  }
> = {
  observed_load: {
    noun: "Observed load",
    command: "af load run",
    what: "A weighted mix read from what production actually served, compiled from an OTLP export or an access log. The mix is the point: what breaks under real traffic is the page nobody thinks about that is nine percent of requests.",
    reproducible:
      "As a shape. The mix and the rate replay, and the generator is seeded so two runs send the same sequence. The individual production requests it was derived from do not replay.",
    measures: "Requests, the rate it managed against the rate it aimed for, and latency per route.",
  },
  http_scenario: {
    noun: "Scenario",
    command: "af load scenario",
    what: "Journeys somebody wrote into the manifest and this version selects by name: steps in an order, with think time between them, and assertions that say what holding up means.",
    reproducible:
      "Exactly. The same scenarios at the same seed plan the same requests in the same order.",
    measures: "Requests and sessions, latency per route, and one row per assertion.",
  },
  browser_workflow: {
    noun: "Workflow",
    command: "af test",
    what: "Workflows out of the manifest, driven in a real browser by an agent. This asks whether a journey still works, which is a different question from how fast it is under load.",
    reproducible:
      "As an outcome rather than as a sequence. An agent reads the page it is on, so two runs can reach the same result by different routes.",
    measures: "One count per outcome across the workflows it selected, and the steps it took.",
  },
  exploration: {
    noun: "Exploration",
    command: "af explore",
    what: "An agent choosing its own way through the application from a goal and a seed, which is the other way a route nobody wrote down gets found.",
    reproducible: "At the same seed, the same wander.",
    measures: "Which goals were reached, and what the agent found on the way.",
  },
};

/* -------------------------------------------------------------------------
 * Verdicts and run state
 * ---------------------------------------------------------------------- */

/**
 * The product's five words, and no sixth.
 *
 * SIX, and the sixth is the interesting one. `verdict_value` in migration 0001
 * declares five: pass, fail, flaky, blocked and unverified. The ENGINE has a
 * sixth for a RUN, `warn`, which report.go calls "a real finding about the
 * change that does not stop the merge", and the column holding a run's verdict
 * is typed as the WORKFLOW vocabulary. So a run the engine judged `warn`
 * decodes to null on the control plane and renders as "No verdict".
 *
 * That is the third instance of one shape in this feature. `flaky` was missing
 * from this type, which is what this file was reworked to fix. `timed_out` was
 * a state the projection could not reach, so a run that hit its own timeout was
 * recorded as succeeded. And now `warn`. They are not three bugs: every
 * vocabulary in this system exists twice and nothing checked that the two
 * agreed, which is why the test beside this file reads each enum out of the
 * migration that declares it rather than out of a list somebody typed.
 *
 * `warn` is here BEFORE the enum carries it, deliberately, so nothing renders
 * it as an absence in the window while the migration catches up. The test
 * knows it is pending by name and fails the moment that stops being true.
 */
export type Verdict = "pass" | "fail" | "flaky" | "warn" | "blocked" | "unverified";

const VERDICTS = new Set<string>(["pass", "fail", "flaky", "warn", "blocked", "unverified"]);

export function verdictOf(v: unknown): Verdict | null {
  return typeof v === "string" && VERDICTS.has(v) ? (v as Verdict) : null;
}

/**
 * What a verdict means, and whether it is a judgement at all.
 *
 * `conclusive` is false for the two that say nothing about the software, and
 * it is what the run screen keys off to refuse to present a threshold table as
 * a finding. `flaky` is conclusive and is NOT a pass: something was measured
 * and it answered differently on repeat, which is a real finding about the
 * software and the reason it is toned as a warning rather than as a success.
 */
export const VERDICT_FACTS: Record<
  Verdict,
  { label: string; conclusive: boolean; meaning: string }
> = {
  pass: {
    label: "Pass",
    conclusive: true,
    meaning: "Everything that was evaluated held.",
  },
  fail: {
    label: "Fail",
    conclusive: true,
    meaning: "At least one thing was evaluated and did not hold.",
  },
  flaky: {
    label: "Flaky",
    conclusive: true,
    meaning:
      "The same check answered differently on repeat. That is a finding rather than a fluke: a result nobody can rely on is not a result. Flaky is not a pass.",
  },
  warn: {
    label: "Warn",
    conclusive: true,
    meaning:
      "A real finding about the change that does not stop the merge. Something was looked at and something was found, which is why this is not a pass, and it was not judged bad enough to block, which is why it is not a failure.",
  },
  blocked: {
    label: "Blocked",
    conclusive: false,
    meaning:
      "The work never reached the application, so nothing here is a judgement about it. Blocked is not a pass.",
  },
  unverified: {
    label: "Unverified",
    conclusive: false,
    meaning:
      "It finished and nothing could be evaluated, so it proved nothing either way. Unverified is not a pass.",
  },
};

/**
 * Where a run is, which is a different question from what it found.
 *
 * Eight values, straight off `workload_run_state`. The console writes the
 * first, an engine claiming the run writes the second, the engine's own events
 * write the next five, and the deadline writes the last one.
 */
export type RunState =
  | "requested"
  | "accepted"
  | "running"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "timed_out"
  | "abandoned";

const RUN_STATES = new Set<string>([
  "requested",
  "accepted",
  "running",
  "succeeded",
  "failed",
  "cancelled",
  "timed_out",
  "abandoned",
]);

export function runStateOf(v: unknown): RunState | null {
  return typeof v === "string" && RUN_STATES.has(v) ? (v as RunState) : null;
}

export function isRunning(s: RunState): boolean {
  return s === "requested" || s === "accepted" || s === "running";
}

export function isTerminal(s: RunState): boolean {
  return !isRunning(s);
}

export const STATE_FACTS: Record<RunState, { label: string; meaning: string }> = {
  requested: {
    label: "Requested",
    meaning:
      "Recorded here and asked of GitHub Actions. No engine has picked it up yet, so nothing is running.",
  },
  accepted: {
    label: "Accepted",
    meaning: "An engine has claimed this run and is bringing the environment up.",
  },
  running: { label: "Running", meaning: "The engine is doing the work now." },
  succeeded: {
    label: "Succeeded",
    meaning:
      "The engine did the work and reported. What it FOUND is the verdict beside this, which is a separate answer: a run can succeed and fail every threshold in it.",
  },
  failed: {
    label: "Failed",
    meaning: "The engine reported that the work itself failed.",
  },
  cancelled: {
    label: "Cancelled",
    meaning:
      "Stopped before it finished, so anything measured covers only the part that ran and is not a measurement of the whole.",
  },
  timed_out: {
    label: "Timed out",
    meaning: "The engine reported that it ran out of time before finishing.",
  },
  abandoned: {
    label: "Abandoned",
    meaning:
      "The deadline passed with no engine reporting. That is not a failure, because a failure is something an engine told us. It may well have run: what is missing is the report, not necessarily the work.",
  },
};

/* -------------------------------------------------------------------------
 * Definitions
 * ---------------------------------------------------------------------- */

export interface WorkloadRow {
  id: string;
  slug: string;
  name: string;
  kind: Kind;
  description: string | null;
  repository: string | null;
  archivedAt: string | null;
  createdAt: string | null;
  latestVersion: number | null;
  /** Null when the control plane did not count them, which is not none. */
  runs: number | null;
  /** The state and verdict of the most recent run, so a list says which
   *  workloads are currently unhappy without opening each one. One LATERAL
   *  join produces all three, so they always describe the same run. */
  lastState: RunState | null;
  lastVerdict: Verdict | null;
  lastRunAt: string | null;
}

export function readWorkloadRow(v: unknown): WorkloadRow | null {
  const o = obj(v);
  if (!o) return null;
  const slug = str(o.slug);
  const kind = kindOf(o.kind);
  // No slug and no kind, no row. The slug is the identifier every link and
  // every call uses, and rendering a row of an unknown kind would mean picking
  // a result table for it, which is the flattening this module refuses.
  if (!slug || !kind) return null;
  return {
    id: str(o.id) ?? slug,
    slug,
    name: str(o.name) ?? slug,
    kind,
    description: str(o.description),
    repository: str(o.repository),
    archivedAt: str(pick(o, "archived_at", "archivedAt")),
    createdAt: str(pick(o, "created_at", "createdAt")),
    latestVersion: num(pick(o, "latest_version", "latestVersion")),
    runs: num(o.runs),
    lastState: runStateOf(pick(o, "last_state", "lastState")),
    lastVerdict: verdictOf(pick(o, "last_verdict", "lastVerdict")),
    lastRunAt: str(pick(o, "last_run_at", "lastRunAt")),
  };
}

/* -------------------------------------------------------------------------
 * What a version says
 * ---------------------------------------------------------------------- */

/**
 * A version body, per kind.
 *
 * Every field maps to a flag the kind's command actually declares. There is
 * deliberately no shared body type and no optional field carried across kinds:
 * the control plane parses these with a STRICT schema and refuses an unknown
 * key rather than dropping it, because a misspelled `durationSecond` that is
 * silently ignored is a workload that runs and does not do what its author
 * wrote.
 */
export type Body =
  | { kind: "observed_load"; durationSeconds: number | null; scale: number | null }
  | { kind: "http_scenario"; select: string[]; seed: number | null; concurrency: number | null }
  | {
      kind: "browser_workflow";
      select: string[];
      /** Written only by a promotion: the manifest block that has to be in the
       *  repository before `select` can find the workflow. */
      manifestBlock: string | null;
      /** What the promotion's compilation could not carry, one sentence each.
       *  Never empty when it is present. */
      dropped: string[];
    }
  | { kind: "exploration"; select: string[]; seed: string | null };

export function readBody(kind: Kind, v: unknown): Body | null {
  const o = obj(v);
  if (!o) return null;
  const select = list(o.select, str);
  switch (kind) {
    case "observed_load":
      return {
        kind,
        durationSeconds: num(pick(o, "duration_seconds", "durationSeconds")),
        scale: num(o.scale),
      };
    case "http_scenario":
      return { kind, select, seed: num(o.seed), concurrency: num(o.concurrency) };
    case "browser_workflow":
      return {
        kind,
        select,
        manifestBlock: str(pick(o, "manifest_block", "manifestBlock")),
        dropped: list(o.dropped, str),
      };
    case "exploration":
      return { kind, select, seed: str(o.seed) };
  }
}

/**
 * The body as the control plane's schema wants it back.
 *
 * Absent rather than null for every optional knob, because the schema is
 * strict and `z.number().optional()` refuses null. An omitted key means the
 * command's own default, which is a different thing from a value of zero and
 * the only way to say "do not pass this flag".
 */
export function bodyToInput(body: Body): Record<string, unknown> {
  const some = (k: string, v: number | string | null): Record<string, unknown> =>
    v === null ? {} : { [k]: v };
  switch (body.kind) {
    case "observed_load":
      return { ...some("durationSeconds", body.durationSeconds), ...some("scale", body.scale) };
    case "http_scenario":
      return {
        select: body.select,
        ...some("seed", body.seed),
        ...some("concurrency", body.concurrency),
      };
    case "browser_workflow":
      return {
        select: body.select,
        ...some("manifestBlock", body.manifestBlock),
        ...(body.dropped.length > 0 ? { dropped: body.dropped } : {}),
      };
    case "exploration":
      return { select: body.select, ...some("seed", body.seed) };
  }
}

/**
 * Which knobs each kind can actually set, and what it cannot.
 *
 * Read off the engine's own flag sets and off the workflow inputs a dispatch
 * is allowed to send, rather than chosen for the form. A form offering
 * concurrency on an observed mix is a control that exists to be refused, and
 * that is the failure this console warns about elsewhere.
 *
 * `refused` is shown rather than hidden. A reader who finds no concurrency box
 * and no explanation assumes the console forgot; one who is told `af load run`
 * has no such flag has learned something about the product.
 */
export interface Knobs {
  duration: boolean;
  scale: boolean;
  /** How the command spells a seed, or that it takes none here. `af explore
   *  --seed` takes a string and `af load scenario --seed` takes a whole
   *  number, which is why this is not a boolean. */
  seed: "number" | "string" | "no";
  concurrency: boolean;
  select: "required" | "optional" | "no";
  /** What the selection names, in the manifest's own words. */
  selects: string;
  /** What an empty selection means, for the kind that allows one. */
  emptyMeans: string | null;
  refused: { knob: string; because: string }[];
}

export const KNOBS: Record<Kind, Knobs> = {
  observed_load: {
    duration: true,
    scale: true,
    seed: "no",
    concurrency: false,
    select: "no",
    selects: "",
    emptyMeans: null,
    refused: [
      {
        knob: "A selection",
        because:
          "The shape comes from whatever the manifest points at, an OTLP export or an access log, so there is nothing here to name. af load run has no --only flag.",
      },
      {
        knob: "Concurrency",
        because:
          "af load run has no --concurrency flag. Accepting one and running at the generator's own default would produce a run that did not do what its author asked, with nothing in the result saying so.",
      },
      {
        knob: "Seed",
        because:
          "af load run does take --seed, and a dispatch cannot carry one here: the workflow this product shipped before Studio declares four inputs, command, workflows, duration and scale, and sending a fifth is a 422 from GitHub. Observed load stays inside those four so it keeps working against every repository that has not copied the newer workflow.",
      },
    ],
  },
  http_scenario: {
    duration: false,
    scale: false,
    seed: "number",
    concurrency: true,
    select: "required",
    selects: "scenario",
    emptyMeans: null,
    refused: [
      {
        knob: "Duration and scale",
        because:
          "af load scenario has neither flag. A journey runs its steps in order for as long as they take; it does not send at a rate.",
      },
    ],
  },
  browser_workflow: {
    duration: false,
    scale: false,
    seed: "no",
    concurrency: false,
    select: "optional",
    selects: "workflow",
    emptyMeans:
      "Empty means every workflow the manifest declares, which is what af test with no --only does.",
    refused: [
      {
        knob: "Duration, scale, seed and concurrency",
        because:
          "af test declares none of them. It drives a browser through named workflows rather than sending traffic at a rate.",
      },
    ],
  },
  exploration: {
    duration: false,
    scale: false,
    seed: "string",
    concurrency: false,
    select: "required",
    selects: "goal",
    emptyMeans: null,
    refused: [
      {
        knob: "Duration, scale and concurrency",
        because:
          "af explore declares none of them. It walks one goal at a time from a seed rather than sending traffic.",
      },
    ],
  },
};

export interface Version {
  id: string;
  version: number;
  body: Body | null;
  bodyDigest: string | null;
  notes: string | null;
  source: "authored" | "promoted";
  promotedFromRunId: string | null;
  createdAt: string | null;
}

export function readVersion(kind: Kind, v: unknown): Version | null {
  const o = obj(v);
  if (!o) return null;
  const id = str(o.id);
  const version = num(o.version);
  // A version with no number cannot be named on a start request, so a row that
  // has lost it is a row nothing can act on.
  if (!id || version === null) return null;
  const source = o.source === "promoted" ? "promoted" : "authored";
  return {
    id,
    version,
    body: readBody(kind, o.body),
    bodyDigest: str(pick(o, "body_digest", "bodyDigest")),
    notes: str(o.notes),
    source,
    promotedFromRunId: str(pick(o, "promoted_from_run_id", "promotedFromRunId")),
    createdAt: str(pick(o, "created_at", "createdAt")),
  };
}

/* -------------------------------------------------------------------------
 * Runs
 * ---------------------------------------------------------------------- */

export interface RunRow {
  id: string;
  workloadSlug: string | null;
  kind: Kind | null;
  version: number | null;
  state: RunState;
  /** Null until the run reaches one, and null forever on a run nothing
   *  reported. A run still going has no verdict, and inventing one for the
   *  list would be the console's own version of a green run over nothing. */
  verdict: Verdict | null;
  envId: string | null;
  repository: string | null;
  gitRef: string | null;
  attempt: number | null;
  retryOf: string | null;
  supersededBy: string | null;
  /** The code the ENGINE reported, out of the errors catalogue. Never invented
   *  here: an abandoned run deliberately carries none, because no engine
   *  reported it, and a code that looks catalogued and is not sends somebody
   *  to an errors reference that does not have it. */
  failureCode: string | null;
  detail: string | null;
  /** The `af` command the engine reported. A console that finds null says no
   *  command was recorded rather than assembling one that would drift from
   *  what ran. */
  reproduceCommand: string | null;
  manifestDigest: string | null;
  requestedAt: string | null;
  acceptedAt: string | null;
  startedAt: string | null;
  finishedAt: string | null;
  deadlineAt: string | null;
  cancelRequestedAt: string | null;
  cancelledAt: string | null;
  dispatchedAt: string | null;
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
    workloadSlug: str(pick(o, "workload_slug", "workloadSlug")),
    kind: kindOf(o.kind),
    version: num(o.version),
    state,
    verdict: verdictOf(o.verdict),
    envId: str(pick(o, "env_id", "envId")),
    repository: str(o.repository),
    gitRef: str(pick(o, "git_ref", "gitRef")),
    attempt: num(o.attempt),
    retryOf: str(pick(o, "retry_of", "retryOf")),
    supersededBy: str(pick(o, "superseded_by", "supersededBy")),
    failureCode: str(pick(o, "failure_code", "failureCode")),
    detail: str(o.detail),
    reproduceCommand: str(pick(o, "reproduce_command", "reproduceCommand")),
    manifestDigest: str(pick(o, "manifest_digest", "manifestDigest")),
    requestedAt: str(pick(o, "requested_at", "requestedAt")),
    acceptedAt: str(pick(o, "accepted_at", "acceptedAt")),
    startedAt: str(pick(o, "started_at", "startedAt")),
    finishedAt: str(pick(o, "finished_at", "finishedAt")),
    deadlineAt: str(pick(o, "deadline_at", "deadlineAt")),
    cancelRequestedAt: str(pick(o, "cancel_requested_at", "cancelRequestedAt")),
    cancelledAt: str(pick(o, "cancelled_at", "cancelledAt")),
    dispatchedAt: str(pick(o, "dispatched_at", "dispatchedAt")),
  };
}

/* -------------------------------------------------------------------------
 * Results, as the control plane records them
 * ---------------------------------------------------------------------- */

/**
 * A latency distribution.
 *
 * Exactly the engine's five and no sixth. An earlier draft of this file had a
 * p75 that nothing produces, which would have drawn a rung on every chart that
 * no measurement backed.
 */
export interface Latency {
  p50Ms: number | null;
  p90Ms: number | null;
  p95Ms: number | null;
  p99Ms: number | null;
  maxMs: number | null;
}

export function readLatency(o: Record<string, unknown>): Latency {
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
    .filter(([, value]) => value !== null)
    .map(([label, value]) => ({ label, ms: value as number }));
}

export function hasLatency(l: Latency): boolean {
  return percentiles(l).length > 0;
}

/**
 * What each error reason usually means, so a reader is not left with a bare
 * string.
 *
 * The keys ARE the closed set the load runner produces, plus an HTTP status of
 * 500 or above spelled as its number, which cannot be enumerated here. A
 * reason not in this table is rendered plainly rather than dropped or guessed
 * at, because a reason the console has not been taught is still the truth
 * about the run.
 */
export const REASON_NOTES: Record<string, string> = {
  timeout: "The application did not answer inside the request deadline.",
  "connection refused": "Nothing was listening. Usually the service is not up yet.",
  "connection reset": "The connection was closed mid request, often a crash or a restart.",
  "name not resolved":
    "DNS did not answer for the host, which under a deny all egress policy is what a blocked host looks like.",
  "malformed request":
    "The request could not be built. This is the scenario or the mix, not the application.",
  "request failed": "A transport error the runner could not classify further.",
};

export function readErrorReasons(v: unknown): { reason: string; count: number }[] {
  const o = obj(v);
  if (!o) return [];
  const out: { reason: string; count: number }[] = [];
  for (const [reason, raw] of Object.entries(o)) {
    const n = num(raw);
    if (n !== null && n > 0) out.push({ reason, count: n });
  }
  // Commonest first. The order is the finding: it says which failure to chase.
  return out.sort((a, b) => b.count - a.count);
}

/**
 * One run's measurements.
 *
 * One interface with a column per measure rather than four, because that is
 * how the row is stored and a CHECK in the schema already guarantees only one
 * kind's columns are filled. The renderers switch on `kind` and read only the
 * group that kind owns; nothing here averages across them.
 */
export interface RunResult {
  kind: Kind;
  /** Traffic. observed_load and http_scenario. */
  requests: number | null;
  failures: number | null;
  errorRate: number | null;
  targetRate: number | null;
  /** What it achieved. The gap between this and the target is the first thing
   *  worth looking at: reporting the target instead is how a load test says
   *  everything was fine while the queue grew. */
  achievedRate: number | null;
  latency: Latency;
  /** A journey. http_scenario. */
  sessions: number | null;
  iterations: number | null;
  scheduledMs: number | null;
  /** A browser. browser_workflow. Five counts and not two, because a real
   *  `af test` run returned 0 passed, 0 failed, 0 flaky, 0 blocked and 1
   *  unverified: with passed and failed alone a console draws a run that did
   *  nothing as a run with no failures. */
  workflows: number | null;
  workflowsPassed: number | null;
  workflowsFailed: number | null;
  workflowsFlaky: number | null;
  workflowsBlocked: number | null;
  workflowsUnverified: number | null;
  steps: number | null;
  /** A wander. exploration. */
  findings: number | null;
  goals: number | null;
  goalsReached: number | null;

  durationMs: number | null;
  /** Where the traffic mix came from, so a reader can tell production's shape
   *  from a default. */
  source: string | null;
  errorReasons: { reason: string; count: number }[];
  /** Routes the manifest's safe list refused, which is why a blocked scenario
   *  is blocked. */
  refusedRoutes: string[];
  recordedAt: string | null;
}

export function readResult(v: unknown): RunResult | null {
  const o = obj(v);
  if (!o) return null;
  const kind = kindOf(o.kind);
  // No kind, no result. Every renderer below chooses its columns by kind, and
  // a result that will not say which one it is cannot be read at all.
  if (!kind) return null;
  return {
    kind,
    requests: num(o.requests),
    failures: num(o.failures),
    errorRate: num(pick(o, "error_rate", "errorRate")),
    targetRate: num(pick(o, "target_rate", "targetRate")),
    achievedRate: num(pick(o, "achieved_rate", "achievedRate")),
    latency: readLatency(o),
    sessions: num(o.sessions),
    iterations: num(o.iterations),
    scheduledMs: num(pick(o, "scheduled_ms", "scheduledMs")),
    workflows: num(o.workflows),
    workflowsPassed: num(pick(o, "workflows_passed", "workflowsPassed")),
    workflowsFailed: num(pick(o, "workflows_failed", "workflowsFailed")),
    workflowsFlaky: num(pick(o, "workflows_flaky", "workflowsFlaky")),
    workflowsBlocked: num(pick(o, "workflows_blocked", "workflowsBlocked")),
    workflowsUnverified: num(pick(o, "workflows_unverified", "workflowsUnverified")),
    steps: num(o.steps),
    findings: num(o.findings),
    goals: num(o.goals),
    goalsReached: num(pick(o, "goals_reached", "goalsReached")),
    durationMs: num(pick(o, "duration_ms", "durationMs")),
    source: str(o.source),
    errorReasons: readErrorReasons(pick(o, "error_reasons", "errorReasons")),
    refusedRoutes: list(pick(o, "refused_routes", "refusedRoutes"), str),
    recordedAt: str(pick(o, "recorded_at", "recordedAt")),
  };
}

/** How far short of the target the run fell, as a fraction, or null when
 *  either number is missing. Positive means it did not keep up. */
export function rateShortfall(r: RunResult): number | null {
  if (r.targetRate === null || r.achievedRate === null || r.targetRate <= 0) return null;
  return (r.targetRate - r.achievedRate) / r.targetRate;
}

/**
 * A browser run that reported nothing either way.
 *
 * The exit code zero over nothing defect, moved into a table and read back
 * out. A run with workflows to do, none passed and none failed, has not found
 * that the software is fine: it has found that nothing was checked.
 */
export function nothingWasChecked(r: RunResult): boolean {
  if (r.kind !== "browser_workflow") return false;
  const attempted = (r.workflowsPassed ?? 0) + (r.workflowsFailed ?? 0) + (r.workflowsFlaky ?? 0);
  return (r.workflows ?? 0) > 0 && attempted === 0;
}

/**
 * One route's measurement.
 *
 * `scenario` is carried and is not folded into the route, because two
 * scenarios in one run can send the same route and their two p95 values do not
 * average into a p95. The unique index is on the pair for that reason.
 */
export interface RouteMetric {
  scenario: string | null;
  route: string;
  sent: number | null;
  errors: number | null;
  latency: Latency;
  baselineP95Ms: number | null;
  /** A ratio. 0.31 is thirty one percent slower than the baseline. */
  p95Increase: number | null;
}

export function readRouteMetric(v: unknown): RouteMetric | null {
  const o = obj(v);
  if (!o) return null;
  const route = str(o.route);
  if (!route) return null;
  return {
    scenario: str(o.scenario),
    route,
    sent: num(o.sent),
    errors: num(o.errors),
    latency: readLatency(o),
    baselineP95Ms: num(pick(o, "baseline_p95_ms", "baselineP95Ms")),
    p95Increase: num(pick(o, "p95_increase", "p95Increase")),
  };
}

/**
 * The comparison for one route, or null when there is nothing to compare
 * against. Never zero for absent.
 *
 * Both halves are required rather than defaulting the ratio to zero, because
 * zero means no regression and the schema constrains the pair to exist
 * together. A row that arrives with only one of them is a row the console
 * cannot interpret, and guessing would report a clean comparison that nobody
 * made.
 */
export function increase(r: RouteMetric): number | null {
  return r.baselineP95Ms !== null && r.p95Increase !== null ? r.p95Increase : null;
}

/**
 * One threshold, what it concluded, and what it measured.
 *
 * The verdict is the product's same five words, not a private vocabulary, and
 * `measure` is text rather than an enum because the engine adds one by
 * releasing and a customer's database should not need a migration to record a
 * measure it was sent.
 */
export interface ThresholdVerdict {
  scenario: string | null;
  name: string;
  /** The route it was narrowed to. Null for a scenario wide assertion. */
  scope: string | null;
  measure: string | null;
  threshold: number | null;
  observed: number | null;
  verdict: Verdict;
  detail: string | null;
}

export function readThreshold(v: unknown): ThresholdVerdict | null {
  const o = obj(v);
  if (!o) return null;
  const name = str(o.name);
  // The column is `value` and it is a verdict. No verdict, no row, for the
  // same reason a run with no state is dropped: rendering it would mean
  // choosing a tone, and a wrong choice paints something inconclusive as a
  // pass.
  const verdict = verdictOf(o.value ?? o.verdict);
  if (!name || !verdict) return null;
  return {
    scenario: str(o.scenario),
    name,
    scope: str(o.scope),
    measure: str(o.measure),
    threshold: num(o.threshold),
    observed: num(o.observed),
    verdict,
    detail: str(o.detail),
  };
}

/**
 * A threshold or an observation, in the unit its measure implies.
 *
 * The engine sends both as a bare float and the unit lives in `measure`, so
 * this is the one place that mapping is written down. `p95_below_ms` is
 * milliseconds and `error_rate_below` is a fraction; the other two measures
 * carry no number at all and never reach here with one.
 */
export function measured(measure: string | null, value: number | null): string {
  if (value === null) return "--";
  if (measure === "p95_below_ms") return ms(value);
  if (measure === "error_rate_below") return percent(value);
  // A measure this console has not been taught. The number is still true, so
  // it is printed rather than hidden behind a dash.
  return String(value);
}

/** The two measures that are comparisons against a number. The other two,
 *  `every_request_succeeded` and `status_in`, carry no threshold at all, which
 *  is a different thing from a threshold that went unmeasured. */
export function isNumericMeasure(measure: string | null): boolean {
  return measure === "p95_below_ms" || measure === "error_rate_below";
}

/**
 * Whether the bytes behind a piece of evidence can actually be fetched.
 *
 * Three values and not a boolean. `runner_local` is the honest one: a trace at
 * a path on the CI runner, on a machine that no longer exists. Reports in this
 * product have carried exactly those paths, and a console that renders one as
 * a link sends somebody to a 404 and blames itself.
 */
export type Availability = "uploaded" | "runner_local" | "not_retained";

export function availabilityOf(v: unknown): Availability | null {
  return v === "uploaded" || v === "runner_local" || v === "not_retained" ? v : null;
}

export const AVAILABILITY_FACTS: Record<
  Availability,
  { label: string; fetchable: boolean; meaning: string }
> = {
  uploaded: {
    label: "Kept",
    fetchable: true,
    meaning: "Stored, with a checksum to verify it against.",
  },
  runner_local: {
    label: "On the runner",
    fetchable: false,
    meaning:
      "It was written to a path on the CI runner and never uploaded. The machine is gone, so the path is a record of where it was rather than somewhere to fetch it from.",
  },
  not_retained: {
    label: "Dropped",
    fetchable: false,
    meaning: "It existed and retention did not keep it.",
  },
};

export interface EvidenceItem {
  kind: string;
  label: string | null;
  availability: Availability;
  locator: string;
  sha256: string | null;
  sizeBytes: number | null;
}

export function readEvidence(v: unknown): EvidenceItem | null {
  const o = obj(v);
  if (!o) return null;
  const kind = str(o.kind);
  const locator = str(o.locator);
  const availability = availabilityOf(o.availability);
  // Every one of the three is NOT NULL in the schema, and availability is what
  // decides whether this is a link or a note. A row missing it cannot be drawn
  // without guessing which, and the wrong guess is a broken link.
  if (!kind || !locator || !availability) return null;
  return {
    kind,
    label: str(o.label),
    availability,
    locator,
    sha256: str(o.sha256),
    sizeBytes: num(pick(o, "size_bytes", "sizeBytes")),
  };
}

/* -------------------------------------------------------------------------
 * The stop request
 * ---------------------------------------------------------------------- */

/**
 * Where a cancellation got to.
 *
 * A stop is a durable command rather than a flag, because the control plane
 * cannot reach a runtime and marking a row and hoping is not a cancellation.
 * `acknowledged` is about this table and `outcome` is about the world, which
 * is why they are two fields.
 */
export type CommandState =
  | "pending"
  | "claimed"
  | "acknowledged"
  | "failed"
  | "expired"
  | "superseded";

const COMMAND_STATES = new Set<string>([
  "pending",
  "claimed",
  "acknowledged",
  "failed",
  "expired",
  "superseded",
]);

export const COMMAND_FACTS: Record<CommandState, { label: string; meaning: string }> = {
  pending: {
    label: "Waiting",
    meaning: "The stop is recorded. No runtime has picked it up yet.",
  },
  claimed: { label: "Claimed", meaning: "A runtime has taken the stop and is acting on it." },
  acknowledged: {
    label: "Acknowledged",
    meaning: "A runtime confirmed it acted. What it did is beside this.",
  },
  failed: {
    label: "Failed",
    meaning: "A runtime tried to stop the run and reported that it could not.",
  },
  expired: {
    label: "Never confirmed",
    meaning:
      "The command's deadline passed with nothing acknowledging it. The run may still be going out there.",
  },
  superseded: { label: "Superseded", meaning: "A later command replaced this one." },
};

export interface CancelCommand {
  id: string;
  state: CommandState;
  outcome: string | null;
  detail: string | null;
  requestedAt: string | null;
  acknowledgedAt: string | null;
}

export function readCancel(v: unknown): CancelCommand | null {
  const o = obj(v);
  if (!o) return null;
  const id = str(o.id);
  const state =
    typeof o.state === "string" && COMMAND_STATES.has(o.state) ? (o.state as CommandState) : null;
  if (!id || !state) return null;
  return {
    id,
    state,
    outcome: str(o.outcome),
    detail: str(o.detail),
    requestedAt: str(pick(o, "requested_at", "requestedAt")),
    acknowledgedAt: str(pick(o, "acknowledged_at", "acknowledgedAt")),
  };
}

/* -------------------------------------------------------------------------
 * One run, whole
 * ---------------------------------------------------------------------- */

export interface RunDetail {
  run: RunRow;
  /**
   * Null until the run reaches a terminal state, and that is not a gap.
   * Nothing writes a result row before a terminal transition, so a running run
   * genuinely has none. An earlier draft of this console carried a "partial
   * results" state for the case where a running run had some of its numbers;
   * that state could never fire and it was deleted rather than kept.
   */
  result: RunResult | null;
  routes: RouteMetric[];
  thresholds: ThresholdVerdict[];
  evidence: EvidenceItem[];
  cancel: CancelCommand | null;
}

export function readRunDetail(v: unknown): RunDetail | null {
  const o = obj(v);
  if (!o) return null;
  const run = readRun(o.run);
  if (!run) return null;
  // A to-one embed is an object or null and a to-many is an array, said
  // explicitly because getting it backwards on this side is what makes one
  // surprising row blank a whole page.
  return {
    run,
    result: readResult(o.result),
    routes: list(o.routes, readRouteMetric),
    thresholds: list(o.thresholds, readThreshold),
    evidence: list(o.evidence, readEvidence),
    cancel: readCancel(o.cancel),
  };
}

/**
 * A run whose verdict disagrees with the thresholds under it.
 *
 * This exists because of the specific way this product has been wrong before:
 * a nightly corpus reported six passing workflows having never once reached an
 * agent, and every summary anybody read said green. The console cannot correct
 * a verdict the control plane recorded, but it can refuse to present a
 * contradiction quietly.
 *
 * Only a conclusive verdict is checked. Flagging a blocked run for having
 * unevaluated thresholds would be restating its own definition at a reader.
 */
export function verdictContradiction(
  verdict: Verdict | null,
  thresholds: ThresholdVerdict[],
): string | null {
  if (verdict === null || !VERDICT_FACTS[verdict].conclusive) return null;
  const broke = thresholds.filter((t) => t.verdict === "fail").length;
  const unstable = thresholds.filter((t) => t.verdict === "flaky").length;
  const unevaluated = thresholds.filter(
    (t) => t.verdict === "blocked" || t.verdict === "unverified",
  ).length;

  if (verdict === "pass" && broke + unstable + unevaluated > 0) {
    // Every half that applies, not just the loudest. Naming only the broken
    // ones would leave a reader believing the rest held, which is the quieter
    // half of the same mistake: a threshold nothing measured has not passed.
    const parts: string[] = [];
    if (broke > 0) {
      parts.push(
        broke === 1 ? "one threshold below broke" : `${broke} of the thresholds below broke`,
      );
    }
    if (unstable > 0) {
      parts.push(unstable === 1 ? "one came back flaky" : `${unstable} came back flaky`);
    }
    if (unevaluated > 0) {
      parts.push(
        unevaluated === 1 ? "one was never evaluated" : `${unevaluated} were never evaluated`,
      );
    }
    const tail =
      broke > 0
        ? "A run cannot pass over a threshold that broke, so one of the two records is wrong."
        : unstable > 0
          ? "A threshold that answers differently on repeat has not held, so this pass covers less than it appears to."
          : "A threshold nothing measured has not held, so this pass covers less than it appears to.";
    return `This run is recorded as a pass, but ${joinWords(parts)}. ${tail}`;
  }
  if (verdict === "fail" && broke === 0) {
    return "This run is recorded as a failure, and no threshold below broke. Whatever failed it is not in this table.";
  }
  return null;
}

/**
 * What decided a verdict, named off the rows that decided it.
 *
 * This exists because of a measured gap rather than for symmetry. A load run
 * that sent traffic and failed a threshold arrives with an EMPTY detail: the
 * engine's mixDetail returns a sentence only when nothing was sent. So the
 * header of a failing load run is a red badge with nothing beside it, which is
 * the worst version of a failure state: the reader knows something broke and
 * is told nothing about what.
 *
 * The reason lives in the threshold rows, so this names them rather than
 * inventing a sentence. Three at most, because a list of forty is a wall
 * somebody skips and the table underneath has all of them anyway.
 *
 * The empty case is said too. A run recorded as a failure with nothing broken
 * under it is a real disagreement between two records, and a silent header
 * there would hide the only clue that the reason is somewhere else.
 */
export function whatDecidedIt(
  verdict: Verdict | null,
  thresholds: ThresholdVerdict[],
): string | null {
  if (verdict !== "fail" && verdict !== "flaky") return null;
  const named = thresholds.filter((t) => t.verdict === verdict);
  const word = verdict === "fail" ? "broke" : "came back flaky";

  if (named.length === 0) {
    if (verdict !== "fail") return null;
    // Two different absences. A table of thresholds that all held is a
    // disagreement between two records; no table at all is a run that was
    // never going to explain itself here. Pointing at "the thresholds below"
    // when there are none sends somebody to an empty card.
    return thresholds.length === 0
      ? "No thresholds were recorded for this run, so what failed it is not on this page. The engine's own output for this branch is where to look."
      : "Nothing in the thresholds below broke, so whatever failed this run was not recorded as one.";
  }
  const label = (t: ThresholdVerdict) =>
    t.scope === null ? t.name : `${t.name} on ${t.scope}`;
  const shown = named.slice(0, 3).map(label);
  const rest = named.length - shown.length;
  const list = rest > 0 ? `${shown.join(", ")} and ${rest} more` : joinWords(shown);
  return named.length === 1
    ? `One threshold ${word}: ${list}. It is in the table below.`
    : `${named.length} thresholds ${word}: ${list}. They are in the table below.`;
}

/** "a", "a and b", "a, b and c". Written out because three clauses joined with
 *  " and " twice reads as a list somebody forgot to punctuate. */
function joinWords(parts: string[]): string {
  if (parts.length <= 1) return parts[0] ?? "";
  return `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
}

/* -------------------------------------------------------------------------
 * Reading a pasted exploration
 * ---------------------------------------------------------------------- */

/**
 * One exploration out of a pasted document, and why a document yielded none.
 *
 * `af explore --json` prints an ENVELOPE: `{headline, explorations, findings,
 * blocked}`, read off `ExploreJSON` in the engine's own CLI. The promotion
 * route compiles ONE exploration and reads `name` and `goal` off the top level
 * of what it is sent. So a person following this console's own instruction and
 * pasting what the command printed got back "this document carries neither a
 * name nor a goal", which is a refusal that names the wrong problem: the
 * document carries both, one level down, once per goal.
 *
 * A run with two goals produces two explorations and only one can be promoted
 * at a time, so unwrapping is not enough and the person has to choose. This
 * reads the envelope, reads a bare exploration, and when it is neither says
 * what it actually found rather than what it wanted.
 */
export interface PastedExploration {
  name: string;
  goal: string | null;
  /** Whether the goal's own words ever appeared on a page. */
  reached: boolean | null;
  verdict: string | null;
  /** The element to send, untouched. Never rebuilt out of the fields above:
   *  the compiler reads more of it than this summary does. */
  raw: unknown;
}

export interface Pasted {
  explorations: PastedExploration[];
  /** Why there is nothing to promote, in words that name what was found
   *  rather than what was expected. */
  refusal: string | null;
}

export function readExplorations(raw: unknown): Pasted {
  const none = (refusal: string): Pasted => ({ explorations: [], refusal });
  const o = obj(raw);
  if (!o) {
    return none(
      "That is a JSON value rather than a document. Paste the object af explore --json printed, braces and all.",
    );
  }

  const one = (v: unknown): PastedExploration | null => {
    const e = obj(v);
    const name = str(e?.name);
    // No name, nothing to promote: the compiler refuses a document without
    // one, and a picker entry with no label is a row nobody can choose.
    if (!e || !name) return null;
    return {
      name,
      goal: str(e.goal),
      reached: bool(e.reached),
      verdict: str(obj(e.outcome)?.verdict),
      raw: e,
    };
  };

  if (Array.isArray(o.explorations)) {
    const found = list(o.explorations, one);
    if (found.length > 0) return { explorations: found, refusal: null };
    const blocked = num(o.blocked) ?? 0;
    return none(
      blocked > 0
        ? `That run explored nothing: ${blocked} of its goals were blocked, so there is no path to compile into a workflow. Blocked is the runner or the environment failing to start rather than a finding about the application.`
        : "That document carries an explorations list with nothing usable in it. An exploration needs a name before anything can be compiled from it.",
    );
  }

  const single = one(o);
  if (single) return { explorations: [single], refusal: null };
  return none(
    "That document carries no explorations list and no name of its own, so there is no exploration in it. What af explore --json prints is an object with an explorations array, and a single exploration out of that array works too.",
  );
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

/** A duration recorded in milliseconds, read as a length of time. Under a
 *  minute it keeps the millisecond precision it was measured at, because a
 *  scenario that took 840ms rounding to "1s" loses the only interesting part. */
export function duration(msValue: number | null): string {
  if (msValue === null) return "--";
  return msValue < 60_000 ? ms(msValue) : seconds(msValue / 1000);
}

// No bytes() here. `console/lib/format.ts` already has one, and two
// declarations of one idea are two things that can drift apart while both look
// authoritative. The evidence table imports that one.
