// An audit entry, written by a real route, watched arriving at a real sink.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// WHY THIS FILE EXISTS WHEN ee/web/audit ALREADY HAS A SUITE. That one drives
// the Forwarder directly and proves every ordering it can meet. What it cannot
// prove is the thing that was actually wrong, which is that anything starts one
// at all: `ee/web/audit` was a complete, tested, green package that no code
// path imported, and a suite that constructs the class under test would have
// stayed green through the entire defect. It did, for as long as the package
// existed.
//
// So this one starts `src/main.ts` as a process with nothing but an
// environment, POSTs a SCIM user to it over TCP, and waits on a socket of its
// own for the audit entry that provisioning writes. Every link is real:
// boot.ts reads the variables, registerEnterprise builds the forwarder from
// AF_AUDIT_STREAM_SINK, the SCIM route authenticates a real bearer token,
// appendAudit chains the entry, the poll loop reads it across tenants under the
// policy migration 0043 adds, and the webhook sink signs and posts it.
//
// THE NEGATIVE CONTROLS ARE THE POINT AND BOTH ARE SHAPED TO BE ABLE TO SAY NO.
// "Nothing arrived in five seconds" is satisfied by a broken receiver, a
// process that never started, and a firewall. So neither negative here waits
// for silence. Each waits for a POSITIVE signal that the forwarder ran and made
// a decision, which is the cursor advancing past the entry, and only then
// asserts that the entry was not sent.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import postgres from 'postgres'
import { createHash, randomBytes, randomUUID } from 'node:crypto'
import { verifyWebhook } from '@antifailure-ee/audit'
import {
  ENTERPRISE_ENTRY,
  adminUrl,
  available,
  licenseFor,
  signingKey,
  startEntryPoint,
  stopAll,
} from './harness.ts'

const hasDatabase = await available()

/** One delivery as the receiver saw it, before anything is parsed, so the
 *  signature can be checked over the exact bytes. */
interface Delivery {
  body: string
  timestamp: string
  signature: string
}

/** A real HTTP receiver, because the claim is that entries reach a sink and a
 *  sink is somebody else's socket. */
