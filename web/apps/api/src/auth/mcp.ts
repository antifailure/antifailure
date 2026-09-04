import { createHash, randomBytes, timingSafeEqual } from 'node:crypto'
import { sql } from 'drizzle-orm'
import { z } from 'zod'
import type { Pool } from '@antifailure/db'
import { appendAudit } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import type { Actor } from '../trpc.ts'
import { ROLES } from '../permissions.ts'

export const MCP_SCOPES = ['mcp:read', 'mcp:write'] as const
const CODE_TTL_MS = 5 * 60 * 1000
const TOKEN_TTL_MS = 90 * 24 * 60 * 60 * 1000
const digest = (value: string): Buffer => createHash('sha256').update(value).digest()
const timestamp = (value: Date | string): number => new Date(value).getTime()

export class McpAuthorizationError extends Error {
  readonly code: string
  constructor(code: string, message: string) { super(message); this.code = code }
}

const redirectUri = z.string().max(2048).refine((value) => {
  try {
    const url = new URL(value)
    const loopback = ['127.0.0.1', '[::1]', 'localhost'].includes(url.hostname)
    return !url.username && !url.password && !url.hash &&
      (url.protocol === 'https:' || (url.protocol === 'http:' && loopback))
  } catch { return false }
}, 'Use an HTTPS redirect URI, or HTTP on loopback for a local client.')

const registration = z.object({
  client_name: z.string().trim().min(1).max(200).default('MCP client'),
  redirect_uris: z.array(redirectUri).min(1).max(10),
  token_endpoint_auth_method: z.literal('none').default('none'),
  grant_types: z.array(z.literal('authorization_code')).length(1).default(['authorization_code']),
  response_types: z.array(z.literal('code')).length(1).default(['code']),
})

export async function registerMcpClient(pool: Pool, input: unknown) {
  const parsed = registration.safeParse(input)
  if (!parsed.success) {
    throw new McpAuthorizationError('invalid_client_metadata', 'Register an authorization-code client using PKCE and exact redirect URIs.')
  }
  const clientId = randomBytes(32).toString('base64url')
  const data = parsed.data
  await pool.withoutTenant(async (db) => {
    await db.execute(sql`INSERT INTO mcp_clients (client_id, client_name, redirect_uris)
      VALUES (${clientId}, ${data.client_name}, ${sql.param(data.redirect_uris)}::text[])`)
  }, { mcpClientId: clientId })
  return { client_id: clientId, ...data }
}

const authorization = z.object({
  client_id: z.string().min(1).max(200),
  redirect_uri: redirectUri,
  response_type: z.literal('code'),
  code_challenge: z.string().regex(/^[A-Za-z0-9_-]{43}$/),
  code_challenge_method: z.literal('S256'),
  resource: z.string().max(2048),
  scope: z.string().max(100).default('mcp:read'),
  state: z.string().max(2048).optional(),
})

export async function describeMcpAuthorization(pool: Pool, input: unknown, resource: string) {
  const parsed = authorization.safeParse(input)
  if (!parsed.success) throw new McpAuthorizationError('invalid_request', 'This connection needs a valid PKCE S256 authorization request.')
  const request = parsed.data
  if (request.resource !== resource) throw new McpAuthorizationError('invalid_target', 'The requested resource is not this MCP endpoint.')
  const scopes = [...new Set(request.scope.split(' ').filter(Boolean))]
  if (!scopes.length || scopes.some((scope) => !MCP_SCOPES.includes(scope as typeof MCP_SCOPES[number]))) {
    throw new McpAuthorizationError('invalid_scope', 'This endpoint supports mcp:read and mcp:write.')
  }
  const client = await pool.withoutTenant(async (db) => {
    const rows = await db.execute<{ client_name: string; redirect_uris: string[] }>(sql`
      SELECT client_name, redirect_uris FROM mcp_clients WHERE client_id = ${request.client_id}`)
    return rows[0]
  }, { mcpClientId: request.client_id })
  if (!client || !client.redirect_uris.includes(request.redirect_uri)) {
    throw new McpAuthorizationError('invalid_request', 'The client or redirect URI is not registered.')
  }
  return { request, scopes, clientName: client.client_name }
}

export async function approveMcpAuthorization(
  pool: Pool, clock: Clock, actor: Actor, input: unknown, resource: string,
) {
  const approval = await describeMcpAuthorization(pool, input, resource)
  const code = randomBytes(32).toString('base64url')
  const hash = digest(code)
  const expires = new Date(clock.now().getTime() + CODE_TTL_MS)
  await pool.withTenant({ orgId: actor.orgId, userId: actor.userId }, async (db) => {
    const membership = await db.execute(sql`SELECT user_id FROM members
      WHERE user_id = ${actor.userId}::uuid AND org_id = ${actor.orgId}::uuid FOR SHARE`)
    if (!membership.length) throw new McpAuthorizationError('access_denied', 'You no longer belong to this organization.')
    await db.execute(sql`INSERT INTO mcp_authorization_codes
      (code_hash, client_id, redirect_uri, code_challenge, code_challenge_method,
       scopes, resource, approved_user_id, approved_org_id, expires_at)
      VALUES (${hash}, ${approval.request.client_id}, ${approval.request.redirect_uri},
        ${approval.request.code_challenge}, 'S256', ${sql.param(approval.scopes)}::text[],
        ${resource}, ${actor.userId}::uuid, ${actor.orgId}::uuid, ${expires.toISOString()})`)
    await appendAudit(db, {
      orgId: actor.orgId, actorUserId: actor.userId, actorLabel: actor.label,
      action: 'mcp.approved', targetType: 'mcp_client', targetId: approval.request.client_id,
      origin: 'web', detail: { scopes: approval.scopes }, occurredAt: clock.now(),
    })
  }, { mcpCodeHash: hash })
  const redirect = new URL(approval.request.redirect_uri)
  redirect.searchParams.set('code', code)
  if (approval.request.state !== undefined) redirect.searchParams.set('state', approval.request.state)
  return { redirect: redirect.toString() }
}

