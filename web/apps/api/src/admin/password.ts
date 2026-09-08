// What a password has to clear, and how the current one is proved, for the two
// things that write `admin_users.password_hash`.
//
// WHY THIS FILE EXISTS AT ALL. Until now there was exactly one writer, the
// `set-operator-password` command in bootstrap.ts, and its rules could live
// beside it. There are now two: that command, which runs on a privileged
// connection string, and `admin.operators.setPassword`, which runs under an
// operator session in the portal. A floor enforced in one of two writers is a
// floor with a way around it, and the way around it would be the one anybody
// can reach from a browser. So the rules moved here and both callers ask.
//
// WHY IT REPORTS A SENTENCE RATHER THAN THROWING. The two callers owe their
// readers different error types: the command raises `OperatorBootstrapRefused`
// and prints, the route raises a `TRPCError` that becomes an HTTP status. A
// shared function that threw would force one of them to catch a foreign error
// class and re-wrap it, and the usual result of that is a caught error whose
// message is replaced by a generic one. The refusal text is the valuable part,
// so it is what crosses the boundary.
//
// NOTHING HERE EVER LOGS, RETURNS OR EMBEDS A PASSWORD. The refusals name the
// LENGTH and the SHAPE of what was rejected and never the value, because a
// refusal that quotes the password writes it into every log that catches it.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'
import { passwordMatches } from './session.ts'

/**
 * The shortest password either writer will accept.
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

/**
 * The longest one, which is a denial of service bound and not an opinion.
 *
 * scrypt at this work factor is deliberately expensive, and it is expensive in
 * MEMORY as much as in time. An unbounded field is an invitation to spend the
 * process's memory budget on one request, and the route that takes this input
 * is reachable by any operator holding admin.operators.write. Nothing a human
 * types or a generator produces comes close to this.
 */
export const MAX_PASSWORD_LENGTH = 1024

/**
 * Why this password cannot be written, or null when it can.
 *
 * Null is the ACCEPTING answer, which is the direction worth stating out loud
 * because it is the one a mistake inverts: a caller that treats a truthy return
 * as "fine" accepts every password this function rejects and rejects every one
 * it accepts. Both writers have a test that watches a short password get
 * refused, which is what would catch that.
 */
export function passwordRefusal(password: string): string | null {
  if (password.length < MIN_PASSWORD_LENGTH) {
    return (
      `That password is ${password.length} characters. It has to be at least ` +
      `${MIN_PASSWORD_LENGTH}, because what is behind this credential is every tenant on ` +
      'this instance. A passphrase is fine and is better than a short one with a symbol in it.'
    )
  }
  if (password.length > MAX_PASSWORD_LENGTH) {
    return (
      `That password is ${password.length} characters, and the limit is ${MAX_PASSWORD_LENGTH}. ` +
      'The bound is about the cost of hashing it rather than about the password being too good.'
    )
  }
  if (password.trim() !== password) {
    // Almost always a trailing newline that a heredoc or a copy and paste
    // added, and it would be part of the password forever with no way to see
    // it. Refusing is kinder than accepting a credential nobody can retype.
    return (
      'That password begins or ends with whitespace, which is almost always a stray newline ' +
      'from a paste or a heredoc. It would be part of the password and invisible in every ' +
      'attempt to type it again.'
    )
  }
  return null
}

/**
 * Whether `password` is the password that operator currently has.
 *
 * THE READ OF `password_hash` LIVES HERE RATHER THAN IN A ROUTE, and that is
 * the reason this function exists instead of two lines at the call site.
 * router.ts states a rule at SAFE_COLUMNS: every query in it names its columns
 * from a list, and none of those lists names a secret. The one caller that
 * genuinely needs the stored hash is the self check on setPassword, and letting
 * it name the column would put `password_hash` into the file whose stated
 * invariant is that the column appears nowhere in it. So the column is named
 * once, here, in a function whose only possible return value is a boolean: the
 * bytes cannot escape it even by accident.
 *
 * IT TAKES THE SCOPE RATHER THAN A HANDLE, so that the scrypt runs with no
 * transaction open. `adminDb` is a transaction, and the operator pool is small;
 * scrypt at N = 2^15 takes roughly a tenth of a second, and doing it between
 * two statements would hold a pooled connection and its snapshot open for the
 * length of it, on the credential that holds BYPASSRLS. Passing `c.adminDb`
 * itself lets the read commit and close before any work starts.
 *
 * FALSE FOR AN UNPROVISIONED OPERATOR, at the same cost as a wrong password,
 * because `passwordMatches` does the scrypt work before it looks at the stored
 * hash. An early return on NULL would make "has never been given a password"
 * distinguishable from "guessed wrong" by timing alone.
 */
export async function operatorPasswordMatches(
  scope: <T>(fn: (db: Db) => Promise<T>) => Promise<T>,
  adminUserId: string,
  password: string,
): Promise<boolean> {
  const stored = await scope(async (db) => {
    const rows = await db.execute<{ password_hash: Buffer | null; password_salt: Buffer | null }>(
      sql`SELECT password_hash, password_salt FROM admin_users WHERE id = ${adminUserId}::uuid`,
    )
    return rows[0] ?? null
  })
  if (!stored) return false
  return passwordMatches(password, { hash: stored.password_hash, salt: stored.password_salt })
}
