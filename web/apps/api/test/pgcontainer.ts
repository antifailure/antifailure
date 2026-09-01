// A Postgres of this suite's own.
//
// The disaster recovery drill creates databases, restores into them and drops
// them, on a cluster it takes for granted for the length of a run. Doing that
// on the shared development container is antisocial in one direction and
// fragile in the other: it disturbs whoever else is using it, and it fails
// whenever somebody recreates it underneath. Both happened, repeatedly, and the
// failures arrived as ECONNRESET halfway through a restore, which reads as a
// bug in the restore.
//
// So this suite brings its own, on its own port, with a name nothing else uses.
// It is reused when it is already running, because starting Postgres costs more
// than the tests do.

import { execFile } from 'node:child_process'
import { readdir } from 'node:fs/promises'
import { promisify } from 'node:util'
import postgres from 'postgres'

const run = promisify(execFile)

export const CONTAINER = 'af-dr-test'
export const PORT = 55433
export const URL = `postgres://postgres:test@127.0.0.1:${PORT}/antifailure`

/**
 * A token unique to this process, put in the name of every database this run
 * creates so that the cleanup can find its own and nothing else.
 *
 * The container above is deliberately shared and reused, which is right when
 * one person runs one suite at a time and wrong the moment two runs overlap,
 * because they share a CLUSTER. The sweep used to drop every `af_dr_` database
 * on it with `WITH (FORCE)`, and FORCE means terminate whoever is connected.
 * So a run finishing would reach into a run still going, kill its connections
 * and delete the database it was restoring into. What came back was an
 * ECONNRESET halfway through a restore, or a missing database, or a stall,
 * none of which name the cause and all of which read as a defect in the
 * backup code.
 *
 * Scoping the names is the half of the problem that can be fixed from here.
 * The other half is that the cluster, its roles and its migrations are still
 * shared, so two checkouts on different branches still migrate one database.
 * That is a design decision above this file and it is written down rather than
 * quietly worked around.
 */
export const RUN = `${process.pid.toString(36)}${Date.now().toString(36).slice(-4)}`

/**
 * The Postgres major this machine has a client for.
 *
 * The suite starts a server matching the newest pg_dump it can find rather than
 * pinning a version, because dumping a newer server with an older client is
 * unsupported and the tool under test refuses it. A GitHub runner ships the
 * version 16 client and no 17, so a suite that insisted on 17 would refuse
 * itself and report that as a failure of the code.
 *
 * Matching is also the honest thing to test. An operator's box has whatever
 * client the base image shipped, and the drill has to work there.
 */
export async function clientMajor(): Promise<number> {
  const found = new Set<number>()

  try {
    const { stdout } = await run('pg_dump', ['--version'])
    const m = stdout.match(/(\d+)/)
    if (m) found.add(Number(m[1]))
  } catch {
    // Nothing on PATH. The directories below may still have one.
  }
  try {
    for (const name of await readdir('/usr/lib/postgresql')) {
      const n = Number(name)
      if (Number.isInteger(n)) found.add(n)
    }
  } catch {
    // Not a Debian layout. Whatever PATH gave is what there is.
  }

  const best = [...found].sort((a, b) => b - a)[0]
  // 17 when there is no client at all, so the failure is "pg_dump is missing"
  // from the tool rather than a confusing image tag from here.
  return best ?? 17
}

async function docker(args: string[]): Promise<string> {
  const { stdout } = await run('docker', args)
  return stdout.trim()
}

async function reachable(timeoutSeconds: number): Promise<boolean> {
  const probe = postgres(URL, { max: 1, connect_timeout: timeoutSeconds, onnotice: () => {} })
  try {
    await probe`SELECT 1`
    return true
  } catch {
    return false
  } finally {
    await probe.end({ timeout: 5 }).catch(() => {})
  }
}

/**
 * Starts the container if it is not already answering, and waits for it.
 *
 * Returns false when there is no Docker at all, which is the one honest reason
 * to skip. Every other failure throws with what Docker said, because a skip the
 * code under test can cause is a pass with extra steps.
 */
export async function start(): Promise<boolean> {
  try {
    await docker(['version', '--format', '{{.Server.Version}}'])
  } catch {
    return false
  }

  if (await reachable(5)) return true

  const existing = await docker([
    'ps', '-a', '--filter', `name=^/${CONTAINER}$`, '--format', '{{.State}}',
  ])
  if (existing === '') {
    const major = await clientMajor()
    await docker([
      'run', '-d', '--name', CONTAINER,
      '-p', `${PORT}:5432`,
      '-e', 'POSTGRES_PASSWORD=test',
      '-e', 'POSTGRES_DB=antifailure',
      `postgres:${major}-alpine`,
    ])
  } else if (existing !== 'running') {
    await docker(['start', CONTAINER])
  }

  // Two minutes, because the first start on a loaded machine includes pulling
  // the image and running initdb, and both are slow when eleven other things
  // are using the same daemon.
  const deadline = Date.now() + 120_000
  while (Date.now() < deadline) {
    if (await reachable(5)) return true
    await new Promise((r) => setTimeout(r, 1000))
  }
  const logs = await docker(['logs', '--tail', '30', CONTAINER]).catch(() => '(no logs)')
  throw new Error(`${CONTAINER} did not accept a connection within two minutes:\n${logs}`)
}

/**
 * Removes databases by name prefix.
 *
 * Callers pass a prefix carrying RUN, so this only ever reaches its own. A
 * bare `af_dr_` here would take another run's databases with it: see RUN.
 */
export async function dropDatabasesNamed(prefix: string): Promise<void> {
  const admin = postgres(URL, { max: 1, connect_timeout: 30, onnotice: () => {} })
  try {
    const rows = await admin<{ datname: string }[]>`
      SELECT datname FROM pg_database WHERE datname LIKE ${prefix + '%'}`
    for (const { datname } of rows) {
      await admin.unsafe(`DROP DATABASE IF EXISTS "${datname}" WITH (FORCE)`).catch(() => {})
    }
  } finally {
    await admin.end({ timeout: 5 })
  }
}
