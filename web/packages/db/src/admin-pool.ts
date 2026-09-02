// The operator's connection, and why it is a second pool rather than a flag.
//
// WHAT THIS IS. A distinct postgres client, authenticating with its own
// credential, whose role holds BYPASSRLS. It is never the application pool with
// an option set, and there is no way to reach it from `Pool`. The two objects
// share nothing but the `Db` type.
//
// WHY A SECOND POOL AND NOT A SECOND MODE. Every cross tenant read in this
// system has to be a CREDENTIAL the application cannot acquire, rather than a
// CLAIM the application makes about itself. A boolean on createPool would be a
// claim: any code path that could reach the pool could set it, and the whole
// boundary would rest on nobody passing `true` by accident. A separate login
// with a separate password is not something a request handler can talk its way
// into. It either has the connection string or it does not.
//
// WHY BYPASSRLS AND NOT A POLICY NAMING THE ROLE. This is the trap, and it is
// worth stating precisely because the two mechanisms look interchangeable and
// are not. Postgres applies a policy when the current user HAS THE PRIVILEGES
// OF the role the policy names, not when it is acting as that role. So a
// `CREATE POLICY ... FOR SELECT TO antifailure_admin` would also apply to
// antifailure_app the moment anybody granted membership, and no test asserting
// "the operator role can read every tenant" would ever notice.
//
// A ROLE ATTRIBUTE BEHAVES THE OPPOSITE WAY, and that is the whole reason this
// works. Attributes (BYPASSRLS, SUPERUSER, LOGIN) are NOT inherited through
// role membership; only privileges are. Measured on Postgres 17.11 rather than
// assumed:
//
//   GRANT probe_admin TO probe_app, default INHERIT, probe_admin BYPASSRLS
//     probe_app reading an RLS table with no scope set  ->  0 rows
//     probe_app after an explicit SET ROLE probe_admin  ->  2 rows
//     with a policy FOR SELECT TO probe_admin instead   ->  2 rows, no SET ROLE
//
// The third line is the trap and the first is the reason this file exists:
// membership alone buys the application nothing, so even a mistaken GRANT does
// not widen antifailure_app. Escalation requires an explicit SET ROLE, which
// requires membership somebody has to have granted on purpose, or the operator
// password, which the application is not given.
//
// WHY IT VERIFIES ITSELF ON FIRST USE. A role that exists WITHOUT the attribute
// reads exactly zero rows through the policies, and every operator screen would
// render its empty state. An empty state is indistinguishable from a product
// with no customers, so the misconfiguration that matters most here is also the
// one that looks like ordinary, working software. `ensureBypass` turns that
// silent condition into a refusal that names the role.

import postgres from 'postgres'
import { drizzle } from 'drizzle-orm/postgres-js'
import { sql as rawSql } from 'drizzle-orm'
import * as schema from './schema.ts'
import type { Db } from './client.ts'

export interface AdminPoolOptions {
  /**
   * The operator connection string, which carries its OWN role and password.
   *
   * Deliberately not derived from the application's URL by swapping the user.
   * Deriving it would mean the application holds everything needed to build the
   * operator credential, which is the property this whole file exists to deny.
   */
  url: string
  /** Connections per replica. Small on purpose: this pool serves operators,
   *  who are a handful of people, not customer traffic. */
  max?: number
  /** Seconds a statement may run. Higher than the application's, because an
   *  operator query legitimately scans across every tenant and the application
   *  ceiling is tuned for queries that never do. */
  statementTimeoutSeconds?: number
  connectTimeoutSeconds?: number
  onNotice?: (notice: unknown) => void
}

/** Who is acting, recorded on the connection for the duration of one
 *  transaction so a statement log can be tied back to a person. */
export interface AdminOperator {
  /** The admin_users row. Never a users row: the two id spaces are separate
   *  and audit_entries.actor_user_id has a foreign key to the second one. */
  adminUserId: string
  /** How to name them a year from now, when the row may be gone. */
  label: string
}

