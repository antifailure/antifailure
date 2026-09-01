// Schema maintenance: keeping the events partitions ahead of the writes.
//
// This is DDL, so it runs as the migration role rather than as the application
// role. The application role is deliberately not allowed to run DDL, because a
// role that can ALTER TABLE can drop the policies that isolate tenants.
//
// The connection is opened for the run and closed after it. A privileged
// connection sitting idle for a day between two seconds of work is a standing
// risk in exchange for nothing.
//
// Running it on a schedule rather than only at deploy time is the point. Doing
// it at startup alone means an installation that has not deployed in as many
// months as the margin runs out of partitions, and running out is not a
// slowdown: an insert with no partition to go in fails.

import { createWriteStream } from 'node:fs'
import { mkdir, rename } from 'node:fs/promises'
import path from 'node:path'
import { once } from 'node:events'
import postgres from 'postgres'
import {
  ANALYTICS_EVENTS,
  applyPartitions,
  currentPartitions,
  planPartitions,
  pruneDefault,
  readPartition,
  type ApplyResult,
} from '@antifailure/db'
import type { Clock } from './clock.ts'
import { rollUp } from './analytics/rollup.ts'

export interface MaintenanceConfig {
  /** A connection string for a role that may run DDL. */
  adminUrl: string
  /** Months kept created beyond the current one. */
  monthsAhead?: number
  /** Drop partitions entirely older than this. Undefined never drops, which is
   *  the default: retention is an operator's decision, not a library's. */
  retentionMonths?: number
  /** Where to write a month before dropping it. Undefined does not archive,
   *  which is only safe when the events are not worth keeping. */
  archiveDir?: string
  /**
   * Delete raw analytics events older than this many days. Undefined never
   * deletes, which is the default for the same reason retentionMonths is.
   *
   * Note what is NOT deleted: the daily aggregates computed from them. A count
   * of page views by channel has nothing in it that identifies anybody, so the
   * shape of a retention policy here is that the rows carrying a surrogate go
   * and the counts stay.
   */
  analyticsRetentionDays?: number
  /** How often to run. */
  intervalMs?: number
  log?: (line: string) => void
  onError?: (err: unknown) => void
}

export interface MaintenanceRun {
  created: string[]
  dropped: string[]
  archived: string[]
  pruned: number
  /** Days of analytics recomputed by the rollup. */
  rolledUp: string[]
  /** Raw analytics events deleted by retention. */
  analyticsPruned: number
}

const DAY_MS = 24 * 60 * 60 * 1000

function noop(): void {}

/**
 * One maintenance pass. Exported separately from the schedule so it can be run
 * by hand, and so the schedule has nothing in it worth testing on its own.
 */
export async function runMaintenance(
  config: MaintenanceConfig,
  clock: Clock,
): Promise<MaintenanceRun> {
  const admin = postgres(config.adminUrl, { max: 1, onnotice: () => {} })
  try {
    const now = clock.now()

    // Creating comes first and unconditionally. Running out of partitions is
    // not a slowdown: an insert with no partition to go in fails. Nothing
    // further down this function is allowed to prevent it.
    const made = await applyPartitions(admin, { now, monthsAhead: config.monthsAhead })

    // Then dropping, which is the irreversible half. A month goes only after a
    // complete copy of it is on disk, so a failed archive costs a retention run
    // rather than the events.
    const archived: string[] = []
    let dropped: string[] = []
    if (config.retentionMonths !== undefined) {
      const doomed = planPartitions(await currentPartitions(admin), {
        now,
        monthsAhead: config.monthsAhead,
        retentionMonths: config.retentionMonths,
      }).drop

      let mayDrop = true
      if (config.archiveDir !== undefined) {
        for (const name of doomed) {
          try {
            await archivePartition(admin, name, config.archiveDir)
            archived.push(name)
          } catch (err) {
            mayDrop = false
            ;(config.onError ?? noop)(
              new Error(`archiving ${name} failed, so nothing was dropped this run`, { cause: err }),
            )
            break
          }
        }
      }

      if (mayDrop) {
        const swept: ApplyResult = await applyPartitions(admin, {
          now,
          monthsAhead: config.monthsAhead,
          retentionMonths: config.retentionMonths,
        })
        dropped = swept.dropped
      }
    }

    // The default partition holds events whose month was already gone when they
    // arrived. Age is the only thing that can be said about them, so they go by
    // age, a bounded number at a time.
    let pruned = 0
    if (config.retentionMonths !== undefined) {
      const { deleted } = await pruneDefault(admin, {
        retentionMonths: config.retentionMonths,
        now,
      })
      pruned = deleted
    }

    // Analytics partitions, kept ahead of the writes exactly as the events
    // partitions are. A range-partitioned table with no partition for an
    // incoming row does not slow down, it fails, and the analytics stream is
    // written from the sign-in path.
    const analyticsPartitions = await applyPartitions(admin, {
      now, monthsAhead: config.monthsAhead, table: ANALYTICS_EVENTS,
    })

    // Then the rollup, which is the only reader of the analytics stream. It
    // rides this pass rather than having a scheduler of its own: it needs the
    // same privileged credential, and a second timer is a second thing to
    // notice had stopped.
    //
    // After the partitions and never before, so a rollup can never be the
    // reason a month does not exist.
    const rolled = await rollUp(admin, clock, {
      now,
      ...(config.analyticsRetentionDays === undefined
        ? {}
        : { retentionDays: config.analyticsRetentionDays }),
    })

    return {
      created: [...made.created, ...analyticsPartitions.created],
      dropped,
      archived,
      pruned,
      rolledUp: rolled.days,
      analyticsPruned: rolled.pruned,
    }
  } finally {
    await admin.end({ timeout: 10 })
  }
}