const redemption = z.object({
  grant_type: z.literal('authorization_code'),
  code: z.string().min(32).max(200),
  client_id: z.string().min(1).max(200),
  redirect_uri: redirectUri,
  code_verifier: z.string().regex(/^[A-Za-z0-9._~-]{43,128}$/),
  resource: z.string().max(2048),
})

interface CodeRow extends Record<string, unknown> {
  client_id: string; redirect_uri: string; code_challenge: string; scopes: string[]
  resource: string; approved_user_id: string; approved_org_id: string
  expires_at: Date | string; redeemed_at: Date | string | null
}

export async function redeemMcpAuthorization(pool: Pool, clock: Clock, input: unknown, resource: string) {
  const parsed = redemption.safeParse(input)
  if (!parsed.success) throw new McpAuthorizationError('invalid_request', 'The token request is incomplete.')
  const request = parsed.data
  if (request.resource !== resource) throw new McpAuthorizationError('invalid_target', 'The requested resource is not this MCP endpoint.')
  const hash = digest(request.code)
  const found = await pool.withoutTenant(async (db) => {
    const rows = await db.execute<CodeRow>(sql`SELECT * FROM mcp_authorization_codes WHERE code_hash = ${hash}`)
    return rows[0]
  }, { mcpCodeHash: hash })
  const invalid = () => new McpAuthorizationError('invalid_grant', 'This authorization code is invalid, expired, or already used.')
  if (!found) throw invalid()
  const accessToken = `afm_${randomBytes(32).toString('base64url')}`
  const expiresAt = new Date(clock.now().getTime() + TOKEN_TTL_MS)
  // Claim and mint in one transaction. A failed token INSERT must leave the
  // code redeemable, and two exchanges must never mint two credentials.
  await pool.withTenant({ orgId: found.approved_org_id, userId: found.approved_user_id }, async (db) => {
    const rows = await db.execute<CodeRow>(sql`SELECT * FROM mcp_authorization_codes
      WHERE code_hash = ${hash} FOR UPDATE`)
    const row = rows[0]
    if (!row || row.redeemed_at || timestamp(row.expires_at) <= clock.now().getTime() ||
        row.client_id !== request.client_id || row.redirect_uri !== request.redirect_uri || row.resource !== resource) throw invalid()
    const expected = Buffer.from(row.code_challenge)
    const presented = Buffer.from(digest(request.code_verifier).toString('base64url'))
    if (expected.length !== presented.length || !timingSafeEqual(expected, presented)) throw invalid()
    const members = await db.execute(sql`SELECT user_id FROM members WHERE
      user_id = ${row.approved_user_id}::uuid AND org_id = ${row.approved_org_id}::uuid FOR SHARE`)
    if (!members.length) throw invalid()
    await db.execute(sql`UPDATE mcp_authorization_codes SET redeemed_at = ${clock.now().toISOString()}
      WHERE code_hash = ${hash}`)
    await db.execute(sql`INSERT INTO engine_tokens
      (org_id, name, token_hash, prefix, created_by, kind, user_id, scopes, expires_at, mcp_client_id, mcp_resource)
      VALUES (${row.approved_org_id}::uuid, 'MCP connection', ${digest(accessToken)},
        ${accessToken.slice(0, 12)}, ${row.approved_user_id}::uuid, 'mcp',
        ${row.approved_user_id}::uuid, ${sql.param(row.scopes)}::text[],
        ${expiresAt.toISOString()}, ${row.client_id}, ${resource})`)
  }, { mcpCodeHash: hash })
  return { access_token: accessToken, token_type: 'Bearer', expires_in: TOKEN_TTL_MS / 1000, scope: found.scopes.join(' ') }
}

export async function identifyMcpToken(pool: Pool, clock: Clock, token: string, resource: string) {
  if (!/^afm_[A-Za-z0-9_-]{43}$/.test(token)) return null
  const hash = digest(token)
  const found = await pool.withoutTenant(async (db) => {
    const rows = await db.execute<{
      id: string; org_id: string; user_id: string; scopes: string[]; mcp_client_id: string
    }>(sql`SELECT id, org_id, user_id, scopes, mcp_client_id FROM engine_tokens
      WHERE token_hash = ${hash} AND kind = 'mcp' AND revoked_at IS NULL
        AND mcp_resource = ${resource}
        AND expires_at > ${clock.now().toISOString()}`)
    return rows[0]
  }, { mcpTokenHash: hash })
  if (!found) return null
  return pool.withTenant({ orgId: found.org_id, userId: found.user_id }, async (db) => {
    const rows = await db.execute<{ role: string; github_login: string; plan: string }>(sql`
      SELECT m.role, u.github_login, o.plan FROM members m
      JOIN users u ON u.id = m.user_id JOIN organizations o ON o.id = m.org_id
      WHERE m.user_id = ${found.user_id}::uuid AND m.org_id = ${found.org_id}::uuid`)
    const member = rows[0]
    if (!member || !ROLES.includes(member.role as typeof ROLES[number])) return null
    await db.execute(sql`UPDATE engine_tokens SET last_used_at = ${clock.now().toISOString()}
      WHERE id = ${found.id}::uuid`)
    return {
      actor: { userId: found.user_id, orgId: found.org_id, label: member.github_login,
        role: member.role as typeof ROLES[number], plan: member.plan, sessionId: '' } satisfies Actor,
      scopes: found.scopes, clientId: found.mcp_client_id,
    }
  })
}
