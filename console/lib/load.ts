/**
 * The Load area's data boundary.
 *
 * Every `workloads.*` path the console calls is named in this file and nowhere
 * else, so the contract with the control plane is one file to reconcile rather
 * than a grep across a dozen components. The shapes those calls return live in
 * `loadshapes.ts` and are re-exported here, so a component imports one module
 * and this file is still the whole contract.
 *
 * WHY THE URL SAYS LOAD AND THE ROUTES SAY WORKLOADS. The `/load` address and
 * the nav entry are what a person reads, and they stay. The tRPC key is not
 * user facing, and `load.run` already exists on the control plane as a
 * different thing: a one off dispatch with no definition behind it. Putting
 * `load.runs.*` beside it would make two adjacent keys mean different things,
 * with the special case reading as the general one.
 *
 * WHAT A DEFINITION ACTUALLY IS, because this is the correction that shaped
 * the area. A version is not a document. All four runnable things are declared
 * in the CUSTOMER'S manifest and selected by name: `af load scenario --only
 * checkout`, `af test --only sign-up`, `af explore --only upgrade`. None of
 * them takes a document and none could, because a scenario is checked against
 * the manifest's safe route list before anything is sent, and a control plane
 * able to hand an engine an arbitrary journey could send traffic the customer
 * never allowed. So a version is a SELECTION plus the knobs the command
 * actually declares, and nothing larger.
 */

import { mutate, query } from "@/lib/api";
import {
  bodyToInput,
  bool,
  list,
  num,
  obj,
  readRun,
  readRunDetail,
  readVersion,
  readWorkloadRow,
  str,
  type Body,
  type Kind,
  type RunDetail,
  type RunRow,
  type RunState,
  type Version,
  type WorkloadRow,
} from "@/lib/loadshapes";

export * from "@/lib/loadshapes";

/* -------------------------------------------------------------------------
 * The calls
 *
 * WHAT IS NOT HERE, AND WHY IT IS SAID RATHER THAN HIDDEN.
 *
 * Neither list route takes a cursor. Both take a limit that the control plane
 * caps at 200 and both answer with a bare array, so there is no `nextCursor`
 * to decode and this file does not invent one: a decoder written for a shape
 * the server does not produce is the exact defect this module was reworked to
 * remove.
 *
 * What a bare array under a cap does is truncate silently, and a table that
 * looks complete and is not is worse than a slow page. So every list asks for
 * one more row than it shows, and `Windowed.more` says whether the control
 * plane had another one. The screen then either offers a wider window or says
 * plainly that it has hit the cap. Nothing here estimates a total: the
 * response carries no count, and "50 of about 200" would be a number the
 * console made up.
 * ---------------------------------------------------------------------- */

/** The largest limit `workloads.list` and `workloads.runs` accept. Asking for
 *  more is refused by the input schema rather than clamped, so this is a real
 *  ceiling and not a default. */
export const LIST_CAP = 200;

export interface Windowed<T> {
  items: T[];
  /** The control plane had at least one more row than this window shows. */
  more: boolean;
  /** The window that produced these rows, so a footer can say what widening
   *  it would do and whether it is already at the cap. */
  limit: number;
}

/**
 * Asks for one more row than it will show, so "there are more" is something
 * the server answered rather than something the console assumed from a full
 * page.
 *
 * `more` is counted off the RAW array and not off the decoded one. The
 * decoders drop a row they cannot read, so a window of fifty one that came
 * back with one unreadable row would decode to fifty and report that the list
 * ended, which is the truncation this whole shape exists to prevent, arrived
 * at a different way.
 *
 * At the cap there is no extra row to ask for, so `more` falls back to the
 * window being exactly full. That is a weaker claim and the footer says so in
 * different words.
 */
async function fetchWindow<T>(
  limit: number,
  fetch: (ask: number) => Promise<unknown>,
  each: (item: unknown) => T | null,
): Promise<Windowed<T>> {
  const capped = Math.min(limit, LIST_CAP);
  const ask = Math.min(capped + 1, LIST_CAP);
  const raw = await fetch(ask);
  const returned = Array.isArray(raw) ? raw.length : 0;
  const rows = list(raw, each);
  if (ask > capped) return { items: rows.slice(0, capped), more: returned > capped, limit: capped };
  return { items: rows, more: returned >= LIST_CAP, limit: capped };
}

export async function listWorkloads(input: {
  kind?: Kind;
  repository?: string;
  includeArchived?: boolean;
  limit?: number;
}): Promise<Windowed<WorkloadRow>> {
  const { limit = 50, ...rest } = input;
  return fetchWindow(
    limit,
    (ask) => query<unknown>("workloads.list", { ...rest, limit: ask }),
    readWorkloadRow,
  );
}

export interface WorkloadDetail {
  workload: WorkloadRow;
  versions: Version[];
  runs: RunRow[];
}

export async function getWorkload(slug: string): Promise<WorkloadDetail | null> {
  const raw = obj(await query<unknown>("workloads.get", { slug }));
  if (!raw) return null;
  const workload = readWorkloadRow(raw.workload);
  if (!workload) return null;
  return {
    workload,
    versions: list(raw.versions, (v) => readVersion(workload.kind, v)),
    runs: list(raw.runs, readRun),
  };
}