export interface MaintenanceHandle {
  stop(): void
}

/**
 * Runs a pass now and then on an interval.
 *
 * A failure is logged and the schedule continues. The failure mode that matters
 * is running out of partitions, and giving up after the first transient error
 * is how that happens quietly.
 */
export function startMaintenance(
  config: MaintenanceConfig,
  clock: Clock,
): MaintenanceHandle {
  const log = config.log ?? (() => {})
  const onError = config.onError ?? ((err) => console.error('partition maintenance', err))

  const pass = async () => {
    try {
      const run = await runMaintenance(config, clock)
      if (run.created.length) log(`created partitions: ${run.created.join(', ')}`)
      if (run.archived.length) log(`archived partitions: ${run.archived.join(', ')}`)
      if (run.dropped.length) log(`dropped partitions: ${run.dropped.join(', ')}`)
      if (run.pruned) log(`pruned ${run.pruned} late events past the retention`)
      if (run.rolledUp.length) {
        log(`rolled up analytics for ${run.rolledUp.join(', ')}`)
      }
      if (run.analyticsPruned) {
        log(`pruned ${run.analyticsPruned} raw analytics events past the retention`)
      }
    } catch (err) {
      onError(err)
    }
  }

  void pass()
  const timer = setInterval(() => void pass(), config.intervalMs ?? DAY_MS)
  timer.unref()
  return { stop: () => clearInterval(timer) }
}

/** Reads the retention from the environment, refusing a value that is not a
 *  whole number of months rather than silently keeping everything forever. */
export function retentionFromEnv(env: NodeJS.ProcessEnv): number | undefined {
  const raw = env.AF_EVENT_RETENTION_MONTHS
  if (raw === undefined || raw === '') return undefined
  const months = Number(raw)
  if (!Number.isInteger(months) || months < 1) {
    throw new Error(
      `AF_EVENT_RETENTION_MONTHS is ${JSON.stringify(raw)}; it has to be a whole number of months, at least 1`,
    )
  }
  return months
}

/**
 * Writes one partition out as newline-delimited JSON, then puts it in place.
 *
 * Newline-delimited because the file is read by something else, quite possibly
 * a line at a time by a tool that will not hold a month of events in memory.
 * Written to a temporary name and renamed at the end so that a file appearing
 * in the directory always means a complete one: a truncated archive that looks
 * finished is worse than no archive, because the drop that follows trusts it.
 */
export async function archivePartition(
  sql: postgres.Sql,
  name: string,
  dir: string,
): Promise<string> {
  await mkdir(dir, { recursive: true })
  const target = path.join(dir, `${name}.jsonl`)
  const temp = `${target}.partial`

  const out = createWriteStream(temp, { flags: 'w' })
  try {
    for await (const batch of readPartition(sql, name)) {
      const chunk = batch.map((row) => JSON.stringify(row)).join('\n') + '\n'
      if (!out.write(chunk)) await once(out, 'drain')
    }
    await new Promise<void>((resolve, reject) => {
      out.end((err?: Error | null) => (err ? reject(err) : resolve()))
    })
  } catch (err) {
    out.destroy()
    throw err
  }

  await rename(temp, target)
  return target
}
