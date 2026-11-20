// Recording control plane journeys in Agentrail, our vendor, and nothing else.
//
// Off unless both credentials are set, like the PostHog sink beside this file.
// The founder rule decides the shape of everything below: this product sells
// data safety, so a customer identifier may never reach the vendor.
//
// THE BOUNDARY IS TWO THINGS, AND BOTH ARE LOAD BEARING.
//
// The first is the inbound allowlist. The SDK exports the full path of every
// request it records, so anything outside the list is refused rather than
// filtered later.
//
// The second is that NO OUTBOUND CALL IS INSTRUMENTED AT ALL. This is the part
// that is easy to get wrong. `init()` wires Undici instrumentation and the
// outgoing half of Http instrumentation, so a route it recorded also carried
// every call made while serving it: `/auth/github/callback` reached GitHub at
// `/orgs/<customer-org>/memberships/<login>`, and a hosted MCP tool call
// reached `/repos/<owner>/<repo>/...`. Both would arrive at the vendor with the
// customer's names in them, one allowlisted route away from the allowlist
// meaning nothing.
//
// So this does its own OpenTelemetry wiring: the same providers `init()` builds,
// with `ignoreOutgoingRequestHook` refusing every outgoing node:http request and
// no Undici instrumentation created at all. MCP steps are recorded, because the
// adapter observes messages rather than the network.
//
// A failure here must never reach a request. Both callers are load bearing: a
// sign in and a customer's MCP client. A vendor having a bad day is not a
// reason for either to fail.

import { createProcessors, type AgentrailNode, type CaptureError } from 'agentrail-node'
import { mcp } from 'agentrail-node/mcp'
import { context, propagation, trace } from '@opentelemetry/api'
import { logs } from '@opentelemetry/api-logs'
import { AsyncLocalStorageContextManager } from '@opentelemetry/context-async-hooks'
import { AlwaysOnSampler, TracerProvider } from '@opentelemetry/sdk-trace'
import { LoggerProvider } from '@opentelemetry/sdk-logs'
import { HttpInstrumentation } from '@opentelemetry/instrumentation-http'
import { createRequire, syncBuiltinESMExports } from 'node:module'
import type { IncomingMessage } from 'node:http'

/** Where OTLP goes when a deployment has not named a collector of its own. */
export const DEFAULT_AGENTRAIL_ENDPOINT = 'https://api.agentrail.co/ingest/otlp'

export interface AgentrailConfig {
  readonly secret: string
  readonly hashKey: string
  readonly endpoint: string
}

/**
 * The configuration, or null for a deployment that has not configured Agentrail.
 *
 * One credential alone THROWS, which refuses to start. That is the same rule the
 * mail pair in boot.ts applies, and for the same reason: a secret with no hash
 * key, or a hash key with no secret, is a process that believes it is reporting
 * something and reports nothing. Refusing at start-up is the only failure an
 * operator sees at the moment they can fix it.
 */
export function agentrailConfigFrom(env: NodeJS.ProcessEnv): AgentrailConfig | null {
  const secret = env.AF_AGENTRAIL_SECRET?.trim()
  const hashKey = env.AF_AGENTRAIL_HASH_KEY?.trim()
  if (!secret && !hashKey) return null

  if (!secret || !hashKey) {
    const missing = secret ? 'AF_AGENTRAIL_HASH_KEY' : 'AF_AGENTRAIL_SECRET'
    throw new Error(
      `Agentrail is half configured: ${missing} is not set. Set both ` +
        'AF_AGENTRAIL_SECRET and AF_AGENTRAIL_HASH_KEY, or neither to turn it off. ' +
        'A secret without a hash key cannot hash an identity, and a hash key without ' +
        'a secret has nowhere to send anything.',
    )
  }

  return {
    secret,
    hashKey,
    endpoint: env.AF_AGENTRAIL_ENDPOINT?.trim() || DEFAULT_AGENTRAIL_ENDPOINT,
  }
}

