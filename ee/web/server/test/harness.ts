// A real enterprise control plane, as a process, on a real port.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// WHY A SUBPROCESS AND A SOCKET, WHEN THE SISTER SUITES USE app.request().
//
// ee/web/sso/test/harness.ts builds a server in process and drives it with
// app.request(). That is the right instrument for what it proves, which is that
// the routes behave, and it registers the extensions itself, "the way the
// enterprise entry point registers it". The thing it cannot prove is the thing
// that was actually wrong: that anything registers them at all. A suite that
// performs the registration under test is a suite that would have stayed green
// through the entire defect, and it did, for as long as the packages have
// existed.
//
// So this one starts src/main.ts as a process, with an environment and nothing
// else, and reaches it over TCP. Everything between the environment and the
// answer is real: boot.ts reads the variables, opens the pool, calls the seam,
// createServer walks the extension routes, and @hono/node-server puts them on a
// socket. If any link in that chain is missing, no request is answered, which
// is what "reachable by nobody" looked like from the outside.

import postgres from 'postgres'
import { spawn, type ChildProcess } from 'node:child_process'
import { generateKeyPairSync, randomBytes, randomUUID, sign } from 'node:crypto'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
export const ENTERPRISE_ENTRY = path.join(here, '..', 'src', 'main.ts')
export const COMMUNITY_ENTRY = path.join(
  here, '..', '..', '..', '..', 'web', 'apps', 'api', 'src', 'main.ts',
)

export const adminUrl =
  process.env.AF_TEST_DATABASE_URL ?? 'postgres://postgres:test@127.0.0.1:55432/antifailure'

export function appUrl(): string {
  const u = new URL(adminUrl)
  u.username = 'antifailure_app'
  u.password = 'app-test-password'
  return u.toString()
}

