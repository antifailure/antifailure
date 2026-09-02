// The Developer Platform lane of the operator portal.
//
// WHAT THIS FILE IS FOR, and why it is empty rather than absent. The portal has
// six navigation groups and they are built in parallel. One module per group,
// mounted once, is what lets that happen without two people editing the same
// file: the Developer Platform sections own THIS file and console/app/admin/platform, and
// nothing else. A lane that had to add its routes to router.ts instead would
// put six writers in one object literal, and a duplicate key in an object
// literal is the one merge conflict git does not report. It has already
// happened once in this directory: see the note in infra.ts about a second
// `admin:` key silently winning.
//
// So the mount is made first and the routes come later. The empty router below
// is not a placeholder nobody reads: it is mounted in router.ts and
// admin-namespaces.test.ts asserts that it is, exactly once, which is what
// makes this a reserved namespace rather than a file somebody forgot.
//
// THE SECTIONS THIS LANE OWNS: Repositories & Pull Requests, MCP Management, API Keys, and Integrations & Webhooks.
//
// PERMISSION PREFIXES RESERVED FOR IT: admin.keys.*, admin.webhooks.*, admin.deploys.*. The
// catalog and the reservations are in permissions.ts, and adding a permission
// under another lane's prefix is the kind of thing a review misses.
//
// A key or a webhook secret is never returned by a route in this file. An operator needs to know that a key exists, when it was last used and how to revoke it; the value itself answers no operator question and reading it is the whole risk.
//
// HOW TO ADD A ROUTE. Build it with adminProcedure(permission), which is the
// only exported way to make one, so declaring the permission and creating the
// route are a single act. There is no unguarded builder to reach for. Every
// read goes through ctx.adminDb, which is the operator pool: a cross tenant
// read has to be a credential the application cannot acquire rather than a
// claim it makes about itself. A mutation records what it changed with
// adminAudit, inside the same transaction as the change.
//
//   import { z } from 'zod'
//   import { adminProcedure, type AdminContext } from './trpc.ts'
//
//   export const platformRouter = router({
//     list: adminProcedure('admin.example.read')
//       .input(z.object({ limit: z.number().int().min(1).max(200).default(50) }))
//       .query(async ({ ctx, input }) => {
//         const c = ctx as AdminContext
//         return c.adminDb((db) => db.execute(sql`...`))
//       }),
//   })

import { router } from '../trpc.ts'

/**
 * The Developer Platform namespace, mounted at `admin.platform`.
 *
 * Empty today. It is still reachable, still walked by the operator route
 * matrix, and still exempt from maintenance mode by its `admin.` prefix, so a
 * route added to it inherits all three without anybody remembering to arrange
 * them.
 */
export const platformRouter = router({})
