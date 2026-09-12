// The orderings an audit forwarder actually meets, one test per cell.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// WHY A TABLE OF ORDERINGS RATHER THAN A LIST OF STATES. This is event driven:
// an entry is written by one path and delivered by another, and the two are not
// synchronised with each other. A suite that only ever writes an entry and then
// runs a pass tests one arrival order out of six, which is exactly the failure
// this repository has already paid for elsewhere. So every case below names the
// ordering it covers in its own title, and the cells are:
//
//   before      an entry exists before the forwarder ever runs
//   during      an entry is written between two passes of a running forwarder
//   gap         a sequence number is missing, because a transaction rolled back
//               after taking one
//   empty       the forwarder starts against a log with nothing above the cursor
//   restart     a second forwarder over the same cursor delivers nothing twice
//   partial     a sink refuses part way through a pass
//   permanent   a sink will never accept a batch, so it is given up on
//   licensed    an entitled organization is forwarded
//   unlicensed  an organization that is not entitled is not forwarded
//   lapses      an entitlement withdrawn while the process runs stops it
//   arrives     an entitlement granted while the process runs starts it
//   two orgs    two organizations in one read get one batch each
//   origins     every origin any entry point writes is carried
//
// Nothing here uses a fake database, a fake pool or a hand written INSERT. The
// entries are written by the real `appendAudit` inside a real tenant
// transaction against real row level security, because the defect being closed
// is that the rows the product already writes reached nothing.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile, readdir } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { Forwarder, PermanentError, verify } from '../src/index.ts'
import { appendAudit, sql } from '@antifailure/db'
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

const KEY = 'a-manifest-signing-key-for-this-run'

/** Every organization the file seeds, so teardown removes them whatever
 *  happened to the case that made one. */
const seeded: string[] = []