export async function available(): Promise<boolean> {
  // Retried, and fatal when a database was named explicitly. Setting
  // AF_TEST_DATABASE_URL is a statement that a database is supposed to be
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

/** A signing key and the AF_LICENSE_PUBLIC_KEYS value that trusts it.
 *
 *  Generated per run. There is no key material in the repository, in a clone of
 *  it, or in an image built from it, which is the same rule the sister suites
 *  follow for the encryption key. */
export function signingKey(): { kid: string; sign: (claims: object) => string; publicKeys: string } {
  const kid = `test-${randomUUID().slice(0, 8)}`
  const { publicKey, privateKey } = generateKeyPairSync('ed25519')
  const raw = publicKey.export({ format: 'der', type: 'spki' }).subarray(12)
  return {
    kid,
    publicKeys: `${kid}=${raw.toString('base64')}`,
    sign: (claims) => {
      const payload = Buffer.from(JSON.stringify({ ...claims, kid }), 'utf8')
      const signature = sign(null, payload, privateKey)
      return `aflic_${payload.toString('base64url')}.${signature.toString('base64url')}`
    },
  }
}

/** A licence that permits exactly these features, for this organization. */
export function licenseFor(
  key: ReturnType<typeof signingKey>,
  org: string,
  features: string[],
  overrides: { seats?: number; expiresAt?: string; trial?: boolean } = {},
): string {
  return key.sign({
    id: `lic-${randomUUID().slice(0, 8)}`,
    org,
    plan: 'enterprise',
    features,
    seats: overrides.seats ?? 0,
    issued_at: new Date(Date.now() - 60_000).toISOString(),
    expires_at: overrides.expiresAt ?? new Date(Date.now() + 365 * 24 * 3600 * 1000).toISOString(),
    trial: overrides.trial ?? false,
  })
}

// Every process this harness starts, so none can be left behind.
//
// A leak here is not untidiness. The first run of the mutation matrix hung for
// fifteen minutes on one cell, because a case that expects a start-up refusal
// got a process that started, and the test failed WITHOUT stopping it: node's
// test runner will not exit while a child's stdio pipes are open. A harness
// whose cleanup depends on the test taking the happy path is a harness that
// stops working exactly when a test fails, which is the only time it matters.
const started: Running[] = []

/** Stops everything, whatever happened to the test that started it. */
export async function stopAll(): Promise<void> {
  await Promise.all(started.splice(0).map((r) => r.stop()))
}

export interface Running {
  port: number
  /** Everything the process printed, which is where the startup lines about
   *  what was mounted and what the licence permits are read from. */
  output: () => string
  get(pathname: string, init?: RequestInit): Promise<Response>
  stop(): Promise<void>
}

/**
 * Starts an entry point and waits for it to be listening.
 *
 * AF_PORT=0 so the kernel chooses, because a suite that picks a port races
 * every other suite on the machine. The chosen one is read out of the line
 * boot.ts prints, which is also a check that the line exists.
 */
export async function startEntryPoint(
  entry: string,
  env: Record<string, string>,
): Promise<Running> {
  const child: ChildProcess = spawn(process.execPath, [entry], {
    env: {
      ...process.env,
      AF_DATABASE_URL: appUrl(),
      AF_GITHUB_CLIENT_ID: 'id',
      AF_GITHUB_CLIENT_SECRET: 'secret',
      AF_GITHUB_REDIRECT_URI: 'https://app.test/auth/github/callback',
      AF_APP_BASE_URL: 'https://enterprise.test',
      AF_INSECURE_COOKIES: '1',
      AF_MIGRATE: '0',
      AF_PORT: '0',
      // 32 random bytes, assembled at run time. keyFromEnv refuses a single
      // repeated byte, which is the shape of a placeholder somebody meant to
      // replace, so this cannot be a constant even in a test.
      AF_EE_SSO_KEY: randomBytes(32).toString('base64'),
      ...env,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  })

  let output = ''
  child.stdout?.on('data', (c) => { output += String(c) })
  child.stderr?.on('data', (c) => { output += String(c) })

  const port = await new Promise<number>((resolve, reject) => {
    // Thirty seconds, because the first start on a cold database opens a pool
    // and reads the console build. A start-up that never listens is itself a
    // failure, so this rejects with everything the process said rather than
    // hanging the suite until the runner kills it.
    const timer = setTimeout(() => {
      child.kill('SIGKILL')
      reject(new Error(`${path.basename(entry)} never listened in 30s. It said:\n${output}`))
    }, 30_000)
    const look = (): void => {
      const match = /control plane listening on :(\d+)/.exec(output)
      if (!match) return
      clearTimeout(timer)
      clearInterval(poll)
      resolve(Number(match[1]))
    }
    const poll = setInterval(look, 50)
    child.on('exit', (code) => {
      clearTimeout(timer)
      clearInterval(poll)
      reject(new Error(`${path.basename(entry)} exited ${code} before listening. It said:\n${output}`))
    })
  })

  const running: Running = {
    port,
    output: () => output,
    get: (pathname, init) =>
      fetch(`http://127.0.0.1:${port}${pathname}`, {
        // Every limit on these routes is keyed by address, and the whole suite
        // shares one. A distinct forwarded address per request keeps a later
        // case from being answered 429 because an earlier one used the budget,
        // which would read as a refusal and mean nothing.
        ...init,
        headers: {
          'x-forwarded-for': `198.51.100.${Math.floor(Math.random() * 200) + 1}`,
          ...(init?.headers ?? {}),
        },
      }),
    stop: () =>
      new Promise<void>((resolve) => {
        if (child.exitCode !== null) {
          resolve()
          return
        }
        child.on('exit', () => resolve())
        child.kill('SIGKILL')
      }),
  }
  started.push(running)
  return running
}

export interface Seeded {
  orgId: string
  slug: string
  handle: string
}

/** One organization with a SAML connection, so the metadata route has something
 *  to answer with and a 404 from it means the route is absent rather than the
 *  row. */
export async function seed(admin: postgres.Sql): Promise<Seeded> {
  const slug = `ee-${randomUUID().slice(0, 8)}`
  const [org] = await admin<{ id: string }[]>`
    INSERT INTO organizations (slug, name) VALUES (${slug}, 'Enterprise') RETURNING id`
  const orgId = org!.id
  const handle = randomBytes(32).toString('base64url')
  // The entity id is unique per seed, not a constant.
  //
  // sso_connections carries a unique index on idp_entity_id, which is correct:
  // two organizations pointing at one identity provider entity is a mistake
  // worth refusing. A constant here meant this suite could be run once against
  // a database and never again, and the second run failed inside
  // _bt_check_unique with no connection to what it was testing. It cost two
  // debugging cycles in the session that wrote it, once blamed on leftover
  // state and once on a leaked process, and it was neither.
  await admin`
    INSERT INTO sso_connections (
      org_id, handle, kind, display_name, enabled, default_role,
      idp_entity_id, idp_sso_url, idp_certificates)
    VALUES (
      ${orgId}, ${handle}, 'saml', 'Directory', true, 'member',
      ${`https://idp.test/${slug}/metadata`}, 'https://idp.test/sso', ${admin.array([] as string[])})`
  return { orgId, slug, handle }
}
