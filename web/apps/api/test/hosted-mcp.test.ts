import { after, before, describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomBytes } from 'node:crypto'
import { startApi, seedOrg, dropOrg, signInAs, type ApiHarness, type Org, type SignedIn } from './harness.ts'
import { registerMcpClient, approveMcpAuthorization, redeemMcpAuthorization, identifyMcpToken } from '../src/auth/mcp.ts'
import type { Actor } from '../src/trpc.ts'

const resource = 'http://app.test/mcp'
interface RpcReply { result: { serverInfo?: { name: string }; content: [{ text: string }] } }
describe('hosted MCP authorization and reachable tools', () => {
  let api: ApiHarness
  let org: Org
  let owner: SignedIn
  before(async () => { api = await startApi(); org = await seedOrg(api.admin, 'hosted-mcp'); owner = await signInAs(api, org, 'owner') })
  after(async () => { await dropOrg(api.admin, org.orgId); await api.close() })

  async function grant(scopes = 'mcp:read', person = owner, role: Actor['role'] = 'owner') {
    const client = await registerMcpClient(api.pool, { client_name: 'Integration client', redirect_uris: ['https://client.test/callback'] })
    const verifier = randomBytes(32).toString('base64url')
    const request = {
      client_id: client.client_id, redirect_uri: 'https://client.test/callback', response_type: 'code',
      code_challenge: createHash('sha256').update(verifier).digest('base64url'), code_challenge_method: 'S256', resource, scope: scopes,
    }
    const approved = await approveMcpAuthorization(api.pool, api.clock, {
      userId: person.userId, orgId: org.orgId, label: 'Integration owner', role, sessionId: '', plan: 'free',
    }, request, resource)
    const exchange = { grant_type: 'authorization_code', client_id: client.client_id,
      redirect_uri: request.redirect_uri, resource, code_verifier: verifier, code: new URL(approved.redirect).searchParams.get('code')! }
    return { request, exchange, token: () => redeemMcpAuthorization(api.pool, api.clock, exchange, resource) }
  }
  async function rpc(token: string, method: string, params: unknown) {
    return api.fetch('/mcp', { method: 'POST', headers: {
      authorization: `Bearer ${token}`, 'content-type': 'application/json', accept: 'application/json, text/event-stream',
      'mcp-protocol-version': '2025-11-25',
    }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }) })
  }

  test('real initialize reaches the mounted protocol server', async () => {
    const token = await (await grant()).token()
    const response = await rpc(token.access_token, 'initialize', { protocolVersion: '2025-11-25', capabilities: {}, clientInfo: { name: 'test', version: '1' } })
    const body = await response.json() as RpcReply
    assert.equal(body.result?.serverInfo?.name, 'Antifailure')
  })
  test('direct tool call reads the authorized tenant without tools/list first', async () => {
    const token = await (await grant()).token()
    const response = await rpc(token.access_token, 'tools/call', { name: 'list_projects', arguments: {} })
    const body = await response.json() as RpcReply
    assert.equal(JSON.parse(body.result.content[0].text)[0].full_name, org.repository)
  })
  test('read scope cannot start an environment through a direct tool call', async () => {
    const token = await (await grant()).token()
    const body = await (await rpc(token.access_token, 'tools/call', { name: 'start_environment', arguments: { repository: org.repository } })).json() as RpcReply
    assert.match(body.result.content[0].text, /Reconnect and approve/)
  })
  test('write scope never overrides a viewer role', async () => {
    const viewer = await signInAs(api, org, 'viewer')
    const token = await (await grant('mcp:write', viewer, 'viewer')).token()
    const body = await (await rpc(token.access_token, 'tools/call', { name: 'start_environment', arguments: { repository: org.repository } })).json() as RpcReply
    assert.match(body.result.content[0].text, /environments.create permission/)
  })
  test('a token is bound to the resource that consent approved', async () => {
    const token = await (await grant()).token()
    assert.equal(await identifyMcpToken(api.pool, api.clock, token.access_token, 'https://other.test/mcp'), null)
  })
  test('revocation makes a previously working token unusable', async () => {
    const token = await (await grant()).token()
    await api.admin`UPDATE engine_tokens SET revoked_at = now() WHERE token_hash = ${createHash('sha256').update(token.access_token).digest()}`
    assert.equal((await rpc(token.access_token, 'tools/list', {})).status, 401)
  })
  test('MCP bearer cannot authenticate the engine ingestion endpoint', async () => {
    const token = await (await grant('mcp:write')).token()
    const response = await api.fetch('/v1/events', { method: 'POST', headers: {
      authorization: `Bearer ${token.access_token}`, 'content-type': 'application/json',
    }, body: JSON.stringify({ events: [] }) })
    assert.equal(response.status, 401)
  })
  test('wrong PKCE verifier leaves the legitimate exchange available', async () => {
    const flow = await grant()
    await assert.rejects(redeemMcpAuthorization(api.pool, api.clock, { ...flow.exchange, code_verifier: 'x'.repeat(43) }, resource), /invalid, expired, or already used/)
    await flow.token()
  })
  test('concurrent code redemption issues exactly one credential', async () => {
    const flow = await grant()
    const outcomes = await Promise.allSettled([flow.token(), flow.token()])
    assert.equal(outcomes.filter((outcome) => outcome.status === 'fulfilled').length, 1)
  })
  test('a removed membership cannot keep using a token', async () => {
    const member = await signInAs(api, org, 'member')
    const token = await (await grant('mcp:read', member, 'member')).token()
    await api.admin`DELETE FROM members WHERE user_id = ${member.userId} AND org_id = ${org.orgId}`
    assert.equal(await identifyMcpToken(api.pool, api.clock, token.access_token, resource), null)
  })
  test('an untrusted browser origin is refused before tool execution', async () => {
    const token = await (await grant()).token()
    const response = await api.fetch('/mcp', { method: 'POST', headers: { origin: 'https://evil.test', authorization: `Bearer ${token.access_token}` } })
    assert.equal(response.status, 403)
  })
  test('consent cannot be submitted without the current session CSRF token', async () => {
    const flow = await grant()
    const response = await api.fetch('/auth/mcp/approve', { method: 'POST', headers: { cookie: owner.cookie, 'content-type': 'application/json' }, body: JSON.stringify(flow.request) })
    assert.equal(response.status, 403)
  })
  test('expired credentials are refused at the protocol endpoint', async () => {
    const token = await (await grant()).token()
    await api.admin`UPDATE engine_tokens SET expires_at = ${new Date(api.clock.now().getTime() - 1000)}
      WHERE token_hash = ${createHash('sha256').update(token.access_token).digest()}`
    assert.equal((await rpc(token.access_token, 'tools/list', {})).status, 401)
  })
  test('OAuth token parameters cannot be repeated', async () => {
    const flow = await grant()
    const response = await api.fetch('/auth/mcp/token', { method: 'POST',
      body: new URLSearchParams(flow.exchange).toString() + '&code=another-code' })
    assert.match((await response.json() as { error_description: string }).error_description, /only once/)
  })
  test('malformed registration JSON is a client error', async () => {
    const response = await api.fetch('/auth/mcp/register', { method: 'POST',
      headers: { 'content-type': 'application/json' }, body: '{' })
    assert.equal(response.status, 400)
  })
  test('oversized registration is refused before parsing', async () => {
    const response = await api.fetch('/auth/mcp/register', { method: 'POST',
      headers: { 'content-type': 'application/json' }, body: JSON.stringify({ padding: 'x'.repeat(40 * 1024) }) })
    assert.equal(response.status, 413)
  })
  test('registration returns a usable public-client identifier', async () => {
    const response = await api.fetch('/auth/mcp/register', { method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ client_name: 'Browser client', redirect_uris: ['https://client.test/callback'] }) })
    const registered = await response.json() as { client_id: string }
    assert.match(registered.client_id, /^[A-Za-z0-9_-]{43}$/)
  })
  test('authorization cannot redirect a code to an unregistered address', async () => {
    const flow = await grant()
    const response = await api.fetch('/auth/mcp/authorize?' + new URLSearchParams({ ...flow.request, redirect_uri: 'https://evil.test/callback' }))
    assert.equal(response.status, 400)
  })
  test('discovery publishes the configured audience rather than request host', async () => {
    const response = await api.fetch('/.well-known/oauth-protected-resource')
    assert.equal((await response.json() as { resource: string }).resource, resource)
  })
  test('unauthenticated protocol response points clients to authorization discovery', async () => {
    const response = await rpc('invalid', 'tools/list', {})
    assert.match(response.headers.get('www-authenticate') ?? '', /resource_metadata="http:\/\/app.test\/\.well-known\/oauth-protected-resource"/)
  })
  test('authorization response cannot be cached with its usable code', async () => {
    const flow = await grant()
    const response = await api.fetch('/auth/mcp/approve', { method: 'POST', headers: {
      cookie: owner.cookie, 'x-antifailure-csrf': owner.csrfToken, 'content-type': 'application/json',
    }, body: JSON.stringify(flow.request) })
    assert.equal(response.headers.get('cache-control'), 'no-store')
  })
})
