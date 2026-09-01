// Durable commands, and the reason a teardown needed one.
//
// `environments.teardown` was an UPDATE and a comment saying the engine reads
// the row and does the removing. Nothing reads the row. There is no query
// anywhere in the engine against `environments`, no poller, and no endpoint
// that would serve one, so the containers, the database branch, the proxy and
// the DNS record all stayed exactly where they were while the console said the
// environment was gone and the bill kept growing. A request that produces
// nothing but a column change is not a teardown, it is a note to nobody.
//
// A command is the note to somebody. It is durable, so a control plane that
// restarts does not forget it. It is leased, so two engines polling the same
// organization do not both act. It is acknowledged, so "requested" and "done"
// are different facts rather than one optimistic column. And it expires, so a
// request nothing ever picked up says that rather than sitting pending forever
// and reading as in progress.
//
// TWO WAYS A COMMAND REACHES A RUNTIME, AND WHY BOTH EXIST.
//
// The control plane cannot open a connection to a customer's runtime, and it
// must not hold the credentials that would let it. So it has exactly two
// levers, and a command uses whichever is available:
//
//   dispatch  ask GitHub to run the customer's own workflow, which is what
//             `environments.create` already does. This works today: the
//             workflow's `down` case runs `af down`.
//   claim     answer an engine that asks. An engine holding a token calls
//             POST /v1/commands/claim and takes a lease.
//
// The dispatch is the fast path and the claim is the one that survives the
// dispatch failing: no App installed, Actions disabled, the workflow file
// missing, GitHub down. A command whose dispatch was refused stays pending and
// is claimable, and the refusal is recorded on it rather than thrown away.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'

export type CommandKind = 'environment.teardown' | 'workload.cancel'

export type CommandState =
  | 'pending'
  | 'claimed'
  | 'acknowledged'
  | 'failed'
  | 'expired'
  | 'superseded'

export interface CommandRow extends Record<string, unknown> {
  id: string
  kind: CommandKind
  state: CommandState
  environment_id: string | null
  workload_run_id: string | null
  attempts: number
  requested_at: Date | string
  expires_at: Date | string
  acknowledged_at: Date | string | null
  outcome: string | null
  detail: string | null
}

/**
 * How long a command waits for somebody to carry it out.
 *
 * Six hours, which is above the thirty minute job timeout the example workflow
 * declares and above a queue that is backed up, and far below "forever". The
 * number matters because it is the boundary between a teardown that is late and
 * a teardown that never happened, and only one of those is worth telling
 * somebody about.
 */
export const COMMAND_TTL_MS = 6 * 60 * 60 * 1000

/**
 * How long a claim holds a command before another engine may take it.
 *
 * Fifteen minutes. Long enough for `af down` to replay a journal against a
 * provider that is retrying, short enough that an engine killed mid-teardown
 * does not strand the command for the rest of its life.
 */
export const COMMAND_LEASE_MS = 15 * 60 * 1000

export interface CreateCommand {
  orgId: string
  kind: CommandKind
  environmentId?: string | null
  workloadRunId?: string | null
  payload?: Record<string, unknown>
  requestedBy?: string | null
  now: Date
}

/**
 * Writes a command, or returns the live one that is already asking for this.
 *
 * The second press of a button is the ordinary case and it must join the first
 * rather than queue a second teardown. Two partial unique indexes decide that
 * in the database, so two requests landing together cannot both read nothing
 * and both write; the loser's insert conflicts and this returns the winner's
 * row.
 *
 * ON CONFLICT cannot name a partial index's predicate without repeating it, so
 * the conflict target is spelled out here exactly as the index is. A mismatch
 * would make the insert raise rather than return, which is loud rather than
 * silent, and the test that presses twice is what watches for it.
 */
