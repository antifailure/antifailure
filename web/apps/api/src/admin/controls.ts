// The switches an operator reaches for during an incident.
//
// The rule this file is built around: A CONTROL THAT IS DECLARED HERE IS
// ENFORCED SOMEWHERE. Not "has a row", not "has a button", not "is written to
// the database" - refused, by a named function, on a path a test drives. A
// maintenance mode nothing checks is worse than no maintenance mode, because
// the operator who engaged it stops looking for the problem.
//
// So every entry carries `enforcedBy`, naming the function that refuses, and
// there is a test asserting each one actually refuses. A switch that cannot be
// wired does not get an entry: it is reported as not built rather than shipped
// as a button that does nothing.
//
// Reversal is part of the definition, not an afterthought. Every control here
// is released by setting engaged_at back to NULL through the same route that
// set it, and the console renders release beside engage rather than behind a
// second screen. An operator who cannot find the way back has an outage this
// product caused.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'

export const CONTROL_NAMES = [
  'maintenance',
  'signups',
  'new_runs',
] as const

export type ControlName = (typeof CONTROL_NAMES)[number]

export interface ControlDefinition {
  name: ControlName
  /** What engaging it does, in the words shown above the confirmation. */
  title: string
  /** Exactly what stops working. Read aloud by the operator before confirming,
   *  so it says what is refused AND what keeps working. */
  effect: string
  /**
   * Where the refusal is, as `path/from/src:symbol`.
   *
   * The file as well as the symbol, because a bare function name proves only
   * that SOME file declares one, and the enforcement moving to a module
   * nothing calls would still satisfy that. A test opens this exact file and
   * greps it for this exact symbol, so a control cannot claim an enforcement
   * it does not have and a rename fails rather than going quiet.
   */
  enforcedBy: string
  /** How to undo it, in one sentence, shown next to the control at all times. */
  release: string
}

/**
 * Every switch, and what each one actually does.
 *
 * Ordered by blast radius, widest first, because that is the order an operator
 * escalates through and because putting the widest one last invites reaching
 * for it by scrolling.
 */
export const CONTROLS: Record<ControlName, ControlDefinition> = {
  maintenance: {
    name: 'maintenance',
    title: 'Maintenance mode',
    effect:
      'Every request that changes something is refused with 503 for every organization on this ' +
      'installation. Reads keep working, engines can still send events so nothing is lost, and ' +
      'the admin portal keeps working so this can be turned off again.',
    enforcedBy: 'server.ts:refuseDuringMaintenance',
    release: 'Release maintenance from this same page. Nothing else has to be restarted.',
  },
  signups: {
    name: 'signups',
    title: 'New sign-ups',
    effect:
      'Nobody new can create an account. Everybody who already has one signs in and works ' +
      'normally, and invitations to existing organizations are unaffected.',
    enforcedBy: 'auth/signin.ts:refuseNewAccounts',
    release: 'Allow sign-ups again from this page. It takes effect on the next attempt.',
  },
  new_runs: {
    name: 'new_runs',
    title: 'New runs and environments',
    effect:
      'No organization can bring up an environment or start an agent or load run. Environments ' +
      'that are already running keep running and stay readable, and nothing is torn down.',
    enforcedBy: 'routers/dispatch.ts:refuseWhileFrozen',
    release: 'Allow runs again from this page. Queued work is not lost; nothing was queued.',
  },
}

export interface ControlState {
  name: ControlName
  definition: ControlDefinition
  engaged: boolean
  engagedAt: Date | string | null
  reason: string | null
  engagedBy: string | null
  updatedAt: Date | string | null
}

/**
 * Every control and whether it is engaged.
 *
 * Returns an entry for every control in the catalog, including the ones with
 * no row, so the page renders the full set on a fresh installation instead of
 * an empty table. A control with no row has never been touched.
 */