export interface AdminPool {
  /**
   * Runs fn inside one transaction as the operator role.
   *
   * The operator is declared for the record rather than for access: BYPASSRLS
   * already decided what is visible, so these settings grant nothing and are
   * read only by the audit helpers and by anyone reading a statement log.
   */
  withOperator<T>(operator: AdminOperator, fn: (db: Db) => Promise<T>): Promise<T>
  /**
   * Proves the connection actually bypasses row level security.
   *
   * Called on first use, and worth calling at start up so a deployment that
   * pointed this at the wrong role fails while somebody is watching rather than
   * on the first support request.
   */
  ensureBypass(): Promise<void>
  /** The raw client, for tests. */
  sql: postgres.Sql
  close(): Promise<void>
}

/** Roles this pool refuses to be, by name, however it was configured. */
const APPLICATION_ROLE = 'antifailure_app'

export function createAdminPool(options: AdminPoolOptions): AdminPool {
  const sql = postgres(options.url, {
    // Ten is the application's number and it is the wrong shape here. Operators
    // are a handful of people and their queries are expensive, so a small
    // ceiling is a feature: it bounds what a runaway operator screen can do to
    // the database the customers are also using.
    max: options.max ?? 4,
    connect_timeout: options.connectTimeoutSeconds ?? 10,
    idle_timeout: 30,
    // Dropped for the reason client.ts gives: a notice quotes the statement
    // that produced it, and statements here quote session hashes.
    onnotice: options.onNotice ?? (() => {}),
    prepare: false,
  })

  const root = drizzle(sql, { schema })
  const statementTimeout = (options.statementTimeoutSeconds ?? 30) * 1000

  // Memoized, not because the check is slow but because it must not become a
  // per request round trip that somebody later removes for being one.
  let verified: Promise<void> | null = null

  function ensureBypass(): Promise<void> {
    verified ??= (async () => {
      const rows = await sql<{ role: string; bypass: boolean }[]>`
        SELECT current_user AS role,
               (SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user) AS bypass`
      const row = rows[0]
      if (!row) {
        throw new Error('the operator pool could not read its own role')
      }
      if (row.role === APPLICATION_ROLE) {
        // The specific misconfiguration worth naming: someone pointed the
        // operator URL at the application credential, which would leave every
        // operator screen subject to the tenant policies and therefore empty.
        throw new Error(
          `the operator pool is connected as ${APPLICATION_ROLE}, which is the application's own role; it needs the separate operator credential`,
        )
      }
      if (!row.bypass) {
        throw new Error(
          `the operator pool is connected as ${row.role}, which does not hold BYPASSRLS, so every operator page would show an empty state that looks like a product with no customers`,
        )
      }
    })()
    return verified
  }

  return {
    sql,
    ensureBypass,
    async withOperator(operator, fn) {
      if (!operator.adminUserId) {
        // An operator scope with nobody in it is an unattributable action, and
        // the audit helpers would write one saying so. Refusing here names the
        // mistake at the call site instead.
        throw new Error('withOperator needs an operator; there is no anonymous admin scope')
      }
      await ensureBypass()
      return root.transaction(async (tx) => {
        await tx.execute(
          rawSql`SELECT set_config('statement_timeout', ${String(statementTimeout)}, true)`,
        )
        // Local to the transaction, the same as every setting in client.ts, so
        // a pooled connection is never handed on carrying an operator's name.
        await tx.execute(
          rawSql`SELECT set_config('antifailure.admin_user_id', ${operator.adminUserId}, true)`,
        )
        await tx.execute(
          rawSql`SELECT set_config('antifailure.admin_label', ${operator.label}, true)`,
        )
        return fn(tx as unknown as Db)
      })
    },
    async close() {
      await sql.end({ timeout: 5 })
    },
  }
}
