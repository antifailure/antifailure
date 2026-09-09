// A control plane the compliance pack can be run against, built by the control
// plane's own code.
//
// WHY THIS IS NODE AND NOT GO. Everything this writes has to be written by the
// thing that writes it in production, or the run proves the pack agrees with a
// fixture rather than with the product. The migrations are applied by
// web/packages/db/src/migrate.ts, the tenant is seeded by the same fixture the
// tenancy suite uses, and every audit entry is appended by appendAudit, which
// is the one implementation that has ever written an entry_hash anybody will
// ever verify. A Go seeder would have had to reimplement the hash, and a
// verifier checked against a chain it wrote itself agrees with itself.
//
// It writes into whatever database AF_TEST_DATABASE_URL names, which the Go
// suite creates and drops around this. Nothing here chooses a database.
//
// It prints one JSON document describing what it wrote, so the Go side asserts
// against the sequence numbers this actually produced rather than against
// numbers it assumed. A sequence is a database's to assign.

import { readFile } from 'node:fs/promises'
import { setup, seedTenant } from '../../../../web/packages/db/test/harness.ts'
import { appendAudit } from '../../../../web/packages/db/src/audit.ts'

const h = await setup()

// Two organizations, and they are not interchangeable.
//
// `clean` is the one the published evidence is produced from and the one that
// has to evaluate with nothing failed, so the exit code 0 case is a real
// report rather than an empty one. `tampered` exists so the tests that alter
// and delete entries never touch the chain the clean report is about: a
// deletion is not undoable, and a suite whose negative arm corrupts its own
// positive arm proves whichever ran first.
const clean = await seedTenant(h.admin, 'compliance-clean')
const tampered = await seedTenant(h.admin, 'compliance-tampered')

// The fixture writes one audit row with a placeholder hash so the tenancy
// suite has something to look for. It belongs to no chain, so both chains
// start from nothing, exactly as audit.test.ts does.
await h.admin`DELETE FROM audit_entries WHERE org_id IN (${clean.orgId}, ${tampered.orgId})`

const day = 24 * 60 * 60 * 1000
const now = Date.now()
// The window the Go side reports on. The anchor entry is written OUTSIDE it on
// purpose: the first entry inside any period carries the hash of the entry
// before it, and a reader that did not fetch that one would report a broken
// link at the first row of every report ever produced. That behaviour has no
// unit test that can see it, because it is a property of the query.
const windowStart = new Date(now - 30 * day)

async function append(org, input) {
  return h.pool.withTenant({ orgId: org }, (db) => appendAudit(db, input))
}

const anchor = await append(clean.orgId, {
  orgId: clean.orgId,
  actorLabel: 'ada',
  action: 'organization.created',
  targetType: 'organization',
  targetId: clean.orgId,
  origin: 'web',
  detail: { plan: 'team' },
  occurredAt: new Date(now - 45 * day),
})

// The entries inside the window. The two access ones are the pair the
// access-removal control exists for: a removal is only evidence of access
// ending if the same member's sessions were revoked with it.
const inside = []
for (const input of [
  {
    actorLabel: 'ada', action: 'environment.created', targetType: 'environment',
    targetId: `env-${clean.slug}`, origin: 'engine',
    detail: { branch: 'main', repository: `${clean.slug}/app` },
    occurredAt: new Date(now - 20 * day),
  },
  {
    actorLabel: 'ada', action: 'golden.published', targetType: 'golden',
    targetId: '2026-01-01T00-00-00Z', origin: 'engine',
    detail: { verified: true, rows: 318000 },
    occurredAt: new Date(now - 18 * day),
  },
  {
    actorLabel: 'ada', action: 'member.removed', targetType: 'user',
    targetId: clean.userId, origin: 'web',
    detail: { role: 'member' },
    occurredAt: new Date(now - 10 * day),
  },
  {
    actorLabel: 'ada', action: 'session.revoked', targetType: 'user',
    targetId: clean.userId, origin: 'web',
    detail: { sessions: 2 },
    occurredAt: new Date(now - 10 * day + 1000),
  },
  {
    actorLabel: 'ada', action: 'environment.torn_down', targetType: 'environment',
    targetId: `env-${clean.slug}`, origin: 'engine',
    detail: { reason: 'pull request closed' },
    occurredAt: new Date(now - 2 * day),
  },
]) {
  inside.push(await append(clean.orgId, { orgId: clean.orgId, ...input }))
}

// The disposable chain. Five entries so that a deletion in the middle has
// entries on both sides of it, which is what makes a broken link visible.
const disposable = []
for (let i = 0; i < 5; i += 1) {
  disposable.push(
    await append(tampered.orgId, {
      orgId: tampered.orgId,
      actorLabel: 'grace',
      action: 'environment.created',
      targetType: 'environment',
      targetId: `env-${i}`,
      origin: 'engine',
      detail: { index: i },
      occurredAt: new Date(now - (20 - i) * day),
    }),
  )
}

// The environment the fixture created, torn down, so the teardown control has
// a destroyed environment to report rather than a running one.
await h.admin`
  UPDATE environments SET torn_down_at = now(), state = 'torn_down'
   WHERE org_id = ${clean.orgId}`

// A real signed attestation, on the golden the fixture published. The pack
// checks the signature before it repeats what the document says, and that path
// has never run against a value that came back out of Postgres as jsonb.
//
// Through h.admin.json rather than as a string with a ::jsonb cast, and the
// difference is not cosmetic. postgres.js sends a JS string bound to a jsonb
// parameter as a JSON STRING, so `{"report":...}` is stored as
// `"{\"report\":...}"`, one level deeper than it looks. Reading it back with
// `attestation::text` then yields a quoted document, ParseAttestation cannot
// decode it, and every masking scan is reported as unverifiable. Caught by
// looking at the stored row rather than at the statement that wrote it.
const attestation = JSON.parse(
  await readFile(new URL('./attestation-clean.json', import.meta.url), 'utf8'),
)
await h.admin`
  UPDATE golden_versions SET attestation = ${h.admin.json(attestation)}
   WHERE org_id = ${clean.orgId}`

// Egress decisions and no leak. Both read out of `events`, which is
// partitioned by occurred_at, so these also prove the reader's query reaches a
// partitioned table rather than only the parent.
for (const [i, decision] of [
  { host: 'api.stripe.com', mode: 'sandbox' },
  { host: 'api.openai.com', mode: 'block' },
  { host: 'metrics.example.test', mode: 'block' },
].entries()) {
  await h.admin`
    INSERT INTO events (org_id, idempotency_key, env_id, type, payload, occurred_at, sequence)
    VALUES (${clean.orgId}, ${`egress-${i}`}, ${`env-${clean.slug}`}, 'egress.decision',
            ${h.admin.json(decision)}, ${new Date(now - 5 * day)}, ${100 + i})`
}

console.log(
  JSON.stringify(
    {
      cleanOrgId: clean.orgId,
      cleanSlug: clean.slug,
      cleanUserId: clean.userId,
      tamperedOrgId: tampered.orgId,
      windowStart: windowStart.toISOString(),
      anchorSeq: anchor.seq,
      insideSeqs: inside.map((e) => e.seq),
      insideHead: inside[inside.length - 1].entryHash,
      disposableSeqs: disposable.map((e) => e.seq),
    },
    null,
    2,
  ),
)

await h.close()
