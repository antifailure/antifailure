#!/usr/bin/env node
// The command an operator runs, at three in the morning or on a schedule.
//
// Separate from main.ts on purpose. The server process is long lived and holds
// a pool; this one does a single job with a privileged connection and exits.
// Putting them in one binary would mean a backup could only be taken by
// something that was also willing to start a web server.
//
// Everything is an argument rather than an environment variable, so that a
// backup taken during an incident does not depend on the shell it was taken in
// being configured the way the deployment is. The one exception is the password
// in the connection string, which has nowhere else to live.

import { writeFile } from 'node:fs/promises'

import { backup, rehearse, restore } from './backup.ts'

function usage(): never {
  console.error(`af-control-plane-backup <command>

  backup    --url <admin connection string> --out <directory> [--label <name>]
            Takes a dump, a roles file, and a manifest describing what a restore
            has to reproduce.

  restore   --url <admin connection string on the TARGET cluster>
            --database <new database name>
            --dump <file> [--roles <file>] [--manifest <file>]
            [--app-password <password>]
            Creates the database and restores into it, then checks the result
            against the manifest. It refuses a database that already exists,
            because restoring over a live one is not a recovery.

  drill     --url <admin connection string> --out <directory>
            --database <throwaway name> [--app-password <password>] [--keep]
            [--max-restore-seconds <n>] [--report <file>]
            Backs up, restores into a throwaway database, checks it, drops it,
            and reports the recovery time it measured. --report writes that
            measurement as JSON, because a number that only ever appeared in a
            log people scroll past is a number nobody has. Without
            --max-restore-seconds it measures and gates nothing, which is right
            against a production database: its size is not a constant, so a
            budget set once fires on growth rather than on a regression.

The connection string must be a role that can read every table and create a
database. It is not the role the application connects as.

--app-password is what makes the cross-tenant read possible: without it nothing
can connect as the role whose access the policies constrain, and every check
becomes a comparison of catalogue text against catalogue text. The restore
command says so and leaves the decision to you; the drill treats it as a
failure, because finding this out before the day it matters is the whole job.

Exit codes: 0 sound, 1 the work failed, 2 the arguments are wrong, 3 the restore
completed and does not match the backup, which is the one that matters, 4 the
restore was sound and slower than the budget it was given.`)
  process.exit(2)
}

function flag(argv: string[], name: string): string | undefined {
  const i = argv.indexOf(`--${name}`)
  if (i === -1) return undefined
  const value = argv[i + 1]
  if (value === undefined || value.startsWith('--')) {
    console.error(`--${name} needs a value.`)
    process.exit(2)
  }
  return value
}

function required(argv: string[], name: string): string {
  const value = flag(argv, name)
  if (value === undefined) {
    console.error(`--${name} is required.`)
    usage()
  }
  return value
}

const [command, ...argv] = process.argv.slice(2)