async function receiver(): Promise<{
  url: string
  deliveries: Delivery[]
  entries: () => { seq: number; orgId: string; action: string; entryHash: string }[]
  close: () => Promise<void>
}> {
  const deliveries: Delivery[] = []
  const server: Server = createServer((req, res) => {
    let body = ''
    req.on('data', (c) => { body += String(c) })
    req.on('end', () => {
      deliveries.push({
        body,
        timestamp: String(req.headers['x-antifailure-timestamp'] ?? ''),
        signature: String(req.headers['x-antifailure-signature'] ?? ''),
      })
      res.writeHead(200).end('{}')
    })
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const port = (server.address() as AddressInfo).port
  return {
    url: `http://127.0.0.1:${String(port)}/ingest`,
    deliveries,
    entries: () =>
      deliveries.flatMap(
        (d) => (JSON.parse(d.body) as { entries: { seq: number; orgId: string; action: string; entryHash: string }[] }).entries,
      ),
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  }
}

/** Waits for a condition, or fails saying what it was still waiting for.
 *  Polling rather than sleeping, so a fast machine is fast and a slow one is
 *  not flaky. */
async function until(what: string, check: () => Promise<boolean> | boolean, ms = 30_000): Promise<void> {
  const deadline = Date.now() + ms
  for (;;) {
    if (await check()) return
    if (Date.now() > deadline) throw new Error(`timed out after ${String(ms)}ms waiting for ${what}`)
    await new Promise((r) => setTimeout(r, 100))
  }
}

describe(
  'the control plane audit log reaches a sink from the real entry point',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let admin: postgres.Sql

    before(async () => {
      admin = postgres(adminUrl, { max: 3, connect_timeout: 30, onnotice: () => {} })
    })

    after(async () => {
      await stopAll()
      await admin.end({ timeout: 5 })
    })

    /** An organization on a plan, with a SCIM bearer token so a real
     *  provisioning request can be made against the running process. */
    async function seedOrg(plan: string): Promise<{ orgId: string; slug: string; token: string }> {
      const slug = `stream-${randomUUID().slice(0, 8)}`
      const [org] = await admin<{ id: string }[]>`
        INSERT INTO organizations (slug, name, plan) VALUES (${slug}, 'Stream', ${plan})
        RETURNING id`
      const token = `afs_${randomBytes(24).toString('base64url')}`
      await admin`
        INSERT INTO scim_tokens (org_id, name, token_hash, prefix)
        VALUES (${org!.id}, 'directory', ${createHash('sha256').update(token, 'utf8').digest()},
                ${token.slice(0, 10)})`
      return { orgId: org!.id, slug, token }
    }

    /** Parks the global cursor at the head, so a case reads only what it
     *  writes. The enterprise job runs every suite against one database. */
    async function parkCursor(): Promise<number> {
      const rows = await admin<{ seq: string }[]>`
        SELECT coalesce(max(seq), 0) AS seq FROM audit_entries`
      const at = Number(rows[0]!.seq)
      await admin`UPDATE audit_stream_cursor SET delivered_seq = ${at} WHERE id`
      await admin`
        INSERT INTO audit_stream_positions (org_id, delivered_seq)
        SELECT org_id, max(seq) FROM audit_entries GROUP BY org_id
        ON CONFLICT (org_id) DO UPDATE SET delivered_seq = EXCLUDED.delivered_seq`
      return at
    }

    async function cursor(): Promise<number> {
      const rows = await admin<{ delivered_seq: string }[]>`
        SELECT delivered_seq FROM audit_stream_cursor WHERE id`
      return Number(rows[0]!.delivered_seq)
    }

    async function highestFor(orgId: string): Promise<number> {
      const rows = await admin<{ seq: string | null }[]>`
        SELECT coalesce(max(seq), 0) AS seq FROM audit_entries WHERE org_id = ${orgId}`
      return Number(rows[0]!.seq)
    }

    // -----------------------------------------------------------------------

    it('a SCIM user created over HTTP arrives at the sink, signed, with its chain hash', async () => {
      const sink = await receiver()
      const org = await seedOrg('enterprise')
      const unlicensed = await seedOrg('free')
      await parkCursor()

      const key = signingKey()
      const secret = randomBytes(32).toString('base64')
      const running = await startEntryPoint(ENTERPRISE_ENTRY, {
        AF_LICENSE_PUBLIC_KEYS: key.publicKeys,
        AF_LICENSE_KEY: licenseFor(key, org.slug, ['sso', 'scim', 'audit_stream']),
        AF_ORG: org.slug,
        AF_AUDIT_STREAM_SINK: 'webhook',
        AF_AUDIT_STREAM_WEBHOOK_URL: sink.url,
        AF_AUDIT_STREAM_WEBHOOK_SECRET: secret,
        AF_AUDIT_STREAM_KEY: randomBytes(32).toString('base64'),
        // Fast, because this suite is waiting on it. The default is ten
        // seconds, which is right for a control plane and wrong for a test.
        AF_AUDIT_STREAM_INTERVAL_MS: '200',
      })

      try {
        assert.match(
          running.output(),
          /audit stream: forwarding to webhook for every organization without its own destination/,
          `the process did not say it had started a forwarder. It said:\n${running.output()}`,
        )

        // THE ENTRY, WRITTEN BY A ROUTE AND NOT BY THIS FILE. usersCreate calls
        // appendAudit with action scim.user.created, inside the same
        // transaction as the membership, on a request authenticated by a real
        // bearer token against a real scim_tokens row.
        const userName = `ada-${randomUUID().slice(0, 8)}@example.test`
        const created = await fetch(`http://127.0.0.1:${String(running.port)}/scim/v2/Users`, {
          method: 'POST',
          headers: {
            authorization: `Bearer ${org.token}`,
            'content-type': 'application/scim+json',
            'x-forwarded-for': '198.51.100.7',
          },
          body: JSON.stringify({
            schemas: ['urn:ietf:params:scim:schemas:core:2.0:User'],
            userName,
            name: { givenName: 'Ada', familyName: 'Lovelace' },
            emails: [{ value: userName, type: 'work', primary: true }],
            active: true,
          }),
        })
        assert.equal(created.status, 201, `SCIM refused the create: ${await created.text()}`)

        // And one for the organization that is not entitled, written through
        // the same appendAudit every entry point uses. It cannot be written
        // through SCIM, because the entitlement gate refuses that request
        // first, which would prove the wrong gate.
        const { createPool, appendAudit } = await import('@antifailure/db')
        const appUrlValue = new URL(adminUrl)
        appUrlValue.username = 'antifailure_app'
        appUrlValue.password = 'app-test-password'
        const pool = createPool({ url: appUrlValue.toString(), max: 2, connectTimeoutSeconds: 30 })
        let refusedSeq = 0
        try {
          const written = await pool.withTenant({ orgId: unlicensed.orgId }, (db) =>
            appendAudit(db, {
              orgId: unlicensed.orgId,
              actorLabel: 'directory',
              action: 'scim.user.created',
              targetType: 'user',
              origin: 'scim',
              detail: { userName: 'nobody@example.test' },
            }),
          )
          refusedSeq = written.seq
        } finally {
          await pool.close()
        }

        // THE THING THIS WHOLE LANE IS ABOUT.
        await until('the SCIM entry to arrive at the webhook', () =>
          sink.entries().some((e) => e.orgId === org.orgId && e.action === 'scim.user.created'),
        )

        const got = sink.entries().find((e) => e.orgId === org.orgId && e.action === 'scim.user.created')!
        const stored = await admin<{ entry_hash: string; seq: string }[]>`
          SELECT entry_hash, seq FROM audit_entries
          WHERE org_id = ${org.orgId} AND action = 'scim.user.created'
          ORDER BY seq DESC LIMIT 1`
        assert.equal(
          got.entryHash, stored[0]!.entry_hash,
          'the hash that arrived is not the one the chain stored, so the tamper evidence does ' +
            'not survive the trip and the sentence in ee/README.md is still false',
        )
        assert.equal(got.seq, Number(stored[0]!.seq))

        // Signed over the exact bytes, checked with the function that ships for
        // a receiver to use. A delivery anybody could forge is not evidence.
        const delivery = sink.deliveries.find((d) => d.body.includes(got.entryHash))!
        assert.ok(delivery, 'no delivery carried the entry, so the entry above came from nowhere')
        assert.equal(
          verifyWebhook(secret, delivery.timestamp, delivery.body, delivery.signature), true,
          'the delivery does not verify under the secret the process was given',
        )
        assert.equal(
          verifyWebhook('the wrong secret', delivery.timestamp, delivery.body, delivery.signature),
          false,
          'the delivery verifies under a secret that did not sign it, so the check proves nothing',
        )

        // THE ENTITLEMENT NEGATIVE, and it waits for a decision rather than for
        // silence: the cursor moving past that sequence number is the forwarder
        // saying it read the entry and chose. Only then is "nothing was sent"
        // a statement about the gate.
        await until(
          'the forwarder to have read and decided about the unlicensed entry',
          async () => (await cursor()) >= refusedSeq,
        )
        assert.equal(
          sink.entries().filter((e) => e.orgId === unlicensed.orgId).length, 0,
          'an organization on the free plan had its audit log forwarded, so the entitlement ' +
            'gate is not gating',
        )
      } finally {
        await running.stop()
        await sink.close()
      }
    })

    it('a licence that does not permit audit_stream forwards nothing, having read the entry', async () => {
      const sink = await receiver()
      const org = await seedOrg('enterprise')
      await parkCursor()

      const key = signingKey()
      // Entitled by plan, refused by the licence key. The two gates are
      // separate authorities and this case is the one that proves the second
      // one is asked at all: the organization is on the enterprise plan, so
      // `licensed` says yes, and only the key can refuse.
      const running = await startEntryPoint(ENTERPRISE_ENTRY, {
        AF_LICENSE_PUBLIC_KEYS: key.publicKeys,
        AF_LICENSE_KEY: licenseFor(key, org.slug, ['sso', 'scim']),
        AF_ORG: org.slug,
        AF_AUDIT_STREAM_SINK: 'webhook',
        AF_AUDIT_STREAM_WEBHOOK_URL: sink.url,
        AF_AUDIT_STREAM_WEBHOOK_SECRET: randomBytes(32).toString('base64'),
        AF_AUDIT_STREAM_KEY: randomBytes(32).toString('base64'),
        AF_AUDIT_STREAM_INTERVAL_MS: '200',
      })

      try {
        assert.match(
          running.output(),
          /enterprise features permitted right now: scim, sso/,
          'the process did not start without audit_stream, so this case is about the wrong licence',
        )

        const userName = `ada-${randomUUID().slice(0, 8)}@example.test`
        const created = await fetch(`http://127.0.0.1:${String(running.port)}/scim/v2/Users`, {
          method: 'POST',
          headers: {
            authorization: `Bearer ${org.token}`,
            'content-type': 'application/scim+json',
            'x-forwarded-for': '198.51.100.8',
          },
          body: JSON.stringify({
            schemas: ['urn:ietf:params:scim:schemas:core:2.0:User'],
            userName,
            emails: [{ value: userName, type: 'work', primary: true }],
            active: true,
          }),
        })
        assert.equal(created.status, 201, `SCIM refused the create: ${await created.text()}`)

        const seq = await highestFor(org.orgId)
        assert.ok(seq > 0, 'the SCIM create wrote no audit entry, so there is nothing to not forward')

        await until(
          'the forwarder to have read and decided about the entry',
          async () => (await cursor()) >= seq,
        )
        assert.deepEqual(
          sink.entries(), [],
          'a licence that does not name audit_stream forwarded the audit log anyway',
        )
      } finally {
        await running.stop()
        await sink.close()
      }
    })

    it('a sink named with its variables missing refuses to start rather than forwarding nothing', async () => {
      const org = await seedOrg('enterprise')
      const key = signingKey()
      let started = false
      try {
        const running = await startEntryPoint(ENTERPRISE_ENTRY, {
          AF_LICENSE_PUBLIC_KEYS: key.publicKeys,
          AF_LICENSE_KEY: licenseFor(key, org.slug, ['audit_stream']),
          AF_ORG: org.slug,
          AF_AUDIT_STREAM_SINK: 'splunk',
          AF_AUDIT_STREAM_KEY: randomBytes(32).toString('base64'),
        })
        started = true
        await running.stop()
      } catch (err) {
        // The refusal names the variable, which is the whole difference between
        // this and a process that starts and forwards nowhere.
        assert.match(
          String(err),
          /AF_AUDIT_STREAM_SPLUNK_URL/,
          `the process failed for a reason that was not the missing variable: ${String(err)}`,
        )
      }
      assert.equal(
        started, false,
        'a control plane told to forward to Splunk with no Splunk URL started anyway, which is ' +
          'a compliance control reporting itself as held while holding nothing',
      )
    })

    it('with no sink configured the process says the audit log is not forwarded', async () => {
      const org = await seedOrg('enterprise')
      const key = signingKey()
      const running = await startEntryPoint(ENTERPRISE_ENTRY, {
        AF_LICENSE_PUBLIC_KEYS: key.publicKeys,
        AF_LICENSE_KEY: licenseFor(key, org.slug, ['audit_stream']),
        AF_ORG: org.slug,
      })
      try {
        // Said out loud in the state that forwards nothing, for the reason
        // readLicense says its own line out loud. An installation that forwards
        // and one that does not produced identical logs.
        assert.match(
          running.output(),
          /no AF_AUDIT_STREAM_SINK is set and no organization can choose a destination, so the control plane's audit log is written and not forwarded/,
          `the silent state was silent. It said:\n${running.output()}`,
        )
      } finally {
        await running.stop()
      }
    })
  },
)
