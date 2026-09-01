// What a workload version is allowed to say, per kind, and what that turns
// into when it is dispatched.
//
// THE CORRECTION THAT SHAPED THIS FILE.
//
// The obvious design is that a version carries the definition: a scenario
// document, a workflow with steps and expectations, a traffic shape. That is
// what the marketing site implies, and it is wrong about this product.
//
// Every one of the four things an engine can run is DECLARED IN THE CUSTOMER'S
// MANIFEST and selected by name on the command line. `af load scenario --only
// checkout` runs the scenario called checkout out of antifailure.yaml. `af test
// --only sign-up` runs the workflow called sign-up. `af explore --only upgrade`
// walks the goal called upgrade. None of them takes a document, and none of
// them could: a scenario is checked against the manifest's safe route list
// before anything is sent, and a control plane that could hand an engine an
// arbitrary journey to send at a customer's environment would be a control
// plane that can send traffic the customer never allowed.
//
// So a version is a SELECTION plus the knobs the command actually has. That is
// smaller than it first appears to be and it is the whole of what is real:
// running the same selection at scale 1 and at scale 4 is two versions, and
// comparing their runs is the thing Studio is for.
//
// The four kinds stay four kinds here as well as in the schema. There is no
// shared body type, no shared field set and no compiler between them, because
// they measure materially different things: a mix has no order, a journey has
// no browser, a workflow has no request rate, and an exploration has no pass.

import { createHash } from 'node:crypto'
import { z } from 'zod'

/** The four kinds, in the order the enum declares them. */
export const WORKLOAD_KINDS = [
  'observed_load',
  'http_scenario',
  'browser_workflow',
  'exploration',
] as const

export type WorkloadKind = (typeof WORKLOAD_KINDS)[number]

/** A name a manifest can carry. Bounded because it lands in a workflow input
 *  and in a command line, and unbounded text in either is not a name. */
const declaredName = z.string().min(1).max(200)

/**
 * Names selected out of the manifest.
 *
 * Empty is meaningful for the two commands that treat it as "all of them",
 * and is refused for the two that do not, which is why the minimum is set per
 * kind below rather than here.
 */
const selection = z.array(declaredName).max(50)

/**
 * `af load run`, whose only flags are `--duration` and `--scale`.
 *
 * There is no selection: the shape comes from whatever the manifest points at,
 * which is an OTLP export or an access log. Naming a profile here would be an
 * input the command has no flag for, which is the dead socket routers/
 * dispatch.ts refuses to send.
 */
const observedLoad = z
  .object({
    /** Seconds, sent as a Go duration. Bounded by what `load.run` accepts. */
    durationSeconds: z.number().int().min(1).max(3600).optional(),
    /** Multiplier on production's rate. */
    scale: z.number().min(0.01).max(100).optional(),
  })
  .strict()

/** `af load scenario --only <names> --seed <n> --concurrency <n>`. */
const httpScenario = z
  .object({
    /** Scenario names from the manifest. At least one: the command's own
     *  default is every scenario, and a workload that means "everything" should
     *  say so by listing them rather than by being empty, because the manifest
     *  gaining a scenario would silently change what this workload runs. */
    select: selection.min(1),
    /** Makes two runs send the same schedule. The command's default is 1. */
    seed: z.number().int().min(0).max(2_147_483_647).optional(),
    concurrency: z.number().int().min(1).max(500).optional(),
  })
  .strict()

/**
 * `af test --only <names>`.
 *
 * `manifestBlock` and `dropped` are written only by a promotion. They are here
 * rather than in a table of their own because they are part of what the version
 * IS: a promoted version's whole value is the block a person pastes into their
 * manifest and the honest list of what the compilation could not carry.
 */
const browserWorkflow = z
  .object({
    /** Workflow names from the manifest. Empty means every workflow, which is
     *  what `af test` with no --only does and what `af ci` does. */
    select: selection,
    /** The manifest block a promoted workflow has to be pasted into the
     *  repository as before `select` can find it. */
    manifestBlock: z.string().max(20_000).optional(),
    /** What the compilation deliberately did not carry over, one sentence
     *  each. Never empty when it is present. */
    dropped: z.array(z.string().min(1).max(600)).max(50).optional(),
  })
  .strict()

/** `af explore --only <goals> --seed <seed>`. */
const exploration = z
  .object({
    /** Goal names from the manifest. At least one, for the same reason a
     *  scenario selection needs one. */
    select: selection.min(1),
    /** The seed replaces the one the manifest declares, so a finding can be
     *  replayed exactly. A string rather than a number because that is what
     *  `--seed` takes here, unlike the load commands. */
    seed: z.string().min(1).max(200).optional(),
  })
  .strict()

export const WORKLOAD_BODY_SCHEMAS = {
  observed_load: observedLoad,
  http_scenario: httpScenario,
  browser_workflow: browserWorkflow,
  exploration,
} as const