describe(
  'the control plane audit log reaches a sink',
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

    /** A tenant, and the cursor parked at the current head so the case reads
     *  only what it writes. Every case starts this way; see harness.maxSeq. */
    async function tenant(plan = 'enterprise'): Promise<Tenant> {
      const t = await seedTenant(h, plan)
      seeded.push(t.orgId)
      await h.setCursor(await h.maxSeq())
      return t
    }

    /** A forwarder over a recording sink, entitled by the plan in the database
     *  rather than by a callback that says yes. `permitted` here is the real
     *  question the enterprise entry point asks, minus the licence key half,
     *  which entrypoint-level tests cover because it needs a real process. */
    function forwarderFor(
      sink: RecordingSink,
      clock: TestClock,
      permitted: (orgId: string, now: Date) => Promise<boolean>,
      deliveryBatchSize = 500,
    ): Forwarder {
      // The pass size is left at its default and only the DELIVERY size moves.
      // Those were one number until this file was first run: a case that set
      // two entries per delivery also read two entries per pass, so a batch
      // that was supposed to be the second of three was never read at all and
      // the assertion about the third could not have failed for the right
      // reason.
      return new Forwarder({
        pool: h.pool, clock, sink, key: KEY, permitted, deliveryBatchSize,
      })
    }

    /** The entitlement as the product asks it, through the catalogue and the
     *  organization's plan. Imported lazily so this file does not become a
     *  dependency of the features package's own graph. */
    async function entitled(orgId: string, now: Date): Promise<boolean> {
      const { licensed } = await import('@antifailure-ee/features')
      return licensed(h.pool, orgId, 'audit_stream', now)
    }

    // -----------------------------------------------------------------------

    it('before: an entry written before the forwarder ever runs is delivered', async () => {
      const org = await tenant()
      const seq = await h.write(org.orgId, 'sso.login')

      const sink = new RecordingSink()
      const f = forwarderFor(sink, new TestClock(), entitled)
      const pass = await f.pass()

      assert.equal(pass.read, 1, 'the pass read something other than the one entry written')
      assert.equal(pass.delivered, 1, 'the entry was read and not delivered')
      assert.deepEqual(sink.seqs(), [seq], 'the sink was given the wrong entry')
      assert.equal(await h.readCursor(), seq, 'the cursor did not advance past a delivered entry')

      // The entry a security team would actually read, checked field by field
      // rather than counted. A forwarder that delivered an empty object would
      // pass every count in this file.
      const got = sink.entries()[0]!
      assert.equal(got.orgId, org.orgId)
      assert.equal(got.action, 'sso.login')
      assert.equal(got.actor, 'harness')
      assert.equal(got.origin, 'system')
      assert.match(got.entryHash, /^[0-9a-f]{64}$/, 'the chain hash did not travel with the entry')
      assert.equal(got.entryHash, await chainHash(h, seq), 'the hash forwarded is not the stored one')
    })

    it('before: the batch manifest verifies over the chain head', async () => {
      const org = await tenant()
      await h.write(org.orgId, 'sso.login')
      const last = await h.write(org.orgId, 'scim.user.created')

      const sink = new RecordingSink()
      await forwarderFor(sink, new TestClock(), entitled).pass()

      const batch = sink.batches[0]!
      assert.equal(verify(batch, KEY).ok, true, verify(batch, KEY).problem)
      assert.equal(batch.manifest.org, org.orgId, 'the manifest names the wrong organization')
      assert.equal(batch.manifest.lastSeq, last)
      assert.equal(
        batch.manifest.headHash,
        await chainHash(h, last),
        'the manifest head is not the last entry chain hash, so a chain of batches cannot be ' +
          'joined end to end',
      )
      assert.equal(
        verify(batch, 'a different key').ok,
        false,
        'the manifest verifies under a key that did not sign it, so the signature proves nothing',
      )
    })

    it('during: an entry written between two passes is delivered without a restart', async () => {
      const org = await tenant()
      const sink = new RecordingSink()
      const f = forwarderFor(sink, new TestClock(), entitled)

      const first = await h.write(org.orgId, 'admin.impersonated')
      assert.equal((await f.pass()).delivered, 1)

      // The ordering that matters: the forwarder object is the same one, the
      // cursor is where the first pass left it, and the entry did not exist
      // when it started.
      const second = await h.write(org.orgId, 'member.removed')
      const pass = await f.pass()

      assert.equal(pass.read, 1, 'the second pass re-read the first entry')
      assert.equal(pass.delivered, 1)
      assert.deepEqual(sink.seqs(), [first, second], 'the sink saw the wrong entries or the wrong order')
      assert.equal(await h.readCursor(), second)
    })

    it('gap: a sequence number burned by a rolled back write does not stall the stream', async () => {
      const org = await tenant()
      const before = await h.write(org.orgId, 'first')

      // A real gap, made the way real gaps are made. appendAudit reads nextval
      // and then inserts; a transaction that rolls back after the read leaves
      // the number consumed and no row, and a sequence is not transactional so
      // the number never comes back. A forwarder that waited for the missing
      // number would stop here and look like a dead sink.
      const burned = await h.burnSequence()
      const after = await h.write(org.orgId, 'second')
      assert.ok(burned > before && burned < after, 'the gap was not made where this case needs it')

      const sink = new RecordingSink()
      const pass = await forwarderFor(sink, new TestClock(), entitled).pass()

      assert.deepEqual(sink.seqs(), [before, after], 'the forwarder did not read across the gap')
      assert.equal(pass.read, 2)
      assert.equal(
        await h.readCursor(),
        after,
        'the cursor stopped at the gap, so every entry after a rolled back write would be lost',
      )
    })

    it('concurrent: a lower sequence that commits after another organization remains deliverable', async () => {
      const slow = await tenant()
      const fast = await tenant()
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
        const sink = new RecordingSink()
        const f = forwarderFor(sink, new TestClock(), entitled)
        await f.pass()
        assert.deepEqual(sink.seqs(), [higher])
        release()
        await pending
        await f.pass()
        assert.deepEqual(sink.seqs(), [higher, lower], 'a committed audit entry was skipped forever')
        await f.pass()
        assert.deepEqual(sink.seqs(), [higher, lower], 'the late commit was replayed')
      } finally {
        release()
        await pending
      }
    })

    it('positions: a tenant reads its own delivery state and no other, and cannot advance it', async () => {
      // WIDENED ON PURPOSE BY 0044, and this test is what bounds the widening.
      // 0043 gave a tenant no read of this table at all. An organization that
      // chose its own collector has to be able to see whether it is working, so
      // it now reads ITS OWN row. It must still read nobody else's and move
      // nothing: advancing a position skips entries, and rewinding one replays
      // an organization's history into its collector.
      const org = await tenant()
      const other = await tenant()
      const seq = await h.write(org.orgId, 'position.boundary')
      await h.write(other.orgId, 'position.boundary')
      await forwarderFor(new RecordingSink(), new TestClock(), entitled).pass()

      const seen = await h.pool.withTenant({ orgId: org.orgId }, (db) =>
        db.execute<{ org_id: string; delivered_seq: string }>(sql`
          SELECT org_id, delivered_seq FROM audit_stream_positions`))
      assert.deepEqual(
        seen.map((r) => r.org_id), [org.orgId],
        'a tenant read delivery state that is not its own, so the SELECT policy 0044 added names ' +
          'no tenant and widened the table for every ordinary request',
      )
      assert.equal(Number(seen[0]!.delivered_seq), seq)

      const moved = await h.pool.withTenant({ orgId: org.orgId }, (db) =>
        db.execute(sql`
          UPDATE audit_stream_positions SET delivered_seq = 0 WHERE org_id = ${org.orgId}
          RETURNING org_id`))
      assert.equal(moved.length, 0, 'a tenant rewound its own delivery position')

      const visible = await h.pool.withAuditForwarder((db) =>
        db.execute<{ delivered_seq: string }>(sql`
          SELECT delivered_seq FROM audit_stream_positions WHERE org_id = ${org.orgId}`))
      assert.equal(Number(visible[0]!.delivered_seq), seq)
    })

    it('empty: a forwarder over a log with nothing above the cursor moves nothing', async () => {
      await tenant()
      const sink = new RecordingSink()
      const at = await h.readCursor()
      const pass = await forwarderFor(sink, new TestClock(), entitled).pass()

      assert.equal(pass.read, 0)
      assert.equal(pass.delivered, 0)
      assert.deepEqual(sink.batches, [], 'an empty pass sent a batch, which a receiver counts')
      assert.equal(await h.readCursor(), at, 'an empty pass moved the cursor')
      assert.equal(pass.from, pass.to, 'an empty pass reported the cursor as having moved')
    })

    it('restart: a second forwarder over the same cursor delivers nothing twice', async () => {
      const org = await tenant()
      const seq = await h.write(org.orgId, 'org.deleted')

      const first = new RecordingSink()
      await forwarderFor(first, new TestClock(), entitled).pass()
      assert.deepEqual(first.seqs(), [seq])

      // A new Forwarder, a new sink, the same cursor row: the process restarted.
      const second = new RecordingSink()
      const pass = await forwarderFor(second, new TestClock(), entitled).pass()

      assert.equal(pass.read, 0, 'a restarted forwarder re-read an entry it had already delivered')
      assert.deepEqual(
        second.seqs(), [],
        'a restart re-delivered the audit log, which is how a SIEM ends up with two of every entry',
      )
    })

    it('partial: a sink that refuses part way through leaves the cursor below what it refused', async () => {
      const org = await tenant()
      const seqs: number[] = []
      for (let i = 0; i < 6; i += 1) seqs.push(await h.write(org.orgId, `entry.${String(i)}`))

      const sink = new RecordingSink()
      // Two per batch, and the second batch is refused with an ordinary error,
      // which means "try again later" rather than "never".
      sink.fail = (batch) => (batch.entries[0]!.seq === seqs[2] ? new Error('collector timed out') : null)

      const f = forwarderFor(sink, new TestClock(), entitled, 2)
      const pass = await f.pass()

      assert.equal(pass.delivered, 2, 'the sink took a different number of entries than it accepted')
      assert.deepEqual(sink.seqs(), [seqs[0], seqs[1]])
      assert.equal(
        await h.readCursor(),
        seqs[1],
        'the cursor advanced past an entry no sink accepted, which loses it silently',
      )

      // And the next pass redelivers from there, which is what makes this at
      // least once rather than lossy.
      sink.fail = null
      const again = await f.pass()
      assert.equal(again.read, 4, 'the retry did not re-read exactly what was left owing')
      assert.deepEqual(sink.seqs(), seqs, 'the entries the sink refused never arrived')
      assert.equal(await h.readCursor(), seqs[5])
    })

    it('permanent: a batch the sink will never accept is given up on and does not stall the rest', async () => {
      const org = await tenant()
      const seqs: number[] = []
      for (let i = 0; i < 6; i += 1) seqs.push(await h.write(org.orgId, `entry.${String(i)}`))

      const sink = new RecordingSink()
      sink.fail = (batch) =>
        batch.entries[0]!.seq === seqs[2] ? new PermanentError('collector answered 413') : null

      const pass = await forwarderFor(sink, new TestClock(), entitled, 2).pass()

      assert.deepEqual(
        sink.seqs(),
        [seqs[0], seqs[1], seqs[4], seqs[5]],
        'a batch the endpoint will never accept was not skipped, or took the ones behind it with it',
      )
      assert.equal(pass.delivered, 4)
      assert.equal(
        await h.readCursor(),
        seqs[5],
        'the cursor stopped below a batch that was given up on, so every pass from now on would ' +
          'read it, drop it, and stall the stream while reporting a healthy sink',
      )
    })

    it('licensed and unlicensed: the entitlement decides, and the cursor advances either way', async () => {
      const paying = await tenant('enterprise')
      const free = await tenant('free')

      const paid = await h.write(paying.orgId, 'sso.login')
      const unpaid = await h.write(free.orgId, 'sso.login')

      const sink = new RecordingSink()
      const pass = await forwarderFor(sink, new TestClock(), entitled).pass()

      assert.equal(pass.read, 2, 'the forwarder did not read both organizations')
      assert.deepEqual(
        sink.seqs(), [paid],
        'an organization on the free plan had its audit log forwarded, or an entitled one did not',
      )
      assert.equal(pass.unlicensed, 1, 'the pass did not report the entry it skipped')
      assert.equal(
        await h.readCursor(), unpaid,
        'the cursor stopped at an unlicensed entry, so one organization that never buys audit ' +
          'streaming would stall the stream for every organization that did',
      )
    })

    it('lapses: an entitlement withdrawn while the process runs stops forwarding with no restart', async () => {
      const org = await tenant('enterprise')
      const sink = new RecordingSink()
      const f = forwarderFor(sink, new TestClock(), entitled)

      const before = await h.write(org.orgId, 'sso.login')
      assert.deepEqual((await f.pass()).organizations, [org.orgId])
      assert.deepEqual(sink.seqs(), [before], 'the entitled entry was not forwarded to begin with')

      // The plan changes under a forwarder that is already running. No restart,
      // no new Forwarder, no new sink.
      await h.setPlan(org.orgId, 'free')
      const after = await h.write(org.orgId, 'sso.login')
      const pass = await f.pass()

      assert.equal(pass.read, 1)
      assert.equal(pass.delivered, 0, 'forwarding continued after the entitlement was withdrawn')
      assert.equal(pass.unlicensed, 1)
      assert.deepEqual(sink.seqs(), [before])
      assert.equal(await h.readCursor(), after)
    })

    it('arrives: an entitlement granted while the process runs starts forwarding with no restart', async () => {
      const org = await tenant('free')
      const sink = new RecordingSink()
      const f = forwarderFor(sink, new TestClock(), entitled)

      await h.write(org.orgId, 'sso.login')
      assert.equal((await f.pass()).delivered, 0, 'a free plan organization was forwarded')

      await h.setPlan(org.orgId, 'enterprise')
      const paid = await h.write(org.orgId, 'sso.login')
      const pass = await f.pass()

      assert.equal(
        pass.delivered, 1,
        'a renewal did not start forwarding again, so the entitlement is cached somewhere it ' +
          'should not be and only a restart would fix it',
      )
      assert.deepEqual(sink.seqs(), [paid])
    })

    it('two organizations in one read get one batch each, each manifest naming its own', async () => {
      const a = await tenant('enterprise')
      const b = await seedTenant(h, 'enterprise')
      seeded.push(b.orgId)

      // Interleaved on purpose, so a forwarder that batched by read order
      // rather than by organization would produce a batch holding both.
      const a1 = await h.write(a.orgId, 'first')
      const b1 = await h.write(b.orgId, 'first')
      const a2 = await h.write(a.orgId, 'second')

      const sink = new RecordingSink()
      const pass = await forwarderFor(sink, new TestClock(), entitled).pass()

      assert.equal(pass.read, 3)
      assert.equal(pass.delivered, 3)
      assert.deepEqual(pass.organizations, [a.orgId, b.orgId].sort())
      assert.equal(sink.batches.length, 2, 'two organizations were put in one batch')

      for (const batch of sink.batches) {
        const orgs = new Set(batch.entries.map((e) => e.orgId))
        assert.equal(
          orgs.size, 1,
          'a batch carried entries from two organizations, so its manifest names one and ' +
            'covers both, and a receiver checking it would be checking the wrong claim',
        )
        assert.equal(batch.manifest.org, [...orgs][0])
        assert.equal(verify(batch, KEY).ok, true)
      }

      const forA = sink.batches.find((x) => x.manifest.org === a.orgId)!
      assert.deepEqual(forA.entries.map((e) => e.seq), [a1, a2])
      const forB = sink.batches.find((x) => x.manifest.org === b.orgId)!
      assert.deepEqual(forB.entries.map((e) => e.seq), [b1])
    })

    it('origins: every entry point that writes an audit entry is carried', async () => {
      // EVERY ENTRY POINT, SCRAPED RATHER THAN LISTED. The forwarder has no
      // WHERE clause on action or origin, so it is entry point independent by
      // construction, and this is the check that the construction is what
      // shipped. The origins come out of the source of every appendAudit call
      // site in the tree, so an entry point added later with an origin nothing
      // here covers fails this test rather than being silently missing from a
      // customer's SIEM.
      const origins = await originsInProduction()
      assert.ok(
        origins.length >= 5,
        `only ${String(origins.length)} origins were scraped out of the source, so the scan is ` +
          'not reading the call sites and this test would pass while carrying nothing',
      )

      const org = await tenant('enterprise')
      const written: number[] = []
      for (const origin of origins) written.push(await h.write(org.orgId, 'entry', { origin }))

      const sink = new RecordingSink()
      await forwarderFor(sink, new TestClock(), entitled).pass()

      assert.deepEqual(
        sink.entries().map((e) => e.origin).sort(),
        [...origins].sort(),
        'an origin some entry point writes did not reach the sink',
      )
      assert.deepEqual(sink.seqs(), written)
    })
  },
)

