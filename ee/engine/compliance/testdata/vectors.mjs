// Reference vectors, produced by the control plane's own implementation.
//
// The Go verifier in this package has to agree, byte for byte, with
// web/packages/db/src/audit.ts, because that is what wrote every hash it will
// ever check. Two implementations that only agree with themselves would report
// a clean log as tampered or a tampered log as clean, and there is no way to
// tell which from reading either one.
//
// So this runs the real TypeScript and writes what it produced into
// audit-chain-vectors.json, and the Go test asserts two things: that it agrees
// with the recorded vectors, and, when node and the workspace's dependencies
// are present, that re-running this produces the recorded vectors unchanged.
// The second is the drift guard: a change to the canonical form on either side
// fails a test rather than silently invalidating every audit log in the field.
//
// Run from ee/engine/compliance/testdata:
//   node vectors.mjs > audit-chain-vectors.json
//
// The cases are chosen for the specific ways this can go wrong. A null actor
// and a null target, because those are stored as NULL and hashed as the empty
// string. An actor named "a" doing "b.c" beside one named "ab" doing ".c",
// which is the pair the length prefixes exist to tell apart. Nested objects
// with keys out of order, because the canonical form sorts them. A whole number
// written as 2.0, which JSON.stringify renders as 2. A string with a quote and
// a backslash. Non-ASCII, which is length-prefixed in bytes rather than in
// characters. And three different fractional-second values, because the
// timestamp is formatted with exactly three digits and Go's own RFC3339Nano
// would drop the trailing zero in .060 and hash something else.

import { auditEntryHash, canonicalJson } from '../../../../web/packages/db/src/audit.ts'
const cases = [
  { seq: 1, orgId: '11111111-1111-1111-1111-111111111111', actorUserId: null,
    actorLabel: 'a', action: 'b.c', targetType: 'organization', targetId: null,
    origin: 'web', detail: {}, occurredAt: new Date('2026-08-27T00:00:00.000Z'), prevHash: null },
  { seq: 2, orgId: '11111111-1111-1111-1111-111111111111', actorUserId: '22222222-2222-2222-2222-222222222222',
    actorLabel: 'ab', action: '.c', targetType: 'environment', targetId: 'env-1',
    origin: 'engine', detail: { z: 1, a: 'x', nested: { b: true, a: [1, 2, 'three'] }, n: null },
    occurredAt: new Date('2026-08-27T12:34:56.789Z'), prevHash: 'deadbeef' },
  { seq: 3, orgId: '11111111-1111-1111-1111-111111111111', actorUserId: null,
    actorLabel: 'unicode päßwörd 日本語', action: 'member.removed', targetType: 'user',
    targetId: '33333333-3333-3333-3333-333333333333', origin: 'scim',
    detail: { big: 1234567890123, float: 1.5, whole: 2.0, s: 'quote"and\\backslash' },
    occurredAt: new Date('2026-01-02T03:04:05.060Z'), prevHash: 'cafe' },
]
console.log(JSON.stringify(cases.map(c => ({
  ...c, occurredAt: c.occurredAt.toISOString(),
  canonicalDetail: canonicalJson(c.detail), hash: auditEntryHash(c),
})), null, 2))
