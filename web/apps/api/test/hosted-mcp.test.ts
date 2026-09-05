import { after, before, describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomBytes } from 'node:crypto'
import { startApi, seedOrg, dropOrg, signInAs, type ApiHarness, type Org, type SignedIn } from './harness.ts'
import { registerMcpClient, approveMcpAuthorization, redeemMcpAuthorization, identifyMcpToken } from '../src/auth/mcp.ts'
import type { Actor } from '../src/trpc.ts'

const resource = 'http://app.test/mcp'
interface RpcReply { result: { serverInfo?: { name: string }; content: [{ text: string }] } }
// A configured origin is operator input and arrives with whatever punctuation
// the operator typed. Stripping its trailing slashes used to be `/\/+$/`, which
// CodeQL flags as polynomial: on a value that does not end in a slash the
// engine retries from every slash in it. The rewrite has to keep the behaviour,
// so the behaviour is asserted rather than the shape of the code.
describe('the hosted audience is stripped of what an operator typed', () => {
  test('trailing slashes do not reach the published resource', async () => {
    const api = await startApi({ appBaseUrl: 'http://slashes.test///' })
    try {
      const body = await (await api.fetch('/.well-known/oauth-protected-resource')).json() as { resource: string }
      assert.equal(body.resource, 'http://slashes.test/mcp')
    } finally { await api.close() }
  })
  test('an installation with no configured origin serves no endpoint at all', async () => {
    const api = await startApi({ appBaseUrl: '' })
    try {
      assert.equal((await api.fetch('/mcp', { method: 'POST' })).status, 404)
    } finally { await api.close() }
  })
})

describe('hosted MCP authorization and reachable tools', () => {
  let api: ApiHarness
  let org: Org
  let owner: SignedIn
  // mcp_clients carries no org_id, so dropOrg cannot reach a registration.
  // Left behind, these accumulate in a shared database forever and inflate the
  // operator's installation wide client count for whatever runs next.
  const registered: string[] = []
  before(async () => { api = await startApi(); org = await seedOrg(api.admin, 'hosted-mcp'); owner = await signInAs(api, org, 'owner') })
  after(async () => {
    if (registered.length) await api.admin`DELETE FROM mcp_clients WHERE client_id = ANY(${registered})`
    await dropOrg(api.admin, org.orgId)
    await api.close()
  })

  async function grant(scopes = 'mcp:read', person = owner, role: Actor['role'] = 'owner') {
    const client = await registerMcpClient(api.pool, { client_name: 'Integration client', redirect_uris: ['https://client.test/callback'] })
    registered.push(client.client_id)
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
  // The operator page has a "Last authentication" column, and the write that
  // fills it is an UPDATE inside withTenant on a table whose own presented_mcp_token
  // policy is SELECT only. If tenant_isolation did not also cover that UPDATE it
  // would affect zero rows, raise nothing, and the column would read "Not used
  // yet" forever. The management suite sets the value with admin SQL, so only a
  // real request through the endpoint can tell a live write from a dead one.
  test('a real protocol request records when the credential last authenticated', async () => {
    const token = await (await grant()).token()
    const hash = createHash('sha256').update(token.access_token).digest()
    const fresh = await api.admin<{ last_used_at: Date | null }[]>`
      SELECT last_used_at FROM engine_tokens WHERE token_hash = ${hash}`
    assert.equal(fresh[0]!.last_used_at, null, 'a credential nobody has used carries a time')
    assert.equal((await rpc(token.access_token, 'tools/list', {})).status, 200)
    const used = await api.admin<{ last_used_at: Date | null }[]>`
      SELECT last_used_at FROM engine_tokens WHERE token_hash = ${hash}`
    assert.notEqual(used[0]!.last_used_at, null)
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
    const created = await response.json() as { client_id: string }
    registered.push(created.client_id)
    assert.match(created.client_id, /^[A-Za-z0-9_-]{43}$/)
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
  // The write half, proved as a dispatch and never as a ready environment.
  //
  // Every other write assertion in this file is a REFUSAL: a read token cannot
  // start anything, a viewer cannot either. A refusal proves the gate and says
  // nothing about the path behind it, so the tool could have been wired to
  // nothing and this suite would still have been green. This is the one test
  // that lets a write through and then reads what actually happened to it.
  test('an approved write reaches the customer repository as one dispatch', async () => {
    const token = await (await grant('mcp:read mcp:write')).token()
    await api.admin`INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
      VALUES (${org.orgId}, ${Math.floor(Math.random() * 1e9)}, ${org.slug}, 'Organization')`
    api.github.addWorkflow(org.repository, 'antifailure.yml')
    const environments = async () => {
      const listed = await (await rpc(token.access_token, 'tools/call', { name: 'list_environments', arguments: {} })).json() as RpcReply
      return JSON.parse(listed.result.content[0].text) as unknown
    }
    const before = api.github.dispatches.length
    const environmentsBefore = await environments()

    const body = await (await rpc(token.access_token, 'tools/call', { name: 'start_environment',
      arguments: { repository: org.repository, branch: 'main', workflow: 'antifailure.yml' } })).json() as RpcReply
    const result = JSON.parse(body.result.content[0].text) as {
      dispatched?: boolean; repository?: string; ref?: string; workflow?: string; pending?: unknown
    }

    // What the tool told the agent.
    assert.deepEqual({ dispatched: result.dispatched, repository: result.repository, ref: result.ref,
      workflow: result.workflow, pendingIsStated: typeof result.pending === 'string' && result.pending.length > 0 },
    { dispatched: true, repository: org.repository, ref: 'main', workflow: 'antifailure.yml', pendingIsStated: true })

    // What GitHub was actually asked for. One dispatch, against the customer's
    // own repository and workflow, carrying the up command.
    assert.equal(api.github.dispatches.length - before, 1)
    const sent = api.github.dispatches[api.github.dispatches.length - 1]!
    assert.deepEqual({ repository: sent.repository, workflow: sent.workflow, ref: sent.ref, command: sent.inputs.command },
      { repository: org.repository, workflow: 'antifailure.yml', ref: 'main', command: 'up' })

    // What the organization's own record says a person did.
    const audited = await api.admin<{ action: string; target_id: string }[]>`
      SELECT action, target_id FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'environment.requested'`
    assert.deepEqual(audited.map((row) => [row.action, row.target_id]), [['environment.requested', org.repository]])

    // And the boundary the tool description promises: a dispatch is not a
    // ready environment. Nothing new is readable until the engine reports it.
    assert.deepEqual(await environments(), environmentsBefore)
  })
  test('a standalone event stream is refused, and says what to use instead', async () => {
    const token = await (await grant()).token()
    for (const method of ['GET', 'DELETE']) {
      const response = await api.fetch('/mcp', { method, headers: { authorization: `Bearer ${token.access_token}` } })
      assert.deepEqual({ method, status: response.status, allow: response.headers.get('allow') },
        { method, status: 405, allow: 'POST' })
    }
  })
  test('authorization response cannot be cached with its usable code', async () => {
    const flow = await grant()
    const response = await api.fetch('/auth/mcp/approve', { method: 'POST', headers: {
      cookie: owner.cookie, 'x-antifailure-csrf': owner.csrfToken, 'content-type': 'application/json',
    }, body: JSON.stringify(flow.request) })
    assert.equal(response.headers.get('cache-control'), 'no-store')
  })
})
