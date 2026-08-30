// The database connection, and the only way the application is allowed to use
// it.
//
// Every statement the application runs goes through withTenant. That is not a
// style preference. Row-level security keys off a session setting, and a
// setting is per connection, so a query that runs outside a transaction that
// set it either sees nothing or, on a pooled connection, sees whatever the
// previous borrower set. The second one is the dangerous case and it is silent,
// so the connection is never handed out raw: the only exported way to reach it
// carries the tenant with it.
//
// SET LOCAL rather than SET, so the setting is reverted when the transaction
// ends however it ends. A pooled connection returned with a tenant still set on
// it is a cross-tenant read waiting for the next borrower.

import postgres from 'postgres'
import { drizzle } from 'drizzle-orm/postgres-js'
import { sql as rawSql } from 'drizzle-orm'
import type { PostgresJsDatabase } from 'drizzle-orm/postgres-js'
import * as schema from './schema.ts'

export type Db = PostgresJsDatabase<typeof schema>

/** Who a transaction runs as. */
export interface Tenant {
  /** The organization every row must belong to. */
  orgId: string
  /** The acting user, when there is one. */
  userId?: string | null
}

export interface PoolOptions {
  url: string
  /** Connections per replica. PgBouncer sits in front in production, so this
   *  is the per-process ceiling rather than the database's. */
  max?: number
  /** Seconds a statement may run before the database cancels it. A control
   *  plane query that takes a minute has already failed the request. */
  statementTimeoutSeconds?: number
  /** Set by tests to keep the pool small and fail fast. */
  connectTimeoutSeconds?: number
  /**
   * Whether row-level security may apply to this connection. True everywhere
   * except the operator command, and it grants nothing: `row_security = off`
   * is not a privilege, and a role the policies already do not apply to is
   * unaffected by it.
   *
   * What it changes is the failure. A privileged tool that turns out to be
   * subject to the policies after all would UPDATE zero rows and report
   * success, which is precisely the failure 0007 was written about: a policy
   * does not raise on a statement that matches nothing. With this false,
   * Postgres raises instead, so a break-glass run by the wrong role says so
   * rather than looking like it worked.
   */
  rowSecurity?: boolean
  onNotice?: (notice: unknown) => void
}

/** What a transaction declares it is asking about, for the policies that key
 *  on a value the caller has to already hold. */
export interface UnscopedOptions {
  /** The hash of a session token, for resolving a cookie. */
  sessionHash?: Buffer
  /** The hash of an engine token, for resolving a bearer token. */
  engineTokenHash?: Buffer
  /** The hash of an email sign-in token, for issuing one and for redeeming
   *  the link it was sent in. */
  emailTokenHash?: Buffer
  /** GitHub numeric ids being upserted, so their rows can be read back. */
  githubIds?: number[]
  /** The user sign-in has just established, so their memberships can be read
   *  and written before any tenant is chosen. */
  signinUserId?: string
  /** The GitHub organization logins the user belongs to, so the installations
   *  for those organizations can be found. */
  githubLogins?: string[]
  /** The identifier in a single sign-on callback URL, for finding the
   *  connection an assertion or an authorization code belongs to. */
  ssoHandle?: string
  /** The Issuer of a provider-initiated assertion, which arrives with no URL
   *  to carry a handle. Separate from ssoHandle rather than sharing it: one
   *  setting compared against two columns means declaring either value
   *  silently matches on the other. */
  ssoEntityId?: string
  /** The email domain the sign-in page is asking about, so it can be told
   *  which identity provider to send the browser to. */
  ssoDomain?: string
  /** The state a browser is returning with, for the login it belongs to. */
  ssoState?: string
  /** The hash of a SCIM bearer token, for resolving a provisioning request. */
  scimTokenHash?: Buffer
  /** The hash of a device code, for a terminal polling its own login. It has
   *  no session and no tenant by definition, and it holds this. */
  deviceCodeHash?: Buffer
  /** The short code a person typed off a terminal, for the browser approving
   *  it. The row has no organization until that approval happens, so it cannot
   *  be reached by tenant. */
  deviceUserCode?: string
}

/** What a verified webhook delivery declares it is about. */
export interface GitHubAccountOptions {
  /** The account login out of a delivery whose signature has been checked. */
  login: string
}

export interface Pool {
  /** Runs fn inside a transaction scoped to one tenant. */
  withTenant<T>(tenant: Tenant, fn: (db: Db) => Promise<T>, opts?: UnscopedOptions): Promise<T>
  /** Runs fn with no tenant set, for the statements that happen before one is
   *  known: resolving a session cookie, resolving an engine token, completing
   *  an OAuth exchange, and the four single sign-on and provisioning lookups
   *  that determine which organization a request concerns. Each is covered by
   *  a policy that does not depend on the tenant, and there are no others.
   *
   *  All but the OAuth handshake are covered by policies keyed on a value the
   *  caller declares here. That is what makes them safe: the connection can
   *  reach the one row the value names, and nothing else. Declaring a value it
   *  did not receive from a client returns nothing. */
  withoutTenant<T>(fn: (db: Db) => Promise<T>, opts?: UnscopedOptions): Promise<T>
  /**
   * Runs fn scoped to one GitHub account, for a webhook delivery.
   *
   * A delivery has no tenant: it may be the thing that CREATES one. So it
   * cannot use withTenant, and using withoutTenant would leave it able to reach
   * every row in the database. What it declares instead is the account the
   * verified payload named, and the policies in 0013 confine it to that
   * account's organization, installation and repositories.
   *
   * The login is lower-cased here rather than at each call site, because the
   * policies compare lower-cased and one caller forgetting would produce
   * statements that match nothing and raise nothing.
   */
  withGitHubAccount<T>(login: string, fn: (db: Db) => Promise<T>): Promise<T>
  /** The raw client, for migrations and tests only. */
  sql: postgres.Sql
  close(): Promise<void>
}