export async function createCommand(db: Db, input: CreateCommand): Promise<CommandRow> {
  const expiresAt = new Date(input.now.getTime() + COMMAND_TTL_MS).toISOString()
  const payload = JSON.stringify(input.payload ?? {})

  const inserted =
    input.kind === 'environment.teardown'
      ? await db.execute<CommandRow>(sql`
          INSERT INTO runtime_commands (org_id, kind, environment_id, payload, requested_by, expires_at)
          VALUES (${input.orgId}, 'environment.teardown', ${input.environmentId},
                  ${payload}::jsonb, ${input.requestedBy ?? null}, ${expiresAt}::timestamptz)
          ON CONFLICT (environment_id)
            WHERE kind = 'environment.teardown' AND state IN ('pending', 'claimed')
            DO NOTHING
          RETURNING ${commandColumns}`)
      : await db.execute<CommandRow>(sql`
          INSERT INTO runtime_commands (org_id, kind, workload_run_id, payload, requested_by, expires_at)
          VALUES (${input.orgId}, 'workload.cancel', ${input.workloadRunId},
                  ${payload}::jsonb, ${input.requestedBy ?? null}, ${expiresAt}::timestamptz)
          ON CONFLICT (workload_run_id)
            WHERE kind = 'workload.cancel' AND state IN ('pending', 'claimed')
            DO NOTHING
          RETURNING ${commandColumns}`)

  if (inserted[0]) return inserted[0]

  const existing = await db.execute<CommandRow>(sql`
    SELECT ${commandColumns} FROM runtime_commands
    WHERE kind = ${input.kind}::runtime_command_kind
      AND state IN ('pending', 'claimed')
      AND (${input.environmentId ?? null}::uuid IS NULL OR environment_id = ${input.environmentId ?? null}::uuid)
      AND (${input.workloadRunId ?? null}::uuid IS NULL OR workload_run_id = ${input.workloadRunId ?? null}::uuid)`)
  const row = existing[0]
  if (!row) {
    // The insert conflicted and the row it conflicted with is gone, which means
    // something settled it between the two statements. Thrown rather than
    // returned as a null the caller would have to handle: a command that
    // neither exists nor can be created means the tenant scoping is wrong, and
    // that is a bug to see rather than a request to drop.
    throw new Error('a runtime command could neither be created nor read back')
  }
  return row
}

/** Records that a command was handed to a runtime, and how. */
export async function recordDispatch(
  db: Db,
  commandId: string,
  detail: string,
): Promise<void> {
  await db.execute(sql`
    UPDATE runtime_commands SET detail = ${detail}, updated_at = now()
    WHERE id = ${commandId} AND state IN ('pending', 'claimed')`)
}

export interface ClaimedCommand {
  id: string
  kind: CommandKind
  envId: string | null
  workloadRunId: string | null
  payload: Record<string, unknown>
  attempts: number
  leaseExpiresAt: string
}

/**
 * Hands an engine the commands it should carry out, with a lease.
 *
 * FOR UPDATE SKIP LOCKED rather than a read and then an update. Two engines
 * polling at the same instant would otherwise both read the same pending row
 * and both take it, and a teardown carried out twice is mostly harmless while a
 * cancel carried out twice is not.
 *
 * A claim whose lease has run out is claimable again, and `attempts` counts up.
 * That is what stops an engine that dies mid-teardown from stranding the
 * command, and it is why the acknowledgement rather than the claim is what
 * marks anything done.
 */
