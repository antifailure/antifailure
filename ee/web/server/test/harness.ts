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
//
// A LIST OF STOPPERS RATHER THAN OF STARTED SERVERS, and registered at spawn
// rather than at first successful listen. Two of the cases in entrypoint.test.ts
// assert that a process REFUSES to start. In those, startEntryPoint rejects and
// never builds its Running value, so a list of Running values did not have the
// child on it and could not stop it. The harness promised that everything it
// starts is on this list; this is what makes that true.
const stoppers: Array<() => Promise<void>> = []

/** Stops everything, whatever happened to the test that started it. */
export async function stopAll(): Promise<void> {
  await Promise.all(stoppers.splice(0).map((stop) => stop()))
}

/** How long a process gets to die after SIGKILL before the harness says so. */
const stopDeadlineMs = 10_000

/**
 * Stops one child, whatever state it is in, however many times it is called.
 *
 * THE BUG THIS EXISTS TO END, and it cost a 25 minute CI timeout that reported
 * itself as `cancelled`: this used to resolve early only when `child.exitCode`
 * was not null. `exitCode` is null for a process that died from a SIGNAL, and
 * SIGKILL is how this harness stops one, so the SECOND stop of an
 * already-stopped child decided it was still running, attached an `exit`
 * listener to an event that had already fired, killed a pid that was gone, and
 * never resolved. Two cases stop their own process in a `finally` and their
 * entries stayed on the list, so teardown stopped each of them a second time
 * and `stopAll` never returned. Eleven tests passed and the file hung until the
 * job's clock ran out.
 *
 * `signalCode` is the other half of the same question, so both are asked. The
 * deadline is here because a teardown that hangs says nothing and a teardown
 * that fails says which process would not die.
 */
function stopChild(child: ChildProcess, entry: string): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    if (child.exitCode !== null || child.signalCode !== null) {
      resolve()
      return
    }
    const deadline = setTimeout(() => {
      reject(new Error(
        `${path.basename(entry)} (pid ${String(child.pid)}) did not die within ` +
        `${String(stopDeadlineMs)}ms of SIGKILL. Failing here rather than waiting, because ` +
        `a teardown that hangs is reported as a cancelled job and says nothing at all.`,
      ))
    }, stopDeadlineMs)
    child.once('exit', () => {
      clearTimeout(deadline)
      resolve()
    })
    child.kill('SIGKILL')
  })
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

  // Registered HERE, before the process has been given a chance to fail, and
  // self removing so that a test which stops its own process in a `finally`
  // does not leave a second stop for teardown to trip over.
  const stop = async (): Promise<void> => {
    const at = stoppers.indexOf(stop)
    if (at !== -1) {
      stoppers.splice(at, 1)
    }
    await stopChild(child, entry)
    // The pipes, not just the process. The runner will not exit while a
    // child's stdio is open, and an exited child whose reader is still
    // attached is the shape that hung this file before.
    child.stdout?.destroy()
    child.stderr?.destroy()
  }
  stoppers.push(stop)

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
    stop,
  }
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
  // On the enterprise plan, because that is now a second question from the
  // licence and both have to be answered before a route runs.
  //
  // ee/web/sso resolves a connection through an entitlement check whose
  // authority is the control plane's own catalogue rather than the licence key,
  // and its default is no. An organization seeded with no plan is refused 403,
  // which is not what any case here is about and which would have turned the
  // two 402 cases into passes for the wrong reason: a licence gate that never
  // ran because an entitlement gate refused first is a gate nobody tested.
  // ee/web/scim/test/harness.ts seeds `plan` for the same reason.
  const [org] = await admin<{ id: string }[]>`
    INSERT INTO organizations (slug, name, plan) VALUES (${slug}, 'Enterprise', 'enterprise')
    RETURNING id`
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

// ---------------------------------------------------------------------------
// Why package.json runs the two files as two processes
//
// THE ORIGINAL REASON WAS WRONG AND IS RECORDED HERE BECAUSE IT COST A JOB.
//
// `node --test test/*.test.ts` hung, and this comment used to say that sharing
// a runner was the cause and that finding out which suite held the loop open
// was not worth the cost. Splitting the command did not fix it: on 2026-09-08
// `entrypoint.test.ts` hung ON ITS OWN for twenty minutes after its last test
// passed, and the enterprise job hit its 25 minute wall and reported itself as
// `cancelled`, which reads as concurrency rather than as anything being wrong.
//
// The cause was in this file and it is fixed above. `stop()` decided whether a
// process was still running by reading `exitCode` alone, `exitCode` is null for
// a process killed by a signal, and SIGKILL is how this harness stops one. Two
// cases stop their own process in a `finally` and stayed on the list, so
// teardown stopped them a second time, waited for an `exit` that had already
// fired, and never returned.
//
// Both files now pass in one runner: 31 tests, three consecutive runs, exit 0.
// The two invocations are kept anyway, and now only for the reason that stands
// on its own: a suite that mutates a process-wide registry and a suite that
// spawns servers are not neighbours. Nobody tidying this back into one command
// will get a four minute red, and nobody reading it should be told a cause that
// was never the cause.