export function createPool(options: PoolOptions): Pool {
  const sql = postgres(options.url, {
    max: options.max ?? 10,
    connect_timeout: options.connectTimeoutSeconds ?? 10,
    idle_timeout: 30,
    // Notices are dropped rather than logged, because a notice quotes the
    // statement that produced it and statements here carry session tokens.
    onnotice: options.onNotice ?? (() => {}),
    prepare: false, // PgBouncer in transaction mode cannot carry prepared statements.
  })

  const root = drizzle(sql, { schema })
  const statementTimeout = (options.statementTimeoutSeconds ?? 15) * 1000

  // Settings are applied with set_config rather than SET LOCAL because
  // set_config takes the name and the value as bind parameters. SET LOCAL
  // would need both spliced into the statement text, and one of these values
  // is an identifier that arrived in a request.
  //
  // The third argument, true, is what makes it local to the transaction. A
  // setting left on a pooled connection is read by whoever borrows it next,
  // which is a cross-tenant read that no test would ever catch because it
  // depends on pool scheduling.
  async function scoped<T>(
    settings: Record<string, string>,
    fn: (db: Db) => Promise<T>,
  ): Promise<T> {
    return root.transaction(async (tx) => {
      await tx.execute(rawSql`SELECT set_config('statement_timeout', ${String(statementTimeout)}, true)`)
      if (options.rowSecurity === false) {
        await tx.execute(rawSql`SELECT set_config('row_security', 'off', true)`)
      }
      for (const [key, value] of Object.entries(settings)) {
        await tx.execute(rawSql`SELECT set_config(${key}, ${value}, true)`)
      }
      return fn(tx as unknown as Db)
    })
  }

  return {
    sql,
    withTenant(tenant, fn, opts) {
      if (!tenant.orgId) {
        // An empty setting would make every policy deny, which reads as an
        // empty database rather than as a bug. Failing here names the mistake.
        throw new Error('withTenant needs an organization; use withoutTenant if there is none')
      }
      return scoped(
        {
          'antifailure.org_id': tenant.orgId,
          'antifailure.user_id': tenant.userId ?? '',
          // Cleared rather than left alone. Every setting here is transaction
          // local and reverts on its own; clearing it makes the invariant
          // checkable by reading this function instead of trusting callers.
          'antifailure.session_hash': '',
          'antifailure.engine_token_hash': '',
          'antifailure.email_token_hash': '',
          'antifailure.github_ids': (opts?.githubIds ?? []).join(','),
          'antifailure.signin_user_id': opts?.signinUserId ?? '',
          'antifailure.github_logins': (opts?.githubLogins ?? []).join(','),
          // Cleared for the same reason as the two above. Every one of these
          // is a policy that returns a row without consulting the tenant, so a
          // transaction that has a tenant has no business declaring one.
          'antifailure.sso_handle': '',
          'antifailure.sso_entity_id': '',
          'antifailure.sso_domain': '',
          'antifailure.sso_state': '',
          'antifailure.scim_token_hash': '',
          'antifailure.device_code_hash': '',
          'antifailure.device_user_code': '',
          'antifailure.github_account': '',
        },
        fn,
      )
    },
    withoutTenant(fn, opts) {
      return scoped(
        {
          'antifailure.org_id': '',
          'antifailure.user_id': '',
          'antifailure.session_hash': opts?.sessionHash ? opts.sessionHash.toString('hex') : '',
          'antifailure.engine_token_hash': opts?.engineTokenHash
            ? opts.engineTokenHash.toString('hex')
            : '',
          'antifailure.email_token_hash': opts?.emailTokenHash
            ? opts.emailTokenHash.toString('hex')
            : '',
          'antifailure.github_ids': (opts?.githubIds ?? []).join(','),
          'antifailure.signin_user_id': opts?.signinUserId ?? '',
          'antifailure.github_logins': (opts?.githubLogins ?? []).join(','),
          'antifailure.sso_handle': opts?.ssoHandle ?? '',
          'antifailure.sso_entity_id': opts?.ssoEntityId ?? '',
          'antifailure.sso_domain': opts?.ssoDomain ?? '',
          'antifailure.sso_state': opts?.ssoState ?? '',
          'antifailure.scim_token_hash': opts?.scimTokenHash
            ? opts.scimTokenHash.toString('hex')
            : '',
          'antifailure.device_code_hash': opts?.deviceCodeHash
            ? opts.deviceCodeHash.toString('hex')
            : '',
          'antifailure.device_user_code': opts?.deviceUserCode ?? '',
          'antifailure.github_account': '',
        },
        fn,
      )
    },
    withGitHubAccount(login, fn) {
      const account = login.trim().toLowerCase()
      if (!account) {
        // An empty setting makes every policy deny, which reads as an empty
        // database rather than as a bug. Naming it here is the difference
        // between a webhook that silently writes nothing and one that says why.
        throw new Error('withGitHubAccount needs an account login')
      }
      return scoped(
        {
          'antifailure.org_id': '',
          'antifailure.user_id': '',
          'antifailure.session_hash': '',
          'antifailure.engine_token_hash': '',
          'antifailure.github_ids': '',
          'antifailure.signin_user_id': '',
          'antifailure.github_logins': '',
          'antifailure.device_code_hash': '',
          'antifailure.device_user_code': '',
          'antifailure.github_account': account,
        },
        fn,
      )
    },
    async close() {
      await sql.end({ timeout: 5 })
    },
  }
}
