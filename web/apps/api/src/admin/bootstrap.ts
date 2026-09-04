// The first operator, and the password nothing could set.
//
// WHAT WAS MISSING, and it was missing completely rather than partly.
//
// `admin_users` rows are created by exactly one thing: `admin.operators.create`
// in admin/router.ts, which is an `adminProcedure` and so needs an operator
// session, which needs an `admin_users` row. On an empty table that is a closed
// loop and the operator portal is unreachable by anybody, forever.
//
// It is worse one level down. `admin.operators.create` writes the row with a
// NULL password and tells the caller, in its own words, "The account exists and
// cannot sign in. Set a password out of band before it is usable." There was no
// out of band. Nothing anywhere in this repository ever wrote
// `admin_users.password_hash`, so even an operator who existed could not be
// given a way in. Migration 0029's header says the root operator's first
// password "is written by the bootstrap command"; that command did not exist.
//
// So there are two commands here and they are deliberately two:
//
//   `bootstrap-operator`     creates the root operator, once, on an empty table.
//   `set-operator-password`  gives a password to an operator that already
//                            exists, which is what makes `operators.create`
//                            usable at all.
//
// WHY THE PASSWORD IS NOT AN ARGUMENT. A command line argument is visible in
// `ps` to every user on the machine, lands in the shell history file, and on a
// CI runner is printed by any step that echoes its own invocation. It comes
// from an environment variable or from standard input, and the reader below
// refuses to invent one.
//
// WHY THIS NEEDS A PRIVILEGED CONNECTION. `admin_users` is not reachable by the
// application role at all: migration 0029 grants it to nobody and the operator
// portal reads it as `antifailure_admin`, which holds BYPASSRLS. A bootstrap
// command that could run on the serving credential would mean the process
// answering public requests holds everything needed to mint an operator, which
// is the boundary the whole two table design exists to draw. So this takes the
// same connection string break-glass and create-org take, and proves it can
// write before it decides anything.

import { createPool, appendAdminAudit, sql, type Pool } from '@antifailure/db'
import { hashPassword } from './session.ts'
import { ADMIN_ROLES, type AdminRole } from './permissions.ts'

export class OperatorBootstrapRefused extends Error {}

/**
 * The shortest password this will write.
 *
 * Twelve rather than eight, and it is a floor rather than a policy: what is
 * behind this credential is every tenant on the instance, and the online
 * guessing rate is already held to one attempt per two seconds by the limit on
 * POST /v1/admin/signin. Twelve characters is what makes offline guessing
 * against a leaked hash hopeless rather than merely slow, given scrypt at
 * N = 2^15.
 *
 * There is deliberately no character class rule. A rule demanding a symbol
 * produces `Password1!` and refuses a passphrase, which is the wrong trade in
 * both directions.
 */
export const MIN_PASSWORD_LENGTH = 12

export interface BootstrapOperatorInput {
  /** A connection string row-level security does not apply to. */
  adminUrl: string
  email: string
  name: string
  /** Read from the environment or standard input by the caller, never from a
   *  command line argument. */
  password: string
  /** Reports what would change and writes nothing. */
  dryRun?: boolean
  /** Who ran it, for the audit entry. */
  operator?: string
  now?: Date
}

export interface BootstrapOperatorResult {
  adminUserId: string
  email: string
  name: string
  role: AdminRole
  /** False for a dry run, and only for a dry run. */
  applied: boolean
  auditSeq: number | null
}

/**
 * Creates the root operator on a control plane that has none.
 *
 * REFUSES WHEN ONE EXISTS, rather than resetting its password. The root
 * operator cannot be deleted, demoted or suspended by anybody including itself,
 * which migration 0029 enforces with triggers, so a command that quietly
 * rewrote its credential would be the one way to take the account over, and it
 * would be a way that runs on a connection string rather than on a session. A
 * forgotten root password is `set-operator-password`, which is a different verb
 * and says so.
 */