/**
 * Every path that may be recorded, and nothing else.
 *
 * An entry ending in `*` is a prefix and everything else is exact, because the
 * alternative is a regular expression over the whole path and a `.*` in one is
 * how a path allowlist stops bounding anything. The query is stripped before
 * the match, so a session id on the end of `/mcp` does not change the answer.
 *
 * `/auth/device/*` and `/auth/mcp/*` are prefixes because their sub-routes are
 * the protocol. The two well known documents are prefixes because a client may
 * append its own suffix, and they are metadata rather than customer data.
 */
export const AGENTRAIL_ALLOWLIST = [
  '/mcp',
  '/auth/github',
  '/auth/github/callback',
  '/auth/email',
  '/auth/email/callback',
  '/auth/device/*',
  '/auth/mcp/*',
  '/.well-known/oauth-protected-resource*',
  '/.well-known/oauth-authorization-server*',
] as const

/** The predicate, exported so the boundary is unit testable without a server.
 *  `url` is what Node gives an IncomingMessage: a path with its query. */
export function agentrailPathAllowed(url: string | undefined | null): boolean {
  if (!url) return false
  const path = url.split('?', 1)[0] ?? ''
  if (!path.startsWith('/')) return false
  for (const entry of AGENTRAIL_ALLOWLIST) {
    if (entry.endsWith('*')) {
      if (path.startsWith(entry.slice(0, -1))) return true
    } else if (path === entry) {
      return true
    }
  }
  return false
}

/** What the rest of the server talks to. Always present, so no call site needs
 *  an `if`, and the off case is the same shape returning the same values. */
export interface AgentrailFacade {
  readonly enabled: boolean
  /** Hashes the id locally and exports the hash. Never awaited and never
   *  throws, because it runs inside a sign-in response. */
  identifyUser(userId: string): void
  /** Wraps an MCP transport so its messages are recorded as steps. */
  mcpTransport<T>(transport: T): T
  /** Flushes what is queued. Awaited only at shutdown. */
  shutdown(): Promise<void>
}

/** A facade that sends nothing. Exported so a caller with no configuration can
 *  use exactly the same code path rather than a branch that is never tested. */
export const NO_AGENTRAIL: AgentrailFacade = {
  enabled: false,
  identifyUser() {},
  mcpTransport<T>(transport: T): T {
    return transport
  },
  async shutdown() {},
}

export interface AgentrailOptions {
  /** The service name Agentrail groups this process's steps under. */
  readonly serviceName: string
  /** Extra observation of capture errors, for a test that needs to assert one
   *  did not happen. The code is still logged either way. */
  readonly onError?: (error: CaptureError) => void
}

/**
 * Starts the recorder and returns the facade.
 *
 * Everything here mirrors agentrail-node's own `init()`, with two deliberate
 * differences: outgoing node:http requests are ignored, and no Undici
 * instrumentation exists. See the header for why.
 *
 * `createProcessors` touches no globals, so a failure part way through leaves
 * only what this function installed, and each check below undoes what it can
 * before throwing. A process that cannot wire this must not start believing it
 * is recording.
 */