try {
  switch (command) {
    case 'backup': {
      const result = await backup({
        adminUrl: required(argv, 'url'),
        outDir: required(argv, 'out'),
        label: flag(argv, 'label'),
        binDir: flag(argv, 'bin-dir'),
      })
      console.log(`dump      ${result.dumpPath}`)
      console.log(`roles     ${result.rolesPath}`)
      console.log(`manifest  ${result.manifestPath}`)
      console.log(
        `${result.manifest.bytes} bytes, sha256 ${result.manifest.sha256}, ` +
          `${result.seconds.toFixed(1)}s`,
      )
      console.log(
        `${Object.keys(result.manifest.policies).length} tables with policies, ` +
          `${result.manifest.rlsForced.length} with row level security forced`,
      )
      break
    }

    case 'restore': {
      const result = await restore({
        adminUrl: required(argv, 'url'),
        targetDatabase: required(argv, 'database'),
        dumpPath: required(argv, 'dump'),
        rolesPath: flag(argv, 'roles'),
        manifestPath: flag(argv, 'manifest'),
        appPassword: flag(argv, 'app-password'),
        binDir: flag(argv, 'bin-dir'),
      })
      console.log(`restored in ${result.seconds.toFixed(1)}s`)
      if (result.isolation.attempted) {
        console.log(
          `${result.isolation.tables.length} tables refused another tenant's rows: ` +
            `${result.isolation.tables.join(', ')}`,
        )
      } else {
        // On stderr and named, rather than left out of the output. A check that
        // did not run and says nothing is indistinguishable from one that
        // passed, and this is the check that decides whether the restored
        // database isolates tenants or only looks like it does.
        console.error(`the cross-tenant read was NOT attempted: ${result.isolation.reason}`)
      }
      if (result.problems.length > 0) {
        console.error('')
        // Not "does not match the backup". A restore can match the manifest in
        // every particular and still hand one tenant another tenant's rows,
        // because the manifest is taken from the source: if the source was
        // already broken, the comparison agrees with it. Naming the finding as
        // a mismatch would send whoever reads this looking at the restore.
        console.error('THE RESTORED DATABASE IS NOT SOUND. Do not point the control plane')
        console.error('at it until every line below is understood:')
        for (const p of result.problems) console.error(`  ${p}`)
        process.exit(3)
      }
      console.log('it matches the backup: rows, policies, row level security, grants, audit chain')
      break
    }

    case 'drill': {
      const budget = flag(argv, 'max-restore-seconds')
      if (budget !== undefined && !(Number(budget) > 0)) {
        console.error(`--max-restore-seconds must be a positive number, not ${budget}.`)
        process.exit(2)
      }
      const result = await rehearse({
        adminUrl: required(argv, 'url'),
        outDir: required(argv, 'out'),
        targetDatabase: required(argv, 'database'),
        appPassword: flag(argv, 'app-password'),
        binDir: flag(argv, 'bin-dir'),
        maxRestoreSeconds: budget === undefined ? undefined : Number(budget),
        drop: !argv.includes('--keep'),
        log: (line) => console.log(line),
      })
      console.log('')
      console.log(`recovery time    ${result.recoveryTimeSeconds.toFixed(1)}s`)
      console.log(`backup time      ${result.backupSeconds.toFixed(1)}s`)
      console.log(`dump size        ${result.bytes} bytes`)

      // Written before the exit codes below, so a drill that failed still
      // leaves its measurement behind. The run that goes red is the one whose
      // timing somebody will want to compare against last week's.
      const report = flag(argv, 'report')
      if (report !== undefined) {
        const measured = { measuredAt: new Date().toISOString(), ...result }
        await writeFile(report, JSON.stringify(measured, null, 2) + '\n', 'utf8')
        console.log(`report           ${report}`)
      }

      if (result.problems.length > 0) {
        console.error('')
        console.error('THE DRILL FOUND PROBLEMS. This backup is not one:')
        for (const p of result.problems) console.error(`  ${p}`)
        process.exit(3)
      }
      if (result.overBudget) {
        // A separate exit code from 3, and a separate paragraph. The backup IS
        // one; it came back slower than the budget. Reporting both the same way
        // would teach whoever reads the failure that "the backup is not one"
        // sometimes means the runner was busy, and then it means nothing.
        console.error('')
        console.error(
          `THE RESTORE IS SOUND AND SLOW. It took ` +
            `${result.overBudget.seconds.toFixed(1)}s against a budget of ` +
            `${result.overBudget.budgetSeconds}s.`,
        )
        console.error('Either the restore got slower or the budget is wrong. Decide which,')
        console.error('and change the one that is wrong rather than re-running until it passes.')
        process.exit(4)
      }
      console.log('')
      console.log('the drill is sound. Record the recovery time above; it is the only')
      console.log('number the runbook is entitled to quote.')
      break
    }

    default:
      usage()
  }
} catch (err) {
  console.error(err instanceof Error ? err.message : String(err))
  process.exit(1)
}