// ---------------------------------------------------------------------------

/** The chain hash the database stored for one sequence number, so a test can
 *  compare what a sink received against what was written rather than against
 *  itself. */
async function chainHash(h: Harness, seq: number): Promise<string> {
  const rows = await h.admin<{ entry_hash: string }[]>`
    SELECT entry_hash FROM audit_entries WHERE seq = ${seq}`
  return rows[0]!.entry_hash
}

/**
 * Every `origin:` an appendAudit call site in this repository writes.
 *
 * Read out of the source rather than written down, for the reason the writers
 * suite gives about its own scan: a list maintained by hand goes stale on the
 * entry point nobody remembered, and that entry point is the one whose entries
 * would be missing from a SIEM.
 */
async function originsInProduction(): Promise<string[]> {
  const here = path.dirname(fileURLToPath(import.meta.url))
  const repo = path.resolve(here, '..', '..', '..', '..')
  const roots = ['web/apps/api/src', 'web/packages/db/src', 'ee/web']
  const found = new Set<string>()

  for (const root of roots) {
    let entries
    try {
      entries = await readdir(path.join(repo, root), { withFileTypes: true, recursive: true })
    } catch {
      continue
    }
    for (const e of entries) {
      if (!e.isFile() || !e.name.endsWith('.ts')) continue
      const full = path.join(e.parentPath, e.name)
      if (full.includes('node_modules') || full.includes(`${path.sep}test${path.sep}`)) continue
      const source = await readFile(full, 'utf8')
      for (const m of source.matchAll(/origin: '([a-z_]+)'/g)) found.add(m[1]!)
    }
  }
  return [...found].sort()
}
