// A Postgres of this CHECKOUT's own.
//
// The disaster recovery drill creates databases, restores into them and drops
// them, on a cluster it takes for granted for the length of a run. Doing that
// on the shared development container is antisocial in one direction and
// fragile in the other: it disturbs whoever else is using it, and it fails
// whenever somebody recreates it underneath. Both happened, repeatedly, and the
// failures arrived as ECONNRESET halfway through a restore, which reads as a
// bug in the restore.
//
// So this suite brings its own. It used to bring one for the machine, named
// af-dr-test on port 55433, and that turned out to be the same mistake one
// level up.
//
// WHY A CONTAINER PER CHECKOUT AND NOT PER MACHINE
//
// A cluster is not a namespace. Three things on it are cluster-wide and none
// of them can be prefixed away:
//
// 1. THE MIGRATION LEDGER. The runner records each migration BY NAME in
//    schema_migrations. With several checkouts sharing one cluster that ledger
//    accumulates every branch's migrations, so the database the drill dumps
//    represents no branch at all. It held four at once on this machine. It also
//    makes a renamed migration a hard failure: the runner sees the new name as
//    unapplied, runs it, and dies on "relation already exists", in a way that
//    reproduces in isolation and so does not look like contention.
// 2. ROLES. antifailure_app and antifailure_sweeper are created by migrations
//    and belong to the cluster, not to a database. Their grants and their
//    membership are exactly what this suite asserts a restore reproduces, so a
//    branch that changes them is changing what every other checkout's drill is
//    checking.
// 3. pg_database ITSELF. dropDatabasesNamed enumerates it and drops with
//    FORCE. On a shared cluster that prefix is the only thing standing between
//    this suite and another run's restore target, and it is not enough: two
//    checkouts running the drill at once both create af_dr_ databases and each
//    one's cleanup takes the other's. That is not hypothetical either.
//
// A per-run database would fix none of the three. So the isolation moves out to
// the container, keyed on the checkout, and the destructive cleanup below stops
// needing to be careful because there is nothing of anybody else's to hit.
//
// WHY THE PORT IS NOT WRITTEN DOWN
//
// The obvious version of this derives a port from the same key and hopes. That
// reintroduces the whole bug quietly: when something else is already listening
// there, the probe connects, the suite runs against a stranger's cluster, and
// it looks exactly like a healthy run. So no port is chosen here at all.
// Docker is asked to publish an ephemeral one on the loopback and is then asked
// which one it picked. The container's NAME is the identity, and the port is
// read back from it on every start, including a restart, which hands out a new
// one.
//
// The container is still reused when it is already running, because starting
// Postgres costs more than the tests do. What is not reused is anybody else's.
//
// WHAT THIS COSTS, SAID PLAINLY
//
// One Postgres per checkout instead of one per machine. On a box carrying
// fifteen worktrees that is fifteen containers, and a checkout that is deleted
// leaves its container behind with a name that no longer means anything to
// anybody. The machine this was written on ran out of disk the same afternoon,
// so that is not a theoretical cost.
//
// So each one carries a label saying which directory it belongs to, and the
// orphans can be found and removed without guessing:
//
//   docker ps -a --filter label=af.role=dr-test \
//     --format '{{.Names}} {{.Label "af.checkout"}}'
//
// Any row whose path no longer exists is safe to remove. A row whose path does
// exist belongs to a checkout that may be mid-run, and removing it takes that
// run's cluster out from under it, which is the whole class of failure this
// file is here to end.

