import type { Hono, Context as HttpContext } from 'hono'
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { WebStandardStreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/webStandardStreamableHttp.js'
import { TRPCError } from '@trpc/server'
import { z } from 'zod'
import { sql } from 'drizzle-orm'
import { bodyLimit } from 'hono/body-limit'
import { appRouter } from './routers/index.ts'
import type { Actor, Context } from './trpc.ts'
import { readCookie, SESSION_COOKIE, CSRF_HEADER, csrfMatches } from './auth/session.ts'
import {
  MCP_SCOPES, McpAuthorizationError, hostedMcpEndpoint, registerMcpClient,
  describeMcpAuthorization, approveMcpAuthorization, redeemMcpAuthorization,
  identifyMcpToken,
} from './auth/mcp.ts'

type BaseContext = Omit<Context, 'actor'>
interface Options {
  base: BaseContext
  actorFrom: (cookie: string | undefined) => Promise<Actor | null>
}

function uniqueParameters(parameters: URLSearchParams): Record<string, string> {
  const values: Record<string, string> = Object.create(null)
  for (const [key, value] of parameters) {
    if (Object.hasOwn(values, key)) throw new McpAuthorizationError('invalid_request', 'OAuth parameters must appear only once.')
    values[key] = value
  }
  return values
}

/** The hosted surface calls the same authorized procedures as the console. */
function toolServer(context: Context, scopes: string[]) {
  const server = new McpServer({ name: 'Antifailure', version: '1.0.0' })
  const caller = appRouter.createCaller(context)
  async function call(write: boolean, action: () => Promise<unknown>) {
    if (!scopes.includes(write ? 'mcp:write' : 'mcp:read')) {
      return { isError: true, content: [{ type: 'text' as const, text: 'Reconnect and approve the scope this tool needs.' }] }
    }
    try {
      const result = await action()
      return { content: [{ type: 'text' as const, text: JSON.stringify(result) }] }
    } catch (error) {
      const safe = error instanceof TRPCError && error.code !== 'INTERNAL_SERVER_ERROR'
        ? error.message : 'The control plane could not complete this request. Try again.'
      return { isError: true, content: [{ type: 'text' as const, text: safe }] }
    }
  }
  const read = { readOnlyHint: true, destructiveHint: false, openWorldHint: false }
  server.registerTool('list_projects', {
    description: 'List the repositories connected to your organization.', inputSchema: z.object({}).strict(), annotations: read,
  }, async () => call(false, () => caller.repositories.list({ includeArchived: false })))
  server.registerTool('list_environments', {
    description: 'List recorded environments. A ready port does not prove that application workflows passed.',
    inputSchema: z.object({ repository: z.string().max(300).optional(), cursor: z.string().max(300).optional(),
      limit: z.number().int().min(1).max(100).default(25) }).strict(), annotations: read,
  }, async (input) => call(false, () => caller.environments.list(input)))
  server.registerTool('list_runs', {
    description: 'Read reported runs. Missing or unfinished results are not passes.',
    inputSchema: z.object({ envId: z.string().max(200).optional(), before: z.string().max(200).optional(),
      limit: z.number().int().min(1).max(100).default(25) }).strict(), annotations: read,
  }, async (input) => call(false, () => caller.runs.recent(input)))
  server.registerTool('get_run', {
    description: 'Read one recorded run by its UUID. This does not invent a verdict for unfinished work.',
    inputSchema: z.object({ runId: z.string().uuid() }).strict(), annotations: read,
  }, async (input) => call(false, () => caller.runs.get(input)))
  server.registerTool('inspect_recorded_egress', {
    description: 'Read reported outbound host and mode counts. No recorded event is not proof of containment.',
    inputSchema: z.object({ envId: z.string().max(200).optional(),
      limit: z.number().int().min(1).max(100).default(25) }).strict(), annotations: read,
  }, async (input) => call(false, () => caller.network.decisions(input)))
  const workflow = z.string().max(100).regex(/^[A-Za-z0-9._-]+\.ya?ml$/).default('antifailure.yml')
  server.registerTool('start_environment', {
    description: 'Request an environment through the connected repository workflow. Returns a dispatch, not a ready environment. Existing permissions, plan limits and safety switches apply.',
    inputSchema: z.object({ repository: z.string().min(1).max(300), branch: z.string().min(1).max(255).optional(), workflow }).strict(),
    annotations: { readOnlyHint: false, destructiveHint: false, idempotentHint: false, openWorldHint: true },
  }, async (input) => call(true, () => caller.environments.create(input)))
  server.registerTool('run_workflows', {
    description: 'Dispatch every manifest workflow against a recorded environment through the customer repository. Results appear when the engine reports them.',
    inputSchema: z.object({ envId: z.string().min(1).max(200), workflow }).strict(),
    annotations: { readOnlyHint: false, destructiveHint: false, idempotentHint: false, openWorldHint: true },
  }, async (input) => call(true, () => caller.agents.run(input)))
  server.registerTool('stop_environment', {
    description: 'Request teardown. The environment is not gone until the runtime acknowledges cleanup.',
    inputSchema: z.object({ envId: z.string().min(1).max(200), reason: z.string().max(500).optional(), workflow }).strict(),
    annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: true, openWorldHint: true },
  }, async (input) => call(true, () => caller.environments.teardown(input)))
  return server
}

