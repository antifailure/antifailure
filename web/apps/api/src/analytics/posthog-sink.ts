// What the HOSTED control plane tells PostHog about itself.
//
// SEPARATE FROM THE PROXY BESIDE IT, AND THE DIFFERENCE IS THE WHOLE POINT.
// posthog.ts forwards a BROWSER'S requests so a reader of the marketing site
// connects to our host rather than a vendor's. This sends events from THIS
// PROCESS about its own hosted service. Nothing here goes through that proxy,
// and routing it through would be a loop with no purpose: the proxy exists
// because a browser has content blockers and a reader has an address worth not
// disclosing, and a server process on our own infrastructure has neither.
//
// THIS IS OUR OWN SERVICE AND OUR OWN HOSTED USERS. That is what makes it ours
// to measure. Read the next paragraph before adding a second one of these
// anywhere.
//
// NEVER PUT A SINK LIKE THIS IN engine/. THE ENGINE RUNS ON A CUSTOMER'S OWN
// MACHINE. `af mcp` is their process, on their hardware, inside their network,
// and `engine/internal/telemetry` already exists for it: it requires a redactor
// before any sink may write, and it exports to THE CUSTOMER'S OWN control
// plane. A path from there to our PostHog would be a new outbound flow nobody
// consented to, out of a product whose entire pitch is that data stays inside
// the customer's boundary, and it would be indefensible on the day somebody
// read the network log. If engine side MCP numbers are ever wanted they travel
// the route that already exists, to the customer's control plane, and only a
// HOSTED control plane forwards anything onward.
//
// WHAT NEVER REACHES HERE, and it is enforced by the shape of the arguments
// rather than by a rule somebody has to remember.
//
// NOT A TOOL'S ARGUMENTS AND NOT ITS RESULTS. A tool call carries project
// identifiers, hostnames, table names, SQL and error text, all of it the
// customer's; `inspect_recorded_egress` alone would ship a customer's outbound
// destinations to a vendor. The tool NAME is a closed set of eight strings this
// repository chose, so it says which capability was used and nothing about who
// used it on what.
//
// NOT A PROMPT AND NOT A COMPLETION. Those are the customer's input and the
// model's answer about it, and they are the single worst thing that could end
// up in a vendor's event store. PostHog's own schema makes `$ai_input` and
// `$ai_output_choices` optional for exactly this reason, so omitting them is
// the supported shape rather than a workaround.
//
// NOT AN ORGANIZATION IDENTIFIER. The surrogate is the same domain separated
// HMAC analytics/record.ts computes for its own store, so the identifier PostHog
// holds is the identifier our own analytics holds, and neither is an org_id.
// That is also why it is reused rather than recomputed: two pseudonyms for one
// organization would be two things to correlate and one more place to leak the
// real one.
//
// A FAILURE HERE MUST NEVER REACH A CALLER. Both call sites are load bearing:
// one is the hosted MCP surface a customer's agent is talking to, and the other
// is the model proxy that spends their money. An analytics vendor being slow or
// down is not a reason for either to fail, so every method below returns void,
// swallows its own errors, and is never awaited on a request path.

import { PostHog } from 'posthog-node'
import { POSTHOG_REGIONS, type PostHogRegion } from './posthog.ts'

/**
 * The eight tools the hosted MCP surface registers.
 *
 * A CLOSED SET, checked at the call site, so this cannot become a free text
 * field by somebody passing a variable. It is the same discipline
 * analytics/catalog.ts applies to its own payloads and for the same reason: a
 * field whose whole domain can be written down is a field a reviewer can
 * approve once, and a string field is one nobody can.
 */
export const MCP_TOOLS = [
  'list_projects',
  'list_environments',
  'list_runs',
  'get_run',
  'inspect_recorded_egress',
  'start_environment',
  'run_workflows',
  'stop_environment',
] as const
export type McpTool = (typeof MCP_TOOLS)[number]

/** How a tool call ended. Never the error text: a message from a tRPC procedure
 *  can quote a repository name or an environment identifier. */
export const MCP_OUTCOMES = ['ok', 'error', 'refused'] as const
export type McpOutcome = (typeof MCP_OUTCOMES)[number]

export interface McpToolCall {
  tool: McpTool
  outcome: McpOutcome
  durationMs: number
  /** The pseudonym, from analytics.surrogate. Null when analytics is off, which
   *  is the only case where this sends nothing rather than sending anonymous. */
  orgSurrogate: string | null
}