import { execFile } from 'node:child_process'
import { createHash } from 'node:crypto'
import { readdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { promisify } from 'node:util'
import postgres from 'postgres'

const run = promisify(execFile)

/** The checkout this file belongs to, resolved to an absolute path.
 *
 *  Two worktrees of the same branch are two checkouts and get two containers,
 *  which is the point: they run different code and their ledgers disagree. */
const checkout = path.resolve(fileURLToPath(new URL('../../../../', import.meta.url)))

/** A short, stable key for this checkout.
 *
 *  Twelve hex characters, which is enough that a collision would be a
 *  curiosity rather than a risk, and short enough that `docker ps` is still
 *  readable. Exported so a test can prove two checkouts differ without
 *  standing up two containers. */
export function keyFor(root: string): string {
  return createHash('sha256').update(path.resolve(root)).digest('hex').slice(0, 12)
}

export const CONTAINER = `af-dr-${keyFor(checkout)}`

/** Set by start(), once Docker has said which port it published. */
let published: string | null = null

/**
 * Where this suite's Postgres is answering.
 *
 * A function rather than a constant, and it throws rather than falling back to
 * a default, because the failure a default produces is the one this file
 * exists to remove: a suite that quietly runs against somebody else's cluster.
 */
export function url(): string {
  if (!published) {
    throw new Error('pgcontainer.url() was read before start() resolved; call start() first')
  }
  return published
}

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

/**
 * The loopback URL for the port this container publishes, asked of Docker.
 *
 * `docker port` answers one line per binding, and a container published with
 * `-p 127.0.0.1::5432` on a dual stack daemon can answer with an IPv6 line as
 * well. The IPv4 one is taken because that is what the connection string and
 * pg_dump's -h both use.
 */
async function publishedUrl(): Promise<string> {
  const out = await docker(['port', CONTAINER, '5432'])
  const line = out.split('\n').map((l) => l.trim()).find((l) => l.startsWith('0.0.0.0:') || l.startsWith('127.0.0.1:'))
  if (!line) {
    throw new Error(`${CONTAINER} publishes no IPv4 port for 5432; docker port said: ${out || '(nothing)'}`)
  }
  const port = line.slice(line.lastIndexOf(':') + 1)
  return `postgres://postgres:test@127.0.0.1:${port}/antifailure`
}

async function reachable(candidate: string, timeoutSeconds: number): Promise<boolean> {
  const probe = postgres(candidate, { max: 1, connect_timeout: timeoutSeconds, onnotice: () => {} })
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

  const existing = await docker([
    'ps', '-a', '--filter', `name=^/${CONTAINER}$`, '--format', '{{.State}}',
  ])
  if (existing === '') {
    const major = await clientMajor()
    await docker([
      'run', '-d', '--name', CONTAINER,
      // No host port written down. Docker picks one and is asked which below.
      // Bound to the loopback so a laptop on a café network is not serving
      // Postgres to it.
      '-p', '127.0.0.1::5432',
      // So an orphan can be attributed. The name is a hash and says nothing on
      // its own, and a container nobody can attribute is a container nobody
      // dares remove.
      '--label', 'af.role=dr-test',
      '--label', `af.checkout=${checkout}`,
      '-e', 'POSTGRES_PASSWORD=test',
      '-e', 'POSTGRES_DB=antifailure',
      `postgres:${major}-alpine`,
    ])
  } else if (existing !== 'running') {
    await docker(['start', CONTAINER])
  }

  // After the start, never before: a restarted container publishes a different
  // host port, and a URL captured before the restart points at nothing or, on a
  // busy machine, at whatever took the port.
  const candidate = await publishedUrl()

  // Two minutes, because the first start on a loaded machine includes pulling
  // the image and running initdb, and both are slow when eleven other things
  // are using the same daemon.
  const deadline = Date.now() + 120_000
  while (Date.now() < deadline) {
    if (await reachable(candidate, 5)) {
      published = candidate
      return true
    }
    await new Promise((r) => setTimeout(r, 1000))
  }
  const logs = await docker(['logs', '--tail', '30', CONTAINER]).catch(() => '(no logs)')
  throw new Error(`${CONTAINER} did not accept a connection within two minutes:\n${logs}`)
}

/** Removes every database this suite made, by name prefix.
 *
 *  This drops with FORCE, which disconnects whoever is using the database, and
 *  on the machine-wide container it took other runs' restore targets while they
 *  were restoring into them. It is safe now because the cluster belongs to this
 *  checkout, and the guard below is what makes that a fact rather than an
 *  assumption: without a resolved URL there is nothing to fall back to. */
export async function dropDatabasesNamed(prefix: string): Promise<void> {
  const admin = postgres(url(), { max: 1, connect_timeout: 30, onnotice: () => {} })
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