export async function controlStates(db: Db): Promise<ControlState[]> {
  const rows = await db.execute<{
    name: string
    engaged_at: Date | string | null
    reason: string | null
    engaged_by: string | null
    updated_at: Date | string
  }>(sql`SELECT name, engaged_at, reason, engaged_by, updated_at FROM platform_controls`)
  const by = new Map(rows.map((r) => [r.name, r]))
  return CONTROL_NAMES.map((name) => {
    const row = by.get(name)
    return {
      name,
      definition: CONTROLS[name],
      engaged: row?.engaged_at != null,
      engagedAt: row?.engaged_at ?? null,
      reason: row?.reason ?? null,
      engagedBy: row?.engaged_by ?? null,
      updatedAt: row?.updated_at ?? null,
    }
  })
}

/**
 * Whether one control is engaged, and why.
 *
 * The hot path. Every enforcement point calls this, so it is one indexed read
 * on a primary key against a table with at most three rows. Returning the
 * reason rather than a boolean is what lets a refusal say something useful:
 * "this installation is paused" with no reason is a message that generates a
 * support ticket.
 */
export async function engagedReason(db: Db, name: ControlName): Promise<string | null> {
  const rows = await db.execute<{ reason: string | null }>(sql`
    SELECT reason FROM platform_controls
    WHERE name = ${name} AND engaged_at IS NOT NULL`)
  if (rows.length === 0) return null
  return rows[0]!.reason ?? 'no reason was recorded'
}

/**
 * Engages or releases one control.
 *
 * Needs the operator connection, which is `ctx.adminDb` on an admin route and
 * connects as `antifailure_admin`. The application role is granted SELECT on
 * this table and nothing else, so on any other connection the INSERT raises
 * `permission denied` rather than writing.
 *
 * The empty-RETURNING guard below is kept anyway, and it is not redundant. A
 * missing privilege raises, but a statement that matches no rows does not: it
 * reports success. A switch that silently did not engage is the worst outcome
 * this file can produce, so the one case that could ever be quiet is made
 * loud.
 */
export async function setControl(
  db: Db,
  now: Date,
  name: ControlName,
  engaged: boolean,
  actor: { label: string },
  reason: string | null,
): Promise<ControlState> {
  if (engaged && (reason === null || reason.trim() === '')) {
    // Refused here as well as in the route's input schema. A switch that stops
    // an installation with no reason recorded is one the next person on call
    // cannot safely release, and the route is not the only caller this
    // function will ever have.
    throw new Error(`engaging ${name} needs a reason`)
  }
  const rows = await db.execute<{
    name: string
    engaged_at: Date | string | null
    reason: string | null
    engaged_by: string | null
    updated_at: Date | string
  }>(sql`
    INSERT INTO platform_controls (name, engaged_at, reason, engaged_by, updated_at)
    VALUES (${name}, ${engaged ? now.toISOString() : null}::timestamptz,
            ${engaged ? reason : null}, ${actor.label}, ${now.toISOString()}::timestamptz)
    ON CONFLICT (name) DO UPDATE
      SET engaged_at = EXCLUDED.engaged_at,
          reason = EXCLUDED.reason,
          engaged_by = EXCLUDED.engaged_by,
          updated_at = EXCLUDED.updated_at
    RETURNING name, engaged_at, reason, engaged_by, updated_at`)
  const row = rows[0]
  if (!row) {
    throw new Error(
      `${name} was not changed. The statement matched no row and wrote nothing, which reports ` +
        `success rather than raising. Run this on the operator connection, ctx.adminDb, which ` +
        `connects as antifailure_admin; the application role is granted SELECT here and nothing ` +
        `else.`,
    )
  }
  return {
    name,
    definition: CONTROLS[name],
    engaged: row.engaged_at != null,
    engagedAt: row.engaged_at,
    reason: row.reason,
    engagedBy: row.engaged_by,
    updatedAt: row.updated_at,
  }
}
