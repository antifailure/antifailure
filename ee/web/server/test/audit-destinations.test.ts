// A hosted organization choosing its own collector, over HTTP, through the real
// registration path, and its audit log arriving there over a real socket.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// WHAT IS REAL AND WHAT IS NOT, said before anything is asserted.
//
// Real: `startControlPlane`, exactly as src/main.ts calls it, with
// `registerEnterprise` in its `beforeServer` hook; the licence gate; the
// organization entitlement from the catalogue; a session issued by the product's
// own issueSession; the configuration routes; the sealing; row level security
// against a real Postgres; the forwarder's poll loop; and an HTTP receiver per
// organization listening on its own port.
//
// Redirected, and only this: the transport. A customer destination must name a
// public host, and the rule that says so refuses every loopback and private
// address on purpose, which is exactly what a receiver in a test is. So
// `registerEnterprise` is given a fetch that sends a request addressed to
// `acme-<id>.collector.example` to the receiver registered under that name. The
// URL the customer typed, the rule it was checked against, the sink that signed
// the batch and the HTTP request itself are all unchanged; only the name
// resolution is the suite's. That is the `fetch` parameter every sink in
// sinks.ts takes, used for the reason it was added.
//
// Why not src/main.ts in a child process, like audit-stream.test.ts: a child
// process cannot be given a fetch, and weakening the customer rule to let a
// loopback receiver through would be proving delivery through a hole the suite
// cut. audit-stream.test.ts still proves the process starts a forwarder; this
// proves the per organization half of it.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import postgres from 'postgres'
import { randomBytes, randomUUID } from 'node:crypto'
import { startControlPlane, type ControlPlane } from '@antifailure/api/boot'
import { CSRF_HEADER, SESSION_COOKIE, csrfTokenFor, issueSession, systemClock } from '@antifailure/api'
import { appendAudit, migrate } from '@antifailure/db'
import { manifestKeyFor, verify, verifyWebhook, type Batch, type Fetcher } from '@antifailure-ee/audit'
import { registerEnterprise, type Registered } from '../src/register.ts'
import { adminUrl, appUrl, available, licenseFor, signingKey } from './harness.ts'

const hasDatabase = await available()

interface Delivery { body: string; headers: Record<string, string> }

interface Receiver {
  host: string
  port: number
  deliveries: Delivery[]
  status: number
  batches(): Batch[]
  close(): Promise<void>
}

