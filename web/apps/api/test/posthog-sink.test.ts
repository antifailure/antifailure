// What the hosted control plane tells PostHog, and everything it must not.
//
// THE ASSERTIONS THAT MATTER HERE ARE THE ABSENCES. That a tool call is
// reported is one line and would be obvious if it broke. That a tool's
// ARGUMENTS are not reported is invisible when it breaks, arrives as a
// customer's hostnames and SQL sitting in a vendor's event store, and is
// discovered by somebody outside this company. So most of what is below drives
// a real payload through the real sink and asserts on what came out, rather
// than reading the code and agreeing with it.
//
// It is a fake client rather than a fake network. The seam is the same one
// providers/proxy.ts uses for a provider: what is under test is what this
// process DECIDES to send, and posthog-node's own transport is not ours to
// test.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createPostHogSink, postHogSinkSummary, MCP_TOOLS, MCP_OUTCOMES } from '../src/analytics/posthog-sink.ts'

interface Captured {
  distinctId: string
  event: string
  properties: Record<string, unknown>
}

function sinkWithFake(behaviour?: () => void) {
  const sent: Captured[] = []
  let flushed = 0
  const client = {
    capture(payload: unknown) {
      behaviour?.()
      sent.push(payload as Captured)
    },
    async shutdown() {
      flushed += 1
    },
  }
  const sink = createPostHogSink({
    projectKey: 'phc_public',
    region: 'us',
    client: client as never,
  })
  return { sink, sent, flushes: () => flushed }
}

const ORG = 'a'.repeat(32)

describe('the hosted control plane reports its own MCP usage', () => {
  it('sends the tool, the outcome and the duration, keyed on the pseudonym', () => {
    const { sink, sent } = sinkWithFake()
    sink.mcpToolCalled({
      tool: 'inspect_recorded_egress',
      outcome: 'ok',
      durationMs: 132.7,
      orgSurrogate: ORG,
    })
    assert.equal(sent.length, 1)
    assert.equal(sent[0]!.event, 'mcp_tool_called')
    assert.equal(sent[0]!.distinctId, ORG)
    assert.deepEqual(sent[0]!.properties, {
      tool: 'inspect_recorded_egress',
      outcome: 'ok',
      duration_ms: 133,
      surface: 'hosted_mcp',
    })
  })

  it('sends the pseudonym and never anything that could be an organization id', () => {
    // The identifier is a 32 character hex surrogate from analytics.surrogate.
    // A uuid shaped value arriving here would mean somebody passed orgId, and
    // that is the mistake this exists to make visible.
    const { sink, sent } = sinkWithFake()
    sink.mcpToolCalled({ tool: 'list_runs', outcome: 'ok', durationMs: 1, orgSurrogate: ORG })
    assert.doesNotMatch(
      JSON.stringify(sent[0]),
      /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/,
      'something uuid shaped reached the sink, which is what an org_id looks like',
    )
  })

  it('sends nothing at all when there is no pseudonym to send', () => {
    // Analytics off. An event carrying no organization says only "somebody used
    // a tool", which is not worth a request to a vendor, and sending it
    // anonymous would be a row nobody can act on and a reader cannot audit.
    const { sink, sent } = sinkWithFake()
    sink.mcpToolCalled({ tool: 'list_runs', outcome: 'ok', durationMs: 1, orgSurrogate: null })
    assert.deepEqual(sent, [])
  })

  it('reports every outcome a call can have, including the refusal', () => {
    // A scope refusal is the one somebody forgets, and it is the interesting
    // one: a client repeatedly refused is a client that connected with the
    // wrong scopes and whose owner does not know.
    const { sink, sent } = sinkWithFake()
    for (const outcome of MCP_OUTCOMES) {
      sink.mcpToolCalled({ tool: 'run_workflows', outcome, durationMs: 5, orgSurrogate: ORG })
    }
    assert.deepEqual(
      sent.map((s) => s.properties.outcome),
      [...MCP_OUTCOMES],
    )
  })
})

