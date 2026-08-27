// A real control plane with SCIM mounted, and a real bearer token.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

import postgres from 'postgres'
import { createHash, randomBytes, randomUUID } from 'node:crypto'
import { createPool, migrate, type Pool } from '@antifailure/db'
import { clearExtensions, createServer, registerExtension, FakeClock } from '@antifailure/api'
import { scimExtension } from '../src/index.ts'

export const adminUrl =
  process.env.AF_TEST_DATABASE_URL ?? 'postgres://postgres:test@127.0.0.1:55432/antifailure'

export const BASE_URL = 'https://antifailure.test'

export async function available(): Promise<boolean> {
  // Retried, and fatal when a database was named explicitly.
  //
  // The three-second connect timeout the other harnesses use is fine on an idle
  // machine and wrong on a busy one: under load the probe fails, every suite in
  // the file skips, and the run exits 0 having proved nothing. That is the
  // failure the skip was invented to avoid, arriving through the skip itself.
  //
  // So: three attempts with a longer deadline, and if AF_TEST_DATABASE_URL was
  // set deliberately, an unreachable database THROWS instead of skipping.
  // Setting the variable is a statement that a database is supposed to be
  // there, and quietly proving nothing is not an acceptable answer to it.
  let last: unknown = null
  for (let attempt = 0; attempt < 3; attempt += 1) {
    try {
      const probe = postgres(adminUrl, { max: 1, connect_timeout: 15, onnotice: () => {} })
      await probe`SELECT 1`
      await probe.end({ timeout: 5 })
      return true
    } catch (err) {
      last = err
    }
  }
  if (process.env.AF_TEST_DATABASE_URL) {
    throw new Error(
      `AF_TEST_DATABASE_URL is set to ${adminUrl} and nothing answered after three attempts. ` +
        `Refusing to skip: a run that names a database and then proves nothing is worse than a ` +
        `red one. Underlying error: ${last instanceof Error ? last.message : String(last)}`,
    )
  }
  return false
}

export interface Harness {
  admin: postgres.Sql
  pool: Pool
  clock: FakeClock
  close(): Promise<void>
  /** A SCIM request as a provisioning client makes one. */
  scim(token: string, path: string, init?: RequestInit): Promise<Response>
}

export interface Tenant {
  orgId: string
  slug: string
  token: string
}

export async function start(): Promise<Harness> {
  const admin = postgres(adminUrl, { max: 2, connect_timeout: 30, onnotice: () => {} })
  await migrate(admin)
  await admin.unsafe(`ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`)

  const url = new URL(adminUrl)
  url.username = 'antifailure_app'
  url.password = 'app-test-password'

  const pool = createPool({ url: url.toString(), max: 3, connectTimeoutSeconds: 30, statementTimeoutSeconds: 60 })
  const clock = new FakeClock()

  clearExtensions()
  registerExtension(scimExtension({ pool, clock, baseUrl: BASE_URL, defaultRole: 'member' }))

  const { app } = createServer({
    pool,
    github: {} as never,
    clock,
    secureCookies: false,
    appBaseUrl: 'https://app.antifailure.test/',
  })

  return {
    admin,
    pool,
    clock,
    async scim(token, path, init = {}) {
      const headers = new Headers(init.headers)
      headers.set('authorization', `Bearer ${token}`)
      if (!headers.has('content-type') && init.body) headers.set('content-type', 'application/scim+json')
      // A stable address per token, because the write limit is keyed by token
      // and the read limit falls back to the address when there is none.
      headers.set('x-forwarded-for', '198.51.100.5')
      return app.request(`${BASE_URL}${path}`, { ...init, headers })
    },
    async close() {
      clearExtensions()
      await pool.close()
      await admin.end({ timeout: 5 })
    },
  }
}

export async function seedTenant(h: Harness, label: string): Promise<Tenant> {
  const slug = `${label}-${randomUUID().slice(0, 8)}`
  const [org] = await h.admin<{ id: string }[]>`
    INSERT INTO organizations (slug, name) VALUES (${slug}, ${label}) RETURNING id`
  const orgId = org!.id

  // Assembled at run time. There is no token in the repository.
  const token = `afs_${randomBytes(24).toString('base64url')}`
  await h.admin`
    INSERT INTO scim_tokens (org_id, name, token_hash, prefix)
    VALUES (${orgId}, 'directory', ${createHash('sha256').update(token, 'utf8').digest()},
            ${token.slice(0, 10)})`

  return { orgId, slug, token }
}

export async function dropTenant(h: Harness, orgId: string): Promise<void> {
  await h.admin`DELETE FROM audit_entries WHERE org_id = ${orgId}`
  await h.admin`DELETE FROM organizations WHERE id = ${orgId}`
}

export function user(userName: string, extra: Record<string, unknown> = {}) {
  return JSON.stringify({
    schemas: ['urn:ietf:params:scim:schemas:core:2.0:User'],
    userName,
    name: { givenName: 'Ada', familyName: 'Lovelace' },
    emails: [{ value: userName, type: 'work', primary: true }],
    active: true,
    ...extra,
  })
}

export function patch(operations: Record<string, unknown>[]) {
  return JSON.stringify({
    schemas: ['urn:ietf:params:scim:api:messages:2.0:PatchOp'],
    Operations: operations,
  })
}