export function mountHostedMcp(app: Hono<any>, options: Options): void {
  const { base } = options
  // No request-derived issuer: a Host header must never mint its own audience.
  const resource = hostedMcpEndpoint(base.appBaseUrl)
  if (!resource) return
  const origin = resource.slice(0, -'/mcp'.length)
  const metadata = `${origin}/.well-known/oauth-protected-resource`
  const boundedBody = bodyLimit({ maxSize: 32 * 1024,
    onError: (c) => c.json({ error: 'invalid_request', error_description: 'The request is larger than 32 KiB.' }, 413) })
  app.use('/auth/mcp/*', boundedBody)
  app.use('/mcp', boundedBody)
  app.use('/auth/mcp/*', async (c, next) => { await next(); c.header('cache-control', 'no-store') })
  app.use('/mcp', async (c, next) => {
    const requestedOrigin = c.req.header('origin')
    if (requestedOrigin && requestedOrigin !== new URL(origin).origin) return c.json({ error: 'Untrusted request origin.' }, 403)
    await next()
    c.header('cache-control', 'no-store')
  })
  const errorResponse = (c: HttpContext, error: unknown) => {
    if (error instanceof SyntaxError) return c.json({ error: 'invalid_request', error_description: 'The body must be valid JSON.' }, 400)
    if (error instanceof McpAuthorizationError) return c.json({ error: error.code, error_description: error.message }, 400)
    throw error
  }
  app.get('/.well-known/oauth-protected-resource', (c) => c.json({
    resource, authorization_servers: [origin], scopes_supported: MCP_SCOPES,
    bearer_methods_supported: ['header'], resource_name: 'Antifailure',
  }))
  app.get('/.well-known/oauth-authorization-server', (c) => c.json({
    issuer: origin, authorization_endpoint: `${origin}/auth/mcp/authorize`,
    token_endpoint: `${origin}/auth/mcp/token`, registration_endpoint: `${origin}/auth/mcp/register`,
    response_types_supported: ['code'], grant_types_supported: ['authorization_code'],
    token_endpoint_auth_methods_supported: ['none'], code_challenge_methods_supported: ['S256'],
    scopes_supported: MCP_SCOPES,
  }))
  app.post('/auth/mcp/register', async (c) => {
    try { return c.json(await registerMcpClient(base.pool, await c.req.json()), 201) }
    catch (error) { return errorResponse(c, error) }
  })
  app.get('/auth/mcp/authorize', async (c) => {
    try {
      await describeMcpAuthorization(base.pool, uniqueParameters(new URL(c.req.url).searchParams), resource)
      return c.redirect(`${origin}/connect-mcp?${new URL(c.req.url).searchParams.toString()}`)
    } catch (error) { return errorResponse(c, error) }
  })
  app.get('/auth/mcp/pending', async (c) => {
    const actor = await options.actorFrom(c.req.header('cookie'))
    if (!actor) return c.json({ error: 'Sign in to an organization first.' }, 401)
    try {
      const approval = await describeMcpAuthorization(base.pool, uniqueParameters(new URL(c.req.url).searchParams), resource)
      const organizationName = await base.pool.withTenant({ orgId: actor.orgId, userId: actor.userId }, async (db) => {
        const rows = await db.execute<{ name: string }>(sql`SELECT name FROM organizations WHERE id = ${actor.orgId}::uuid`)
        return rows[0]?.name ?? 'Your organization'
      })
      return c.json({ clientName: approval.clientName, scopes: approval.scopes, organization: actor.orgId,
        organizationName, redirectUri: approval.request.redirect_uri, expiresInDays: 90 })
    } catch (error) { return errorResponse(c, error) }
  })
  app.post('/auth/mcp/approve', async (c) => {
    const actor = await options.actorFrom(c.req.header('cookie'))
    if (!actor) return c.json({ error: 'Sign in to an organization first.' }, 401)
    const cookie = readCookie(c.req.header('cookie'), SESSION_COOKIE)
    if (!cookie || !csrfMatches(cookie, c.req.header(CSRF_HEADER))) return c.json({ error: 'Refresh this page before approving.' }, 403)
    if (c.req.header('origin') && c.req.header('origin') !== new URL(origin).origin) return c.json({ error: 'Cross-origin approval is refused.' }, 403)
    try { return c.json(await approveMcpAuthorization(base.pool, base.clock, actor, await c.req.json(), resource)) }
    catch (error) { return errorResponse(c, error) }
  })
  app.post('/auth/mcp/token', async (c) => {
    c.header('cache-control', 'no-store')
    c.header('pragma', 'no-cache')
    try {
      const body = uniqueParameters(new URLSearchParams(await c.req.text()))
      return c.json(await redeemMcpAuthorization(base.pool, base.clock, body, resource))
    } catch (error) { return errorResponse(c, error) }
  })
  const handle = async (c: HttpContext) => {
    const authorization = c.req.header('authorization') ?? ''
    const token = /^Bearer /i.test(authorization) ? authorization.slice(7) : ''
    const identity = await identifyMcpToken(base.pool, base.clock, token, resource)
    if (!identity) {
      c.header('www-authenticate', `Bearer resource_metadata="${metadata}", scope="mcp:read"`)
      return c.json({ error: 'invalid_token' }, 401)
    }
    const transport = new WebStandardStreamableHTTPServerTransport({ enableJsonResponse: true })
    const server = toolServer({ ...base, actor: identity.actor }, identity.scopes)
    try {
      await server.connect(transport)
      return await transport.handleRequest(c.req.raw)
    } finally { await server.close() }
  }
  app.post('/mcp', handle)
  const unsupported = (c: HttpContext) => { c.header('allow', 'POST'); return c.json({ error: 'Use POST for this stateless MCP endpoint.' }, 405) }
  app.get('/mcp', unsupported)
  app.delete('/mcp', unsupported)
}