describe('the hosted control plane reports a brokered model call', () => {
  it('uses PostHog\'s own generation shape, with latency in SECONDS', () => {
    // THE SILENT THOUSAND FOLD ERROR. PostHog's $ai_latency is documented in
    // seconds. Everything in this process measures in milliseconds. Sending
    // one into the other produces a dashboard that is wrong by a factor of a
    // thousand and looks entirely plausible, because nobody knows offhand what
    // a model call should cost in either unit.
    const { sink, sent } = sinkWithFake()
    sink.aiGenerated({
      model: 'claude-opus-5',
      provider: 'anthropic',
      inputTokens: 1200,
      outputTokens: 340,
      latencyMs: 2500,
      traceId: 'trace-1',
      costUsd: 0.042,
      orgSurrogate: ORG,
    })
    assert.equal(sent.length, 1)
    assert.equal(sent[0]!.event, '$ai_generation')
    assert.equal(sent[0]!.properties.$ai_latency, 2.5)
    assert.equal(sent[0]!.properties.$ai_model, 'claude-opus-5')
    assert.equal(sent[0]!.properties.$ai_provider, 'anthropic')
    assert.equal(sent[0]!.properties.$ai_input_tokens, 1200)
    assert.equal(sent[0]!.properties.$ai_output_tokens, 340)
    assert.equal(sent[0]!.properties.$ai_trace_id, 'trace-1')
    assert.equal(sent[0]!.properties.$ai_total_cost_usd, 0.042)
  })

  it('omits the cost rather than sending a zero the provider never reported', () => {
    const { sink, sent } = sinkWithFake()
    sink.aiGenerated({
      model: 'gpt-x', provider: 'openai', inputTokens: 1, outputTokens: 1,
      latencyMs: 10, traceId: 't', costUsd: null, orgSurrogate: ORG,
    })
    assert.ok(!('$ai_total_cost_usd' in sent[0]!.properties))
  })

  it('carries no prompt and no completion, and says the absence is deliberate', () => {
    // THE WORST THING THAT COULD END UP IN A VENDOR'S EVENT STORE. A prompt is
    // the page a customer's application rendered; a completion is what a model
    // said about it. PostHog's schema makes $ai_input and $ai_output_choices
    // optional, so leaving them out is the supported shape and not a hack.
    const { sink, sent } = sinkWithFake()
    sink.aiGenerated({
      model: 'claude-opus-5', provider: 'anthropic', inputTokens: 1, outputTokens: 1,
      latencyMs: 1, traceId: 't', costUsd: 1, orgSurrogate: ORG,
    })
    const properties = sent[0]!.properties
    assert.ok(!('$ai_input' in properties), 'the prompt was sent')
    assert.ok(!('$ai_output_choices' in properties), 'the completion was sent')
    // Named absences, so a reader of a trace learns it was withheld on purpose
    // rather than wondering whether the instrumentation is broken.
    assert.equal(properties.af_input_withheld, true)
    assert.equal(properties.af_output_withheld, true)
  })

  it('sends nothing when there is no pseudonym', () => {
    const { sink, sent } = sinkWithFake()
    sink.aiGenerated({
      model: 'm', provider: 'anthropic', inputTokens: 1, outputTokens: 1,
      latencyMs: 1, traceId: 't', costUsd: 1, orgSurrogate: null,
    })
    assert.deepEqual(sent, [])
  })
})

