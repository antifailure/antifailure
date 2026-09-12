// One organization's audit log reaching the collector it chose, and no other.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// WHAT THIS FILE HAS TO PROVE HARDEST is isolation, and it is proved by
// construction rather than by absence. A test that looks for "nothing of
// organization B arrived at A's collector" is satisfied by a collector that
// received nothing at all, so every isolation assertion here stands beside a
// positive one: A's collector DID receive A's entries, under A's credential,
// and B's did receive B's.
//
// THE ORDERINGS, one case per cell, named in each title:
//
//   configured then written   the ordinary case
//   written then configured   history before the save is not replayed
//   two at once               two organizations with two destinations in one pass
//   reconfigured between      a new URL and credential between two passes
//   reconfigured mid pass     a save that commits while a pass is delivering
//   revoked                   the collector starts answering 401
//   retried                   a 503, then recovery
//   entitlement withdrawn     while a destination is configured
//   switched off and on       and the installation sink is not a fallback
//   installation fallback     an organization with no destination
//   late commit               pull request 372's case, per destination
//   copied ciphertext         a sealed credential moved between organizations
//   rotated sealing key       the key the rows were sealed under is gone
//   inward destination        a row naming this control plane's own network
//
// The collector here is a recording fetch rather than a socket, because the
// sinks take their transport as a parameter for exactly this and a fetch lets a
// case make a collector answer 401 or 503 per host. The claim that entries
// cross a real socket is made by ee/web/server/test/audit-destinations.test.ts,
// through the real registration path.
//
// Credentials are random per run and never printed, including in a failure
// message: every assertion about a credential compares, it does not quote.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes, randomUUID } from 'node:crypto'
import { appendAudit, sql } from '@antifailure/db'
import { fingerprintOf } from '@antifailure/api'
import {
  DestinationRefused,
  Forwarder,
  checkCustomerDestination,
  manifestKeyFor,
  readDelivery,
  readDestination,
  saveDestination,
  setDestinationEnabled,
  sinkFor,
  verify,
  verifyWebhook,
  type Batch,
  type Entry,
  type Fetcher,
  type Kind,
} from '../src/index.ts'
import {
  RecordingSink,
  TestClock,
  available,
  dropTenant,
  seedTenant,
  start,
  type Harness,
  type Tenant,
} from './harness.ts'

const hasDatabase = await available()

/** The sealing key this run's credentials are sealed under. */
const SEALING = randomBytes(32)

/** One request as the collector saw it. */
interface Seen {
  host: string
  headers: Record<string, string>
  body: string
}

/**
 * A collector per host, behind one fetch, because in production one fetch
 * carries every organization's deliveries and the isolation claim is about which
 * host a batch was addressed to.
 */
class Collector {
  readonly seen: Seen[] = []
  /** The status a request gets. 200 unless a case says otherwise. */
  respond: (request: Seen) => number = () => 200
  /** Runs once, on the next request, before it is answered. */
  onNext: ((request: Seen) => Promise<void>) | null = null

  readonly fetch: Fetcher = async (url, init) => {
    const headers: Record<string, string> = {}
    new Headers(init.headers as ConstructorParameters<typeof Headers>[0]).forEach((v, k) => { headers[k] = v })
    const request: Seen = { host: new URL(url).host, headers, body: String(init.body ?? '') }
    this.seen.push(request)
    const hook = this.onNext
    if (hook) {
      this.onNext = null
      await hook(request)
    }
    return new Response(null, { status: this.respond(request) })
  }

  at(host: string): Seen[] {
    return this.seen.filter((s) => s.host === host)
  }

  /** Every entry delivered to one host, whatever the protocol. */
  entriesAt(host: string, kind: Kind): Entry[] {
    return this.at(host).flatMap((s) => entriesOf(kind, s.body))
  }

  /** Every batch delivered to one host with its manifest, for verification. */
  batchesAt(host: string, kind: Kind): Batch[] {
    return this.at(host).map((s) => {
      if (kind === 'webhook') return JSON.parse(s.body) as Batch
      const entries = entriesOf(kind, s.body)
      const manifest = kind === 'splunk'
        ? JSON.parse((JSON.parse(s.body.split('\n')[0]!) as { fields: { antifailure_manifest: string } }).fields.antifailure_manifest)
        : JSON.parse((JSON.parse(s.body) as { UserProperties: { antifailure_manifest: string } }[])[0]!.UserProperties.antifailure_manifest)
      return { entries, manifest } as Batch
    })
  }
}