export async function bootstrapOperator(
  input: BootstrapOperatorInput,
): Promise<BootstrapOperatorResult> {
  const email = normaliseEmail(input.email)
  const name = input.name.trim()
  if (!name) throw new OperatorBootstrapRefused('An operator needs a name. Pass --name.')
  assertUsablePassword(input.password)

  const now = input.now ?? new Date()
  const pool = createPool({ url: input.adminUrl, max: 1, rowSecurity: false })
  try {
    await assertUnrestricted(pool)

    const roots = await pool.withoutTenant((db) =>
      db.execute<{ email: string }>(sql`SELECT email FROM admin_users WHERE is_root`),
    )
    if (roots[0]) {
      throw new OperatorBootstrapRefused(
        `This control plane already has a root operator: ${roots[0].email}. There is exactly ` +
          'one, ever, and it cannot be deleted or demoted, so this command will not take it ' +
          'over. If nobody can sign in as it, set its password with ' +
          '`set-operator-password --email ' + roots[0].email + '` instead.',
      )
    }

    const taken = await pool.withoutTenant((db) =>
      db.execute<{ id: string }>(sql`SELECT id FROM admin_users WHERE email = ${email}`),
    )
    if (taken[0]) {
      throw new OperatorBootstrapRefused(
        `An operator with the address ${email} already exists and is not the root operator. ` +
          'Give the root operator a different address, or set a password on the existing one ' +
          'with `set-operator-password`.',
      )
    }

    if (input.dryRun) {
      return {
        adminUserId: '',
        email,
        name,
        role: 'owner',
        applied: false,
        auditSeq: null,
      }
    }

    // Hashed here rather than in the statement. scrypt at this work factor takes
    // roughly a tenth of a second, and doing it inside the transaction would
    // hold the advisory lock the audit append takes for the length of it.
    const { hash, salt } = await hashPassword(input.password)

    const created = await pool.withoutTenant(async (db) => {
      const rows = await db.execute<{ id: string }>(sql`
        INSERT INTO admin_users
          (email, name, role, password_hash, password_salt, password_set_at, is_root,
           created_at, updated_at)
        VALUES (${email}, ${name}, 'owner', ${hash}, ${salt}, ${now.toISOString()}, true,
                ${now.toISOString()}, ${now.toISOString()})
        RETURNING id`)
      const adminUserId = rows[0]!.id

      // The first entry in the operator chain, and it says how the platform
      // acquired an operator at all. Somebody reading this history later has to
      // be able to tell an account created by another operator from the one
      // created by whoever held the database credential on the first night.
      //
      // `adminUserId` is the account created rather than an actor, because
      // there was no actor: nobody was signed in, and inventing one would be
      // the entry claiming a person did this through the portal.
      const entry = await appendAdminAudit(db, {
        adminUserId,
        actorLabel: input.operator ?? 'bootstrap',
        action: 'operator.bootstrapped',
        targetType: 'operator',
        targetId: email,
        origin: 'system',
        severity: 'high',
        detail: {
          role: 'owner',
          isRoot: true,
          reason: 'the first operator on this control plane',
          operator: input.operator ?? null,
        },
        occurredAt: now,
      })
      return { adminUserId, seq: entry.seq }
    })

    return {
      adminUserId: created.adminUserId,
      email,
      name,
      role: 'owner',
      applied: true,
      auditSeq: created.seq,
    }
  } finally {
    await pool.close()
  }
}

export interface SetOperatorPasswordInput {
  adminUrl: string
  email: string
  password: string
  dryRun?: boolean
  operator?: string
  now?: Date
}

export interface SetOperatorPasswordResult {
  adminUserId: string
  email: string
  role: AdminRole
  /** Whether this account could sign in BEFORE the change. False is the
   *  ordinary case after `operators.create`, and true means a working
   *  credential was replaced, which is worth a different sentence. */
  hadPassword: boolean
  applied: boolean
  auditSeq: number | null
}

/**
 * Gives an existing operator a password.
 *
 * This is what `admin.operators.create` has always assumed exists. It writes
 * the row with a NULL hash and says "Set a password out of band before it is
 * usable", and until now there was no out of band, so every operator anybody
 * created through the portal was an account that could not sign in.
 *
 * It does NOT create the account. Creating operators is the portal's job, under
 * an operator session, audited to a named actor; a command that could conjure
 * one from a connection string would make a leaked database credential into an
 * identity, which is the rule breakglass and bootstrap-org both keep.
 *
 * Every existing session belonging to that operator is revoked in the same
 * transaction. Changing a password because it leaked and leaving the sessions
 * it opened alive is a reset that resets nothing.
 */