describe('the sink cannot break the thing it is measuring', () => {
  it('swallows a failing client rather than throwing into an MCP tool call', () => {
    // BOTH CALL SITES ARE LOAD BEARING. One is the hosted MCP surface a
    // customer's agent is talking to; the other is the proxy that spends their
    // money. A vendor being slow or down is not a reason for either to fail.
    const { sink } = sinkWithFake(() => {
      throw new Error('posthog is having a bad day')
    })
    assert.doesNotThrow(() =>
      sink.mcpToolCalled({ tool: 'list_runs', outcome: 'ok', durationMs: 1, orgSurrogate: ORG }),
    )
    assert.doesNotThrow(() =>
      sink.aiGenerated({
        model: 'm', provider: 'anthropic', inputTokens: 1, outputTokens: 1,
        latencyMs: 1, traceId: 't', costUsd: 1, orgSurrogate: ORG,
      }),
    )
  })

  it('swallows a failing flush rather than turning a clean shutdown into a bad exit', async () => {
    const sink = createPostHogSink({
      projectKey: 'phc_public',
      region: 'us',
      client: {
        capture() {},
        shutdown: async () => {
          throw new Error('unreachable')
        },
      } as never,
    })
    await assert.doesNotReject(() => sink.shutdown())
  })

  it('flushes what it batched, or a deploy loses the last ten seconds', async () => {
    const { sink, flushes } = sinkWithFake()
    await sink.shutdown()
    assert.equal(flushes(), 1)
  })
})

describe('the sink is off unless it is configured', () => {
  it('sends nothing and says so when no project key is set', () => {
    const sink = createPostHogSink({ projectKey: null, region: 'us' })
    assert.equal(sink.enabled, false)
    assert.match(postHogSinkSummary(sink), /nothing is reported to PostHog/)
    // Every method still works and does nothing, so no call site needs a
    // branch. The alternative is `if (sink)` at every producer, which is the
    // check somebody eventually forgets.
    assert.doesNotThrow(() =>
      sink.mcpToolCalled({ tool: 'list_runs', outcome: 'ok', durationMs: 1, orgSurrogate: ORG }),
    )
  })

  it('sends nothing when a key is set and no region is', () => {
    // A key with no region has nowhere to go, and defaulting to a cloud would
    // pick a continent on the operator's behalf.
    const sink = createPostHogSink({ projectKey: 'phc_public', region: null })
    assert.equal(sink.enabled, false)
  })

  it('says what it will and will not send, out loud, when it is on', () => {
    const { sink } = sinkWithFake()
    const said = postHogSinkSummary(sink)
    assert.match(said, /tool name, outcome, duration/)
    assert.match(said, /never an argument, a result, a prompt or a completion/)
  })
})

describe('the tool list is the tool list', () => {
  it('names every tool the hosted MCP surface actually registers', async () => {
    // THE GATE THAT STOPS THIS BECOMING A FREE TEXT FIELD. MCP_TOOLS is a
    // closed set so that a tool name can never be a variable somebody passes,
    // and a closed set that has drifted from the real surface is worse than no
    // set: a tool added later would either fail to type check, which is the
    // good case, or be reported under a name nothing serves.
    const source = await import('node:fs/promises').then((fs) =>
      fs.readFile(new URL('../src/mcp.ts', import.meta.url), 'utf8'),
    )
    const registered = [...source.matchAll(/registerTool\('([a-z_]+)'/g)].map((m) => m[1]!)
    assert.ok(registered.length > 0, 'no tool was found in mcp.ts, so this checked nothing')
    assert.deepEqual(
      [...registered].sort(),
      [...MCP_TOOLS].sort(),
      'the hosted MCP surface and the reported tool list disagree',
    )
  })

  it('reports every registered tool from its own call site', async () => {
    // Defined, wired, effective. A tool whose call site does not name itself
    // would report under a neighbour's name, and a dashboard would be
    // confidently wrong rather than empty.
    const source = await import('node:fs/promises').then((fs) =>
      fs.readFile(new URL('../src/mcp.ts', import.meta.url), 'utf8'),
    )
    for (const tool of MCP_TOOLS) {
      assert.match(
        source,
        new RegExp(`call\\('${tool}',`),
        `${tool} is registered and its handler does not report itself, so its calls are counted ` +
          'as some other tool or not at all',
      )
    }
  })
})
