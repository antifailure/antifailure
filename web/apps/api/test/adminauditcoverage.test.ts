// Every operator mutation records what it did, checked rather than believed.
//
// THE RULE THIS ENFORCES, from admin-audit.ts and admin/trpc.ts: reads are
// audited automatically by the middleware, refusals are audited by
// recordRefusal, and MUTATIONS audit themselves by calling adminAudit inside
// their own transaction. The first two are one call site each and cannot rot.
// The third is a line every new route has to remember, and it is exactly the
// kind of line that is remembered nineteen times and forgotten once.
//
// admin-routes.test.ts counts audit entries around the writes it drives, which
// is the strongest possible check and covers the routes it drives. It cannot
// cover a route it does not know about, and a route added next month with no
// audit call and no test is green everywhere: it compiles, it works, it returns
// a success, and the log is missing the one action somebody later comes looking
// for. An audit log missing the actions people care about is worse than no
// audit log, because it looks complete.
//
// SO THIS IS A STATIC CHECK, AND IT IS HONEST ABOUT BEING ONE. It reads the
// source of every module under src/admin, finds every route built with
// adminProcedure that declares .mutation, and asks whether an audit call is
// REACHABLE from that handler: directly, or through the helpers in this
// directory that it calls. It cannot prove the call happens on every path, and
// it does not claim to. What it proves is that a mutation with no audit call
// anywhere behind it fails a test, which is the failure that actually happens.
//
// WHY THE WALK IS TRANSITIVE. The nine billing mutations do not call adminAudit
// themselves. They call into money.ts, which calls runOnce in ledger.ts, which
// calls appendAdminAudit. A check that looked only at the handler body would
// report all nine as unaudited, and the fix for a false alarm on nine routes is
// that somebody deletes the check.
//
// The test at the bottom points the same walker at a body that audits nothing
// and asserts it says no. A check that cannot return no is worse than no check:
// it is a green light nobody wired up.

import { describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'

const ADMIN_DIR = new URL('../src/admin/', import.meta.url)

/** What counts as recording an action. Both are the real entry points: the
 *  helper in admin/trpc.ts, and the database function it wraps. */
const AUDIT_CALLS = /\b(adminAudit|appendAdminAudit)\s*\(/

/**
 * Names the walk refuses to follow, and the reason it has to.
 *
 * THE NEAR MISS THIS EXISTS FOR, found by the negative test below rather than
 * by reading. Every route's segment begins with its own `adminProcedure(...)`
 * call. adminProcedure is defined in this directory, and its body contains the
 * middleware that calls auditRead and recordRefusal, both of which call
 * appendAdminAudit. So a transitive walk that followed it found an audit call
 * from EVERY route, including a mutation that records nothing, and the whole
 * check passed vacuously while looking green.
 *
 * Those three are the automatic mechanisms: one entry per read, one per
 * refusal. They are exactly what a mutation's own entry is not, so following
 * them is following the wrong thing.
 */
const NEVER_FOLLOW = new Set(['adminProcedure', 'auditRead', 'recordRefusal'])

/** How deep the call graph is followed. Six is well past the deepest real
 *  chain, which is route -> money.ts -> ledger.ts -> appendAdminAudit, and a
 *  ceiling means a cycle cannot hang the suite. */
const MAX_DEPTH = 6

function adminSources(): Map<string, string> {
  const files = new Map<string, string>()
  for (const name of readdirSync(ADMIN_DIR)) {
    if (!name.endsWith('.ts')) continue
    files.set(name, readFileSync(new URL(name, ADMIN_DIR), 'utf8'))
  }
  return files
}

/**
 * Every module-level definition in this directory, by name.
 *
 * Names are global across the directory rather than per file, which is coarser
 * than resolving imports and is the right kind of coarse: a false MATCH here
 * would need two functions with the same name in one directory where the wrong
 * one audits, and the failure mode of being coarse is that the check passes
 * something it should have looked at more closely. A false ALARM is what makes
 * a check get deleted, and this shape cannot produce one.
 */
function definitions(files: Map<string, string>): Map<string, string> {
  const defs = new Map<string, string>()
  const start = /^(?:export\s+)?(?:async\s+)?(?:function|const)\s+([A-Za-z_$][\w$]*)/gm
  for (const source of files.values()) {
    const marks: { name: string; at: number }[] = []
    for (const m of source.matchAll(start)) marks.push({ name: m[1]!, at: m.index! })
    marks.forEach((mark, i) => {
      const end = i + 1 < marks.length ? marks[i + 1]!.at : source.length
      defs.set(mark.name, source.slice(mark.at, end))
    })
  }
  return defs
}

/** Whether an audit call is reachable from this body through the directory's
 *  own helpers. */
function auditIsReachable(
  body: string,
  defs: Map<string, string>,
  seen = new Set<string>(),
  depth = 0,
): boolean {
  if (AUDIT_CALLS.test(body)) return true
  if (depth >= MAX_DEPTH) return false
  for (const call of body.matchAll(/\b([A-Za-z_$][\w$]*)\s*\(/g)) {
    const name = call[1]!
    if (seen.has(name) || NEVER_FOLLOW.has(name)) continue
    const target = defs.get(name)
    if (!target) continue
    seen.add(name)
    if (auditIsReachable(target, defs, seen, depth + 1)) return true
  }
  return false
}

interface Route {
  file: string
  name: string
  permission: string
  body: string
}

/**
 * Every route in the directory, split at its own `adminProcedure(` call.
 *
 * A route runs from the key that names it to the next key that names one, which
 * is what makes the segment the whole builder chain: the input schema, the
 * handler, and nothing from its neighbour.
 */
function routes(files: Map<string, string>): Route[] {
  const found: Route[] = []
  const head = /([A-Za-z_$][\w$]*)\s*:\s*adminProcedure\(\s*'([^']+)'\s*\)/g
  for (const [file, source] of files) {
    const marks: { name: string; permission: string; at: number }[] = []
    for (const m of source.matchAll(head)) {
      marks.push({ name: m[1]!, permission: m[2]!, at: m.index! })
    }
    marks.forEach((mark, i) => {
      const end = i + 1 < marks.length ? marks[i + 1]!.at : source.length
      found.push({ file, name: mark.name, permission: mark.permission, body: source.slice(mark.at, end) })
    })
  }
  return found
}

describe('every operator mutation records what it changed', () => {
  const files = adminSources()
  const defs = definitions(files)
  const all = routes(files)

  test('the walk found the routes it is supposed to be checking', () => {
    // Without this the suite passes brilliantly against zero routes, which is
    // what a regex that stopped matching looks like from the outside. The floor
    // is the count on the branch this was written on, and it rises with the
    // portal the same way the matrix floor does.
    assert.ok(
      all.length >= 45,
      `the source walk found only ${all.length} operator routes, so it has stopped matching them`,
    )
    const mutations = all.filter((r) => /\.mutation\(/.test(r.body))
    assert.ok(
      mutations.length >= 20,
      `the walk found only ${mutations.length} operator mutations, which is fewer than exist`,
    )
  })

  test('no mutation writes without recording it', () => {
    const silent = all
      .filter((r) => /\.mutation\(/.test(r.body))
      .filter((r) => !auditIsReachable(r.body, defs))
      .map((r) => `${r.file}: ${r.name} (${r.permission})`)

    assert.deepEqual(
      silent,
      [],
      'these operator mutations change something and no audit call is reachable from them:\n  ' +
        silent.join('\n  ') +
        '\n\nCall adminAudit(db, ctx, {...}) inside the mutation\'s own transaction, before the ' +
        'write, so the change cannot commit without its record.',
    )
  })

  test('the check says no when a mutation records nothing', () => {
    // An instrument that cannot return no is a green light nobody wired up.
    // Two bodies, differing only in the audit call.
    const silent = `
      suspend: adminProcedure('admin.tenants.suspend')
        .mutation(async ({ ctx, input }) => {
          const c = ctx as AdminContext
          return c.adminDb(async (db) => {
            await db.execute(sql\`UPDATE organizations SET suspended_at = now()\`)
            return { suspended: true }
          })
        })`
    const recorded = silent.replace(
      'await db.execute',
      'await adminAudit(db, c, { action: \'organization.suspended\' })\n            await db.execute',
    )
    assert.equal(auditIsReachable(silent, defs), false, 'the check passed a mutation that records nothing')
    assert.equal(auditIsReachable(recorded, defs), true, 'the check failed a mutation that records properly')
  })

  test('a query that records nothing is reported as recording nothing', () => {
    // The vacuity guard, made against real source rather than a synthetic
    // string. security.posture is a read: the middleware records it and the
    // handler records nothing itself. If the walker reports an audit call
    // reachable from THIS, it is reporting one reachable from everything, and
    // the test above is passing for a reason that has nothing to do with the
    // routes.
    const posture = all.find((r) => r.name === 'posture')
    assert.ok(posture, 'security.posture is not in the walk any more')
    assert.equal(
      auditIsReachable(posture.body, defs),
      false,
      'a plain read appears to audit, so the walk is finding the middleware and not the handler',
    )
  })

  test('the transitive walk is what makes the billing routes pass, not a blanket exemption', () => {
    // Stated as its own test because it is the property most likely to be
    // broken by a refactor: move the audit call out of ledger.ts and nine
    // routes stop recording anything, with no other symptom. If this ever
    // fails, the answer is not to widen the walk.
    const refund = all.find((r) => r.name === 'refund')
    assert.ok(refund, 'the billing refund route is not in the walk any more')
    assert.equal(
      AUDIT_CALLS.test(refund.body),
      false,
      'refund now audits directly, so this test is describing a shape that no longer exists',
    )
    assert.equal(
      auditIsReachable(refund.body, defs),
      true,
      'the refund route can no longer reach an audit call through money.ts and ledger.ts',
    )
  })
})

describe('the actions that are recorded outside a route', () => {
  // Three things happen to an operator account with no adminProcedure behind
  // them, because they happen before or instead of a session. All three are
  // recorded in admin/session.ts, and a portal that recorded failures and not
  // successes could not answer "who was signed in at three in the morning",
  // which is the first question of every incident review.
  const source = readFileSync(new URL('session.ts', ADMIN_DIR), 'utf8')

  for (const action of ['admin.signin_failed', 'admin.signed_in', 'admin.signed_out']) {
    test(`${action} is written`, () => {
      assert.match(
        source,
        new RegExp(`action: '${action.replace('.', '\\.')}'`),
        `${action} is no longer recorded, so the operator chain has a hole where a sign-in belongs`,
      )
    })
  }
})
