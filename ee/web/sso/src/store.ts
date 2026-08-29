// Every statement single sign-on runs, and which of them is allowed to run
// without a tenant.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// There are exactly four unauthenticated reads in this file and each one is a
// lookup that DETERMINES the tenant, which is the one thing a tenant-scoped
// query cannot do. Each declares the single value it is already holding and the
// policy in migration 0013 returns the row that value names and nothing else.
// Everything else in this file is scoped, and stays scoped, including every
// write.
//
// The rule that shapes the whole file: a secret is never read on an unscoped
// connection. Row-level security is row level, so the policy that lets a
// callback find a connection by its handle would expose every column of that
// row if the secrets lived there. They live in a second table with one policy
// that requires the tenant, and the sequence is always: resolve the routing row
// unscoped, learn the organization, then read the secret scoped to it.

import { sql } from '@antifailure/db'
import type { Db, Pool } from '@antifailure/db'

export type Role = 'owner' | 'admin' | 'member' | 'viewer'

export interface Connection {
  id: string
  orgId: string
  handle: string
  kind: 'saml' | 'oidc'
  displayName: string
  enabled: boolean
  enforced: boolean
  defaultRole: Role
  idpEntityId: string | null
  idpSsoUrl: string | null
  idpCertificates: string[]
  oidcIssuer: string | null
  oidcClientId: string | null
  oidcAuthorizationEndpoint: string | null
  oidcTokenEndpoint: string | null
  oidcJwksUri: string | null
  groupRoleMap: Record<string, Role>
  clockSkewSeconds: number
}

const CONNECTION_COLUMNS = sql`
  id, org_id, handle, kind, display_name, enabled, enforced,
  default_role::text AS default_role, idp_entity_id, idp_sso_url, idp_certificates,
  oidc_issuer, oidc_client_id, oidc_authorization_endpoint, oidc_token_endpoint,
  oidc_jwks_uri, group_role_map, clock_skew_seconds`

/* eslint-disable @typescript-eslint/no-explicit-any */
function toConnection(row: Record<string, any>): Connection {
  return {
    id: row.id,
    orgId: row.org_id,
    handle: row.handle,
    kind: row.kind,
    displayName: row.display_name,
    enabled: row.enabled,
    enforced: row.enforced,
    defaultRole: row.default_role,
    idpEntityId: row.idp_entity_id,
    idpSsoUrl: row.idp_sso_url,
    // Postgres text[] arrives as an array through the driver, and as a literal
    // through some paths. Both are handled, because a certificate list that
    // silently becomes the string "{abc}" produces a signature failure nobody
    // can explain.
    idpCertificates: Array.isArray(row.idp_certificates)
      ? row.idp_certificates
      : parseArrayLiteral(String(row.idp_certificates ?? '')),
    oidcIssuer: row.oidc_issuer,
    oidcClientId: row.oidc_client_id,
    oidcAuthorizationEndpoint: row.oidc_authorization_endpoint,
    oidcTokenEndpoint: row.oidc_token_endpoint,
    oidcJwksUri: row.oidc_jwks_uri,
    groupRoleMap:
      typeof row.group_role_map === 'string'
        ? JSON.parse(row.group_role_map)
        : (row.group_role_map ?? {}),
    clockSkewSeconds: Number(row.clock_skew_seconds),
  }
}
/* eslint-enable @typescript-eslint/no-explicit-any */

function parseArrayLiteral(literal: string): string[] {
  const inner = literal.replace(/^\{/, '').replace(/\}$/, '')
  if (inner === '') return []
  return inner.split(',').map((v) => v.replace(/^"|"$/g, ''))
}

// ---------------------------------------------------------------------------
// The four unscoped lookups
// ---------------------------------------------------------------------------

/**
 * The connection named by the handle in a callback URL.
 *
 * Unscoped, because working out which organization this assertion concerns is
 * exactly what it is doing. Reaches no secret: those are in a second table this
 * cannot see.
 */
export async function connectionByHandle(pool: Pool, handle: string): Promise<Connection | null> {
  if (!handle) return null
  const rows = await pool.withoutTenant(
    async (db) =>
      db.execute<Record<string, unknown>>(sql`SELECT ${CONNECTION_COLUMNS} FROM sso_connections`),
    { ssoHandle: handle },
  )
  return rows[0] ? toConnection(rows[0]) : null
}

/** The connection whose issuer matches, for a provider-initiated assertion. */
export async function connectionByEntityId(
  pool: Pool,
  entityId: string,
): Promise<Connection | null> {
  if (!entityId) return null
  const rows = await pool.withoutTenant(
    async (db) =>
      db.execute<Record<string, unknown>>(sql`SELECT ${CONNECTION_COLUMNS} FROM sso_connections`),
    { ssoEntityId: entityId },
  )
  return rows[0] ? toConnection(rows[0]) : null
}

