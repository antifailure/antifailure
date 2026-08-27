// One schema-maintenance pass, then exit.
//
// The events table is partitioned by month and partitions are created ahead of
// the writes, because a range-partitioned table with no partition for an
// incoming row does not slow down, it fails.
//
// Creating them is DDL, so it needs the privileged role. The application can do
// this itself on a timer, and when it does, every replica serving requests is
// holding a credential that can ALTER TABLE. That is a strange thing to give
// the process that is exposed to the internet in order to solve a housekeeping
// problem, so the chart runs this as a CronJob instead and the Deployment gets
// no DDL credential at all.
//
// runMaintenance is exported by the application for exactly this: its own
// comment says it is "exported separately from the schedule so it can be run by
// hand".

import { runMaintenance, retentionFromEnv } from './apps/api/src/maintenance.ts'
import { systemClock } from './apps/api/src/clock.ts'

const adminUrl = process.env.AF_MAINTENANCE_DATABASE_URL ?? process.env.AF_MIGRATION_DATABASE_URL
if (!adminUrl) {
  console.error(
    'neither AF_MAINTENANCE_DATABASE_URL nor AF_MIGRATION_DATABASE_URL is set. ' +
      'A maintenance pass needs a role that may run DDL.',
  )
  process.exit(2)
}

const run = await runMaintenance(
  {
    adminUrl,
    retentionMonths: retentionFromEnv(process.env),
    archiveDir: process.env.AF_EVENT_ARCHIVE_DIR,
    log: (line) => console.log(line),
  },
  systemClock,
)

// Said out loud every pass, including the boring ones. "created: none" a day
// after the last partition was made is the signal that something is wrong, and
// it is only legible if the quiet passes are reported too.
console.log(
  `created: ${run.created.join(', ') || 'none'}; ` +
    `archived: ${run.archived.join(', ') || 'none'}; ` +
    `dropped: ${run.dropped.join(', ') || 'none'}; ` +
    `pruned: ${run.pruned} rows`,
)
