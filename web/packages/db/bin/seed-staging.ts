#!/usr/bin/env node
// The command behind `npm run seed`.
//
// It exists because package.json declared it and nothing did. The script and
// the `bin` entry were both written, the module they call was written and
// tested, and the file in between was never committed: `.gitignore` carried an
// unanchored `bin/`, which matches a directory of that name at any depth. Git
// silently declined to add it, every local run worked because the file was on
// disk, and CI failed with "Cannot find module" naming a path that plainly
// exists. The ignore rule is anchored now.
//
// Three steps, in this order, and the order is the point. Migrations first,
// because a seed against a schema it did not create fails on the first column
// somebody added and reports it as a seeding bug. Then a truncate, because a
// seeder that appends is a seeder whose second run produces a database nobody
// can reason about. Then the rows.

import postgres from 'postgres'
import { migrate } from '../src/migrate.ts'
import { resetStaging, seedStaging, type StagingReport } from '../src/staging.ts'

/** Reads a variable, or names it and stops. */
function required(name: string, ...fallbacks: string[]): string {
  for (const key of [name, ...fallbacks]) {
    const value = process.env[key]
    if (value) return value
  }
  const names = [name, ...fallbacks].join(' or ')
  console.error(`${names} is not set: this seeder needs a database to shape.`)
  process.exit(2)
}

function scale(): number {
  const raw = process.env.AF_SEED_SCALE
  if (!raw) return 1
  const value = Number(raw)
  if (!Number.isFinite(value) || value < 1) {
    console.error(`AF_SEED_SCALE is "${raw}", which is not a number 1 or above.`)
    process.exit(2)
  }
  return value
}

function report(counts: StagingReport): void {
  const rows = Object.entries(counts)
    .filter(([, n]) => n > 0)
    .map(([name, n]) => `${n} ${name.replace(/([A-Z])/g, ' $1').toLowerCase()}`)
  console.log(`seeded ${rows.join(', ')}`)
}

async function main(): Promise<void> {
  // The owning role. Every table is written here, including the ones the
  // application deliberately cannot write to, which is the whole reason this
  // is not something the application could do to itself.
  const adminUrl = required('AF_MIGRATION_DATABASE_URL', 'AF_STAGING_DATABASE_URL', 'DATABASE_URL')
  // The connection the application would use. The audit chain is appended
  // through it rather than through the admin one, because a chain built by a
  // different path is not the chain the application later verifies.
  const appUrl = process.env.AF_STAGING_DATABASE_URL ?? adminUrl

  // One connection, not a pool. Migrations reserve one anyway, and a seeder
  // that opens ten against a database that has just started is ten chances to
  // hit a server still accepting its first connection.
  const admin = postgres(adminUrl, { max: 1, connect_timeout: 30, onnotice: () => {} })
  try {
    const migrated = await migrate(admin, { log: (line) => console.log(line) })
    console.log(
      migrated.applied.length > 0
        ? `applied ${migrated.applied.length} migrations`
        : `schema already current, ${migrated.alreadyApplied.length} migrations`,
    )

    await resetStaging(admin)
    report(await seedStaging(admin, { appUrl, scale: scale(), log: (l) => console.log(l) }))
  } finally {
    await admin.end({ timeout: 5 })
  }
}

// Awaited rather than fired, so a rejection is an exit code rather than an
// unhandled rejection warning on a process that has already reported success.
try {
  await main()
} catch (err) {
  console.error(err instanceof Error ? err.stack ?? err.message : String(err))
  process.exit(1)
}
