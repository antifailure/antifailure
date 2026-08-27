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
import { promisify } from 'node:util'
import postgres from 'postgres'

const run = promisify(execFile)

export const CONTAINER = 'af-dr-test'
export const PORT = 55433
export const URL = `postgres://postgres:test@127.0.0.1:${PORT}/antifailure`

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
    await docker([
      'run', '-d', '--name', CONTAINER,
      '-p', `${PORT}:5432`,
      '-e', 'POSTGRES_PASSWORD=test',
      '-e', 'POSTGRES_DB=antifailure',
      'postgres:17-alpine',
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

/** Removes every database this suite made, by name prefix. */
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
