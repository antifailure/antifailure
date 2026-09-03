// The direction the operator route matrix does not check.
//
// admin-matrix.ts walks the router and asserts that every permission a route
// DECLARES exists in the catalog. That catches a typo. It cannot catch the
// opposite fault, because nothing in it ever looks at the catalog and asks what
// is missing from the router:
//
//   a permission that is in ADMIN_PERMISSIONS and guards no route at all.
//
// That is not a tidiness problem. A permission is granted to roles, described
// in ADMIN_PERMISSION_DESCRIPTIONS, rendered on the permissions page and read by
// whoever decides which role somebody gets. All of that says a capability
// exists. If no route declares it, the capability does not exist, and the
// operator granted it has been told they can do something they cannot. It is
// the same failure as a service method with zero call sites, wearing an access
// control table.
//
// It has already happened once here, and it is the reason this file exists.
// admin.audit.export has been in the catalog since 0029, is held by owner and
// security, is described as "Export the platform audit chain and verify its
// hashes", and no route has ever declared it. Every check in the repository
// passed the whole time, because every check was pointed the other way.
//
// THE EXEMPTION LIST IS THE POINT, not an escape hatch. A permission may sit in
// it only with the name of the lane wiring it, so the list is a work item
// rather than a silence. It must shrink. A test whose exemption list grows is a
// test being edited to agree with the code, which is worse than no test,
// because it reports green while the thing it guards gets worse.
//
// The tenant catalog has had this rule since it was written: permissions.test.ts
// asserts that every tenant permission guards at least one route. The platform
// catalog simply never grew the matching one.

import { test, describe } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { appRouter } from '../src/routers/index.ts'
import {
  ADMIN_PERMISSIONS,
  ADMIN_PERMISSION_DESCRIPTIONS,
  ADMIN_ROLE_PERMISSIONS,
  ADMIN_ROLES,
  adminRolesWith,
  type AdminPermission,
} from '../src/admin/permissions.ts'

/**
 * Permissions that guard no route yet, and who is wiring each one.
 *
 * ONLY EVER SHORTER. Adding a line here is claiming that a permission being
 * dead is a known, owned, temporary state, so it carries the owner's name. A
 * line with no owner is a permission somebody quietly gave up on, and it should
 * be deleted from the catalog instead.
 */
const AWAITING_A_ROUTE: Partial<Record<AdminPermission, string>> = {
  // Held by owner and security, described, granted, and guarding nothing since
  // 0029. The Security lane is building the export route behind it. When that
  // lands, this line is deleted rather than edited.
  'admin.audit.export': 'the Security lane, which is building the export route',
}

/**
 * Every permission enforced by a PLAIN HTTP route, read from the source.
 *
 * The walk below sees the tRPC tree and nothing else, so a permission enforced
 * on an ordinary route is invisible to it and reads as dead. That is not
 * hypothetical: `admin.impersonation.start` guards
 * `POST /v1/admin/impersonation/start`, which cannot be a procedure because it
 * ends in a Set-Cookie for the CUSTOMER's session, and this suite called it
 * dead on the day it was wired.
 *
 * An exemption would have been the wrong answer. The exemption list means "being
 * built", and this permission is not being built: it is enforced, right now, in
 * a place the instrument could not look. Widening the instrument is the fix;
 * exempting it would have taught the next reader that the list is where
 * inconvenient answers go.
 *
 * Read as source rather than by calling anything, because enforcement here is a
 * call inside a handler and there is no registry to ask. It still says no: a
 * permission enforced nowhere appears in neither set and fails.
 */
function httpGuardedPermissions(): Set<string> {
  const dir = new URL('../src/admin/', import.meta.url)
  const found = new Set<string>()
  for (const file of readdirSync(dir)) {
    if (!file.endsWith('.ts')) continue
    const source = readFileSync(new URL(file, dir), 'utf8')
    // Both spellings, because the first version of this read only a quoted
    // literal and the customers lane then hoisted its permission into a
    // constant, at which point the walk stopped seeing an enforcement that had
    // not moved. A check that a refactor can blind is a check that reports
    // green for a reason unrelated to the thing it guards.
    for (const m of source.matchAll(/adminRoleHas\([^,]+,\s*'(admin\.[a-z.]+)'\s*\)/g)) {
      found.add(m[1]!)
    }
    for (const m of source.matchAll(/adminRoleHas\([^,]+,\s*([A-Z][A-Z0-9_]*)\s*\)/g)) {
      const bound = source.match(
        new RegExp(`(?:const|let)\\s+${m[1]}\\s*(?::[^=]+)?=\\s*'(admin\\.[a-z.]+)'`),
      )
      if (bound) found.add(bound[1]!)
    }
  }
  return found
}