export async function claimCommands(
  db: Db,
  input: { orgId: string; envId?: string | null; holder: string; limit: number; now: Date },
): Promise<ClaimedCommand[]> {
  const now = input.now.toISOString()
  const leaseUntil = new Date(input.now.getTime() + COMMAND_LEASE_MS).toISOString()
  const envFilter = input.envId ?? null

  const rows = await db.execute<{
    id: string
    kind: CommandKind
    env_id: string | null
    workload_run_id: string | null
    payload: Record<string, unknown>
    attempts: number
    lease_expires_at: string
  }>(sql`
    WITH claimable AS (
      SELECT c.id FROM runtime_commands c
      WHERE (
              c.state = 'pending'
              OR (c.state = 'claimed' AND c.lease_expires_at < ${now}::timestamptz)
            )
        AND c.expires_at > ${now}::timestamptz
        -- The environment is named the way an engine knows it, by env_id,
        -- because an engine on a runner holds that string and has never seen
        -- this database's primary keys. EXISTS rather than a join so that the
        -- row lock below applies to runtime_commands alone: FOR UPDATE cannot
        -- lock the nullable side of an outer join, and a command may name
        -- either an environment or a run.
        AND (
          ${envFilter}::text IS NULL
          OR EXISTS (
            SELECT 1 FROM environments e
            WHERE e.id = c.environment_id AND e.env_id = ${envFilter}::text
          )
          OR EXISTS (
            SELECT 1 FROM workload_runs wr
            JOIN environments we ON we.id = wr.environment_id
            WHERE wr.id = c.workload_run_id AND we.env_id = ${envFilter}::text
          )
        )
      ORDER BY c.requested_at
      LIMIT ${input.limit}
      FOR UPDATE SKIP LOCKED
    )
    UPDATE runtime_commands c SET
      state = 'claimed',
      lease_holder = ${input.holder},
      lease_expires_at = ${leaseUntil}::timestamptz,
      claimed_at = COALESCE(c.claimed_at, ${now}::timestamptz),
      attempts = c.attempts + 1,
      updated_at = ${now}::timestamptz
    FROM claimable
    WHERE c.id = claimable.id
    RETURNING c.id, c.kind::text AS kind, c.payload, c.attempts,
              to_char(c.lease_expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS lease_expires_at,
              (SELECT e2.env_id FROM environments e2 WHERE e2.id = c.environment_id) AS env_id,
              c.workload_run_id`)

  return rows.map((r) => ({
    id: r.id,
    kind: r.kind,
    envId: r.env_id,
    workloadRunId: r.workload_run_id,
    payload: r.payload ?? {},
    attempts: Number(r.attempts),
    leaseExpiresAt: r.lease_expires_at,
  }))
}

export type CommandOutcome = 'done' | 'failed'

/**
 * The runtime saying what happened.
 *
 * Only a claimed command may be acknowledged, and only by the holder of its
 * lease. Without the holder check a second engine that took the command after
 * the first one's lease ran out could have its answer overwritten by the first
 * one waking up, which is the ordering that turns "the teardown failed" into
 * "the teardown succeeded" an hour late.
 *
 * Returns false when nothing was acknowledged, which the endpoint turns into a
 * refusal naming the reason rather than a silent 200. An acknowledgement that
 * matched no row is exactly the failure this whole file exists to end.
 */
export async function acknowledgeCommand(
  db: Db,
  input: {
    commandId: string
    holder: string
    outcome: CommandOutcome
    detail?: string | null
    now: Date
  },
): Promise<boolean> {
  const rows = await db.execute<{ id: string }>(sql`
    UPDATE runtime_commands SET
      state = ${input.outcome === 'done' ? 'acknowledged' : 'failed'}::runtime_command_state,
      outcome = ${input.outcome},
      detail = ${input.detail ?? null},
      acknowledged_at = ${input.now.toISOString()}::timestamptz,
      lease_holder = NULL,
      lease_expires_at = NULL,
      updated_at = ${input.now.toISOString()}::timestamptz
    WHERE id = ${input.commandId}
      AND state = 'claimed'
      AND lease_holder = ${input.holder}
    RETURNING id`)
  return rows.length > 0
}

/**
 * Acknowledges the live teardown for an environment, from the engine's own
 * report that the environment is gone.
 *
 * This is the acknowledgement that works today, and it is the reason the
 * teardown loop is closed rather than half built. `af down` emits
 * `env.destroyed`, the control plane sink maps it to `environment.torn_down`,
 * ingestion projects it, and this is what turns the command from "requested"
 * into "the runtime says it is gone". No engine change, no new client, no
 * polling: the event the engine already sends is the receipt.
 *
 * Not conditioned on the command being claimed. The engine that ran the
 * teardown reached it through a workflow dispatch and never claimed anything,
 * so requiring a claim here would leave every command that took the fast path
 * pending forever, which is the defect wearing different clothes.
 */
export async function acknowledgeTeardownFromEvent(
  db: Db,
  input: { environmentId: string; now: Date; detail: string },
): Promise<number> {
  const rows = await db.execute<{ id: string }>(sql`
    UPDATE runtime_commands SET
      state = 'acknowledged',
      outcome = 'done',
      detail = ${input.detail},
      acknowledged_at = ${input.now.toISOString()}::timestamptz,
      lease_holder = NULL,
      lease_expires_at = NULL,
      updated_at = ${input.now.toISOString()}::timestamptz
    WHERE kind = 'environment.teardown'
      AND environment_id = ${input.environmentId}
      AND state IN ('pending', 'claimed')
    RETURNING id`)
  return rows.length
}