export interface DomainRoute {
  orgId: string
  connectionId: string
}

/**
 * Where an email domain's sign-in should go.
 *
 * Verified claims only. The domain is not a secret and this does not pretend
 * otherwise: what it discloses is that a domain uses single sign-on and which
 * connection handles it, which is the same fact the redirect announces. It
 * discloses nothing about any other domain, and nothing an organization has not
 * verified.
 */
export async function routeForDomain(pool: Pool, domain: string): Promise<DomainRoute | null> {
  const lowered = domain.trim().toLowerCase()
  if (!lowered || !/^[a-z0-9.-]+$/.test(lowered)) return null
  const rows = await pool.withoutTenant(
    async (db) =>
      db.execute<{ org_id: string; connection_id: string }>(
        sql`SELECT org_id, connection_id FROM sso_domains`,
      ),
    { ssoDomain: lowered },
  )
  return rows[0] ? { orgId: rows[0].org_id, connectionId: rows[0].connection_id } : null
}

export interface LoginState {
  state: string
  orgId: string
  connectionId: string
  nonce: string | null
  codeVerifier: string | null
  requestId: string | null
  relayState: string | null
  redirectTo: string | null
  expiresAt: Date
}

/**
 * Takes the state a browser returned with, and destroys it.
 *
 * Deleted and returned in one statement, so two callbacks racing on the same
 * state cannot both find it. That is what makes a login single use, and a
 * replayable callback is a session fixation primitive.
 */
export async function consumeLoginState(pool: Pool, state: string): Promise<LoginState | null> {
  if (!state) return null
  const rows = await pool.withoutTenant(
    async (db) =>
      db.execute<Record<string, unknown>>(sql`
        DELETE FROM sso_login_states WHERE state = ${state}
        RETURNING state, org_id, connection_id, nonce, code_verifier, request_id,
                  relay_state, redirect_to, expires_at`),
    { ssoState: state },
  )
  const row = rows[0]
  if (!row) return null
  return {
    state: row.state as string,
    orgId: row.org_id as string,
    connectionId: row.connection_id as string,
    nonce: (row.nonce as string) ?? null,
    codeVerifier: (row.code_verifier as string) ?? null,
    requestId: (row.request_id as string) ?? null,
    relayState: (row.relay_state as string) ?? null,
    redirectTo: (row.redirect_to as string) ?? null,
    expiresAt: asDate(row.expires_at),
  }
}

// ---------------------------------------------------------------------------
// Everything else, scoped
// ---------------------------------------------------------------------------

export async function connectionById(
  pool: Pool,
  orgId: string,
  connectionId: string,
): Promise<Connection | null> {
  const rows = await pool.withTenant({ orgId }, async (db) =>
    db.execute<Record<string, unknown>>(
      sql`SELECT ${CONNECTION_COLUMNS} FROM sso_connections WHERE id = ${connectionId}`,
    ),
  )
  return rows[0] ? toConnection(rows[0]) : null
}

export async function enabledConnections(pool: Pool, orgId: string): Promise<Connection[]> {
  const rows = await pool.withTenant({ orgId }, async (db) =>
    db.execute<Record<string, unknown>>(
      sql`SELECT ${CONNECTION_COLUMNS} FROM sso_connections WHERE enabled ORDER BY kind`,
    ),
  )
  return rows.map(toConnection)
}

export interface ConnectionSecrets {
  oidcClientSecret: Buffer | null
  spPrivateKey: Buffer | null
  spCertificate: string | null
}

/** Scoped, always. Nothing unauthenticated reaches this table. */
export async function connectionSecrets(
  pool: Pool,
  orgId: string,
  connectionId: string,
): Promise<ConnectionSecrets | null> {
  const rows = await pool.withTenant({ orgId }, async (db) =>
    db.execute<Record<string, unknown>>(sql`
      SELECT oidc_client_secret, sp_private_key, sp_certificate
      FROM sso_connection_secrets WHERE connection_id = ${connectionId}`),
  )
  const row = rows[0]
  if (!row) return null
  return {
    oidcClientSecret: asBuffer(row.oidc_client_secret),
    spPrivateKey: asBuffer(row.sp_private_key),
    spCertificate: (row.sp_certificate as string) ?? null,
  }
}

export interface NewLoginState {
  state: string
  orgId: string
  connectionId: string
  nonce?: string | null
  codeVerifier?: string | null
  requestId?: string | null
  relayState?: string | null
  redirectTo?: string | null
  expiresAt: Date
}

export async function saveLoginState(pool: Pool, input: NewLoginState): Promise<void> {
  await pool.withTenant({ orgId: input.orgId }, async (db) => {
    await db.execute(sql`
      INSERT INTO sso_login_states
        (state, org_id, connection_id, nonce, code_verifier, request_id, relay_state, redirect_to, expires_at)
      VALUES (${input.state}, ${input.orgId}, ${input.connectionId}, ${input.nonce ?? null},
              ${input.codeVerifier ?? null}, ${input.requestId ?? null}, ${input.relayState ?? null},
              ${input.redirectTo ?? null}, ${input.expiresAt.toISOString()})`)
  })
}