function entriesOf(kind: Kind, body: string): Entry[] {
  if (kind === 'webhook') return (JSON.parse(body) as Batch).entries
  if (kind === 'splunk') {
    return body.split('\n').filter((l) => l !== '').map((l) => (JSON.parse(l) as { event: Entry }).event)
  }
  return (JSON.parse(body) as { Body: string }[]).map((r) => JSON.parse(r.Body) as Entry)
}

function host(label: string): string {
  return `${label}-${randomUUID().slice(0, 8)}.collector.example`
}

function urlFor(kind: Kind, at: string): string {
  if (kind === 'splunk') return `https://${at}/services/collector`
  if (kind === 'event_hubs') return `https://${at}/hub/messages`
  return `https://${at}/ingest`
}

function credential(): string {
  return `cred_${randomBytes(24).toString('base64url')}`
}

const seeded: string[] = []

describe(
  'an organization audit log reaches the destination it chose and no other',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: Harness

    before(async () => {
      h = await start()
    })

    after(async () => {
      for (const orgId of seeded) await dropTenant(h, orgId)
      await h.close()
    })

    async function tenant(plan = 'enterprise'): Promise<Tenant> {
      const t = await seedTenant(h, plan)
      seeded.push(t.orgId)
      await h.setCursor(await h.maxSeq())
      return t
    }

    async function entitled(orgId: string, now: Date): Promise<boolean> {
      const { licensed } = await import('@antifailure-ee/features')
      return licensed(h.pool, orgId, 'audit_stream', now)
    }

    async function configure(
      org: Tenant,
      kind: Kind,
      at: string,
      secret: string,
      extra: { enabled?: boolean } = {},
    ) {
      return saveDestination(h.pool, SEALING, {
        orgId: org.orgId, kind, url: urlFor(kind, at), credential: secret,
        actorUserId: null, actorLabel: 'the suite', origin: 'web', ...extra,
      }, new Date())
    }

    function forwarder(
      collector: Collector,
      options: {
        permitted?: (orgId: string, now: Date) => Promise<boolean>
        sink?: RecordingSink
        sealing?: Buffer
        deliveryBatchSize?: number
        onResolve?: (orgId: string) => void
      } = {},
    ): Forwarder {
      const sealing = options.sealing ?? SEALING
      return new Forwarder({
        pool: h.pool,
        clock: new TestClock(),
        permitted: options.permitted ?? entitled,
        destinations: (db, orgId) => {
          options.onResolve?.(orgId)
          return sinkFor(db, sealing, collector.fetch, orgId)
        },
        ...(options.sink ? { sink: options.sink, key: 'an-installation-manifest-key' } : {}),
        ...(options.deliveryBatchSize ? { deliveryBatchSize: options.deliveryBatchSize } : {}),
      })
    }

    async function actionsOf(orgId: string): Promise<{ seq: number; action: string }[]> {
      const rows = await h.admin<{ seq: string; action: string }[]>`
        SELECT seq, action FROM audit_entries WHERE org_id = ${orgId} ORDER BY seq`
      return rows.map((r) => ({ seq: Number(r.seq), action: r.action }))
    }

    // -----------------------------------------------------------------------
    // Orderings
    // -----------------------------------------------------------------------

    it('configured then written: an entry written after the save reaches the destination, signed under its own credential', async () => {
      const org = await tenant()
      const at = host('a')
      const secret = credential()
      await configure(org, 'webhook', at, secret)
      const seq = await h.write(org.orgId, 'configured.then.written')

      const collector = new Collector()
      await forwarder(collector).pass()

      const got = collector.entriesAt(at, 'webhook')
      assert.deepEqual(
        got.map((e) => e.action), ['audit_stream.destination.saved', 'configured.then.written'],
        'the destination did not receive the record of its own creation followed by the entry',
      )
      assert.equal(got[1]!.seq, seq)
      assert.ok(got.every((e) => e.orgId === org.orgId))

      for (const request of collector.at(at)) {
        assert.equal(
          verifyWebhook(secret, request.headers['x-antifailure-timestamp']!, request.body,
            request.headers['x-antifailure-signature']!),
          true, 'a delivery does not verify under the secret the organization gave',
        )
      }
      for (const batch of collector.batchesAt(at, 'webhook')) {
        const checked = verify(batch, manifestKeyFor(secret))
        assert.equal(checked.ok, true, `a manifest does not verify under the organization's derived key: ${checked.problem}`)
        assert.equal(batch.manifest.org, org.orgId)
      }
    })

    it('written then configured: history before the save is not replayed, and the save is the first thing delivered', async () => {
      const org = await tenant()
      const before = [await h.write(org.orgId, 'history.one'), await h.write(org.orgId, 'history.two')]
      const at = host('late')
      const saved = await configure(org, 'webhook', at, credential())
      assert.equal(saved.fromSeq, before[1], 'from_seq is not the head the organization had reached at the save')
      const after = await h.write(org.orgId, 'after.the.save')

      const collector = new Collector()
      await forwarder(collector).pass()

      const got = collector.entriesAt(at, 'webhook')
      assert.deepEqual(
        got.map((e) => e.action), ['audit_stream.destination.saved', 'after.the.save'],
        'entries written before the destination existed were streamed into it, or the save was not first',
      )
      assert.equal(got[1]!.seq, after)
      for (const seq of before) {
        assert.ok(!got.some((e) => e.seq === seq), `history entry ${String(seq)} was replayed`)
      }
    })

    it('two at once: each destination receives its own organization entries and credential, and nothing of the other', async () => {
      const a = await tenant()
      const b = await tenant()
      const atA = host('splunk-a')
      const atB = host('hook-b')
      const tokenA = credential()
      const secretB = credential()
      await configure(a, 'splunk', atA, tokenA)
      await configure(b, 'webhook', atB, secretB)
      const writtenA = [await h.write(a.orgId, 'two.a'), await h.write(a.orgId, 'two.a')]
      const writtenB = [await h.write(b.orgId, 'two.b'), await h.write(b.orgId, 'two.b')]
      // Interleaved in sequence, so both organizations are in one read.
      writtenA.push(await h.write(a.orgId, 'two.a'))
      writtenB.push(await h.write(b.orgId, 'two.b'))

      const collector = new Collector()
      const pass = await forwarder(collector).pass()
      assert.deepEqual(pass.organizations, [a.orgId, b.orgId].sort())

      const toA = collector.entriesAt(atA, 'splunk')
      const toB = collector.entriesAt(atB, 'webhook')
      // The positive halves first, so the negatives below cannot pass on silence.
      for (const seq of writtenA) assert.ok(toA.some((e) => e.seq === seq), `A's entry ${String(seq)} did not reach A`)
      for (const seq of writtenB) assert.ok(toB.some((e) => e.seq === seq), `B's entry ${String(seq)} did not reach B`)

      assert.ok(toA.every((e) => e.orgId === a.orgId), "an entry that is not A's reached A's collector")
      assert.ok(toB.every((e) => e.orgId === b.orgId), "an entry that is not B's reached B's collector")
      assert.equal(
        collector.seen.filter((s) => s.host !== atA && s.host !== atB).length, 0,
        'a delivery went to a host neither organization named',
      )

      // The credential that went with each delivery, compared and never quoted.
      for (const request of collector.at(atA)) {
        assert.ok(request.headers.authorization === `Splunk ${tokenA}`, "A's delivery did not carry A's token")
        assert.ok(!request.body.includes(tokenA), "A's token appeared in a request body")
      }
      for (const request of collector.at(atB)) {
        assert.equal(request.headers.authorization, undefined, "B's webhook carried an authorization header")
        assert.equal(
          verifyWebhook(secretB, request.headers['x-antifailure-timestamp']!, request.body,
            request.headers['x-antifailure-signature']!),
          true, "B's delivery does not verify under B's secret",
        )
      }
      const everything = collector.seen.map((s) => JSON.stringify(s)).join('\n')
      const toBOnly = collector.at(atB).map((s) => JSON.stringify(s)).join('\n')
      const toAOnly = collector.at(atA).map((s) => JSON.stringify(s)).join('\n')
      assert.ok(!toBOnly.includes(tokenA), "A's token reached B's collector")
      assert.ok(!toAOnly.includes(secretB), "B's secret reached A's collector")
      assert.ok(!everything.includes(secretB), "B's webhook secret travelled in a request")

      for (const batch of collector.batchesAt(atA, 'splunk')) {
        assert.equal(batch.manifest.org, a.orgId)
        assert.equal(verify(batch, manifestKeyFor(tokenA)).ok, true, "A's manifest does not verify under A's key")
        assert.equal(verify(batch, manifestKeyFor(secretB)).ok, false, "A's manifest verifies under B's key")
      }
    })

    it('reconfigured between passes: entries before the change went to the old destination, after it to the new, none lost and none twice', async () => {
      const org = await tenant()
      const oldAt = host('old')
      const newAt = host('new')
      await configure(org, 'webhook', oldAt, credential())
      const first = await h.write(org.orgId, 'before.the.change')
      const collector = new Collector()
      const f = forwarder(collector)
      await f.pass()

      const resaved = await configure(org, 'webhook', newAt, credential())
      const second = await h.write(org.orgId, 'after.the.change')
      await f.pass()

      const toOld = collector.entriesAt(oldAt, 'webhook').map((e) => e.seq)
      const toNew = collector.entriesAt(newAt, 'webhook').map((e) => e.seq)
      assert.ok(toOld.includes(first) && !toOld.includes(second), 'the old destination got the wrong side of the change')
      assert.ok(toNew.includes(second) && !toNew.includes(first), 'the new destination got the wrong side of the change')
      const all = [...toOld, ...toNew]
      assert.equal(new Set(all).size, all.length, 'an entry was delivered twice across the change')
      const expected = (await actionsOf(org.orgId)).map((r) => r.seq)
      assert.deepEqual([...all].sort((x, y) => x - y), expected, 'an entry was lost across the change')
      assert.equal(resaved.fromSeq, 0, 'amending a destination moved from_seq and could discard pending entries')
    })

    it('reconfigured mid pass: a pass that resolved the old destination finishes there, and the next pass uses the new one', async () => {
      const org = await tenant()
      const oldAt = host('mid-old')
      const newAt = host('mid-new')
      await configure(org, 'webhook', oldAt, credential())
      await h.write(org.orgId, 'mid.one')
      await h.write(org.orgId, 'mid.two')

      const collector = new Collector()
      // The save commits while the first delivery of the pass is in flight.
      collector.onNext = async () => { await configure(org, 'webhook', newAt, credential()) }
      const f = forwarder(collector, { deliveryBatchSize: 1 })
      await f.pass()
      await f.pass()

      const toOld = collector.entriesAt(oldAt, 'webhook').map((e) => e.action)
      const toNew = collector.entriesAt(newAt, 'webhook').map((e) => e.action)
      assert.deepEqual(toOld, ['audit_stream.destination.saved', 'mid.one', 'mid.two'])
      assert.deepEqual(toNew, ['audit_stream.destination.saved'])
      const all = [...collector.entriesAt(oldAt, 'webhook'), ...collector.entriesAt(newAt, 'webhook')].map((e) => e.seq)
      assert.deepEqual([...all].sort((x, y) => x - y), (await actionsOf(org.orgId)).map((r) => r.seq))
    })

    it("revoked: a collector answering 401 is recorded where the organization reads it, and a new credential resumes delivery", async () => {
      const org = await tenant()
      const at = host('revoked')
      const first = credential()
      await configure(org, 'splunk', at, first)
      const collector = new Collector()
      const f = forwarder(collector)
      await f.pass()
      const healthy = await readDelivery(h.pool, org.orgId)
      assert.ok(healthy?.lastDeliveredAt, 'the first delivery was not recorded')
      assert.equal(healthy.lastError, null)

      // The customer's own system revokes the token; this control plane is not told.
      collector.respond = (r) => (r.headers.authorization === `Splunk ${first}` ? 401 : 200)
      const lost = await h.write(org.orgId, 'while.revoked')
      await f.pass()
      const failing = await readDelivery(h.pool, org.orgId)
      assert.match(failing?.lastError ?? '', /answered 401/, 'the refusal is not visible to the organization')
      assert.equal(failing!.consecutiveFailures, 1)
      assert.ok(!(failing!.lastError ?? '').includes(first), 'the recorded failure carries the credential')
      assert.ok(failing!.deliveredSeq >= lost, 'a permanent refusal stalled the stream instead of being given up on')

      const second = credential()
      await configure(org, 'splunk', at, second)
      const resumed = await h.write(org.orgId, 'after.the.new.credential')
      await f.pass()
      const fixed = await readDelivery(h.pool, org.orgId)
      assert.equal(fixed?.lastError, null)
      assert.equal(fixed!.consecutiveFailures, 0)
      const accepted = collector.at(at).filter((r) => r.headers.authorization === `Splunk ${second}`)
      assert.ok(
        accepted.flatMap((r) => entriesOf('splunk', r.body)).some((e) => e.seq === resumed),
        'the entry after the new credential was not delivered under it',
      )
    })

    it('retried: a 503 holds the entries below the position, and the next pass delivers them exactly once', async () => {
      const org = await tenant()
      const at = host('retry')
      await configure(org, 'webhook', at, credential())
      const collector = new Collector()
      collector.respond = () => 503
      const f = forwarder(collector)
      const failed = await f.pass()
      assert.equal(failed.delivered, 0)
      const held = await readDelivery(h.pool, org.orgId)
      assert.match(held?.lastError ?? '', /answered 503/)
      assert.equal(held!.deliveredSeq, 0, 'a transient failure advanced the position and lost the entry')

      collector.respond = () => 200
      await f.pass()
      await f.pass()
      const accepted = collector.seen.filter((s) => s.host === at).slice(1)
      const seqs = accepted.flatMap((s) => entriesOf('webhook', s.body)).map((e) => e.seq)
      assert.deepEqual(seqs, (await actionsOf(org.orgId)).map((r) => r.seq), 'the held entries were not delivered exactly once')
      assert.equal((await readDelivery(h.pool, org.orgId))!.consecutiveFailures, 0)
    })

    it('entitlement withdrawn: nothing is delivered and the credential is never opened', async () => {
      const org = await tenant()
      const at = host('withdrawn')
      await configure(org, 'webhook', at, credential())
      await h.setPlan(org.orgId, 'free')
      await h.write(org.orgId, 'while.unentitled')

      const collector = new Collector()
      const resolved: string[] = []
      const pass = await forwarder(collector, { onResolve: (o) => resolved.push(o) }).pass()
      assert.ok(pass.unlicensed >= 2, 'the pass did not read the entries it declined')
      assert.equal(collector.at(at).length, 0, 'an unentitled organization was delivered to its destination')
      assert.ok(!resolved.includes(org.orgId), 'the credential of an unentitled organization was opened')

      await h.setPlan(org.orgId, 'enterprise')
      const regranted = await h.write(org.orgId, 'after.the.regrant')
      await forwarder(collector).pass()
      assert.deepEqual(collector.entriesAt(at, 'webhook').map((e) => e.seq), [regranted])
    })

    it('switched off and on: entries are declined rather than sent to the installation sink, and on starts from the switch', async () => {
      const org = await tenant()
      const at = host('switch')
      await configure(org, 'webhook', at, credential())
      await setDestinationEnabled(h.pool, { orgId: org.orgId, enabled: false, actorUserId: null, actorLabel: 'the suite', origin: 'web' }, new Date())
      const declined = await h.write(org.orgId, 'while.off')

      const collector = new Collector()
      const installation = new RecordingSink()
      const pass = await forwarder(collector, { sink: installation }).pass()
      assert.ok(pass.disabled >= 1)
      assert.equal(collector.at(at).length, 0, 'a switched off destination was delivered to')
      assert.ok(
        !installation.entries().some((e) => e.orgId === org.orgId),
        'switching a destination off rerouted the organization entries to the installation sink',
      )

      await setDestinationEnabled(h.pool, { orgId: org.orgId, enabled: true, actorUserId: null, actorLabel: 'the suite', origin: 'web' }, new Date())
      const resumed = await h.write(org.orgId, 'after.on')
      await forwarder(collector, { sink: installation }).pass()
      const got = collector.entriesAt(at, 'webhook')
      assert.deepEqual(got.map((e) => e.action), ['audit_stream.destination.enabled', 'after.on'])
      assert.equal(got[1]!.seq, resumed)
      assert.ok(!got.some((e) => e.seq === declined))
    })

    it('installation fallback: an organization with no destination goes to the installation sink, and one with a destination does not', async () => {
      const own = await tenant()
      const none = await tenant()
      const at = host('own')
      await configure(own, 'webhook', at, credential())
      const ownSeq = await h.write(own.orgId, 'has.its.own')
      const noneSeq = await h.write(none.orgId, 'has.none')

      const collector = new Collector()
      const installation = new RecordingSink()
      await forwarder(collector, { sink: installation }).pass()

      assert.ok(installation.seqs().includes(noneSeq), 'the installation sink did not receive the organization with no destination')
      assert.ok(!installation.entries().some((e) => e.orgId === own.orgId), 'an organization with its own destination also reached the installation sink')
      assert.ok(collector.entriesAt(at, 'webhook').some((e) => e.seq === ownSeq))
      assert.ok(!collector.entriesAt(at, 'webhook').some((e) => e.orgId === none.orgId))
    })

    it('late commit: a lower sequence that commits after another organization still reaches its own destination', async () => {
      const slow = await tenant()
      const fast = await tenant()
      const atSlow = host('slow')
      const atFast = host('fast')
      await configure(slow, 'webhook', atSlow, credential())
      await configure(fast, 'webhook', atFast, credential())

      let announce!: (seq: number) => void
      const allocated = new Promise<number>((resolve) => { announce = resolve })
      let release!: () => void
      const allowedToCommit = new Promise<void>((resolve) => { release = resolve })
      const pending = h.pool.withTenant({ orgId: slow.orgId }, async (db) => {
        const entry = await appendAudit(db, {
          orgId: slow.orgId, actorLabel: 'harness', action: 'late.commit',
          targetType: 'organization', origin: 'system',
        })
        announce(entry.seq)
        await allowedToCommit
      })
      try {
        const lower = await allocated
        const higher = await h.write(fast.orgId, 'early.commit')
        assert.ok(higher > lower)
        const collector = new Collector()
        const f = forwarder(collector)
        await f.pass()
        assert.ok(collector.entriesAt(atFast, 'webhook').some((e) => e.seq === higher))
        release()
        await pending
        await f.pass()
        await f.pass()
        const toSlow = collector.entriesAt(atSlow, 'webhook').filter((e) => e.action === 'late.commit')
        assert.deepEqual(toSlow.map((e) => e.seq), [lower], 'the late commit was skipped or replayed')
        assert.ok(!collector.entriesAt(atFast, 'webhook').some((e) => e.orgId === slow.orgId))
        assert.ok(!collector.entriesAt(atSlow, 'webhook').some((e) => e.orgId === fast.orgId))
      } finally {
        release()
        await pending
      }
    })

    // -----------------------------------------------------------------------
    // The credential
    // -----------------------------------------------------------------------

    it("copied ciphertext: a credential sealed for one organization does not open in another's row, and nothing is delivered with it", async () => {
      const a = await tenant()
      const b = await tenant()
      const atA = host('copy-a')
      const atB = host('copy-b')
      const secretA = credential()
      await configure(a, 'webhook', atA, secretA)
      await configure(b, 'webhook', atB, credential())
      // Somebody who can write rows moves A's sealed credential into B's.
      await h.admin`
        UPDATE audit_stream_destinations AS target
        SET ciphertext = source.ciphertext, nonce = source.nonce
        FROM audit_stream_destinations AS source
        WHERE source.org_id = ${a.orgId} AND target.org_id = ${b.orgId}`
      const heldSeq = await h.write(b.orgId, 'with.a.copied.credential')

      const collector = new Collector()
      const pass = await forwarder(collector).pass()
      assert.ok(pass.refused >= 1, 'the copied credential was not refused')
      assert.equal(collector.at(atB).length, 0, "B's collector was delivered to using A's credential")
      const state = await readDelivery(h.pool, b.orgId)
      assert.match(state?.lastError ?? '', /cannot be opened/)
      assert.ok(collector.at(atA).length > 0, "A's own destination stopped working, so the refusal above proves nothing")

      // Held, not lost: repairing B's credential delivers what waited.
      await configure(b, 'webhook', atB, credential())
      await forwarder(collector).pass()
      assert.ok(
        collector.entriesAt(atB, 'webhook').some((e) => e.seq === heldSeq),
        'the entry held behind the refused credential was lost rather than delivered after repair',
      )
      assert.ok(!JSON.stringify(collector.at(atB)).includes(secretA), "A's credential reached B's collector")
    })

    it('copied between kinds: a credential sealed for one protocol does not open as another', async () => {
      const org = await tenant()
      const at = host('kind')
      await configure(org, 'webhook', at, credential())
      await h.admin`UPDATE audit_stream_destinations SET kind = 'event_hubs' WHERE org_id = ${org.orgId}`
      await h.write(org.orgId, 'with.the.kind.changed')
      const collector = new Collector()
      const pass = await forwarder(collector).pass()
      assert.ok(pass.refused >= 1)
      assert.equal(collector.at(at).length, 0, 'a webhook secret was replayed as an Event Hubs signature')
    })

    it('rotated sealing key: rows sealed under the old key hold the stream and lose nothing', async () => {
      const org = await tenant()
      const at = host('rotated')
      await configure(org, 'webhook', at, credential())
      const held = await h.write(org.orgId, 'across.a.rotation')

      const collector = new Collector()
      const rotated = await forwarder(collector, { sealing: randomBytes(32) }).pass()
      assert.ok(rotated.refused >= 1)
      assert.equal(collector.at(at).length, 0)
      assert.equal((await readDelivery(h.pool, org.orgId))!.deliveredSeq, 0, 'a credential that would not open advanced the position')

      await forwarder(collector).pass()
      assert.ok(collector.entriesAt(at, 'webhook').some((e) => e.seq === held), 'the held entry was lost')
    })

    it('stored sealed: the row holds no plaintext, and the screen read carries no ciphertext', async () => {
      const org = await tenant()
      const secret = credential()
      await configure(org, 'webhook', host('sealed'), secret)
      const [row] = await h.admin<{ ciphertext: Buffer; last4: string; fingerprint: string }[]>`
        SELECT ciphertext, last4, fingerprint FROM audit_stream_destinations WHERE org_id = ${org.orgId}`
      assert.ok(!Buffer.from(row!.ciphertext).includes(Buffer.from(secret, 'utf8')), 'the credential is stored in the clear')
      assert.equal(row!.last4, secret.slice(-4))
      assert.equal(row!.fingerprint, fingerprintOf(secret))

      const screen = await readDestination(h.pool, org.orgId)
      assert.ok(screen)
      const keys = Object.keys(screen)
      assert.ok(!keys.includes('ciphertext') && !keys.includes('nonce'), 'the screen read carries the sealed value')
      assert.ok(!JSON.stringify(screen).includes(secret))

      const entries = await h.admin<{ detail: unknown }[]>`
        SELECT detail FROM audit_entries WHERE org_id = ${org.orgId} AND action = 'audit_stream.destination.saved'`
      assert.equal(entries.length, 1)
      assert.ok(!JSON.stringify(entries[0]!.detail).includes(secret), 'the audit entry for the save carries the credential')
    })

    it('refused at save: a destination the customer rule refuses writes no row and no audit entry, and the refusal does not quote the credential', async () => {
      const org = await tenant()
      const secret = credential()
      for (const url of ['https://127.0.0.1:8443/ingest', 'http://collector.example/ingest', 'https://10.0.0.5/ingest']) {
        await assert.rejects(
          saveDestination(h.pool, SEALING, {
            orgId: org.orgId, kind: 'webhook', url, credential: secret,
            actorUserId: null, actorLabel: 'the suite', origin: 'web',
          }, new Date()),
          (err: unknown) => err instanceof DestinationRefused && !err.message.includes(secret),
        )
      }
      await assert.rejects(
        configure(org, 'webhook', host('spaced'), 'two words in it'),
        DestinationRefused,
      )
      assert.equal((await h.admin`SELECT 1 FROM audit_stream_destinations WHERE org_id = ${org.orgId}`).length, 0)
      assert.equal((await actionsOf(org.orgId)).length, 0, 'a refused save wrote an audit entry')
    })

    // -----------------------------------------------------------------------
    // The destination as untrusted input
    // -----------------------------------------------------------------------

    it("inward destination: a row naming this control plane's own network is refused at delivery even when it bypassed the validator", async () => {
      const org = await tenant()
      await configure(org, 'webhook', host('inward'), credential())
      const collector = new Collector()
      for (const inward of ['https://127.0.0.1:8443/ingest', 'https://169.254.169.254/metadata', 'https://10.0.0.5/ingest', 'https://[::1]/ingest']) {
        // Written by the migration role, which no validator stands in front of.
        await h.admin`UPDATE audit_stream_destinations SET url = ${inward} WHERE org_id = ${org.orgId}`
        await h.write(org.orgId, 'toward.the.inside')
        const pass = await forwarder(collector).pass()
        assert.ok(pass.refused >= 1, `${inward} was not refused at delivery`)
      }
      assert.equal(collector.seen.length, 0, 'a delivery was attempted toward the control plane network')
      assert.match((await readDelivery(h.pool, org.orgId))?.lastError ?? '', /network/)
    })

    it('the database refuses a plaintext destination and one with credentials in its authority', async () => {
      const org = await tenant()
      for (const url of ['http://collector.example/ingest', 'https://user:pw@collector.example/ingest']) {
        const refused = await h.admin`
          INSERT INTO audit_stream_destinations (org_id, kind, url, ciphertext, nonce, fingerprint, last4)
          VALUES (${org.orgId}, 'webhook', ${url}, ${randomBytes(40)}, ${randomBytes(12)}, 'f', 'abcd')`
          .then(() => null, (err: { code?: string }) => err.code)
        assert.equal(refused, '23514', `the database accepted ${url.replace(/\/\/.*@/, '//<userinfo>@')}`)
      }
      // And an @ outside the authority is an ordinary URL, not userinfo.
      await h.admin`
        INSERT INTO audit_stream_destinations (org_id, kind, url, ciphertext, nonce, fingerprint, last4)
        VALUES (${org.orgId}, 'webhook', 'https://collector.example/in@gest', ${randomBytes(40)}, ${randomBytes(12)}, 'f', 'abcd')`
    })
  },
)