/**
 * Marks commands nobody carried out.
 *
 * Runs inside the caller's tenant transaction, which is what makes it work at
 * all: every policy on this table keys on current_org(), so a sweep with no
 * tenant set matches nothing and reports success, which is precisely how the
 * device authorization sweeper deleted zero rows for the life of the process.
 *
 * Called from every route that reads or writes a command, so an organization
 * that uses the product resolves its own expiries. See workloads/store.ts for
 * why that is the shape rather than one global sweep.
 */
export async function expireOverdueCommands(
  db: Db,
  input: { now: Date },
): Promise<number> {
  const rows = await db.execute<{ id: string }>(sql`
    UPDATE runtime_commands SET
      state = 'expired',
      -- Appended rather than replacing what is there. The detail already says
      -- how the command reached a runtime, or why it could not, and that is
      -- half of what somebody debugging a teardown needs; the other half is
      -- that nothing ever came back. COALESCE would have kept the first and
      -- lost the second on every command whose dispatch succeeded, which is
      -- the common case and the one worth explaining.
      detail = CASE WHEN detail IS NULL OR detail = ''
                    THEN 'no runtime confirmed this before it expired'
                    ELSE detail || '; no runtime confirmed this before it expired' END,
      lease_holder = NULL,
      lease_expires_at = NULL,
      updated_at = ${input.now.toISOString()}::timestamptz
    WHERE state IN ('pending', 'claimed')
      AND expires_at <= ${input.now.toISOString()}::timestamptz
    RETURNING id`)
  return rows.length
}

/** The live command for one environment, for a console that has to say whether
 *  a teardown was actually confirmed. */
export async function teardownFor(
  db: Db,
  environmentId: string,
): Promise<CommandRow | null> {
  const rows = await db.execute<CommandRow>(sql`
    SELECT ${commandColumns} FROM runtime_commands
    WHERE kind = 'environment.teardown' AND environment_id = ${environmentId}
    ORDER BY requested_at DESC LIMIT 1`)
  return rows[0] ?? null
}

// The columns every read of this table returns. Written once so that two reads
// cannot disagree about the shape they hand the console.
const commandColumns = sql`
  id, kind::text AS kind, state::text AS state, environment_id, workload_run_id,
  attempts, requested_at, expires_at, acknowledged_at, outcome, detail`

/**
 * Settles the live cancel for a run that has just reached a terminal state.
 *
 * Two outcomes, and they are different sentences. A run that ended as
 * `cancelled` is a command that worked, so it is acknowledged. A run that
 * finished any other way while a cancel was outstanding is a command that
 * arrived too late, so it is `superseded`: nothing went wrong, and nobody
 * should be shown a failed teardown for a run that simply finished first.
 *
 * This is the ordering "cancel during execution, where the execution wins". It
 * is settled here, on the terminal event, rather than waiting for an engine to
 * acknowledge a command it will never see, because the run it referred to no
 * longer exists to be cancelled.
 */
export async function settleCancelForRun(
  db: Db,
  input: { runId: string; finalState: string; now: Date },
): Promise<void> {
  const now = input.now.toISOString()
  if (input.finalState === 'cancelled') {
    await db.execute(sql`
      UPDATE runtime_commands SET
        state = 'acknowledged', outcome = 'done',
        detail = COALESCE(detail, 'the run reported that it stopped'),
        acknowledged_at = ${now}::timestamptz,
        lease_holder = NULL, lease_expires_at = NULL, updated_at = ${now}::timestamptz
      WHERE kind = 'workload.cancel' AND workload_run_id = ${input.runId}
        AND state IN ('pending', 'claimed')`)
    return
  }
  await db.execute(sql`
    UPDATE runtime_commands SET
      state = 'superseded',
      detail = ${`the run finished as ${input.finalState} before the cancel reached a runtime`},
      lease_holder = NULL, lease_expires_at = NULL, updated_at = ${now}::timestamptz
    WHERE kind = 'workload.cancel' AND workload_run_id = ${input.runId}
      AND state IN ('pending', 'claimed')`)
}
