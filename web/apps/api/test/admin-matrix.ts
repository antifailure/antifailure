// The operator route matrix, as something any tree can be pointed at.
//
// WHY THIS IS A SHARED FUNCTION RATHER THAN A BLOCK IN ONE SUITE. The portal is
// built by four agents and is deliberately NOT one tRPC tree. admin-money mounts
// its money routes separately at /admin/trpc rather than inside appRouter, and
// that is the right call for a reason worth repeating: permissions.test.ts walks
// appRouter asserting every procedure declares a TENANT permission, and teaching
// it to skip a third of what it walks would stop it being the thing that catches
// an unguarded route.
//
// But a second tree needs a SECOND WALK, or it is guarded by neither matrix. And
// a route nobody enumerates is worse than one in a tree that does enumerate it,
// because the second at least fails. The suite stays green precisely because
// nothing looked.
//
// That is the same fault as the migration queue seeing one branch, and as a
// duplicate `admin:` key in an object literal: A GUARD THAT ENUMERATES REALITY
// ONLY ENUMERATES THE REALITY IT WAS POINTED AT. So the walk takes the tree as
// an argument, and adding a tree means pointing this at it.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { ADMIN_PERMISSIONS } from '../src/admin/permissions.ts'

/** The shape every tRPC router exposes, narrowed to what the walk needs. */
interface WalkableRouter {
  _def: { procedures: Record<string, { _def: { meta?: { adminPermission?: string } } }> }
}

export interface MatrixOptions {
  /** What to call this tree when an assertion fails. */
  name: string
  /** The router to walk. */
  router: unknown
  /**
   * The path prefix operator routes must carry.
   *
   * Load bearing rather than cosmetic: maintenance mode exempts `/trpc/admin.*`
   * so an operator can still reach the switch that releases an outage, and a
   * route outside the prefix goes dark exactly when it is needed.
   */
  prefix: string
  /**
   * The fewest routes this tree must contain.
   *
   * Required rather than optional, and it is the assertion that matters most.
   * Without it every check below passes on an empty list, which is how a matrix
   * test comes to guard nothing while reporting green. A tree whose routes move
   * elsewhere should fail here loudly rather than quietly stop checking.
   */
  atLeast: number
}

/**
 * Asserts every operator route in one tree declares a real platform permission.
 *
 * Call it inside a `describe` from any suite. It registers four tests and needs
 * no database, which is the point: this is the gate that catches a route added
 * next month, on the machine where it was added, rather than in CI.
 */
export function assertOperatorRoutesAreGuarded(options: MatrixOptions): void {
  const procedures = (options.router as WalkableRouter)._def.procedures
  const paths = Object.keys(procedures).filter((p) => p.startsWith(options.prefix))

  test(`${options.name}: has operator routes at all, so the checks below are not vacuous`, () => {
    assert.ok(
      paths.length >= options.atLeast,
      `${options.name} has ${paths.length} routes under "${options.prefix}", expected at least ` +
        `${options.atLeast}. Either routes moved out of this tree, or this walk is pointed at ` +
        `the wrong one, and in both cases the assertions below are now checking nothing.`,
    )
  })

  test(`${options.name}: no route is unguarded`, () => {
    const unguarded = paths.filter((p) => !procedures[p]!._def.meta?.adminPermission)
    assert.deepEqual(
      unguarded,
      [],
      `these ${options.name} routes declare no permission, so they run unguarded:\n  ` +
        `${unguarded.join('\n  ')}\n` +
        'Build them with adminProcedure(permission), which takes the permission as an argument ' +
        'so declaring it and creating the route are one act.',
    )
  })

  test(`${options.name}: every declared permission exists in the platform catalog`, () => {
    // A permission that is not in the catalog is not a typo caught elsewhere:
    // adminRoleHas returns false for it, so the route refuses EVERY operator
    // including owner, and it looks like a permissions bug rather than a
    // spelling one. admin-infra found this shape by breaking it on purpose.
    const known = new Set<string>(ADMIN_PERMISSIONS)
    const bogus = paths
      .map((p) => [p, procedures[p]!._def.meta!.adminPermission!] as const)
      .filter(([, perm]) => perm !== undefined && !known.has(perm))
    assert.deepEqual(
      bogus,
      [],
      `these ${options.name} routes declare permissions that do not exist in ` +
        `src/admin/permissions.ts: ${JSON.stringify(bogus)}`,
    )
  })

  test(`${options.name}: every route carries the "${options.prefix}" prefix`, () => {
    const all = Object.keys(procedures)
    const strays = all.filter(
      (p) => procedures[p]!._def.meta?.adminPermission && !p.startsWith(options.prefix),
    )
    assert.deepEqual(
      strays,
      [],
      `these routes declare a platform permission but sit outside "${options.prefix}":\n  ` +
        `${strays.join('\n  ')}\n` +
        'Maintenance mode exempts /trpc/admin.*, so a route outside the prefix is refused ' +
        'with 503 exactly when an operator needs it to end an outage.',
    )
  })
}