describe('the customer destination rule', () => {
  const refused = [
    'http://collector.example/ingest',
    'https://user:secret@collector.example/ingest',
    'https://127.0.0.1/ingest',
    'https://127.8.9.10/ingest',
    'https://[::1]/ingest',
    'https://0.0.0.0/ingest',
    'https://10.1.2.3/ingest',
    'https://172.16.0.1/ingest',
    'https://172.31.255.255/ingest',
    'https://192.168.1.1/ingest',
    'https://169.254.169.254/metadata',
    'https://100.64.0.1/ingest',
    'https://224.0.0.1/ingest',
    'https://[fd00::1]/ingest',
    'https://[fe80::1]/ingest',
    'https://[ff02::1]/ingest',
    'https://[::ffff:10.0.0.1]/ingest',
    'https://[::ffff:a00:1]/ingest',
    'https://[::ffff:127.0.0.1]/ingest',
    'https://[64:ff9b::10.0.0.1]/ingest',
    'https://[64:ff9b::a9fe:a9fe]/ingest',
    'https://[::10.0.0.1]/ingest',
    // The parser normalises every IPv4 spelling to dotted decimal before this
    // rule sees it, and these prove the rule relies on that rather than on the
    // spelling somebody typed.
    'https://2130706433/ingest',
    'https://0x7f.0.0.1/ingest',
    'https://127.1/ingest',
    'https://0177.0.0.1/ingest',
    'https://localhost/ingest',
    'https://collector.local/ingest',
    'https://collector/ingest',
    'not a url',
  ]
  const accepted = [
    'https://http-inputs-acme.splunkcloud.com/services/collector',
    'https://acme.servicebus.windows.net/audit/messages',
    'https://8.8.8.8/ingest',
    'https://172.32.0.1/ingest',
    'https://[2606:4700:4700::1111]/ingest',
    'https://[::ffff:8.8.8.8]/ingest',
  ]
  for (const url of refused) {
    it(`refuses ${url}`, () => {
      assert.notEqual(checkCustomerDestination(url), null)
    })
  }
  for (const url of accepted) {
    it(`accepts ${url}`, () => {
      assert.equal(checkCustomerDestination(url), null)
    })
  }
})