export async function startAgentrail(
  config: AgentrailConfig | null,
  options: AgentrailOptions,
): Promise<AgentrailFacade> {
  if (!config) return NO_AGENTRAIL

  const sdk = createProcessors({
    collector: { endpoint: config.endpoint, secret: config.secret },
    hashKey: config.hashKey,
    serviceName: options.serviceName,
    // The allowlist, inverted, because the SDK asks what to leave out.
    ignoreIncomingRequest: (request: IncomingMessage) => !agentrailPathAllowed(request.url),
    // The code only. An export failure's message can quote an address or a
    // body, and this log outlives the request it came from.
    onError: (error) => {
      options.onError?.(error)
      console.error(`agentrail: ${error.code} (${error.signal}, ${error.count})`)
    },
  })

  const provider = new TracerProvider({
    sampler: sdk.sampler(new AlwaysOnSampler()),
    spanProcessors: [sdk.spanProcessor],
  })
  if (!trace.setGlobalTracerProvider(provider)) {
    await sdk.client.shutdown()
    throw new Error(
      'Agentrail cannot start: a global tracer provider is already installed. ' +
        'Wire createProcessors into that provider instead of starting Agentrail twice.',
    )
  }

  const manager = new AsyncLocalStorageContextManager().enable()
  if (!context.setGlobalContextManager(manager)) {
    trace.disable()
    manager.disable()
    await provider.shutdown()
    throw new Error('Agentrail cannot start: a global context manager is already installed.')
  }

  if (!propagation.setGlobalPropagator(sdk.propagator)) {
    trace.disable()
    context.disable()
    manager.disable()
    await provider.shutdown()
    throw new Error('Agentrail cannot start: a global propagator is already installed.')
  }

  const logger = new LoggerProvider({ processors: [sdk.logProcessor] })
  if (logs.setGlobalLoggerProvider(logger) !== logger) {
    trace.disable()
    context.disable()
    propagation.disable()
    manager.disable()
    await Promise.all([provider.shutdown(), logger.shutdown()])
    throw new Error('Agentrail cannot start: a global logger provider is already installed.')
  }

  const http = new HttpInstrumentation({
    ...sdk.httpOptions,
    // THE SECOND HALF OF THE BOUNDARY. Inbound requests are recorded when the
    // allowlist admits them; everything this process calls out to is not
    // recorded at all, so no GitHub org, repository or membership path can ride
    // along on an allowlisted step.
    ignoreOutgoingRequestHook: () => true,
  })
  http.setTracerProvider(provider)

  // Load the core modules through the instrumentation loader, then refresh
  // Node's ESM bindings. Same two lines as init.js, and each one matters: the
  // first gives the instrumentation a module to patch, the second makes a
  // binding imported before this point see the patch.
  const require = createRequire(import.meta.url)
  require('node:http')
  require('node:https')
  syncBuiltinESMExports()

  let stopping: Promise<void> | undefined
  const stop = (): Promise<void> => {
    stopping ??= (async () => {
      http.disable()
      await sdk.client.flush()
      await Promise.all([provider.shutdown(), logger.shutdown()])
      manager.disable()
    })()
    return stopping
  }

  // The MCP adapter finds the runtime through the client it is given, so it is
  // the client createProcessors returned and not a copy of it.
  const capture = mcp({ client: sdk.client })

  return {
    enabled: true,

    identifyUser(userId) {
      try {
        // The SDK hashes this before export, so the vendor never sees the id.
        sdk.client.identify({ kind: 'user', id: userId })
      } catch (error) {
        // Called from inside a sign-in response. Nothing here may throw into it.
        console.error('agentrail: identify failed', error instanceof Error ? error.name : 'unknown')
      }
    },

    mcpTransport<T>(transport: T): T {
      return capture.transport(transport)
    },

    async shutdown() {
      try {
        await stop()
      } catch {
        // A vendor flush must not turn a clean shutdown into a failed deploy.
      }
    },
  }
}

/** The startup line, printed either way. "Recording" and "not recording" are
 *  two states an operator most needs to be sure about, and they look identical
 *  from outside the process. */
export function agentrailSummary(facade: AgentrailFacade): string {
  return facade.enabled
    ? 'control plane journeys are reported to Agentrail: sign-ins, the auth routes, hosted ' +
        'MCP calls, and the paths of requests to those routes only. No organization id, slug, ' +
        'repository name or environment id, and no outbound call, MCP argument or result'
    : 'nothing is reported to Agentrail from this process: AF_AGENTRAIL_SECRET or ' +
        'AF_AGENTRAIL_HASH_KEY is not set'
}

/** Kept for the type the facade is built from, so a change to the client's
 *  identify signature is a compile error here rather than at a call site. */
export type AgentrailClient = AgentrailNode