export interface AiGeneration {
  model: string
  provider: string
  inputTokens: number
  outputTokens: number
  /** Measured around the outbound call, in milliseconds. Converted to SECONDS
   *  on the way out, because that is what PostHog's schema means by
   *  `$ai_latency`, and sending milliseconds into a seconds field is a silent
   *  thousand fold error on a dashboard nobody would question. */
  latencyMs: number
  traceId: string
  costUsd: number | null
  orgSurrogate: string | null
}

export interface PostHogSink {
  /** False when no project key is configured. Every method still works and
   *  sends nothing, so a caller never has to check. Same contract as
   *  analytics/record.ts, deliberately. */
  readonly enabled: boolean
  mcpToolCalled(call: McpToolCall): void
  aiGenerated(generation: AiGeneration): void
  /** Flushes what is queued. Awaited only at shutdown, never on a request. */
  shutdown(): Promise<void>
}

export interface PostHogSinkOptions {
  /** The PostHog project API key. Public by design: it can only write events
   *  into one project and reads nothing back. */
  projectKey: string | null
  region: PostHogRegion | null
  /** Overridden in tests, so nothing here reaches a real PostHog. */
  client?: Pick<PostHog, 'capture' | 'shutdown'>
}

/** A sink that sends nothing, so a disabled deployment has no branch at any
 *  call site. The alternative is `if (sink)` at every producer, which is the
 *  check somebody eventually forgets. */
const SILENT: PostHogSink = {
  enabled: false,
  mcpToolCalled() {},
  aiGenerated() {},
  async shutdown() {},
}

export function createPostHogSink(options: PostHogSinkOptions): PostHogSink {
  const { projectKey, region } = options
  if (!options.client && (!projectKey || !region)) return SILENT

  const client =
    options.client ??
    new PostHog(projectKey!, {
      host: POSTHOG_REGIONS[region!].ingestion,
      // Batched, because a hosted MCP session makes a burst of tool calls and
      // one HTTP request per call would put a vendor's latency on the critical
      // path of a customer's agent.
      flushAt: 20,
      flushInterval: 10_000,
    })

  /** Every send goes through here, so there is one place a failure is contained
   *  rather than eight. posthog-node queues rather than awaits, but a
   *  constructor failure, a serialisation failure or a queue at its bound all
   *  throw synchronously, and any one of them reaching the MCP surface would
   *  turn a vendor's bad day into a customer's. */
  function send(event: string, distinctId: string, properties: Record<string, unknown>): void {
    try {
      client.capture({ distinctId, event, properties })
    } catch {
      // Deliberately silent, and deliberately not counted here. A counter would
      // be a second thing to get wrong on this path; the process already
      // reports what it can reach at start-up.
    }
  }

  return {
    enabled: true,

    mcpToolCalled(call) {
      // No surrogate means analytics is off, and an event with no organization
      // at all would be a row saying only "somebody used a tool". That is not
      // worth a request to a vendor, so nothing is sent.
      if (!call.orgSurrogate) return
      send('mcp_tool_called', call.orgSurrogate, {
        tool: call.tool,
        outcome: call.outcome,
        duration_ms: Math.round(call.durationMs),
        // Grouped so a dashboard can read the hosted surface separately from
        // the marketing site, which is the other producer in this project.
        surface: 'hosted_mcp',
      })
    },

    aiGenerated(generation) {
      if (!generation.orgSurrogate) return
      send('$ai_generation', generation.orgSurrogate, {
        $ai_model: generation.model,
        $ai_provider: generation.provider,
        $ai_input_tokens: generation.inputTokens,
        $ai_output_tokens: generation.outputTokens,
        // SECONDS. See latencyMs above.
        $ai_latency: generation.latencyMs / 1000,
        $ai_trace_id: generation.traceId,
        ...(generation.costUsd === null ? {} : { $ai_total_cost_usd: generation.costUsd }),
        // Named absences. A reader of a PostHog trace who sees no prompt should
        // learn that it was withheld on purpose rather than wonder whether the
        // instrumentation is broken, which is the difference between a policy
        // and a bug that looks like one.
        af_input_withheld: true,
        af_output_withheld: true,
      })
    },

    async shutdown() {
      try {
        await client.shutdown()
      } catch {
        // Nothing to do about it at exit, and throwing here would turn a clean
        // shutdown into a non-zero status for an analytics flush.
      }
    },
  }
}

/** What to say at start-up. Both absences look exactly like working software. */
export function postHogSinkSummary(sink: PostHogSink): string {
  return sink.enabled
    ? 'hosted MCP tool calls and brokered model calls are reported to PostHog: tool name, outcome, ' +
        'duration, token counts and a pseudonymous organization, never an argument, a result, a ' +
        'prompt or a completion'
    : 'nothing is reported to PostHog from this process: AF_POSTHOG_PROJECT_KEY or ' +
        'AF_POSTHOG_REGION is not set'
}
