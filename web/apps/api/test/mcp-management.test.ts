import { after, afterEach, before, beforeEach, describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes, randomUUID } from 'node:crypto'
import { startApi, seedOrg, signInAs, type ApiHarness, type Org } from './harness.ts'
import { adminSignIn, hashPassword } from '../src/admin/session.ts'
import { appRouter } from '../src/routers/index.ts'
type Surface = Awaited<ReturnType<ReturnType<typeof appRouter.createCaller>['admin']['platform']['mcp']['surface']>>

describe('hosted MCP operator measurements', () => {
  let h: ApiHarness
  let org: Org
  let client: string
  let active: string
  let session: { token: string; csrfToken: string }
  // mcp_clients carries no org_id on purpose, so the operator's client count is
  // installation wide and every other suite sharing this database contributes
  // to it. The absolute number is therefore whatever ran first; what this suite
  // owns is the ONE registration it makes, so the assertion below is a delta.
  // Asserting the absolute number passed alone and failed in the full run.
  let clientsBefore: number
  before(async () => {
    h = await startApi()
    h.clock.advance(Date.now() - h.clock.now().getTime())
    const email = `mcp-operator-${randomUUID()}@example.test`
    const password = randomBytes(24).toString('base64url')
    const { hash, salt } = await hashPassword(password)
    await h.admin`INSERT INTO admin_users(email,name,role,password_hash,password_salt,password_set_at)
      VALUES(${email},'MCP operator','owner',${hash},${salt},now())`
    session = await adminSignIn(h.pool, { email, password }, h.clock.now())
  })
  after(async () => { await h.close() })
  beforeEach(async () => {
    clientsBefore = (await surface()).counts.clients
    org = await seedOrg(h.admin, 'mcp-management')
    const user = await signInAs(h, org, 'owner')
    client = randomUUID()
    await h.admin`INSERT INTO mcp_clients(client_id,client_name,redirect_uris)
      VALUES(${client},'Integration client',ARRAY['https://client.test/cb'])`
    const future = new Date(h.clock.now().getTime() + 86400000).toISOString()
    const past = new Date(h.clock.now().getTime() - 86400000).toISOString()
    for (const state of ['active', 'expired', 'revoked']) {
      const [row] = await h.admin<{ id: string }[]>`INSERT INTO engine_tokens
        (org_id,name,token_hash,prefix,kind,user_id,created_by,scopes,expires_at,revoked_at,mcp_client_id,mcp_resource,last_used_at)
        VALUES(${org.orgId},'MCP connection',${randomBytes(32)},${`afm_${state}`},'mcp',${user.userId},${user.userId},
          ARRAY['mcp:read'],${state === 'expired' ? h.clock.now().toISOString() : future},${state === 'revoked' ? past : null},${client},
          'http://app.test/mcp',${state === 'active' ? past : null}) RETURNING id`
      if (state === 'active') active = row!.id
    }
    await h.admin`INSERT INTO engine_tokens(org_id,name,token_hash,prefix,kind,expires_at)
      VALUES(${org.orgId},'Not MCP',${randomBytes(32)},'afe_other','engine',${future})`
  })
  afterEach(async () => {
    await h.admin`DELETE FROM organizations WHERE id=${org.orgId}`
    await h.admin`DELETE FROM mcp_clients WHERE client_id=${client}`
  })
  async function surface() {
    const response = await h.fetch('/trpc/admin.platform.mcp.surface', { headers: { cookie: `af_admin_session=${session.token}` } })
    const body = await response.json() as { result?: { data?: Surface } }
    if (!body.result?.data) throw new Error(`MCP surface returned ${response.status}`)
    return body.result.data
  }
  test('counts only MCP credentials and partitions their standing', async () => {
    const counts = (await surface()).counts
    assert.deepEqual({ clients: counts.clients - clientsBefore, active: counts.active,
      revoked: counts.revoked, expired: counts.expired },
    { clients: 1, active: 1, revoked: 1, expired: 1 })
  })
  test('the endpoint comes from configured application origin', async () => {
    assert.equal((await surface()).endpoint, 'http://app.test/mcp')
  })
  // The operator page prints an address and the server answers on one. They
  // used to be computed from appBaseUrl twice, in two files, so a change to
  // either could leave this page naming somewhere nothing is served. Read both
  // and compare, rather than asserting the same literal in two suites.
  test('the printed endpoint is the audience the server actually publishes', async () => {
    const discovery = await (await h.fetch('/.well-known/oauth-protected-resource')).json() as { resource: string }
    assert.equal((await surface()).endpoint, discovery.resource)
  })
  test('connections show identity, scopes and authentication without token hashes', async () => {
    const data = await surface()
    const row = data.connections.find((r: { id: string }) => r.id === active)
    if (!row) throw new Error('The active credential is absent')
    assert.deepEqual({ client: row.clientName, org: row.orgSlug, scopes: row.scopes,
      last: row.lastAuthenticatedAt !== null, state: row.standing, hash: 'token_hash' in row },
    { client: 'Integration client', org: org.slug, scopes: ['mcp:read'], last: true, state: 'active', hash: false })
  })
  test('expiration and revocation remain distinct', async () => {
    assert.deepEqual((await surface()).connections.map((r: { standing: string }) => r.standing).sort(), ['active', 'expired', 'revoked'])
  })
  test('revocation changes the measured state and tells the person to reconnect', async () => {
    const response = await h.fetch('/trpc/admin.platform.keys.revoke', { method: 'POST', headers: {
      cookie: `af_admin_session=${session.token}`, 'content-type': 'application/json', 'x-antifailure-admin-csrf': session.csrfToken,
    }, body: JSON.stringify({ tokenId: active, reason: 'The client is no longer authorized' }) })
    const body = await response.json() as { result?: { data?: { effect?: string } } }
    const data = await surface()
    assert.deepEqual({ active: data.counts.active, revoked: data.counts.revoked, reconnect: /reconnects their MCP client/.test(body.result?.data?.effect ?? '') },
      { active: 0, revoked: 2, reconnect: true })
  })
  test('the searchable credential directory accepts the MCP filter', async () => {
    const response = await h.fetch('/trpc/admin.platform.keys.list?input='+encodeURIComponent(JSON.stringify({kind:'mcp',limit:25})), {headers:{cookie:`af_admin_session=${session.token}`}})
    assert.equal(response.status, 200)
  })
  test('the list is bounded and discloses truncation', async () => {
    await h.admin`INSERT INTO engine_tokens(org_id,name,token_hash,prefix,kind,user_id,created_by,scopes,expires_at,mcp_client_id,mcp_resource)
      SELECT org_id,name,decode(md5(g::text),'hex'),prefix,kind,user_id,created_by,scopes,expires_at,mcp_client_id,mcp_resource
      FROM engine_tokens CROSS JOIN generate_series(1,51) g WHERE id=${active}`
    const data = await surface()
    assert.deepEqual({ length: data.connections.length, more: data.hasMore }, {length:50,more:true})
  })
})