async function receiver(label: string): Promise<Receiver> {
  const deliveries: Delivery[] = []
  const made: Receiver = {
    host: `${label}-${randomUUID().slice(0, 8)}.collector.example`,
    port: 0,
    deliveries,
    status: 200,
    batches: () => deliveries.map((d) => JSON.parse(d.body) as Batch),
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  }
  const server: Server = createServer((req, res) => {
    let body = ''
    req.on('data', (c) => { body += String(c) })
    req.on('end', () => {
      const headers: Record<string, string> = {}
      for (const [k, v] of Object.entries(req.headers)) headers[k] = String(v)
      deliveries.push({ body, headers })
      res.writeHead(made.status).end()
    })
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  made.port = (server.address() as AddressInfo).port
  return made
}

async function until(what: string, check: () => Promise<boolean> | boolean, ms = 30_000): Promise<void> {
  const deadline = Date.now() + ms
  for (;;) {
    if (await check()) return
    if (Date.now() > deadline) throw new Error(`timed out after ${String(ms)}ms waiting for ${what}`)
    await new Promise((r) => setTimeout(r, 100))
  }
}

describe(
  'an organization chooses its own audit collector over HTTP and receives its own log there',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let admin: postgres.Sql
    let plane: ControlPlane
    let registered: Registered | undefined
    const receivers = new Map<string, Receiver>()
    /** Every credential the suite hands the product, so the whole output can be
     *  searched for each one. Compared, never printed. */
    const credentials: string[] = []
    /** Everything the process wrote to stdout and stderr while it ran. */
    let captured = ''
    const restore: (() => void)[] = []

    const fetcher: Fetcher = (url, init) => {
      const target = new URL(url)
      const r = receivers.get(target.host)
      if (!r) return Promise.reject(new Error(`the suite has no receiver named ${target.host}`))
      return fetch(`http://127.0.0.1:${String(r.port)}${target.pathname}${target.search}`, init)
    }

    interface Person { userId: string; cookie: string; csrf: string }
    interface Org { orgId: string; slug: string; owner: Person; viewer: Person }

    async function person(orgId: string, role: string): Promise<Person> {
      const slug = `p-${randomUUID().slice(0, 8)}`
      const [user] = await admin<{ id: string }[]>`
        INSERT INTO users (github_id, github_login, email, name)
        VALUES (${Math.floor(Math.random() * 1e12)}, ${slug}, ${`${slug}@example.test`}, ${slug})
        RETURNING id`
      await admin`INSERT INTO members (org_id, user_id, role) VALUES (${orgId}, ${user!.id}, ${role})`
      const issued = await issueSession(plane.pool, systemClock, { userId: user!.id, orgId })
      return {
        userId: user!.id,
        cookie: `${SESSION_COOKIE}=${issued.token}`,
        csrf: csrfTokenFor(issued.token),
      }
    }

    async function org(plan = 'enterprise'): Promise<Org> {
      const slug = `dest-${randomUUID().slice(0, 8)}`
      const [row] = await admin<{ id: string }[]>`
        INSERT INTO organizations (slug, name, plan) VALUES (${slug}, 'Destination', ${plan})
        RETURNING id`
      return { orgId: row!.id, slug, owner: await person(row!.id, 'owner'), viewer: await person(row!.id, 'viewer') }
    }

    function call(who: Person, method: string, body?: unknown, csrf = true): Promise<Response> {
      return fetch(`http://127.0.0.1:${String(plane.port)}/enterprise/audit-stream`, {
        method,
        headers: {
          cookie: who.cookie,
          'content-type': 'application/json',
          'x-forwarded-for': `198.51.100.${String(Math.floor(Math.random() * 200) + 1)}`,
          ...(csrf ? { [CSRF_HEADER]: who.csrf } : {}),
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      })
    }

    const responses: string[] = []
    async function json(res: Response): Promise<Record<string, unknown>> {
      const text = await res.text()
      responses.push(text)
      return JSON.parse(text) as Record<string, unknown>
    }

    async function writeFor(orgId: string, action: string): Promise<number> {
      const url = new URL(adminUrl)
      url.username = 'antifailure_app'
      url.password = 'app-test-password'
      const written = await plane.pool.withTenant({ orgId }, (db) =>
        appendAudit(db, { orgId, actorLabel: 'the suite', action, targetType: 'organization', origin: 'system' }))
      return written.seq
    }

    before(async () => {
      admin = postgres(adminUrl, { max: 3, connect_timeout: 30, onnotice: () => {} })
      await migrate(admin)
      await admin.unsafe(`ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`)

      for (const stream of [process.stdout, process.stderr]) {
        const original = stream.write.bind(stream)
        stream.write = ((chunk: unknown, ...rest: unknown[]) => {
          captured += String(chunk)
          return (original as (...a: unknown[]) => boolean)(chunk, ...rest)
        }) as typeof stream.write
        restore.push(() => { stream.write = original })
      }

      const key = signingKey()
      const env: Record<string, string> = {
        AF_DATABASE_URL: appUrl(),
        AF_GITHUB_CLIENT_ID: 'id',
        AF_GITHUB_CLIENT_SECRET: 'secret',
        AF_GITHUB_REDIRECT_URI: 'https://app.test/auth/github/callback',
        AF_APP_BASE_URL: 'https://enterprise.test',
        AF_INSECURE_COOKIES: '1',
        AF_MIGRATE: '0',
        AF_PORT: '0',
        AF_EE_SSO_KEY: randomBytes(32).toString('base64'),
        AF_LICENSE_PUBLIC_KEYS: key.publicKeys,
        AF_LICENSE_KEY: licenseFor(key, 'acme-installation', ['sso', 'scim', 'audit_stream']),
        AF_ORG: 'acme-installation',
        AF_PROVIDER_KEY_SECRET: randomBytes(32).toString('base64'),
        AF_AUDIT_STREAM_INTERVAL_MS: '200',
      }
      Object.assign(process.env, env)
      delete process.env.AF_AUDIT_STREAM_SINK

      plane = await startControlPlane({
        beforeServer: (ctx) => {
          registered = registerEnterprise({
            pool: ctx.pool,
            clock: ctx.clock,
            baseUrl: 'https://enterprise.test',
            appBaseUrl: 'https://enterprise.test/',
            secureCookies: ctx.secureCookies,
            env: ctx.env,
            log: ctx.log,
            fetch: fetcher,
          })
        },
        beforeClose: () => registered?.auditStream?.stop(),
      })
    })

    after(async () => {
      await plane?.close()
      for (const r of receivers.values()) await r.close()
      for (const undo of restore) undo()
      await admin.end({ timeout: 5 })
    })

    // -----------------------------------------------------------------------

    it('registration mounts the routes and starts a forwarder with no installation sink', () => {
      assert.ok(registered, 'registerEnterprise never ran')
      assert.ok(registered.mounted.includes('audit-stream'), 'the configuration routes were not mounted')
      assert.ok(registered.auditStream, 'no forwarder was started, so a saved destination would receive nothing')
      assert.match(captured, /to each organization's own destination where it has chosen one/)
    })

    it('two organizations each save a collector over HTTP and each receives its own log there and nothing of the other', async () => {
      const a = await org()
      const b = await org()
      const ra = await receiver('acme')
      const rb = await receiver('bravo')
      receivers.set(ra.host, ra)
      receivers.set(rb.host, rb)
      const secretA = `cred_${randomBytes(24).toString('base64url')}`
      const secretB = `cred_${randomBytes(24).toString('base64url')}`
      credentials.push(secretA, secretB)

      const savedA = await call(a.owner, 'PUT', { kind: 'webhook', url: `https://${ra.host}/ingest`, credential: secretA })
      const viewA = await json(savedA)
      assert.equal(savedA.status, 200, `the owner could not save: ${String(viewA.error)}`)
      const shown = viewA.destination as { credential: { last4: string } }
      assert.equal(shown.credential.last4, secretA.slice(-4))
      const savedB = await call(b.owner, 'PUT', { kind: 'webhook', url: `https://${rb.host}/ingest`, credential: secretB })
      assert.equal(savedB.status, 200)
      await json(savedB)

      const seqA = await writeFor(a.orgId, 'dest.http.a')
      const seqB = await writeFor(b.orgId, 'dest.http.b')

      const entriesOf = (r: Receiver) => r.batches().flatMap((batch) => batch.entries)
      await until("A's entry at A's collector", () => entriesOf(ra).some((e) => e.seq === seqA))
      await until("B's entry at B's collector", () => entriesOf(rb).some((e) => e.seq === seqB))

      assert.deepEqual(
        entriesOf(ra).map((e) => e.action), ['audit_stream.destination.saved', 'dest.http.a'],
        "A's collector did not receive the record of its save, made by the real route, and then A's entry",
      )
      assert.ok(entriesOf(ra).every((e) => e.orgId === a.orgId), "an entry that is not A's reached A's collector")
      assert.ok(entriesOf(rb).every((e) => e.orgId === b.orgId), "an entry that is not B's reached B's collector")

      for (const [r, secret, other] of [[ra, secretA, secretB], [rb, secretB, secretA]] as const) {
        for (const d of r.deliveries) {
          assert.equal(
            verifyWebhook(secret, d.headers['x-antifailure-timestamp']!, d.body, d.headers['x-antifailure-signature']!),
            true, 'a delivery does not verify under the organization secret',
          )
          assert.ok(!JSON.stringify(d).includes(other), "the other organization's secret reached this collector")
        }
        for (const batch of r.batches()) {
          assert.equal(verify(batch, manifestKeyFor(secret)).ok, true, 'a manifest does not verify under the derived key')
        }
      }

      const state = await json(await call(a.viewer, 'GET'))
      const delivery = state.delivery as { lastDeliveredAt: string | null; lastError: string | null }
      assert.ok(delivery.lastDeliveredAt, 'a member cannot see that the stream delivered')
      assert.equal(delivery.lastError, null)
    })

    it('a viewer, a request with no CSRF header and an unentitled organization are refused, and nothing is stored', async () => {
      const a = await org()
      const free = await org('free')
      const r = await receiver('refused')
      receivers.set(r.host, r)
      const secret = `cred_${randomBytes(24).toString('base64url')}`
      credentials.push(secret)
      const body = { kind: 'webhook', url: `https://${r.host}/ingest`, credential: secret }

      const byViewer = await call(a.viewer, 'PUT', body)
      assert.equal(byViewer.status, 403)
      await json(byViewer)
      const noCsrf = await call(a.owner, 'PUT', body, false)
      assert.equal(noCsrf.status, 403)
      await json(noCsrf)
      const unentitled = await call(free.owner, 'PUT', body)
      assert.equal(unentitled.status, 402)
      await json(unentitled)

      const stored = await admin`
        SELECT 1 FROM audit_stream_destinations WHERE org_id IN (${a.orgId}, ${free.orgId})`
      assert.equal(stored.length, 0, 'a refused request stored a destination')
    })

    it("a destination naming this control plane's own network is refused when saved", async () => {
      const a = await org()
      const secret = `cred_${randomBytes(24).toString('base64url')}`
      credentials.push(secret)
      for (const url of [`https://127.0.0.1:${String(plane.port)}/ingest`, 'http://collector.example/ingest', 'https://169.254.169.254/metadata']) {
        const res = await call(a.owner, 'PUT', { kind: 'webhook', url, credential: secret })
        assert.equal(res.status, 400, `${url} was accepted`)
        const refusal = await json(res)
        assert.ok(!String(refusal.error).includes(secret))
      }
      assert.equal((await admin`SELECT 1 FROM audit_stream_destinations WHERE org_id = ${a.orgId}`).length, 0)
    })

    it('a collector that starts refusing is reported to the organization, and a removed destination receives nothing more', async () => {
      const a = await org()
      const b = await org()
      const ra = await receiver('failing')
      const rb = await receiver('heartbeat')
      receivers.set(ra.host, ra)
      receivers.set(rb.host, rb)
      const secretA = `cred_${randomBytes(24).toString('base64url')}`
      const secretB = `cred_${randomBytes(24).toString('base64url')}`
      credentials.push(secretA, secretB)
      assert.equal((await call(a.owner, 'PUT', { kind: 'webhook', url: `https://${ra.host}/ingest`, credential: secretA })).status, 200)
      assert.equal((await call(b.owner, 'PUT', { kind: 'webhook', url: `https://${rb.host}/ingest`, credential: secretB })).status, 200)
      await until('the first delivery to A', () => ra.deliveries.length > 0)

      ra.status = 401
      // A real route writes the entry that meets the refusal.
      assert.equal((await call(a.owner, 'PATCH', { enabled: true })).status, 200)
      await until('the refusal to be visible to the organization', async () => {
        const state = await json(await call(a.viewer, 'GET'))
        const delivery = state.delivery as { lastError: string | null; consecutiveFailures: number }
        return /answered 401/.test(delivery.lastError ?? '') && delivery.consecutiveFailures >= 1
      })

      const removed = await call(a.owner, 'DELETE')
      assert.equal(removed.status, 200)
      await json(removed)
      const before = ra.deliveries.length
      const orphan = await writeFor(a.orgId, 'after.removal')
      const heartbeat = await writeFor(b.orgId, 'after.removal.heartbeat')
      assert.ok(heartbeat > orphan)
      // A POSITIVE signal that a pass ran after the orphan committed: B's later
      // entry arriving. Only then is A's silence evidence rather than luck.
      await until("B's later entry", () => rb.batches().some((batch) => batch.entries.some((e) => e.seq === heartbeat)))
      assert.equal(ra.deliveries.length, before, 'a removed destination kept receiving')
    })

    it('no credential appears in any response, any line the process printed, or any stored column', async () => {
      assert.ok(credentials.length >= 6, 'the cases above did not run, so this proves nothing')
      const printed = captured
      const answered = responses.join('\n')
      const stored = JSON.stringify(await admin`
        SELECT org_id, kind, url, fingerprint, last4, encode(ciphertext, 'escape') AS c
        FROM audit_stream_destinations`)
      const logged = JSON.stringify(await admin`
        SELECT detail FROM audit_entries WHERE action LIKE 'audit_stream.%'`)
      let found = 0
      for (const secret of credentials) {
        if (printed.includes(secret)) found += 1
        if (answered.includes(secret)) found += 1
        if (stored.includes(secret)) found += 1
        if (logged.includes(secret)) found += 1
      }
      assert.equal(found, 0, `a credential appeared in ${String(found)} place(s) it must never be`)
    })
  },
)