export type ObservedLoadBody = z.infer<typeof observedLoad>
export type HttpScenarioBody = z.infer<typeof httpScenario>
export type BrowserWorkflowBody = z.infer<typeof browserWorkflow>
export type ExplorationBody = z.infer<typeof exploration>

export type WorkloadBody =
  | ObservedLoadBody
  | HttpScenarioBody
  | BrowserWorkflowBody
  | ExplorationBody

export interface ParsedBody {
  body: WorkloadBody
  digest: string
}

export class BodyRefused extends Error {}

/**
 * Validates a body against its kind and returns it with its digest.
 *
 * Strict on the write boundary: an unknown key is refused rather than dropped.
 * A misspelled `durationSecond` that is silently ignored is a workload that
 * runs and does not do what its author wrote, and the person reading the result
 * has no way to tell. That is the same decision the manifest parser makes with
 * KnownFields.
 */
export function parseBody(kind: WorkloadKind, raw: unknown): ParsedBody {
  const schema = WORKLOAD_BODY_SCHEMAS[kind]
  const parsed = schema.safeParse(raw)
  if (!parsed.success) {
    const first = parsed.error.issues[0]
    const where = first?.path.length ? first.path.join('.') : 'the body'
    throw new BodyRefused(
      `a ${kind} workload cannot say that: ${where}, ${first?.message ?? 'is not valid'}`,
    )
  }
  const body = parsed.data as WorkloadBody
  return { body, digest: digestOf(body) }
}

/**
 * sha256 of the body in a canonical form.
 *
 * Keys sorted at every depth, because two bodies that differ only in the order
 * JSON.stringify happened to emit their keys are the same definition, and a
 * digest that says otherwise makes "this save changed nothing" answer wrongly
 * on a round trip through the database.
 */
export function digestOf(body: unknown): string {
  return createHash('sha256').update(canonical(body)).digest('hex')
}

function canonical(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value ?? null)
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`
  const entries = Object.entries(value as Record<string, unknown>)
    .filter(([, v]) => v !== undefined)
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
  return `{${entries.map(([k, v]) => `${JSON.stringify(k)}:${canonical(v)}`).join(',')}}`
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

/**
 * The workflow_dispatch inputs a version turns into, or the reason it cannot
 * be dispatched.
 *
 * The inputs are exactly the flags the engine's CLI has and nothing else,
 * which is the rule routers/dispatch.ts states and the reason it sends no
 * runtime and no profile. An input the workflow cannot act on is a dead socket
 * one process along.
 */
export interface Dispatchable {
  inputs: Record<string, string>
  /** Whether a repository still carrying the four-input workflow this product
   *  shipped before Studio can take this dispatch. False means the customer has
   *  to copy the current examples/github-workflow.yml first, and the refusal
   *  GitHub sends back says so. */
  needsUpdatedWorkflow: boolean
}

/**
 * The four inputs `examples/github-workflow.yml` declared before Studio.
 *
 * Every one of them is sent on every dispatch, empty where it does not apply,
 * because GitHub keeps an omitted input's declared default and a default of
 * `up` arriving on a load dispatch would run the wrong command entirely.
 *
 * Sending an input the workflow does not declare is a 422 from GitHub, so the
 * two commands that need more than these four are marked as needing the newer
 * workflow rather than quietly failing against an older one.
 */
const LEGACY_INPUTS = { command: '', workflows: '', duration: '', scale: '' }

export function dispatchInputs(kind: WorkloadKind, body: WorkloadBody): Dispatchable {
  switch (kind) {
    case 'observed_load': {
      const b = body as ObservedLoadBody
      return {
        needsUpdatedWorkflow: false,
        inputs: {
          ...LEGACY_INPUTS,
          command: 'load',
          // A Go duration, because that is what `af load run --duration`
          // parses. Seconds on this side so the console cannot send `1 hour`
          // and have the engine refuse it after the job has started.
          duration: b.durationSeconds === undefined ? '' : `${b.durationSeconds}s`,
          scale: b.scale === undefined ? '' : String(b.scale),
        },
      }
    }
    case 'browser_workflow': {
      const b = body as BrowserWorkflowBody
      return {
        needsUpdatedWorkflow: false,
        inputs: { ...LEGACY_INPUTS, command: 'agents', workflows: b.select.join(',') },
      }
    }
    case 'http_scenario': {
      const b = body as HttpScenarioBody
      return {
        needsUpdatedWorkflow: true,
        inputs: {
          ...LEGACY_INPUTS,
          command: 'scenario',
          workflows: b.select.join(','),
          seed: b.seed === undefined ? '' : String(b.seed),
          concurrency: b.concurrency === undefined ? '' : String(b.concurrency),
        },
      }
    }
    case 'exploration': {
      const b = body as ExplorationBody
      return {
        needsUpdatedWorkflow: true,
        inputs: {
          ...LEGACY_INPUTS,
          command: 'explore',
          workflows: b.select.join(','),
          seed: b.seed ?? '',
        },
      }
    }
  }
}