/** Every permission any route in the tree declares. */
function declaredPermissions(): Set<string> {
  const procedures = (
    appRouter as unknown as {
      _def: { procedures: Record<string, { _def: { meta?: { adminPermission?: string } } }> }
    }
  )._def.procedures
  const declared = new Set<string>()
  for (const path of Object.keys(procedures)) {
    const permission = procedures[path]!._def.meta?.adminPermission
    if (permission) declared.add(permission)
  }
  // The plain HTTP routes too, or a permission enforced on one reads as dead.
  for (const p of httpGuardedPermissions()) declared.add(p)
  return declared
}

describe('every platform permission actually guards something', () => {
  test('the walk found routes, so the assertions below are not vacuous', () => {
    // The same guard admin-matrix.ts opens with, for the same reason. Every
    // assertion in this file passes on an empty set, and a set that came back
    // empty because the import moved would report perfect coverage.
    const declared = declaredPermissions()
    assert.ok(
      declared.size >= 15,
      `only ${declared.size} distinct platform permissions are declared by any route in ` +
        'appRouter. Either the operator routes moved out of this tree or this walk is pointed ' +
        'at the wrong one, and in both cases the checks below are now checking nothing.',
    )
  })

  test('no permission is granted to a role while guarding no route', () => {
    const declared = declaredPermissions()
    const dead = ADMIN_PERMISSIONS.filter(
      (p) => !declared.has(p) && !(p in AWAITING_A_ROUTE),
    )
    assert.deepEqual(
      dead,
      [],
      `these platform permissions are in the catalog, are described, and are granted to roles, ` +
        `and no route declares any of them:\n  ${dead.join('\n  ')}\n` +
        'An operator holding one has been told they can do something the product cannot do. ' +
        'Either build the route it was meant to guard, or delete it from ADMIN_PERMISSIONS, ' +
        'ADMIN_PERMISSION_DESCRIPTIONS and every role that holds it. If it is genuinely being ' +
        'built right now, add it to AWAITING_A_ROUTE in this file with the name of the lane ' +
        'building it, and delete that line when the route lands.',
    )
  })

  test('the exemption list has not been used to hide a permission that now works', () => {
    // The half of the rule that makes the list shrink on its own. Without this,
    // a line stays after its route lands and the next dead permission gets
    // added beside it because that is what the list looks like it is for.
    const declared = declaredPermissions()
    const wired = Object.keys(AWAITING_A_ROUTE).filter((p) => declared.has(p))
    assert.deepEqual(
      wired,
      [],
      `these permissions are listed in AWAITING_A_ROUTE and a route now declares them:\n  ` +
        `${wired.join('\n  ')}\nDelete those lines. The list is a work item, and one that has ` +
        'been done is a line that makes the next reader think the list is decorative.',
    )
  })

  test('every exemption names the lane that owns it', () => {
    const nameless = Object.entries(AWAITING_A_ROUTE).filter(
      ([, owner]) => !owner || owner.trim() === '',
    )
    assert.deepEqual(
      nameless.map(([p]) => p),
      [],
      'an exemption with no owner is a permission somebody gave up on rather than one being ' +
        'built. Name the lane, or delete the permission from the catalog.',
    )
  })

  test('a permission is described, granted and guarded, or it is none of the three', () => {
    /*
     * The three lists that have to agree, checked as one fact rather than
     * three.
     *
     * admin-boundary.test.ts already asserts that every permission is described
     * and that every permission is held by some role. What no test asserted is
     * the JOIN: that the permission a role holds and the description an auditor
     * reads and the route that enforces it are the same permission. Two of the
     * three agreeing is exactly the state admin.audit.export was in.
     */
    const declared = declaredPermissions()
    const inconsistent: string[] = []
    for (const permission of ADMIN_PERMISSIONS) {
      const described = Boolean(ADMIN_PERMISSION_DESCRIPTIONS[permission])
      const held = adminRolesWith(permission).length > 0
      const guarded = declared.has(permission) || permission in AWAITING_A_ROUTE
      if (!described || !held || !guarded) {
        inconsistent.push(
          `${permission}: ${described ? 'described' : 'NOT described'}, ` +
            `${held ? 'held by a role' : 'held by NO role'}, ` +
            `${guarded ? 'guards a route' : 'guards NO route'}`,
        )
      }
    }
    assert.deepEqual(inconsistent, [], `\n  ${inconsistent.join('\n  ')}`)
  })

  test('no role is granted a permission that is not in the catalog', () => {
    // Cheap, and it fails in a way that names the role rather than the
    // permission. A role table entry that survives a permission being renamed
    // grants nothing and reads as if it grants something.
    const known = new Set<string>(ADMIN_PERMISSIONS)
    const strays: string[] = []
    for (const role of ADMIN_ROLES) {
      for (const permission of ADMIN_ROLE_PERMISSIONS[role]) {
        if (!known.has(permission)) strays.push(`${role} holds ${permission}`)
      }
    }
    assert.deepEqual(strays, [], `\n  ${strays.join('\n  ')}`)
  })
})
