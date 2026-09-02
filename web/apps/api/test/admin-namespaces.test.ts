// The six navigation groups have a mounted namespace each, and exactly one.
//
// WHY THIS TEST EXISTS. The portal's twenty two sections are built in parallel,
// and the arrangement that makes that safe is one router module per navigation
// group, mounted once in admin/router.ts. That arrangement is worth nothing if
// it is only a comment. Three ways it silently stops being true:
//
//   a module is written and never mounted, so a lane adds routes to a router
//     nothing serves and every one of them answers 404;
//   two lanes mount under the same key, and the second `customers:` in an
//     object literal does not conflict in git, does not fail to compile, and
//     silently wins or loses on key order. That exact failure has already
//     happened in this directory once, with `admin:`;
//   a lane's routes drift out from under its own key, so the console's paths
//     and the module that owns them stop agreeing.
//
// None of the three is visible in a review of the diff that causes it, and all
// three are visible here.
//
// WHY IT DOES NOT ASSERT THE ROUTERS ARE EMPTY. They are, today, and they are
// meant to stop being. A test that pinned that would have to be deleted by the
// first person to do the work it exists to enable, and a test everybody deletes
// is a test that guards nothing.

import { describe, test } from 'node:test'
import { readFileSync } from 'node:fs'
import assert from 'node:assert/strict'
import { adminRouter } from '../src/admin/router.ts'
import { administrationRouter } from '../src/admin/administration.ts'
import { customersRouter } from '../src/admin/customers.ts'
import { operationsRouter } from '../src/admin/operations.ts'
import { platformRouter } from '../src/admin/platform.ts'
import { productRouter } from '../src/admin/product.ts'
import { securityRouter } from '../src/admin/security.ts'

/** The navigation's six groups, in the product owner's order, and the module
 *  each one is served by. The slugs are the same strings console/lib/admin-nav
 *  declares as `slug`, which is what makes a route in the console and a module
 *  on the server findable from each other. */
const GROUPS = {
  customers: customersRouter,
  product: productRouter,
  platform: platformRouter,
  operations: operationsRouter,
  security: securityRouter,
  administration: administrationRouter,
} as const

/** What a tRPC router exposes, narrowed to the two things this file reads. */
interface Mounted {
  _def: {
    record: Record<string, unknown>
    procedures: Record<string, unknown>
  }
}

describe('the operator portal reserves one namespace per navigation group', () => {
  const record = (adminRouter as unknown as Mounted)._def.record

  test('every group is mounted', () => {
    const missing = Object.keys(GROUPS).filter((slug) => !(slug in record))
    assert.deepEqual(
      missing,
      [],
      `these navigation groups have a module and no mount, so every route added to them ` +
        `would answer 404:\n  ${missing.join('\n  ')}\n` +
        'Add `slug: slugRouter` to adminRouter in src/admin/router.ts.',
    )
  })

  test('each group is mounted with the module that names it, and no other', () => {
    // READ FROM SOURCE, and that is a decision rather than laziness.
    //
    // The obvious test is to compare the mounted value against the module's
    // export by identity. It does not work and cannot be made to: mounting a
    // child router COPIES its record into the parent, so the parent never holds
    // the object the module exported. And two empty routers are identical at
    // runtime in every other respect, so there is nothing left to tell
    // `productRouter` from `platformRouter` once they are mounted.
    //
    // The mistake this catches lives in the source anyway. `platform:
    // productRouter` is a copy and paste, it compiles, it mounts, and it serves
    // the wrong lane's routes the moment either lane writes one. The line that
    // says which module is mounted where is the only place that fact exists, so
    // it is the place to check it.
    const source = readRouterSource()
    for (const slug of Object.keys(GROUPS)) {
      assert.match(
        source,
        new RegExp(`^  ${slug}: ${slug}Router,$`, 'm'),
        `src/admin/router.ts does not mount "${slug}" with ${slug}Router. A group mounted with ` +
          "another group's module compiles and serves the wrong lane's routes.",
      )
      assert.match(
        source,
        new RegExp(`^import \\{ ${slug}Router \\} from '\\./${slug}\\.ts'$`, 'm'),
        `src/admin/router.ts does not import ${slug}Router from './${slug}.ts', so the name in ` +
          'the mount above may belong to a different file than the one that owns the lane.',
      )
    }
  })

  test('no group is mounted twice', () => {
    // A duplicate key in an object literal is legal TypeScript, legal
    // JavaScript, and a clean merge. What it is not is two mounts: the last one
    // wins and the first one's routes are gone with no error anywhere. Counting
    // the source is the only place that can see it, because by the time the
    // object exists there is only one key left.
    const source = readRouterSource()
    for (const slug of Object.keys(GROUPS)) {
      const mounts = source.match(new RegExp(`^  ${slug}: \\w+Router,$`, 'gm')) ?? []
      assert.equal(
        mounts.length,
        1,
        `src/admin/router.ts mounts "${slug}" ${mounts.length} times. A second key with the ` +
          "same name is a clean merge that silently discards one lane's routes.",
      )
    }
  })

  test('every route under a group carries its group in the path', () => {
    // The console reaches these as admin.<group>.<route>, and the navigation
    // routes are /admin/<group>/<section>. A route that ends up under the wrong
    // group is reachable, so nothing else fails, and the page that wants it
    // looks in the other lane's module forever.
    const paths = Object.keys((adminRouter as unknown as Mounted)._def.procedures)
    for (const slug of Object.keys(GROUPS)) {
      const own = Object.keys(
        (GROUPS[slug as keyof typeof GROUPS] as unknown as Mounted)._def.procedures,
      )
      const wrong = own.filter((p) => !paths.includes(`${slug}.${p}`))
      assert.deepEqual(
        wrong,
        [],
        `these routes are exported by src/admin/${slug}.ts and are not served under ` +
          `admin.${slug}.*:\n  ${wrong.join('\n  ')}`,
      )
    }
  })
})

/** The mount list as written, because a duplicate key is invisible once the
 *  object has been built. Read from disk rather than imported for the same
 *  reason. */
function readRouterSource(): string {
  // Read straight from source, which is how this suite runs.
  return readFileSync(new URL('../src/admin/router.ts', import.meta.url), 'utf8')
}