export async function listRuns(input: {
  slug?: string;
  envId?: string;
  state?: RunState;
  limit?: number;
}): Promise<Windowed<RunRow>> {
  const { limit = 50, ...rest } = input;
  return fetchWindow(limit, (ask) => query<unknown>("workloads.runs", { ...rest, limit: ask }), readRun);
}

export async function inspectRun(runId: string): Promise<RunDetail | null> {
  return readRunDetail(await query<unknown>("workloads.inspect", { runId }));
}

/* -------------------------------------------------------------------------
 * Writing
 * ---------------------------------------------------------------------- */

export async function createWorkload(
  input: {
    repository: string;
    slug: string;
    name: string;
    kind: Kind;
    description?: string;
    body: Body;
  },
  csrf: string,
): Promise<{ slug: string; version: number | null }> {
  const raw = obj(
    await mutate<unknown>("workloads.create", { ...input, body: bodyToInput(input.body) }, csrf),
  );
  return { slug: str(raw?.slug) ?? input.slug, version: num(raw?.version) };
}

export async function addVersion(
  input: { slug: string; body: Body; notes?: string },
  csrf: string,
): Promise<{ version: number | null; created: boolean; note: string | null }> {
  const raw = obj(
    await mutate<unknown>(
      "workloads.addVersion",
      {
        slug: input.slug,
        body: bodyToInput(input.body),
        ...(input.notes ? { notes: input.notes } : {}),
      },
      csrf,
    ),
  );
  return {
    version: num(raw?.version),
    created: bool(raw?.created) ?? false,
    note: str(raw?.note),
  };
}

export async function archiveWorkload(
  input: { slug: string; reason?: string },
  csrf: string,
): Promise<void> {
  await mutate("workloads.archive", input, csrf);
}

/**
 * What a promotion produced.
 *
 * `dropped` and `manifestBlock` are the whole point of the screen that shows
 * this, and neither may be summarised away. `dropped` is never empty: every
 * promotion carries at least the note that the expectation is the goal rather
 * than a passing page's words. And until the block is in the repository's
 * antifailure.yaml, `af test --only` cannot find the workflow this version
 * selects, so a promotion that returned a slug and nothing else would look
 * finished and would not be.
 */
export interface Promotion {
  slug: string | null;
  version: number | null;
  /** False when the workload already existed and this added a version to it. */
  created: boolean;
  dropped: string[];
  manifestBlock: string | null;
}

export async function promoteExploration(
  input: {
    repository: string;
    slug?: string;
    fromRunId?: string;
    exploration: unknown;
    persona?: string;
  },
  csrf: string,
): Promise<Promotion> {
  const raw = obj(await mutate<unknown>("workloads.promote", input, csrf));
  return {
    slug: str(raw?.slug),
    version: num(raw?.version),
    created: bool(raw?.created) ?? false,
    dropped: list(raw?.dropped, str),
    manifestBlock: str(raw?.manifest_block ?? raw?.manifestBlock),
  };
}

/**
 * What a start request may carry.
 *
 * Three fields, and every knob is missing on purpose. Duration, scale, seed
 * and concurrency live in the VERSION, so changing the scale makes a new
 * version and comparing scale 1 against scale 4 is comparing two versions
 * rather than two runs whose settings are only recorded in a form somebody has
 * closed.
 *
 * `version` absent means the latest, which is what a Run button means.
 */
export interface StartInput {
  slug: string;
  envId: string;
  version?: number;
  /** Makes a repeated request one run. The console sends the same value on a
   *  double click; without one every call is a new run. */
  requestKey?: string;
}

export interface Started {
  runId: string | null;
  /** False when the run was recorded and GitHub was not asked, which happens
   *  when an identical request had already been made. */
  dispatched: boolean;
  note: string | null;
}

export async function startRun(input: StartInput, csrf: string): Promise<Started> {
  const raw = obj(await mutate<unknown>("workloads.start", input, csrf));
  return {
    runId: str(raw?.runId),
    dispatched: bool(raw?.dispatched) ?? false,
    note: str(raw?.note) ?? str(raw?.pending),
  };
}

export interface Cancelled {
  /** True when the run had not been claimed by anything, so it is already
   *  over. False means a command is waiting for a runtime to confirm. */
  stopped: boolean;
  commandId: string | null;
  alreadyRequested: boolean;
}

export async function cancelRun(
  input: { runId: string; reason?: string },
  csrf: string,
): Promise<Cancelled> {
  const raw = obj(await mutate<unknown>("workloads.cancel", input, csrf));
  return {
    stopped: bool(raw?.stopped) ?? false,
    commandId: str(raw?.commandId),
    alreadyRequested: bool(raw?.alreadyRequested) ?? false,
  };
}

export async function retryRun(runId: string, csrf: string): Promise<Started> {
  const raw = obj(await mutate<unknown>("workloads.retry", { runId }, csrf));
  return {
    runId: str(raw?.runId),
    dispatched: bool(raw?.dispatched) ?? false,
    note: str(raw?.note) ?? str(raw?.pending),
  };
}