export async function setOperatorPassword(
  input: SetOperatorPasswordInput,
): Promise<SetOperatorPasswordResult> {
  const email = normaliseEmail(input.email)
  assertUsablePassword(input.password)

  const now = input.now ?? new Date()
  const pool = createPool({ url: input.adminUrl, max: 1, rowSecurity: false })
  try {
    await assertUnrestricted(pool)

    const found = await pool.withoutTenant((db) =>
      db.execute<{ id: string; role: AdminRole; has_password: boolean; suspended_at: Date | null }>(sql`
        SELECT id, role, (password_hash IS NOT NULL) AS has_password, suspended_at
        FROM admin_users WHERE email = ${email}`),
    )
    const row = found[0]
    if (!row) {
      throw new OperatorBootstrapRefused(
        `No operator has the address ${email}. Create one in the operator portal, which records ` +
          'who created it, or use `bootstrap-operator` if this control plane has no operators ' +
          'at all yet.',
      )
    }
    if (!ADMIN_ROLES.includes(row.role)) {
      throw new OperatorBootstrapRefused(
        `The operator ${email} holds the role ${row.role}, which this build does not know. ` +
          'Refusing rather than writing a credential for an account whose permissions cannot ' +
          'be reasoned about.',
      )
    }
    if (row.suspended_at) {
      // Not refused. A suspended operator with a new password still cannot sign
      // in, because adminSignIn checks the suspension, so this changes nothing
      // about their access. Saying so stops somebody setting a password, being
      // unable to sign in, and concluding the command is broken.
      console.error(
        `note: ${email} is suspended, so this password will not let them sign in until an ` +
          'operator restores the account in the portal.',
      )
    }

    if (input.dryRun) {
      return {
        adminUserId: row.id,
        email,
        role: row.role,
        hadPassword: row.has_password,
        applied: false,
        auditSeq: null,
      }
    }

    const { hash, salt } = await hashPassword(input.password)

    const seq = await pool.withoutTenant(async (db) => {
      await db.execute(sql`
        UPDATE admin_users
        SET password_hash = ${hash}, password_salt = ${salt},
            password_set_at = ${now.toISOString()}, updated_at = ${now.toISOString()}
        WHERE id = ${row.id}::uuid`)

      // Every session that account already holds. A password set because the
      // old one leaked, that leaves the sessions the old one opened alive, is a
      // reset that resets nothing: an operator session lasts twelve hours and
      // reads every tenant's data for all of them.
      const revoked = await db.execute<{ id: string }>(sql`
        UPDATE admin_sessions SET revoked_at = ${now.toISOString()}
        WHERE admin_user_id = ${row.id}::uuid AND revoked_at IS NULL
          AND expires_at > ${now.toISOString()}
        RETURNING id`)

      const entry = await appendAdminAudit(db, {
        adminUserId: row.id,
        actorLabel: input.operator ?? 'bootstrap',
        action: 'operator.password_set',
        targetType: 'operator',
        targetId: email,
        origin: 'system',
        severity: 'high',
        detail: {
          replacedAPassword: row.has_password,
          sessionsRevoked: revoked.length,
          operator: input.operator ?? null,
        },
        occurredAt: now,
      })
      return entry.seq
    })

    return {
      adminUserId: row.id,
      email,
      role: row.role,
      hadPassword: row.has_password,
      applied: true,
      auditSeq: seq,
    }
  } finally {
    await pool.close()
  }
}

function normaliseEmail(value: string): string {
  const email = value.trim().toLowerCase()
  // The same shape the table's own CHECK constraint enforces, checked here so
  // the failure is a sentence rather than a constraint violation quoting a
  // regular expression.
  const at = email.indexOf('@')
  if (at <= 0 || at !== email.lastIndexOf('@') || !email.slice(at + 1).includes('.')) {
    throw new OperatorBootstrapRefused(
      `"${value}" is not an email address. An operator is identified by one, lowercased.`,
    )
  }
  return email
}

function assertUsablePassword(password: string): void {
  if (password.length < MIN_PASSWORD_LENGTH) {
    throw new OperatorBootstrapRefused(
      `That password is ${password.length} characters. It has to be at least ` +
        `${MIN_PASSWORD_LENGTH}, because what is behind this credential is every tenant on ` +
        'this instance. A passphrase is fine and is better than a short one with a symbol in it.',
    )
  }
  if (password.trim() !== password) {
    // Almost always a trailing newline that a heredoc or a copy and paste
    // added, and it would be part of the password forever with no way to see
    // it. Refusing is kinder than accepting a credential nobody can retype.
    throw new OperatorBootstrapRefused(
      'That password begins or ends with whitespace, which is almost always a stray newline ' +
        'from a paste or a heredoc. It would be part of the password and invisible in every ' +
        'attempt to type it again.',
    )
  }
}

/**
 * Proves the connection can actually reach admin_users before anything depends
 * on it.
 *
 * The same probe break-glass and create-org make, and for a sharper reason
 * here: migration 0029 grants `admin_users` to nobody at all, so the
 * application's own credential does not merely read zero rows, it cannot see
 * the table. Failing here, where the message can name the credential, is the
 * difference between a five second fix and reading the migration.
 */
async function assertUnrestricted(pool: Pool): Promise<void> {
  try {
    await pool.withoutTenant((db) => db.execute(sql`SELECT 1 FROM admin_users LIMIT 1`))
  } catch (error) {
    throw new OperatorBootstrapRefused(
      `This connection cannot read admin_users: ${reasonFor(error)}\n\n` +
        'The operator tables are deliberately unreachable by the role that serves requests: ' +
        'migration 0029 grants them to nobody, and the operator portal reads them as a separate ' +
        'role holding BYPASSRLS. Use a connection row-level security does not apply to: the ' +
        'cluster superuser, or the migration role. That is the same connection string the ' +
        'bootstrap job uses, not the one in AF_DATABASE_URL.',
    )
  }
}

/** The database's own words, not the driver's. */
function reasonFor(error: unknown): string {
  const cause = (error as { cause?: unknown }).cause
  if (cause instanceof Error && cause.message) return cause.message
  return error instanceof Error ? error.message : String(error)
}