/**
 * Remembers an assertion, and says whether it had already been seen.
 *
 * The UNIQUE constraint is what makes this correct. A SELECT followed by an
 * INSERT lets two requests carrying the same assertion both find it absent,
 * which is precisely the window a replay is aiming at. Here the INSERT either
 * takes the identifier or conflicts, and the conflict IS the answer.
 */
export async function rememberAssertion(
  pool: Pool,
  orgId: string,
  connectionId: string,
  assertionId: string,
  expiresAt: Date,
): Promise<{ fresh: boolean }> {
  const rows = await pool.withTenant({ orgId }, async (db) =>
    db.execute<{ id: string }>(sql`
      INSERT INTO sso_assertions_seen (org_id, connection_id, assertion_id, expires_at)
      VALUES (${orgId}, ${connectionId}, ${assertionId}, ${expiresAt.toISOString()})
      ON CONFLICT (connection_id, assertion_id) DO NOTHING
      RETURNING id`),
  )
  return { fresh: rows.length > 0 }
}

/**
 * Removes what has expired, for one organization.
 *
 * Housekeeping and not enforcement, exactly like the session sweeper: a state
 * past its expiry is refused when it is presented, and an assertion past its
 * NotOnOrAfter is refused on its own terms, so a late sweep costs table size
 * and nothing else.
 *
 * Per organization, and called in-band from the login path rather than from a
 * scheduled job. That is not a convenience, it is the only version that can
 * work: there is no policy letting an unscoped connection see either table, so
 * a global sweeper running as the application role would match zero rows and
 * report success forever, which is the shape of dead code that looks like a
 * working feature. A scheduled sweep would have to run as the migration role,
 * the way maintenance.ts does, and it is not worth a second privileged
 * connection to clean up rows that expire in ten minutes and are already
 * refused.
 */
export async function sweepOrg(
  pool: Pool,
  orgId: string,
  now: Date,
): Promise<{ states: number; assertions: number }> {
  return pool.withTenant({ orgId }, async (db) => {
    const states = await db.execute<{ n: string }>(sql`
      WITH gone AS (DELETE FROM sso_login_states WHERE expires_at <= ${now.toISOString()} RETURNING 1)
      SELECT count(*) AS n FROM gone`)
    const assertions = await db.execute<{ n: string }>(sql`
      WITH gone AS (DELETE FROM sso_assertions_seen WHERE expires_at <= ${now.toISOString()} RETURNING 1)
      SELECT count(*) AS n FROM gone`)
    return { states: Number(states[0]?.n ?? 0), assertions: Number(assertions[0]?.n ?? 0) }
  })
}

// ---------------------------------------------------------------------------
// Membership
// ---------------------------------------------------------------------------

export interface Member {
  userId: string
  role: Role
  source: string
}

export async function memberByEmail(
  pool: Pool,
  orgId: string,
  email: string,
): Promise<Member | null> {
  const rows = await pool.withTenant({ orgId }, async (db) =>
    db.execute<{ user_id: string; role: Role; source: string }>(sql`
      SELECT m.user_id, m.role, m.source
      FROM members m JOIN users u ON u.id = m.user_id
      WHERE m.org_id = ${orgId} AND lower(u.email) = ${email.toLowerCase()}`),
  )
  const row = rows[0]
  return row ? { userId: row.user_id, role: row.role, source: row.source } : null
}

export async function seatsUsed(pool: Pool, orgId: string): Promise<number> {
  const rows = await pool.withTenant({ orgId }, async (db) =>
    db.execute<{ n: string }>(sql`SELECT count(*) AS n FROM members WHERE org_id = ${orgId}`),
  )
  return Number(rows[0]?.n ?? 0)
}

/** Runs fn inside one tenant transaction, for callers that need several
 *  statements to agree with each other. */
export function inTenant<T>(pool: Pool, orgId: string, fn: (db: Db) => Promise<T>): Promise<T> {
  return pool.withTenant({ orgId }, fn)
}

function asDate(value: unknown): Date {
  return value instanceof Date ? value : new Date(String(value))
}

function asBuffer(value: unknown): Buffer | null {
  if (value === null || value === undefined) return null
  if (Buffer.isBuffer(value)) return value
  if (value instanceof Uint8Array) return Buffer.from(value)
  // postgres.js hands bytea back as a Buffer; a string here means the column
  // was read through a path that hex-encoded it, and decoding the wrong way
  // would produce a key that silently fails to decrypt.
  if (typeof value === 'string' && value.startsWith('\\x')) {
    return Buffer.from(value.slice(2), 'hex')
  }
  throw new Error('a bytea column came back in a shape this does not understand')
}

export { sql }
